#!/bin/sh
# Real systemd + package acquisition regression, with no detection-service calls.
# All system/package writes occur inside disposable nodes.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
container="qch-ipquality-systemd-$$"
image="qch-ipquality-systemd-$$"
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker image rm "$image" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT INT TERM
cd "$root"
CGO_ENABLED=0 go build -buildvcs=false -o "$work/qagent" ./cmd/agent
CGO_ENABLED=0 go test -buildvcs=false -c -o "$work/ip-quality.test" ./internal/agent
for base in ${QCH_IPQUALITY_SYSTEMD_IMAGES:-debian:bookworm-slim debian:trixie-slim}; do
  printf '%s\n' "IPQuality package sandbox: $base"
  python3 - "$base" "$work/qagent.service" <<'PY'
from pathlib import Path
import sys
unit = Path('deploy/systemd/qagent.service').read_text()
unit = unit.replace('ExecStart=/usr/local/bin/qagent', 'ExecStart=/usr/local/lib/qagent/ip-quality.test -test.run=^TestIPQualityDependencyHelperSystemdSandbox$ -test.count=1 -test.v')
unit = unit.replace('Restart=on-failure', 'Restart=no')
unit += '\n[Service]\nEnvironment=QCH_TEST_IPQUALITY_SYSTEMD_SANDBOX=1\n'
if sys.argv[1] == 'debian:trixie-slim':
    unit += 'Environment=QCH_TEST_IPQUALITY_LEGACY_UID_FAILURE=1\n'
Path(sys.argv[2]).write_text(unit)
PY
  docker build --build-arg "IPQUALITY_TEST_BASE=$base" -f deploy/tests/Dockerfile.ip-quality-systemd -t "$image" "$work"
  # Private cgroup namespace; no host service/package directories are mounted.
  docker run -d --name "$container" --privileged --cgroupns=private --tmpfs /run --tmpfs /run/lock --tmpfs /tmp "$image" >/dev/null
  ready=false
  for attempt in $(seq 1 60); do
    if docker exec "$container" systemctl list-units >/dev/null 2>&1; then ready=true; break; fi
    sleep 1
  done
  if [ "$ready" != true ]; then docker logs "$container" >&2; exit 1; fi
  docker exec "$container" systemctl start qagent.service
  finished=false
  for attempt in $(seq 1 420); do
    state=$(docker exec "$container" systemctl show qagent.service -p ActiveState --value)
    case "$state" in inactive|failed) finished=true; break ;; esac
    sleep 1
  done
  docker exec "$container" journalctl -u qagent.service --no-pager
  [ "$finished" = true ] || { printf '%s\n' 'package sandbox test timed out' >&2; exit 1; }
  [ "$(docker exec "$container" systemctl show qagent.service -p ExecMainStatus --value)" = 0 ]
  docker exec "$container" journalctl -u qagent.service --no-pager | grep -q 'repeat preparation passed'
  docker rm -f "$container" >/dev/null
  docker image rm "$image" >/dev/null
done
