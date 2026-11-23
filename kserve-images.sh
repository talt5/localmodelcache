#!/bin/bash
# Auto-generated from kserve-images.env - DO NOT EDIT MANUALLY

# Image URL to use all building/pushing image targets
export TAG="${TAG:-latest}"
export STORAGE_INIT_IMG="${STORAGE_INIT_IMG:-storage-initializer}"
export LOCALMODEL_CONTROLLER_IMG="${LOCALMODEL_CONTROLLER_IMG:-kserve-localmodel-controller}"
export LOCALMODEL_AGENT_IMG="${LOCALMODEL_AGENT_IMG:-kserve-localmodelnode-agent}"
export KERNELCACHE_AGENT_IMG="${KERNELCACHE_AGENT_IMG:-kserve-kernelcachenode-agent}"
export MCV_IMG="${MCV_IMG:-kserve-mcv}"

# CI mode: export all variables to GITHUB_ENV
if [[ "${1:-}" == "--ci" ]]; then
  for var in TAG STORAGE_INIT_IMG LOCALMODEL_CONTROLLER_IMG LOCALMODEL_AGENT_IMG KERNELCACHE_AGENT_IMG MCV_IMG; do
    echo "${var}=${!var}" >> $GITHUB_ENV
  done
  echo "✅ Exported KServe image variables to GITHUB_ENV"
fi
