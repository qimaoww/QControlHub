#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  printf '%s\n' 'bootstrap-core-services.sh must run as root' >&2
  exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)

for command_name in getent groupadd id install systemctl useradd; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf '%s\n' "required command is unavailable: $command_name" >&2
    exit 1
  }
done

service_user=qcontrolhub-core
service_group=qcontrolhub-core
if ! getent group "$service_group" >/dev/null 2>&1; then
  groupadd --system "$service_group"
fi
if ! id "$service_user" >/dev/null 2>&1; then
  nologin_shell=/usr/sbin/nologin
  [ -x "$nologin_shell" ] || nologin_shell=/bin/false
  useradd --system --gid "$service_group" --home-dir /nonexistent --shell "$nologin_shell" "$service_user"
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

ensure_directory /etc/mihomo
ensure_directory /usr/local/etc/xray
ensure_directory /etc/sing-box
ensure_directory /etc/shadowsocks-rust

install_if_missing "$repository_dir/examples/configs/mihomo-minimal.yaml" /etc/mihomo/config.yaml root "$service_group" 0640
install_if_missing "$repository_dir/examples/configs/xray-minimal.json" /usr/local/etc/xray/config.json root "$service_group" 0640
install_if_missing "$repository_dir/examples/configs/sing-box-minimal.json" /etc/sing-box/config.json root "$service_group" 0640
install_if_missing "$repository_dir/examples/configs/shadowsocks-rust-minimal.json" /etc/shadowsocks-rust/config.json root "$service_group" 0640

for engine in mihomo xray sing-box shadowsocks-rust; do
  install_if_missing "$script_dir/systemd/$engine.service" "/etc/systemd/system/$engine.service" root root 0644
done

systemctl daemon-reload
systemctl enable mihomo.service xray.service sing-box.service shadowsocks-rust.service >/dev/null
printf '%s\n' 'core services are bootstrapped; install each official binary from the QControlHub node page'
