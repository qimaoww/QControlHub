
write_external_compose() {
    local docker_network config_key
    docker_network="$(read_env_key QCH_DOCKER_NETWORK)"
    config_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    cat > "$EXTERNAL_COMPOSE_FILE" <<'YAML'
name: qcontrolhub

services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:latest
    restart: unless-stopped
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL:?QCH_DATABASE_URL required}
      QCH_ADMIN_TOKEN: ${QCH_ADMIN_TOKEN:-}
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:-}
      QCH_LISTEN: 0.0.0.0:8080
      QCH_DEFAULT_AGENT_ENGINES: ${QCH_DEFAULT_AGENT_ENGINES:-}
      QCH_BEHIND_TLS_PROXY: ${QCH_BEHIND_TLS_PROXY:-true}
      QCH_ALLOW_INSECURE_HTTP: ${QCH_ALLOW_INSECURE_HTTP:-false}
      QCH_ALLOW_INSECURE_DATABASE: ${QCH_ALLOW_INSECURE_DATABASE:-false}
      QCH_CORS_ORIGINS: ${QCH_CORS_ORIGINS:-}
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-}
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-172.30.254.2/32,172.30.254.1/32}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_WEBHOOK_SECRET: ${QCH_WEBHOOK_SECRET:-}
YAML
    if [ -n "$config_key" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:?QCH_CONFIG_ENCRYPTION_KEY required}
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}
      QCH_OPERATOR_TOKENS: ${QCH_OPERATOR_TOKENS:-}
      QCH_AUDITOR_TOKENS: ${QCH_AUDITOR_TOKENS:-}
      QCH_READONLY_TOKENS: ${QCH_READONLY_TOKENS:-}
      QCH_AGENT_PUBLIC_IP_PROBE_ENABLED: ${QCH_AGENT_PUBLIC_IP_PROBE_ENABLED:-true}
      QCH_AGENT_PUBLIC_IP_PROBE_IPV4_ENDPOINT: ${QCH_AGENT_PUBLIC_IP_PROBE_IPV4_ENDPOINT:-}
      QCH_AGENT_PUBLIC_IP_PROBE_IPV6_ENDPOINT: ${QCH_AGENT_PUBLIC_IP_PROBE_IPV6_ENDPOINT:-}
      QCH_AGENT_PUBLIC_IP_PROBE_INTERVAL: ${QCH_AGENT_PUBLIC_IP_PROBE_INTERVAL:-5m}
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      - control-host
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      control-host:
        ipv4_address: ${QCH_CONTROL_PLANE_PROXY_ADDRESS:-172.30.254.3}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
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
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:latest
    restart: unless-stopped
    depends_on:
      control-plane:
        condition: service_healthy
    ports:
      - "${QCH_BIND_ADDRESS:-127.0.0.1}:${QCH_PORT:-8080}:8080"
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      - control-host
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      control-host:
        ipv4_address: ${QCH_WEB_PROXY_ADDRESS:-172.30.254.2}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    read_only: true
    tmpfs:
      - /tmp:size=8m,mode=1777
      - /var/cache/nginx:size=8m,mode=1777
      - /var/run:size=1m,mode=1777
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETGID
      - SETUID
    security_opt:
      - no-new-privileges:true
    pids_limit: 64
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://127.0.0.1:8080/healthz"]
      interval: 10s
      timeout: 3s
      retries: 6
      start_period: 5s
    stop_grace_period: 10s

networks:
  control-host:
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    name: ${QCH_DOCKER_NETWORK:?custom Docker network required}
    external: true
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    ipam:
      config:
        - subnet: ${QCH_CONTROL_PROXY_SUBNET:-172.30.254.0/24}
          gateway: ${QCH_CONTROL_PROXY_GATEWAY:-172.30.254.1}
YAML
    fi
}

write_secret_compose_override() {
    cat > "$SECRET_COMPOSE_FILE" <<'YAML'
services:
  control-plane:
    environment:
      QCH_ADMIN_TOKEN: ""
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:?administrator token digest required}
      QCH_CONFIG_ENCRYPTION_KEY: ""
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ""
      QCH_CONFIG_ENCRYPTION_KEY_FILE: /run/secrets/qch-config-encryption-key
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE: /run/secrets/qch-config-encryption-previous-keys
    volumes:
      - type: bind
        source: ${QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE:?configuration encryption key file required}
        target: /run/secrets/qch-config-encryption-key
        read_only: true
      - type: bind
        source: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE:?previous configuration encryption keys file required}
        target: /run/secrets/qch-config-encryption-previous-keys
        read_only: true
YAML
    chmod 0600 "$SECRET_COMPOSE_FILE"
}

COMPOSE_ARGS=()

run_compose() (
    # --env-file alone does not override exported shell variables. The
    # external deployment must read its own database, credentials and network.
    if [ "$MODE" = external ]; then
        local env_key
        while IFS= read -r env_key; do
            unset "$env_key"
        done < <(awk '
            { sub(/^\xef\xbb\xbf/, "") }
            /^QCH_[A-Za-z0-9_]+=/ { sub(/=.*/, ""); print }
        ' "$ENV_FILE")
        unset QCH_DATABASE_URL QCH_ADMIN_TOKEN QCH_ADMIN_TOKEN_SHA256 \
            QCH_CONFIG_ENCRYPTION_KEY QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS \
            QCH_CONFIG_ENCRYPTION_KEY_FILE QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE \
            QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE \
            QCH_ALLOW_INSECURE_DATABASE QCH_DOCKER_NETWORK QCH_BIND_ADDRESS QCH_PORT
    fi
    QCH_SOURCE_DIR="$REPO_ROOT" docker compose -p qcontrolhub --project-directory "$WORK_DIR" --env-file "$ENV_FILE" "$@"
)

compose() {
    run_compose "${COMPOSE_ARGS[@]}" "$@"
}

prepare_external_update_compose() {
    # Merge only application settings. Do not regenerate the topology: an
    # existing deployment may have its own network, ports, CA mounts, etc.
    cat > "$UPDATE_BACKUP_DIR/docker-compose.update.yml" <<'YAML'
services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:latest
    restart: unless-stopped
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL:?QCH_DATABASE_URL required}
      QCH_ADMIN_TOKEN: ${QCH_ADMIN_TOKEN:-}
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:-}
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}
      QCH_ALLOW_INSECURE_DATABASE: ${QCH_ALLOW_INSECURE_DATABASE:-false}
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:latest
    restart: unless-stopped
YAML
    # Keep variable references, not rendered credentials, in the persisted
    # Compose. The existing secret override remains a separate unmodified file.
    run_compose -f "$EXTERNAL_COMPOSE_FILE" -f "$UPDATE_BACKUP_DIR/docker-compose.update.yml" \
        config --no-interpolate --no-normalize --no-path-resolution \
        > "$UPDATE_BACKUP_DIR/docker-compose.next.yml" || die "无法保留现有 Compose 配置"
    [ -s "$UPDATE_BACKUP_DIR/docker-compose.next.yml" ] || die "生成的 Compose 配置为空"
    chmod 0600 "$UPDATE_BACKUP_DIR/docker-compose.next.yml"
    mv -f -- "$UPDATE_BACKUP_DIR/docker-compose.next.yml" "$EXTERNAL_COMPOSE_FILE"
}
