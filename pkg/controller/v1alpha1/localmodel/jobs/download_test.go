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

package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	pkgtypes "github.com/kserve/kserve/pkg/types"
)

func TestContainerFromConfig(t *testing.T) {
	tests := map[string]struct {
		config    *pkgtypes.StorageInitializerConfig
		image     string
		resources corev1.ResourceRequirements
	}{
		"no config": {image: DefaultJobImage},
		"image and resources": {
			config: &pkgtypes.StorageInitializerConfig{
				Image: "reg/init:v1", CpuRequest: "100m", CpuLimit: "2", MemoryRequest: "100Mi", MemoryLimit: "4Gi",
			},
			image: "reg/init:v1",
			resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("100Mi")},
				Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
			},
		},
		"unset and invalid values are left out": {
			config:    &pkgtypes.StorageInitializerConfig{CpuLimit: "lots", MemoryLimit: "4Gi"},
			image:     DefaultJobImage,
			resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")}},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			container := ContainerFromConfig(tc.config)
			require.Equal(t, tc.image, container.Image)
			require.Equal(t, tc.resources, container.Resources)
		})
	}
}
