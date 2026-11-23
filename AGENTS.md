# LocalModelCache

A fork of KServe trimmed to model management: LocalModelCache (with LocalModelNamespaceCache, LocalModelNode and LocalModelNodeGroup), ClusterStorageContainer and KernelCache. It caches models and compiled GPU kernels for any workload; it does not serve models, and InferenceService and LLMInferenceService are gone. The Go codebase implements custom controllers using controller-runtime.

## Constraints

- **Generated files are read-only.** Never hand-edit these - they are overwritten by `make precommit`:
  - `charts/*/README.md`, `charts/*/templates/`, `charts/localmodelcache/files/resources.yaml` and `values.yaml`
  - Common Helm helpers (`_utils.tpl`, `_resources.tpl`) synced from `charts/_common/`
  - CRDs under `config/crd/`, RBAC roles under `config/rbac/*/role.yaml`, `zz_generated.deepcopy.go`
- **Makefile is the source of truth.** Read `Makefile` and `Makefile.tools.mk` before running or modifying build steps.
- **Distro build tags.** Several files use `//go:build !distro`. Check for `_default.go` / `_distro.go` pairs when adding platform-specific code. `GOTAGS` Makefile variable controls this.
- **`make precommit` before committing.** It handles formatting, linting, code generation, and manifest sync in the right order.

## Project Structure

- API types: `pkg/apis/cache/v1alpha1`
- Controllers: `pkg/controller/v1alpha1/{localmodel,localmodelnode,kernelcache,kernelcachenode}`
- Binaries: `cmd/localmodel` (controller and webhooks), `cmd/localmodelnode` and `cmd/kernelcachenode` (node agents)
- Settings: the `localmodelcache-config` ConfigMap, parsed by `pkg/localmodelcache/config.go` and `pkg/kernelcache/config`
- Python: only `python/storage` and `python/storage-initializer`, the model download image
- Webhooks: `pkg/webhook/admission/`
- CRDs generated from Go types via controller-gen markers

## Commands

`make help` lists the targets you run directly, when to run each and what it rewrites or needs.

Prerequisites: Go (version pinned in `go.mod`) and `make`.

```
make test                       # full Go test suite (downloads envtest assets)
make precommit                  # formatting, linting, codegen, manifest sync
make deploy-helm                # install the charts from this checkout (needs cert-manager)
```

To iterate, run a subset with `make test-focus`. It uses the envtest assets of `make test` but skips its `fmt`, `vet` and `manifests` steps, and it fails when no test or spec ran:

```
make test-focus PKG=./pkg/localmodelcache RUN='^TestGetStorageInitializerConfigs$'
make test-focus PKG=./pkg/controller/v1alpha1/localmodel FOCUS='own namespace'
```

`PKG` takes package patterns, `RUN` a `go test -run` regex and `FOCUS` a Ginkgo focus regex. With `FOCUS`, packages without a Ginkgo suite are skipped. A Ginkgo suite runs from one `Test*` function, so naming it in `RUN` skips the plain tests in the same package. After an API type change, run `make manifests` first: envtest loads the CRDs from `test/crds/`.

## Testing

- Tests live next to the code they test
- Controller tests mix unit tests (`fake.NewClientBuilder`) and envtest integration suites
- Prefer table-driven tests for pure logic, envtest for controller wiring
- Controllers use same-package tests

### envtest

envtest runs a real API server and etcd but NOT built-in controllers (Deployment, ReplicaSet, etc.) or garbage collection. Simulate status updates that external controllers would normally perform.

**Framework and tooling:**
- Suites use Ginkgo/Gomega (`ginkgo.RunSpecs`, `It()`, `Context()`, `BeforeSuite()`)
- Use `pkgtest.NewEnvTest()` from `pkg/testing/` to set up suites - not raw `envtest.Environment{}`. The returned `*pkgtest.Client` wraps the K8s client, environment, and cleanup
- Check `pkg/testing/` for existing Gomega matchers (`BeOwnedBy`, `BeControlledBy`, etc.) before writing new ones

**Isolation and cleanup:**
- Each test creates its own namespace
- Clean up resources with `defer` immediately after creation
- Use `retry.RetryOnConflict` when updating resources the controller is also reconciling

**Assertions:**
- Use `Eventually`/`Consistently`, never `time.Sleep`
- Simulate external controller behavior (status updates) with helper functions

## Development Workflow

1. **Analyze** - understand the task, identify affected controllers/types, search for reusable patterns
2. **Test first** - write the test before implementation code
3. **Implement** - minimal, focused changes following existing project patterns
4. **Verify** - run selective tests, then `make precommit` before committing

## Pull requests

Before you write a PR title or description, read `.github/PULL_REQUEST_TEMPLATE.md`. Its comments are the rules: follow them, then delete every one. `.github/PR_DESCRIPTION_EXAMPLES.md` shows the right size.

## Controller-Runtime Patterns

### Reconcile Loop

- Reconcile must be **idempotent** - same input run N times produces the same result
- Propagate `context.Context` via function arguments, avoid `context.Background()`
- Handle `NotFound` as success for deleted objects
- Use `Patch` with `MergeFrom` for updates to reduce conflicts
- Return errors for failures (controller-runtime handles backoff). `Requeue: true` only for async work in progress, `RequeueAfter` only for wall-clock delays
- Short-circuit when no-op - already-compliant objects should reconcile with no API calls

### Status and Conditions

- `spec` is user-owned (desired state), `status` is controller-owned (observed state) - never write both in a single API call
- Always include `observedGeneration` in conditions - without it, `Ready: True` may reflect a previous spec generation
- Use conditions (not phase fields) with CamelCase types and positive polarity: `Ready`, `Available`, etc.
- Keep condition logic in `*_lifecycle.go` files with typed `Mark*` helpers (`MarkReady()`, `MarkNotReady(reason, msg)`) for transitions - never manipulate condition slices directly. See `local_model_cache_lifecycle.go` as the reference pattern
- Guard status writes with deep-equal checks - skip when nothing changed to avoid watch churn and infinite reconcile loops
- `reason` is CamelCase and part of the API contract. `lastTransitionTime` updates only when `status` (True/False/Unknown) changes, not on reason/message changes
- For composite conditions, surface the first failing sub-condition's Reason/Message in the parent so users don't have to inspect each one individually
- Use `ClearCondition()` to remove conditions that no longer apply rather than leaving stale values

### Watches and Caching

- Use predicates to drop irrelevant events early
- Prefer `Owns()` and targeted watches over broad `Watches()`
- Add field indexers to avoid expensive list+filter patterns
- Use `APIReader` only when cache staleness causes real correctness issues
- Cached client has no read-your-write consistency - writes hit the API server but are not instantly visible in cache
