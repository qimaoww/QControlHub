#!/bin/sh
set -eu
# Fingerprint the actual floating base image, including libc and architecture.
# Pulling here is also timed as its own job step; later runs reuse that image.
image=${1:?usage: container-toolchain.sh IMAGE}
docker pull "$image" >/dev/null
identity=$(docker run --rm --entrypoint sh "$image" -ec '
  . /etc/os-release
  printf "%s-%s-%s-%s-%s\n" "$ID" "$VERSION_ID" "$(uname -m)" "$(go env GOVERSION)" "$(ldd --version 2>&1 | head -n 2)"+' | tr -cs 'A-Za-z0-9_.-' '-' | sed 's/-$//')
printf 'toolchain: %s\n' "$identity"
printf 'identity=%s\n' "$identity" >> "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"
