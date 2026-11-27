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

package kernelcache

import (
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kernelcacheconfig "github.com/talt5/localmodelcache/pkg/kernelcache/config"
)

const testDigest = "@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func registryHost(t *testing.T, server *httptest.Server) string {
	t.Helper()
	return strings.TrimPrefix(strings.TrimPrefix(server.URL, "https://"), "http://")
}

// A registry that answers at all, even with 401, can serve the image.
func TestRegistryProbeAcceptsAnyAnswer(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, "/v2/", r.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	probe := &RegistryProbe{}
	insecure := kernelcacheconfig.KernelCacheRegistryConfig{Insecure: true}
	image := registryHost(t, server) + "/team/cache" + testDigest
	require.NoError(t, probe.Check(t.Context(), nil, "team", insecure, image))
	require.NoError(t, probe.Check(t.Context(), nil, "team", insecure, image))
	require.Equal(t, int32(1), requests.Load(), "the answer is reused")
}

func TestRegistryProbeUsesTheRegistryCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	fixture := newPodMutatorFixture()
	reader := fixture.mutator(t, enabledKernelCache).Client
	require.NoError(t, reader.Create(t.Context(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "registry-ca", Namespace: "team"},
		Data:       map[string]string{"ca.crt": string(caPEM)},
	}))
	withCA := kernelcacheconfig.KernelCacheRegistryConfig{
		CAConfigMapRef: &kernelcacheconfig.KernelCacheConfigMapKeyRef{Name: "registry-ca", Key: "ca.crt"},
	}
	image := registryHost(t, server) + "/team/cache" + testDigest

	require.NoError(t, (&RegistryProbe{}).Check(t.Context(), reader, "team", withCA, image))
	require.Error(t, (&RegistryProbe{}).Check(t.Context(), reader, "team", kernelcacheconfig.KernelCacheRegistryConfig{}, image),
		"without the CA the registry's certificate is not trusted")
}

func TestRegistryProbeRejectsAnUnreachableRegistry(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	probe := &RegistryProbe{Timeout: time.Second}
	err = probe.Check(t.Context(), nil, "team", kernelcacheconfig.KernelCacheRegistryConfig{Insecure: true}, address+"/team/cache"+testDigest)
	require.ErrorContains(t, err, address)
}

// A Pod given a cache whose registry is down waits in ImagePullBackOff until the registry is
// back, even on a node that has the image, so it starts without the cache instead.
func TestPodMutatorSkipsACacheWhoseRegistryIsUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	fixture, mutator := mountableFixtureWithImage(t, address+"/team/cache"+testDigest)
	mutator.RegistryProbe = &RegistryProbe{Timeout: time.Second}

	response := mutator.Handle(t.Context(), podCreateRequest(t, fixture.pod))
	require.True(t, response.Allowed, response.Result)
	require.False(t, patchesSpec(response), "the Pod spec must not be changed")
	require.Len(t, response.Warnings, 1)
	require.Contains(t, response.Warnings[0], "unreachable")
}
