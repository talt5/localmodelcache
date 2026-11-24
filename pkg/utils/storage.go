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

package utils

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/talt5/localmodelcache/pkg/constants"
)

type StorageMountParams struct {
	MountPath  string
	SubPath    string
	VolumeName string
	PVCName    string
	ReadOnly   bool
	// DefaultVolumeSource overrides the emptyDir used when PVCName is empty.
	// When nil, emptyDir is used (backward-compatible default).
	DefaultVolumeSource *corev1.VolumeSource
	// MountsPerPath identifies an existing mount by (VolumeName, MountPath, SubPath) rather
	// than by VolumeName alone, so one volume can be mounted at several paths in the same
	// container. Leave it unset unless the caller owns every mount on the volume: with the
	// default, a mount that already carries the volume name suppresses this one, which is
	// how a workload overrides an injected volume by declaring its own.
	MountsPerPath bool
}

// defaultHuggingFaceEnvVars are the download settings AddDefaultHuggingFaceEnvVars
// puts on a storage-initializer.
var defaultHuggingFaceEnvVars = []corev1.EnvVar{
	{
		Name:  "HF_HUB_ENABLE_HF_TRANSFER",
		Value: "1",
	},
	{
		Name:  "HF_XET_HIGH_PERFORMANCE",
		Value: "1",
	},
	{
		Name:  "HF_XET_NUM_CONCURRENT_RANGE_GETS",
		Value: "8",
	},
}

// MaxOCISourcesPerPod is the maximum number of OCI modelcar sidecars that can be
// injected into a single pod. This prevents resource exhaustion from unbounded
// sidecar injection — each OCI URI adds 2 containers (sidecar + init) and 1 volume.
const MaxOCISourcesPerPod = 10

// ParseOciScheme splits any OCI storageUri into its mode, a normalized oci:// URI, and
// whether it is an OCI URI at all.
//
//   - "oci+native://reg/img:tag" → ("native", "oci://reg/img:tag", true)
//   - "oci+modelcar://reg/img:tag" → ("modelcar", "oci://reg/img:tag", true)
//   - "oci+fetch://reg/img:tag"   → ("fetch",    "oci://reg/img:tag", true)
//   - "oci://reg/img:tag"         → ("",         "oci://reg/img:tag", true)  // mode resolved later
//   - "s3://bucket/key"           → ("",         "s3://bucket/key",  false)
func ParseOciScheme(uri string) (mode string, normalizedURI string, isOci bool) {
	const ociPlus = "oci+"
	if strings.HasPrefix(uri, ociPlus) {
		rest := uri[len(ociPlus):]
		sepIdx := strings.Index(rest, "://")
		if sepIdx > 0 {
			return rest[:sepIdx], constants.OciURIPrefix + rest[sepIdx+3:], true
		}
	}
	if strings.HasPrefix(uri, constants.OciURIPrefix) {
		return "", uri, true
	}
	return "", uri, false
}
