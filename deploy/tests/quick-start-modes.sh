#!/usr/bin/env bash
set -euo pipefail
export COLUMNS=72

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-quick-start-modes.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
umask 077
export XDG_CONFIG_HOME="$test_root/config"
unset QCH_IMAGE_TAG

# Exercise the real CLI entry points without pulling images, creating a
# database, or touching any host services. Only Docker/curl are substituted.
bundled_image_tag() {
    local image_tag
    if [ "${QCH_IMAGE_TAG+x}" = x ]; then
        image_tag="$QCH_IMAGE_TAG"
    else
        image_tag="$(sed -n 's/^QCH_IMAGE_TAG=//p' "$QCH_INSTALL_DIR/.env" | tail -n 1)"
        case "$image_tag" in
            \"*\") image_tag="${image_tag#\"}"; image_tag="${image_tag%\"}" ;;
            \'*\') image_tag="${image_tag#\'}"; image_tag="${image_tag%\'}" ;;
        esac
    fi
    [ -n "$image_tag" ] || image_tag=latest
    printf '%s' "$image_tag"
}

docker() {
    local image="${*: -1}"
    printf '%s\n' "$*" >> "$QCH_MODES_TEST_LOG"
    case " $* " in
        *" config --no-interpolate "*) cat "$QCH_INSTALL_DIR/docker-compose.external.yml" ;;
        *" config --images "*)
            printf '%s\n' \
                'postgres:17-alpine' \
                "ghcr.io/qimaoww/qcontrol-plane:$(bundled_image_tag)" \
                "ghcr.io/qimaoww/qcontrol-web:$(bundled_image_tag)"
            ;;
        *" ps -q control-plane "*) printf '%s\n' control-container ;;
        *" ps -q qcontrol-web "*) printf '%s\n' web-container ;;
        *" ps -q postgres "*) printf '%s\n' postgres-container ;;
        *" pull "*) [ "${QCH_MODES_FAIL_PULL:-false}" = false ] ;;
        *" network inspect "*) return 0 ;;
        *" inspect "*)
            case "$image" in
                control-container)
                    case "$*" in
                        *"{{.Config.Image}}"*) printf '%s\n' ghcr.io/qimaoww/qcontrol-plane:latest ;;
                        *org.opencontainers.image.version*) printf '%s\n' old-control-version ;;
                        *) printf '%s\n' sha256:old-control ;;
                    esac
                    ;;
                web-container)
                    case "$*" in
                        *"{{.Config.Image}}"*) printf '%s\n' ghcr.io/qimaoww/qcontrol-web:latest ;;
                        *org.opencontainers.image.version*) printf '%s\n' old-web-version ;;
                        *) printf '%s\n' sha256:old-web ;;
                    esac
                    ;;
                postgres-container)
                    case "$*" in
                        *"{{.Config.Image}}"*) printf '%s\n' postgres:17-alpine ;;
                        *org.opencontainers.image.version*) printf '%s\n' old-postgres-version ;;
                        *) printf '%s\n' sha256:old-postgres ;;
                    esac
                    ;;
                ghcr.io/qimaoww/qcontrol-plane:*)
                    case "$*" in
                        *org.opencontainers.image.version*) printf '%s\n' "${QCH_MODES_TARGET_CONTROL_VERSION:-new-control-version}" ;;
                        *) printf '%s\n' "${QCH_MODES_TARGET_CONTROL_ID:-sha256:new-control}" ;;
                    esac
                    ;;
                ghcr.io/qimaoww/qcontrol-web:*)
                    case "$*" in
                        *org.opencontainers.image.version*) printf '%s\n' "${QCH_MODES_TARGET_WEB_VERSION:-new-web-version}" ;;
                        *) printf '%s\n' "${QCH_MODES_TARGET_WEB_ID:-sha256:new-web}" ;;
                    esac
                    ;;
                postgres:17-alpine)
                    case "$*" in
                        *org.opencontainers.image.version*) printf '%s\n' "${QCH_MODES_TARGET_POSTGRES_VERSION:-old-postgres-version}" ;;
                        *) printf '%s\n' "${QCH_MODES_TARGET_POSTGRES_ID:-sha256:old-postgres}" ;;
                    esac
                    ;;
                sha256:old-control) printf '%s\n' old-control-version ;;
                sha256:old-web) printf '%s\n' old-web-version ;;
                sha256:old-postgres) printf '%s\n' old-postgres-version ;;
                sha256:new-control) printf '%s\n' new-control-version ;;
                sha256:new-web) printf '%s\n' new-web-version ;;
                sha256:new-postgres) printf '%s\n' new-postgres-version ;;
                *) return 1 ;;
            esac
            ;;
        *) return 0 ;;
    esac
}
curl() { return 0; }
export -f docker curl bundled_image_tag

export QCH_INSTALL_DIR="$test_root/bundled"
export QCH_MODES_TEST_LOG="$test_root/bundled.log"
bash "$repo_root/deploy/quick-start.sh" -m bundled -o install > "$test_root/bundled-install.out"
grep -Fxq -- "$QCH_INSTALL_DIR" "$XDG_CONFIG_HOME/qcontrolhub/install-dir"
grep -q '^POSTGRES_PASSWORD=.' "$QCH_INSTALL_DIR/.env"
[ -s "$QCH_INSTALL_DIR/.secrets/config-encryption-key" ]
[ ! -f "$QCH_INSTALL_DIR/docker-compose.external.yml" ]
grep -Fq -- "-f $repo_root/docker-compose.yml -f $QCH_INSTALL_DIR/docker-compose.secrets.yml up -d" "$QCH_MODES_TEST_LOG"
cp "$QCH_INSTALL_DIR/.env" "$test_root/bundled-before.env"
cp "$QCH_INSTALL_DIR/.secrets/config-encryption-key" "$test_root/bundled-before.key"
cp "$QCH_INSTALL_DIR/docker-compose.secrets.yml" "$test_root/bundled-before-compose.yml"
export QCH_MODES_TARGET_CONTROL_ID=sha256:old-control
export QCH_MODES_TARGET_WEB_ID=sha256:old-web
export QCH_MODES_TARGET_POSTGRES_ID=sha256:old-postgres
export QCH_MODES_TARGET_CONTROL_VERSION=old-control-version
export QCH_MODES_TARGET_WEB_VERSION=old-web-version
export QCH_MODES_TARGET_POSTGRES_VERSION=old-postgres-version
: > "$QCH_MODES_TEST_LOG"
bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-update.out"
grep -Fq '更新内置 PostgreSQL 部署并复用现有配置' "$test_root/bundled-update.out"
grep -Fq '当前镜像与目标版本一致，无需更新' "$test_root/bundled-update.out"
grep -Fq old-control-version "$test_root/bundled-update.out"
cmp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"
cmp "$test_root/bundled-before.key" "$QCH_INSTALL_DIR/.secrets/config-encryption-key"
cmp "$test_root/bundled-before-compose.yml" "$QCH_INSTALL_DIR/docker-compose.secrets.yml"
if grep -Fq ' up -d' "$QCH_MODES_TEST_LOG"; then
    printf '%s\n' 'bundled update recreated containers with unchanged application images' >&2
    exit 1
fi
grep -Fq 'pull postgres:17-alpine' "$QCH_MODES_TEST_LOG"

# A pinned tag is a comparison target, not a claim about the latest release.
: > "$QCH_MODES_TEST_LOG"
QCH_IMAGE_TAG=release-test bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-pinned-no-update.out"
grep -Eq 'control-plane[[:space:]]+目标标签：release-test' "$test_root/bundled-pinned-no-update.out"
grep -Eq 'qcontrol-web[[:space:]]+目标标签：release-test' "$test_root/bundled-pinned-no-update.out"
grep -Eq 'PostgreSQL[[:space:]]+目标标签：17-alpine' "$test_root/bundled-pinned-no-update.out"
grep -Fq '当前镜像与目标版本一致，无需更新' "$test_root/bundled-pinned-no-update.out"
if grep -Fq '已是最新版本' "$test_root/bundled-pinned-no-update.out" || grep -Fq ' up -d' "$QCH_MODES_TEST_LOG"; then
    printf '%s\n' 'pinned bundled no-op reported latest or recreated containers' >&2
    exit 1
fi

# Even the first registry pull failing must leave current versions visible.
: > "$QCH_MODES_TEST_LOG"
if QCH_MODES_FAIL_PULL=true bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-pull-failure.out" 2>&1; then
    printf '%s\n' 'bundled update ignored a failed image pull' >&2
    exit 1
fi
grep -Eq 'control-plane[[:space:]]+当前版本：old-control-version' "$test_root/bundled-pull-failure.out"
grep -Eq 'qcontrol-web[[:space:]]+当前版本：old-web-version' "$test_root/bundled-pull-failure.out"
grep -Eq 'PostgreSQL[[:space:]]+当前版本：old-postgres-version' "$test_root/bundled-pull-failure.out"
grep -Fq '正在检查更新（拉取目标镜像）' "$test_root/bundled-pull-failure.out"
if grep -Eq '目标版本：|无需更新' "$test_root/bundled-pull-failure.out" || grep -Fq ' up -d' "$QCH_MODES_TEST_LOG"; then
    printf '%s\n' 'failed bundled check reported a result or recreated containers' >&2
    exit 1
fi
cmp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"
cmp "$test_root/bundled-before.key" "$QCH_INSTALL_DIR/.secrets/config-encryption-key"

export QCH_MODES_TARGET_CONTROL_ID=sha256:new-control
export QCH_MODES_TARGET_CONTROL_VERSION=new-control-version
: > "$QCH_MODES_TEST_LOG"
bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-new-version.out"
grep -Fq old-control-version "$test_root/bundled-new-version.out"
grep -Fq new-control-version "$test_root/bundled-new-version.out"
grep -Fq '更新完成' "$test_root/bundled-new-version.out"
control_pull_line="$(grep -nF 'pull ghcr.io/qimaoww/qcontrol-plane:latest' "$QCH_MODES_TEST_LOG" | cut -d: -f1)"
web_pull_line="$(grep -nF 'pull ghcr.io/qimaoww/qcontrol-web:latest' "$QCH_MODES_TEST_LOG" | cut -d: -f1)"
postgres_pull_line="$(grep -nF 'pull postgres:17-alpine' "$QCH_MODES_TEST_LOG" | cut -d: -f1)"
up_line="$(grep -nF ' up -d --no-build --pull never' "$QCH_MODES_TEST_LOG" | cut -d: -f1)"
if [ "$control_pull_line" -ge "$web_pull_line" ] || \
    [ "$web_pull_line" -ge "$postgres_pull_line" ] || \
    [ "$postgres_pull_line" -ge "$up_line" ]; then
    printf '%s\n' 'bundled update pull/recreate order changed' >&2
    exit 1
fi
cmp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"
cmp "$test_root/bundled-before.key" "$QCH_INSTALL_DIR/.secrets/config-encryption-key"
cmp "$test_root/bundled-before-compose.yml" "$QCH_INSTALL_DIR/docker-compose.secrets.yml"

# A PostgreSQL-only image update must not be mistaken for an application
# no-op; Compose needs to apply the new database image as well.
export QCH_MODES_TARGET_CONTROL_ID=sha256:old-control
export QCH_MODES_TARGET_CONTROL_VERSION=old-control-version
export QCH_MODES_TARGET_POSTGRES_ID=sha256:new-postgres
export QCH_MODES_TARGET_POSTGRES_VERSION=new-postgres-version
: > "$QCH_MODES_TEST_LOG"
bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-postgres-only.out"
grep -Eq 'control-plane[[:space:]]+当前版本：old-control-version' "$test_root/bundled-postgres-only.out"
grep -Eq 'qcontrol-web[[:space:]]+当前版本：old-web-version' "$test_root/bundled-postgres-only.out"
grep -Eq 'control-plane[[:space:]]+目标版本：old-control-version' "$test_root/bundled-postgres-only.out"
grep -Eq 'qcontrol-web[[:space:]]+目标版本：old-web-version' "$test_root/bundled-postgres-only.out"
grep -Eq 'PostgreSQL[[:space:]]+当前版本：old-postgres-version' "$test_root/bundled-postgres-only.out"
grep -Eq 'PostgreSQL[[:space:]]+目标版本：new-postgres-version' "$test_root/bundled-postgres-only.out"
grep -Fq '更新完成' "$test_root/bundled-postgres-only.out"
grep -Fq 'pull postgres:17-alpine' "$QCH_MODES_TEST_LOG"
grep -Fq ' up -d --no-build --pull never' "$QCH_MODES_TEST_LOG"
cmp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"
cmp "$test_root/bundled-before.key" "$QCH_INSTALL_DIR/.secrets/config-encryption-key"
cmp "$test_root/bundled-before-compose.yml" "$QCH_INSTALL_DIR/docker-compose.secrets.yml"

export QCH_MODES_TARGET_POSTGRES_ID=sha256:old-postgres
export QCH_MODES_TARGET_POSTGRES_VERSION=old-postgres-version
export QCH_MODES_TARGET_CONTROL_ID=sha256:new-control
export QCH_MODES_TARGET_CONTROL_VERSION=new-control-version

# An explicit local build bypasses the remote version check, while a custom
# image tag is used consistently for both the pull and the version comparison.
: > "$QCH_MODES_TEST_LOG"
QCH_IMAGE_TAG=local bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-local.out"
grep -Fq '本地构建模式无法检查远程镜像版本' "$test_root/bundled-local.out"
grep -Fq ' up -d --build' "$QCH_MODES_TEST_LOG"
if grep -Fq 'pull ' "$QCH_MODES_TEST_LOG"; then
    printf '%s\n' 'local bundled update unexpectedly pulled remote images' >&2
    exit 1
fi

: > "$QCH_MODES_TEST_LOG"
QCH_IMAGE_TAG=release-test bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-custom-tag.out"
grep -Fq 'pull ghcr.io/qimaoww/qcontrol-plane:release-test' "$QCH_MODES_TEST_LOG"
grep -Fq 'pull ghcr.io/qimaoww/qcontrol-web:release-test' "$QCH_MODES_TEST_LOG"
grep -Fq 'image inspect --format {{.Id}} ghcr.io/qimaoww/qcontrol-plane:release-test' "$QCH_MODES_TEST_LOG"
grep -Fq ' up -d --no-build --pull never' "$QCH_MODES_TEST_LOG"
if grep -Fq 'pull ghcr.io/qimaoww/qcontrol-plane:latest' "$QCH_MODES_TEST_LOG"; then
    printf '%s\n' 'custom tag bundled update pulled latest instead of the selected tag' >&2
    exit 1
fi
cmp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"
cmp "$test_root/bundled-before.key" "$QCH_INSTALL_DIR/.secrets/config-encryption-key"
cmp "$test_root/bundled-before-compose.yml" "$QCH_INSTALL_DIR/docker-compose.secrets.yml"

# Compose strips quotes in .env values before interpolating the image tag.
sed -i 's/^QCH_IMAGE_TAG=latest$/QCH_IMAGE_TAG="quoted-release"/' "$QCH_INSTALL_DIR/.env"
cp "$QCH_INSTALL_DIR/.env" "$test_root/bundled-quoted-before.env"
: > "$QCH_MODES_TEST_LOG"
bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-quoted-tag.out"
grep -Fq 'pull ghcr.io/qimaoww/qcontrol-plane:quoted-release' "$QCH_MODES_TEST_LOG"
grep -Fq 'pull ghcr.io/qimaoww/qcontrol-web:quoted-release' "$QCH_MODES_TEST_LOG"
grep -Fq 'image inspect --format {{.Id}} ghcr.io/qimaoww/qcontrol-plane:quoted-release' "$QCH_MODES_TEST_LOG"
grep -Fq ' up -d --no-build --pull never' "$QCH_MODES_TEST_LOG"
cmp "$test_root/bundled-quoted-before.env" "$QCH_INSTALL_DIR/.env"
cp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"
bash "$repo_root/deploy/quick-start.sh" -o uninstall > "$test_root/bundled-uninstall.out"
grep -Fq '已保留数据：Docker PostgreSQL 命名卷' "$test_root/bundled-uninstall.out"
cmp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"

for network in default shared-edge; do
    export QCH_MODES_TARGET_CONTROL_ID=sha256:new-control
    export QCH_MODES_TARGET_WEB_ID=sha256:new-web
    export QCH_MODES_TARGET_CONTROL_VERSION=new-control-version
    export QCH_MODES_TARGET_WEB_VERSION=new-web-version
    export QCH_INSTALL_DIR="$test_root/external-$network"
    export QCH_MODES_TEST_LOG="$test_root/external-$network.log"
    network_args=()
    [ "$network" = default ] || network_args=(-n "$network")
    bash "$repo_root/deploy/quick-start.sh" -m external -o install \
        -d 'postgres://remote.example.test/qcontrolhub?sslmode=verify-full' \
        "${network_args[@]}" > "$test_root/external-$network-install.out"
    [ -s "$QCH_INSTALL_DIR/.secrets/config-encryption-key" ]
    grep -Fxq 'QCH_CONFIG_ENCRYPTION_KEY=' "$QCH_INSTALL_DIR/.env"
    grep -Fq -- "-f $QCH_INSTALL_DIR/docker-compose.external.yml -f $QCH_INSTALL_DIR/docker-compose.secrets.yml up -d --force-recreate" "$QCH_MODES_TEST_LOG"
    if [ "$network" = shared-edge ]; then
        grep -Fxq 'QCH_DOCKER_NETWORK=shared-edge' "$QCH_INSTALL_DIR/.env"
        grep -Fq 'external: true' "$QCH_INSTALL_DIR/docker-compose.external.yml"
        grep -Fq 'network inspect -- shared-edge' "$QCH_MODES_TEST_LOG"
    fi
    cp "$QCH_INSTALL_DIR/.env" "$test_root/external-$network-before.env"
    cp "$QCH_INSTALL_DIR/.secrets/config-encryption-key" "$test_root/external-$network-before.key"
    bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/external-$network-update.out"
    grep -Fq '更新外部 PostgreSQL 部署并复用现有配置' "$test_root/external-$network-update.out"
    cmp "$test_root/external-$network-before.env" "$QCH_INSTALL_DIR/.env"
    cmp "$test_root/external-$network-before.key" "$QCH_INSTALL_DIR/.secrets/config-encryption-key"
    if grep -Fq -- "-f $repo_root/docker-compose.yml" "$QCH_MODES_TEST_LOG"; then
        printf '%s\n' 'external mode selected the bundled database Compose' >&2
        exit 1
    fi
    if grep -Fq 'QCH_DISABLE_DATABASE_MIGRATIONS' "$QCH_INSTALL_DIR/docker-compose.external.yml"; then
        printf '%s\n' 'installer changed the original schema initialization behavior' >&2
        exit 1
    fi
    bash "$repo_root/deploy/quick-start.sh" -o uninstall > "$test_root/external-$network-uninstall.out"
    grep -Fq '外部 PostgreSQL 数据未被修改' "$test_root/external-$network-uninstall.out"
    cmp "$test_root/external-$network-before.env" "$QCH_INSTALL_DIR/.env"
done

printf '%s\n' 'quick-start bundled/external CLI regression passed'
