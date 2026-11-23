/*
Copyright 2025 The KServe Authors.

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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/talt5/localmodelcache/pkg/controller/v1alpha1/localmodel/reconcilers"
)

var _ = Describe("Node changed predicate", func() {
	node := func(ready corev1.ConditionStatus, labels map[string]string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node", Labels: labels},
			Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}},
		}
	}
	gpu := map[string]string{"gpu": "true"}

	DescribeTable("update", func(oldNode, newNode *corev1.Node, want bool) {
		Expect(reconcilers.NodeChangedPredicate().Update(event.UpdateEvent{ObjectOld: oldNode, ObjectNew: newNode})).To(Equal(want))
	},
		Entry("becomes ready", node(corev1.ConditionFalse, nil), node(corev1.ConditionTrue, nil), true),
		Entry("becomes ready for the first time", &corev1.Node{}, node(corev1.ConditionTrue, nil), true),
		Entry("becomes unready", node(corev1.ConditionTrue, nil), node(corev1.ConditionUnknown, nil), true),
		Entry("is labeled while ready", node(corev1.ConditionTrue, nil), node(corev1.ConditionTrue, gpu), true),
		Entry("loses a label", node(corev1.ConditionTrue, gpu), node(corev1.ConditionTrue, nil), true),
		Entry("stays ready", node(corev1.ConditionTrue, gpu), node(corev1.ConditionTrue, gpu), false),
		Entry("stays unready", node(corev1.ConditionFalse, gpu), node(corev1.ConditionUnknown, gpu), false),
	)

	It("passes creation and deletion", func() {
		Expect(reconcilers.NodeChangedPredicate().Create(event.CreateEvent{Object: node(corev1.ConditionTrue, nil)})).To(BeTrue())
		Expect(reconcilers.NodeChangedPredicate().Delete(event.DeleteEvent{Object: node(corev1.ConditionTrue, nil)})).To(BeTrue())
	})
})
