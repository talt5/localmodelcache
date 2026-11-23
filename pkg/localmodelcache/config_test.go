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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kserve/kserve/pkg/constants"
)

func TestGetConfigMapReadsLocalModelConfigMap(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: constants.InferenceServiceConfigMapName, Namespace: constants.KServeNamespace,
		}},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: constants.LocalModelConfigMapName, Namespace: constants.KServeNamespace},
			Data:       map[string]string{LocalModelConfigKeyName: `{"jobNamespace":"jobs"}`},
		},
	)

	configMap, err := GetConfigMap(context.Background(), clientset)
	require.NoError(t, err)
	assert.Equal(t, constants.LocalModelConfigMapName, configMap.Name)

	cfg, err := NewLocalModelConfig(configMap)
	require.NoError(t, err)
	assert.Equal(t, "jobs", cfg.JobNamespace)
}

func TestGetConfigMapMissing(t *testing.T) {
	_, err := GetConfigMap(context.Background(), fake.NewSimpleClientset())
	assert.Error(t, err)
}

func TestNewLocalModelConfig(t *testing.T) {
	t.Run("missing key yields the zero config", func(t *testing.T) {
		cfg, err := NewLocalModelConfig(&corev1.ConfigMap{Data: map[string]string{}})
		require.NoError(t, err)
		assert.Empty(t, cfg.JobNamespace)
	})

	t.Run("parses every field", func(t *testing.T) {
		cfg, err := NewLocalModelConfig(&corev1.ConfigMap{Data: map[string]string{
			LocalModelConfigKeyName: `{
				"jobNamespace": "test-ns",
				"defaultJobImage": "test-image",
				"fsGroup": 1000,
				"sharedPVCImportFSGroup": 2000,
				"jobTTLSecondsAfterFinished": 3600,
				"reconcilationFrequencyInSecs": 60,
				"disableVolumeManagement": true
			}`,
		}})
		require.NoError(t, err)
		assert.Equal(t, "test-ns", cfg.JobNamespace)
		assert.Equal(t, "test-image", cfg.DefaultJobImage)
		require.NotNil(t, cfg.FSGroup)
		assert.Equal(t, int64(1000), *cfg.FSGroup)
		require.NotNil(t, cfg.SharedPVCImportFSGroup)
		assert.Equal(t, int64(2000), *cfg.SharedPVCImportFSGroup)
		require.NotNil(t, cfg.JobTTLSecondsAfterFinished)
		assert.Equal(t, int32(3600), *cfg.JobTTLSecondsAfterFinished)
		require.NotNil(t, cfg.ReconcilationFrequencyInSecs)
		assert.Equal(t, int64(60), *cfg.ReconcilationFrequencyInSecs)
		assert.True(t, cfg.DisableVolumeManagement)
	})

	t.Run("invalid json is an error", func(t *testing.T) {
		cfg, err := NewLocalModelConfig(&corev1.ConfigMap{Data: map[string]string{
			LocalModelConfigKeyName: `invalid-json`,
		}})
		require.Error(t, err)
		assert.Nil(t, cfg)
	})
}

func TestGetStorageInitializerConfigs(t *testing.T) {
	baseJSON := func(extra string) string {
		return `{"image":"kserve/storage-initializer:latest","memoryRequest":"100Mi","memoryLimit":"1Gi","cpuRequest":"100m","cpuLimit":"1","caBundleConfigMapName":"","caBundleVolumeMountPath":"/etc/ssl/custom-certs"` + extra + `}`
	}
	parse := func(raw string) *corev1.ConfigMap {
		return &corev1.ConfigMap{Data: map[string]string{StorageInitializerConfigKeyName: raw}}
	}

	t.Run("parses the OCI fields", func(t *testing.T) {
		cm := parse(baseJSON(`,"enableOciModelSupport":true,"ociModelMode":"native"`))
		cfg, err := GetStorageInitializerConfigs(cm)
		require.NoError(t, err)
		assert.True(t, cfg.EnableOciModelSupport)
		assert.Equal(t, "native", cfg.OciModelMode)
	})

	t.Run("enableModelcar alone keeps the mode empty", func(t *testing.T) {
		cm := parse(baseJSON(`,"enableModelcar":true`))
		cfg, err := GetStorageInitializerConfigs(cm)
		require.NoError(t, err)
		assert.True(t, cfg.EnableOciImageSource)
		assert.Empty(t, cfg.OciModelMode)
	})

	t.Run("fetch mode is valid", func(t *testing.T) {
		cm := parse(baseJSON(`,"ociModelMode":"fetch"`))
		cfg, err := GetStorageInitializerConfigs(cm)
		require.NoError(t, err)
		assert.Equal(t, "fetch", cfg.OciModelMode)
	})

	t.Run("an unknown mode is an error", func(t *testing.T) {
		cm := parse(baseJSON(`,"ociModelMode":"invalid-mode"`))
		_, err := GetStorageInitializerConfigs(cm)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid-mode")
	})

	t.Run("an invalid quantity is an error", func(t *testing.T) {
		cm := parse(`{"memoryRequest":"lots","memoryLimit":"1Gi","cpuRequest":"100m","cpuLimit":"1"}`)
		_, err := GetStorageInitializerConfigs(cm)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "memoryRequest")
	})

	t.Run("invalid json is an error, not a panic", func(t *testing.T) {
		cm := parse(`not-json`)
		_, err := GetStorageInitializerConfigs(cm)
		require.Error(t, err)
	})
}
