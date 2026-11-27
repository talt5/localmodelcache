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
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/kernelcache/captureconfig"
	kernelcacheconfig "github.com/talt5/localmodelcache/pkg/kernelcache/config"
	cacheidentity "github.com/talt5/localmodelcache/pkg/kernelcache/identity"
	"github.com/talt5/localmodelcache/pkg/kernelcache/registryauth"
	"github.com/talt5/localmodelcache/pkg/kernelcache/reporter"
)

func TestBuildRuntimeIdentityFactorsIncludesSupportedRuntimeConfigFactors(t *testing.T) {
	container := &corev1.Container{
		Image:   "registry.example/vllm:v0.28.0",
		Command: []string{"vllm", "--dtype", "bfloat16", "-tp", "2"},
		Args:    []string{"--max-model-len=4096", "--enforce-eager"},
		Env: []corev1.EnvVar{
			{Name: "VLLM_USE_AOT_COMPILE", Value: "true"},
			{Name: "VLLM_DISABLED_KERNELS", Value: "Marlin, ExLlama"},
			{Name: "VLLM_CACHE_ROOT", Value: "/tmp/vllm"},
			{Name: "VLLM_BATCH_INVARIANT", ValueFrom: &corev1.EnvVarSource{}},
		},
	}

	identityFactors := buildRuntimeIdentityFactors(container, "hf://org/model")
	require.Equal(t, map[string]string{
		cacheidentity.DTypeFactor:              "bfloat16",
		cacheidentity.TensorParallelSizeFactor: "2",
		cacheidentity.MaxModelLenFactor:        "4096",
		cacheidentity.EnforceEagerFactor:       "true",
		"env.VLLM_USE_AOT_COMPILE":             "true",
		"env.VLLM_DISABLED_KERNELS":            "ExLlama,Marlin",
	}, identityFactors.RuntimeConfigFactors)
}

func TestInjectSidecar(t *testing.T) {
	for _, tc := range []struct {
		name              string
		capture           *v1alpha1.KernelCacheCapture
		registryEndpoint  string
		wantContainerPath string
		wantTargetImage   string
		wantCaptureName   string
	}{
		{
			name:              "uses deterministic revision capture when capture is missing",
			registryEndpoint:  "registry.example:5000",
			wantContainerPath: "/cache/vllm",
			wantTargetImage:   "registry.example:5000/test/kernel-cache-qwen:",
			wantCaptureName:   "qwen-kcc-669889f77f",
		},
		{
			name: "uses capture overrides",
			capture: &v1alpha1.KernelCacheCapture{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen-kcc-669889f77f", Namespace: "test"},
				Spec: v1alpha1.KernelCacheCaptureSpec{
					SourceRef:   v1alpha1.KernelCacheSourceRef{Kind: constants.KernelCacheWorkloadKind, Name: "qwen"},
					CachePaths:  []v1alpha1.KernelCachePath{{ContainerName: "vllm", ContainerPath: "/custom/vllm", OCIPath: "io.vllm.cache"}},
					TargetImage: "registry.example:5000/test/custom:v1",
				},
			},
			registryEndpoint:  "registry.example:5000",
			wantContainerPath: "/custom/vllm",
			wantTargetImage:   "registry.example:5000/test/custom:v1",
			wantCaptureName:   "qwen-kcc-669889f77f",
		},
		{
			name: "resolves omitted capture path from runtime environment",
			capture: &v1alpha1.KernelCacheCapture{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen-kcc-669889f77f", Namespace: "test"},
				Spec: v1alpha1.KernelCacheCaptureSpec{
					SourceRef:  v1alpha1.KernelCacheSourceRef{Kind: constants.KernelCacheWorkloadKind, Name: "qwen"},
					CachePaths: []v1alpha1.KernelCachePath{{ContainerName: "vllm"}},
				},
			},
			registryEndpoint:  "registry.example:5000",
			wantContainerPath: "/cache/vllm",
			wantTargetImage:   "registry.example:5000/test/kernel-cache-qwen:",
			wantCaptureName:   "qwen-kcc-669889f77f",
		},
		{
			name: "uses default paths when capture only sets target image",
			capture: &v1alpha1.KernelCacheCapture{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen-kcc-669889f77f", Namespace: "test"},
				Spec: v1alpha1.KernelCacheCaptureSpec{
					SourceRef:   v1alpha1.KernelCacheSourceRef{Kind: constants.KernelCacheWorkloadKind, Name: "qwen"},
					TargetImage: "registry.example:5000/test/custom:v1",
				},
			},
			wantContainerPath: "/cache/vllm",
			wantTargetImage:   "registry.example:5000/test/custom:v1",
			wantCaptureName:   "qwen-kcc-669889f77f",
		},
		{
			name: "uses default target image when capture only sets paths",
			capture: &v1alpha1.KernelCacheCapture{
				ObjectMeta: metav1.ObjectMeta{Name: "qwen-kcc-669889f77f", Namespace: "test"},
				Spec: v1alpha1.KernelCacheCaptureSpec{
					SourceRef:  v1alpha1.KernelCacheSourceRef{Kind: constants.KernelCacheWorkloadKind, Name: "qwen"},
					CachePaths: []v1alpha1.KernelCachePath{{ContainerName: "vllm", ContainerPath: "/custom/vllm", OCIPath: "io.vllm.cache"}},
				},
			},
			registryEndpoint:  "registry.example:5000",
			wantContainerPath: "/custom/vllm",
			wantTargetImage:   "registry.example:5000/test/kernel-cache-qwen:",
			wantCaptureName:   "qwen-kcc-669889f77f",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, appsv1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tc.capture != nil {
				builder.WithObjects(tc.capture)
			}
			mutator := &PodMutator{Client: builder.Build()}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Labels:    map[string]string{constants.KernelCacheWorkloadLabelKey: "qwen", "pod-template-hash": "669889f77f"},
				Annotations: map[string]string{
					constants.KernelCacheModelURIAnnotationKey: "hf://Qwen/Qwen3-0.6B",
				},
			}, Spec: corev1.PodSpec{ServiceAccountName: "runtime", Containers: []corev1.Container{{Name: "vllm", Env: []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"}}, ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt(8080)}}, PeriodSeconds: 5, TimeoutSeconds: 2}}}}}
			cfg := &kernelcacheconfig.KernelCacheConfig{MCVImage: "example/mcv:test", MCVCaptureReadinessTimeoutSeconds: 900, Registry: kernelcacheconfig.KernelCacheRegistryConfig{Endpoint: tc.registryEndpoint}}
			originalConfig := cfg.DeepCopy()
			originalEnv := append([]corev1.EnvVar(nil), pod.Spec.Containers[0].Env...)
			require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
			require.NotContains(t, pod.Annotations, "internal.local.cache.ai/mcv-injected")
			require.Equal(t, originalConfig, cfg)
			require.Equal(t, originalEnv, pod.Spec.Containers[0].Env)
			require.Equal(t, "runtime", pod.Spec.ServiceAccountName)
			require.Len(t, pod.Spec.Containers, 2)
			require.Equal(t, cfg.MCVImage, pod.Spec.Containers[1].Image)
			require.Equal(t, tc.wantContainerPath, pod.Spec.Containers[0].VolumeMounts[0].MountPath)
			captureValue := containerEnvValue(pod.Spec.Containers[1], captureconfig.CaptureConfigEnv)
			var captureConfig captureconfig.CaptureConfig
			require.NoError(t, json.Unmarshal([]byte(captureValue), &captureConfig))
			readinessValue := containerEnvValue(pod.Spec.Containers[1], captureconfig.ReadinessConfigEnv)
			var readinessConfig captureconfig.ReadinessConfig
			require.NoError(t, json.Unmarshal([]byte(readinessValue), &readinessConfig))
			require.Equal(t, int64(900), readinessConfig.MCVCaptureReadinessTimeoutSeconds)
			require.True(t, strings.HasPrefix(captureConfig.TargetImage, tc.wantTargetImage))
			captureName := captureConfig.Capture.Name
			require.Equal(t, tc.wantCaptureName, captureName)
			require.Equal(t, tc.wantContainerPath, captureConfig.CachePaths[0].ContainerPath)
			require.Equal(t, "io.vllm.cache", captureConfig.CachePaths[0].OCIPath)
			require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
			require.Len(t, pod.Spec.Containers, 2)
		})
	}
}

func TestInjectSidecarUsesReplicaSetRevisionIdentity(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "team",
		Labels: map[string]string{
			constants.KernelCacheWorkloadLabelKey: "qwen",
			"pod-template-hash":                   "669889f77f",
		},
		Annotations: map[string]string{
			constants.KernelCacheModelURIAnnotationKey: "hf://Qwen/Qwen3-0.6B",
		},
	}, Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name:           "vllm",
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt(8080)}}},
	}}}}
	cfg := &kernelcacheconfig.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: kernelcacheconfig.KernelCacheRegistryConfig{Endpoint: "registry.example:5000"}}
	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))

	var captureConfig captureconfig.CaptureConfig
	require.NoError(t, json.Unmarshal([]byte(containerEnvValue(pod.Spec.Containers[1], captureconfig.CaptureConfigEnv)), &captureConfig))
	require.Equal(t, "qwen-kcc-669889f77f", captureConfig.Capture.Name)
	require.Contains(t, captureConfig.TargetImage, ":669889f77f")
	require.Equal(t, reporter.SecretName(captureConfig.Capture.Name), pod.Annotations[reporter.AccessSecretAnnotation])
	var reporterVolume *corev1.Volume
	for index := range pod.Spec.Volumes {
		if pod.Spec.Volumes[index].Name == "mcv-reporter-access" {
			reporterVolume = &pod.Spec.Volumes[index]
		}
	}
	require.NotNil(t, reporterVolume)
	require.True(t, *reporterVolume.Projected.Sources[0].Secret.Optional, "the Secret is gone when the Pod starts again after its capture")
}

func TestInjectSidecarConfiguresRegistryAccessForServiceAccountToken(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	pod := sidecarTestPod()
	cfg := &kernelcacheconfig.KernelCacheConfig{
		MCVImage: "example/mcv:test",
		Registry: kernelcacheconfig.KernelCacheRegistryConfig{
			Endpoint: "registry.example:5000",
			Auth: kernelcacheconfig.KernelCacheRegistryAuth{
				Type:        kernelcacheconfig.KernelCacheRegistryAuthTypeServiceAccountToken,
				PushRoleRef: &kernelcacheconfig.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-pusher"},
				PullRoleRef: &kernelcacheconfig.KernelCacheRegistryRoleRef{Kind: "ClusterRole", Name: "registry-puller"},
			},
		},
	}

	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
	var captureConfig captureconfig.CaptureConfig
	require.NoError(t, json.Unmarshal([]byte(containerEnvValue(pod.Spec.Containers[1], captureconfig.CaptureConfigEnv)), &captureConfig))
	require.Equal(t, "mcv-registry-"+captureConfig.Capture.SessionID, pod.Annotations[registryauth.AccessSecretAnnotation])
	require.Equal(t, "/var/run/secrets/mcv-registry/access.json", containerEnvValue(pod.Spec.Containers[1], "MCV_REGISTRY_ACCESS_FILE"))
	var registryVolume *corev1.Volume
	for index := range pod.Spec.Volumes {
		if pod.Spec.Volumes[index].Name == "mcv-registry-access" {
			registryVolume = &pod.Spec.Volumes[index]
		}
	}
	require.NotNil(t, registryVolume)
	require.Equal(t, "mcv-registry-"+captureConfig.Capture.SessionID, registryVolume.Projected.Sources[0].Secret.Name)
}

func TestInjectSidecarSkipsPodWithoutReplicaSetRevision(t *testing.T) {
	pod := sidecarTestPod()
	delete(pod.Labels, "pod-template-hash")
	cfg := &kernelcacheconfig.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: kernelcacheconfig.KernelCacheRegistryConfig{Endpoint: "registry.example:5000"}}
	mutator := &PodMutator{Client: fake.NewClientBuilder().Build()}

	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 1)
}

func TestReadinessProbeConfigRestrictsHostToLoopback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		host    string
		wantURL string
		wantErr bool
	}{
		{name: "defaults to loopback", wantURL: "http://127.0.0.1:8080/health"},
		{name: "accepts localhost", host: "localhost", wantURL: "http://127.0.0.1:8080/health"},
		{name: "accepts loopback IPv4", host: "127.0.0.2", wantURL: "http://127.0.0.2:8080/health"},
		{name: "accepts loopback IPv6", host: "::1", wantURL: "http://[::1]:8080/health"},
		{name: "rejects service host", host: "kubernetes.default.svc", wantErr: true},
		{name: "rejects metadata address", host: "169.254.169.254", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			container := &corev1.Container{
				Name: "runtime",
				ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{Host: tc.host, Path: "/health", Port: intstr.FromInt(8080)},
				}},
			}
			config, err := readinessProbeConfig(container, 600)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantURL, config.URL)
		})
	}
}

func sidecarTestPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Labels:    map[string]string{constants.KernelCacheWorkloadLabelKey: "qwen", "pod-template-hash": "669889f77f"},
			Annotations: map[string]string{
				constants.KernelCacheModelURIAnnotationKey: "hf://Qwen/Qwen3-0.6B",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "vllm",
			Env:  []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"}},
			ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt(8080)},
			}},
		}}},
	}
}

func containerEnvValue(container corev1.Container, name string) string {
	for _, env := range container.Env {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

func TestTerminalCaptureSkipsSidecarWithoutReadyCache(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-kcc-669889f77f", Namespace: "test"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: constants.KernelCacheWorkloadKind, Name: "qwen",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{Phase: v1alpha1.KernelCacheCapturePhaseComplete},
	}
	pod := sidecarTestPod()
	pod.Spec.ServiceAccountName = "runtime"
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture).Build()}
	cfg := &kernelcacheconfig.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: kernelcacheconfig.KernelCacheRegistryConfig{Endpoint: "registry.example"}}
	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 1)
	require.NotContains(t, pod.Annotations, reporter.AccessSecretAnnotation)
	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 1)
}

func TestSidecarSkipsPodWithoutModelSource(t *testing.T) {
	pod := sidecarTestPod()
	delete(pod.Annotations, constants.KernelCacheModelURIAnnotationKey)
	cfg := &kernelcacheconfig.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: kernelcacheconfig.KernelCacheRegistryConfig{Endpoint: "registry.example"}}
	mutator := &PodMutator{Client: fake.NewClientBuilder().Build()}

	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 1)
	require.NotContains(t, pod.Annotations, reporter.AccessSecretAnnotation)
}

func TestTerminalCaptureSkipsSidecarWithReadyCompatibilityCache(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	pod := sidecarTestPod()
	pod.Namespace = "team"
	pod.Spec.Containers[0].Image = "registry.example/vllm:v0.10"
	pod.Annotations = map[string]string{
		constants.KernelCacheModelURIAnnotationKey: "hf://Qwen/Qwen3-0.6B",
	}
	requested, err := identityForPod(pod, pod.Annotations[constants.KernelCacheModelURIAnnotationKey])
	require.NoError(t, err)
	cache := readyCache("matching", requested)
	node := kernelCacheNode("node-a", cache, v1alpha1.KernelCacheNodePreparationStateReady)
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-kcc-669889f77f", Namespace: "team"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: constants.KernelCacheWorkloadKind, Name: "qwen",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{Phase: v1alpha1.KernelCacheCapturePhaseComplete},
	}
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&node, &cache, capture).Build()}
	selection, found, err := mutator.findKernelCacheSelectionWithConfig(context.Background(), pod, &kernelcacheconfig.KernelCacheConfig{ArtifactSecurity: kernelcacheconfig.KernelCacheArtifactSecurityConfig{Mode: "none"}})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, kernelCacheMatchCompatibility, selection.matchType)

	cfg := &kernelcacheconfig.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: kernelcacheconfig.KernelCacheRegistryConfig{Endpoint: "registry.example"}}
	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 1)
	require.NotContains(t, pod.Annotations, reporter.AccessSecretAnnotation)
}

func TestTerminalCaptureSkipsSidecarWithReadyWorkloadCache(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	pod := sidecarTestPod()
	pod.Namespace = "team"
	pod.Spec.Containers[0].Image = "registry.example/vllm@sha256:" + strings.Repeat("b", 64)
	pod.Annotations = map[string]string{
		constants.KernelCacheModelURIAnnotationKey: "hf://Qwen/Qwen3-0.6B",
	}
	requested, err := identityForPod(pod, pod.Annotations[constants.KernelCacheModelURIAnnotationKey])
	require.NoError(t, err)
	cache := readyCache("matching", requested)
	node := kernelCacheNode("node-a", cache, v1alpha1.KernelCacheNodePreparationStateReady)
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-kcc-669889f77f", Namespace: "team"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: constants.KernelCacheWorkloadKind, Name: "qwen",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{Phase: v1alpha1.KernelCacheCapturePhaseComplete},
	}
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&node, &cache, capture).Build()}
	selection, found, err := mutator.findKernelCacheSelectionWithConfig(context.Background(), pod, &kernelcacheconfig.KernelCacheConfig{ArtifactSecurity: kernelcacheconfig.KernelCacheArtifactSecurityConfig{Mode: "none"}})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, kernelCacheMatchWorkload, selection.matchType)

	cfg := &kernelcacheconfig.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: kernelcacheconfig.KernelCacheRegistryConfig{Endpoint: "registry.example"}}
	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 1)
	require.NotContains(t, pod.Annotations, reporter.AccessSecretAnnotation)
}

func TestNonTerminalCaptureStillInjectsCaptureSidecar(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	capture := &v1alpha1.KernelCacheCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-kcc-669889f77f", Namespace: "test"},
		Spec: v1alpha1.KernelCacheCaptureSpec{SourceRef: v1alpha1.KernelCacheSourceRef{
			Kind: constants.KernelCacheWorkloadKind, Name: "qwen",
		}},
		Status: v1alpha1.KernelCacheCaptureStatus{Phase: v1alpha1.KernelCacheCapturePhaseCapturing},
	}
	pod := sidecarTestPod()
	mutator := &PodMutator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(capture).Build()}
	cfg := &kernelcacheconfig.KernelCacheConfig{MCVImage: "example/mcv:test", Registry: kernelcacheconfig.KernelCacheRegistryConfig{Endpoint: "registry.example"}}
	require.NoError(t, mutator.injectMCVSidecar(context.Background(), pod, cfg))
	require.Len(t, pod.Spec.Containers, 2)
	require.Equal(t, reporter.SecretName(capture.Name), pod.Annotations[reporter.AccessSecretAnnotation])
}

func TestSkipSidecarForTerminalCapture(t *testing.T) {
	for _, test := range []struct {
		name            string
		phase           v1alpha1.KernelCacheCapturePhase
		reason          string
		conditionStatus metav1.ConditionStatus
		generated       bool
		hasArtifact     bool
		wantSkip        bool
	}{
		{name: "complete without ready cache", phase: v1alpha1.KernelCacheCapturePhaseComplete, wantSkip: true},
		{name: "unchanged without ready cache", phase: v1alpha1.KernelCacheCapturePhaseUnchanged, wantSkip: true},
		{name: "failed without retry reason", phase: v1alpha1.KernelCacheCapturePhaseFailed, wantSkip: true},
		{name: "producer gone user capture", phase: v1alpha1.KernelCacheCapturePhaseFailed, reason: "ProducerGone", wantSkip: true},
		{name: "producer gone with artifact", phase: v1alpha1.KernelCacheCapturePhaseFailed, reason: "ProducerGone", generated: true, hasArtifact: true, wantSkip: true},
		{name: "producer gone generated capture", phase: v1alpha1.KernelCacheCapturePhaseFailed, reason: "ProducerGone", generated: true, wantSkip: false},
		{name: "producer gone with ready condition", phase: v1alpha1.KernelCacheCapturePhaseFailed, reason: "ProducerGone", conditionStatus: metav1.ConditionTrue, generated: true, wantSkip: true},
		{name: "producer gone with unknown condition", phase: v1alpha1.KernelCacheCapturePhaseFailed, reason: "ProducerGone", conditionStatus: metav1.ConditionUnknown, generated: true, wantSkip: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &v1alpha1.KernelCacheCapture{
				Status: v1alpha1.KernelCacheCaptureStatus{Phase: test.phase},
			}
			if test.generated {
				capture.Labels = map[string]string{constants.KernelCacheCaptureGeneratedLabelKey: "true"}
			}
			if test.hasArtifact {
				capture.Status.Artifact = &v1alpha1.KernelCacheArtifact{}
			}
			if test.reason != "" {
				conditionStatus := metav1.ConditionFalse
				if test.conditionStatus != "" {
					conditionStatus = test.conditionStatus
				}
				capture.Status.Conditions = []metav1.Condition{{
					Type:   "Ready",
					Status: conditionStatus,
					Reason: test.reason,
				}}
			}
			require.Equal(t, test.wantSkip, skipSidecarForTerminalCapture(capture))
		})
	}
	for _, phase := range []v1alpha1.KernelCacheCapturePhase{
		"",
		v1alpha1.KernelCacheCapturePhasePending,
		v1alpha1.KernelCacheCapturePhaseWaitingForWorkload,
		v1alpha1.KernelCacheCapturePhaseCapturing,
		v1alpha1.KernelCacheCapturePhasePushing,
	} {
		t.Run("non-terminal-"+string(phase), func(t *testing.T) {
			require.False(t, skipSidecarForTerminalCapture(&v1alpha1.KernelCacheCapture{
				Status: v1alpha1.KernelCacheCaptureStatus{Phase: phase},
			}))
		})
	}
}

func TestSidecarManifestsAndExistingMount(t *testing.T) {
	cfg := &kernelcacheconfig.KernelCacheConfig{
		MCVImage: "example/mcv:test", TargetImage: "registry.example/cache:capture-id",
		CachePaths:         []v1alpha1.KernelCachePath{{ContainerName: "main", ContainerPath: "/cache", OCIPath: "io.vllm.cache"}},
		ReporterSecretName: reporter.SecretName("capture"),
		CaptureName:        "capture",
		CaptureNamespace:   "team",
		CaptureSessionID:   "session-id",
	}
	manifests, err := getSidecarManifestsWithConfigs(cfg, captureconfig.ReadinessConfig{URL: "http://127.0.0.1:8080/", MCVCaptureReadinessTimeoutSeconds: 600}, captureconfig.RuntimeInfo{})
	require.NoError(t, err)
	require.Len(t, manifests.Containers, 1)
	require.Len(t, manifests.Volumes, 2)
	var captureConfig captureconfig.CaptureConfig
	require.NoError(t, json.Unmarshal([]byte(containerEnvValue(manifests.Containers[0], captureconfig.CaptureConfigEnv)), &captureConfig))
	require.Equal(t, cfg.TargetImage, captureConfig.TargetImage)
	var readinessConfig captureconfig.ReadinessConfig
	require.NoError(t, json.Unmarshal([]byte(containerEnvValue(manifests.Containers[0], captureconfig.ReadinessConfigEnv)), &readinessConfig))
	require.Equal(t, "http://127.0.0.1:8080/", readinessConfig.URL)
	require.Empty(t, manifests.Containers[0].Command)
	require.Equal(t, "true", containerEnvValue(manifests.Containers[0], captureconfig.CaptureModeEnv))

	podSpec := corev1.PodSpec{
		Volumes:    []corev1.Volume{{Name: "existing", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		Containers: []corev1.Container{{Name: "main", VolumeMounts: []corev1.VolumeMount{{Name: "existing", MountPath: "/cache", SubPath: "vllm"}}}},
	}
	require.NoError(t, addCacheMount(&podSpec, &manifests.Containers[0], 0, cfg.CachePaths[0], manifests.Volumes[0]))
	require.Len(t, podSpec.Volumes, 1)
	require.Equal(t, corev1.VolumeMount{Name: "existing", MountPath: "/workspace/cache/0", SubPath: "vllm"}, manifests.Containers[0].VolumeMounts[0])
}

// A namespace with a ResourceQuota refuses Pods with a container that sets no requests or limits.
func TestSidecarManifestsSetResources(t *testing.T) {
	custom := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("2Gi")},
	}
	for name, test := range map[string]struct {
		configured *corev1.ResourceRequirements
		want       corev1.ResourceRequirements
	}{
		"default":    {want: kernelcacheconfig.DefaultMCVResources()},
		"configured": {configured: &custom, want: custom},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &kernelcacheconfig.KernelCacheConfig{
				MCVImage: "example/mcv:test", TargetImage: "registry.example/cache:capture-id",
				CachePaths:         []v1alpha1.KernelCachePath{{ContainerName: "main", ContainerPath: "/cache", OCIPath: "io.vllm.cache"}},
				ReporterSecretName: reporter.SecretName("capture"),
				CaptureName:        "capture",
				CaptureNamespace:   "team",
				CaptureSessionID:   "session-id",
				MCVResources:       test.configured,
			}
			manifests, err := getSidecarManifestsWithConfigs(cfg, captureconfig.ReadinessConfig{URL: "http://127.0.0.1:8080/", MCVCaptureReadinessTimeoutSeconds: 600}, captureconfig.RuntimeInfo{})
			require.NoError(t, err)
			require.Equal(t, test.want, manifests.Containers[0].Resources)
		})
	}
}

// The capture watches the mounted path, so vLLM must write there whatever HOME is.
func TestAddCacheMountPinsVLLMCacheRoot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       []corev1.EnvVar
		cachePath v1alpha1.KernelCachePath
		wantEnv   []corev1.EnvVar
	}{
		{
			name:      "sets the default path",
			cachePath: v1alpha1.KernelCachePath{ContainerName: "main", ContainerPath: "/var/cache/vllm", OCIPath: "io.vllm.cache"},
			wantEnv:   []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/var/cache/vllm"}},
		},
		{
			name:      "keeps the runtime's own value",
			env:       []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"}},
			cachePath: v1alpha1.KernelCachePath{ContainerName: "main", ContainerPath: "/cache/vllm", OCIPath: "io.vllm.cache"},
			wantEnv:   []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"}},
		},
		{
			name:      "leaves a Triton cache alone",
			cachePath: v1alpha1.KernelCachePath{ContainerName: "main", ContainerPath: "/cache/triton", OCIPath: "io.triton.cache"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &kernelcacheconfig.KernelCacheConfig{
				MCVImage: "example/mcv:test", TargetImage: "registry.example/cache:capture-id",
				CachePaths:         []v1alpha1.KernelCachePath{tc.cachePath},
				ReporterSecretName: reporter.SecretName("capture"),
				CaptureName:        "capture",
				CaptureNamespace:   "team",
				CaptureSessionID:   "session-id",
			}
			manifests, err := getSidecarManifestsWithConfigs(cfg, captureconfig.ReadinessConfig{URL: "http://127.0.0.1:8080/", MCVCaptureReadinessTimeoutSeconds: 600}, captureconfig.RuntimeInfo{})
			require.NoError(t, err)
			podSpec := corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Env: tc.env}}}

			require.NoError(t, addCacheMount(&podSpec, &manifests.Containers[0], 0, tc.cachePath, manifests.Volumes[0]))
			require.Equal(t, tc.wantEnv, podSpec.Containers[0].Env)
		})
	}
}

func TestDefaultCachePaths(t *testing.T) {
	for _, tc := range []struct {
		name         string
		container    corev1.Container
		wantVLLMPath string
	}{
		{
			name: "container uses environment values",
			container: corev1.Container{
				Name: "vllm",
				Env: []corev1.EnvVar{
					{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"},
					{Name: "TRITON_CACHE_DIR", Value: "/cache/triton"},
				},
			},
			wantVLLMPath: "/cache/vllm",
		},
		{
			name: "container uses fallbacks",
			container: corev1.Container{
				Name: "server",
			},
			wantVLLMPath: "/var/cache/vllm",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, err := defaultCachePaths(&tc.container)
			require.NoError(t, err)
			require.Equal(t, []v1alpha1.KernelCachePath{
				{ContainerName: tc.container.Name, ContainerPath: tc.wantVLLMPath, OCIPath: "io.vllm.cache"},
			}, paths)
		})
	}
}
