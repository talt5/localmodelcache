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

package localmodel

import (
	"fmt"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

var _ = Describe("Caches and the nodes that hold them", func() {
	const (
		timeout      = time.Second * 10
		interval     = time.Millisecond * 100
		jobNamespace = "localmodelcache-jobs"
		groupLabel   = "test/group"
	)

	var suffix int64
	BeforeEach(func() { suffix = time.Now().UnixNano() })
	unique := func(name string) string { return fmt.Sprintf("%s-%d", name, suffix) }

	create := func(ctx SpecContext, obj client.Object) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
			if _, ok := obj.(*corev1.Namespace); ok {
				return // Nothing removes namespaces in envtest.
			}
			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj))
			}, timeout, interval).Should(BeTrue())
		})
	}
	nodeGroup := func(name string) *v1alpha1.LocalModelNodeGroup {
		return &v1alpha1.LocalModelNodeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.LocalModelNodeGroupSpec{
				PersistentVolumeSpec: corev1.PersistentVolumeSpec{
					AccessModes:            []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Capacity:               corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")},
					StorageClassName:       "standard",
					PersistentVolumeSource: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/models"}},
					NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
						MatchExpressions: []corev1.NodeSelectorRequirement{{Key: groupLabel, Operator: corev1.NodeSelectorOpIn, Values: []string{name}}},
					}}}},
				},
				PersistentVolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")}},
				},
			},
		}
	}
	node := func(name, group string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{groupLabel: group}},
			Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
		}
	}
	setGroup := func(ctx SpecContext, obj *corev1.Node, group string) {
		GinkgoHelper()
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Labels = map[string]string{groupLabel: group}
		Expect(k8sClient.Patch(ctx, obj, patch)).To(Succeed())
	}
	clusterCache := func(name string, groups ...string) *v1alpha1.LocalModelCache {
		return &v1alpha1.LocalModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: "hf://org/" + name, ModelSize: resource.MustParse("1Gi"), NodeGroups: groups},
		}
	}
	namespaceCache := func(namespace, name string, groups ...string) *v1alpha1.LocalModelNamespaceCache {
		return &v1alpha1.LocalModelNamespaceCache{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
			Spec:       v1alpha1.LocalModelNamespaceCacheSpec{SourceModelUri: "hf://org/" + name, ModelSize: resource.MustParse("1Gi"), NodeGroups: groups},
		}
	}
	held := func(ctx SpecContext, node string) func() []string {
		return func() []string {
			localModelNode := &v1alpha1.LocalModelNode{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: node}, localModelNode); err != nil {
				return []string{err.Error()}
			}
			names := []string{}
			for i := range localModelNode.Spec.LocalModels {
				names = append(names, localModelNode.Spec.LocalModels[i].GetStatusKey())
			}
			return names
		}
	}
	nodesOf := func(ctx SpecContext, cache client.Object) func() map[string]v1alpha1.NodeStatus {
		return func() map[string]v1alpha1.NodeStatus {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache); err != nil {
				return nil
			}
			status := map[string]v1alpha1.NodeStatus{}
			switch c := cache.(type) {
			case *v1alpha1.LocalModelCache:
				status = c.Status.NodeStatus
			case *v1alpha1.LocalModelNamespaceCache:
				status = c.Status.NodeStatus
			}
			return status
		}
	}
	markDownloaded := func(ctx SpecContext, node string, keys ...string) {
		GinkgoHelper()
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			localModelNode := &v1alpha1.LocalModelNode{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: node}, localModelNode); err != nil {
				return err
			}
			localModelNode.Status.ModelStatus = map[string]v1alpha1.ModelStatus{}
			for _, key := range keys {
				localModelNode.Status.ModelStatus[key] = v1alpha1.ModelDownloaded
			}
			return k8sClient.Status().Update(ctx, localModelNode)
		})).To(Succeed())
	}
	// The pv-protection finalizer keeps a deleted volume or claim in envtest.
	gone := func(ctx SpecContext, obj client.Object) func() bool {
		return func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)
			return errors.IsNotFound(err) || (err == nil && !obj.GetDeletionTimestamp().IsZero())
		}
	}

	It("Should cache on a Ready node labeled later, in any of the cache's node groups", func(ctx SpecContext) {
		first, second, worker := unique("first"), unique("second"), unique("worker")
		create(ctx, nodeGroup(first))
		create(ctx, nodeGroup(second))
		ready := node(worker, "none")
		create(ctx, ready)
		cache := clusterCache(unique("cache"), first, second)
		create(ctx, cache)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache)).To(Succeed())
			g.Expect(cache.Status.ModelCopies).To(Equal(&v1alpha1.ModelCopies{}))
		}, timeout, interval).Should(Succeed())

		setGroup(ctx, ready, second)
		Eventually(held(ctx, worker), timeout, interval).Should(Equal([]string{cache.Name}))
		Eventually(nodesOf(ctx, cache), timeout, interval).Should(Equal(map[string]v1alpha1.NodeStatus{worker: v1alpha1.NodeDownloadPending}))
	})

	It("Should cache once a missing node group is created", func(ctx SpecContext) {
		group, worker := unique("late"), unique("worker")
		create(ctx, node(worker, group))
		cache := clusterCache(unique("cache"), group)
		create(ctx, cache)
		Eventually(func() *v1alpha1.ModelCopies {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache)
			return cache.Status.ModelCopies
		}, timeout, interval).Should(Equal(&v1alpha1.ModelCopies{}))

		create(ctx, nodeGroup(group))
		Eventually(held(ctx, worker), timeout, interval).Should(Equal([]string{cache.Name}))
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Namespace: jobNamespace, Name: cache.Name + "-" + group}, &corev1.PersistentVolumeClaim{})
		}, timeout, interval).Should(Succeed(), "the download claim should be created")
	})

	It("Should list every model again when a LocalModelNode is deleted", func(ctx SpecContext) {
		group, worker, namespace := unique("group"), unique("worker"), unique("ns")
		create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		create(ctx, nodeGroup(group))
		create(ctx, node(worker, group))
		cluster, namespaced := clusterCache(unique("cluster"), group), namespaceCache(namespace, unique("namespaced"), group)
		create(ctx, cluster)
		create(ctx, namespaced)
		all := []string{cluster.Name, namespace + "/" + namespaced.Name}
		Eventually(held(ctx, worker), timeout, interval).Should(Equal(all))

		before := &v1alpha1.LocalModelNode{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: worker}, before)).To(Succeed())
		Expect(k8sClient.Delete(ctx, before)).To(Succeed())
		after := &v1alpha1.LocalModelNode{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: worker}, after)).To(Succeed())
			g.Expect(after.UID).NotTo(Equal(before.UID))
		}, timeout, interval).Should(Succeed())
		Expect(after.Generation).To(Equal(int64(1)), "it should be created with the whole list: the node agent deletes what the list leaves out")
		Expect(held(ctx, worker)()).To(Equal(all))
	})

	It("Should not count a node that left the group, but keep its files until the cache is deleted", func(ctx SpecContext) {
		group, stays, leaves := unique("group"), unique("stays"), unique("leaves")
		create(ctx, nodeGroup(group))
		create(ctx, node(stays, group))
		leaving := node(leaves, group)
		create(ctx, leaving)
		cache := clusterCache(unique("cache"), group)
		create(ctx, cache)
		for _, name := range []string{stays, leaves} {
			Eventually(held(ctx, name), timeout, interval).Should(Equal([]string{cache.Name}))
			markDownloaded(ctx, name, cache.Name)
		}
		Eventually(nodesOf(ctx, cache), timeout, interval).Should(Equal(map[string]v1alpha1.NodeStatus{
			stays: v1alpha1.NodeDownloaded, leaves: v1alpha1.NodeDownloaded,
		}))

		setGroup(ctx, leaving, "none")
		Eventually(nodesOf(ctx, cache), timeout, interval).Should(Equal(map[string]v1alpha1.NodeStatus{stays: v1alpha1.NodeDownloaded}))
		Expect(cache.Status.ModelCopies).To(Equal(&v1alpha1.ModelCopies{Total: 1, Available: 1}))
		Consistently(held(ctx, leaves), time.Second, interval).Should(Equal([]string{cache.Name}), "Pods on the node may still read the files")

		Expect(k8sClient.Delete(ctx, cache)).To(Succeed())
		for _, name := range []string{stays, leaves} {
			Eventually(held(ctx, name), timeout, interval).Should(BeEmpty())
		}
	})

	It("Should forget a node that left the cluster", func(ctx SpecContext) {
		group, worker := unique("group"), unique("worker")
		create(ctx, nodeGroup(group))
		removed := node(worker, group)
		create(ctx, removed)
		cache := clusterCache(unique("cache"), group)
		create(ctx, cache)
		Eventually(held(ctx, worker), timeout, interval).Should(Equal([]string{cache.Name}))
		markDownloaded(ctx, worker, cache.Name)
		Eventually(nodesOf(ctx, cache), timeout, interval).Should(HaveKeyWithValue(worker, v1alpha1.NodeDownloaded))

		Expect(k8sClient.Delete(ctx, removed)).To(Succeed())
		Eventually(nodesOf(ctx, cache), timeout, interval).Should(BeEmpty())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: worker}, &v1alpha1.LocalModelNode{}))
		}, timeout, interval).Should(BeTrue(), "its LocalModelNode should be deleted")
	})

	It("Should delete caches and their volumes after their node group is gone", func(ctx SpecContext) {
		group, worker, namespace := unique("group"), unique("worker"), unique("ns")
		create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		deleted := nodeGroup(group)
		create(ctx, deleted)
		create(ctx, node(worker, group))
		cluster, namespaced := clusterCache(unique("cluster"), group), namespaceCache(namespace, unique("namespaced"), group)
		create(ctx, cluster)
		create(ctx, namespaced)
		servingPV := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: namespaced.Name + "-" + group + "-" + namespace}}
		downloadPV := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: servingPV.Name + "-download"}}
		downloadPVC := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: jobNamespace, Name: downloadPV.Name}}
		for _, obj := range []client.Object{servingPV, downloadPV, downloadPVC} {
			Eventually(func() error { return k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj) }, timeout, interval).Should(Succeed())
		}
		Eventually(held(ctx, worker), timeout, interval).Should(HaveLen(2))

		Expect(k8sClient.Delete(ctx, deleted)).To(Succeed())
		Consistently(held(ctx, worker), time.Second, interval).Should(HaveLen(2), "files stay while the caches exist")
		for _, cache := range []client.Object{cluster, namespaced} {
			Expect(k8sClient.Delete(ctx, cache)).To(Succeed())
			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache))
			}, timeout, interval).Should(BeTrue())
		}
		for _, obj := range []client.Object{servingPV, downloadPV, downloadPVC} {
			Eventually(gone(ctx, obj), timeout, interval).Should(BeTrue(), obj.GetName())
		}
		Eventually(held(ctx, worker), timeout, interval).Should(BeEmpty())
	})

	It("Should remove the models and volumes of a node group the cache no longer names", func(ctx SpecContext) {
		keep, drop, namespace := unique("keep"), unique("drop"), unique("ns")
		keepNode, dropNode := unique("keep-node"), unique("drop-node")
		create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		for _, group := range []string{keep, drop} {
			create(ctx, nodeGroup(group))
		}
		create(ctx, node(keepNode, keep))
		create(ctx, node(dropNode, drop))
		cache := namespaceCache(namespace, unique("cache"), keep, drop)
		create(ctx, cache)
		key := namespace + "/" + cache.Name
		Eventually(held(ctx, keepNode), timeout, interval).Should(Equal([]string{key}))
		Eventually(held(ctx, dropNode), timeout, interval).Should(Equal([]string{key}))
		objects := func(group string) []client.Object {
			name := cache.Name + "-" + group
			return []client.Object{
				&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}},
				&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name + "-" + namespace}},
				&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: jobNamespace, Name: name + "-" + namespace + "-download"}},
				&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name + "-" + namespace + "-download"}},
			}
		}
		for _, obj := range slices.Concat(objects(keep), objects(drop)) {
			Eventually(func() error { return k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj) }, timeout, interval).Should(Succeed())
		}

		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache); err != nil {
				return err
			}
			cache.Spec.NodeGroups = []string{keep}
			return k8sClient.Update(ctx, cache)
		})).To(Succeed())
		Eventually(held(ctx, dropNode), timeout, interval).Should(BeEmpty())
		for _, obj := range objects(drop) {
			Eventually(gone(ctx, obj), timeout, interval).Should(BeTrue(), obj.GetName())
		}
		Consistently(held(ctx, keepNode), time.Second, interval).Should(Equal([]string{key}))
		for _, obj := range objects(keep) {
			Expect(gone(ctx, obj)()).To(BeFalse(), obj.GetName())
		}
		Eventually(nodesOf(ctx, cache), timeout, interval).Should(Equal(map[string]v1alpha1.NodeStatus{keepNode: v1alpha1.NodeDownloadPending}))
		Expect(cache.Status.PersistentVolumeClaims).To(Equal([]string{cache.Name + "-" + keep}))
	})

	It("Should end up with exactly the existing caches after concurrent changes", func(ctx SpecContext) {
		group := unique("group")
		workers := []string{unique("worker-1"), unique("worker-2"), unique("worker-3")}
		create(ctx, nodeGroup(group))
		for _, worker := range workers {
			create(ctx, node(worker, group))
		}
		var wg sync.WaitGroup
		want := []string{}
		for i := range 12 {
			cache := clusterCache(fmt.Sprintf("%s-%02d", unique("cache"), i), group)
			create(ctx, cache)
			if i%2 == 0 {
				want = append(want, cache.Name)
				continue
			}
			wg.Go(func() {
				defer GinkgoRecover()
				Expect(k8sClient.Delete(ctx, cache)).To(Succeed())
			})
		}
		wg.Wait()
		for _, worker := range workers {
			Eventually(held(ctx, worker), timeout, interval).Should(Equal(want))
		}
		for _, worker := range workers {
			Consistently(held(ctx, worker), time.Second, interval).Should(Equal(want))
		}
	})

	It("Should point a volume at its claim when the claim is deleted and created again", func(ctx SpecContext) {
		group, namespace := unique("group"), unique("ns")
		create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		create(ctx, nodeGroup(group))
		cache := namespaceCache(namespace, unique("cache"), group)
		create(ctx, cache)

		for _, claim := range []types.NamespacedName{
			{Namespace: namespace, Name: cache.Name + "-" + group},
			{Namespace: jobNamespace, Name: cache.Name + "-" + group + "-" + namespace + "-download"},
		} {
			pvc, pv := &corev1.PersistentVolumeClaim{}, &corev1.PersistentVolume{}
			Eventually(func() error { return k8sClient.Get(ctx, claim, pvc) }, timeout, interval).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName}, pv)).To(Succeed())
			Expect(pv.Spec.ClaimRef).NotTo(BeNil(), "the volume should be reserved for its claim")
			Expect(pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name).To(Equal(claim.String()))
			Expect(pv.Spec.PersistentVolumeReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimRetain))

			// What the PersistentVolume controller does when it binds them.
			pv.Spec.ClaimRef.UID = pvc.UID
			Expect(k8sClient.Update(ctx, pv)).To(Succeed())

			before := pvc.UID
			pvc.Finalizers = nil // Nothing removes pvc-protection in envtest.
			Expect(k8sClient.Update(ctx, pvc)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pvc)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, claim, pvc)).To(Succeed())
				g.Expect(pvc.UID).NotTo(Equal(before))
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName}, pv)).To(Succeed())
				g.Expect(pv.Spec.ClaimRef.UID).To(Equal(pvc.UID))
			}, timeout, interval).Should(Succeed())
		}
	})

	// Kubernetes ignores a finalizer that is added while the object is being deleted.
	It("Should delete the volumes of a cache that was deleted without its finalizer", func(ctx SpecContext) {
		group, namespace := unique("group"), unique("ns")
		create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		create(ctx, nodeGroup(group))
		cache, other := namespaceCache(namespace, unique("cache"), group), namespaceCache(namespace, unique("other"), group)
		create(ctx, cache)
		create(ctx, other)
		// The claim in the cache's namespace is owned by the cache. The rest cannot be.
		unowned := func(cache *v1alpha1.LocalModelNamespaceCache) []client.Object {
			name := cache.Name + "-" + group + "-" + namespace
			return []client.Object{
				&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name}},
				&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name + "-download"}},
				&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: jobNamespace, Name: name + "-download"}},
			}
		}
		for _, obj := range slices.Concat(unowned(cache), unowned(other)) {
			Eventually(func() error { return k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj) }, timeout, interval).Should(Succeed())
		}

		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache); err != nil {
				return err
			}
			cache.Finalizers = nil
			return k8sClient.Update(ctx, cache)
		})).To(Succeed())
		Expect(k8sClient.Delete(ctx, cache)).To(Succeed())
		for _, obj := range unowned(cache) {
			Eventually(gone(ctx, obj), timeout, interval).Should(BeTrue(), obj.GetName())
		}
		for _, obj := range unowned(other) {
			Expect(gone(ctx, obj)()).To(BeFalse(), obj.GetName())
		}
	})

	It("Should delete a cluster-scoped cache that still has the finalizer of an earlier version", func(ctx SpecContext) {
		cache := clusterCache(unique("cache"), unique("missing"))
		cache.Finalizers = []string{"local.cache.ai/localmodelcache-finalizer"}
		create(ctx, cache)
		Expect(k8sClient.Delete(ctx, cache)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache))
		}, timeout, interval).Should(BeTrue())
	})
})
