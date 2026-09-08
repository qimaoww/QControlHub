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

write_test_secret() {
    local file="$1" value="$2"
    mkdir -p "$(dirname "$file")"
    chmod 700 "$(dirname "$file")"
    printf '%s\n' "$value" > "$file"
    chmod 600 "$file"
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

# External PostgreSQL reconfiguration keeps the six deployment-critical values
# unchanged and stores the encryption key directly for the external Compose.
ENV_FILE="$test_root/external.env"
EXTERNAL_COMPOSE_FILE="$test_root/external-compose.yml"
SECRET_COMPOSE_FILE="$test_root/secrets-compose.yml"
configure_secret_paths "$test_root/external-secrets"
DATABASE_URL="postgresql://user:pass@db.example.test:5432/qcontrolhub?sslmode=verify-full"
ADMIN_TOKEN=""
DOCKER_NETWORK=""
FORCE=false
legacy_admin="$(printf 'l%.0s' {1..32})"
legacy_key="$(printf 'k%.0s' {1..32})"
update_env_file \
    "QCH_DATABASE_URL=$DATABASE_URL" \
    "QCH_ADMIN_TOKEN=$legacy_admin" \
    "QCH_ADMIN_TOKEN_SHA256=$(sha256_hex "$legacy_admin")" \
    "QCH_CONFIG_ENCRYPTION_KEY=$legacy_key" \
    "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=external-old" \
    "QCH_ALLOW_INSECURE_DATABASE=false"
external_before="$(<"$ENV_FILE")"
prepare_external_env
for key in QCH_DATABASE_URL QCH_ADMIN_TOKEN QCH_ADMIN_TOKEN_SHA256 QCH_CONFIG_ENCRYPTION_KEY QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS QCH_ALLOW_INSECURE_DATABASE; do
    assert_equal "preserved external $key" "$(printf '%s\n' "$external_before" | awk -F= -v key="$key" '$1 == key { print substr($0, length(key) + 2); exit }')" "$(read_env_key "$key")"
done
assert_equal "direct config key" "$legacy_key" "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"

# A partially edited environment must not silently accept credentials whose
# raw token and digest disagree.
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
grep -Fq 'image: ghcr.io/qimaoww/qcontrol-plane:latest' "$EXTERNAL_COMPOSE_FILE"
grep -Fq 'image: ghcr.io/qimaoww/qcontrol-web:latest' "$EXTERNAL_COMPOSE_FILE"
grep -Fq 'QCH_DATABASE_URL: ${QCH_DATABASE_URL:?QCH_DATABASE_URL required}' "$EXTERNAL_COMPOSE_FILE"
grep -Fq 'QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:?QCH_CONFIG_ENCRYPTION_KEY required}' "$EXTERNAL_COMPOSE_FILE"
grep -Fq 'ipv4_address: ${QCH_WEB_PROXY_ADDRESS:-172.30.254.2}' "$EXTERNAL_COMPOSE_FILE"
if grep -Eq '^[[:space:]]{2}postgres:|postgres-data|depends_on:[[:space:]]*postgres|127\.0\.0\.1:5432|QCH_IMAGE_TAG|^[[:space:]]+build:' "$EXTERNAL_COMPOSE_FILE"; then
    printf '%s\n' 'quick-start regression: external Compose contains a local database or mutable image source' >&2
    exit 1
fi
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    docker compose -p qcontrolhub --env-file "$ENV_FILE" -f "$EXTERNAL_COMPOSE_FILE" config --quiet
fi

# An explicitly selected existing network is used as an external network; no
# site-specific network name is baked into the generated Compose.
update_env_file "QCH_DOCKER_NETWORK=shared-edge"
write_external_compose
grep -Fq 'name: ${QCH_DOCKER_NETWORK:?custom Docker network required}' "$EXTERNAL_COMPOSE_FILE"
grep -Fq 'external: true' "$EXTERNAL_COMPOSE_FILE"
if grep -Fq 'ipv4_address:' "$EXTERNAL_COMPOSE_FILE" || grep -Fq '1panel-network' "$EXTERNAL_COMPOSE_FILE"; then
    printf '%s\n' 'quick-start regression: custom network inherited default or site-specific topology' >&2
    exit 1
fi
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    docker compose -p qcontrolhub --env-file "$ENV_FILE" -f "$EXTERNAL_COMPOSE_FILE" config --quiet
fi

validate_database_url 'postgres://user:pass@db.example.test/qcontrolhub'
validate_external_update_env

# External deployments created by the previous installer keep their blank
# key fields and continue using the existing read-only secret-file override.
ENV_FILE="$test_root/external-secret.env"
SECRET_COMPOSE_FILE="$test_root/external-secret-compose.yml"
configure_secret_paths "$test_root/external-existing-secrets"
write_test_secret "$CONFIG_KEY_FILE" "$legacy_key"
write_test_secret "$PREVIOUS_CONFIG_KEYS_FILE" "external-old"
update_env_file \
    "QCH_DATABASE_URL=postgresql://remote.example.test/qcontrolhub?sslmode=verify-full" \
    "QCH_ADMIN_TOKEN=" \
    "QCH_ADMIN_TOKEN_SHA256=$(sha256_hex "$legacy_admin")" \
    "QCH_CONFIG_ENCRYPTION_KEY=" \
    "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=" \
    "QCH_ALLOW_INSECURE_DATABASE=false" \
    "QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE=$CONFIG_KEY_FILE" \
    "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE=$PREVIOUS_CONFIG_KEYS_FILE"
write_secret_compose_override
legacy_secret_env_before="$(<"$ENV_FILE")"
validate_external_update_env
write_external_compose
grep -Fq 'QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}' "$EXTERNAL_COMPOSE_FILE"
ACTION="update"
MODE="external"
configure_compose_args
assert_equal "legacy external secret override count" "2" "$(printf '%s\n' "${COMPOSE_ARGS[@]}" | grep -Fc -- '-f')"
assert_equal "legacy external env unchanged" "$legacy_secret_env_before" "$(<"$ENV_FILE")"

ACTION="update"
MODE=""
detect_existing_mode
assert_equal "detected external deployment" "external" "$MODE"

ENV_FILE="$test_root/detect-invalid.env"
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
grep -Fq -- "compose -p qcontrolhub" "$compose_environment_log" || { printf '%s\n' 'quick-start regression: stable Compose project name was not passed' >&2; exit 1; }
grep -Fq -- "--project-directory $WORK_DIR" "$compose_environment_log" || { printf '%s\n' 'quick-start regression: selected project directory was not passed to Compose' >&2; exit 1; }
unset -f docker

# The saved work directory must be resolved before the interactive action menu
# runs, so option 4 displays the persisted path instead of the runtime script
# directory.
(
    menu_runtime_dir="$test_root/menu-runtime"
    menu_work_dir="$test_root/menu-work"
    mkdir -p "$menu_runtime_dir" "$menu_work_dir"
    REPO_ROOT="$menu_runtime_dir"
    WORK_DIR="$REPO_ROOT"
    QCH_INSTALL_DIR="$menu_work_dir"
    XDG_CONFIG_HOME="$test_root/menu-config"
    ACTION=""
    MODE=""
    choose_action() {
        assert_equal "menu displays persisted work directory" "$menu_work_dir" "$WORK_DIR"
        ACTION="uninstall"
    }
    prepare_action_and_work_dir
    assert_equal "menu action selection" "uninstall" "$ACTION"
    grep -Fxq -- "$menu_work_dir" "$XDG_CONFIG_HOME/qcontrolhub/install-dir"
)

compose_log="$test_root/uninstall-compose.log"
compose() { printf '%s\n' "$*" > "$compose_log"; }
MODE="external"
uninstall_services > "$test_root/uninstall-output.txt"
assert_equal "safe uninstall compose arguments" "down --remove-orphans" "$(<"$compose_log")"
grep -Fq '外部 PostgreSQL 数据未被修改' "$test_root/uninstall-output.txt"
update_env_file "QCH_PORT=18080"
assert_equal "custom panel URL" "http://127.0.0.1:18080" "$(local_panel_url)"
update_env_file "QCH_BIND_ADDRESS=192.0.2.10"
assert_equal "specific bind panel URL" "http://192.0.2.10:18080" "$(local_panel_url)"
update_env_file "QCH_BIND_ADDRESS=::"
assert_equal "IPv6 bind panel URL" "http://[::1]:18080" "$(local_panel_url)"
for required_menu_text in '安装 / 重新配置' '更新现有部署' '卸载服务（保留配置、密钥和数据库卷）'; do
    grep -Fq "$required_menu_text" "$repo_root/deploy/quick-start.sh"
done

printf '%s\n' 'quick-start external configuration regression passed'
