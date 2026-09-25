
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
            ui_heading "更新检查"
            ui_text 2 "  更新内置 PostgreSQL 部署并复用现有配置"
            [ -f "$ENV_FILE" ] || die "未找到现有部署配置：$ENV_FILE"
            resolve_bundled_image_refs
            current_control_image="$(current_update_image_id control-plane)" || die "无法读取 control-plane 当前镜像"
            current_web_image="$(current_update_image_id qcontrol-web)" || die "无法读取 qcontrol-web 当前镜像"
            current_postgres_image="$(current_update_image_id postgres)" || die "无法读取 PostgreSQL 当前镜像"
            show_current_application_versions "$current_control_image" "$current_web_image"
            current_postgres_version="$(update_image_version "$current_postgres_image")" || die "无法读取 PostgreSQL 当前版本"
            ui_service_row PostgreSQL "当前版本：$current_postgres_version"
            show_update_targets "$BUNDLED_CONTROL_IMAGE_REF" "$BUNDLED_WEB_IMAGE_REF" "$BUNDLED_POSTGRES_IMAGE_REF"
            if [ "$BUNDLED_CONTROL_IMAGE_REF" = "ghcr.io/qimaoww/qcontrol-plane:local" ]; then
                echo "-> 本地构建模式无法检查远程镜像版本，将重新构建"
            else
                ui_section "正在检查更新（拉取目标镜像）..."
                docker pull "$BUNDLED_CONTROL_IMAGE_REF" || die "拉取 $BUNDLED_CONTROL_IMAGE_REF 失败"
                docker pull "$BUNDLED_WEB_IMAGE_REF" || die "拉取 $BUNDLED_WEB_IMAGE_REF 失败"
                docker pull "$BUNDLED_POSTGRES_IMAGE_REF" || die "拉取 $BUNDLED_POSTGRES_IMAGE_REF 失败"
                app_changed=false
                if application_update_available "$BUNDLED_CONTROL_IMAGE_REF" "$BUNDLED_WEB_IMAGE_REF" \
                    "$current_control_image" "$current_web_image"; then
                    app_changed=true
                fi
                target_postgres_image="$(docker image inspect --format '{{.Id}}' "$BUNDLED_POSTGRES_IMAGE_REF")" || \
                    die "无法读取 PostgreSQL 目标镜像"
                [ -n "$target_postgres_image" ] || die "PostgreSQL 目标镜像 ID 为空"
                target_postgres_version="$(update_image_version "$target_postgres_image")" || die "无法读取 PostgreSQL 目标版本"
                show_target_image_version PostgreSQL "$current_postgres_image" "$target_postgres_image" "$target_postgres_version"
                if [ "$app_changed" = false ] && [ "$current_postgres_image" = "$target_postgres_image" ]; then
                    if [ "$FORCE" = false ]; then
                        show_no_update_result
                        exit 0
                    fi
                    echo "-> 镜像无变化，继续执行 -f 指定的密钥轮换"
                else
                    echo "-> 检测到镜像变化，开始更新"
                fi
            fi
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
            ui_heading "更新检查"
            ui_text 2 "  更新外部 PostgreSQL 部署并复用现有配置"
            [ -z "$DATABASE_URL" ] || die "更新禁止使用 -d；QCH_DATABASE_URL 必须原样复用"
            [ -z "$ADMIN_TOKEN" ] || die "更新禁止使用 -a；管理员令牌不得轮换"
            [ -z "$DOCKER_NETWORK" ] || die "更新禁止使用 -n；Docker 网络配置必须原样复用"
            [ "$FORCE" = false ] || die "外部 PostgreSQL 更新不支持 -f；令牌和配置加密密钥不得轮换"
            validate_external_update_env
            update_external_services
            exit 0
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

        show_result "部署完成" "$panel_url" "docker compose -p qcontrolhub --project-directory $WORK_DIR --env-file $ENV_FILE -f $EXTERNAL_COMPOSE_FILE down"
        ;;
esac
