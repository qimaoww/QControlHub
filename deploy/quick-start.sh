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

require_commands() {
    local command_name
    for command_name in docker openssl curl awk mktemp; do
        command -v "$command_name" >/dev/null 2>&1 || die "缺少依赖：$command_name"
    done
    docker compose version >/dev/null 2>&1 || die "当前 Docker 未安装 Compose v2（docker compose）"
    docker info >/dev/null 2>&1 || die "Docker Engine 未运行，请先启动 Docker Engine"
}

random_hex() {
    openssl rand -hex 32
}

sha256_hex() {
    printf '%s' "$1" | openssl dgst -sha256 | awk '{print $NF}'
}

validate_admin_token_digest() {
    local digest="$1"
    [ "${#digest}" -eq 64 ] || die "QCH_ADMIN_TOKEN_SHA256 必须是 64 个十六进制字符"
    case "$digest" in
        *[!0-9A-Fa-f]*) die "QCH_ADMIN_TOKEN_SHA256 必须是 64 个十六进制字符" ;;
    esac
}

read_secret_file() {
    local file="$1" value
    [ -f "$file" ] || return 0
    [ ! -L "$file" ] || die "secret 文件不能是符号链接：$file"
    value="$(<"$file")"
    case "$value" in
        *$'\n'*|*$'\r'*) die "secret 文件只能包含一行：$file" ;;
    esac
    printf '%s' "$value"
}

write_secret_file() {
    local file="$1" value="$2" temp_file
    [ ! -L "$SECRET_DIR" ] || die "secret 目录不能是符号链接：$SECRET_DIR"
    mkdir -p "$SECRET_DIR"
    chmod 0700 "$SECRET_DIR"
    [ ! -L "$file" ] || die "secret 文件不能是符号链接：$file"
    umask 077
    temp_file="$(mktemp "$SECRET_DIR/.tmp.XXXXXX")"
    printf '%s\n' "$value" > "$temp_file"
    # The parent directory is private on the host. Compose bind-mounts file
    # secrets without honoring uid/gid/mode, so the non-root control-plane
    # process needs the mounted file itself to be readable.
    chmod 0644 "$temp_file"
    mv -f -- "$temp_file" "$file"
}

backup_secret_file() {
    local file="$1" backup_file
    [ -f "$file" ] || return 0
    backup_file="${file}.bak.$(date +%Y%m%d%H%M%S).$$.${RANDOM}"
    cp -p -- "$file" "$backup_file"
    chmod 0600 "$backup_file"
    echo "-> 已备份 secret：$backup_file"
}

prepare_admin_token() {
    local raw_token stored_digest legacy_token legacy_digest
    raw_token="$ADMIN_TOKEN"
    stored_digest="$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
    legacy_token="$(read_env_key QCH_ADMIN_TOKEN)"
    if [ "$FORCE" = false ] && [ -z "$raw_token" ] && [ -n "$stored_digest" ] && [ -n "$legacy_token" ]; then
        validate_secret QCH_ADMIN_TOKEN "$legacy_token"
        validate_admin_token_digest "$stored_digest"
        legacy_digest="$(sha256_hex "$legacy_token")"
        [ "$legacy_digest" = "$(printf '%s' "$stored_digest" | tr 'A-F' 'a-f')" ] || \
            die "QCH_ADMIN_TOKEN 与 QCH_ADMIN_TOKEN_SHA256 不匹配；请先确认正确的管理员 token"
    fi
    if [ "$FORCE" = true ]; then
        raw_token="${ADMIN_TOKEN:-$(random_hex)}"
    elif [ -n "$raw_token" ]; then
        :
    elif [ -n "$stored_digest" ]; then
        raw_token=""
    elif [ -n "$legacy_token" ]; then
        raw_token="$legacy_token"
    else
        raw_token="$(random_hex)"
    fi
    if [ -n "$raw_token" ]; then
        validate_secret QCH_ADMIN_TOKEN "$raw_token"
        stored_digest="$(sha256_hex "$raw_token")"
        ADMIN_TOKEN_TO_DISPLAY="$raw_token"
    else
        ADMIN_TOKEN_TO_DISPLAY=""
    fi
    validate_admin_token_digest "$stored_digest"
    ADMIN_TOKEN_DIGEST="$(printf '%s' "$stored_digest" | tr 'A-F' 'a-f')"
}

prepare_config_keyring() {
    local legacy_key legacy_previous
    CONFIG_KEY="$(read_secret_file "$CONFIG_KEY_FILE")"
    PREVIOUS_CONFIG_KEYS="$(read_secret_file "$PREVIOUS_CONFIG_KEYS_FILE")"
    legacy_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    legacy_previous="$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"
    [ -n "$CONFIG_KEY" ] || CONFIG_KEY="$legacy_key"
    [ -n "$PREVIOUS_CONFIG_KEYS" ] || PREVIOUS_CONFIG_KEYS="$legacy_previous"
    if [ "$FORCE" = true ]; then
        backup_secret_file "$CONFIG_KEY_FILE"
        backup_secret_file "$PREVIOUS_CONFIG_KEYS_FILE"
        PREVIOUS_CONFIG_KEYS="$(prepend_unique_csv "$PREVIOUS_CONFIG_KEYS" "$CONFIG_KEY")"
        CONFIG_KEY="$(random_hex)"
    elif [ -z "$CONFIG_KEY" ]; then
        CONFIG_KEY="$(random_hex)"
    fi
    validate_secret QCH_CONFIG_ENCRYPTION_KEY "$CONFIG_KEY"
    write_secret_file "$CONFIG_KEY_FILE" "$CONFIG_KEY"
    write_secret_file "$PREVIOUS_CONFIG_KEYS_FILE" "$PREVIOUS_CONFIG_KEYS"
}

read_env_key() {
    local key="$1"
    [ -f "$ENV_FILE" ] || return 0
    awk -v key="$key" '
        {
            line = $0
            sub(/^\xef\xbb\xbf/, "", line)
            if (index(line, key "=") == 1) {
                print substr(line, length(key) + 2)
                exit
            }
        }
    ' "$ENV_FILE"
}

backup_env() {
    [ -f "$ENV_FILE" ] || return 0
    local backup_file
    umask 077
    backup_file="${ENV_FILE}.bak.$(date +%Y%m%d%H%M%S).$$.${RANDOM}"
    cp -p -- "$ENV_FILE" "$backup_file"
    awk '
        /^(QCH_ADMIN_TOKEN|QCH_CONFIG_ENCRYPTION_KEY|QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)=/ {
            sub(/=.*/, "=")
        }
        { print }
    ' "$backup_file" > "${backup_file}.sanitized"
    mv -f -- "${backup_file}.sanitized" "$backup_file"
    chmod 600 "$backup_file"
    echo "-> 已备份现有 .env：$backup_file"
}

update_env_file() {
    local temp_file next_file entry key value
    umask 077
    temp_file="$(mktemp "${ENV_FILE}.tmp.XXXXXX")"
    if [ -f "$ENV_FILE" ]; then
        cp -- "$ENV_FILE" "$temp_file"
    fi

    for entry in "$@"; do
        key="${entry%%=*}"
        value="${entry#*=}"
        case "$key" in
            ''|*[!A-Za-z0-9_]*) rm -f -- "$temp_file"; die "非法环境变量名：$key" ;;
        esac
        next_file="$(mktemp "${ENV_FILE}.tmp.XXXXXX")"
        if ! awk -v key="$key" -v value="$value" '
            BEGIN { prefix = key "="; bom = "\357\273\277"; replaced = 0 }
            {
                line = $0
                line_bom = ""
                if (substr(line, 1, 3) == bom) {
                    line_bom = bom
                    line = substr(line, 4)
                }
            }
            index(line, prefix) == 1 {
                if (!replaced) { print prefix value; replaced = 1 }
                next
            }
            { print }
            END { if (!replaced) print prefix value }
        ' "$temp_file" > "$next_file"; then
            rm -f -- "$temp_file" "$next_file"
            die "更新 .env 失败"
        fi
        chmod 600 "$next_file"
        mv -f -- "$next_file" "$temp_file"
    done

    chmod 600 "$temp_file"
    mv -f -- "$temp_file" "$ENV_FILE"
}

validate_secret() {
    local name="$1" value="$2"
    if [ "${#value}" -lt 32 ]; then
        die "$name 至少需要 32 个字符"
    fi
    case "$value" in
        *$'\n'*|*$'\r'*) die "$name 不能包含换行" ;;
    esac
}

validate_database_url() {
    local url="$1"
    case "$url" in
        postgresql://*|postgres://*) ;;
        *) die "外部 PostgreSQL 连接串格式错误" ;;
    esac
    case "$url" in
        *$'\n'*|*$'\r'*) die "DATABASE_URL 不能包含换行" ;;
    esac
}

append_trusted_proxy() {
    local current="$1" cidr="$2"
    case ",$current," in
        *",$cidr,"*) printf '%s' "$current" ;;
        ",,") printf '%s' "$cidr" ;;
        *) printf '%s,%s' "$current" "$cidr" ;;
    esac
}

prepend_unique_csv() {
    local current="$1" value="$2"
    [ -n "$value" ] || { printf '%s' "$current"; return; }
    case ",$current," in
        *",$value,"*) printf '%s' "$current" ;;
        ",,") printf '%s' "$value" ;;
        *) printf '%s,%s' "$value" "$current" ;;
    esac
}

write_external_compose() {
    local docker_network config_key
    docker_network="$(read_env_key QCH_DOCKER_NETWORK)"
    config_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    cat > "$EXTERNAL_COMPOSE_FILE" <<'YAML'
name: qcontrolhub

services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:latest
    restart: unless-stopped
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL:?QCH_DATABASE_URL required}
      QCH_ADMIN_TOKEN: ${QCH_ADMIN_TOKEN:-}
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:-}
      QCH_LISTEN: 0.0.0.0:8080
      QCH_BEHIND_TLS_PROXY: ${QCH_BEHIND_TLS_PROXY:-true}
      QCH_ALLOW_INSECURE_HTTP: ${QCH_ALLOW_INSECURE_HTTP:-false}
      QCH_ALLOW_INSECURE_DATABASE: ${QCH_ALLOW_INSECURE_DATABASE:-false}
      QCH_CORS_ORIGINS: ${QCH_CORS_ORIGINS:-}
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-}
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-172.30.254.2/32,172.30.254.1/32}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_WEBHOOK_SECRET: ${QCH_WEBHOOK_SECRET:-}
YAML
    if [ -n "$config_key" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:?QCH_CONFIG_ENCRYPTION_KEY required}
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}
      QCH_OPERATOR_TOKENS: ${QCH_OPERATOR_TOKENS:-}
      QCH_AUDITOR_TOKENS: ${QCH_AUDITOR_TOKENS:-}
      QCH_READONLY_TOKENS: ${QCH_READONLY_TOKENS:-}
      QCH_AGENT_PUBLIC_IP_PROBE_ENABLED: ${QCH_AGENT_PUBLIC_IP_PROBE_ENABLED:-true}
      QCH_AGENT_PUBLIC_IP_PROBE_IPV4_ENDPOINT: ${QCH_AGENT_PUBLIC_IP_PROBE_IPV4_ENDPOINT:-}
      QCH_AGENT_PUBLIC_IP_PROBE_IPV6_ENDPOINT: ${QCH_AGENT_PUBLIC_IP_PROBE_IPV6_ENDPOINT:-}
      QCH_AGENT_PUBLIC_IP_PROBE_INTERVAL: ${QCH_AGENT_PUBLIC_IP_PROBE_INTERVAL:-5m}
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      - control-host
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      control-host:
        ipv4_address: ${QCH_CONTROL_PLANE_PROXY_ADDRESS:-172.30.254.3}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    read_only: true
    tmpfs:
      - /tmp:size=16m,mode=1777
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    pids_limit: 128
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://127.0.0.1:8080/readyz"]
      interval: 10s
      timeout: 3s
      retries: 6
      start_period: 10s
    stop_grace_period: 15s
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:latest
    restart: unless-stopped
    depends_on:
      control-plane:
        condition: service_healthy
    ports:
      - "${QCH_BIND_ADDRESS:-127.0.0.1}:${QCH_PORT:-8080}:8080"
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      - control-host
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      control-host:
        ipv4_address: ${QCH_WEB_PROXY_ADDRESS:-172.30.254.2}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    read_only: true
    tmpfs:
      - /tmp:size=8m,mode=1777
      - /var/cache/nginx:size=8m,mode=1777
      - /var/run:size=1m,mode=1777
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETGID
      - SETUID
    security_opt:
      - no-new-privileges:true
    pids_limit: 64
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://127.0.0.1:8080/healthz"]
      interval: 10s
      timeout: 3s
      retries: 6
      start_period: 5s
    stop_grace_period: 10s

networks:
  control-host:
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    name: ${QCH_DOCKER_NETWORK:?custom Docker network required}
    external: true
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    ipam:
      config:
        - subnet: ${QCH_CONTROL_PROXY_SUBNET:-172.30.254.0/24}
          gateway: ${QCH_CONTROL_PROXY_GATEWAY:-172.30.254.1}
YAML
    fi
}

write_secret_compose_override() {
    cat > "$SECRET_COMPOSE_FILE" <<'YAML'
services:
  control-plane:
    environment:
      QCH_ADMIN_TOKEN: ""
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:?administrator token digest required}
      QCH_CONFIG_ENCRYPTION_KEY: ""
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ""
      QCH_CONFIG_ENCRYPTION_KEY_FILE: /run/secrets/qch-config-encryption-key
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE: /run/secrets/qch-config-encryption-previous-keys
    volumes:
      - type: bind
        source: ${QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE:?configuration encryption key file required}
        target: /run/secrets/qch-config-encryption-key
        read_only: true
      - type: bind
        source: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE:?previous configuration encryption keys file required}
        target: /run/secrets/qch-config-encryption-previous-keys
        read_only: true
YAML
    chmod 0600 "$SECRET_COMPOSE_FILE"
}

COMPOSE_ARGS=()

run_compose() (
    # --env-file alone does not override exported shell variables. The
    # external deployment must read its own database, credentials and network.
    if [ "$MODE" = external ]; then
        local env_key
        while IFS= read -r env_key; do
            unset "$env_key"
        done < <(awk '
            { sub(/^\xef\xbb\xbf/, "") }
            /^QCH_[A-Za-z0-9_]+=/ { sub(/=.*/, ""); print }
        ' "$ENV_FILE")
        unset QCH_DATABASE_URL QCH_ADMIN_TOKEN QCH_ADMIN_TOKEN_SHA256 \
            QCH_CONFIG_ENCRYPTION_KEY QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS \
            QCH_CONFIG_ENCRYPTION_KEY_FILE QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE \
            QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE \
            QCH_ALLOW_INSECURE_DATABASE QCH_DOCKER_NETWORK QCH_BIND_ADDRESS QCH_PORT
    fi
    QCH_SOURCE_DIR="$REPO_ROOT" docker compose -p qcontrolhub --project-directory "$WORK_DIR" --env-file "$ENV_FILE" "$@"
)

compose() {
    run_compose "${COMPOSE_ARGS[@]}" "$@"
}

prepare_external_update_compose() {
    # Merge only application settings. Do not regenerate the topology: an
    # existing deployment may have its own network, ports, CA mounts, etc.
    cat > "$UPDATE_BACKUP_DIR/docker-compose.update.yml" <<'YAML'
services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:latest
    restart: unless-stopped
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL:?QCH_DATABASE_URL required}
      QCH_ADMIN_TOKEN: ${QCH_ADMIN_TOKEN:-}
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:-}
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}
      QCH_ALLOW_INSECURE_DATABASE: ${QCH_ALLOW_INSECURE_DATABASE:-false}
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:latest
    restart: unless-stopped
YAML
    # Keep variable references, not rendered credentials, in the persisted
    # Compose. The existing secret override remains a separate unmodified file.
    run_compose -f "$EXTERNAL_COMPOSE_FILE" -f "$UPDATE_BACKUP_DIR/docker-compose.update.yml" \
        config --no-interpolate --no-normalize --no-path-resolution \
        > "$UPDATE_BACKUP_DIR/docker-compose.next.yml" || die "无法保留现有 Compose 配置"
    [ -s "$UPDATE_BACKUP_DIR/docker-compose.next.yml" ] || die "生成的 Compose 配置为空"
    chmod 0600 "$UPDATE_BACKUP_DIR/docker-compose.next.yml"
    mv -f -- "$UPDATE_BACKUP_DIR/docker-compose.next.yml" "$EXTERNAL_COMPOSE_FILE"
}

show_diagnostics() {
    echo "-> 最近的 Compose 状态："
    compose ps || true
    echo "-> 最近的控制面日志："
    compose logs --tail=80 control-plane || true
    if [ "$MODE" = "bundled" ]; then
        echo "-> 最近的 PostgreSQL 日志："
        compose logs --tail=40 postgres || true
    fi
    echo "-> 最近的 SPA 日志："
    compose logs --tail=40 qcontrol-web || true
}

start_services() {
    echo "-> 校验 Docker Compose 配置"
    compose config --quiet || die "Docker Compose 配置无效，请检查 .env 和连接参数"
    if [ "$(read_env_key QCH_IMAGE_TAG)" = "local" ]; then
        echo "-> 构建并启动本地 Docker 镜像"
        if ! compose up -d --build; then
            show_diagnostics
            die "Docker Compose 启动失败"
        fi
    else
        echo "-> 拉取 GHCR 镜像并启动 Docker Compose"
        if ! compose pull || ! compose up -d; then
            show_diagnostics
            die "Docker Compose 启动失败；请确认已登录 ghcr.io 且镜像标签存在"
        fi
    fi
}

start_external_services() {
    echo "-> 拉取外部 PostgreSQL 部署的 latest 应用镜像"
    docker pull ghcr.io/qimaoww/qcontrol-plane:latest
    docker pull ghcr.io/qimaoww/qcontrol-web:latest
    echo "-> 校验 Docker Compose 配置"
    compose config --quiet || die "Docker Compose 配置无效，请检查 .env 和连接参数"
    if ! compose up -d --force-recreate --no-build --pull never --no-deps control-plane qcontrol-web; then
        show_diagnostics
        die "Docker Compose 启动失败；请确认已登录 ghcr.io 且镜像可用"
    fi
}

wait_ready() {
    local url="$1" timeout="$2" deadline
    deadline=$(( $(date +%s) + timeout ))
    while [ "$(date +%s)" -lt "$deadline" ]; do
        if curl -sf -m 3 "$url" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

validate_external_update_env() {
    local qch_database_url
    [ -f "$ENV_FILE" ] || die "未找到现有部署配置：$ENV_FILE"
    qch_database_url="$(read_env_key QCH_DATABASE_URL)"
    test -n "${qch_database_url:-}" || {
        echo "缺少原外部 PostgreSQL 连接串，终止更新" >&2
        exit 1
    }

    case "$qch_database_url" in
        postgresql://*|postgres://*) ;;
        *)
            echo "外部 PostgreSQL 连接串格式错误" >&2
            exit 1
            ;;
    esac

    validate_external_config_key_source
    validate_external_compose_database_boundary
}

validate_external_compose_database_boundary() {
    [ -f "$EXTERNAL_COMPOSE_FILE" ] || die "未找到外部部署配置：$EXTERNAL_COMPOSE_FILE"
    if awk '
        /^services:[[:space:]]*$/ { in_services = 1; next }
        in_services && /^[^[:space:]]/ { in_services = 0 }
        in_services && /^  postgres:[[:space:]]*$/ { found = 1 }
        END { exit found ? 0 : 1 }
    ' "$EXTERNAL_COMPOSE_FILE" || \
        grep -Eq '^[[:space:]]{4}depends_on:[[:space:]]*postgres|^[[:space:]]{6}postgres:[[:space:]]*$|postgres-data|127\.0\.0\.1:5432' "$EXTERNAL_COMPOSE_FILE"; then
        die "外部 PostgreSQL Compose 包含本地数据库服务、数据卷、依赖或连接地址"
    fi
}

validate_external_config_key_source() {
    local config_key config_source previous_source
    config_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    if [ -n "$config_key" ]; then
        validate_secret QCH_CONFIG_ENCRYPTION_KEY "$config_key"
        return 0
    fi

    config_source="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE)"
    previous_source="$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE)"
    [ -n "$config_source" ] && [ -n "$previous_source" ] && [ -f "$SECRET_COMPOSE_FILE" ] || \
        die "缺少原 QCH_CONFIG_ENCRYPTION_KEY 或既有 secret keyring，终止更新"
    case "$config_source" in
        /*) ;;
        *) config_source="$WORK_DIR/$config_source" ;;
    esac
    case "$previous_source" in
        /*) ;;
        *) previous_source="$WORK_DIR/$previous_source" ;;
    esac
    [ -f "$config_source" ] && [ -f "$previous_source" ] || \
        die "原配置加密 secret keyring 文件不完整，终止更新"
}

validate_external_network() {
    local network
    network="$(read_env_key QCH_DOCKER_NETWORK)"
    [ -n "$network" ] || return 0
    case "$network" in
        [A-Za-z0-9]*) ;;
        *) die "Docker 网络名称必须以字母或数字开头" ;;
    esac
    case "$network" in
        *[!A-Za-z0-9_.-]*) die "Docker 网络名称只能包含字母、数字、点、下划线和短横线" ;;
    esac
    docker network inspect -- "$network" >/dev/null 2>&1 || \
        die "自定义 Docker 网络不存在：$network"
}

update_container_id() {
    local service="$1" ids count
    ids="$(compose ps -q "$service")"
    count="$(printf '%s\n' "$ids" | awk 'NF { count++ } END { print count + 0 }')"
    [ "$count" -eq 1 ] || die "更新前必须有且仅有一个正在运行的 $service 容器"
    printf '%s' "$ids"
}

capture_update_image() {
    local service="$1" container_id image_id image_ref backup_tag
    container_id="$(update_container_id "$service")"
    image_id="$(docker inspect --format '{{.Image}}' "$container_id")"
    image_ref="$(docker inspect --format '{{.Config.Image}}' "$container_id")"
    [ -n "$image_id" ] && [ -n "$image_ref" ] || die "无法读取 $service 当前镜像"
    backup_tag="qcontrolhub-update-rollback/$service:$(date +%Y%m%d%H%M%S)-$$"
    docker image tag "$image_id" "$backup_tag" || die "无法保存 $service 当前镜像"
    case "$service" in
        control-plane)
            UPDATE_CONTROL_IMAGE="$image_id"
            UPDATE_CONTROL_REF="$image_ref"
            UPDATE_CONTROL_BACKUP_TAG="$backup_tag"
            ;;
        qcontrol-web)
            UPDATE_WEB_IMAGE="$image_id"
            UPDATE_WEB_REF="$image_ref"
            UPDATE_WEB_BACKUP_TAG="$backup_tag"
            ;;
    esac
}

begin_external_update() {
    [ -f "$EXTERNAL_COMPOSE_FILE" ] || die "未找到外部部署配置：$EXTERNAL_COMPOSE_FILE"
    [ ! -L "$ENV_FILE" ] || die ".env 不能是符号链接：$ENV_FILE"
    [ ! -L "$EXTERNAL_COMPOSE_FILE" ] || die "外部 Compose 不能是符号链接：$EXTERNAL_COMPOSE_FILE"

    UPDATE_SERVICES_CHANGED=false
    UPDATE_HAD_SECRET_COMPOSE=false
    UPDATE_CONTROL_IMAGE=""
    UPDATE_CONTROL_REF=""
    UPDATE_CONTROL_BACKUP_TAG=""
    UPDATE_WEB_IMAGE=""
    UPDATE_WEB_REF=""
    UPDATE_WEB_BACKUP_TAG=""
    umask 077
    UPDATE_BACKUP_DIR="$(mktemp -d "$WORK_DIR/.qcontrolhub-update.XXXXXX")"
    cp -p -- "$ENV_FILE" "$UPDATE_BACKUP_DIR/.env"
    cp -p -- "$EXTERNAL_COMPOSE_FILE" "$UPDATE_BACKUP_DIR/docker-compose.external.yml"
    if [ -z "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)" ] && [ -f "$SECRET_COMPOSE_FILE" ]; then
        cp -p -- "$SECRET_COMPOSE_FILE" "$UPDATE_BACKUP_DIR/docker-compose.secrets.yml"
        UPDATE_HAD_SECRET_COMPOSE=true
    fi
    UPDATE_ROLLBACK_ARMED=true
    capture_update_image control-plane
    capture_update_image qcontrol-web
}

restore_update_image_ref() {
    local image_id="$1" image_ref="$2" backup_tag="$3"
    if [ -z "$image_id" ] || [ -z "$image_ref" ]; then
        printf '%s' "$backup_tag"
        return 0
    fi
    case "$image_ref" in
        *@sha256:*|sha256:*) printf '%s' "$backup_tag" ;;
        *)
            if docker image tag "$image_id" "$image_ref"; then
                printf '%s' "$image_ref"
            else
                printf '%s' "$backup_tag"
            fi
            ;;
    esac
}

cleanup_external_update_backup() {
    [ -z "$UPDATE_CONTROL_BACKUP_TAG" ] || docker image rm "$UPDATE_CONTROL_BACKUP_TAG" >/dev/null 2>&1 || true
    [ -z "$UPDATE_WEB_BACKUP_TAG" ] || docker image rm "$UPDATE_WEB_BACKUP_TAG" >/dev/null 2>&1 || true
    if [ -n "$UPDATE_BACKUP_DIR" ] && [ -d "$UPDATE_BACKUP_DIR" ]; then
        rm -f -- \
            "$UPDATE_BACKUP_DIR/.env" \
            "$UPDATE_BACKUP_DIR/docker-compose.external.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.secrets.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.update.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.next.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.rollback.yml"
        rmdir -- "$UPDATE_BACKUP_DIR" 2>/dev/null || true
    fi
    UPDATE_BACKUP_DIR=""
}

rollback_external_update() {
    local -a rollback_args
    [ "$UPDATE_ROLLBACK_ARMED" = true ] || return 0
    UPDATE_ROLLBACK_ARMED=false
    echo "-> 更新失败，正在恢复旧配置和旧镜像" >&2

    if ! cp -p -- "$UPDATE_BACKUP_DIR/.env" "$ENV_FILE" || \
        ! cp -p -- "$UPDATE_BACKUP_DIR/docker-compose.external.yml" "$EXTERNAL_COMPOSE_FILE"; then
        echo "错误：旧配置恢复失败；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
        return 1
    fi
    if [ "$UPDATE_HAD_SECRET_COMPOSE" = true ]; then
        if ! cp -p -- "$UPDATE_BACKUP_DIR/docker-compose.secrets.yml" "$SECRET_COMPOSE_FILE"; then
            echo "错误：旧 secret 配置恢复失败；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
            return 1
        fi
    fi

    restore_update_image_ref "$UPDATE_CONTROL_IMAGE" "$UPDATE_CONTROL_REF" "$UPDATE_CONTROL_BACKUP_TAG" >/dev/null
    restore_update_image_ref "$UPDATE_WEB_IMAGE" "$UPDATE_WEB_REF" "$UPDATE_WEB_BACKUP_TAG" >/dev/null
    if [ "$UPDATE_SERVICES_CHANGED" = true ]; then
        # Immutable IDs plus --pull never are required even when the old
        # Compose used latest with pull_policy: always.
        if ! cat > "$UPDATE_BACKUP_DIR/docker-compose.rollback.yml" <<YAML
services:
  control-plane:
    image: $UPDATE_CONTROL_IMAGE
  qcontrol-web:
    image: $UPDATE_WEB_IMAGE
YAML
        then
            echo "错误：无法写入回滚镜像配置；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
            return 1
        fi
        rollback_args=(-f "$EXTERNAL_COMPOSE_FILE")
        if [ "$UPDATE_HAD_SECRET_COMPOSE" = true ]; then
            rollback_args+=(-f "$SECRET_COMPOSE_FILE")
        fi
        rollback_args+=(-f "$UPDATE_BACKUP_DIR/docker-compose.rollback.yml")
        if ! run_compose "${rollback_args[@]}" config --quiet || \
            ! run_compose "${rollback_args[@]}" up -d --force-recreate --no-build --pull never --no-deps control-plane qcontrol-web; then
            echo "错误：旧部署恢复失败；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
            return 1
        fi
        if ! check_external_endpoints "$(local_panel_url)"; then
            echo "错误：原版本容器已恢复，但健康检查仍未通过；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
            return 1
        fi
    fi

    cleanup_external_update_backup
    echo "-> 已恢复旧配置，原版本容器继续运行" >&2
}

external_update_exit_handler() {
    local status=$?
    trap - EXIT HUP INT TERM
    if [ "$status" -ne 0 ] && [ "$UPDATE_ROLLBACK_ARMED" = true ]; then
        rollback_external_update || status=1
    fi
    exit "$status"
}

check_external_endpoints() {
    local panel_url="$1"
    echo "-> 检查 $panel_url/healthz"
    wait_ready "$panel_url/healthz" "$READY_TIMEOUT" || return 1
    echo "-> 检查 $panel_url/readyz"
    wait_ready "$panel_url/readyz" "$READY_TIMEOUT"
}

update_external_services() (
    local panel_url
    trap external_update_exit_handler EXIT
    trap 'exit 130' HUP INT TERM
    validate_external_network
    panel_url="$(local_panel_url)"
    check_external_endpoints "$panel_url" || die "更新前原部署未通过 healthz 和 readyz 检查"
    begin_external_update

    echo "-> 保留现有拓扑并更新 $EXTERNAL_COMPOSE_FILE 中的应用配置"
    prepare_external_update_compose
    cmp -s "$UPDATE_BACKUP_DIR/.env" "$ENV_FILE" || die "更新过程改写了 .env，已终止"
    docker pull ghcr.io/qimaoww/qcontrol-plane:latest || die "拉取 control-plane:latest 失败"
    docker pull ghcr.io/qimaoww/qcontrol-web:latest || die "拉取 qcontrol-web:latest 失败"

    compose config --quiet || die "Docker Compose 配置校验失败"
    UPDATE_SERVICES_CHANGED=true
    if ! compose up -d --force-recreate --no-build --pull never --no-deps control-plane qcontrol-web; then
        show_diagnostics
        die "使用新镜像重建应用容器失败"
    fi

    if ! check_external_endpoints "$panel_url"; then
        show_diagnostics
        die "应用未在 ${READY_TIMEOUT} 秒内通过 healthz 和 readyz 检查"
    fi
    cmp -s "$UPDATE_BACKUP_DIR/.env" "$ENV_FILE" || die "更新过程改写了 .env，已终止"

    UPDATE_ROLLBACK_ARMED=false
    trap - EXIT HUP INT TERM
    cleanup_external_update_backup
)

prepare_bundled_env() {
    local postgres_password webhook_secret
    local behind_proxy allow_http allow_database cors_origins bind_address port image_tag version
    local proxy_subnet proxy_gateway web_proxy_address control_plane_proxy_address trusted_proxy_cidrs

    if [ "$FORCE" = true ]; then
        backup_env
    fi

    postgres_password="$(read_env_key POSTGRES_PASSWORD)"
    [ -n "$postgres_password" ] || postgres_password="$(random_hex)"

    prepare_admin_token
    webhook_secret="$(read_env_key QCH_WEBHOOK_SECRET)"
    if [ "$FORCE" = true ] || [ -z "$webhook_secret" ]; then
        webhook_secret="$(random_hex)"
    fi
    prepare_config_keyring

    behind_proxy="$(read_env_key QCH_BEHIND_TLS_PROXY)"; [ -n "$behind_proxy" ] || behind_proxy=true
    allow_http="$(read_env_key QCH_ALLOW_INSECURE_HTTP)"; [ -n "$allow_http" ] || allow_http=false
    allow_database="$(read_env_key QCH_ALLOW_INSECURE_DATABASE)"; [ -n "$allow_database" ] || allow_database=true
    cors_origins="$(read_env_key QCH_CORS_ORIGINS)"
    bind_address="$(read_env_key QCH_BIND_ADDRESS)"; [ -n "$bind_address" ] || bind_address=127.0.0.1
    port="$(read_env_key QCH_PORT)"; [ -n "$port" ] || port=8080
    image_tag="$(read_env_key QCH_IMAGE_TAG)"; [ -n "$image_tag" ] || image_tag=latest
    version="$(read_env_key VERSION)"; [ -n "$version" ] || version=dev
    proxy_subnet="$(read_env_key QCH_CONTROL_PROXY_SUBNET)"; [ -n "$proxy_subnet" ] || proxy_subnet=172.30.254.0/24
    proxy_gateway="$(read_env_key QCH_CONTROL_PROXY_GATEWAY)"; [ -n "$proxy_gateway" ] || proxy_gateway=172.30.254.1
    web_proxy_address="$(read_env_key QCH_WEB_PROXY_ADDRESS)"; [ -n "$web_proxy_address" ] || web_proxy_address=172.30.254.2
    control_plane_proxy_address="$(read_env_key QCH_CONTROL_PLANE_PROXY_ADDRESS)"; [ -n "$control_plane_proxy_address" ] || control_plane_proxy_address=172.30.254.3
    trusted_proxy_cidrs="$(read_env_key QCH_TRUSTED_PROXY_CIDRS)"
    trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$web_proxy_address/32")"
    trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$proxy_gateway/32")"

    local postgres_db postgres_user postgres_port
    postgres_db="$(read_env_key POSTGRES_DB)"; [ -n "$postgres_db" ] || postgres_db=qcontrolhub
    postgres_user="$(read_env_key POSTGRES_USER)"; [ -n "$postgres_user" ] || postgres_user=qcontrolhub
    postgres_port="$(read_env_key POSTGRES_PORT)"; [ -n "$postgres_port" ] || postgres_port=5432

    update_env_file \
        "POSTGRES_DB=$postgres_db" \
        "POSTGRES_USER=$postgres_user" \
        "POSTGRES_PASSWORD=$postgres_password" \
        "POSTGRES_PORT=$postgres_port" \
        "QCH_ADMIN_TOKEN=" \
        "QCH_ADMIN_TOKEN_SHA256=$ADMIN_TOKEN_DIGEST" \
        "QCH_WEBHOOK_SECRET=$webhook_secret" \
        "QCH_CONFIG_ENCRYPTION_KEY=" \
        "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=" \
        "QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE=.secrets/config-encryption-key" \
        "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE=.secrets/config-encryption-previous-keys" \
        "QCH_BEHIND_TLS_PROXY=$behind_proxy" \
        "QCH_ALLOW_INSECURE_HTTP=$allow_http" \
        "QCH_ALLOW_INSECURE_DATABASE=$allow_database" \
        "QCH_CORS_ORIGINS=$cors_origins" \
        "QCH_CONTROL_PROXY_SUBNET=$proxy_subnet" \
        "QCH_CONTROL_PROXY_GATEWAY=$proxy_gateway" \
        "QCH_WEB_PROXY_ADDRESS=$web_proxy_address" \
        "QCH_CONTROL_PLANE_PROXY_ADDRESS=$control_plane_proxy_address" \
        "QCH_TRUSTED_PROXY_CIDRS=$trusted_proxy_cidrs" \
        "QCH_BIND_ADDRESS=$bind_address" \
        "QCH_PORT=$port" \
        "QCH_IMAGE_TAG=$image_tag" \
        "VERSION=$version"
}

prepare_external_env() {
    local db_url webhook_secret env_existed existing_admin_token existing_admin_digest
    local docker_network existing_network
    local behind_proxy allow_http allow_database cors_origins bind_address port
    local proxy_subnet proxy_gateway web_proxy_address control_plane_proxy_address trusted_proxy_cidrs
    local -a secret_sources=()

    [ -f "$ENV_FILE" ] && env_existed=true || env_existed=false
    db_url="$(read_env_key QCH_DATABASE_URL)"
    if [ -n "$DATABASE_URL" ]; then
        if [ -n "$db_url" ] && [ "$DATABASE_URL" != "$db_url" ]; then
            die "拒绝覆盖现有 QCH_DATABASE_URL；更新数据库连接请先人工确认并修改 .env"
        fi
        db_url="$DATABASE_URL"
    fi
    if [ -z "$db_url" ]; then
        [ -t 0 ] || die "缺少 DATABASE_URL；非交互模式请使用 -d 传入"
        echo ""
        echo "请输入外部 PostgreSQL 连接串"
        echo "示例：postgresql://user:pass@db.example.com:5432/qcontrolhub?sslmode=verify-full"
        echo ""
        read -r -p "DATABASE_URL: " db_url
    fi
    [ -n "$db_url" ] || die "DATABASE_URL 不能为空"
    validate_database_url "$db_url"

    existing_network="$(read_env_key QCH_DOCKER_NETWORK)"
    docker_network="$existing_network"
    if [ -n "$DOCKER_NETWORK" ]; then
        if [ -n "$existing_network" ] && [ "$DOCKER_NETWORK" != "$existing_network" ]; then
            die "拒绝覆盖现有 QCH_DOCKER_NETWORK；请先人工确认并修改 .env"
        fi
        docker_network="$DOCKER_NETWORK"
    elif [ "$env_existed" = false ] && [ -t 0 ]; then
        echo ""
        echo "请输入已有 Docker 网络名称（直接回车使用 Compose 默认网络）"
        read -r docker_network
    fi
    case "$docker_network" in
        "") ;;
        [A-Za-z0-9]* )
            case "$docker_network" in
                *[!A-Za-z0-9_.-]*) die "Docker 网络名称只能包含字母、数字、点、下划线和短横线" ;;
            esac
            ;;
        *) die "Docker 网络名称必须以字母或数字开头" ;;
    esac

    if [ "$FORCE" = true ]; then
        die "外部 PostgreSQL 部署不支持 -f；不会轮换管理员令牌或配置加密密钥"
    fi

    existing_admin_token="$(read_env_key QCH_ADMIN_TOKEN)"
    existing_admin_digest="$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
    if [ -n "$ADMIN_TOKEN" ] && [ -n "$existing_admin_token$existing_admin_digest" ]; then
        if [ -n "$existing_admin_token" ]; then
            [ "$ADMIN_TOKEN" = "$existing_admin_token" ] || \
                die "拒绝覆盖现有 QCH_ADMIN_TOKEN；外部部署不会轮换管理员令牌"
        else
            [ "$(sha256_hex "$ADMIN_TOKEN")" = "$(printf '%s' "$existing_admin_digest" | tr 'A-F' 'a-f')" ] || \
                die "-a 与现有 QCH_ADMIN_TOKEN_SHA256 不匹配；拒绝轮换管理员令牌"
        fi
    fi
    if [ "$env_existed" = true ] && [ -z "$existing_admin_token" ] && [ -z "$existing_admin_digest" ]; then
        die "现有外部部署缺少管理员令牌配置；拒绝自动生成并覆盖"
    fi
    prepare_admin_token
    webhook_secret="$(read_env_key QCH_WEBHOOK_SECRET)"
    if [ -z "$webhook_secret" ]; then
        webhook_secret="$(random_hex)"
    fi
    CONFIG_KEY="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    PREVIOUS_CONFIG_KEYS="$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"
    if [ "$env_existed" = false ]; then
        # Keep the original fresh-install secret-file workflow. Existing
        # deployments retain their original key source without conversion.
        prepare_config_keyring
        CONFIG_KEY=""
        PREVIOUS_CONFIG_KEYS=""
        secret_sources=(
            "QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE=.secrets/config-encryption-key"
            "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE=.secrets/config-encryption-previous-keys"
        )
    elif [ -z "$CONFIG_KEY" ]; then
        validate_external_config_key_source
    else
        validate_secret QCH_CONFIG_ENCRYPTION_KEY "$CONFIG_KEY"
    fi

    behind_proxy="$(read_env_key QCH_BEHIND_TLS_PROXY)"; [ -n "$behind_proxy" ] || behind_proxy=true
    allow_http="$(read_env_key QCH_ALLOW_INSECURE_HTTP)"; [ -n "$allow_http" ] || allow_http=false
    allow_database="$(read_env_key QCH_ALLOW_INSECURE_DATABASE)"; [ -n "$allow_database" ] || allow_database=false
    cors_origins="$(read_env_key QCH_CORS_ORIGINS)"
    bind_address="$(read_env_key QCH_BIND_ADDRESS)"; [ -n "$bind_address" ] || bind_address=127.0.0.1
    port="$(read_env_key QCH_PORT)"; [ -n "$port" ] || port=8080
    proxy_subnet="$(read_env_key QCH_CONTROL_PROXY_SUBNET)"; [ -n "$proxy_subnet" ] || proxy_subnet=172.30.254.0/24
    proxy_gateway="$(read_env_key QCH_CONTROL_PROXY_GATEWAY)"; [ -n "$proxy_gateway" ] || proxy_gateway=172.30.254.1
    web_proxy_address="$(read_env_key QCH_WEB_PROXY_ADDRESS)"; [ -n "$web_proxy_address" ] || web_proxy_address=172.30.254.2
    control_plane_proxy_address="$(read_env_key QCH_CONTROL_PLANE_PROXY_ADDRESS)"; [ -n "$control_plane_proxy_address" ] || control_plane_proxy_address=172.30.254.3
    trusted_proxy_cidrs="$(read_env_key QCH_TRUSTED_PROXY_CIDRS)"
    if [ -z "$docker_network" ]; then
        trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$web_proxy_address/32")"
        trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$proxy_gateway/32")"
    fi

    update_env_file \
        "QCH_DATABASE_URL=$db_url" \
        "QCH_ADMIN_TOKEN=$existing_admin_token" \
        "QCH_ADMIN_TOKEN_SHA256=$ADMIN_TOKEN_DIGEST" \
        "QCH_WEBHOOK_SECRET=$webhook_secret" \
        "QCH_CONFIG_ENCRYPTION_KEY=$CONFIG_KEY" \
        "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=$PREVIOUS_CONFIG_KEYS" \
        "QCH_BEHIND_TLS_PROXY=$behind_proxy" \
        "QCH_ALLOW_INSECURE_HTTP=$allow_http" \
        "QCH_ALLOW_INSECURE_DATABASE=$allow_database" \
        "QCH_CORS_ORIGINS=$cors_origins" \
        "QCH_CONTROL_PROXY_SUBNET=$proxy_subnet" \
        "QCH_CONTROL_PROXY_GATEWAY=$proxy_gateway" \
        "QCH_WEB_PROXY_ADDRESS=$web_proxy_address" \
        "QCH_CONTROL_PLANE_PROXY_ADDRESS=$control_plane_proxy_address" \
        "QCH_TRUSTED_PROXY_CIDRS=$trusted_proxy_cidrs" \
        "QCH_BIND_ADDRESS=$bind_address" \
        "QCH_PORT=$port" \
        "QCH_DOCKER_NETWORK=$docker_network" \
        "${secret_sources[@]}"
    if [ "$env_existed" = false ]; then
        write_secret_compose_override
    fi
}

show_result() {
    local result_name="$1" url="$2" stop_cmd="$3"
    echo ""
    echo "============================================"
    echo "  QControlHub $result_name"
    echo "============================================"
    echo ""
    echo "  访问地址：  $url"
    echo "  管理员 token：请使用密码管理器中保存的原文"
    echo "  配置文件：  $ENV_FILE"
    [ -d "$SECRET_DIR" ] && echo "  既有密钥目录：$SECRET_DIR"
    echo ""
    echo "  停止服务：  $stop_cmd"
    echo "  查看日志：  ${stop_cmd/down/logs -f}"
    echo ""
}

local_panel_url() {
    local port address
    port="$(read_env_key QCH_PORT)"
    [ -n "$port" ] || port=8080
    address="$(read_env_key QCH_BIND_ADDRESS)"
    case "$address" in
        ""|0.0.0.0) address=127.0.0.1 ;;
        ::|\[::\]) address='[::1]' ;;
        \[*\]) ;;
        *:*) address="[$address]" ;;
    esac
    printf 'http://%s:%s' "$address" "$port"
}

show_admin_token_once() {
    [ -n "$ADMIN_TOKEN_TO_DISPLAY" ] || return 0
    echo ""
    echo "============================================"
    echo "  管理员 token（仅本次显示）"
    echo "  $ADMIN_TOKEN_TO_DISPLAY"
    echo ""
    echo "  请立即保存到密码管理器。"
    echo "  .env 只保存 SHA-256 摘要，之后无法恢复原文。"
    echo "============================================"
    echo ""
    ADMIN_TOKEN_TO_DISPLAY=""
}

resolve_work_dir() {
    if [ -z "${QCH_INSTALL_DIR:-}" ]; then
        WORK_DIR="$REPO_ROOT"
    else
        case "$QCH_INSTALL_DIR" in
            /*) WORK_DIR="$QCH_INSTALL_DIR" ;;
            *) WORK_DIR="$REPO_ROOT/$QCH_INSTALL_DIR" ;;
        esac
    fi
    mkdir -p "$WORK_DIR" || die "无法创建目录：$WORK_DIR"
    WORK_DIR="$(cd "$WORK_DIR" && pwd)" || die "无法进入目录：$WORK_DIR"
    cd "$WORK_DIR"
    ENV_FILE="$WORK_DIR/.env"
    EXTERNAL_COMPOSE_FILE="$WORK_DIR/docker-compose.external.yml"
    SECRET_COMPOSE_FILE="$WORK_DIR/docker-compose.secrets.yml"
    SECRET_DIR="$WORK_DIR/.secrets"
    CONFIG_KEY_FILE="$SECRET_DIR/config-encryption-key"
    PREVIOUS_CONFIG_KEYS_FILE="$SECRET_DIR/config-encryption-previous-keys"
    persist_install_dir_preference "$WORK_DIR" ||
        echo "警告：无法保存安装目录，下次从远程一键命令运行时可能需要重新设置目录：$WORK_DIR" >&2
}

choose_install_dir() {
    local input_dir
    echo ""
    echo "当前目录：$WORK_DIR"
    echo "请输入新的目录（直接回车保持不变）："
    read -r input_dir
    case "$input_dir" in
        "") ;;
        *) QCH_INSTALL_DIR="$input_dir"; resolve_work_dir ;;
    esac
}

choose_action() {
    [ -t 0 ] || die "未指定操作；非交互模式请使用 -o install、-o update 或 -o uninstall"
    while :; do
        echo ""
        echo "QControlHub 管理菜单"
        echo ""
        echo "  1. 安装 / 重新配置"
        echo "  2. 更新现有部署"
        echo "  3. 卸载服务（保留配置、密钥和数据库卷）"
        echo "  4. 设置目录"
        echo ""
        read -r -p "请选择 [1-4] " choice
        case "$choice" in
            1) ACTION="install"; return ;;
            2) ACTION="update"; return ;;
            3) ACTION="uninstall"; return ;;
            4) choose_install_dir ;;
            *) echo "无效选择：$choice" ;;
        esac
    done
}

choose_mode() {
    [ -t 0 ] || die "未指定部署模式；非交互模式请使用 -m bundled 或 -m external"
    echo ""
    echo "QControlHub 数据库模式"
    echo ""
    echo "  1. 内置 PostgreSQL + 控制面（推荐）"
    echo "  2. 连接外部 PostgreSQL"
    echo ""
    read -r -p "请选择 [1-2] " choice
    case "$choice" in
        1) MODE="bundled" ;;
        2) MODE="external" ;;
        *) die "无效选择：$choice" ;;
    esac
}

detect_existing_mode() {
    [ -f "$ENV_FILE" ] || die "未找到现有部署配置：$ENV_FILE"
    if [ -n "$(read_env_key QCH_DATABASE_URL)" ] || [ -f "$EXTERNAL_COMPOSE_FILE" ]; then
        MODE="external"
        if [ "$ACTION" = "uninstall" ] && [ ! -f "$EXTERNAL_COMPOSE_FILE" ]; then
            die "外部 PostgreSQL 部署缺少 $EXTERNAL_COMPOSE_FILE"
        fi
    else
        MODE="bundled"
    fi
}

configure_compose_args() {
    case "$MODE" in
        bundled)
            COMPOSE_ARGS=(-f "$REPO_ROOT/docker-compose.yml")
            if [ "$ACTION" != "uninstall" ] || [ -f "$SECRET_COMPOSE_FILE" ]; then
                COMPOSE_ARGS+=(-f "$SECRET_COMPOSE_FILE")
            fi
            ;;
        external)
            COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE")
            if [ -z "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)" ] && [ -f "$SECRET_COMPOSE_FILE" ]; then
                COMPOSE_ARGS+=(-f "$SECRET_COMPOSE_FILE")
            fi
            ;;
    esac
}

uninstall_services() {
    echo "-> 停止并移除 QControlHub 服务容器和网络"
    compose down --remove-orphans
    echo ""
    echo "============================================"
    echo "  QControlHub 服务已卸载"
    echo "============================================"
    echo ""
    echo "  已保留配置：$ENV_FILE"
    [ -d "$SECRET_DIR" ] && echo "  已保留密钥：$SECRET_DIR"
    if [ "$MODE" = "bundled" ]; then
        echo "  已保留数据：Docker PostgreSQL 命名卷"
    else
        echo "  外部 PostgreSQL 数据未被修改"
    fi
    echo ""
}

prepare_action_and_work_dir() {
    # The directory selector is part of the action menu, so resolve the saved
    # work directory first. Otherwise option 4 displays the runtime script
    # directory even though QCH_INSTALL_DIR has already been restored.
    resolve_work_dir
    if [ -z "$ACTION" ]; then
        if [ -n "$MODE" ]; then
            ACTION="install"
        else
            choose_action
        fi
    fi
}

# Keep the environment preparation functions sourceable for the isolated shell
# regression without running Docker or mutating the caller's deployment.
if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then
    return 0
fi

# ---- 选择管理操作和部署方式 ----
prepare_action_and_work_dir
if [ "$ACTION" = "install" ]; then
    [ -n "$MODE" ] || choose_mode
else
    [ -n "$MODE" ] || detect_existing_mode
fi
if [ "$ACTION" = "uninstall" ] && [ "$FORCE" = true ]; then
    die "卸载操作不支持 -f；默认始终保留配置、密钥和数据库卷"
fi
configure_compose_args

require_commands

# ---- 执行管理操作 ----
if [ "$ACTION" = "uninstall" ]; then
    uninstall_services
    exit 0
fi

case "$MODE" in
    bundled)
        if [ "$ACTION" = "update" ]; then
            echo "-> 更新内置 PostgreSQL 部署并复用现有配置"
        elif [ -f "$ENV_FILE" ] && [ "$FORCE" = false ]; then
            echo "-> 复用已有 .env，并补齐缺失配置"
        elif [ "$FORCE" = true ]; then
            echo "-> 轮换应用密钥（保留 PostgreSQL 密码）"
        else
            echo "-> 生成部署配置写入 .env"
        fi
        prepare_bundled_env
        show_admin_token_once
        write_secret_compose_override
        start_services

        panel_url="$(local_panel_url)"
        echo "-> 等待控制面就绪..."
        if ! wait_ready "$panel_url/readyz" "$READY_TIMEOUT"; then
            show_diagnostics
            die "控制面未在 ${READY_TIMEOUT} 秒内就绪"
        fi

        [ "$ACTION" = "update" ] && result_name="更新完成" || result_name="部署完成"
        show_result "$result_name" "$panel_url" "docker compose --project-directory $WORK_DIR --env-file $ENV_FILE -f $REPO_ROOT/docker-compose.yml -f $SECRET_COMPOSE_FILE down"
        ;;
    external)
        if [ "$ACTION" = "update" ]; then
            echo "-> 更新外部 PostgreSQL 部署并复用现有配置"
            [ -z "$DATABASE_URL" ] || die "更新禁止使用 -d；QCH_DATABASE_URL 必须原样复用"
            [ -z "$ADMIN_TOKEN" ] || die "更新禁止使用 -a；管理员令牌不得轮换"
            [ -z "$DOCKER_NETWORK" ] || die "更新禁止使用 -n；Docker 网络配置必须原样复用"
            [ "$FORCE" = false ] || die "外部 PostgreSQL 更新不支持 -f；令牌和配置加密密钥不得轮换"
            validate_external_update_env
            update_external_services
        elif [ -f "$ENV_FILE" ] && [ "$FORCE" = false ]; then
            echo "-> 复用已有 .env，并补齐缺失配置"
            prepare_external_env
            show_admin_token_once
            configure_compose_args

            echo "-> 生成 $EXTERNAL_COMPOSE_FILE"
            write_external_compose
            validate_external_network
            start_external_services
        elif [ "$FORCE" = true ]; then
            die "外部 PostgreSQL 部署不支持 -f；不会轮换管理员令牌或配置加密密钥"
        else
            echo "-> 生成部署配置写入 .env"
            prepare_external_env
            show_admin_token_once
            configure_compose_args

            echo "-> 生成 $EXTERNAL_COMPOSE_FILE"
            write_external_compose
            validate_external_network
            start_external_services
        fi

        panel_url="$(local_panel_url)"
        if [ "$ACTION" != "update" ]; then
            echo "-> 等待应用健康和数据库就绪..."
        fi
        if [ "$ACTION" != "update" ] && ! check_external_endpoints "$panel_url"; then
            show_diagnostics
            die "应用未在 ${READY_TIMEOUT} 秒内通过 healthz 和 readyz 检查"
        fi

        [ "$ACTION" = "update" ] && result_name="更新完成" || result_name="部署完成"
        show_result "$result_name" "$panel_url" "docker compose -p qcontrolhub --project-directory $WORK_DIR --env-file $ENV_FILE -f $EXTERNAL_COMPOSE_FILE down"
        ;;
esac
