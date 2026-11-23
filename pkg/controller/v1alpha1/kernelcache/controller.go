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

// +kubebuilder:rbac:groups=local.cache.ai,resources=kernelcachenodegroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=local.cache.ai,resources=kernelcachenodes,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=local.cache.ai,resources=kernelcaches,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=local.cache.ai,resources=kernelcaches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=local.cache.ai,resources=kernelcachecaptures,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=local.cache.ai,resources=kernelcachecaptures/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;create;update;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=apps,resources=daemonsets,resourceNames=localmodelcache-kernelcache-agent,verbs=get;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;create;patch;update;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames=localmodelcache-kernelcache-token-requester,verbs=bind

package kernelcache

import "github.com/talt5/localmodelcache/pkg/controller/v1alpha1/kernelcache/reconcilers"

type (
	KernelCacheCaptureControllerReconciler = reconcilers.KernelCacheCaptureControllerReconciler
	KernelCacheCaptureReconciler           = reconcilers.KernelCacheCaptureReconciler
	KernelCacheNodeReconciler              = reconcilers.KernelCacheNodeReconciler
	KernelCacheReconciler                  = reconcilers.KernelCacheReconciler
)
