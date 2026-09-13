#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-quick-start-ready.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

# shellcheck source=../quick-start.sh
source "$repo_root/deploy/quick-start.sh"

fail() {
    printf '%s\n' "quick-start readiness regression: $1" >&2
    exit 1
}

# wait_ready reads the wall clock with `date +%s`. The fake clock below returns
# the value stored in the file and advances it by one second per read, so a
# deadline that passes between two reads is reproduced deterministically instead
# of depending on CI scheduling.
clock_file="$test_root/clock"
date() {
    case "${1:-}" in
        +%s)
            local value
            read -r value < "$clock_file"
            printf '%s\n' "$((value + 1))" > "$clock_file"
            printf '%s\n' "$value"
            ;;
        *)
            command date "$@"
            ;;
    esac
}
# Keep the retry loop fast without changing how often it consults the clock.
sleep() { :; }

probe_attempts=0
probe_limit=10
curl() {
    probe_attempts=$((probe_attempts + 1))
    [ "$probe_attempts" -le "$probe_limit" ] ||
        fail "readiness probe ignored the deadline after $probe_limit attempts"
    [ "$probe_attempts" -ge "${CURL_SUCCEEDS_AT:-1}" ]
}

# A healthy deployment must still be probed once when the deadline is already
# reached at the first loop test. quick-start-update.sh runs with
# READY_TIMEOUT=1, which is short enough for the crossing to happen in CI.
printf '1000\n' > "$clock_file"
probe_attempts=0
CURL_SUCCEEDS_AT=1
if ! wait_ready "http://127.0.0.1:18081/healthz" 1; then
    fail 'a healthy endpoint was rejected before the first probe'
fi
[ "$probe_attempts" -eq 1 ] || fail "expected exactly one probe, saw $probe_attempts"

# An unhealthy deployment must stop once the deadline is reached. The probe
# limit turns a missing deadline check into a fast failure instead of a hang.
printf '2000\n' > "$clock_file"
probe_attempts=0
CURL_SUCCEEDS_AT=99
if wait_ready "http://127.0.0.1:18081/readyz" 1; then
    fail 'an unhealthy endpoint was reported ready'
fi
[ "$probe_attempts" -eq 1 ] || fail "expected a bounded single probe, saw $probe_attempts"

# While the deadline has not passed, failures must be retried until the
# endpoint answers.
printf '3000\n' > "$clock_file"
probe_attempts=0
CURL_SUCCEEDS_AT=3
if ! wait_ready "http://127.0.0.1:18081/readyz" 5; then
    fail 'the retry loop gave up before the deadline'
fi
[ "$probe_attempts" -eq 3 ] || fail "expected three probes before success, saw $probe_attempts"

printf '%s\n' 'quick-start readiness probe regression passed'
