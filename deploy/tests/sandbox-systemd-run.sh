#!/bin/sh
# Isolated filesystem sandbox only: run the bounded helper without a systemd
# daemon. Runtime service identities are covered by the permission tests.
set -eu
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
[ "${1:-}" = "--" ] || exit 64
shift
exec "$@"
