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
package localmodelcache

import (
	"fmt"
	"testing"

	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

var storageURI = "gs://testbucket/testmodel"

func makeTestLocalModelCache() v1alpha1.LocalModelCache {
	localModelCache := v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name: "iris",
		},
		Spec: v1alpha1.LocalModelCacheSpec{
			ModelSize:      resource.MustParse("1Gi"),
			NodeGroups:     []string{"gpu1"},
			SourceModelUri: storageURI,
		},
	}
	return localModelCache
}

func makeTestLocalModelCacheWithSameStorageURI() v1alpha1.LocalModelCache {
	localModelCache := v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name: "blah",
		},
		Spec: v1alpha1.LocalModelCacheSpec{
			ModelSize:      resource.MustParse("1Gi"),
			NodeGroups:     []string{"gpu1"},
			SourceModelUri: storageURI,
		},
	}
	return localModelCache
}

// A cluster-scoped cache exposes no claims to workloads, so nothing blocks its deletion.
func TestDeleteLocalModelCacheIsAllowed(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	lmc := makeTestLocalModelCache()
	s := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(s)).To(gomega.Succeed())
	fakeClient := fake.NewClientBuilder().WithObjects(&lmc).WithScheme(s).Build()
	validator := LocalModelCacheValidator{Client: fakeClient}
	warnings, err := validator.ValidateDelete(t.Context(), &lmc)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestUnableToCreateLocalModelCacheWithSameStorageURI(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	lmc := makeTestLocalModelCache()
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithObjects(&lmc).WithScheme(s).Build()
	validator := LocalModelCacheValidator{Client: fakeClient}
	newLmc := makeTestLocalModelCacheWithSameStorageURI()
	warnings, err := validator.ValidateCreate(t.Context(), &newLmc)
	g.Expect(warnings).NotTo(gomega.BeNil())
	g.Expect(err).To(gomega.MatchError(fmt.Errorf("LocalModelCache %s has the same StorageURI %s", lmc.Name, newLmc.Spec.SourceModelUri)))
}

func makeTestLocalModelCacheWithDifferentStorageURI() v1alpha1.LocalModelCache {
	localModelCache := v1alpha1.LocalModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name: "different",
		},
		Spec: v1alpha1.LocalModelCacheSpec{
			ModelSize:      resource.MustParse("1Gi"),
			NodeGroups:     []string{"gpu1"},
			SourceModelUri: "gs://testbucket/differentmodel",
		},
	}
	return localModelCache
}

func TestValidateUpdate_LocalModelCacheWithSameStorageURI(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	existingLmc := makeTestLocalModelCache()
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithObjects(&existingLmc).WithScheme(s).Build()
	validator := LocalModelCacheValidator{Client: fakeClient}
	// newLmc has a different name but same StorageURI as existingLmc
	newLmc := makeTestLocalModelCacheWithSameStorageURI()
	oldLmc := makeTestLocalModelCacheWithDifferentStorageURI()
	warnings, err := validator.ValidateUpdate(t.Context(), &oldLmc, &newLmc)
	g.Expect(warnings).NotTo(gomega.BeNil())
	g.Expect(err).To(gomega.MatchError(fmt.Errorf("LocalModelCache %s has the same StorageURI %s", existingLmc.Name, newLmc.Spec.SourceModelUri)))
}

func TestValidateUpdate_LocalModelCacheWithUniqueStorageURI(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	existingLmc := makeTestLocalModelCache()
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithObjects(&existingLmc).WithScheme(s).Build()
	validator := LocalModelCacheValidator{Client: fakeClient}
	// newLmc has a unique StorageURI
	newLmc := makeTestLocalModelCacheWithDifferentStorageURI()
	oldLmc := makeTestLocalModelCacheWithSameStorageURI()
	warnings, err := validator.ValidateUpdate(t.Context(), &oldLmc, &newLmc)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestValidateUpdate_DeletionBypass(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	existingLmc := makeTestLocalModelCache()
	s := runtime.NewScheme()
	err := v1alpha1.AddToScheme(s)
	if err != nil {
		t.Errorf("unable to add scheme : %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithObjects(&existingLmc).WithScheme(s).Build()
	validator := LocalModelCacheValidator{Client: fakeClient}
	oldLmc := makeTestLocalModelCacheWithDifferentStorageURI()
	newLmc := makeTestLocalModelCacheWithSameStorageURI()
	now := metav1.Now()
	newLmc.DeletionTimestamp = &now
	warnings, err := validator.ValidateUpdate(t.Context(), &oldLmc, &newLmc)
	g.Expect(warnings).To(gomega.BeNil())
	g.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestLocalModelCacheValidatorEnforcesStorageLimit(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	s := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(s)).To(gomega.Succeed())
	nodeGroup := v1alpha1.LocalModelNodeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu1"},
		Spec:       v1alpha1.LocalModelNodeGroupSpec{StorageLimit: resource.MustParse("10Gi")},
	}
	stored := makeTestLocalModelCache()
	stored.UID = "stored"
	reader := fake.NewClientBuilder().WithObjects(&nodeGroup, &stored).WithScheme(s).Build()
	validator := LocalModelCacheValidator{Client: reader}

	added := makeTestLocalModelCache()
	added.Name = "added"
	added.Spec.SourceModelUri = "gs://testbucket/other"
	added.Spec.ModelSize = resource.MustParse("9Gi")
	_, err := validator.ValidateCreate(t.Context(), &added)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	added.Spec.ModelSize = resource.MustParse("10Gi")
	_, err = validator.ValidateCreate(t.Context(), &added)
	g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("storageLimit 10Gi")))

	grown := stored.DeepCopy()
	grown.Spec.ModelSize = resource.MustParse("11Gi")
	_, err = validator.ValidateUpdate(t.Context(), &stored, grown)
	g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("storageLimit 10Gi")))
	_, err = validator.ValidateUpdate(t.Context(), grown, grown)
	g.Expect(err).NotTo(gomega.HaveOccurred(), "a cache that already exceeds the limit can be updated")
}
