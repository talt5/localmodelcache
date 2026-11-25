# Model cache

How to download models to nodes or to a shared PVC, and mount them.

## Define a node group

Label each node that stores models:

```sh
kubectl label node <node> local.cache.ai/node=worker
```

Then create a node group. Its PersistentVolume spec describes the local volume on each node,
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
each source once, would exceed it. The check trusts `modelSize` and counts each group on
its own, so also give the models their own disk: PV and PVC sizes do not limit a `local` volume.

## Cache the model and mount it

Cache the model in the namespace your workload runs in:

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

## Wait for the download

The volume is pinned to the node group, so the Pod runs on one of its nodes. On a node where the
download has not started, the Pod waits in `ContainerCreating` until the folder exists. It does
not wait for the download to finish, so check that `status.copies.available` equals
`status.copies.total`, the number of nodes in the group, before you deploy.

## How claims behave

The node group's volume must be `local` or `hostPath`. The volume behind the claim is always
`hostPath`: Kubernetes would change the group and the SELinux labels of a `local` volume for the
first Pod that mounts it, and lock out the others.

A claim always exposes the model it was created for. If a PersistentVolume of the same name
exposes another path, for example one created by a version that used `subPath`, the controller
logs an error and leaves the claim out of the status. Delete and recreate that cache, and remove
`subPath` from its workloads.

With `disableVolumeManagement` the controller creates no claims, and `status.subPath` gives the
model's path inside the node group's volume.

## Delete a cache

Deleting the cache, or removing a node group from it, is refused while a running Pod mounts the
claim. A node that leaves the group, for example because it lost its label, is no longer counted
but keeps its files until the cache is deleted. The whole example is in
[docs/samples/vllm-node-local.yaml](samples/vllm-node-local.yaml).

## Share a model between namespaces

`LocalModelCache` is cluster-scoped. The controller creates the PVC `<cache>-<node group>` in every
namespace that `namespaceSelector` selects, also in namespaces created or labelled later:

```yaml
apiVersion: local.cache.ai/v1alpha1
kind: LocalModelCache
metadata:
  name: qwen3
spec:
  sourceModelUri: hf://Qwen/Qwen3-0.6B
  modelSize: 2Gi
  nodeGroups: [gpu]
  # namespaceSelector:          # the default
  #   matchLabels:
  #     local.cache.ai/models: enabled
```

```sh
kubectl label namespace team-a team-b local.cache.ai/models=enabled
kubectl get localmodelcache qwen3 -o jsonpath='{.status.persistentVolumeClaims}'
# ["team-a/qwen3-gpu","team-b/qwen3-gpu"]
```

Mount `qwen3-gpu` as above: at any directory, without a `subPath` and with `readOnly: true`. The
namespaces share one copy of the model on each node. The whole example is in
[docs/samples/vllm-cluster-cache.yaml](samples/vllm-cluster-cache.yaml).

| `namespaceSelector` | Namespaces |
| --- | --- |
| left out | those labelled `local.cache.ai/models: enabled`, for every such cache |
| `matchExpressions: [{key: kubernetes.io/metadata.name, operator: In, values: [team-a, team-b]}]` | a fixed list |
| `matchExpressions: [{key: kubernetes.io/metadata.name, operator: DoesNotExist}]` | none: only download |
| `{}` | all, including those of the system |

Whoever may label namespaces decides who can read the models. The job namespace never gets a claim
for workloads.

Deleting the cache, or a change of its selector or node groups, is refused while a running Pod
mounts a claim that it would delete. Nothing refuses to remove the label of a namespace: its claims
stay in `Terminating` until the Pods that mount them finish, and new Pods there cannot use them.
Running Pods keep the files, since the cache still exists.

### `LocalModelCache` or `LocalModelNamespaceCache`

| | `LocalModelCache` | `LocalModelNamespaceCache` |
| --- | --- | --- |
| Scope | cluster | namespace |
| PVCs for workloads | in the selected namespaces | in its own namespace |
| Shared PVC (`pvcRef`) | no | yes |

Caches with the same source, which is `sourceModelUri` and `storage.parameters`, share one folder on
each node. The files stay until the last of them is deleted.

A claim belongs to the cache that created it. A `LocalModelNamespaceCache` is refused while a claim
it would create exists, either one of a `LocalModelCache` or your own. A `LocalModelCache` is
accepted, leaves such claims alone and creates its own when the name is free. So do not reuse the
name of a `LocalModelCache` for a `LocalModelNamespaceCache`.

### Move from `LocalModelNamespaceCache`

Claims of caches with the same name and node group have the same name, so the `LocalModelCache`
creates its claim when the other is gone. The files stay all the time.

1. Create the `LocalModelCache` with the same name and source, and label the namespaces.
2. In one namespace at a time, stop the workload and delete the `LocalModelNamespaceCache`.
3. Wait until the status of the `LocalModelCache` lists the claim, then start the workload.

### Your own volumes

With `disableVolumeManagement`, or for a claim of another name, create a `hostPath` PV and a PVC
yourself. The path is the node group's path followed by `status.subPath`. Give the PV the node
affinity of the node group, `type: Directory` and a `claimRef` to your claim, and mount the claim
with `readOnly: true`. Nothing protects it: deleting the last cache of the model removes the files
under running Pods.

## Shared PVC instead of node disks

To keep one copy on network storage, create a ReadWriteMany PVC and point the cache at it
with `pvcRef` instead of `nodeGroups`. The controller imports the model into `models/<storage
key>` on the claim with a Job in the same namespace, and `status.subPath` reports that path. The
data is yours: deleting the cache is allowed at any time and leaves the files on the PVC.
See [docs/samples/vllm-shared-pvc.yaml](samples/vllm-shared-pvc.yaml).

## Credentials and internal sources

For a private source, put the credentials in a Secret, list it under `secrets` of a ServiceAccount, and
name that ServiceAccount in the cache's `spec.serviceAccountName`. `oci://` sources use
`spec.imagePullSecrets` instead. They belong in the namespace that
runs the download: `localmodelcache-jobs`, or the namespace of the cache when it uses `pvcRef`.

For S3, the Secret holds `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`. Annotate it with
`local.cache.ai/s3-endpoint` and the other `local.cache.ai/s3-*` settings if the storage is not AWS.

For a private CA, set the Helm value `localmodelcache.storage.caBundleConfigMapName`, and create that
ConfigMap with the key `cabundle.crt` in the same namespaces. If it is missing, the download Pod waits
in `ContainerCreating`.

## Custom download container

Download Jobs run the `localmodelcache-storage-initializer` image built from `python/storage`. To use another
image for some URIs, for example one from an internal registry, create a
`ClusterStorageContainer` with `workloadType: localModelDownloadJob`. See
[docs/samples/storage-container.yaml](samples/storage-container.yaml).

## Teams that share a cluster

Node-local caches do not separate teams. A cache exposes the folder of its source, even if another
cache downloaded the model with credentials that this one lacks. Download Jobs run in one namespace,
and any cache can name the credentials there. Use shared PVCs, whose import runs in the namespace of
the cache, or let RBAC decide who creates node-local caches.

Caches whose `storage.parameters` differ, such as `endpoint_url`, never share a folder, so one cannot
replace the model of another. The parameters of a cache cannot change.
