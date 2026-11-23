/*
Copyright 2026 The KServe Authors.

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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

// CacheUsage is the disk space a node-local cache claims on each node of its node groups.
type CacheUsage struct {
	SourceModelUri string
	ModelSize      resource.Quantity
	NodeGroups     []string
}

// CacheUsageOf returns the usage of a cluster-scoped cache.
func CacheUsageOf(cache *v1alpha1.LocalModelCache) CacheUsage {
	return CacheUsage{
		SourceModelUri: cache.Spec.SourceModelUri,
		ModelSize:      cache.Spec.ModelSize,
		NodeGroups:     cache.Spec.NodeGroups,
	}
}

// NamespaceCacheUsageOf returns the usage of a namespace-scoped cache. A shared-PVC cache has
// no node groups, so it claims nothing.
func NamespaceCacheUsageOf(cache *v1alpha1.LocalModelNamespaceCache) CacheUsage {
	return CacheUsage{
		SourceModelUri: cache.Spec.SourceModelUri,
		ModelSize:      cache.Spec.ModelSize,
		NodeGroups:     cache.Spec.NodeGroups,
	}
}

// CheckStorageLimit returns an error when writing a cache would push a node group over its
// storageLimit. oldUsage is nil on create. uid identifies the stored copy of the cache, which
// is left out of the count.
//
// Caches with the same source URI share one folder on each node, so they count once, at the
// largest modelSize among them. A write that does not raise a group's total is always allowed,
// so caches that already exceed a limit can still be updated.
func CheckStorageLimit(ctx context.Context, reader client.Reader, uid types.UID, oldUsage *CacheUsage, usage CacheUsage) error {
	if len(usage.NodeGroups) == 0 {
		return nil
	}
	others, err := otherUsages(ctx, reader, uid)
	if err != nil {
		return err
	}
	before := others
	if oldUsage != nil {
		before = append(append([]CacheUsage{}, others...), *oldUsage)
	}
	after := append(append([]CacheUsage{}, others...), usage)

	for _, name := range usage.NodeGroups {
		total := groupTotal(after, name)
		current := groupTotal(before, name)
		if total.Cmp(current) <= 0 {
			continue
		}
		nodeGroup := &v1alpha1.LocalModelNodeGroup{}
		if err := reader.Get(ctx, types.NamespacedName{Name: name}, nodeGroup); apierrors.IsNotFound(err) {
			continue // The reconciler reports missing node groups.
		} else if err != nil {
			return err
		}
		if limit := nodeGroup.Spec.StorageLimit; total.Cmp(limit) > 0 {
			return fmt.Errorf("LocalModelNodeGroup %s: caching %s needs %s in total, which exceeds its storageLimit %s (in use: %s)",
				name, usage.SourceModelUri, total.String(), limit.String(), current.String())
		}
	}
	return nil
}

// otherUsages lists the usage of every cache in the cluster except the one with the given UID.
func otherUsages(ctx context.Context, reader client.Reader, uid types.UID) ([]CacheUsage, error) {
	var usages []CacheUsage
	caches := &v1alpha1.LocalModelCacheList{}
	if err := reader.List(ctx, caches); err != nil {
		return nil, err
	}
	for i := range caches.Items {
		if uid == "" || caches.Items[i].UID != uid {
			usages = append(usages, CacheUsageOf(&caches.Items[i]))
		}
	}
	namespaceCaches := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := reader.List(ctx, namespaceCaches); err != nil {
		return nil, err
	}
	for i := range namespaceCaches.Items {
		if uid == "" || namespaceCaches.Items[i].UID != uid {
			usages = append(usages, NamespaceCacheUsageOf(&namespaceCaches.Items[i]))
		}
	}
	return usages, nil
}

// groupTotal sums the model sizes cached on a node group, counting each source URI once.
func groupTotal(usages []CacheUsage, nodeGroup string) resource.Quantity {
	sizes := map[string]resource.Quantity{}
	for _, usage := range usages {
		for _, name := range usage.NodeGroups {
			if name != nodeGroup {
				continue
			}
			if size, ok := sizes[usage.SourceModelUri]; !ok || usage.ModelSize.Cmp(size) > 0 {
				sizes[usage.SourceModelUri] = usage.ModelSize
			}
		}
	}
	total := resource.Quantity{Format: resource.BinarySI}
	for _, size := range sizes {
		total.Add(size)
	}
	return total
}
