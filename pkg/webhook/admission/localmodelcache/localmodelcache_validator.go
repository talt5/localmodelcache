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

package localmodelcache

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

// maxConcurrentPodLists bounds the requests that one admission request makes at a time.
const maxConcurrentPodLists = 16

// logger for the validation webhook.
var localModelCacheValidatorLogger = logf.Log.WithName("localmodelcache-v1alpha1-validation-webhook")

// +kubebuilder:object:generate=false
// +k8s:openapi-gen=false
// LocalModelCacheValidator is responsible for validating the LocalModelCache resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type LocalModelCacheValidator struct {
	client.Client
	// Reader lists caches for the storage limit, and volumes and Pods for the in-use checks. Set it to the manager's APIReader.
	Reader client.Reader
}

// +kubebuilder:webhook:verbs=create;update;delete,path=/validate-localmodelcaches,mutating=false,failurePolicy=fail,groups=local.cache.ai,resources=localmodelcaches,versions=v1alpha1,name=localmodelcache-validator.webhook.local.cache.ai
var _ admission.Validator[*v1alpha1.LocalModelCache] = &LocalModelCacheValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (v *LocalModelCacheValidator) ValidateCreate(ctx context.Context, localModelCache *v1alpha1.LocalModelCache) (admission.Warnings, error) {
	localModelCacheValidatorLogger.Info("validate create", "name", localModelCache.Name)
	claims := localmodelcache.ClusterCacheClaimNames(localModelCache)
	if err := localmodelcache.ValidateModelSize(nil, localModelCache.Spec.ModelSize); err != nil {
		return nil, err
	}
	if err := validateNamespaceSelector(localModelCache); err != nil {
		return nil, err
	}
	localModelCacheWithSameStorageURI, err := v.validateUniqueStorageURI(ctx, localModelCache)
	if err != nil {
		localModelCacheValidatorLogger.Error(err, "Unable to check LocalModelCache with the same storage URI")
		return nil, err
	}
	if localModelCacheWithSameStorageURI != nil {
		return admission.Warnings{}, fmt.Errorf("LocalModelCache %s has the same StorageURI %s", localModelCacheWithSameStorageURI.Name, localModelCacheWithSameStorageURI.Spec.SourceModelUri)
	}
	if err := v.validateUniqueClaims(ctx, localModelCache, claims); err != nil {
		return nil, err
	}
	return nil, localmodelcache.CheckStorageLimit(ctx, v.reader(), "", nil, localmodelcache.CacheUsageOf(localModelCache))
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (v *LocalModelCacheValidator) ValidateUpdate(ctx context.Context, oldCache, localModelCache *v1alpha1.LocalModelCache) (admission.Warnings, error) {
	if localModelCache.GetDeletionTimestamp() != nil {
		return nil, nil
	}
	localModelCacheValidatorLogger.Info("validate update", "name", localModelCache.Name)
	if err := localmodelcache.ValidateModelSize(&oldCache.Spec.ModelSize, localModelCache.Spec.ModelSize); err != nil {
		return nil, err
	}
	if err := localmodelcache.ValidateStorageParameters(oldCache.Spec.Storage, localModelCache.Spec.Storage); err != nil {
		return nil, err
	}
	if err := validateNamespaceSelector(localModelCache); err != nil {
		return nil, err
	}
	localModelCacheWithSameStorageURI, err := v.validateUniqueStorageURI(ctx, localModelCache)
	if err != nil {
		localModelCacheValidatorLogger.Error(err, "Unable to check LocalModelCache with the same storage URI")
		return nil, err
	}
	if localModelCacheWithSameStorageURI != nil {
		return admission.Warnings{}, fmt.Errorf("LocalModelCache %s has the same StorageURI %s", localModelCacheWithSameStorageURI.Name, localModelCacheWithSameStorageURI.Spec.SourceModelUri)
	}
	// Claims the cache already names do not stop other changes to it.
	added := slices.DeleteFunc(localmodelcache.ClusterCacheClaimNames(localModelCache), func(name string) bool {
		return slices.Contains(localmodelcache.ClusterCacheClaimNames(oldCache), name)
	})
	if err := v.validateUniqueClaims(ctx, localModelCache, added); err != nil {
		return nil, err
	}
	if err := v.validateRemovedClaimsNotInUse(ctx, oldCache, localModelCache); err != nil {
		return nil, err
	}
	oldUsage := localmodelcache.CacheUsageOf(oldCache)
	return nil, localmodelcache.CheckStorageLimit(ctx, v.reader(), localModelCache.UID, &oldUsage, localmodelcache.CacheUsageOf(localModelCache))
}

// ValidateDelete denies deleting a cache while a Pod mounts one of its claims. The node agent
// removes a model folder once no cache references its source, which would pull the files out from
// under the running Pod.
func (v *LocalModelCacheValidator) ValidateDelete(ctx context.Context, localModelCache *v1alpha1.LocalModelCache) (admission.Warnings, error) {
	localModelCacheValidatorLogger.Info("validate delete", "name", localModelCache.Name)
	volumes, err := v.servingVolumes(ctx, localModelCache)
	if err != nil {
		return nil, err
	}
	return nil, v.validateNotInUse(ctx, localModelCache, volumes)
}

func validateNamespaceSelector(cache *v1alpha1.LocalModelCache) error {
	if _, err := cache.Spec.WorkloadNamespaceSelector(); err != nil {
		return fmt.Errorf("namespaceSelector is invalid: %w", err)
	}
	return nil
}

// validateUniqueClaims denies a cache with a claim that another LocalModelCache has too, such as
// those of cache a with group b-c and cache a-b with group c.
func (v *LocalModelCacheValidator) validateUniqueClaims(ctx context.Context, cache *v1alpha1.LocalModelCache, claims []string) error {
	if len(claims) == 0 {
		return nil
	}
	caches := &v1alpha1.LocalModelCacheList{}
	if err := v.reader().List(ctx, caches); err != nil {
		return err
	}
	for i := range caches.Items {
		other := &caches.Items[i]
		if other.Name == cache.Name {
			continue
		}
		if name := localmodelcache.SharedName(claims, localmodelcache.ClusterCacheClaimNames(other)); name != "" {
			return fmt.Errorf("PersistentVolumeClaim %s is that of LocalModelCache %s: use another name for this cache", name, other.Name)
		}
	}
	namespaceCaches := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := v.reader().List(ctx, namespaceCaches); err != nil {
		return err
	}
	for i := range namespaceCaches.Items {
		other := &namespaceCaches.Items[i]
		volumes := localmodelcache.NamespaceCacheVolumeNames(other.Namespace, localmodelcache.NamespaceCacheClaimNames(other))
		if name := localmodelcache.SharedName(localmodelcache.ClusterCacheDownloadVolumeNames(claims), volumes); name != "" {
			return fmt.Errorf("PersistentVolume %s is that of LocalModelNamespaceCache %s/%s: use another name for this cache", name, other.Namespace, other.Name)
		}
	}
	return nil
}

// validateRemovedClaimsNotInUse denies a change of the node groups or the namespace selector while
// a Pod mounts a claim that the controller would delete.
func (v *LocalModelCacheValidator) validateRemovedClaimsNotInUse(ctx context.Context, oldCache, newCache *v1alpha1.LocalModelCache) error {
	if equality.Semantic.DeepEqual(oldCache.Spec.NodeGroups, newCache.Spec.NodeGroups) &&
		equality.Semantic.DeepEqual(oldCache.Spec.NamespaceSelector, newCache.Spec.NamespaceSelector) {
		return nil
	}
	selector, err := newCache.Spec.WorkloadNamespaceSelector()
	if err != nil {
		return err
	}
	volumes, err := v.servingVolumes(ctx, newCache)
	if err != nil {
		return err
	}
	// One request, however many namespaces there are.
	namespaces := &metav1.PartialObjectMetadataList{}
	namespaces.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("NamespaceList"))
	if err := v.reader().List(ctx, namespaces, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return err
	}
	selected := sets.New[string]()
	for i := range namespaces.Items {
		selected.Insert(namespaces.Items[i].Name)
	}
	claimNames := localmodelcache.ClusterCacheClaimNames(newCache)
	var removed []corev1.PersistentVolume
	for i := range volumes {
		ref := volumes[i].Spec.ClaimRef
		keepsGroup := slices.Contains(claimNames, ref.Name)
		// The change does not remove one that is already being deleted. But with its node group,
		// the files would go from under the Pods that still hold it.
		if !volumes[i].DeletionTimestamp.IsZero() && keepsGroup {
			continue
		}
		if !selected.Has(ref.Namespace) || !keepsGroup {
			removed = append(removed, volumes[i])
		}
	}
	return v.validateNotInUse(ctx, newCache, removed)
}

// servingVolumes returns the volumes of the cache that workloads mount, not those of its download
// Jobs. Unlike the claims, they carry the cache's label even if a user recreated the claim.
func (v *LocalModelCacheValidator) servingVolumes(ctx context.Context, cache *v1alpha1.LocalModelCache) ([]corev1.PersistentVolume, error) {
	volumes := &corev1.PersistentVolumeList{}
	if err := v.reader().List(ctx, volumes, client.MatchingLabels{constants.LocalModelUIDLabelKey: string(cache.UID)}); err != nil {
		localModelCacheValidatorLogger.Error(err, "Unable to list PersistentVolumes")
		return nil, err
	}
	return slices.DeleteFunc(volumes.Items, func(volume corev1.PersistentVolume) bool {
		ref := volume.Spec.ClaimRef
		return ref == nil || volume.Name != localmodelcache.ClusterCacheVolumeName(types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name})
	}), nil
}

// validateNotInUse returns an error while a Pod mounts the claim of one of the volumes.
func (v *LocalModelCacheValidator) validateNotInUse(ctx context.Context, cache *v1alpha1.LocalModelCache, volumes []corev1.PersistentVolume) error {
	claims := map[string][]string{}
	for i := range volumes {
		ref := volumes[i].Spec.ClaimRef
		claims[ref.Namespace] = append(claims[ref.Namespace], ref.Name)
	}
	// The webhook must answer in time for a cache with claims in very many namespaces.
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(maxConcurrentPodLists)
	for namespace, names := range claims {
		group.Go(func() error {
			for {
				pod, name, err := localmodelcache.FindPodMountingClaim(ctx, v.reader(), namespace, names)
				if err != nil {
					return fmt.Errorf("list Pods in namespace %s: %w", namespace, err)
				}
				if pod == nil {
					return nil
				}
				// The claim may be that of something else. The volume of the cache then has no claim.
				key := types.NamespacedName{Namespace: namespace, Name: name}
				claim := &corev1.PersistentVolumeClaim{}
				err = v.reader().Get(ctx, key, claim)
				if client.IgnoreNotFound(err) != nil {
					return err
				}
				if volume := claim.Spec.VolumeName; err != nil || volume == "" || volume == localmodelcache.ClusterCacheVolumeName(key) {
					return fmt.Errorf("LocalModelCache %s is in use: Pod %s/%s mounts PersistentVolumeClaim %s. Delete the workload first",
						cache.Name, namespace, pod.Name, name)
				}
				names = slices.DeleteFunc(names, func(other string) bool { return other == name })
			}
		})
	}
	return group.Wait()
}

// Checks if there are other LocalModelCache with the same storage URI and parameters
func (v *LocalModelCacheValidator) validateUniqueStorageURI(ctx context.Context, currentLocalModelCache *v1alpha1.LocalModelCache) (*v1alpha1.LocalModelCache, error) {
	// Get all LocalModelCache CR
	localModelCacheList := &v1alpha1.LocalModelCacheList{}
	if err := v.List(ctx, localModelCacheList); err != nil {
		localModelCacheValidatorLogger.Error(err, "Unable to list LocalModelCaches")
		return nil, err
	}
	// Check the storage URI of each LocalModelCache CR except for the current one
	for _, localModelCache := range localModelCacheList.Items {
		// Skip the current one
		if localModelCache.Name == currentLocalModelCache.Name {
			continue
		}
		if localModelCache.Spec.StorageKey() == currentLocalModelCache.Spec.StorageKey() {
			return &localModelCache, nil
		}
	}
	return nil, nil
}

// reader returns the uncached reader, which sees caches created a moment ago. Unit tests that
// set no Reader fall back to the client.
func (v *LocalModelCacheValidator) reader() client.Reader {
	if v.Reader != nil {
		return v.Reader
	}
	return v.Client
}
