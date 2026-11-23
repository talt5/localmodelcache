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

package podconfig

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	kernelcacheutil "github.com/talt5/localmodelcache/pkg/kernelcache"
	kernelcacheconfig "github.com/talt5/localmodelcache/pkg/kernelcache/config"
)

func TestCaptureRegistryProjection(t *testing.T) {
	cfg := kernelcacheconfig.KernelCacheRegistryConfig{
		Endpoint: "registry.example:5000",
		Auth: kernelcacheconfig.KernelCacheRegistryAuth{
			Type:        kernelcacheconfig.KernelCacheRegistryAuthTypeServiceAccountToken,
			PushRoleRef: &kernelcacheconfig.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-pusher"},
			PullRoleRef: &kernelcacheconfig.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"},
		},
		CAConfigMapRef: &kernelcacheconfig.KernelCacheConfigMapKeyRef{Name: "registry-ca", Key: "bundle"},
	}
	pod := corev1.PodSpec{ServiceAccountName: "runtime"}
	container := corev1.Container{Name: "mcv"}
	require.NoError(t, ApplyCaptureRegistry(&pod, &container, cfg, "capture-secret"))
	require.NoError(t, ApplyCaptureRegistry(&pod, &container, cfg, "capture-secret"))
	require.Equal(t, "runtime", pod.ServiceAccountName)
	require.Len(t, pod.Volumes, 1)
	require.Len(t, pod.Volumes[0].Projected.Sources, 2)
	require.Len(t, container.VolumeMounts, 1)
	require.Len(t, container.Env, 3)
	require.True(t, container.VolumeMounts[0].ReadOnly)
	require.Equal(t, "access.json", pod.Volumes[0].Projected.Sources[0].Secret.Items[0].Key)
	require.True(t, *pod.Volumes[0].Projected.Sources[0].Secret.Optional)
	require.Nil(t, pod.Volumes[0].Projected.Sources[0].ServiceAccountToken)
	require.Nil(t, pod.AutomountServiceAccountToken)
}

func TestCaptureRegistryCompletesPartialState(t *testing.T) {
	cfg := captureRegistryTestConfig()
	fullPod := corev1.PodSpec{}
	fullContainer := corev1.Container{Name: "mcv"}
	require.NoError(t, ApplyCaptureRegistry(&fullPod, &fullContainer, cfg, "capture-secret"))

	t.Run("volume only", func(t *testing.T) {
		pod := corev1.PodSpec{Volumes: append([]corev1.Volume(nil), fullPod.Volumes...)}
		container := corev1.Container{Name: "mcv"}

		require.NoError(t, ApplyCaptureRegistry(&pod, &container, cfg, "capture-secret"))
		require.Equal(t, fullPod.Volumes, pod.Volumes)
		require.Equal(t, fullContainer.VolumeMounts, container.VolumeMounts)
		require.Equal(t, fullContainer.Env, container.Env)
	})

	t.Run("container configuration only", func(t *testing.T) {
		pod := corev1.PodSpec{}
		container := corev1.Container{
			Name:         "mcv",
			VolumeMounts: append([]corev1.VolumeMount(nil), fullContainer.VolumeMounts...),
			Env:          append([]corev1.EnvVar(nil), fullContainer.Env...),
		}

		require.NoError(t, ApplyCaptureRegistry(&pod, &container, cfg, "capture-secret"))
		require.Equal(t, fullPod.Volumes, pod.Volumes)
		require.Equal(t, fullContainer.VolumeMounts, container.VolumeMounts)
		require.Equal(t, fullContainer.Env, container.Env)
	})
}

func TestCaptureRegistryRejectsConflictsWithoutPartialMutation(t *testing.T) {
	for _, test := range []struct {
		name      string
		pod       corev1.PodSpec
		container corev1.Container
		want      string
	}{
		{
			name: "volume",
			pod: corev1.PodSpec{Volumes: []corev1.Volume{{
				Name: registryVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			}}},
			want: "conflicting volume",
		},
		{
			name: "volume mount",
			container: corev1.Container{VolumeMounts: []corev1.VolumeMount{{
				Name: "other", MountPath: registryPath,
			}}},
			want: "conflicting volume mount",
		},
		{
			name: "environment variable",
			container: corev1.Container{Env: []corev1.EnvVar{{
				Name: "MCV_REGISTRY_ALLOWED_ENDPOINT", Value: "other.example",
			}}},
			want: "conflicting environment variable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			podBefore := test.pod.DeepCopy()
			containerBefore := test.container.DeepCopy()

			err := ApplyCaptureRegistry(&test.pod, &test.container, captureRegistryTestConfig(), "capture-secret")
			require.ErrorContains(t, err, test.want)
			require.Equal(t, podBefore, &test.pod)
			require.Equal(t, containerBefore, &test.container)
		})
	}
}

func captureRegistryTestConfig() kernelcacheconfig.KernelCacheRegistryConfig {
	return kernelcacheconfig.KernelCacheRegistryConfig{
		Endpoint: "registry.example:5000",
		Auth: kernelcacheconfig.KernelCacheRegistryAuth{
			Type:        kernelcacheconfig.KernelCacheRegistryAuthTypeServiceAccountToken,
			PushRoleRef: &kernelcacheconfig.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-pusher"},
			PullRoleRef: &kernelcacheconfig.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"},
		},
		CAConfigMapRef: &kernelcacheconfig.KernelCacheConfigMapKeyRef{Name: "registry-ca", Key: "bundle"},
	}
}

func TestRegistryCAWithoutAuthentication(t *testing.T) {
	cfg := kernelcacheconfig.KernelCacheRegistryConfig{
		Auth:           kernelcacheconfig.KernelCacheRegistryAuth{Type: kernelcacheconfig.KernelCacheRegistryAuthTypeNone},
		CAConfigMapRef: &kernelcacheconfig.KernelCacheConfigMapKeyRef{Name: "ca", Key: "bundle"},
	}
	pod := corev1.PodSpec{}
	container := corev1.Container{}
	require.NoError(t, ApplyCaptureRegistry(&pod, &container, cfg, ""))
	require.Len(t, container.Env, 1)
	require.Equal(t, "MCV_REGISTRY_CA_FILE", container.Env[0].Name)
	cfg.Auth.Type = "invalid"
	require.Error(t, ApplyCaptureRegistry(&pod, &container, cfg, ""))
}

func TestCaptureRegistryInsecureWithoutAuthentication(t *testing.T) {
	cfg := kernelcacheconfig.KernelCacheRegistryConfig{Insecure: true}
	container := corev1.Container{}

	require.NoError(t, ApplyCaptureRegistry(&corev1.PodSpec{}, &container, cfg, ""))
	require.Equal(t, []corev1.EnvVar{{Name: kernelcacheutil.RegistryInsecureEnv, Value: "true"}}, container.Env)
}
