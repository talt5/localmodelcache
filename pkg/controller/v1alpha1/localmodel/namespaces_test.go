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
	"context"
	"slices"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
)

var _ = Describe("LocalModelCache claims in namespaces", func() {
	const (
		timeout  = 10 * time.Second
		interval = 250 * time.Millisecond
		uri      = "hf://org/shared"
	)
	var (
		ctx    context.Context
		id     string
		groupA string
		groupB string
	)

	// objects are released by hand: envtest has no controller that removes protection finalizers.
	release := func(obj client.Object) {
		_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				return client.IgnoreNotFound(err)
			}
			obj.SetFinalizers(nil)
			return k8sClient.Update(ctx, obj)
		})
	}
	newGroup := func(name string) {
		group := &v1alpha1.LocalModelNodeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.LocalModelNodeGroupSpec{
				StorageLimit: resource.MustParse("10Gi"),
				PersistentVolumeSpec: corev1.PersistentVolumeSpec{
					AccessModes:            []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Capacity:               corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")},
					StorageClassName:       "standard",
					PersistentVolumeSource: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/mnt/models"}},
					NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
						MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "group", Operator: corev1.NodeSelectorOpIn, Values: []string{name}}},
					}}}},
				},
				PersistentVolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, group)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { _ = k8sClient.Delete(ctx, group) })
	}
	newNamespace := func(prefix string, labelled bool) string {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: prefix + "-" + id}}
		if labelled {
			namespace.Labels = map[string]string{constants.ModelsNamespaceLabelKey: constants.ModelsNamespaceLabelValue}
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { _ = k8sClient.Delete(ctx, namespace) })
		return namespace.Name
	}
	setLabel := func(name, value string) {
		namespace := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, namespace)).To(Succeed())
		patch := client.MergeFrom(namespace.DeepCopy())
		if namespace.Labels == nil {
			namespace.Labels = map[string]string{}
		}
		namespace.Labels[constants.ModelsNamespaceLabelKey] = value
		Expect(k8sClient.Patch(ctx, namespace, patch)).To(Succeed())
	}
	newCache := func(selector *metav1.LabelSelector, groups ...string) *v1alpha1.LocalModelCache {
		cache := &v1alpha1.LocalModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-" + id},
			Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: uri, ModelSize: resource.MustParse("1Gi"), NodeGroups: groups, NamespaceSelector: selector},
		}
		Expect(k8sClient.Create(ctx, cache)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { _ = k8sClient.Delete(ctx, cache) })
		return cache
	}
	names := func(values ...string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: corev1.LabelMetadataName, Operator: metav1.LabelSelectorOpIn, Values: values},
		}}
	}
	claimKey := func(cache *v1alpha1.LocalModelCache, namespace, group string) types.NamespacedName {
		return types.NamespacedName{Namespace: namespace, Name: cache.Name + "-" + group}
	}
	expectClaims := func(cache *v1alpha1.LocalModelCache, want ...types.NamespacedName) {
		GinkgoHelper()
		list := make([]string, 0, len(want))
		for _, key := range want {
			list = append(list, key.String())
		}
		slices.Sort(list)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache)).To(Succeed())
			g.Expect(cache.Status.ModelCopies).NotTo(BeNil())
			if len(list) == 0 {
				g.Expect(cache.Status.PersistentVolumeClaims).To(BeEmpty())
				return
			}
			g.Expect(cache.Status.PersistentVolumeClaims).To(Equal(list))
		}, timeout, interval).Should(Succeed())
	}
	expectExists := func(key types.NamespacedName) *corev1.PersistentVolumeClaim {
		GinkgoHelper()
		claim := &corev1.PersistentVolumeClaim{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, claim)).To(Succeed())
			g.Expect(claim.DeletionTimestamp.IsZero()).To(BeTrue())
		}, timeout, interval).Should(Succeed())
		return claim
	}
	expectRemoved := func(key types.NamespacedName) {
		GinkgoHelper()
		removed := func(obj client.Object, key types.NamespacedName) {
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, obj)
				return apierr.IsNotFound(err) || (err == nil && !obj.GetDeletionTimestamp().IsZero())
			}, timeout, interval).Should(BeTrue())
		}
		removed(&corev1.PersistentVolumeClaim{}, key)
		removed(&corev1.PersistentVolume{}, types.NamespacedName{Name: key.Namespace + "." + key.Name})
	}
	releaseBoth := func(key types.NamespacedName) {
		release(&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}})
		release(&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: key.Namespace + "." + key.Name}})
	}

	BeforeEach(func() {
		ctx = context.Background()
		id = strconv.FormatInt(time.Now().UnixNano(), 10)
		groupA, groupB = "a-"+id, "b-"+id
		newGroup(groupA)
		newGroup(groupB)
	})

	It("Should create them in the namespaces with the default label", func() {
		one, two, other := newNamespace("one", true), newNamespace("two", true), newNamespace("other", false)
		cache := newCache(nil, groupA, groupB)
		expectClaims(cache, claimKey(cache, one, groupA), claimKey(cache, one, groupB), claimKey(cache, two, groupA), claimKey(cache, two, groupB))
		Expect(cache.Status.SubPath).To(Equal(v1alpha1.GetStorageSubPath(uri, nil)))

		for _, namespace := range []string{one, two} {
			key := claimKey(cache, namespace, groupA)
			claim := expectExists(key)
			Expect(metav1.IsControlledBy(claim, cache)).To(BeTrue())
			Expect(claim.Spec.VolumeName).To(Equal(namespace + "." + key.Name))
			volume := &corev1.PersistentVolume{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: claim.Spec.VolumeName}, volume)).To(Succeed())
			Expect(metav1.IsControlledBy(volume, cache)).To(BeTrue())
			Expect(volume.Spec.HostPath.Path).To(Equal("/mnt/models/" + v1alpha1.GetStorageSubPath(uri, nil)))
			Expect(volume.Spec.ClaimRef.Namespace).To(Equal(namespace))
			Expect(volume.Spec.ClaimRef.Name).To(Equal(key.Name))
			Expect(volume.Spec.PersistentVolumeReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimRetain))
			Expect(volume.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values).To(Equal([]string{groupA}))
		}
		err := k8sClient.Get(ctx, claimKey(cache, other, groupA), &corev1.PersistentVolumeClaim{})
		Expect(apierr.IsNotFound(err)).To(BeTrue())
	})

	It("Should follow the labels of namespaces", func() {
		cache := newCache(nil, groupA)
		expectClaims(cache)
		created := newNamespace("created", true)
		expectClaims(cache, claimKey(cache, created, groupA))
		labelled := newNamespace("labelled", false)
		setLabel(labelled, constants.ModelsNamespaceLabelValue)
		expectClaims(cache, claimKey(cache, created, groupA), claimKey(cache, labelled, groupA))

		setLabel(labelled, "disabled")
		expectClaims(cache, claimKey(cache, created, groupA))
		expectRemoved(claimKey(cache, labelled, groupA))
		// The label returns while the claim waits for a Pod, which then finishes.
		setLabel(labelled, constants.ModelsNamespaceLabelValue)
		releaseBoth(claimKey(cache, labelled, groupA))
		expectExists(claimKey(cache, labelled, groupA))
		expectClaims(cache, claimKey(cache, created, groupA), claimKey(cache, labelled, groupA))
	})

	It("Should follow the selector and the node groups", func() {
		one, two := newNamespace("one", false), newNamespace("two", false)
		cache := newCache(names(one), groupA, groupB)
		expectClaims(cache, claimKey(cache, one, groupA), claimKey(cache, one, groupB))
		cache.Spec.NamespaceSelector = names(one, two)
		cache.Spec.NodeGroups = []string{groupA}
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current := &v1alpha1.LocalModelCache{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), current); err != nil {
				return err
			}
			current.Spec = cache.Spec
			return k8sClient.Update(ctx, current)
		})).To(Succeed())
		expectClaims(cache, claimKey(cache, one, groupA), claimKey(cache, two, groupA))
		expectRemoved(claimKey(cache, one, groupB))
	})

	It("Should restore a deleted claim and keep those of a missing node group", func() {
		one := newNamespace("one", true)
		cache := newCache(nil, groupA, groupB)
		key := claimKey(cache, one, groupA)
		old := expectExists(key)
		Expect(k8sClient.Delete(ctx, old)).To(Succeed())
		release(&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}})
		Eventually(func(g Gomega) {
			claim := &corev1.PersistentVolumeClaim{}
			g.Expect(k8sClient.Get(ctx, key, claim)).To(Succeed())
			g.Expect(claim.UID).NotTo(Equal(old.UID))
		}, timeout, interval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, &v1alpha1.LocalModelNodeGroup{ObjectMeta: metav1.ObjectMeta{Name: groupB}})).To(Succeed())
		expectClaims(cache, key)
		expectExists(claimKey(cache, one, groupB))
	})

	It("Should select every namespace but the job namespace with an empty selector", func() {
		one := newNamespace("one", false)
		cache := newCache(&metav1.LabelSelector{}, groupA)
		expectExists(claimKey(cache, one, groupA))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cache), cache)).To(Succeed())
			g.Expect(cache.Status.PersistentVolumeClaims).To(ContainElement(claimKey(cache, one, groupA).String()))
			g.Expect(cache.Status.PersistentVolumeClaims).NotTo(ContainElement(HavePrefix("localmodelcache-jobs/")))
		}, timeout, interval).Should(Succeed())
		download := expectExists(claimKey(cache, "localmodelcache-jobs", groupA))
		Expect(download.Spec.VolumeName).To(Equal(cache.Name + "-" + groupA + "-download"))
	})

	It("Should wait for a claim of the same name to go away", func() {
		one, two := newNamespace("one", true), newNamespace("two", true)
		key := types.NamespacedName{Namespace: one, Name: "shared-" + id + "-" + groupA}
		// The namespace cache has the same name, so its claim does too.
		namespaceCache := &v1alpha1.LocalModelNamespaceCache{
			ObjectMeta: metav1.ObjectMeta{Namespace: one, Name: "shared-" + id},
			Spec:       v1alpha1.LocalModelNamespaceCacheSpec{SourceModelUri: uri, ModelSize: resource.MustParse("1Gi"), NodeGroups: []string{groupA}},
		}
		Expect(k8sClient.Create(ctx, namespaceCache)).To(Succeed())
		taken := expectExists(key)

		cache := newCache(nil, groupA)
		expectClaims(cache, claimKey(cache, two, groupA))
		Consistently(func(g Gomega) {
			claim := &corev1.PersistentVolumeClaim{}
			g.Expect(k8sClient.Get(ctx, key, claim)).To(Succeed())
			g.Expect(claim.UID).To(Equal(taken.UID))
			g.Expect(metav1.IsControlledBy(claim, namespaceCache)).To(BeTrue())
		}, time.Second, interval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, namespaceCache)).To(Succeed())
		Eventually(func() bool {
			return apierr.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(namespaceCache), namespaceCache))
		}, timeout, interval).Should(BeTrue())
		// The garbage collector's part.
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, taken))).To(Succeed())
		release(&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}})
		expectClaims(cache, claimKey(cache, one, groupA), claimKey(cache, two, groupA))
		Expect(expectExists(key).Spec.VolumeName).To(Equal(one + "." + key.Name))
	})

	It("Should leave a namespace that is being deleted alone", func() {
		one := newNamespace("one", true)
		cache := newCache(nil, groupA)
		key := claimKey(cache, one, groupA)
		expectClaims(cache, key)
		// Without a namespace controller it stays terminating.
		Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: one}})).To(Succeed())
		expectClaims(cache)
		expectExists(key)
	})

	It("Should create none when volume management is disabled", func() {
		configMap := &corev1.ConfigMap{}
		configKey := types.NamespacedName{Name: constants.LocalModelConfigMapName, Namespace: constants.SystemNamespace}
		Expect(k8sClient.Get(ctx, configKey, configMap)).To(Succeed())
		original := configMap.Data["localModel"]
		set := func(ctx context.Context, data string) {
			Expect(k8sClient.Get(ctx, configKey, configMap)).To(Succeed())
			configMap.Data["localModel"] = data
			Expect(k8sClient.Update(ctx, configMap)).To(Succeed())
		}
		set(ctx, `{"jobNamespace": "localmodelcache-jobs", "disableVolumeManagement": true}`)
		DeferCleanup(func(ctx context.Context) { set(ctx, original) })

		one := newNamespace("one", true)
		cache := newCache(nil, groupA)
		expectClaims(cache)
		expectExists(claimKey(cache, "localmodelcache-jobs", groupA))
		Expect(apierr.IsNotFound(k8sClient.Get(ctx, claimKey(cache, one, groupA), &corev1.PersistentVolumeClaim{}))).To(BeTrue())
	})
})
