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

package localmodelnamespacecache

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// The validator only runs when a ValidatingWebhookConfiguration routes the resource to it.
// Delete protection needs the DELETE operation, and the path must match the one
// ctrl.NewWebhookManagedBy derives from the type.
func TestLocalModelNamespaceCacheWebhookIsRegistered(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	manifestPath := filepath.Join(filepath.Dir(filename), "../../../../config/webhook/localmodel/manifests.yaml")
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- the path is resolved from this test source file
	require.NoError(t, err)

	type manifest struct {
		Kind     string `json:"kind"`
		Webhooks []struct {
			Name         string `json:"name"`
			ClientConfig struct {
				Service struct {
					Path string `json:"path"`
				} `json:"service"`
			} `json:"clientConfig"`
			Rules []struct {
				Operations []string `json:"operations"`
				Resources  []string `json:"resources"`
			} `json:"rules"`
		} `json:"webhooks"`
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	found := false
	for {
		object := manifest{}
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if object.Kind != "ValidatingWebhookConfiguration" {
			continue
		}
		for _, webhook := range object.Webhooks {
			for _, rule := range webhook.Rules {
				if len(rule.Resources) != 1 || rule.Resources[0] != "localmodelnamespacecaches" {
					continue
				}
				found = true
				require.Equal(t, "/validate-local-cache-ai-v1alpha1-localmodelnamespacecache", webhook.ClientConfig.Service.Path)
				require.ElementsMatch(t, []string{"CREATE", "UPDATE", "DELETE"}, rule.Operations)
			}
		}
	}
	require.True(t, found, "no ValidatingWebhookConfiguration routes localmodelnamespacecaches to the validator")
}
