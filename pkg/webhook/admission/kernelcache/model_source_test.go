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
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/talt5/localmodelcache/pkg/constants"
)

func TestAnnotationModelSourceResolver(t *testing.T) {
	resolver := annotationModelSourceResolver{}

	t.Run("resolves annotation", func(t *testing.T) {
		pod := &corev1.Pod{}
		pod.Annotations = map[string]string{
			constants.KernelCacheModelURIAnnotationKey: " hf://Qwen/Qwen3-0.6B ",
		}

		uri, known, err := resolver.Resolve(context.Background(), nil, pod, workloadRef{Kind: constants.KernelCacheWorkloadKind, Name: "qwen"})
		require.NoError(t, err)
		require.True(t, known)
		require.Equal(t, "hf://Qwen/Qwen3-0.6B", uri)
	})

	t.Run("returns unresolved when annotation is missing", func(t *testing.T) {
		uri, known, err := resolver.Resolve(context.Background(), nil, &corev1.Pod{}, workloadRef{Kind: constants.KernelCacheWorkloadKind, Name: "qwen"})
		require.NoError(t, err)
		require.False(t, known)
		require.Empty(t, uri)
	})

	t.Run("returns unresolved when annotation is invalid", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			constants.KernelCacheModelURIAnnotationKey: "%",
		}}}

		uri, known, err := resolver.Resolve(context.Background(), nil, pod, workloadRef{Kind: constants.KernelCacheWorkloadKind, Name: "qwen"})
		require.NoError(t, err)
		require.False(t, known)
		require.Empty(t, uri)
	})
}

func TestResolveWorkloadModelSourceDoesNotAssumeUnsupportedWorkloads(t *testing.T) {
	uri, known, err := resolveWorkloadModelSource(
		context.Background(),
		nil,
		&corev1.Pod{},
		workloadRef{Kind: "StatefulSet", Name: "llm"},
	)
	require.NoError(t, err)
	require.False(t, known)
	require.Empty(t, uri)
}
