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

package main

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/constants"
	pkgtest "github.com/talt5/localmodelcache/pkg/testing"
)

func TestManager(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Manager Suite")
}

var _ = Describe("Manager cache", func() {
	It("Should hold only the objects the controllers use", func(ctx SpecContext) {
		env := pkgtest.NewEnvTest().Start(context.Background())
		pod := func(namespace, name string, labels map[string]string) *corev1.Pod {
			return &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "image"}}},
			}
		}
		configMap := func(namespace, name string) *corev1.ConfigMap {
			return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
		}
		for _, obj := range []client.Object{
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: constants.SystemNamespace}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team"}},
			configMap(constants.SystemNamespace, constants.LocalModelConfigMapName),
			configMap(constants.SystemNamespace, "other"),
			configMap("team", constants.LocalModelConfigMapName),
			pod("team", "vllm", map[string]string{constants.KernelCacheWorkloadLabelKey: "vllm"}),
			pod("team", "other", nil),
		} {
			Expect(env.Create(ctx, obj)).To(Succeed())
		}

		options, err := cacheOptions()
		Expect(err).NotTo(HaveOccurred())
		options.Scheme = env.Environment.Scheme
		informers, err := cache.New(env.Config, options)
		Expect(err).NotTo(HaveOccurred())
		running, stop := context.WithCancel(context.Background())
		defer stop()
		go func() {
			defer GinkgoRecover()
			Expect(informers.Start(running)).To(Succeed())
		}()

		Expect(informers.WaitForCacheSync(ctx)).To(BeTrue())
		configMaps := &corev1.ConfigMapList{}
		Expect(informers.List(ctx, configMaps)).To(Succeed())
		Expect(configMaps.Items).To(HaveLen(1))
		Expect(client.ObjectKeyFromObject(&configMaps.Items[0])).To(Equal(
			client.ObjectKey{Namespace: constants.SystemNamespace, Name: constants.LocalModelConfigMapName}))
		pods := &corev1.PodList{}
		Expect(informers.List(ctx, pods)).To(Succeed())
		Expect(pods.Items).To(HaveLen(1))
		Expect(pods.Items[0].Name).To(Equal("vllm"))
		Expect(pods.Items[0].ManagedFields).To(BeEmpty())
	})
})
