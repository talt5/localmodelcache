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
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/kserve/kserve/pkg/constants"
	kernelcacheconfig "github.com/kserve/kserve/pkg/kernelcache/config"
	"github.com/kserve/kserve/pkg/kernelcache/workload"
)

// +kubebuilder:webhook:path=/mutate-kernelcache-pods,mutating=true,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,name=kernelcache.kserve-webhook-server.pod-mutator,reinvocationPolicy=IfNeeded

// PodMutator is the KernelCache Pod webhook entry point.
type PodMutator struct {
	Client    client.Client
	Reader    client.Reader
	Clientset kubernetes.Interface
	Decoder   admission.Decoder
}

var logger = logf.Log.WithName("kernelcache-pod-mutator")

func (m *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	if err := m.Decoder.Decode(req, pod); err != nil {
		logger.Error(err, "Failed to decode Pod")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Pods opt in through their template with the kernelcache-supported annotation.
	if pod.Annotations[constants.KernelCacheSupportedAnnotationKey] != "true" {
		return admission.ValidationResponse(true, "")
	}
	logger.Info("Mutating Pod for KernelCache", "pod", pod.GenerateName, "namespace", req.Namespace)

	// Load kernelcache configuration
	pod.Namespace = req.Namespace
	kernelCacheConfig, err := m.getKernelCacheConfig(ctx)
	if err != nil {
		logger.Error(err, "Failed to load kernelcache configuration")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if !kernelCacheConfig.Enabled {
		return admission.ValidationResponse(true, "")
	}

	// Kernel caches are captured per Deployment revision, so the Pod must belong to a
	// Deployment. Record it on the Pod for the controllers that map Pods to workloads.
	deployment, err := workload.ResolveDeployment(ctx, m.reader(), pod)
	if err != nil {
		logger.Error(err, "Failed to resolve the Pod's Deployment")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if deployment == nil {
		return admission.Allowed("").WithWarnings("kernel cache skipped: the Pod is not owned by a Deployment")
	}
	if _, err := resolveRuntimeContainerName(pod, ""); err != nil {
		return admission.Allowed("").WithWarnings("kernel cache skipped: " + err.Error())
	}
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[constants.KernelCacheWorkloadLabelKey] = deployment.Name

	sidecarInjection := kernelCacheConfig.DefaultSidecarInjection
	if value, exists := pod.Annotations[constants.KernelCacheSidecarInjectionAnnotationKey]; exists {
		switch value {
		case "true":
			sidecarInjection = true
		case "false":
			sidecarInjection = false
		default:
			err := fmt.Errorf("%s must be true or false", constants.KernelCacheSidecarInjectionAnnotationKey)
			logger.Error(err, "Failed to parse sidecar injection annotation")
			return admission.Errored(http.StatusBadRequest, err)
		}
	}
	cacheMounted, err := m.injectKernelCacheArtifact(ctx, pod, kernelCacheConfig)
	if err != nil {
		logger.Error(err, "Failed to mount KernelCache")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if sidecarInjection && !cacheMounted {
		if err := m.injectMCVSidecar(ctx, pod, kernelCacheConfig); err != nil {
			logger.Error(err, "Failed to inject MCV sidecar")
			return admission.Errored(http.StatusInternalServerError, err)
		}
	}
	logger.Info("KernelCache mutation completed", "pod", pod.Name, "namespace", req.Namespace)

	patchedPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, patchedPod)
}

func (m *PodMutator) getKernelCacheConfig(ctx context.Context) (*kernelcacheconfig.KernelCacheConfig, error) {
	configMap, err := m.Clientset.CoreV1().ConfigMaps(constants.KServeNamespace).Get(ctx, constants.LocalModelConfigMapName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return kernelcacheconfig.NewKernelCacheConfig(configMap)
}

// reader returns the uncached reader for owner lookups, falling back to the client in tests.
func (m *PodMutator) reader() client.Reader {
	if m.Reader != nil {
		return m.Reader
	}
	return m.Client
}
