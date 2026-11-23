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

The API group stays `serving.kserve.io/v1alpha1`. The chart, ConfigMap and namespace names also
keep their `kserve` prefix, so use those names in the commands below.

## Install

The webhooks need [cert-manager](https://cert-manager.io). Install it first, for example with
`make deploy-cert-manager`.

With Helm:

```sh
helm upgrade -i kserve-localmodel-crd charts/kserve-localmodel-crd -n kserve --create-namespace
helm upgrade -i kserve-localmodel charts/kserve-localmodel-resources -n kserve
```

On OpenShift, also set `openshift.enabled=true` and keep the model cache under `/var`, since
the root filesystem is read-only:

```sh
helm upgrade -i kserve-localmodel charts/kserve-localmodel-resources -n kserve \
  --set openshift.enabled=true \
  --set kserve.localmodelnode.controller.hostPath=/var/mnt/models
```

This creates the SecurityContextConstraints the node agent and the download Jobs need and pins
every Pod to its SCC. See [the chart README](charts/kserve-localmodel-resources/README.md)
for all values.

Without Helm, `make deploy` applies `config/default`: the CRDs, the controller and both node
agents, in the `kserve` namespace.

The controller reads its settings from the `kserve-localmodel-config` ConfigMap in its own
namespace:

| Key | Configures |
| --- | --- |
| `localModel` | The download Job namespace and image, `fsGroup`, and whether the controller creates the PVCs workloads mount (`disableVolumeManagement`). |
| `storageInitializer` | Resources and CA bundle of the download container. |
| `credentials` | Where download Jobs find storage credentials. |
| `kernelcache` | Kernel capture, prefetch, registry and artifact signing. |

## Cache a model and mount it

Start with a node group. Its PersistentVolume spec describes the local volume on each node,
and its path must match the node agent's host path (`kserve.localmodelnode.controller.hostPath`):

```yaml
apiVersion: serving.kserve.io/v1alpha1
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
              - key: kserve/localmodel
                operator: In
                values: [worker]
  persistentVolumeClaimSpec:
    accessModes: [ReadWriteOnce]
    resources:
      requests:
        storage: 200Gi
    storageClassName: local-storage
```

Then cache the model in the namespace your workload runs in:

```yaml
apiVersion: serving.kserve.io/v1alpha1
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
        readOnly: true   # keep it: otherwise OpenShift relabels the files for this Pod only
```

The volume is pinned to the node group, so the Pod runs on one of its nodes. On a node where the
download has not started, the Pod waits in `ContainerCreating` until the folder exists. It does
not wait for the download to finish, so check that `status.copies.available` equals
`status.copies.total` before you deploy. The node group's volume must be `local` or `hostPath`.

A claim always exposes the model it was created for. If a PersistentVolume of the same name
exposes another path, for example one created by a version that used `subPath`, the controller
logs an error and leaves the claim out of the status. Delete and recreate that cache, and remove
`subPath` from its workloads.

With `disableVolumeManagement` the controller creates no claims, and `status.subPath` gives the
model's path inside the node group's volume.

Deleting the cache is refused while a running Pod mounts its claim. The whole example is in
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
  local:
    path: /mnt/models/<status.subPath>   # /var/mnt/models/<status.subPath> on OpenShift
  nodeAffinity:
    required:
      nodeSelectorTerms:
        - matchExpressions:
            - key: kserve/localmodel
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
| `serving.kserve.io/kernelcache-supported` | `"true"` to opt in. |
| `serving.kserve.io/kernelcache-model-uri` | The model the Pod serves. It is part of the cache identity. |
| `serving.kserve.io/kernelcache-container` | The runtime container. Needed only when the Pod has more than one. |
| `serving.kserve.io/kernelcache-sidecar-injection` | `"true"` or `"false"` to override capture for this workload. |

The `kernelcache` key of `kserve-localmodel-config` turns the feature on and sets the registry
captured images are pushed to. See
[docs/samples/vllm-kernel-cache.yaml](docs/samples/vllm-kernel-cache.yaml).

## Development

`make help` lists the targets. The common ones:

| Target | Does |
| --- | --- |
| `make test` | Go unit and envtest suites. |
| `make precommit` | Formatting, lint, code generation and chart sync. Run it before committing. |
| `make manifests` | Regenerates CRDs, RBAC and deepcopy after API or RBAC marker changes. |
| `make docker-build docker-push` | Builds and pushes the controller and node agent images (`KO_DOCKER_REPO`, `TAG`). |
| `make deploy-helm` | Installs the charts from this checkout. |
