
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
    local raw_token stored_digest legacy_token legacy_digest
    raw_token="$ADMIN_TOKEN"
    stored_digest="$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
    legacy_token="$(read_env_key QCH_ADMIN_TOKEN)"
    if [ "$FORCE" = false ] && [ -z "$raw_token" ] && [ -n "$stored_digest" ] && [ -n "$legacy_token" ]; then
        validate_secret QCH_ADMIN_TOKEN "$legacy_token"
        validate_admin_token_digest "$stored_digest"
        legacy_digest="$(sha256_hex "$legacy_token")"
        [ "$legacy_digest" = "$(printf '%s' "$stored_digest" | tr 'A-F' 'a-f')" ] || \
            die "QCH_ADMIN_TOKEN 与 QCH_ADMIN_TOKEN_SHA256 不匹配；请先确认正确的管理员 token"
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
