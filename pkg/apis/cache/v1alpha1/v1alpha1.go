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

// NOTE: Boilerplate only.  Ignore this file.

// +k8s:openapi-gen=true
// +kubebuilder:object:generate=true
// +k8s:defaulter-gen=TypeMeta
// +groupName=local.cache.ai
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/talt5/localmodelcache/pkg/constants"
)

var (
	// APIVersion is the current API version used to register these objects
	APIVersion = "v1alpha1"

	// SchemeGroupVersion is group version used to register these objects
	SchemeGroupVersion = schema.GroupVersion{Group: constants.APIGroupName, Version: APIVersion}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types of this group to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// addKnownTypes registers every type belonging to this API group with the scheme.
// Keep this list in sync when adding a new type to the group.
func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(SchemeGroupVersion,
		&ClusterStorageContainer{}, &ClusterStorageContainerList{},
		&KernelCache{}, &KernelCacheList{},
		&KernelCacheCapture{}, &KernelCacheCaptureList{},
		&KernelCacheNode{}, &KernelCacheNodeList{},
		&KernelCacheNodeGroup{}, &KernelCacheNodeGroupList{},
		&LocalModelCache{}, &LocalModelCacheList{},
		&LocalModelNode{}, &LocalModelNodeList{},
		&LocalModelNodeGroup{}, &LocalModelNodeGroupList{},
		&LocalModelNamespaceCache{}, &LocalModelNamespaceCacheList{},
	)
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
	return nil
}
