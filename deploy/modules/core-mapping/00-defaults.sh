#!/bin/sh

# Managed core paths this library validates the qagent-* units against. They
# mirror agent.DefaultSpecs() and are only referenced when an existing core was
# actually mapped, which is why an unset value used to abort the installer under
# `set -u` instead of failing closed. Keep them overridable so the installer
# test harness can point them at a fixture tree.
qagent_xray_binary=${qagent_xray_binary:-/usr/local/lib/qagent/cores/xray}
qagent_xray_config=${qagent_xray_config:-/etc/qagent/xray/config.json}
qagent_singbox_binary=${qagent_singbox_binary:-/usr/local/lib/qagent/cores/sing-box}
qagent_singbox_config=${qagent_singbox_config:-/etc/qagent/sing-box/config.json}
qagent_ssrust_binary=${qagent_ssrust_binary:-/usr/local/lib/qagent/cores/ssserver}
qagent_ssrust_config=${qagent_ssrust_config:-/etc/qagent/shadowsocks-rust/config.json}
qagent_ssrust_acl=${qagent_ssrust_acl:-/etc/qagent/shadowsocks-rust/qch-mainland-block.acl}

mapped_engines=""
mapped_xray_binary=""
mapped_xray_config=""
mapped_xray_config_directory=""
mapped_xray_service=""
mapped_singbox_binary=""
mapped_singbox_config=""
mapped_singbox_config_directory=""
mapped_singbox_work_directory=""
mapped_singbox_service_binary=""
mapped_singbox_service=""

# These paths are part of the managed systemd unit contract. Keep them in the
# shared mapping library because both the remote installer and the bootstrap
# script source it independently. When a fresh node already runs Xray or
# sing-box, bootstrap installs the corresponding inactive QAgent unit and then
# immediately performs the full ownership check, so every expected path must
# already be initialized under `set -u`.
qagent_xray_binary=/usr/local/lib/qagent/cores/xray
qagent_xray_config=/etc/qagent/xray/config.json
qagent_singbox_binary=/usr/local/lib/qagent/cores/sing-box
qagent_singbox_config=/etc/qagent/sing-box/config.json

openrc_init_root=${OPENRC_INIT_ROOT:-/etc/init.d}
openrc_state_root=${OPENRC_STATE_ROOT:-/run/openrc}
openrc_run_root=${OPENRC_RUN_ROOT:-/run}
openrc_supervisor_executable=${OPENRC_SUPERVISOR_EXECUTABLE:-/sbin/supervise-daemon}
proc_root=${QCH_PROC_ROOT:-/proc}

case "${service_manager:-systemd}" in
  openrc)
    xray_service_candidates="xray"
    singbox_service_candidates="sing-box singbox"
    ;;
  *)
    xray_service_candidates="xray.service"
    singbox_service_candidates="sing-box.service singbox.service"
    ;;
esac
xray_binary_candidates="/usr/local/bin/xray /usr/bin/xray /etc/xray/bin/xray /etc/v2ray-agent/xray/xray"
xray_config_candidates="/usr/local/etc/xray/config.json /etc/xray/config.json"
singbox_binary_candidates="/usr/local/bin/sing-box /usr/bin/sing-box /etc/sing-box/bin/sing-box /etc/v2ray-agent/sing-box/sing-box"
singbox_direct_binary_candidates="/etc/sing-box/bin/sing-box /etc/v2ray-agent/sing-box/sing-box"
singbox_config_candidates="/etc/sing-box/config.json /usr/local/etc/sing-box/config.json /etc/v2ray-agent/sing-box/conf/config.json"
# Some supported installer archives preserve a numeric file owner that has no
# account on the target host. This fixed list is intentionally not overridable
# through the environment: only known real-core destinations may use the
# inactive-orphan owner exception below.
installer_orphan_owner_binary_candidates="/etc/xray/bin/xray /etc/v2ray-agent/xray/xray /etc/sing-box/bin/sing-box /etc/v2ray-agent/sing-box/sing-box"

append_csv() {
  current=$1
  value=$2
  if [ -n "$current" ]; then
    printf '%s,%s' "$current" "$value"
  else
    printf '%s' "$value"
  fi
}
