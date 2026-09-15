
prepare_default_agent_engines() {
    local engine answer selection seen=","
    DEFAULT_AGENT_ENGINES="$(read_env_key QCH_DEFAULT_AGENT_ENGINES)"
    # Existing installations keep the database-owned choice on upgrade.
    [ ! -f "$ENV_FILE" ] || return 0
    selection="${QCH_DEFAULT_AGENT_ENGINES:-mihomo,xray,sing-box,ss-rust}"
    if [ -z "${QCH_DEFAULT_AGENT_ENGINES:-}" ] && [ -t 0 ]; then
        echo ""
        echo "选择新 Agent 默认启用的内核能力（不自动安装内核）。"
        echo "后续可在系统设置修改默认值，节点设置可单独开关。"
        selection=""
        for engine in mihomo xray sing-box ss-rust; do
            while true; do
                read -r -p "默认启用 $engine？[Y/n] " answer
                case "$answer" in
                    ""|y|Y|yes|YES) selection="${selection:+$selection,}$engine"; break ;;
                    n|N|no|NO) break ;;
                    *) echo "请输入 y 或 n。" ;;
                esac
            done
        done
        selection="${selection:-none}"
    fi
    if [ "$selection" != none ]; then
        case "$selection" in ,*|*,|*,,*) die "QCH_DEFAULT_AGENT_ENGINES 含空内核名称" ;; esac
        local -a selected_engines
        IFS=',' read -r -a selected_engines <<< "$selection"
        for engine in "${selected_engines[@]}"; do
            case "$engine" in mihomo|xray|sing-box|ss-rust) ;; *) die "无效内核能力：$engine" ;; esac
            case "$seen" in *",$engine,"*) die "重复内核能力：$engine" ;; esac
            seen="$seen$engine,"
        done
    fi
    DEFAULT_AGENT_ENGINES="$selection"
}

prepare_bundled_env() {
    prepare_default_agent_engines
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
        "QCH_DEFAULT_AGENT_ENGINES=$DEFAULT_AGENT_ENGINES" \
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
    prepare_default_agent_engines
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
        "QCH_DEFAULT_AGENT_ENGINES=$DEFAULT_AGENT_ENGINES" \
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
