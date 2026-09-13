#!/usr/bin/env bash
# Run the repository checks with the Go test suite split into parallel shards.
#
# Migration and HOT-page regressions observe PostgreSQL's cluster-wide xmin
# horizon, so packages that connect to PostgreSQL must stay sequential with
# each other ("go test -p 1"). Those packages share one shard and one database,
# the remaining packages run in parallel shards with databases of their own,
# and the non-Go checks run as separate tasks instead of extending the critical
# path.
#
# Environment:
#   QCH_TEST_DATABASE_URL  base PostgreSQL URL; shard databases add a suffix
#   QCH_TEST_SHARDS        number of parallel Go test shards (default 4)
#
# Options:
#   --checks COMMAND       non-Go checks; repeatable, each runs as one task
#   --exclude REGEX        extended regex of packages to keep out of the shards
#   --extra COMMAND        extra task that receives its own database
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
        'usage: run-ci-tests.sh [--checks COMMAND]... [--exclude REGEX] [--extra COMMAND] [--dry-run]' \
        '' \
        'Runs the non-Go checks and the Go test suite in parallel shards.' \
        'QCH_TEST_DATABASE_URL is required; QCH_TEST_SHARDS defaults to 4.'
}

base_url="${QCH_TEST_DATABASE_URL:-}"
shard_count="${QCH_TEST_SHARDS:-4}"
exclude=""
extra=""
dry_run=false
declare -a check_commands=()

while [ "$#" -gt 0 ]; do
    case "$1" in
        --checks)
            [ "$#" -ge 2 ] || { printf '%s\n' '--checks requires a command' >&2; exit 2; }
            check_commands+=("$2")
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

# One database per shard keeps the sequential package requirement without
# letting the shards share PostgreSQL's cluster-wide xmin horizon.
ensure_database() {
    local name="$1"
    if [ "$(psql "$base_url" -tAc "SELECT 1 FROM pg_database WHERE datname = '$name'")" = "1" ]; then
        return 0
    fi
    psql "$base_url" -v ON_ERROR_STOP=1 -q -c "CREATE DATABASE \"$name\"" >/dev/null
    printf 'created test database %s\n' "$name"
}

packages="$(go list ./...)"
if [ -n "$exclude" ]; then
    packages="$(printf '%s\n' "$packages" | grep -Ev "$exclude" || true)"
fi
[ -n "$packages" ] || { printf '%s\n' 'no Go packages selected for the test shards' >&2; exit 1; }

# Greedy longest-processing-time packing on the number of test functions:
# counting tests tracks runtime closely enough to balance the shards without
# keeping a hardcoded package list in sync with the repository.
weighted=""
database_packages=""
database_tests=0
while IFS= read -r package; do
    [ -n "$package" ] || continue
    directory="$(go list -f '{{.Dir}}' "$package")"
    test_files=("$directory"/*_test.go)
    tests=0
    if [ -e "${test_files[0]}" ]; then
        tests="$({ grep -hcE '^func (Test|Fuzz|Benchmark)' "${test_files[@]}" || true; } | awk '{ total += $1 } END { print total + 0 }')"
    fi
    # The xmin horizon that gates HOT-page pruning is cluster-wide: a
    # transaction in another database still pins the pages measured by the
    # store performance invariants. Every package that connects to PostgreSQL
    # therefore shares one shard and stays sequential with the other database
    # users, while packages that never connect stay fully parallel.
    if grep -qs 'QCH_TEST_DATABASE_URL' "$directory"/*.go; then
        database_packages="${database_packages}${database_packages:+ }${package}"
        database_tests=$((database_tests + tests))
    else
        weighted="${weighted}${tests}"$'\t'"${package}"$'\n'
    fi
done <<<"$packages"

declare -a shard_loads=() shard_packages=()
for ((index = 0; index < shard_count; index++)); do
    shard_loads[index]=0
    shard_packages[index]=""
done
# Seed the shared database shard before packing the independent packages.
if [ -n "$database_packages" ]; then
    shard_loads[0]=$database_tests
    shard_packages[0]="$database_packages"
fi

testless=0
while IFS=$'\t' read -r tests package; do
    [ -n "$package" ] || continue
    if [ "$tests" -eq 0 ]; then
        slot=$((testless % shard_count))
        testless=$((testless + 1))
    else
        slot=0
        for ((index = 1; index < shard_count; index++)); do
            if [ "${shard_loads[index]}" -lt "${shard_loads[slot]}" ]; then
                slot=$index
            fi
        done
        shard_loads[slot]=$((shard_loads[slot] + tests))
    fi
    shard_packages[slot]="${shard_packages[slot]}${shard_packages[slot]:+ }${package}"
done < <(printf '%s' "$weighted" | sort -rn)

# Every selected package must land in exactly one shard.
selected="$(printf '%s\n' "$packages" | sort)"
assigned="$(printf '%s\n' "${shard_packages[@]}" | tr ' ' '\n' | grep -v '^$' | sort)"
if [ "$assigned" != "$selected" ]; then
    printf '%s\n' 'shard assignment did not cover every selected package' >&2
    diff <(printf '%s\n' "$selected") <(printf '%s\n' "$assigned") >&2 || true
    exit 1
fi

if [ "$dry_run" = true ]; then
    for ((index = 1; index <= shard_count; index++)); do
        [ -n "${shard_packages[index - 1]}" ] || continue
        printf 'shard %s/%s: %s\n' "$index" "$shard_count" "${shard_packages[index - 1]}"
    done
    [ -z "$extra" ] || printf 'extra: %s\n' "$extra"
    exit 0
fi

declare -a task_pids=()
start_task() {
    local name="$1"
    local command="$2"
    printf '%s\n' "-> $name: $command"
    ( bash -c "$command" 2>&1 | sed "s|^|[$name] |" ) &
    task_pids+=("$!")
}

for ((index = 1; index <= shard_count; index++)); do
    list="${shard_packages[index - 1]}"
    [ -n "$list" ] || continue
    name="go-tests-$index-of-$shard_count"
    ensure_database "${base_name}_shard${index}"
    printf '%s\n' "-> $name: $list"
    (
        QCH_TEST_DATABASE_URL="$(database_url "shard${index}")" go test -p 1 $list 2>&1 |
            sed "s|^|[$name] |"
    ) &
    task_pids+=("$!")
done

if [ -n "$extra" ]; then
    ensure_database "${base_name}_extra"
    name="go-tests-extra"
    (
        QCH_TEST_DATABASE_URL="$(database_url extra)" bash -c "$extra" 2>&1 |
            sed "s|^|[$name] |"
    ) &
    task_pids+=("$!")
fi

check_index=0
for command in "${check_commands[@]}"; do
    check_index=$((check_index + 1))
    start_task "checks-$check_index" "$command"
done

status=0
for pid in "${task_pids[@]}"; do
    if ! wait "$pid"; then
        status=1
    fi
done
exit "$status"
