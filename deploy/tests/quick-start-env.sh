#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-quick-start.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

# Source the real preparation functions. No Docker command is available or
# needed; the test exercises only .env, secret files and generated Compose.
# shellcheck source=../quick-start.sh
source "$repo_root/deploy/quick-start.sh"

assert_equal() {
    local label="$1" expected="$2" actual="$3"
    if [ "$expected" != "$actual" ]; then
        printf '%s\n' "quick-start regression: $label mismatch" >&2
        printf '%s\n' "expected: $expected" >&2
        printf '%s\n' "actual:   $actual" >&2
        exit 1
    fi
}

assert_file_mode() {
    local file="$1" expected="$2"
    [ "$(stat -c '%a' "$file")" = "$expected" ] || {
        printf '%s\n' "quick-start regression: $file mode is not $expected" >&2
        exit 1
    }
}

assert_secret_source_mode() {
    local file="$1" mode
    mode="$(stat -c '%a' "$file")"
    case "$mode" in
        600|644) ;;
        *) printf '%s\n' "quick-start regression: $file mode is unsafe: $mode" >&2; exit 1 ;;
    esac
}

configure_secret_paths() {
    SECRET_DIR="$1"
    CONFIG_KEY_FILE="$SECRET_DIR/config-encryption-key"
    PREVIOUS_CONFIG_KEYS_FILE="$SECRET_DIR/config-encryption-previous-keys"
}

ENV_FILE="$test_root/bundled.env"
EXTERNAL_COMPOSE_FILE="$test_root/external-compose.yml"
SECRET_COMPOSE_FILE="$test_root/secrets-compose.yml"
configure_secret_paths "$test_root/bundled-secrets"
ADMIN_TOKEN="$(printf 'a%.0s' {1..32})"
DATABASE_URL=""
FORCE=false
prepare_bundled_env

expected_digest="$(sha256_hex "$ADMIN_TOKEN")"
assert_equal "persisted raw administrator token" "" "$(read_env_key QCH_ADMIN_TOKEN)"
assert_equal "administrator token digest" "$expected_digest" "$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
assert_equal "first token display" "$ADMIN_TOKEN" "$ADMIN_TOKEN_TO_DISPLAY"
assert_file_mode "$SECRET_DIR" 700
assert_secret_source_mode "$CONFIG_KEY_FILE"
assert_secret_source_mode "$PREVIOUS_CONFIG_KEYS_FILE"
bundled_first_current="$(read_secret_file "$CONFIG_KEY_FILE")"
assert_equal "plaintext config key in env" "" "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
assert_equal "first previous ring" "" "$(read_secret_file "$PREVIOUS_CONFIG_KEYS_FILE")"

write_secret_file "$PREVIOUS_CONFIG_KEYS_FILE" "older-key,oldest-key"
bundled_before_force="$(<"$ENV_FILE")"
ADMIN_TOKEN=""
FORCE=true
prepare_bundled_env
bundled_second_current="$(read_secret_file "$CONFIG_KEY_FILE")"
assert_equal "bundled force rotates current key" false "$( [ "$bundled_first_current" = "$bundled_second_current" ] && printf true || printf false )"
assert_equal "bundled force prepends previous keys" "$bundled_first_current,older-key,oldest-key" "$(read_secret_file "$PREVIOUS_CONFIG_KEYS_FILE")"
[ -n "$ADMIN_TOKEN_TO_DISPLAY" ] || { printf '%s\n' 'quick-start regression: rotated administrator token was not shown once' >&2; exit 1; }
assert_equal "rotated administrator digest" "$(sha256_hex "$ADMIN_TOKEN_TO_DISPLAY")" "$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
bundled_backup="$(find "$test_root" -maxdepth 1 -name 'bundled.env.bak.*' -print | head -n 1)"
[ -n "$bundled_backup" ] || { printf '%s\n' 'quick-start regression: bundled backup missing' >&2; exit 1; }
assert_file_mode "$bundled_backup" 600
assert_equal "bundled backup content" "$bundled_before_force" "$(<"$bundled_backup")"
config_backup="$(find "$SECRET_DIR" -maxdepth 1 -name 'config-encryption-key.bak.*' -print | head -n 1)"
[ -n "$config_backup" ] || { printf '%s\n' 'quick-start regression: config key backup missing' >&2; exit 1; }
assert_file_mode "$config_backup" 600

prepare_bundled_env
bundled_third_current="$(read_secret_file "$CONFIG_KEY_FILE")"
assert_equal "second bundled force prepends newest key" "$bundled_second_current,$bundled_first_current,older-key,oldest-key" "$(read_secret_file "$PREVIOUS_CONFIG_KEYS_FILE")"
assert_equal "second bundled force rotates again" false "$( [ "$bundled_second_current" = "$bundled_third_current" ] && printf true || printf false )"
secret_backup_count="$(find "$SECRET_DIR" -maxdepth 1 -name 'config-encryption-key.bak.*' -type f | wc -l | tr -d ' ')"
[ "$secret_backup_count" -ge 2 ] || { printf '%s\n' 'quick-start regression: repeated secret backup missing' >&2; exit 1; }

# A legacy plaintext .env is migrated in place: raw login/configuration
# secrets are blanked, the login digest is persisted, and key material moves
# under the host-private secret directory.
ENV_FILE="$test_root/external.env"
configure_secret_paths "$test_root/external-secrets"
DATABASE_URL="postgresql://user:pass@db.example.test:5432/qcontrolhub?sslmode=verify-full"
ADMIN_TOKEN=""
FORCE=false
legacy_admin="$(printf 'l%.0s' {1..32})"
legacy_key="$(printf 'k%.0s' {1..32})"
update_env_file \
    "QCH_ADMIN_TOKEN=$legacy_admin" \
    "QCH_CONFIG_ENCRYPTION_KEY=$legacy_key" \
    "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=external-old"
backup_env
legacy_backup="$(find "$test_root" -maxdepth 1 -name 'external.env.bak.*' -print | head -n 1)"
if grep -Fq "$legacy_admin" "$legacy_backup" || grep -Fq "$legacy_key" "$legacy_backup"; then
    printf '%s\n' 'quick-start regression: plaintext secret leaked into .env backup' >&2
    exit 1
fi
prepare_external_env
assert_equal "legacy token one-time display" "$legacy_admin" "$ADMIN_TOKEN_TO_DISPLAY"
assert_equal "migrated raw token" "" "$(read_env_key QCH_ADMIN_TOKEN)"
assert_equal "migrated token digest" "$(sha256_hex "$legacy_admin")" "$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
assert_equal "migrated config key env" "" "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
assert_equal "migrated config key file" "$legacy_key" "$(read_secret_file "$CONFIG_KEY_FILE")"
assert_equal "migrated previous keyring" "external-old" "$(read_secret_file "$PREVIOUS_CONFIG_KEYS_FILE")"
assert_equal "current key secret source" ".secrets/config-encryption-key" "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE)"
assert_equal "previous key secret source" ".secrets/config-encryption-previous-keys" "$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE)"

# A partially edited legacy environment must not silently prefer a digest that
# does not belong to the plaintext token being migrated.
ENV_FILE="$test_root/mismatched-admin.env"
update_env_file \
    "QCH_ADMIN_TOKEN=$legacy_admin" \
    "QCH_ADMIN_TOKEN_SHA256=$(sha256_hex "$(printf 'x%.0s' {1..32})")"
ADMIN_TOKEN=""
FORCE=false
if (prepare_admin_token >/dev/null 2>&1); then
    printf '%s\n' 'quick-start regression: mismatched legacy administrator credentials were accepted' >&2
    exit 1
fi
ENV_FILE="$test_root/external.env"

write_external_compose
write_secret_compose_override
grep -Fq 'QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:-}' "$EXTERNAL_COMPOSE_FILE"
if [ "$(grep -Fc 'context: ${QCH_SOURCE_DIR:-.}' "$EXTERNAL_COMPOSE_FILE")" -ne 2 ]; then
    printf '%s\n' 'quick-start regression: external local builds do not use the source directory' >&2
    exit 1
fi
grep -Fq 'QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:?administrator token digest required}' "$SECRET_COMPOSE_FILE"
grep -Fq 'target: /run/secrets/qch-config-encryption-key' "$SECRET_COMPOSE_FILE"
if grep -Fq "$legacy_admin" "$ENV_FILE" || grep -Fq "$legacy_key" "$ENV_FILE"; then
    printf '%s\n' 'quick-start regression: legacy plaintext secret remains in .env' >&2
    exit 1
fi

ACTION="update"
MODE=""
detect_existing_mode
assert_equal "detected external deployment" "external" "$MODE"

ENV_FILE="$test_root/detect-bundled.env"
EXTERNAL_COMPOSE_FILE="$test_root/missing-external-compose.yml"
update_env_file "POSTGRES_DB=qcontrolhub"
MODE=""
detect_existing_mode
assert_equal "detected bundled deployment" "bundled" "$MODE"

compose_environment_log="$test_root/compose-environment.log"
docker() { printf 'source=%s\nargs=%s\n' "$QCH_SOURCE_DIR" "$*" > "$compose_environment_log"; }
WORK_DIR="$test_root/separate config"
COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE")
compose config
grep -Fxq "source=$repo_root" "$compose_environment_log" || { printf '%s\n' 'quick-start regression: source directory was not exported to Compose' >&2; exit 1; }
grep -Fq -- "--project-directory $WORK_DIR" "$compose_environment_log" || { printf '%s\n' 'quick-start regression: selected project directory was not passed to Compose' >&2; exit 1; }
unset -f docker

compose_log="$test_root/uninstall-compose.log"
compose() { printf '%s\n' "$*" > "$compose_log"; }
MODE="bundled"
uninstall_services > "$test_root/uninstall-output.txt"
assert_equal "safe uninstall compose arguments" "down --remove-orphans" "$(<"$compose_log")"
grep -Fq '已保留数据：Docker PostgreSQL 命名卷' "$test_root/uninstall-output.txt"
update_env_file "QCH_PORT=18080"
assert_equal "custom panel URL" "http://127.0.0.1:18080" "$(local_panel_url)"
for required_menu_text in '安装 / 重新配置' '更新现有部署' '卸载服务（保留配置、密钥和数据库卷）'; do
    grep -Fq "$required_menu_text" "$repo_root/deploy/quick-start.sh"
done

printf '%s\n' 'quick-start secret persistence regression passed'
