
[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'must run as root' >&2; exit 1; }
if [ -f /etc/alpine-release ]; then
  apk add --no-cache coreutils curl findutils gzip jq tar unzip xz >/dev/null
  linux_libc=musl
else
  linux_libc=gnu
fi
for tool in curl find gunzip install jq sha256sum tar unzip xz; do
  command -v "$tool" >/dev/null 2>&1 || { printf '%s\n' "missing tool: $tool" >&2; exit 1; }
done

mihomo_gz="https://github.com/MetaCubeX/mihomo/releases/download/v1.19.29/mihomo-linux-amd64-v1-v1.19.29.gz"
xray_zip="https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-64.zip"
singbox_tgz="https://github.com/SagerNet/sing-box/releases/download/v1.13.16/sing-box-1.13.16-linux-amd64.tar.gz"
ssrust_txz="https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.24.0/shadowsocks-v1.24.0.x86_64-unknown-linux-${linux_libc}.tar.xz"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
trap 'exit 130' HUP INT TERM
core_dir=/usr/local/lib/qagent/cores
install -d -o root -g root -m 0755 "$core_dir"

# Query the same metadata channel as the Go updater; no checksum fallback is
# allowed when GitHub omits a digest. Verify every archive before replacing any
# executable. The original URL remains the authority, never the proxy URL.
verified_download() {
  asset_url=$1
  asset_path=$2
  release_path=${asset_url#https://github.com/}
  release_repo=${release_path%%/releases/download/*}
  release_tag=${release_path#*/releases/download/}
  release_tag=${release_tag%%/*}
  qch_github_download "https://api.github.com/repos/$release_repo/releases/tags/$release_tag" "$work/metadata.json" 2097152 20
  asset_record=$(jq -cer --arg url "$asset_url" '[.assets[] | select(.browser_download_url == $url)] | if length == 1 then .[0] else error("missing or ambiguous asset") end' "$work/metadata.json")
  asset_size=$(printf '%s' "$asset_record" | jq -er '.size | select(type == "number" and floor == . and . >= 1048576 and . <= 134217728)')
  asset_digest=$(printf '%s' "$asset_record" | jq -er '.digest | select(test("^sha256:[a-fA-F0-9]{64}$")) | ltrimstr("sha256:") | ascii_downcase')
  qch_github_download "$asset_url" "$asset_path" "$asset_size"
  [ "$(wc -c < "$asset_path")" -eq "$asset_size" ] || { printf '%s\n' 'release size mismatch' >&2; exit 1; }
  printf '%s  %s\n' "$asset_digest" "$asset_path" | sha256sum -c - >/dev/null || { printf '%s\n' 'release SHA-256 mismatch' >&2; exit 1; }
}
verified_download "$mihomo_gz" "$work/mihomo.gz"
verified_download "$xray_zip" "$work/xray.zip"
verified_download "$singbox_tgz" "$work/sing-box.tgz"
verified_download "$ssrust_txz" "$work/shadowsocks-rust.txz"

# Stage replacements on the destination filesystem, then rename atomically.
atomic_install() {
  staged=$(mktemp "$core_dir/.qch-install.XXXXXX")
  install -o root -g root -m "$3" "$1" "$staged" || { rm -f "$staged"; return 1; }
  mv -f "$staged" "$core_dir/$2" || { rm -f "$staged"; return 1; }
}

echo '== mihomo v1.19.29 =='
version_output=$(qch_stage_gzip_binary "$work/mihomo.gz" "$work/mihomo" -v)
printf '%s\n' "$version_output" | head -n1

echo '== xray v26.3.27 =='
mkdir -p "$work/xray"
unzip -o -q "$work/xray.zip" -d "$work/xray"
version_output=$("$work/xray/xray" version)
printf '%s\n' "$version_output" | head -n1
# Xray resolves `geoip:cn`/`geosite:*` from the configuration directory.
# Keep the official resource files beside the Xray binary (the lookup path
# used by Xray) so mainland
# access policies remain a compact file-backed reference after installation.
for asset in geoip.dat geosite.dat; do
  [ -f "$work/xray/$asset" ] || { printf '%s\n' "Xray release is missing $asset" >&2; exit 1; }
done

echo '== sing-box v1.13.16 =='
mkdir -p "$work/sb"
tar -xzf "$work/sing-box.tgz" -C "$work/sb"
singbox_binary=$(find "$work/sb" -type f -name sing-box -print -quit)
[ -n "$singbox_binary" ] || { printf '%s\n' 'sing-box binary is missing from release archive' >&2; exit 1; }
version_output=$("$singbox_binary" version)
printf '%s\n' "$version_output" | head -n1

echo '== Shadowsocks Rust v1.24.0 =='
mkdir -p "$work/ssrust"
tar -xJf "$work/shadowsocks-rust.txz" -C "$work/ssrust"
ssserver_binary=$(find "$work/ssrust" -type f -name ssserver -print -quit)
[ -n "$ssserver_binary" ] || { printf '%s\n' 'ssserver binary is missing from release archive' >&2; exit 1; }
version_output=$("$ssserver_binary" --version)
printf '%s\n' "$version_output" | head -n1

atomic_install "$work/mihomo" mihomo 0755
atomic_install "$work/xray/xray" xray 0755
for asset in geoip.dat geosite.dat; do atomic_install "$work/xray/$asset" "$asset" 0644; done
atomic_install "$singbox_binary" sing-box 0755
atomic_install "$ssserver_binary" ssserver 0755

echo '== installed =='
ls -l "$core_dir/mihomo" "$core_dir/xray" "$core_dir/sing-box" "$core_dir/ssserver" "$core_dir/geoip.dat" "$core_dir/geosite.dat"
