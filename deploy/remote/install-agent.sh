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
    apk add --no-cache ca-certificates coreutils curl libcap nftables openrc >/dev/null
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

nftables_available() {
  if [ -n "${QCH_NFT:-}" ]; then
    [ -x "$nft_cmd" ]
    return
  fi
  [ -x "$nft_cmd" ] || command -v nft >/dev/null 2>&1
}

install_nftables() {
  nftables_available && return 0
  printf '%s\n' 'nft not found; installing the nftables package for port traffic monitoring'
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -qq || {
      printf '%s\n' 'failed to update APT package metadata for nftables' >&2
      exit 1
    }
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with APT' >&2
      exit 1
    }
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with apk' >&2
      exit 1
    }
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with dnf' >&2
      exit 1
    }
  elif command -v yum >/dev/null 2>&1; then
    yum install -y nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with yum' >&2
      exit 1
    }
  elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with zypper' >&2
      exit 1
    }
  else
    printf '%s\n' 'nftables is required, but no supported package manager was found (apt, apk, dnf, yum, or zypper)' >&2
    exit 1
  fi
  nftables_available || {
    printf '%s\n' 'the nftables package was installed, but the nft executable is still unavailable' >&2
    exit 1
  }
}

run_uninstall() {
  case "$service_manager" in
    systemd)
      if [ -e "$service_unit_dir/qagent.service" ]; then
        "$systemctl_cmd" disable qagent.service >/dev/null 2>&1 || true
        "$systemctl_cmd" stop qagent.service >/dev/null 2>&1 || true
        rm -f "$service_unit_dir/qagent.service"
        "$systemctl_cmd" daemon-reload
      fi
      for unit in "$service_unit_dir"/qagent-*.service; do
        [ -e "$unit" ] || continue
        if grep -q '^Description=.* managed by QAgent$' "$unit"; then
          "$systemctl_cmd" disable "${unit##*/}" >/dev/null 2>&1 || true
          "$systemctl_cmd" stop "${unit##*/}" >/dev/null 2>&1 || true
          rm -f "$unit"
        else
          printf '%s\n' "preserved non-QAgent unit: $unit"
        fi
      done
      "$systemctl_cmd" daemon-reload
      ;;
    openrc)
      if [ -e "$openrc_init_dir/qagent" ]; then
        "$rc_service_cmd" qagent stop >/dev/null 2>&1 || true
        rm -f "$openrc_runlevels_root/default/qagent"
        rm -f "$openrc_init_dir/qagent"
      fi
      for service in "$openrc_init_dir"/qagent-*; do
        [ -e "$service" ] || continue
        if grep -q '^# QControlHub managed OpenRC service:' "$service"; then
          "$rc_service_cmd" "${service##*/}" stop >/dev/null 2>&1 || true
          rm -f "$openrc_runlevels_root/default/${service##*/}"
          rm -f "$service"
        else
          printf '%s\n' "preserved non-QAgent OpenRC service: $service"
        fi
      done
      ;;
  esac
  rm -f "$agent_env_file" "$openrc_conf_dir/qagent" "$qagent_bin_link"
  rm -rf "$qagent_bin_dir" "$qagent_etc_dir" "$core_asset_root"
  printf '%s\n' "已卸载 QControlHub Agent；保留节点状态目录 $agent_state_dir，如需彻底清理请手动删除。"
}

if [ "$action" = uninstall ]; then
  run_uninstall
  exit 0
fi

install_nftables

control="${1:?usage: install-agent.sh install|update <control-plane-url|ip[:port]> <add-node-credential> [agent-name]}"
token="${2:?usage: install-agent.sh install|update <control-plane-url|ip[:port]> <add-node-credential> [agent-name]}"
name_arg="${3:-}"
default_name=$(hostname)
name=${name_arg:-$default_name}
ca_file="${QCH_TLS_CA_FILE:-}"
allow_insecure_live="${QCH_ALLOW_INSECURE_LIVE:-false}"

validate_environment_value QCH_SERVER_URL "$control"
validate_environment_value QCH_ENROLLMENT_TOKEN "$token"
validate_environment_value QCH_AGENT_NAME "$name"
validate_environment_value QCH_TLS_CA_FILE "$ca_file"
validate_environment_value QCH_ALLOW_INSECURE_LIVE "$allow_insecure_live"

case "$control" in
  http://*|https://*|ws://*|wss://*) server_url="$control" ;;
  *) server_url="http://$control" ;;
esac
case "$server_url" in */) server_url=${server_url%/} ;; esac
case "$server_url" in
  ws://*) http_origin="http://${server_url#ws://}" ;;
  wss://*) http_origin="https://${server_url#wss://}" ;;
  http://*|https://*) http_origin="$server_url" ;;
  *) printf '%s\n' 'invalid control-plane URL' >&2; exit 1 ;;
esac
server_host=${server_url#*://}
case "$server_host" in
  ""|*/*|*'?'*|*'#'*|*@*|*'"'*|*\\*) printf '%s\n' 'control-plane URL must be a bare origin' >&2; exit 1 ;;
esac
case "$server_host" in
  *[[:space:]]*) printf '%s\n' 'control-plane URL must not contain whitespace' >&2; exit 1 ;;
esac

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-agent.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
repository_dir="$work_dir/qcontrolhub"
mkdir -p "$repository_dir/deploy/$service_manager" "$repository_dir/examples/configs"

download() {
  source_path=$1
  destination=$2
  if [ -n "$ca_file" ]; then
    "$download_cmd" --fail --silent --show-error --cacert "$ca_file" -H "X-QControlHub-Enrollment: $token" "$http_origin$source_path" -o "$destination"
  else
    "$download_cmd" --fail --silent --show-error -H "X-QControlHub-Enrollment: $token" "$http_origin$source_path" -o "$destination"
  fi
}

echo '== 1/6 下载安装资源 =='
for asset in \
  deploy/bootstrap-core-services.sh \
  deploy/existing-core-mapping.sh \
  examples/configs/mihomo-minimal.yaml \
  examples/configs/xray-minimal.json \
  examples/configs/sing-box-minimal.json \
  examples/configs/shadowsocks-rust-minimal.json
do
  download "/install-assets/$asset" "$repository_dir/$asset"
done
if [ "$service_manager" = openrc ]; then
  service_assets="qagent qagent-mihomo qagent-xray qagent-sing-box qagent-shadowsocks-rust"
  for service_asset in $service_assets; do
    download "/install-assets/deploy/openrc/$service_asset" "$repository_dir/deploy/openrc/$service_asset"
  done
else
  service_assets="qagent.service qagent-core-journal.conf qagent-mihomo.service qagent-xray.service qagent-sing-box.service qagent-shadowsocks-rust.service"
  for service_asset in $service_assets; do
    download "/install-assets/deploy/systemd/$service_asset" "$repository_dir/deploy/systemd/$service_asset"
  done
fi
. "$repository_dir/deploy/existing-core-mapping.sh"

echo "== 2/6 下载 agent 二进制（控制面 GET /api/v1/agent-binary）=="
download /api/v1/agent-binary "$work_dir/qagent"
[ -s "$work_dir/qagent" ] || { printf '%s\n' 'downloaded agent binary is empty' >&2; exit 1; }
chmod 0755 "$work_dir/qagent"

echo '== 3/6 检测现有核心并暂存按需安装资源 =='
run_discovery() {
  label=$1
  shift
  if "$@"; then
    return 0
  else
    result=$?
  fi
  case "$result" in
    1) return 0 ;;
    2)
      printf '%s\n' "warning: existing $label service was left unchanged because it could not be mapped safely; QAgent installation will continue and only this core's remote tasks will remain disabled" >&2
      return 0
      ;;
    *)
      printf '%s\n' "unexpected $label discovery failure; installation stopped without changing services" >&2
      exit "$result"
      ;;
  esac
}
run_discovery Xray discover_existing_xray
run_discovery sing-box discover_existing_singbox
QCH_SERVICE_MANAGER="$service_manager" sh "$repository_dir/deploy/bootstrap-core-services.sh" --prepare-agent
install -d -o root -g "$core_service_group" -m 0750 "$qagent_etc_dir"

# Deploying QAgent must not create four unused core services. Keep the
# credential-protected installation assets in a protected local directory. The
# Agent invokes the bootstrap for exactly one engine only after an explicit
# panel install/import task arrives.
for asset_directory in \
  "$qagent_bin_dir" \
  "$qagent_bin_dir/cores" \
  "$core_share_root" \
  "$core_asset_root" \
  "$core_asset_root/deploy" \
  "$core_asset_root/deploy/$service_manager" \
  "$core_asset_root/examples" \
  "$core_asset_root/examples/configs"
do
  [ ! -L "$asset_directory" ] || { printf '%s\n' "refusing symlinked core asset directory: $asset_directory" >&2; exit 1; }
  install -d -o root -g root -m 0755 "$asset_directory"
done

stage_core_asset() {
  source_file=$1
  destination=$2
  mode=$3
  [ ! -L "$destination" ] || { printf '%s\n' "refusing symlinked core asset: $destination" >&2; exit 1; }
  [ ! -e "$destination" ] || [ -f "$destination" ] || {
    printf '%s\n' "refusing non-regular core asset: $destination" >&2
    exit 1
  }
  install -o root -g root -m "$mode" "$source_file" "$destination"
}

stage_core_asset "$repository_dir/deploy/bootstrap-core-services.sh" "$core_asset_root/deploy/bootstrap-core-services.sh" 0755
stage_core_asset "$repository_dir/deploy/existing-core-mapping.sh" "$core_asset_root/deploy/existing-core-mapping.sh" 0644
for config_asset in mihomo-minimal.yaml xray-minimal.json sing-box-minimal.json shadowsocks-rust-minimal.json; do
  stage_core_asset "$repository_dir/examples/configs/$config_asset" "$core_asset_root/examples/configs/$config_asset" 0644
done
for service_asset in $service_assets; do
  if [ "$service_manager" = openrc ]; then service_mode=0755; else service_mode=0644; fi
  stage_core_asset "$repository_dir/deploy/$service_manager/$service_asset" "$core_asset_root/deploy/$service_manager/$service_asset" "$service_mode"
done

echo '== 4/6 安装 agent 二进制并写入环境文件 =='
if [ -x "$qagent_bin_dir/qagent" ] || [ -f "$agent_env_file" ] || \
   [ -e "$service_unit_dir/qagent.service" ] || [ -e "$openrc_init_dir/qagent" ]; then
  if [ "$action" = update ]; then
    echo '更新已有 QControlHub Agent（保留可复用的自定义配置）。'
  elif [ "$action" = migrate ]; then
    echo '迁移到新的控制面板（保留已装内核与配置，仅重新注册身份）。'
  else
    echo '检测到已有 QControlHub Agent；本次按覆盖升级处理，保留可复用的自定义配置。'
  fi
fi

# 本次安装由脚本显式管理的配置键：这些始终以本次为准，不继承上次的值。
managed_env_keys="QCH_SERVER_URL QCH_ENROLLMENT_TOKEN QCH_REENROLL QCH_TLS_CA_FILE QCH_ALLOW_INSECURE_LIVE QCH_ALLOW_HTTP QCH_SERVICE_MANAGER QCH_EXISTING_XRAY_BINARY QCH_EXISTING_XRAY_CONFIG QCH_EXISTING_XRAY_CONFIG_DIRECTORY QCH_EXISTING_XRAY_SERVICE QCH_EXISTING_SING_BOX_BINARY QCH_EXISTING_SING_BOX_CONFIG QCH_EXISTING_SING_BOX_CONFIG_DIRECTORY QCH_EXISTING_SING_BOX_WORK_DIRECTORY QCH_EXISTING_SING_BOX_SERVICE_BINARY QCH_EXISTING_SING_BOX_SERVICE"

install -d -o root -g root -m 0700 "$agent_conf_dir"
install -d -o root -g root -m 0755 "$qagent_bin_dir"
[ ! -L "$qagent_bin_dir/qagent" ] || { printf '%s\n' "refusing symlinked Agent binary: $qagent_bin_dir/qagent" >&2; exit 1; }
[ ! -e "$qagent_bin_dir/qagent" ] || [ -f "$qagent_bin_dir/qagent" ] || {
  printf '%s\n' "refusing non-regular Agent binary: $qagent_bin_dir/qagent" >&2
  exit 1
}
install -m 0755 "$work_dir/qagent" "$qagent_bin_dir/qagent"
[ ! -e "$qagent_bin_link" ] || [ -L "$qagent_bin_link" ] || {
  printf '%s\n' "refusing to overwrite non-symlink compatibility link: $qagent_bin_link" >&2
  exit 1
}
ln -sfn "$qagent_bin_dir/qagent" "$qagent_bin_link"

# 读取上次安装留下的可复用配置。只继承非本次管理的 QCH_* 键，便于在重复
# 安装时保留运维自行追加的探测/标签/心跳等设置，而不是把整份环境重置。
existing_name=""
existing_labels=""
existing_engines=""
existing_state=""
existing_server=""
inherited_env="$work_dir/qagent.inherited.env"
: > "$inherited_env"
read_existing_agent_env() {
  [ -r "$agent_env_file" ] || return 0
  while IFS='=' read -r environment_key environment_value; do
    case "$environment_key" in
      QCH_SERVER_URL) existing_server=$environment_value; continue ;;
      QCH_AGENT_NAME) existing_name=$environment_value; continue ;;
      QCH_AGENT_LABELS) existing_labels=$environment_value; continue ;;
      QCH_AGENT_ENGINES) existing_engines=$environment_value; continue ;;
      QCH_AGENT_STATE) existing_state=$environment_value; continue ;;
    esac
    case "$environment_key" in QCH_*) ;; *) continue ;; esac
    case " $managed_env_keys " in *" $environment_key "*) continue ;; esac
    printf '%s=%s\n' "$environment_key" "$environment_value" >> "$inherited_env"
  done < "$agent_env_file"
}
read_existing_agent_env

final_name=${name_arg:-${existing_name:-$default_name}}
final_labels=${existing_labels:-region=cn-east}
final_engines=${existing_engines:-mihomo,xray,sing-box,ss-rust}
final_state=${existing_state:-$agent_state_file}
validate_environment_value QCH_AGENT_NAME "$final_name"
validate_environment_value QCH_AGENT_LABELS "$final_labels"
validate_environment_value QCH_AGENT_ENGINES "$final_engines"
validate_environment_value QCH_AGENT_STATE "$final_state"

install -d -o root -g root -m 0755 "$(dirname "$final_state")"
reenroll_required=false
if [ "$action" = migrate ] || { [ -n "$existing_server" ] && [ "$existing_server" != "$server_url" ]; }; then
  reenroll_required=true
fi
previous_state_present=false
previous_private_key=""
if [ -s "$final_state" ]; then
  previous_state_present=true
  previous_private_key=$(sed -n 's/.*"private_key":"\([^"]*\)".*/\1/p' "$final_state" | head -n 1)
fi
umask 077
{
  printf '%s\n' "QCH_SERVER_URL=$server_url"
  if [ -n "$ca_file" ]; then printf '%s\n' "QCH_TLS_CA_FILE=$ca_file"; fi
  case "$server_url" in
    http://*|ws://*) printf '%s\n' 'QCH_ALLOW_HTTP=true' ;;
  esac
  printf '%s\n' "QCH_ALLOW_INSECURE_LIVE=$allow_insecure_live"
  printf '%s\n' "QCH_ENROLLMENT_TOKEN=$token"
  printf '%s\n' "QCH_AGENT_NAME=$final_name"
  printf '%s\n' "QCH_AGENT_LABELS=$final_labels"
  printf '%s\n' "QCH_AGENT_STATE=$final_state"
  printf '%s\n' "QCH_AGENT_ENGINES=$final_engines"
  printf '%s\n' "QCH_SERVICE_MANAGER=$service_manager"
  if [ -n "$mapped_xray_config" ] || [ -n "$mapped_xray_config_directory" ]; then
    printf '%s\n' \
      "QCH_EXISTING_XRAY_BINARY=$mapped_xray_binary" \
      "QCH_EXISTING_XRAY_CONFIG=$mapped_xray_config" \
      "QCH_EXISTING_XRAY_CONFIG_DIRECTORY=$mapped_xray_config_directory" \
      "QCH_EXISTING_XRAY_SERVICE=$mapped_xray_service"
  fi
  if [ -n "$mapped_singbox_config" ]; then
    printf '%s\n' \
      "QCH_EXISTING_SING_BOX_BINARY=$mapped_singbox_binary" \
      "QCH_EXISTING_SING_BOX_CONFIG=$mapped_singbox_config" \
      "QCH_EXISTING_SING_BOX_CONFIG_DIRECTORY=$mapped_singbox_config_directory" \
      "QCH_EXISTING_SING_BOX_SERVICE_BINARY=$mapped_singbox_service_binary" \
      "QCH_EXISTING_SING_BOX_SERVICE=$mapped_singbox_service"
    if [ -n "$mapped_singbox_work_directory" ]; then
      printf '%s\n' "QCH_EXISTING_SING_BOX_WORK_DIRECTORY=$mapped_singbox_work_directory"
    fi
  fi
  if [ -s "$inherited_env" ]; then cat "$inherited_env"; fi
} > "$agent_env_file"
chmod 0600 "$agent_env_file"

openrc_conf="$openrc_conf_dir/qagent"
write_openrc_environment() {
  : > "$work_dir/qagent.openrc.conf"
  while IFS='=' read -r environment_key environment_value; do
    case "$environment_key" in
      QCH_*) case "$environment_key" in *[!A-Z0-9_]*) printf '%s\n' "refusing invalid Agent environment key: $environment_key" >&2; exit 1 ;; esac ;;
      *) printf '%s\n' "refusing invalid Agent environment key: $environment_key" >&2; exit 1 ;;
    esac
    escaped_value=$(printf '%s' "$environment_value" | sed "s/'/'\\\\''/g")
    printf "export %s='%s'\n" "$environment_key" "$escaped_value" >> "$work_dir/qagent.openrc.conf"
  done < "$agent_env_file"
  install -d -o root -g root -m 0755 "$openrc_conf_dir"
  install -o root -g root -m 0600 "$work_dir/qagent.openrc.conf" "$openrc_conf"
}

install_managed_service() {
  source_file=$1
  destination=$2
  mode=$3
  marker=$4
  [ ! -L "$destination" ] || { printf '%s\n' "refusing symlinked managed service: $destination" >&2; exit 1; }
  if [ -e "$destination" ]; then
    [ -f "$destination" ] || { printf '%s\n' "refusing non-regular managed service: $destination" >&2; exit 1; }
    if ! grep -q "$marker" "$destination"; then
      printf '%s\n' "refusing to overwrite non-QAgent service: $destination" >&2
      exit 1
    fi
    if cmp -s "$source_file" "$destination"; then
      printf '%s\n' "managed service already current: $destination"
      return
    fi
  fi
  install -o root -g root -m "$mode" "$source_file" "$destination"
  printf '%s\n' "installed managed service: $destination"
}

ensure_openrc_enabled() {
  service_name=$1
  if [ -e "$openrc_runlevels_root/default/$service_name" ]; then
    printf '%s\n' "openrc service already enabled: $service_name"
    return
  fi
  "$rc_update_cmd" add "$service_name" default >/dev/null
}

echo "== 5/6 安装 $service_manager 服务 =="
if [ "$service_manager" = openrc ]; then
  install_managed_service "$repository_dir/deploy/openrc/qagent" "$openrc_init_dir/qagent" 0755 '^# QControlHub managed OpenRC service:'
  write_openrc_environment
  ensure_openrc_enabled qagent
else
  install_managed_service "$repository_dir/deploy/systemd/qagent.service" "$service_unit_dir/qagent.service" 0644 '^Description=QControlHub remote engine agent$'
  "$systemctl_cmd" daemon-reload
fi

echo '== 6/6 启动 agent =='
if [ "$service_manager" = openrc ]; then
  if "$rc_service_cmd" qagent status >/dev/null 2>&1; then "$rc_service_cmd" qagent restart; else "$rc_service_cmd" qagent start; fi
else
  "$systemctl_cmd" enable qagent.service >/dev/null
  # restart also starts an inactive unit and guarantees repeated installation
  # replaces the running process with the freshly downloaded binary.
  "$systemctl_cmd" restart qagent.service
fi
sleep 3
if [ "$service_manager" = openrc ]; then
  "$rc_service_cmd" qagent status
else
  "$systemctl_cmd" --no-pager status qagent.service | head -n 10
fi

agent_enrollment_completed() {
  [ -s "$final_state" ] || return 1
  grep -Fq "\"server\":\"$server_host\"" "$final_state" || return 1
  if [ "$reenroll_required" = true ] && [ "$previous_state_present" = true ]; then
    current_private_key=$(sed -n 's/.*"private_key":"\([^"]*\)".*/\1/p' "$final_state" | head -n 1)
    [ -n "$current_private_key" ] || return 1
    [ "$current_private_key" != "$previous_private_key" ] || return 1
  fi
}

waited_seconds=0
while ! agent_enrollment_completed; do
  if [ "$waited_seconds" -ge "$enrollment_wait_seconds" ]; then
    if [ "$service_manager" = openrc ]; then
      "$rc_service_cmd" qagent stop >/dev/null 2>&1 || true
    else
      "$systemctl_cmd" stop qagent.service >/dev/null 2>&1 || true
    fi
    printf '%s\n' "Agent did not confirm enrollment with $server_host within ${enrollment_wait_seconds}s; the service was stopped and enrollment credentials were retained, rerun this installer to retry" >&2
    exit 1
  fi
  sleep 1
  waited_seconds=$((waited_seconds + 1))
done

# The add-node credential is needed only until the state file proves that the
# Agent enrolled against this control plane. On a failed or interrupted
# migration the stopped service retains it so rerunning this installer can
# retry without destroying the prior identity or any managed core state.
sed -i '/^QCH_ENROLLMENT_TOKEN=/d' "$agent_env_file"
chmod 0600 "$agent_env_file"
if [ "$service_manager" = openrc ]; then
  sed -i '/^export QCH_ENROLLMENT_TOKEN=/d' "$openrc_conf"
  chmod 0600 "$openrc_conf"
fi
