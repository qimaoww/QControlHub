#!/bin/sh
set -eu
# Pinned upstream assets, not arbitrary binaries supplied by a PR.
root=${1:?usage: fetch-permission-cores.sh OUTPUT_DIRECTORY}
mkdir -p "$root/bin" "$root/musl"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
fetch() {
  curl --fail --location --retry 3 --max-time 120 "$1" -o "$stage/$2"
  printf '%s  %s\n' "$3" "$stage/$2" | sha256sum -c -
}
fetch https://github.com/MetaCubeX/mihomo/releases/download/v1.19.33/mihomo-linux-amd64-v1.19.33.gz mihomo.gz 132f6f3edf9a65e62f04853aed3358472e0b2b7666daa8419edfa2ddf5bb6323
gzip -dc "$stage/mihomo.gz" > "$root/bin/mihomo"
fetch https://github.com/XTLS/Xray-core/releases/download/v26.10.31/Xray-linux-64.zip xray.zip e4bd9119f50ae7c41658b1abd0679ae06f98bd864a2907ff33f2f2cf7081b214
unzip -p "$stage/xray.zip" xray > "$root/bin/xray"
fetch https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-linux-amd64.tar.gz sing-box.tar.gz 699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968
tar -xzf "$stage/sing-box.tar.gz" -C "$stage"
cp "$stage/sing-box-1.14.4-linux-amd64/sing-box" "$root/bin/sing-box"
fetch https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-linux-amd64-musl.tar.gz sing-box-musl.tar.gz 8f7c5f0651238850d96a0e488e5473cd77ec1a0098f63942e943f475a12057ca
tar -xzf "$stage/sing-box-musl.tar.gz" -C "$stage"
cp "$stage/sing-box-1.14.4-linux-amd64-musl/sing-box" "$root/musl/sing-box"
fetch https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.25.0/shadowsocks-v1.25.0.x86_64-unknown-linux-musl.tar.xz ss.tar.xz 8439bf43c324b0fc273e663d0b1f8926fd8f666cbd1e0fd59b35096f0e778e92
tar -xf "$stage/ss.tar.xz" -C "$root/bin" ssserver sslocal
chmod 0755 "$root/bin/"* "$root/musl/"*
