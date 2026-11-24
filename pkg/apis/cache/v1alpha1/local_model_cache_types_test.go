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

package v1alpha1

import (
	"testing"

	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestGetStorageKey(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	cases := []struct {
		name        string
		uri1        string
		uri2        string
		shouldMatch bool
	}{
		{
			name:        "same URI returns same key",
			uri1:        "hf://meta-llama/meta-llama-3-8b-instruct",
			uri2:        "hf://meta-llama/meta-llama-3-8b-instruct",
			shouldMatch: true,
		},
		{
			name:        "different URIs return different keys",
			uri1:        "hf://meta-llama/meta-llama-3-8b-instruct",
			uri2:        "hf://mistral/mistral-7b-instruct",
			shouldMatch: false,
		},
		{
			name:        "gs URIs same bucket same model",
			uri1:        "gs://bucket/model",
			uri2:        "gs://bucket/model",
			shouldMatch: true,
		},
		{
			name:        "gs URIs different paths",
			uri1:        "gs://bucket/model1",
			uri2:        "gs://bucket/model2",
			shouldMatch: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key1 := GetStorageKey(tc.uri1, nil)
			key2 := GetStorageKey(tc.uri2, nil)
			g.Expect(key1).To(gomega.HaveLen(16)) // Should be 16 chars
			g.Expect(key2).To(gomega.HaveLen(16))
			if tc.shouldMatch {
				g.Expect(key1).To(gomega.Equal(key2))
			} else {
				g.Expect(key1).NotTo(gomega.Equal(key2))
			}
		})
	}
}

func TestGetStorageKeyConsistency(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	uri := "hf://meta-llama/meta-llama-3-8b-instruct"
	// Ensure calling GetStorageKey multiple times returns the same result
	key1 := GetStorageKey(uri, nil)
	key2 := GetStorageKey(uri, nil)
	key3 := GetStorageKey(uri, nil)
	g.Expect(key1).To(gomega.Equal(key2))
	g.Expect(key2).To(gomega.Equal(key3))
}

func TestGetStorageKeyDependsOnTheSourceOnly(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	uri := "s3://bucket/model"
	storage := func(key string, parameters map[string]string) *LocalModelStorageSpec {
		spec := &LocalModelStorageSpec{}
		if key != "" {
			spec.StorageKey = &key
		}
		if parameters != nil {
			spec.Parameters = &parameters
		}
		return spec
	}
	plain := GetStorageKey(uri, nil)
	for name, same := range map[string]*LocalModelStorageSpec{
		"empty spec":        {},
		"empty parameters":  storage("", map[string]string{}),
		"other credentials": storage("my-secret", nil),
	} {
		g.Expect(GetStorageKey(uri, same)).To(gomega.Equal(plain), name)
	}

	// The parameters can point the same URI at another server.
	endpoint := GetStorageKey(uri, storage("", map[string]string{"endpoint_url": "https://a", "region": "x"}))
	g.Expect(endpoint).To(gomega.HaveLen(16))
	g.Expect(endpoint).NotTo(gomega.Equal(plain))
	g.Expect(GetStorageKey(uri, storage("my-secret", map[string]string{"region": "x", "endpoint_url": "https://a"}))).To(gomega.Equal(endpoint))
	g.Expect(GetStorageKey(uri, storage("", map[string]string{"endpoint_url": "https://b", "region": "x"}))).NotTo(gomega.Equal(endpoint))
	// A URI cannot imitate them.
	g.Expect(GetStorageKey(uri+`{"endpoint_url":"https://a","region":"x"}`, nil)).NotTo(gomega.Equal(endpoint))
	g.Expect(GetStorageSubPath(uri, nil)).To(gomega.Equal("models/" + plain))
}

func TestLocalModelCache_MatchStorageURI(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	uriSpec := LocalModelCacheSpec{
		SourceModelUri: "gs://bucket/model",
		ModelSize:      resource.MustParse("123Gi"),
		NodeGroups:     []string{"gpu"},
	}
	nonUriSpec := LocalModelCacheSpec{
		SourceModelUri: "foo:bar:baz",
		ModelSize:      resource.MustParse("123Gi"),
		NodeGroups:     []string{"gpu"},
	}
	cases := []struct {
		name       string
		spec       LocalModelCacheSpec
		storageUri string
		isMatch    bool
	}{
		{
			name:       "exact match for uri",
			spec:       uriSpec,
			storageUri: "gs://bucket/model",
			isMatch:    true,
		},
		{
			name:       "uri subpath match",
			spec:       uriSpec,
			storageUri: "gs://bucket/model/modelfiles/",
			isMatch:    true,
		},
		{
			name:       "model file match",
			spec:       uriSpec,
			storageUri: "gs://bucket/model/modelfiles/model.pt",
			isMatch:    true,
		},
		{
			name:       "uriSpec does not match",
			spec:       uriSpec,
			storageUri: "gs://bucket/model2",
			isMatch:    false,
		},
		{
			name:       "uriSpec does not match (2nd)",
			spec:       uriSpec,
			storageUri: "gs://bucket/foo",
			isMatch:    false,
		},
		{
			name:       "nonUriSpec match",
			spec:       nonUriSpec,
			storageUri: "foo:bar:baz",
			isMatch:    true,
		},
		{
			name:       "nonUriSpec does not match",
			spec:       nonUriSpec,
			storageUri: "foo:bar:bazzzz",
			isMatch:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			match := tc.spec.MatchStorageURI(tc.storageUri)
			g.Expect(match).To(gomega.Equal(tc.isMatch))
		})
	}
}
