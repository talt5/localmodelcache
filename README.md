# LocalModelCache

LocalModelCache downloads models to the nodes that serve them and caches compiled GPU kernels,
for any workload that can mount a PVC, such as a vLLM Deployment or your own server. It does
not serve models itself.

It is a fork of [KServe](https://github.com/kserve/kserve) trimmed to its model-management
features, with no InferenceService or LLMInferenceService. In this document, LocalModelCache
is the product and `LocalModelCache` is one of its resources.

| Resource | Scope | What it does |
| --- | --- | --- |
| `LocalModelNodeGroup` | cluster | A set of nodes, and the local volume on each of them, that holds cached models. |
| `LocalModelCache` | cluster | Downloads a model to every node of its node groups. |
| `LocalModelNamespaceCache` | namespace | Downloads a model to the nodes of its node groups, or into a ReadWriteMany PVC you provide, and gives workloads in its namespace a PVC to mount. |
| `ClusterStorageContainer` | cluster | Replaces the container that downloads models from a given URI scheme. |
| `KernelCache`, `KernelCacheCapture`, `KernelCacheNodeGroup` | namespace, namespace, cluster | Capture the compiled kernel cache of a running Deployment as an OCI image and mount it into its later Pods. |

The API group is `local.cache.ai`, so LocalModelCache and upstream KServe can share a cluster.

## Install

The webhooks need [cert-manager](https://cert-manager.io). Install it first, for example with
`make deploy-cert-manager`.

With Helm:

```sh
helm upgrade -i localmodelcache-crd charts/localmodelcache-crd -n localmodelcache --create-namespace
helm upgrade -i localmodelcache charts/localmodelcache -n localmodelcache
```

On OpenShift, also set `openshift.enabled=true` and keep the model cache under `/var`, since
the root filesystem is read-only:

```sh
helm upgrade -i localmodelcache charts/localmodelcache -n localmodelcache \
  --set openshift.enabled=true \
  --set localmodelcache.localmodelnode.controller.hostPath=/var/mnt/models
```

This creates the SecurityContextConstraints the node agent and the download Jobs need and pins
every Pod to its SCC. See [the chart README](charts/localmodelcache/README.md)
for all values.

Without Helm, `make deploy` applies `config/default`: the CRDs, the controller and both node
agents, in the `localmodelcache` namespace.

The controller reads its settings from the `localmodelcache-config` ConfigMap in its own
namespace:

| Key | Configures |
| --- | --- |
| `localModel` | The download Job namespace, image and tolerations, `fsGroup`, and whether the controller creates the PVCs workloads mount (`disableVolumeManagement`). |
| `storageInitializer` | Resources and CA bundle of the download container. The CA bundle is a ConfigMap with the key `cabundle.crt`, in the job namespace and in the namespace of each shared-PVC cache. |
| `credentials` | Where download Jobs find storage credentials. |
| `kernelcache` | Kernel capture, prefetch, registry and artifact signing. |

### Upgrade from v0.1

Nothing upgrades in place: v0.2 changes the API group, namespaces and every object name. Helm
would also delete the old CRDs together with all their resources.

1. Save your manifests and stop the workloads that mount caches. Delete the caches, kernel caches
   and node groups while the old controller still runs, so that it can clean up.
2. Uninstall `kserve-localmodel`, then `kserve-localmodel-crd`. Keep CRDs that an upstream KServe
   on the same cluster uses.
3. Install v0.2. Label the nodes `local.cache.ai/node=worker`, and recreate the Secrets,
   ServiceAccounts and CA ConfigMap in `localmodelcache-jobs`.
4. In your manifests, replace `serving.kserve.io` with `local.cache.ai` in `apiVersion` and
   annotations, and the Helm values root `kserve` with `localmodelcache`. Models download again,
   and kernel caches are captured again.

Never run the v0.1 and v0.2 node agents on the same host path: each deletes the model folders it
does not know.

## Cache a model and mount it

Start with a node group. Its PersistentVolume spec describes the local volume on each node,
and its path must match the node agent's host path (`localmodelcache.localmodelnode.controller.hostPath`):

```yaml
apiVersion: local.cache.ai/v1alpha1
kind: LocalModelNodeGroup
metadata:
  name: gpu
spec:
  storageLimit: 200Gi
  persistentVolumeSpec:
    accessModes: [ReadWriteOnce]
    capacity:
      storage: 200Gi
    local:
      path: /mnt/models   # /var/mnt/models on OpenShift
    storageClassName: local-storage
    nodeAffinity:
      required:
        nodeSelectorTerms:
          - matchExpressions:
              - key: local.cache.ai/node
                operator: In
                values: [worker]
  persistentVolumeClaimSpec:
    accessModes: [ReadWriteOnce]
    resources:
      requests:
        storage: 200Gi
    storageClassName: local-storage
```

If the nodes are tainted, set `localmodelcache.localmodelnode.controller.tolerations`. It applies to
the node agent and to the download Jobs.

`storageLimit` caps the group. A cache is refused when the `modelSize` of all its caches, counting
each `sourceModelUri` once, would exceed it. The check trusts `modelSize` and counts each group on
its own, so also give the models their own disk: PV and PVC sizes do not limit a `local` volume.

Then cache the model in the namespace your workload runs in:

```yaml
apiVersion: local.cache.ai/v1alpha1
kind: LocalModelNamespaceCache
metadata:
  name: qwen3
  namespace: inference
spec:
  sourceModelUri: hf://Qwen/Qwen3-0.6B
  modelSize: 2Gi
  nodeGroups: [gpu]
```

The controller downloads the model to every node in the group and creates the PVC
`qwen3-gpu` in `inference`, which `status.persistentVolumeClaims` lists. The root of the claim
is the model's own folder, so mount it at any directory without a `subPath`:

```yaml
spec:
  containers:
    - name: vllm
      image: vllm/vllm-openai:latest
      args: ["--model", "/models/qwen3"]
      volumeMounts:
        - name: model
          mountPath: /models/qwen3
          readOnly: true
  volumes:
    - name: model
      persistentVolumeClaim:
        claimName: qwen3-gpu
        readOnly: true   # the Pod cannot change the model
```

The volume is pinned to the node group, so the Pod runs on one of its nodes. On a node where the
download has not started, the Pod waits in `ContainerCreating` until the folder exists. It does
not wait for the download to finish, so check that `status.copies.available` equals
`status.copies.total`, the number of nodes in the group, before you deploy.

The node group's volume must be `local` or `hostPath`. The volume behind the claim is always
`hostPath`: Kubernetes would change the group and the SELinux labels of a `local` volume for the
first Pod that mounts it, and lock out the others.

A claim always exposes the model it was created for. If a PersistentVolume of the same name
exposes another path, for example one created by a version that used `subPath`, the controller
logs an error and leaves the claim out of the status. Delete and recreate that cache, and remove
`subPath` from its workloads.

With `disableVolumeManagement` the controller creates no claims, and `status.subPath` gives the
model's path inside the node group's volume.

Deleting the cache, or removing a node group from it, is refused while a running Pod mounts the
claim. A node that leaves the group, for example because it lost its label, is no longer counted
but keeps its files until the cache is deleted. The whole example is in
[docs/samples/vllm-node-local.yaml](docs/samples/vllm-node-local.yaml).

### `LocalModelCache` or `LocalModelNamespaceCache`

A PVC belongs to one namespace, so the cluster-scoped `LocalModelCache` only downloads models:

| | `LocalModelCache` | `LocalModelNamespaceCache` |
| --- | --- | --- |
| Scope | cluster | namespace |
| Downloads to the node group | yes | yes |
| Creates a PVC for workloads | no | yes |
| Refuses deletion while Pods use the model | no | yes, with `nodeGroups` |
| Shared PVC (`pvcRef`) | no | yes |

If a few namespaces use a model, create a `LocalModelNamespaceCache` in each. To pre-load a model
centrally as well, create a `LocalModelCache` with the same `sourceModelUri`. Caches with the
same URI share one folder on each node, and the files stay until the last of them is deleted.

To use a `LocalModelCache` alone, create the PV and PVC yourself in each namespace. Take
`<status.subPath>` from `kubectl get localmodelcache <name> -o jsonpath='{.status.subPath}'`:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: qwen3-team-a   # unique per namespace
spec:
  accessModes: [ReadOnlyMany]
  capacity:
    storage: 10Gi
  storageClassName: local-storage
  persistentVolumeReclaimPolicy: Retain
  claimRef:
    namespace: team-a
    name: qwen3
  hostPath:
    path: /mnt/models/<status.subPath>   # /var/mnt/models/<status.subPath> on OpenShift
    type: Directory
  nodeAffinity:
    required:
      nodeSelectorTerms:
        - matchExpressions:
            - key: local.cache.ai/node
              operator: In
              values: [worker]
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: qwen3
  namespace: team-a
spec:
  accessModes: [ReadOnlyMany]
  storageClassName: local-storage
  volumeName: qwen3-team-a
  resources:
    requests:
      storage: 10Gi
```

Mount this claim with `readOnly: true` too. Nothing protects it: deleting the `LocalModelCache`
removes the files under running Pods, unless another cache uses the same URI.

### Shared PVC instead of node disks

To keep one copy on network storage, create a ReadWriteMany PVC and point the cache at it
with `pvcRef` instead of `nodeGroups`. The controller imports the model into `models/<storage
key>` on the claim with a Job in the same namespace, and `status.subPath` reports that path. The
data is yours: deleting the cache is allowed at any time and leaves the files on the PVC.
See [docs/samples/vllm-shared-pvc.yaml](docs/samples/vllm-shared-pvc.yaml).

### Custom download container

Download Jobs run the `storage-initializer` image built from `python/storage`. To use another
image for some URIs, for example one from an internal registry, create a
`ClusterStorageContainer` with `workloadType: localModelDownloadJob`. See
[docs/samples/storage-container.yaml](docs/samples/storage-container.yaml).

## Kernel cache

The kernel cache captures the compiled kernels (for example vLLM's `torch.compile` and Triton
caches) of a running Deployment and mounts them into its later Pods, so they skip compilation.
Opt in through the Pod template of a Deployment:

| Annotation | Value |
| --- | --- |
| `local.cache.ai/kernelcache-supported` | `"true"` to opt in. |
| `local.cache.ai/kernelcache-model-uri` | The model the Pod serves. It is part of the cache identity. |
| `local.cache.ai/kernelcache-container` | The runtime container. Needed only when the Pod has more than one. |
| `local.cache.ai/kernelcache-sidecar-injection` | `"true"` or `"false"` to override capture for this workload. |

The `kernelcache` key of `localmodelcache-config` turns the feature on and sets the registry
captured images are pushed to. The cluster must support OCI image volumes. With Helm and the
registry of OpenShift:

```yaml
localmodelcache:
  kernelcache:
    enabled: true
    registry:
      endpoint: image-registry.openshift-image-registry.svc:5000
      caConfigMapRef: {name: openshift-service-ca.crt, key: service-ca.crt}
      auth:
        type: serviceAccountToken
        # The chart lets the controller bind these roles in the namespaces of the workloads.
        pushRoleRef: {kind: ClusterRole, name: "system:image-builder"}
        pullRoleRef: {kind: ClusterRole, name: "system:image-puller"}
```

Then name the nodes that get the images in a `KernelCacheNodeGroup`, whose `nodeSelector` and
`tolerations` also place the kernel cache agent, and deploy
[docs/samples/vllm-kernel-cache.yaml](docs/samples/vllm-kernel-cache.yaml).

A Pod that cannot use or capture a cache starts without it, with an admission warning. One reason is
a volume of its own at a parent of the cache path, such as `/root/.cache`.

A cache outlives its Deployment, so that the next Deployment with the same runtime and model starts
fast. To remove one, delete its `KernelCache` and, in the registry, its image and signature.

## Development

`make help` lists the targets. The common ones:

| Target | Does |
| --- | --- |
| `make test` | Go unit and envtest suites. |
| `make precommit` | Formatting, lint, code generation and chart sync. Run it before committing. |
| `make manifests` | Regenerates CRDs, RBAC and deepcopy after API or RBAC marker changes. |
| `make docker-build docker-push` | Builds and pushes the controller and node agent images (`KO_DOCKER_REPO`, `TAG`). |
| `make deploy-helm` | Installs the charts from this checkout. |
