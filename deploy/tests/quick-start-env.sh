#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-quick-start.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

# Source the real preparation functions. No Docker command is available or
# needed; the test exercises only .env and generated Compose state.
# shellcheck source=../quick-start.sh
source "$repo_root/deploy/quick-start.sh"

assert_equal() {
    local label="$1" expected="$2" actual="$3"
    if [ "$expected" != "$actual" ]; then
        printf '%s\n' "quick-start regression: $label mismatch" >&2
        exit 1
    fi
}

assert_file_mode() {
    local file="$1"
    [ "$(stat -c '%a' "$file")" = "600" ] || {
        printf '%s\n' "quick-start regression: backup mode is not 0600" >&2
        exit 1
    }
}

ENV_FILE="$test_root/bundled.env"
EXTERNAL_COMPOSE_FILE="$test_root/external-compose.yml"
ADMIN_TOKEN="$(printf 'a%.0s' {1..32})"
DATABASE_URL=""
FORCE=false
prepare_bundled_env
bundled_first_current="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
assert_equal "first previous ring" "" "$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"

update_env_file "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=older-key,oldest-key"
bundled_before_force="$(<"$ENV_FILE")"
FORCE=true
prepare_bundled_env
bundled_second_current="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
assert_equal "bundled force rotates current key" false "$( [ "$bundled_first_current" = "$bundled_second_current" ] && printf true || printf false )"
assert_equal "bundled force prepends previous keys" "$bundled_first_current,older-key,oldest-key" "$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"
bundled_backup="$(find "$test_root" -maxdepth 1 -name 'bundled.env.bak.*' -print | head -n 1)"
[ -n "$bundled_backup" ] || { printf '%s\n' 'quick-start regression: bundled backup missing' >&2; exit 1; }
assert_file_mode "$bundled_backup"
assert_equal "bundled backup content" "$bundled_before_force" "$(<"$bundled_backup")"

bundled_before_second_force="$(<"$ENV_FILE")"
prepare_bundled_env
bundled_third_current="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
assert_equal "second bundled force prepends newest key" "$bundled_second_current,$bundled_first_current,older-key,oldest-key" "$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"
assert_equal "second bundled force rotates again" false "$( [ "$bundled_second_current" = "$bundled_third_current" ] && printf true || printf false )"
bundled_backup_count="$(find "$test_root" -maxdepth 1 -name 'bundled.env.bak.*' -type f | wc -l | tr -d ' ')"
[ "$bundled_backup_count" -ge 2 ] || { printf '%s\n' 'quick-start regression: repeated force backup missing' >&2; exit 1; }
latest_bundled_backup="$(find "$test_root" -maxdepth 1 -name 'bundled.env.bak.*' -type f -printf '%T@ %p\n' | sort -n | tail -n 1 | cut -d' ' -f2-)"
assert_file_mode "$latest_bundled_backup"
assert_equal "second bundled backup content" "$bundled_before_second_force" "$(<"$latest_bundled_backup")"

ENV_FILE="$test_root/external.env"
DATABASE_URL="postgresql://user:pass@db.example.test:5432/qcontrolhub?sslmode=verify-full"
FORCE=false
prepare_external_env
external_first_current="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
update_env_file "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=external-old"
external_before_reuse="$(<"$ENV_FILE")"
prepare_external_env
assert_equal "external non-force current key" "$external_first_current" "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
assert_equal "external non-force previous ring" "external-old" "$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"
FORCE=true
prepare_external_env
assert_equal "external force prepends previous key" "$external_first_current,external-old" "$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"
external_backup="$(find "$test_root" -maxdepth 1 -name 'external.env.bak.*' -print | head -n 1)"
[ -n "$external_backup" ] || { printf '%s\n' 'quick-start regression: external backup missing' >&2; exit 1; }
assert_file_mode "$external_backup"
assert_equal "external backup content" "$external_before_reuse" "$(<"$external_backup")"
write_external_compose
grep -Fq 'QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}' "$EXTERNAL_COMPOSE_FILE"

printf '%s\n' 'quick-start environment rotation regression passed'
