#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
container="qch-managed-tls-$$"
image="qch-managed-tls-$$"
cleanup() {
 docker rm -f "$container" >/dev/null 2>&1 || true
 docker image rm "$image" >/dev/null 2>&1 || true
 rm -rf "$work"
}
trap cleanup EXIT INT TERM
cd "$root"
CGO_ENABLED=0 go build -buildvcs=false -o "$work/qagent" ./cmd/agent
CGO_ENABLED=0 go test -buildvcs=false -c -o "$work/tls.test" ./internal/agent
python3 - "$work/qagent.service" <<'PY'
from pathlib import Path
import sys
unit = Path('deploy/systemd/qagent.service').read_text()
unit = unit.replace('ExecStart=/usr/local/bin/qagent', 'ExecStart=/usr/local/lib/qagent/tls.test -test.run=^TestManagedTLSSystemdSandbox$ -test.count=1 -test.v')
unit = unit.replace('Restart=on-failure', 'Restart=no')
unit += '\n[Service]\nEnvironment=QCH_TEST_MANAGED_TLS_SYSTEMD=1\n'
Path(sys.argv[1]).write_text(unit)
PY
docker build -t "$image" -f deploy/tests/Dockerfile.managed-tls "$work"
docker run -d --name "$container" --privileged --cgroupns=private --tmpfs /run --tmpfs /run/lock --tmpfs /tmp "$image" >/dev/null
ready=false
for attempt in $(seq 1 60); do
 if docker exec "$container" systemctl list-units >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
[ "$ready" = true ] || { docker logs "$container" >&2; exit 1; }
docker exec "$container" systemctl start qagent.service
finished=false
for attempt in $(seq 1 300); do
 state=$(docker exec "$container" systemctl show qagent.service -p ActiveState --value)
 case "$state" in inactive|failed) finished=true; break ;; esac
 sleep 1
done
docker exec "$container" journalctl -u qagent.service --no-pager
[ "$finished" = true ]
[ "$(docker exec "$container" systemctl show qagent.service -p ExecMainStatus --value)" = 0 ]
docker exec "$container" journalctl -u qagent.service --no-pager | grep -q 'managed TLS installed, validated, reloaded'
