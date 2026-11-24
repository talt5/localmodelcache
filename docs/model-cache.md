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

## `LocalModelCache` or `LocalModelNamespaceCache`

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
same source, which is `sourceModelUri` and `storage.parameters`, share one folder on each node. The
files stay until the last of them is deleted.

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
