/*
Copyright 2026 The LocalModelCache Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reconcilers

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
)

// cacheVolumes creates, repairs and deletes the PersistentVolumes and PersistentVolumeClaims of
// one cache. It marks each of them with the cache's UID, so that they can all be found again
// when a node group is gone or no longer in the cache's spec.
type cacheVolumes struct {
	clientset kubernetes.Interface
	scheme    *runtime.Scheme
	log       logr.Logger
	// owner is the LocalModelCache or LocalModelNamespaceCache.
	owner client.Object

	// kept are the volumes and claims that prune leaves alone.
	keptVolumes sets.Set[string]
	keptClaims  sets.Set[types.NamespacedName]
}

func newCacheVolumes(clientset kubernetes.Interface, scheme *runtime.Scheme, log logr.Logger, owner client.Object) *cacheVolumes {
	return &cacheVolumes{
		clientset:   clientset,
		scheme:      scheme,
		log:         log,
		owner:       owner,
		keptVolumes: sets.New[string](),
		keptClaims:  sets.New[types.NamespacedName](),
	}
}

// keep records a volume and its claim as part of the cache's spec.
func (v *cacheVolumes) keep(volume string, claim types.NamespacedName) {
	v.keptVolumes.Insert(volume)
	v.keptClaims.Insert(claim)
}

// ensure creates a volume and the claim bound to it, and repairs the binding when the claim was
// deleted and created again. It returns an error, and changes nothing, while an object of either
// name is being deleted or is something else: a workload that mounted the claim would see other
// files.
func (v *cacheVolumes) ensure(ctx context.Context, name string, pvSpec *corev1.PersistentVolumeSpec, claimKey types.NamespacedName, claimSpec *corev1.PersistentVolumeClaimSpec) error {
	claims := v.clientset.CoreV1().PersistentVolumeClaims(claimKey.Namespace)
	claim, err := claims.Get(ctx, claimKey.Name, metav1.GetOptions{})
	switch {
	case apierr.IsNotFound(err):
		claim = nil
	case err != nil:
		return err
	case !claim.DeletionTimestamp.IsZero():
		return fmt.Errorf("PersistentVolumeClaim %s is being deleted", claimKey)
	case claim.Spec.VolumeName != name:
		return fmt.Errorf("PersistentVolumeClaim %s is bound to volume %q instead of %q", claimKey, claim.Spec.VolumeName, name)
	}

	volume, err := v.ensureVolume(ctx, name, pvSpec, claimKey, claim)
	if err != nil {
		return err
	}

	if claim == nil {
		claim = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Namespace: claimKey.Namespace, Name: claimKey.Name},
			Spec:       *claimSpec.DeepCopy(),
		}
		claim.Spec.VolumeName = name
		v.mark(claim)
		// Only a claim in the cache's namespace, or of a cluster-scoped cache, can be owned.
		if ns := v.owner.GetNamespace(); ns == "" || ns == claimKey.Namespace {
			if err := controllerutil.SetControllerReference(v.owner, claim, v.scheme); err != nil {
				return err
			}
		}
		v.log.Info("Create PVC", "name", claimKey.Name, "namespace", claimKey.Namespace)
		if claim, err = claims.Create(ctx, claim, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create PersistentVolumeClaim %s: %w", claimKey, err)
		}
	} else if v.created(claim) && v.mark(claim) {
		if claim, err = claims.Update(ctx, claim, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update PersistentVolumeClaim %s: %w", claimKey, err)
		}
	}

	// A volume stays tied to the UID of its first claim. Point it at the claim that replaced it.
	marked := v.mark(volume)
	if ref := volume.Spec.ClaimRef; ref.UID != "" && ref.UID != claim.UID {
		v.log.Info("Rebind PV to its recreated PVC", "name", name)
		ref.UID, ref.ResourceVersion = claim.UID, ""
	} else if !marked {
		return nil
	}
	if _, err := v.clientset.CoreV1().PersistentVolumes().Update(ctx, volume, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update PersistentVolume %s: %w", name, err)
	}
	return nil
}

// ensureVolume returns the volume, creating it if needed. claim is nil when it does not exist.
func (v *cacheVolumes) ensureVolume(ctx context.Context, name string, spec *corev1.PersistentVolumeSpec, claimKey types.NamespacedName, claim *corev1.PersistentVolumeClaim) (*corev1.PersistentVolume, error) {
	// Reserve the volume for its claim, or any pending claim of the same storage class may take it.
	claimRef := &corev1.ObjectReference{APIVersion: "v1", Kind: "PersistentVolumeClaim", Namespace: claimKey.Namespace, Name: claimKey.Name}
	if claim != nil {
		claimRef.UID = claim.UID
	}

	volumes := v.clientset.CoreV1().PersistentVolumes()
	volume, err := volumes.Get(ctx, name, metav1.GetOptions{})
	if apierr.IsNotFound(err) {
		volume = &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: *spec.DeepCopy()}
		volume.Spec.ClaimRef = claimRef
		v.mark(volume)
		// A PersistentVolume is cluster-scoped, so a namespaced cache cannot own it. The cache's
		// finalizer deletes it.
		if v.owner.GetNamespace() == "" {
			if err := controllerutil.SetControllerReference(v.owner, volume, v.scheme); err != nil {
				return nil, err
			}
		}
		v.log.Info("Create PV", "name", name)
		if volume, err = volumes.Create(ctx, volume, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("create PersistentVolume %s: %w", name, err)
		}
		return volume, nil
	}
	if err != nil {
		return nil, err
	}

	if !volume.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("PersistentVolume %s is being deleted", name)
	}
	if got, want := nodePath(volume.Spec.PersistentVolumeSource), nodePath(spec.PersistentVolumeSource); got != want {
		return nil, fmt.Errorf("PersistentVolume %s exposes %q instead of %q: delete it, or delete and recreate the cache %s",
			name, got, want, client.ObjectKeyFromObject(v.owner))
	}
	if volume.Spec.ClaimRef == nil {
		volume.Spec.ClaimRef = claimRef
		if volume, err = volumes.Update(ctx, volume, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("update PersistentVolume %s: %w", name, err)
		}
	}
	if ref := volume.Spec.ClaimRef; ref.Namespace != claimKey.Namespace || ref.Name != claimKey.Name {
		return nil, fmt.Errorf("PersistentVolume %s is bound to PersistentVolumeClaim %s/%s instead of %s", name, ref.Namespace, ref.Name, claimKey)
	}
	return volume, nil
}

// created reports whether an existing claim was created for the cache, rather than by a user who
// bound a claim of the same name to the volume.
func (v *cacheVolumes) created(claim *corev1.PersistentVolumeClaim) bool {
	if ns := v.owner.GetNamespace(); ns != "" && ns != claim.Namespace {
		return true // In the job namespace, where the cache cannot own it.
	}
	return metav1.IsControlledBy(claim, v.owner)
}

// mark sets the metadata that ties an object to the cache and reports whether it changed.
func (v *cacheVolumes) mark(obj client.Object) bool {
	changed := false
	labels := obj.GetLabels()
	if labels[constants.LocalModelUIDLabelKey] != string(v.owner.GetUID()) {
		if labels == nil {
			labels = map[string]string{}
		}
		labels[constants.LocalModelUIDLabelKey] = string(v.owner.GetUID())
		obj.SetLabels(labels)
		changed = true
	}
	annotations := obj.GetAnnotations()
	for key, value := range map[string]string{
		constants.LocalModelAnnotationKey:          v.owner.GetName(),
		constants.LocalModelNamespaceAnnotationKey: v.owner.GetNamespace(),
	} {
		if current, ok := annotations[key]; ok && current == value {
			continue
		}
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[key] = value
		changed = true
	}
	obj.SetAnnotations(annotations)
	return changed
}

// prune deletes the cache's volumes and claims that were not kept.
func (v *cacheVolumes) prune(ctx context.Context, reader client.Reader) error {
	volumes, claims, err := listVolumes(ctx, reader, client.MatchingLabels{constants.LocalModelUIDLabelKey: string(v.owner.GetUID())})
	if err != nil {
		return err
	}
	volumes = slices.DeleteFunc(volumes, func(volume corev1.PersistentVolume) bool { return v.keptVolumes.Has(volume.Name) })
	claims = slices.DeleteFunc(claims, func(claim corev1.PersistentVolumeClaim) bool {
		return v.keptClaims.Has(client.ObjectKeyFromObject(&claim))
	})
	return deleteVolumes(ctx, v.clientset, v.log, volumes, claims)
}

// listVolumes returns the volumes and claims that caches created and the selector matches.
func listVolumes(ctx context.Context, reader client.Reader, selector client.ListOption) ([]corev1.PersistentVolume, []corev1.PersistentVolumeClaim, error) {
	volumes := &corev1.PersistentVolumeList{}
	if err := reader.List(ctx, volumes, selector); err != nil {
		return nil, nil, err
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := reader.List(ctx, claims, selector); err != nil {
		return nil, nil, err
	}
	return volumes.Items, claims.Items, nil
}

// deleteVolumes deletes the given volumes and claims, unless they were replaced since they were read.
func deleteVolumes(ctx context.Context, clientset kubernetes.Interface, log logr.Logger, volumes []corev1.PersistentVolume, claims []corev1.PersistentVolumeClaim) error {
	var errs []error
	for i := range claims {
		claim := &claims[i]
		if !claim.DeletionTimestamp.IsZero() {
			continue
		}
		log.Info("Deleting PVC", "name", claim.Name, "namespace", claim.Namespace)
		errs = append(errs, client.IgnoreNotFound(clientset.CoreV1().PersistentVolumeClaims(claim.Namespace).Delete(ctx, claim.Name,
			metav1.DeleteOptions{Preconditions: metav1.NewUIDPreconditions(string(claim.UID))})))
	}
	for i := range volumes {
		volume := &volumes[i]
		if !volume.DeletionTimestamp.IsZero() {
			continue
		}
		log.Info("Deleting PV", "name", volume.Name)
		errs = append(errs, client.IgnoreNotFound(clientset.CoreV1().PersistentVolumes().Delete(ctx, volume.Name,
			metav1.DeleteOptions{Preconditions: metav1.NewUIDPreconditions(string(volume.UID))})))
	}
	return errors.Join(errs...)
}

// cacheOf returns the cache a volume or claim was created for.
func cacheOf(obj client.Object) types.NamespacedName {
	return types.NamespacedName{
		Namespace: obj.GetAnnotations()[constants.LocalModelNamespaceAnnotationKey],
		Name:      obj.GetAnnotations()[constants.LocalModelAnnotationKey],
	}
}

// downloadVolumeSpec is the node group's PersistentVolume spec for download Jobs.
func downloadVolumeSpec(nodeGroup *v1alpha1.LocalModelNodeGroup) *corev1.PersistentVolumeSpec {
	spec := nodeGroup.Spec.PersistentVolumeSpec.DeepCopy()
	// The volume is only a pointer to a node directory. The default for a claim that is deleted
	// must not be to recycle or delete what it points at.
	spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
	return spec
}

// servingVolumeSpec is the node group's PersistentVolume spec narrowed to the model's folder, so
// that workloads mount the model at the root of the claim.
//
// It is always a hostPath volume. The kubelet gives a local volume to the first Pod that mounts
// it without readOnly: it changes the group of the files to the Pod's fsGroup and, with SELinux,
// relabels them for that Pod alone. Other Pods then cannot read the model, the Pod can overwrite
// it, and the node agent can no longer delete it. The kubelet leaves the files of a hostPath
// volume as they are. Its type never creates a missing folder, so a Pod on a node that has not
// started the download waits for it.
func servingVolumeSpec(nodeGroup *v1alpha1.LocalModelNodeGroup, subPath string) (*corev1.PersistentVolumeSpec, error) {
	spec := nodeGroup.Spec.PersistentVolumeSpec.DeepCopy()
	if spec.Local == nil && spec.HostPath == nil {
		return nil, fmt.Errorf("LocalModelNodeGroup %s: persistentVolumeSpec must be a local or hostPath volume", nodeGroup.Name)
	}
	spec.PersistentVolumeSource = corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{
		Path: path.Join(nodePath(spec.PersistentVolumeSource), subPath),
		Type: ptr.To(corev1.HostPathDirectory),
	}}
	spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
	// The kubelet rejects mount options on a hostPath volume.
	spec.MountOptions = nil
	return spec, nil
}

// nodePath is the node directory a local or hostPath volume exposes.
func nodePath(source corev1.PersistentVolumeSource) string {
	switch {
	case source.Local != nil:
		return path.Clean(source.Local.Path)
	case source.HostPath != nil:
		return path.Clean(source.HostPath.Path)
	}
	return ""
}
