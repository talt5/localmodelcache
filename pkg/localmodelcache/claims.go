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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

// ClaimName returns the PersistentVolumeClaim that exposes a cache on a node group to workloads.
func ClaimName(cache, nodeGroup string) string {
	return cache + "-" + nodeGroup
}

// NamespaceCacheClaimNames returns the PersistentVolumeClaims that expose a node-local
// LocalModelNamespaceCache to the workloads in its namespace, in the order of its node groups.
func NamespaceCacheClaimNames(cache *v1alpha1.LocalModelNamespaceCache) []string {
	return claimNames(cache.Name, cache.Spec.NodeGroups)
}

// ClusterCacheClaimNames returns the PersistentVolumeClaims that expose a LocalModelCache in each
// namespace it selects, in the order of its node groups.
func ClusterCacheClaimNames(cache *v1alpha1.LocalModelCache) []string {
	return claimNames(cache.Name, cache.Spec.NodeGroups)
}

func claimNames(cache string, nodeGroups []string) []string {
	claims := make([]string, 0, len(nodeGroups))
	for _, nodeGroup := range nodeGroups {
		claims = append(claims, ClaimName(cache, nodeGroup))
	}
	return claims
}

// ClusterCacheVolumeName returns the PersistentVolume behind a claim of a LocalModelCache. It is
// never that of a LocalModelNamespaceCache, or two caches of the same name would fight over it.
func ClusterCacheVolumeName(claim types.NamespacedName) string {
	return claim.Namespace + "." + claim.Name
}

// NamespaceCacheVolumeName returns the PersistentVolume behind a claim of a node-local
// LocalModelNamespaceCache.
func NamespaceCacheVolumeName(claim types.NamespacedName) string {
	return claim.Name + "-" + claim.Namespace
}

// FindPodMountingClaim returns a Pod in the namespace that mounts one of the claims, and that
// claim, or nil.
func FindPodMountingClaim(ctx context.Context, reader client.Reader, namespace string, claims []string) (*corev1.Pod, string, error) {
	if len(claims) == 0 {
		return nil, "", nil
	}
	pods := &corev1.PodList{}
	if err := reader.List(ctx, pods, client.InNamespace(namespace)); err != nil {
		return nil, "", err
	}
	for i := range pods.Items {
		for _, claim := range claims {
			if PodMountsClaim(&pods.Items[i], claim) {
				return &pods.Items[i], claim, nil
			}
		}
	}
	return nil, "", nil
}

// PodMountsClaim reports whether pod references claimName and may still read it. The rule is
// the one pvc-protection uses: a Pod that references the claim and has not reached Succeeded
// or Failed is using it, terminating Pods included.
func PodMountsClaim(pod *corev1.Pod, claimName string) bool {
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return false
	}
	for i := range pod.Spec.Volumes {
		if pvcSource := pod.Spec.Volumes[i].PersistentVolumeClaim; pvcSource != nil && pvcSource.ClaimName == claimName {
			return true
		}
	}
	return false
}
