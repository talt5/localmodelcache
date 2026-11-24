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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	kernelcacheconfig "github.com/talt5/localmodelcache/pkg/kernelcache/config"
)

// podMutatorFixture is a vLLM Deployment, its ReplicaSet and a Pod about to be created from it.
type podMutatorFixture struct {
	deployment *appsv1.Deployment
	replicaSet *appsv1.ReplicaSet
	pod        *corev1.Pod
}

func newPodMutatorFixture() *podMutatorFixture {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "vllm", Namespace: "team", UID: types.UID("deployment-uid"),
	}}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "vllm-669889f77f", Namespace: "team", UID: types.UID("replicaset-uid"),
		Labels: map[string]string{appsv1.DefaultDeploymentUniqueLabelKey: "669889f77f"},
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
			deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"),
		)},
	}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "vllm-669889f77f-",
			Labels:       map[string]string{appsv1.DefaultDeploymentUniqueLabelKey: "669889f77f"},
			Annotations: map[string]string{
				constants.KernelCacheSupportedAnnotationKey:        "true",
				constants.KernelCacheModelURIAnnotationKey:         "hf://Qwen/Qwen3-0.6B",
				constants.KernelCacheSidecarInjectionAnnotationKey: "false",
			},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
				replicaSet, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"),
			)},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "vllm", Image: "vllm/vllm-openai:v0.10.0"}}},
	}
	return &podMutatorFixture{deployment: deployment, replicaSet: replicaSet, pod: pod}
}

func (f *podMutatorFixture) mutator(t *testing.T, kernelCacheJSON string) *PodMutator {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: constants.LocalModelConfigMapName, Namespace: constants.SystemNamespace},
		Data:       map[string]string{kernelcacheconfig.KernelCacheConfigKeyName: kernelCacheJSON},
	}
	return &PodMutator{
		Client:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(f.deployment, f.replicaSet).Build(),
		Clientset: k8sfake.NewSimpleClientset(configMap),
		Decoder:   admission.NewDecoder(scheme),
	}
}

func podCreateRequest(t *testing.T, pod *corev1.Pod) admission.Request {
	t.Helper()
	raw, err := json.Marshal(pod)
	require.NoError(t, err)
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		UID:       types.UID("1"),
		Namespace: "team",
		Operation: admissionv1.Create,
		Object:    runtime.RawExtension{Raw: raw},
	}}
}

// patchedLabel returns the value the response's JSON patch sets for the workload label, or "".
func patchedLabel(response admission.Response) string {
	for _, op := range response.Patches {
		if op.Path == "/metadata/labels/local.cache.ai~1kernelcache-workload" {
			value, _ := op.Value.(string)
			return value
		}
		if op.Path == "/metadata/labels" {
			if labels, ok := op.Value.(map[string]any); ok {
				value, _ := labels[constants.KernelCacheWorkloadLabelKey].(string)
				return value
			}
		}
	}
	return ""
}

const enabledKernelCache = `{"enabled": true, "artifactSecurity": {"mode": "none"}}`

func TestPodMutatorLabelsOptedInDeploymentPods(t *testing.T) {
	fixture := newPodMutatorFixture()
	response := fixture.mutator(t, enabledKernelCache).Handle(t.Context(), podCreateRequest(t, fixture.pod))
	require.True(t, response.Allowed, response.Result)
	require.Empty(t, response.Warnings)
	require.Equal(t, "vllm", patchedLabel(response), "the Pod should carry its Deployment's name")
}

func TestPodMutatorIgnoresPodsWithoutTheOptIn(t *testing.T) {
	fixture := newPodMutatorFixture()
	delete(fixture.pod.Annotations, constants.KernelCacheSupportedAnnotationKey)
	response := fixture.mutator(t, enabledKernelCache).Handle(t.Context(), podCreateRequest(t, fixture.pod))
	require.True(t, response.Allowed)
	require.Empty(t, response.Patches)
}

func TestPodMutatorIgnoresPodsWhenKernelCacheIsDisabled(t *testing.T) {
	fixture := newPodMutatorFixture()
	response := fixture.mutator(t, `{"enabled": false, "artifactSecurity": {"mode": "none"}}`).Handle(t.Context(), podCreateRequest(t, fixture.pod))
	require.True(t, response.Allowed)
	require.Empty(t, response.Patches)
}

func TestPodMutatorWarnsForPodsWithoutADeployment(t *testing.T) {
	fixture := newPodMutatorFixture()
	fixture.pod.OwnerReferences = nil
	response := fixture.mutator(t, enabledKernelCache).Handle(t.Context(), podCreateRequest(t, fixture.pod))
	require.True(t, response.Allowed)
	require.Empty(t, response.Patches)
	require.Len(t, response.Warnings, 1)
	require.Contains(t, response.Warnings[0], "not owned by a Deployment")
}

func TestPodMutatorWarnsWhenTheRuntimeContainerIsAmbiguous(t *testing.T) {
	fixture := newPodMutatorFixture()
	fixture.pod.Spec.Containers = append(fixture.pod.Spec.Containers, corev1.Container{Name: "proxy", Image: "proxy:latest"})
	response := fixture.mutator(t, enabledKernelCache).Handle(t.Context(), podCreateRequest(t, fixture.pod))
	require.True(t, response.Allowed)
	require.Empty(t, response.Patches)
	require.Len(t, response.Warnings, 1)
	require.Contains(t, response.Warnings[0], constants.KernelCacheContainerAnnotationKey)

	// Naming the runtime container resolves it.
	fixture.pod.Annotations[constants.KernelCacheContainerAnnotationKey] = "vllm"
	response = fixture.mutator(t, enabledKernelCache).Handle(t.Context(), podCreateRequest(t, fixture.pod))
	require.True(t, response.Allowed, response.Result)
	require.Equal(t, "vllm", patchedLabel(response))
}

func TestPodMutatorAdmitsPodsItCannotCapture(t *testing.T) {
	probe := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt32(8000)}}}
	tests := map[string]struct {
		probe   *corev1.Probe
		warning string
	}{
		"no readiness probe": {warning: "readinessProbe"},
		"no registry":        {probe: probe, warning: "kernelcache.registry.endpoint"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newPodMutatorFixture()
			fixture.pod.Annotations[constants.KernelCacheSidecarInjectionAnnotationKey] = "true"
			fixture.pod.Spec.Containers[0].ReadinessProbe = tc.probe

			response := fixture.mutator(t, enabledKernelCache).Handle(t.Context(), podCreateRequest(t, fixture.pod))

			require.True(t, response.Allowed, response.Result)
			require.Len(t, response.Warnings, 1)
			require.Contains(t, response.Warnings[0], tc.warning)
			require.Equal(t, "vllm", patchedLabel(response))
			for _, op := range response.Patches {
				require.NotContains(t, op.Path, "/spec", "the Pod spec must not be changed")
			}
		})
	}
}

// mountableFixture has a ready KernelCache that matches its Pod.
func mountableFixture(t *testing.T) (*podMutatorFixture, *PodMutator) {
	t.Helper()
	fixture := newPodMutatorFixture()
	fixture.pod.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "VLLM_CACHE_ROOT", Value: "/cache/vllm"}}
	identified := fixture.pod.DeepCopy()
	identified.Namespace = "team"
	identified.Labels[constants.KernelCacheWorkloadLabelKey] = "vllm"
	identity, err := identityForPod(identified, "hf://Qwen/Qwen3-0.6B")
	require.NoError(t, err)
	cache := readyCache("captured", identity)
	cache.Spec.Artifact.CachePaths[0].ContainerName = ""
	cache.Spec.Artifact.CachePaths[0].ContainerPath = ""
	node := kernelCacheNode("node-a", cache, v1alpha1.KernelCacheNodePreparationStateReady)
	mutator := fixture.mutator(t, enabledKernelCache)
	require.NoError(t, mutator.Client.Create(t.Context(), &cache))
	require.NoError(t, mutator.Client.Create(t.Context(), &node))
	return fixture, mutator
}

func patchesSpec(response admission.Response) bool {
	for _, op := range response.Patches {
		if strings.HasPrefix(op.Path, "/spec") {
			return true
		}
	}
	return false
}

func TestPodMutatorMountsAMatchingCache(t *testing.T) {
	fixture, mutator := mountableFixture(t)
	response := mutator.Handle(t.Context(), podCreateRequest(t, fixture.pod))
	require.True(t, response.Allowed, response.Result)
	require.Empty(t, response.Warnings)
	require.True(t, patchesSpec(response))
}

// The kernel cache only saves time, so a Pod that cannot use it starts without it.
func TestPodMutatorAdmitsPodsThatCannotMountTheCache(t *testing.T) {
	tests := map[string]func(*corev1.PodSpec){
		"a volume at a parent of the cache path": func(spec *corev1.PodSpec) {
			spec.Volumes = []corev1.Volume{{Name: "home"}}
			spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "home", MountPath: "/cache"}}
		},
		"a volume inside the cache path": func(spec *corev1.PodSpec) {
			spec.Volumes = []corev1.Volume{{Name: "inner"}}
			spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "inner", MountPath: "/cache/vllm/inner"}}
		},
		"a read-only volume at the cache path": func(spec *corev1.PodSpec) {
			spec.Volumes = []corev1.Volume{{Name: "fixed"}}
			spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "fixed", MountPath: "/cache/vllm", ReadOnly: true}}
		},
		"a volume with the name the webhook uses": func(spec *corev1.PodSpec) {
			spec.Volumes = []corev1.Volume{{Name: kernelCacheSourceVolumeName}}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			fixture, mutator := mountableFixture(t)
			change(&fixture.pod.Spec)
			response := mutator.Handle(t.Context(), podCreateRequest(t, fixture.pod))
			require.True(t, response.Allowed, response.Result)
			require.False(t, patchesSpec(response), "the Pod spec must not be changed")
			require.Equal(t, "vllm", patchedLabel(response))
		})
	}
}

func TestPodMutatorAdmitsPodsWhenItCannotDecide(t *testing.T) {
	t.Run("invalid settings", func(t *testing.T) {
		fixture := newPodMutatorFixture()
		response := fixture.mutator(t, `{"enabled": true, "defaultMountType": "nope"}`).Handle(t.Context(), podCreateRequest(t, fixture.pod))
		require.True(t, response.Allowed, response.Result)
		require.Len(t, response.Warnings, 1)
		require.Contains(t, response.Warnings[0], "defaultMountType")
	})
	t.Run("the API cannot be read", func(t *testing.T) {
		fixture, mutator := mountableFixture(t)
		mutator.Reader = interceptor.NewClient(mutator.Client.(client.WithWatch), interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return errors.New("timeout")
			},
		})
		response := mutator.Handle(t.Context(), podCreateRequest(t, fixture.pod))
		require.True(t, response.Allowed, response.Result)
		require.Len(t, response.Warnings, 1)
		require.Contains(t, response.Warnings[0], "timeout")
		require.Equal(t, "vllm", patchedLabel(response))
	})
}
