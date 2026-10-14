#!/usr/bin/env bash
# Run the repository checks with the Go test suite split into parallel shards.
#
# Migration and HOT-page regressions observe PostgreSQL's cluster-wide xmin
# horizon, so packages that connect to PostgreSQL must stay sequential with
# each other ("go test -p 1"). Those packages share one shard and one database,
# the remaining packages run in parallel shards with databases of their own,
# and the non-Go checks run as separate tasks. Optional after-checks wait for
# every parallel task before running serially, so duplicate browser suites do
# not compete with each other or the remaining parallel work.
#
# Environment:
#   QCH_TEST_DATABASE_URL  base PostgreSQL URL; shard databases add a suffix
#   QCH_TEST_SHARDS        number of parallel Go test shards (default 4)
#
# Options:
#   --checks COMMAND       non-Go checks; repeatable, each runs as one task
#   --after-checks COMMAND serial checks after all tasks finish; repeatable
#   --exclude REGEX        extended regex of packages to keep out of the shards
#   --extra COMMAND        extra task; waits for the database shard by default
#   --extra-independent    assert that the extra task never uses PostgreSQL
#   --dry-run              print the shard assignment and exit
#   -h, --help             show this help
set -euo pipefail

# CI containers run as a different user than the checkout owner, so git refuses
# to report status inside the workspace. Test binaries do not need VCS
# metadata, and "go list" fails package loading without it, so disable stamping
# explicitly instead of depending on the checkout's ownership.
GOFLAGS="${GOFLAGS:+${GOFLAGS} }-buildvcs=false"
export GOFLAGS

usage() {
    printf '%s\n' \
        'usage: run-ci-tests.sh [--checks COMMAND]... [--after-checks COMMAND]... [--exclude REGEX] [--extra COMMAND] [--dry-run]' \
        '' \
        'Runs the Go suite and checks in parallel, then runs every after-check serially.' \
        'After-checks run even if an earlier task failed; failures determine the final exit.' \
        'QCH_TEST_DATABASE_URL is required; QCH_TEST_SHARDS defaults to 4.'
}

base_url="${QCH_TEST_DATABASE_URL:-}"
shard_count="${QCH_TEST_SHARDS:-4}"
exclude=""
extra=""
extra_independent=false
script_directory="$(cd "$(dirname "$0")" && pwd)"
summary_script="$script_directory/go-test-summary.mjs"
prefix_script="$script_directory/prefix-ci-output.mjs"
dry_run=false
declare -a check_commands=()
declare -a after_check_commands=()

while [ "$#" -gt 0 ]; do
    case "$1" in
        --checks)
            [ "$#" -ge 2 ] || { printf '%s\n' '--checks requires a command' >&2; exit 2; }
            check_commands+=("$2")
            shift 2
            ;;
        --after-checks)
            [ "$#" -ge 2 ] || { printf '%s\n' '--after-checks requires a command' >&2; exit 2; }
            after_check_commands+=("$2")
            shift 2
            ;;
        --exclude)
            [ "$#" -ge 2 ] || { printf '%s\n' '--exclude requires a pattern' >&2; exit 2; }
            exclude="$2"
            shift 2
            ;;
        --extra)
            [ "$#" -ge 2 ] || { printf '%s\n' '--extra requires a command' >&2; exit 2; }
            extra="$2"
            shift 2
            ;;
        --extra-independent)
            extra_independent=true
            shift
            ;;
        --dry-run)
            dry_run=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            printf 'unknown argument: %s\n' "$1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

case "$shard_count" in
    ''|*[!0-9]*) printf '%s\n' 'QCH_TEST_SHARDS must be a positive integer' >&2; exit 2 ;;
esac
[ "$shard_count" -ge 1 ] || { printf '%s\n' 'QCH_TEST_SHARDS must be a positive integer' >&2; exit 2; }

if [ "$dry_run" = false ]; then
    [ -n "$base_url" ] || { printf '%s\n' 'QCH_TEST_DATABASE_URL is required' >&2; exit 2; }
    command -v psql >/dev/null 2>&1 || {
        printf '%s\n' 'psql is required to create one database per test shard' >&2
        exit 1
    }
fi

base_name=""
base_prefix=""
base_query=""
if [ -n "$base_url" ]; then
    base_without_query="${base_url%%\?*}"
    base_name="${base_without_query##*/}"
    case "$base_name" in
        ''|*[!A-Za-z0-9_]*) printf '%s\n' 'cannot derive a database name from QCH_TEST_DATABASE_URL' >&2; exit 2 ;;
    esac
    base_prefix="${base_without_query%/*}/"
    case "$base_url" in
        *\?*) base_query="?${base_url#*\?}" ;;
    esac
fi

database_url() {
    printf '%s' "${base_prefix}${base_name}_$1${base_query}"
}

# Separate databases isolate data only. The planner puts all PostgreSQL
# users in one sequential shard because xmin is cluster-wide.
ensure_database() {
    local name="$1"
    if [ "$(psql "$base_url" -tAc "SELECT 1 FROM pg_database WHERE datname = '$name'")" = "1" ]; then
        return 0
    fi
    psql "$base_url" -v ON_ERROR_STOP=1 -q -c "CREATE DATABASE \"$name\"" >/dev/null
    printf 'created test database %s\n' "$name"
}

# Runtime weights come from real CI package timings; unknown packages use a
# conservative test-count estimate. The planner loads all metadata once.
assignment="$(node "$(dirname "$0")/plan-ci-tests.mjs" "$shard_count" "$exclude")"
declare -a shard_loads=() shard_packages=()
while IFS=$'\t' read -r weight list; do
    shard_loads+=("$weight")
    shard_packages+=("$list")
done <<<"$assignment"

if [ "$dry_run" = true ]; then
    for ((index = 1; index <= shard_count; index++)); do
        [ -n "${shard_packages[index - 1]}" ] || continue
        printf 'shard %s/%s: %s\n' "$index" "$shard_count" "${shard_packages[index - 1]}"
        printf 'estimated seconds: %s\n' "${shard_loads[index - 1]}"
    done
    [ -z "$extra" ] || printf 'extra: %s\n' "$extra"
    for command in "${after_check_commands[@]}"; do
        printf 'after-checks: %s\n' "$command"
    done
    exit 0
fi

declare -a task_pids=()
cleanup() {
    trap - EXIT INT TERM
    for pid in "${task_pids[@]}"; do kill -TERM -- "-$pid" 2>/dev/null || true; done
    if [ "${#task_pids[@]}" -gt 0 ]; then
        sleep 1
        for pid in "${task_pids[@]}"; do kill -KILL -- "-$pid" 2>/dev/null || true; done
    fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
start_task() {
    local name="$1"
    local command="$2"
    printf '%s\n' "-> $name: $command"
    setsid bash -o pipefail -c 'bash -c "$1" 2>&1 | node "$3" "$2"' _ "$command" "$name" "$prefix_script" &
    task_pids+=("$!")
}

for ((index = 1; index <= shard_count; index++)); do
    list="${shard_packages[index - 1]}"
    [ -n "$list" ] || continue
    name="go-tests-$index-of-$shard_count"
    ensure_database "${base_name}_shard${index}"
    printf '%s\n' "-> $name: $list"
    setsid bash -o pipefail -c '
        QCH_TEST_DATABASE_URL="$1" go test -json -count=1 -timeout 20m -p 1 -skip "^TestAgentBrowserRuntimeSmoke$" ${@:4} 2>&1 |
            node "$3" "$2"
    ' _ "$(database_url "shard${index}")" "$name" "$summary_script" $list &
    task_pids+=("$!")
done


check_index=0
for command in "${check_commands[@]}"; do
    check_index=$((check_index + 1))
    start_task "checks-$check_index" "$command"
done

status=0
if [ -n "$extra" ] && [ "$extra_independent" = false ]; then
    # A different database still shares xmin: unknown extra tasks wait for
    # the database shard. Only explicitly database-free extras may overlap.
    if ! wait "${task_pids[0]}"; then status=1; fi
fi
if [ -n "$extra" ]; then
    ensure_database "${base_name}_extra"
    name="go-tests-extra"
    setsid bash -o pipefail -c '
        QCH_TEST_DATABASE_URL="$1" bash -c "$3" 2>&1 | node "$4" "$2"
    ' _ "$(database_url extra)" "$name" "$extra" "$prefix_script" &
    task_pids+=("$!")
fi

for pid in "${task_pids[@]}"; do
    if ! wait "$pid"; then
        status=1
    fi
done
after_check_index=0
for command in "${after_check_commands[@]}"; do
    after_check_index=$((after_check_index + 1))
    name="after-checks-$after_check_index"
    start_task "$name" "$command"
    if ! wait "${task_pids[${#task_pids[@]} - 1]}"; then
        status=1
    fi
done
exit "$status"
