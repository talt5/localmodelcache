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
`qwen3-gpu` in `inference`. Its status says what to mount:

```sh
kubectl get localmodelnamespacecache qwen3 -n inference \
  -o jsonpath='{.status.persistentVolumeClaims} {.status.subPath}'
```

Mount the claim with that `subPath` at any directory. The PVC's volume is pinned to the node
group, so the Pod lands on a node that has the model:

```yaml
spec:
  containers:
    - name: vllm
      image: vllm/vllm-openai:latest
      args: ["--model", "/models/qwen3"]
      volumeMounts:
        - name: model
          mountPath: /models/qwen3
          subPath: models/<storage key>   # status.subPath
          readOnly: true
  volumes:
    - name: model
      persistentVolumeClaim:
        claimName: qwen3-gpu
        readOnly: true
```

Deleting the cache is refused while a running Pod mounts its claim. The whole example is in
[docs/samples/vllm-node-local.yaml](docs/samples/vllm-node-local.yaml).

A cluster-scoped `LocalModelCache` pre-loads the nodes without creating claims. Caches with
the same `sourceModelUri` share one folder on each node, so a `LocalModelNamespaceCache` in a
workload namespace reuses a model the `LocalModelCache` already downloaded.

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
