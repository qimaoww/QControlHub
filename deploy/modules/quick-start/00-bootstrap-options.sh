#!/usr/bin/env bash
# QControlHub 一键交互式部署脚本（Linux）
#
# 提供两种部署方式：
#   bundled  — Docker Compose 内置 PostgreSQL + 控制面（从零开始）
#   external — 连接已有 PostgreSQL，仅部署控制面容器
#
# 用法：
#   ./deploy/quick-start.sh                          # 交互式选择部署方式
#   ./deploy/quick-start.sh -m bundled               # 全套部署（内置 PostgreSQL + 控制面）
#   ./deploy/quick-start.sh -m external -d 'postgresql://user:pass@db:5432/qcontrolhub?sslmode=verify-full'

set -euo pipefail

MODE=""
ACTION=""
DATABASE_URL=""
ADMIN_TOKEN=""
DOCKER_NETWORK=""
FORCE=false
READY_TIMEOUT=60

install_dir_preference_file() {
    local config_root="${XDG_CONFIG_HOME:-${HOME:-}}"
    [ -n "$config_root" ] || return 1
    case "$config_root" in
        /*) printf '%s\n' "$config_root/qcontrolhub/install-dir" ;;
        *) return 1 ;;
    esac
}

read_install_dir_preference() {
    local preference_file value
    preference_file="$(install_dir_preference_file 2>/dev/null)" || return 0
    [ -f "$preference_file" ] || return 0
    [ ! -L "$preference_file" ] || return 0
    value="$(<"$preference_file")" || return 0
    case "$value" in
        /*) ;;
        *) return 0 ;;
    esac
    case "$value" in
        *$'\n'*|*$'\r'*) return 0 ;;
    esac
    printf '%s\n' "$value"
}

persist_install_dir_preference() {
    local install_dir="$1" preference_file preference_dir temp_file
    case "$install_dir" in
        /*) ;;
        *) return 1 ;;
    esac
    case "$install_dir" in
        *$'\n'*|*$'\r'*) return 1 ;;
    esac
    preference_file="$(install_dir_preference_file 2>/dev/null)" || return 1
    preference_dir="$(dirname -- "$preference_file")"
    [ ! -L "$preference_dir" ] || return 1
    mkdir -p "$preference_dir" || return 1
    chmod 0700 "$preference_dir" || return 1
    [ ! -L "$preference_file" ] || return 1
    umask 077
    temp_file="$(mktemp "$preference_dir/.install-dir.XXXXXX")" || return 1
    if ! printf '%s\n' "$install_dir" > "$temp_file"; then
        rm -f -- "$temp_file"
        return 1
    fi
    chmod 0600 "$temp_file" || {
        rm -f -- "$temp_file"
        return 1
    }
    if ! mv -f -- "$temp_file" "$preference_file"; then
        rm -f -- "$temp_file"
        return 1
    fi
}

usage() {
    cat <<'USAGE'
用法：
  ./deploy/quick-start.sh [-o install|update|uninstall] [-m bundled|external] [选项]

操作：
  install             安装或重新配置 QControlHub
  update              更新现有部署并保持配置和数据
  uninstall           卸载服务，保留配置、密钥和数据库卷

部署模式：
  bundled             Docker Compose 内置 PostgreSQL + 控制面
  external            仅部署控制面并连接已有 PostgreSQL

选项：
  -m MODE             选择部署模式；省略时交互选择
  -o OPERATION        选择安装、更新或卸载；省略时交互选择
  -d DATABASE_URL     external 模式使用的 PostgreSQL 连接串
  -a ADMIN_TOKEN      管理员令牌（至少 32 字节）
  -n DOCKER_NETWORK   external 安装连接的已有 Docker 网络；省略时可交互选择或使用默认网络
  -f                  bundled 模式显式轮换 token 与应用密钥；external 更新不支持
  -t SECONDS          就绪检查超时时间（默认 60 秒）
  -h                  显示帮助

安装可选择 bundled 或 external；普通 external 更新逐字节保留 .env，不迁移凭据或轮换密钥。
交互式菜单中选择的安装目录会保存到当前用户配置；后续从远程一键命令运行时会自动复用。显式设置 QCH_INSTALL_DIR 时以该值为准。
USAGE
}

die() {
    printf '错误：%s\n' "$*" >&2
    exit 1
}

bootstrap_streamed_script() {
    local script_path install_dir install_dir_source persisted_install_dir origin_url branch marker_file marker_temp bootstrap_ref base_url script_temp compose_temp install_label
    script_path="${BASH_SOURCE[0]}"
    case "$script_path" in
        /dev/fd/*|/proc/self/fd/*) ;;
        *) return 0 ;;
    esac

    command -v curl >/dev/null 2>&1 || die "缺少依赖：curl"
    if [ -n "${QCH_INSTALL_DIR:-}" ]; then
        install_dir="$QCH_INSTALL_DIR"
        install_dir_source="explicit"
    elif [ -f "$PWD/.qcontrolhub-quick-start" ] || { [ -d "$PWD/.git" ] && [ -f "$PWD/deploy/quick-start.sh" ] && [ -f "$PWD/docker-compose.yml" ]; }; then
        install_dir="$PWD"
        install_dir_source="current"
    elif persisted_install_dir="$(read_install_dir_preference)" && [ -n "$persisted_install_dir" ]; then
        install_dir="$persisted_install_dir"
        install_dir_source="preference"
    else
        install_dir="$PWD/qcontrolhub"
        install_dir_source="default"
    fi
    case "$install_dir" in
        /*) ;;
        *) install_dir="$PWD/$install_dir" ;;
    esac
    case "$install_dir" in
        *$'\n'*|*$'\r'*) die "QCH_INSTALL_DIR 不能包含换行" ;;
    esac
    [ "$install_dir" != "/" ] || die "安装目录不能是文件系统根目录"
    [ ! -L "$install_dir" ] || die "安装目录不能是符号链接：$install_dir"
    marker_file="$install_dir/.qcontrolhub-quick-start"
    [ ! -L "$marker_file" ] || die "一键安装标记不能是符号链接：$marker_file"
    bootstrap_ref="${QCH_BOOTSTRAP_REF:-main}"
    case "$bootstrap_ref" in
        ""|/*|*/|*..*|*[!A-Za-z0-9._/-]*) die "QCH_BOOTSTRAP_REF 不是安全的 Git ref" ;;
    esac
    base_url="https://raw.githubusercontent.com/qimaoww/qcontrolhub/$bootstrap_ref"

    if [ -e "$install_dir" ]; then
        [ -d "$install_dir" ] || die "安装路径已存在但不是目录：$install_dir"
        if [ -f "$marker_file" ]; then
            :
        elif [ -d "$install_dir/.git" ]; then
            command -v git >/dev/null 2>&1 || die "迁移旧版 Git 安装目录需要 git"
            origin_url="$(git -C "$install_dir" remote get-url origin 2>/dev/null || true)"
            case "$origin_url" in
                https://github.com/qimaoww/qcontrolhub|https://github.com/qimaoww/qcontrolhub.git|git@github.com:qimaoww/qcontrolhub.git) ;;
                *) die "安装目录不是 QControlHub 官方仓库：$install_dir" ;;
            esac
            branch="$(git -C "$install_dir" symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
            [ "$branch" = "main" ] || die "旧版 Git 安装目录必须位于 main 分支，当前为：${branch:-detached}"
            git -C "$install_dir" diff --quiet -- deploy/quick-start.sh docker-compose.yml || \
                die "旧版 Git 安装目录的运行文件包含未提交修改，请先处理"
            git -C "$install_dir" diff --cached --quiet -- deploy/quick-start.sh docker-compose.yml || \
                die "旧版 Git 安装目录的运行文件包含已暂存修改，请先处理"
        # A directory previously selected from the menu is explicit user
        # intent. It can contain deployment state even when it predates the
        # standalone bootstrap marker.
        elif [ "$install_dir_source" = "preference" ]; then
            :
        elif [ -n "$(ls -A "$install_dir")" ]; then
            die "安装目录已存在且不是 QControlHub 一键安装目录：$install_dir"
        fi
        install_label="更新"
    else
        mkdir -p "$install_dir"
        install_label="安装"
    fi
    [ ! -L "$install_dir/deploy" ] || die "运行文件目录不能是符号链接：$install_dir/deploy"
    mkdir -p "$install_dir/deploy"

    echo "-> ${install_label} QControlHub 运行文件：$install_dir"
    script_temp="$(mktemp "$install_dir/deploy/.quick-start.sh.tmp.XXXXXX")"
    compose_temp="$(mktemp "$install_dir/.docker-compose.yml.tmp.XXXXXX")"
    marker_temp=""
    cleanup_bootstrap_downloads() {
        rm -f -- "$script_temp" "$compose_temp"
        [ -z "$marker_temp" ] || rm -f -- "$marker_temp"
    }
    trap cleanup_bootstrap_downloads EXIT HUP INT TERM
    curl -fsSL "$base_url/deploy/quick-start.sh" -o "$script_temp" || die "下载 quick-start.sh 失败"
    curl -fsSL "$base_url/docker-compose.yml" -o "$compose_temp" || die "下载 docker-compose.yml 失败"
    bash -n "$script_temp" || die "下载的 quick-start.sh 语法无效"
    grep -Fq 'name: qcontrolhub' "$compose_temp" || die "下载的 docker-compose.yml 内容无效"
    chmod 0755 "$script_temp"
    chmod 0644 "$compose_temp"
    mv -f -- "$compose_temp" "$install_dir/docker-compose.yml"
    mv -f -- "$script_temp" "$install_dir/deploy/quick-start.sh"
    marker_temp="$(mktemp "$install_dir/.qcontrolhub-quick-start.tmp.XXXXXX")"
    printf '%s\n' "$base_url" > "$marker_temp"
    chmod 0644 "$marker_temp"
    mv -f -- "$marker_temp" "$marker_file"
    marker_temp=""
    trap - EXIT HUP INT TERM
    persist_install_dir_preference "$install_dir" ||
        echo "警告：无法保存安装目录，下次从远程一键命令运行时可能需要重新设置目录：$install_dir" >&2
    export QCH_INSTALL_DIR="$install_dir"
    exec "$install_dir/deploy/quick-start.sh" "$@"
}

bootstrap_streamed_script "$@"

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    usage
    exit 0
fi

while getopts ':m:o:d:a:n:ft:h' opt; do
    case "$opt" in
        m) MODE="$OPTARG" ;;
        o) ACTION="$OPTARG" ;;
        d) DATABASE_URL="$OPTARG" ;;
        a) ADMIN_TOKEN="$OPTARG" ;;
        n) DOCKER_NETWORK="$OPTARG" ;;
        f) FORCE=true ;;
        t) READY_TIMEOUT="$OPTARG" ;;
        h) usage; exit 0 ;;
        :) die "选项 -$OPTARG 需要参数；使用 -h 查看帮助" ;;
        \?) die "未知选项：-$OPTARG；使用 -h 查看帮助" ;;
    esac
done
shift $((OPTIND - 1))
[ "$#" -eq 0 ] || die "不支持位置参数：$1；使用 -h 查看帮助"

case "$MODE" in
    ""|bundled|external) ;;
    *) die "未知部署模式：$MODE（可选 bundled / external）" ;;
esac
case "$ACTION" in
    ""|install|update|uninstall) ;;
    *) die "未知操作：$ACTION（可选 install / update / uninstall）" ;;
esac
if ! [[ "$READY_TIMEOUT" =~ ^[1-9][0-9]*$ ]]; then
    die "就绪检查超时时间必须是正整数"
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
WORK_DIR="$REPO_ROOT"
ENV_FILE="$REPO_ROOT/.env"
EXTERNAL_COMPOSE_FILE="$REPO_ROOT/docker-compose.external.yml"
SECRET_COMPOSE_FILE="$REPO_ROOT/docker-compose.secrets.yml"
SECRET_DIR="$REPO_ROOT/.secrets"
CONFIG_KEY_FILE="$SECRET_DIR/config-encryption-key"
PREVIOUS_CONFIG_KEYS_FILE="$SECRET_DIR/config-encryption-previous-keys"
ADMIN_TOKEN_TO_DISPLAY=""
ADMIN_TOKEN_DIGEST=""
CONFIG_KEY=""
PREVIOUS_CONFIG_KEYS=""
UPDATE_ROLLBACK_ARMED=false
UPDATE_SERVICES_CHANGED=false
UPDATE_BACKUP_DIR=""
UPDATE_HAD_SECRET_COMPOSE=false
UPDATE_CONTROL_IMAGE=""
UPDATE_CONTROL_REF=""
UPDATE_CONTROL_BACKUP_TAG=""
UPDATE_WEB_IMAGE=""
UPDATE_WEB_REF=""
UPDATE_WEB_BACKUP_TAG=""
