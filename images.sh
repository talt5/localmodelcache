#!/bin/bash
# Auto-generated from images.env - DO NOT EDIT MANUALLY

# Image URL to use all building/pushing image targets
export TAG="${TAG:-latest}"
export STORAGE_INIT_IMG="${STORAGE_INIT_IMG:-storage-initializer}"
export LOCALMODEL_CONTROLLER_IMG="${LOCALMODEL_CONTROLLER_IMG:-localmodelcache-controller}"
export LOCALMODEL_AGENT_IMG="${LOCALMODEL_AGENT_IMG:-localmodelcache-node-agent}"
export KERNELCACHE_AGENT_IMG="${KERNELCACHE_AGENT_IMG:-localmodelcache-kernelcache-agent}"
export MCV_IMG="${MCV_IMG:-localmodelcache-mcv}"

# CI mode: export all variables to GITHUB_ENV
if [[ "${1:-}" == "--ci" ]]; then
  for var in TAG STORAGE_INIT_IMG LOCALMODEL_CONTROLLER_IMG LOCALMODEL_AGENT_IMG KERNELCACHE_AGENT_IMG MCV_IMG; do
    echo "${var}=${!var}" >> $GITHUB_ENV
  done
  echo "✅ Exported LocalModelCache image variables to GITHUB_ENV"
fi
