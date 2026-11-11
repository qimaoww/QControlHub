#!/usr/bin/env bash
# Command simulations exercise failure paths without changing host packages or services.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$repo_root/deploy/quick-start.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
eval "$(declare -f install_docker_engine | sed '1s/install_docker_engine/native_install_docker_engine/')"
eval "$(declare -f docker_install_privileges | sed '1s/docker_install_privileges/native_docker_install_privileges/')"
unset DOCKER_CONTEXT DOCKER_HOST
export XDG_CONFIG_HOME="$test_root/config"

# Simulate the default socket independently of any Docker on the test runner.
function [ {
    if builtin [ "${1:-}" = '!' ] && builtin [ "${2:-}" = -x ] && builtin [ "${3:-}" = /usr/bin/docker ]; then return 0; fi
    if builtin [ "${1:-}" = -S ] && builtin [ "${2:-}" = /var/run/docker.sock ]; then builtin [ "${fixture_socket:-false}" = true ]; return; fi
    if builtin [ "${1:-}" = '!' ] && builtin [ "${2:-}" = -S ] && builtin [ "${3:-}" = /var/run/docker.sock ]; then return 0; fi
    builtin [ "$@"
}
# State lives in files so assertions also observe calls in subshells.
record() { printf '%s\n' "$*" >> "$test_root/calls"; }
command() {
    if [ "$*" = '-v docker' ] && [ ! -f "$test_root/cli" ]; then return 1; fi
    if [ "$*" = '-v dockerd' ]; then [ "${fixture_daemon:-false}" = true ] && printf /usr/sbin/dockerd; return; fi
    builtin command "$@"
}
docker() {
    case "$*" in
        info) [ -f "$test_root/ready" ] ;;
        'buildx version') [ -f "$test_root/buildx" ] ;;
        'compose version') [ -f "$test_root/compose" ] ;;
        'context inspect --format {{.Endpoints.docker.Host}}')
            [ "${bad_context:-false}" = false ] || return 1
            printf '%s' "${fixture_endpoint:-unix:///var/run/docker.sock}"
            ;;
        *) record "unexpected docker $*"; return 1 ;;
    esac
}
docker_probe() { docker "$@"; }
docker_install_privileges() {
    record privileges
    [ "${deny_privileges:-false}" = false ] || die '需要 root 权限'
    DOCKER_INSTALL_SUDO=()
}
docker_service_manager() {
    [ "${unsupported_init:-false}" = false ] || die '不支持的服务管理方式'
    printf '%s' "${fixture_manager:-systemd}"
}
install_docker_engine() {
    record engine
    [ "${fail_install:-false}" = false ] || die '安装 Docker Engine 失败'
    touch "$test_root/cli"
}
install_docker_compose() {
    record compose
    [ "${fail_compose:-false}" = false ] || die '安装 Compose v2 失败'
    touch "$test_root/compose"
}
timeout() { shift; "$@"; }
systemctl() {
    record "systemctl $*"
    case "$*" in
        'enable docker') return 0 ;;
        'is-active --quiet docker') [ "${active:-false}" = true ] ;;
        'start docker')
            [ "${fail_start:-false}" = false ] || return 1
            touch "$test_root/ready"
            ;;
        *) return 1 ;;
    esac
}
rc-update() { record "rc-update $*"; }
rc-service() {
    record "rc-service $*"
    case "$*" in
        'docker status') return 1 ;;
        'docker start') systemctl start docker ;;
        *) return 1 ;;
    esac
}
reset_case() {
    rm -f "$test_root/cli" "$test_root/ready" "$test_root/compose"
    : > "$test_root/calls"
    ACTION=install
    fixture_socket=false fixture_daemon=false
    fixture_endpoint=unix:///var/run/docker.sock
    bad_context=false deny_privileges=false unsupported_init=false
    fail_install=false fail_compose=false fail_start=false active=false fixture_manager=systemd
}
expect_failure() {
    local message="$1"
    if (require_commands) > "$test_root/output" 2>&1; then
        printf 'expected failure: %s\n' "$message" >&2; exit 1
    fi
    grep -Fq "$message" "$test_root/output"
}
assert_absent() { if grep -Fq "$1" "$test_root/calls"; then cat "$test_root/calls" >&2; exit 1; fi; }

for MODE in bundled external; do
    reset_case
    require_commands
    grep -Fxq engine "$test_root/calls"
    grep -Fxq compose "$test_root/calls"
    grep -Fxq 'systemctl start docker' "$test_root/calls"
    : > "$test_root/calls"
    require_commands
    [ ! -s "$test_root/calls" ] # Reuse/second invocation never changes workloads.
done
reset_case
touch "$test_root/cli" "$test_root/ready"
require_commands
grep -Fxq compose "$test_root/calls"
assert_absent engine
assert_absent systemctl
reset_case
touch "$test_root/cli" "$test_root/compose"
require_commands
assert_absent engine
assert_absent compose
reset_case
fixture_manager=openrc
require_commands
grep -Fxq 'rc-service docker start' "$test_root/calls"

reset_case; deny_privileges=true
expect_failure 'root 权限'
assert_absent engine
reset_case; unsupported_init=true
expect_failure '无法自动启动 Docker'
assert_absent engine
reset_case; fail_install=true
expect_failure '安装 Docker Engine 失败'
assert_absent 'systemctl start'
assert_absent compose
reset_case; fail_start=true
expect_failure '启动 Docker 服务失败'
assert_absent compose
reset_case; touch "$test_root/cli" "$test_root/ready"; fail_compose=true
expect_failure '安装 Compose v2 失败'
reset_case; touch "$test_root/cli"; fixture_endpoint=tcp://remote.invalid:2376
expect_failure 'Docker 连接失败'
[ ! -s "$test_root/calls" ]
reset_case; touch "$test_root/cli"; bad_context=true
expect_failure '无法读取 Docker context'
[ ! -s "$test_root/calls" ]
# An active service with an API failure must never be restarted.
reset_case; touch "$test_root/cli"; active=true
expect_failure 'Docker 服务已运行但连接失败'
assert_absent 'systemctl start'
# Missing CLI after an interrupted distro installation can recover on rerun.
dpkg-query() {
    [ "${fixture_distro_owned:-true}" = true ] || return 1
    case "$*" in
        *'${Status}'*) printf 'install ok installed' ;;
        '-S /usr/sbin/dockerd') printf 'docker.io: /usr/sbin/dockerd' ;;
        *) return 1 ;;
    esac
}
reset_case
fixture_daemon=true
require_commands
grep -Fxq engine "$test_root/calls"
: > "$test_root/calls"
require_commands
[ ! -s "$test_root/calls" ]
reset_case
fixture_daemon=true fixture_distro_owned=false
expect_failure '请修复原安装来源'
assert_absent engine
unset -f dpkg-query
reset_case
fail_install=true
expect_failure '安装 Docker Engine 失败'
fail_install=false
require_commands
grep -Fxq 'systemctl start docker' "$test_root/calls"
reset_case
DOCKER_HOST=tcp://remote.invalid:2376
expect_failure '指定了 Docker 连接地址'
[ ! -s "$test_root/calls" ]
unset DOCKER_HOST
reset_case
DOCKER_CONTEXT=remote
expect_failure '指定了 Docker context'
[ ! -s "$test_root/calls" ]
unset DOCKER_CONTEXT
for operation in update uninstall; do
    reset_case
    ACTION="$operation"
    expect_failure '缺少依赖：docker'
    [ ! -s "$test_root/calls" ]
done
# Exercise native package selection with simulated package managers, not the
# orchestration substitute above. No package command can reach the host.
apt-get() { record "apt-get $*"; touch "$test_root/cli"; }
apk() { record "apk $*"; touch "$test_root/cli"; }
dnf() { record "dnf $*"; touch "$test_root/cli"; }
zypper() { record "zypper $*"; touch "$test_root/cli"; }
env() {
    if [[ "${1:-}" = DEBIAN_FRONTEND=* ]]; then shift; "$@"; else builtin command env "$@"; fi
}
uname() {
    case "$1" in -s) printf '%s' "${fixture_kernel:-Linux}" ;; -m) printf '%s' "${fixture_arch:-x86_64}" ;; esac
}
reset_case
native_install_docker_engine
grep -Eq '^(apt-get|apk|dnf|zypper) ' "$test_root/calls"
# Debian package split regression: package-manager success alone never
# supplies the CLI in this fixture. Check the actual requested package set.
apt-cache() {
    [ "${split_cli:-true}" = true ] || return 100
    printf 'Package: docker-cli\n'
}
dpkg-query() {
    case "$*" in
        *'${Status}'*) [ "${cli_installed:-false}" = true ] || return 1; printf 'install ok installed' ;;
        *'${Version}'*) printf '26.1.5+fixture' ;;
        *) return 1 ;;
    esac
}
reset_case
split_cli=true cli_installed=false
install_debian_docker
grep -Fq -- '--no-upgrade docker.io docker-cli' "$test_root/calls"
reset_case
split_cli=false
install_debian_docker
grep -Eq -- '--no-upgrade docker.io$' "$test_root/calls"
reset_case
split_cli=true cli_installed=true
install_debian_docker
grep -Fq -- '--reinstall docker-cli=26.1.5+fixture' "$test_root/calls"
assert_absent --no-upgrade
unset -f apt-cache dpkg-query
fixture_arch=unsupported
if (native_install_docker_engine) > "$test_root/output" 2>&1; then exit 1; fi
grep -Fq '不支持当前架构' "$test_root/output"
fixture_arch=x86_64 fixture_kernel=Darwin
if (native_install_docker_engine) > "$test_root/output" 2>&1; then exit 1; fi
grep -Fq '仅支持 Linux' "$test_root/output"
# Password-requiring sudo must fail immediately; no prompt is allowed.
id() { printf 1000; }
sudo() { record "sudo $*"; return 1; }
if (native_docker_install_privileges) > "$test_root/output" 2>&1; then exit 1; fi
grep -Fxq 'sudo -n true' "$test_root/calls"
grep -Fq 'root 权限' "$test_root/output"

# Buildx is needed only for explicitly requested local image builds.
reset_case
resolve_bundled_image_refs() { BUNDLED_CONTROL_IMAGE_REF="ghcr.io/qimaoww/qcontrol-plane:${fixture_tag:-latest}"; }
compose() { record "compose $*"; }
install_docker_buildx() { record buildx; }
fixture_tag=latest
start_services
assert_absent buildx
fixture_tag=local
start_services
grep -Fxq buildx "$test_root/calls"
# Real checksum calculation with simulated downloads/install commands verifies
# that corrupt or incomplete plugins never reach the installation command.
qch_github_download() {
    local destination="$2"
    printf '%s' "$destination" > "$test_root/download-path"
    printf fixture > "$destination"
    [ "${fail_download:-false}" = false ]
}
install() { record 'plugin install'; return 1; }
fail_download=true
if (install_docker_plugin qch-bootstrap-fixture https://invalid.example/plugin bad) > "$test_root/output" 2>&1; then exit 1; fi
grep -Fq '下载 qch-bootstrap-fixture 失败' "$test_root/output"
[ ! -e "$(cat "$test_root/download-path")" ]
fail_download=false
if (install_docker_plugin qch-bootstrap-fixture https://invalid.example/plugin bad) > "$test_root/output" 2>&1; then exit 1; fi
grep -Fq '下载校验失败' "$test_root/output"
[ ! -e "$(cat "$test_root/download-path")" ]
assert_absent 'plugin install'
checksum="$(printf fixture | openssl dgst -sha256 | awk '{print $NF}')"
if (install_docker_plugin qch-bootstrap-fixture https://invalid.example/plugin "$checksum") > "$test_root/output" 2>&1; then exit 1; fi
grep -Fq '安装 qch-bootstrap-fixture 失败' "$test_root/output"
[ ! -e "$(cat "$test_root/download-path")" ]
printf '%s\n' 'quick-start Docker bootstrap command simulations passed'
