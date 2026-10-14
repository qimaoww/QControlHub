#!/bin/sh
# Opt-in live integration: actual package repositories and upstream services.
# Run on a Docker host; no dependencies are preinstalled in the test nodes.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
container="qch-ipquality-clean-$$"
cleanup() { docker rm -f "$container" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT INT TERM
cd "$root"
CGO_ENABLED=0 go build -buildvcs=false -o "$work/qagent" ./cmd/agent
for image in ${QCH_IPQUALITY_TEST_IMAGES:-debian:bookworm-slim ubuntu:24.04 alpine:3.23}; do
  printf '%s\n' "Live IPQuality: $image"
  docker run -d --name "$container" --mount "type=bind,src=$work/qagent,dst=/qagent,readonly" "$image" sh -c 'sleep 36000' >/dev/null
  for phase in first repeat; do
    if ! timeout 660 docker exec "$container" /qagent ip-quality >"$work/$phase.json" 2>"$work/$phase.log"; then
      cat "$work/$phase.log" >&2
      exit 1
    fi
    python3 - "$work/$phase.json" <<'PY'
import json,sys
with open(sys.argv[1]) as report_file:
    result=json.load(report_file)
assert 1 <= len(result['reports']) <= 2
assert len(result['reports_text']) == len(result['reports'])
print('Validated %d report(s)' % len(result['reports']))
PY
  done
  grep -q '需要补齐' "$work/first.log"
  grep -q '安装依赖：完成并重新验证' "$work/first.log"
  grep -q '全部可执行，复用已有依赖' "$work/repeat.log"
  if grep -q '安装依赖：' "$work/repeat.log"; then
    printf '%s\n' 'Repeated detection unexpectedly installed dependencies' >&2
    exit 1
  fi
  docker rm -f "$container" >/dev/null
done
