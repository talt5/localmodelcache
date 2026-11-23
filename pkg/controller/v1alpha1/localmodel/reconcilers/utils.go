/*
Copyright 2024 The KServe Authors.

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

package reconcilers

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

var (
	// FinalizerName is no longer set. It is removed from caches that still have it.
	FinalizerName               = "local.cache.ai/localmodelcache-finalizer"
	NamespaceCacheFinalizerName = "local.cache.ai/localmodelnamespacecache-finalizer"
)

// LocalModelParams holds common parameters extracted from either LocalModelCache or LocalModelNamespaceCache
type LocalModelParams struct {
	Name               string
	Namespace          string // Empty for cluster-scoped LocalModelCache
	SourceModelUri     string
	NodeGroups         []string
	ServiceAccountName string
	ImagePullSecrets   []corev1.LocalObjectReference
	Storage            *v1alpha1.LocalModelStorageSpec
}

// key identifies the cache among the models of a LocalModelNode.
func (p *LocalModelParams) key() types.NamespacedName {
	return types.NamespacedName{Namespace: p.Namespace, Name: p.Name}
}

// modelInfo returns the LocalModelNode entry of the cache. The node agent uses nodeGroupName to
// find the claim its download Job mounts.
func (p *LocalModelParams) modelInfo(nodeGroupName string) v1alpha1.LocalModelInfo {
	return v1alpha1.LocalModelInfo{
		ModelName:          p.Name,
		SourceModelUri:     p.SourceModelUri,
		Namespace:          p.Namespace,
		NodeGroup:          nodeGroupName,
		ServiceAccountName: p.ServiceAccountName,
		ImagePullSecrets:   p.ImagePullSecrets,
		Storage:            p.Storage,
	}
}

// ExtractLocalModelParams extracts common parameters from either LocalModelCache or LocalModelNamespaceCache
// Only one of the parameters should be non-nil
func ExtractLocalModelParams(localModelCache *v1alpha1.LocalModelCache, localModelNamespaceCache *v1alpha1.LocalModelNamespaceCache) LocalModelParams {
	if localModelCache != nil {
		return LocalModelParams{
			Name:               localModelCache.Name,
			Namespace:          "", // Cluster-scoped
			SourceModelUri:     localModelCache.Spec.SourceModelUri,
			NodeGroups:         localModelCache.Spec.NodeGroups,
			ServiceAccountName: localModelCache.Spec.ServiceAccountName,
			ImagePullSecrets:   localModelCache.Spec.ImagePullSecrets,
			Storage:            localModelCache.Spec.Storage,
		}
	}
	if localModelNamespaceCache != nil {
		return LocalModelParams{
			Name:               localModelNamespaceCache.Name,
			Namespace:          localModelNamespaceCache.Namespace,
			SourceModelUri:     localModelNamespaceCache.Spec.SourceModelUri,
			NodeGroups:         localModelNamespaceCache.Spec.NodeGroups,
			ServiceAccountName: localModelNamespaceCache.Spec.ServiceAccountName,
			ImagePullSecrets:   localModelNamespaceCache.Spec.ImagePullSecrets,
			Storage:            localModelNamespaceCache.Spec.Storage,
		}
	}
	return LocalModelParams{}
}

// IsNodeReady checks if a node is in ready state
func IsNodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// NodeStatusFromLocalModelStatus converts a ModelStatus to NodeStatus
func NodeStatusFromLocalModelStatus(modelStatus v1alpha1.ModelStatus) v1alpha1.NodeStatus {
	switch modelStatus {
	case v1alpha1.ModelDownloadPending:
		return v1alpha1.NodeDownloadPending
	case v1alpha1.ModelDownloading:
		return v1alpha1.NodeDownloading
	case v1alpha1.ModelDownloadError:
		return v1alpha1.NodeDownloadError
	case v1alpha1.ModelDownloaded:
		return v1alpha1.NodeDownloaded
	}
	return v1alpha1.NodeDownloadPending
}

// setMountReferences records where workloads find the model: the claims that expose it and,
// when the model is not at the root of the volume, the directory that holds its files.
func setMountReferences(status *v1alpha1.LocalModelCacheStatus, claims []string, subPath string) {
	status.SubPath = subPath
	if len(claims) == 0 {
		claims = nil
	}
	status.PersistentVolumeClaims = claims
}

// getNodeGroups returns the node groups of a cache. A node group that does not exist is left
// out: the cache goes on with the others, and reconciles again when the group is created.
func getNodeGroups(ctx context.Context, reader client.Reader, log logr.Logger, names []string) ([]v1alpha1.LocalModelNodeGroup, error) {
	nodeGroups := make([]v1alpha1.LocalModelNodeGroup, 0, len(names))
	for _, name := range names {
		nodeGroup := v1alpha1.LocalModelNodeGroup{}
		if err := reader.Get(ctx, types.NamespacedName{Name: name}, &nodeGroup); apierr.IsNotFound(err) {
			log.Info("LocalModelNodeGroup not found", "name", name)
			continue
		} else if err != nil {
			return nil, err
		}
		nodeGroups = append(nodeGroups, nodeGroup)
	}
	return nodeGroups, nil
}

// ReconcileCacheStatus records where workloads find the model and how far each node of the node
// groups is with it. Only one of localModelCache or localModelNamespaceCache should be non-nil.
func ReconcileCacheStatus(
	ctx context.Context,
	c client.Client,
	log logr.Logger,
	localModelCache *v1alpha1.LocalModelCache,
	localModelNamespaceCache *v1alpha1.LocalModelNamespaceCache,
	nodeGroups []v1alpha1.LocalModelNodeGroup,
	claims []string,
	subPath string,
) error {
	var cache client.Object
	var status *v1alpha1.LocalModelCacheStatus
	if localModelCache != nil {
		cache, status = localModelCache, &localModelCache.Status
	} else {
		cache, status = localModelNamespaceCache, &localModelNamespaceCache.Status
	}
	params := ExtractLocalModelParams(localModelCache, localModelNamespaceCache)

	before := status.DeepCopy()
	setMountReferences(status, claims, subPath)
	// A failure to read the nodes keeps their last known state and still records the mount
	// references.
	nodeStatus, nodesErr := nodeStatusOfCache(ctx, c, log, &params, nodeGroups)
	if nodesErr == nil {
		status.NodeStatus = nodeStatus
		status.ModelCopies = countCopies(nodeStatus)
	}

	if !equality.Semantic.DeepEqual(before, status) {
		if err := c.Status().Update(ctx, cache); err != nil {
			return errors.Join(nodesErr, err)
		}
	}
	return nodesErr
}

// nodeStatusOfCache returns the state of the model on every node that is in one of the node
// groups now, so nodes that left a group or the cluster are not counted.
func nodeStatusOfCache(ctx context.Context, reader client.Reader, log logr.Logger, params *LocalModelParams, nodeGroups []v1alpha1.LocalModelNodeGroup) (map[string]v1alpha1.NodeStatus, error) {
	nodes := &corev1.NodeList{}
	if err := reader.List(ctx, nodes); err != nil {
		return nil, err
	}
	localModelNodes := &v1alpha1.LocalModelNodeList{}
	if err := reader.List(ctx, localModelNodes); err != nil {
		return nil, err
	}
	modelStatus := make(map[string]v1alpha1.ModelStatus, len(localModelNodes.Items))
	for i := range localModelNodes.Items {
		modelStatus[localModelNodes.Items[i].Name] = modelStatusOnNode(&localModelNodes.Items[i], params)
	}

	nodeStatus := map[string]v1alpha1.NodeStatus{}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if matchingNodeGroups(log, nodeGroups, node).Len() == 0 {
			continue
		}
		status := NodeStatusFromLocalModelStatus(modelStatus[node.Name])
		// The files of a finished download survive a node that is down for a while.
		if !IsNodeReady(*node) && status != v1alpha1.NodeDownloaded {
			status = v1alpha1.NodeNotReady
		}
		nodeStatus[node.Name] = status
	}
	return nodeStatus, nil
}

// modelStatusOnNode returns the state the node agent reported for the cache. A state reported
// before the node was told to hold this model, for example for an earlier cache of the same
// name, does not count.
func modelStatusOnNode(localModelNode *v1alpha1.LocalModelNode, params *LocalModelParams) v1alpha1.ModelStatus {
	for i := range localModelNode.Spec.LocalModels {
		info := &localModelNode.Spec.LocalModels[i]
		if info.ModelName == params.Name && info.Namespace == params.Namespace &&
			info.StorageKey() == v1alpha1.GetStorageKey(params.SourceModelUri, params.Storage) {
			return localModelNode.Status.ModelStatus[info.GetStatusKey()]
		}
	}
	return ""
}

func countCopies(nodeStatus map[string]v1alpha1.NodeStatus) *v1alpha1.ModelCopies {
	copies := &v1alpha1.ModelCopies{Total: len(nodeStatus)}
	for _, status := range nodeStatus {
		switch status {
		case v1alpha1.NodeDownloaded:
			copies.Available++
		case v1alpha1.NodeDownloadError:
			copies.Failed++
		}
	}
	return copies
}

// affectedByNode reports whether a change to a node can change the status of a cache: the node
// is in one of its node groups, or was counted before.
func affectedByNode(inGroups sets.Set[string], nodeName string, nodeGroups []string, status *v1alpha1.LocalModelCacheStatus) bool {
	if _, ok := status.NodeStatus[nodeName]; ok {
		return true
	}
	return inGroups.HasAny(nodeGroups...)
}
