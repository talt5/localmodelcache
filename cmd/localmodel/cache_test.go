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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

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

	// Reads the Secret as the controller does, with the permission to get Secrets only.
	It("Should read Secrets without listing them", func(ctx SpecContext) {
		env := pkgtest.NewEnvTest().Start(context.Background())
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "signer"}}
		Expect(env.Create(ctx, secret)).To(Succeed())
		Expect(env.Create(ctx, &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: "get-secrets"},
			Rules:      []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}}},
		})).To(Succeed())
		Expect(env.Create(ctx, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "get-secrets"},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "get-secrets"},
			Subjects:   []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: "controller"}},
		})).To(Succeed())
		controller, err := env.AddUser(envtest.User{Name: "controller"}, nil)
		Expect(err).NotTo(HaveOccurred())

		mgr, err := manager.New(controller.Config(), manager.Options{
			Scheme:  env.Environment.Scheme,
			Client:  clientOptions(),
			Metrics: metricsserver.Options{BindAddress: "0"},
		})
		Expect(err).NotTo(HaveOccurred())
		running, stop := context.WithCancel(context.Background())
		defer stop()
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(running)).To(Succeed())
		}()

		Eventually(func(g Gomega, ctx SpecContext) {
			reading, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			g.Expect(mgr.GetClient().Get(reading, client.ObjectKeyFromObject(secret), &corev1.Secret{})).To(Succeed())
		}).WithContext(ctx).WithTimeout(20 * time.Second).Should(Succeed())
	})
})
