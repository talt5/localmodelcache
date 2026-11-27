# localmodelcache

![Version: v0.3.0](https://img.shields.io/badge/Version-v0.3.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.3.0](https://img.shields.io/badge/AppVersion-v0.3.0-informational?style=flat-square)

LocalModelCache - Local Model Storage and Caching for Edge and On-Premise Deployments

**Homepage:** <https://github.com/talt5/localmodelcache>

## Installing the Chart

To install the chart, run the following:

```console
$ helm install localmodelcache oci://ghcr.io/talt5/charts/localmodelcache --version v0.3.0
```

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| LocalModelCache |  | <https://github.com/talt5/localmodelcache> |

## Source Code

* <https://github.com/talt5/localmodelcache>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| localmodelcache.createJobNamespaces | bool | `true` | Create localmodel.jobNamespace, and kernelcache.jobNamespace when the kernel cache is enabled, if they do not exist. They are kept when the release is uninstalled. If false, create them before you install the chart. |
| localmodelcache.kernelcache.abandonedCapturePolicy | string | `"retain"` |  |
| localmodelcache.kernelcache.artifactSecurity.cert.signingProfileRef | string | `"kernelcache-signer"` |  |
| localmodelcache.kernelcache.artifactSecurity.cert.subjectRegexp | string | `"spiffe://localmodelcache/kernelcache-signer"` |  |
| localmodelcache.kernelcache.artifactSecurity.cert.trustBundle | string | `""` | Secret or ConfigMap (namespace/name) with the CA that artifacts must be signed by. Empty uses kernelcache-root-ca in the release namespace. |
| localmodelcache.kernelcache.artifactSecurity.cert.trustBundleKey | string | `""` |  |
| localmodelcache.kernelcache.artifactSecurity.failurePolicy | string | `"reject"` |  |
| localmodelcache.kernelcache.artifactSecurity.mode | string | `"cert"` |  |
| localmodelcache.kernelcache.defaultMountType | string | `"oci"` |  |
| localmodelcache.kernelcache.defaultNodeGroup | string | `""` |  |
| localmodelcache.kernelcache.defaultSidecarInjection | bool | `true` |  |
| localmodelcache.kernelcache.enabled | bool | `false` |  |
| localmodelcache.kernelcache.jobNamespace | string | `"localmodelcache-kernelcache-jobs"` |  |
| localmodelcache.kernelcache.jobTTLSecondsAfterFinished | int | `600` |  |
| localmodelcache.kernelcache.mcvCaptureReadinessTimeoutSeconds | int | `600` |  |
| localmodelcache.kernelcache.mcvImage | string | `"ghcr.io/talt5/localmodelcache-mcv"` |  |
| localmodelcache.kernelcache.mcvResources | object | `{"limits":{"cpu":"1","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}}` | Resources of the capture sidecar. A namespace with a ResourceQuota refuses Pods whose containers set none. |
| localmodelcache.kernelcache.mcvTag | string | `""` |  |
| localmodelcache.kernelcache.prefetchImage | string | `"registry.access.redhat.com/ubi9/ubi-minimal:latest"` |  |
| localmodelcache.kernelcache.prefetchTag | string | `""` |  |
| localmodelcache.kernelcache.reconcileIntervalSeconds | int | `300` |  |
| localmodelcache.kernelcache.registry.auth.pullRoleRef.kind | string | `""` |  |
| localmodelcache.kernelcache.registry.auth.pullRoleRef.name | string | `""` |  |
| localmodelcache.kernelcache.registry.auth.pushRoleRef.kind | string | `""` |  |
| localmodelcache.kernelcache.registry.auth.pushRoleRef.name | string | `""` |  |
| localmodelcache.kernelcache.registry.auth.tokenTTLSeconds | int | `600` |  |
| localmodelcache.kernelcache.registry.auth.type | string | `"none"` |  |
| localmodelcache.kernelcache.registry.caConfigMapRef.key | string | `""` |  |
| localmodelcache.kernelcache.registry.caConfigMapRef.name | string | `""` |  |
| localmodelcache.kernelcache.registry.endpoint | string | `""` |  |
| localmodelcache.kernelcache.registry.insecure | bool | `false` | Push and pull cache images over plain HTTP, for a test registry without TLS. It excludes caConfigMapRef and the serviceAccountToken auth type, and the nodes' container runtime must allow the registry too. |
| localmodelcache.kernelcache.runtimeImagePrefetch.enabled | bool | `false` |  |
| localmodelcache.kernelcache.runtimeImagePrefetch.jobTTLSecondsAfterFinished | int | `86400` |  |
| localmodelcache.kernelcachenode.controller.extraArgs | list | `[]` | Additional container arguments. |
| localmodelcache.kernelcachenode.controller.image | string | `"ghcr.io/talt5/localmodelcache-kernelcache-agent"` |  |
| localmodelcache.kernelcachenode.controller.imagePullPolicy | string | `"IfNotPresent"` |  |
| localmodelcache.kernelcachenode.controller.resources.limits.cpu | string | `"100m"` |  |
| localmodelcache.kernelcachenode.controller.resources.limits.memory | string | `"300Mi"` |  |
| localmodelcache.kernelcachenode.controller.resources.requests.cpu | string | `"10m"` |  |
| localmodelcache.kernelcachenode.controller.resources.requests.memory | string | `"128Mi"` |  |
| localmodelcache.kernelcachenode.controller.tag | string | `""` |  |
| localmodelcache.localmodel.agent.reconcilationFrequencyInSecs | int | `60` |  |
| localmodelcache.localmodel.controller.extraArgs | list | `[]` | Additional container arguments. Example: ["--log-format=otel-json"]. |
| localmodelcache.localmodel.controller.image | string | `"ghcr.io/talt5/localmodelcache-controller"` |  |
| localmodelcache.localmodel.controller.imagePullPolicy | string | `"IfNotPresent"` |  |
| localmodelcache.localmodel.controller.resources.limits.cpu | string | `"100m"` |  |
| localmodelcache.localmodel.controller.resources.limits.memory | string | `"300Mi"` |  |
| localmodelcache.localmodel.controller.resources.requests.cpu | string | `"100m"` |  |
| localmodelcache.localmodel.controller.resources.requests.memory | string | `"200Mi"` |  |
| localmodelcache.localmodel.controller.tag | string | `""` |  |
| localmodelcache.localmodel.defaultJobImage | string | `"ghcr.io/talt5/localmodelcache-storage-initializer"` |  |
| localmodelcache.localmodel.defaultJobTag | string | `""` |  |
| localmodelcache.localmodel.disableVolumeManagement | bool | `false` |  |
| localmodelcache.localmodel.jobNamespace | string | `"localmodelcache-jobs"` |  |
| localmodelcache.localmodel.jobTTLSecondsAfterFinished | int | `3600` |  |
| localmodelcache.localmodel.securityContext.fsGroup | int | `1000` |  |
| localmodelcache.localmodel.sharedPVCImportFSGroup | int | `1000` | fsGroup for shared-PVC import Jobs, which run in the cache's namespace. Independent of securityContext.fsGroup (per-node download Jobs). Set to null to omit it and let namespace admission (e.g. an OpenShift SCC) assign the group. |
| localmodelcache.localmodelnode.controller.affinity | object | `{}` |  |
| localmodelcache.localmodelnode.controller.extraArgs | list | `[]` | Additional container arguments. Example: ["--log-format=otel-json"]. |
| localmodelcache.localmodelnode.controller.hostPath | string | `"/mnt/models"` | Node directory that holds the model cache. It must match the path of the PersistentVolume in your LocalModelNodeGroup. On OpenShift (RHCOS) the root filesystem is immutable, so use a path under /var, such as /var/mnt/models. |
| localmodelcache.localmodelnode.controller.image | string | `"ghcr.io/talt5/localmodelcache-node-agent"` |  |
| localmodelcache.localmodelnode.controller.imagePullPolicy | string | `"IfNotPresent"` |  |
| localmodelcache.localmodelnode.controller.nodeSelector."local.cache.ai/node" | string | `"worker"` |  |
| localmodelcache.localmodelnode.controller.resources.limits.cpu | string | `"100m"` |  |
| localmodelcache.localmodelnode.controller.resources.limits.memory | string | `"300Mi"` |  |
| localmodelcache.localmodelnode.controller.resources.requests.cpu | string | `"100m"` |  |
| localmodelcache.localmodelnode.controller.resources.requests.memory | string | `"200Mi"` |  |
| localmodelcache.localmodelnode.controller.tag | string | `""` |  |
| localmodelcache.localmodelnode.controller.tolerations | list | `[]` | Tolerations of the node agent, and of the download Jobs it creates on its node. Example: [{key: nvidia.com/gpu, operator: Exists, effect: NoSchedule}]. |
| localmodelcache.storage.caBundleConfigMapName | string | `""` | ConfigMap with the CA certificates (key cabundle.crt) that downloads trust. Create it in every namespace that runs download Jobs: localmodelcache.localmodel.jobNamespace, and the namespace of each shared-PVC cache. A ConfigMap named by the storage credentials takes precedence. |
| localmodelcache.storage.caBundleVolumeMountPath | string | `"/etc/ssl/custom-certs"` |  |
| localmodelcache.storage.enableOciModelSupport | bool | `false` |  |
| localmodelcache.storage.image | string | `"ghcr.io/talt5/localmodelcache-storage-initializer"` |  |
| localmodelcache.storage.ociModelMode | string | `""` |  |
| localmodelcache.storage.resources.limits.cpu | string | `"2"` |  |
| localmodelcache.storage.resources.limits.memory | string | `"4Gi"` |  |
| localmodelcache.storage.resources.requests.cpu | string | `"100m"` |  |
| localmodelcache.storage.resources.requests.memory | string | `"100Mi"` |  |
| localmodelcache.storage.s3.CABundle | string | `""` |  |
| localmodelcache.storage.s3.CABundleConfigMap | string | `""` |  |
| localmodelcache.storage.s3.accessKeyIdName | string | `"AWS_ACCESS_KEY_ID"` |  |
| localmodelcache.storage.s3.endpoint | string | `""` |  |
| localmodelcache.storage.s3.region | string | `""` |  |
| localmodelcache.storage.s3.secretAccessKeyName | string | `"AWS_SECRET_ACCESS_KEY"` |  |
| localmodelcache.storage.s3.useAnonymousCredential | string | `""` |  |
| localmodelcache.storage.s3.useHttps | string | `""` |  |
| localmodelcache.storage.s3.useVirtualBucket | string | `""` |  |
| localmodelcache.storage.s3.verifySSL | string | `""` |  |
| localmodelcache.storage.storageSecretNameAnnotation | string | `"local.cache.ai/secretName"` |  |
| localmodelcache.storage.storageSpecSecretName | string | `"storage-config"` |  |
| localmodelcache.storage.tag | string | `""` |  |
| localmodelcache.version | string | `"v0.3.0"` |  |
| openshift.downloadJobServiceAccount | string | `"default"` | ServiceAccount in localmodelcache.localmodel.jobNamespace that runs the model download Jobs. It is granted the download Job SCC, which permits the Jobs' fsGroup. |
| openshift.enabled | bool | `false` | Deploy on OpenShift. Creates SecurityContextConstraints (SCCs), grants them to the chart's ServiceAccounts and the download Job ServiceAccount, and pins each pod to its SCC. |
| openshift.localmodelnode.prepareHostPath.enabled | bool | `true` | Run an init container that creates the cache directory, gives it to runAsUser:runAsGroup and labels it container_file_t so SELinux lets pods write it. Disable it if you prepare the nodes yourself, for example with a MachineConfig. |
| openshift.localmodelnode.prepareHostPath.image | string | `"registry.access.redhat.com/ubi9/ubi-minimal:latest"` | Image for the init container. It needs sh, mkdir, chown, chmod and chcon. |
| openshift.localmodelnode.runAsGroup | int | `1000` | GID the node agent runs as. Download Jobs share it through their fsGroup (localmodelcache.localmodel.securityContext.fsGroup), so both can write the cache. |
| openshift.localmodelnode.runAsUser | int | `1000` | UID the node agent runs as. It owns the model cache on the node. |
| openshift.localmodelnode.seLinuxType | string | `"spc_t"` | SELinux type for the node agent pod, including its init container. spc_t lets the agent read cache files whatever SELinux categories the pods that wrote or mounted them used, and lets the init container relabel the host directory. Empty uses the runtime default (container_t). |
| openshift.sharedPVCImportFSGroup | string | `nil` | fsGroup of shared-PVC import Jobs on OpenShift, replacing localmodelcache.localmodel.sharedPVCImportFSGroup. The Jobs run in the cache's namespace under restricted-v2, which rejects a group outside the namespace's range. Leave it null so admission assigns the namespace's group, which the pods mounting the claim also get. |
