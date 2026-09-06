#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-quick-start-modes.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
umask 077

# Exercise the real CLI entry points without pulling images, creating a
# database, or touching any host services. Only Docker/curl are substituted.
docker() {
    printf '%s\n' "$*" >> "$QCH_MODES_TEST_LOG"
    case " $* " in
        *" ps -q control-plane "*) printf '%s\n' control-container ;;
        *" ps -q qcontrol-web "*) printf '%s\n' web-container ;;
        *" inspect --format {{.Image}} "*) printf '%s\n' sha256:old-image ;;
        *" inspect --format {{.Config.Image}} control-container "*) printf '%s\n' ghcr.io/qimaoww/qcontrol-plane:latest ;;
        *" inspect --format {{.Config.Image}} web-container "*) printf '%s\n' ghcr.io/qimaoww/qcontrol-web:latest ;;
        *) return 0 ;;
    esac
}
curl() { return 0; }
export -f docker curl

export QCH_INSTALL_DIR="$test_root/bundled"
export QCH_MODES_TEST_LOG="$test_root/bundled.log"
bash "$repo_root/deploy/quick-start.sh" -m bundled -o install > "$test_root/bundled-install.out"
grep -q '^POSTGRES_PASSWORD=.' "$QCH_INSTALL_DIR/.env"
[ -s "$QCH_INSTALL_DIR/.secrets/config-encryption-key" ]
[ ! -f "$QCH_INSTALL_DIR/docker-compose.external.yml" ]
grep -Fq -- "-f $repo_root/docker-compose.yml -f $QCH_INSTALL_DIR/docker-compose.secrets.yml up -d" "$QCH_MODES_TEST_LOG"
cp "$QCH_INSTALL_DIR/.env" "$test_root/bundled-before.env"
cp "$QCH_INSTALL_DIR/.secrets/config-encryption-key" "$test_root/bundled-before.key"
bash "$repo_root/deploy/quick-start.sh" -o update > "$test_root/bundled-update.out"
grep -Fq '更新内置 PostgreSQL 部署并复用现有配置' "$test_root/bundled-update.out"
cmp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"
cmp "$test_root/bundled-before.key" "$QCH_INSTALL_DIR/.secrets/config-encryption-key"
bash "$repo_root/deploy/quick-start.sh" -o uninstall > "$test_root/bundled-uninstall.out"
grep -Fq '已保留数据：Docker PostgreSQL 命名卷' "$test_root/bundled-uninstall.out"
cmp "$test_root/bundled-before.env" "$QCH_INSTALL_DIR/.env"

for network in default shared-edge; do
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
