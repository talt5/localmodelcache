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

package testing

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChartImagesArePublished fails when the chart names an image that no workflow publishes: the
// chart then installs, but its Pods cannot pull the image.
func TestChartImagesArePublished(t *testing.T) {
	read := func(path string) string {
		data, err := os.ReadFile(filepath.Join("..", "..", path)) // #nosec G304 -- fixed paths in the repository
		require.NoError(t, err)
		return string(data)
	}
	names := func(pattern, text string) map[string]bool {
		found := map[string]bool{}
		for _, match := range regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1) {
			found[match[1]] = true
		}
		return found
	}

	workflows, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*docker-publish*.yml"))
	require.NoError(t, err)
	published := ""
	var publishedSb47 strings.Builder
	for _, workflow := range workflows {
		publishedSb47.WriteString(read(filepath.Join(".github", "workflows", filepath.Base(workflow))))
	}
	published += publishedSb47.String()

	charted := names(`ghcr\.io/talt5/([\w-]+)`, read("charts/localmodelcache/values.yaml"))
	require.NotEmpty(t, charted)
	require.Equal(t, charted, names(`(?m)^  IMAGE_NAME: ([\w-]+)$`, published))
	require.Equal(t, charted, names(`(?m)_IMG \?= ([\w-]+)$`, read("images.env")))
}
