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

package reconcilers

import (
	"cmp"
	"context"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	controllerutils "github.com/talt5/localmodelcache/pkg/controller/v1alpha1/utils"
)

// NodeModelsReconciler writes the spec of each LocalModelNode: the models its node must hold.
//
// It rebuilds the whole list from the caches and node groups every time, because the node agent
// deletes the folder of every model the list leaves out. A list edited one model at a time could
// lose a model to a concurrent write, keep a deleted one, or come back with a single model after
// the LocalModelNode was deleted. Nothing else may write it.
type NodeModelsReconciler struct {
	client.Client
	APIReader client.Reader
	Log       logr.Logger
}

func (c *NodeModelsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Read the LocalModelNode before the caches. The patch below fails if the object has changed
	// since this read, so a list built from older caches never replaces a newer one.
	localModelNode := &v1alpha1.LocalModelNode{}
	err := c.Get(ctx, req.NamespacedName, localModelNode)
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}
	exists := err == nil

	node := &corev1.Node{}
	if err := c.Get(ctx, req.NamespacedName, node); apierr.IsNotFound(err) {
		if !exists {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, c.deleteForRemovedNode(ctx, localModelNode)
	} else if err != nil {
		return ctrl.Result{}, err
	}

	nodeGroups := &v1alpha1.LocalModelNodeGroupList{}
	if err := c.List(ctx, nodeGroups); err != nil {
		return ctrl.Result{}, err
	}
	caches, err := listNodeLocalCaches(ctx, c.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	models := modelsForNode(matchingNodeGroups(c.Log, nodeGroups.Items, node), caches, localModelNode.Spec.LocalModels)

	if !exists {
		if len(models) == 0 {
			return ctrl.Result{}, nil
		}
		c.Log.Info("Creating LocalModelNode", "node", req.Name, "models", len(models))
		return ctrl.Result{}, c.Create(ctx, &v1alpha1.LocalModelNode{
			ObjectMeta: metav1.ObjectMeta{Name: req.Name},
			Spec:       v1alpha1.LocalModelNodeSpec{LocalModels: models},
		})
	}
	if equality.Semantic.DeepEqual(localModelNode.Spec.LocalModels, models) {
		return ctrl.Result{}, nil
	}
	c.Log.Info("Updating LocalModelNode", "node", req.Name, "models", len(models), "before", len(localModelNode.Spec.LocalModels))
	patch := client.MergeFromWithOptions(localModelNode.DeepCopy(), client.MergeFromWithOptimisticLock{})
	localModelNode.Spec.LocalModels = models
	return ctrl.Result{}, c.Patch(ctx, localModelNode, patch)
}

// deleteForRemovedNode deletes the LocalModelNode of a node that left the cluster, and with it
// the download Jobs it owns. If the node returns, the new LocalModelNode lists its models again.
func (c *NodeModelsReconciler) deleteForRemovedNode(ctx context.Context, localModelNode *v1alpha1.LocalModelNode) error {
	// The Node informer may lag behind the LocalModelNode informer.
	err := c.APIReader.Get(ctx, types.NamespacedName{Name: localModelNode.Name}, &corev1.Node{})
	if !apierr.IsNotFound(err) {
		return err
	}
	c.Log.Info("Deleting LocalModelNode of a removed node", "node", localModelNode.Name)
	return client.IgnoreNotFound(c.Delete(ctx, localModelNode, client.Preconditions{UID: &localModelNode.UID}))
}

// modelsForNode returns the models a node must hold, given the node groups it is in and the
// models it holds now.
//
// A node that left a node group, for example because its label was removed, keeps the models it
// already has until their cache is deleted or no longer names the group: Pods that started
// before may still read the files.
func modelsForNode(nodeGroups sets.Set[string], caches []LocalModelParams, current []v1alpha1.LocalModelInfo) []v1alpha1.LocalModelInfo {
	held := make(map[types.NamespacedName]string, len(current))
	for i := range current {
		held[types.NamespacedName{Namespace: current[i].Namespace, Name: current[i].ModelName}] = current[i].NodeGroup
	}
	// Never nil: the API requires the list to be present.
	models := []v1alpha1.LocalModelInfo{}
	for _, cache := range caches {
		index := slices.IndexFunc(cache.NodeGroups, nodeGroups.Has)
		if index < 0 {
			if group, ok := held[cache.key()]; ok {
				index = slices.Index(cache.NodeGroups, group)
			}
		}
		if index >= 0 {
			models = append(models, cache.modelInfo(cache.NodeGroups[index]))
		}
	}
	return models
}

// matchingNodeGroups returns the names of the node groups the node is in. A node group with an
// invalid node affinity matches no node, so that it cannot hold up the others.
func matchingNodeGroups(log logr.Logger, nodeGroups []v1alpha1.LocalModelNodeGroup, node *corev1.Node) sets.Set[string] {
	names := sets.New[string]()
	for i := range nodeGroups {
		matches, err := controllerutils.CheckNodeAffinity(&nodeGroups[i].Spec.PersistentVolumeSpec, *node)
		if err != nil {
			log.Error(err, "Invalid node affinity", "nodeGroup", nodeGroups[i].Name)
			continue
		}
		if matches {
			names.Insert(nodeGroups[i].Name)
		}
	}
	return names
}

// listNodeLocalCaches returns every cache that places a model on nodes and is not being deleted,
// ordered by namespace and name.
func listNodeLocalCaches(ctx context.Context, reader client.Reader) ([]LocalModelParams, error) {
	var caches []LocalModelParams
	clusterCaches := &v1alpha1.LocalModelCacheList{}
	if err := reader.List(ctx, clusterCaches); err != nil {
		return nil, err
	}
	for i := range clusterCaches.Items {
		if clusterCaches.Items[i].DeletionTimestamp.IsZero() {
			caches = append(caches, ExtractLocalModelParams(&clusterCaches.Items[i], nil))
		}
	}
	namespaceCaches := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := reader.List(ctx, namespaceCaches); err != nil {
		return nil, err
	}
	for i := range namespaceCaches.Items {
		cache := &namespaceCaches.Items[i]
		if cache.DeletionTimestamp.IsZero() && !cache.Spec.SharedPVCMode() {
			caches = append(caches, ExtractLocalModelParams(nil, cache))
		}
	}
	slices.SortFunc(caches, func(a, b LocalModelParams) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return caches, nil
}

// allNodes maps any change to a cache or node group to every node.
func (c *NodeModelsReconciler) allNodes(ctx context.Context, _ client.Object) []reconcile.Request {
	names := sets.New[string]()
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		c.Log.Error(err, "Unable to list Nodes")
		return nil
	}
	for i := range nodes.Items {
		names.Insert(nodes.Items[i].Name)
	}
	localModelNodes := &v1alpha1.LocalModelNodeList{}
	if err := c.List(ctx, localModelNodes); err != nil {
		c.Log.Error(err, "Unable to list LocalModelNodes")
		return nil
	}
	for i := range localModelNodes.Items {
		names.Insert(localModelNodes.Items[i].Name)
	}
	requests := make([]reconcile.Request, 0, names.Len())
	for name := range names {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
	}
	return requests
}

func (c *NodeModelsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if c.APIReader == nil {
		c.APIReader = mgr.GetAPIReader()
	}
	specChanged := builder.WithPredicates(predicate.GenerationChangedPredicate{})
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LocalModelNode{}, specChanged).
		Watches(&corev1.Node{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(NodeChangedPredicate())).
		Watches(&v1alpha1.LocalModelNodeGroup{}, handler.EnqueueRequestsFromMapFunc(c.allNodes), specChanged).
		Watches(&v1alpha1.LocalModelCache{}, handler.EnqueueRequestsFromMapFunc(c.allNodes), specChanged).
		Watches(&v1alpha1.LocalModelNamespaceCache{}, handler.EnqueueRequestsFromMapFunc(c.allNodes), specChanged).
		Complete(c)
}
