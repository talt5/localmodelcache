/*
Copyright 2021 The KServe Authors.

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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

var storageURI = "gs://testbucket/testmodel"

func makeTestLocalModelNamespaceCache() v1alpha1.LocalModelNamespaceCache {
	return v1alpha1.LocalModelNamespaceCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "iris",
			Namespace: "default",
		},
		Spec: v1alpha1.LocalModelNamespaceCacheSpec{
			ModelSize:      resource.MustParse("1Gi"),
			NodeGroups:     []string{"gpu1"},
			SourceModelUri: storageURI,
		},
	}
}

func makeTestLocalModelNodeGroup(name string) v1alpha1.LocalModelNodeGroup {
	return v1alpha1.LocalModelNodeGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.LocalModelNodeGroupSpec{
			StorageLimit:              resource.MustParse("10Gi"),
			PersistentVolumeSpec:      corev1.PersistentVolumeSpec{},
			PersistentVolumeClaimSpec: corev1.PersistentVolumeClaimSpec{},
		},
	}
}

// makeTestPodMountingClaim returns the Pod vllm-0 in namespace, mounting claim.
func makeTestPodMountingClaim(namespace, claim string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "vllm-0", Namespace: namespace},
		Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
			Name: "model",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim, ReadOnly: true},
			},
		}}},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func TestValidateDelete_InUseCheck(t *testing.T) {
	nodeLocal := makeTestLocalModelNamespaceCache()
	shared := makeTestSharedPVCCache("model-store")

	tests := []struct {
		name    string
		cache   v1alpha1.LocalModelNamespaceCache
		pods    []*corev1.Pod
		wantErr string
	}{
		{
			name:  "node-local cache without pods",
			cache: nodeLocal,
		},
		{
			name:    "running pod mounts the cache claim",
			cache:   nodeLocal,
			pods:    []*corev1.Pod{makeTestPodMountingClaim("default", "iris-gpu1", corev1.PodRunning)},
			wantErr: "LocalModelNamespaceCache default/iris is in use: Pod vllm-0 mounts PersistentVolumeClaim iris-gpu1",
		},
		{
			name:    "pending pod mounts the cache claim",
			cache:   nodeLocal,
			pods:    []*corev1.Pod{makeTestPodMountingClaim("default", "iris-gpu1", corev1.PodPending)},
			wantErr: "Pod vllm-0 mounts PersistentVolumeClaim iris-gpu1",
		},
		{
			name:  "finished pod no longer blocks",
			cache: nodeLocal,
			pods:  []*corev1.Pod{makeTestPodMountingClaim("default", "iris-gpu1", corev1.PodSucceeded)},
		},
		{
			name:  "pod in another namespace with the same claim name",
			cache: nodeLocal,
			pods:  []*corev1.Pod{makeTestPodMountingClaim("other", "iris-gpu1", corev1.PodRunning)},
		},
		{
			name:  "pod mounts an unrelated claim",
			cache: nodeLocal,
			pods:  []*corev1.Pod{makeTestPodMountingClaim("default", "scratch", corev1.PodRunning)},
		},
		{
			name:  "shared-PVC cache keeps the data on delete",
			cache: shared,
			pods:  []*corev1.Pod{makeTestPodMountingClaim("default", "model-store", corev1.PodRunning)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gomega.NewGomegaWithT(t)
			s := runtime.NewScheme()
			g.Expect(v1alpha1.AddToScheme(s)).To(gomega.Succeed())
			g.Expect(corev1.AddToScheme(s)).To(gomega.Succeed())
			builder := fake.NewClientBuilder().WithScheme(s)
			for _, pod := range tt.pods {
				builder = builder.WithObjects(pod)
			}
			validator := LocalModelNamespaceCacheValidator{Client: builder.Build()}
			cache := tt.cache
			_, err := validator.ValidateDelete(t.Context(), &cache)
			if tt.wantErr == "" {
				g.Expect(err).ToNot(gomega.HaveOccurred())
				return
			}
			g.Expect(err).To(gomega.HaveOccurred())
			g.Expect(err.Error()).To(gomega.ContainSubstring(tt.wantErr))
		})
	}
}

// The validator lists Pods through Reader when it is set, so the manager's APIReader is used
// instead of a cluster-wide Pod informer.
func TestValidateDelete_UsesReaderForPods(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	s := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(s)).To(gomega.Succeed())
	g.Expect(corev1.AddToScheme(s)).To(gomega.Succeed())
	reader := fake.NewClientBuilder().WithScheme(s).
		WithObjects(makeTestPodMountingClaim("default", "iris-gpu1", corev1.PodRunning)).
		Build()
	validator := LocalModelNamespaceCacheValidator{
		Client: fake.NewClientBuilder().WithScheme(s).Build(),
		Reader: reader,
	}
	cache := makeTestLocalModelNamespaceCache()
	_, err := validator.ValidateDelete(t.Context(), &cache)
	g.Expect(err).To(gomega.HaveOccurred())
}

func TestUnableToCreateLocalModelNamespaceCacheWithMissingNodeGroup(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	lmnc := makeTestLocalModelNamespaceCache()
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	// No NodeGroup exists in the fake client
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	validator := LocalModelNamespaceCacheValidator{Client: fakeClient}
	warnings, err := validator.ValidateCreate(t.Context(), &lmnc)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(err.Error()).To(gomega.ContainSubstring("NodeGroup gpu1 not found"))
}

func TestValidateCreate_LocalModelNamespaceCacheWithValidNodeGroups(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	lmnc := makeTestLocalModelNamespaceCache()
	nodeGroup := makeTestLocalModelNodeGroup("gpu1")
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithObjects(&nodeGroup).WithScheme(s).Build()
	validator := LocalModelNamespaceCacheValidator{Client: fakeClient}
	warnings, err := validator.ValidateCreate(t.Context(), &lmnc)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestValidateUpdate_LocalModelNamespaceCacheWithMissingNodeGroup(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	oldLmnc := makeTestLocalModelNamespaceCache()
	newLmnc := makeTestLocalModelNamespaceCache()
	newLmnc.Spec.NodeGroups = []string{"nonexistent-nodegroup"}
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	// Only gpu1 exists, not nonexistent-nodegroup
	nodeGroup := makeTestLocalModelNodeGroup("gpu1")
	fakeClient := fake.NewClientBuilder().WithObjects(&nodeGroup).WithScheme(s).Build()
	validator := LocalModelNamespaceCacheValidator{Client: fakeClient}
	warnings, err := validator.ValidateUpdate(t.Context(), &oldLmnc, &newLmnc)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(err.Error()).To(gomega.ContainSubstring("NodeGroup nonexistent-nodegroup not found"))
}

func TestValidateUpdate_LocalModelNamespaceCacheWithValidNodeGroups(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	oldLmnc := makeTestLocalModelNamespaceCache()
	newLmnc := makeTestLocalModelNamespaceCache()
	nodeGroup := makeTestLocalModelNodeGroup("gpu1")
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithObjects(&nodeGroup).WithScheme(s).Build()
	validator := LocalModelNamespaceCacheValidator{Client: fakeClient}
	warnings, err := validator.ValidateUpdate(t.Context(), &oldLmnc, &newLmnc)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestValidateUpdate_LocalModelNamespaceCacheDeletionBypass(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	oldLmnc := makeTestLocalModelNamespaceCache()
	newLmnc := makeTestLocalModelNamespaceCache()
	newLmnc.Spec.NodeGroups = []string{"nonexistent-nodegroup"}
	now := metav1.Now()
	newLmnc.DeletionTimestamp = &now
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	validator := LocalModelNamespaceCacheValidator{Client: fakeClient}
	warnings, err := validator.ValidateUpdate(t.Context(), &oldLmnc, &newLmnc)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).ToNot(gomega.HaveOccurred())
}

func makeTestSharedPVCCache(pvcRef string) v1alpha1.LocalModelNamespaceCache {
	return v1alpha1.LocalModelNamespaceCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "iris",
			Namespace: "default",
		},
		Spec: v1alpha1.LocalModelNamespaceCacheSpec{
			ModelSize:      resource.MustParse("1Gi"),
			SourceModelUri: storageURI,
			PVCRef:         &pvcRef,
		},
	}
}

func TestValidateStorageMode(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	pvcRef := "shared-pvc"
	empty := ""
	bad := "Bad_Name"

	cases := []struct {
		name    string
		mutate  func(*v1alpha1.LocalModelNamespaceCache)
		wantErr string
	}{
		{
			name:    "neither mode",
			mutate:  func(c *v1alpha1.LocalModelNamespaceCache) { c.Spec.NodeGroups = nil; c.Spec.PVCRef = nil },
			wantErr: "one of nodeGroups or pvcRef must be set",
		},
		{
			name: "both modes",
			mutate: func(c *v1alpha1.LocalModelNamespaceCache) {
				c.Spec.NodeGroups = []string{"gpu1"}
				c.Spec.PVCRef = &pvcRef
			},
			wantErr: "mutually exclusive",
		},
		{
			name:   "node groups only",
			mutate: func(c *v1alpha1.LocalModelNamespaceCache) { c.Spec.NodeGroups = []string{"gpu1"}; c.Spec.PVCRef = nil },
		},
		{
			name:   "pvcRef only",
			mutate: func(c *v1alpha1.LocalModelNamespaceCache) { c.Spec.NodeGroups = nil; c.Spec.PVCRef = &pvcRef },
		},
		{
			name:    "empty pvcRef",
			mutate:  func(c *v1alpha1.LocalModelNamespaceCache) { c.Spec.NodeGroups = nil; c.Spec.PVCRef = &empty },
			wantErr: "must not be empty",
		},
		{
			name:    "invalid pvcRef name",
			mutate:  func(c *v1alpha1.LocalModelNamespaceCache) { c.Spec.NodeGroups = nil; c.Spec.PVCRef = &bad },
			wantErr: "not a valid PersistentVolumeClaim name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := makeTestLocalModelNamespaceCache()
			tc.mutate(&cache)
			err := validateStorageMode(&cache)
			if tc.wantErr == "" {
				g.Expect(err).ToNot(gomega.HaveOccurred())
			} else {
				g.Expect(err).To(gomega.HaveOccurred())
				g.Expect(err.Error()).To(gomega.ContainSubstring(tc.wantErr))
			}
		})
	}
}

func TestValidatePVCRefImmutable(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	oldCache := makeTestSharedPVCCache("pvc-a")
	newCache := makeTestSharedPVCCache("pvc-b")
	err := validatePVCRefImmutable(&oldCache, &newCache)
	g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("pvcRef is immutable")))

	same := makeTestSharedPVCCache("pvc-a")
	g.Expect(validatePVCRefImmutable(&oldCache, &same)).ToNot(gomega.HaveOccurred())
}

func TestValidateCreate_SharedPVCDestinationConflict(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	s := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(s)).To(gomega.Succeed())

	existing := makeTestSharedPVCCache("shared-pvc")
	existing.Name = "other-cache"
	fakeClient := fake.NewClientBuilder().WithObjects(&existing).WithScheme(s).Build()
	validator := LocalModelNamespaceCacheValidator{Client: fakeClient}

	// Same tuple (same pvc, same source URI => same storageKey) => conflict.
	conflict := makeTestSharedPVCCache("shared-pvc")
	warnings, err := validator.ValidateCreate(t.Context(), &conflict)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("already holds")))

	// Different source URI on same PVC => different storage key => allowed.
	differentModel := makeTestSharedPVCCache("shared-pvc")
	differentModel.Spec.SourceModelUri = "gs://testbucket/other-model"
	_, err = validator.ValidateCreate(t.Context(), &differentModel)
	g.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestValidateUpdate_SharedPVCSkipsDestinationConflict(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	s := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(s)).To(gomega.Succeed())

	// A contender with the same (pvcRef, storageKey) exists, as after a create race. The
	// reconciler resolves ownership; the webhook must not block updates (e.g. finalizer
	// patches) on either side.
	existing := makeTestSharedPVCCache("shared-pvc")
	existing.Name = "other-cache"
	fakeClient := fake.NewClientBuilder().WithObjects(&existing).WithScheme(s).Build()
	validator := LocalModelNamespaceCacheValidator{Client: fakeClient}

	oldCache := makeTestSharedPVCCache("shared-pvc")
	newCache := makeTestSharedPVCCache("shared-pvc")
	newCache.Finalizers = []string{"serving.kserve.io/test-finalizer"}
	warnings, err := validator.ValidateUpdate(t.Context(), &oldCache, &newCache)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).ToNot(gomega.HaveOccurred())
}
