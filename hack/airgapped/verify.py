#!/usr/bin/env python3
"""Check an air-gapped bundle before it is archived.

Fails when an image that the charts or cert-manager use is missing, when a tar is damaged, holds
another image than listed or is not linux/amd64, or when a template placeholder is left.
"""

import hashlib
import json
import pathlib
import re
import subprocess
import sys
import tarfile

REGISTRY = "registry.invalid/bundle"


def check_tar(path, image):
    """Return what is wrong with a tar written by docker save."""
    problems = []
    with tarfile.open(path) as tar:
        members = {member.name: member for member in tar.getmembers()}
        manifest = json.load(tar.extractfile("manifest.json"))
        if len(manifest) != 1 or manifest[0]["RepoTags"] != [image]:
            problems.append(
                f"holds {[entry['RepoTags'] for entry in manifest]}, not {image}"
            )
        for entry in manifest:
            config = json.load(tar.extractfile(entry["Config"]))
            if (config["os"], config["architecture"]) != ("linux", "amd64"):
                problems.append(f"is for {config['os']}/{config['architecture']}")
            for blob in [entry["Config"], *entry["Layers"]]:
                if blob not in members:
                    problems.append(f"lacks {blob}")
                    continue
                digest = re.fullmatch(r"blobs/sha256/([0-9a-f]{64})", blob)
                if digest:
                    actual = hashlib.sha256()
                    content = tar.extractfile(members[blob])
                    while chunk := content.read(1 << 20):
                        actual.update(chunk)
                    if actual.hexdigest() != digest[1]:
                        problems.append(f"has a damaged {blob}")
    return problems


def chart_images(bundle):
    """Return the images of the chart, with every feature on, under the bundle's values."""
    values = (
        bundle.joinpath("values-airgapped.yaml")
        .read_text()
        .replace("REGISTRY", REGISTRY)
    )
    (chart,) = [
        path for path in bundle.glob("charts/*.tgz") if "-crd-" not in path.name
    ]
    rendered = subprocess.run(
        [
            "helm",
            "template",
            "bundle",
            str(chart),
            "-f",
            "-",
            "--set",
            "localmodelcache.kernelcache.enabled=true",
        ],
        input=values,
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    # In Pods, in the settings ConfigMap, and wherever the values do not set the registry.
    named = set(
        re.findall(r'(?:[iI]mage|IMAGE)"?:\s*"?([^\s",{}]+[:@][^\s",{}]+)', rendered)
    )
    return named | set(re.findall(re.escape(REGISTRY) + r"/[\w./-]+:[\w.-]+", rendered))


def main(bundle):
    listed = dict(
        line.split() for line in bundle.joinpath("images.txt").read_text().splitlines()
    )
    problems = []
    for tar, image in listed.items():
        path = bundle / "images" / tar
        problems += [
            f"{tar} {problem}"
            for problem in (check_tar(path, image) if path.exists() else ["is missing"])
        ]
    problems += [
        f"{path.name} is not in images.txt"
        for path in bundle.glob("images/*")
        if path.name not in listed
    ]

    # load-images.sh pushes every image to <registry>/<last part of its name>.
    loaded = {f"{REGISTRY}/{image.rsplit('/', 1)[1]}" for image in listed.values()}
    used = chart_images(bundle)
    if not used:
        problems.append("found no image in the chart")
    problems += [
        f"the chart uses {image}, which is not in the bundle"
        for image in sorted(used - loaded)
    ]

    (manifest,) = bundle.glob("cert-manager-*.yaml")
    cert_manager = set(
        re.findall(r"quay\.io/jetstack/[\w.-]+:[\w.-]+", manifest.read_text())
    )
    if not cert_manager:
        problems.append("found no image in the cert-manager manifest")
    missing = sorted(cert_manager - set(listed.values()))
    problems += [
        f"cert-manager uses {image}, which is not in the bundle" for image in missing
    ]

    for name in ("README.md", "values-airgapped.yaml", "load-images.sh"):
        left = re.findall(r"@[A-Z_]+@", bundle.joinpath(name).read_text())
        problems += [f"{name} still has {placeholder}" for placeholder in left]

    for problem in problems:
        print(problem, file=sys.stderr)
    if problems:
        return 1
    print(
        f"{len(listed)} images, {len(used)} used by the chart: all present, intact and linux/amd64"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(pathlib.Path(sys.argv[1])))
