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

# Release signature fixtures. The installer verifies a `sha256sum -c` list signed
# with an Ed25519 release key, so the sandbox signs a real list with a throwaway
# key. openssl is present in both CI images; without it the redeploy flow cannot
# exercise the verification path, so say so instead of silently skipping it.
if ! command -v openssl >/dev/null 2>&1; then
  printf '%s\n' "install-agent redeploy test: skipped (needs openssl)"
  exit 0
fi
release_key="$test_root/release-key.pem"
release_pub="$test_root/release-key.pub"
openssl genpkey -algorithm ED25519 -out "$release_key" 2>/dev/null
openssl pkey -in "$release_key" -pubout -out "$release_pub" 2>/dev/null

# write_release_checksums builds and signs the artifact list the fake control
# plane serves. It hashes exactly what the fake curl hands out, so the happy path
# and the tampering path differ only by the bytes on the wire.
write_release_checksums() {
  list="$1"
  signature="$2"
  (
    cd "$repo_root"
    printf '%s  %s\n' "$(sha256sum /bin/true | cut -d' ' -f1)" "api/v1/agent-binary"
    bash deploy/tests/release-assets.sh --files | while IFS= read -r asset
    do
      printf '%s  %s\n' "$(sha256sum "$asset" | cut -d' ' -f1)" "$asset"
    done
  ) | LC_ALL=C sort > "$list"
  openssl pkeyutl -sign -inkey "$release_key" -rawin -in "$list" -out "$test_root/checksums.raw"
  openssl base64 -A -in "$test_root/checksums.raw" -out "$signature"
}
# The signed list is served from an override directory, so the repository tree is
# never written to and every case can swap the bytes the control plane returns.
checksums_root="$test_root/checksums"
mkdir -p "$checksums_root/install-assets"
write_release_checksums "$checksums_root/install-assets/SHA256SUMS" "$checksums_root/install-assets/SHA256SUMS.sig"
# A correctly signed list whose digest does not match the served bytes exercises
# artifact substitution: the signature is fine, the content is not.
mkdir -p "$test_root/substituted/install-assets"
write_release_checksums "$test_root/substituted/install-assets/SHA256SUMS" "$test_root/substituted/install-assets/SHA256SUMS.sig"
awk '{
  if ($2 == "deploy/existing-core-mapping.sh") $0 = (substr($0, 1, 1) == "0" ? "1" : "0") substr($0, 2)
  print
}' "$test_root/substituted/install-assets/SHA256SUMS" > "$test_root/substituted/install-assets/SHA256SUMS.next"
mv "$test_root/substituted/install-assets/SHA256SUMS.next" "$test_root/substituted/install-assets/SHA256SUMS"
openssl pkeyutl -sign -inkey "$release_key" -rawin -in "$test_root/substituted/install-assets/SHA256SUMS" -out "$test_root/checksums.raw"
openssl base64 -A -in "$test_root/checksums.raw" -out "$test_root/substituted/install-assets/SHA256SUMS.sig"
# A list whose bytes changed after signing exercises the signature check.
mkdir -p "$test_root/broken-signature/install-assets"
write_release_checksums "$test_root/broken-signature/install-assets/SHA256SUMS" "$test_root/broken-signature/install-assets/SHA256SUMS.sig"
printf 'garbage\n' >> "$test_root/broken-signature/install-assets/SHA256SUMS"
mkdir -p "$test_root/missing-entry/install-assets"
grep -v '  deploy/existing-core-mapping.sh$' "$checksums_root/install-assets/SHA256SUMS" > "$test_root/missing-entry/install-assets/SHA256SUMS"
openssl pkeyutl -sign -inkey "$release_key" -rawin -in "$test_root/missing-entry/install-assets/SHA256SUMS" -out "$test_root/checksums.raw"
openssl base64 -A -in "$test_root/checksums.raw" -out "$test_root/missing-entry/install-assets/SHA256SUMS.sig"

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
# QCH_ASSET_OVERRIDE lets a case swap one served asset (the signed checksum list,
# its signature, or an agent binary) without rebuilding the whole asset root.
if [ -n "${QCH_ASSET_OVERRIDE:-}" ] && [ -f "$QCH_ASSET_OVERRIDE$path" ]; then
  src="$QCH_ASSET_OVERRIDE$path"
fi
[ -f "$src" ] || { printf '%s\n' "fake curl: missing asset $src" >&2; exit 1; }
cp "$src" "$dest"
EOF

cat > "$fake_bin/systemctl" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$QCH_SYSTEMCTL_LOG"
case " $* " in
  *' is-active '*|*' is-enabled '*|*' show '*) exit 1 ;;
esac
if [ "$*" = 'restart qagent.service' ] && [ "${QCH_SIMULATE_AGENT_ENROLLMENT:-true}" = true ]; then
  server_url=$(sed -n 's/^QCH_SERVER_URL=//p' "$QCH_AGENT_ENV_FILE" | tail -n 1)
  state_path=$(sed -n 's/^QCH_AGENT_STATE=//p' "$QCH_AGENT_ENV_FILE" | tail -n 1)
  server_host=${server_url#*://}
  restart_count=$(grep -c '^restart qagent.service$' "$QCH_SYSTEMCTL_LOG")
  mkdir -p "$(dirname "$state_path")"
  printf '{"agent_id":"abc123","private_key":"fake-key-%s","server":"%s"}\n' "$restart_count" "$server_host" > "$state_path"
fi
exit 0
EOF

cat > "$fake_bin/rc-service" <<'EOF'
#!/bin/sh
set -eu
case "$*" in
  'qagent status') [ -e "$QCH_OPENRC_INIT_ROOT/.running" ]; exit ;;
  'qagent start'|'qagent restart')
    server_url=$(sed -n 's/^QCH_SERVER_URL=//p' "$QCH_AGENT_ENV_FILE" | tail -n 1)
    state_path=$(sed -n 's/^QCH_AGENT_STATE=//p' "$QCH_AGENT_ENV_FILE" | tail -n 1)
    mkdir -p "$(dirname "$state_path")"
    printf '{"agent_id":"abc123","private_key":"fake-openrc-key","server":"%s"}\n' "${server_url#*://}" > "$state_path"
    touch "$QCH_OPENRC_INIT_ROOT/.running"
    ;;
esac
exit 0
EOF
chmod 0755 "$fake_bin/rc-service"

cat > "$fake_bin/apt-get" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$QCH_PACKAGE_LOG"
case " $* " in
  *' install '*' nftables '*)
    printf '%s\n' '#!/bin/sh' 'exit 0' > "$QCH_NFT"
    chmod 0755 "$QCH_NFT"
    ;;
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

for helper_name in groupadd useradd addgroup adduser rc-update supervise-daemon; do
  printf '%s\n' '#!/bin/sh' 'exit 0' > "$fake_bin/$helper_name"
  chmod 0755 "$fake_bin/$helper_name"
done
chmod 0755 "$fake_bin/curl" "$fake_bin/systemctl" "$fake_bin/apt-get" "$fake_bin/id" "$fake_bin/getent"

export QCH_ASSET_ROOT="$repo_root"
export QCH_SYSTEMCTL_LOG="$test_root/systemctl.log"
export QCH_PACKAGE_LOG="$test_root/package.log"
export QCH_CURL="$fake_bin/curl"
export QCH_SYSTEMCTL="$fake_bin/systemctl"
export QCH_NFT="$fake_bin/nft"
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
# The fake service uses a non-loopback HTTP origin. Opt in with the same flag
# the real Agent requires; a CA file does not turn HTTP into TLS.
ca_file="$test_root/control-plane-ca.pem"
printf '%s\n' 'sandbox placeholder CA' > "$ca_file"
export QCH_TLS_CA_FILE="$ca_file"
export QCH_ALLOW_INSECURE_LIVE=true
export PATH="$fake_bin:$PATH"

control="http://sandbox.local"
token="test-enrollment-token"
# The fake control plane serves the signed checksum list, so every install below
# verifies the release unless a case deliberately overrides this.
export QCH_RELEASE_PUBLIC_KEY="$release_pub"
export QCH_ASSET_OVERRIDE="$checksums_root"

echo '== first install (fresh node) =='
sh "$installer" "$control" "$token" > "$test_root/first.log"
[ -x "$QCH_NFT" ] || { printf '%s\n' 'first install: nftables executable was not installed' >&2; exit 1; }
grep -q '^update -qq$' "$QCH_PACKAGE_LOG" || { printf '%s\n' 'first install: APT metadata was not updated for nftables' >&2; exit 1; }
grep -q '^install -y --no-install-recommends nftables$' "$QCH_PACKAGE_LOG" || { printf '%s\n' 'first install: nftables APT package was not installed' >&2; exit 1; }
[ -f "$QCH_AGENT_ENV_FILE" ] || { printf '%s\n' 'first install: agent env missing' >&2; exit 1; }
installed_release_key="$test_root/env/release-key.pub.pem"
grep -Fxq "QCH_RELEASE_PUBLIC_KEY=$installed_release_key" "$QCH_AGENT_ENV_FILE"
cmp "$release_pub" "$installed_release_key"
grep -q '^QCH_AGENT_LABELS=region=cn-east$' "$QCH_AGENT_ENV_FILE" || { printf '%s\n' 'first install: default label missing' >&2; exit 1; }
grep -q '^QCH_AGENT_NAME=' "$QCH_AGENT_ENV_FILE" || { printf '%s\n' 'first install: agent name missing' >&2; exit 1; }
if grep -q '^QCH_ENROLLMENT_TOKEN=' "$QCH_AGENT_ENV_FILE"; then
  printf '%s\n' 'first install: enrollment token remained after confirmed enrollment' >&2
  exit 1
fi

# Simulate an operator customizing the deployment and the Agent already having
# enrolled (state file present, so the enrollment token must be scrubbed).
custom_state="$test_root/state/custom/agent-state.json"
mkdir -p "$(dirname "$custom_state")"
mv "$QCH_AGENT_STATE_FILE" "$custom_state"
printf '%s\n' \
  'QCH_AGENT_NAME=custom-node' \
  'QCH_AGENT_LABELS=region=us-west' \
  'QCH_PUBLIC_IP_PROBE=true' \
  "QCH_AGENT_STATE=$custom_state" >> "$QCH_AGENT_ENV_FILE"

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
if grep -q '^QCH_ENROLLMENT_TOKEN=' "$QCH_AGENT_ENV_FILE"; then
  printf '%s\n' 'migrate: enrollment token not scrubbed despite state file' >&2
  exit 1
fi

echo '== interrupted migration retains credentials =='
export QCH_SIMULATE_AGENT_ENROLLMENT=false
export QCH_AGENT_ENROLLMENT_WAIT_SECONDS=1
failed_control="http://offline-panel.local"
if sh "$installer" migrate "$failed_control" "$token" > "$test_root/failed-migrate.log" 2>&1; then
  printf '%s\n' 'interrupted migration: installer unexpectedly succeeded' >&2
  exit 1
fi
grep -q '^QCH_ENROLLMENT_TOKEN=' "$QCH_AGENT_ENV_FILE" || { printf '%s\n' 'interrupted migration: enrollment token was removed' >&2; exit 1; }
grep -q 'service was stopped and enrollment credentials were retained' "$test_root/failed-migrate.log" || { printf '%s\n' 'interrupted migration: retry guidance missing' >&2; exit 1; }
grep -q '^stop qagent.service$' "$QCH_SYSTEMCTL_LOG" || { printf '%s\n' 'interrupted migration: Agent service was not stopped' >&2; exit 1; }

echo '== retry interrupted migration =='
export QCH_SIMULATE_AGENT_ENROLLMENT=true
sh "$installer" migrate "$failed_control" "$token" > "$test_root/retry-migrate.log"
assert_env_once QCH_SERVER_URL 'http://offline-panel.local'
if grep -q '^QCH_ENROLLMENT_TOKEN=' "$QCH_AGENT_ENV_FILE"; then
  printf '%s\n' 'migration retry: temporary credentials were not scrubbed' >&2
  exit 1
fi

echo '== bare host defaults to https (no plaintext downgrade) =='
sh "$installer" update sandbox.example.com "$token" > "$test_root/bare-host.log"
assert_env_once QCH_SERVER_URL 'https://sandbox.example.com'
if grep -q '^QCH_ALLOW_HTTP=' "$QCH_AGENT_ENV_FILE"; then
  printf '%s\n' 'bare host: QCH_ALLOW_HTTP was set for an https control plane' >&2
  exit 1
fi

echo '== a CA file cannot authorize remote plaintext or loopback-looking DNS =='
for cleartext_url in \
  http://cleartext-panel.local \
  http://127.attacker.invalid \
  http://127.0.0.1.attacker.invalid \
  ws://cleartext-panel.local
do
  if QCH_ALLOW_INSECURE_LIVE=false sh "$installer" update "$cleartext_url" "$token" > "$test_root/cleartext.log" 2>&1; then
    printf '%s\n' "cleartext guard: installer accepted $cleartext_url without explicit opt-in" >&2
    exit 1
  fi
  grep -qi 'refusing a plaintext control-plane URL' "$test_root/cleartext.log"
  assert_env_once QCH_SERVER_URL 'https://sandbox.example.com'
done

echo '== explicit remote plaintext opt-in does not require a meaningless CA file =='
QCH_TLS_CA_FILE= sh "$installer" update "$control" "$token" > "$test_root/insecure-opt-in.log"
assert_env_once QCH_ALLOW_HTTP true
assert_env_once QCH_ALLOW_INSECURE_LIVE true

echo '== IPv4 and bracketed IPv6 loopback work without insecure-live opt-in =='
for loopback_url in http://localhost:8080 http://127.0.0.1:8080 'http://[::1]:8080' 'ws://[::1]:8080'
do
  QCH_ALLOW_INSECURE_LIVE=false QCH_TLS_CA_FILE= \
    sh "$installer" update "$loopback_url" "$token" > "$test_root/loopback.log"
  grep -Fxq "QCH_SERVER_URL=$loopback_url" "$QCH_AGENT_ENV_FILE"
done

# A control plane that substitutes an artifact must be rejected even though the
# substituted file arrives over a valid connection from the expected host.
echo '== tampered artifact is rejected =='
if QCH_ASSET_OVERRIDE="$test_root/substituted" sh "$installer" update "$control" "$token" > "$test_root/tampered.log" 2>&1; then
  printf '%s\n' 'tampered artifact: installer accepted content that failed the signed list' >&2
  exit 1
fi
grep -q 'do not match the signed checksum list' "$test_root/tampered.log" || {
  printf '%s\n' 'tampered artifact: expected checksum mismatch was not reported' >&2
  sed 's/^/  installer: /' "$test_root/tampered.log" >&2
  exit 1
}

# Editing the checksum list after it was signed must break the signature check,
# otherwise the list would be no better than an unsigned one.
echo '== modified checksum list is rejected =='
if QCH_ASSET_OVERRIDE="$test_root/broken-signature" sh "$installer" update "$control" "$token" > "$test_root/broken.log" 2>&1; then
  printf '%s\n' 'modified checksum list: installer accepted a list that no longer matches its signature' >&2
  exit 1
fi
grep -q 'RELEASE SIGNATURE VERIFICATION FAILED' "$test_root/broken.log" || {
  printf '%s\n' 'modified checksum list: expected signature failure was not reported' >&2
  exit 1
}

echo '== a valid signature must cover every required download =='
if QCH_ASSET_OVERRIDE="$test_root/missing-entry" sh "$installer" update "$control" "$token" > "$test_root/missing-entry.log" 2>&1; then
  printf '%s\n' 'missing checksum entry: installer accepted an unverified required script' >&2
  exit 1
fi
grep -q 'required download is absent from signed checksum list: deploy/existing-core-mapping.sh' "$test_root/missing-entry.log"

# A pinned key with no published signature must fail closed, so removing the
# endpoint cannot silently downgrade the deployment to unverified installs.
echo '== pinned key without a published signature fails closed =='
mkdir -p "$test_root/no-checksums"
if QCH_RELEASE_PUBLIC_KEY= QCH_ASSET_OVERRIDE="$test_root/no-checksums" sh "$installer" update "$control" "$token" > "$test_root/nosig.log" 2>&1; then
  printf '%s\n' 'missing signature: installer proceeded despite a pinned release key' >&2
  exit 1
fi
grep -q 'failed to download the signed checksum list' "$test_root/nosig.log" || {
  printf '%s\n' 'missing signature: expected download failure was not reported' >&2
  exit 1
}

# The explicit opt-out keeps a control plane that publishes no signature usable.
echo '== explicit opt-out allows an unsigned release =='
if ! QCH_ASSET_OVERRIDE="$test_root/no-checksums" QCH_RELEASE_PUBLIC_KEY= QCH_ALLOW_UNSIGNED_RELEASE=true \
  sh "$installer" update "$control" "$token" > "$test_root/unsigned.log" 2>&1; then
  printf '%s\n' 'unsigned opt-out: installer refused an explicitly allowed unsigned release' >&2
  exit 1
fi
assert_env_once QCH_RELEASE_PUBLIC_KEY "$installed_release_key"

echo '== signed OpenRC install uses the same complete release list =='
QCH_RELEASE_PUBLIC_KEY= QCH_SERVICE_MANAGER=openrc QCH_RC_SERVICE="$fake_bin/rc-service" QCH_RC_UPDATE=/bin/true \
  sh "$installer" update "$control" "$token" > "$test_root/openrc.log"
grep -q 'release signature verified' "$test_root/openrc.log"
assert_env_once QCH_RELEASE_PUBLIC_KEY "$installed_release_key"
grep -Fxq "export QCH_RELEASE_PUBLIC_KEY='$installed_release_key'" "$QCH_OPENRC_CONF_DIR/qagent"
QCH_SERVICE_MANAGER=openrc QCH_RC_SERVICE="$fake_bin/rc-service" QCH_RC_UPDATE=/bin/true \
  sh "$installer" uninstall > "$test_root/openrc-uninstall.log"

echo '== uninstall agent =='
sh "$installer" uninstall > "$test_root/uninstall.log"
[ ! -e "$QCH_AGENT_ENV_FILE" ] || { printf '%s\n' 'uninstall: env file still present' >&2; exit 1; }
[ ! -e "$QCH_SYSTEMD_UNIT_ROOT/qagent.service" ] || { printf '%s\n' 'uninstall: unit still present' >&2; exit 1; }
[ ! -e "$QCH_AGENT_BIN_DIR/qagent" ] || { printf '%s\n' 'uninstall: binary still present' >&2; exit 1; }
[ ! -e "$QCH_AGENT_ETC_DIR" ] || { printf '%s\n' 'uninstall: config dir still present' >&2; exit 1; }
[ ! -e "$QCH_AGENT_BIN_LINK" ] || { printf '%s\n' 'uninstall: compatibility link still present' >&2; exit 1; }
[ -d "$test_root/state" ] || { printf '%s\n' 'uninstall: state dir should be preserved' >&2; exit 1; }

printf '%s\n' 'install-agent redeploy: OK'
