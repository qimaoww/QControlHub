# Privilege checks never prompt: the streamed/noninteractive entry must not hang.
docker_install_privileges() {
    DOCKER_INSTALL_SUDO=()
    if [ "$(id -u)" != 0 ]; then
        command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null ||
            die "补齐或启动 Docker 需要 root 权限，请以 sudo 重新运行安装命令"
        DOCKER_INSTALL_SUDO=(sudo -n)
    fi
}

docker_service_manager() {
    if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
        printf systemd
    elif [ -f /etc/alpine-release ] && command -v rc-service >/dev/null 2>&1; then
        printf openrc
    else
        die "无法自动启动 Docker：需要 systemd 或 Alpine OpenRC"
    fi
}

install_docker_engine() {
    local ID=''
    [ "$(uname -s)" = Linux ] || die "自动安装 Docker 仅支持 Linux"
    case "$(uname -m)" in
        x86_64|aarch64|arm64) ;;
        *) die "自动安装 Docker 不支持当前架构" ;;
    esac
    [ -r /etc/os-release ] || die "无法识别系统，不能自动安装 Docker"
    . /etc/os-release
    # Use the distribution's packages, without adding repositories, replacing
    # configuration, or invoking a convenience installer that upgrades Docker.
    case "$ID" in
        debian|ubuntu)
            command -v apt-get >/dev/null 2>&1 || die "系统缺少 apt-get"
            "${DOCKER_INSTALL_SUDO[@]}" env DEBIAN_FRONTEND=noninteractive apt-get \
                -o APT::Update::Error-Mode=any -o Acquire::Retries=2 update || die "更新 Docker 软件包索引失败"
            "${DOCKER_INSTALL_SUDO[@]}" env DEBIAN_FRONTEND=noninteractive apt-get \
                install -y --no-install-recommends --no-upgrade --no-remove docker.io || die "安装 Docker Engine 失败，请检查软件源和网络"
            ;;
        alpine)
            command -v apk >/dev/null 2>&1 || die "系统缺少 apk"
            "${DOCKER_INSTALL_SUDO[@]}" apk add --no-cache docker docker-openrc || die "安装 Docker Engine 失败，请检查软件源和网络"
            ;;
        fedora)
            command -v dnf >/dev/null 2>&1 || die "系统缺少 dnf"
            "${DOCKER_INSTALL_SUDO[@]}" dnf install -y --setopt=install_weak_deps=False moby-engine || die "安装 Docker Engine 失败，请检查软件源和网络"
            ;;
        opensuse-leap|opensuse-tumbleweed|sles)
            command -v zypper >/dev/null 2>&1 || die "系统缺少 zypper"
            "${DOCKER_INSTALL_SUDO[@]}" zypper --non-interactive install --no-recommends docker || die "安装 Docker Engine 失败，请检查软件源和网络"
            ;;
        *) die "当前系统不支持自动安装 Docker，请先配置可用的 Docker Engine" ;;
    esac
    command -v docker >/dev/null 2>&1 || die "安装后仍未找到 Docker 命令"
}

install_docker_plugin() {
    local name="$1" url="$2" checksum="$3" temp_file target
    target="/usr/local/lib/docker/cli-plugins/docker-$name"
    [ ! -e "$target" ] && [ ! -L "$target" ] || die "已有 $name 插件不可用，请检查 $target 和 Docker 插件配置"
    docker_install_privileges
    temp_file="$(mktemp)" || die "无法创建 $name 下载临时文件"
    if ! curl -fL --connect-timeout 20 --max-time 180 --retry 2 "$url" -o "$temp_file"; then
        rm -f -- "$temp_file"
        die "下载 $name 失败，请检查 GitHub 网络连接"
    fi
    if [ "$(openssl dgst -sha256 "$temp_file" | awk '{print $NF}')" != "$checksum" ]; then
        rm -f -- "$temp_file"
        die "$name 下载校验失败"
    fi
    if ! "${DOCKER_INSTALL_SUDO[@]}" install -d -m 0755 "${target%/*}" ||
        ! "${DOCKER_INSTALL_SUDO[@]}" install -m 0755 "$temp_file" "$target"; then
        rm -f -- "$temp_file"
        die "安装 $name 失败"
    fi
    rm -f -- "$temp_file"
    docker "$name" version >/dev/null 2>&1 || die "$name 已安装但不可用，请检查 Docker 插件配置"
}

install_docker_compose() {
    local arch checksum
    case "$(uname -s)/$(uname -m)" in
        Linux/x86_64) arch=x86_64; checksum=dba9d98e1ba5bfe11d88c99b9bd32fc4a0624a30fafe68eea34d61a3e42fd372 ;;
        Linux/aarch64|Linux/arm64) arch=aarch64; checksum=d26373b19e89160546d15407516cc59f453030d9bc5b43ba7faf16f7b4980137 ;;
        *) die "自动补齐 Compose v2 不支持当前系统或架构" ;;
    esac
    install_docker_plugin compose "https://github.com/docker/compose/releases/download/v2.40.3/docker-compose-linux-$arch" "$checksum"
}

install_docker_buildx() {
    local arch checksum
    case "$(uname -s)/$(uname -m)" in
        Linux/x86_64) arch=amd64; checksum=7d2d7d6d4680aa349614965aaa33ccec43f1a9a21e908a5ce4cb6adfa5ad5141 ;;
        Linux/aarch64|Linux/arm64) arch=arm64; checksum=50e85c4e290b698908421161c1aee069bcc5d4a65b7b7201bfac3a176b223a7e ;;
        *) die "自动补齐 Buildx 不支持当前系统或架构" ;;
    esac
    install_docker_plugin buildx "https://github.com/docker/buildx/releases/download/v0.29.1/buildx-v0.29.1.linux-$arch" "$checksum"
}

ensure_install_docker() {
    local endpoint manager new_engine=false attempt
    if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
        if ! docker compose version >/dev/null 2>&1; then
            echo "-> 补齐 Compose v2"
            install_docker_compose
        fi
        return 0
    fi
    # Never start a local daemon or install an engine for a remote/custom
    # endpoint. Context inspection failures also fail closed.
    if [ -n "${DOCKER_CONTEXT:-}" ] || [ -z "${DOCKER_HOST:-}" ]; then
        if command -v docker >/dev/null 2>&1; then
            endpoint="$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null)" ||
                die "无法读取 Docker context，请检查 Docker 连接配置"
        else
            [ -z "${DOCKER_CONTEXT:-}" ] || die "指定了 Docker context，但未找到 Docker 命令"
            endpoint=unix:///var/run/docker.sock
        fi
    else
        endpoint="$DOCKER_HOST"
    fi
    if ! command -v docker >/dev/null 2>&1; then
        [ "$endpoint" = unix:///var/run/docker.sock ] || die "指定了 Docker 连接地址，但未找到 Docker 命令"
        # An existing daemon without its CLI is an incomplete installation;
        # installing a different engine package could replace its runtime.
        [ ! -S /var/run/docker.sock ] || die "已有 Docker socket，但缺少 Docker CLI，请修复原安装的软件包"
        ! command -v dockerd >/dev/null 2>&1 || die "已有 dockerd，但缺少 Docker CLI，请修复原安装的软件包"
        manager="$(docker_service_manager)" || die "无法自动启动 Docker：需要 systemd 或 Alpine OpenRC"
        docker_install_privileges
        echo "-> 安装 Docker Engine"
        install_docker_engine
        if [ "$manager" = systemd ]; then
            "${DOCKER_INSTALL_SUDO[@]}" systemctl enable docker || die "设置 Docker 开机启动失败"
        else
            "${DOCKER_INSTALL_SUDO[@]}" rc-update add docker default || die "设置 Docker 开机启动失败"
        fi
        new_engine=true
        if [ "${#DOCKER_INSTALL_SUDO[@]}" -gt 0 ]; then
            # Keep the new root-owned daemon accessible in this process only.
            docker() {
                local env_name preserved_names=QCH_SOURCE_DIR
                # Compose relies on exported QCH values for secret file paths
                # and explicit overrides. Preserve names, never log their values.
                while IFS= read -r env_name; do
                    case "$env_name" in
                        QCH_*|COMPOSE_*|DOCKER_CONFIG) preserved_names+=",$env_name" ;;
                    esac
                done < <(compgen -e)
                command sudo -n --preserve-env="$preserved_names" docker --host unix:///var/run/docker.sock "$@"
            }
        fi
    fi
    if ! docker info >/dev/null 2>&1; then
        [ "$endpoint" = unix:///var/run/docker.sock ] || die "Docker 连接失败，请检查连接地址、context 和访问权限"
        if [ "$new_engine" = false ] && [ -S /var/run/docker.sock ]; then
            [ -r /var/run/docker.sock ] && [ -w /var/run/docker.sock ] ||
                die "无权访问 Docker socket，请以有 Docker 权限的用户重新运行"
        fi
        manager="$(docker_service_manager)" || die "无法自动启动 Docker：需要 systemd 或 Alpine OpenRC"
        docker_install_privileges
        # Do not restart an active daemon: a failed info can indicate TLS,
        # authorization or API incompatibility, rather than a stopped service.
        if [ "$manager" = systemd ]; then
            if "${DOCKER_INSTALL_SUDO[@]}" systemctl is-active --quiet docker; then
                [ "$new_engine" = true ] || die "Docker 服务已运行但连接失败，请检查 Docker 权限和连接配置"
            else
                "${DOCKER_INSTALL_SUDO[@]}" systemctl start docker || die "启动 Docker 服务失败，请检查 systemctl status docker"
            fi
        else
            if "${DOCKER_INSTALL_SUDO[@]}" rc-service docker status >/dev/null 2>&1; then
                die "Docker 服务已运行但连接失败，请检查 Docker 权限和连接配置"
            fi
            "${DOCKER_INSTALL_SUDO[@]}" rc-service docker start || die "启动 Docker 服务失败，请检查 rc-service docker status"
        fi
        for attempt in {1..30}; do
            docker info >/dev/null 2>&1 && break
            sleep 1
        done
        docker info >/dev/null 2>&1 || die "Docker Engine 启动后仍不可访问，请检查服务日志和访问权限"
    fi
    if ! docker compose version >/dev/null 2>&1; then
        echo "-> 补齐 Compose v2"
        install_docker_compose
    fi
}

require_commands() {
    local command_name
    for command_name in openssl curl awk mktemp; do
        command -v "$command_name" >/dev/null 2>&1 || die "缺少依赖：$command_name"
    done
    if [ "$ACTION" = install ]; then
        ensure_install_docker
    fi
    command -v docker >/dev/null 2>&1 || die "缺少依赖：docker"
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
    [ ! -L "$file" ] || die "密钥文件不能是符号链接：$file"
    value="$(<"$file")"
    case "$value" in
        *$'\n'*|*$'\r'*) die "密钥文件只能包含一行：$file" ;;
    esac
    printf '%s' "$value"
}

write_secret_file() {
    local file="$1" value="$2" temp_file
    [ ! -L "$SECRET_DIR" ] || die "密钥目录不能是符号链接：$SECRET_DIR"
    mkdir -p "$SECRET_DIR"
    chmod 0700 "$SECRET_DIR"
    [ ! -L "$file" ] || die "密钥文件不能是符号链接：$file"
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
    echo "-> 已备份密钥：$backup_file"
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
            die "QCH_ADMIN_TOKEN 与 QCH_ADMIN_TOKEN_SHA256 不匹配；请先确认正确的管理员令牌"
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
