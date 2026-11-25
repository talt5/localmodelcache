# Upgrade

## From v0.2 to v0.3

Upgrade the CRD chart first, then `localmodelcache`. Caches and their files stay.

A `LocalModelCache` now creates claims in the namespaces labelled `local.cache.ai/models: enabled`.
See [Share a model between namespaces](model-cache.md#share-a-model-between-namespaces). Nothing
changes for existing caches until you label a namespace.

These are now refused, when a cache is created or gets another node group. Existing caches stay:

- a `LocalModelNamespaceCache` while a PVC it would create exists. Create the cache first, or use
  another name. With `disableVolumeManagement` your own PVCs are not checked.
- a cache whose claim or volume names equal those of another cache. The names are joined with dashes,
  so `a` with the group `b-c` equals `a-b` with the group `c`, and a `LocalModelNamespaceCache` `a` with
  the group `b` in the namespace `c-d` equals one with the group `b-c` in the namespace `d`.

Outside OpenShift the node agent now runs as UID and GID 1000, as the download Jobs do. Run a custom
download container as UID 1000 too, or the node agent cannot delete what it downloads.

Before you roll back to v0.2, stop the workloads that mount claims of a `LocalModelCache`: v0.2 deletes
those claims. The files stay.

## From v0.1 to v0.2

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
