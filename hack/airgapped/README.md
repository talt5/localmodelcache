# LocalModelCache @VERSION@, air-gapped bundle (linux/amd64)

For OpenShift 4.22 (Kubernetes 1.35) or later. `images.txt` lists every image. vLLM itself is not
included. If you run an older version, read UPGRADE.md first.

1. On a machine that reaches the internal registry: `./load-images.sh <registry>/<project>`.
   It verifies checksums, pushes the images and prints two `sed` commands. Run them.
2. The cluster must be able to pull from that registry. If it needs credentials, add it to the
   cluster-wide pull secret.
3. Skip this step if cert-manager is already installed: `oc apply -f cert-manager.yaml`
4. On each node that stores models: `oc label node <node> local.cache.ai/node=worker`
   If these nodes are tainted, set `localmodelcache.localmodelnode.controller.tolerations` in
   values.yaml. The download Jobs get the same tolerations.
5. Install:

       helm upgrade -i localmodelcache-crd charts/localmodelcache-crd-@VERSION@.tgz -n localmodelcache --create-namespace
       helm upgrade -i localmodelcache charts/localmodelcache-@VERSION@.tgz -n localmodelcache -f values.yaml

6. Adapt `sample-vllm-node-local.yaml`: the model must come from storage inside the network, such
   as `s3://`.

## Internal S3

1. Put the credentials in a Secret in `localmodelcache-jobs`, annotated with
   `local.cache.ai/s3-endpoint` and the other `s3-*` settings. List it under `secrets` of a
   ServiceAccount there, and name that ServiceAccount in the cache's `spec.serviceAccountName`.
2. For a private CA, set `localmodelcache.storage.caBundleConfigMapName: corp-ca` in values.yaml
   and run `oc create configmap corp-ca -n localmodelcache-jobs --from-file=cabundle.crt=<ca.pem>`.
3. For a shared-PVC cache (`pvcRef`), create the same objects in the cache's namespace. If the
   ConfigMap is missing, the download Pod waits in `ContainerCreating`.

The images keep their own licences: LocalModelCache and cert-manager (Apache-2.0), Red Hat UBI
(Red Hat UBI EULA) and, in localmodelcache-mcv:@VERSION@-cuda, NVIDIA CUDA (NVIDIA Deep Learning
Container License).
