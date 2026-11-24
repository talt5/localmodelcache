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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

const nodeAgent = "localmodelcache-node-agent"

// grant is what one verb on one resource allows. The namespace is empty for the whole cluster.
type grant struct{ namespace, resource, verb string }

// grantsOf returns what the bindings in the manifests give a service account.
func grantsOf(t *testing.T, serviceAccount string, manifests ...string) []grant {
	t.Helper()
	type object struct {
		Kind     string
		Metadata struct{ Name, Namespace string }
		Rules    []rbacv1.PolicyRule
		RoleRef  rbacv1.RoleRef
		Subjects []rbacv1.Subject
	}
	var objects []object
	for _, manifest := range manifests {
		for _, document := range strings.Split("\n"+manifest, "\n---\n") {
			parsed := object{}
			require.NoError(t, yaml.Unmarshal([]byte(document), &parsed))
			objects = append(objects, parsed)
		}
	}
	var grants []grant
	for _, binding := range objects {
		namespace := ""
		switch binding.Kind {
		case "ClusterRoleBinding":
		case "RoleBinding":
			namespace = binding.Metadata.Namespace
		default:
			continue
		}
		if !slices.ContainsFunc(binding.Subjects, func(subject rbacv1.Subject) bool { return subject.Name == serviceAccount }) {
			continue
		}
		found := false
		for _, role := range objects {
			if role.Kind != binding.RoleRef.Kind || role.Metadata.Name != binding.RoleRef.Name ||
				(role.Kind == "Role" && role.Metadata.Namespace != namespace) {
				continue
			}
			found = true
			for _, rule := range role.Rules {
				for _, resource := range rule.Resources {
					for _, verb := range rule.Verbs {
						grants = append(grants, grant{namespace, resource, verb})
					}
				}
			}
		}
		require.True(t, found, "%s %s refers to a missing role", binding.Kind, binding.Metadata.Name)
	}
	return grants
}

// An agent runs on every node, so a compromised node must not open the rest of the cluster.
func requireLeastPrivilege(t *testing.T, grants []grant, systemNamespace, jobNamespace string) {
	t.Helper()
	require.NotEmpty(t, grants)
	for _, granted := range grants {
		switch granted.resource {
		case "secrets", "serviceaccounts":
			require.Equal(t, grant{jobNamespace, granted.resource, "get"}, granted)
		case "jobs":
			require.Equal(t, jobNamespace, granted.namespace)
		case "configmaps":
			require.Equal(t, grant{systemNamespace, "configmaps", "get"}, granted)
		case "localmodelnodes", "nodes", "localmodelnodegroups", "clusterstoragecontainers":
			require.Contains(t, []string{"get", "list", "watch"}, granted.verb, granted.resource)
		case "localmodelnodes/status":
			require.Contains(t, []string{"get", "update", "patch"}, granted.verb)
		case "localmodelnodes/finalizers": // To own its Jobs on OpenShift.
			require.Equal(t, "update", granted.verb)
		case "tokenreviews", "subjectaccessreviews": // To serve metrics.
			require.Equal(t, "create", granted.verb)
		default:
			t.Errorf("unexpected permission %+v", granted)
		}
	}
	for _, needed := range []grant{
		{jobNamespace, "jobs", "create"},
		{jobNamespace, "jobs", "delete"},
		{jobNamespace, "jobs", "watch"},
		{jobNamespace, "secrets", "get"},
		{jobNamespace, "serviceaccounts", "get"},
		{systemNamespace, "configmaps", "get"},
		{"", "localmodelnodes", "watch"},
		{"", "localmodelnodes/status", "update"},
	} {
		require.Contains(t, grants, needed)
	}
}

func TestNodeAgentPermissions(t *testing.T) {
	var manifests []string
	for _, pattern := range []string{"../../config/rbac/localmodelnode/*.yaml", "../../config/rbac/localmodelnode-jobs/*.yaml"} {
		files, err := filepath.Glob(pattern)
		require.NoError(t, err)
		require.NotEmpty(t, files, pattern)
		for _, file := range files {
			data, err := os.ReadFile(file) // #nosec G304 -- the pattern is fixed
			require.NoError(t, err)
			manifests = append(manifests, string(data))
		}
	}
	requireLeastPrivilege(t, grantsOf(t, nodeAgent, manifests...), "localmodelcache", "localmodelcache-jobs")
}

func TestChartNodeAgentPermissions(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	for name, tc := range map[string]struct {
		namespace, jobNamespace string
		sets                    []string
	}{
		"default":                       {"localmodelcache", "localmodelcache-jobs", nil},
		"other namespaces":              {"elsewhere", "downloads", []string{"localmodelcache.localmodel.jobNamespace=downloads"}},
		"jobs in the release namespace": {"elsewhere", "elsewhere", []string{"localmodelcache.localmodel.jobNamespace=elsewhere"}},
	} {
		t.Run(name, func(t *testing.T) {
			rendered := renderInNamespace(t, helm, tc.namespace, tc.sets...)
			requireLeastPrivilege(t, grantsOf(t, nodeAgent, rendered), tc.namespace, tc.jobNamespace)
		})
	}
}

// The controller reads single Secrets, such as the storage credentials of an import Job.
func TestControllerCannotListSecrets(t *testing.T) {
	var manifests []string
	for _, file := range []string{
		"../../config/rbac/localmodel/role.yaml", "../../config/rbac/localmodel/role_binding.yaml",
		"../../config/rbac/kernelcache/role.yaml", "../../config/rbac/kernelcache/role_binding.yaml",
	} {
		data, err := os.ReadFile(file) // #nosec G304 -- fixed paths
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		manifests = append(manifests, string(data))
	}
	for _, granted := range grantsOf(t, "localmodelcache-controller-manager", manifests...) {
		if granted.resource == "secrets" {
			require.NotContains(t, []string{"list", "watch", "*"}, granted.verb)
		}
	}
}
