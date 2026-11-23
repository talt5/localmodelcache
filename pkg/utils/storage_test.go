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

package utils

import (
	"testing"

	"github.com/onsi/gomega"
)

func TestMaxOCISourcesPerPod(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	// Ensure the constant is reasonable and matches documented limit
	g.Expect(MaxOCISourcesPerPod).To(gomega.Equal(10),
		"MaxOCISourcesPerPod should be 10 — each URI adds 2 containers + 1 volume")
}

func TestParseOciScheme(t *testing.T) {
	cases := []struct {
		uri       string
		wantMode  string
		wantNorm  string
		wantIsOci bool
	}{
		{
			uri:       "oci+native://registry.io/image:tag",
			wantMode:  "native",
			wantNorm:  "oci://registry.io/image:tag",
			wantIsOci: true,
		},
		{
			uri:       "oci+modelcar://registry.io/image:tag",
			wantMode:  "modelcar",
			wantNorm:  "oci://registry.io/image:tag",
			wantIsOci: true,
		},
		{
			uri:       "oci+fetch://registry.io/image:tag",
			wantMode:  "fetch",
			wantNorm:  "oci://registry.io/image:tag",
			wantIsOci: true,
		},
		{
			uri:       "oci://registry.io/image:tag",
			wantMode:  "",
			wantNorm:  "oci://registry.io/image:tag",
			wantIsOci: true,
		},
		{
			uri:       "s3://bucket/path/to/model",
			wantMode:  "",
			wantNorm:  "s3://bucket/path/to/model",
			wantIsOci: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.uri, func(t *testing.T) {
			g := gomega.NewGomegaWithT(t)
			gotMode, gotNorm, gotIsOci := ParseOciScheme(tc.uri)
			g.Expect(gotMode).To(gomega.Equal(tc.wantMode), "mode")
			g.Expect(gotNorm).To(gomega.Equal(tc.wantNorm), "normalizedURI")
			g.Expect(gotIsOci).To(gomega.Equal(tc.wantIsOci), "isOci")
		})
	}
}
