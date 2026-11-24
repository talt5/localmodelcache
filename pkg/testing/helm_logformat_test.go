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

package testing

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestControllerLogFormatHelmRender(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not available")
	}

	root := ProjectRoot()

	t.Run("default omits log format", func(t *testing.T) {
		localmodel := renderChart(t, helm, root)
		requireLogFormat(t, localmodel, "Deployment", "localmodelcache-controller-manager", "")
		requireLogFormat(t, localmodel, "DaemonSet", "localmodelcache-node-agent", "")
		require.Empty(t, managerArgs(t, localmodel, "DaemonSet", "localmodelcache-kernelcache-agent"))
	})

	t.Run("otel-json when configured", func(t *testing.T) {
		localmodel := renderChart(t, helm, root, "localmodelcache.localmodel.controller.extraArgs[0]=--log-format=otel-json")
		requireLogFormat(t, localmodel, "Deployment", "localmodelcache-controller-manager", "--log-format=otel-json")
		requireLogFormat(t, localmodel, "DaemonSet", "localmodelcache-node-agent", "")

		node := renderChart(t, helm, root, "localmodelcache.localmodelnode.controller.extraArgs[0]=--log-format=otel-json")
		requireLogFormat(t, node, "Deployment", "localmodelcache-controller-manager", "")
		requireLogFormat(t, node, "DaemonSet", "localmodelcache-node-agent", "--log-format=otel-json")
		require.Empty(t, managerArgs(t, node, "DaemonSet", "localmodelcache-kernelcache-agent"))

		cache := renderChart(t, helm, root, "localmodelcache.kernelcachenode.controller.extraArgs[0]=--zap-log-level=debug")
		require.Equal(t, []string{"--zap-log-level=debug"}, managerArgs(t, cache, "DaemonSet", "localmodelcache-kernelcache-agent"))
		requireLogFormat(t, cache, "Deployment", "localmodelcache-controller-manager", "")
		requireLogFormat(t, cache, "DaemonSet", "localmodelcache-node-agent", "")
	})
}

// renderChart renders the localmodelcache chart with the given --set values.
func renderChart(t *testing.T, helm, root string, sets ...string) []byte {
	t.Helper()

	const chart = "charts/localmodelcache"
	args := make([]string, 0, 3+2*len(sets))
	args = append(args, "template", "localmodelcache", chart)
	for _, set := range sets {
		args = append(args, "--set", set)
	}

	cmd := exec.CommandContext(t.Context(), helm, args...) // #nosec G204 -- helm from PATH, fixed chart paths
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "helm template %s\n%s", chart, out)
	return out
}

func requireLogFormat(t *testing.T, manifest []byte, kind, name, want string) {
	t.Helper()

	args := managerArgs(t, manifest, kind, name)
	var got []string
	for _, arg := range args {
		if arg == "--log-format" || strings.HasPrefix(arg, "--log-format=") {
			got = append(got, arg)
		}
	}
	if want == "" {
		require.Empty(t, got, "%s %s log format args", kind, name)
		return
	}
	require.Equal(t, []string{want}, got, "%s %s log format args", kind, name)
}

func managerArgs(t *testing.T, manifest []byte, kind, name string) []string {
	t.Helper()

	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	for {
		obj := map[string]any{}
		err := decoder.Decode(&obj)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if len(obj) == 0 {
			continue
		}

		u := unstructured.Unstructured{Object: obj}
		if u.GetKind() != kind || u.GetName() != name {
			continue
		}

		containers, found, err := unstructured.NestedSlice(u.Object, "spec", "template", "spec", "containers")
		require.NoError(t, err)
		require.Truef(t, found, "%s %s has no containers", kind, name)
		for _, container := range containers {
			cm, ok := container.(map[string]any)
			require.True(t, ok)
			if cm["name"] != "manager" {
				continue
			}
			args, found, err := unstructured.NestedStringSlice(cm, "args")
			require.NoError(t, err)
			if !found {
				return nil
			}
			return args
		}
		t.Fatalf("%s %s has no manager container", kind, name)
	}

	t.Fatalf("rendered manifests have no %s %s", kind, name)
	return nil
}
