/*
Copyright 2026 The KServe Authors.

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

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	kernelcachetypes "github.com/talt5/localmodelcache/pkg/kernelcache/types"
)

// KernelCacheConfigKeyName is the ConfigMap key holding KernelCacheConfig as JSON.
const KernelCacheConfigKeyName = "kernelcache"

const (
	DefaultKernelCacheMCVImage                                = "ghcr.io/talt5/kserve-mcv:latest-minimal"
	DefaultKernelCachePrefetchImage                           = "registry.access.redhat.com/ubi9/ubi-minimal:latest"
	DefaultKernelCacheMountType                               = "oci"
	DefaultKernelCacheJobNamespace                            = "kserve-kernelcache-jobs"
	DefaultKernelCacheJobTTLSeconds                     int32 = 600
	DefaultKernelCacheRuntimeImagePrefetchJobTTLSeconds int32 = 86400
	DefaultKernelCacheReconcileIntervalSeconds          int64 = 300
	DefaultKernelCacheMCVCaptureReadinessTimeoutSeconds int64 = 600
	// DefaultKernelCacheRegistryTokenTTLSeconds is the default lifetime of a
	// registry ServiceAccount token issued for KernelCache access.
	DefaultKernelCacheRegistryTokenTTLSeconds   int64 = 600
	DefaultKernelCacheAbandonedCapturePolicy          = "retain"
	DefaultKernelCacheArtifactSecurityMode            = "cert"
	DefaultKernelCacheArtifactSigningProfileRef       = "kernelcache-signer"
	DefaultKernelCacheArtifactTrustBundle             = "kserve/kernelcache-root-ca"
	DefaultKernelCacheArtifactSubjectRegexp           = "spiffe://kserve/kernelcache-signer"
	// KernelCacheRegistryAuthTypeNone disables registry credential provisioning.
	KernelCacheRegistryAuthTypeNone = "none"
	// KernelCacheRegistryAuthTypeServiceAccountToken uses the Kubernetes
	// TokenRequest API to issue short-lived registry credentials.
	KernelCacheRegistryAuthTypeServiceAccountToken = "serviceAccountToken"
)

type KernelCacheRuntimeImagePrefetchConfig struct {
	Enabled                    bool   `json:"enabled,omitempty"`
	JobTTLSecondsAfterFinished *int32 `json:"jobTTLSecondsAfterFinished,omitempty"`
}

// KernelCacheConfig contains the shared KernelCache configuration loaded from
// the kernelcache entry of the kserve-localmodel-config ConfigMap.
type KernelCacheConfig struct {
	Enabled                           bool                                  `json:"enabled"`
	DefaultSidecarInjection           bool                                  `json:"defaultSidecarInjection"`
	DefaultMountType                  string                                `json:"defaultMountType,omitempty"`
	DefaultNodeGroup                  string                                `json:"defaultNodeGroup,omitempty"`
	JobNamespace                      string                                `json:"jobNamespace"`
	MCVImage                          string                                `json:"mcvImage,omitempty"`
	MCVCaptureReadinessTimeoutSeconds int64                                 `json:"mcvCaptureReadinessTimeoutSeconds,omitempty"`
	PrefetchImage                     string                                `json:"prefetchImage,omitempty"`
	RuntimeImagePrefetch              KernelCacheRuntimeImagePrefetchConfig `json:"runtimeImagePrefetch,omitempty"`
	JobTTLSecondsAfterFinished        *int32                                `json:"jobTTLSecondsAfterFinished,omitempty"`
	ReconcileIntervalSeconds          *int64                                `json:"reconcileIntervalSeconds,omitempty"`
	AbandonedCapturePolicy            string                                `json:"abandonedCapturePolicy,omitempty"`
	// Registry configures the OCI registry used by capture and prefetch operations.
	Registry KernelCacheRegistryConfig `json:"registry,omitempty"`
	// ArtifactSecurity controls signing of completed capture artifacts.
	ArtifactSecurity KernelCacheArtifactSecurityConfig `json:"artifactSecurity,omitempty"`
	// CachePaths is resolved for each Pod and is not read from the ConfigMap.
	// +listType=atomic
	CachePaths []v1alpha1.KernelCachePath `json:"-"`
	// TargetImage is resolved for each Pod and is not read from the ConfigMap.
	TargetImage string `json:"-"`
	// ReadinessEnv is resolved for each Pod and is not read from the ConfigMap.
	// +listType=atomic
	ReadinessEnv []corev1.EnvVar `json:"-"`
	// ReporterSecretName is the per-capture reporter Secret mounted into MCV.
	ReporterSecretName string `json:"-"`
	// CredentialSecretName is the per-capture registry Secret mounted into MCV.
	CredentialSecretName string `json:"-"`
	// CaptureName identifies the KernelCacheCapture associated with the Pod.
	CaptureName string `json:"-"`
	// CaptureNamespace is the namespace of the associated KernelCacheCapture.
	CaptureNamespace string `json:"-"`
	// CaptureSessionID identifies the active capture session for the Pod.
	CaptureSessionID string `json:"-"`
}

// KernelCacheRegistryConfig defines the registry endpoint, trust bundle, and
// authentication used by KernelCache capture and prefetch operations.
type KernelCacheRegistryConfig struct {
	// Endpoint is the OCI registry host and optional port.
	Endpoint string `json:"endpoint,omitempty"`
	// Insecure allows MCV to use plain HTTP for registry operations. The default is false.
	Insecure bool `json:"insecure,omitempty"`
	// Auth configures how registry credentials are provisioned.
	Auth KernelCacheRegistryAuth `json:"auth,omitempty"`
	// CAConfigMapRef optionally references a ConfigMap key containing the
	// registry CA bundle.
	CAConfigMapRef *KernelCacheConfigMapKeyRef `json:"caConfigMapRef,omitempty"`
}

// KernelCacheRegistryAuth defines how KernelCache obtains registry credentials.
type KernelCacheRegistryAuth struct {
	// Type selects none or serviceAccountToken authentication. The zero value
	// is treated as none.
	Type string `json:"type,omitempty"`
	// TokenTTLSeconds is the lifetime of a token issued through TokenRequest.
	// The default is 600 seconds when serviceAccountToken is selected.
	TokenTTLSeconds int64 `json:"tokenTTLSeconds,omitempty"`
	// PushRoleRef identifies the Role or ClusterRole bound to the per-capture
	// ServiceAccount used to publish captured images.
	PushRoleRef *KernelCacheRegistryRoleRef `json:"pushRoleRef,omitempty"`
	// PullRoleRef identifies the Role or ClusterRole bound to the prefetch
	// ServiceAccount used to pull cache images.
	PullRoleRef *KernelCacheRegistryRoleRef `json:"pullRoleRef,omitempty"`
}

// KernelCacheRegistryRoleRef identifies the Kubernetes Role or ClusterRole
// used to authorize registry access.
type KernelCacheRegistryRoleRef struct {
	// Kind is Role or ClusterRole.
	Kind string `json:"kind"`
	// Name is the name of the referenced Role or ClusterRole.
	Name string `json:"name"`
}

// KernelCacheConfigMapKeyRef identifies a value in a ConfigMap.
type KernelCacheConfigMapKeyRef struct {
	// Name is the name of the referenced ConfigMap.
	Name string `json:"name"`
	// Key is the data key containing the referenced value.
	Key string `json:"key"`
}

// Validate checks registry authentication settings without provisioning any
// credentials. Credential lifecycle handling is performed by consumers.
func (c *KernelCacheRegistryConfig) Validate() error {
	if c.CAConfigMapRef != nil && (c.CAConfigMapRef.Name == "" || c.CAConfigMapRef.Key == "") {
		return errors.New("registry.caConfigMapRef requires name and key")
	}
	if c.Insecure && c.CAConfigMapRef != nil {
		return errors.New("registry.insecure cannot be used with registry.caConfigMapRef")
	}
	if c.Insecure && c.Auth.Type == KernelCacheRegistryAuthTypeServiceAccountToken {
		return errors.New("registry.insecure cannot be used with registry.auth.type serviceAccountToken")
	}
	switch c.Auth.Type {
	case "", KernelCacheRegistryAuthTypeNone:
		return nil
	case KernelCacheRegistryAuthTypeServiceAccountToken:
		if c.Endpoint == "" || strings.ContainsAny(c.Endpoint, "/ \t\n") {
			return errors.New("registry.endpoint must be a registry host with optional port")
		}
		if err := validateKernelCacheRegistryRoleRef("pushRoleRef", c.Auth.PushRoleRef); err != nil {
			return err
		}
		if err := validateKernelCacheRegistryRoleRef("pullRoleRef", c.Auth.PullRoleRef); err != nil {
			return err
		}
		if c.Auth.TokenTTLSeconds != 0 &&
			(c.Auth.TokenTTLSeconds < DefaultKernelCacheRegistryTokenTTLSeconds || c.Auth.TokenTTLSeconds > 3600) {
			return errors.New("registry.auth.tokenTTLSeconds must be between 600 and 3600")
		}
	default:
		return fmt.Errorf("unsupported registry.auth.type %q", c.Auth.Type)
	}
	return nil
}

func validateKernelCacheRegistryRoleRef(field string, ref *KernelCacheRegistryRoleRef) error {
	if ref == nil || ref.Kind == "" || ref.Name == "" {
		return fmt.Errorf("registry.auth.%s requires kind and name", field)
	}
	if ref.Kind != "Role" && ref.Kind != "ClusterRole" {
		return fmt.Errorf("registry.auth.%s.kind must be Role or ClusterRole", field)
	}
	return nil
}

// KernelCacheArtifactSecurityConfig configures signing of completed artifacts.
type KernelCacheArtifactSecurityConfig struct {
	Mode          string                        `json:"mode,omitempty"`
	FailurePolicy string                        `json:"failurePolicy,omitempty"`
	Cert          KernelCacheArtifactCertConfig `json:"cert,omitempty"`
}

// KernelCacheArtifactCertConfig contains certificate signing profile settings.
type KernelCacheArtifactCertConfig struct {
	SigningProfileRef string `json:"signingProfileRef,omitempty"`
	TrustBundle       string `json:"trustBundle,omitempty"`
	TrustBundleKey    string `json:"trustBundleKey,omitempty"`
	SubjectRegexp     string `json:"subjectRegexp,omitempty"`
}

// ToSecurityConfig converts ConfigMap data to the security package contract.
func (c *KernelCacheArtifactSecurityConfig) ToSecurityConfig() kernelcachetypes.SecurityConfig {
	mode := c.Mode
	if mode == "" || mode == "none" {
		mode = string(kernelcachetypes.ModeDisabled)
	}
	return kernelcachetypes.SecurityConfig{
		Mode:          kernelcachetypes.Mode(mode),
		FailurePolicy: kernelcachetypes.FailurePolicy(c.FailurePolicy),
		Cert: kernelcachetypes.CertConfig{
			TrustBundle:    c.Cert.TrustBundle,
			TrustBundleKey: c.Cert.TrustBundleKey,
			SubjectRegexp:  c.Cert.SubjectRegexp,
		},
	}
}

// DeepCopy returns an independent configuration for one Pod admission.
func (c *KernelCacheConfig) DeepCopy() *KernelCacheConfig {
	out := *c
	out.CachePaths = append([]v1alpha1.KernelCachePath(nil), c.CachePaths...)
	out.ReadinessEnv = (&corev1.Container{Env: c.ReadinessEnv}).DeepCopy().Env
	if c.JobTTLSecondsAfterFinished != nil {
		value := *c.JobTTLSecondsAfterFinished
		out.JobTTLSecondsAfterFinished = &value
	}
	if c.RuntimeImagePrefetch.JobTTLSecondsAfterFinished != nil {
		value := *c.RuntimeImagePrefetch.JobTTLSecondsAfterFinished
		out.RuntimeImagePrefetch.JobTTLSecondsAfterFinished = &value
	}
	if c.ReconcileIntervalSeconds != nil {
		value := *c.ReconcileIntervalSeconds
		out.ReconcileIntervalSeconds = &value
	}
	if c.Registry.CAConfigMapRef != nil {
		value := *c.Registry.CAConfigMapRef
		out.Registry.CAConfigMapRef = &value
	}
	if c.Registry.Auth.PushRoleRef != nil {
		value := *c.Registry.Auth.PushRoleRef
		out.Registry.Auth.PushRoleRef = &value
	}
	if c.Registry.Auth.PullRoleRef != nil {
		value := *c.Registry.Auth.PullRoleRef
		out.Registry.Auth.PullRoleRef = &value
	}
	return &out
}

// NewKernelCacheConfig parses the KernelCache configuration and applies source defaults.
func NewKernelCacheConfig(configMap *corev1.ConfigMap) (*KernelCacheConfig, error) {
	jobTTLSeconds := DefaultKernelCacheJobTTLSeconds
	runtimeImagePrefetchJobTTLSeconds := DefaultKernelCacheRuntimeImagePrefetchJobTTLSeconds
	reconcileIntervalSeconds := DefaultKernelCacheReconcileIntervalSeconds
	kernelCacheConfig := &KernelCacheConfig{
		DefaultSidecarInjection:           true,
		DefaultMountType:                  DefaultKernelCacheMountType,
		JobNamespace:                      DefaultKernelCacheJobNamespace,
		MCVImage:                          DefaultKernelCacheMCVImage,
		MCVCaptureReadinessTimeoutSeconds: DefaultKernelCacheMCVCaptureReadinessTimeoutSeconds,
		PrefetchImage:                     DefaultKernelCachePrefetchImage,
		RuntimeImagePrefetch: KernelCacheRuntimeImagePrefetchConfig{
			JobTTLSecondsAfterFinished: &runtimeImagePrefetchJobTTLSeconds,
		},
		JobTTLSecondsAfterFinished: &jobTTLSeconds,
		ReconcileIntervalSeconds:   &reconcileIntervalSeconds,
		AbandonedCapturePolicy:     DefaultKernelCacheAbandonedCapturePolicy,
		Registry: KernelCacheRegistryConfig{
			Insecure: false,
			Auth:     KernelCacheRegistryAuth{Type: KernelCacheRegistryAuthTypeNone},
		},
		ArtifactSecurity: KernelCacheArtifactSecurityConfig{
			Mode:          DefaultKernelCacheArtifactSecurityMode,
			FailurePolicy: string(kernelcachetypes.FailurePolicyReject),
			Cert: KernelCacheArtifactCertConfig{
				SigningProfileRef: DefaultKernelCacheArtifactSigningProfileRef,
				TrustBundle:       DefaultKernelCacheArtifactTrustBundle,
				SubjectRegexp:     DefaultKernelCacheArtifactSubjectRegexp,
			},
		},
	}
	if kernelCache, ok := configMap.Data[KernelCacheConfigKeyName]; ok {
		if err := json.Unmarshal([]byte(kernelCache), kernelCacheConfig); err != nil {
			return nil, fmt.Errorf("unable to unmarshal kernelcache: %w", err)
		}
	}
	if kernelCacheConfig.MCVImage == "" {
		kernelCacheConfig.MCVImage = DefaultKernelCacheMCVImage
	}
	if kernelCacheConfig.PrefetchImage == "" {
		kernelCacheConfig.PrefetchImage = DefaultKernelCachePrefetchImage
	}
	if kernelCacheConfig.DefaultMountType == "" {
		kernelCacheConfig.DefaultMountType = DefaultKernelCacheMountType
	}
	if kernelCacheConfig.JobNamespace == "" {
		kernelCacheConfig.JobNamespace = DefaultKernelCacheJobNamespace
	}
	if kernelCacheConfig.JobTTLSecondsAfterFinished == nil {
		value := DefaultKernelCacheJobTTLSeconds
		kernelCacheConfig.JobTTLSecondsAfterFinished = &value
	}
	if kernelCacheConfig.RuntimeImagePrefetch.JobTTLSecondsAfterFinished == nil {
		value := DefaultKernelCacheRuntimeImagePrefetchJobTTLSeconds
		kernelCacheConfig.RuntimeImagePrefetch.JobTTLSecondsAfterFinished = &value
	}
	if kernelCacheConfig.ReconcileIntervalSeconds == nil {
		value := DefaultKernelCacheReconcileIntervalSeconds
		kernelCacheConfig.ReconcileIntervalSeconds = &value
	}
	if kernelCacheConfig.AbandonedCapturePolicy == "" {
		kernelCacheConfig.AbandonedCapturePolicy = DefaultKernelCacheAbandonedCapturePolicy
	}
	if kernelCacheConfig.AbandonedCapturePolicy != "retain" && kernelCacheConfig.AbandonedCapturePolicy != "delete" {
		return nil, errors.New("kernelcache.abandonedCapturePolicy must be retain or delete")
	}
	if kernelCacheConfig.DefaultMountType != "" && kernelCacheConfig.DefaultMountType != "oci" {
		return nil, fmt.Errorf("kernelcache.defaultMountType must be oci, got %q", kernelCacheConfig.DefaultMountType)
	}
	if kernelCacheConfig.MCVCaptureReadinessTimeoutSeconds <= 0 {
		return nil, errors.New("kernelcache.mcvCaptureReadinessTimeoutSeconds must be greater than zero")
	}
	if kernelCacheConfig.Registry.Auth.Type == KernelCacheRegistryAuthTypeServiceAccountToken &&
		kernelCacheConfig.Registry.Auth.TokenTTLSeconds == 0 {
		kernelCacheConfig.Registry.Auth.TokenTTLSeconds = DefaultKernelCacheRegistryTokenTTLSeconds
	}
	if err := kernelCacheConfig.Registry.Validate(); err != nil {
		return nil, err
	}
	if kernelCacheConfig.ArtifactSecurity.Mode == "" {
		kernelCacheConfig.ArtifactSecurity.Mode = DefaultKernelCacheArtifactSecurityMode
	}
	if kernelCacheConfig.ArtifactSecurity.Mode == DefaultKernelCacheArtifactSecurityMode {
		if kernelCacheConfig.ArtifactSecurity.Cert.SigningProfileRef == "" {
			kernelCacheConfig.ArtifactSecurity.Cert.SigningProfileRef = DefaultKernelCacheArtifactSigningProfileRef
		}
		if kernelCacheConfig.ArtifactSecurity.Cert.TrustBundle == "" {
			kernelCacheConfig.ArtifactSecurity.Cert.TrustBundle = DefaultKernelCacheArtifactTrustBundle
		}
		if kernelCacheConfig.ArtifactSecurity.Cert.SubjectRegexp == "" {
			kernelCacheConfig.ArtifactSecurity.Cert.SubjectRegexp = DefaultKernelCacheArtifactSubjectRegexp
		}
	}
	if kernelCacheConfig.ArtifactSecurity.FailurePolicy == "" {
		kernelCacheConfig.ArtifactSecurity.FailurePolicy = string(kernelcachetypes.FailurePolicyReject)
	}
	securityConfig := kernelCacheConfig.ArtifactSecurity.ToSecurityConfig()
	securityConfig.Default()
	if err := securityConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid kernelcache.artifactSecurity: %w", err)
	}
	if securityConfig.FailurePolicy != kernelcachetypes.FailurePolicyReject {
		return nil, errors.New("kernelcache.artifactSecurity.failurePolicy must be reject")
	}
	if securityConfig.Mode == kernelcachetypes.ModeCert && strings.TrimSpace(kernelCacheConfig.ArtifactSecurity.Cert.SigningProfileRef) == "" {
		return nil, errors.New("kernelcache.artifactSecurity.cert.signingProfileRef is required for cert mode")
	}
	return kernelCacheConfig, nil
}
