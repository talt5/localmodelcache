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

package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/talt5/localmodelcache/pkg/constants"
)

// GetStorageKey returns the name of the folder that holds a model, which caches with the same
// source share. It is a hash of the URI and, if there are any, the storage parameters, since those
// can point the URI at another server.
func GetStorageKey(sourceModelUri string, storage *LocalModelStorageSpec) string {
	hash := sha256.Sum256([]byte(sourceModelUri))
	if storage != nil && storage.Parameters != nil && len(*storage.Parameters) > 0 {
		// Marshalling a map of strings cannot fail, and sorts its keys.
		parameters, _ := json.Marshal(*storage.Parameters)
		parametersHash := sha256.Sum256(parameters)
		// Hashing the hashes keeps any text in the URI from imitating parameters.
		hash = sha256.Sum256(append(hash[:], parametersHash[:]...))
	}
	return hex.EncodeToString(hash[:])[:16] // Use first 16 chars of hash
}

// GetStorageSubPath returns the directory, relative to the root of a cache volume, that holds
// the model: models/<storage key>.
func GetStorageSubPath(sourceModelUri string, storage *LocalModelStorageSpec) string {
	return path.Join("models", GetStorageKey(sourceModelUri, storage))
}

// StorageKey returns the name of the folder that holds the model.
func (spec *LocalModelCacheSpec) StorageKey() string {
	return GetStorageKey(spec.SourceModelUri, spec.Storage)
}

// LocalModelStorageSpec defines credential and storage configuration for model download
// +k8s:openapi-gen=true
type LocalModelStorageSpec struct {
	// The Storage Key in the secret for this object.
	// +optional
	StorageKey *string `json:"key,omitempty"`
	// Parameters to override the default storage credentials and config. They are part of the
	// model's source and cannot change: caches that differ in them do not share a folder.
	// +optional
	Parameters *map[string]string `json:"parameters,omitempty"`
}

// LocalModelCacheSpec
// +k8s:openapi-gen=true
type LocalModelCacheSpec struct {
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="StorageUri is immutable"
	// Original StorageUri
	SourceModelUri string `json:"sourceModelUri" validate:"required"`
	// Model size to make sure it does not exceed the disk space reserved for local models. The limit is defined on the NodeGroup.
	ModelSize resource.Quantity `json:"modelSize" validate:"required"`
	// group of nodes to cache the model on.
	// +kubebuilder:validation:MinItems=1
	NodeGroups []string `json:"nodeGroups" validate:"required"`
	// NamespaceSelector selects the namespaces whose workloads may mount the model. The controller
	// creates the claim <cache>-<node group> in each of them, for each node group. Left out, it
	// selects the namespaces labelled local.cache.ai/models=enabled. An empty selector selects
	// every namespace.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
	// ServiceAccountName specifies the service account to use for credential lookup.
	// The service account must exist in the download job namespace (localModel.jobNamespace).
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
	// ImagePullSecrets is a single kubernetes.io/dockerconfigjson secret in the download
	// job namespace used to authenticate OCI (oci://) imports. The list shape matches
	// PodSpec.imagePullSecrets; MaxItems=1 because credential merging is not supported.
	// Combine credentials for multiple registries into one secret.
	// Credentials from serviceAccountName and storage are not used for oci:// sources.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:XValidation:rule="self.all(s, s.name != '')",message="imagePullSecrets.name must be non-empty"
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	// +optional
	Storage *LocalModelStorageSpec `json:"storage,omitempty"`
}

// LocalModelCache
// +k8s:openapi-gen=true
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope="Cluster"
// +kubebuilder:printcolumn:name="SubPath",type="string",JSONPath=".status.subPath"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type LocalModelCache struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalModelCacheSpec   `json:"spec,omitempty"`
	Status LocalModelCacheStatus `json:"status,omitempty"`
}

// LocalModelCacheList
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
type LocalModelCacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalModelCache `json:"items" validate:"required"`
}

// WorkloadNamespaceSelector returns the selector of the namespaces that get a claim.
func (spec *LocalModelCacheSpec) WorkloadNamespaceSelector() (labels.Selector, error) {
	if spec.NamespaceSelector == nil {
		return labels.SelectorFromSet(labels.Set{constants.ModelsNamespaceLabelKey: constants.ModelsNamespaceLabelValue}), nil
	}
	return metav1.LabelSelectorAsSelector(spec.NamespaceSelector)
}

// MatchStorageURI checks if storageUri matches the sourceModelUri or is a subdirectory of it
func (spec *LocalModelCacheSpec) MatchStorageURI(storageUri string) bool {
	return MatchStorageURI(spec.SourceModelUri, storageUri)
}
