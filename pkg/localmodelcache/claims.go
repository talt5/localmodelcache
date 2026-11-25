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
	"crypto/sha256"
	"encoding/hex"
	"slices"

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

// ClusterCacheVolumeName returns the PersistentVolume behind a claim of a LocalModelCache. The hash
// keeps it apart from the volumes of other claims, of LocalModelNamespaceCaches and of downloads:
// names of caches, node groups and namespaces can be joined to the same text in many ways.
func ClusterCacheVolumeName(claim types.NamespacedName) string {
	hash := sha256.Sum256([]byte(claim.Namespace + "/" + claim.Name))
	return claim.Name + "-" + claim.Namespace + "-" + hex.EncodeToString(hash[:])[:16]
}

// SharedName returns a name that is in both lists, or "".
func SharedName(a, b []string) string {
	for _, name := range a {
		if slices.Contains(b, name) {
			return name
		}
	}
	return ""
}

// NamespaceCacheVolumeName returns the PersistentVolume behind a claim of a node-local
// LocalModelNamespaceCache.
func NamespaceCacheVolumeName(claim types.NamespacedName) string {
	return claim.Name + "-" + claim.Namespace
}

// NamespaceCacheObjects describes the claims and volumes of a LocalModelNamespaceCache in the
// namespace with these claims, those of its downloads included.
func NamespaceCacheObjects(namespace, jobNamespace string, claims []string) []string {
	objects := make([]string, 0, 4*len(claims))
	for _, claim := range claims {
		volume := NamespaceCacheVolumeName(types.NamespacedName{Namespace: namespace, Name: claim})
		download := DownloadVolumeName(volume)
		objects = append(objects, describeClaim(namespace, claim), describeVolume(volume),
			describeClaim(jobNamespace, download), describeVolume(download))
	}
	return objects
}

// ClusterCacheObjects describes the claims and volumes of a LocalModelCache with these claims.
// Those in the namespaces it selects are left out: their volumes have unique names, and the cache
// waits for the claims.
func ClusterCacheObjects(jobNamespace string, claims []string) []string {
	objects := make([]string, 0, 2*len(claims))
	for _, claim := range claims {
		objects = append(objects, describeClaim(jobNamespace, claim), describeVolume(DownloadVolumeName(claim)))
	}
	return objects
}

func describeClaim(namespace, name string) string {
	return "PersistentVolumeClaim " + namespace + "/" + name
}

func describeVolume(name string) string {
	return "PersistentVolume " + name
}

// DownloadVolumeName returns the PersistentVolume for downloads that belongs to a name.
func DownloadVolumeName(name string) string {
	return name + "-download"
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
