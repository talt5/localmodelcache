# Common chart sources

`make generate-chart-manifests` builds the `kserve-localmodel-resources` chart from these
files. Edit them here, never the generated files in the chart.

| File | Becomes |
| --- | --- |
| `common-sections.yaml` | The `kserve.version`, `kserve.storage`, `kserve.localmodel` and `kserve.kernelcache` values. |
| `kserve-localmodel-resources-specific.yaml` | The controller and node agent values and the `openshift` values. |
| `_utils.tpl`, `_resources.tpl` | The chart's helper templates, copied into `templates/` by `make sync-helm-common-helpers` and `make sync-helm-multi-resource-helpers`. |

The script merges the two values files with `yq` into
`charts/kserve-localmodel-resources/values.yaml`, then builds `files/resources.yaml` from
`config/overlays/charts-localmodel` with kustomize. The chart renders `files/resources.yaml`
with the `files/*-patch.yaml` overlays on top. Finally helm-docs rewrites each chart's
README from its values.

Document a value with a `# --` comment above it so helm-docs picks it up.
