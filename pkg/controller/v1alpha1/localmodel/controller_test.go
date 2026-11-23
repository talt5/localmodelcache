/*
Copyright 2024 The KServe Authors.

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

package localmodel

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
)

var _ = Describe("CachedModel controller", func() {
	const (
		timeout             = time.Second * 10
		duration            = time.Second * 10
		interval            = time.Millisecond * 250
		modelCacheNamespace = "kserve-localmodel-jobs"
		sourceModelUri      = "s3://mybucket/mymodel"
	)
	var (
		localModelSpec = v1alpha1.LocalModelCacheSpec{
			SourceModelUri: sourceModelUri,
			ModelSize:      resource.MustParse("123Gi"),
			NodeGroups:     []string{"gpu1", "gpu2"},
		}
		localModelNodeGroupSpec1 = v1alpha1.LocalModelNodeGroupSpec{
			PersistentVolumeSpec: corev1.PersistentVolumeSpec{
				AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				VolumeMode:                    ptr.To(corev1.PersistentVolumeFilesystem),
				Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")},
				StorageClassName:              "standard",
				PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/models",
						Type: ptr.To(corev1.HostPathDirectory),
					},
				},
				NodeAffinity: &corev1.VolumeNodeAffinity{
					Required: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "node.kubernetes.io/instance-type",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"gpu1"},
									},
								},
							},
						},
					},
				},
			},
			PersistentVolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")}},
			},
		}
		localModelNodeGroupSpec2 = v1alpha1.LocalModelNodeGroupSpec{
			PersistentVolumeSpec: corev1.PersistentVolumeSpec{
				AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				VolumeMode:                    ptr.To(corev1.PersistentVolumeFilesystem),
				Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")},
				StorageClassName:              "standard",
				PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/models",
						Type: ptr.To(corev1.HostPathDirectory),
					},
				},
				NodeAffinity: &corev1.VolumeNodeAffinity{
					Required: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "node.kubernetes.io/instance-type",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"gpu2"},
									},
								},
							},
						},
					},
				},
			},
			PersistentVolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")}},
			},
		}
	)

	Context("When creating a local model", func() {
		It("Should create pv, pvc, localmodelnode, and update status from localmodelnode", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu1",
				},
				Spec: localModelNodeGroupSpec1,
			}
			nodeGroup2 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu2",
				},
				Spec: localModelNodeGroupSpec2,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)
			Expect(k8sClient.Create(ctx, nodeGroup2)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup2)

			cachedModel := &v1alpha1.LocalModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name: "iris",
				},
				Spec: localModelSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())

			modelLookupKey := types.NamespacedName{Name: "iris"}
			pvLookupKey1 := types.NamespacedName{Name: "iris-gpu1-download"}
			pvcLookupKey1 := types.NamespacedName{Name: "iris-gpu1", Namespace: modelCacheNamespace}
			pvLookupKey2 := types.NamespacedName{Name: "iris-gpu2-download"}
			pvcLookupKey2 := types.NamespacedName{Name: "iris-gpu2", Namespace: modelCacheNamespace}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				return err == nil && cachedModel.Status.ModelCopies != nil
			}, timeout, interval).Should(BeTrue())
			// A cluster-scoped cache reports where the files are, but exposes no claims.
			Expect(cachedModel.Status.SubPath).To(Equal(v1alpha1.GetStorageSubPath(sourceModelUri)))
			Expect(cachedModel.Status.PersistentVolumeClaims).To(BeEmpty())

			persistentVolume1 := &corev1.PersistentVolume{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvLookupKey1, persistentVolume1)
				return err == nil && persistentVolume1 != nil
			}, timeout, interval).Should(BeTrue())
			persistentVolume2 := &corev1.PersistentVolume{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvLookupKey2, persistentVolume2)
				return err == nil && persistentVolume2 != nil
			}, timeout, interval).Should(BeTrue())

			persistentVolumeClaim1 := &corev1.PersistentVolumeClaim{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvcLookupKey1, persistentVolumeClaim1)
				return err == nil && persistentVolumeClaim1 != nil
			}, timeout, interval).Should(BeTrue())
			persistentVolumeClaim2 := &corev1.PersistentVolumeClaim{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvcLookupKey2, persistentVolumeClaim2)
				return err == nil && persistentVolumeClaim2 != nil
			}, timeout, interval).Should(BeTrue())

			nodeName1 := "node-1"
			nodeName2 := "node-2"
			node1 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName1,
					Labels: map[string]string{
						"node.kubernetes.io/instance-type": "gpu1",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}
			node2 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName2,
					Labels: map[string]string{
						"node.kubernetes.io/instance-type": "gpu2",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, node1)).Should(Succeed())
			defer k8sClient.Delete(ctx, node1)
			Expect(k8sClient.Create(ctx, node2)).Should(Succeed())
			defer k8sClient.Delete(ctx, node2)

			localModelNode1 := &v1alpha1.LocalModelNode{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName1}, localModelNode1)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(localModelNode1.Spec.LocalModels).Should(ContainElement(v1alpha1.LocalModelInfo{ModelName: cachedModel.Name, SourceModelUri: sourceModelUri, NodeGroup: "gpu1"}))
			localModelNode2 := &v1alpha1.LocalModelNode{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName2}, localModelNode2)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(localModelNode2.Spec.LocalModels).Should(ContainElement(v1alpha1.LocalModelInfo{ModelName: cachedModel.Name, SourceModelUri: sourceModelUri, NodeGroup: "gpu2"}))

			// Todo: Test agent download
			// Update the LocalModelNode status to be successful
			localModelNode1.Status.ModelStatus = map[string]v1alpha1.ModelStatus{cachedModel.Name: v1alpha1.ModelDownloaded}
			Expect(k8sClient.Status().Update(ctx, localModelNode1)).Should(Succeed())
			localModelNode2.Status.ModelStatus = map[string]v1alpha1.ModelStatus{cachedModel.Name: v1alpha1.ModelDownloaded}
			Expect(k8sClient.Status().Update(ctx, localModelNode2)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				if err != nil {
					return false
				}
				if cachedModel.Status.ModelCopies.Available != 2 || cachedModel.Status.ModelCopies.Total != 2 || cachedModel.Status.ModelCopies.Failed != 0 {
					return false
				}
				if cachedModel.Status.NodeStatus[nodeName1] != v1alpha1.NodeDownloaded {
					return false
				}
				if cachedModel.Status.NodeStatus[nodeName2] != v1alpha1.NodeDownloaded {
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue(), "Node status should be downloaded")

			// Now let's test deletion
			Expect(k8sClient.Delete(ctx, cachedModel)).Should(Succeed())

			newLocalModel := &v1alpha1.LocalModelCache{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, newLocalModel)
				return err == nil
			}, timeout, interval).Should(BeFalse(), "Should not get the local model after deletion")
		})

		It("Should copy imagePullSecrets onto LocalModelNode LocalModelInfo", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			nodeGroup := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu1"},
				Spec:       localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup)

			nodeName := "node-oci-pull-secret"
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"node.kubernetes.io/instance-type": "gpu1",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			defer k8sClient.Delete(ctx, node)

			modelName := fmt.Sprintf("oci-cache-%d", time.Now().UnixNano())
			cachedModel := &v1alpha1.LocalModelCache{
				ObjectMeta: metav1.ObjectMeta{Name: modelName},
				Spec: v1alpha1.LocalModelCacheSpec{
					SourceModelUri:   "oci://ghcr.io/example/model:v1",
					ModelSize:        resource.MustParse("1Gi"),
					NodeGroups:       []string{"gpu1"},
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: "reg-cred"}},
				},
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())
			defer func() {
				Expect(k8sClient.Delete(ctx, cachedModel)).Should(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: modelName}, cachedModel)
					return err != nil && errors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())
			}()

			localModelNode := &v1alpha1.LocalModelNode{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, localModelNode)
				if err != nil {
					return false
				}
				for _, info := range localModelNode.Spec.LocalModels {
					if info.ModelName == modelName &&
						len(info.ImagePullSecrets) == 1 &&
						info.ImagePullSecrets[0].Name == "reg-cred" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "LocalModelInfo should include imagePullSecrets")
		})
	})

	Context("When DisableVolumeManagement is set to true", func() {
		It("Should NOT create/delete pvs and pvcs if localmodel config value DisableVolumeManagement is true", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			// Update configmap to enable DisableVolumeManagement
			configMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: constants.LocalModelConfigMapName, Namespace: constants.SystemNamespace}, configMap)).Should(Succeed())
			originalData := configMap.Data["localModel"]
			configMap.Data["localModel"] = `{
				"jobNamespace": "kserve-localmodel-jobs",
				"defaultJobImage": "kserve/storage-initializer:latest",
				"disableVolumeManagement": true
			}`
			Expect(k8sClient.Update(ctx, configMap)).Should(Succeed())
			defer func() {
				// Restore original configmap data
				configMap.Data["localModel"] = originalData
				Expect(k8sClient.Update(ctx, configMap)).Should(Succeed())
			}()
			testNamespace := "test-namespace"
			namespaceObj := createTestNamespace(ctx, testNamespace)
			defer k8sClient.Delete(ctx, namespaceObj)

			modelName := "iris3"
			cachedModel := &v1alpha1.LocalModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name: modelName,
				},
				Spec: localModelSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())
			defer k8sClient.Delete(ctx, cachedModel)

			pvcName := "test-pvc"
			pvName := pvcName + "-" + testNamespace

			pv := createTestPV(ctx, pvName, cachedModel)
			defer k8sClient.Delete(ctx, pv)

			pvc := createTestPVC(ctx, pvcName, testNamespace, pvName, cachedModel)
			defer k8sClient.Delete(ctx, pvc)

			// Expects test-pv and test-pvc to not get deleted
			pvLookupKey := types.NamespacedName{Name: pvName}
			pvcLookupKey := types.NamespacedName{Name: pvcName, Namespace: testNamespace}

			persistentVolume := &corev1.PersistentVolume{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvLookupKey, persistentVolume)
				return err == nil && persistentVolume != nil
			}, timeout, interval).Should(BeTrue())

			persistentVolumeClaim := &corev1.PersistentVolumeClaim{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvcLookupKey, persistentVolumeClaim)
				return err == nil && persistentVolumeClaim != nil
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("When creating multiple localModels", func() {
		// With two nodes and two local models, each node should have both local models
		It("Should create localModelNode correctly", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu1",
				},
				Spec: localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)
			nodeGroup2 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu2",
				},
				Spec: localModelNodeGroupSpec2,
			}
			Expect(k8sClient.Create(ctx, nodeGroup2)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup2)

			node1 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Labels: map[string]string{
						"node.kubernetes.io/instance-type": "gpu1",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}

			node2 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node2",
					Labels: map[string]string{
						"node.kubernetes.io/instance-type": "gpu2",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}

			nodes := []*corev1.Node{node1, node2}
			for _, node := range nodes {
				Expect(k8sClient.Create(ctx, node)).Should(Succeed())
				defer k8sClient.Delete(ctx, node)
			}

			model1 := "iris1"
			cachedModel1 := &v1alpha1.LocalModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name: model1,
				},
				Spec: localModelSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel1)).Should(Succeed())
			model2 := "iris2"
			cachedModel2 := &v1alpha1.LocalModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name: model2,
				},
				Spec: localModelSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel2)).Should(Succeed())

			for _, node := range nodes {
				localModelNode := &v1alpha1.LocalModelNode{}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, localModelNode)
					return err == nil && len(localModelNode.Spec.LocalModels) == 2
				}, timeout, interval).Should(BeTrue())
			}

			// Now let's test deletion - delete one model and expect one model exists
			Expect(k8sClient.Delete(ctx, cachedModel1)).Should(Succeed())
			for _, node := range nodes {
				localModelNode := &v1alpha1.LocalModelNode{}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, localModelNode)
					// Only one model exists
					return err == nil && len(localModelNode.Spec.LocalModels) == 1
				}, timeout, interval).Should(BeTrue())
			}

			// Delete the last model and expect the spec to be empty
			Expect(k8sClient.Delete(ctx, cachedModel2)).Should(Succeed())
			for _, node := range nodes {
				localModelNode := &v1alpha1.LocalModelNode{}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, localModelNode)
					return err == nil && len(localModelNode.Spec.LocalModels) == 0
				}, timeout, interval).Should(BeTrue())
			}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: model1}, cachedModel1)
				return err != nil && errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue(), "Should not get the local model after deletion")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: model2}, cachedModel2)
				return err != nil && errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue(), "Should not get the local model after deletion")
		})
	})
})

var _ = Describe("LocalModelNamespaceCache controller", func() {
	const (
		timeout             = time.Second * 10
		interval            = time.Millisecond * 250
		sourceModelUri      = "s3://mybucket/mymodel"
		modelCacheNamespace = "kserve-localmodel-jobs"
	)
	var (
		localModelNamespaceSpec = v1alpha1.LocalModelNamespaceCacheSpec{
			SourceModelUri: sourceModelUri,
			ModelSize:      resource.MustParse("123Gi"),
			NodeGroups:     []string{"gpu1"},
		}
		localModelNodeGroupSpec1 = v1alpha1.LocalModelNodeGroupSpec{
			PersistentVolumeSpec: corev1.PersistentVolumeSpec{
				AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				VolumeMode:                    ptr.To(corev1.PersistentVolumeFilesystem),
				Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")},
				StorageClassName:              "standard",
				PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/models",
						Type: ptr.To(corev1.HostPathDirectory),
					},
				},
				NodeAffinity: &corev1.VolumeNodeAffinity{
					Required: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "node.kubernetes.io/instance-type",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"gpu1"},
									},
								},
							},
						},
					},
				},
			},
			PersistentVolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")}},
			},
		}
	)

	Context("When creating a namespace-scoped local model", func() {
		It("Should create pv, pvc, localmodelnode, and update status from localmodelnode", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			testNamespace := fmt.Sprintf("test-ns-cache-%d", time.Now().UnixNano())
			namespaceObj := createTestNamespace(ctx, testNamespace)
			defer k8sClient.Delete(ctx, namespaceObj)

			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu1",
				},
				Spec: localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)

			modelName := "ns-iris"
			cachedModel := &v1alpha1.LocalModelNamespaceCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      modelName,
					Namespace: testNamespace,
				},
				Spec: localModelNamespaceSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())
			defer k8sClient.Delete(ctx, cachedModel)

			modelLookupKey := types.NamespacedName{Name: modelName, Namespace: testNamespace}
			pvLookupKey1 := types.NamespacedName{Name: modelName + "-gpu1-" + testNamespace + "-download"}
			pvcLookupKey1 := types.NamespacedName{Name: modelName + "-gpu1-" + testNamespace + "-download", Namespace: modelCacheNamespace}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				return err == nil && cachedModel.Status.ModelCopies != nil
			}, timeout, interval).Should(BeTrue())

			persistentVolume1 := &corev1.PersistentVolume{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvLookupKey1, persistentVolume1)
				return err == nil && persistentVolume1 != nil
			}, timeout, interval).Should(BeTrue(), "Download PV should be created")

			persistentVolumeClaim1 := &corev1.PersistentVolumeClaim{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvcLookupKey1, persistentVolumeClaim1)
				return err == nil && persistentVolumeClaim1 != nil
			}, timeout, interval).Should(BeTrue(), "Download PVC should be created in jobNamespace")

			nodeName1 := "ns-node-1"
			node1 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName1,
					Labels: map[string]string{
						"node.kubernetes.io/instance-type": "gpu1",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, node1)).Should(Succeed())
			defer k8sClient.Delete(ctx, node1)

			localModelNode1 := &v1alpha1.LocalModelNode{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName1}, localModelNode1)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			expectedModelInfo := v1alpha1.LocalModelInfo{
				ModelName:      modelName,
				SourceModelUri: sourceModelUri,
				Namespace:      testNamespace,
				NodeGroup:      "gpu1",
			}
			Expect(localModelNode1.Spec.LocalModels).Should(ContainElement(expectedModelInfo))

			statusKey := testNamespace + "/" + modelName
			localModelNode1.Status.ModelStatus = map[string]v1alpha1.ModelStatus{statusKey: v1alpha1.ModelDownloaded}
			Expect(k8sClient.Status().Update(ctx, localModelNode1)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				if err != nil {
					return false
				}
				if cachedModel.Status.ModelCopies == nil {
					return false
				}
				if cachedModel.Status.ModelCopies.Available != 1 || cachedModel.Status.ModelCopies.Total != 1 || cachedModel.Status.ModelCopies.Failed != 0 {
					return false
				}
				if cachedModel.Status.NodeStatus[nodeName1] != v1alpha1.NodeDownloaded {
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue(), "Node status should be downloaded")
		})

		It("Should create the serving PV and PVC in its own namespace without any consumer", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			testNamespace := fmt.Sprintf("test-ns-serving-%d", time.Now().UnixNano())
			namespaceObj := createTestNamespace(ctx, testNamespace)
			defer k8sClient.Delete(ctx, namespaceObj)

			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu1"},
				Spec:       localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)

			modelName := "ns-serving"
			cachedModel := &v1alpha1.LocalModelNamespaceCache{
				ObjectMeta: metav1.ObjectMeta{Name: modelName, Namespace: testNamespace},
				Spec:       localModelNamespaceSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())

			servingPVName := modelName + "-gpu1-" + testNamespace
			servingPVCName := modelName + "-gpu1"

			servingPV := &corev1.PersistentVolume{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: servingPVName}, servingPV)
			}, timeout, interval).Should(Succeed(), "serving PV should be created without any consumer")
			Expect(servingPV.Spec.NodeAffinity).To(Equal(localModelNodeGroupSpec1.PersistentVolumeSpec.NodeAffinity))
			Expect(servingPV.Spec.HostPath.Path).To(Equal("/models/"+v1alpha1.GetStorageSubPath(sourceModelUri)),
				"the serving PV should expose only this model's folder")

			// The download Job mounts models/<storage key> itself, so its PV keeps the root.
			downloadPV := &corev1.PersistentVolume{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: servingPVName + "-download"}, downloadPV)
			}, timeout, interval).Should(Succeed())
			Expect(downloadPV.Spec.HostPath.Path).To(Equal("/models"))

			servingPVC := &corev1.PersistentVolumeClaim{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: servingPVCName, Namespace: testNamespace}, servingPVC)
			}, timeout, interval).Should(Succeed(), "serving PVC should be created in the cache namespace")
			Expect(servingPVC.Spec.VolumeName).To(Equal(servingPVName))
			Expect(metav1.IsControlledBy(servingPVC, cachedModel)).To(BeTrue(), "the cache should own its serving PVC")

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: modelName, Namespace: testNamespace}, cachedModel)).To(Succeed())
				g.Expect(cachedModel.Status.PersistentVolumeClaims).To(Equal([]string{servingPVCName}))
				g.Expect(cachedModel.Status.SubPath).To(BeEmpty())
			}, timeout, interval).Should(Succeed(), "status should tell workloads which claim to mount, with no subPath")

			// Deleting the cache deletes the cluster-scoped serving PV, which it cannot own. The
			// admission-added pv-protection finalizer keeps it Terminating in envtest, where no
			// controller releases it.
			Expect(k8sClient.Delete(ctx, cachedModel)).Should(Succeed())
			Eventually(func() bool {
				pv := &corev1.PersistentVolume{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: servingPVName}, pv)
				return errors.IsNotFound(err) || (err == nil && !pv.DeletionTimestamp.IsZero())
			}, timeout, interval).Should(BeTrue(), "serving PV should be deleted with the cache")
		})

		It("Should not create the serving PVC when volume management is disabled", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			configMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: constants.LocalModelConfigMapName, Namespace: constants.SystemNamespace}, configMap)).Should(Succeed())
			originalData := configMap.Data["localModel"]
			configMap.Data["localModel"] = `{
				"jobNamespace": "kserve-localmodel-jobs",
				"defaultJobImage": "kserve/storage-initializer:latest",
				"disableVolumeManagement": true
			}`
			Expect(k8sClient.Update(ctx, configMap)).Should(Succeed())
			defer func() {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: constants.LocalModelConfigMapName, Namespace: constants.SystemNamespace}, configMap)).Should(Succeed())
				configMap.Data["localModel"] = originalData
				Expect(k8sClient.Update(ctx, configMap)).Should(Succeed())
			}()

			testNamespace := fmt.Sprintf("test-ns-novolume-%d", time.Now().UnixNano())
			namespaceObj := createTestNamespace(ctx, testNamespace)
			defer k8sClient.Delete(ctx, namespaceObj)

			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu1"},
				Spec:       localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)

			modelName := "ns-novolume"
			cachedModel := &v1alpha1.LocalModelNamespaceCache{
				ObjectMeta: metav1.ObjectMeta{Name: modelName, Namespace: testNamespace},
				Spec:       localModelNamespaceSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())
			defer k8sClient.Delete(ctx, cachedModel)

			// The download PVC still exists; only the serving claim is left to the user.
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: modelName + "-gpu1-" + testNamespace + "-download", Namespace: modelCacheNamespace}, &corev1.PersistentVolumeClaim{})
			}, timeout, interval).Should(Succeed())
			Consistently(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: modelName + "-gpu1", Namespace: testNamespace}, &corev1.PersistentVolumeClaim{})
				return errors.IsNotFound(err)
			}, time.Second*2, interval).Should(BeTrue(), "no serving PVC when volume management is disabled")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: modelName, Namespace: testNamespace}, cachedModel)).Should(Succeed())
			Expect(cachedModel.Status.PersistentVolumeClaims).To(BeEmpty())
			Expect(cachedModel.Status.SubPath).To(Equal(v1alpha1.GetStorageSubPath(sourceModelUri)),
				"users who manage the volume need the model's path in it")
		})

		It("Should not offer a claim backed by a PV that exposes another path", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			testNamespace := fmt.Sprintf("test-ns-stalepv-%d", time.Now().UnixNano())
			namespaceObj := createTestNamespace(ctx, testNamespace)
			defer k8sClient.Delete(ctx, namespaceObj)

			modelName := "ns-stalepv"
			// A PV left by an earlier version, or by a deleted cache of another model.
			stalePV := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: modelName + "-gpu1-" + testNamespace},
				Spec:       *localModelNodeGroupSpec1.PersistentVolumeSpec.DeepCopy(),
			}
			Expect(k8sClient.Create(ctx, stalePV)).Should(Succeed())
			defer k8sClient.Delete(ctx, stalePV)

			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu1"},
				Spec:       localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)

			cachedModel := &v1alpha1.LocalModelNamespaceCache{
				ObjectMeta: metav1.ObjectMeta{Name: modelName, Namespace: testNamespace},
				Spec:       localModelNamespaceSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())
			defer k8sClient.Delete(ctx, cachedModel)

			// The download still goes ahead.
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: stalePV.Name + "-download", Namespace: modelCacheNamespace}, &corev1.PersistentVolumeClaim{})
			}, timeout, interval).Should(Succeed())
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: modelName + "-gpu1", Namespace: testNamespace}, &corev1.PersistentVolumeClaim{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue(), "no claim should be bound to the stale PV")
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: modelName, Namespace: testNamespace}, cachedModel)).To(Succeed())
				g.Expect(cachedModel.Status.PersistentVolumeClaims).To(BeEmpty())
			}, time.Second*2, interval).Should(Succeed())
		})

		It("Should delete LocalModelNamespaceCache and run finalizer cleanup", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			testNamespace := fmt.Sprintf("test-ns-delete-%d", time.Now().UnixNano())
			namespaceObj := createTestNamespace(ctx, testNamespace)
			defer k8sClient.Delete(ctx, namespaceObj)

			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu1",
				},
				Spec: localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)

			modelName := "ns-iris-delete"
			cachedModel := &v1alpha1.LocalModelNamespaceCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      modelName,
					Namespace: testNamespace,
				},
				Spec: localModelNamespaceSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())

			modelLookupKey := types.NamespacedName{Name: modelName, Namespace: testNamespace}
			pvcLookupKey := types.NamespacedName{Name: modelName + "-gpu1-" + testNamespace + "-download", Namespace: modelCacheNamespace}

			// Wait for the model to be created with finalizer
			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				if err != nil {
					return false
				}
				for _, f := range cachedModel.ObjectMeta.Finalizers {
					if f == "local.cache.ai/localmodelnamespacecache-finalizer" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "LocalModelNamespaceCache should have finalizer")

			persistentVolumeClaim := &corev1.PersistentVolumeClaim{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvcLookupKey, persistentVolumeClaim)
				return err == nil && persistentVolumeClaim != nil
			}, timeout, interval).Should(BeTrue(), "Download PVC should be created")

			// Now delete the LocalModelNamespaceCache
			Expect(k8sClient.Delete(ctx, cachedModel)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				return err != nil && errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue(), "LocalModelNamespaceCache should be deleted after finalizer cleanup")

			// Note: In envtest, Kubernetes garbage collection is not enabled,
			// so PVCs with owner references won't be automatically deleted.
			// In a real cluster, the PVC would be garbage collected when the owner is deleted.
			// The important verification is that the finalizer ran (proven by the CR being deleted).
		})

		It("Should track both cluster-scoped and namespace-scoped models on LocalModelNode", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			testNamespace := fmt.Sprintf("test-ns-mixed-%d", time.Now().UnixNano())
			namespaceObj := createTestNamespace(ctx, testNamespace)
			defer k8sClient.Delete(ctx, namespaceObj)

			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu1",
				},
				Spec: localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)

			clusterModelName := "cluster-model"
			clusterModel := &v1alpha1.LocalModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterModelName,
				},
				Spec: v1alpha1.LocalModelCacheSpec{
					SourceModelUri: sourceModelUri,
					ModelSize:      resource.MustParse("123Gi"),
					NodeGroups:     []string{"gpu1"},
				},
			}
			Expect(k8sClient.Create(ctx, clusterModel)).Should(Succeed())
			defer k8sClient.Delete(ctx, clusterModel)

			nsModelName := "ns-model"
			nsModel := &v1alpha1.LocalModelNamespaceCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nsModelName,
					Namespace: testNamespace,
				},
				Spec: localModelNamespaceSpec,
			}
			Expect(k8sClient.Create(ctx, nsModel)).Should(Succeed())
			defer k8sClient.Delete(ctx, nsModel)

			nodeName := "mixed-node"
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"node.kubernetes.io/instance-type": "gpu1",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			defer k8sClient.Delete(ctx, node)

			localModelNode := &v1alpha1.LocalModelNode{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, localModelNode)
				if err != nil {
					return false
				}
				return len(localModelNode.Spec.LocalModels) == 2
			}, timeout, interval).Should(BeTrue(), "LocalModelNode should have both cluster and namespace models")

			clusterModelInfo := v1alpha1.LocalModelInfo{
				ModelName:      clusterModelName,
				SourceModelUri: sourceModelUri,
				Namespace:      "", // Empty for cluster-scoped
				NodeGroup:      "gpu1",
			}
			Expect(localModelNode.Spec.LocalModels).Should(ContainElement(clusterModelInfo))

			nsModelInfo := v1alpha1.LocalModelInfo{
				ModelName:      nsModelName,
				SourceModelUri: sourceModelUri,
				Namespace:      testNamespace,
				NodeGroup:      "gpu1",
			}
			Expect(localModelNode.Spec.LocalModels).Should(ContainElement(nsModelInfo))
		})

		It("Should create download PV for LocalModelNamespaceCache and verify finalizer is set", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			testNamespace := fmt.Sprintf("test-ns-pv-delete-%d", time.Now().UnixNano())
			namespaceObj := createTestNamespace(ctx, testNamespace)
			defer k8sClient.Delete(ctx, namespaceObj)

			nodeGroup1 := &v1alpha1.LocalModelNodeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu1",
				},
				Spec: localModelNodeGroupSpec1,
			}
			Expect(k8sClient.Create(ctx, nodeGroup1)).Should(Succeed())
			defer k8sClient.Delete(ctx, nodeGroup1)

			modelName := "ns-iris-pv-delete"
			cachedModel := &v1alpha1.LocalModelNamespaceCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      modelName,
					Namespace: testNamespace,
				},
				Spec: localModelNamespaceSpec,
			}
			Expect(k8sClient.Create(ctx, cachedModel)).Should(Succeed())
			defer k8sClient.Delete(ctx, cachedModel)

			modelLookupKey := types.NamespacedName{Name: modelName, Namespace: testNamespace}
			downloadPVLookupKey := types.NamespacedName{Name: modelName + "-gpu1-" + testNamespace + "-download"}

			// Wait for the model to be created with finalizer
			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelLookupKey, cachedModel)
				if err != nil {
					return false
				}
				for _, f := range cachedModel.ObjectMeta.Finalizers {
					if f == "local.cache.ai/localmodelnamespacecache-finalizer" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "LocalModelNamespaceCache should have finalizer")

			// Wait for the download PV to be created
			downloadPV := &corev1.PersistentVolume{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, downloadPVLookupKey, downloadPV)
				return err == nil && downloadPV != nil
			}, timeout, interval).Should(BeTrue(), "Download PV should be created")

			// Verify the PV does NOT have an owner reference (since PVs are cluster-scoped
			// and cannot be owned by namespace-scoped resources)
			Expect(downloadPV.OwnerReferences).To(BeEmpty(),
				"Download PV should not have owner reference for namespace-scoped LocalModelNamespaceCache")

			// Note: In a real cluster, when LocalModelNamespaceCache is deleted,
			// the finalizer explicitly deletes the PV since it cannot rely on garbage collection.
			// This is verified by the finalizer being present and the deletion logic in utils.go.
		})
	})
})

func createTestNamespace(ctx context.Context, name string) *corev1.Namespace {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())
	return namespace
}

func createTestPV(ctx context.Context, pvName string, cachedModel *v1alpha1.LocalModelCache) *corev1.PersistentVolume {
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "local.cache.ai/v1alpha1",
					Kind:       "LocalModelCache",
					Name:       cachedModel.Name,
					UID:        cachedModel.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("2Gi"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/mnt/data",
					Type: ptr.To(corev1.HostPathDirectoryOrCreate),
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, pv)).Should(Succeed())
	return pv
}

func createTestPVC(ctx context.Context, pvcName, namespace, pvName string, cachedModel *v1alpha1.LocalModelCache) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "local.cache.ai/v1alpha1",
					Kind:       "LocalModelCache",
					Name:       cachedModel.Name,
					UID:        cachedModel.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("2Gi"),
				},
			},
			VolumeName: pvName,
		},
	}
	Expect(k8sClient.Create(ctx, pvc)).Should(Succeed())
	return pvc
}
