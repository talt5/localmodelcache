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

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

// nodeListErrorClient fails every Node list so GetNodesFromNodeGroup returns an error.
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
		t.Fatalf("add KServe scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.LocalModelCache{}).
		WithObjects(objs...).
		Build()
}

const testSourceModelUri = "hf://org/model"

func TestReconcileLocalModelNodeRecordsMountReferencesWithoutNodeGroups(t *testing.T) {
	cache := &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: testSourceModelUri},
	}
	cl := newLocalModelCacheClient(t, cache)

	err := ReconcileLocalModelNode(context.Background(), cl, logr.Discard(), cache, nil,
		map[string]*v1alpha1.LocalModelNodeGroup{}, []string{"cache-gpu"}, v1alpha1.GetStorageSubPath(testSourceModelUri))
	if err != nil {
		t.Fatalf("ReconcileLocalModelNode() error = %v", err)
	}

	persisted := &v1alpha1.LocalModelCache{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "cache"}, persisted); err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if want := v1alpha1.GetStorageSubPath(testSourceModelUri); persisted.Status.SubPath != want {
		t.Fatalf("Status.SubPath = %q, want %q", persisted.Status.SubPath, want)
	}
	if !reflect.DeepEqual(persisted.Status.PersistentVolumeClaims, []string{"cache-gpu"}) {
		t.Fatalf("Status.PersistentVolumeClaims = %#v, want [cache-gpu]", persisted.Status.PersistentVolumeClaims)
	}
}

func TestReconcileLocalModelNodeRecordsMountReferencesOnNodeGroupError(t *testing.T) {
	cache := &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: testSourceModelUri},
	}
	listErr := errors.New("node list failed")
	cl := &nodeListErrorClient{Client: newLocalModelCacheClient(t, cache), err: listErr}
	nodeGroups := map[string]*v1alpha1.LocalModelNodeGroup{
		"gpu": {ObjectMeta: metav1.ObjectMeta{Name: "gpu"}},
	}

	err := ReconcileLocalModelNode(context.Background(), cl, logr.Discard(), cache, nil, nodeGroups, nil, v1alpha1.GetStorageSubPath(testSourceModelUri))
	if !errors.Is(err, listErr) {
		t.Fatalf("ReconcileLocalModelNode() error = %v, want %v", err, listErr)
	}

	persisted := &v1alpha1.LocalModelCache{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "cache"}, persisted); err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if want := v1alpha1.GetStorageSubPath(testSourceModelUri); persisted.Status.SubPath != want {
		t.Fatalf("Status.SubPath = %q, want %q recorded despite the node-group error", persisted.Status.SubPath, want)
	}
	if persisted.Status.PersistentVolumeClaims != nil {
		t.Fatalf("Status.PersistentVolumeClaims = %#v, want none for a cluster-scoped cache", persisted.Status.PersistentVolumeClaims)
	}
}

func TestReconcileLocalModelNodeClearsStaleSubPath(t *testing.T) {
	cache := &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec:       v1alpha1.LocalModelCacheSpec{SourceModelUri: testSourceModelUri},
		Status:     v1alpha1.LocalModelCacheStatus{SubPath: v1alpha1.GetStorageSubPath(testSourceModelUri)},
	}
	cl := newLocalModelCacheClient(t, cache)

	if err := ReconcileLocalModelNode(t.Context(), cl, logr.Discard(), cache, nil,
		map[string]*v1alpha1.LocalModelNodeGroup{}, []string{"cache-gpu"}, ""); err != nil {
		t.Fatalf("ReconcileLocalModelNode() error = %v", err)
	}

	persisted := &v1alpha1.LocalModelCache{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: "cache"}, persisted); err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if persisted.Status.SubPath != "" {
		t.Fatalf("Status.SubPath = %q, want it cleared", persisted.Status.SubPath)
	}
}

func TestServingPersistentVolumeSpec(t *testing.T) {
	subPath := v1alpha1.GetStorageSubPath(testSourceModelUri)
	tests := map[string]struct {
		source  corev1.PersistentVolumeSource
		want    corev1.PersistentVolumeSource
		wantErr bool
	}{
		"local": {
			source: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/mnt/models"}},
			want:   corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/mnt/models/" + subPath}},
		},
		"trailing slash": {
			source: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/mnt/models/"}},
			want:   corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{Path: "/mnt/models/" + subPath}},
		},
		"hostPath never creates the folder": {
			source: corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{
				Path: "/mnt/models", Type: ptr.To(corev1.HostPathDirectoryOrCreate),
			}},
			want: corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{
				Path: "/mnt/models/" + subPath, Type: ptr.To(corev1.HostPathDirectory),
			}},
		},
		"other volume types are rejected": {
			source:  corev1.PersistentVolumeSource{NFS: &corev1.NFSVolumeSource{Server: "nfs", Path: "/models"}},
			wantErr: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			nodeGroup := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu"},
				Spec: v1alpha1.LocalModelNodeGroupSpec{PersistentVolumeSpec: corev1.PersistentVolumeSpec{
					StorageClassName:       "local-storage",
					PersistentVolumeSource: tc.source,
				}},
			}
			before := nodeGroup.DeepCopy()

			got, err := servingPersistentVolumeSpec(nodeGroup, testSourceModelUri)
			if (err != nil) != tc.wantErr {
				t.Fatalf("servingPersistentVolumeSpec() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(nodeGroup, before) {
				t.Fatal("servingPersistentVolumeSpec() modified the node group")
			}
			if tc.wantErr {
				return
			}
			if !reflect.DeepEqual(got.PersistentVolumeSource, tc.want) {
				t.Fatalf("source = %#v, want %#v", got.PersistentVolumeSource, tc.want)
			}
			if got.StorageClassName != "local-storage" {
				t.Fatalf("StorageClassName = %q, want the node group's", got.StorageClassName)
			}
		})
	}
}

func TestUpdateLocalModelNodeReplacesImagePullSecrets(t *testing.T) {
	node := &v1alpha1.LocalModelNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Spec: v1alpha1.LocalModelNodeSpec{
			LocalModels: []v1alpha1.LocalModelInfo{{
				ModelName:        "cache",
				SourceModelUri:   "oci://registry.example/model:v1",
				ImagePullSecrets: []corev1.LocalObjectReference{{Name: "reg-cred-a"}},
			}},
		},
	}
	cache := &v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec: v1alpha1.LocalModelCacheSpec{
			SourceModelUri:   "oci://registry.example/model:v1",
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "reg-cred-b"}},
		},
	}
	cl := newLocalModelCacheClient(t, node, cache)
	if err := UpdateLocalModelNode(context.Background(), cl, logr.Discard(), node, cache, nil, "gpu"); err != nil {
		t.Fatalf("UpdateLocalModelNode() error = %v", err)
	}
	got := &v1alpha1.LocalModelNode{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
		t.Fatalf("get node: %v", err)
	}
	if len(got.Spec.LocalModels) != 1 || len(got.Spec.LocalModels[0].ImagePullSecrets) != 1 ||
		got.Spec.LocalModels[0].ImagePullSecrets[0].Name != "reg-cred-b" {
		t.Fatalf("LocalModels = %#v, want imagePullSecrets [reg-cred-b]", got.Spec.LocalModels)
	}
}
