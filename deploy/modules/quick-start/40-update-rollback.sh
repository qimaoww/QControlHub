
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
