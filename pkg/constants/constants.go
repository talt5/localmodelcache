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

// LocalModelCache Constants
const (
	APIGroupName = "local.cache.ai"
)

var SystemNamespace = GetEnvOrDefault("POD_NAMESPACE", "localmodelcache")

// LocalModelConfigMapName is the ConfigMap, in SystemNamespace, that configures the
// local model cache and kernel cache controllers and their node agents.
var LocalModelConfigMapName = "localmodelcache-config"

// Kernel cache annotations and labels
var (
	KernelCacheSupportedAnnotationKey        = APIGroupName + "/kernelcache-supported"
	KernelCacheSidecarInjectionAnnotationKey = APIGroupName + "/kernelcache-sidecar-injection"
	KernelCacheNodeGroupAnnotationKey        = APIGroupName + "/kernelcache-nodegroup"
	// KernelCacheModelURIAnnotationKey names the model a kernel-cache Pod serves. It is part of
	// the cache identity, so Pods that serve another model never share a cache.
	KernelCacheModelURIAnnotationKey = APIGroupName + "/kernelcache-model-uri"
	// KernelCacheContainerAnnotationKey names the runtime container of a kernel-cache Pod. It is
	// required when the Pod has more than one container.
	KernelCacheContainerAnnotationKey = APIGroupName + "/kernelcache-container"
	// KernelCacheWorkloadLabelKey is set by the kernel cache webhook to the name of the
	// Deployment that owns an opted-in Pod. Controllers map Pods to their workload through it.
	KernelCacheWorkloadLabelKey = APIGroupName + "/kernelcache-workload"
)

// KernelCacheWorkloadKind is the kind of workload kernel caches are captured from and mounted
// into: Pods owned by a Deployment through its ReplicaSet.
const KernelCacheWorkloadKind = "Deployment"

// Namespaces with this label get the claims of every LocalModelCache without a namespaceSelector.
const (
	ModelsNamespaceLabelKey   = APIGroupName + "/models"
	ModelsNamespaceLabelValue = "enabled"
)

// Internal annotations
var (
	InternalAnnotationsPrefix = "internal." + APIGroupName
	// LocalModelUIDLabelKey holds the UID of the cache a PersistentVolume or claim belongs to.
	LocalModelUIDLabelKey = InternalAnnotationsPrefix + "/localmodel-uid"
	// LocalModelAnnotationKey and LocalModelNamespaceAnnotationKey name that cache. Its namespace
	// is empty for a cluster-scoped LocalModelCache.
	LocalModelAnnotationKey                          = InternalAnnotationsPrefix + "/localmodel"
	LocalModelNamespaceAnnotationKey                 = InternalAnnotationsPrefix + "/localmodel-namespace"
	KernelCacheUsageAnnotationKey                    = InternalAnnotationsPrefix + "/kernelcache-usage"
	KernelCacheCaptureGeneratedLabelKey              = InternalAnnotationsPrefix + "/kernelcache-capture-generated"
	KernelCacheNodeGroupSelectionSourceAnnotationKey = InternalAnnotationsPrefix + "/kernelcache-nodegroup-selection-source"
)

// StorageSpec Constants
var (
	DefaultStorageSpecSecret = "storage-config"
)

const (
	OciURIPrefix = "oci://"

	PvcSourceMountName = "localmodelcache-pvc-source"
)

// GPU Constants
const (
	NvidiaGPUResourceType          = "nvidia.com/gpu"
	NvidiaMigGPUResourceTypePrefix = "nvidia.com/mig"
	AmdGPUResourceType             = "amd.com/gpu"
	IntelGPUResourceType           = "intel.com/gpu"
	GaudiGPUResourceType           = "habana.ai/gaudi"
)

var DefaultGPUResourceTypeList = []string{
	NvidiaGPUResourceType,
	AmdGPUResourceType,
	IntelGPUResourceType,
	GaudiGPUResourceType,
}

// DefaultModelLocalMountPath is where models will be mounted by the storage-initializer
const DefaultModelLocalMountPath = "/mnt/models"

// DefaultCaBundleVolumeMountPath Default path to mount CA bundle configmap volume
const DefaultCaBundleVolumeMountPath = "/etc/ssl/custom-certs"

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
func KernelCacheTargetImage(registry, namespace, workloadName, captureID string) string {
	return fmt.Sprintf("%s/%s/kernel-cache-%s:%s", strings.TrimSuffix(registry, "/"), namespace, workloadName, captureID)
}
