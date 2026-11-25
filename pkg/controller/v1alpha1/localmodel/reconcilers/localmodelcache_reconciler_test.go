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
	"reflect"
	"slices"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

func newLocalModelReconciler(t *testing.T, objects ...client.Object) *LocalModelReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return &LocalModelReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
		Log:    logr.Discard(),
		Scheme: scheme,
	}
}

func modelsNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{constants.ModelsNamespaceLabelKey: constants.ModelsNamespaceLabelValue},
	}}
}

func requestNames(requests []reconcile.Request) []string {
	names := make([]string, 0, len(requests))
	for _, request := range requests {
		names = append(names, request.Name)
	}
	slices.Sort(names)
	return names
}

func TestWorkloadNamespacesLeavesOutTheJobNamespace(t *testing.T) {
	c := newLocalModelReconciler(t, modelsNamespace("jobs"), modelsNamespace("team-a"),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other"}})
	namespaces, err := c.workloadNamespaces(t.Context(), &v1alpha1.LocalModelCache{}, &localmodelcache.LocalModelConfig{JobNamespace: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(namespaces) != 1 || namespaces[0].Name != "team-a" {
		t.Fatalf("workloadNamespaces() = %v, want team-a", namespaces)
	}
}

func TestEventsReconcileTheAffectedCaches(t *testing.T) {
	cache := func(name, group string, selector *metav1.LabelSelector) *v1alpha1.LocalModelCache {
		return &v1alpha1.LocalModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.LocalModelCacheSpec{NodeGroups: []string{group}, NamespaceSelector: selector},
		}
	}
	c := newLocalModelReconciler(t, cache("default", "gpu", nil), cache("all", "gpu", &metav1.LabelSelector{}),
		cache("other", "cpu", &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}))
	tests := map[string]struct {
		got  func() []string
		want []string
	}{
		"labelled namespace": {func() []string {
			return requestNames(c.namespaceFunc(t.Context(), modelsNamespace("team-a")))
		}, []string{"all", "default"}},
		"plain namespace": {func() []string {
			return requestNames(c.namespaceFunc(t.Context(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "x"}}))
		}, []string{"all"}},
		"deleted claim of a cache": {func() []string {
			return requestNames(c.deletedClaimFunc(t.Context(), &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "other-cpu", Namespace: "team-a"}}))
		}, []string{"other"}},
		"deleted claim of something else": {func() []string {
			return requestNames(c.deletedClaimFunc(t.Context(), &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "team-a"}}))
		}, []string{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tc.got(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNamespaceChangedPredicate(t *testing.T) {
	plain := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team-a"}}
	annotated := plain.DeepCopy()
	annotated.Annotations = map[string]string{"a": "b"}
	terminating := plain.DeepCopy()
	terminating.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
	for name, tc := range map[string]struct {
		changed *corev1.Namespace
		want    bool
	}{
		"annotation": {annotated, false},
		"label":      {modelsNamespace("team-a"), true},
		"deletion":   {terminating, true},
	} {
		if got := NamespaceChangedPredicate().Update(event.UpdateEvent{ObjectOld: plain, ObjectNew: tc.changed}); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestDeletedClaimReconcilesNamespaceCaches(t *testing.T) {
	cache := func(namespace, name string) *v1alpha1.LocalModelNamespaceCache {
		return &v1alpha1.LocalModelNamespaceCache{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
			Spec:       v1alpha1.LocalModelNamespaceCacheSpec{NodeGroups: []string{"gpu"}},
		}
	}
	c := &LocalModelNamespaceCacheReconciler{
		Client: newLocalModelReconciler(t, cache("team-a", "one"), cache("team-a", "two"), cache("team-b", "one")).Client,
		Log:    logr.Discard(),
	}
	got := c.deletedClaimFuncNamespaceCache(t.Context(), &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "one-gpu"}})
	want := []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: "team-a", Name: "one"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestYieldClaim(t *testing.T) {
	key := types.NamespacedName{Namespace: "team-a", Name: "one-gpu"}
	cache := &v1alpha1.LocalModelNamespaceCache{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "one"},
		Spec:       v1alpha1.LocalModelNamespaceCacheSpec{NodeGroups: []string{"gpu"}},
	}
	deleted := cache.DeepCopy()
	deleted.Finalizers = []string{NamespaceCacheFinalizerName}
	deleted.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
	elsewhere := cache.DeepCopy()
	elsewhere.Namespace = "team-b"
	claim := func(volume string) *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
			Spec:       corev1.PersistentVolumeClaimSpec{VolumeName: volume},
		}
	}
	for name, tc := range map[string]struct {
		objects []client.Object
		yields  bool
	}{
		"nobody else":             {nil, false},
		"other namespace":         {[]client.Object{elsewhere}, false},
		"cache is being deleted":  {[]client.Object{deleted}, false},
		"claim is already ours":   {[]client.Object{cache, claim("team-a.one-gpu")}, false},
		"claim was deleted":       {[]client.Object{cache}, true},
		"claim is of other cache": {[]client.Object{cache, claim("one-gpu-team-a")}, true},
	} {
		err := newLocalModelReconciler(t, tc.objects...).yieldClaim(t.Context(), "team-a.one-gpu", key)
		if (err != nil) != tc.yields {
			t.Errorf("%s: yieldClaim() = %v, want error %v", name, err, tc.yields)
		}
	}
}
