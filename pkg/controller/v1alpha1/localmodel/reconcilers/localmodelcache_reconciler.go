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
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	APIReader client.Reader
	Clientset *kubernetes.Clientset
	Log       logr.Logger
	Scheme    *runtime.Scheme
}

// Reconcile creates the PV and PVC that download Jobs mount and those that workloads mount in each
// namespace the cache selects, and reports the state of the model on each node.
// NodeModelsReconciler tells the nodes to hold it.
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

	// Without the namespaces, prune would delete claims that workloads use.
	namespaces, err := c.workloadNamespaces(ctx, localModel, localModelConfig)
	if err != nil {
		return reconcile.Result{}, err
	}

	var errs []error
	volumes := newCacheVolumes(c.Clientset, c.Scheme, c.Log, localModel)
	volumes.yields = c.yieldClaim
	for _, name := range localModel.Spec.NodeGroups {
		volumes.keep(clusterVolumeNames(localModel, name, localModelConfig))
		for i := range namespaces {
			volumes.keep(clusterServingVolumeNames(localModel, name, namespaces[i].Name))
		}
	}
	for i := range nodeGroups {
		pvName, claim := clusterVolumeNames(localModel, nodeGroups[i].Name, localModelConfig)
		errs = append(errs, volumes.ensure(ctx, pvName, downloadVolumeSpec(&nodeGroups[i]), claim, &nodeGroups[i].Spec.PersistentVolumeClaimSpec))
	}

	subPath := v1alpha1.GetStorageSubPath(localModel.Spec.SourceModelUri, localModel.Spec.Storage)
	var claims []string
	if !localModelConfig.DisableVolumeManagement {
		for i := range namespaces {
			// Nothing can be created in a namespace that is being deleted, and its Pods may
			// still use the claims.
			if !namespaces[i].DeletionTimestamp.IsZero() {
				continue
			}
			for j := range nodeGroups {
				pvName, claim := clusterServingVolumeNames(localModel, nodeGroups[j].Name, namespaces[i].Name)
				if err := volumes.ensureServing(ctx, c.Client, &nodeGroups[j], subPath, pvName, claim); err != nil {
					errs = append(errs, err)
					continue
				}
				claims = append(claims, claim.String())
			}
		}
		slices.Sort(claims)
		claims = slices.Compact(claims) // A node group can be listed twice.
	}
	errs = append(errs,
		volumes.prune(ctx, c.Client),
		ReconcileCacheStatus(ctx, c.Client, c.Log, localModel, nil, nodeGroups, claims, subPath))
	return ctrl.Result{}, errors.Join(errs...)
}

// workloadNamespaces returns the namespaces the cache selects. The job namespace is never one of
// them: its download claim has the name of a workload's claim.
func (c *LocalModelReconciler) workloadNamespaces(ctx context.Context, localModel *v1alpha1.LocalModelCache, config *localmodelcache.LocalModelConfig) ([]metav1.PartialObjectMetadata, error) {
	selector, err := localModel.Spec.WorkloadNamespaceSelector()
	if err != nil {
		return nil, fmt.Errorf("namespaceSelector: %w", err)
	}
	namespaces := &metav1.PartialObjectMetadataList{}
	namespaces.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("NamespaceList"))
	if err := c.List(ctx, namespaces, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(namespaces.Items, func(namespace metav1.PartialObjectMetadata) bool {
		return namespace.Name == config.JobNamespace
	}), nil
}

// clusterServingVolumeNames returns the PV and the PVC that workloads mount for a node group.
func clusterServingVolumeNames(localModel *v1alpha1.LocalModelCache, nodeGroup, namespace string) (string, types.NamespacedName) {
	claim := types.NamespacedName{Namespace: namespace, Name: localmodelcache.ClaimName(localModel.Name, nodeGroup)}
	return localmodelcache.ClusterCacheVolumeName(claim), claim
}

// yieldClaim returns the LocalModelNamespaceCache that names a claim which the cache does not have,
// or "". When a user deletes the claim of that cache, it must not come back with another model.
func (c *LocalModelReconciler) yieldClaim(ctx context.Context, volume string, key types.NamespacedName) (string, error) {
	claim := &corev1.PersistentVolumeClaim{}
	// The informer may still show a claim that is gone.
	if err := c.APIReader.Get(ctx, key, claim); client.IgnoreNotFound(err) != nil {
		return "", err
	} else if err == nil && claim.DeletionTimestamp.IsZero() && claim.Spec.VolumeName == volume {
		return "", nil
	}
	caches := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := c.List(ctx, caches, client.InNamespace(key.Namespace)); err != nil {
		return "", err
	}
	for i := range caches.Items {
		cache := &caches.Items[i]
		if cache.DeletionTimestamp.IsZero() && slices.Contains(localmodelcache.NamespaceCacheClaimNames(cache), key.Name) {
			return cache.Name, nil
		}
	}
	return "", nil
}

// clusterVolumeNames returns the PV and the PVC that download Jobs mount for a node group.
func clusterVolumeNames(localModel *v1alpha1.LocalModelCache, nodeGroup string, config *localmodelcache.LocalModelConfig) (string, types.NamespacedName) {
	name := localmodelcache.ClaimName(localModel.Name, nodeGroup)
	return localmodelcache.DownloadVolumeName(name), types.NamespacedName{Namespace: config.JobNamespace, Name: name}
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

// namespaceFunc reconciles the caches that select the namespace. It gets the old and the new
// labels of one that changed.
func (c *LocalModelReconciler) namespaceFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	return c.requestsFor(ctx, func(model *v1alpha1.LocalModelCache) bool {
		selector, err := model.Spec.WorkloadNamespaceSelector()
		return err == nil && selector.Matches(labels.Set(obj.GetLabels()))
	})
}

// deletedClaimFunc reconciles the caches that can now create a claim of that name, which something
// else had, such as a LocalModelNamespaceCache.
func (c *LocalModelReconciler) deletedClaimFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	return c.requestsFor(ctx, func(model *v1alpha1.LocalModelCache) bool {
		return slices.Contains(localmodelcache.ClusterCacheClaimNames(model), obj.GetName())
	})
}

// namespaceCacheFunc reconciles the caches that yielded a claim to a LocalModelNamespaceCache, which
// may be gone without ever having had it.
func (c *LocalModelReconciler) namespaceCacheFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	names := localmodelcache.NamespaceCacheClaimNames(obj.(*v1alpha1.LocalModelNamespaceCache))
	return c.requestsFor(ctx, func(model *v1alpha1.LocalModelCache) bool {
		return localmodelcache.SharedName(localmodelcache.ClusterCacheClaimNames(model), names) != ""
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
	if c.APIReader == nil {
		c.APIReader = mgr.GetAPIReader()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LocalModelCache{}).
		Owns(&corev1.PersistentVolume{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(c.deletedClaimFunc), builder.WithPredicates(DeletedPredicate())).
		Watches(&v1alpha1.LocalModelNamespaceCache{}, handler.EnqueueRequestsFromMapFunc(c.namespaceCacheFunc), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WatchesMetadata(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(c.namespaceFunc), builder.WithPredicates(NamespaceChangedPredicate())).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(c.nodeFunc), builder.WithPredicates(NodeChangedPredicate())).
		Watches(&v1alpha1.LocalModelNodeGroup{}, handler.EnqueueRequestsFromMapFunc(c.nodeGroupFunc), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Updates model status when localmodelnode status changes
		Watches(&v1alpha1.LocalModelNode{}, handler.EnqueueRequestsFromMapFunc(c.localmodelNodeFunc), builder.WithPredicates(LocalModelNodeChangedPredicate())).
		Complete(c)
}
