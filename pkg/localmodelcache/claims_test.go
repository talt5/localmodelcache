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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

func TestNamespaceCacheClaimNames(t *testing.T) {
	cache := &v1alpha1.LocalModelNamespaceCache{
		ObjectMeta: metav1.ObjectMeta{Name: "llama", Namespace: "team-a"},
		Spec:       v1alpha1.LocalModelNamespaceCacheSpec{NodeGroups: []string{"gpu-a", "gpu-b"}},
	}
	assert.Equal(t, []string{"llama-gpu-a", "llama-gpu-b"}, NamespaceCacheClaimNames(cache))

	cache.Spec.NodeGroups = nil
	assert.Empty(t, NamespaceCacheClaimNames(cache))
}

func TestPodMountsClaim(t *testing.T) {
	podWithClaim := func(claim string, phase corev1.PodPhase) *corev1.Pod {
		return &corev1.Pod{
			Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
				Name: "model",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim},
				},
			}}},
			Status: corev1.PodStatus{Phase: phase},
		}
	}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{name: "running pod with the claim", pod: podWithClaim("llama-gpu", corev1.PodRunning), want: true},
		{name: "pending pod with the claim", pod: podWithClaim("llama-gpu", corev1.PodPending), want: true},
		{name: "succeeded pod no longer reads", pod: podWithClaim("llama-gpu", corev1.PodSucceeded), want: false},
		{name: "failed pod no longer reads", pod: podWithClaim("llama-gpu", corev1.PodFailed), want: false},
		{name: "other claim", pod: podWithClaim("other", corev1.PodRunning), want: false},
		{name: "no volumes", pod: &corev1.Pod{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PodMountsClaim(tt.pod, "llama-gpu"))
		})
	}
}
