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

package localmodelnamespacecache

import (
	"testing"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
)

func claimValidator(g gomega.Gomega, objects ...client.Object) LocalModelNamespaceCacheValidator {
	s := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(s)).To(gomega.Succeed())
	g.Expect(corev1.AddToScheme(s)).To(gomega.Succeed())
	for _, name := range []string{"gpu1", "gpu2"} {
		group := makeTestLocalModelNodeGroup(name)
		objects = append(objects, &group)
	}
	return LocalModelNamespaceCacheValidator{Client: fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()}
}

func testClaim(namespace, volume string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "iris-gpu1", Namespace: namespace,
			Annotations: map[string]string{constants.LocalModelAnnotationKey: "iris"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{VolumeName: volume},
	}
}

var clusterVolume = localmodelcache.ClusterCacheVolumeName(types.NamespacedName{Namespace: "default", Name: "iris-gpu1"})

func TestExistingClaims(t *testing.T) {
	terminating := testClaim("default", clusterVolume)
	terminating.Finalizers = []string{"kubernetes.io/pvc-protection"}
	terminating.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
	ofOther := testClaim("default", "iris-gpu1-default")
	ofOther.Annotations[constants.LocalModelAnnotationKey] = "iris-gpu"
	disabled := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: constants.SystemNamespace, Name: constants.LocalModelConfigMapName},
		Data:       map[string]string{"localModel": `{"disableVolumeManagement": true}`},
	}
	tests := map[string]struct {
		objects []client.Object
		wantErr string
	}{
		"none":                                 {},
		"in another namespace":                 {objects: []client.Object{testClaim("other", "")}},
		"left by a cache of the same name":     {objects: []client.Object{testClaim("default", "iris-gpu1-default")}},
		"of a user, without volume management": {objects: []client.Object{testClaim("default", ""), disabled}},
		"of a user":                            {objects: []client.Object{testClaim("default", "")}, wantErr: "default/iris-gpu1 already exists and does not belong to this cache"},
		"left by another namespace cache":      {objects: []client.Object{ofOther}, wantErr: "does not belong to this cache"},
		"of a LocalModelCache, terminating":    {objects: []client.Object{terminating}, wantErr: "is being deleted: try again"},
		"of a LocalModelCache":                 {objects: []client.Object{testClaim("default", clusterVolume), &v1alpha1.LocalModelCache{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}}, wantErr: "default/iris-gpu1 already exists and belongs to LocalModelCache iris"},
		"of a LocalModelCache that is gone":    {objects: []client.Object{testClaim("default", clusterVolume)}, wantErr: "does not belong to this cache: delete it"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			validator := claimValidator(g, tc.objects...)
			cache := makeTestLocalModelNamespaceCache()
			check := func(err error) {
				if tc.wantErr == "" {
					g.Expect(err).NotTo(gomega.HaveOccurred())
				} else {
					g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(tc.wantErr)))
				}
			}
			_, err := validator.ValidateCreate(t.Context(), &cache)
			check(err)

			before := cache.DeepCopy()
			before.Spec.NodeGroups = []string{"gpu2"}
			_, err = validator.ValidateUpdate(t.Context(), before, &cache)
			check(err)

			// A cache that already names the claim stays editable.
			edited := cache.DeepCopy()
			edited.Spec.ServiceAccountName = "other"
			_, err = validator.ValidateUpdate(t.Context(), &cache, edited)
			g.Expect(err).NotTo(gomega.HaveOccurred())
		})
	}
}

func TestDeleteWhilePodsMountTheClaimOfALocalModelCache(t *testing.T) {
	for volume, denied := range map[string]bool{clusterVolume: false, "iris-gpu1-default": true, "": true} {
		g := gomega.NewWithT(t)
		validator := claimValidator(g, testClaim("default", volume), makeTestPodMountingClaim("default", "iris-gpu1", corev1.PodRunning))
		cache := makeTestLocalModelNamespaceCache()
		_, err := validator.ValidateDelete(t.Context(), &cache)
		g.Expect(err != nil).To(gomega.Equal(denied), "volume %q: %v", volume, err)
	}
}

func TestClaimOfAnotherNamespaceCache(t *testing.T) {
	g := gomega.NewWithT(t)
	other := makeTestLocalModelNamespaceCache()
	other.Name, other.Spec.NodeGroups = "iris-gpu", []string{"large"}
	validator := claimValidator(g, &other, &v1alpha1.LocalModelNodeGroup{ObjectMeta: metav1.ObjectMeta{Name: "gpu-large"}, Spec: makeTestLocalModelNodeGroup("").Spec})
	cache := makeTestLocalModelNamespaceCache()
	_, err := validator.ValidateCreate(t.Context(), &cache)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	clashing := cache.DeepCopy()
	clashing.Spec.NodeGroups = []string{"gpu1", "gpu-large"}
	want := gomega.MatchError(gomega.ContainSubstring("PersistentVolumeClaim default/iris-gpu-large is that of LocalModelNamespaceCache iris-gpu"))
	_, err = validator.ValidateCreate(t.Context(), clashing)
	g.Expect(err).To(want)
	_, err = validator.ValidateUpdate(t.Context(), &cache, clashing)
	g.Expect(err).To(want)
	clashing.Namespace = "elsewhere"
	_, err = validator.ValidateCreate(t.Context(), clashing)
	g.Expect(err).NotTo(gomega.HaveOccurred())
}

func TestVolumeOfAnotherCache(t *testing.T) {
	cache := func(namespace, name, group string) *v1alpha1.LocalModelNamespaceCache {
		c := makeTestLocalModelNamespaceCache()
		c.Namespace, c.Name, c.Spec.NodeGroups = namespace, name, []string{group}
		return &c
	}
	tests := map[string]struct {
		other client.Object
		cache *v1alpha1.LocalModelNamespaceCache
		want  string
	}{
		"group and namespace": {cache("prod-east", "llama", "gpu1"), cache("east", "llama", "gpu1-prod"), "PersistentVolume llama-gpu1-prod-east is that of LocalModelNamespaceCache prod-east/llama"},
		"download volume":     {cache("x", "llama", "gpu1"), cache("x-download", "llama", "gpu1"), "PersistentVolume llama-gpu1-x-download is that of LocalModelNamespaceCache x/llama"},
		"cluster cache": {&v1alpha1.LocalModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "llama"}, Spec: v1alpha1.LocalModelCacheSpec{NodeGroups: []string{"gpu1"}},
		}, cache("download", "llama", "gpu1"), "PersistentVolume llama-gpu1-download is that of LocalModelCache llama"},
		"same names, other namespace": {cache("a", "llama", "gpu1"), cache("b", "llama", "gpu1"), ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			group := makeTestLocalModelNodeGroup("gpu1-prod")
			validator := claimValidator(g, tc.other, &group)
			_, err := validator.ValidateCreate(t.Context(), tc.cache)
			if tc.want == "" {
				g.Expect(err).NotTo(gomega.HaveOccurred())
				return
			}
			g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(tc.want)))
		})
	}
}
