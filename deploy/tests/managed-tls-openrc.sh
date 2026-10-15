#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
container="qch-managed-tls-openrc-$$"
cleanup() { docker rm -f "$container" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT INT TERM
cd "$root"
CGO_ENABLED=0 go build -buildvcs=false -o "$work/qagent" ./cmd/agent
CGO_ENABLED=0 go test -buildvcs=false -c -o "$work/tls.test" ./internal/agent
docker run --name "$container" --init --cap-add NET_ADMIN --cap-add SYS_PTRACE \
 -v "$work:/fixtures:ro" -v "$root:/src:ro" alpine:3.23 sh -ec '
 apk add --no-cache openrc libcap >/dev/null
 QCH_SERVICE_MANAGER=openrc sh /src/deploy/bootstrap-core-services.sh --prepare-agent
 mkdir -p /usr/local/lib/qagent /etc/qagent/tls-frontends/requests /run/openrc
 chmod 700 /etc/qagent/tls-frontends/requests
 cp /fixtures/qagent /fixtures/tls.test /usr/local/lib/qagent/
 echo default > /run/openrc/softlevel
 cp /src/deploy/openrc/qagent /etc/init.d/qagent
 sed -i "s|command=\"/usr/local/lib/qagent/qagent\"|command=\"/usr/local/lib/qagent/tls.test\"|" /etc/init.d/qagent
 printf "\nexport QCH_TEST_MANAGED_TLS_SYSTEMD=1\nexport QCH_TEST_TLS_MANAGER=openrc\ncommand_args=\"-test.run=^TestManagedTLSSystemdSandbox$ -test.count=1 -test.v\"\n" >> /etc/init.d/qagent
 chmod +x /etc/init.d/qagent
 rc-service qagent start
 for attempt in $(seq 1 240); do
  if grep -q "PASS\|FAIL" /var/log/qagent/qagent.log 2>/dev/null; then break; fi
  sleep 1
 done
 rc-service qagent stop || true
 cat /var/log/qagent/qagent.log
 ! grep -q FAIL /var/log/qagent/qagent.log
 grep -q "managed TLS installed, validated, reloaded" /var/log/qagent/qagent.log
 '
