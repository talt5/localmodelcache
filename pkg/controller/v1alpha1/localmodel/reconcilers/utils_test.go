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

package reconcilers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

// nodeListErrorClient fails every Node list.
type nodeListErrorClient struct {
	client.Client
	err error
}

func (c *nodeListErrorClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*corev1.NodeList); ok {
		return c.err
	}
	return c.Client.List(ctx, list, opts...)
}

func newLocalModelCacheClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add LocalModelCache scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.LocalModelCache{}, &v1alpha1.LocalModelNode{}).
		WithObjects(objs...).
		Build()
}

const testSourceModelUri = "hf://org/model"

func testNodeGroup(name string) *v1alpha1.LocalModelNodeGroup {
	return &v1alpha1.LocalModelNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.LocalModelNodeGroupSpec{PersistentVolumeSpec: corev1.PersistentVolumeSpec{
			NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "group", Operator: corev1.NodeSelectorOpIn, Values: []string{name}}},
			}}}},
		}},
	}
}

func testNode(name, group string, ready corev1.ConditionStatus) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"group": group}},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}},
	}
}

func TestReconcileCacheStatusRecordsMountReferencesWithoutNodeGroups(t *testing.T) {
	cache := &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: testSourceModelUri},
	}
	cl := newLocalModelCacheClient(t, cache)

	err := ReconcileCacheStatus(t.Context(), cl, logr.Discard(), cache, nil, nil, []string{"cache-gpu"}, v1alpha1.GetStorageSubPath(testSourceModelUri, nil))
	if err != nil {
		t.Fatalf("ReconcileCacheStatus() error = %v", err)
	}

	persisted := &v1alpha1.LocalModelCache{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: "cache"}, persisted); err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if want := v1alpha1.GetStorageSubPath(testSourceModelUri, nil); persisted.Status.SubPath != want {
		t.Fatalf("Status.SubPath = %q, want %q", persisted.Status.SubPath, want)
	}
	if !reflect.DeepEqual(persisted.Status.PersistentVolumeClaims, []string{"cache-gpu"}) {
		t.Fatalf("Status.PersistentVolumeClaims = %#v, want [cache-gpu]", persisted.Status.PersistentVolumeClaims)
	}
}

func TestReconcileCacheStatusKeepsNodesAndRecordsMountReferencesOnError(t *testing.T) {
	known := map[string]v1alpha1.NodeStatus{"worker": v1alpha1.NodeDownloaded}
	cache := &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: testSourceModelUri},
		Status:     v1alpha1.LocalModelCacheStatus{NodeStatus: known},
	}
	listErr := errors.New("node list failed")
	cl := &nodeListErrorClient{Client: newLocalModelCacheClient(t, cache), err: listErr}

	err := ReconcileCacheStatus(t.Context(), cl, logr.Discard(), cache, nil,
		[]v1alpha1.LocalModelNodeGroup{*testNodeGroup("gpu")}, nil, v1alpha1.GetStorageSubPath(testSourceModelUri, nil))
	if !errors.Is(err, listErr) {
		t.Fatalf("ReconcileCacheStatus() error = %v, want %v", err, listErr)
	}

	persisted := &v1alpha1.LocalModelCache{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: "cache"}, persisted); err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if want := v1alpha1.GetStorageSubPath(testSourceModelUri, nil); persisted.Status.SubPath != want {
		t.Fatalf("Status.SubPath = %q, want %q recorded despite the node error", persisted.Status.SubPath, want)
	}
	if !reflect.DeepEqual(persisted.Status.NodeStatus, known) {
		t.Fatalf("Status.NodeStatus = %#v, want the last known state", persisted.Status.NodeStatus)
	}
}

func TestReconcileCacheStatusClearsStaleSubPath(t *testing.T) {
	cache := &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: testSourceModelUri},
		Status:     v1alpha1.LocalModelCacheStatus{SubPath: v1alpha1.GetStorageSubPath(testSourceModelUri, nil)},
	}
	cl := newLocalModelCacheClient(t, cache)

	if err := ReconcileCacheStatus(t.Context(), cl, logr.Discard(), cache, nil, nil, []string{"cache-gpu"}, ""); err != nil {
		t.Fatalf("ReconcileCacheStatus() error = %v", err)
	}

	persisted := &v1alpha1.LocalModelCache{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: "cache"}, persisted); err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if persisted.Status.SubPath != "" {
		t.Fatalf("Status.SubPath = %q, want it cleared", persisted.Status.SubPath)
	}
}

func TestReconcileCacheStatusCountsOnlyNodesInItsGroups(t *testing.T) {
	cache := &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: testSourceModelUri, NodeGroups: []string{"a", "b"}},
		Status: v1alpha1.LocalModelCacheStatus{NodeStatus: map[string]v1alpha1.NodeStatus{
			"deleted":   v1alpha1.NodeDownloaded,
			"unlabeled": v1alpha1.NodeDownloaded,
		}},
	}
	params := ExtractLocalModelParams(cache, nil)
	holds := func(node string, status v1alpha1.ModelStatus) *v1alpha1.LocalModelNode {
		return &v1alpha1.LocalModelNode{
			ObjectMeta: metav1.ObjectMeta{Name: node},
			Spec:       v1alpha1.LocalModelNodeSpec{LocalModels: []v1alpha1.LocalModelInfo{params.modelInfo("a")}},
			Status:     v1alpha1.LocalModelNodeStatus{ModelStatus: map[string]v1alpha1.ModelStatus{"cache": status}},
		}
	}
	// The status of an earlier cache of the same name, which had another URI.
	stale := holds("stale", v1alpha1.ModelDownloaded)
	stale.Spec.LocalModels[0].SourceModelUri = "hf://org/other"

	cl := newLocalModelCacheClient(t, cache,
		testNode("done-a", "a", corev1.ConditionTrue), holds("done-a", v1alpha1.ModelDownloaded),
		testNode("failed-b", "b", corev1.ConditionTrue), holds("failed-b", v1alpha1.ModelDownloadError),
		testNode("down-done", "a", corev1.ConditionUnknown), holds("down-done", v1alpha1.ModelDownloaded),
		testNode("down-busy", "a", corev1.ConditionUnknown), holds("down-busy", v1alpha1.ModelDownloading),
		testNode("new", "b", corev1.ConditionTrue),
		testNode("stale", "a", corev1.ConditionTrue), stale,
		testNode("unlabeled", "", corev1.ConditionTrue), holds("unlabeled", v1alpha1.ModelDownloaded),
		holds("deleted", v1alpha1.ModelDownloaded),
	)

	if err := ReconcileCacheStatus(t.Context(), cl, logr.Discard(), cache, nil,
		[]v1alpha1.LocalModelNodeGroup{*testNodeGroup("a"), *testNodeGroup("b")}, nil, ""); err != nil {
		t.Fatalf("ReconcileCacheStatus() error = %v", err)
	}
	persisted := &v1alpha1.LocalModelCache{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: "cache"}, persisted); err != nil {
		t.Fatalf("get cache: %v", err)
	}
	want := map[string]v1alpha1.NodeStatus{
		"done-a":    v1alpha1.NodeDownloaded,
		"failed-b":  v1alpha1.NodeDownloadError,
		"down-done": v1alpha1.NodeDownloaded,
		"down-busy": v1alpha1.NodeNotReady,
		"new":       v1alpha1.NodeDownloadPending,
		"stale":     v1alpha1.NodeDownloadPending,
	}
	if !reflect.DeepEqual(persisted.Status.NodeStatus, want) {
		t.Fatalf("Status.NodeStatus = %#v, want %#v", persisted.Status.NodeStatus, want)
	}
	if got, want := *persisted.Status.ModelCopies, (v1alpha1.ModelCopies{Total: 6, Available: 2, Failed: 1}); got != want {
		t.Fatalf("Status.ModelCopies = %+v, want %+v", got, want)
	}
}

func TestServingVolumeSpec(t *testing.T) {
	subPath := v1alpha1.GetStorageSubPath(testSourceModelUri, nil)
	hostPath := corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{
		Path: "/mnt/models/" + subPath, Type: ptr.To(corev1.HostPathDirectory),
	}}
	tests := map[string]struct {
		source  corev1.PersistentVolumeSource
		want    corev1.PersistentVolumeSource
		wantErr bool
	}{
		// The kubelet would relabel and chown a local volume for the first Pod that mounts it.
		"local becomes hostPath": {
			source: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/mnt/models", FSType: ptr.To("xfs")}},
			want:   hostPath,
		},
		"trailing slash": {
			source: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/mnt/models/"}},
			want:   hostPath,
		},
		"hostPath never creates the folder": {
			source: corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{
				Path: "/mnt/models", Type: ptr.To(corev1.HostPathDirectoryOrCreate),
			}},
			want: hostPath,
		},
		"other volume types are rejected": {
			source:  corev1.PersistentVolumeSource{NFS: &corev1.NFSVolumeSource{Server: "nfs", Path: "/models"}},
			wantErr: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			nodeGroup := testNodeGroup("gpu")
			nodeGroup.Spec.PersistentVolumeSpec.StorageClassName = "local-storage"
			nodeGroup.Spec.PersistentVolumeSpec.PersistentVolumeSource = tc.source
			nodeGroup.Spec.PersistentVolumeSpec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete
			nodeGroup.Spec.PersistentVolumeSpec.MountOptions = []string{"noatime"}
			before := nodeGroup.DeepCopy()

			got, err := servingVolumeSpec(nodeGroup, subPath)
			if (err != nil) != tc.wantErr {
				t.Fatalf("servingVolumeSpec() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(nodeGroup, before) {
				t.Fatal("servingVolumeSpec() modified the node group")
			}
			if tc.wantErr {
				return
			}
			want := before.Spec.PersistentVolumeSpec.DeepCopy()
			want.PersistentVolumeSource = tc.want
			want.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
			want.MountOptions = nil // The kubelet rejects them on a hostPath volume.
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("servingVolumeSpec() = %#v, want %#v", got, want)
			}
		})
	}
}
