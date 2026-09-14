#!/bin/sh
# A disposable, real image deployment. No host Agent services are installed.
set -eu

repo_root=$(cd "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-release-image.XXXXXX")
mkdir -p "$repo_root/dist"
artifact_root=$(mktemp -d "$repo_root/dist/release-image-test.XXXXXX")
test_id=${test_root##*.}
plane_image="qch-release-plane:$test_id"
web_image="qch-release-web:$test_id"
test_network="qch-release-$test_id"
db_container="$test_network-db"
plane_container="$test_network-plane"
web_container="$test_network-web"

cleanup() {
  result=$?
  if [ "$result" -ne 0 ]; then
    for container in "$plane_container" "$web_container"; do
      if docker container inspect "$container" >/dev/null 2>&1; then docker logs --tail 30 "$container" >&2 || true; fi
    done
  fi
  docker rm --force --volumes "$web_container" "$plane_container" "$db_container" >/dev/null 2>&1 || true
  docker network rm "$test_network" >/dev/null 2>&1 || true
  docker image rm "$web_image" "$plane_image" >/dev/null 2>&1 || true
  rm -rf "$test_root" "$artifact_root"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

release_version=${VERSION:-release-image-test}
make signing-key release-image-checksums VERSION="$release_version" \
  RELEASE_KEY="$test_root/release.key" RELEASE_DIR="$artifact_root" \
  RELEASE_IMAGE_DIR="$test_root/agent"
for target in qcontrol-plane qcontrol-web; do
  image=$plane_image
  if [ "$target" = qcontrol-web ]; then image=$web_image; fi
  docker build --load --target "$target" --tag "$image" \
    --build-arg VERSION="$release_version" \
    --build-arg RELEASE_ARTIFACTS="${artifact_root#"$repo_root"/}" .
done

docker network create "$test_network" >/dev/null
docker run --detach --name "$db_container" --network "$test_network" --network-alias postgres \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=release-test-password -e POSTGRES_DB=qcontrolhub \
  postgres:17-alpine >/dev/null
db_ready=false
for attempt in $(seq 1 30); do
  if docker exec "$db_container" pg_isready -U postgres -d qcontrolhub >/dev/null 2>&1; then db_ready=true; break; fi
  sleep 1
done
[ "$db_ready" = true ] || { printf '%s\n' 'release test PostgreSQL did not become ready' >&2; exit 1; }
export QCH_RELEASE_TEST_ADMIN_TOKEN=qch-release-image-test-admin-token-not-production
docker run --detach --name "$plane_container" --network "$test_network" --network-alias control-plane \
  --read-only --cap-drop ALL --security-opt no-new-privileges --tmpfs /tmp:rw,mode=1777 \
  -e QCH_DATABASE_URL='postgresql://postgres:release-test-password@postgres:5432/qcontrolhub?sslmode=disable' \
  -e QCH_ADMIN_TOKEN="$QCH_RELEASE_TEST_ADMIN_TOKEN" -e QCH_LISTEN=0.0.0.0:8080 \
  -e QCH_CONFIG_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  -e QCH_ALLOW_INSECURE_HTTP=true -e QCH_ALLOW_INSECURE_DATABASE=true \
  -e QCH_AGENT_PUBLIC_IP_PROBE_ENABLED=false "$plane_image" >/dev/null
docker run --detach --name "$web_container" --network "$test_network" --publish 127.0.0.1::8080 \
  --read-only --cap-drop ALL --cap-add CHOWN --cap-add SETGID --cap-add SETUID \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,mode=1777 --tmpfs /var/cache/nginx:rw,mode=1777 --tmpfs /var/run:rw,mode=1777 \
  "$web_image" >/dev/null
QCH_RELEASE_TEST_URL="http://$(docker port "$web_container" 8080/tcp)"
export QCH_RELEASE_TEST_URL
ready=false
for attempt in $(seq 1 45); do
  if curl --fail --silent --max-time 2 "$QCH_RELEASE_TEST_URL/readyz" >/dev/null; then ready=true; break; fi
  sleep 1
done
[ "$ready" = true ] || { printf '%s\n' 'signed release deployment did not become ready' >&2; exit 1; }
QCH_RELEASE_TEST_KEY="$test_root/release.key.pub.pem" QCH_RELEASE_TEST_VERSION="$release_version" \
  go test ./internal/agent -run '^TestReleaseImageEndToEnd$' -count=1 -v
