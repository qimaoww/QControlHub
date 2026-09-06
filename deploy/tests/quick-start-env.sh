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
grep -Fq 'QCH_DISABLE_DATABASE_MIGRATIONS: "true"' "$EXTERNAL_COMPOSE_FILE"
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
assert_equal "fixed external deployment mode" "external" "$MODE"

compose_environment_log="$test_root/compose-environment.log"
docker() { printf 'source=%s\nargs=%s\n' "$QCH_SOURCE_DIR" "$*" > "$compose_environment_log"; }
WORK_DIR="$test_root/separate config"
COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE")
compose config
grep -Fxq "source=$repo_root" "$compose_environment_log" || { printf '%s\n' 'quick-start regression: source directory was not exported to Compose' >&2; exit 1; }
grep -Fq -- "compose -p qcontrolhub" "$compose_environment_log" || { printf '%s\n' 'quick-start regression: stable Compose project name was not passed' >&2; exit 1; }
grep -Fq -- "--project-directory $WORK_DIR" "$compose_environment_log" || { printf '%s\n' 'quick-start regression: selected project directory was not passed to Compose' >&2; exit 1; }
unset -f docker

compose_log="$test_root/uninstall-compose.log"
compose() { printf '%s\n' "$*" > "$compose_log"; }
MODE="external"
uninstall_services > "$test_root/uninstall-output.txt"
assert_equal "safe uninstall compose arguments" "down --remove-orphans" "$(<"$compose_log")"
grep -Fq '外部 PostgreSQL 数据未被修改' "$test_root/uninstall-output.txt"
update_env_file "QCH_PORT=18080"
assert_equal "custom panel URL" "http://127.0.0.1:18080" "$(local_panel_url)"
for required_menu_text in '安装 / 重新配置外部 PostgreSQL 部署' '更新现有部署（保留原配置，失败自动回滚）' '卸载应用容器（保留配置、密钥和外部数据库）'; do
    grep -Fq "$required_menu_text" "$repo_root/deploy/quick-start.sh"
done
if bash "$repo_root/deploy/quick-start.sh" -m bundled -o install >/dev/null 2>&1; then
    printf '%s\n' 'quick-start regression: production installer still accepted bundled PostgreSQL' >&2
    exit 1
fi

printf '%s\n' 'quick-start external configuration regression passed'
