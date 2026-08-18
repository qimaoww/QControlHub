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
#   ./deploy/quick-start.sh -m external -n 1panel-network -d 'postgresql://user:pass@postgresql:5432/qcontrolhub?sslmode=disable'

set -euo pipefail

MODE=""
DATABASE_URL=""
DATABASE_NETWORK=""
ADMIN_TOKEN=""
FORCE=false
READY_TIMEOUT=60

usage() {
    cat <<'USAGE'
用法：
  ./deploy/quick-start.sh [-m bundled|external] [选项]

部署模式：
  bundled             Docker Compose 内置 PostgreSQL + 控制面
  external            仅部署控制面并连接已有 PostgreSQL

选项：
  -m MODE             选择部署模式；省略时交互选择
  -d DATABASE_URL     external 模式使用的 PostgreSQL 连接串
  -n NETWORK          external 模式加入已有 Docker 网络，便于按服务名连接数据库
  -a ADMIN_TOKEN      管理员令牌（至少 32 字节）
  -f                  轮换应用密钥；执行前备份现有 .env，数据库密码不变
  -t SECONDS          就绪检查超时时间（默认 60 秒）
  -h                  显示帮助

重复执行默认会复用 .env，仅补齐缺失配置；不会覆盖已有密钥或 CORS 设置。
USAGE
}

die() {
    printf '错误：%s\n' "$*" >&2
    exit 1
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    usage
    exit 0
fi

while getopts ':m:d:n:a:ft:h' opt; do
    case "$opt" in
        m) MODE="$OPTARG" ;;
        d) DATABASE_URL="$OPTARG" ;;
        n) DATABASE_NETWORK="$OPTARG" ;;
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
[ -z "$DATABASE_NETWORK" ] || [ "$MODE" = "external" ] || die "-n 仅能用于 external 模式"
if ! [[ "$READY_TIMEOUT" =~ ^[1-9][0-9]*$ ]]; then
    die "就绪检查超时时间必须是正整数"
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
ENV_FILE="$REPO_ROOT/.env"
EXTERNAL_COMPOSE_FILE="$REPO_ROOT/docker-compose.external.yml"
EXTERNAL_NETWORK_COMPOSE_FILE="$REPO_ROOT/docker-compose.external-network.yml"

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
    backup_file="${ENV_FILE}.bak.$(date +%Y%m%d%H%M%S).$$"
    cp -p -- "$ENV_FILE" "$backup_file"
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

validate_network_name() {
    local network="$1"
    case "$network" in
        ''|*[!A-Za-z0-9_.-]*|[.-]*) die "Docker 网络名称无效：$network" ;;
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
      QCH_ADMIN_TOKEN: ${QCH_ADMIN_TOKEN:?admin token required}
      QCH_LISTEN: 0.0.0.0:8080
      QCH_BEHIND_TLS_PROXY: ${QCH_BEHIND_TLS_PROXY:-true}
      QCH_ALLOW_INSECURE_HTTP: ${QCH_ALLOW_INSECURE_HTTP:-false}
      QCH_ALLOW_INSECURE_DATABASE: ${QCH_ALLOW_INSECURE_DATABASE:-false}
      QCH_CORS_ORIGINS: ${QCH_CORS_ORIGINS:-}
      QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-}
      QCH_WEBHOOK_SECRET: ${QCH_WEBHOOK_SECRET:-}
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
      QCH_OPERATOR_TOKENS: ${QCH_OPERATOR_TOKENS:-}
      QCH_READONLY_TOKENS: ${QCH_READONLY_TOKENS:-}
    ports:
      - "${QCH_BIND_ADDRESS:-127.0.0.1}:${QCH_PORT:-8080}:8080"
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
YAML
}

write_external_network_compose() {
    cat > "$EXTERNAL_NETWORK_COMPOSE_FILE" <<'YAML'
services:
  control-plane:
    networks:
      - external-database

networks:
  external-database:
    external: true
    name: ${QCH_DATABASE_NETWORK:?external database Docker network required}
YAML
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
    local postgres_password admin_token webhook_secret config_key
    local behind_proxy allow_http allow_database cors_origins bind_address port image_tag version

    if [ "$FORCE" = true ]; then
        backup_env
    fi

    postgres_password="$(read_env_key POSTGRES_PASSWORD)"
    [ -n "$postgres_password" ] || postgres_password="$(random_hex)"

    admin_token="${ADMIN_TOKEN:-$(read_env_key QCH_ADMIN_TOKEN)}"
    if [ "$FORCE" = true ] || [ -z "$admin_token" ]; then
        admin_token="${ADMIN_TOKEN:-$(random_hex)}"
    fi
    webhook_secret="$(read_env_key QCH_WEBHOOK_SECRET)"
    if [ "$FORCE" = true ] || [ -z "$webhook_secret" ]; then
        webhook_secret="$(random_hex)"
    fi
    config_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    if [ "$FORCE" = true ] || [ -z "$config_key" ]; then
        config_key="$(random_hex)"
    fi
    validate_secret QCH_ADMIN_TOKEN "$admin_token"

    behind_proxy="$(read_env_key QCH_BEHIND_TLS_PROXY)"; [ -n "$behind_proxy" ] || behind_proxy=true
    allow_http="$(read_env_key QCH_ALLOW_INSECURE_HTTP)"; [ -n "$allow_http" ] || allow_http=false
    allow_database="$(read_env_key QCH_ALLOW_INSECURE_DATABASE)"; [ -n "$allow_database" ] || allow_database=true
    cors_origins="$(read_env_key QCH_CORS_ORIGINS)"
    bind_address="$(read_env_key QCH_BIND_ADDRESS)"; [ -n "$bind_address" ] || bind_address=127.0.0.1
    port="$(read_env_key QCH_PORT)"; [ -n "$port" ] || port=8080
    image_tag="$(read_env_key QCH_IMAGE_TAG)"; [ -n "$image_tag" ] || image_tag=latest
    version="$(read_env_key VERSION)"; [ -n "$version" ] || version=dev

    local postgres_db postgres_user postgres_port
    postgres_db="$(read_env_key POSTGRES_DB)"; [ -n "$postgres_db" ] || postgres_db=qcontrolhub
    postgres_user="$(read_env_key POSTGRES_USER)"; [ -n "$postgres_user" ] || postgres_user=qcontrolhub
    postgres_port="$(read_env_key POSTGRES_PORT)"; [ -n "$postgres_port" ] || postgres_port=5432

    update_env_file \
        "POSTGRES_DB=$postgres_db" \
        "POSTGRES_USER=$postgres_user" \
        "POSTGRES_PASSWORD=$postgres_password" \
        "POSTGRES_PORT=$postgres_port" \
        "QCH_ADMIN_TOKEN=$admin_token" \
        "QCH_WEBHOOK_SECRET=$webhook_secret" \
        "QCH_CONFIG_ENCRYPTION_KEY=$config_key" \
        "QCH_BEHIND_TLS_PROXY=$behind_proxy" \
        "QCH_ALLOW_INSECURE_HTTP=$allow_http" \
        "QCH_ALLOW_INSECURE_DATABASE=$allow_database" \
        "QCH_CORS_ORIGINS=$cors_origins" \
        "QCH_BIND_ADDRESS=$bind_address" \
        "QCH_PORT=$port" \
        "QCH_IMAGE_TAG=$image_tag" \
        "VERSION=$version"
}

prepare_external_env() {
    local db_url db_network admin_token webhook_secret config_key
    local behind_proxy allow_http allow_database cors_origins bind_address port image_tag version

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

    db_network="${DATABASE_NETWORK:-$(read_env_key QCH_DATABASE_NETWORK)}"
    if [ -n "$db_network" ]; then
        validate_network_name "$db_network"
    fi

    if [ "$FORCE" = true ]; then
        backup_env
    fi

    admin_token="${ADMIN_TOKEN:-$(read_env_key QCH_ADMIN_TOKEN)}"
    if [ "$FORCE" = true ] || [ -z "$admin_token" ]; then
        admin_token="${ADMIN_TOKEN:-$(random_hex)}"
    fi
    webhook_secret="$(read_env_key QCH_WEBHOOK_SECRET)"
    if [ "$FORCE" = true ] || [ -z "$webhook_secret" ]; then
        webhook_secret="$(random_hex)"
    fi
    config_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    if [ "$FORCE" = true ] || [ -z "$config_key" ]; then
        config_key="$(random_hex)"
    fi
    validate_secret QCH_ADMIN_TOKEN "$admin_token"

    behind_proxy="$(read_env_key QCH_BEHIND_TLS_PROXY)"; [ -n "$behind_proxy" ] || behind_proxy=true
    allow_http="$(read_env_key QCH_ALLOW_INSECURE_HTTP)"; [ -n "$allow_http" ] || allow_http=false
    allow_database="$(read_env_key QCH_ALLOW_INSECURE_DATABASE)"; [ -n "$allow_database" ] || allow_database=false
    cors_origins="$(read_env_key QCH_CORS_ORIGINS)"
    bind_address="$(read_env_key QCH_BIND_ADDRESS)"; [ -n "$bind_address" ] || bind_address=127.0.0.1
    port="$(read_env_key QCH_PORT)"; [ -n "$port" ] || port=8080
    image_tag="$(read_env_key QCH_IMAGE_TAG)"; [ -n "$image_tag" ] || image_tag=latest
    version="$(read_env_key VERSION)"; [ -n "$version" ] || version=dev

    update_env_file \
        "QCH_DATABASE_URL=$db_url" \
        "QCH_DATABASE_NETWORK=$db_network" \
        "QCH_ADMIN_TOKEN=$admin_token" \
        "QCH_WEBHOOK_SECRET=$webhook_secret" \
        "QCH_CONFIG_ENCRYPTION_KEY=$config_key" \
        "QCH_BEHIND_TLS_PROXY=$behind_proxy" \
        "QCH_ALLOW_INSECURE_HTTP=$allow_http" \
        "QCH_ALLOW_INSECURE_DATABASE=$allow_database" \
        "QCH_CORS_ORIGINS=$cors_origins" \
        "QCH_BIND_ADDRESS=$bind_address" \
        "QCH_PORT=$port" \
        "QCH_IMAGE_TAG=$image_tag" \
        "VERSION=$version"
}

local_service_url() {
    local port
    port="$(read_env_key QCH_PORT)"
    [ -n "$port" ] || port=8080
    printf 'http://127.0.0.1:%s' "$port"
}

show_result() {
    local token="$1" url="$2" stop_cmd="$3"
    echo ""
    echo "============================================"
    echo "  QControlHub 部署完成"
    echo "============================================"
    echo ""
    echo "  访问地址：  $url"
    echo "  管理员令牌：$token"
    echo "  配置文件：  $ENV_FILE"
    echo ""
    echo "  停止服务：  $stop_cmd"
    echo "  查看日志：  ${stop_cmd/down/logs -f}"
    echo ""
}

# ---- 选择部署方式 ----
if [ -z "$MODE" ]; then
    [ -t 0 ] || die "未指定部署模式；非交互模式请使用 -m bundled 或 -m external"
    echo ""
    echo "QControlHub 部署方式选择"
    echo ""
    echo "  1. 全套部署 — Docker Compose 内置 PostgreSQL + 控制面（推荐）"
    echo "  2. 连接外部 PostgreSQL — 仅部署控制面容器"
    echo ""
    read -r -p "请选择 [1-2] " choice
    case "$choice" in
        1) MODE="bundled" ;;
        2) MODE="external" ;;
        *) echo "无效选择：$choice"; exit 1 ;;
    esac
fi

case "$MODE" in
    bundled)
        COMPOSE_ARGS=()
        ;;
    external)
        COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE")
        ;;
esac

require_commands

# ---- 执行部署 ----
case "$MODE" in
    bundled)
        if [ -f "$ENV_FILE" ] && [ "$FORCE" = false ]; then
            echo "-> 复用已有 .env，并补齐缺失配置"
        elif [ "$FORCE" = true ]; then
            echo "-> 轮换应用密钥（保留 PostgreSQL 密码）"
        else
            echo "-> 生成部署配置写入 .env"
        fi
        prepare_bundled_env
        start_services

        echo "-> 等待控制面就绪..."
        service_url="$(local_service_url)"
        if ! wait_ready "$service_url/readyz" "$READY_TIMEOUT"; then
            show_diagnostics
            die "控制面未在 ${READY_TIMEOUT} 秒内就绪"
        fi

        token="$(read_env_key QCH_ADMIN_TOKEN)"
        show_result "$token" "$service_url" "docker compose down"
        ;;
    external)
        if [ -f "$ENV_FILE" ] && [ "$FORCE" = false ]; then
            echo "-> 复用已有 .env，并补齐缺失配置"
        elif [ "$FORCE" = true ]; then
            echo "-> 轮换应用密钥"
        else
            echo "-> 生成部署配置写入 .env"
        fi
        prepare_external_env

        echo "-> 生成 $EXTERNAL_COMPOSE_FILE"
        write_external_compose
        if [ -n "$(read_env_key QCH_DATABASE_NETWORK)" ]; then
            echo "-> 加入外部数据库 Docker 网络：$(read_env_key QCH_DATABASE_NETWORK)"
            write_external_network_compose
            COMPOSE_ARGS+=(-f "$EXTERNAL_NETWORK_COMPOSE_FILE")
        fi

        start_services

        echo "-> 等待控制面就绪..."
        service_url="$(local_service_url)"
        if ! wait_ready "$service_url/readyz" "$READY_TIMEOUT"; then
            show_diagnostics
            die "控制面未在 ${READY_TIMEOUT} 秒内就绪"
        fi

        stop_cmd="docker compose -f docker-compose.external.yml"
        if [ -n "$(read_env_key QCH_DATABASE_NETWORK)" ]; then
            stop_cmd="$stop_cmd -f docker-compose.external-network.yml"
        fi
        show_result "$(read_env_key QCH_ADMIN_TOKEN)" "$service_url" "$stop_cmd down"
        ;;
esac
