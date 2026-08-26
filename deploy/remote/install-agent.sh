#!/bin/sh
# install-agent.sh — QControlHub agent 一键安装（root 执行，无需预装仓库）
#
# 用法：
#   sh deploy/remote/install-agent.sh <control-plane-url|ip[:port]> <add-node-credential> [agent-name]
#
# 示例：
#   QCH_TLS_CA_FILE=/etc/qcontrolhub/control-plane-ca.pem \
#   sh deploy/remote/install-agent.sh https://qcontrolhub.example.com <token> shanghai-edge-01
#
# 从控制面 GET /api/v1/agent-binary 下载 agent 可执行文件，引导核心服务，
# 写入 /etc/qcontrolhub/agent.env，安装 systemd 或 OpenRC 服务并启动。
set -eu

[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'install-agent.sh must run as root' >&2; exit 1; }

control="${1:?usage: install-agent.sh <control-plane-url|ip[:port]> <add-node-credential> [agent-name]}"
token="${2:?usage: install-agent.sh <control-plane-url|ip[:port]> <add-node-credential> [agent-name]}"
name="${3:-$(hostname)}"
ca_file="${QCH_TLS_CA_FILE:-}"
allow_insecure_live="${QCH_ALLOW_INSECURE_LIVE:-false}"

validate_environment_value() {
  environment_key=$1
  environment_value=$2
  sanitized_value=$(printf '%s' "$environment_value" | tr -d '\r\n')
  [ "$sanitized_value" = "$environment_value" ] || {
    printf '%s\n' "refusing multiline Agent environment value: $environment_key" >&2
    exit 1
  }
}

validate_environment_value QCH_SERVER_URL "$control"
validate_environment_value QCH_ENROLLMENT_TOKEN "$token"
validate_environment_value QCH_AGENT_NAME "$name"
validate_environment_value QCH_TLS_CA_FILE "$ca_file"
validate_environment_value QCH_ALLOW_INSECURE_LIVE "$allow_insecure_live"

if [ -f /etc/alpine-release ]; then
  command -v apk >/dev/null 2>&1 || { printf '%s\n' 'Alpine apk is unavailable' >&2; exit 1; }
  apk add --no-cache ca-certificates coreutils curl libcap nftables openrc >/dev/null
  service_manager=openrc
elif command -v systemctl >/dev/null 2>&1; then
  service_manager=systemd
else
  printf '%s\n' 'unsupported init system: systemd or Alpine OpenRC is required' >&2
  exit 1
fi
case "$control" in
  http://*|https://*|ws://*|wss://*) server_url="$control" ;;
  *) server_url="http://$control" ;;
esac
case "$server_url" in
  ws://*) http_origin="http://${server_url#ws://}" ;;
  wss://*) http_origin="https://${server_url#wss://}" ;;
  http://*|https://*) http_origin="$server_url" ;;
  *) printf '%s\n' 'invalid control-plane URL' >&2; exit 1 ;;
esac

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-agent.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
repository_dir="$work_dir/qcontrolhub"
mkdir -p "$repository_dir/deploy/$service_manager" "$repository_dir/examples/configs"

download() {
  source_path=$1
  destination=$2
  if [ -n "$ca_file" ]; then
    curl --fail --silent --show-error --cacert "$ca_file" -H "X-QControlHub-Enrollment: $token" "$http_origin$source_path" -o "$destination"
  else
    curl --fail --silent --show-error -H "X-QControlHub-Enrollment: $token" "$http_origin$source_path" -o "$destination"
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
  [ "$result" -eq 1 ] || {
    printf '%s\n' "unsafe $label service state; installation stopped without changing services" >&2
    exit "$result"
  }
}
run_discovery Xray discover_existing_xray
run_discovery sing-box discover_existing_singbox
QCH_SERVICE_MANAGER="$service_manager" sh "$repository_dir/deploy/bootstrap-core-services.sh" --prepare-agent
install -d -o root -g qcontrolhub-core -m 0750 /etc/qagent

# Deploying QAgent must not create four unused core services. Keep the
# credential-protected installation assets in a protected local directory. The
# Agent invokes the bootstrap for exactly one engine only after an explicit
# panel install/import task arrives.
core_asset_root=/usr/local/share/qcontrolhub/core-install
for asset_directory in \
  /usr/local/lib/qagent \
  /usr/local/lib/qagent/cores \
  /usr/local/share/qcontrolhub \
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

echo '== 4/6 写入 agent 环境文件 =='
mkdir -p /usr/local/lib/qagent
install -m 0755 "$work_dir/qagent" /usr/local/lib/qagent/qagent
ln -sfn /usr/local/lib/qagent/qagent /usr/local/bin/qagent
mkdir -p /etc/qcontrolhub /var/lib/qcontrolhub
umask 077
{
  printf '%s\n' "QCH_SERVER_URL=$server_url"
  if [ -n "$ca_file" ]; then printf '%s\n' "QCH_TLS_CA_FILE=$ca_file"; fi
  case "$server_url" in
    http://*|ws://*) printf '%s\n' 'QCH_ALLOW_HTTP=true' "QCH_ALLOW_INSECURE_LIVE=$allow_insecure_live" ;;
  esac
  printf '%s\n' "QCH_ENROLLMENT_TOKEN=$token"
  printf '%s\n' "QCH_AGENT_NAME=$name"
  printf '%s\n' 'QCH_AGENT_LABELS=region=cn-east'
  printf '%s\n' 'QCH_AGENT_STATE=/var/lib/qcontrolhub/agent-state.json'
  printf '%s\n' 'QCH_AGENT_ENGINES=mihomo,xray,sing-box,ss-rust'
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
} > /etc/qcontrolhub/agent.env
chmod 0600 /etc/qcontrolhub/agent.env

openrc_conf=/etc/conf.d/qagent
write_openrc_environment() {
  : > "$work_dir/qagent.openrc.conf"
  while IFS='=' read -r environment_key environment_value; do
    case "$environment_key" in
      QCH_*) case "$environment_key" in *[!A-Z0-9_]*) printf '%s\n' "refusing invalid Agent environment key: $environment_key" >&2; exit 1 ;; esac ;;
      *) printf '%s\n' "refusing invalid Agent environment key: $environment_key" >&2; exit 1 ;;
    esac
    escaped_value=$(printf '%s' "$environment_value" | sed "s/'/'\\\\''/g")
    printf "export %s='%s'\n" "$environment_key" "$escaped_value" >> "$work_dir/qagent.openrc.conf"
  done < /etc/qcontrolhub/agent.env
  install -d -o root -g root -m 0755 /etc/conf.d
  install -o root -g root -m 0600 "$work_dir/qagent.openrc.conf" "$openrc_conf"
}

echo "== 5/6 安装 $service_manager 服务 =="
if [ "$service_manager" = openrc ]; then
  install -o root -g root -m 0755 "$repository_dir/deploy/openrc/qagent" /etc/init.d/qagent
  write_openrc_environment
  rc-update add qagent default >/dev/null
else
  install -m 0644 "$repository_dir/deploy/systemd/qagent.service" /etc/systemd/system/qagent.service
  systemctl daemon-reload
fi

echo '== 6/6 启动 agent =='
if [ "$service_manager" = openrc ]; then
  if rc-service qagent status >/dev/null 2>&1; then rc-service qagent restart; else rc-service qagent start; fi
else
  systemctl enable qagent.service >/dev/null
  # restart also starts an inactive unit and guarantees repeated installation
  # replaces the running process with the freshly downloaded binary.
  systemctl restart qagent.service
fi
sleep 3
if [ "$service_manager" = openrc ]; then
  rc-service qagent status
else
  systemctl --no-pager status qagent.service | head -n 10
fi

# 添加节点凭证只用于下载和注册；无论首次还是覆盖安装，只要身份文件
# 存在就立即从环境文件移除，避免添加节点凭证残留。
if [ -s /var/lib/qcontrolhub/agent-state.json ]; then
  sed -i '/^QCH_ENROLLMENT_TOKEN=/d' /etc/qcontrolhub/agent.env
  chmod 0600 /etc/qcontrolhub/agent.env
  if [ "$service_manager" = openrc ]; then
    sed -i '/^export QCH_ENROLLMENT_TOKEN=/d' "$openrc_conf"
    chmod 0600 "$openrc_conf"
  fi
fi
