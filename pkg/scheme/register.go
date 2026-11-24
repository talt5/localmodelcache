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

package scheme

import (
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

type addToSchemeFunc func(scheme *runtime.Scheme) error

// AddAPIs registers the local.cache.ai/v1alpha1 API group.
func AddAPIs(s *runtime.Scheme) error {
	return addAll(s, v1alpha1.AddToScheme)
}

// AddCoreKubernetesAPIs registers the built-in Kubernetes APIs the controllers use.
func AddCoreKubernetesAPIs(s *runtime.Scheme) error {
	return addAll(s,
		corev1.AddToScheme,
		rbacv1.AddToScheme,
		appsv1.AddToScheme,
		batchv1.AddToScheme,
		apiextv1.AddToScheme,
	)
}

// AddControllerAPIs registers the APIs used by the controllers, the node agents and tests.
func AddControllerAPIs(s *runtime.Scheme) error {
	return addAll(s,
		AddAPIs,
		AddCoreKubernetesAPIs,
	)
}

// AddAll registers every API group the controllers and envtest suites use.
func AddAll(s *runtime.Scheme) error {
	return AddControllerAPIs(s)
}

func addAll(s *runtime.Scheme, fns ...addToSchemeFunc) error {
	for _, fn := range fns {
		if err := fn(s); err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}
