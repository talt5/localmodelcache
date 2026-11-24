# Install

## Requirements

The webhooks need [cert-manager](https://cert-manager.io). Install it first, for example with
`make deploy-cert-manager`.

Label each node that stores models with `local.cache.ai/node=worker`. The node agent runs only there.

Outside OpenShift, prepare the host path, `/mnt/models` by default, on those nodes. The node agent (UID
65532) and the download Jobs (UID 1000) must both be able to write to it and to delete files in it. On a
test cluster, `mkdir -p /mnt/models/models && chmod -R 2777 /mnt/models` is enough. The job namespace
must not enforce the `restricted` Pod Security Standard.

## Helm

From a checkout:

```sh
helm upgrade -i localmodelcache-crd charts/localmodelcache-crd -n localmodelcache --create-namespace
helm upgrade -i localmodelcache charts/localmodelcache -n localmodelcache
```

The charts are also published as `oci://ghcr.io/talt5/charts/localmodelcache-crd` and
`oci://ghcr.io/talt5/charts/localmodelcache`.

> [!IMPORTANT]
> The download image of v0.2.0 was published under its old name. To install v0.2.0 from GHCR, add these
> flags to the second command:
>
> ```sh
> --set localmodelcache.storage.image=ghcr.io/talt5/storage-initializer
> --set localmodelcache.localmodel.defaultJobImage=ghcr.io/talt5/storage-initializer
> ```

## OpenShift

Set `openshift.enabled=true` and keep the model cache under `/var`, since
the root filesystem is read-only:

```sh
helm upgrade -i localmodelcache charts/localmodelcache -n localmodelcache \
  --set openshift.enabled=true \
  --set localmodelcache.localmodelnode.controller.hostPath=/var/mnt/models
```

This creates the SecurityContextConstraints the node agent and the download Jobs need and pins
every Pod to its SCC. See [the chart README](../charts/localmodelcache/README.md)
for all values.

## Air-gapped clusters

Each [release](https://github.com/talt5/localmodelcache/releases) has a bundle for `linux/amd64` with the
charts and every image, including cert-manager. Its README describes the installation.

## Without Helm

`make deploy` applies `config/default`: the CRDs, the controller and both node
agents, in the `localmodelcache` namespace.

## Settings

The controller reads its settings from the `localmodelcache-config` ConfigMap in its own
namespace:

| Key | Configures |
| --- | --- |
| `localModel` | The download Job namespace, image and tolerations, `fsGroup`, and whether the controller creates the PVCs workloads mount (`disableVolumeManagement`). |
| `storageInitializer` | Resources and CA bundle of the download container. The CA bundle is a ConfigMap with the key `cabundle.crt`, in the job namespace and in the namespace of each shared-PVC cache. |
| `credentials` | Where download Jobs find storage credentials. |
| `kernelcache` | Kernel capture, prefetch, registry and artifact signing. |
