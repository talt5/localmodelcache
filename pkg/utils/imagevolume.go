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
	"k8s.io/apimachinery/pkg/version"
)

// ServerVersioner is the single-method discovery interface required by
// CheckImageVolumeCompatibility. It matches the signature of
// discovery.DiscoveryInterface.ServerVersion, so the real discovery client
// satisfies it without wrapping.
type ServerVersioner interface {
	ServerVersion() (*version.Info, error)
}

// ImageVolumeStatus is the compatibility level returned by CheckImageVolumeCompatibility.
type ImageVolumeStatus int

const (
	// ImageVolumeOK — K8s ≥ 1.35: beta defaults-on (1.35) or stable (≥ 1.36).
	ImageVolumeOK ImageVolumeStatus = iota
	// ImageVolumeNeedsGate — K8s 1.33–1.34: beta, requires --feature-gates=ImageVolume=true.
	// subPath on ImageVolume VolumeMounts is supported at this level.
	ImageVolumeNeedsGate
	// ImageVolumeSubPathUnsupported — K8s 1.31–1.32: alpha, requires --feature-gates=ImageVolume=true.
	// subPath on ImageVolume VolumeMounts is explicitly forbidden at this level.
	ImageVolumeSubPathUnsupported
	// ImageVolumeUnsupported — K8s < 1.31: ImageVolume not available at all.
	ImageVolumeUnsupported
	// ImageVolumeUnknown — discovery or minor-version parse failure; callers should not warn.
	ImageVolumeUnknown
)

// ImageVolumeCheckResult is returned by CheckImageVolumeCompatibility.
type ImageVolumeCheckResult struct {
	Status ImageVolumeStatus
	// Major and Minor are the raw version strings from ServerVersion, populated
	// only when Status is NeedsGate or Unsupported (for use in condition messages).
	Major string
	Minor string
}
