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
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kernelcacheconfig "github.com/talt5/localmodelcache/pkg/kernelcache/config"
	"github.com/talt5/localmodelcache/pkg/kernelcache/registry"
)

const (
	defaultRegistryProbeTimeout = 2 * time.Second
	defaultRegistryProbeTTL     = 10 * time.Second
)

// RegistryProbe tells whether the registry of a cache image answers. Before a Pod with a cache
// volume starts, the kubelet pulls the image or checks the Pod's pull credentials with the
// registry, even on a node that has the image. While the registry is down such a Pod waits in
// ImagePullBackOff, so the webhook leaves the cache out instead. Answers, including failures,
// are reused for a few seconds to spare the registry and the admission time.
type RegistryProbe struct {
	// Timeout bounds one check. Zero means two seconds.
	Timeout time.Duration
	// TTL is how long an answer is reused. Zero means ten seconds.
	TTL time.Duration

	mu      sync.Mutex
	answers map[string]registryAnswer
}

type registryAnswer struct {
	err error
	at  time.Time
}

// Check returns an error when the registry of imageReference does not answer. Any HTTP response
// counts as an answer: the registry may ask for credentials that only the kubelet has.
func (p *RegistryProbe) Check(
	ctx context.Context,
	reader client.Reader,
	namespace string,
	cfg kernelcacheconfig.KernelCacheRegistryConfig,
	imageReference string,
) error {
	ref, err := name.ParseReference(imageReference)
	if err != nil {
		return fmt.Errorf("parse cache image %q: %w", imageReference, err)
	}
	host := ref.Context().RegistryStr()
	// The registry CA comes from the workload namespace.
	key := namespace + "/" + host

	p.mu.Lock()
	answer, found := p.answers[key]
	p.mu.Unlock()
	if found && time.Since(answer.at) < p.ttl() {
		return answer.err
	}

	err = p.probe(ctx, reader, namespace, cfg, host)
	p.mu.Lock()
	if p.answers == nil {
		p.answers = map[string]registryAnswer{}
	}
	p.answers[key] = registryAnswer{err: err, at: time.Now()}
	p.mu.Unlock()
	return err
}

func (p *RegistryProbe) probe(
	ctx context.Context,
	reader client.Reader,
	namespace string,
	cfg kernelcacheconfig.KernelCacheRegistryConfig,
	host string,
) error {
	transport, err := registry.NewTransport(ctx, reader, namespace, cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// Like a container runtime, try plain HTTP only for a registry configured as insecure.
	schemes := []string{"https"}
	if cfg.Insecure {
		schemes = append(schemes, "http")
	}
	var failures []error
	for _, scheme := range schemes {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+host+"/v2/", nil)
		if err != nil {
			return err
		}
		response, err := httpClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
			return nil
		}
		failures = append(failures, err)
	}
	return fmt.Errorf("registry %s is unreachable: %w", host, errors.Join(failures...))
}

func (p *RegistryProbe) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return defaultRegistryProbeTimeout
}

func (p *RegistryProbe) ttl() time.Duration {
	if p.TTL > 0 {
		return p.TTL
	}
	return defaultRegistryProbeTTL
}
