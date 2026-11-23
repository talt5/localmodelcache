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

package localmodelcache

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/types"
)

const (
	// LocalModelConfigKeyName is the ConfigMap key holding LocalModelConfig as JSON.
	LocalModelConfigKeyName = "localModel"
	// StorageInitializerConfigKeyName is the ConfigMap key holding the download container
	// settings (types.StorageInitializerConfig) as JSON.
	StorageInitializerConfigKeyName = "storageInitializer"
)

// LocalModelConfig configures the local model cache controller and node agent.
type LocalModelConfig struct {
	JobNamespace    string `json:"jobNamespace"`
	DefaultJobImage string `json:"defaultJobImage,omitempty"`
	FSGroup         *int64 `json:"fsGroup,omitempty"`
	// SharedPVCImportFSGroup is applied to shared-PVC import Jobs, which run in the cache's
	// own namespace. It is independent of FSGroup, which applies only to per-node download
	// Jobs in JobNamespace. Leave it unset to let namespace admission assign the group.
	SharedPVCImportFSGroup       *int64 `json:"sharedPVCImportFSGroup,omitempty"`
	JobTTLSecondsAfterFinished   *int32 `json:"jobTTLSecondsAfterFinished,omitempty"`
	ReconcilationFrequencyInSecs *int64 `json:"reconcilationFrequencyInSecs,omitempty"`
	DisableVolumeManagement      bool   `json:"disableVolumeManagement,omitempty"`
	// Tolerations of the per-node download Jobs. A Job can only download to a tainted node that
	// it tolerates.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// GetConfigMap returns the local model ConfigMap (constants.LocalModelConfigMapName) from
// constants.SystemNamespace.
func GetConfigMap(ctx context.Context, clientset kubernetes.Interface) (*corev1.ConfigMap, error) {
	return clientset.CoreV1().ConfigMaps(constants.SystemNamespace).Get(
		ctx, constants.LocalModelConfigMapName, metav1.GetOptions{})
}

// NewLocalModelConfig parses the localModel key. A missing key yields the zero config.
func NewLocalModelConfig(configMap *corev1.ConfigMap) (*LocalModelConfig, error) {
	localModelConfig := &LocalModelConfig{}
	if localModel, ok := configMap.Data[LocalModelConfigKeyName]; ok {
		if err := json.Unmarshal([]byte(localModel), localModelConfig); err != nil {
			return nil, fmt.Errorf("unable to parse %q config json: %w", LocalModelConfigKeyName, err)
		}
	}
	return localModelConfig, nil
}

// GetStorageInitializerConfigs parses the storageInitializer key, which configures the
// container that download and import Jobs run. CPU and memory values must be valid
// Kubernetes resource quantities, and ociModelMode, when set, must be a known mode.
func GetStorageInitializerConfigs(configMap *corev1.ConfigMap) (*types.StorageInitializerConfig, error) {
	storageInitializerConfig := &types.StorageInitializerConfig{}
	if initializerConfig, ok := configMap.Data[StorageInitializerConfigKeyName]; ok {
		if err := json.Unmarshal([]byte(initializerConfig), storageInitializerConfig); err != nil {
			return nil, fmt.Errorf("unable to parse %q config json: %w", StorageInitializerConfigKeyName, err)
		}
	}

	resourceDefaults := map[string]string{
		"memoryRequest": storageInitializerConfig.MemoryRequest,
		"memoryLimit":   storageInitializerConfig.MemoryLimit,
		"cpuRequest":    storageInitializerConfig.CpuRequest,
		"cpuLimit":      storageInitializerConfig.CpuLimit,
	}
	// The modelcar fields are optional; validate them only when set.
	if storageInitializerConfig.CpuModelcar != "" {
		resourceDefaults["cpuModelcar"] = storageInitializerConfig.CpuModelcar
	}
	if storageInitializerConfig.MemoryModelcar != "" {
		resourceDefaults["memoryModelcar"] = storageInitializerConfig.MemoryModelcar
	}
	for key, value := range resourceDefaults {
		if _, err := resource.ParseQuantity(value); err != nil {
			return storageInitializerConfig, fmt.Errorf("failed to parse resource configuration for %q.%q: %w", StorageInitializerConfigKeyName, key, err)
		}
	}

	if storageInitializerConfig.OciModelMode != "" {
		switch storageInitializerConfig.OciModelMode {
		case types.OciModelModeModelcar, types.OciModelModeNative, types.OciModelModeFetch:
		default:
			return nil, fmt.Errorf("invalid %q.ociModelMode %q: must be one of %q, %q, %q",
				StorageInitializerConfigKeyName,
				storageInitializerConfig.OciModelMode,
				types.OciModelModeModelcar, types.OciModelModeNative, types.OciModelModeFetch)
		}
	}
	return storageInitializerConfig, nil
}
