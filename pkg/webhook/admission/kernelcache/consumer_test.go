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

package kernelcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	kernelcacheconfig "github.com/talt5/localmodelcache/pkg/kernelcache/config"
)

func TestInjectKernelCacheMountResolvesOptionalContainerPath(t *testing.T) {
	tests := []struct {
		name          string
		requestedPath string
		env           []corev1.EnvVar
		mountType     v1alpha1.KernelCacheMountType
		wantPath      string
	}{
		{
			name:          "preserves explicit path",
			requestedPath: "/explicit/cache",
			env:           []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/runtime/cache"}},
			mountType:     v1alpha1.KernelCacheMountTypeOCI,
			wantPath:      "/explicit/cache",
		},
		{
			name:      "uses runtime environment",
			env:       []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/runtime/cache"}},
			mountType: v1alpha1.KernelCacheMountTypeOCI,
			wantPath:  "/runtime/cache",
		},
		{
			name:      "uses fallback",
			mountType: "",
			wantPath:  "/root/.cache/vllm",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "model-pod", Namespace: "team"},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "vllm",
					Env:  test.env,
				}}},
			}
			cache := &v1alpha1.KernelCache{
				ObjectMeta: metav1.ObjectMeta{Name: "manual-cache", Namespace: "team"},
				Spec: v1alpha1.KernelCacheSpec{
					MountType: test.mountType,
					Artifact: v1alpha1.KernelCacheArtifact{
						ImageReference: "registry.example/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						CachePaths: []v1alpha1.KernelCachePath{{
							ContainerPath: test.requestedPath,
							OCIPath:       "io.vllm.cache",
						}},
					},
				},
			}

			err := injectKernelCacheMount(pod, cache, &kernelcacheconfig.KernelCacheConfig{})
			require.NoError(t, err)
			require.Contains(t, pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "kernel-cache-runtime-0",
				MountPath: test.wantPath,
			})
			require.Len(t, pod.Spec.InitContainers, 1)
			require.Contains(t, pod.Spec.InitContainers[0].Args, test.wantPath)
		})
	}
}

func TestInjectKernelCacheMountRejectsUnresolvedEnvironment(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "model-pod", Namespace: "team"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "vllm",
			Env: []corev1.EnvVar{{
				Name:      "VLLM_CACHE_ROOT",
				ValueFrom: &corev1.EnvVarSource{},
			}},
		}}},
	}
	cache := &v1alpha1.KernelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "manual-cache", Namespace: "team"},
		Spec: v1alpha1.KernelCacheSpec{
			MountType: v1alpha1.KernelCacheMountTypeOCI,
			Artifact: v1alpha1.KernelCacheArtifact{
				ImageReference: "registry.example/cache@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				CachePaths:     []v1alpha1.KernelCachePath{{OCIPath: "io.vllm.cache"}},
			},
		},
	}

	err := injectKernelCacheMount(pod, cache, &kernelcacheconfig.KernelCacheConfig{})
	require.ErrorContains(t, err, "VLLM_CACHE_ROOT uses valueFrom")
}

// A node reboot runs the init containers again, over the links of the first run.
func TestKernelCacheLinkerCanRunAgain(t *testing.T) {
	if output, err := exec.CommandContext(t.Context(), "cp", "--version").CombinedOutput(); err != nil || !strings.Contains(string(output), "GNU") {
		t.Skip("the linker image has GNU cp")
	}
	source, target := t.TempDir(), filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "torch", "rank_0"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source, "torch", "rank_0", "model"), []byte("captured"), 0o600))
	link := func() {
		t.Helper()
		output, err := exec.CommandContext(t.Context(), "/bin/sh", "-c", kernelCacheLinkerScript, kernelCacheLinkerName, source, target).CombinedOutput() // #nosec G204 -- fixed script
		require.NoError(t, err, string(output))
	}
	link()
	// What the runtime adds stays.
	require.NoError(t, os.WriteFile(filepath.Join(target, "torch", "new"), []byte("compiled"), 0o600))
	link()

	content, err := os.ReadFile(filepath.Join(target, "torch", "rank_0", "model")) // #nosec G304 -- test directory
	require.NoError(t, err)
	require.Equal(t, "captured", string(content))
	destination, err := os.Readlink(filepath.Join(target, "torch", "rank_0", "model"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(source, "torch", "rank_0", "model"), filepath.Clean(destination))
	require.FileExists(t, filepath.Join(target, "torch", "new"))
}
