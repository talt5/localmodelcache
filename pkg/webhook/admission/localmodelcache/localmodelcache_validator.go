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

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

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
	// Reader lists caches for the storage limit. Set it to the manager's APIReader.
	Reader client.Reader
}

// +kubebuilder:webhook:verbs=create;update;delete,path=/validate-localmodelcaches,mutating=false,failurePolicy=fail,groups=local.cache.ai,resources=localmodelcaches,versions=v1alpha1,name=localmodelcache.kserve-webhook-server.validator
var _ admission.Validator[*v1alpha1.LocalModelCache] = &LocalModelCacheValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (v *LocalModelCacheValidator) ValidateCreate(ctx context.Context, localModelCache *v1alpha1.LocalModelCache) (admission.Warnings, error) {
	localModelCacheValidatorLogger.Info("validate create", "name", localModelCache.Name)
	localModelCacheWithSameStorageURI, err := v.validateUniqueStorageURI(ctx, localModelCache)
	if err != nil {
		localModelCacheValidatorLogger.Error(err, "Unable to check LocalModelCache with the same storage URI")
		return nil, err
	}
	if localModelCacheWithSameStorageURI != nil {
		return admission.Warnings{}, fmt.Errorf("LocalModelCache %s has the same StorageURI %s", localModelCacheWithSameStorageURI.Name, localModelCacheWithSameStorageURI.Spec.SourceModelUri)
	}
	return nil, localmodelcache.CheckStorageLimit(ctx, v.reader(), "", nil, localmodelcache.CacheUsageOf(localModelCache))
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (v *LocalModelCacheValidator) ValidateUpdate(ctx context.Context, oldCache, localModelCache *v1alpha1.LocalModelCache) (admission.Warnings, error) {
	if localModelCache.GetDeletionTimestamp() != nil {
		return nil, nil
	}
	localModelCacheValidatorLogger.Info("validate update", "name", localModelCache.Name)
	localModelCacheWithSameStorageURI, err := v.validateUniqueStorageURI(ctx, localModelCache)
	if err != nil {
		localModelCacheValidatorLogger.Error(err, "Unable to check LocalModelCache with the same storage URI")
		return nil, err
	}
	if localModelCacheWithSameStorageURI != nil {
		return admission.Warnings{}, fmt.Errorf("LocalModelCache %s has the same StorageURI %s", localModelCacheWithSameStorageURI.Name, localModelCacheWithSameStorageURI.Spec.SourceModelUri)
	}
	oldUsage := localmodelcache.CacheUsageOf(oldCache)
	return nil, localmodelcache.CheckStorageLimit(ctx, v.reader(), localModelCache.UID, &oldUsage, localmodelcache.CacheUsageOf(localModelCache))
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
// A cluster-scoped cache exposes no claims to workloads: they mount the claims of a
// LocalModelNamespaceCache, whose own delete check protects them, and the node agent keeps a
// model folder while any cache still references its source URI.
func (v *LocalModelCacheValidator) ValidateDelete(_ context.Context, localModelCache *v1alpha1.LocalModelCache) (admission.Warnings, error) {
	localModelCacheValidatorLogger.Info("validate delete", "name", localModelCache.Name)
	return nil, nil
}

// Checks if there are other LocalModelCache with the same storage URI
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
		if localModelCache.Spec.SourceModelUri == currentLocalModelCache.Spec.SourceModelUri {
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
