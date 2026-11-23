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
	"errors"

	corev1 "k8s.io/api/core/v1"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	kernelcacheutil "github.com/talt5/localmodelcache/pkg/kernelcache"
	cacheidentity "github.com/talt5/localmodelcache/pkg/kernelcache/identity"
)

type workloadRef struct {
	Kind string
	Name string
}

// identityForPod builds the KernelCache identity from the Pod workload, runtime container, and model URI.
func identityForPod(pod *corev1.Pod, modelURI string) (v1alpha1.KernelCacheIdentity, error) {
	workload, err := workloadRefForPod(pod)
	if err != nil {
		return v1alpha1.KernelCacheIdentity{}, err
	}

	container, err := runtimeContainerForPod(pod)
	if err != nil {
		return v1alpha1.KernelCacheIdentity{}, err
	}

	identityFactors := buildRuntimeIdentityFactors(container, modelURI)
	identityFactors.Namespace = pod.Namespace
	identityFactors.WorkloadKind = workload.Kind
	identityFactors.WorkloadName = workload.Name
	return cacheidentity.Build(identityFactors)
}

// workloadRefForPod reads the Deployment that the webhook resolved for the Pod and recorded in
// the kernelcache-workload label.
func workloadRefForPod(pod *corev1.Pod) (workloadRef, error) {
	if name := pod.Labels[constants.KernelCacheWorkloadLabelKey]; name != "" {
		return workloadRef{Kind: constants.KernelCacheWorkloadKind, Name: name}, nil
	}
	return workloadRef{}, errors.New("workload identity label is required")
}

// resolveRuntimeContainerName resolves the requested container, or else the container the
// kernelcache-container annotation names, or else the Pod's only runtime container.
func resolveRuntimeContainerName(pod *corev1.Pod, requested string) (string, error) {
	if requested == "" {
		requested = pod.Annotations[constants.KernelCacheContainerAnnotationKey]
	}
	return kernelcacheutil.ResolveRuntimeContainerName(pod.Spec.Containers, requested)
}

// runtimeContainerForPod finds the Pod's runtime container.
func runtimeContainerForPod(pod *corev1.Pod) (*corev1.Container, error) {
	name, err := resolveRuntimeContainerName(pod, "")
	if err != nil {
		return nil, err
	}
	return &pod.Spec.Containers[findContainerIndex(pod.Spec.Containers, name)], nil
}

// buildRuntimeIdentityFactors builds runtime identity factors from a container and model URI.
func buildRuntimeIdentityFactors(container *corev1.Container, modelURI string) cacheidentity.KernelCacheIdentityFactors {
	runtimeConfigFactors := cacheidentity.ExtractRuntimeConfigFactors(container)
	identityFactors := cacheidentity.KernelCacheIdentityFactors{
		RuntimeImage:         container.Image,
		ModelURIHash:         cacheidentity.ModelURIHash(modelURI),
		RuntimeConfigFactors: runtimeConfigFactors,
	}
	if len(container.Command) > 0 {
		identityFactors.CommandHash = cacheidentity.HashStrings(container.Command)
	}
	if len(container.Args) > 0 {
		identityFactors.ArgsHash = cacheidentity.HashStrings(container.Args)
	}
	return identityFactors
}
