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

package localmodelcache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

func testVolume(name, namespace, claim string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{constants.LocalModelUIDLabelKey: "iris-uid"}},
		Spec:       corev1.PersistentVolumeSpec{ClaimRef: &corev1.ObjectReference{Namespace: namespace, Name: claim}},
	}
}

func testPod(namespace string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "vllm-0", Namespace: namespace},
		Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "model", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "iris-gpu1"},
		}}}},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func TestClaimsInUse(t *testing.T) {
	servingVolume := localmodelcache.ClusterCacheVolumeName(types.NamespacedName{Namespace: "team-a", Name: "iris-gpu1"})
	inUse := "LocalModelCache iris is in use: Pod team-a/vllm-0 mounts PersistentVolumeClaim iris-gpu1"
	named := func(values ...string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: corev1.LabelMetadataName, Operator: metav1.LabelSelectorOpIn, Values: values},
		}}
	}
	running := testPod("team-a", corev1.PodRunning)
	terminating := testVolume(servingVolume, "team-a", "iris-gpu1")
	terminating.Finalizers = []string{"kubernetes.io/pv-protection"}
	terminating.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}

	tests := map[string]struct {
		pod          *corev1.Pod
		volume       *corev1.PersistentVolume // team-a's by default
		unlabelled   bool
		change       func(*v1alpha1.LocalModelCacheSpec)
		wantDenied   bool // on delete
		wantUpdateOK bool
	}{
		"no Pod":                 {change: func(s *v1alpha1.LocalModelCacheSpec) { s.NamespaceSelector = named("team-b") }, wantUpdateOK: true},
		"finished Pod":           {pod: testPod("team-a", corev1.PodSucceeded), change: func(s *v1alpha1.LocalModelCacheSpec) { s.NamespaceSelector = named("team-b") }, wantUpdateOK: true},
		"Pod in other namespace": {pod: testPod("team-b", corev1.PodRunning), change: func(s *v1alpha1.LocalModelCacheSpec) { s.NamespaceSelector = named("team-b") }, wantUpdateOK: true},
		"download Job does not count": {
			pod:    testPod("jobs", corev1.PodRunning),
			volume: testVolume("iris-gpu1-download", "jobs", "iris-gpu1"),
			change: func(s *v1alpha1.LocalModelCacheSpec) { s.NodeGroups = []string{"gpu2"} }, wantUpdateOK: true,
		},
		"selector drops namespace":        {pod: running, change: func(s *v1alpha1.LocalModelCacheSpec) { s.NamespaceSelector = named("team-b") }, wantDenied: true},
		"selector keeps namespace":        {pod: running, change: func(s *v1alpha1.LocalModelCacheSpec) { s.NamespaceSelector = named("team-a") }, wantDenied: true, wantUpdateOK: true},
		"empty selector keeps it":         {pod: running, change: func(s *v1alpha1.LocalModelCacheSpec) { s.NamespaceSelector = &metav1.LabelSelector{} }, wantDenied: true, wantUpdateOK: true},
		"node group removed":              {pod: running, change: func(s *v1alpha1.LocalModelCacheSpec) { s.NodeGroups = []string{"gpu2"} }, wantDenied: true},
		"node group added":                {pod: running, change: func(s *v1alpha1.LocalModelCacheSpec) { s.NodeGroups = []string{"gpu1", "gpu2"} }, wantDenied: true, wantUpdateOK: true},
		"unrelated, label was lost":       {pod: running, unlabelled: true, change: func(s *v1alpha1.LocalModelCacheSpec) { s.ServiceAccountName = "other" }, wantDenied: true, wantUpdateOK: true},
		"terminating, unselected":         {pod: running, volume: terminating, change: func(s *v1alpha1.LocalModelCacheSpec) { s.NamespaceSelector = named("team-b") }, wantDenied: true, wantUpdateOK: true},
		"terminating, node group removed": {pod: running, volume: terminating, change: func(s *v1alpha1.LocalModelCacheSpec) { s.NodeGroups = []string{"gpu2"} }, wantDenied: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			scheme := runtime.NewScheme()
			g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
			g.Expect(clientgoscheme.AddToScheme(scheme)).To(gomega.Succeed())
			cache := makeTestLocalModelCache()
			cache.UID = "iris-uid"
			labels := map[string]string{corev1.LabelMetadataName: "team-a", constants.ModelsNamespaceLabelKey: constants.ModelsNamespaceLabelValue}
			if tc.unlabelled {
				delete(labels, constants.ModelsNamespaceLabelKey)
			}
			objects := []client.Object{&cache, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team-a", Labels: labels}}}
			if tc.volume == nil {
				tc.volume = testVolume(servingVolume, "team-a", "iris-gpu1")
			}
			objects = append(objects, tc.volume.DeepCopy())
			if tc.pod != nil {
				objects = append(objects, tc.pod.DeepCopy())
			}
			for _, group := range []string{"gpu1", "gpu2"} {
				objects = append(objects, &v1alpha1.LocalModelNodeGroup{
					ObjectMeta: metav1.ObjectMeta{Name: group},
					Spec:       v1alpha1.LocalModelNodeGroupSpec{StorageLimit: resource.MustParse("10Gi")},
				})
			}
			validator := LocalModelCacheValidator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()}

			_, err := validator.ValidateDelete(t.Context(), &cache)
			if tc.wantDenied {
				g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(inUse)))
			} else {
				g.Expect(err).NotTo(gomega.HaveOccurred())
			}

			updated := cache.DeepCopy()
			tc.change(&updated.Spec)
			_, err = validator.ValidateUpdate(t.Context(), &cache, updated)
			if tc.wantUpdateOK {
				g.Expect(err).NotTo(gomega.HaveOccurred())
			} else {
				g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(inUse)))
			}
		})
	}
}

func TestInvalidNamespaceSelector(t *testing.T) {
	g := gomega.NewWithT(t)
	cache := makeTestLocalModelCache()
	invalid := cache.DeepCopy()
	invalid.Spec.NamespaceSelector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "a", Operator: "Near"}}}
	validator := LocalModelCacheValidator{}
	_, err := validator.ValidateCreate(t.Context(), invalid)
	g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("namespaceSelector is invalid")))
	_, err = validator.ValidateUpdate(t.Context(), &cache, invalid)
	g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("namespaceSelector is invalid")))
}

// The check must fit in an admission request, so it cannot list the Pods of one namespace at a time.
func TestInUseCheckWithManyNamespaces(t *testing.T) {
	g := gomega.NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(gomega.Succeed())
	cache := makeTestLocalModelCache()
	cache.UID = "iris-uid"
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for i := range 200 {
		key := types.NamespacedName{Namespace: fmt.Sprintf("team-%d", i), Name: "iris-gpu1"}
		builder = builder.WithObjects(testVolume(localmodelcache.ClusterCacheVolumeName(key), key.Namespace, key.Name))
	}
	slow := interceptor.NewClient(builder.WithObjects(testPod("team-150", corev1.PodRunning)).Build(), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*corev1.PodList); ok {
				time.Sleep(20 * time.Millisecond) // a slow API server
			}
			return c.List(ctx, list, opts...)
		},
	})
	validator := LocalModelCacheValidator{Client: slow}
	start := time.Now()
	_, err := validator.ValidateDelete(t.Context(), &cache)
	g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("Pod team-150/vllm-0")))
	g.Expect(time.Since(start)).To(gomega.BeNumerically("<", 2*time.Second), "one after another, the lists take 3s")
}

func TestClaimOfAnotherLocalModelCache(t *testing.T) {
	g := gomega.NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
	other := makeTestLocalModelCacheWithDifferentStorageURI()
	other.Name, other.Spec.NodeGroups = "iris-gpu", []string{"large"}
	validator := LocalModelCacheValidator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&other).Build()}
	cache := makeTestLocalModelCache()
	_, err := validator.ValidateCreate(t.Context(), &cache)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	clashing := cache.DeepCopy()
	clashing.Spec.NodeGroups = []string{"gpu1", "gpu-large"}
	for _, err := range []error{
		func() error { _, err := validator.ValidateCreate(t.Context(), clashing); return err }(),
		func() error { _, err := validator.ValidateUpdate(t.Context(), &cache, clashing); return err }(),
	} {
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("PersistentVolumeClaim iris-gpu-large is that of LocalModelCache iris-gpu")))
	}
}
