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

package localmodelnode

import (
	"errors"
	"fmt"
	"testing"

	"github.com/talt5/localmodelcache/pkg/credentials"
)

func TestErrStorageKeyNotFound_IsTyped(t *testing.T) {
	err := fmt.Errorf("specified storage key foo not found: %w", credentials.ErrStorageKeyNotFound)
	if !errors.Is(err, credentials.ErrStorageKeyNotFound) {
		t.Error("expected errors.Is to match ErrStorageKeyNotFound")
	}
}

func TestErrStorageConfigSecretNotFound_IsTyped(t *testing.T) {
	err := fmt.Errorf("can't read storage secret bar: %w", credentials.ErrStorageConfigSecretNotFound)
	if !errors.Is(err, credentials.ErrStorageConfigSecretNotFound) {
		t.Error("expected errors.Is to match ErrStorageConfigSecretNotFound")
	}
}
