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
FORCE=false
READY_TIMEOUT=60

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
  -f                  轮换管理员 token 与应用密钥；数据库密码不变
  -t SECONDS          就绪检查超时时间（默认 60 秒）
  -h                  显示帮助

重复执行默认会复用 .env，仅补齐缺失配置；不会覆盖已有密钥或 CORS 设置。
USAGE
}

die() {
    printf '错误：%s\n' "$*" >&2
    exit 1
}

bootstrap_streamed_script() {
    local script_path install_dir origin_url branch
    script_path="${BASH_SOURCE[0]}"
    case "$script_path" in
        /dev/fd/*|/proc/self/fd/*) ;;
        *) return 0 ;;
    esac

    command -v git >/dev/null 2>&1 || die "缺少依赖：git"
    install_dir="${QCH_INSTALL_DIR:-$PWD/qcontrolhub}"
    case "$install_dir" in
        /*) ;;
        *) install_dir="$PWD/$install_dir" ;;
    esac
    case "$install_dir" in
        *$'\n'*|*$'\r'*) die "QCH_INSTALL_DIR 不能包含换行" ;;
    esac

    if [ -e "$install_dir" ]; then
        [ -d "$install_dir/.git" ] || die "安装目录已存在但不是 Git 仓库：$install_dir"
        origin_url="$(git -C "$install_dir" remote get-url origin 2>/dev/null || true)"
        case "$origin_url" in
            https://github.com/qimaoww/qcontrolhub|https://github.com/qimaoww/qcontrolhub.git|git@github.com:qimaoww/qcontrolhub.git) ;;
            *) die "安装目录不是 QControlHub 官方仓库：$install_dir" ;;
        esac
        branch="$(git -C "$install_dir" symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
        [ "$branch" = "main" ] || die "安装目录必须位于 main 分支，当前为：${branch:-detached}"
        git -C "$install_dir" diff --quiet || die "安装目录包含未提交修改，请先处理后再运行"
        git -C "$install_dir" diff --cached --quiet || die "安装目录包含已暂存修改，请先处理后再运行"
        echo "-> 更新 QControlHub：$install_dir"
        git -C "$install_dir" fetch --prune origin main
        git -C "$install_dir" merge-base --is-ancestor HEAD FETCH_HEAD || die "安装目录的 main 已偏离官方历史，请人工处理"
        git -C "$install_dir" merge --ff-only FETCH_HEAD
    else
        echo "-> 下载 QControlHub：$install_dir"
        mkdir -p "$(dirname "$install_dir")"
        git clone --depth 1 --branch main --single-branch https://github.com/qimaoww/qcontrolhub.git "$install_dir"
    fi
    exec "$install_dir/deploy/quick-start.sh" "$@"
}

bootstrap_streamed_script "$@"

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    usage
    exit 0
fi

while getopts ':m:o:d:a:ft:h' opt; do
    case "$opt" in
        m) MODE="$OPTARG" ;;
        o) ACTION="$OPTARG" ;;
        d) DATABASE_URL="$OPTARG" ;;
        a) ADMIN_TOKEN="$OPTARG" ;;
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
    local raw_token stored_digest legacy_token
    raw_token="$ADMIN_TOKEN"
    stored_digest="$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
    legacy_token="$(read_env_key QCH_ADMIN_TOKEN)"
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
        postgresql://*) ;;
        *) die "DATABASE_URL 必须以 postgresql:// 开头" ;;
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
    cat > "$EXTERNAL_COMPOSE_FILE" <<'YAML'
name: qcontrolhub

services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:${QCH_IMAGE_TAG:-latest}
    build:
      context: .
      target: qcontrol-plane
      args:
        VERSION: ${VERSION:-dev}
    restart: unless-stopped
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL:?external database URL required}
      QCH_ADMIN_TOKEN: ${QCH_ADMIN_TOKEN:-}
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:-}
      QCH_LISTEN: 0.0.0.0:8080
      QCH_BEHIND_TLS_PROXY: ${QCH_BEHIND_TLS_PROXY:-true}
      QCH_ALLOW_INSECURE_HTTP: ${QCH_ALLOW_INSECURE_HTTP:-false}
      QCH_ALLOW_INSECURE_DATABASE: ${QCH_ALLOW_INSECURE_DATABASE:-false}
      QCH_CORS_ORIGINS: ${QCH_CORS_ORIGINS:-}
      QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-172.30.254.2/32,172.30.254.1/32}
      QCH_WEBHOOK_SECRET: ${QCH_WEBHOOK_SECRET:-}
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}
      QCH_CONFIG_ENCRYPTION_KEY_FILE: ${QCH_CONFIG_ENCRYPTION_KEY_FILE:-}
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE:-}
      QCH_OPERATOR_TOKENS: ${QCH_OPERATOR_TOKENS:-}
      QCH_AUDITOR_TOKENS: ${QCH_AUDITOR_TOKENS:-}
      QCH_READONLY_TOKENS: ${QCH_READONLY_TOKENS:-}
    networks:
      control-host:
        ipv4_address: ${QCH_CONTROL_PLANE_PROXY_ADDRESS:-172.30.254.3}
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
    image: ghcr.io/qimaoww/qcontrol-web:${QCH_IMAGE_TAG:-latest}
    build:
      context: .
      target: qcontrol-web
      args:
        VERSION: ${VERSION:-dev}
    restart: unless-stopped
    depends_on:
      control-plane:
        condition: service_healthy
    ports:
      - "${QCH_BIND_ADDRESS:-127.0.0.1}:${QCH_PORT:-8080}:8080"
    networks:
      control-host:
        ipv4_address: ${QCH_WEB_PROXY_ADDRESS:-172.30.254.2}
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
    ipam:
      config:
        - subnet: ${QCH_CONTROL_PROXY_SUBNET:-172.30.254.0/24}
          gateway: ${QCH_CONTROL_PROXY_GATEWAY:-172.30.254.1}
YAML
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

compose() {
    docker compose "${COMPOSE_ARGS[@]}" "$@"
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
    local db_url webhook_secret
    local behind_proxy allow_http allow_database cors_origins bind_address port image_tag version
    local proxy_subnet proxy_gateway web_proxy_address control_plane_proxy_address trusted_proxy_cidrs

    db_url="${DATABASE_URL:-$(read_env_key QCH_DATABASE_URL)}"
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

    if [ "$FORCE" = true ]; then
        backup_env
    fi

    prepare_admin_token
    webhook_secret="$(read_env_key QCH_WEBHOOK_SECRET)"
    if [ "$FORCE" = true ] || [ -z "$webhook_secret" ]; then
        webhook_secret="$(random_hex)"
    fi
    prepare_config_keyring

    behind_proxy="$(read_env_key QCH_BEHIND_TLS_PROXY)"; [ -n "$behind_proxy" ] || behind_proxy=true
    allow_http="$(read_env_key QCH_ALLOW_INSECURE_HTTP)"; [ -n "$allow_http" ] || allow_http=false
    allow_database="$(read_env_key QCH_ALLOW_INSECURE_DATABASE)"; [ -n "$allow_database" ] || allow_database=false
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

    update_env_file \
        "QCH_DATABASE_URL=$db_url" \
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
    echo "  密钥目录：  $SECRET_DIR"
    echo ""
    echo "  停止服务：  $stop_cmd"
    echo "  查看日志：  ${stop_cmd/down/logs -f}"
    echo ""
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

choose_action() {
    [ -t 0 ] || die "未指定操作；非交互模式请使用 -o install、-o update 或 -o uninstall"
    echo ""
    echo "QControlHub 管理菜单"
    echo ""
    echo "  1. 安装 / 重新配置"
    echo "  2. 更新现有部署"
    echo "  3. 卸载服务（保留配置、密钥和数据库卷）"
    echo ""
    read -r -p "请选择 [1-3] " choice
    case "$choice" in
        1) ACTION="install" ;;
        2) ACTION="update" ;;
        3) ACTION="uninstall" ;;
        *) die "无效选择：$choice" ;;
    esac
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
        bundled) COMPOSE_ARGS=(-f "$REPO_ROOT/docker-compose.yml") ;;
        external) COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE") ;;
    esac
    if [ "$ACTION" != "uninstall" ] || [ -f "$SECRET_COMPOSE_FILE" ]; then
        COMPOSE_ARGS+=(-f "$SECRET_COMPOSE_FILE")
    fi
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

# Keep the environment preparation functions sourceable for the isolated shell
# regression without running Docker or mutating the caller's deployment.
if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then
    return 0
fi

# ---- 选择管理操作和部署方式 ----
if [ -z "$ACTION" ]; then
    if [ -n "$MODE" ]; then
        ACTION="install"
    else
        choose_action
    fi
fi
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

        echo "-> 等待控制面就绪..."
        if ! wait_ready "http://127.0.0.1:8080/readyz" "$READY_TIMEOUT"; then
            show_diagnostics
            die "控制面未在 ${READY_TIMEOUT} 秒内就绪"
        fi

        [ "$ACTION" = "update" ] && result_name="更新完成" || result_name="部署完成"
        show_result "$result_name" "http://127.0.0.1:8080" "docker compose -f docker-compose.yml -f docker-compose.secrets.yml down"
        ;;
    external)
        if [ "$ACTION" = "update" ]; then
            echo "-> 更新外部 PostgreSQL 部署并复用现有配置"
        elif [ -f "$ENV_FILE" ] && [ "$FORCE" = false ]; then
            echo "-> 复用已有 .env，并补齐缺失配置"
        elif [ "$FORCE" = true ]; then
            echo "-> 轮换应用密钥"
        else
            echo "-> 生成部署配置写入 .env"
        fi
        prepare_external_env
        show_admin_token_once

        echo "-> 生成 $EXTERNAL_COMPOSE_FILE"
        write_external_compose
        write_secret_compose_override

        start_services

        echo "-> 等待控制面就绪..."
        if ! wait_ready "http://127.0.0.1:8080/readyz" "$READY_TIMEOUT"; then
            show_diagnostics
            die "控制面未在 ${READY_TIMEOUT} 秒内就绪"
        fi

        [ "$ACTION" = "update" ] && result_name="更新完成" || result_name="部署完成"
        show_result "$result_name" "http://127.0.0.1:8080" "docker compose -f docker-compose.external.yml -f docker-compose.secrets.yml down"
        ;;
esac
