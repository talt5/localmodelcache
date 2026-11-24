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
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoUpstreamNames fails when a tracked file uses a name of KServe, the project this one was
// forked from, or of its InferenceService. Names that live outside the Go build, such as API paths in scripts, would
// otherwise only fail at runtime.
func TestNoUpstreamNames(t *testing.T) {
	root, err := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	dir := strings.TrimSpace(string(root))
	listed, err := exec.CommandContext(t.Context(), "git", "-C", dir, "ls-files", "-z").Output() // #nosec G204 -- dir is the checkout root
	require.NoError(t, err)

	// docs/upgrade.md names what v0.1 called things.
	skipped := map[string]bool{"LICENSE": true, "docs/upgrade.md": true, "NOTICE": true, "pkg/testing/branding_test.go": true}
	// Copyright notices and attribution must stay, and upstream issues are references.
	allowed := regexp.MustCompile(`The KServe Authors|fork of \[?KServe|\(https://github\.com/kserve/kserve\)|kserve#\d+|upstream KServe|[Ww]ebhook[Ss]erver` +
		`|no InferenceService or LLMInferenceService|InferenceService and LLMInferenceService are gone`)
	name := regexp.MustCompile(`(?i)kserve|inference-?service|llmisvc|\bisvc`)

	for path := range strings.SplitSeq(strings.TrimRight(string(listed), "\x00"), "\x00") {
		if skipped[path] {
			continue
		}
		require.NotRegexp(t, name, path)
		data, err := os.ReadFile(filepath.Join(dir, path)) // #nosec G304 -- the path comes from git ls-files
		if os.IsNotExist(err) || bytes.IndexByte(data, 0) >= 0 {
			continue // Deleted in the working tree, or binary.
		}
		require.NoError(t, err)
		for i, line := range bytes.Split(data, []byte("\n")) {
			if name.Match(allowed.ReplaceAll(line, nil)) {
				t.Errorf("%s:%d: %s", path, i+1, line)
			}
		}
	}
}
