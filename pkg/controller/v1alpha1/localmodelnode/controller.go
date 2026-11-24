/*
Copyright 2024 The KServe Authors.

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

// +kubebuilder:rbac:groups=local.cache.ai,resources=localmodelnodegroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=local.cache.ai,resources=clusterstoragecontainers,verbs=get;list;watch
// +kubebuilder:rbac:groups=local.cache.ai,resources=localmodelnodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=local.cache.ai,resources=localmodelnodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=local.cache.ai,resources=localmodelnodes/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=configmaps,resourceNames=localmodelcache-config,verbs=get,namespace=localmodelcache
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create
//
// The permissions in the job namespace, which is a setting, are in config/rbac/localmodelnode-jobs
// and in the chart's files/node-agent-jobs-patch.yaml.
package localmodelnode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/talt5/localmodelcache/pkg/apis/cache/v1alpha1"
	"github.com/talt5/localmodelcache/pkg/constants"
	"github.com/talt5/localmodelcache/pkg/controller/v1alpha1/localmodel/jobs"
	"github.com/talt5/localmodelcache/pkg/controller/v1alpha1/utils"
	"github.com/talt5/localmodelcache/pkg/credentials"
	"github.com/talt5/localmodelcache/pkg/localmodelcache"
	pkgtypes "github.com/talt5/localmodelcache/pkg/types"
	lmcutils "github.com/talt5/localmodelcache/pkg/utils"
)

// errInvalidImagePullSecret is returned by launchJob when the named
// imagePullSecret is missing or not a usable dockerconfigjson. downloadModels
// maps it to ModelDownloadError and continues so Status().Update still runs
// for other models on the node.
var errInvalidImagePullSecret = errors.New("invalid image pull secret")

type ensureModelRootFolderResult struct {
	Result   ctrl.Result
	Continue bool
}

type LocalModelNodeReconciler struct {
	client.Client
	APIReader client.Reader
	// JobNamespace is the only namespace whose Jobs the Client can read. Empty means any.
	JobNamespace      string
	Clientset         *kubernetes.Clientset
	Log               logr.Logger
	Scheme            *runtime.Scheme
	CredentialBuilder *credentials.CredentialBuilder
	ConfigMap         *corev1.ConfigMap
}

var (
	FSGroup                    *int64
	jobNamespace               string
	jobTTLSecondsAfterFinished int32         = 3600                   // One hour. Can be overwritten by the value in the configmap
	reconcilationFreqency      time.Duration = time.Minute            // Reconcile every one minute to check if model folders exist. Can be overwritten by the value in configmap
	nodeName                                 = os.Getenv("NODE_NAME") // Name of current node, passed as an env variable via downward API
	modelsRootFolder                         = filepath.Join(MountPath, "models")
	fsHelper                   FileSystemInterface
	storageInitializerConfig   *pkgtypes.StorageInitializerConfig
	jobTolerations             []corev1.Toleration
)

// Returns the first matching nodegroup for a node.
// NOTE: A node may match multiple nodegroups with overlapping affinity.
// This is only used as a fallback when modelInfo.NodeGroup is not set.
func (c *LocalModelNodeReconciler) getNodeGroupFromNode(ctx context.Context, nodeName string) (*v1alpha1.LocalModelNodeGroup, error) {
	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		return nil, err
	}
	nodeGroups := &v1alpha1.LocalModelNodeGroupList{}
	if err := c.List(ctx, nodeGroups); err != nil {
		return nil, err
	}
	for _, nodeGroup := range nodeGroups.Items {
		matches, err := utils.CheckNodeAffinity(&nodeGroup.Spec.PersistentVolumeSpec, *node)
		if err != nil {
			return nil, err
		}
		if matches {
			return &nodeGroup, nil
		}
	}

	return nil, fmt.Errorf("did not find matching nodegroup for node: %s", nodeName)
}

func (c *LocalModelNodeReconciler) launchJob(ctx context.Context, localModelNode v1alpha1.LocalModelNode, modelInfo v1alpha1.LocalModelInfo) (*batchv1.Job, error) {
	jobName := modelInfo.ModelName + "-" + localModelNode.Name

	// Use NodeGroup from modelInfo if set, otherwise fall back to getNodeGroupFromNode
	nodeGroupName := modelInfo.NodeGroup
	if nodeGroupName == "" {
		nodeGroup, err := c.getNodeGroupFromNode(ctx, nodeName)
		if nodeGroup == nil {
			c.Log.Error(err, "Failed to get node group for current node", "node name", nodeName)
			return nil, err
		}
		nodeGroupName = nodeGroup.Name
	}

	pvcName := modelInfo.ModelName + "-" + nodeGroupName
	if modelInfo.Namespace != "" {
		pvcName += "-" + modelInfo.Namespace + "-download"
	}
	c.Log.Info("Using PVC name to create download job", "current node", nodeName, "node group", nodeGroupName, "PVC name", pvcName)

	// Resolve the download container from ClusterStorageContainer (backward compatibility) or
	// the StorageInitializerConfig fallback.
	container, err := jobs.ResolveDownloadContainer(ctx, c.Client, storageInitializerConfig, modelInfo.SourceModelUri)
	if err != nil {
		return nil, err
	}

	// Use hash-based folder path for storage deduplication
	storageKey := modelInfo.StorageKey()
	storageUri := modelInfo.SourceModelUri
	if _, normalized, isOci := lmcutils.ParseOciScheme(storageUri); isOci {
		storageUri = normalized
	}
	container.Args = []string{storageUri, MountPath}
	container.VolumeMounts = []corev1.VolumeMount{
		{
			MountPath: MountPath,
			Name:      constants.PvcSourceMountName,
			ReadOnly:  false,
			SubPath:   filepath.Join("models", storageKey),
		},
	}

	volumes := []corev1.Volume{
		{
			Name: constants.PvcSourceMountName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		},
	}

	jobNs := jobNamespace

	if secretName, ok := credentials.FirstNamedImagePullSecret(modelInfo.ImagePullSecrets); ok {
		reader := c.APIReader
		if reader == nil {
			reader = c.Client
		}
		if err := credentials.FetchAndValidateDockerConfigJSONSecret(ctx, reader, jobNs, secretName); err != nil {
			return nil, fmt.Errorf("%w: %w", errInvalidImagePullSecret, err)
		}
		credentials.MountImagePullSecretsAsDockerConfig(modelInfo.ImagePullSecrets, container, &volumes)
	}

	// Only inject if credentials are explicitly configured in LocalModelCache
	if modelInfo.ServiceAccountName != "" || modelInfo.Storage != nil {
		if err := jobs.InjectCredentials(ctx, c.CredentialBuilder, c.Log, container, &volumes, modelInfo.ServiceAccountName, modelInfo.Storage, jobNs); err != nil {
			c.Log.Error(err, "Failed to inject credentials", "model", modelInfo.ModelName)
			// Don't fail the job creation, continue with whatever credentials were injected
		}
	}

	if _, _, isOci := lmcutils.ParseOciScheme(modelInfo.SourceModelUri); isOci {
		if storageInitializerConfig != nil && storageInitializerConfig.OciInsecureRegistry {
			credentials.SetOciInsecureRegistryEnv(container)
		}
	}

	credentials.MountCaBundle(storageInitializerConfig, container, &volumes)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: jobName,
			Namespace:    jobNs,
			Labels:       downloadJobLabels(&modelInfo, localModelNode.Name),
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &jobTTLSecondsAfterFinished,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					// By name, since the hostname label can differ from it. The scheduler places the
					// Pod, rather than spec.nodeName, so that it binds the claim.
					Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
							MatchFields: []corev1.NodeSelectorRequirement{{
								Key: metav1.ObjectNameField, Operator: corev1.NodeSelectorOpIn, Values: []string{nodeName},
							}},
						}}},
					}},
					Tolerations:   jobTolerations,
					Containers:    []corev1.Container{*container},
					RestartPolicy: corev1.RestartPolicyNever,
					Volumes:       volumes,
					SecurityContext: &corev1.PodSecurityContext{
						FSGroup: FSGroup,
					},
				},
			},
		},
	}
	if err := c.enhanceDownloadJob(ctx, job, storageKey); err != nil {
		c.Log.Error(err, "Failed to enhance download job", "name", modelInfo.ModelName)
		return nil, err
	}
	if err := controllerutil.SetControllerReference(&localModelNode, job, c.Scheme); err != nil {
		c.Log.Error(err, "Failed to set controller reference", "name", modelInfo.ModelName)
		return nil, err
	}
	jobs := c.Clientset.BatchV1().Jobs(jobNs)
	createdJob, err := jobs.Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		c.Log.Error(err, "Failed to create job.", "name", job.Name)
		return nil, err
	}
	c.Log.Info("Created job", "name", createdJob.Name, "namespace", createdJob.Namespace,
		"model", modelInfo.ModelName, "storageKey", storageKey)
	return createdJob, err
}

func getModelStatusFromJobStatus(jobStatus batchv1.JobStatus) v1alpha1.ModelStatus {
	switch {
	case jobStatus.Succeeded > 0:
		return v1alpha1.ModelDownloaded
	case jobStatus.Failed > 0:
		return v1alpha1.ModelDownloadError
	case jobStatus.Ready != nil && *jobStatus.Ready > 0:
		return v1alpha1.ModelDownloading
	default:
		return v1alpha1.ModelDownloadPending
	}
}

// Delete models that are not in the spec
// Uses hash-based folder names (storageKey) for storage deduplication
func (c *LocalModelNodeReconciler) deleteModels(localModelNode v1alpha1.LocalModelNode) error {
	// 1. Scan model dir and get a list of existing folders representing downloaded models
	foldersToRemove := map[string]struct{}{}
	entries, err := fsHelper.getModelFolders()
	if err != nil {
		c.Log.Error(err, "Failed to list model folder")
		return err
	}
	for _, entry := range entries {
		// Models could only exist in sub dir
		if entry.IsDir() {
			foldersToRemove[entry.Name()] = struct{}{}
		}
	}

	// 2. Compare with list of models from LocalModelNode CR using storage keys (URI hashes)
	for _, localModelInfo := range localModelNode.Spec.LocalModels {
		// Remove expected models from local model set using storage key
		storageKey := localModelInfo.StorageKey()
		delete(foldersToRemove, storageKey)
	}
	// 3. Models not in LocalModelNode CR spec should be deleted
	var errs []error
	if len(foldersToRemove) != 0 {
		c.Log.Info("Found model(s) to remove", "num of models", len(foldersToRemove))
		for storageKey := range foldersToRemove {
			c.Log.Info("Removing model folder", "storageKey", storageKey)
			if err := fsHelper.removeModel(storageKey); err != nil {
				c.Log.Error(err, "Failed to remove model directory", "storageKey", storageKey)
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// cleanupJobs deletes the download Jobs of models that are not in the spec.
func (c *LocalModelNodeReconciler) cleanupJobs(ctx context.Context, localModelNode v1alpha1.LocalModelNode) error {
	nodeJobs, err := listDownloadJobs(ctx, c.Client, client.MatchingLabels{nodeLabel: jobs.LabelValue(localModelNode.Name)})
	if err != nil {
		c.Log.Error(err, "Failed to list jobs", "node", localModelNode.Name, "namespace", jobNamespace)
		return err
	}
	for i := range nodeJobs {
		job := &nodeJobs[i]
		inSpec := slices.ContainsFunc(localModelNode.Spec.LocalModels, func(model v1alpha1.LocalModelInfo) bool { return isJobOf(job, &model) })
		if inSpec || job.Labels[modelLabel] == "" {
			continue
		}
		c.Log.Info("Deleting job", "job", job.Name, "model", job.Labels[modelLabel], "namespace", job.Namespace)
		if err := c.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
			c.Log.Error(err, "Failed to delete job", "job", job.Name)
			return err
		}
	}
	return nil
}

func (c *LocalModelNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Name != nodeName {
		c.Log.Info("Skipping LocalModelNode because it is not for current node", "name", req.Name, "current node", nodeName)
		return reconcile.Result{}, nil
	}

	c.Log.Info("Agent reconciling LocalModelNode", "name", req.Name, "node", nodeName)

	// 1. Load config
	configMap, err := localmodelcache.GetConfigMap(ctx, c.Clientset)
	if err != nil {
		c.Log.Error(err, "unable to get configmap", "name", constants.LocalModelConfigMapName, "namespace", constants.SystemNamespace)
		return reconcile.Result{}, err
	}
	c.ConfigMap = configMap
	localModelConfig, err := localmodelcache.NewLocalModelConfig(configMap)
	if err != nil {
		c.Log.Error(err, "Failed to get local model config")
		return reconcile.Result{}, err
	}
	if c.JobNamespace != "" && c.JobNamespace != localModelConfig.JobNamespace {
		return reconcile.Result{}, fmt.Errorf("localModel.jobNamespace changed from %q to %q: restart the node agent",
			c.JobNamespace, localModelConfig.JobNamespace)
	}
	jobNamespace = localModelConfig.JobNamespace
	FSGroup = localModelConfig.FSGroup
	if localModelConfig.ReconcilationFrequencyInSecs != nil {
		reconcilationFreqency = time.Duration(*localModelConfig.ReconcilationFrequencyInSecs) * time.Second
	}
	if localModelConfig.JobTTLSecondsAfterFinished != nil {
		jobTTLSecondsAfterFinished = *localModelConfig.JobTTLSecondsAfterFinished
	}

	jobTolerations = localModelConfig.Tolerations

	storageInitializerConfig, err = localmodelcache.GetStorageInitializerConfigs(configMap)
	if err != nil {
		c.Log.Error(err, "Failed to get storage initializer config, using defaults")
		// Don't fail, just use defaults
	}

	// Built every time, so that changes to the settings apply without a restart.
	credentialConfig, err := credentials.GetCredentialConfig(configMap)
	if err != nil {
		c.Log.Error(err, "Failed to get credentials config")
		return reconcile.Result{}, err
	}
	c.CredentialBuilder = credentials.NewCredentialBuilderFromConfig(c.Client, c.Clientset, credentialConfig)

	// 2. Init fsHelper and ensure model root folder exists and is writable
	if fsHelper == nil {
		fsHelper = NewFileSystemHelper(modelsRootFolder)
	}

	folderResult, err := c.ensureModelRootFolderExistsAndIsWritable(ctx, localModelConfig)
	if err != nil || !folderResult.Continue {
		if folderResult != nil {
			return folderResult.Result, err
		}
		return ctrl.Result{}, err
	}

	// 3. Get LocalModelNode CR
	localModelNode := v1alpha1.LocalModelNode{}
	if err := c.Get(ctx, req.NamespacedName, &localModelNode); err != nil {
		c.Log.Error(err, "Error getting LocalModelNode", "name", req.Name)
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// 4. Cleanup jobs for models that are not in the spec
	if err := c.cleanupJobs(ctx, localModelNode); err != nil {
		c.Log.Error(err, "Job cleanup err")
		return reconcile.Result{}, err
	}

	// 5. Download models not present locally
	downloadErr := c.downloadModels(ctx, &localModelNode)
	if downloadErr != nil {
		c.Log.Error(downloadErr, "Model download err")
	}

	// 6. Delete models that are not in the spec
	deleteErr := c.deleteModels(localModelNode)
	if deleteErr != nil {
		c.Log.Error(deleteErr, "Model deletion err")
	}
	if err := errors.Join(downloadErr, deleteErr); err != nil {
		return reconcile.Result{}, err
	}

	// Requeue to check local folders periodically
	return reconcile.Result{RequeueAfter: reconcilationFreqency}, nil
}

func (c *LocalModelNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if c.APIReader == nil {
		c.APIReader = mgr.GetAPIReader()
	}
	return ctrl.NewControllerManagedBy(mgr).
		// Do not reconcile on status change, when a job is created and the status is updated, the next reconcile is triggered immediately and
		// there is a chance that the job is not returned when we list jobs, causing the same job to be created twice.
		// Keep AnnotationChangedPredicate because we use it to trigger reconciliation in the test
		For(&v1alpha1.LocalModelNode{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Owns(&batchv1.Job{}).
		Complete(c)
}
