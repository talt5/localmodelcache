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

package testing

import (
	"os/exec"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

var namespaceObject = regexp.MustCompile(`(?m)^kind: Namespace\nmetadata:\n  annotations:\n    helm\.sh/resource-policy: keep\n  name: (\S+)$`)

// Nothing else creates the namespaces that download Jobs run in.
func TestChartCreatesJobNamespaces(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	tests := map[string]struct {
		sets []string
		want []string
	}{
		"default":      {want: []string{"localmodelcache-jobs"}},
		"kernel cache": {sets: []string{"localmodelcache.kernelcache.enabled=true"}, want: []string{"localmodelcache-jobs", "localmodelcache-kernelcache-jobs"}},
		"disabled":     {sets: []string{"localmodelcache.createJobNamespaces=false"}},
		"jobs in the release namespace": {
			sets: []string{"localmodelcache.localmodel.jobNamespace=localmodelcache"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			matches := namespaceObject.FindAllStringSubmatch(renderInNamespace(t, helm, "localmodelcache", tc.sets...), -1)
			require.Len(t, matches, len(tc.want))
			for i, want := range tc.want {
				require.Equal(t, want, matches[i][1])
			}
		})
	}
}

// The API server verifies the webhook certificate against the Service's DNS name.
func TestChartWebhookCertificateFollowsTheReleaseNamespace(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	rendered := renderInNamespace(t, helm, "elsewhere")
	require.Contains(t, rendered, "- localmodel-webhook-server-service.elsewhere.svc\n")
	require.NotContains(t, rendered, ".localmodelcache.svc")
	require.NotContains(t, rendered, "namespace: localmodelcache\n")
}

func renderInNamespace(t *testing.T, helm, namespace string, sets ...string) string {
	t.Helper()
	args := make([]string, 0, 5+2*len(sets))
	args = append(args, "template", "localmodelcache", "../../charts/localmodelcache", "--namespace", namespace)
	for _, set := range sets {
		args = append(args, "--set", set)
	}
	output, err := exec.CommandContext(t.Context(), helm, args...).CombinedOutput() // #nosec G204 -- fixed arguments
	require.NoError(t, err, string(output))
	return string(output)
}
