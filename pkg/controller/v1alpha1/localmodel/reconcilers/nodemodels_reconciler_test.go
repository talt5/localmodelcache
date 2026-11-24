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
	"context"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

func TestModelsForNode(t *testing.T) {
	cache := func(namespace, name string, groups ...string) LocalModelParams {
		return LocalModelParams{Namespace: namespace, Name: name, SourceModelUri: "hf://org/" + name, NodeGroups: groups}
	}
	info := func(namespace, name, group string) v1alpha1.LocalModelInfo {
		return v1alpha1.LocalModelInfo{Namespace: namespace, ModelName: name, SourceModelUri: "hf://org/" + name, NodeGroup: group}
	}
	tests := map[string]struct {
		nodeGroups []string
		caches     []LocalModelParams
		current    []v1alpha1.LocalModelInfo
		want       []v1alpha1.LocalModelInfo
	}{
		"every cache of the node's groups": {
			nodeGroups: []string{"a", "b"},
			caches:     []LocalModelParams{cache("", "one", "a"), cache("ns", "two", "b"), cache("ns", "three", "c")},
			want:       []v1alpha1.LocalModelInfo{info("", "one", "a"), info("ns", "two", "b")},
		},
		"any of the cache's groups, in spec order": {
			nodeGroups: []string{"a", "b"},
			caches:     []LocalModelParams{cache("", "one", "c", "b", "a")},
			want:       []v1alpha1.LocalModelInfo{info("", "one", "b")},
		},
		"a deleted cache is dropped": {
			nodeGroups: []string{"a"},
			current:    []v1alpha1.LocalModelInfo{info("", "gone", "a")},
			want:       []v1alpha1.LocalModelInfo{},
		},
		"a node that left the group keeps its models": {
			caches:  []LocalModelParams{cache("", "one", "a"), cache("", "never-held", "a")},
			current: []v1alpha1.LocalModelInfo{info("", "one", "a")},
			want:    []v1alpha1.LocalModelInfo{info("", "one", "a")},
		},
		"a group removed from the cache drops its models": {
			caches:  []LocalModelParams{cache("", "one", "b")},
			current: []v1alpha1.LocalModelInfo{info("", "one", "a")},
			want:    []v1alpha1.LocalModelInfo{},
		},
		"caches of the same name do not mix": {
			nodeGroups: []string{"b"},
			caches:     []LocalModelParams{cache("", "one", "a"), cache("ns", "one", "b")},
			current:    []v1alpha1.LocalModelInfo{info("ns", "one", "b")},
			want:       []v1alpha1.LocalModelInfo{info("ns", "one", "b")},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := modelsForNode(sets.New(tc.nodeGroups...), tc.caches, tc.current)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("modelsForNode() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestModelsForNodeCopiesCredentials(t *testing.T) {
	cache := LocalModelParams{
		Name: "cache", SourceModelUri: "oci://registry.example/model:v1", NodeGroups: []string{"a"},
		ServiceAccountName: "downloader",
		ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "reg-cred-b"}},
		Storage:            &v1alpha1.LocalModelStorageSpec{StorageKey: ptr.To("key")},
	}
	current := []v1alpha1.LocalModelInfo{{
		ModelName: "cache", SourceModelUri: cache.SourceModelUri, NodeGroup: "a",
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: "reg-cred-a"}},
	}}
	got := modelsForNode(sets.New("a"), []LocalModelParams{cache}, current)
	want := []v1alpha1.LocalModelInfo{{
		ModelName: "cache", SourceModelUri: cache.SourceModelUri, NodeGroup: "a",
		ServiceAccountName: "downloader", ImagePullSecrets: cache.ImagePullSecrets, Storage: cache.Storage,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("modelsForNode() = %+v, want %+v", got, want)
	}
}

func testCache(name string, groups ...string) *v1alpha1.LocalModelCache {
	return &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: "hf://org/" + name, NodeGroups: groups},
	}
}

// reconcileWorker reconciles the LocalModelNode "worker".
func reconcileWorker(t *testing.T, cl client.Client, apiReader client.Reader) error {
	t.Helper()
	reconciler := &NodeModelsReconciler{Client: cl, APIReader: apiReader, Log: logr.Discard()}
	_, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "worker"}})
	return err
}

// heldByWorker returns the models of the LocalModelNode "worker".
func heldByWorker(t *testing.T, cl client.Reader) []string {
	t.Helper()
	localModelNode := &v1alpha1.LocalModelNode{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: "worker"}, localModelNode); err != nil {
		t.Fatalf("get LocalModelNode: %v", err)
	}
	names := make([]string, 0, len(localModelNode.Spec.LocalModels))
	for i := range localModelNode.Spec.LocalModels {
		names = append(names, localModelNode.Spec.LocalModels[i].GetStatusKey())
	}
	return names
}

func TestNodeModelsReconcilerCreatesTheWholeList(t *testing.T) {
	deleting := testCache("deleting", "a")
	deleting.Finalizers, deleting.DeletionTimestamp = []string{"test/finalizer"}, ptr.To(metav1.Now())
	shared := &v1alpha1.LocalModelNamespaceCache{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: "shared"},
		Spec:       v1alpha1.LocalModelNamespaceCacheSpec{SourceModelUri: "hf://org/shared", PVCRef: ptr.To("claim")},
	}
	namespaced := &v1alpha1.LocalModelNamespaceCache{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: "alpha"},
		Spec:       v1alpha1.LocalModelNamespaceCacheSpec{SourceModelUri: "hf://org/alpha", NodeGroups: []string{"a"}},
	}
	// One node group with an invalid affinity must not hold up the others.
	invalid := testNodeGroup("invalid")
	invalid.Spec.PersistentVolumeSpec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Operator = "Nonsense"

	cl := newLocalModelCacheClient(t, testNodeGroup("a"), invalid, testNode("worker", "a", corev1.ConditionTrue),
		testCache("zulu", "a"), testCache("elsewhere", "b"), testCache("broken", "invalid"), deleting, shared, namespaced)
	if err := reconcileWorker(t, cl, cl); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got, want := heldByWorker(t, cl), []string{"zulu", "team/alpha"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
}

func TestNodeModelsReconcilerSkipsNodesWithoutModels(t *testing.T) {
	cl := newLocalModelCacheClient(t, testNodeGroup("a"), testNode("worker", "other", corev1.ConditionTrue), testCache("one", "a"))
	if err := reconcileWorker(t, cl, cl); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	err := cl.Get(t.Context(), types.NamespacedName{Name: "worker"}, &v1alpha1.LocalModelNode{})
	if !apierr.IsNotFound(err) {
		t.Fatalf("get LocalModelNode: %v, want not found", err)
	}
}

func TestNodeModelsReconcilerEmptiesTheList(t *testing.T) {
	// The API rejects a null list, so it must be patched to an empty one.
	held := &v1alpha1.LocalModelNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Spec: v1alpha1.LocalModelNodeSpec{LocalModels: []v1alpha1.LocalModelInfo{
			{ModelName: "ghost", SourceModelUri: "hf://org/ghost", NodeGroup: "a"},
		}},
	}
	cl := newLocalModelCacheClient(t, testNodeGroup("a"), testNode("worker", "a", corev1.ConditionTrue), held)
	if err := reconcileWorker(t, cl, cl); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := heldByWorker(t, cl); len(got) != 0 {
		t.Fatalf("models = %v, want none", got)
	}
}

// staleReader returns an older copy of a LocalModelNode, as an informer that lags behind does.
type staleReader struct {
	client.Client
	stale *v1alpha1.LocalModelNode
}

func (c *staleReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if localModelNode, ok := obj.(*v1alpha1.LocalModelNode); ok {
		c.stale.DeepCopyInto(localModelNode)
		return nil
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestNodeModelsReconcilerNeverOverwritesANewerList(t *testing.T) {
	held := &v1alpha1.LocalModelNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Spec:       v1alpha1.LocalModelNodeSpec{LocalModels: []v1alpha1.LocalModelInfo{}},
	}
	cl := newLocalModelCacheClient(t, testNodeGroup("a"), testNode("worker", "a", corev1.ConditionTrue), testCache("one", "a"), held)
	stale := &v1alpha1.LocalModelNode{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: "worker"}, stale); err != nil {
		t.Fatalf("get LocalModelNode: %v", err)
	}
	newer := stale.DeepCopy()
	newer.Spec.LocalModels = []v1alpha1.LocalModelInfo{{ModelName: "newer", SourceModelUri: "hf://org/newer"}}
	if err := cl.Update(t.Context(), newer); err != nil {
		t.Fatalf("update LocalModelNode: %v", err)
	}

	err := reconcileWorker(t, &staleReader{Client: cl, stale: stale}, cl)
	if !apierr.IsConflict(err) {
		t.Fatalf("Reconcile() error = %v, want a conflict", err)
	}
	if got, want := heldByWorker(t, cl), []string{"newer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
}

func TestNodeModelsReconcilerRemovedNode(t *testing.T) {
	held := func() *v1alpha1.LocalModelNode {
		return &v1alpha1.LocalModelNode{
			ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: "uid"},
			Spec:       v1alpha1.LocalModelNodeSpec{LocalModels: []v1alpha1.LocalModelInfo{{ModelName: "one", SourceModelUri: "hf://org/one"}}},
		}
	}
	t.Run("is deleted", func(t *testing.T) {
		cl := newLocalModelCacheClient(t, held(), testCache("one", "a"))
		if err := reconcileWorker(t, cl, cl); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if err := cl.Get(t.Context(), types.NamespacedName{Name: "worker"}, &v1alpha1.LocalModelNode{}); !apierr.IsNotFound(err) {
			t.Fatalf("get LocalModelNode: %v, want not found", err)
		}
	})
	t.Run("is kept while the Node informer lags", func(t *testing.T) {
		cl := newLocalModelCacheClient(t, held(), testCache("one", "a"))
		apiReader := newLocalModelCacheClient(t, testNode("worker", "a", corev1.ConditionTrue))
		if err := reconcileWorker(t, cl, apiReader); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if got, want := heldByWorker(t, cl), []string{"one"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("models = %v, want %v", got, want)
		}
	})
}
