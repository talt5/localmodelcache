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

// Package jobs holds download-Job building blocks shared by the node-agent (per-node hostPath
// fan-out) and the manager (shared-PVC import) paths: download-container resolution and
// credential construction. Keeping these in one leaf package avoids duplicating the container
// and credential wiring across the two reconcilers.
package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/credentials"
	pkgtypes "github.com/talt5/localmodelcache/pkg/types"
)

const (
	// DownloadContainerName is the name of the model download container.
	DownloadContainerName = "localmodelcache-download"
	// DefaultJobImage is used when no StorageInitializerConfig image is set.
	DefaultJobImage = "ghcr.io/talt5/localmodelcache-storage-initializer:latest"
)

// LabelValue returns the name of an object as a label value. A name can have 253 characters and a
// label value only 63, so a longer name is cut and gets a hash to keep it unique.
func LabelValue(name string) string {
	if len(name) <= validation.LabelValueMaxLength {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(sum[:])[:16]
	return name[:validation.LabelValueMaxLength-len(hash)-1] + "-" + hash
}

// ResolveDownloadContainer returns the download container for a source URI. It prefers a matching
// enabled ClusterStorageContainer (workload type LocalModelDownloadJob) for backward
// compatibility, and falls back to the StorageInitializerConfig image otherwise.
func ResolveDownloadContainer(ctx context.Context, cl client.Client, config *pkgtypes.StorageInitializerConfig, sourceModelUri string) (*corev1.Container, error) {
	container, err := containerFromStorageContainers(ctx, cl, sourceModelUri)
	if err != nil {
		return nil, err
	}
	if container == nil {
		container = ContainerFromConfig(config)
	}
	return container, nil
}

// ContainerFromConfig returns the fallback download container built from the
// StorageInitializerConfig image and resources, or the default image when unset.
func ContainerFromConfig(config *pkgtypes.StorageInitializerConfig) *corev1.Container {
	image := DefaultJobImage
	if config != nil && config.Image != "" {
		image = config.Image
	}
	return &corev1.Container{
		Name:                     DownloadContainerName,
		Image:                    image,
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		Resources:                resourcesFromConfig(config),
	}
}

// resourcesFromConfig leaves out values that are unset or not valid quantities, so that a bad
// setting never stops downloads. GetStorageInitializerConfigs reports invalid values.
func resourcesFromConfig(config *pkgtypes.StorageInitializerConfig) corev1.ResourceRequirements {
	if config == nil {
		return corev1.ResourceRequirements{}
	}
	list := func(cpu, memory string) corev1.ResourceList {
		out := corev1.ResourceList{}
		for name, value := range map[corev1.ResourceName]string{corev1.ResourceCPU: cpu, corev1.ResourceMemory: memory} {
			if quantity, err := resource.ParseQuantity(value); err == nil {
				out[name] = quantity
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return corev1.ResourceRequirements{
		Requests: list(config.CpuRequest, config.MemoryRequest),
		Limits:   list(config.CpuLimit, config.MemoryLimit),
	}
}

// containerFromStorageContainers returns the container spec of the first enabled
// ClusterStorageContainer that supports the source URI for local-model downloads, or nil.
func containerFromStorageContainers(ctx context.Context, cl client.Client, sourceModelUri string) (*corev1.Container, error) {
	storageContainers := &v1alpha1.ClusterStorageContainerList{}
	if err := cl.List(ctx, storageContainers); err != nil {
		return nil, err
	}
	for i := range storageContainers.Items {
		sc := &storageContainers.Items[i]
		if sc.IsDisabled() {
			continue
		}
		if sc.Spec.WorkloadType != v1alpha1.LocalModelDownloadJob {
			continue
		}
		supported, err := sc.Spec.IsStorageUriSupported(sourceModelUri)
		if err != nil {
			return nil, fmt.Errorf("error checking storage container %s: %w", sc.Name, err)
		}
		if supported {
			return sc.Spec.Container.DeepCopy(), nil
		}
	}
	return nil, nil
}

// InjectCredentials injects storage credentials into the download container. Storage-spec
// credentials take precedence over service-account credentials. Secrets are resolved in the
// given namespace (the cache/Job namespace). It is a no-op when cb is nil.
func InjectCredentials(
	ctx context.Context,
	cb *credentials.CredentialBuilder,
	log logr.Logger,
	container *corev1.Container,
	volumes *[]corev1.Volume,
	serviceAccountName string,
	storage *v1alpha1.LocalModelStorageSpec,
	namespace string,
) error {
	if cb == nil {
		log.Info("CredentialBuilder not initialized, skipping credential injection")
		return nil
	}

	if storage != nil && storage.StorageKey != nil {
		var params map[string]string
		if storage.Parameters != nil {
			params = *storage.Parameters
		}
		log.Info("Injecting storage spec credentials", "storageKey", *storage.StorageKey)
		return cb.CreateStorageSpecSecretEnvs(ctx, namespace, nil, *storage.StorageKey, params, container)
	}

	if serviceAccountName == "" {
		serviceAccountName = "default"
	}
	log.Info("Injecting service account credentials", "serviceAccountName", serviceAccountName)
	return cb.CreateSecretVolumeAndEnv(ctx, namespace, nil, serviceAccountName, container, volumes)
}
