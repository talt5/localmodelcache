/*
Copyright 2024 The KServe Authors.

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

package localmodelnamespacecache

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	apivalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

// logger for the validation webhook.
var localModelNamespaceCacheValidatorLogger = logf.Log.WithName("localmodelnamespacecache-v1alpha1-validation-webhook")

// +kubebuilder:object:generate=false
// +k8s:openapi-gen=false
// LocalModelNamespaceCacheValidator is responsible for validating the LocalModelNamespaceCache resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type LocalModelNamespaceCacheValidator struct {
	client.Client
	// Reader reads claims and Pods for the checks on claims, and caches for the storage limit. Set it
	// to the manager's APIReader.
	Reader client.Reader
}

// +kubebuilder:webhook:verbs=create;update;delete,path=/validate-localmodelnamespacecaches,mutating=false,failurePolicy=fail,groups=local.cache.ai,resources=localmodelnamespacecaches,versions=v1alpha1,name=localmodelnamespacecache-validator.webhook.local.cache.ai
var _ admission.Validator[*v1alpha1.LocalModelNamespaceCache] = &LocalModelNamespaceCacheValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (v *LocalModelNamespaceCacheValidator) ValidateCreate(ctx context.Context, localModelNamespaceCache *v1alpha1.LocalModelNamespaceCache) (admission.Warnings, error) {
	localModelNamespaceCacheValidatorLogger.Info("validate create", "name", localModelNamespaceCache.Name, "namespace", localModelNamespaceCache.Namespace)

	if err := validateStorageMode(localModelNamespaceCache); err != nil {
		return nil, err
	}
	if err := localmodelcache.ValidateModelSize(nil, localModelNamespaceCache.Spec.ModelSize); err != nil {
		return nil, err
	}

	if localModelNamespaceCache.Spec.SharedPVCMode() {
		if err := v.validateDestinationConflict(ctx, localModelNamespaceCache); err != nil {
			return nil, err
		}
		return nil, nil
	}

	if err := v.validateNodeGroups(ctx, localModelNamespaceCache.Spec.NodeGroups); err != nil {
		return nil, err
	}

	if err := v.validateClaimsFree(ctx, localModelNamespaceCache, localmodelcache.NamespaceCacheClaimNames(localModelNamespaceCache)); err != nil {
		return nil, err
	}

	return nil, localmodelcache.CheckStorageLimit(ctx, v.reader(), "", nil, localmodelcache.NamespaceCacheUsageOf(localModelNamespaceCache))
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (v *LocalModelNamespaceCacheValidator) ValidateUpdate(ctx context.Context, oldCache, localModelNamespaceCache *v1alpha1.LocalModelNamespaceCache) (admission.Warnings, error) {
	if localModelNamespaceCache.GetDeletionTimestamp() != nil {
		return nil, nil
	}
	localModelNamespaceCacheValidatorLogger.Info("validate update", "name", localModelNamespaceCache.Name, "namespace", localModelNamespaceCache.Namespace)

	if err := validateStorageMode(localModelNamespaceCache); err != nil {
		return nil, err
	}
	if err := validatePVCRefImmutable(oldCache, localModelNamespaceCache); err != nil {
		return nil, err
	}
	if err := localmodelcache.ValidateModelSize(&oldCache.Spec.ModelSize, localModelNamespaceCache.Spec.ModelSize); err != nil {
		return nil, err
	}
	if err := localmodelcache.ValidateStorageParameters(oldCache.Spec.Storage, localModelNamespaceCache.Spec.Storage); err != nil {
		return nil, err
	}

	// pvcRef and sourceModelUri are both immutable, so the (pvcRef, storageKey) destination
	// cannot change on update; the create-time conflict check is sufficient. Re-checking here
	// would wedge both sides of a create race, since neither cache could be patched (for
	// example to add the finalizer) while the other exists.
	if localModelNamespaceCache.Spec.SharedPVCMode() {
		return nil, nil
	}

	// A node group that was deleted since does not stop other changes to the cache.
	if err := v.validateNodeGroups(ctx, without(localModelNamespaceCache.Spec.NodeGroups, oldCache.Spec.NodeGroups)); err != nil {
		return nil, err
	}
	// Claims the cache already names are the controller's concern: the cache must stay editable.
	added := without(localmodelcache.NamespaceCacheClaimNames(localModelNamespaceCache), localmodelcache.NamespaceCacheClaimNames(oldCache))
	if err := v.validateClaimsFree(ctx, localModelNamespaceCache, added); err != nil {
		return nil, err
	}
	// The controller deletes the claim of a removed node group and the model on its nodes.
	removed := without(localmodelcache.NamespaceCacheClaimNames(oldCache), localmodelcache.NamespaceCacheClaimNames(localModelNamespaceCache))
	if err := v.validateNotInUse(ctx, localModelNamespaceCache, removed); err != nil {
		return nil, err
	}

	oldUsage := localmodelcache.NamespaceCacheUsageOf(oldCache)
	return nil, localmodelcache.CheckStorageLimit(ctx, v.reader(), localModelNamespaceCache.UID, &oldUsage,
		localmodelcache.NamespaceCacheUsageOf(localModelNamespaceCache))
}

// ValidateDelete denies deleting a node-local cache while a Pod in its namespace mounts one of
// its claims. The node agent removes a model folder once no cache references its source URI,
// which would pull the files out from under the running Pod. A shared-PVC cache never deletes
// the data on its claim, so it can always be deleted.
func (v *LocalModelNamespaceCacheValidator) ValidateDelete(ctx context.Context, localModelNamespaceCache *v1alpha1.LocalModelNamespaceCache) (admission.Warnings, error) {
	localModelNamespaceCacheValidatorLogger.Info("validate delete", "name", localModelNamespaceCache.Name, "namespace", localModelNamespaceCache.Namespace)
	if localModelNamespaceCache.Spec.SharedPVCMode() {
		return nil, nil
	}
	return nil, v.validateNotInUse(ctx, localModelNamespaceCache, localmodelcache.NamespaceCacheClaimNames(localModelNamespaceCache))
}

// validateClaimsFree returns an error if one of the claims is that of another
// LocalModelNamespaceCache, or exists and is not bound to the volume of
// the cache, so the controller could not offer it. A claim left by an earlier cache of the same
// name is.
func (v *LocalModelNamespaceCacheValidator) validateClaimsFree(ctx context.Context, cache *v1alpha1.LocalModelNamespaceCache, claims []string) error {
	if len(claims) == 0 {
		return nil
	}
	// Names such as a with group b-c and a-b with group c give the same claim and volumes.
	caches := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := v.reader().List(ctx, caches, client.InNamespace(cache.Namespace)); err != nil {
		return err
	}
	for i := range caches.Items {
		other := &caches.Items[i]
		if other.Name == cache.Name {
			continue
		}
		if name := localmodelcache.SharedClaimName(claims, localmodelcache.NamespaceCacheClaimNames(other)); name != "" {
			return fmt.Errorf("PersistentVolumeClaim %s/%s is that of LocalModelNamespaceCache %s: use another name for this cache",
				cache.Namespace, name, other.Name)
		}
	}
	disabled, err := v.volumeManagementDisabled(ctx)
	if err != nil || disabled {
		return err // The claims are the users' own.
	}
	for _, name := range claims {
		key := types.NamespacedName{Namespace: cache.Namespace, Name: name}
		claim := &corev1.PersistentVolumeClaim{}
		if err := v.reader().Get(ctx, key, claim); apierr.IsNotFound(err) {
			continue
		} else if err != nil {
			return err
		}
		// A user's claim has no such annotation.
		owner := claim.Annotations[constants.LocalModelAnnotationKey]
		switch volume := claim.Spec.VolumeName; {
		case volume == localmodelcache.NamespaceCacheVolumeName(key) && (owner == "" || owner == cache.Name):
		case !claim.DeletionTimestamp.IsZero():
			return fmt.Errorf("PersistentVolumeClaim %s already exists and is being deleted: try again when it is gone", key)
		case volume == localmodelcache.ClusterCacheVolumeName(key):
			return fmt.Errorf("PersistentVolumeClaim %s already exists and belongs to LocalModelCache %s: mount that claim, or use another name for this cache",
				key, owner)
		default:
			return fmt.Errorf("PersistentVolumeClaim %s already exists and does not belong to this cache: delete it, or use another name for this cache", key)
		}
	}
	return nil
}

func (v *LocalModelNamespaceCacheValidator) volumeManagementDisabled(ctx context.Context) (bool, error) {
	configMap := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: constants.SystemNamespace, Name: constants.LocalModelConfigMapName}
	if err := v.reader().Get(ctx, key, configMap); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	config, err := localmodelcache.NewLocalModelConfig(configMap)
	if err != nil {
		return false, err
	}
	return config.DisableVolumeManagement, nil
}

// validateNotInUse returns an error while a Pod in the cache's namespace mounts one of the claims.
// A claim that a LocalModelCache created does not count: it stays.
func (v *LocalModelNamespaceCacheValidator) validateNotInUse(ctx context.Context, cache *v1alpha1.LocalModelNamespaceCache, names []string) error {
	claims := make([]string, 0, len(names))
	for _, name := range names {
		key := types.NamespacedName{Namespace: cache.Namespace, Name: name}
		claim := &corev1.PersistentVolumeClaim{}
		err := v.reader().Get(ctx, key, claim)
		if client.IgnoreNotFound(err) != nil {
			return err
		}
		if err != nil || claim.Spec.VolumeName != localmodelcache.ClusterCacheVolumeName(key) {
			claims = append(claims, name)
		}
	}
	pod, claim, err := localmodelcache.FindPodMountingClaim(ctx, v.reader(), cache.Namespace, claims)
	if err != nil {
		localModelNamespaceCacheValidatorLogger.Error(err, "Unable to list Pods", "namespace", cache.Namespace)
		return err
	}
	if pod != nil {
		return fmt.Errorf("LocalModelNamespaceCache %s/%s is in use: Pod %s mounts PersistentVolumeClaim %s. Delete the workload first",
			cache.Namespace, cache.Name, pod.Name, claim)
	}
	return nil
}

// reader returns the uncached reader, which avoids a cluster-wide Pod informer and sees caches
// created a moment ago. Unit tests that set no Reader fall back to the client.
func (v *LocalModelNamespaceCacheValidator) reader() client.Reader {
	if v.Reader != nil {
		return v.Reader
	}
	return v.Client
}

// validateStorageMode enforces the mutually-exclusive, at-least-one storage-mode rule and
// validates the pvcRef value. It mirrors the CEL rules on the spec so the same behavior is
// covered where direct unit tests and API lookups are needed. It covers all four cases:
// neither, node groups only, pvcRef only, and both.
func validateStorageMode(cache *v1alpha1.LocalModelNamespaceCache) error {
	hasNodeGroups := len(cache.Spec.NodeGroups) > 0
	hasPVCRef := cache.Spec.PVCRef != nil

	switch {
	case hasNodeGroups && hasPVCRef:
		return errors.New("nodeGroups and pvcRef are mutually exclusive")
	case !hasNodeGroups && !hasPVCRef:
		return errors.New("one of nodeGroups or pvcRef must be set")
	}

	if hasPVCRef {
		pvcName := *cache.Spec.PVCRef
		if pvcName == "" {
			return errors.New("pvcRef must not be empty")
		}
		if errs := apivalidation.IsDNS1123Subdomain(pvcName); len(errs) > 0 {
			return fmt.Errorf("pvcRef %q is not a valid PersistentVolumeClaim name: %v", pvcName, errs)
		}
	}
	return nil
}

// validatePVCRefImmutable rejects changes to pvcRef after creation.
func validatePVCRefImmutable(oldCache, newCache *v1alpha1.LocalModelNamespaceCache) error {
	oldRef := ""
	if oldCache.Spec.PVCRef != nil {
		oldRef = *oldCache.Spec.PVCRef
	}
	newRef := ""
	if newCache.Spec.PVCRef != nil {
		newRef = *newCache.Spec.PVCRef
	}
	if oldRef != newRef {
		return errors.New("pvcRef is immutable")
	}
	return nil
}

// validateDestinationConflict rejects a cache whose (namespace, pvcRef, storageKey) tuple is
// already claimed by another shared-PVC cache. Reconciliation remains authoritative against
// admission races, but this provides early feedback. Different storage keys on the same PVC
// are allowed.
func (v *LocalModelNamespaceCacheValidator) validateDestinationConflict(ctx context.Context, cache *v1alpha1.LocalModelNamespaceCache) error {
	storageKey := cache.Spec.StorageKey()
	caches := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := v.List(ctx, caches, client.InNamespace(cache.Namespace)); err != nil {
		return err
	}
	for i := range caches.Items {
		other := &caches.Items[i]
		if other.Name == cache.Name {
			continue
		}
		if !other.Spec.SharedPVCMode() {
			continue
		}
		if *other.Spec.PVCRef != *cache.Spec.PVCRef {
			continue
		}
		if other.Spec.StorageKey() == storageKey {
			return fmt.Errorf("destination pvc %q already holds model %q via cache %s/%s",
				*cache.Spec.PVCRef, cache.Spec.SourceModelUri, other.Namespace, other.Name)
		}
	}
	return nil
}

// validateNodeGroups checks that the node groups exist
func (v *LocalModelNamespaceCacheValidator) validateNodeGroups(ctx context.Context, nodeGroupNames []string) error {
	for _, nodeGroupName := range nodeGroupNames {
		nodeGroup := &v1alpha1.LocalModelNodeGroup{}
		if err := v.Get(ctx, client.ObjectKey{Name: nodeGroupName}, nodeGroup); err != nil {
			return fmt.Errorf("NodeGroup %s not found: %w", nodeGroupName, err)
		}
	}
	return nil
}

// without returns the values that are not in excluded.
func without(values, excluded []string) []string {
	return slices.DeleteFunc(slices.Clone(values), func(value string) bool { return slices.Contains(excluded, value) })
}
