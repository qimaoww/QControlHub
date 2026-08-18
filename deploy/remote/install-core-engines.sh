#!/bin/sh
# install-core-engines.sh — 下载并安装 mihomo / xray / sing-box / Shadowsocks Rust 官方 linux-amd64 二进制
# 用法（root）： bash deploy/remote/install-core-engines.sh
# 安装路径与 agent 默认 EngineSpec 一致：/usr/local/bin/{mihomo,xray,sing-box,ssserver}
set -eu

[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'must run as root' >&2; exit 1; }
for tool in curl gunzip unzip tar xz; do
  command -v "$tool" >/dev/null 2>&1 || { printf '%s\n' "missing tool: $tool" >&2; exit 1; }
done

mihomo_gz="https://github.com/MetaCubeX/mihomo/releases/download/v1.19.29/mihomo-linux-amd64-v1-v1.19.29.gz"
xray_zip="https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-64.zip"
singbox_tgz="https://github.com/SagerNet/sing-box/releases/download/v1.13.16/sing-box-1.13.16-linux-amd64.tar.gz"
ssrust_txz="https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.24.0/shadowsocks-v1.24.0.x86_64-unknown-linux-gnu.tar.xz"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo '== mihomo v1.19.29 =='
curl -fsSL -o "$work/mihomo.gz" "$mihomo_gz"
gunzip -c "$work/mihomo.gz" > /usr/local/bin/mihomo
chmod 0755 /usr/local/bin/mihomo
/usr/local/bin/mihomo -v | head -n1

echo '== xray v26.3.27 =='
curl -fsSL -o "$work/xray.zip" "$xray_zip"
mkdir -p "$work/xray"
unzip -o -q "$work/xray.zip" -d "$work/xray"
cp "$work/xray/xray" /usr/local/bin/xray
chmod 0755 /usr/local/bin/xray
/usr/local/bin/xray version | head -n1

echo '== sing-box v1.13.16 =='
curl -fsSL -o "$work/sing-box.tgz" "$singbox_tgz"
mkdir -p "$work/sb"
tar -xzf "$work/sing-box.tgz" -C "$work/sb"
find "$work/sb" -type f -name sing-box -exec cp {} /usr/local/bin/sing-box \;
chmod 0755 /usr/local/bin/sing-box
/usr/local/bin/sing-box version | head -n1

echo '== Shadowsocks Rust v1.24.0 =='
curl -fsSL -o "$work/shadowsocks-rust.txz" "$ssrust_txz"
mkdir -p "$work/ssrust"
tar -xJf "$work/shadowsocks-rust.txz" -C "$work/ssrust"
find "$work/ssrust" -type f -name ssserver -exec cp {} /usr/local/bin/ssserver \;
chmod 0755 /usr/local/bin/ssserver
/usr/local/bin/ssserver --version | head -n1

echo '== installed =='
ls -l /usr/local/bin/mihomo /usr/local/bin/xray /usr/local/bin/sing-box /usr/local/bin/ssserver
