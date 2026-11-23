/*
Copyright 2026 The LocalModelCache Authors.

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

package main

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/constants"
)

// cacheOptions limits the objects the manager keeps in memory to those its controllers use. The
// ConfigMaps and Pods of a whole cluster can exceed the memory limit of the controller.
func cacheOptions() (cache.Options, error) {
	// The kernel cache webhook sets the label on the Pods it handles.
	kernelCachePod, err := labels.NewRequirement(constants.KernelCacheWorkloadLabelKey, selection.Exists, nil)
	if err != nil {
		return cache.Options{}, err
	}
	return cache.Options{
		DefaultTransform: cache.TransformStripManagedFields(),
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Pod{}: {Label: labels.NewSelector().Add(*kernelCachePod)},
			&corev1.ConfigMap{}: {Namespaces: map[string]cache.Config{
				constants.SystemNamespace: {
					FieldSelector: fields.OneTermEqualSelector("metadata.name", constants.LocalModelConfigMapName),
				},
			}},
		},
	}, nil
}
