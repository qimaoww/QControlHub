#!/bin/sh
# install-agent.sh — QControlHub agent 一键安装（root 执行，无需预装仓库）
#
# 用法：
#   sh deploy/remote/install-agent.sh install   <control-plane-url|ip[:port]> <add-node-credential> [agent-name]
#   sh deploy/remote/install-agent.sh update    <control-plane-url|ip[:port]> <add-node-credential> [agent-name]
#   sh deploy/remote/install-agent.sh migrate   <new-control-plane-url|ip[:port]> <add-node-credential> [agent-name]
#   sh deploy/remote/install-agent.sh uninstall
#
# 示例：
#   QCH_TLS_CA_FILE=/etc/qcontrolhub/control-plane-ca.pem \
#   sh deploy/remote/install-agent.sh install https://qcontrolhub.example.com <token> shanghai-edge-01
# 兼容旧式：省略首位的 install 动作时，脚本仍按安装处理。
#
# 从控制面 GET /api/v1/agent-binary 下载 agent 可执行文件，引导核心服务，
# 写入 /etc/qcontrolhub/agent.env，安装 systemd 或 OpenRC 服务并启动。
set -eu

[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'install-agent.sh must run as root' >&2; exit 1; }

action=${1:-install}
if [ "$#" -gt 0 ]; then
  case "$1" in
    install|update|migrate|uninstall)
      action=$1
      shift
      ;;
  esac
fi

# Keep every managed path and helper command overridable so an idempotency
# regression harness can run against a sandbox tree. Production defaults are
# unchanged.
qagent_bin_dir=${QCH_AGENT_BIN_DIR:-/usr/local/lib/qagent}
qagent_bin_link=${QCH_AGENT_BIN_LINK:-/usr/local/bin/qagent}
qagent_etc_dir=${QCH_AGENT_ETC_DIR:-/etc/qagent}
core_service_group=${QCH_AGENT_SERVICE_GROUP:-qcontrolhub-core}
agent_env_file=${QCH_AGENT_ENV_FILE:-/etc/qcontrolhub/agent.env}
agent_state_file=${QCH_AGENT_STATE_FILE:-/var/lib/qcontrolhub/agent-state.json}
enrollment_wait_seconds=${QCH_AGENT_ENROLLMENT_WAIT_SECONDS:-45}
core_asset_root=${QCH_CORE_ASSET_ROOT:-/usr/local/share/qcontrolhub/core-install}
core_share_root=$(dirname "$core_asset_root")
agent_state_dir=$(dirname "$agent_state_file")
service_unit_dir=${QCH_SYSTEMD_UNIT_ROOT:-/etc/systemd/system}
openrc_init_dir=${QCH_OPENRC_INIT_ROOT:-/etc/init.d}
openrc_conf_dir=${QCH_OPENRC_CONF_DIR:-/etc/conf.d}
openrc_runlevels_root=${QCH_OPENRC_RUNLEVELS_ROOT:-/etc/runlevels}
systemctl_cmd=${QCH_SYSTEMCTL:-systemctl}
rc_service_cmd=${QCH_RC_SERVICE:-rc-service}
rc_update_cmd=${QCH_RC_UPDATE:-rc-update}
download_cmd=${QCH_CURL:-curl}
nft_cmd=${QCH_NFT:-/usr/sbin/nft}
agent_conf_dir=$(dirname "$agent_env_file")

validate_environment_value() {
  environment_key=$1
  environment_value=$2
  sanitized_value=$(printf '%s' "$environment_value" | tr -d '\r\n')
  [ "$sanitized_value" = "$environment_value" ] || {
    printf '%s\n' "refusing multiline Agent environment value: $environment_key" >&2
    exit 1
  }
}

service_manager=${QCH_SERVICE_MANAGER:-}
if [ -z "$service_manager" ]; then
  if [ -f /etc/alpine-release ]; then
    command -v apk >/dev/null 2>&1 || { printf '%s\n' 'Alpine apk is unavailable' >&2; exit 1; }
    apk add --no-cache ca-certificates coreutils curl iproute2 libcap nftables openrc >/dev/null
    service_manager=openrc
  elif command -v "$systemctl_cmd" >/dev/null 2>&1; then
    service_manager=systemd
  else
    printf '%s\n' 'unsupported init system: systemd or Alpine OpenRC is required' >&2
    exit 1
  fi
fi
case "$service_manager" in
  systemd|openrc) ;;
  *) printf '%s\n' "unsupported init system: $service_manager" >&2; exit 1 ;;
esac
case "$enrollment_wait_seconds" in
  ""|0|*[!0-9]*) printf '%s\n' 'QCH_AGENT_ENROLLMENT_WAIT_SECONDS must be a positive integer' >&2; exit 1 ;;
esac
