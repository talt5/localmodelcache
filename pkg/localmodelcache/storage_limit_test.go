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

package localmodelcache

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

func TestCheckStorageLimit(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	usage := func(uri, size string, groups ...string) CacheUsage {
		return CacheUsage{SourceModelUri: uri, StorageKey: v1alpha1.GetStorageKey(uri, nil), ModelSize: resource.MustParse(size), NodeGroups: groups}
	}
	// gpu holds a (4Gi, cached twice) and b (3Gi) of 10Gi. full already exceeds its 5Gi.
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1alpha1.LocalModelNodeGroup{ObjectMeta: metav1.ObjectMeta{Name: "gpu"}, Spec: v1alpha1.LocalModelNodeGroupSpec{StorageLimit: resource.MustParse("10Gi")}},
		&v1alpha1.LocalModelNodeGroup{ObjectMeta: metav1.ObjectMeta{Name: "full"}, Spec: v1alpha1.LocalModelNodeGroupSpec{StorageLimit: resource.MustParse("5Gi")}},
		&v1alpha1.LocalModelCache{ObjectMeta: metav1.ObjectMeta{Name: "a", UID: "uid-a"}, Spec: v1alpha1.LocalModelCacheSpec{
			SourceModelUri: "hf://a", ModelSize: resource.MustParse("4Gi"), NodeGroups: []string{"gpu"},
		}},
		&v1alpha1.LocalModelNamespaceCache{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns", UID: "uid-ns-a"}, Spec: v1alpha1.LocalModelNamespaceCacheSpec{
			SourceModelUri: "hf://a", ModelSize: resource.MustParse("2Gi"), NodeGroups: []string{"gpu"},
		}},
		&v1alpha1.LocalModelNamespaceCache{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns", UID: "uid-b"}, Spec: v1alpha1.LocalModelNamespaceCacheSpec{
			SourceModelUri: "hf://b", ModelSize: resource.MustParse("3Gi"), NodeGroups: []string{"gpu"},
		}},
		&v1alpha1.LocalModelNamespaceCache{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns", UID: "uid-s"}, Spec: v1alpha1.LocalModelNamespaceCacheSpec{
			SourceModelUri: "hf://s", ModelSize: resource.MustParse("500Gi"), PVCRef: ptr.To("pvc"),
		}},
		&v1alpha1.LocalModelNamespaceCache{ObjectMeta: metav1.ObjectMeta{Name: "f", Namespace: "ns", UID: "uid-f"}, Spec: v1alpha1.LocalModelNamespaceCacheSpec{
			SourceModelUri: "hf://f", ModelSize: resource.MustParse("8Gi"), NodeGroups: []string{"full"},
		}},
	).Build()

	negative := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1alpha1.LocalModelNodeGroup{ObjectMeta: metav1.ObjectMeta{Name: "gpu"}, Spec: v1alpha1.LocalModelNodeGroupSpec{StorageLimit: resource.MustParse("10Gi")}},
		&v1alpha1.LocalModelCache{ObjectMeta: metav1.ObjectMeta{Name: "n", UID: "uid-n"}, Spec: v1alpha1.LocalModelCacheSpec{
			SourceModelUri: "hf://n", ModelSize: resource.MustParse("-100Gi"), NodeGroups: []string{"gpu"},
		}},
	).Build()
	require.ErrorContains(t, CheckStorageLimit(t.Context(), negative, "", nil, usage("hf://c", "11Gi", "gpu")), "in use: 0",
		"a negative modelSize must not make room for other models")

	otherServer := usage("hf://a", "4Gi", "gpu")
	otherServer.StorageKey = "another-folder"
	tests := map[string]struct {
		uid     types.UID
		old     *CacheUsage
		usage   CacheUsage
		wantErr string
	}{
		"fits exactly":                         {usage: usage("hf://c", "3Gi", "gpu")},
		"too large":                            {usage: usage("hf://c", "3073Mi", "gpu"), wantErr: "storageLimit 10Gi (in use: 7Gi)"},
		"same URI counts once":                 {usage: usage("hf://a", "4Gi", "gpu")},
		"same URI on another server does not":  {usage: otherServer, wantErr: "needs 11Gi"},
		"same URI with a larger size":          {usage: usage("hf://a", "8Gi", "gpu"), wantErr: "needs 11Gi"},
		"every group is checked":               {usage: usage("hf://c", "1Gi", "gpu", "full"), wantErr: "LocalModelNodeGroup full"},
		"same URI in a full group":             {usage: usage("hf://f", "8Gi", "full")},
		"new URI in a full group":              {usage: usage("hf://g", "1Mi", "full"), wantErr: "LocalModelNodeGroup full"},
		"missing group is left to others":      {usage: usage("hf://c", "900Gi", "nope")},
		"shared PVC claims nothing":            {usage: usage("hf://c", "900Gi")},
		"update of an over-limit cache":        {uid: "uid-f", old: ptr.To(usage("hf://f", "8Gi", "full")), usage: usage("hf://f", "8Gi", "full")},
		"shrinking an over-limit cache":        {uid: "uid-f", old: ptr.To(usage("hf://f", "8Gi", "full")), usage: usage("hf://f", "6Gi", "full")},
		"growing within the limit":             {uid: "uid-b", old: ptr.To(usage("hf://b", "3Gi", "gpu")), usage: usage("hf://b", "6Gi", "gpu")},
		"growing past the limit":               {uid: "uid-b", old: ptr.To(usage("hf://b", "3Gi", "gpu")), usage: usage("hf://b", "7Gi", "gpu"), wantErr: "needs 11Gi"},
		"adding a group that has no room":      {uid: "uid-b", old: ptr.To(usage("hf://b", "3Gi", "gpu")), usage: usage("hf://b", "3Gi", "gpu", "full"), wantErr: "LocalModelNodeGroup full"},
		"a smaller copy of a shared URI grows": {uid: "uid-ns-a", old: ptr.To(usage("hf://a", "2Gi", "gpu")), usage: usage("hf://a", "4Gi", "gpu")},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := CheckStorageLimit(t.Context(), reader, tc.uid, tc.old, tc.usage)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
