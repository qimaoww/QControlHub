#!/usr/bin/env bash
# Opt-in integration test in a disposable Docker daemon. REGISTRY must contain
# qch-control:{old,new} and qch-web:{old,new}, running the actual applications.
# The registry seam changes repositories only; checks, pulls and Compose are real.
set -euo pipefail
: "${QCH_TEST_UPDATE_REGISTRY:?set the private fixture registry (host:port)}"
: "${QCH_TEST_UPDATE_ROOT:?set an empty disposable test directory}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
registry="$QCH_TEST_UPDATE_REGISTRY"
test_root="$QCH_TEST_UPDATE_ROOT"
[ -f /.dockerenv ] && [ "$(docker info --format '{{.Name}}')" = "$(hostname)" ] || {
    printf '%s\n' 'requires a disposable Docker-in-Docker daemon' >&2; exit 1;
}
[ -z "$(docker ps -aq --filter label=com.docker.compose.project=qcontrolhub)" ] || {
    printf '%s\n' 'refusing an existing qcontrolhub deployment' >&2; exit 1;
}
mkdir "$test_root"
source "$repo_root/deploy/quick-start.sh"
WORK_DIR="$test_root"
ENV_FILE="$WORK_DIR/.env"
EXTERNAL_COMPOSE_FILE="$WORK_DIR/docker-compose.external.yml"
SECRET_COMPOSE_FILE="$WORK_DIR/docker-compose.secrets.yml"
COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE")
MODE=external
READY_TIMEOUT=45
umask 077
eval "$(declare -f remote_update_image | sed '1s/remote_update_image/fixture_remote_update_image/')"
remote_update_image() {
    local ref="$1" role
    case "$ref" in
        ghcr.io/qimaoww/qcontrol-plane:*) role=control ;;
        ghcr.io/qimaoww/qcontrol-web:*) role=web ;;
        postgres:*) fixture_remote_update_image "$registry/qch-postgres:latest" "$2"; return ;;
        *) fixture_remote_update_image "$@"; return ;;
    esac
    fixture_remote_update_image "$registry/qch-$role:${ref##*:}" "$2"
}
docker() {
    printf '%s\n' "$*" >> "$test_root/docker.log"
    command docker "$@"
}
set_target() {
    docker buildx imagetools create --tag "$registry/qch-$1:latest" "$registry/qch-$1:$2" >/dev/null
}
cleanup() {
    if [ "$?" -ne 0 ]; then compose logs --tail=25 control-plane >&2 || true; fi
    compose down --volumes --remove-orphans >/dev/null 2>&1 || true
    docker rm -fv qch-update-fixture-db >/dev/null 2>&1 || true
    docker network rm qch-update-fixture >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker network create qch-update-fixture >/dev/null
docker run -d --name qch-update-fixture-db --network qch-update-fixture \
    -e POSTGRES_USER=fixture -e POSTGRES_PASSWORD=isolated-fixture-password -e POSTGRES_DB=fixture \
    postgres:17-alpine >/dev/null
for attempt in {1..45}; do
    if docker exec qch-update-fixture-db pg_isready -U fixture -d fixture >/dev/null 2>&1; then break; fi
    sleep 1
done
cat > "$ENV_FILE" <<'ENV'
# Isolated synthetic credentials; preserve comments and ordering byte for byte.
QCH_DATABASE_URL=postgres://fixture:isolated-fixture-password@qch-update-fixture-db:5432/fixture?sslmode=disable
QCH_ADMIN_TOKEN=isolated-administrator-token-value-for-testing
QCH_CONFIG_ENCRYPTION_KEY=1111111111111111111111111111111111111111111111111111111111111111
QCH_ALLOW_INSECURE_DATABASE=true
QCH_DOCKER_NETWORK=qch-update-fixture
QCH_BIND_ADDRESS=127.0.0.1
QCH_PORT=18109
ENV
printf 'preserved mount\n' > "$WORK_DIR/preserved.txt"
cat > "$EXTERNAL_COMPOSE_FILE" <<YAML
services:
  control-plane:
    image: $registry/qch-control:latest
    environment:
      QCH_DATABASE_URL: \${QCH_DATABASE_URL}
      QCH_ADMIN_TOKEN: \${QCH_ADMIN_TOKEN}
      QCH_CONFIG_ENCRYPTION_KEY: \${QCH_CONFIG_ENCRYPTION_KEY}
      QCH_ALLOW_INSECURE_DATABASE: "true"
      QCH_BEHIND_TLS_PROXY: "true"
      QCH_AGENT_PUBLIC_IP_PROBE_ENABLED: "false"
      QCH_LISTEN: 0.0.0.0:8080
    volumes:
      - ./preserved.txt:/fixture/preserved.txt:ro
    networks: [site]
  qcontrol-web:
    image: $registry/qch-web:latest
    ports: ["127.0.0.1:18109:8080"]
    networks: [site]
networks:
  site:
    name: qch-update-fixture
    external: true
YAML
cp "$ENV_FILE" "$test_root/expected.env"
set_target control old
set_target web old
compose pull
start_application_services "${COMPOSE_ARGS[@]}"
check_external_endpoints http://127.0.0.1:18109

assert_noop() {
    local before after
    before="$(compose ps -q)"
    : > "$test_root/docker.log"
    update_external_services > "$test_root/noop.out"
    after="$(compose ps -q)"
    [ "$before" = "$after" ]
    grep -Fq '无需更新' "$test_root/noop.out"
    if grep -Eq '(^pull | up -d)' "$test_root/docker.log"; then return 1; fi
    cmp "$test_root/expected.env" "$ENV_FILE"
}
assert_noop
set_target control new
: > "$test_root/docker.log"
update_external_services > "$test_root/single.out"
[ "$(grep -c '^pull ' "$test_root/docker.log")" -eq 1 ]
cmp "$test_root/expected.env" "$ENV_FILE"
assert_noop
set_target control old
set_target web new
: > "$test_root/docker.log"
update_external_services > "$test_root/multiple.out"
[ "$(grep -c '^pull ' "$test_root/docker.log")" -eq 2 ]
cmp "$test_root/expected.env" "$ENV_FILE"
assert_noop

# Inject a post-start health failure, using real container recreation/recovery.
# The originals must retain their immutable image identity and deployment data.
control_before="$(current_update_image_id control-plane)"
web_before="$(current_update_image_id qcontrol-web)"
cp "$EXTERNAL_COMPOSE_FILE" "$test_root/before-rollback.yml"
set_target control new
eval "$(declare -f check_external_endpoints | sed '1s/check_external_endpoints/fixture_check_endpoints/')"
check_external_endpoints() {
    [ "$UPDATE_SERVICES_CHANGED" != true ] || [ "$UPDATE_ROLLBACK_ARMED" != true ] || return 1
    fixture_check_endpoints "$@"
}
if (update_external_services) > "$test_root/rollback.out" 2>&1; then exit 1; fi
[ "$control_before" = "$(current_update_image_id control-plane)" ]
[ "$web_before" = "$(current_update_image_id qcontrol-web)" ]
cmp "$test_root/before-rollback.yml" "$EXTERNAL_COMPOSE_FILE"
cmp "$test_root/expected.env" "$ENV_FILE"
container="$(compose ps -q control-plane)"
[ "$(docker exec "$container" cat /fixture/preserved.txt)" = 'preserved mount' ]
printf '%s\n' 'real Docker remote comparison, immutable pull, Compose, no-op and rollback passed'

if [ "${QCH_TEST_UPDATE_ARM64:-0}" = 1 ]; then
    compose down --volumes
    for role in control web; do
        docker buildx imagetools create --tag "$registry/qch-$role:latest" \
            "$registry/qch-$role:new" "$registry/qch-$role:new-arm64" >/dev/null
    done
    printf 'services:\n  control-plane:\n    platform: linux/arm64/v8\n  qcontrol-web:\n    platform: linux/arm64/v8\n' > "$WORK_DIR/arm64.yml"
    COMPOSE_ARGS+=(-f "$WORK_DIR/arm64.yml")
    prepare_update_plan
    pull_update_images
    write_update_compose_override > "$WORK_DIR/checked-arm64.yml"
    run_compose "${COMPOSE_ARGS[@]}" -f "$WORK_DIR/checked-arm64.yml" create --no-build --pull never
    for ref in "${UPDATE_IMAGE_LOCAL_IDS[@]}"; do
        [ "$(docker image inspect --format '{{.Architecture}}' "$ref")" = arm64 ]
    done
    printf '%s\n' 'real ARM64 manifest selection, pull, verification and Compose create passed (no ARM execution)'
    compose down --volumes
fi

if [ "${QCH_TEST_UPDATE_BUNDLED:-0}" = 1 ]; then
    cleanup
    REPO_ROOT="$test_root/bundled"
    WORK_DIR="$REPO_ROOT"
    mkdir "$WORK_DIR"
    cp "$repo_root/docker-compose.yml" "$REPO_ROOT/docker-compose.yml"
    ENV_FILE="$WORK_DIR/.env"
    SECRET_COMPOSE_FILE="$WORK_DIR/docker-compose.secrets.yml"
    SECRET_DIR="$WORK_DIR/.secrets"
    CONFIG_KEY_FILE="$SECRET_DIR/config-encryption-key"
    PREVIOUS_CONFIG_KEYS_FILE="$SECRET_DIR/config-encryption-previous-keys"
    MODE=bundled ACTION=update FORCE=false
    printf 'POSTGRES_PASSWORD=isolated-fixture-password\nQCH_PORT=18110\nPOSTGRES_PORT=15435\nQCH_IMAGE_TAG=latest\n' > "$ENV_FILE"
    prepare_bundled_env
    write_secret_compose_override
    configure_compose_args
    for role in control web postgres; do
        set_target "$role" old
        docker pull --platform linux/amd64 "$registry/qch-$role:old" >/dev/null
        case "$role" in
            control) local_tag=ghcr.io/qimaoww/qcontrol-plane:latest ;;
            web) local_tag=ghcr.io/qimaoww/qcontrol-web:latest ;;
            postgres) local_tag=postgres:17-alpine ;;
        esac
        docker image tag "$registry/qch-$role:old" "$local_tag"
    done
    compose up -d --no-build --pull never
    wait_ready http://127.0.0.1:18110/readyz "$READY_TIMEOUT"
    : > "$test_root/docker.log"
    before="$(compose ps -q)"
    prepare_update_plan
    [ "$UPDATE_IMAGES_CHANGED" = false ]
    [ "$before" = "$(compose ps -q)" ]
    if grep -Eq '(^pull | up -d)' "$test_root/docker.log"; then exit 1; fi

    cp "$CONFIG_KEY_FILE" "$WORK_DIR/before-rotation.key"
    FORCE=true
    prepare_bundled_env
    write_secret_compose_override
    start_services
    if cmp -s "$WORK_DIR/before-rotation.key" "$CONFIG_KEY_FILE"; then exit 1; fi
    [ "$(read_env_key POSTGRES_PASSWORD)" = isolated-fixture-password ]
    FORCE=false
    set_target postgres new
    : > "$test_root/docker.log"
    prepare_update_plan
    [ "$UPDATE_IMAGES_CHANGED" = true ]
    [ "${UPDATE_IMAGE_CURRENT[0]}" = "${UPDATE_IMAGE_TARGETS[0]}" ]
    [ "${UPDATE_IMAGE_CURRENT[1]}" = "${UPDATE_IMAGE_TARGETS[1]}" ]
    pull_update_images
    [ "$(grep -c '^pull ' "$test_root/docker.log")" -eq 1 ]
    start_services
    wait_ready http://127.0.0.1:18110/readyz "$READY_TIMEOUT"
    prepare_update_plan
    [ "$UPDATE_IMAGES_CHANGED" = false ]
    printf '%s\n' 'real bundled no-op, explicit key rotation and PostgreSQL-only update passed'
fi
