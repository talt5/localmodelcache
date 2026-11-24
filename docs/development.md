# Development

You need Go (the version in `go.mod`), GNU make 4 or later, and Helm. On macOS, also install GNU sed. The
Makefile prints how to set both up. `make help` lists the targets. The common ones:

| Target | Does |
| --- | --- |
| `make test` | Go unit and envtest suites. |
| `make test-focus PKG=… RUN=…` | One package or test, while you iterate. |
| `make precommit` | Formatting, lint, code generation and chart sync. Run it before committing. |
| `make manifests` | Regenerates CRDs, RBAC and deepcopy after API or RBAC marker changes. |
| `make docker-build docker-push` | Builds and pushes the controller and node agent images (`KO_DOCKER_REPO`, `TAG`). |
| `make deploy-helm` | Installs the charts from this checkout. |
| `make airgapped-bundle` | Builds the air-gapped bundle of this version. |

## Release

1. Set the version in `deps.env`, `charts/_common/common-sections.yaml` and the `Chart.yaml` files,
   run `make precommit`, and merge it to `master`.
2. Push the tag, such as `v0.2.0`, on that merge commit. The workflows publish the images and attach
   the air-gapped bundle to a draft release.
3. Check that every image the chart names can be pulled from GHCR without a login. A new package starts
   private.
4. Complete the notes and publish the release, which also publishes the charts.
