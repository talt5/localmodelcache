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

package workload

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// WorkloadRevision identifies the Deployment, ReplicaSet, and revision for a capture Pod.
type WorkloadRevision struct {
	// Source is the owning Deployment.
	Source *appsv1.Deployment
	// ReplicaSet is the owning ReplicaSet.
	ReplicaSet *appsv1.ReplicaSet
	// RevisionID is the Deployment revision identifier shared by the Pod and ReplicaSet.
	RevisionID string
}

// ResolveDeploymentRevision resolves the owning Deployment, ReplicaSet, and revision for
// a Deployment-backed Pod. It requires a revision and rejects mismatched Pod and ReplicaSet
// revisions.
func ResolveDeploymentRevision(ctx context.Context, reader client.Reader, pod *corev1.Pod) (*WorkloadRevision, error) {
	resolved, err := resolveDeploymentBackedWorkload(ctx, reader, pod)
	if err != nil || resolved == nil || resolved.RevisionID == "" {
		return nil, err
	}

	podRevisionID := pod.Labels[appsv1.DefaultDeploymentUniqueLabelKey]
	replicaSetRevisionID := resolved.ReplicaSet.Labels[appsv1.DefaultDeploymentUniqueLabelKey]
	if podRevisionID != "" && replicaSetRevisionID != "" && podRevisionID != replicaSetRevisionID {
		return nil, fmt.Errorf("pod revision %q does not match ReplicaSet revision %q", podRevisionID, replicaSetRevisionID)
	}
	return resolved, nil
}
