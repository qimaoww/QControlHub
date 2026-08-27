#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  printf '%s\n' "install-agent redeploy test: skipped (needs root)"
  exit 0
fi

repo_root=$(cd "$(dirname -- "$0")/../.." && pwd)
installer="$repo_root/deploy/remote/install-agent.sh"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-redeploy.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fake_bin="$test_root/bin"
mkdir -p \
  "$fake_bin" \
  "$test_root/etc/qagent" \
  "$test_root/env" \
  "$test_root/state" \
  "$test_root/unit" \
  "$test_root/init" \
  "$test_root/conf" \
  "$test_root/runlevels/default" \
  "$test_root/opt/binary" \
  "$test_root/core"

cat > "$fake_bin/curl" <<'EOF'
#!/bin/sh
set -eu
asset_root=${QCH_ASSET_ROOT:?}
dest=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) dest=$2; shift 2 ;;
    -H|--cacert) shift 2 ;;
    --*) shift ;;
    *) url=$1; shift ;;
  esac
done
[ -n "$dest" ] || { printf '%s\n' 'fake curl: missing -o' >&2; exit 1; }
[ -n "$url" ] || { printf '%s\n' 'fake curl: missing url' >&2; exit 1; }
path=$(printf '%s' "$url" | sed 's#^[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*##')
if [ "$path" = '/api/v1/agent-binary' ]; then
  cp /bin/true "$dest"
  exit 0
fi
asset_path=${path#/install-assets}
[ "$asset_path" != "$path" ] || { printf '%s\n' "fake curl: unknown path $path" >&2; exit 1; }
src="$asset_root$asset_path"
[ -f "$src" ] || { printf '%s\n' "fake curl: missing asset $src" >&2; exit 1; }
cp "$src" "$dest"
EOF

cat > "$fake_bin/systemctl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$QCH_SYSTEMCTL_LOG"
case " $* " in
  *' is-active '*|*' is-enabled '*|*' show '*) exit 1 ;;
esac
exit 0
EOF

# Keep the core service account bootstrap on the "already exists" path so the
# test never creates a real system account or group.
cat > "$fake_bin/id" <<'EOF'
#!/bin/sh
case " $* " in *' -u '*) printf '%s\n' '0' ;; esac
exit 0
EOF

cat > "$fake_bin/getent" <<'EOF'
#!/bin/sh
case "$1 $2" in
  'group qcontrolhub-core') exit 0 ;;
esac
exit 2
EOF

for helper_name in groupadd useradd; do
  printf '%s\n' '#!/bin/sh' 'exit 0' > "$fake_bin/$helper_name"
  chmod 0755 "$fake_bin/$helper_name"
done
chmod 0755 "$fake_bin/curl" "$fake_bin/systemctl" "$fake_bin/id" "$fake_bin/getent"

export QCH_ASSET_ROOT="$repo_root"
export QCH_SYSTEMCTL_LOG="$test_root/systemctl.log"
export QCH_CURL="$fake_bin/curl"
export QCH_SYSTEMCTL="$fake_bin/systemctl"
export QCH_SERVICE_MANAGER=systemd
export QCH_AGENT_BIN_DIR="$test_root/opt/binary"
export QCH_AGENT_BIN_LINK="$test_root/opt/qagent"
export QCH_AGENT_ETC_DIR="$test_root/etc/qagent"
export QCH_AGENT_SERVICE_GROUP=root
export QCH_AGENT_ENV_FILE="$test_root/env/agent.env"
export QCH_AGENT_STATE_FILE="$test_root/state/agent-state.json"
export QCH_CORE_ASSET_ROOT="$test_root/core"
export QCH_SYSTEMD_UNIT_ROOT="$test_root/unit"
export QCH_OPENRC_INIT_ROOT="$test_root/init"
export QCH_OPENRC_CONF_DIR="$test_root/conf"
export QCH_OPENRC_RUNLEVELS_ROOT="$test_root/runlevels"
export PATH="$fake_bin:$PATH"

control="http://sandbox.local"
token="test-enrollment-token"

echo '== first install (fresh node) =='
sh "$installer" "$control" "$token" > "$test_root/first.log"
[ -f "$QCH_AGENT_ENV_FILE" ] || { printf '%s\n' 'first install: agent env missing' >&2; exit 1; }
grep -q '^QCH_AGENT_LABELS=region=cn-east$' "$QCH_AGENT_ENV_FILE" || { printf '%s\n' 'first install: default label missing' >&2; exit 1; }
grep -q '^QCH_ENROLLMENT_TOKEN=' "$QCH_AGENT_ENV_FILE" || { printf '%s\n' 'first install: enrollment token missing' >&2; exit 1; }
grep -q '^QCH_AGENT_NAME=' "$QCH_AGENT_ENV_FILE" || { printf '%s\n' 'first install: agent name missing' >&2; exit 1; }

# Simulate an operator customizing the deployment and the Agent already having
# enrolled (state file present, so the enrollment token must be scrubbed).
printf '%s\n' \
  'QCH_AGENT_NAME=custom-node' \
  'QCH_AGENT_LABELS=region=us-west' \
  'QCH_PUBLIC_IP_PROBE=true' >> "$QCH_AGENT_ENV_FILE"
printf '%s\n' '{"agent_id":"abc123"}' > "$QCH_AGENT_STATE_FILE"

# Mark the unit and the binary as already installed so the redeploy path is
# exercised as an upgrade rather than a fresh install.
cp "$repo_root/deploy/systemd/qagent.service" "$QCH_SYSTEMD_UNIT_ROOT/qagent.service"

echo '== second install (already deployed) =='
sh "$installer" "$control" "$token" > "$test_root/second.log"

assert_env_once() {
  environment_key=$1
  environment_value=$2
  count=$(grep -c "^$environment_key=$environment_value\$" "$QCH_AGENT_ENV_FILE")
  [ "$count" -eq 1 ] || {
    printf '%s\n' "second install: expected exactly one $environment_key=$environment_value, got $count" >&2
    exit 1
  }
}

grep -q '覆盖升级' "$test_root/second.log" || { printf '%s\n' 'second install: expected upgrade notice missing' >&2; exit 1; }
assert_env_once QCH_AGENT_NAME custom-node
assert_env_once QCH_AGENT_LABELS 'region=us-west'
assert_env_once QCH_PUBLIC_IP_PROBE true
if grep -q '^QCH_ENROLLMENT_TOKEN=' "$QCH_AGENT_ENV_FILE"; then
  printf '%s\n' 'second install: enrollment token not scrubbed despite state file' >&2
  exit 1
fi

restart_count=$(grep -c '^restart qagent.service$' "$QCH_SYSTEMCTL_LOG" || true)
[ "$restart_count" -ge 2 ] || { printf '%s\n' "second install: expected agent restart, got $restart_count" >&2; exit 1; }

echo '== update existing agent =='
sh "$installer" update "$control" "$token" > "$test_root/update.log"
grep -q '更新已有' "$test_root/update.log" || { printf '%s\n' 'update: expected update notice missing' >&2; exit 1; }
assert_env_once QCH_AGENT_NAME custom-node
assert_env_once QCH_AGENT_LABELS 'region=us-west'
assert_env_once QCH_PUBLIC_IP_PROBE true
if grep -q '^QCH_ENROLLMENT_TOKEN=' "$QCH_AGENT_ENV_FILE"; then
  printf '%s\n' 'update: enrollment token not scrubbed despite state file' >&2
  exit 1
fi

echo '== migrate to another control plane =='
new_control="http://newpanel.local"
sh "$installer" migrate "$new_control" "$token" > "$test_root/migrate.log"
grep -q '迁移到新的控制面板' "$test_root/migrate.log" || { printf '%s\n' 'migrate: expected migration notice missing' >&2; exit 1; }
assert_env_once QCH_SERVER_URL 'http://newpanel.local'
assert_env_once QCH_AGENT_NAME custom-node
assert_env_once QCH_AGENT_LABELS 'region=us-west'
assert_env_once QCH_PUBLIC_IP_PROBE true
if grep -q '^QCH_REENROLL=' "$QCH_AGENT_ENV_FILE"; then
  printf '%s\n' 'migrate: re-enroll flag not scrubbed despite state file' >&2
  exit 1
fi
if grep -q '^QCH_ENROLLMENT_TOKEN=' "$QCH_AGENT_ENV_FILE"; then
  printf '%s\n' 'migrate: enrollment token not scrubbed despite state file' >&2
  exit 1
fi

echo '== uninstall agent =='
sh "$installer" uninstall > "$test_root/uninstall.log"
[ ! -e "$QCH_AGENT_ENV_FILE" ] || { printf '%s\n' 'uninstall: env file still present' >&2; exit 1; }
[ ! -e "$QCH_SYSTEMD_UNIT_ROOT/qagent.service" ] || { printf '%s\n' 'uninstall: unit still present' >&2; exit 1; }
[ ! -e "$QCH_AGENT_BIN_DIR/qagent" ] || { printf '%s\n' 'uninstall: binary still present' >&2; exit 1; }
[ ! -e "$QCH_AGENT_ETC_DIR" ] || { printf '%s\n' 'uninstall: config dir still present' >&2; exit 1; }
[ ! -e "$QCH_AGENT_BIN_LINK" ] || { printf '%s\n' 'uninstall: compatibility link still present' >&2; exit 1; }
[ -d "$test_root/state" ] || { printf '%s\n' 'uninstall: state dir should be preserved' >&2; exit 1; }

printf '%s\n' 'install-agent redeploy: OK'
