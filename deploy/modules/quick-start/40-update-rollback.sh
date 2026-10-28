
update_container_id() {
    local service="$1" ids count
    ids="$(compose ps -q "$service")"
    count="$(printf '%s\n' "$ids" | awk 'NF { count++ } END { print count + 0 }')"
    [ "$count" -eq 1 ] || die "更新前必须有且仅有一个正在运行的 $(ui_service_name "$service") 容器"
    printf '%s' "$ids"
}

current_update_image_id() {
    local service="$1" identity="${2:-id}" ids count image_id metadata
    if [ "$MODE" = bundled ]; then
        # Older bundled installs may not have the generated secret override yet.
        ids="$(run_compose -f "$REPO_ROOT/docker-compose.yml" ps -q "$service")" || \
            die "无法读取 $(ui_service_name "$service") 当前容器"
    else
        ids="$(compose ps -q "$service")" || die "无法读取 $(ui_service_name "$service") 当前容器"
    fi
    count="$(printf '%s\n' "$ids" | awk 'NF { count++ } END { print count + 0 }')"
    [ "$count" -le 1 ] || die "更新前发现多个正在运行的 $(ui_service_name "$service") 容器"
    [ "$count" -eq 1 ] || return 0
    metadata="$(docker inspect --format '{{json .}}' "$ids")" || die "无法读取 $(ui_service_name "$service") 当前镜像"
    # With the containerd store .Image may identify an index. The container's
    # descriptor records the exact platform manifest that was actually started.
    image_id="$(jq -er --arg kind "$identity" '
        (if $kind == "identity" then .ImageManifestDescriptor.digest // .Image else .Image end) |
        strings | select(test("^sha256:[0-9a-f]{64}$"))' <<< "$metadata")" || die "当前容器镜像身份无效"
    [ -n "$image_id" ] || die "无法读取 $(ui_service_name "$service") 当前镜像"
    printf '%s' "$image_id"
}

update_image_version() {
    local image_id="$1" version short_id
    if [ -z "$image_id" ]; then
        printf '未运行'
        return 0
    fi
    version="$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$image_id")" || \
        die "无法读取镜像版本：$image_id"
    case "$version" in
        ""|"<no value>")
            short_id="${image_id#sha256:}"
            printf '镜像 %s' "${short_id:0:12}"
            ;;
        *)
            if [[ "$version" =~ ^[0-9a-fA-F]{40}$ ]]; then
                version="${version:0:12}"
            fi
            printf '%s' "$version"
            ;;
    esac
}

show_current_application_versions() {
    local control_version web_version
    control_version="$(update_image_version "$1")" || die "无法读取控制面当前版本"
    web_version="$(update_image_version "$2")" || die "无法读取网页服务当前版本"
    ui_section "当前运行版本"
    ui_service_row control-plane "当前版本：$control_version"
    ui_service_row qcontrol-web "当前版本：$web_version"
}

show_update_targets() {
    ui_section "目标镜像"
    ui_service_row control-plane "目标标签：${1##*:}"
    ui_service_row qcontrol-web "目标标签：${2##*:}"
    if [ -n "${3:-}" ]; then
        ui_service_row PostgreSQL "目标标签：${3##*:}"
    fi
}

show_target_image_version() {
    local service="$1" current="$2" target="$3" version="$4" status
    if [ -z "$current" ]; then
        status="待启动"
    elif [ "$current" = "$target" ]; then
        status="无变化"
    else
        status="有更新"
    fi
    ui_service_row "$service" "目标版本：$version [$status]"
}

require_update_tools() {
    command -v jq >/dev/null 2>&1 || die "远程镜像检查需要 jq，请先安装 jq"
    docker buildx version >/dev/null 2>&1 || die "远程镜像检查需要 Docker Buildx，请先安装 Buildx 插件"
}

normalize_update_platform() {
    local platform="$1"
    case "$platform" in
        linux/x86_64) platform=linux/amd64 ;;
        linux/aarch64|linux/arm64/v8) platform=linux/arm64 ;;
        linux/armhf|linux/armv7l) platform=linux/arm/v7 ;;
        linux/armel|linux/armv6l) platform=linux/arm/v6 ;;
    esac
    [[ "$platform" =~ ^linux/[a-z0-9_]+(/[a-zA-Z0-9_.-]+)?$ ]] || die "无法确定部署平台：$platform"
    printf '%s' "$platform"
}

# No layer downloads. Hash the exact manifest bytes, then keep every subsequent
# registry request and pull on that immutable reference, including single images.
remote_update_image() (
    local ref="$1" platform="$2" manifest_file repository digest pinned metadata config_id actual_platform version indexed=false
    umask 077
    manifest_file="$(mktemp)" || die "无法创建镜像检查临时文件"
    trap 'rm -f -- "$manifest_file"' EXIT
    trap 'exit 130' HUP INT TERM
    docker buildx imagetools inspect --raw "$ref" > "$manifest_file" || die "远程镜像检查失败：$ref"
    repository="${ref%%@*}"
    # Strip a tag, retaining a registry's optional port.
    if [[ "${repository##*/}" == *:* ]]; then repository="${repository%:*}"; fi
    if jq -e '.manifests | type == "array"' "$manifest_file" >/dev/null 2>&1; then
        digest="$(jq -er --arg platform "$platform" '
            [.manifests[] | select(.platform != null) |
             select((.platform.os + "/" + .platform.architecture +
                (if (.platform.variant // "") == "" or
                    (.platform.architecture == "arm64" and .platform.variant == "v8")
                 then "" else "/" + .platform.variant end)) == $platform)] |
            if length == 1 then .[0].digest else error("平台缺失或不唯一") end
        ' "$manifest_file")" || die "目标镜像缺少唯一的 $platform 平台：$ref"
        [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "目标镜像摘要无效：$ref"
        indexed=true
        pinned="$repository@$digest"
        docker buildx imagetools inspect --raw "$pinned" > "$manifest_file" || die "远程平台镜像检查失败：$pinned"
        [ "sha256:$(openssl dgst -sha256 "$manifest_file" | awk '{print $NF}')" = "$digest" ] || die "远程平台镜像摘要不匹配：$ref"
    else
        digest="sha256:$(openssl dgst -sha256 "$manifest_file" | awk '{print $NF}')"
        pinned="$repository@$digest"
    fi
    config_id="$(jq -er 'select(.schemaVersion == 2) | .config.digest | select(test("^sha256:[0-9a-f]{64}$"))' "$manifest_file")" || \
        die "目标镜像身份信息不完整：$ref"
    metadata="$(docker buildx imagetools inspect --format '{{json .Image}}' "$pinned")" || die "无法读取目标镜像版本和平台：$ref"
    actual_platform="$(jq -er '.os + "/" + .architecture + (if (.variant // "") == "" then "" else "/" + .variant end)' <<< "$metadata")" || \
        die "目标镜像平台信息不完整：$ref"
    actual_platform="$(normalize_update_platform "$actual_platform")" || exit 1
    # Older ARM image configs omit the variant carried by their index entry.
    if [ "$indexed" = true ] && [[ "$platform" == "$actual_platform/"* ]]; then
        actual_platform="$platform"
    fi
    [ "$platform" = "$actual_platform" ] || die "目标镜像平台不匹配：$ref（需要 $platform，实际 $actual_platform）"
    version="$(jq -er '.config.Labels["org.opencontainers.image.version"] // "" | strings | select(test("[[:cntrl:]]") | not)' <<< "$metadata")" || die "目标镜像版本信息无效：$ref"
    if [ -z "$version" ]; then version="镜像 ${config_id:7:12}"; fi
    if [[ "$version" =~ ^[0-9a-fA-F]{40}$ ]]; then version="${version:0:12}"; fi
    jq -cn --arg id "$config_id" --arg ref "$pinned" --arg version "$version" '{id:$id, ref:$ref, version:$version}'
)

prepare_update_plan() {
    local config default_platform service platform ref current metadata local_id i
    local -a config_args
    require_update_tools
    if [ "$MODE" = bundled ]; then
        config_args=(-f "$REPO_ROOT/docker-compose.yml")
        UPDATE_IMAGE_SERVICES=(control-plane qcontrol-web postgres)
    else
        config_args=("${COMPOSE_ARGS[@]}")
        UPDATE_IMAGE_SERVICES=(control-plane qcontrol-web)
    fi
    # Compose also reads DOCKER_DEFAULT_PLATFORM from the project's .env. Older
    # config output omits it from services; resolve it through an extension so
    # quoting, interpolation and shell precedence remain Compose's responsibility.
    config="$(run_compose "${config_args[@]}" -f - config --format json <<'YAML'
x-qch-update-default-platform: ${DOCKER_DEFAULT_PLATFORM:-}
YAML
    )" || die "无法解析部署镜像和平台配置"
    default_platform="$(jq -er '."x-qch-update-default-platform" // "" | strings' <<< "$config")" || die "无法解析默认部署平台"
    if [ -z "$default_platform" ]; then
        default_platform="$(docker info --format '{{.OSType}}/{{.Architecture}}')" || die "无法读取 Docker Engine 平台"
    fi
    UPDATE_IMAGE_REFS=() UPDATE_IMAGE_PLATFORMS=() UPDATE_IMAGE_CURRENT=()
    UPDATE_IMAGE_TARGETS=() UPDATE_IMAGE_PINNED=() UPDATE_IMAGE_VERSIONS=() UPDATE_IMAGE_LOCAL_IDS=()
    UPDATE_IMAGES_CHANGED=false
    ui_section "正在检查远程镜像"
    for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
        service="${UPDATE_IMAGE_SERVICES[$i]}"
        platform="$(jq -er --arg s "$service" --arg p "$default_platform" '.services[$s] | .platform // $p | strings | select(length > 0)' <<< "$config")" || die "无法读取 $(ui_service_name "$service") 部署平台"
        platform="$(normalize_update_platform "$platform")" || die "无法确定 $(ui_service_name "$service") 部署平台"
        if [ "$MODE" = external ]; then
            case "$service" in
                control-plane) ref=ghcr.io/qimaoww/qcontrol-plane:latest ;;
                qcontrol-web) ref=ghcr.io/qimaoww/qcontrol-web:latest ;;
            esac
        else
            ref="$(jq -er --arg s "$service" '.services[$s].image | strings | select(length > 0)' <<< "$config")" || die "无法读取 $(ui_service_name "$service") 目标镜像"
        fi
        local_id="$(current_update_image_id "$service")" || die "无法读取 $(ui_service_name "$service") 当前镜像"
        current="$(current_update_image_id "$service" identity)" || die "无法读取 $(ui_service_name "$service") 当前平台镜像"
        metadata="$(remote_update_image "$ref" "$platform")" || die "$(ui_service_name "$service") 远程检查失败，已终止更新"
        # Classic storage uses config IDs; containerd uses manifest descriptors.
        # Normalize both to the checked config identity for comparison/display.
        if [ "$current" = "$(jq -er '.ref | split("@") | .[1]' <<< "$metadata")" ]; then
            current="$(jq -er .id <<< "$metadata")"
        fi
        UPDATE_IMAGE_REFS+=("$ref") UPDATE_IMAGE_PLATFORMS+=("$platform") UPDATE_IMAGE_CURRENT+=("$current")
        UPDATE_IMAGE_LOCAL_IDS+=("$local_id")
        UPDATE_IMAGE_TARGETS+=("$(jq -er .id <<< "$metadata")")
        UPDATE_IMAGE_PINNED+=("$(jq -er .ref <<< "$metadata")")
        UPDATE_IMAGE_VERSIONS+=("$(jq -er .version <<< "$metadata")")
        [ "$current" = "${UPDATE_IMAGE_TARGETS[$i]}" ] || UPDATE_IMAGES_CHANGED=true
    done
    ui_section "镜像对比结果"
    for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
        show_target_image_version "${UPDATE_IMAGE_SERVICES[$i]}" "${UPDATE_IMAGE_CURRENT[$i]}" \
            "${UPDATE_IMAGE_TARGETS[$i]}" "${UPDATE_IMAGE_VERSIONS[$i]}"
    done
}

pull_update_images() {
    local i actual metadata actual_platform
    for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
        [ "${UPDATE_IMAGE_CURRENT[$i]}" != "${UPDATE_IMAGE_TARGETS[$i]}" ] || continue
        ui_section "拉取镜像：${UPDATE_IMAGE_REFS[$i]}"
        docker pull --platform "${UPDATE_IMAGE_PLATFORMS[$i]}" "${UPDATE_IMAGE_PINNED[$i]}" || die "镜像拉取失败：${UPDATE_IMAGE_REFS[$i]}"
        metadata="$(docker image inspect --format '{{json .}}' "${UPDATE_IMAGE_PINNED[$i]}")" || die "无法校验已拉取的镜像"
        actual="$(jq -er '.Id | strings | select(test("^sha256:[0-9a-f]{64}$"))' <<< "$metadata")" || die "已拉取的镜像身份无效"
        [ "$actual" = "${UPDATE_IMAGE_TARGETS[$i]}" ] || \
            [ "$actual" = "${UPDATE_IMAGE_PINNED[$i]##*@}" ] || die "已拉取的镜像与检查结果不一致，已终止更新"
        actual_platform="$(jq -er '.Os + "/" + .Architecture + (if (.Variant // "") == "" then "" else "/" + .Variant end)' <<< "$metadata")" || die "已拉取的镜像平台信息无效"
        actual_platform="$(normalize_update_platform "$actual_platform")" || die "已拉取的镜像平台无效"
        # Legacy ARM configs omit the variant already verified in the registry.
        if [[ "${UPDATE_IMAGE_PLATFORMS[$i]}" == "$actual_platform/"* ]]; then
            actual_platform="${UPDATE_IMAGE_PLATFORMS[$i]}"
        fi
        [ "$actual_platform" = "${UPDATE_IMAGE_PLATFORMS[$i]}" ] || die "已拉取的镜像平台与检查结果不一致，已终止更新"
        UPDATE_IMAGE_LOCAL_IDS[$i]="$actual"
    done
}

write_update_compose_override() {
    local i
    printf 'services:\n'
    for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
        printf '  %s:\n    image: %s\n    platform: %s\n' "${UPDATE_IMAGE_SERVICES[$i]}" \
            "${UPDATE_IMAGE_LOCAL_IDS[$i]}" "${UPDATE_IMAGE_PLATFORMS[$i]}"
    done
}

capture_update_image() {
    local service="$1" container_id image_id image_ref backup_tag
    container_id="$(update_container_id "$service")"
    image_id="$(docker inspect --format '{{.Image}}' "$container_id")"
    image_ref="$(docker inspect --format '{{.Config.Image}}' "$container_id")"
    [ -n "$image_id" ] && [ -n "$image_ref" ] || die "无法读取 $(ui_service_name "$service") 当前镜像"
    backup_tag="qcontrolhub-update-rollback/$service:$(date +%Y%m%d%H%M%S)-$$"
    docker image tag "$image_id" "$backup_tag" || die "无法保存 $(ui_service_name "$service") 当前镜像"
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
            "$UPDATE_BACKUP_DIR/docker-compose.checked.yml" \
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
            echo "错误：旧密钥配置恢复失败；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
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
            ! start_application_services "${rollback_args[@]}"; then
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

    show_current_application_versions "$UPDATE_CONTROL_IMAGE" "$UPDATE_WEB_IMAGE"
    show_update_targets ghcr.io/qimaoww/qcontrol-plane:latest ghcr.io/qimaoww/qcontrol-web:latest
    prepare_update_plan
    if [ "$UPDATE_IMAGES_CHANGED" = false ]; then
        show_no_update_result
        UPDATE_ROLLBACK_ARMED=false
        trap - EXIT HUP INT TERM
        cleanup_external_update_backup
        return 0
    fi
    pull_update_images

    echo "-> 检测到镜像变化，开始更新"
    echo "-> 保留现有拓扑并更新 $EXTERNAL_COMPOSE_FILE 中的应用配置"
    prepare_external_update_compose
    cmp -s "$UPDATE_BACKUP_DIR/.env" "$ENV_FILE" || die "更新过程改写了 .env，已终止"

    compose config --quiet || die "Docker Compose 配置校验失败"
    UPDATE_SERVICES_CHANGED=true
    write_update_compose_override > "$UPDATE_BACKUP_DIR/docker-compose.checked.yml"
    if ! start_application_services "${COMPOSE_ARGS[@]}" -f "$UPDATE_BACKUP_DIR/docker-compose.checked.yml"; then
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
    show_result "更新完成" "$panel_url" "docker compose -p qcontrolhub --project-directory $WORK_DIR --env-file $ENV_FILE -f $EXTERNAL_COMPOSE_FILE down"
)
