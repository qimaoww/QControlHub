#!/bin/sh
# install-agent.sh — QControlHub agent 一键安装（root 执行，无需预装仓库）
#
# 用法：
#   bash deploy/remote/install-agent.sh <control-plane-url|ip[:port]> <enrollment-token> [agent-name]
#
# 示例：
#   QCH_TLS_CA_FILE=/etc/qcontrolhub/control-plane-ca.pem \
#   QCH_AGENT_DRY_RUN=true \
#   bash deploy/remote/install-agent.sh https://192.168.31.205:8443 <token> shanghai-edge-01
#
# 从控制面 GET /api/v1/agent-binary 下载 agent 可执行文件，引导核心服务，
# 写入 /etc/qcontrolhub/agent.env，安装 systemd 单元并启动。
set -eu

[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'install-agent.sh must run as root' >&2; exit 1; }

control="${1:?usage: install-agent.sh <control-plane-url|ip[:port]> <enrollment-token> [agent-name]}"
token="${2:?usage: install-agent.sh <control-plane-url|ip[:port]> <enrollment-token> [agent-name]}"
name="${3:-$(hostname)}"
ca_file="${QCH_TLS_CA_FILE:-}"
dry_run="${QCH_AGENT_DRY_RUN:-true}"
allow_insecure_live="${QCH_ALLOW_INSECURE_LIVE:-true}"

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

case "$dry_run" in
  true|false) ;;
  *) printf '%s\n' 'QCH_AGENT_DRY_RUN must be true or false' >&2; exit 1 ;;
esac

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-agent.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
repository_dir="$work_dir/qcontrolhub"
mkdir -p "$repository_dir/deploy/systemd" "$repository_dir/examples/configs"

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
  deploy/systemd/qagent.service \
  deploy/systemd/mihomo.service \
  deploy/systemd/xray.service \
  deploy/systemd/sing-box.service \
  deploy/systemd/shadowsocks-rust.service \
  examples/configs/mihomo-minimal.yaml \
  examples/configs/xray-minimal.json \
  examples/configs/sing-box-minimal.json \
  examples/configs/shadowsocks-rust-minimal.json
do
  download "/install-assets/$asset" "$repository_dir/$asset"
done

echo "== 2/6 下载 agent 二进制（控制面 GET /api/v1/agent-binary）=="
download /api/v1/agent-binary "$work_dir/qagent"
[ -s "$work_dir/qagent" ] || { printf '%s\n' 'downloaded agent binary is empty' >&2; exit 1; }

echo '== 3/6 引导核心服务（mihomo/xray/sing-box/ss-rust 单元与最小配置）=='
bash "$repository_dir/deploy/bootstrap-core-services.sh"

echo '== 4/6 写入 agent 环境文件 =='
install -m 0755 "$work_dir/qagent" /usr/local/bin/qagent
mkdir -p /etc/qcontrolhub /var/lib/qcontrolhub
had_state=false
if [ -s /var/lib/qcontrolhub/agent-state.json ]; then had_state=true; fi
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
  printf '%s\n' "QCH_AGENT_DRY_RUN=$dry_run"
} > /etc/qcontrolhub/agent.env
chmod 0600 /etc/qcontrolhub/agent.env

echo '== 5/6 安装 systemd 单元 =='
install -m 0644 "$repository_dir/deploy/systemd/qagent.service" /etc/systemd/system/qagent.service
systemctl daemon-reload

echo '== 6/6 启动 agent =='
systemctl enable --now qagent.service
sleep 3
systemctl --no-pager status qagent.service | head -n 10

# 只有本次安装新建身份后才删除一次性注册码；旧身份迁移失败时保留它，便于排查。
if [ "$had_state" = false ] && [ -s /var/lib/qcontrolhub/agent-state.json ]; then
  sed -i '/^QCH_ENROLLMENT_TOKEN=/d' /etc/qcontrolhub/agent.env
  chmod 0600 /etc/qcontrolhub/agent.env
fi
