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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierr "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/talt5/localmodelcache/pkg/credentials"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

// LocalModelNamespaceCacheReconciler reconciles namespace-scoped LocalModelNamespaceCache resources
type LocalModelNamespaceCacheReconciler struct {
	client.Client
	APIReader         client.Reader
	Clientset         *kubernetes.Clientset
	Log               logr.Logger
	Scheme            *runtime.Scheme
	CredentialBuilder *credentials.CredentialBuilder
}

// Reconcile creates the PV and PVC that workloads in the cache's namespace mount and those that
// download Jobs mount, and reports the state of the model on each node. NodeModelsReconciler
// tells the nodes to hold it.
func (c *LocalModelNamespaceCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.Log.Info("Reconciling namespace-scoped localmodel", "name", req.Name, "namespace", req.Namespace)
	localModel := &v1alpha1.LocalModelNamespaceCache{}
	if err := c.Get(ctx, req.NamespacedName, localModel); apierr.IsNotFound(err) {
		return reconcile.Result{}, c.deleteOrphans(ctx, req.NamespacedName)
	} else if err != nil {
		return reconcile.Result{}, err
	}

	volumes := newCacheVolumes(c.Clientset, c.Scheme, c.Log, localModel)
	if !localModel.Spec.SharedPVCMode() && !localModel.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, c.finalizeNodeLocal(ctx, localModel, volumes)
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

	// Shared-PVC mode takes a dedicated branch before any node-group resolution or per-node
	// PV/PVC/LocalModelNode fan-out.
	if localModel.Spec.SharedPVCMode() {
		return c.reconcileSharedPVC(ctx, localModel, configMap, localModelConfig)
	}

	if !controllerutil.ContainsFinalizer(localModel, NamespaceCacheFinalizerName) {
		patch := client.MergeFromWithOptions(localModel.DeepCopy(), client.MergeFromWithOptimisticLock{})
		controllerutil.AddFinalizer(localModel, NamespaceCacheFinalizerName)
		if err := c.Patch(ctx, localModel, patch); err != nil {
			return ctrl.Result{}, err
		}
	}
	nodeGroups, err := getNodeGroups(ctx, c.Client, c.Log, localModel.Spec.NodeGroups)
	if err != nil {
		return reconcile.Result{}, err
	}

	for _, name := range localModel.Spec.NodeGroups {
		volumes.keep(servingVolumeNames(localModel, name))
		volumes.keep(namespaceDownloadVolumeNames(localModel, name, localModelConfig))
	}
	var errs []error
	for i := range nodeGroups {
		pvName, claim := namespaceDownloadVolumeNames(localModel, nodeGroups[i].Name, localModelConfig)
		errs = append(errs, volumes.ensure(ctx, pvName, downloadVolumeSpec(&nodeGroups[i]), claim, &nodeGroups[i].Spec.PersistentVolumeClaimSpec))
	}

	// The root of the claims that workloads mount is the model's folder. Users who manage the
	// volumes themselves need its path instead.
	storageSubPath := v1alpha1.GetStorageSubPath(localModel.Spec.SourceModelUri, localModel.Spec.Storage)
	var claims []string
	var subPath string
	if localModelConfig.DisableVolumeManagement {
		subPath = storageSubPath
	} else {
		for i := range nodeGroups {
			pvName, claim := servingVolumeNames(localModel, nodeGroups[i].Name)
			if err := volumes.ensureServing(ctx, c.Client, &nodeGroups[i], storageSubPath, pvName, claim); err != nil {
				errs = append(errs, err)
				continue
			}
			if !slices.Contains(claims, claim.Name) { // A node group can be listed twice.
				claims = append(claims, claim.Name)
			}
		}
	}
	errs = append(errs,
		volumes.prune(ctx, c.Client),
		ReconcileCacheStatus(ctx, c.Client, c.Log, nil, localModel, nodeGroups, claims, subPath))
	return ctrl.Result{}, errors.Join(errs...)
}

// deleteOrphans deletes the volumes of a cache that does not exist. Its finalizer normally does,
// but Kubernetes can delete an object while the finalizer is being added to it.
func (c *LocalModelNamespaceCacheReconciler) deleteOrphans(ctx context.Context, key types.NamespacedName) error {
	volumes, claims, err := listVolumes(ctx, c.Client, client.HasLabels{constants.LocalModelUIDLabelKey})
	if err != nil {
		return err
	}
	volumes = slices.DeleteFunc(volumes, func(volume corev1.PersistentVolume) bool { return cacheOf(&volume) != key })
	// Garbage collection deletes the claims that the cache owned.
	claims = slices.DeleteFunc(claims, func(claim corev1.PersistentVolumeClaim) bool {
		return cacheOf(&claim) != key || claim.Namespace == key.Namespace
	})
	if len(volumes)+len(claims) == 0 {
		return nil
	}
	// The informer may not know a new cache yet.
	if err := c.APIReader.Get(ctx, key, &v1alpha1.LocalModelNamespaceCache{}); !apierr.IsNotFound(err) {
		return err
	}
	return deleteVolumes(ctx, c.Clientset, c.Log, volumes, claims)
}

// finalizeNodeLocal deletes the volumes of the cache, which include cluster-scoped ones that it
// cannot own. It reads them from the API server, since the cache may not know new ones yet.
func (c *LocalModelNamespaceCacheReconciler) finalizeNodeLocal(ctx context.Context, localModel *v1alpha1.LocalModelNamespaceCache, volumes *cacheVolumes) error {
	if !controllerutil.ContainsFinalizer(localModel, NamespaceCacheFinalizerName) {
		return nil
	}
	reader := c.APIReader
	if reader == nil {
		reader = c.Client
	}
	if err := volumes.prune(ctx, reader); err != nil {
		return err
	}
	patch := client.MergeFromWithOptions(localModel.DeepCopy(), client.MergeFromWithOptimisticLock{})
	controllerutil.RemoveFinalizer(localModel, NamespaceCacheFinalizerName)
	return client.IgnoreNotFound(c.Patch(ctx, localModel, patch))
}

// servingVolumeNames returns the PV and the PVC that workloads mount for a node group.
func servingVolumeNames(localModel *v1alpha1.LocalModelNamespaceCache, nodeGroup string) (string, types.NamespacedName) {
	claim := types.NamespacedName{Namespace: localModel.Namespace, Name: localmodelcache.ClaimName(localModel.Name, nodeGroup)}
	return localmodelcache.NamespaceCacheVolumeName(claim), claim
}

// namespaceDownloadVolumeNames returns the PV and the PVC that download Jobs mount for a node group.
func namespaceDownloadVolumeNames(localModel *v1alpha1.LocalModelNamespaceCache, nodeGroup string, config *localmodelcache.LocalModelConfig) (string, types.NamespacedName) {
	volume, _ := servingVolumeNames(localModel, nodeGroup)
	name := localmodelcache.DownloadVolumeName(volume)
	return name, types.NamespacedName{Namespace: config.JobNamespace, Name: name}
}

// nodeFuncNamespaceCache reconciles the caches whose status a change to the node can affect.
func (c *LocalModelNamespaceCacheReconciler) nodeFuncNamespaceCache(ctx context.Context, obj client.Object) []reconcile.Request {
	node := obj.(*corev1.Node)
	nodeGroups := &v1alpha1.LocalModelNodeGroupList{}
	if err := c.List(ctx, nodeGroups); err != nil {
		c.Log.Error(err, "list node groups error when reconciling nodes")
		return nil
	}
	inGroups := matchingNodeGroups(c.Log, nodeGroups.Items, node)
	return c.requestsFor(ctx, func(model *v1alpha1.LocalModelNamespaceCache) bool {
		return affectedByNode(inGroups, node.Name, model.Spec.NodeGroups, &model.Status)
	})
}

// nodeGroupFuncNamespaceCache reconciles the caches that name the node group.
func (c *LocalModelNamespaceCacheReconciler) nodeGroupFuncNamespaceCache(ctx context.Context, obj client.Object) []reconcile.Request {
	return c.requestsFor(ctx, func(model *v1alpha1.LocalModelNamespaceCache) bool {
		return slices.Contains(model.Spec.NodeGroups, obj.GetName())
	})
}

func (c *LocalModelNamespaceCacheReconciler) requestsFor(ctx context.Context, include func(*v1alpha1.LocalModelNamespaceCache) bool) []reconcile.Request {
	models := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := c.List(ctx, models); err != nil {
		c.Log.Error(err, "list namespace models error")
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

// volumeFuncNamespaceCache reconciles the cache that a volume, or a claim in the job namespace,
// was created for, so that one that is deleted comes back. The cache cannot own them.
func (c *LocalModelNamespaceCacheReconciler) volumeFuncNamespaceCache(_ context.Context, obj client.Object) []reconcile.Request {
	key := cacheOf(obj)
	if key.Namespace == "" || key.Name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: key}}
}

// Given a PVC, reconcile shared-PVC caches in the same namespace that reference it by name.
// PVCs are watched via a map (never owned), so their creation, binding, expansion, and capacity
// changes trigger reconciliation without the controller taking ownership of user-provided claims.
func (c *LocalModelNamespaceCacheReconciler) pvcFuncNamespaceCache(ctx context.Context, obj client.Object) []reconcile.Request {
	pvc := obj.(*corev1.PersistentVolumeClaim)
	models := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := c.List(ctx, models, client.InNamespace(pvc.Namespace)); err != nil {
		c.Log.Error(err, "list namespace models error when reconciling PVCs")
		return []reconcile.Request{}
	}
	requests := []reconcile.Request{}
	for i := range models.Items {
		model := &models.Items[i]
		if model.Spec.SharedPVCMode() && *model.Spec.PVCRef == pvc.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: model.Name, Namespace: model.Namespace},
			})
		}
	}
	return requests
}

// deletedClaimFuncNamespaceCache reconciles the caches that can now create a claim of that name,
// which something else had, such as a LocalModelCache.
func (c *LocalModelNamespaceCacheReconciler) deletedClaimFuncNamespaceCache(ctx context.Context, obj client.Object) []reconcile.Request {
	return c.requestsFor(ctx, func(model *v1alpha1.LocalModelNamespaceCache) bool {
		return model.Namespace == obj.GetNamespace() && slices.Contains(localmodelcache.NamespaceCacheClaimNames(model), obj.GetName())
	})
}

// Given a deleted shared-PVC cache, reconcile caches that were contending for the same
// destination so the next deterministic owner can start its import.
func (c *LocalModelNamespaceCacheReconciler) deletedCacheFuncNamespaceCache(ctx context.Context, obj client.Object) []reconcile.Request {
	deleted := obj.(*v1alpha1.LocalModelNamespaceCache)
	if !deleted.Spec.SharedPVCMode() {
		return nil
	}

	reader := c.APIReader
	if reader == nil {
		reader = c.Client
	}
	caches := &v1alpha1.LocalModelNamespaceCacheList{}
	if err := reader.List(ctx, caches, client.InNamespace(deleted.Namespace)); err != nil {
		c.Log.Error(err, "list namespace models error when reconciling deleted cache")
		return nil
	}

	storageKey := deleted.Spec.StorageKey()
	requests := []reconcile.Request{}
	for i := range caches.Items {
		cache := &caches.Items[i]
		if !cache.Spec.SharedPVCMode() || *cache.Spec.PVCRef != *deleted.Spec.PVCRef {
			continue
		}
		if cache.Spec.StorageKey() != storageKey {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: cache.Name, Namespace: cache.Namespace},
		})
	}
	return requests
}

// Given a LocalModelNode object, reconcile all namespace-scoped LocalModelNamespaceCache CRs that are referenced in it.
func (c *LocalModelNamespaceCacheReconciler) localmodelNodeFuncNamespaceCache(ctx context.Context, obj client.Object) []reconcile.Request {
	localmodelNode := obj.(*v1alpha1.LocalModelNode)
	requests := []reconcile.Request{}
	for _, modelInfo := range localmodelNode.Spec.LocalModels {
		// Only handle namespace-scoped LocalModelNamespaceCache (non-empty namespace)
		if modelInfo.Namespace != "" {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      modelInfo.ModelName,
					Namespace: modelInfo.Namespace,
				},
			})
		}
	}
	return requests
}

func (c *LocalModelNamespaceCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if c.APIReader == nil {
		c.APIReader = mgr.GetAPIReader()
	}
	controllerBuilder := ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LocalModelNamespaceCache{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		// Shared-PVC import Jobs are owned by the cache, so their status changes trigger reconciliation.
		Owns(&batchv1.Job{})

	return controllerBuilder.
		Watches(&v1alpha1.LocalModelNamespaceCache{}, handler.EnqueueRequestsFromMapFunc(c.deletedCacheFuncNamespaceCache), builder.WithPredicates(DeletedPredicate())).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(c.nodeFuncNamespaceCache), builder.WithPredicates(NodeChangedPredicate())).
		Watches(&v1alpha1.LocalModelNodeGroup{}, handler.EnqueueRequestsFromMapFunc(c.nodeGroupFuncNamespaceCache), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.LocalModelNode{}, handler.EnqueueRequestsFromMapFunc(c.localmodelNodeFuncNamespaceCache), builder.WithPredicates(LocalModelNodeChangedPredicate())).
		Watches(&corev1.PersistentVolume{}, handler.EnqueueRequestsFromMapFunc(c.volumeFuncNamespaceCache)).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(c.volumeFuncNamespaceCache)).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(c.deletedClaimFuncNamespaceCache), builder.WithPredicates(DeletedPredicate())).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(c.pvcFuncNamespaceCache)).
		Complete(c)
}
