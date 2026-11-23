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

package localmodelnode

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/controller/v1alpha1/localmodel/jobs"
)

var _ = Describe("Download Jobs", func() {
	const (
		timeout   = time.Second * 10
		interval  = time.Millisecond * 100
		namespace = "localmodelcache-jobs"
	)

	var worker string
	var configMap *corev1.ConfigMap
	BeforeEach(func(ctx SpecContext) {
		fsMock.clear()
		worker = fmt.Sprintf("worker-%d", time.Now().UnixNano())
		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: constants.LocalModelConfigMapName, Namespace: constants.SystemNamespace},
			Data: map[string]string{
				"localModel": `{
					"jobNamespace": "localmodelcache-jobs",
					"reconcilationFrequencyInSecs": 1,
					"tolerations": [{"key": "nvidia.com/gpu", "operator": "Exists", "effect": "NoSchedule"}]
				}`,
				"storageInitializer": `{"image": "initializer", "cpuRequest": "1", "cpuLimit": "1", "memoryRequest": "1Gi", "memoryLimit": "1Gi"}`,
			},
		}
		Expect(k8sClient.Create(ctx, configMap)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(k8sClient.Delete(ctx, configMap)).To(Succeed()) })
		nodeName = worker
	})

	hold := func(ctx SpecContext, models ...v1alpha1.LocalModelInfo) *v1alpha1.LocalModelNode {
		GinkgoHelper()
		for i := range models {
			models[i].NodeGroup = "gpu"
		}
		localModelNode := &v1alpha1.LocalModelNode{
			ObjectMeta: metav1.ObjectMeta{Name: worker},
			Spec:       v1alpha1.LocalModelNodeSpec{LocalModels: models},
		}
		Expect(k8sClient.Create(ctx, localModelNode)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(k8sClient.Delete(ctx, localModelNode)).To(Succeed()) })
		return localModelNode
	}
	jobsOf := func(ctx SpecContext, model v1alpha1.LocalModelInfo) func() []batchv1.Job {
		return func() []batchv1.Job {
			list := &batchv1.JobList{}
			if err := k8sClient.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels{"node": worker}); err != nil {
				return nil
			}
			found := []batchv1.Job{}
			for i := range list.Items {
				if isJobOf(&list.Items[i], &model) {
					found = append(found, list.Items[i])
				}
			}
			return found
		}
	}
	onlyJobOf := func(ctx SpecContext, model v1alpha1.LocalModelInfo) batchv1.Job {
		GinkgoHelper()
		Eventually(jobsOf(ctx, model), timeout, interval).Should(HaveLen(1))
		return jobsOf(ctx, model)()[0]
	}
	finish := func(ctx SpecContext, job batchv1.Job, succeeded bool) {
		GinkgoHelper()
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(&job), &job); err != nil {
				return err
			}
			if succeeded {
				job.Status.Succeeded = 1
				return k8sClient.Status().Update(ctx, &job)
			}
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Failed = 7
			for _, condition := range []batchv1.JobConditionType{batchv1.JobFailureTarget, batchv1.JobFailed} {
				job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{
					Type: condition, Status: corev1.ConditionTrue, LastTransitionTime: now,
				})
			}
			return k8sClient.Status().Update(ctx, &job)
		})).To(Succeed())
	}
	status := func(ctx SpecContext) func() map[string]v1alpha1.ModelStatus {
		return func() map[string]v1alpha1.ModelStatus {
			localModelNode := &v1alpha1.LocalModelNode{}
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: worker}, localModelNode); err != nil {
				return nil
			}
			return localModelNode.Status.ModelStatus
		}
	}
	addFolder := func(uri string) { fsMock.mockModel(&MockFileInfo{name: v1alpha1.GetStorageKey(uri), isDir: true}) }

	It("Should run on the node by name, with the configured tolerations", func(ctx SpecContext) {
		model := v1alpha1.LocalModelInfo{ModelName: "model", SourceModelUri: "hf://org/placed"}
		hold(ctx, model)
		spec := onlyJobOf(ctx, model).Spec.Template.Spec
		Expect(spec.NodeSelector).To(BeEmpty(), "the hostname label can differ from the node name")
		Expect(spec.NodeName).To(BeEmpty(), "the scheduler has to bind the claim")
		Expect(spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms).To(Equal([]corev1.NodeSelectorTerm{{
			MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{worker}}},
		}}))
		Expect(spec.Tolerations).To(ContainElement(corev1.Toleration{
			Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
		}))
	})

	It("Should download the other models when one cannot be downloaded", func(ctx SpecContext) {
		// The API server rejects every Job with this container.
		invalid := &v1alpha1.ClusterStorageContainer{
			ObjectMeta: metav1.ObjectMeta{Name: worker},
			Spec: v1alpha1.StorageContainerSpec{
				SupportedUriFormats: []v1alpha1.SupportedUriFormat{{Prefix: "broken://"}},
				Container:           corev1.Container{Name: "Not_A_Name", Image: "image"},
				WorkloadType:        v1alpha1.LocalModelDownloadJob,
			},
		}
		Expect(k8sClient.Create(ctx, invalid)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(k8sClient.Delete(ctx, invalid)).To(Succeed()) })

		broken := v1alpha1.LocalModelInfo{ModelName: "broken", SourceModelUri: "broken://model"}
		long := v1alpha1.LocalModelInfo{ModelName: strings.Repeat("long-name.", 20) + "end", SourceModelUri: "hf://org/long"}
		good := v1alpha1.LocalModelInfo{ModelName: "good", SourceModelUri: "hf://org/good"}
		hold(ctx, broken, long, good)

		onlyJobOf(ctx, good)
		Expect(onlyJobOf(ctx, long).Labels).To(HaveKeyWithValue("model", jobs.LabelValue(long.ModelName)))
		Eventually(status(ctx), timeout, interval).Should(Equal(map[string]v1alpha1.ModelStatus{
			"broken":       v1alpha1.ModelDownloadError,
			long.ModelName: v1alpha1.ModelDownloadPending,
			"good":         v1alpha1.ModelDownloadPending,
		}))
		Expect(jobsOf(ctx, broken)()).To(BeEmpty())
	})

	It("Should let another cache of the URI try after a failure", func(ctx SpecContext) {
		const uri = "hf://org/shared"
		first := v1alpha1.LocalModelInfo{ModelName: "first", SourceModelUri: uri}
		second := v1alpha1.LocalModelInfo{ModelName: "second", Namespace: "team", SourceModelUri: uri}
		third := v1alpha1.LocalModelInfo{ModelName: "third", Namespace: "team", SourceModelUri: uri}
		hold(ctx, first, second, third)
		job := onlyJobOf(ctx, first)
		Consistently(jobsOf(ctx, second), time.Second, interval).Should(BeEmpty(), "one download at a time writes to the folder")

		finish(ctx, job, false)
		job = onlyJobOf(ctx, second)
		Expect(jobsOf(ctx, third)()).To(BeEmpty())
		addFolder(uri)
		finish(ctx, job, true)
		Eventually(status(ctx), timeout, interval).Should(Equal(map[string]v1alpha1.ModelStatus{
			"first": v1alpha1.ModelDownloaded, "team/second": v1alpha1.ModelDownloaded, "team/third": v1alpha1.ModelDownloaded,
		}))
		Consistently(jobsOf(ctx, third), time.Second, interval).Should(BeEmpty())
	})

	It("Should keep caches of the same name apart", func(ctx SpecContext) {
		cluster := v1alpha1.LocalModelInfo{ModelName: "model", SourceModelUri: "hf://org/cluster"}
		namespaced := v1alpha1.LocalModelInfo{ModelName: "model", Namespace: "team", SourceModelUri: "hf://org/namespaced"}
		hold(ctx, cluster, namespaced)
		onlyJobOf(ctx, cluster)
		addFolder(namespaced.SourceModelUri)
		finish(ctx, onlyJobOf(ctx, namespaced), true)
		Eventually(status(ctx), timeout, interval).Should(Equal(map[string]v1alpha1.ModelStatus{
			"model": v1alpha1.ModelDownloadPending, "team/model": v1alpha1.ModelDownloaded,
		}))
	})

	It("Should report an error while a downloaded model keeps disappearing", func(ctx SpecContext) {
		model := v1alpha1.LocalModelInfo{ModelName: "model", SourceModelUri: "hf://org/lost"}
		hold(ctx, model)
		addFolder(model.SourceModelUri)
		finish(ctx, onlyJobOf(ctx, model), true)
		Eventually(status(ctx), timeout, interval).Should(HaveKeyWithValue("model", v1alpha1.ModelDownloaded))

		fsMock.clear()
		Eventually(jobsOf(ctx, model), timeout, interval).Should(HaveLen(2), "it should download again")
		addFolder(model.SourceModelUri)
		for _, job := range jobsOf(ctx, model)() {
			if job.Status.Succeeded == 0 {
				finish(ctx, job, true)
			}
		}
		Eventually(status(ctx), timeout, interval).Should(HaveKeyWithValue("model", v1alpha1.ModelDownloaded))

		fsMock.clear()
		Eventually(status(ctx), timeout, interval).Should(HaveKeyWithValue("model", v1alpha1.ModelDownloadError))
		Consistently(jobsOf(ctx, model), time.Second, interval).Should(HaveLen(2))
	})

	It("Should delete the Job of a cache whose source URI changed", func(ctx SpecContext) {
		before := v1alpha1.LocalModelInfo{ModelName: "model", SourceModelUri: "hf://org/before"}
		after := v1alpha1.LocalModelInfo{ModelName: "model", SourceModelUri: "hf://org/after", NodeGroup: "gpu"}
		localModelNode := hold(ctx, before)
		addFolder(before.SourceModelUri)
		finish(ctx, onlyJobOf(ctx, before), true)
		Eventually(status(ctx), timeout, interval).Should(HaveKeyWithValue("model", v1alpha1.ModelDownloaded))

		// A cache that was deleted and created again with another URI.
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(localModelNode), localModelNode); err != nil {
				return err
			}
			localModelNode.Spec.LocalModels = []v1alpha1.LocalModelInfo{after}
			return k8sClient.Update(ctx, localModelNode)
		})).To(Succeed())
		onlyJobOf(ctx, after)
		Eventually(jobsOf(ctx, before), timeout, interval).Should(BeEmpty())
		Eventually(status(ctx), timeout, interval).Should(HaveKeyWithValue("model", v1alpha1.ModelDownloadPending))
	})

	It("Should use changed credential settings without a restart", func(ctx SpecContext) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: worker, Namespace: namespace},
			StringData: map[string]string{"awsAccessKeyID": "id", "awsSecretAccessKey": "key"},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(k8sClient.Delete(ctx, secret)).To(Succeed()) })
		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: worker, Namespace: namespace},
			Secrets:    []corev1.ObjectReference{{Name: secret.Name}},
		}
		Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(k8sClient.Delete(ctx, serviceAccount)).To(Succeed()) })
		setEndpoint := func(endpoint string) {
			GinkgoHelper()
			configMap.Data["credentials"] = fmt.Sprintf(`{"s3": {"s3Endpoint": %q}}`, endpoint)
			Expect(k8sClient.Update(ctx, configMap)).To(Succeed())
		}
		endpoints := func(job batchv1.Job) []string {
			var values []string
			for _, env := range job.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "S3_ENDPOINT" {
					values = append(values, env.Value)
				}
			}
			return values
		}

		setEndpoint("first.example.com")
		first := v1alpha1.LocalModelInfo{ModelName: "first", SourceModelUri: "s3://bucket/first", ServiceAccountName: worker, NodeGroup: "gpu"}
		localModelNode := hold(ctx, first)
		Expect(endpoints(onlyJobOf(ctx, first))).To(Equal([]string{"first.example.com"}))

		setEndpoint("second.example.com")
		second := v1alpha1.LocalModelInfo{ModelName: "second", SourceModelUri: "s3://bucket/second", ServiceAccountName: worker, NodeGroup: "gpu"}
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(localModelNode), localModelNode); err != nil {
				return err
			}
			localModelNode.Spec.LocalModels = []v1alpha1.LocalModelInfo{first, second}
			return k8sClient.Update(ctx, localModelNode)
		})).To(Succeed())
		Expect(endpoints(onlyJobOf(ctx, second))).To(Equal([]string{"second.example.com"}))
	})
})
