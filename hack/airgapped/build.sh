#!/bin/bash
# Build the air-gapped bundle: every image the charts and cert-manager use (linux/amd64), the
# charts, the cert-manager manifest, a values file, a sample and a loader for an internal registry.
# Usage: hack/airgapped/build.sh <output directory>
#
# The versions come from deps.env. A new image in the charts makes verify.py fail until it is
# added here.
set -euo pipefail
[ $# -eq 1 ] || { echo "usage: $0 <output directory>" >&2; exit 2; }
root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$1"
out=$(cd "$1" && pwd)
cd "$root"
# shellcheck disable=SC1091
. ./deps.env
version=$LOCALMODELCACHE_VERSION
platform=linux/amd64
name=localmodelcache-airgapped-$version-linux-amd64
bundle=$out/localmodelcache-$version
[ ! -e "$bundle" ] || { echo "$bundle already exists; move it away first" >&2; exit 1; }
for tool in docker helm python3 curl; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

# Registries and package mirrors drop connections; a layer that was fetched is not fetched again.
retry() {
  local attempt
  for attempt in 1 2 3; do
    "$@" && return
    echo "attempt $attempt failed: $*" >&2
    sleep 5
  done
  return 1
}

mkdir -p "$bundle/images" "$bundle/charts"

add() { # <image>: save it and list it
  local image=$1 tar
  tar=$(basename "$image" | tr : _).tar
  docker save --platform "$platform" "$image" -o "$bundle/images/$tar"
  echo "$tar $image" >>"$bundle/images.txt"
}

build() { # <repository>:<tag> <docker build arguments>
  local image=ghcr.io/talt5/localmodelcache-$1
  shift
  retry docker build --platform "$platform" --provenance=false -t "$image" "$@"
  add "$image"
}

pull() { # <image>
  retry docker pull --platform "$platform" "$1"
  add "$1"
}

build "controller:$version" -f localmodel.Dockerfile .
build "node-agent:$version" -f localmodel-agent.Dockerfile .
build "kernelcache-agent:$version" -f kernelcache-agent.Dockerfile .
build "storage-initializer:$version" -f python/storage-initializer.Dockerfile python
build "mcv:$version-minimal" --target mcv-minimal -f kernelcache/mcv/mcv.Dockerfile .
build "mcv:$version-cuda" --target mcv-cuda -f kernelcache/mcv/mcv.Dockerfile .
pull "registry.access.redhat.com/ubi9/ubi-minimal:$UBI_MINIMAL_VERSION"
for component in controller cainjector webhook acmesolver; do
  pull "quay.io/jetstack/cert-manager-$component:$CERT_MANAGER_VERSION"
done

retry curl -fsSL -o "$bundle/cert-manager-$CERT_MANAGER_VERSION.yaml" \
  "https://github.com/cert-manager/cert-manager/releases/download/$CERT_MANAGER_VERSION/cert-manager.yaml"
helm package charts/localmodelcache-crd charts/localmodelcache -d "$bundle/charts"

for file in README.md values-airgapped.yaml load-images.sh; do
  sed -e "s|@VERSION@|$version|g" -e "s|@CERT_MANAGER_VERSION@|$CERT_MANAGER_VERSION|g" \
    -e "s|@UBI_MINIMAL_VERSION@|$UBI_MINIMAL_VERSION|g" "hack/airgapped/$file" >"$bundle/$file"
done
chmod +x "$bundle/load-images.sh"
cp LICENSE NOTICE "$bundle/"
cp docs/upgrade.md "$bundle/UPGRADE.md"
# OpenShift keeps the models under /var.
sed -e 's|path: /mnt/models|path: /var/mnt/models|' -e '/# On OpenShift use \/var\/mnt\/models/d' \
  docs/samples/vllm-node-local.yaml >"$bundle/sample-vllm-node-local.yaml"

python3 hack/airgapped/verify.py "$bundle"

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
cd "$bundle"
# shellcheck disable=SC2094 # find leaves SHA256SUMS out
find . -type f ! -name SHA256SUMS | sed 's|^\./||' | LC_ALL=C sort |
  while read -r file; do sha256 "$file"; done >SHA256SUMS
cd "$out"
COPYFILE_DISABLE=1 tar czf "$name.tar.gz" "$(basename "$bundle")"
sha256 "$name.tar.gz" >"$name.tar.gz.sha256"
echo "$out/$name.tar.gz"
