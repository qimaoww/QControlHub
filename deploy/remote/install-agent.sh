#!/bin/sh
# install-agent.sh — QControlHub agent 一键安装（root 执行，目标机已带仓库目录）
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

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd)

echo "== 1/5 下载 agent 二进制（控制面 GET /api/v1/agent-binary）=="
if [ -n "$ca_file" ]; then
  curl --fail --silent --show-error --cacert "$ca_file" "$http_origin/api/v1/agent-binary" -o /tmp/qagent-bin
else
  curl --fail --silent --show-error "$http_origin/api/v1/agent-binary" -o /tmp/qagent-bin
fi
[ -s /tmp/qagent-bin ] || { printf '%s\n' 'downloaded agent binary is empty' >&2; exit 1; }

echo '== 2/5 引导核心服务（mihomo/xray/sing-box/ss-rust 单元与最小配置）=='
bash "$repository_dir/deploy/bootstrap-core-services.sh"

echo '== 3/5 写入 agent 环境文件 =='
install -m 0755 /tmp/qagent-bin /usr/local/bin/qagent
rm -f /tmp/qagent-bin
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

echo '== 4/5 安装 systemd 单元 =='
install -m 0644 "$repository_dir/deploy/systemd/qagent.service" /etc/systemd/system/qagent.service
systemctl daemon-reload

echo '== 5/5 启动 agent =='
systemctl enable --now qagent.service
sleep 3
systemctl --no-pager status qagent.service | head -n 10

# 只有本次安装新建身份后才删除一次性注册码；旧身份迁移失败时保留它，便于排查。
if [ "$had_state" = false ] && [ -s /var/lib/qcontrolhub/agent-state.json ]; then
  sed -i '/^QCH_ENROLLMENT_TOKEN=/d' /etc/qcontrolhub/agent.env
  chmod 0600 /etc/qcontrolhub/agent.env
fi
