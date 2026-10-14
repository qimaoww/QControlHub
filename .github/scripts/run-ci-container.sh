#!/bin/sh
set -eu
state=$(mktemp -d)
cleanup() {
  if [ -s "$state/id" ]; then
    container=$(cat "$state/id")
    docker stop --time 2 "$container" >/dev/null 2>&1 || true
    docker rm --force "$container" >/dev/null 2>&1 || true
  fi
  rm -rf "$state"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
docker run --cidfile "$state/id" "$@"
