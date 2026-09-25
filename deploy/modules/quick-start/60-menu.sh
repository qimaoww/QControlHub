
# Presentation stays independent of Docker so menus can be previewed safely.
ui_text() {
    local style="$1"
    shift
    if [ -t 1 ] && [ "${TERM:-dumb}" != dumb ] && [ -z "${NO_COLOR:-}" ]; then
        printf '\033[%sm%s\033[0m\n' "$style" "$*"
    else
        printf '%s\n' "$*"
    fi
}

ui_width() {
    local width="${COLUMNS:-72}"
    [[ "$width" =~ ^[1-9][0-9]{0,3}$ ]] || width=72
    [ "$width" -le 72 ] || width=72
    printf '%s' "$width"
}

ui_rule() {
    local line
    printf -v line '%*s' "$(ui_width)" ''
    ui_text 2 "${line// /-}"
}

ui_heading() {
    printf '\n'
    ui_rule
    ui_text '1;36' "  QControlHub / $1"
    ui_rule
}

ui_section() {
    printf '\n'
    ui_text 1 "  $1"
}

ui_detail() {
    ui_text 2 "  $1"
    printf '    %s\n' "$2"
}

# Only ASCII service names use padding; Chinese labels stay unpadded.
ui_service_row() {
    if [ "$(ui_width)" -lt 60 ]; then
        ui_detail "$1" "$2"
    else
        printf '  %-16s %s\n' "$1" "$2"
    fi
}

show_action_menu() {
    ui_heading "管理菜单"
    ui_section "工作目录"
    printf '  %s\n' "$WORK_DIR"
    ui_section "部署管理"
    printf '  [1] 安装 / 重新配置\n'
    printf '  [2] 更新现有部署\n'
    ui_text 2 "      检查镜像版本，无变化时跳过更新"
    ui_section "维护选项"
    printf '  [3] 卸载服务\n'
    ui_text 2 "      保留配置、密钥和数据库卷"
    printf '  [4] 设置目录\n'
    printf '\n'
}

show_mode_menu() {
    ui_heading "数据库模式"
    ui_section "选择部署方式"
    printf '  [1] 内置 PostgreSQL（推荐）\n'
    ui_text 2 "      自动部署数据库和应用，适合从零安装"
    printf '\n  [2] 连接外部 PostgreSQL\n'
    ui_text 2 "      复用已有数据库，仅部署应用"
    printf '\n'
}

show_no_update_result() {
    ui_heading "检查完成"
    ui_text '1;32' "  当前镜像与目标版本一致，无需更新"
    ui_text 2 "  保留当前运行容器和部署配置"
    printf '\n'
}

show_result() {
    local result_name="$1" url="$2" stop_cmd="$3"
    ui_heading "$result_name"
    ui_section "访问面板"
    ui_text '1;32' "  $url"
    ui_text 2 "  管理员 token：请使用密码管理器中保存的原文"
    ui_section "部署文件"
    ui_detail "配置文件" "$ENV_FILE"
    if [ -d "$SECRET_DIR" ]; then
        ui_detail "既有密钥目录" "$SECRET_DIR"
    fi
    ui_section "常用命令"
    ui_detail "查看日志" "${stop_cmd% down} logs -f"
    ui_detail "停止服务" "$stop_cmd"
    printf '\n'
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
    ui_heading "管理员 token（仅本次显示）"
    printf '\n  %s\n\n' "$ADMIN_TOKEN_TO_DISPLAY"
    ui_text '1;33' "  请立即保存到密码管理器。"
    ui_text 2 "  .env 只保存 SHA-256 摘要，之后无法恢复原文。"
    printf '\n'
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
    ui_heading "设置目录"
    ui_detail "当前目录" "$WORK_DIR"
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
        show_action_menu
        read -r -p "  请选择 [1-4] > " choice
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
    show_mode_menu
    read -r -p "  请选择 [1-2] > " choice
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
    ui_heading "服务已卸载"
    ui_section "已保留的部署数据"
    ui_detail "已保留配置" "$ENV_FILE"
    if [ -d "$SECRET_DIR" ]; then
        ui_detail "已保留密钥" "$SECRET_DIR"
    fi
    if [ "$MODE" = "bundled" ]; then
        echo "  已保留数据：Docker PostgreSQL 命名卷"
    else
        echo "  外部 PostgreSQL 数据未被修改"
    fi
    echo ""
}
