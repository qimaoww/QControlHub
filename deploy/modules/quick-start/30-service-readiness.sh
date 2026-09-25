
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
    resolve_bundled_image_refs
    if [ "$BUNDLED_CONTROL_IMAGE_REF" = "ghcr.io/qimaoww/qcontrol-plane:local" ]; then
        echo "-> 构建并启动本地 Docker 镜像"
        if ! compose up -d --build; then
            show_diagnostics
            die "Docker Compose 启动失败"
        fi
    elif [ "$ACTION" = "update" ]; then
        echo "-> 使用已拉取的应用镜像启动 Docker Compose"
        if ! compose up -d --no-build --pull never; then
            show_diagnostics
            die "Docker Compose 启动失败；请检查镜像拉取和 Compose 配置"
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
    # Probe before consulting the clock: with a one-second timeout the deadline
    # can pass between its computation and the first loop test, which would skip
    # the only request and report a healthy deployment as unavailable.
    while :; do
        if curl -sf -m 3 "$url" >/dev/null 2>&1; then
            return 0
        fi
        [ "$(date +%s)" -lt "$deadline" ] || return 1
        sleep 2
    done
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
