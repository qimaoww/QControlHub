#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-quick-start-update.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

# shellcheck source=../quick-start.sh
source "$repo_root/deploy/quick-start.sh"

WORK_DIR="$test_root/deployment"
ENV_FILE="$WORK_DIR/.env"
EXTERNAL_COMPOSE_FILE="$WORK_DIR/docker-compose.external.yml"
SECRET_COMPOSE_FILE="$WORK_DIR/docker-compose.secrets.yml"
COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE")
READY_TIMEOUT=1
MODE=external
mkdir -p "$WORK_DIR"

write_fixture() {
    cat > "$ENV_FILE" <<'ENV'
# Preserve comments, ordering, credentials, database name and SSL query exactly.
QCH_DATABASE_URL=postgres://remote-user:p%40ss@db.example.test:6543/original-db?sslmode=verify-full&application_name=qcontrolhub
QCH_ADMIN_TOKEN=original-administrator-token-value
QCH_ADMIN_TOKEN_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
QCH_CONFIG_ENCRYPTION_KEY=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=older-a,older-b
QCH_ALLOW_INSECURE_DATABASE=false
QCH_BIND_ADDRESS=127.0.0.1
QCH_PORT=18081
QCH_DOCKER_NETWORK=
ENV
    chmod 600 "$ENV_FILE"
    cat > "$EXTERNAL_COMPOSE_FILE" <<'YAML'
name: qcontrolhub
services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:latest
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL}
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY}
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:latest
YAML
    cp "$ENV_FILE" "$test_root/expected.env"
    cp "$EXTERNAL_COMPOSE_FILE" "$test_root/expected-compose.yml"
}

docker() {
    printf '%s\n' "$*" >> "$QCH_UPDATE_DOCKER_LOG"
    case " $* " in
        *" compose "*" ps -q control-plane "*) printf '%s\n' control-container ;;
        *" compose "*" ps -q qcontrol-web "*) printf '%s\n' web-container ;;
        *" inspect "*"{{.Image}}"*" control-container "*) printf '%s\n' sha256:old-control ;;
        *" inspect "*"{{.Config.Image}}"*" control-container "*) printf '%s\n' ghcr.io/qimaoww/qcontrol-plane:latest ;;
        *" inspect "*"{{.Image}}"*" web-container "*) printf '%s\n' sha256:old-web ;;
        *" inspect "*"{{.Config.Image}}"*" web-container "*) printf '%s\n' ghcr.io/qimaoww/qcontrol-web:latest ;;
        *" pull ghcr.io/qimaoww/qcontrol-web:latest "*) [ "${QCH_UPDATE_FAIL_PULL:-false}" = false ] ;;
        *" compose "*" up -d --force-recreate "*)
            case " $* " in
                *" --no-build "*) return 0 ;;
                *) [ "${QCH_UPDATE_FAIL_UP:-false}" = false ] ;;
            esac
            ;;
        *) return 0 ;;
    esac
}

curl() {
    printf '%s\n' "$*" >> "$QCH_UPDATE_CURL_LOG"
    return 0
}

export QCH_UPDATE_DOCKER_LOG="$test_root/docker-success.log"
export QCH_UPDATE_CURL_LOG="$test_root/curl-success.log"
export QCH_UPDATE_FAIL_UP=false
export QCH_UPDATE_FAIL_PULL=false
write_fixture
validate_external_update_env
update_external_services

cmp -s "$test_root/expected.env" "$ENV_FILE" || {
    printf '%s\n' 'quick-start update regression: successful update changed .env' >&2
    exit 1
}
grep -Fq 'image: ghcr.io/qimaoww/qcontrol-plane:latest' "$EXTERNAL_COMPOSE_FILE"
grep -Fq 'image: ghcr.io/qimaoww/qcontrol-web:latest' "$EXTERNAL_COMPOSE_FILE"
pull_control_line="$(grep -nF 'pull ghcr.io/qimaoww/qcontrol-plane:latest' "$QCH_UPDATE_DOCKER_LOG" | tail -n 1 | cut -d: -f1)"
pull_web_line="$(grep -nF 'pull ghcr.io/qimaoww/qcontrol-web:latest' "$QCH_UPDATE_DOCKER_LOG" | tail -n 1 | cut -d: -f1)"
config_line="$(grep -nF 'config --quiet' "$QCH_UPDATE_DOCKER_LOG" | tail -n 1 | cut -d: -f1)"
up_line="$(grep -nF 'up -d --force-recreate' "$QCH_UPDATE_DOCKER_LOG" | tail -n 1 | cut -d: -f1)"
[ "$pull_control_line" -lt "$pull_web_line" ] && [ "$pull_web_line" -lt "$config_line" ] && [ "$config_line" -lt "$up_line" ] || {
    printf '%s\n' 'quick-start update regression: pull/config/up order changed' >&2
    exit 1
}
[ "$(grep -Fc '/healthz' "$QCH_UPDATE_CURL_LOG")" -eq 2 ]
[ "$(grep -Fc '/readyz' "$QCH_UPDATE_CURL_LOG")" -eq 2 ]

export QCH_UPDATE_DOCKER_LOG="$test_root/docker-failure.log"
export QCH_UPDATE_CURL_LOG="$test_root/curl-failure.log"
export QCH_UPDATE_FAIL_UP=true
write_fixture
if (update_external_services) >"$test_root/update-failure.out" 2>&1; then
    printf '%s\n' 'quick-start update regression: simulated recreate failure succeeded' >&2
    exit 1
fi
cmp -s "$test_root/expected.env" "$ENV_FILE" || {
    printf '%s\n' 'quick-start update regression: rollback did not restore .env exactly' >&2
    exit 1
}
cmp -s "$test_root/expected-compose.yml" "$EXTERNAL_COMPOSE_FILE" || {
    printf '%s\n' 'quick-start update regression: rollback did not restore old Compose' >&2
    exit 1
}
grep -Fq 'image tag sha256:old-control ghcr.io/qimaoww/qcontrol-plane:latest' "$QCH_UPDATE_DOCKER_LOG"
grep -Fq 'image tag sha256:old-web ghcr.io/qimaoww/qcontrol-web:latest' "$QCH_UPDATE_DOCKER_LOG"
grep -Fq -- '--no-build' "$QCH_UPDATE_DOCKER_LOG"
grep -Fq '已恢复旧配置，原版本容器继续运行' "$test_root/update-failure.out"
if find "$WORK_DIR" -maxdepth 1 -name '.qcontrolhub-update.*' -print -quit | grep -q .; then
    printf '%s\n' 'quick-start update regression: completed rollback left sensitive backups' >&2
    exit 1
fi

# A pull failure happens before Compose touches the running containers. The
# old config and image tags are restored without recreating either service.
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-pull-failure.log"
export QCH_UPDATE_CURL_LOG="$test_root/curl-pull-failure.log"
export QCH_UPDATE_FAIL_UP=false
export QCH_UPDATE_FAIL_PULL=true
write_fixture
if (update_external_services) >"$test_root/pull-failure.out" 2>&1; then
    printf '%s\n' 'quick-start update regression: simulated pull failure succeeded' >&2
    exit 1
fi
cmp -s "$test_root/expected.env" "$ENV_FILE"
cmp -s "$test_root/expected-compose.yml" "$EXTERNAL_COMPOSE_FILE"
if grep -Fq 'up -d --force-recreate' "$QCH_UPDATE_DOCKER_LOG"; then
    printf '%s\n' 'quick-start update regression: pull failure unnecessarily recreated containers' >&2
    exit 1
fi
grep -Fq 'image tag sha256:old-control ghcr.io/qimaoww/qcontrol-plane:latest' "$QCH_UPDATE_DOCKER_LOG"
grep -Fq 'image tag sha256:old-web ghcr.io/qimaoww/qcontrol-web:latest' "$QCH_UPDATE_DOCKER_LOG"
grep -Fq '已恢复旧配置，原版本容器继续运行' "$test_root/pull-failure.out"

printf '%s\n' 'quick-start external update rollback regression passed'
