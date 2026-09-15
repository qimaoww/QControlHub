
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
