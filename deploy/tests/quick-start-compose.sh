#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
    printf '%s\n' 'quick-start real Compose regression skipped: Compose v2 not installed'
    exit 0
fi

test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-quick-start-compose.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
umask 077
# shellcheck source=../quick-start.sh
source "$repo_root/deploy/quick-start.sh"
WORK_DIR="$test_root"
ENV_FILE="$WORK_DIR/.env"
EXTERNAL_COMPOSE_FILE="$WORK_DIR/docker-compose.external.yml"
MODE=external
UPDATE_BACKUP_DIR="$WORK_DIR/backup"
mkdir -p "$UPDATE_BACKUP_DIR"
COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE")

cat > "$ENV_FILE" <<'ENV'
QCH_DATABASE_URL=postgres://remote:p%40ss@db.example.test:6543/original?sslmode=verify-full&application_name=qch
QCH_ADMIN_TOKEN=original-administrator-token-value
QCH_ADMIN_TOKEN_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
QCH_CONFIG_ENCRYPTION_KEY=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=older-a,older-b
QCH_ALLOW_INSECURE_DATABASE=false
QCH_DOCKER_NETWORK=existing-site-network
QCH_PORT=18081
QCH_BIND_ADDRESS=127.0.0.1
ENV
cp "$ENV_FILE" "$test_root/expected.env"
cat > "$EXTERNAL_COMPOSE_FILE" <<'YAML'
services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:previous
    pull_policy: always
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL:?required}
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
      QCH_BEHIND_TLS_PROXY: "true"
    networks:
      - site
    volumes:
      - ./db-ca.pem:/run/secrets/db-ca.pem:ro
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:previous
    networks:
      - site
    ports:
      - "${QCH_BIND_ADDRESS:-127.0.0.1}:${QCH_PORT:-8080}:8080"
networks:
  site:
    external: true
    name: ${QCH_DOCKER_NETWORK}
YAML

# Simulate a shell that has another deployment's configuration exported.
export QCH_DATABASE_URL='postgres://wrong-database.invalid/other?sslmode=disable'
export QCH_ADMIN_TOKEN=wrong-token QCH_ADMIN_TOKEN_SHA256=wrong-digest
export QCH_CONFIG_ENCRYPTION_KEY=wrong-key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=wrong-previous
export QCH_ALLOW_INSECURE_DATABASE=true QCH_DOCKER_NETWORK=wrong-network
export QCH_BIND_ADDRESS=0.0.0.0 QCH_PORT=19999
prepare_external_update_compose
cmp "$test_root/expected.env" "$ENV_FILE"
grep -Fq '${QCH_DATABASE_URL:?QCH_DATABASE_URL required}' "$EXTERNAL_COMPOSE_FILE"
if grep -Fq 'remote:p%40ss' "$EXTERNAL_COMPOSE_FILE"; then
    printf '%s\n' 'rendered database credentials leaked into persisted Compose' >&2
    exit 1
fi
compose config --format json > "$test_root/rendered.json"
node - "$test_root/rendered.json" "$ENV_FILE" "$WORK_DIR" <<'JS'
const fs = require('node:fs');
const assert = require('node:assert/strict');
const config = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const expected = Object.fromEntries(fs.readFileSync(process.argv[3], 'utf8').trim().split('\n').map(line => {
  const index = line.indexOf('=');
  return [line.slice(0, index), line.slice(index + 1)];
}));
const control = config.services['control-plane'];
for (const key of ['QCH_DATABASE_URL', 'QCH_ADMIN_TOKEN', 'QCH_ADMIN_TOKEN_SHA256', 'QCH_CONFIG_ENCRYPTION_KEY', 'QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS', 'QCH_ALLOW_INSECURE_DATABASE']) {
  assert.equal(control.environment[key], expected[key], `${key} must come from .env`);
}
assert.deepEqual(Object.keys(config.services).sort(), ['control-plane', 'qcontrol-web']);
assert.equal(control.image, 'ghcr.io/qimaoww/qcontrol-plane:latest');
assert.equal(config.services['qcontrol-web'].image, 'ghcr.io/qimaoww/qcontrol-web:latest');
assert.equal(control.restart, 'unless-stopped');
assert.equal(config.networks.site.external, true);
assert.equal(config.networks.site.name, 'existing-site-network');
assert.deepEqual(Object.keys(control.networks), ['site']);
assert.equal(control.volumes[0].target, '/run/secrets/db-ca.pem');
assert.equal(control.volumes[0].source, `${process.argv[4]}/db-ca.pem`);
assert.equal(control.volumes[0].read_only, true);
assert.equal(control.environment.QCH_BEHIND_TLS_PROXY, 'true');
assert.equal(config.services['qcontrol-web'].ports[0].host_ip, '127.0.0.1');
assert.equal(config.services['qcontrol-web'].ports[0].published, '18081');
JS

printf '%s\n' 'quick-start real Compose topology/environment regression passed'
