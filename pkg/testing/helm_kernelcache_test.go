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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"

	"github.com/talt5/localmodelcache/pkg/constants"
	kernelcacheconfig "github.com/talt5/localmodelcache/pkg/kernelcache/config"
)

// findObject parses the rendered object of a kind and name, and reports whether there is one.
func findObject(t *testing.T, rendered, kind, name string, into any) bool {
	t.Helper()
	for _, document := range strings.Split(rendered, "\n---\n") {
		if strings.Contains(document, "\nkind: "+kind+"\n") && strings.Contains(document, "\n  name: "+name+"\n") {
			require.NoError(t, yaml.Unmarshal([]byte(document), into))
			return true
		}
	}
	return false
}

// The controller sets the affinity and tolerations of the agent from the KernelCacheNodeGroups.
// Server-side apply refuses an upgrade if the chart sets them too.
func TestChartKernelCacheAgentPlacement(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	tests := map[string]struct {
		sets         []string
		nodeSelector map[string]string
	}{
		"disabled": {nodeSelector: map[string]string{"local.cache.ai/kernelcache-agent": "disabled"}},
		"enabled":  {sets: []string{"localmodelcache.kernelcache.enabled=true"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rendered := renderInNamespace(t, helm, "localmodelcache", tc.sets...)
			agent := &appsv1.DaemonSet{}
			require.True(t, findObject(t, rendered, "DaemonSet", "localmodelcache-kernelcache-agent", agent))
			require.Equal(t, tc.nodeSelector, agent.Spec.Template.Spec.NodeSelector)

			untyped := map[string]any{}
			require.True(t, findObject(t, rendered, "DaemonSet", "localmodelcache-kernelcache-agent", &untyped))
			template, _ := untyped["spec"].(map[string]any)["template"].(map[string]any)
			spec, _ := template["spec"].(map[string]any)
			require.NotEmpty(t, spec)
			require.NotContains(t, spec, "affinity")
			require.NotContains(t, spec, "tolerations")
		})
	}
}

// Kubernetes only lets the controller bind roles it holds itself or may bind.
func TestChartLetsTheControllerBindTheRegistryRoles(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	const binder = "localmodelcache-kernelcache-registry-binder"
	auth := "localmodelcache.kernelcache.registry.auth."
	token := []string{
		"localmodelcache.kernelcache.enabled=true", auth + "type=serviceAccountToken",
		auth + "pushRoleRef.kind=ClusterRole", auth + "pushRoleRef.name=system:image-builder",
		auth + "pullRoleRef.kind=Role", auth + "pullRoleRef.name=puller",
	}
	rendered := renderInNamespace(t, helm, "elsewhere", token...)
	role := &rbacv1.ClusterRole{}
	require.True(t, findObject(t, rendered, "ClusterRole", binder, role))
	require.Equal(t, []rbacv1.PolicyRule{
		{APIGroups: []string{rbacv1.GroupName}, Resources: []string{"clusterroles"}, ResourceNames: []string{"system:image-builder"}, Verbs: []string{"bind"}},
		{APIGroups: []string{rbacv1.GroupName}, Resources: []string{"roles"}, ResourceNames: []string{"puller"}, Verbs: []string{"bind"}},
	}, role.Rules)
	binding := &rbacv1.ClusterRoleBinding{}
	require.True(t, findObject(t, rendered, "ClusterRoleBinding", binder, binding))
	require.Equal(t, rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: binder}, binding.RoleRef)
	require.Equal(t, []rbacv1.Subject{{Kind: "ServiceAccount", Name: "localmodelcache-controller-manager", Namespace: "elsewhere"}}, binding.Subjects)

	for name, sets := range map[string][]string{
		"kernel cache disabled":     token[1:],
		"registry without auth":     {"localmodelcache.kernelcache.enabled=true"},
		"kernel cache not set up":   nil,
		"roles set, auth type none": append([]string{"localmodelcache.kernelcache.enabled=true"}, token[2:]...),
	} {
		t.Run(name, func(t *testing.T) {
			require.NotContains(t, renderInNamespace(t, helm, "localmodelcache", sets...), binder)
		})
	}
}

// The certificate of the signer is issued in the release namespace.
func TestChartKernelCacheTrustBundleFollowsTheReleaseNamespace(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	before := constants.SystemNamespace
	constants.SystemNamespace = "elsewhere"
	t.Cleanup(func() { constants.SystemNamespace = before })

	configMap := &corev1.ConfigMap{}
	require.True(t, findObject(t, renderInNamespace(t, helm, "elsewhere"), "ConfigMap", "localmodelcache-config", configMap))
	config, err := kernelcacheconfig.NewKernelCacheConfig(configMap)
	require.NoError(t, err)
	require.Equal(t, "elsewhere/kernelcache-root-ca", config.ArtifactSecurity.Cert.TrustBundle)

	configMap = &corev1.ConfigMap{}
	require.True(t, findObject(t, renderInNamespace(t, helm, "elsewhere", "localmodelcache.kernelcache.artifactSecurity.cert.trustBundle=pki/ca"),
		"ConfigMap", "localmodelcache-config", configMap))
	config, err = kernelcacheconfig.NewKernelCacheConfig(configMap)
	require.NoError(t, err)
	require.Equal(t, "pki/ca", config.ArtifactSecurity.Cert.TrustBundle)
}

// A registry without TLS, such as a test registry, needs registry.insecure in the controller's
// ConfigMap. The chart passes it through and leaves it off by default.
func TestChartKernelCacheRegistryInsecure(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	for name, test := range map[string]struct {
		sets []string
		want bool
	}{
		"default":  {want: false},
		"insecure": {sets: []string{"localmodelcache.kernelcache.registry.endpoint=registry.local:5000", "localmodelcache.kernelcache.registry.insecure=true"}, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			configMap := &corev1.ConfigMap{}
			require.True(t, findObject(t, renderInNamespace(t, helm, constants.SystemNamespace, test.sets...), "ConfigMap", "localmodelcache-config", configMap))
			config, err := kernelcacheconfig.NewKernelCacheConfig(configMap)
			require.NoError(t, err)
			require.Equal(t, test.want, config.Registry.Insecure)
		})
	}
}
