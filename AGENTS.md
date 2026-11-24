# LocalModelCache

Caches models and compiled GPU kernels on Kubernetes nodes for any workload. It does not serve models. It is a fork of KServe trimmed to model management: InferenceService and LLMInferenceService are gone, so do not reintroduce them or their names (`TestNoUpstreamNames` fails). The Go code implements custom controllers with controller-runtime. The API group is `local.cache.ai/v1alpha1`.

The target platform is OpenShift 4.22 (Kubernetes 1.35), often air-gapped. Plain Kubernetes is supported too.

## Constraints

- **Generated files are read-only.** `make precommit` overwrites them. Edit the source instead:

  | Generated | Source |
  | --- | --- |
  | `config/crd/{full,minimal}/*/local.cache.ai_*.yaml`, `charts/localmodelcache-crd*/templates/`, `test/crds/`, `zz_generated.deepcopy.go` | Go types in `pkg/apis/cache/v1alpha1` |
  | `config/rbac/{localmodel,localmodelnode,kernelcache,kernelcachenode}/role.yaml` | RBAC markers in the controllers |
  | `charts/localmodelcache/files/resources.yaml` | `config/` |
  | `charts/localmodelcache/values.yaml` | `charts/_common/*.yaml` |
  | `charts/localmodelcache/templates/_utils.tpl`, `_resources.tpl` | `charts/_common/*.tpl` |
  | `charts/*/README.md` | values comments and `README.md.gotmpl` |
  | `images.sh` | `images.env` |

  The other files in `charts/localmodelcache/files/` (`*-patch.yaml`) and `templates/` are written by hand.
- **The Makefile is the source of truth.** Read `Makefile`, its `Makefile.*.mk` includes and `deps.env` (tool and release versions) before running or modifying build steps. GNU make 4 and GNU sed are required.
- **Two Go modules.** `kernelcache/mcv` has its own `go.mod` and needs gpgme and other CGO libraries. Where they are missing, `make vet` and `make go-lint` skip it and `make test` never runs it; the `mcv-build-test` workflow does. Change Go and shared dependency versions in both modules.
- **Platform hooks.** `//go:build !distro` guards `pkg/controller/v1alpha1/localmodelnode/platform_default.go` and `pkg/testing/ctrl_default.go`. Distributions replace `cmd/*/main_start_default.go` instead. `GOTAGS` sets the tags.
- **Image names** must agree in `charts/_common`, `images.env` and the publishing workflows (`TestChartImagesArePublished`).
- **`make precommit` before committing.** It handles formatting, linting, code generation and manifest sync in the right order.
- Keep `The KServe Authors` copyright headers. New files get `The LocalModelCache Authors`.

## Project Structure

- API types: `pkg/apis/cache/v1alpha1`
- Controllers: `pkg/controller/v1alpha1/{localmodel,localmodelnode,kernelcache,kernelcachenode}`
- Webhooks: `pkg/webhook/admission/{localmodelcache,localmodelnamespacecache,kernelcache}`
- Binaries: `cmd/localmodel` (controller and webhooks), `cmd/localmodelnode` and `cmd/kernelcachenode` (node agents), `cmd/crd-gen` (minimal CRDs)
- Settings: the `localmodelcache-config` ConfigMap, parsed by `pkg/localmodelcache/config.go`, `pkg/credentials` and `pkg/kernelcache/config`
- Model download image: `python/storage` (library) and `python/storage-initializer` (entrypoint)
- Kernel capture image: `kernelcache/mcv`
- Manifests: `config/` (kustomize) and `charts/` (Helm)
- Air-gapped bundle: `hack/airgapped`
- User documentation: `README.md` is the overview and the details are in `docs/`. The tests read `docs/model-cache.md` and `docs/samples`, and the bundle ships `docs/upgrade.md`

## Design

Read this before changing the controllers:

- `NodeModelsReconciler` is the only writer of `LocalModelNode.spec`. It rebuilds it from all caches.
- A cache's folder on a node is keyed by its source: `sourceModelUri` and `storage.parameters`. Caches with the same source share it, and it is deleted with the last of them. A node that leaves its group keeps its files until then.
- The claim `<cache>-<node group>` is rooted at the model's folder, so workloads need no `subPath`. Shared-PVC caches (`pvcRef`) do, from `status.subPath`.
- Volumes behind these claims are always `hostPath`. The kubelet relabels and re-owns `local` volumes for the first Pod, which locks out the others. For the same reason, workloads mount claims with `readOnly: true`.
- Volumes carry a label with the cache UID, and orphans are deleted by it: Kubernetes ignores a finalizer added to an object that is being deleted.
- The node agent deletes model folders that it does not know.
- The controller reads Secrets without the cache (`clientOptions` in `cmd/localmodel/cache.go`), since its RBAC has `get` only. It caches only kernel cache Pods and its own ConfigMap (`cacheOptions`): read others with `APIReader`.
- Deployments opt into the kernel cache with Pod template annotations. The Pod webhook must never hold up a Pod: without a usable cache the Pod starts without it, with a warning.

## Commands

`make help` lists the targets you run directly, when to run each and what it rewrites or needs.

```
make test                       # full Go test suite (downloads envtest assets)
make precommit                  # formatting, linting, codegen, manifest sync
make deploy-helm                # install the charts from this checkout (needs cert-manager)
make airgapped-bundle           # build the air-gapped bundle (needs Docker, Helm and network)
```

To iterate, run a subset with `make test-focus`. It uses the envtest assets of `make test` but skips its `fmt`, `vet` and `manifests` steps, and it fails when no test or spec ran:

```
make test-focus PKG=./pkg/localmodelcache RUN='^TestGetStorageInitializerConfigs$'
make test-focus PKG=./pkg/controller/v1alpha1/localmodel FOCUS='own namespace'
```

`PKG` takes package patterns, `RUN` a `go test -run` regex and `FOCUS` a Ginkgo focus regex. With `FOCUS`, packages without a Ginkgo suite are skipped. A Ginkgo suite runs from one `Test*` function, so naming it in `RUN` skips the plain tests in the same package. After an API type change, run `make manifests` first: envtest loads the CRDs from `test/crds/`.

## Testing

- Tests live next to the code they test, in the same package for controllers
- Prefer table-driven tests for pure logic. Use envtest for the wiring of the localmodel and localmodelnode controllers, and `fake.NewClientBuilder` for reconcilers, as the kernel cache ones do
- The fake client ignores RBAC and admission. After changing RBAC, also test on a cluster
- `pkg/testing` has the tests of the whole repository: names, charts, images and RBAC. Chart tests are skipped without Helm
- Python: `.github/workflows/python-test.yml` has the commands

### envtest

envtest runs a real API server and etcd but NOT built-in controllers (Deployment, ReplicaSet, etc.) or garbage collection. Simulate the status updates they would perform with helper functions.

- Suites use Ginkgo/Gomega. Set them up with `pkgtest.NewEnvTest()` from `pkg/testing/`, not raw `envtest.Environment{}`. The returned `*pkgtest.Client` wraps the Kubernetes client, environment and cleanup
- Check `pkg/testing/` for existing Gomega matchers (`BeOwnedBy`, `BeControlledBy`, etc.) before writing new ones
- Each test creates its own namespace, and cleans up with `defer` or `DeferCleanup` immediately after creation
- Use `retry.RetryOnConflict` when updating resources the controller is also reconciling
- Use `Eventually`/`Consistently`, never `time.Sleep`

## Development Workflow

1. **Analyze** - understand the task, identify affected controllers/types, search for reusable patterns
2. **Test first** - write the test before implementation code
3. **Implement** - minimal, focused changes following existing project patterns
4. **Verify** - run selective tests, then `make precommit` before committing
5. **Document** - update `docs/` and the samples when behaviour or settings change

## Pull requests and releases

Before you write a PR title or description, read `.github/PULL_REQUEST_TEMPLATE.md`. Its comments are the rules: follow them, then delete every one. `.github/PR_DESCRIPTION_EXAMPLES.md` shows the right size.

Sign off commits (`git commit -s`). Releases follow [docs/development.md](docs/development.md#release). Tag only a commit that is on `master`, and publish the draft only after every image of the chart can be pulled.

## Controller-Runtime Patterns

New code follows these. Some older code does not.

### Reconcile Loop

- Reconcile must be **idempotent** - same input run N times produces the same result
- Propagate `context.Context` via function arguments, avoid `context.Background()`
- Handle `NotFound` as success for deleted objects
- Use `Patch` with `MergeFrom` for updates to reduce conflicts
- Return errors for failures (controller-runtime handles backoff). Use `RequeueAfter` for delays and for work in progress that no watch reports
- Short-circuit when no-op - already-compliant objects should reconcile with no API calls

### Status and Conditions

- `spec` is user-owned (desired state), `status` is controller-owned (observed state) - never write both in a single API call. The specs of `LocalModelNode` and of captured kernel caches are controller-owned
- Use conditions with CamelCase types and positive polarity for new status: `Ready`, `Available`, etc. The kernel cache kinds also have a `state` or `phase` field
- Always include `observedGeneration` in conditions - without it, `Ready: True` may reflect a previous spec generation
- Change conditions through typed `Mark*` helpers, never on the slice. See `local_model_cache_lifecycle.go` as the reference pattern
- Guard status writes with deep-equal checks - skip when nothing changed to avoid watch churn and infinite reconcile loops
- `reason` is CamelCase and part of the API contract. `lastTransitionTime` updates only when `status` (True/False/Unknown) changes, not on reason/message changes
- For composite conditions, surface the first failing sub-condition's Reason/Message in the parent
- Remove conditions that no longer apply rather than leaving stale values

### Watches and Caching

- Use predicates to drop irrelevant events early
- Prefer `Owns()` and targeted watches over broad `Watches()`
- Avoid expensive list+filter patterns
- Use `APIReader` only when cache staleness causes real correctness issues
- Cached client has no read-your-write consistency - writes hit the API server but are not instantly visible in cache
