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

	corev1 "k8s.io/api/core/v1"
	apivalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
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
	// Reader lists Pods for the in-use check on delete and caches for the storage limit. Set it
	// to the manager's APIReader.
	Reader client.Reader
}

// +kubebuilder:webhook:verbs=create;update;delete,path=/validate-localmodelnamespacecaches,mutating=false,failurePolicy=fail,groups=local.cache.ai,resources=localmodelnamespacecaches,versions=v1alpha1,name=localmodelnamespacecache.kserve-webhook-server.validator
var _ admission.Validator[*v1alpha1.LocalModelNamespaceCache] = &LocalModelNamespaceCacheValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (v *LocalModelNamespaceCacheValidator) ValidateCreate(ctx context.Context, localModelNamespaceCache *v1alpha1.LocalModelNamespaceCache) (admission.Warnings, error) {
	localModelNamespaceCacheValidatorLogger.Info("validate create", "name", localModelNamespaceCache.Name, "namespace", localModelNamespaceCache.Namespace)

	if err := validateStorageMode(localModelNamespaceCache); err != nil {
		return nil, err
	}

	if localModelNamespaceCache.Spec.SharedPVCMode() {
		if err := v.validateDestinationConflict(ctx, localModelNamespaceCache); err != nil {
			return nil, err
		}
		return nil, nil
	}

	if err := v.validateNodeGroups(ctx, localModelNamespaceCache); err != nil {
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

	// pvcRef and sourceModelUri are both immutable, so the (pvcRef, storageKey) destination
	// cannot change on update; the create-time conflict check is sufficient. Re-checking here
	// would wedge both sides of a create race, since neither cache could be patched (for
	// example to add the finalizer) while the other exists.
	if localModelNamespaceCache.Spec.SharedPVCMode() {
		return nil, nil
	}

	if err := v.validateNodeGroups(ctx, localModelNamespaceCache); err != nil {
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
	claims := localmodelcache.NamespaceCacheClaimNames(localModelNamespaceCache)
	if len(claims) == 0 {
		return nil, nil
	}

	pods := &corev1.PodList{}
	if err := v.reader().List(ctx, pods, client.InNamespace(localModelNamespaceCache.Namespace)); err != nil {
		localModelNamespaceCacheValidatorLogger.Error(err, "Unable to list Pods", "namespace", localModelNamespaceCache.Namespace)
		return nil, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		for _, claim := range claims {
			if localmodelcache.PodMountsClaim(pod, claim) {
				return admission.Warnings{}, fmt.Errorf("LocalModelNamespaceCache %s/%s is in use: Pod %s mounts PersistentVolumeClaim %s. Delete the workload first",
					localModelNamespaceCache.Namespace, localModelNamespaceCache.Name, pod.Name, claim)
			}
		}
	}
	return nil, nil
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
	storageKey := v1alpha1.GetStorageKey(cache.Spec.SourceModelUri)
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
		if v1alpha1.GetStorageKey(other.Spec.SourceModelUri) == storageKey {
			return fmt.Errorf("destination pvc %q already holds model %q via cache %s/%s",
				*cache.Spec.PVCRef, cache.Spec.SourceModelUri, other.Namespace, other.Name)
		}
	}
	return nil
}

// validateNodeGroups checks that all node groups specified in the spec exist
func (v *LocalModelNamespaceCacheValidator) validateNodeGroups(ctx context.Context, cache *v1alpha1.LocalModelNamespaceCache) error {
	for _, nodeGroupName := range cache.Spec.NodeGroups {
		nodeGroup := &v1alpha1.LocalModelNodeGroup{}
		if err := v.Get(ctx, client.ObjectKey{Name: nodeGroupName}, nodeGroup); err != nil {
			return fmt.Errorf("NodeGroup %s not found: %w", nodeGroupName, err)
		}
	}
	return nil
}
