#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  printf '%s\n' 'bootstrap-core-services.sh must run as root' >&2
  exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
openrc_runlevels_root=${OPENRC_RUNLEVELS_ROOT:-/etc/runlevels}

# OpenRC's rc-update exits non-zero when a service is already enabled, which
# aborts this script under `set -eu` on a repeat bootstrap. Enable only when the
# runlevel link is absent so repeated Agent engine tasks stay idempotent.
enable_openrc_service() {
  service_name=$1
  if [ -e "$openrc_runlevels_root/default/$service_name" ]; then
    printf '%s\n' "openrc service already enabled: $service_name"
    return
  fi
  rc-update add "$service_name" default >/dev/null
  printf '%s\n' "enabled openrc service: $service_name"
}

requested_engine=${1:-all}
case "$requested_engine" in
  --prepare-agent) selected_engines="" ;;
  all) selected_engines="mihomo xray sing-box shadowsocks-rust" ;;
  mihomo|xray|sing-box|shadowsocks-rust) selected_engines=$requested_engine ;;
  *)
    printf '%s\n' 'usage: bootstrap-core-services.sh [all|mihomo|xray|sing-box|shadowsocks-rust|--prepare-agent]' >&2
    exit 1
    ;;
esac

service_manager=${QCH_SERVICE_MANAGER:-}
if [ -z "$service_manager" ]; then
  if [ -f /etc/alpine-release ] && command -v rc-service >/dev/null 2>&1; then
    service_manager=openrc
  else
    service_manager=systemd
  fi
fi
case "$service_manager" in
  systemd) required_commands="chmod chown cmp getent grep groupadd id install systemctl useradd" ;;
  openrc) required_commands="addgroup adduser chmod chown cmp grep id install rc-service rc-update supervise-daemon" ;;
  *) printf '%s\n' "unsupported service manager: $service_manager" >&2; exit 1 ;;
esac
mapping_library="$script_dir/existing-core-mapping.sh"
[ -r "$mapping_library" ] || { printf '%s\n' "required mapping library is unavailable: $mapping_library" >&2; exit 1; }
. "$mapping_library"
for command_name in $required_commands; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf '%s\n' "required command is unavailable: $command_name" >&2
    exit 1
  }
done

service_user=qcontrolhub-core
service_group=qcontrolhub-core
if [ "$service_manager" = openrc ]; then
  if ! grep -q "^${service_group}:" /etc/group; then addgroup -S "$service_group"; fi
  if ! id "$service_user" >/dev/null 2>&1; then
    nologin_shell=/sbin/nologin
    [ -x "$nologin_shell" ] || nologin_shell=/bin/false
    adduser -S -D -H -h /nonexistent -s "$nologin_shell" -G "$service_group" "$service_user"
  fi
else
  if ! getent group "$service_group" >/dev/null 2>&1; then groupadd --system "$service_group"; fi
  if ! id "$service_user" >/dev/null 2>&1; then
    nologin_shell=/usr/sbin/nologin
    [ -x "$nologin_shell" ] || nologin_shell=/bin/false
    useradd --system --gid "$service_group" --home-dir /nonexistent --shell "$nologin_shell" "$service_user"
  fi
fi

if [ "$requested_engine" = --prepare-agent ]; then
  printf '%s\n' 'QAgent core service account is prepared; no core configuration or service was installed'
  exit 0
fi

ensure_directory() {
  destination=$1
  if [ -L "$destination" ]; then
    printf '%s\n' "refusing symlinked directory: $destination" >&2
    exit 1
  fi
  if [ ! -d "$destination" ]; then
    install -d -o root -g "$service_group" -m 0750 "$destination"
  fi
}

ensure_service_state_directory() {
  destination=$1
  if [ -L "$destination" ]; then
    printf '%s\n' "refusing symlinked state directory: $destination" >&2
    exit 1
  fi
  if [ -e "$destination" ] && [ ! -d "$destination" ]; then
    printf '%s\n' "refusing non-directory state path: $destination" >&2
    exit 1
  fi
  if [ ! -d "$destination" ]; then
    install -d -o root -g root -m 0750 "$destination"
  fi
  # Capability-bounded root has CAP_CHOWN but deliberately lacks CAP_FOWNER.
  # Keep root as owner while setting the mode, then transfer ownership last.
  chown root:root "$destination"
  chmod 0750 "$destination"
  chown "$service_user:$service_group" "$destination"
}

install_managed_unit() {
  source_file=$1
  destination=$2
  if [ -L "$destination" ]; then
    printf '%s\n' "refusing symlinked managed unit: $destination" >&2
    exit 1
  fi
  if [ -e "$destination" ]; then
    if [ ! -f "$destination" ]; then
      printf '%s\n' "refusing non-regular managed unit: $destination" >&2
      exit 1
    fi
    if ! grep -q '^Description=.* managed by QAgent$' "$destination"; then
      printf '%s\n' "preserved non-QAgent unit: $destination"
      return
    fi
    if cmp -s "$source_file" "$destination"; then
      printf '%s\n' "managed unit already current: $destination"
      return
    fi
  fi
  install -o root -g root -m 0644 "$source_file" "$destination"
  printf '%s\n' "installed managed unit: $destination"
}

install_managed_openrc_service() {
  source_file=$1
  destination=$2
  if [ -L "$destination" ]; then
    printf '%s\n' "refusing symlinked managed OpenRC service: $destination" >&2
    exit 1
  fi
  if [ -e "$destination" ]; then
    if [ ! -f "$destination" ]; then
      printf '%s\n' "refusing non-regular managed OpenRC service: $destination" >&2
      exit 1
    fi
    if ! grep -q '^# QControlHub managed OpenRC service:' "$destination"; then
      printf '%s\n' "preserved non-QAgent OpenRC service: $destination"
      return
    fi
    if cmp -s "$source_file" "$destination"; then
      printf '%s\n' "managed OpenRC service already current: $destination"
      return
    fi
  fi
  install -o root -g root -m 0755 "$source_file" "$destination"
  printf '%s\n' "installed managed OpenRC service: $destination"
}

install_if_missing() {
  source_file=$1
  destination=$2
  owner=$3
  group=$4
  mode=$5
  if [ -L "$destination" ]; then
    printf '%s\n' "refusing symlinked destination: $destination" >&2
    exit 1
  fi
  if [ -e "$destination" ]; then
    printf '%s\n' "preserved existing file: $destination"
    return
  fi
  install -o "$owner" -g "$group" -m "$mode" "$source_file" "$destination"
  printf '%s\n' "installed: $destination"
}

ensure_directory /usr/local/lib/qagent
ensure_directory /usr/local/lib/qagent/cores

for engine in $selected_engines; do
  ensure_directory "/etc/qagent/$engine"
  state_directory="/var/lib/qcontrolhub-$engine"
  if [ -L "$state_directory" ]; then
    printf '%s\n' "refusing symlinked state directory: $state_directory" >&2
    exit 1
  fi
  ensure_service_state_directory "$state_directory"
  case "$engine" in
    mihomo) source_config="$repository_dir/examples/configs/mihomo-minimal.yaml"; destination_config=/etc/qagent/mihomo/config.yaml ;;
    xray) source_config="$repository_dir/examples/configs/xray-minimal.json"; destination_config=/etc/qagent/xray/config.json ;;
    sing-box) source_config="$repository_dir/examples/configs/sing-box-minimal.json"; destination_config=/etc/qagent/sing-box/config.json ;;
    shadowsocks-rust) source_config="$repository_dir/examples/configs/shadowsocks-rust-minimal.json"; destination_config=/etc/qagent/shadowsocks-rust/config.json ;;
  esac
  install_if_missing "$source_config" "$destination_config" root "$service_group" 0640
done

enabled_services=""
skipped_engines=""
for engine in $selected_engines; do
  require_skipped_core_service_inactive "$engine"
  if [ "$service_manager" = openrc ]; then
    managed_service="qagent-$engine"
    install_managed_openrc_service "$script_dir/openrc/$managed_service" "/etc/init.d/$managed_service"
  else
    managed_service="qagent-$engine.service"
    install_managed_unit "$script_dir/systemd/$managed_service" "/etc/systemd/system/$managed_service"
  fi
  if skip_core_service "$engine"; then
    skipped_engines="$skipped_engines $engine"
  else
    enabled_services="$enabled_services $managed_service"
  fi
done

if [ "$service_manager" = openrc ]; then
  for engine in $skipped_engines; do
    disable_skipped_core_service "$engine"
    printf '%s\n' "kept existing $engine service; disabled qagent-$engine"
  done
  for managed_service in $enabled_services; do enable_openrc_service "$managed_service"; done
else
  journal_config_dir=/etc/systemd/journald@qagent-cores.conf.d
  if [ -L "$journal_config_dir" ]; then
    printf '%s\n' "refusing symlinked journal configuration directory: $journal_config_dir" >&2
    exit 1
  fi
  install -d -o root -g root -m 0755 "$journal_config_dir"
  journal_config=$journal_config_dir/10-qcontrolhub-volatile.conf
  if [ -L "$journal_config" ]; then
    printf '%s\n' "refusing symlinked journal configuration: $journal_config" >&2
    exit 1
  fi
  install -o root -g root -m 0644 "$script_dir/systemd/qagent-core-journal.conf" "$journal_config"
  systemctl daemon-reload
  for engine in $skipped_engines; do
    disable_skipped_core_service "$engine"
    printf '%s\n' "kept existing $engine service; disabled qagent-$engine.service"
  done
  [ -z "$enabled_services" ] || systemctl enable $enabled_services >/dev/null
fi
printf '%s\n' "core service bootstrap completed for: $selected_engines"
