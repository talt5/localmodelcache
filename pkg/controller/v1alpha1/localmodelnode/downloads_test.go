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
	"fmt"
	"reflect"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
)

func TestNextDownload(t *testing.T) {
	const uri = "hf://org/model"
	a := v1alpha1.LocalModelInfo{ModelName: "a", SourceModelUri: uri}
	b := v1alpha1.LocalModelInfo{ModelName: "b", Namespace: "team", SourceModelUri: uri}
	// A namespace cache with the name of a cluster cache.
	teamA := v1alpha1.LocalModelInfo{ModelName: "a", Namespace: "team", SourceModelUri: uri}

	// Jobs are listed oldest first.
	var minute int
	job := func(model v1alpha1.LocalModelInfo, status batchv1.JobStatus) batchv1.Job {
		minute++
		return batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:              fmt.Sprintf("job-%d", minute),
				Labels:            downloadJobLabels(&model, "worker"),
				CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 1, 0, minute, 0, 0, time.UTC)),
			},
			Status: status,
		}
	}
	// Timestamps have a resolution of one second.
	sameSecond := func(jobs ...batchv1.Job) []batchv1.Job {
		for i := range jobs {
			jobs[i].CreationTimestamp = jobs[0].CreationTimestamp
		}
		return jobs
	}
	succeeded := batchv1.JobStatus{Succeeded: 1}
	failed := batchv1.JobStatus{Failed: 7, Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}
	running := batchv1.JobStatus{Active: 1, Ready: ptr.To(int32(1))}
	retrying := batchv1.JobStatus{Active: 1, Failed: 1}
	downloaded := map[string]v1alpha1.ModelStatus{"a": v1alpha1.ModelDownloaded}

	tests := map[string]struct {
		models         []v1alpha1.LocalModelInfo
		folderExists   bool
		previous       map[string]v1alpha1.ModelStatus
		jobs           []batchv1.Job
		wantStatus     v1alpha1.ModelStatus
		wantCandidates []v1alpha1.LocalModelInfo
	}{
		"nothing yet": {
			models:         []v1alpha1.LocalModelInfo{a, b},
			wantCandidates: []v1alpha1.LocalModelInfo{a, b},
		},
		"a folder without a Job or status may be incomplete": {
			models: []v1alpha1.LocalModelInfo{a}, folderExists: true,
			previous:       map[string]v1alpha1.ModelStatus{"a": v1alpha1.ModelDownloading},
			wantCandidates: []v1alpha1.LocalModelInfo{a},
		},
		"the Job of a finished download expired": {
			models: []v1alpha1.LocalModelInfo{a}, folderExists: true, previous: downloaded,
			wantStatus: v1alpha1.ModelDownloaded,
		},
		"a new cache of a downloaded URI": {
			models: []v1alpha1.LocalModelInfo{b, a}, folderExists: true, previous: downloaded,
			wantStatus: v1alpha1.ModelDownloaded,
		},
		"the folder was lost after its Job expired": {
			models: []v1alpha1.LocalModelInfo{a}, previous: downloaded,
			wantCandidates: []v1alpha1.LocalModelInfo{a},
		},
		"running": {
			models: []v1alpha1.LocalModelInfo{a, b}, folderExists: true,
			jobs:       []batchv1.Job{job(a, running)},
			wantStatus: v1alpha1.ModelDownloading,
		},
		"a running Job counts more than an old status": {
			models: []v1alpha1.LocalModelInfo{a, b}, folderExists: true, previous: downloaded,
			jobs:       []batchv1.Job{job(b, running)},
			wantStatus: v1alpha1.ModelDownloading,
		},
		"a Job that still retries is not replaced": {
			models: []v1alpha1.LocalModelInfo{a, b}, folderExists: true,
			jobs:       []batchv1.Job{job(a, retrying)},
			wantStatus: v1alpha1.ModelDownloadError,
		},
		"succeeded": {
			models: []v1alpha1.LocalModelInfo{a, b}, folderExists: true,
			jobs:       []batchv1.Job{job(b, succeeded)},
			wantStatus: v1alpha1.ModelDownloaded,
		},
		"another cache of the URI tries after a failure": {
			models: []v1alpha1.LocalModelInfo{a, b}, folderExists: true,
			jobs:           []batchv1.Job{job(a, failed)},
			wantCandidates: []v1alpha1.LocalModelInfo{b},
		},
		"a namespace cache of the same name tries after a failure": {
			models: []v1alpha1.LocalModelInfo{a, teamA}, folderExists: true,
			jobs:           []batchv1.Job{job(a, failed)},
			wantCandidates: []v1alpha1.LocalModelInfo{teamA},
		},
		"all failed": {
			models: []v1alpha1.LocalModelInfo{a, b}, folderExists: true,
			jobs:       []batchv1.Job{job(a, failed), job(b, failed)},
			wantStatus: v1alpha1.ModelDownloadError,
		},
		"one succeeded after another failed": {
			models: []v1alpha1.LocalModelInfo{a, b}, folderExists: true,
			jobs:       []batchv1.Job{job(a, failed), job(b, succeeded)},
			wantStatus: v1alpha1.ModelDownloaded,
		},
		"the folder was lost": {
			models:         []v1alpha1.LocalModelInfo{a},
			jobs:           []batchv1.Job{job(a, succeeded)},
			previous:       downloaded,
			wantCandidates: []v1alpha1.LocalModelInfo{a},
		},
		"the folder was lost, and only one cache could download it": {
			models:         []v1alpha1.LocalModelInfo{a, b},
			jobs:           []batchv1.Job{job(a, failed), job(b, succeeded)},
			wantCandidates: []v1alpha1.LocalModelInfo{b},
		},
		"the folder was lost again": {
			models:     []v1alpha1.LocalModelInfo{a},
			jobs:       []batchv1.Job{job(a, succeeded), job(a, succeeded)},
			previous:   downloaded,
			wantStatus: v1alpha1.ModelDownloadError,
		},
		"the new download, started in the second the old one finished": {
			models:     []v1alpha1.LocalModelInfo{a},
			jobs:       sameSecond(job(a, succeeded), job(a, running)),
			previous:   downloaded,
			wantStatus: v1alpha1.ModelDownloading,
		},
		"another cache tries in the second the first one failed": {
			models: []v1alpha1.LocalModelInfo{a, b}, folderExists: true,
			jobs:       sameSecond(job(a, failed), job(b, running)),
			wantStatus: v1alpha1.ModelDownloading,
		},
		"the new download after the folder was lost": {
			models:     []v1alpha1.LocalModelInfo{a},
			jobs:       []batchv1.Job{job(a, succeeded), job(a, running)},
			previous:   downloaded,
			wantStatus: v1alpha1.ModelDownloading,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			status, candidates := nextDownload(tc.models, tc.folderExists, tc.previous, tc.jobs)
			if status != tc.wantStatus || !reflect.DeepEqual(candidates, tc.wantCandidates) {
				t.Fatalf("nextDownload() = %q, %+v, want %q, %+v", status, candidates, tc.wantStatus, tc.wantCandidates)
			}
		})
	}
}

func TestIsJobOf(t *testing.T) {
	model := v1alpha1.LocalModelInfo{ModelName: "model", Namespace: "team", SourceModelUri: "hf://org/model"}
	with := func(change func(*v1alpha1.LocalModelInfo)) v1alpha1.LocalModelInfo {
		other := model
		change(&other)
		return other
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Labels: downloadJobLabels(&model, "worker")}}
	for name, tc := range map[string]struct {
		model v1alpha1.LocalModelInfo
		want  bool
	}{
		"its model":                     {model, true},
		"another name":                  {with(func(m *v1alpha1.LocalModelInfo) { m.ModelName = "other" }), false},
		"another namespace":             {with(func(m *v1alpha1.LocalModelInfo) { m.Namespace = "other" }), false},
		"cluster-scoped, same name":     {with(func(m *v1alpha1.LocalModelInfo) { m.Namespace = "" }), false},
		"same name, another source URI": {with(func(m *v1alpha1.LocalModelInfo) { m.SourceModelUri = "hf://org/other" }), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isJobOf(job, &tc.model); got != tc.want {
				t.Fatalf("isJobOf() = %v, want %v", got, tc.want)
			}
		})
	}
}
