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

// Package config loads the KernelCache section from the kserve-localmodel-config
// ConfigMap.
package config

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kserve/kserve/pkg/constants"
)

// Load gets and parses the KernelCache configuration from the kserve-localmodel-config
// ConfigMap in constants.KServeNamespace.
func Load(ctx context.Context, reader client.Reader) (*KernelCacheConfig, error) {
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: constants.KServeNamespace, Name: constants.LocalModelConfigMapName}
	if err := reader.Get(ctx, key, configMap); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%s was not found: %w", constants.LocalModelConfigMapName, err)
		}
		return nil, fmt.Errorf("get ConfigMap %s/%s: %w", key.Namespace, key.Name, err)
	}

	config, err := NewKernelCacheConfig(configMap)
	if err != nil {
		return nil, fmt.Errorf("parse KernelCache configuration from ConfigMap %s/%s: %w", key.Namespace, key.Name, err)
	}
	return config, nil
}
