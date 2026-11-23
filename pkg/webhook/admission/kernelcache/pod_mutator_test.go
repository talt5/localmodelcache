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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
	kernelcacheconfig "github.com/kserve/kserve/pkg/kernelcache/config"
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
		ObjectMeta: metav1.ObjectMeta{Name: constants.LocalModelConfigMapName, Namespace: constants.KServeNamespace},
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
		if op.Path == "/metadata/labels/serving.kserve.io~1kernelcache-workload" {
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
