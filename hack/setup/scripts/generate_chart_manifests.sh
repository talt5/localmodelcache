#!/bin/bash

# Regenerates the kserve-localmodel-resources chart's base manifests and values.yaml.
# The chart renders files/resources.yaml with the files/*-patch.yaml overlays on top.

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

source "${SCRIPT_DIR}/../common.sh"

kustomize build "${REPO_ROOT}/config/overlays/charts-localmodel" > "${REPO_ROOT}/charts/kserve-localmodel-resources/files/resources.yaml"
echo "✅ Built kserve-localmodel-resources manifests"

yq eval-all '. as $item ireduce ({}; . * $item)' \
  "${REPO_ROOT}/charts/_common/common-sections.yaml" \
  "${REPO_ROOT}/charts/_common/kserve-localmodel-resources-specific.yaml" \
  > "${REPO_ROOT}/charts/kserve-localmodel-resources/values.yaml"
echo "✅ Generated kserve-localmodel-resources values.yaml"
