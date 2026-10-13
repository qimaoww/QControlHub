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
fetch https://github.com/MetaCubeX/mihomo/releases/download/v1.19.32/mihomo-linux-amd64-v1.19.32.gz mihomo.gz 8451100836c9eda194331c2babfad490b2faf30cebc1a04b0e76fd8ac2d35d10
gzip -dc "$stage/mihomo.gz" > "$root/bin/mihomo"
fetch https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-64.zip xray.zip 23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae
unzip -p "$stage/xray.zip" xray > "$root/bin/xray"
fetch https://github.com/SagerNet/sing-box/releases/download/v1.14.3/sing-box-1.14.3-linux-amd64.tar.gz sing-box.tar.gz e2bdf179c15a3652955dc44867e33ca0965c9c9b91b34267dcc5a5d639a5feee
tar -xzf "$stage/sing-box.tar.gz" -C "$stage"
cp "$stage/sing-box-1.14.3-linux-amd64/sing-box" "$root/bin/sing-box"
fetch https://github.com/SagerNet/sing-box/releases/download/v1.14.3/sing-box-1.14.3-linux-amd64-musl.tar.gz sing-box-musl.tar.gz 41fc81fd041134d4d7d850a928d7d2cde17a7d6563953f406b22d32a68bfab1f
tar -xzf "$stage/sing-box-musl.tar.gz" -C "$stage"
cp "$stage/sing-box-1.14.3-linux-amd64-musl/sing-box" "$root/musl/sing-box"
fetch https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.25.0/shadowsocks-v1.25.0.x86_64-unknown-linux-musl.tar.xz ss.tar.xz 8439bf43c324b0fc273e663d0b1f8926fd8f666cbd1e0fd59b35096f0e778e92
tar -xf "$stage/ss.tar.xz" -C "$root/bin" ssserver sslocal
chmod 0755 "$root/bin/"* "$root/musl/"*
