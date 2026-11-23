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
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

// LocalModelReconciler reconciles cluster-scoped LocalModelCache resources
type LocalModelReconciler struct {
	client.Client
	Clientset *kubernetes.Clientset
	Log       logr.Logger
	Scheme    *runtime.Scheme
}

// Reconcile creates the PV and PVC that download Jobs mount, and reports the state of the model
// on each node. NodeModelsReconciler tells the nodes to hold it.
//
// A cluster-scoped cache only pre-loads the nodes. Workloads get a claim to mount from a
// LocalModelNamespaceCache with the same sourceModelUri in their namespace, which shares the
// node folder and does not download the model again.
func (c *LocalModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.Log.Info("Reconciling localmodel", "name", req.Name)
	localModel := &v1alpha1.LocalModelCache{}
	if err := c.Get(ctx, req.NamespacedName, localModel); err != nil {
		// Ignore not-found errors, we can get them on deleted requests.
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if controllerutil.ContainsFinalizer(localModel, FinalizerName) {
		patch := client.MergeFromWithOptions(localModel.DeepCopy(), client.MergeFromWithOptimisticLock{})
		controllerutil.RemoveFinalizer(localModel, FinalizerName)
		if err := c.Patch(ctx, localModel, patch); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	// The cache owns its volumes, so garbage collection deletes them.
	if !localModel.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	configMap, err := localmodelcache.GetConfigMap(ctx, c.Clientset)
	if err != nil {
		c.Log.Error(err, "unable to get configmap", "name", constants.LocalModelConfigMapName, "namespace", constants.SystemNamespace)
		return reconcile.Result{}, err
	}
	localModelConfig, err := localmodelcache.NewLocalModelConfig(configMap)
	if err != nil {
		c.Log.Error(err, "Failed to get local model config")
		return reconcile.Result{}, err
	}
	nodeGroups, err := getNodeGroups(ctx, c.Client, c.Log, localModel.Spec.NodeGroups)
	if err != nil {
		return reconcile.Result{}, err
	}

	var errs []error
	volumes := newCacheVolumes(c.Clientset, c.Scheme, c.Log, localModel)
	for _, name := range localModel.Spec.NodeGroups {
		volumes.keep(clusterVolumeNames(localModel, name, localModelConfig))
	}
	for i := range nodeGroups {
		pvName, claim := clusterVolumeNames(localModel, nodeGroups[i].Name, localModelConfig)
		errs = append(errs, volumes.ensure(ctx, pvName, downloadVolumeSpec(&nodeGroups[i]), claim, &nodeGroups[i].Spec.PersistentVolumeClaimSpec))
	}
	errs = append(errs,
		volumes.prune(ctx, c.Client),
		ReconcileCacheStatus(ctx, c.Client, c.Log, localModel, nil, nodeGroups, nil, v1alpha1.GetStorageSubPath(localModel.Spec.SourceModelUri, localModel.Spec.Storage)))
	return ctrl.Result{}, errors.Join(errs...)
}

// clusterVolumeNames returns the PV and the PVC that download Jobs mount for a node group.
func clusterVolumeNames(localModel *v1alpha1.LocalModelCache, nodeGroup string, config *localmodelcache.LocalModelConfig) (string, types.NamespacedName) {
	name := localModel.Name + "-" + nodeGroup
	return name + "-download", types.NamespacedName{Namespace: config.JobNamespace, Name: name}
}

// nodeFunc reconciles the caches whose status a change to the node can affect.
func (c *LocalModelReconciler) nodeFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	node := obj.(*corev1.Node)
	nodeGroups := &v1alpha1.LocalModelNodeGroupList{}
	if err := c.List(ctx, nodeGroups); err != nil {
		c.Log.Error(err, "list node groups error when reconciling nodes")
		return nil
	}
	inGroups := matchingNodeGroups(c.Log, nodeGroups.Items, node)
	return c.requestsFor(ctx, func(model *v1alpha1.LocalModelCache) bool {
		return affectedByNode(inGroups, node.Name, model.Spec.NodeGroups, &model.Status)
	})
}

// nodeGroupFunc reconciles the caches that name the node group.
func (c *LocalModelReconciler) nodeGroupFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	return c.requestsFor(ctx, func(model *v1alpha1.LocalModelCache) bool {
		return slices.Contains(model.Spec.NodeGroups, obj.GetName())
	})
}

func (c *LocalModelReconciler) requestsFor(ctx context.Context, include func(*v1alpha1.LocalModelCache) bool) []reconcile.Request {
	models := &v1alpha1.LocalModelCacheList{}
	if err := c.List(ctx, models); err != nil {
		c.Log.Error(err, "list models error")
		return nil
	}
	requests := []reconcile.Request{}
	for i := range models.Items {
		if include(&models.Items[i]) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&models.Items[i])})
		}
	}
	return requests
}

// Given a LocalModelNode object, reconcile all cluster-scoped LocalModelCache CRs that are referenced in it.
func (c *LocalModelReconciler) localmodelNodeFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	localmodelNode := obj.(*v1alpha1.LocalModelNode)
	requests := []reconcile.Request{}
	for _, modelInfo := range localmodelNode.Spec.LocalModels {
		// Only handle cluster-scoped LocalModelCache (empty namespace)
		if modelInfo.Namespace == "" {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: modelInfo.ModelName,
				},
			})
		}
	}
	return requests
}

func (c *LocalModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LocalModelCache{}).
		Owns(&corev1.PersistentVolume{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(c.nodeFunc), builder.WithPredicates(NodeChangedPredicate())).
		Watches(&v1alpha1.LocalModelNodeGroup{}, handler.EnqueueRequestsFromMapFunc(c.nodeGroupFunc), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Updates model status when localmodelnode status changes
		Watches(&v1alpha1.LocalModelNode{}, handler.EnqueueRequestsFromMapFunc(c.localmodelNodeFunc), builder.WithPredicates(LocalModelNodeChangedPredicate())).
		Complete(c)
}
