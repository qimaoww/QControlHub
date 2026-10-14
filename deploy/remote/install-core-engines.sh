#!/bin/sh
# install-core-engines.sh — 下载并安装 mihomo / xray / sing-box / Shadowsocks Rust 官方 linux-amd64 二进制
# 用法（root）： sh deploy/remote/install-core-engines.sh
# 安装路径与 agent 默认 EngineSpec 一致：/usr/local/lib/qagent/cores/{mihomo,xray,sing-box,ssserver}
set -eu

# Public GitHub downloads only. Keep this contract in sync with internal/githubfetch
# and its Go/Shell fixture tests. Never pass control-plane URLs or credentials here.
qch_github_original() {
    qch_original=${1#https://v6.gh-proxy.org/}
    case "$qch_original" in
        https://*) ;;
        *) return 1 ;;
    esac
    case "$qch_original" in
        *[[:space:]]*|*\#*|*\\*) return 1 ;;
    esac
    qch_authority=${qch_original#https://}
    qch_authority=${qch_authority%%/*}
    qch_authority=${qch_authority%%\?*}
    qch_authority=$(printf '%s' "$qch_authority" | tr '[:upper:]' '[:lower:]')
    case "$qch_authority" in
        *[@:%?]*) return 1 ;;
        github.com|api.github.com|raw.githubusercontent.com|*.githubusercontent.com) ;;
        *) return 1 ;;
    esac
    printf '%s\n' "$qch_original"
}

qch_github_probe() {
    # TLS validation, two independent providers, literal routes, no HTTP proxy.
    curl -q --noproxy '*' --silent --head --output /dev/null --connect-timeout 2 --max-time 3 \
        --resolve "$1:443:$2" "https://$1/" 2>/dev/null
}

# Match curl/Go's hostname NO_PROXY exclusions for public HTTPS origins.
qch_github_egress_proxy() (
    [ -n "${https_proxy:-${HTTPS_PROXY:-${all_proxy:-${ALL_PROXY:-}}}}" ] || exit 1
    qch_host=${1#https://}; qch_host=${qch_host%%/*}; qch_host=${qch_host%%\?*}
    qch_host=$(printf '%s' "$qch_host" | tr '[:upper:]' '[:lower:]')
    qch_exclusions=$(printf '%s' "${no_proxy:-${NO_PROXY:-}}" | tr '[:upper:]' '[:lower:]' | tr -d ' ')
    IFS=,
    set -f
    for qch_exclusion in $qch_exclusions; do
        qch_exclusion=${qch_exclusion%:443}
        case "$qch_exclusion" in
            '*') exit 1 ;;
            '') continue ;;
            .*|\*.*)
                qch_suffix=${qch_exclusion#\*}
                case "$qch_host" in *"$qch_suffix") exit 1 ;; esac ;;
            *) case "$qch_host" in "$qch_exclusion"|*."$qch_exclusion") exit 1 ;; esac ;;
        esac
    done
    exit 0
)

qch_github_probe_network() (
    qch_github_mode=unknown
    qch_probe_dir=$(mktemp -d) || exit 1
    trap 'rm -rf "$qch_probe_dir"' EXIT
    trap 'kill ${qch_p1:-} ${qch_p2:-} ${qch_p3:-} ${qch_p4:-} 2>/dev/null || :; exit 130' HUP INT TERM
    (qch_github_probe one.one.one.one 1.1.1.1 && : > "$qch_probe_dir/v4a") & qch_p1=$!
    (qch_github_probe dns.google 8.8.8.8 && : > "$qch_probe_dir/v4b") & qch_p2=$!
    (qch_github_probe one.one.one.one '[2606:4700:4700::1111]' && : > "$qch_probe_dir/v6a") & qch_p3=$!
    (qch_github_probe dns.google '[2001:4860:4860::8888]' && : > "$qch_probe_dir/v6b") & qch_p4=$!
    wait "$qch_p1" || :; wait "$qch_p2" || :; wait "$qch_p3" || :; wait "$qch_p4" || :
    qch_v4=false; qch_v6=false
    if [ -e "$qch_probe_dir/v4a" ] || [ -e "$qch_probe_dir/v4b" ]; then qch_v4=true; fi
    if [ -e "$qch_probe_dir/v6a" ] || [ -e "$qch_probe_dir/v6b" ]; then qch_v6=true; fi
    if "$qch_v4" && "$qch_v6"; then
        qch_github_mode=dual-stack
    elif "$qch_v4"; then
        qch_github_mode=ipv4
    elif "$qch_v6"; then
        # A successful IPv6 TLS connection already proves a selected route.
        # Where iproute2 exists, require its independent route observation.
        if ! command -v ip >/dev/null 2>&1 || ip -6 route get 2606:4700:4700::1111 >/dev/null 2>&1; then
            # This proves system DNS and TLS work for the actual channel,
            # using a real resource rather than the proxy's homepage.
            if curl -q --noproxy '*' -6 --silent --head --output /dev/null --connect-timeout 2 --max-time 3 \
                'https://v6.gh-proxy.org/https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/go.mod' 2>/dev/null; then
                qch_github_mode=ipv6-only
                # API IPv6 connectivity also covers working DNS64/NAT64.
                # Preserve direct requests when translation already works.
                if curl -q --noproxy '*' -6 --silent --head --output /dev/null --connect-timeout 2 --max-time 3 \
                    https://api.github.com/meta 2>/dev/null; then
                    qch_github_mode=ipv6-github-direct
                    if command -v timeout >/dev/null 2>&1 && command -v getent >/dev/null 2>&1 &&
                        timeout 1 getent hosts ipv4only.arpa 2>/dev/null | awk '$1 ~ /:/ && $1 !~ /^::ffff:/ { found=1 } END { exit !found }'; then
                        qch_github_mode=nat64-dns64
                    fi
                fi
            fi
        fi
    fi
    printf '%s\n' "$qch_github_mode"
)

qch_github_detect() {
    qch_now=$(date +%s)
    if [ "$qch_now" -lt "${qch_github_expires:-0}" ]; then return; fi
    qch_github_mode=$(qch_github_probe_network) || return 1
    qch_ttl=300
    [ "$qch_github_mode" != unknown ] || qch_ttl=15
    qch_github_expires=$((qch_now + qch_ttl))
    printf 'GitHub download network=%s accelerated=%s\n' "$qch_github_mode" "$( [ "$qch_github_mode" = ipv6-only ] && printf true || printf false )" >&2
}

qch_github_download() {
    qch_github_detect || return 1
    (
        # A subshell isolates all state, traps and curl options from enrollment.
        qch_source=$(qch_github_original "$1") || { printf '%s\n' 'refusing untrusted GitHub source' >&2; exit 1; }
        qch_destination=$2
        qch_limit=${3:-134217728}
        qch_timeout=${4:-90}
        qch_partial=$(mktemp "${qch_destination}.download.XXXXXX") || exit 1
        trap 'rm -f "$qch_partial"' EXIT
        qch_headers=$(mktemp "${qch_destination}.headers.XXXXXX") || exit 1
        trap 'rm -f "$qch_partial" "$qch_headers"' EXIT
        trap 'exit 130' HUP INT TERM
        qch_attempt=1
        while [ "$qch_attempt" -le 3 ]; do
            qch_deadline=$(( $(date +%s) + qch_timeout ))
            qch_next=$qch_source
            qch_hops=0
            while [ "$qch_hops" -le 5 ]; do
                qch_remaining=$((qch_deadline - $(date +%s)))
                [ "$qch_remaining" -gt 0 ] || { printf '%s\n' 'GitHub download attempt timed out' >&2; break; }
                qch_wire=$qch_next
                if [ "$qch_github_mode" = ipv6-only ] && ! qch_github_egress_proxy "$qch_next"; then qch_wire="https://v6.gh-proxy.org/$qch_next"; fi
                # No automatic redirects: inspect every original GitHub target
                # before another request. -q disables user curlrc credentials.
                if qch_result=$(curl -q --silent --show-error --proto '=https' --connect-timeout 10 --max-time "$qch_remaining" \
                    --max-filesize "$qch_limit" --dump-header "$qch_headers" --output "$qch_partial" --write-out '%{http_code}\n%{redirect_url}' "$qch_wire"); then
                    qch_status=$(printf '%s\n' "$qch_result" | head -n 1)
                    case "$qch_status" in
                        403|429)
                            qch_reset=$(awk 'tolower($1) == "x-ratelimit-reset:" { gsub(/\r/, "", $2); if ($2 ~ /^[0-9]+$/) print $2 }' "$qch_headers" | tail -n 1)
                            if [ -n "$qch_reset" ]; then
                                qch_reset_time=$(date -u -d "@$qch_reset" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || printf '%s' "$qch_reset")
                                printf 'GitHub API quota exhausted: HTTP %s; retry after %s (shared upstream quota)\n' "$qch_status" "$qch_reset_time" >&2
                                exit 1
                            fi ;;
                    esac
                    case "$qch_status" in
                        200)
                            qch_size=$(wc -c < "$qch_partial")
                            [ "$qch_size" -gt 0 ] && [ "$qch_size" -le "$qch_limit" ] || exit 1
                            mv -f "$qch_partial" "$qch_destination" || exit 1
                            exit 0 ;;
                        301|302|303|307|308)
                            qch_next=$(qch_github_original "$(printf '%s\n' "$qch_result" | tail -n 1)") || {
                                printf '%s\n' 'GitHub download rejected redirect outside public HTTPS GitHub sources' >&2; exit 1;
                            }
                            qch_hops=$((qch_hops + 1))
                            continue ;;
                        408|425|429|5??) break ;;
                        *) printf 'GitHub download response failed: HTTP %s; check repository, resource, GitHub API rate limits and channel availability\n' "$qch_status" >&2; exit 1 ;;
                    esac
                else
                    printf 'GitHub download request failed: attempt=%s network=%s; check DNS and HTTPS connectivity\n' "$qch_attempt" "$qch_github_mode" >&2
                    break
                fi
            done
            [ "$qch_hops" -le 5 ] || { printf '%s\n' 'too many GitHub download redirects' >&2; exit 1; }
            [ "$qch_attempt" -ge 3 ] || sleep "$qch_attempt"
            qch_attempt=$((qch_attempt + 1))
        done
        printf '%s\n' 'GitHub download failed after 3 attempts; existing files preserved' >&2
        exit 1
    )
}

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
gunzip -c "$work/mihomo.gz" > "$work/mihomo"
version_output=$("$work/mihomo" -v)
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
