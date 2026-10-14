
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
