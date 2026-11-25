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

package reconcilers

import (
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

// NodeChangedPredicate passes the Node events that change which models a node must hold or
// whether it can serve them: creation, deletion, and changes to its labels or readiness.
func NodeChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode := e.ObjectOld.(*corev1.Node)
			newNode := e.ObjectNew.(*corev1.Node)
			return IsNodeReady(*oldNode) != IsNodeReady(*newNode) || !reflect.DeepEqual(oldNode.Labels, newNode.Labels)
		},
	}
}

// LocalModelNodeChangedPredicate passes the LocalModelNode events that can change the status of
// the caches it lists.
func LocalModelNodeChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode := e.ObjectOld.(*v1alpha1.LocalModelNode)
			newNode := e.ObjectNew.(*v1alpha1.LocalModelNode)
			return !reflect.DeepEqual(oldNode.Spec, newNode.Spec) || !reflect.DeepEqual(oldNode.Status, newNode.Status)
		},
	}
}

// NamespaceChangedPredicate passes the Namespace events that change which caches select it, or
// whether claims can be created in it.
func NamespaceChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return !reflect.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) ||
				e.ObjectOld.GetDeletionTimestamp().IsZero() != e.ObjectNew.GetDeletionTimestamp().IsZero()
		},
	}
}
