#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  printf '%s\n' "OpenRC existing-core binding test: skipped (needs root)"
  exit 0
fi

test_root=$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-openrc-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

mkdir -p \
  "$test_root/bin" \
  "$test_root/openrc-init" \
  "$test_root/openrc-state/options/qch-test-openrc" \
  "$test_root/openrc-run" \
  "$test_root/proc/100" "$test_root/proc/200" "$test_root/proc/300" \
  "$test_root/core"

cat > "$test_root/bin/rc-service" <<'EOF'
#!/bin/sh
case "$1 $2" in
  'qch-test-openrc status') exit 0 ;;
  *) exit 1 ;;
esac
EOF
chmod 0755 "$test_root/bin/rc-service"

supervisor_exe="$test_root/bin/supervise-daemon"
printf '%s\n' '#!/bin/sh' 'exit 0' > "$supervisor_exe"
chmod 0755 "$supervisor_exe"

core="$test_root/core/xray"
printf '%s\n' '#!/bin/sh' 'exit 0' > "$core"
chmod 0755 "$core"
config="$test_root/core/config.json"
printf '%s\n' '{"inbounds":[]}' > "$config"
chmod 0644 "$config"

init_script="$test_root/openrc-init/qch-test-openrc"
printf '%s\n' '#!/sbin/openrc-run' 'command=/bin/true' 'supervisor=supervise-daemon' > "$init_script"
chmod 0755 "$init_script"

write_openrc_proc() {
  pid=$1
  comm=$2
  ppid=$3
  start_time=$4
  executable=$5
  shift 5
  printf '%s (%s) S %s 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 %s\n' "$pid" "$comm" "$ppid" "$start_time" > "$test_root/proc/$pid/stat"
  ln -sfn "$executable" "$test_root/proc/$pid/exe"
  :
}

write_openrc_cmdline() {
  pid=$1
  shift
  printf '%s\0' "$@" > "$test_root/proc/$pid/cmdline"
}

# Supervisor owns the only service child; the child is a protected core.
write_openrc_proc 100 supervise-daemon 1 4000 "$supervisor_exe"
write_openrc_cmdline 100 supervise-daemon qch-test-openrc --start "$core" -- 100
write_openrc_proc 200 xray 100 5000 "$core"
write_openrc_cmdline 200 "$core" run -config "$config"

printf '%s\n' 200 > "$test_root/openrc-state/options/qch-test-openrc/child_pid"
printf '%s\n' "$test_root/openrc-run/supervise-qch-test-openrc.pid" > "$test_root/openrc-state/options/qch-test-openrc/pidfile"
printf '%s\n' 100 > "$test_root/openrc-run/supervise-qch-test-openrc.pid"

export service_manager=openrc
export OPENRC_INIT_ROOT="$test_root/openrc-init"
export OPENRC_STATE_ROOT="$test_root/openrc-state"
export OPENRC_RUN_ROOT="$test_root/openrc-run"
export QCH_PROC_ROOT="$test_root/proc"
export OPENRC_SUPERVISOR_EXECUTABLE="$supervisor_exe"
export PATH="$test_root/bin:/usr/bin:/bin"

# shellcheck source=../existing-core-mapping.sh
. "$(dirname -- "$0")/../existing-core-mapping.sh"

expect_status() {
  want=$1
  label=$2
  shift 2
  set +e
  "$@" >/dev/null 2>&1
  got=$?
  set -e
  [ "$got" -eq "$want" ] || {
    printf '%s\n' "$label returned $got, want $want" >&2
    exit 1
  }
}

# A protected supervise-daemon child with an exact core invocation is accepted.
expect_status 0 supervised-core service_uses_paths qch-test-openrc "$core" "$config" xray "$core"

# A child executable that drifts away from the mapped core must fail closed.
ln -sfn /bin/echo "$test_root/proc/200/exe"
expect_status 1 child-executable-drift service_uses_paths qch-test-openrc "$core" "$config" xray "$core"
ln -sfn "$core" "$test_root/proc/200/exe"

# A missing supervise-daemon child PID means the layout cannot be proven owned;
# discovery must fail closed rather than guess from a global process scan.
rm -f "$test_root/openrc-state/options/qch-test-openrc/child_pid"
expect_status 1 no-supervised-metadata service_uses_paths qch-test-openrc "$core" "$config" xray "$core"

printf '%s\n' "OpenRC existing-core binding test: ok"
