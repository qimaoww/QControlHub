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

qch_github_probe_budget() {
    qch_budget=3
    if [ -n "${qch_overall_deadline:-}" ]; then
        qch_probe_remaining=$((qch_overall_deadline - $(date +%s)))
        [ "$qch_probe_remaining" -gt 0 ] || return 1
        [ "$qch_probe_remaining" -ge "$qch_budget" ] || qch_budget=$qch_probe_remaining
    fi
    printf '%s\n' "$qch_budget"
}

qch_github_probe() {
    # TLS validation, two independent providers, literal routes, no HTTP proxy.
    qch_tls_budget=$(qch_github_probe_budget) || return 1
    curl -q --noproxy '*' --silent --head --output /dev/null --connect-timeout 2 --max-time "$qch_tls_budget" \
        --resolve "$1:443:$2" "https://$1/" 2>/dev/null
}

# Match curl's hostname NO_PROXY exclusions for public HTTPS origins. A leading
# dot includes the apex host; wildcard subdomains and host:port are literals.
qch_github_egress_proxy() (
    [ -n "${https_proxy:-${HTTPS_PROXY:-${all_proxy:-${ALL_PROXY:-}}}}" ] || exit 1
    qch_host=${1#https://}; qch_host=${qch_host%%/*}; qch_host=${qch_host%%\?*}
    qch_host=$(printf '%s' "$qch_host" | tr '[:upper:]' '[:lower:]')
    qch_exclusions=$(printf '%s' "${no_proxy:-${NO_PROXY:-}}" | tr '[:upper:]' '[:lower:]' | tr -d ' ')
    IFS=,
    set -f
    for qch_exclusion in $qch_exclusions; do
        qch_exclusion=${qch_exclusion#.}
        case "$qch_exclusion" in
            '*') exit 1 ;;
            '') continue ;;
            *) case "$qch_host" in "$qch_exclusion"|*."$qch_exclusion") exit 1 ;; esac ;;
        esac
    done
    exit 0
)

qch_github_probe_network() (
    qch_overall_deadline=${1:-}
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
            if qch_channel_budget=$(qch_github_probe_budget) && curl -q --noproxy '*' -6 --silent --head --output /dev/null --connect-timeout 2 --max-time "$qch_channel_budget" \
                'https://v6.gh-proxy.org/https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/go.mod' 2>/dev/null; then
                qch_github_mode=ipv6-only
                # API IPv6 connectivity also covers working DNS64/NAT64.
                # Preserve direct requests when translation already works.
                if qch_api_budget=$(qch_github_probe_budget) && curl -q --noproxy '*' -6 --silent --head --output /dev/null --connect-timeout 2 --max-time "$qch_api_budget" \
                    https://api.github.com/meta 2>/dev/null; then
                    qch_github_mode=ipv6-github-direct
                    if qch_github_probe_budget >/dev/null && command -v timeout >/dev/null 2>&1 && command -v getent >/dev/null 2>&1 &&
                        timeout 1 getent hosts ipv4only.arpa 2>/dev/null | awk '$1 ~ /:/ && $1 !~ /^::ffff:/ { found=1 } END { exit !found }'; then
                        qch_github_mode=nat64-dns64
                    fi
                fi
            fi
        fi
    fi
    printf 'GitHub 网络检测：IPv4 TLS=%s，IPv6 TLS=%s，网络=%s\n' "$qch_v4" "$qch_v6" "$qch_github_mode" >&2
    printf '%s\n' "$qch_github_mode"
)

qch_github_detect() {
    qch_now=$(date +%s)
    if [ "$qch_now" -lt "${qch_github_expires:-0}" ]; then return; fi
    qch_github_mode=$(qch_github_probe_network "${1:-}") || return 1
    qch_ttl=300
    [ "$qch_github_mode" != unknown ] || qch_ttl=15
    qch_github_expires=$((qch_now + qch_ttl))
    printf 'GitHub 下载：网络=%s，加速=%s\n' "$qch_github_mode" "$( [ "$qch_github_mode" = ipv6-only ] && printf true || printf false )" >&2
}

qch_github_download() {
    qch_overall_deadline=$(( $(date +%s) + ${4:-90} ))
    qch_checked_source=$(qch_github_original "$1") || { printf '%s\n' '拒绝不受信任的 GitHub 来源' >&2; return 1; }
    if ! qch_github_egress_proxy "$qch_checked_source"; then qch_github_detect "$qch_overall_deadline" || return 1; fi
    if (
        # A subshell isolates all state, traps and curl options from enrollment.
        qch_source=$(qch_github_original "$1") || { printf '%s\n' '拒绝不受信任的 GitHub 来源' >&2; exit 1; }
        qch_destination=$2
        qch_limit=${3:-134217728}
        qch_deadline=$qch_overall_deadline
        qch_use_channel=false
        qch_request_count=0
        if [ "$(date +%s)" -lt "${qch_github_channel_expires:-0}" ]; then qch_use_channel=true; fi
        qch_partial=$(mktemp "${qch_destination}.download.XXXXXX") || exit 1
        trap 'rm -f "$qch_partial"' EXIT
        qch_headers=$(mktemp "${qch_destination}.headers.XXXXXX") || exit 1
        qch_curl_error=$(mktemp "${qch_destination}.error.XXXXXX") || exit 1
        trap 'rm -f "$qch_partial" "$qch_headers" "$qch_curl_error"' EXIT
        trap 'exit 130' HUP INT TERM
        qch_attempt=1
        while [ "$qch_attempt" -le 3 ]; do
            qch_next=$qch_source
            qch_hops=0
            while [ "$qch_hops" -le 5 ]; do
                qch_remaining=$((qch_deadline - $(date +%s)))
                [ "$qch_remaining" -gt 0 ] || { printf '%s\n' 'GitHub 下载超时' >&2; break; }
                qch_wire=$qch_next
                set --
                qch_explicit_proxy=false
                if qch_github_egress_proxy "$qch_next"; then
                    qch_explicit_proxy=true
                elif [ "${qch_github_mode:-unknown}" = ipv6-only ] || "$qch_use_channel"; then
                    qch_wire="https://v6.gh-proxy.org/$qch_next"
                    set -- --noproxy '*'
                fi
                qch_request_count=$((qch_request_count + 1))
                # No automatic redirects: inspect every original GitHub target
                # before another request. -q disables user curlrc credentials.
                if qch_result=$(curl -q --silent --show-error --proto '=https' --connect-timeout 5 --max-time "$qch_remaining" "$@" \
                    --max-filesize "$qch_limit" --dump-header "$qch_headers" --output "$qch_partial" --write-out '%{http_code}\n%{redirect_url}' "$qch_wire" 2>"$qch_curl_error"); then
                    qch_status=$(printf '%s\n' "$qch_result" | head -n 1)
                    case "$qch_status" in
                        403|429)
                            qch_reset=$(awk 'tolower($1) == "x-ratelimit-reset:" { gsub(/\r/, "", $2); if ($2 ~ /^[0-9]+$/) print $2 }' "$qch_headers" | tail -n 1)
                            if [ -n "$qch_reset" ]; then
                                qch_reset_time=$(date -u -d "@$qch_reset" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || printf '%s' "$qch_reset")
                                printf 'GitHub API 配额已耗尽：HTTP %s；请在 %s 后重试（共享配额）\n' "$qch_status" "$qch_reset_time" >&2
                                exit 1
                            fi ;;
                    esac
                    case "$qch_status" in
                        200)
                            qch_size=$(wc -c < "$qch_partial")
                            [ "$qch_size" -gt 0 ] && [ "$qch_size" -le "$qch_limit" ] || exit 1
                            mv -f "$qch_partial" "$qch_destination" || exit 1
                            if "$qch_use_channel"; then exit 10; fi
                            exit 0 ;;
                        301|302|303|307|308)
                            qch_next=$(qch_github_original "$(printf '%s\n' "$qch_result" | tail -n 1)") || {
                                printf '%s\n' 'GitHub 下载拒绝跳转到非公开 HTTPS GitHub 来源' >&2; exit 1;
                            }
                            qch_hops=$((qch_hops + 1))
                            continue ;;
                        408|425|429|5??) break ;;
                        *) printf 'GitHub 下载失败：HTTP %s；请检查资源、API 配额和网络\n' "$qch_status" >&2; exit 1 ;;
                    esac
                else
                    qch_curl_status=$?
                    cat "$qch_curl_error" >&2
                    qch_request_host=${qch_wire#https://}
                    qch_request_host=${qch_request_host%%/*}
                    qch_request_host=${qch_request_host%%\?*}
                    printf 'GitHub 下载请求失败：请求=%s，重试=%s，网络=%s，curl 状态=%s，主机=%s\n' "$qch_request_count" "$qch_attempt" "${qch_github_mode:-unknown}" "$qch_curl_status" "$qch_request_host" >&2
                    case "$qch_curl_status" in
                        6|7|28|52|56)
                            if "$qch_explicit_proxy"; then
                                printf '%s\n' '保留指定的出口代理，不启用加速' >&2
                                exit 69
                            fi
                            if [ "$qch_wire" = "$qch_next" ] && ! "$qch_use_channel"; then
                                printf '%s\n' 'GitHub 直连解析或连接失败，尝试加速下载' >&2
                                qch_use_channel=true
                                continue
                            fi
                            printf '%s\n' '加速下载失败：请检查 v6.gh-proxy.org 的 DNS 和 HTTPS 连接；已保留原文件' >&2
                            exit 69 ;;
                        *) printf '%s\n' 'curl 请求失败，请检查 TLS、本地文件和大小限制；不再重试加速' >&2; exit 1 ;;
                    esac
                fi
            done
            [ "$qch_hops" -le 5 ] || { printf '%s\n' 'GitHub 下载跳转次数过多' >&2; exit 1; }
            qch_remaining=$((qch_deadline - $(date +%s)))
            [ "$qch_remaining" -gt "$qch_attempt" ] || break
            [ "$qch_attempt" -ge 3 ] || sleep "$qch_attempt"
            qch_attempt=$((qch_attempt + 1))
        done
        printf 'GitHub 下载失败：已尝试 %s 次，超过超时或重试限制；已保留原文件\n' "$qch_attempt" >&2
        exit 1
    ); then
        return 0
    else
        qch_download_status=$?
        case "$qch_download_status" in
            10) qch_github_channel_expires=$(( $(date +%s) + 30 )); return 0 ;;
            69) qch_github_expires=0; qch_github_channel_expires=0 ;;
            130) return 130 ;;
        esac
        return 1
    fi
}

# A gzip stream carries no executable mode. Prepare the staged binary before
# version verification; installation only starts after every core is verified.
qch_stage_gzip_binary() {
  qch_gzip_archive=$1
  qch_gzip_binary=$2
  shift 2
  gunzip -c "$qch_gzip_archive" > "$qch_gzip_binary" || return 1
  chmod 0755 "$qch_gzip_binary" || return 1
  "$qch_gzip_binary" "$@"
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
