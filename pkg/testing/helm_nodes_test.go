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
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

// renderNodes returns the node agent and the settings that its download Jobs use.
func renderNodes(t *testing.T, helm string, sets ...string) (*appsv1.DaemonSet, *localmodelcache.LocalModelConfig) {
	t.Helper()
	var agent *appsv1.DaemonSet
	var config *localmodelcache.LocalModelConfig
	for _, document := range strings.Split(renderInNamespace(t, helm, "localmodelcache", sets...), "\n---\n") {
		switch {
		case strings.Contains(document, "kind: DaemonSet") && strings.Contains(document, "name: localmodelcache-node-agent"):
			agent = &appsv1.DaemonSet{}
			require.NoError(t, yaml.Unmarshal([]byte(document), agent))
		case strings.Contains(document, "kind: ConfigMap") && strings.Contains(document, "name: localmodelcache-config"):
			configMap := &corev1.ConfigMap{}
			require.NoError(t, yaml.Unmarshal([]byte(document), configMap))
			config = &localmodelcache.LocalModelConfig{}
			require.NoError(t, json.Unmarshal([]byte(configMap.Data[localmodelcache.LocalModelConfigKeyName]), config))
		}
	}
	require.NotNil(t, agent)
	require.NotNil(t, config)
	return agent, config
}

// Download Jobs run on the nodes of the agent, so they need the same tolerations.
func TestChartGivesDownloadJobsTheTolerationsOfTheNodeAgent(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	agent, config := renderNodes(t, helm)
	require.Empty(t, agent.Spec.Template.Spec.Tolerations)
	require.Empty(t, config.Tolerations)

	tolerations := "localmodelcache.localmodelnode.controller.tolerations[0]"
	agent, config = renderNodes(t, helm, tolerations+".key=nvidia.com/gpu", tolerations+".operator=Exists", tolerations+".effect=NoSchedule")
	want := []corev1.Toleration{{Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}
	require.Equal(t, want, agent.Spec.Template.Spec.Tolerations)
	require.Equal(t, want, config.Tolerations)
}

// Downloads go to the path of the node group's volume, and the agent looks for them in its own.
func TestDefaultHostPathIsTheDocumentedOne(t *testing.T) {
	nodeGroupPath := regexp.MustCompile(`(?m)local:\n(?: +#.*\n)* +path: (\S+)`)
	guide, err := os.ReadFile("../../docs/model-cache.md")
	require.NoError(t, err)
	sample, err := os.ReadFile("../../docs/samples/vllm-node-local.yaml")
	require.NoError(t, err)

	manifest, err := os.ReadFile("../../config/localmodelnodes/manager.yaml")
	require.NoError(t, err)
	agent := &appsv1.DaemonSet{}
	require.NoError(t, yaml.Unmarshal(manifest, agent))
	agents := map[string]*appsv1.DaemonSet{"kustomize": agent}
	if helm, err := exec.LookPath("helm"); err == nil {
		agents["chart"], _ = renderNodes(t, helm)
	}
	for name, agent := range agents {
		for _, document := range [][]byte{guide, sample} {
			match := nodeGroupPath.FindSubmatch(document)
			require.NotNil(t, match)
			require.Equal(t, string(match[1]), agent.Spec.Template.Spec.Volumes[0].HostPath.Path, name)
		}
	}
}

// The agent deletes the files that download Jobs create, whose folders only their user can write.
func TestNodeAgentRunsAsTheUserOfDownloadJobs(t *testing.T) {
	dockerfile, err := os.ReadFile("../../python/storage-initializer.Dockerfile")
	require.NoError(t, err)
	users := regexp.MustCompile(`(?m)^USER (\d+)$`).FindAllSubmatch(dockerfile, -1)
	require.NotEmpty(t, users)
	want := string(users[len(users)-1][1])

	manifest, err := os.ReadFile("../../config/localmodelnodes/manager.yaml")
	require.NoError(t, err)
	agent := &appsv1.DaemonSet{}
	require.NoError(t, yaml.Unmarshal(manifest, agent))
	agents := map[string]*appsv1.DaemonSet{"kustomize": agent}
	if helm, err := exec.LookPath("helm"); err == nil {
		var config *localmodelcache.LocalModelConfig
		agents["chart"], config = renderNodes(t, helm)
		require.NotNil(t, config.FSGroup)
		require.Equal(t, config.FSGroup, agents["chart"].Spec.Template.Spec.SecurityContext.RunAsGroup)
		agents["chart on OpenShift"], _ = renderNodes(t, helm, "openshift.enabled=true")
	}
	for name, agent := range agents {
		security := agent.Spec.Template.Spec.SecurityContext
		require.NotNil(t, security.RunAsUser, name)
		require.Equal(t, want, strconv.FormatInt(*security.RunAsUser, 10), name)
		require.NotNil(t, security.RunAsGroup, name)
		require.NotNil(t, security.RunAsNonRoot, name)
	}
}
