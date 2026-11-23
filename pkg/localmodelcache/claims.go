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
	corev1 "k8s.io/api/core/v1"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

// NamespaceCacheClaimNames returns the PersistentVolumeClaims that expose a node-local
// LocalModelNamespaceCache to the workloads in its namespace: <cache>-<node group> for each
// node group, in spec order.
func NamespaceCacheClaimNames(cache *v1alpha1.LocalModelNamespaceCache) []string {
	claims := make([]string, 0, len(cache.Spec.NodeGroups))
	for _, nodeGroupName := range cache.Spec.NodeGroups {
		claims = append(claims, cache.Name+"-"+nodeGroupName)
	}
	return claims
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
