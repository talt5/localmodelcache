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
	"fmt"
	"testing"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	kernelcachetypes "github.com/kserve/kserve/pkg/kernelcache/types"
)

func TestNewKernelCacheConfigDefaults(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	for _, configMap := range []*corev1.ConfigMap{
		{},
		{Data: map[string]string{KernelCacheConfigKeyName: `{}`}},
	} {
		config, err := NewKernelCacheConfig(configMap)
		g.Expect(err).ShouldNot(gomega.HaveOccurred())
		g.Expect(config.Enabled).To(gomega.BeFalse())
		g.Expect(config.DefaultSidecarInjection).To(gomega.BeTrue())
		g.Expect(config.DefaultMountType).To(gomega.Equal(DefaultKernelCacheMountType))
		g.Expect(config.DefaultNodeGroup).To(gomega.BeEmpty())
		g.Expect(config.JobNamespace).To(gomega.Equal(DefaultKernelCacheJobNamespace))
		g.Expect(config.MCVImage).To(gomega.Equal(DefaultKernelCacheMCVImage))
		g.Expect(config.PrefetchImage).To(gomega.Equal(DefaultKernelCachePrefetchImage))
		g.Expect(config.RuntimeImagePrefetch.Enabled).To(gomega.BeFalse())
		g.Expect(config.RuntimeImagePrefetch.JobTTLSecondsAfterFinished).ToNot(gomega.BeNil())
		g.Expect(*config.RuntimeImagePrefetch.JobTTLSecondsAfterFinished).To(gomega.Equal(DefaultKernelCacheRuntimeImagePrefetchJobTTLSeconds))
		g.Expect(config.MCVCaptureReadinessTimeoutSeconds).To(gomega.Equal(DefaultKernelCacheMCVCaptureReadinessTimeoutSeconds))
		g.Expect(config.AbandonedCapturePolicy).To(gomega.Equal(DefaultKernelCacheAbandonedCapturePolicy))
		g.Expect(config.Registry.Endpoint).To(gomega.BeEmpty())
		g.Expect(config.Registry.Insecure).To(gomega.BeFalse())
		g.Expect(config.Registry.CAConfigMapRef).To(gomega.BeNil())
		g.Expect(config.Registry.Auth.Type).To(gomega.Equal(KernelCacheRegistryAuthTypeNone))
		g.Expect(config.Registry.Auth.TokenTTLSeconds).To(gomega.Equal(int64(0)))
		g.Expect(config.Registry.Auth.PushRoleRef).To(gomega.BeNil())
		g.Expect(config.Registry.Auth.PullRoleRef).To(gomega.BeNil())
		g.Expect(config.ArtifactSecurity.Mode).To(gomega.Equal(DefaultKernelCacheArtifactSecurityMode))
		g.Expect(config.ArtifactSecurity.FailurePolicy).To(gomega.Equal(string(kernelcachetypes.FailurePolicyReject)))
		g.Expect(config.ArtifactSecurity.Cert).To(gomega.Equal(KernelCacheArtifactCertConfig{
			SigningProfileRef: DefaultKernelCacheArtifactSigningProfileRef,
			TrustBundle:       DefaultKernelCacheArtifactTrustBundle,
			SubjectRegexp:     DefaultKernelCacheArtifactSubjectRegexp,
		}))
		g.Expect(config.JobTTLSecondsAfterFinished).ToNot(gomega.BeNil())
		g.Expect(*config.JobTTLSecondsAfterFinished).To(gomega.Equal(DefaultKernelCacheJobTTLSeconds))
		g.Expect(config.ReconcileIntervalSeconds).ToNot(gomega.BeNil())
		g.Expect(*config.ReconcileIntervalSeconds).To(gomega.Equal(DefaultKernelCacheReconcileIntervalSeconds))
	}
}

func TestNewKernelCacheConfigAcceptsCertificateArtifactSecurity(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{
			"artifactSecurity": {
				"mode": "cert",
				"failurePolicy": "reject",
				"cert": {
					"signingProfileRef": "kernelcache-signer",
					"trustBundle": "kserve/kernelcache-root-ca",
					"subjectRegexp": "spiffe://kserve/kernelcache-signer"
				}
			}
		}`,
	}}

	config, err := NewKernelCacheConfig(configMap)
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
	g.Expect(config.ArtifactSecurity.Mode).To(gomega.Equal("cert"))
	g.Expect(config.ArtifactSecurity.FailurePolicy).To(gomega.Equal(string(kernelcachetypes.FailurePolicyReject)))
	g.Expect(config.ArtifactSecurity.Cert).To(gomega.Equal(KernelCacheArtifactCertConfig{
		SigningProfileRef: "kernelcache-signer",
		TrustBundle:       "kserve/kernelcache-root-ca",
		SubjectRegexp:     "spiffe://kserve/kernelcache-signer",
	}))
}

func TestNewKernelCacheConfigAllowsDisabledArtifactSecurity(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{"artifactSecurity":{"mode":"none"}}`,
	}}

	config, err := NewKernelCacheConfig(configMap)
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
	g.Expect(config.ArtifactSecurity.Mode).To(gomega.Equal("none"))
}

func TestKernelCacheRegistryConfigRejectsIncompleteCAConfigMapRef(t *testing.T) {
	tests := []struct {
		name string
		ref  *KernelCacheConfigMapKeyRef
	}{
		{name: "missing name", ref: &KernelCacheConfigMapKeyRef{Key: "bundle.pem"}},
		{name: "missing key", ref: &KernelCacheConfigMapKeyRef{Name: "registry-ca"}},
		{name: "missing name and key", ref: &KernelCacheConfigMapKeyRef{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			config := KernelCacheRegistryConfig{CAConfigMapRef: test.ref}
			g.Expect(config.Validate()).To(gomega.MatchError("registry.caConfigMapRef requires name and key"))
		})
	}
}

func TestKernelCacheRegistryConfigRejectsInsecureWithCAConfigMapRef(t *testing.T) {
	g := gomega.NewWithT(t)
	config := KernelCacheRegistryConfig{
		Insecure: true,
		CAConfigMapRef: &KernelCacheConfigMapKeyRef{
			Name: "registry-ca",
			Key:  "ca.crt",
		},
	}

	g.Expect(config.Validate()).To(gomega.MatchError("registry.insecure cannot be used with registry.caConfigMapRef"))
}

func TestNewKernelCacheConfigUsesServiceAccountTokenRegistry(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{
			"registry": {
				"endpoint": "registry.example:5000",
				"caConfigMapRef": {"name": "custom-ca", "key": "bundle.pem"},
				"auth": {
					"type": "serviceAccountToken",
					"pushRoleRef": {"kind": "ClusterRole", "name": "registry-pusher"},
					"pullRoleRef": {"kind": "ClusterRole", "name": "registry-puller"}
				}
			}
		}`,
	}}

	config, err := NewKernelCacheConfig(configMap)
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
	g.Expect(config.Registry.Endpoint).To(gomega.Equal("registry.example:5000"))
	g.Expect(config.Registry.Insecure).To(gomega.BeFalse())
	g.Expect(config.Registry.CAConfigMapRef).To(gomega.Equal(&KernelCacheConfigMapKeyRef{
		Name: "custom-ca",
		Key:  "bundle.pem",
	}))
	g.Expect(config.Registry.Auth.Type).To(gomega.Equal(KernelCacheRegistryAuthTypeServiceAccountToken))
	g.Expect(config.Registry.Auth.TokenTTLSeconds).To(gomega.Equal(DefaultKernelCacheRegistryTokenTTLSeconds))
	g.Expect(config.Registry.Auth.PushRoleRef).To(gomega.Equal(&KernelCacheRegistryRoleRef{
		Kind: "ClusterRole",
		Name: "registry-pusher",
	}))
	g.Expect(config.Registry.Auth.PullRoleRef).To(gomega.Equal(&KernelCacheRegistryRoleRef{
		Kind: "ClusterRole",
		Name: "registry-puller",
	}))
}

func TestNewKernelCacheConfigRejectsInsecureServiceAccountTokenRegistry(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{
			"registry": {
				"endpoint": "registry.example:5000",
				"insecure": true,
				"auth": {
					"type": "serviceAccountToken",
					"pushRoleRef": {"kind": "ClusterRole", "name": "registry-pusher"},
					"pullRoleRef": {"kind": "ClusterRole", "name": "registry-puller"}
				}
			}
		}`,
	}}

	_, err := NewKernelCacheConfig(configMap)
	g.Expect(err).To(gomega.MatchError("registry.insecure cannot be used with registry.auth.type serviceAccountToken"))
}

func TestNewKernelCacheConfigRejectsUnsupportedRegistryAuthType(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{"registry":{"auth":{"type":"unsupported"}}}`,
	}}

	_, err := NewKernelCacheConfig(configMap)
	g.Expect(err).To(gomega.MatchError(`unsupported registry.auth.type "unsupported"`))
}

func TestKernelCacheRegistryConfigRejectsInvalidServiceAccountTokenSettings(t *testing.T) {
	tests := []struct {
		name        string
		config      KernelCacheRegistryConfig
		expectedErr string
	}{
		{
			name: "missing endpoint",
			config: KernelCacheRegistryConfig{
				Auth: KernelCacheRegistryAuth{
					Type: KernelCacheRegistryAuthTypeServiceAccountToken,
				},
			},
			expectedErr: "registry.endpoint must be a registry host with optional port",
		},
		{
			name: "missing role reference",
			config: KernelCacheRegistryConfig{
				Endpoint: "registry.example:5000",
				Auth: KernelCacheRegistryAuth{
					Type: KernelCacheRegistryAuthTypeServiceAccountToken,
				},
			},
			expectedErr: "registry.auth.pushRoleRef requires kind and name",
		},
		{
			name: "missing pull role reference",
			config: KernelCacheRegistryConfig{
				Endpoint: "registry.example:5000",
				Auth: KernelCacheRegistryAuth{
					Type:        KernelCacheRegistryAuthTypeServiceAccountToken,
					PushRoleRef: &KernelCacheRegistryRoleRef{Kind: "Role", Name: "pusher"},
				},
			},
			expectedErr: "registry.auth.pullRoleRef requires kind and name",
		},
		{
			name: "invalid role reference kind",
			config: KernelCacheRegistryConfig{
				Endpoint: "registry.example:5000",
				Auth: KernelCacheRegistryAuth{
					Type:        KernelCacheRegistryAuthTypeServiceAccountToken,
					PushRoleRef: &KernelCacheRegistryRoleRef{Kind: "ServiceAccount", Name: "pusher"},
					PullRoleRef: &KernelCacheRegistryRoleRef{Kind: "Role", Name: "puller"},
				},
			},
			expectedErr: "registry.auth.pushRoleRef.kind must be Role or ClusterRole",
		},
		{
			name: "endpoint contains path",
			config: KernelCacheRegistryConfig{
				Endpoint: "registry.example:5000/lib",
				Auth: KernelCacheRegistryAuth{
					Type:        KernelCacheRegistryAuthTypeServiceAccountToken,
					PushRoleRef: &KernelCacheRegistryRoleRef{Kind: "Role", Name: "pusher"},
					PullRoleRef: &KernelCacheRegistryRoleRef{Kind: "Role", Name: "puller"},
				},
			},
			expectedErr: "registry.endpoint must be a registry host with optional port",
		},
		{
			name: "invalid token ttl",
			config: KernelCacheRegistryConfig{
				Endpoint: "registry.example:5000",
				Auth: KernelCacheRegistryAuth{
					Type:            KernelCacheRegistryAuthTypeServiceAccountToken,
					TokenTTLSeconds: 300,
					PushRoleRef:     &KernelCacheRegistryRoleRef{Kind: "Role", Name: "pusher"},
					PullRoleRef:     &KernelCacheRegistryRoleRef{Kind: "Role", Name: "puller"},
				},
			},
			expectedErr: "registry.auth.tokenTTLSeconds must be between 600 and 3600",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			g.Expect(test.config.Validate()).To(gomega.MatchError(test.expectedErr))
		})
	}
}

func TestKernelCacheRegistryConfigAcceptsTokenTTLBoundaries(t *testing.T) {
	for _, ttl := range []int64{DefaultKernelCacheRegistryTokenTTLSeconds, 3600} {
		t.Run(fmt.Sprintf("ttl_%d", ttl), func(t *testing.T) {
			g := gomega.NewWithT(t)
			config := KernelCacheRegistryConfig{
				Endpoint: "registry.example:5000",
				Auth: KernelCacheRegistryAuth{
					Type:            KernelCacheRegistryAuthTypeServiceAccountToken,
					TokenTTLSeconds: ttl,
					PushRoleRef:     &KernelCacheRegistryRoleRef{Kind: "Role", Name: "pusher"},
					PullRoleRef:     &KernelCacheRegistryRoleRef{Kind: "Role", Name: "puller"},
				},
			}
			g.Expect(config.Validate()).To(gomega.Succeed())
		})
	}
}

func TestNewKernelCacheConfigRejectsInvalidJSON(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{
		Data: map[string]string{KernelCacheConfigKeyName: `not-json`},
	}

	config, err := NewKernelCacheConfig(configMap)
	g.Expect(config).To(gomega.BeNil())
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(err.Error()).To(gomega.ContainSubstring("unable to unmarshal kernelcache"))
}

func TestNewKernelCacheConfigRejectsInvalidMCVCaptureReadinessTimeout(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{"mcvCaptureReadinessTimeoutSeconds":0}`,
	}}

	_, err := NewKernelCacheConfig(configMap)
	g.Expect(err).To(gomega.MatchError("kernelcache.mcvCaptureReadinessTimeoutSeconds must be greater than zero"))
}

func TestNewKernelCacheConfigRejectsPVCDefaultMountType(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{"defaultMountType":"pvc"}`,
	}}

	_, err := NewKernelCacheConfig(configMap)
	g.Expect(err).To(gomega.MatchError(`kernelcache.defaultMountType must be oci, got "pvc"`))
}

func TestNewKernelCacheConfigUsesConfiguredMCVCaptureReadinessTimeout(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{"mcvCaptureReadinessTimeoutSeconds":900}`,
	}}

	config, err := NewKernelCacheConfig(configMap)
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
	g.Expect(config.MCVCaptureReadinessTimeoutSeconds).To(gomega.Equal(int64(900)))
}

func TestNewKernelCacheConfigUsesConfiguredValues(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{
			"enabled": true,
			"defaultSidecarInjection": false,
			"defaultMountType": "oci",
			"defaultNodeGroup": "gpu-nodes",
			"jobNamespace": "kernel-cache-jobs",
			"mcvImage": "example/mcv:test",
			"mcvCaptureReadinessTimeoutSeconds": 900,
			"prefetchImage": "example/prefetch:test",
			"runtimeImagePrefetch": {
				"enabled": true,
				"jobTTLSecondsAfterFinished": 7200
			},
			"jobTTLSecondsAfterFinished": 600,
			"reconcileIntervalSeconds": 300,
			"abandonedCapturePolicy": "delete"
		}`,
	}}

	config, err := NewKernelCacheConfig(configMap)
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
	g.Expect(config.Enabled).To(gomega.BeTrue())
	g.Expect(config.DefaultSidecarInjection).To(gomega.BeFalse())
	g.Expect(config.DefaultMountType).To(gomega.Equal("oci"))
	g.Expect(config.DefaultNodeGroup).To(gomega.Equal("gpu-nodes"))
	g.Expect(config.JobNamespace).To(gomega.Equal("kernel-cache-jobs"))
	g.Expect(config.MCVImage).To(gomega.Equal("example/mcv:test"))
	g.Expect(config.MCVCaptureReadinessTimeoutSeconds).To(gomega.Equal(int64(900)))
	g.Expect(config.PrefetchImage).To(gomega.Equal("example/prefetch:test"))
	g.Expect(config.RuntimeImagePrefetch.Enabled).To(gomega.BeTrue())
	g.Expect(config.RuntimeImagePrefetch.JobTTLSecondsAfterFinished).ToNot(gomega.BeNil())
	g.Expect(*config.RuntimeImagePrefetch.JobTTLSecondsAfterFinished).To(gomega.Equal(int32(7200)))
	g.Expect(config.JobTTLSecondsAfterFinished).ToNot(gomega.BeNil())
	g.Expect(*config.JobTTLSecondsAfterFinished).To(gomega.Equal(int32(600)))
	g.Expect(config.ReconcileIntervalSeconds).ToNot(gomega.BeNil())
	g.Expect(*config.ReconcileIntervalSeconds).To(gomega.Equal(int64(300)))
	g.Expect(config.AbandonedCapturePolicy).To(gomega.Equal("delete"))
}

func TestNewKernelCacheConfigRestoresDefaultsForEmptyValues(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	configMap := &corev1.ConfigMap{Data: map[string]string{
		KernelCacheConfigKeyName: `{
			"defaultMountType": "",
			"jobNamespace": "",
			"jobTTLSecondsAfterFinished": null,
			"reconcileIntervalSeconds": null
		}`,
	}}

	config, err := NewKernelCacheConfig(configMap)
	g.Expect(err).ShouldNot(gomega.HaveOccurred())
	g.Expect(config.DefaultMountType).To(gomega.Equal(DefaultKernelCacheMountType))
	g.Expect(config.JobNamespace).To(gomega.Equal(DefaultKernelCacheJobNamespace))
	g.Expect(*config.JobTTLSecondsAfterFinished).To(gomega.Equal(DefaultKernelCacheJobTTLSeconds))
	g.Expect(*config.ReconcileIntervalSeconds).To(gomega.Equal(DefaultKernelCacheReconcileIntervalSeconds))
}

func TestNewKernelCacheConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		expectedErr string
	}{
		{
			name:        "invalid mount type",
			config:      `{"defaultMountType":"pvc"}`,
			expectedErr: `kernelcache.defaultMountType must be oci, got "pvc"`,
		},
		{
			name:        "invalid readiness timeout",
			config:      `{"mcvCaptureReadinessTimeoutSeconds":0}`,
			expectedErr: "kernelcache.mcvCaptureReadinessTimeoutSeconds must be greater than zero",
		},
		{
			name:        "invalid abandoned capture policy",
			config:      `{"abandonedCapturePolicy":"invalid"}`,
			expectedErr: "kernelcache.abandonedCapturePolicy must be retain or delete",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			configMap := &corev1.ConfigMap{Data: map[string]string{
				KernelCacheConfigKeyName: test.config,
			}}

			_, err := NewKernelCacheConfig(configMap)
			g.Expect(err).To(gomega.MatchError(test.expectedErr))
		})
	}
}
