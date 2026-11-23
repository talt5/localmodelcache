/*
Copyright 2021 The KServe Authors.

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

package constants

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

// KServe Constants
const (
	KServeAPIGroupName = "serving.kserve.io"
)

var KServeNamespace = GetEnvOrDefault("POD_NAMESPACE", "kserve")

var (
	InferenceServiceConfigMapName = "inferenceservice-config"
	// LocalModelConfigMapName is the ConfigMap, in KServeNamespace, that configures the
	// local model cache and kernel cache controllers and their node agents.
	LocalModelConfigMapName = "kserve-localmodel-config"
)

// InferenceService Annotations
var (
	KernelCacheSupportedAnnotationKey        = KServeAPIGroupName + "/kernelcache-supported"
	KernelCacheSidecarInjectionAnnotationKey = KServeAPIGroupName + "/kernelcache-sidecar-injection"
	KernelCacheNodeGroupAnnotationKey        = KServeAPIGroupName + "/kernelcache-nodegroup"
	// KernelCacheModelURIAnnotationKey names the model a kernel-cache Pod serves. It is part of
	// the cache identity, so Pods that serve another model never share a cache.
	KernelCacheModelURIAnnotationKey = KServeAPIGroupName + "/kernelcache-model-uri"
	// KernelCacheContainerAnnotationKey names the runtime container of a kernel-cache Pod. It is
	// required when the Pod has more than one container.
	KernelCacheContainerAnnotationKey = KServeAPIGroupName + "/kernelcache-container"
	// KernelCacheWorkloadLabelKey is set by the kernel cache webhook to the name of the
	// Deployment that owns an opted-in Pod. Controllers map Pods to their workload through it.
	KernelCacheWorkloadLabelKey = KServeAPIGroupName + "/kernelcache-workload"
)

// KernelCacheWorkloadKind is the kind of workload kernel caches are captured from and mounted
// into: Pods owned by a Deployment through its ReplicaSet.
const KernelCacheWorkloadKind = "Deployment"

// InferenceService Internal Annotations
var (
	InferenceServiceInternalAnnotationsPrefix        = "internal." + KServeAPIGroupName
	LocalModelLabel                                  = InferenceServiceInternalAnnotationsPrefix + "/localmodel"
	LocalModelNamespaceLabel                         = InferenceServiceInternalAnnotationsPrefix + "/localmodel-namespace"
	KernelCacheUsageAnnotationKey                    = InferenceServiceInternalAnnotationsPrefix + "/kernelcache-usage"
	KernelCacheCaptureGeneratedLabelKey              = InferenceServiceInternalAnnotationsPrefix + "/kernelcache-capture-generated"
	KernelCacheNodeGroupSelectionSourceAnnotationKey = InferenceServiceInternalAnnotationsPrefix + "/kernelcache-nodegroup-selection-source"
	LocalModelLoRAAnnotationKey                      = InferenceServiceInternalAnnotationsPrefix + "/localmodel-lora"
)

// StorageSpec Constants
var (
	DefaultStorageSpecSecret = "storage-config"
)

const (
	OciURIPrefix = "oci://"

	PvcSourceMountName           = "kserve-pvc-source"
	StorageInitializerVolumeName = "kserve-provision-location"

	CpuModelcarDefault    = "10m"
	MemoryModelcarDefault = "15Mi"
)

// GPU Constants
const (
	NvidiaGPUResourceType          = "nvidia.com/gpu"
	NvidiaMigGPUResourceTypePrefix = "nvidia.com/mig"
	AmdGPUResourceType             = "amd.com/gpu"
	IntelGPUResourceType           = "intel.com/gpu"
	GaudiGPUResourceType           = "habana.ai/gaudi"
)

var CustomGPUResourceTypesAnnotationKey = KServeAPIGroupName + "/gpu-resource-types"

var DefaultGPUResourceTypeList = []string{
	NvidiaGPUResourceType,
	AmdGPUResourceType,
	IntelGPUResourceType,
	GaudiGPUResourceType,
}

// InferenceService Environment Variables
const (
	ModelInitModeEnvVarKey = "MODEL_INIT_MODE"
)

// InferenceService model server args
const (
	ArgumentModelName = "--model_name"
)

// InferenceService container names
const (
	InferenceServiceContainerName = "kserve-container"

	// TransformerContainerName transformer container name in collocation
	TransformerContainerName = "transformer-container"

	ModelcarContainerName     = "modelcar"
	ModelcarInitContainerName = "modelcar-init"
)

// DefaultModelLocalMountPath is where models will be mounted by the storage-initializer
const DefaultModelLocalMountPath = "/mnt/models"

// DefaultCaBundleVolumeMountPath Default path to mount CA bundle configmap volume
const DefaultCaBundleVolumeMountPath = "/etc/ssl/custom-certs"

// DefaultGlobalCaBundleConfigMapName Default CA bundle configmap name that will be created in the user namespace.
const DefaultGlobalCaBundleConfigMapName = "global-ca-bundle"

// Custom CA bundle configmap Environment Variables
const (
	CaBundleConfigMapNameEnvVarKey   = "CA_BUNDLE_CONFIGMAP_NAME"
	CaBundleVolumeMountPathEnvVarKey = "CA_BUNDLE_VOLUME_MOUNT_POINT"
)

func GetEnvOrDefault(key string, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// KernelCacheCaptureRevisionName returns the deterministic generated capture
// name for a workload revision. The revision identifier is retained in full so
// Pods from the same ReplicaSet resolve the same KCC. Long source names are
// shortened with a digest to keep the result within the Kubernetes name limit.
func KernelCacheCaptureRevisionName(sourceName, revisionID string) string {
	const marker = "-kcc-"
	const maxNameLength = 63

	if sourceName == "" || revisionID == "" {
		return ""
	}
	suffix := marker + revisionID
	available := maxNameLength - len(suffix)
	if available <= 0 {
		return ""
	}
	if len(sourceName) <= available {
		return sourceName + suffix
	}

	digest := sha256.Sum256([]byte(sourceName))
	digestText := fmt.Sprintf("-%x", digest[:6])
	keep := available - len(digestText)
	if keep < 1 {
		return ""
	}
	return strings.TrimRight(sourceName[:keep], "-.") + digestText + suffix
}

// KernelCacheTargetImage returns the OCI image reference for one capture.
func KernelCacheTargetImage(registry, namespace, inferenceServiceName, captureID string) string {
	return fmt.Sprintf("%s/%s/kernel-cache-%s:%s", strings.TrimSuffix(registry, "/"), namespace, inferenceServiceName, captureID)
}
