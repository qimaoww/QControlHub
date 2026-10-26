#!/usr/bin/env bash
set -euo pipefail
export COLUMNS=72

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$repo_root/deploy/tests/image-registry-fixture.sh"
export QCH_IMAGE_TEST_PREFIX=QCH_UPDATE
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
    image: ghcr.io/qimaoww/qcontrol-plane:previous
    pull_policy: always
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL}
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY}
    volumes:
      - ./db-ca.pem:/run/secrets/db-ca.pem:ro
    networks:
      - site-network
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:previous
    networks:
      - site-network
networks:
  site-network:
    name: existing-site-network
    external: true
YAML
    cp "$ENV_FILE" "$test_root/expected.env"
    cp "$EXTERNAL_COMPOSE_FILE" "$test_root/expected-compose.yml"
}

docker() {
    printf '%s\n' "$*" >> "$QCH_UPDATE_DOCKER_LOG"
    case " $* " in
        *" buildx version "*) return 0 ;;
        *" buildx imagetools inspect "*) mock_registry "$@" ;;
        *" info --format "*) printf linux/amd64 ;;
        *" config --format json "*) mock_compose_images "$(bundled_image_tag 2>/dev/null || printf latest)" ;;
        *" image inspect --format {{json .}} "*"@sha256:"*) mock_local_image "${*: -1}" ;;
        *" inspect --format {{json .}} control-container "*) mock_current_image "sha256:$(printf '%064d' 1)" ghcr.io/qimaoww/qcontrol-plane:latest ;;
        *" inspect --format {{json .}} web-container "*) mock_current_image "sha256:$(printf '%064d' 2)" ghcr.io/qimaoww/qcontrol-web:latest ;;
        *" config --no-interpolate "*)
            # The separate real-Compose regression validates this merge.
            sed 's/:previous/:latest/g' "$EXTERNAL_COMPOSE_FILE"
            ;;
        *" compose "*" ps -q control-plane "*) printf '%s\n' control-container ;;
        *" compose "*" ps -q qcontrol-web "*) printf '%s\n' web-container ;;
        *" inspect "*"org.opencontainers.image.version"*" control-container "*) printf '%s\n' old-control-version ;;
        *" inspect "*"org.opencontainers.image.version"*" web-container "*) printf '%s\n' old-web-version ;;
        *" inspect "*"{{.Image}}"*" control-container "*) printf '%s\n' sha256:0000000000000000000000000000000000000000000000000000000000000001 ;;
        *" inspect "*"{{.Config.Image}}"*" control-container "*) printf '%s\n' ghcr.io/qimaoww/qcontrol-plane:latest ;;
        *" inspect "*"{{.Image}}"*" web-container "*) printf '%s\n' sha256:0000000000000000000000000000000000000000000000000000000000000002 ;;
        *" inspect "*"{{.Config.Image}}"*" web-container "*) printf '%s\n' ghcr.io/qimaoww/qcontrol-web:latest ;;
        *" inspect "*"sha256:0000000000000000000000000000000000000000000000000000000000000001 "*)
            case "$*" in
                *org.opencontainers.image.version*) printf '%s\n' old-control-version ;;
                *) printf '%s\n' sha256:0000000000000000000000000000000000000000000000000000000000000001 ;;
            esac
            ;;
        *" inspect "*"sha256:0000000000000000000000000000000000000000000000000000000000000002 "*)
            case "$*" in
                *org.opencontainers.image.version*) printf '%s\n' old-web-version ;;
                *) printf '%s\n' sha256:0000000000000000000000000000000000000000000000000000000000000002 ;;
            esac
            ;;
        *" inspect "*"sha256:0000000000000000000000000000000000000000000000000000000000000004 "*)
            case "$*" in
                *org.opencontainers.image.version*) printf '%s\n' new-control-version ;;
                *) printf '%s\n' sha256:0000000000000000000000000000000000000000000000000000000000000004 ;;
            esac
            ;;
        *" inspect "*"sha256:0000000000000000000000000000000000000000000000000000000000000005 "*)
            case "$*" in
                *org.opencontainers.image.version*) printf '%s\n' new-web-version ;;
                *) printf '%s\n' sha256:0000000000000000000000000000000000000000000000000000000000000005 ;;
            esac
            ;;
        *" inspect "*"ghcr.io/qimaoww/qcontrol-plane:latest "*)
            case "$*" in
                *org.opencontainers.image.version*) printf '%s\n' "${QCH_UPDATE_TARGET_CONTROL_VERSION:-new-control-version}" ;;
                *) printf '%s\n' "${QCH_UPDATE_TARGET_CONTROL_ID:-sha256:0000000000000000000000000000000000000000000000000000000000000004}" ;;
            esac
            ;;
        *" inspect "*"ghcr.io/qimaoww/qcontrol-web:latest "*)
            case "$*" in
                *org.opencontainers.image.version*) printf '%s\n' "${QCH_UPDATE_TARGET_WEB_VERSION:-new-web-version}" ;;
                *) printf '%s\n' "${QCH_UPDATE_TARGET_WEB_ID:-sha256:0000000000000000000000000000000000000000000000000000000000000005}" ;;
            esac
            ;;
        *" pull --platform "*)
            mock_assert_comparison || return 1
            [ "${QCH_UPDATE_FAIL_PULL:-false}" = false ] ;;
        *" compose "*" up -d --force-recreate "*)
            case " $* " in
                *"docker-compose.rollback.yml"*)
                    grep -Fq 'image: sha256:0000000000000000000000000000000000000000000000000000000000000001' "$UPDATE_BACKUP_DIR/docker-compose.rollback.yml"
                    grep -Fq 'image: sha256:0000000000000000000000000000000000000000000000000000000000000002' "$UPDATE_BACKUP_DIR/docker-compose.rollback.yml"
                    case " $* " in *" --pull never "*) ;; *) return 1 ;; esac
                    case " $* " in *" --no-deps control-plane "*|*" --no-deps qcontrol-web "*) ;; *) return 1 ;; esac
                    printf '%s\n' rollback > "$QCH_UPDATE_PHASE"
                    return 0
                    ;;
                *)
                    if [ "${QCH_MOCK_LOCAL_STORE:-}" = containerd ]; then
                        for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
                            [ "${UPDATE_IMAGE_CURRENT[$i]}" != "${UPDATE_IMAGE_TARGETS[$i]}" ] || continue
                            grep -Fq "image: ${UPDATE_IMAGE_PINNED[$i]##*@}" "$UPDATE_BACKUP_DIR/docker-compose.checked.yml" || return 1
                        done
                    fi
                    printf '%s\n' updated > "$QCH_UPDATE_PHASE"
                    [ "${QCH_UPDATE_FAIL_UP:-false}" = false ]
                    ;;
            esac
            ;;
        *" exec control-container wget "*)
            if [ "${QCH_UPDATE_FAIL_CONTROL_READY:-false}" = true ] && \
                [ "$(<"$QCH_UPDATE_PHASE")" = updated ]; then
                return 1
            fi
            return 0
            ;;
        *) return 0 ;;
    esac
}

curl() {
    printf '%s\n' "$*" >> "$QCH_UPDATE_CURL_LOG"
    if [ "${QCH_UPDATE_FAIL_HEALTH:-false}" = true ] && [ -f "$QCH_UPDATE_PHASE" ] && \
        [ "$(<"$QCH_UPDATE_PHASE")" = updated ]; then
        return 1
    fi
    return 0
}

export QCH_UPDATE_DOCKER_LOG="$test_root/docker-no-update.log"
export QCH_UPDATE_CURL_LOG="$test_root/curl-no-update.log"
export QCH_UPDATE_FAIL_UP=false
export QCH_UPDATE_FAIL_PULL=false
export QCH_UPDATE_PHASE="$test_root/phase"
export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000001
export QCH_UPDATE_TARGET_WEB_ID=sha256:0000000000000000000000000000000000000000000000000000000000000002
export QCH_UPDATE_TARGET_CONTROL_VERSION=old-control-version
export QCH_UPDATE_TARGET_WEB_VERSION=old-web-version
write_fixture
update_external_services > "$test_root/no-update.out"
cmp "$test_root/expected.env" "$ENV_FILE"
cmp "$test_root/expected-compose.yml" "$EXTERNAL_COMPOSE_FILE"
grep -Fq '当前镜像与目标版本一致，无需更新' "$test_root/no-update.out"
grep -Eq '控制面[[:space:]]+目标标签：latest' "$test_root/no-update.out"
grep -Eq '网页服务[[:space:]]+目标标签：latest' "$test_root/no-update.out"
if grep -Eq '(^pull |up -d)' "$QCH_UPDATE_DOCKER_LOG"; then
    printf '%s\n' 'quick-start update regression: unchanged images pulled or recreated containers' >&2
    exit 1
fi
if find "$WORK_DIR" -maxdepth 1 -name '.qcontrolhub-update.*' -print -quit | grep -q .; then
    printf '%s\n' 'quick-start update regression: no-op left update backups' >&2
    exit 1
fi

export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000004
export QCH_UPDATE_TARGET_WEB_ID=sha256:0000000000000000000000000000000000000000000000000000000000000005
export QCH_UPDATE_TARGET_CONTROL_VERSION=new-control-version
export QCH_UPDATE_TARGET_WEB_VERSION=new-web-version
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-success.log"
export QCH_UPDATE_CURL_LOG="$test_root/curl-success.log"
write_fixture
validate_external_update_env
export QCH_MOCK_OUTPUT="$test_root/update-success.out"
update_external_services > "$QCH_MOCK_OUTPUT"
unset QCH_MOCK_OUTPUT
grep -Fq old-control-version "$test_root/update-success.out"
grep -Fq new-control-version "$test_root/update-success.out"
grep -Fq old-web-version "$test_root/update-success.out"
grep -Fq new-web-version "$test_root/update-success.out"

cmp -s "$test_root/expected.env" "$ENV_FILE" || {
    printf '%s\n' 'quick-start update regression: successful update changed .env' >&2
    exit 1
}
grep -Fq 'image: ghcr.io/qimaoww/qcontrol-plane:latest' "$EXTERNAL_COMPOSE_FILE"
grep -Fq 'image: ghcr.io/qimaoww/qcontrol-web:latest' "$EXTERNAL_COMPOSE_FILE"
grep -Fq 'name: existing-site-network' "$EXTERNAL_COMPOSE_FILE"
grep -Fq './db-ca.pem:/run/secrets/db-ca.pem:ro' "$EXTERNAL_COMPOSE_FILE"
pull_control_line="$(grep -nF "pull --platform linux/amd64 $(mock_pinned_ref ghcr.io/qimaoww/qcontrol-plane:latest)" "$QCH_UPDATE_DOCKER_LOG" | tail -n 1 | cut -d: -f1)"
pull_web_line="$(grep -nF "pull --platform linux/amd64 $(mock_pinned_ref ghcr.io/qimaoww/qcontrol-web:latest)" "$QCH_UPDATE_DOCKER_LOG" | tail -n 1 | cut -d: -f1)"
config_line="$(grep -nF 'config --quiet' "$QCH_UPDATE_DOCKER_LOG" | tail -n 1 | cut -d: -f1)"
up_line="$(grep -nF 'up -d --force-recreate' "$QCH_UPDATE_DOCKER_LOG" | tail -n 1 | cut -d: -f1)"
check_line="$(grep -nF 'inspect --raw ghcr.io/qimaoww/qcontrol-web:latest' "$QCH_UPDATE_DOCKER_LOG" | cut -d: -f1)"
[ "$check_line" -lt "$pull_control_line" ] && [ "$pull_control_line" -lt "$pull_web_line" ] && [ "$pull_web_line" -lt "$config_line" ] && [ "$config_line" -lt "$up_line" ] || {
    printf '%s\n' 'quick-start update regression: pull/config/up order changed' >&2
    exit 1
}
[ "$(grep -Fc '/healthz' "$QCH_UPDATE_CURL_LOG")" -eq 2 ]
[ "$(grep -Fc '/readyz' "$QCH_UPDATE_CURL_LOG")" -eq 2 ]
# The web container must be created only after the control plane answered
# readiness, both during updates and when restoring the original images.
control_up="$(grep -n 'up .*--no-deps control-plane$' "$QCH_UPDATE_DOCKER_LOG" | cut -d: -f1)"
control_ready="$(grep -n 'exec control-container wget' "$QCH_UPDATE_DOCKER_LOG" | cut -d: -f1)"
web_up="$(grep -n 'up .*--no-deps qcontrol-web$' "$QCH_UPDATE_DOCKER_LOG" | cut -d: -f1)"
[ "$control_up" -lt "$control_ready" ] && [ "$control_ready" -lt "$web_up" ]

# Either application image changing is enough to update the deployment.
for changed_service in control web; do
    export QCH_UPDATE_DOCKER_LOG="$test_root/docker-$changed_service-only.log"
    export QCH_UPDATE_CURL_LOG="$test_root/curl-$changed_service-only.log"
    export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000001
    export QCH_UPDATE_TARGET_WEB_ID=sha256:0000000000000000000000000000000000000000000000000000000000000002
    export QCH_UPDATE_TARGET_CONTROL_VERSION=old-control-version
    export QCH_UPDATE_TARGET_WEB_VERSION=old-web-version
    if [ "$changed_service" = control ]; then
        export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000004
        export QCH_UPDATE_TARGET_CONTROL_VERSION=new-control-version
    else
        export QCH_UPDATE_TARGET_WEB_ID=sha256:0000000000000000000000000000000000000000000000000000000000000005
        export QCH_UPDATE_TARGET_WEB_VERSION=new-web-version
    fi
    write_fixture
    update_external_services > "$test_root/$changed_service-only.out"
    cmp "$test_root/expected.env" "$ENV_FILE"
    [ "$(grep -c '^pull ' "$QCH_UPDATE_DOCKER_LOG")" -eq 1 ]
    grep -Fq 'up -d --force-recreate' "$QCH_UPDATE_DOCKER_LOG" || {
        printf '%s\n' "quick-start update regression: $changed_service-only change skipped update" >&2
        exit 1
    }
done

# Matching display versions must not hide changed image content.
export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000004
export QCH_UPDATE_TARGET_WEB_ID=sha256:0000000000000000000000000000000000000000000000000000000000000002
export QCH_UPDATE_TARGET_CONTROL_VERSION=old-control-version
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-same-label.log"
write_fixture
update_external_services > "$test_root/same-label.out"
grep -Eq '控制面[[:space:]]+目标版本：old-control-version \[有更新\]' "$test_root/same-label.out"
[ "$(grep -c '^pull ' "$QCH_UPDATE_DOCKER_LOG")" -eq 1 ]

# An OCI index's attestations/other architectures do not change this platform.
export QCH_MOCK_REGISTRY_MODE=index
export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000001
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-index-noop.log"
write_fixture
update_external_services > "$test_root/index-noop.out"
grep -Fq '无需更新' "$test_root/index-noop.out"
if grep -Eq '(^pull | up -d)' "$QCH_UPDATE_DOCKER_LOG"; then exit 1; fi

# containerd's running index ID differs from both the config and the selected
# manifest. Compare the container's actual platform descriptor for a no-op.
export QCH_MOCK_CURRENT_STORE=containerd
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-containerd-noop.log"
write_fixture
update_external_services > "$test_root/containerd-noop.out"
grep -Fq '无需更新' "$test_root/containerd-noop.out"
if grep -Eq '(^pull | up -d)' "$QCH_UPDATE_DOCKER_LOG"; then exit 1; fi
unset QCH_MOCK_CURRENT_STORE

# After a child-manifest pull, the checked local manifest ID (not the remote
# config digest) must be the immutable ID passed to Compose.
export QCH_MOCK_LOCAL_STORE=containerd
export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000004
export QCH_UPDATE_TARGET_WEB_ID=sha256:0000000000000000000000000000000000000000000000000000000000000005
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-containerd-update.log"
write_fixture
update_external_services > "$test_root/containerd-update.out"
cmp "$test_root/expected.env" "$ENV_FILE"
[ "$(grep -c '^pull ' "$QCH_UPDATE_DOCKER_LOG")" -eq 2 ]

for failure in mismatch unavailable platform; do
    export QCH_MOCK_LOCAL_STORE="$failure"
    if [ "$failure" = platform ]; then
        export QCH_MOCK_LOCAL_STORE=containerd QCH_MOCK_LOCAL_PLATFORM=linux/s390x
    fi
    export QCH_UPDATE_DOCKER_LOG="$test_root/docker-local-$failure.log"
    write_fixture
    if (update_external_services) > "$test_root/local-$failure.out" 2>&1; then exit 1; fi
    if grep -Fq ' up -d' "$QCH_UPDATE_DOCKER_LOG"; then exit 1; fi
    cmp "$test_root/expected.env" "$ENV_FILE"
    cmp "$test_root/expected-compose.yml" "$EXTERNAL_COMPOSE_FILE"
    unset QCH_MOCK_LOCAL_PLATFORM
done
unset QCH_MOCK_LOCAL_STORE
export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000001
export QCH_UPDATE_TARGET_WEB_ID=sha256:0000000000000000000000000000000000000000000000000000000000000002

# Missing, malformed, ambiguous and unavailable metadata always fail before pull.
for failure in bad-json missing-id ambiguous missing-platform wrong-digest metadata-failure; do
    export QCH_MOCK_REGISTRY_MODE="$failure"
    export QCH_UPDATE_DOCKER_LOG="$test_root/docker-remote-$failure.log"
    write_fixture
    if (update_external_services) > "$test_root/remote-$failure.out" 2>&1; then exit 1; fi
    if grep -Fq '无需更新' "$test_root/remote-$failure.out" ||
        grep -Eq '(^pull | up -d)' "$QCH_UPDATE_DOCKER_LOG"; then exit 1; fi
    cmp "$test_root/expected.env" "$ENV_FILE"
    cmp "$test_root/expected-compose.yml" "$EXTERNAL_COMPOSE_FILE"
done
unset QCH_MOCK_REGISTRY_MODE
export QCH_UPDATE_FAIL_REMOTE=true
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-remote-offline.log"
write_fixture
if (update_external_services) > "$test_root/remote-offline.out" 2>&1; then exit 1; fi
grep -Fq '远程镜像检查失败' "$test_root/remote-offline.out"
if grep -Eq '(^pull | up -d)' "$QCH_UPDATE_DOCKER_LOG"; then exit 1; fi
export QCH_UPDATE_FAIL_REMOTE=false

# Compose's platform overrides DOCKER_DEFAULT_PLATFORM. The pull and runtime
# override both keep the selected architecture; .env remains byte-for-byte.
export QCH_MOCK_COMPOSE_PLATFORM=linux/arm64/v8 QCH_MOCK_IMAGE_PLATFORM=linux/arm64
export DOCKER_DEFAULT_PLATFORM=linux/amd64
export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000004
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-platform.log"
write_fixture
update_external_services > "$test_root/platform.out"
grep -Fq 'pull --platform linux/arm64 ' "$QCH_UPDATE_DOCKER_LOG"
cmp "$test_root/expected.env" "$ENV_FILE"
unset QCH_MOCK_COMPOSE_PLATFORM DOCKER_DEFAULT_PLATFORM
export QCH_MOCK_ENV_PLATFORM=linux/arm64/v8
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-env-platform.log"
write_fixture
update_external_services > "$test_root/env-platform.out"
grep -Fq 'pull --platform linux/arm64 ' "$QCH_UPDATE_DOCKER_LOG"
cmp "$test_root/expected.env" "$ENV_FILE"
unset QCH_MOCK_ENV_PLATFORM QCH_MOCK_IMAGE_PLATFORM

export QCH_UPDATE_TARGET_CONTROL_ID=sha256:0000000000000000000000000000000000000000000000000000000000000004
export QCH_UPDATE_TARGET_WEB_ID=sha256:0000000000000000000000000000000000000000000000000000000000000005
export QCH_UPDATE_TARGET_CONTROL_VERSION=new-control-version
export QCH_UPDATE_TARGET_WEB_VERSION=new-web-version

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
grep -Fq 'image tag sha256:0000000000000000000000000000000000000000000000000000000000000001 ghcr.io/qimaoww/qcontrol-plane:latest' "$QCH_UPDATE_DOCKER_LOG"
grep -Fq 'image tag sha256:0000000000000000000000000000000000000000000000000000000000000002 ghcr.io/qimaoww/qcontrol-web:latest' "$QCH_UPDATE_DOCKER_LOG"
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
grep -Eq '控制面[[:space:]]+当前版本：old-control-version' "$test_root/pull-failure.out"
grep -Eq '网页服务[[:space:]]+当前版本：old-web-version' "$test_root/pull-failure.out"
grep -Eq '控制面[[:space:]]+目标标签：latest' "$test_root/pull-failure.out"
grep -Eq '网页服务[[:space:]]+目标标签：latest' "$test_root/pull-failure.out"
grep -Fq '正在检查远程镜像' "$test_root/pull-failure.out"
grep -Fq '目标版本：new-control-version' "$test_root/pull-failure.out"
grep -Fq '镜像拉取失败' "$test_root/pull-failure.out"
if grep -Fq '无需更新' "$test_root/pull-failure.out"; then
    printf '%s\n' 'failed external pull reported no update' >&2
    exit 1
fi
cmp -s "$test_root/expected.env" "$ENV_FILE"
cmp -s "$test_root/expected-compose.yml" "$EXTERNAL_COMPOSE_FILE"
if grep -Fq 'up -d --force-recreate' "$QCH_UPDATE_DOCKER_LOG"; then
    printf '%s\n' 'quick-start update regression: pull failure unnecessarily recreated containers' >&2
    exit 1
fi
grep -Fq 'image tag sha256:0000000000000000000000000000000000000000000000000000000000000001 ghcr.io/qimaoww/qcontrol-plane:latest' "$QCH_UPDATE_DOCKER_LOG"
grep -Fq 'image tag sha256:0000000000000000000000000000000000000000000000000000000000000002 ghcr.io/qimaoww/qcontrol-web:latest' "$QCH_UPDATE_DOCKER_LOG"
grep -Fq '已恢复旧配置，原版本容器继续运行' "$test_root/pull-failure.out"

# A successful recreate is not sufficient: failed new-instance readiness
# must restore the old images, and rollback checks must probe them again.
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-health-failure.log"
export QCH_UPDATE_CURL_LOG="$test_root/curl-health-failure.log"
export QCH_UPDATE_FAIL_PULL=false QCH_UPDATE_FAIL_HEALTH=true
printf '%s\n' old > "$QCH_UPDATE_PHASE"
write_fixture
if (update_external_services) > "$test_root/health-failure.out" 2>&1; then
    printf '%s\n' 'quick-start update regression: failed health check succeeded' >&2
    exit 1
fi
cmp "$test_root/expected.env" "$ENV_FILE"
cmp "$test_root/expected-compose.yml" "$EXTERNAL_COMPOSE_FILE"
[ "$(<"$QCH_UPDATE_PHASE")" = rollback ]
grep -Fq '已恢复旧配置，原版本容器继续运行' "$test_root/health-failure.out"

# A failed migration cannot start the new frontend. The recovered control
# plane must pass readiness before rollback starts the original frontend.
export QCH_UPDATE_FAIL_HEALTH=false QCH_UPDATE_FAIL_CONTROL_READY=true
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-control-not-ready.log"
export QCH_UPDATE_CURL_LOG="$test_root/curl-control-not-ready.log"
printf '%s\n' old > "$QCH_UPDATE_PHASE"
write_fixture
if (update_external_services) > "$test_root/control-not-ready.out" 2>&1; then
    printf '%s\n' 'quick-start update regression: migration failure succeeded' >&2
    exit 1
fi
grep -Fq '已恢复旧配置，原版本容器继续运行' "$test_root/control-not-ready.out"
web_up="$(grep 'up .*--no-deps qcontrol-web$' "$QCH_UPDATE_DOCKER_LOG")"
[ "$(printf '%s\n' "$web_up" | wc -l)" -eq 1 ]
grep -Fq 'docker-compose.rollback.yml' <<< "$web_up"
export QCH_UPDATE_FAIL_CONTROL_READY=false

# Recovery itself can fail (for example, storage becomes read-only). Do not
# delete the only backups or claim the original deployment has been restored.
export QCH_UPDATE_DOCKER_LOG="$test_root/docker-restore-failure.log"
export QCH_UPDATE_CURL_LOG="$test_root/curl-restore-failure.log"
export QCH_UPDATE_FAIL_HEALTH=false QCH_UPDATE_FAIL_UP=true
write_fixture
if (
    cp() {
        if [ "${3:-}" = "$UPDATE_BACKUP_DIR/.env" ]; then
            return 1
        fi
        command cp "$@"
    }
    update_external_services
) > "$test_root/restore-failure.out" 2>&1; then
    printf '%s\n' 'quick-start update regression: failed file restoration succeeded' >&2
    exit 1
fi
grep -Fq '旧配置恢复失败；回滚材料保留在' "$test_root/restore-failure.out"
if grep -Fq '已恢复旧配置，原版本容器继续运行' "$test_root/restore-failure.out"; then
    printf '%s\n' 'quick-start update regression: failed restoration reported success' >&2
    exit 1
fi
backup_path="$(find "$WORK_DIR" -maxdepth 1 -name '.qcontrolhub-update.*' -type d -print -quit)"
[ -n "$backup_path" ]
cmp "$test_root/expected.env" "$backup_path/.env"
cmp "$test_root/expected-compose.yml" "$backup_path/docker-compose.external.yml"
# Retained rollback files contain credentials and must never be accidentally
# staged when this installer is used from a repository checkout.
grep -Fxq '/.qcontrolhub-update.*/' "$repo_root/.gitignore"

printf '%s\n' 'quick-start external update rollback regression passed'
