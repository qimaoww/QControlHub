#!/bin/sh
set -eu
state=$(mktemp -d)
name=qch-ci-${state##*/}
client_pid=
cleanup() {
  if [ -n "$client_pid" ]; then kill -TERM "$client_pid" 2>/dev/null || true; fi
  # The name is known even if cancellation arrives during image pulling or
  # container creation, before Docker has written the CID file.
  docker stop --time 2 "$name" >/dev/null 2>&1 || true
  docker rm --force "$name" >/dev/null 2>&1 || true
  if [ -n "$client_pid" ]; then wait "$client_pid" 2>/dev/null || true; fi
  rm -rf "$state"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
docker run --name "$name" --cidfile "$state/id" "$@" &
client_pid=$!
wait "$client_pid"
