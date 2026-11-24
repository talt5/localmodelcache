#!/bin/bash
# Push every image in images/ to an internal registry. Run `podman login` (or docker login) first.
# Usage: ./load-images.sh registry.example.com/localmodelcache
# ENGINE=docker uses docker when podman is installed as well.
set -euo pipefail
[ $# -eq 1 ] || { echo "usage: $0 <registry>/<project>" >&2; exit 2; }
registry=${1%/}
cd "$(dirname "$0")"
engine=${ENGINE:-$(command -v podman || command -v docker)} || { echo "podman or docker is required" >&2; exit 1; }
if command -v sha256sum >/dev/null; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi
while read -r tar source; do
  target=$registry/${source##*/}
  "$engine" load -i "images/$tar"
  "$engine" tag "$source" "$target"
  "$engine" push "$target"
done < images.txt
cat <<MSG

All images pushed. Next:
  sed "s|REGISTRY|$registry|g" values-airgapped.yaml > values.yaml
  sed "s|quay.io/jetstack|$registry|g" cert-manager-@CERT_MANAGER_VERSION@.yaml > cert-manager.yaml
MSG
