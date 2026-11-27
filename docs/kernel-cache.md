# Kernel cache

The kernel cache captures the compiled kernels (for example vLLM's `torch.compile` and Triton
caches) of a running Deployment and mounts them into its later Pods, so they skip compilation.

## Turn it on

The `kernelcache` key of `localmodelcache-config` turns the feature on and sets the registry
captured images are pushed to. The cluster must support OCI image volumes, which
Kubernetes 1.35 and OpenShift 4.22 enable by default. With Helm and the
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

Then name the nodes that get the images in a `KernelCacheNodeGroup`. Its `nodeSelector` and
`tolerations` also place the kernel cache agent:

```yaml
apiVersion: local.cache.ai/v1alpha1
kind: KernelCacheNodeGroup
metadata:
  name: gpu
spec:
  nodeSelector:
    local.cache.ai/node: worker
```

## Opt in a Deployment

A Deployment opts in with annotations on its Pod template:

| Annotation | Value |
| --- | --- |
| `local.cache.ai/kernelcache-supported` | `"true"` to opt in. |
| `local.cache.ai/kernelcache-model-uri` | The model the Pod serves. It is part of the cache identity. |
| `local.cache.ai/kernelcache-container` | The runtime container. Needed only when the Pod has more than one. |
| `local.cache.ai/kernelcache-nodegroup` | The `KernelCacheNodeGroup` of the cache. Needed only when the node is in more than one. |
| `local.cache.ai/kernelcache-sidecar-injection` | `"true"` or `"false"` to override capture for this workload. |

A full example is [docs/samples/vllm-kernel-cache.yaml](samples/vllm-kernel-cache.yaml).

The cache is the runtime container's `VLLM_CACHE_ROOT`, or `/var/cache/vllm` when it sets none.
In that case the webhook sets `VLLM_CACHE_ROOT` to `/var/cache/vllm`, so vLLM writes there
whatever `HOME` the image or the platform gives it. vLLM's own default, `~/.cache/vllm`, does not
work on OpenShift: images run there with `HOME=/` and often cannot enter `/root`.

## Limits and cleanup

A Pod that cannot use or capture a cache starts without it, with an admission warning. One reason is
a volume of its own at a parent of the cache path, such as `/var/cache`.

The capture sidecar runs as UID 1000, or as the namespace's UID on OpenShift. It leaves out the
files it cannot read, such as those a runtime that runs as root writes with mode `0600`. vLLM's
compiled kernels are readable by all users.

A runtime that compiles nothing into the cache path, such as vLLM's CPU build, which keeps
Inductor's cache in `/tmp/torchinductor_<user>`, fails its capture with `no compiled kernels
found`. To capture it, set `TORCHINDUCTOR_CACHE_DIR` under the cache path's
`torch_compile_cache`, such as `/var/cache/vllm/torch_compile_cache/inductor`.

For a test registry without TLS, set `registry.insecure: true`. The container runtime of the
nodes must allow the registry as well.

A cache outlives its Deployment, so that the next Deployment with the same runtime and model starts
fast. To remove one, delete its `KernelCache` and, in the registry, its image and signature.
