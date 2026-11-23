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

	"github.com/google/go-cmp/cmp"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/kserve/kserve/pkg/credentials/gcs"
)

func TestContainsUtil(t *testing.T) {
	scenarios := map[string]struct {
		input1   []string
		input2   string
		expected bool
	}{
		"SliceContainsString": {
			input1:   []string{"hey", "hello"},
			input2:   "hey",
			expected: true,
		},
		"SliceDoesNotContainString": {
			input1:   []string{"hey", "hello"},
			input2:   "he",
			expected: false,
		},
		"SliceIsEmpty": {
			input1:   []string{},
			input2:   "hey",
			expected: false,
		},
	}
	for name, scenario := range scenarios {
		result := Includes(scenario.input1, scenario.input2)
		if diff := cmp.Diff(scenario.expected, result); diff != "" {
			t.Errorf("Test %q unexpected result (-want +got): %v", name, diff)
		}
	}
}

func TestAppendVolumeIfNotExists(t *testing.T) {
	scenarios := map[string]struct {
		volumes         []corev1.Volume
		volume          corev1.Volume
		expectedVolumes []corev1.Volume
	}{
		"DuplicateVolume": {
			volumes: []corev1.Volume{
				{
					Name: gcs.GCSCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
				{
					Name: "blue",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
			},
			volume: corev1.Volume{
				Name: gcs.GCSCredentialVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "user-gcp-sa",
					},
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gcs.GCSCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
				{
					Name: "blue",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
			},
		},
		"NotDuplicateVolume": {
			volumes: []corev1.Volume{
				{
					Name: gcs.GCSCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
				{
					Name: "blue",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
			},
			volume: corev1.Volume{
				Name: "green",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "user-gcp-sa",
					},
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gcs.GCSCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
				{
					Name: "blue",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
				{
					Name: "green",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "user-gcp-sa",
						},
					},
				},
			},
		},
	}

	for name, scenario := range scenarios {
		volumes := AppendVolumeIfNotExists(scenario.volumes, scenario.volume)

		if diff := cmp.Diff(scenario.expectedVolumes, volumes); diff != "" {
			t.Errorf("Test %q unexpected volume (-want +got): %v", name, diff)
		}
	}
}

func TestMergeEnvs(t *testing.T) {
	scenarios := map[string]struct {
		baseEnvs     []corev1.EnvVar
		overrideEnvs []corev1.EnvVar
		expectedEnvs []corev1.EnvVar
	}{
		"EmptyOverrides": {
			baseEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value1",
				},
			},
			overrideEnvs: []corev1.EnvVar{},
			expectedEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value1",
				},
			},
		},
		"EmptyBase": {
			baseEnvs: []corev1.EnvVar{},
			overrideEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value1",
				},
			},
			expectedEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value1",
				},
			},
		},
		"NoOverlap": {
			baseEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value1",
				},
			},
			overrideEnvs: []corev1.EnvVar{
				{
					Name:  "name2",
					Value: "value2",
				},
			},
			expectedEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value1",
				},
				{
					Name:  "name2",
					Value: "value2",
				},
			},
		},
		"SingleOverlap": {
			baseEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value1",
				},
			},
			overrideEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value2",
				},
			},
			expectedEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value2",
				},
			},
		},
		"MultiOverlap": {
			baseEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value1",
				},
				{
					Name:  "name2",
					Value: "value2",
				},
				{
					Name:  "name3",
					Value: "value3",
				},
			},
			overrideEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value3",
				},
				{
					Name:  "name3",
					Value: "value1",
				},
				{
					Name:  "name4",
					Value: "value4",
				},
			},
			expectedEnvs: []corev1.EnvVar{
				{
					Name:  "name1",
					Value: "value3",
				},
				{
					Name:  "name2",
					Value: "value2",
				},
				{
					Name:  "name3",
					Value: "value1",
				},
				{
					Name:  "name4",
					Value: "value4",
				},
			},
		},
	}

	for name, scenario := range scenarios {
		envs := MergeEnvs(scenario.baseEnvs, scenario.overrideEnvs)

		if diff := cmp.Diff(scenario.expectedEnvs, envs); diff != "" {
			t.Errorf("Test %q unexpected envs (-want +got): %v", name, diff)
		}
	}
}

func TestRemoveString(t *testing.T) {
	g := gomega.NewGomegaWithT(t)
	testStrings := []string{
		"Model Tensorflow",
		"SKLearn Model",
		"Model",
		"ModelPytorch",
	}
	expected := []string{
		"Model Tensorflow",
		"SKLearn Model",
		"ModelPytorch",
	}
	res := RemoveString(testStrings, "Model")
	g.Expect(res).Should(gomega.Equal(expected))
}
