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

package localmodelnode

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/controller/v1alpha1/localmodel/jobs"
)

// Labels of download Jobs.
const (
	modelLabel          = "model"
	modelNamespaceLabel = "modelNamespace"
	nodeLabel           = "node"
	storageKeyLabel     = "storageKey"
)

// maxCompletedJobs limits how often a finished download is repeated while its Jobs exist, which
// jobTTLSecondsAfterFinished bounds. It stops an endless loop when the Job writes to a directory
// that is not the one the agent sees.
const maxCompletedJobs = 2

// downloadJobLabels identify the download Job of a model on a node. A label value cannot hold
// the status key (namespace/name), so the namespace has its own label.
func downloadJobLabels(model *v1alpha1.LocalModelInfo, node string) map[string]string {
	labels := map[string]string{
		modelLabel:      jobs.LabelValue(model.ModelName),
		nodeLabel:       jobs.LabelValue(node),
		storageKeyLabel: model.StorageKey(),
	}
	if model.Namespace != "" {
		labels[modelNamespaceLabel] = model.Namespace
	}
	return labels
}

// isJobOf reports whether the Job downloads the model. Caches of the same name differ in their
// namespace or their source URI.
func isJobOf(job *batchv1.Job, model *v1alpha1.LocalModelInfo) bool {
	for _, key := range []string{modelLabel, modelNamespaceLabel, storageKeyLabel} {
		if job.Labels[key] != downloadJobLabels(model, "")[key] {
			return false
		}
	}
	return true
}

func listDownloadJobs(ctx context.Context, reader client.Reader, labels client.MatchingLabels) ([]batchv1.Job, error) {
	list := &batchv1.JobList{}
	if err := reader.List(ctx, list, client.InNamespace(jobNamespace), labels); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// groupByFolder groups the models that share a folder, which are those with the same source.
func groupByFolder(models []v1alpha1.LocalModelInfo) [][]v1alpha1.LocalModelInfo {
	var groups [][]v1alpha1.LocalModelInfo
	index := map[string]int{}
	for _, model := range models {
		storageKey := model.StorageKey()
		i, ok := index[storageKey]
		if !ok {
			i = len(groups)
			index[storageKey] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], model)
	}
	return groups
}

func sameStatus(models []v1alpha1.LocalModelInfo, status v1alpha1.ModelStatus) map[string]v1alpha1.ModelStatus {
	statuses := make(map[string]v1alpha1.ModelStatus, len(models))
	for i := range models {
		statuses[models[i].GetStatusKey()] = status
	}
	return statuses
}

// nextDownload decides what the folder that models share needs next. It returns either the
// status of all of them, or the models to try to start a download for, in order.
//
// folderJobs are the Jobs of the folder. One that has not finished decides, or else the newest.
// Each model gets a Job of its own after the Jobs of others failed, since its credentials may work
// where theirs did not. A failed Job is not retried until it is deleted: the Job has retried
// already, and it expires after jobTTLSecondsAfterFinished.
func nextDownload(models []v1alpha1.LocalModelInfo, folderExists bool, previous map[string]v1alpha1.ModelStatus, folderJobs []batchv1.Job) (v1alpha1.ModelStatus, []v1alpha1.LocalModelInfo) {
	if len(folderJobs) == 0 {
		// The Jobs of a finished download expire. Without one, the folder may be incomplete.
		downloaded := slices.ContainsFunc(models, func(model v1alpha1.LocalModelInfo) bool {
			return previous[model.GetStatusKey()] == v1alpha1.ModelDownloaded
		})
		if folderExists && downloaded {
			return v1alpha1.ModelDownloaded, nil
		}
		return "", models
	}

	completed := 0
	for i := range folderJobs {
		switch {
		case folderJobs[i].Status.Succeeded > 0:
			completed++
		case !jobFailed(&folderJobs[i]):
			return getModelStatusFromJobStatus(folderJobs[i].Status), nil
		}
	}
	latest := slices.MaxFunc(folderJobs, func(a, b batchv1.Job) int {
		return cmp.Or(a.CreationTimestamp.Compare(b.CreationTimestamp.Time), cmp.Compare(a.Status.Succeeded, b.Status.Succeeded))
	})
	if latest.Status.Succeeded > 0 {
		if folderExists {
			return v1alpha1.ModelDownloaded, nil
		}
		// The folder was removed after the download.
		if completed >= maxCompletedJobs {
			return v1alpha1.ModelDownloadError, nil
		}
		// Download it again with the credentials that worked.
		for i := range models {
			if isJobOf(&latest, &models[i]) {
				return "", models[i : i+1]
			}
		}
		return "", models
	}

	untried := slices.DeleteFunc(slices.Clone(models), func(model v1alpha1.LocalModelInfo) bool {
		return slices.ContainsFunc(folderJobs, func(job batchv1.Job) bool { return isJobOf(&job, &model) })
	})
	if len(untried) == 0 {
		return v1alpha1.ModelDownloadError, nil
	}
	return "", untried
}

func jobFailed(job *batchv1.Job) bool {
	return slices.ContainsFunc(job.Status.Conditions, func(condition batchv1.JobCondition) bool {
		return condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue
	})
}

// launchErrorStatus tells an error that someone has to fix from one that may pass.
func launchErrorStatus(err error) v1alpha1.ModelStatus {
	if errors.Is(err, errInvalidImagePullSecret) || apierrors.IsInvalid(err) || apierrors.IsForbidden(err) ||
		apierrors.IsBadRequest(err) || apierrors.IsNotFound(err) {
		return v1alpha1.ModelDownloadError
	}
	return v1alpha1.ModelDownloadPending
}

// downloadModels starts the Jobs that download missing models and records the state of every
// model in the status of the LocalModelNode. A model that cannot be downloaded does not hold up
// the others.
func (c *LocalModelNodeReconciler) downloadModels(ctx context.Context, localModelNode *v1alpha1.LocalModelNode) error {
	nodeJobs, err := listDownloadJobs(ctx, c.Client, client.MatchingLabels{nodeLabel: jobs.LabelValue(localModelNode.Name)})
	if err != nil {
		return err
	}

	newStatus := map[string]v1alpha1.ModelStatus{}
	var errs []error
	for _, models := range groupByFolder(localModelNode.Spec.LocalModels) {
		statuses, err := c.downloadFolder(ctx, localModelNode, models, nodeJobs)
		maps.Copy(newStatus, statuses)
		errs = append(errs, err)
	}

	if !maps.Equal(localModelNode.Status.ModelStatus, newStatus) {
		localModelNode.Status.ModelStatus = newStatus
		if err := c.Status().Update(ctx, localModelNode); err != nil {
			errs = append(errs, err)
		} else {
			c.Log.Info("status updated", "name", localModelNode.Name, "modelStatus", newStatus)
		}
	}
	return errors.Join(errs...)
}

// downloadFolder starts the download of the folder that models share if it needs one, and
// returns the status of each model.
func (c *LocalModelNodeReconciler) downloadFolder(ctx context.Context, localModelNode *v1alpha1.LocalModelNode, models []v1alpha1.LocalModelInfo, nodeJobs []batchv1.Job) (map[string]v1alpha1.ModelStatus, error) {
	storageKey := models[0].StorageKey()
	previous := localModelNode.Status.ModelStatus
	unchanged := sameStatus(models, v1alpha1.ModelDownloadPending)
	for key := range unchanged {
		if status, ok := previous[key]; ok {
			unchanged[key] = status
		}
	}

	folderExists, err := fsHelper.hasModelFolder(storageKey)
	if err != nil {
		return unchanged, fmt.Errorf("check model folder %s: %w", storageKey, err)
	}
	folderJobs := slices.DeleteFunc(slices.Clone(nodeJobs), func(job batchv1.Job) bool { return job.Labels[storageKeyLabel] != storageKey })
	status, candidates := nextDownload(models, folderExists, previous, folderJobs)
	if len(candidates) > 0 {
		// The informer may not know a Job that was created a moment ago.
		folderJobs, err = listDownloadJobs(ctx, c.APIReader, client.MatchingLabels{
			nodeLabel: jobs.LabelValue(localModelNode.Name), storageKeyLabel: storageKey,
		})
		if err != nil {
			return unchanged, fmt.Errorf("list download Jobs of model folder %s: %w", storageKey, err)
		}
		status, candidates = nextDownload(models, folderExists, previous, folderJobs)
	}
	if len(candidates) == 0 {
		return sameStatus(models, status), nil
	}

	// Models whose Job failed before are not among the candidates.
	statuses := sameStatus(models, v1alpha1.ModelDownloadError)
	var errs []error
	for _, model := range candidates {
		job, err := c.launchJob(ctx, *localModelNode, model)
		if err == nil {
			return sameStatus(models, getModelStatusFromJobStatus(job.Status)), nil
		}
		c.Log.Error(err, "Failed to launch download Job", "model", model.GetStatusKey(), "storageKey", storageKey)
		statuses[model.GetStatusKey()] = launchErrorStatus(err)
		if !errors.Is(err, errInvalidImagePullSecret) {
			errs = append(errs, fmt.Errorf("launch download Job of %s: %w", model.GetStatusKey(), err))
		}
	}
	return statuses, errors.Join(errs...)
}
