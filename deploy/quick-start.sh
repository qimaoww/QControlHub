#!/usr/bin/env bash
# QControlHub 一键交互式部署脚本（Linux）
#
# 提供两种部署方式：
#   bundled  — Docker Compose 内置 PostgreSQL + 控制面和网页服务（从零开始）
#   external — 连接已有 PostgreSQL，部署控制面和网页服务
#
# 用法：
#   ./deploy/quick-start.sh                          # 交互式选择部署方式
#   ./deploy/quick-start.sh -m bundled               # 全套部署（内置 PostgreSQL + 控制面和网页服务）
#   ./deploy/quick-start.sh -m external -d 'postgresql://user:pass@db:5432/qcontrolhub?sslmode=verify-full'

set -euo pipefail

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

MODE=""
ACTION=""
DATABASE_URL=""
ADMIN_TOKEN=""
DOCKER_NETWORK=""
FORCE=false
READY_TIMEOUT=360

install_dir_preference_file() {
    local config_root="${XDG_CONFIG_HOME:-${HOME:-}}"
    [ -n "$config_root" ] || return 1
    case "$config_root" in
        /*) printf '%s\n' "$config_root/qcontrolhub/install-dir" ;;
        *) return 1 ;;
    esac
}

read_install_dir_preference() {
    local preference_file value
    preference_file="$(install_dir_preference_file 2>/dev/null)" || return 0
    [ -f "$preference_file" ] || return 0
    [ ! -L "$preference_file" ] || return 0
    value="$(<"$preference_file")" || return 0
    case "$value" in
        /*) ;;
        *) return 0 ;;
    esac
    case "$value" in
        *$'\n'*|*$'\r'*) return 0 ;;
    esac
    printf '%s\n' "$value"
}

persist_install_dir_preference() {
    local install_dir="$1" preference_file preference_dir temp_file
    case "$install_dir" in
        /*) ;;
        *) return 1 ;;
    esac
    case "$install_dir" in
        *$'\n'*|*$'\r'*) return 1 ;;
    esac
    preference_file="$(install_dir_preference_file 2>/dev/null)" || return 1
    preference_dir="$(dirname -- "$preference_file")"
    [ ! -L "$preference_dir" ] || return 1
    mkdir -p "$preference_dir" || return 1
    chmod 0700 "$preference_dir" || return 1
    [ ! -L "$preference_file" ] || return 1
    umask 077
    temp_file="$(mktemp "$preference_dir/.install-dir.XXXXXX")" || return 1
    if ! printf '%s\n' "$install_dir" > "$temp_file"; then
        rm -f -- "$temp_file"
        return 1
    fi
    chmod 0600 "$temp_file" || {
        rm -f -- "$temp_file"
        return 1
    }
    if ! mv -f -- "$temp_file" "$preference_file"; then
        rm -f -- "$temp_file"
        return 1
    fi
}

usage() {
    cat <<'USAGE'
用法：
  ./deploy/quick-start.sh [-o install|update|uninstall] [-m bundled|external] [选项]

操作：
  install             安装或重新配置 QControlHub
  update              更新现有部署并保持配置和数据
  uninstall           卸载服务，保留配置、密钥和数据库卷

部署模式：
  bundled             内置 PostgreSQL，部署控制面和网页服务
  external            连接已有 PostgreSQL，部署控制面和网页服务

选项：
  -m MODE             选择部署模式；省略时交互选择
  -o OPERATION        选择安装、更新或卸载；省略时交互选择
  -d DATABASE_URL     外部模式使用的 PostgreSQL 连接串
  -a ADMIN_TOKEN      管理员令牌（至少 32 字节）
  -n DOCKER_NETWORK   外部模式安装连接的已有 Docker 网络；省略时可交互选择或使用默认网络
  -f                  内置模式显式轮换令牌 与应用密钥；外部模式更新不支持
  -t SECONDS          每阶段就绪检查超时时间（默认 360 秒，包含数据库迁移等待）
  -h                  显示帮助

普通外部模式更新逐字节保留 .env，不迁移凭据或轮换密钥。
交互式菜单中选择的安装目录会保存到当前用户配置；后续从远程一键命令运行时会自动复用。显式设置 QCH_INSTALL_DIR 时以该值为准。
USAGE
}

die() {
    printf '错误：%s\n' "$*" >&2
    exit 1
}

bootstrap_streamed_script() {
    local script_path install_dir install_dir_source persisted_install_dir origin_url branch marker_file marker_temp bootstrap_ref base_url script_temp compose_temp install_label
    script_path="${BASH_SOURCE[0]}"
    if [ "${QCH_BOOTSTRAP:-}" != 1 ]; then
        case "$script_path" in
            /dev/fd/*|/proc/self/fd/*)
                # Bash may still have unread source in the producer's pipe.
                # Drain it before exec closes that pipe (curl would report 23).
                cat -- "$script_path" >/dev/null || die "读取远程入口脚本失败"
                ;;
            *) return 0 ;;
        esac
    fi
    unset QCH_BOOTSTRAP

    command -v curl >/dev/null 2>&1 || die "缺少依赖：curl"
    if [ -n "${QCH_INSTALL_DIR:-}" ]; then
        install_dir="$QCH_INSTALL_DIR"
        install_dir_source="explicit"
    elif [ -f "$PWD/.qcontrolhub-quick-start" ] || { [ -d "$PWD/.git" ] && [ -f "$PWD/deploy/quick-start.sh" ] && [ -f "$PWD/docker-compose.yml" ]; }; then
        install_dir="$PWD"
        install_dir_source="current"
    elif persisted_install_dir="$(read_install_dir_preference)" && [ -n "$persisted_install_dir" ]; then
        install_dir="$persisted_install_dir"
        install_dir_source="preference"
    else
        install_dir="$PWD/qcontrolhub"
        install_dir_source="default"
    fi
    case "$install_dir" in
        /*) ;;
        *) install_dir="$PWD/$install_dir" ;;
    esac
    case "$install_dir" in
        *$'\n'*|*$'\r'*) die "QCH_INSTALL_DIR 不能包含换行" ;;
    esac
    [ "$install_dir" != "/" ] || die "安装目录不能是文件系统根目录"
    [ ! -L "$install_dir" ] || die "安装目录不能是符号链接：$install_dir"
    marker_file="$install_dir/.qcontrolhub-quick-start"
    [ ! -L "$marker_file" ] || die "一键安装标记不能是符号链接：$marker_file"
    bootstrap_ref="${QCH_BOOTSTRAP_REF:-main}"
    case "$bootstrap_ref" in
        ""|/*|*/|*..*|*[!A-Za-z0-9._/-]*) die "QCH_BOOTSTRAP_REF 不是安全的 Git 引用" ;;
    esac
    base_url="https://raw.githubusercontent.com/qimaoww/qcontrolhub/$bootstrap_ref"

    if [ -e "$install_dir" ]; then
        [ -d "$install_dir" ] || die "安装路径已存在但不是目录：$install_dir"
        if [ -f "$marker_file" ]; then
            :
        elif [ -d "$install_dir/.git" ]; then
            command -v git >/dev/null 2>&1 || die "迁移旧版 Git 安装目录需要 git"
            origin_url="$(git -C "$install_dir" remote get-url origin 2>/dev/null || true)"
            case "$origin_url" in
                https://github.com/qimaoww/qcontrolhub|https://github.com/qimaoww/qcontrolhub.git|git@github.com:qimaoww/qcontrolhub.git) ;;
                *) die "安装目录不是 QControlHub 官方仓库：$install_dir" ;;
            esac
            branch="$(git -C "$install_dir" symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
            [ "$branch" = "main" ] || die "旧版 Git 安装目录必须位于 main 分支，当前为：${branch:-分离状态}"
            git -C "$install_dir" diff --quiet -- deploy/quick-start.sh docker-compose.yml || \
                die "旧版 Git 安装目录的运行文件包含未提交修改，请先处理"
            git -C "$install_dir" diff --cached --quiet -- deploy/quick-start.sh docker-compose.yml || \
                die "旧版 Git 安装目录的运行文件包含已暂存修改，请先处理"
        # A directory previously selected from the menu is explicit user
        # intent. It can contain deployment state even when it predates the
        # standalone bootstrap marker.
        elif [ "$install_dir_source" = "preference" ]; then
            :
        elif [ -n "$(ls -A "$install_dir")" ]; then
            die "安装目录已存在且不是 QControlHub 一键安装目录：$install_dir"
        fi
        install_label="更新"
    else
        mkdir -p "$install_dir"
        install_label="安装"
    fi
    [ ! -L "$install_dir/deploy" ] || die "运行文件目录不能是符号链接：$install_dir/deploy"
    mkdir -p "$install_dir/deploy"

    echo "-> ${install_label} QControlHub 运行文件：$install_dir"
    script_temp="$(mktemp "$install_dir/deploy/.quick-start.sh.tmp.XXXXXX")"
    compose_temp="$(mktemp "$install_dir/.docker-compose.yml.tmp.XXXXXX")"
    marker_temp=""
    cleanup_bootstrap_downloads() {
        rm -f -- "$script_temp" "$compose_temp"
        [ -z "$marker_temp" ] || rm -f -- "$marker_temp"
    }
    trap cleanup_bootstrap_downloads EXIT HUP INT TERM
    qch_github_download "$base_url/deploy/quick-start.sh" "$script_temp" 2097152 20 || die "下载 quick-start.sh 失败"
    qch_github_download "$base_url/docker-compose.yml" "$compose_temp" 2097152 20 || die "下载 docker-compose.yml 失败"
    bash -n "$script_temp" || die "下载的 quick-start.sh 语法无效"
    grep -Fq 'name: qcontrolhub' "$compose_temp" || die "下载的 docker-compose.yml 内容无效"
    chmod 0755 "$script_temp"
    chmod 0644 "$compose_temp"
    mv -f -- "$compose_temp" "$install_dir/docker-compose.yml"
    mv -f -- "$script_temp" "$install_dir/deploy/quick-start.sh"
    marker_temp="$(mktemp "$install_dir/.qcontrolhub-quick-start.tmp.XXXXXX")"
    printf '%s\n' "$base_url" > "$marker_temp"
    chmod 0644 "$marker_temp"
    mv -f -- "$marker_temp" "$marker_file"
    marker_temp=""
    trap - EXIT HUP INT TERM
    persist_install_dir_preference "$install_dir" ||
        echo "警告：无法保存安装目录，下次从远程一键命令运行时可能需要重新设置目录：$install_dir" >&2
    export QCH_INSTALL_DIR="$install_dir"
    exec "$install_dir/deploy/quick-start.sh" "$@"
}

bootstrap_streamed_script "$@"

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    usage
    exit 0
fi

while getopts ':m:o:d:a:n:ft:h' opt; do
    case "$opt" in
        m) MODE="$OPTARG" ;;
        o) ACTION="$OPTARG" ;;
        d) DATABASE_URL="$OPTARG" ;;
        a) ADMIN_TOKEN="$OPTARG" ;;
        n) DOCKER_NETWORK="$OPTARG" ;;
        f) FORCE=true ;;
        t) READY_TIMEOUT="$OPTARG" ;;
        h) usage; exit 0 ;;
        :) die "选项 -$OPTARG 需要参数；使用 -h 查看帮助" ;;
        \?) die "未知选项：-$OPTARG；使用 -h 查看帮助" ;;
    esac
done
shift $((OPTIND - 1))
[ "$#" -eq 0 ] || die "不支持位置参数：$1；使用 -h 查看帮助"

case "$MODE" in
    ""|bundled|external) ;;
    *) die "未知部署模式：$MODE（可选 bundled / external）" ;;
esac
case "$ACTION" in
    ""|install|update|uninstall) ;;
    *) die "未知操作：$ACTION（可选 install / update / uninstall）" ;;
esac
if ! [[ "$READY_TIMEOUT" =~ ^[1-9][0-9]*$ ]]; then
    die "就绪检查超时时间必须是正整数"
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
WORK_DIR="$REPO_ROOT"
ENV_FILE="$REPO_ROOT/.env"
EXTERNAL_COMPOSE_FILE="$REPO_ROOT/docker-compose.external.yml"
SECRET_COMPOSE_FILE="$REPO_ROOT/docker-compose.secrets.yml"
SECRET_DIR="$REPO_ROOT/.secrets"
CONFIG_KEY_FILE="$SECRET_DIR/config-encryption-key"
PREVIOUS_CONFIG_KEYS_FILE="$SECRET_DIR/config-encryption-previous-keys"
ADMIN_TOKEN_TO_DISPLAY=""
ADMIN_TOKEN_DIGEST=""
CONFIG_KEY=""
PREVIOUS_CONFIG_KEYS=""
UPDATE_ROLLBACK_ARMED=false
UPDATE_SERVICES_CHANGED=false
UPDATE_BACKUP_DIR=""
UPDATE_HAD_SECRET_COMPOSE=false
UPDATE_CONTROL_IMAGE=""
UPDATE_CONTROL_REF=""
UPDATE_CONTROL_BACKUP_TAG=""
UPDATE_WEB_IMAGE=""
UPDATE_WEB_REF=""
UPDATE_WEB_BACKUP_TAG=""

require_commands() {
    local command_name
    for command_name in docker openssl curl awk mktemp; do
        command -v "$command_name" >/dev/null 2>&1 || die "缺少依赖：$command_name"
    done
    docker compose version >/dev/null 2>&1 || die "当前 Docker 未安装 Compose v2（docker compose）"
    docker info >/dev/null 2>&1 || die "Docker Engine 未运行，请先启动 Docker Engine"
}

random_hex() {
    openssl rand -hex 32
}

sha256_hex() {
    printf '%s' "$1" | openssl dgst -sha256 | awk '{print $NF}'
}

validate_admin_token_digest() {
    local digest="$1"
    [ "${#digest}" -eq 64 ] || die "QCH_ADMIN_TOKEN_SHA256 必须是 64 个十六进制字符"
    case "$digest" in
        *[!0-9A-Fa-f]*) die "QCH_ADMIN_TOKEN_SHA256 必须是 64 个十六进制字符" ;;
    esac
}

read_secret_file() {
    local file="$1" value
    [ -f "$file" ] || return 0
    [ ! -L "$file" ] || die "密钥文件不能是符号链接：$file"
    value="$(<"$file")"
    case "$value" in
        *$'\n'*|*$'\r'*) die "密钥文件只能包含一行：$file" ;;
    esac
    printf '%s' "$value"
}

write_secret_file() {
    local file="$1" value="$2" temp_file
    [ ! -L "$SECRET_DIR" ] || die "密钥目录不能是符号链接：$SECRET_DIR"
    mkdir -p "$SECRET_DIR"
    chmod 0700 "$SECRET_DIR"
    [ ! -L "$file" ] || die "密钥文件不能是符号链接：$file"
    umask 077
    temp_file="$(mktemp "$SECRET_DIR/.tmp.XXXXXX")"
    printf '%s\n' "$value" > "$temp_file"
    # The parent directory is private on the host. Compose bind-mounts file
    # secrets without honoring uid/gid/mode, so the non-root control-plane
    # process needs the mounted file itself to be readable.
    chmod 0644 "$temp_file"
    mv -f -- "$temp_file" "$file"
}

backup_secret_file() {
    local file="$1" backup_file
    [ -f "$file" ] || return 0
    backup_file="${file}.bak.$(date +%Y%m%d%H%M%S).$$.${RANDOM}"
    cp -p -- "$file" "$backup_file"
    chmod 0600 "$backup_file"
    echo "-> 已备份密钥：$backup_file"
}

prepare_admin_token() {
    local raw_token stored_digest legacy_token legacy_digest
    raw_token="$ADMIN_TOKEN"
    stored_digest="$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
    legacy_token="$(read_env_key QCH_ADMIN_TOKEN)"
    if [ "$FORCE" = false ] && [ -z "$raw_token" ] && [ -n "$stored_digest" ] && [ -n "$legacy_token" ]; then
        validate_secret QCH_ADMIN_TOKEN "$legacy_token"
        validate_admin_token_digest "$stored_digest"
        legacy_digest="$(sha256_hex "$legacy_token")"
        [ "$legacy_digest" = "$(printf '%s' "$stored_digest" | tr 'A-F' 'a-f')" ] || \
            die "QCH_ADMIN_TOKEN 与 QCH_ADMIN_TOKEN_SHA256 不匹配；请先确认正确的管理员令牌"
    fi
    if [ "$FORCE" = true ]; then
        raw_token="${ADMIN_TOKEN:-$(random_hex)}"
    elif [ -n "$raw_token" ]; then
        :
    elif [ -n "$stored_digest" ]; then
        raw_token=""
    elif [ -n "$legacy_token" ]; then
        raw_token="$legacy_token"
    else
        raw_token="$(random_hex)"
    fi
    if [ -n "$raw_token" ]; then
        validate_secret QCH_ADMIN_TOKEN "$raw_token"
        stored_digest="$(sha256_hex "$raw_token")"
        ADMIN_TOKEN_TO_DISPLAY="$raw_token"
    else
        ADMIN_TOKEN_TO_DISPLAY=""
    fi
    validate_admin_token_digest "$stored_digest"
    ADMIN_TOKEN_DIGEST="$(printf '%s' "$stored_digest" | tr 'A-F' 'a-f')"
}

prepare_config_keyring() {
    local legacy_key legacy_previous
    CONFIG_KEY="$(read_secret_file "$CONFIG_KEY_FILE")"
    PREVIOUS_CONFIG_KEYS="$(read_secret_file "$PREVIOUS_CONFIG_KEYS_FILE")"
    legacy_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    legacy_previous="$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"
    [ -n "$CONFIG_KEY" ] || CONFIG_KEY="$legacy_key"
    [ -n "$PREVIOUS_CONFIG_KEYS" ] || PREVIOUS_CONFIG_KEYS="$legacy_previous"
    if [ "$FORCE" = true ]; then
        backup_secret_file "$CONFIG_KEY_FILE"
        backup_secret_file "$PREVIOUS_CONFIG_KEYS_FILE"
        PREVIOUS_CONFIG_KEYS="$(prepend_unique_csv "$PREVIOUS_CONFIG_KEYS" "$CONFIG_KEY")"
        CONFIG_KEY="$(random_hex)"
    elif [ -z "$CONFIG_KEY" ]; then
        CONFIG_KEY="$(random_hex)"
    fi
    validate_secret QCH_CONFIG_ENCRYPTION_KEY "$CONFIG_KEY"
    write_secret_file "$CONFIG_KEY_FILE" "$CONFIG_KEY"
    write_secret_file "$PREVIOUS_CONFIG_KEYS_FILE" "$PREVIOUS_CONFIG_KEYS"
}

read_env_key() {
    local key="$1"
    [ -f "$ENV_FILE" ] || return 0
    awk -v key="$key" '
        {
            line = $0
            sub(/^\xef\xbb\xbf/, "", line)
            if (index(line, key "=") == 1) {
                print substr(line, length(key) + 2)
                exit
            }
        }
    ' "$ENV_FILE"
}

backup_env() {
    [ -f "$ENV_FILE" ] || return 0
    local backup_file
    umask 077
    backup_file="${ENV_FILE}.bak.$(date +%Y%m%d%H%M%S).$$.${RANDOM}"
    cp -p -- "$ENV_FILE" "$backup_file"
    awk '
        /^(QCH_ADMIN_TOKEN|QCH_CONFIG_ENCRYPTION_KEY|QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)=/ {
            sub(/=.*/, "=")
        }
        { print }
    ' "$backup_file" > "${backup_file}.sanitized"
    mv -f -- "${backup_file}.sanitized" "$backup_file"
    chmod 600 "$backup_file"
    echo "-> 已备份现有 .env：$backup_file"
}

update_env_file() {
    local temp_file next_file entry key value
    umask 077
    temp_file="$(mktemp "${ENV_FILE}.tmp.XXXXXX")"
    if [ -f "$ENV_FILE" ]; then
        cp -- "$ENV_FILE" "$temp_file"
    fi

    for entry in "$@"; do
        key="${entry%%=*}"
        value="${entry#*=}"
        case "$key" in
            ''|*[!A-Za-z0-9_]*) rm -f -- "$temp_file"; die "非法环境变量名：$key" ;;
        esac
        next_file="$(mktemp "${ENV_FILE}.tmp.XXXXXX")"
        if ! awk -v key="$key" -v value="$value" '
            BEGIN { prefix = key "="; bom = "\357\273\277"; replaced = 0 }
            {
                line = $0
                line_bom = ""
                if (substr(line, 1, 3) == bom) {
                    line_bom = bom
                    line = substr(line, 4)
                }
            }
            index(line, prefix) == 1 {
                if (!replaced) { print prefix value; replaced = 1 }
                next
            }
            { print }
            END { if (!replaced) print prefix value }
        ' "$temp_file" > "$next_file"; then
            rm -f -- "$temp_file" "$next_file"
            die "更新 .env 失败"
        fi
        chmod 600 "$next_file"
        mv -f -- "$next_file" "$temp_file"
    done

    chmod 600 "$temp_file"
    mv -f -- "$temp_file" "$ENV_FILE"
}

validate_secret() {
    local name="$1" value="$2"
    if [ "${#value}" -lt 32 ]; then
        die "$name 至少需要 32 个字符"
    fi
    case "$value" in
        *$'\n'*|*$'\r'*) die "$name 不能包含换行" ;;
    esac
}

validate_database_url() {
    local url="$1"
    case "$url" in
        postgresql://*|postgres://*) ;;
        *) die "外部 PostgreSQL 连接串格式错误" ;;
    esac
    case "$url" in
        *$'\n'*|*$'\r'*) die "DATABASE_URL 不能包含换行" ;;
    esac
}

append_trusted_proxy() {
    local current="$1" cidr="$2"
    case ",$current," in
        *",$cidr,"*) printf '%s' "$current" ;;
        ",,") printf '%s' "$cidr" ;;
        *) printf '%s,%s' "$current" "$cidr" ;;
    esac
}

prepend_unique_csv() {
    local current="$1" value="$2"
    [ -n "$value" ] || { printf '%s' "$current"; return; }
    case ",$current," in
        *",$value,"*) printf '%s' "$current" ;;
        ",,") printf '%s' "$value" ;;
        *) printf '%s,%s' "$value" "$current" ;;
    esac
}

write_external_compose() {
    local docker_network config_key
    docker_network="$(read_env_key QCH_DOCKER_NETWORK)"
    config_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    cat > "$EXTERNAL_COMPOSE_FILE" <<'YAML'
name: qcontrolhub

services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:latest
    restart: unless-stopped
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL:?必须设置 QCH_DATABASE_URL}
      QCH_DATABASE_STARTUP_TIMEOUT: ${QCH_DATABASE_STARTUP_TIMEOUT:-5m}
      QCH_ADMIN_TOKEN: ${QCH_ADMIN_TOKEN:-}
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:-}
      QCH_LISTEN: 0.0.0.0:8080
      QCH_DEFAULT_AGENT_ENGINES: ${QCH_DEFAULT_AGENT_ENGINES:-}
      QCH_BEHIND_TLS_PROXY: ${QCH_BEHIND_TLS_PROXY:-true}
      QCH_ALLOW_INSECURE_HTTP: ${QCH_ALLOW_INSECURE_HTTP:-false}
      QCH_ALLOW_INSECURE_DATABASE: ${QCH_ALLOW_INSECURE_DATABASE:-false}
      QCH_CORS_ORIGINS: ${QCH_CORS_ORIGINS:-}
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-}
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-172.30.254.2/32,172.30.254.1/32}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_WEBHOOK_SECRET: ${QCH_WEBHOOK_SECRET:-}
YAML
    if [ -n "$config_key" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:?必须设置 QCH_CONFIG_ENCRYPTION_KEY}
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}
      QCH_OPERATOR_TOKENS: ${QCH_OPERATOR_TOKENS:-}
      QCH_AUDITOR_TOKENS: ${QCH_AUDITOR_TOKENS:-}
      QCH_READONLY_TOKENS: ${QCH_READONLY_TOKENS:-}
      QCH_AGENT_PUBLIC_IP_PROBE_ENABLED: ${QCH_AGENT_PUBLIC_IP_PROBE_ENABLED:-true}
      QCH_AGENT_PUBLIC_IP_PROBE_IPV4_ENDPOINT: ${QCH_AGENT_PUBLIC_IP_PROBE_IPV4_ENDPOINT:-}
      QCH_AGENT_PUBLIC_IP_PROBE_IPV6_ENDPOINT: ${QCH_AGENT_PUBLIC_IP_PROBE_IPV6_ENDPOINT:-}
      QCH_AGENT_PUBLIC_IP_PROBE_INTERVAL: ${QCH_AGENT_PUBLIC_IP_PROBE_INTERVAL:-5m}
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      - control-host
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      control-host:
        ipv4_address: ${QCH_CONTROL_PLANE_PROXY_ADDRESS:-172.30.254.3}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    read_only: true
    tmpfs:
      - /tmp:size=16m,mode=1777
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    pids_limit: 128
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://127.0.0.1:8080/readyz"]
      interval: 10s
      timeout: 3s
      retries: 6
      start_period: ${QCH_DATABASE_STARTUP_TIMEOUT:-5m}
    stop_grace_period: 15s
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:latest
    restart: unless-stopped
    depends_on:
      control-plane:
        condition: service_healthy
    ports:
      - "${QCH_BIND_ADDRESS:-127.0.0.1}:${QCH_PORT:-8080}:8080"
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      - control-host
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    networks:
      control-host:
        ipv4_address: ${QCH_WEB_PROXY_ADDRESS:-172.30.254.2}
YAML
    fi
    cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    read_only: true
    tmpfs:
      - /tmp:size=8m,mode=1777
      - /var/cache/nginx:size=8m,mode=1777
      - /var/run:size=1m,mode=1777
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETGID
      - SETUID
    security_opt:
      - no-new-privileges:true
    pids_limit: 64
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://127.0.0.1:8080/healthz"]
      interval: 10s
      timeout: 3s
      retries: 6
      start_period: 5s
    stop_grace_period: 10s

networks:
  control-host:
YAML
    if [ -n "$docker_network" ]; then
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    name: ${QCH_DOCKER_NETWORK:?必须设置自定义 Docker 网络}
    external: true
YAML
    else
        cat >> "$EXTERNAL_COMPOSE_FILE" <<'YAML'
    ipam:
      config:
        - subnet: ${QCH_CONTROL_PROXY_SUBNET:-172.30.254.0/24}
          gateway: ${QCH_CONTROL_PROXY_GATEWAY:-172.30.254.1}
YAML
    fi
}

write_secret_compose_override() {
    cat > "$SECRET_COMPOSE_FILE" <<'YAML'
services:
  control-plane:
    environment:
      QCH_ADMIN_TOKEN: ""
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:?必须设置管理员令牌摘要}
      QCH_CONFIG_ENCRYPTION_KEY: ""
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ""
      QCH_CONFIG_ENCRYPTION_KEY_FILE: /run/secrets/qch-config-encryption-key
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE: /run/secrets/qch-config-encryption-previous-keys
    volumes:
      - type: bind
        source: ${QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE:?必须设置配置加密密钥文件}
        target: /run/secrets/qch-config-encryption-key
        read_only: true
      - type: bind
        source: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE:?必须设置旧配置加密密钥文件}
        target: /run/secrets/qch-config-encryption-previous-keys
        read_only: true
YAML
    chmod 0600 "$SECRET_COMPOSE_FILE"
}

COMPOSE_ARGS=()

run_compose() (
    # --env-file alone does not override exported shell variables. The
    # external deployment must read its own database, credentials and network.
    if [ "$MODE" = external ]; then
        local env_key
        while IFS= read -r env_key; do
            unset "$env_key"
        done < <(awk '
            { sub(/^\xef\xbb\xbf/, "") }
            /^QCH_[A-Za-z0-9_]+=/ { sub(/=.*/, ""); print }
        ' "$ENV_FILE")
        unset QCH_DATABASE_URL QCH_ADMIN_TOKEN QCH_ADMIN_TOKEN_SHA256 \
            QCH_CONFIG_ENCRYPTION_KEY QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS \
            QCH_CONFIG_ENCRYPTION_KEY_FILE QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_FILE \
            QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE \
            QCH_ALLOW_INSECURE_DATABASE QCH_DOCKER_NETWORK QCH_BIND_ADDRESS QCH_PORT \
            QCH_DATABASE_STARTUP_TIMEOUT
    fi
    QCH_SOURCE_DIR="$REPO_ROOT" docker compose -p qcontrolhub --project-directory "$WORK_DIR" --env-file "$ENV_FILE" "$@"
)

compose() {
    run_compose "${COMPOSE_ARGS[@]}" "$@"
}

resolve_bundled_image_refs() {
    local images image_ref
    BUNDLED_CONTROL_IMAGE_REF=""
    BUNDLED_WEB_IMAGE_REF=""
    BUNDLED_POSTGRES_IMAGE_REF=""
    # Let Compose resolve .env quoting, comments and exported overrides. The
    # production Compose owns these three image repositories.
    images="$(run_compose -f "$REPO_ROOT/docker-compose.yml" config --images)" || \
        die "无法解析内置部署的镜像配置"
    while IFS= read -r image_ref; do
        case "$image_ref" in
            ghcr.io/qimaoww/qcontrol-plane:*) BUNDLED_CONTROL_IMAGE_REF="$image_ref" ;;
            ghcr.io/qimaoww/qcontrol-web:*) BUNDLED_WEB_IMAGE_REF="$image_ref" ;;
            postgres:*|docker.io/library/postgres:*) BUNDLED_POSTGRES_IMAGE_REF="$image_ref" ;;
        esac
    done <<< "$images"
    [ -n "$BUNDLED_CONTROL_IMAGE_REF" ] && [ -n "$BUNDLED_WEB_IMAGE_REF" ] && \
        [ -n "$BUNDLED_POSTGRES_IMAGE_REF" ] || die "内置部署的镜像配置不完整"
}

prepare_external_update_compose() {
    # Merge only application settings. Do not regenerate the topology: an
    # existing deployment may have its own network, ports, CA mounts, etc.
    cat > "$UPDATE_BACKUP_DIR/docker-compose.update.yml" <<'YAML'
services:
  control-plane:
    image: ghcr.io/qimaoww/qcontrol-plane:latest
    restart: unless-stopped
    environment:
      QCH_DATABASE_URL: ${QCH_DATABASE_URL:?必须设置 QCH_DATABASE_URL}
      QCH_DATABASE_STARTUP_TIMEOUT: ${QCH_DATABASE_STARTUP_TIMEOUT:-5m}
      QCH_ADMIN_TOKEN: ${QCH_ADMIN_TOKEN:-}
      QCH_ADMIN_TOKEN_SHA256: ${QCH_ADMIN_TOKEN_SHA256:-}
      QCH_CONFIG_ENCRYPTION_KEY: ${QCH_CONFIG_ENCRYPTION_KEY:-}
      QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}
      QCH_ALLOW_INSECURE_DATABASE: ${QCH_ALLOW_INSECURE_DATABASE:-false}
    healthcheck:
      start_period: ${QCH_DATABASE_STARTUP_TIMEOUT:-5m}
  qcontrol-web:
    image: ghcr.io/qimaoww/qcontrol-web:latest
    restart: unless-stopped
YAML
    # Keep variable references, not rendered credentials, in the persisted
    # Compose. The existing secret override remains a separate unmodified file.
    run_compose -f "$EXTERNAL_COMPOSE_FILE" -f "$UPDATE_BACKUP_DIR/docker-compose.update.yml" \
        config --no-interpolate --no-normalize --no-path-resolution \
        > "$UPDATE_BACKUP_DIR/docker-compose.next.yml" || die "无法保留现有 Compose 配置"
    [ -s "$UPDATE_BACKUP_DIR/docker-compose.next.yml" ] || die "生成的 Compose 配置为空"
    chmod 0600 "$UPDATE_BACKUP_DIR/docker-compose.next.yml"
    mv -f -- "$UPDATE_BACKUP_DIR/docker-compose.next.yml" "$EXTERNAL_COMPOSE_FILE"
}

show_diagnostics() {
    echo "-> 最近的 Compose 状态："
    compose ps || true
    echo "-> 最近的控制面日志："
    compose logs --tail=80 control-plane || true
    if [ "$MODE" = "bundled" ]; then
        echo "-> 最近的 PostgreSQL 日志："
        compose logs --tail=40 postgres || true
    fi
    echo "-> 最近的网页服务日志："
    compose logs --tail=40 qcontrol-web || true
}

start_services() {
    echo "-> 校验 Docker Compose 配置"
    compose config --quiet || die "Docker Compose 配置无效，请检查 .env 和连接参数"
    resolve_bundled_image_refs
    if [ "$BUNDLED_CONTROL_IMAGE_REF" = "ghcr.io/qimaoww/qcontrol-plane:local" ]; then
        echo "-> 构建并启动本地 Docker 镜像"
        if ! compose up -d --build; then
            show_diagnostics
            die "Docker Compose 启动失败"
        fi
    elif [ "$ACTION" = "update" ]; then
        echo "-> 使用已拉取的应用镜像启动 Docker Compose"
        if ! write_update_compose_override | compose -f - up -d --no-build --pull never; then
            show_diagnostics
            die "Docker Compose 启动失败；请检查镜像拉取和 Compose 配置"
        fi
    else
        echo "-> 拉取 GHCR 镜像并启动 Docker Compose"
        if ! compose pull || ! compose up -d; then
            show_diagnostics
            die "Docker Compose 启动失败；请确认已登录 ghcr.io 且镜像标签存在"
        fi
    fi
}

start_external_services() {
    echo "-> 拉取外部 PostgreSQL 部署的 latest 应用镜像"
    docker pull ghcr.io/qimaoww/qcontrol-plane:latest
    docker pull ghcr.io/qimaoww/qcontrol-web:latest
    echo "-> 校验 Docker Compose 配置"
    compose config --quiet || die "Docker Compose 配置无效，请检查 .env 和连接参数"
    if ! start_application_services "${COMPOSE_ARGS[@]}"; then
        show_diagnostics
        die "Docker Compose 启动失败；请确认已登录 ghcr.io 且镜像可用"
    fi
}

# Compose still waits on dependencies between services explicitly named in the
# same `up`, even with --no-deps. Start one at a time so our readiness budget
# governs migrations and rollback also starts the web container after recovery.
start_application_services() {
    run_compose "$@" up -d --force-recreate --no-build --pull never --no-deps control-plane || return 1
    echo "-> 等待控制面就绪（最多 ${READY_TIMEOUT} 秒；数据库迁移期间尚未提供服务）"
    wait_control_plane_ready "$READY_TIMEOUT" "$@" || return 1
    run_compose "$@" up -d --force-recreate --no-build --pull never --no-deps qcontrol-web
}

wait_control_plane_ready() {
    local timeout="$1" deadline container
    shift
    deadline=$(( $(date +%s) + timeout ))
    container="$(run_compose "$@" ps -q control-plane)" || return 1
    [ -n "$container" ] || return 1
    while :; do
        if docker exec "$container" wget -q -T 3 -O - http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
            return 0
        fi
        if [ "$(date +%s)" -ge "$deadline" ]; then
            echo "错误：控制面未在 ${timeout} 秒内就绪；请检查数据库迁移、锁等待及 QCH_DATABASE_STARTUP_TIMEOUT" >&2
            return 1
        fi
        sleep 2
    done
}

wait_ready() {
    local url="$1" timeout="$2" deadline
    deadline=$(( $(date +%s) + timeout ))
    # Probe before consulting the clock: with a one-second timeout the deadline
    # can pass between its computation and the first loop test, which would skip
    # the only request and report a healthy deployment as unavailable.
    while :; do
        if curl -sf -m 3 "$url" >/dev/null 2>&1; then
            return 0
        fi
        [ "$(date +%s)" -lt "$deadline" ] || return 1
        sleep 2
    done
}

validate_external_update_env() {
    local qch_database_url
    [ -f "$ENV_FILE" ] || die "未找到现有部署配置：$ENV_FILE"
    qch_database_url="$(read_env_key QCH_DATABASE_URL)"
    test -n "${qch_database_url:-}" || {
        echo "缺少原外部 PostgreSQL 连接串，终止更新" >&2
        exit 1
    }

    case "$qch_database_url" in
        postgresql://*|postgres://*) ;;
        *)
            echo "外部 PostgreSQL 连接串格式错误" >&2
            exit 1
            ;;
    esac

    validate_external_config_key_source
    validate_external_compose_database_boundary
}

validate_external_compose_database_boundary() {
    [ -f "$EXTERNAL_COMPOSE_FILE" ] || die "未找到外部部署配置：$EXTERNAL_COMPOSE_FILE"
    if awk '
        /^services:[[:space:]]*$/ { in_services = 1; next }
        in_services && /^[^[:space:]]/ { in_services = 0 }
        in_services && /^  postgres:[[:space:]]*$/ { found = 1 }
        END { exit found ? 0 : 1 }
    ' "$EXTERNAL_COMPOSE_FILE" || \
        grep -Eq '^[[:space:]]{4}depends_on:[[:space:]]*postgres|^[[:space:]]{6}postgres:[[:space:]]*$|postgres-data|127\.0\.0\.1:5432' "$EXTERNAL_COMPOSE_FILE"; then
        die "外部 PostgreSQL Compose 包含本地数据库服务、数据卷、依赖或连接地址"
    fi
}

validate_external_config_key_source() {
    local config_key config_source previous_source
    config_key="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    if [ -n "$config_key" ]; then
        validate_secret QCH_CONFIG_ENCRYPTION_KEY "$config_key"
        return 0
    fi

    config_source="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE)"
    previous_source="$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE)"
    [ -n "$config_source" ] && [ -n "$previous_source" ] && [ -f "$SECRET_COMPOSE_FILE" ] || \
        die "缺少原 QCH_CONFIG_ENCRYPTION_KEY 或既有密钥文件组，终止更新"
    case "$config_source" in
        /*) ;;
        *) config_source="$WORK_DIR/$config_source" ;;
    esac
    case "$previous_source" in
        /*) ;;
        *) previous_source="$WORK_DIR/$previous_source" ;;
    esac
    [ -f "$config_source" ] && [ -f "$previous_source" ] || \
        die "原配置加密密钥文件组文件不完整，终止更新"
}

validate_external_network() {
    local network
    network="$(read_env_key QCH_DOCKER_NETWORK)"
    [ -n "$network" ] || return 0
    case "$network" in
        [A-Za-z0-9]*) ;;
        *) die "Docker 网络名称必须以字母或数字开头" ;;
    esac
    case "$network" in
        *[!A-Za-z0-9_.-]*) die "Docker 网络名称只能包含字母、数字、点、下划线和短横线" ;;
    esac
    docker network inspect -- "$network" >/dev/null 2>&1 || \
        die "自定义 Docker 网络不存在：$network"
}

update_container_id() {
    local service="$1" ids count
    ids="$(compose ps -q "$service")"
    count="$(printf '%s\n' "$ids" | awk 'NF { count++ } END { print count + 0 }')"
    [ "$count" -eq 1 ] || die "更新前必须有且仅有一个正在运行的 $(ui_service_name "$service") 容器"
    printf '%s' "$ids"
}

current_update_image_id() {
    local service="$1" identity="${2:-id}" ids count image_id metadata
    if [ "$MODE" = bundled ]; then
        # Older bundled installs may not have the generated secret override yet.
        ids="$(run_compose -f "$REPO_ROOT/docker-compose.yml" ps -q "$service")" || \
            die "无法读取 $(ui_service_name "$service") 当前容器"
    else
        ids="$(compose ps -q "$service")" || die "无法读取 $(ui_service_name "$service") 当前容器"
    fi
    count="$(printf '%s\n' "$ids" | awk 'NF { count++ } END { print count + 0 }')"
    [ "$count" -le 1 ] || die "更新前发现多个正在运行的 $(ui_service_name "$service") 容器"
    [ "$count" -eq 1 ] || return 0
    metadata="$(docker inspect --format '{{json .}}' "$ids")" || die "无法读取 $(ui_service_name "$service") 当前镜像"
    # With the containerd store .Image may identify an index. The container's
    # descriptor records the exact platform manifest that was actually started.
    image_id="$(jq -er --arg kind "$identity" '
        (if $kind == "identity" then .ImageManifestDescriptor.digest // .Image else .Image end) |
        strings | select(test("^sha256:[0-9a-f]{64}$"))' <<< "$metadata")" || die "当前容器镜像身份无效"
    [ -n "$image_id" ] || die "无法读取 $(ui_service_name "$service") 当前镜像"
    printf '%s' "$image_id"
}

update_image_version() {
    local image_id="$1" version short_id
    if [ -z "$image_id" ]; then
        printf '未运行'
        return 0
    fi
    version="$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$image_id")" || \
        die "无法读取镜像版本：$image_id"
    case "$version" in
        ""|"<no value>")
            short_id="${image_id#sha256:}"
            printf '镜像 %s' "${short_id:0:12}"
            ;;
        *)
            if [[ "$version" =~ ^[0-9a-fA-F]{40}$ ]]; then
                version="${version:0:12}"
            fi
            printf '%s' "$version"
            ;;
    esac
}

show_current_application_versions() {
    local control_version web_version
    control_version="$(update_image_version "$1")" || die "无法读取控制面当前版本"
    web_version="$(update_image_version "$2")" || die "无法读取网页服务当前版本"
    ui_section "当前运行版本"
    ui_service_row control-plane "当前版本：$control_version"
    ui_service_row qcontrol-web "当前版本：$web_version"
}

show_update_targets() {
    ui_section "目标镜像"
    ui_service_row control-plane "目标标签：${1##*:}"
    ui_service_row qcontrol-web "目标标签：${2##*:}"
    if [ -n "${3:-}" ]; then
        ui_service_row PostgreSQL "目标标签：${3##*:}"
    fi
}

show_target_image_version() {
    local service="$1" current="$2" target="$3" version="$4" status
    if [ -z "$current" ]; then
        status="待启动"
    elif [ "$current" = "$target" ]; then
        status="无变化"
    else
        status="有更新"
    fi
    ui_service_row "$service" "目标版本：$version [$status]"
}

require_update_tools() {
    command -v jq >/dev/null 2>&1 || die "远程镜像检查需要 jq，请先安装 jq"
    docker buildx version >/dev/null 2>&1 || die "远程镜像检查需要 Docker Buildx，请先安装 Buildx 插件"
}

normalize_update_platform() {
    local platform="$1"
    case "$platform" in
        linux/x86_64) platform=linux/amd64 ;;
        linux/aarch64|linux/arm64/v8) platform=linux/arm64 ;;
        linux/armhf|linux/armv7l) platform=linux/arm/v7 ;;
        linux/armel|linux/armv6l) platform=linux/arm/v6 ;;
    esac
    [[ "$platform" =~ ^linux/[a-z0-9_]+(/[a-zA-Z0-9_.-]+)?$ ]] || die "无法确定部署平台：$platform"
    printf '%s' "$platform"
}

# No layer downloads. Hash the exact manifest bytes, then keep every subsequent
# registry request and pull on that immutable reference, including single images.
remote_update_image() (
    local ref="$1" platform="$2" manifest_file repository digest pinned metadata config_id actual_platform version indexed=false
    umask 077
    manifest_file="$(mktemp)" || die "无法创建镜像检查临时文件"
    trap 'rm -f -- "$manifest_file"' EXIT
    trap 'exit 130' HUP INT TERM
    docker buildx imagetools inspect --raw "$ref" > "$manifest_file" || die "远程镜像检查失败：$ref"
    repository="${ref%%@*}"
    # Strip a tag, retaining a registry's optional port.
    if [[ "${repository##*/}" == *:* ]]; then repository="${repository%:*}"; fi
    if jq -e '.manifests | type == "array"' "$manifest_file" >/dev/null 2>&1; then
        digest="$(jq -er --arg platform "$platform" '
            [.manifests[] | select(.platform != null) |
             select((.platform.os + "/" + .platform.architecture +
                (if (.platform.variant // "") == "" or
                    (.platform.architecture == "arm64" and .platform.variant == "v8")
                 then "" else "/" + .platform.variant end)) == $platform)] |
            if length == 1 then .[0].digest else error("平台缺失或不唯一") end
        ' "$manifest_file")" || die "目标镜像缺少唯一的 $platform 平台：$ref"
        [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "目标镜像摘要无效：$ref"
        indexed=true
        pinned="$repository@$digest"
        docker buildx imagetools inspect --raw "$pinned" > "$manifest_file" || die "远程平台镜像检查失败：$pinned"
        [ "sha256:$(openssl dgst -sha256 "$manifest_file" | awk '{print $NF}')" = "$digest" ] || die "远程平台镜像摘要不匹配：$ref"
    else
        digest="sha256:$(openssl dgst -sha256 "$manifest_file" | awk '{print $NF}')"
        pinned="$repository@$digest"
    fi
    config_id="$(jq -er 'select(.schemaVersion == 2) | .config.digest | select(test("^sha256:[0-9a-f]{64}$"))' "$manifest_file")" || \
        die "目标镜像身份信息不完整：$ref"
    metadata="$(docker buildx imagetools inspect --format '{{json .Image}}' "$pinned")" || die "无法读取目标镜像版本和平台：$ref"
    actual_platform="$(jq -er '.os + "/" + .architecture + (if (.variant // "") == "" then "" else "/" + .variant end)' <<< "$metadata")" || \
        die "目标镜像平台信息不完整：$ref"
    actual_platform="$(normalize_update_platform "$actual_platform")" || exit 1
    # Older ARM image configs omit the variant carried by their index entry.
    if [ "$indexed" = true ] && [[ "$platform" == "$actual_platform/"* ]]; then
        actual_platform="$platform"
    fi
    [ "$platform" = "$actual_platform" ] || die "目标镜像平台不匹配：$ref（需要 $platform，实际 $actual_platform）"
    version="$(jq -er '.config.Labels["org.opencontainers.image.version"] // "" | strings | select(test("[[:cntrl:]]") | not)' <<< "$metadata")" || die "目标镜像版本信息无效：$ref"
    if [ -z "$version" ]; then version="镜像 ${config_id:7:12}"; fi
    if [[ "$version" =~ ^[0-9a-fA-F]{40}$ ]]; then version="${version:0:12}"; fi
    jq -cn --arg id "$config_id" --arg ref "$pinned" --arg version "$version" '{id:$id, ref:$ref, version:$version}'
)

prepare_update_plan() {
    local config default_platform service platform ref current metadata local_id i
    local -a config_args
    require_update_tools
    if [ "$MODE" = bundled ]; then
        config_args=(-f "$REPO_ROOT/docker-compose.yml")
        UPDATE_IMAGE_SERVICES=(control-plane qcontrol-web postgres)
    else
        config_args=("${COMPOSE_ARGS[@]}")
        UPDATE_IMAGE_SERVICES=(control-plane qcontrol-web)
    fi
    # Compose also reads DOCKER_DEFAULT_PLATFORM from the project's .env. Older
    # config output omits it from services; resolve it through an extension so
    # quoting, interpolation and shell precedence remain Compose's responsibility.
    config="$(run_compose "${config_args[@]}" -f - config --format json <<'YAML'
x-qch-update-default-platform: ${DOCKER_DEFAULT_PLATFORM:-}
YAML
    )" || die "无法解析部署镜像和平台配置"
    default_platform="$(jq -er '."x-qch-update-default-platform" // "" | strings' <<< "$config")" || die "无法解析默认部署平台"
    if [ -z "$default_platform" ]; then
        default_platform="$(docker info --format '{{.OSType}}/{{.Architecture}}')" || die "无法读取 Docker Engine 平台"
    fi
    UPDATE_IMAGE_REFS=() UPDATE_IMAGE_PLATFORMS=() UPDATE_IMAGE_CURRENT=()
    UPDATE_IMAGE_TARGETS=() UPDATE_IMAGE_PINNED=() UPDATE_IMAGE_VERSIONS=() UPDATE_IMAGE_LOCAL_IDS=()
    UPDATE_IMAGES_CHANGED=false
    ui_section "正在检查远程镜像"
    for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
        service="${UPDATE_IMAGE_SERVICES[$i]}"
        platform="$(jq -er --arg s "$service" --arg p "$default_platform" '.services[$s] | .platform // $p | strings | select(length > 0)' <<< "$config")" || die "无法读取 $(ui_service_name "$service") 部署平台"
        platform="$(normalize_update_platform "$platform")" || die "无法确定 $(ui_service_name "$service") 部署平台"
        if [ "$MODE" = external ]; then
            case "$service" in
                control-plane) ref=ghcr.io/qimaoww/qcontrol-plane:latest ;;
                qcontrol-web) ref=ghcr.io/qimaoww/qcontrol-web:latest ;;
            esac
        else
            ref="$(jq -er --arg s "$service" '.services[$s].image | strings | select(length > 0)' <<< "$config")" || die "无法读取 $(ui_service_name "$service") 目标镜像"
        fi
        local_id="$(current_update_image_id "$service")" || die "无法读取 $(ui_service_name "$service") 当前镜像"
        current="$(current_update_image_id "$service" identity)" || die "无法读取 $(ui_service_name "$service") 当前平台镜像"
        metadata="$(remote_update_image "$ref" "$platform")" || die "$(ui_service_name "$service") 远程检查失败，已终止更新"
        # Classic storage uses config IDs; containerd uses manifest descriptors.
        # Normalize both to the checked config identity for comparison/display.
        if [ "$current" = "$(jq -er '.ref | split("@") | .[1]' <<< "$metadata")" ]; then
            current="$(jq -er .id <<< "$metadata")"
        fi
        UPDATE_IMAGE_REFS+=("$ref") UPDATE_IMAGE_PLATFORMS+=("$platform") UPDATE_IMAGE_CURRENT+=("$current")
        UPDATE_IMAGE_LOCAL_IDS+=("$local_id")
        UPDATE_IMAGE_TARGETS+=("$(jq -er .id <<< "$metadata")")
        UPDATE_IMAGE_PINNED+=("$(jq -er .ref <<< "$metadata")")
        UPDATE_IMAGE_VERSIONS+=("$(jq -er .version <<< "$metadata")")
        [ "$current" = "${UPDATE_IMAGE_TARGETS[$i]}" ] || UPDATE_IMAGES_CHANGED=true
    done
    ui_section "镜像对比结果"
    for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
        show_target_image_version "${UPDATE_IMAGE_SERVICES[$i]}" "${UPDATE_IMAGE_CURRENT[$i]}" \
            "${UPDATE_IMAGE_TARGETS[$i]}" "${UPDATE_IMAGE_VERSIONS[$i]}"
    done
}

pull_update_images() {
    local i actual metadata actual_platform
    for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
        [ "${UPDATE_IMAGE_CURRENT[$i]}" != "${UPDATE_IMAGE_TARGETS[$i]}" ] || continue
        ui_section "拉取镜像：${UPDATE_IMAGE_REFS[$i]}"
        docker pull --platform "${UPDATE_IMAGE_PLATFORMS[$i]}" "${UPDATE_IMAGE_PINNED[$i]}" || die "镜像拉取失败：${UPDATE_IMAGE_REFS[$i]}"
        metadata="$(docker image inspect --format '{{json .}}' "${UPDATE_IMAGE_PINNED[$i]}")" || die "无法校验已拉取的镜像"
        actual="$(jq -er '.Id | strings | select(test("^sha256:[0-9a-f]{64}$"))' <<< "$metadata")" || die "已拉取的镜像身份无效"
        [ "$actual" = "${UPDATE_IMAGE_TARGETS[$i]}" ] || \
            [ "$actual" = "${UPDATE_IMAGE_PINNED[$i]##*@}" ] || die "已拉取的镜像与检查结果不一致，已终止更新"
        actual_platform="$(jq -er '.Os + "/" + .Architecture + (if (.Variant // "") == "" then "" else "/" + .Variant end)' <<< "$metadata")" || die "已拉取的镜像平台信息无效"
        actual_platform="$(normalize_update_platform "$actual_platform")" || die "已拉取的镜像平台无效"
        # Legacy ARM configs omit the variant already verified in the registry.
        if [[ "${UPDATE_IMAGE_PLATFORMS[$i]}" == "$actual_platform/"* ]]; then
            actual_platform="${UPDATE_IMAGE_PLATFORMS[$i]}"
        fi
        [ "$actual_platform" = "${UPDATE_IMAGE_PLATFORMS[$i]}" ] || die "已拉取的镜像平台与检查结果不一致，已终止更新"
        UPDATE_IMAGE_LOCAL_IDS[$i]="$actual"
    done
}

write_update_compose_override() {
    local i
    printf 'services:\n'
    for i in "${!UPDATE_IMAGE_SERVICES[@]}"; do
        printf '  %s:\n    image: %s\n    platform: %s\n' "${UPDATE_IMAGE_SERVICES[$i]}" \
            "${UPDATE_IMAGE_LOCAL_IDS[$i]}" "${UPDATE_IMAGE_PLATFORMS[$i]}"
    done
}

capture_update_image() {
    local service="$1" container_id image_id image_ref backup_tag
    container_id="$(update_container_id "$service")"
    image_id="$(docker inspect --format '{{.Image}}' "$container_id")"
    image_ref="$(docker inspect --format '{{.Config.Image}}' "$container_id")"
    [ -n "$image_id" ] && [ -n "$image_ref" ] || die "无法读取 $(ui_service_name "$service") 当前镜像"
    backup_tag="qcontrolhub-update-rollback/$service:$(date +%Y%m%d%H%M%S)-$$"
    docker image tag "$image_id" "$backup_tag" || die "无法保存 $(ui_service_name "$service") 当前镜像"
    case "$service" in
        control-plane)
            UPDATE_CONTROL_IMAGE="$image_id"
            UPDATE_CONTROL_REF="$image_ref"
            UPDATE_CONTROL_BACKUP_TAG="$backup_tag"
            ;;
        qcontrol-web)
            UPDATE_WEB_IMAGE="$image_id"
            UPDATE_WEB_REF="$image_ref"
            UPDATE_WEB_BACKUP_TAG="$backup_tag"
            ;;
    esac
}

begin_external_update() {
    [ -f "$EXTERNAL_COMPOSE_FILE" ] || die "未找到外部部署配置：$EXTERNAL_COMPOSE_FILE"
    [ ! -L "$ENV_FILE" ] || die ".env 不能是符号链接：$ENV_FILE"
    [ ! -L "$EXTERNAL_COMPOSE_FILE" ] || die "外部 Compose 不能是符号链接：$EXTERNAL_COMPOSE_FILE"

    UPDATE_SERVICES_CHANGED=false
    UPDATE_HAD_SECRET_COMPOSE=false
    UPDATE_CONTROL_IMAGE=""
    UPDATE_CONTROL_REF=""
    UPDATE_CONTROL_BACKUP_TAG=""
    UPDATE_WEB_IMAGE=""
    UPDATE_WEB_REF=""
    UPDATE_WEB_BACKUP_TAG=""
    umask 077
    UPDATE_BACKUP_DIR="$(mktemp -d "$WORK_DIR/.qcontrolhub-update.XXXXXX")"
    cp -p -- "$ENV_FILE" "$UPDATE_BACKUP_DIR/.env"
    cp -p -- "$EXTERNAL_COMPOSE_FILE" "$UPDATE_BACKUP_DIR/docker-compose.external.yml"
    if [ -z "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)" ] && [ -f "$SECRET_COMPOSE_FILE" ]; then
        cp -p -- "$SECRET_COMPOSE_FILE" "$UPDATE_BACKUP_DIR/docker-compose.secrets.yml"
        UPDATE_HAD_SECRET_COMPOSE=true
    fi
    UPDATE_ROLLBACK_ARMED=true
    capture_update_image control-plane
    capture_update_image qcontrol-web
}

restore_update_image_ref() {
    local image_id="$1" image_ref="$2" backup_tag="$3"
    if [ -z "$image_id" ] || [ -z "$image_ref" ]; then
        printf '%s' "$backup_tag"
        return 0
    fi
    case "$image_ref" in
        *@sha256:*|sha256:*) printf '%s' "$backup_tag" ;;
        *)
            if docker image tag "$image_id" "$image_ref"; then
                printf '%s' "$image_ref"
            else
                printf '%s' "$backup_tag"
            fi
            ;;
    esac
}

cleanup_external_update_backup() {
    [ -z "$UPDATE_CONTROL_BACKUP_TAG" ] || docker image rm "$UPDATE_CONTROL_BACKUP_TAG" >/dev/null 2>&1 || true
    [ -z "$UPDATE_WEB_BACKUP_TAG" ] || docker image rm "$UPDATE_WEB_BACKUP_TAG" >/dev/null 2>&1 || true
    if [ -n "$UPDATE_BACKUP_DIR" ] && [ -d "$UPDATE_BACKUP_DIR" ]; then
        rm -f -- \
            "$UPDATE_BACKUP_DIR/.env" \
            "$UPDATE_BACKUP_DIR/docker-compose.external.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.secrets.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.update.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.next.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.checked.yml" \
            "$UPDATE_BACKUP_DIR/docker-compose.rollback.yml"
        rmdir -- "$UPDATE_BACKUP_DIR" 2>/dev/null || true
    fi
    UPDATE_BACKUP_DIR=""
}

rollback_external_update() {
    local -a rollback_args
    [ "$UPDATE_ROLLBACK_ARMED" = true ] || return 0
    UPDATE_ROLLBACK_ARMED=false
    echo "-> 更新失败，正在恢复旧配置和旧镜像" >&2

    if ! cp -p -- "$UPDATE_BACKUP_DIR/.env" "$ENV_FILE" || \
        ! cp -p -- "$UPDATE_BACKUP_DIR/docker-compose.external.yml" "$EXTERNAL_COMPOSE_FILE"; then
        echo "错误：旧配置恢复失败；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
        return 1
    fi
    if [ "$UPDATE_HAD_SECRET_COMPOSE" = true ]; then
        if ! cp -p -- "$UPDATE_BACKUP_DIR/docker-compose.secrets.yml" "$SECRET_COMPOSE_FILE"; then
            echo "错误：旧密钥配置恢复失败；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
            return 1
        fi
    fi

    restore_update_image_ref "$UPDATE_CONTROL_IMAGE" "$UPDATE_CONTROL_REF" "$UPDATE_CONTROL_BACKUP_TAG" >/dev/null
    restore_update_image_ref "$UPDATE_WEB_IMAGE" "$UPDATE_WEB_REF" "$UPDATE_WEB_BACKUP_TAG" >/dev/null
    if [ "$UPDATE_SERVICES_CHANGED" = true ]; then
        # Immutable IDs plus --pull never are required even when the old
        # Compose used latest with pull_policy: always.
        if ! cat > "$UPDATE_BACKUP_DIR/docker-compose.rollback.yml" <<YAML
services:
  control-plane:
    image: $UPDATE_CONTROL_IMAGE
  qcontrol-web:
    image: $UPDATE_WEB_IMAGE
YAML
        then
            echo "错误：无法写入回滚镜像配置；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
            return 1
        fi
        rollback_args=(-f "$EXTERNAL_COMPOSE_FILE")
        if [ "$UPDATE_HAD_SECRET_COMPOSE" = true ]; then
            rollback_args+=(-f "$SECRET_COMPOSE_FILE")
        fi
        rollback_args+=(-f "$UPDATE_BACKUP_DIR/docker-compose.rollback.yml")
        if ! run_compose "${rollback_args[@]}" config --quiet || \
            ! start_application_services "${rollback_args[@]}"; then
            echo "错误：旧部署恢复失败；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
            return 1
        fi
        if ! check_external_endpoints "$(local_panel_url)"; then
            echo "错误：原版本容器已恢复，但健康检查仍未通过；回滚材料保留在 $UPDATE_BACKUP_DIR" >&2
            return 1
        fi
    fi

    cleanup_external_update_backup
    echo "-> 已恢复旧配置，原版本容器继续运行" >&2
}

external_update_exit_handler() {
    local status=$?
    trap - EXIT HUP INT TERM
    if [ "$status" -ne 0 ] && [ "$UPDATE_ROLLBACK_ARMED" = true ]; then
        rollback_external_update || status=1
    fi
    exit "$status"
}

check_external_endpoints() {
    local panel_url="$1"
    echo "-> 检查 $panel_url/healthz"
    wait_ready "$panel_url/healthz" "$READY_TIMEOUT" || return 1
    echo "-> 检查 $panel_url/readyz"
    wait_ready "$panel_url/readyz" "$READY_TIMEOUT"
}

update_external_services() (
    local panel_url
    trap external_update_exit_handler EXIT
    trap 'exit 130' HUP INT TERM
    validate_external_network
    panel_url="$(local_panel_url)"
    check_external_endpoints "$panel_url" || die "更新前原部署未通过 healthz 和 readyz 检查"
    begin_external_update

    show_current_application_versions "$UPDATE_CONTROL_IMAGE" "$UPDATE_WEB_IMAGE"
    show_update_targets ghcr.io/qimaoww/qcontrol-plane:latest ghcr.io/qimaoww/qcontrol-web:latest
    prepare_update_plan
    if [ "$UPDATE_IMAGES_CHANGED" = false ]; then
        show_no_update_result
        UPDATE_ROLLBACK_ARMED=false
        trap - EXIT HUP INT TERM
        cleanup_external_update_backup
        return 0
    fi
    pull_update_images

    echo "-> 检测到镜像变化，开始更新"
    echo "-> 保留现有拓扑并更新 $EXTERNAL_COMPOSE_FILE 中的应用配置"
    prepare_external_update_compose
    cmp -s "$UPDATE_BACKUP_DIR/.env" "$ENV_FILE" || die "更新过程改写了 .env，已终止"

    compose config --quiet || die "Docker Compose 配置校验失败"
    UPDATE_SERVICES_CHANGED=true
    write_update_compose_override > "$UPDATE_BACKUP_DIR/docker-compose.checked.yml"
    if ! start_application_services "${COMPOSE_ARGS[@]}" -f "$UPDATE_BACKUP_DIR/docker-compose.checked.yml"; then
        show_diagnostics
        die "使用新镜像重建应用容器失败"
    fi

    if ! check_external_endpoints "$panel_url"; then
        show_diagnostics
        die "应用未在 ${READY_TIMEOUT} 秒内通过 healthz 和 readyz 检查"
    fi
    cmp -s "$UPDATE_BACKUP_DIR/.env" "$ENV_FILE" || die "更新过程改写了 .env，已终止"

    UPDATE_ROLLBACK_ARMED=false
    trap - EXIT HUP INT TERM
    cleanup_external_update_backup
    show_result "更新完成" "$panel_url" "docker compose -p qcontrolhub --project-directory $WORK_DIR --env-file $ENV_FILE -f $EXTERNAL_COMPOSE_FILE down"
)

prepare_default_agent_engines() {
    local engine answer selection seen=","
    DEFAULT_AGENT_ENGINES="$(read_env_key QCH_DEFAULT_AGENT_ENGINES)"
    # Existing installations keep the database-owned choice on upgrade.
    [ ! -f "$ENV_FILE" ] || return 0
    selection="${QCH_DEFAULT_AGENT_ENGINES:-mihomo,xray,sing-box,ss-rust}"
    if [ -z "${QCH_DEFAULT_AGENT_ENGINES:-}" ] && [ -t 0 ]; then
        echo ""
        echo "选择新 Agent 默认启用的内核能力（不自动安装内核）。"
        echo "后续可在系统设置修改默认值，节点设置可单独开关。"
        selection=""
        for engine in mihomo xray sing-box ss-rust; do
            while true; do
                read -r -p "默认启用 $engine？[Y/n] " answer
                case "$answer" in
                    ""|y|Y|yes|YES) selection="${selection:+$selection,}$engine"; break ;;
                    n|N|no|NO) break ;;
                    *) echo "请输入 y 或 n。" ;;
                esac
            done
        done
        selection="${selection:-none}"
    fi
    if [ "$selection" != none ]; then
        case "$selection" in ,*|*,|*,,*) die "QCH_DEFAULT_AGENT_ENGINES 含空内核名称" ;; esac
        local -a selected_engines
        IFS=',' read -r -a selected_engines <<< "$selection"
        for engine in "${selected_engines[@]}"; do
            case "$engine" in mihomo|xray|sing-box|ss-rust) ;; *) die "无效内核能力：$engine" ;; esac
            case "$seen" in *",$engine,"*) die "重复内核能力：$engine" ;; esac
            seen="$seen$engine,"
        done
    fi
    DEFAULT_AGENT_ENGINES="$selection"
}

prepare_bundled_env() {
    prepare_default_agent_engines
    local postgres_password webhook_secret
    local behind_proxy allow_http allow_database cors_origins bind_address port image_tag version
    local proxy_subnet proxy_gateway web_proxy_address control_plane_proxy_address trusted_proxy_cidrs

    if [ "$FORCE" = true ]; then
        backup_env
    fi

    postgres_password="$(read_env_key POSTGRES_PASSWORD)"
    [ -n "$postgres_password" ] || postgres_password="$(random_hex)"

    prepare_admin_token
    webhook_secret="$(read_env_key QCH_WEBHOOK_SECRET)"
    if [ "$FORCE" = true ] || [ -z "$webhook_secret" ]; then
        webhook_secret="$(random_hex)"
    fi
    prepare_config_keyring

    behind_proxy="$(read_env_key QCH_BEHIND_TLS_PROXY)"; [ -n "$behind_proxy" ] || behind_proxy=true
    allow_http="$(read_env_key QCH_ALLOW_INSECURE_HTTP)"; [ -n "$allow_http" ] || allow_http=false
    allow_database="$(read_env_key QCH_ALLOW_INSECURE_DATABASE)"; [ -n "$allow_database" ] || allow_database=true
    cors_origins="$(read_env_key QCH_CORS_ORIGINS)"
    bind_address="$(read_env_key QCH_BIND_ADDRESS)"; [ -n "$bind_address" ] || bind_address=127.0.0.1
    port="$(read_env_key QCH_PORT)"; [ -n "$port" ] || port=8080
    image_tag="$(read_env_key QCH_IMAGE_TAG)"; [ -n "$image_tag" ] || image_tag=latest
    version="$(read_env_key VERSION)"; [ -n "$version" ] || version=dev
    proxy_subnet="$(read_env_key QCH_CONTROL_PROXY_SUBNET)"; [ -n "$proxy_subnet" ] || proxy_subnet=172.30.254.0/24
    proxy_gateway="$(read_env_key QCH_CONTROL_PROXY_GATEWAY)"; [ -n "$proxy_gateway" ] || proxy_gateway=172.30.254.1
    web_proxy_address="$(read_env_key QCH_WEB_PROXY_ADDRESS)"; [ -n "$web_proxy_address" ] || web_proxy_address=172.30.254.2
    control_plane_proxy_address="$(read_env_key QCH_CONTROL_PLANE_PROXY_ADDRESS)"; [ -n "$control_plane_proxy_address" ] || control_plane_proxy_address=172.30.254.3
    trusted_proxy_cidrs="$(read_env_key QCH_TRUSTED_PROXY_CIDRS)"
    trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$web_proxy_address/32")"
    trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$proxy_gateway/32")"

    local postgres_db postgres_user postgres_port
    postgres_db="$(read_env_key POSTGRES_DB)"; [ -n "$postgres_db" ] || postgres_db=qcontrolhub
    postgres_user="$(read_env_key POSTGRES_USER)"; [ -n "$postgres_user" ] || postgres_user=qcontrolhub
    postgres_port="$(read_env_key POSTGRES_PORT)"; [ -n "$postgres_port" ] || postgres_port=5432

    update_env_file \
        "POSTGRES_DB=$postgres_db" \
        "QCH_DEFAULT_AGENT_ENGINES=$DEFAULT_AGENT_ENGINES" \
        "POSTGRES_USER=$postgres_user" \
        "POSTGRES_PASSWORD=$postgres_password" \
        "POSTGRES_PORT=$postgres_port" \
        "QCH_ADMIN_TOKEN=" \
        "QCH_ADMIN_TOKEN_SHA256=$ADMIN_TOKEN_DIGEST" \
        "QCH_WEBHOOK_SECRET=$webhook_secret" \
        "QCH_CONFIG_ENCRYPTION_KEY=" \
        "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=" \
        "QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE=.secrets/config-encryption-key" \
        "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE=.secrets/config-encryption-previous-keys" \
        "QCH_BEHIND_TLS_PROXY=$behind_proxy" \
        "QCH_ALLOW_INSECURE_HTTP=$allow_http" \
        "QCH_ALLOW_INSECURE_DATABASE=$allow_database" \
        "QCH_CORS_ORIGINS=$cors_origins" \
        "QCH_CONTROL_PROXY_SUBNET=$proxy_subnet" \
        "QCH_CONTROL_PROXY_GATEWAY=$proxy_gateway" \
        "QCH_WEB_PROXY_ADDRESS=$web_proxy_address" \
        "QCH_CONTROL_PLANE_PROXY_ADDRESS=$control_plane_proxy_address" \
        "QCH_TRUSTED_PROXY_CIDRS=$trusted_proxy_cidrs" \
        "QCH_BIND_ADDRESS=$bind_address" \
        "QCH_PORT=$port" \
        "QCH_IMAGE_TAG=$image_tag" \
        "VERSION=$version"
}

prepare_external_env() {
    prepare_default_agent_engines
    local db_url webhook_secret env_existed existing_admin_token existing_admin_digest
    local docker_network existing_network
    local behind_proxy allow_http allow_database cors_origins bind_address port
    local proxy_subnet proxy_gateway web_proxy_address control_plane_proxy_address trusted_proxy_cidrs
    local -a secret_sources=()

    [ -f "$ENV_FILE" ] && env_existed=true || env_existed=false
    db_url="$(read_env_key QCH_DATABASE_URL)"
    if [ -n "$DATABASE_URL" ]; then
        if [ -n "$db_url" ] && [ "$DATABASE_URL" != "$db_url" ]; then
            die "拒绝覆盖现有 QCH_DATABASE_URL；更新数据库连接请先人工确认并修改 .env"
        fi
        db_url="$DATABASE_URL"
    fi
    if [ -z "$db_url" ]; then
        [ -t 0 ] || die "缺少 DATABASE_URL；非交互模式请使用 -d 传入"
        echo ""
        echo "请输入外部 PostgreSQL 连接串"
        echo "示例：postgresql://user:pass@db.example.com:5432/qcontrolhub?sslmode=verify-full"
        echo ""
        read -r -p "DATABASE_URL: " db_url
    fi
    [ -n "$db_url" ] || die "DATABASE_URL 不能为空"
    validate_database_url "$db_url"

    existing_network="$(read_env_key QCH_DOCKER_NETWORK)"
    docker_network="$existing_network"
    if [ -n "$DOCKER_NETWORK" ]; then
        if [ -n "$existing_network" ] && [ "$DOCKER_NETWORK" != "$existing_network" ]; then
            die "拒绝覆盖现有 QCH_DOCKER_NETWORK；请先人工确认并修改 .env"
        fi
        docker_network="$DOCKER_NETWORK"
    elif [ "$env_existed" = false ] && [ -t 0 ]; then
        echo ""
        echo "请输入已有 Docker 网络名称（直接回车使用 Compose 默认网络）"
        read -r docker_network
    fi
    case "$docker_network" in
        "") ;;
        [A-Za-z0-9]* )
            case "$docker_network" in
                *[!A-Za-z0-9_.-]*) die "Docker 网络名称只能包含字母、数字、点、下划线和短横线" ;;
            esac
            ;;
        *) die "Docker 网络名称必须以字母或数字开头" ;;
    esac

    if [ "$FORCE" = true ]; then
        die "外部 PostgreSQL 部署不支持 -f；不会轮换管理员令牌或配置加密密钥"
    fi

    existing_admin_token="$(read_env_key QCH_ADMIN_TOKEN)"
    existing_admin_digest="$(read_env_key QCH_ADMIN_TOKEN_SHA256)"
    if [ -n "$ADMIN_TOKEN" ] && [ -n "$existing_admin_token$existing_admin_digest" ]; then
        if [ -n "$existing_admin_token" ]; then
            [ "$ADMIN_TOKEN" = "$existing_admin_token" ] || \
                die "拒绝覆盖现有 QCH_ADMIN_TOKEN；外部部署不会轮换管理员令牌"
        else
            [ "$(sha256_hex "$ADMIN_TOKEN")" = "$(printf '%s' "$existing_admin_digest" | tr 'A-F' 'a-f')" ] || \
                die "-a 与现有 QCH_ADMIN_TOKEN_SHA256 不匹配；拒绝轮换管理员令牌"
        fi
    fi
    if [ "$env_existed" = true ] && [ -z "$existing_admin_token" ] && [ -z "$existing_admin_digest" ]; then
        die "现有外部部署缺少管理员令牌配置；拒绝自动生成并覆盖"
    fi
    prepare_admin_token
    webhook_secret="$(read_env_key QCH_WEBHOOK_SECRET)"
    if [ -z "$webhook_secret" ]; then
        webhook_secret="$(random_hex)"
    fi
    CONFIG_KEY="$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)"
    PREVIOUS_CONFIG_KEYS="$(read_env_key QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS)"
    if [ "$env_existed" = false ]; then
        # Keep the original fresh-install secret-file workflow. Existing
        # deployments retain their original key source without conversion.
        prepare_config_keyring
        CONFIG_KEY=""
        PREVIOUS_CONFIG_KEYS=""
        secret_sources=(
            "QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE=.secrets/config-encryption-key"
            "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE=.secrets/config-encryption-previous-keys"
        )
    elif [ -z "$CONFIG_KEY" ]; then
        validate_external_config_key_source
    else
        validate_secret QCH_CONFIG_ENCRYPTION_KEY "$CONFIG_KEY"
    fi

    behind_proxy="$(read_env_key QCH_BEHIND_TLS_PROXY)"; [ -n "$behind_proxy" ] || behind_proxy=true
    allow_http="$(read_env_key QCH_ALLOW_INSECURE_HTTP)"; [ -n "$allow_http" ] || allow_http=false
    allow_database="$(read_env_key QCH_ALLOW_INSECURE_DATABASE)"; [ -n "$allow_database" ] || allow_database=false
    cors_origins="$(read_env_key QCH_CORS_ORIGINS)"
    bind_address="$(read_env_key QCH_BIND_ADDRESS)"; [ -n "$bind_address" ] || bind_address=127.0.0.1
    port="$(read_env_key QCH_PORT)"; [ -n "$port" ] || port=8080
    proxy_subnet="$(read_env_key QCH_CONTROL_PROXY_SUBNET)"; [ -n "$proxy_subnet" ] || proxy_subnet=172.30.254.0/24
    proxy_gateway="$(read_env_key QCH_CONTROL_PROXY_GATEWAY)"; [ -n "$proxy_gateway" ] || proxy_gateway=172.30.254.1
    web_proxy_address="$(read_env_key QCH_WEB_PROXY_ADDRESS)"; [ -n "$web_proxy_address" ] || web_proxy_address=172.30.254.2
    control_plane_proxy_address="$(read_env_key QCH_CONTROL_PLANE_PROXY_ADDRESS)"; [ -n "$control_plane_proxy_address" ] || control_plane_proxy_address=172.30.254.3
    trusted_proxy_cidrs="$(read_env_key QCH_TRUSTED_PROXY_CIDRS)"
    if [ -z "$docker_network" ]; then
        trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$web_proxy_address/32")"
        trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$proxy_gateway/32")"
    fi

    update_env_file \
        "QCH_DATABASE_URL=$db_url" \
        "QCH_DEFAULT_AGENT_ENGINES=$DEFAULT_AGENT_ENGINES" \
        "QCH_ADMIN_TOKEN=$existing_admin_token" \
        "QCH_ADMIN_TOKEN_SHA256=$ADMIN_TOKEN_DIGEST" \
        "QCH_WEBHOOK_SECRET=$webhook_secret" \
        "QCH_CONFIG_ENCRYPTION_KEY=$CONFIG_KEY" \
        "QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS=$PREVIOUS_CONFIG_KEYS" \
        "QCH_BEHIND_TLS_PROXY=$behind_proxy" \
        "QCH_ALLOW_INSECURE_HTTP=$allow_http" \
        "QCH_ALLOW_INSECURE_DATABASE=$allow_database" \
        "QCH_CORS_ORIGINS=$cors_origins" \
        "QCH_CONTROL_PROXY_SUBNET=$proxy_subnet" \
        "QCH_CONTROL_PROXY_GATEWAY=$proxy_gateway" \
        "QCH_WEB_PROXY_ADDRESS=$web_proxy_address" \
        "QCH_CONTROL_PLANE_PROXY_ADDRESS=$control_plane_proxy_address" \
        "QCH_TRUSTED_PROXY_CIDRS=$trusted_proxy_cidrs" \
        "QCH_BIND_ADDRESS=$bind_address" \
        "QCH_PORT=$port" \
        "QCH_DOCKER_NETWORK=$docker_network" \
        "${secret_sources[@]}"
    if [ "$env_existed" = false ]; then
        write_secret_compose_override
    fi
}

# Presentation stays independent of Docker so menus can be previewed safely.
ui_text() {
    local style="$1" literal=false
    shift
    if [ "${1:-}" = --literal ]; then
        literal=true
        shift
    fi
    if [ -t 1 ] && [ "${TERM:-dumb}" != dumb ] && [ -z "${NO_COLOR:-}" ]; then
        printf '\033[%sm' "$style"
        if [ "$literal" = true ]; then printf '%s\n' "$*"; else ui_wrap "$*"; fi
        printf '\033[0m'
    else
        if [ "$literal" = true ]; then printf '%s\n' "$*"; else ui_wrap "$*"; fi
    fi
}

# Bash's printf pads bytes. Count terminal cells for the Chinese labels and
# punctuation used here; use a UTF-8 locale even when the caller uses LC_ALL=C.
ui_char_width() {
    local LC_ALL=C.UTF-8 code
    printf -v code '%d' "'$1"
    if (( (code >= 0x2e80 && code <= 0xa4cf) ||
          (code >= 0xac00 && code <= 0xd7a3) ||
          (code >= 0xf900 && code <= 0xfaff) ||
          (code >= 0xfe10 && code <= 0xfe6f) ||
          (code >= 0xff01 && code <= 0xff60) )); then
        printf 2
    else
        printf 1
    fi
}

ui_wrap() {
    local LC_ALL=C.UTF-8 text="$1" width cells=0 char size i
    width="$(ui_width)"
    for ((i=0; i<${#text}; i++)); do
        char="${text:i:1}"
        if [ "$char" = $'\n' ]; then
            printf '\n'; cells=0; continue
        fi
        size="$(ui_char_width "$char")"
        if [ "$cells" -gt 0 ] && [ "$((cells + size))" -gt "$width" ]; then
            printf '\n'; cells=0
        fi
        printf '%s' "$char"
        cells=$((cells + size))
    done
    printf '\n'
}

ui_service_name() {
    case "$1" in
        control-plane) printf '控制面' ;;
        qcontrol-web) printf '网页服务' ;;
        postgres|PostgreSQL) printf 'PostgreSQL 数据库' ;;
        *) printf '%s' "$1" ;;
    esac
}

ui_width() {
    local width="${COLUMNS:-72}"
    [[ "$width" =~ ^[1-9][0-9]{0,3}$ ]] || width=72
    [ "$width" -le 72 ] || width=72
    printf '%s' "$width"
}

ui_rule() {
    local line
    printf -v line '%*s' "$(ui_width)" ''
    ui_text 2 "${line// /-}"
}

ui_heading() {
    printf '\n'
    ui_rule
    ui_text '1;36' "  QControlHub / $1"
    ui_rule
}

ui_section() {
    printf '\n'
    ui_text 1 "  $1"
}

ui_detail() {
    ui_text 2 "  $1"
    # Preserve copyable values; the terminal can soft-wrap their visual lines.
    ui_text 0 --literal "    $2"
}

ui_service_row() {
    local LC_ALL=C.UTF-8 name cells=0 i padding
    name="$(ui_service_name "$1")"
    if [ "$(ui_width)" -lt 60 ]; then
        ui_detail "$name" "$2"
    else
        for ((i=0; i<${#name}; i++)); do
            cells=$((cells + $(ui_char_width "${name:i:1}")))
        done
        printf -v padding '%*s' "$((18 - cells))" ''
        ui_text 0 "  $name$padding $2"
    fi
}

show_action_menu() {
    ui_heading "管理菜单"
    ui_section "工作目录"
    ui_text 0 --literal "  $WORK_DIR"
    ui_section "部署管理"
    ui_text 0 "  [1] 安装 / 重新配置"
    ui_text 0 "  [2] 更新现有部署"
    ui_text 2 "      检查镜像版本，无变化时跳过更新"
    ui_section "维护选项"
    ui_text 0 "  [3] 卸载服务"
    ui_text 2 "      保留配置、密钥和数据库卷"
    ui_text 0 "  [4] 设置目录"
    printf '\n'
}

show_mode_menu() {
    ui_heading "数据库模式"
    ui_section "选择部署方式"
    ui_text 0 "  [1] 内置 PostgreSQL（推荐）"
    ui_text 2 "      自动部署数据库和应用，适合从零安装"
    printf '\n'
    ui_text 0 "  [2] 连接外部 PostgreSQL"
    ui_text 2 "      复用已有数据库，仅部署应用"
    printf '\n'
}

show_no_update_result() {
    ui_heading "检查完成"
    ui_text '1;32' "  当前镜像与目标版本一致，无需更新"
    printf '\n'
}

show_result() {
    local result_name="$1" url="$2" stop_cmd="$3"
    ui_heading "$result_name"
    ui_section "访问面板"
    ui_text '1;32' --literal "  $url"
    ui_text 2 "  登录方式：用户名 + 密码 / 管理令牌"
    ui_section "部署文件"
    ui_detail "配置文件" "$ENV_FILE"
    if [ -d "$SECRET_DIR" ]; then
        ui_detail "既有密钥目录" "$SECRET_DIR"
    fi
    ui_section "常用命令"
    ui_detail "查看日志" "${stop_cmd% down} logs -f"
    ui_detail "停止服务" "$stop_cmd"
    printf '\n'
}

local_panel_url() {
    local port address
    port="$(read_env_key QCH_PORT)"
    [ -n "$port" ] || port=8080
    address="$(read_env_key QCH_BIND_ADDRESS)"
    case "$address" in
        ""|0.0.0.0) address=127.0.0.1 ;;
        ::|\[::\]) address='[::1]' ;;
        \[*\]) ;;
        *:*) address="[$address]" ;;
    esac
    printf 'http://%s:%s' "$address" "$port"
}

show_admin_token_once() {
    [ -n "$ADMIN_TOKEN_TO_DISPLAY" ] || return 0
    ui_heading "管理员令牌（仅本次显示）"
    printf '\n  %s\n\n' "$ADMIN_TOKEN_TO_DISPLAY"
    ui_text '1;33' "  请立即保存到密码管理器。"
    ui_text 2 "  .env 只保存 SHA-256 摘要，之后无法恢复原文。"
    printf '\n'
    ADMIN_TOKEN_TO_DISPLAY=""
}

resolve_work_dir() {
    if [ -z "${QCH_INSTALL_DIR:-}" ]; then
        WORK_DIR="$REPO_ROOT"
    else
        case "$QCH_INSTALL_DIR" in
            /*) WORK_DIR="$QCH_INSTALL_DIR" ;;
            *) WORK_DIR="$REPO_ROOT/$QCH_INSTALL_DIR" ;;
        esac
    fi
    mkdir -p "$WORK_DIR" || die "无法创建目录：$WORK_DIR"
    WORK_DIR="$(cd "$WORK_DIR" && pwd)" || die "无法进入目录：$WORK_DIR"
    cd "$WORK_DIR"
    ENV_FILE="$WORK_DIR/.env"
    EXTERNAL_COMPOSE_FILE="$WORK_DIR/docker-compose.external.yml"
    SECRET_COMPOSE_FILE="$WORK_DIR/docker-compose.secrets.yml"
    SECRET_DIR="$WORK_DIR/.secrets"
    CONFIG_KEY_FILE="$SECRET_DIR/config-encryption-key"
    PREVIOUS_CONFIG_KEYS_FILE="$SECRET_DIR/config-encryption-previous-keys"
    persist_install_dir_preference "$WORK_DIR" ||
        echo "警告：无法保存安装目录，下次从远程一键命令运行时可能需要重新设置目录：$WORK_DIR" >&2
}

choose_install_dir() {
    local input_dir
    echo ""
    ui_heading "设置目录"
    ui_detail "当前目录" "$WORK_DIR"
    echo "请输入新的目录（直接回车保持不变）："
    read -r input_dir
    case "$input_dir" in
        "") ;;
        *) QCH_INSTALL_DIR="$input_dir"; resolve_work_dir ;;
    esac
}

choose_action() {
    [ -t 0 ] || die "未指定操作；非交互模式请使用 -o install、-o update 或 -o uninstall"
    while :; do
        show_action_menu
        read -r -p "  请选择 [1-4] > " choice
        case "$choice" in
            1) ACTION="install"; return ;;
            2) ACTION="update"; return ;;
            3) ACTION="uninstall"; return ;;
            4) choose_install_dir ;;
            *) echo "无效选择：$choice" ;;
        esac
    done
}

choose_mode() {
    [ -t 0 ] || die "未指定部署模式；非交互模式请使用 -m bundled 或 -m external"
    show_mode_menu
    read -r -p "  请选择 [1-2] > " choice
    case "$choice" in
        1) MODE="bundled" ;;
        2) MODE="external" ;;
        *) die "无效选择：$choice" ;;
    esac
}

detect_existing_mode() {
    [ -f "$ENV_FILE" ] || die "未找到现有部署配置：$ENV_FILE"
    if [ -n "$(read_env_key QCH_DATABASE_URL)" ] || [ -f "$EXTERNAL_COMPOSE_FILE" ]; then
        MODE="external"
        if [ "$ACTION" = "uninstall" ] && [ ! -f "$EXTERNAL_COMPOSE_FILE" ]; then
            die "外部 PostgreSQL 部署缺少 $EXTERNAL_COMPOSE_FILE"
        fi
    else
        MODE="bundled"
    fi
}

configure_compose_args() {
    case "$MODE" in
        bundled)
            COMPOSE_ARGS=(-f "$REPO_ROOT/docker-compose.yml")
            if [ "$ACTION" != "uninstall" ] || [ -f "$SECRET_COMPOSE_FILE" ]; then
                COMPOSE_ARGS+=(-f "$SECRET_COMPOSE_FILE")
            fi
            ;;
        external)
            COMPOSE_ARGS=(-f "$EXTERNAL_COMPOSE_FILE")
            if [ -z "$(read_env_key QCH_CONFIG_ENCRYPTION_KEY)" ] && [ -f "$SECRET_COMPOSE_FILE" ]; then
                COMPOSE_ARGS+=(-f "$SECRET_COMPOSE_FILE")
            fi
            ;;
    esac
}

uninstall_services() {
    echo "-> 停止并移除 QControlHub 服务容器和网络"
    compose down --remove-orphans
    ui_heading "服务已卸载"
    ui_section "已保留的部署数据"
    ui_detail "已保留配置" "$ENV_FILE"
    if [ -d "$SECRET_DIR" ]; then
        ui_detail "已保留密钥" "$SECRET_DIR"
    fi
    if [ "$MODE" = "bundled" ]; then
        echo "  已保留数据：Docker PostgreSQL 命名卷"
    else
        echo "  外部 PostgreSQL 数据未被修改"
    fi
    echo ""
}

prepare_action_and_work_dir() {
    # The directory selector is part of the action menu, so resolve the saved
    # work directory first. Otherwise option 4 displays the runtime script
    # directory even though QCH_INSTALL_DIR has already been restored.
    resolve_work_dir
    if [ -z "$ACTION" ]; then
        if [ -n "$MODE" ]; then
            ACTION="install"
        else
            choose_action
        fi
    fi
}

# Keep the environment preparation functions sourceable for the isolated shell
# regression without running Docker or mutating the caller's deployment.
if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then
    return 0
fi

# ---- 选择管理操作和部署方式 ----
prepare_action_and_work_dir
if [ "$ACTION" = "install" ]; then
    [ -n "$MODE" ] || choose_mode
else
    [ -n "$MODE" ] || detect_existing_mode
fi
if [ "$ACTION" = "uninstall" ] && [ "$FORCE" = true ]; then
    die "卸载操作不支持 -f；默认始终保留配置、密钥和数据库卷"
fi
configure_compose_args

require_commands

# ---- 执行管理操作 ----
if [ "$ACTION" = "uninstall" ]; then
    uninstall_services
    exit 0
fi

case "$MODE" in
    bundled)
        if [ "$ACTION" = "update" ]; then
            ui_heading "更新检查"
            ui_text 2 "  更新内置 PostgreSQL 部署并复用现有配置"
            [ -f "$ENV_FILE" ] || die "未找到现有部署配置：$ENV_FILE"
            resolve_bundled_image_refs
            current_control_image="$(current_update_image_id control-plane)" || die "无法读取控制面当前镜像"
            current_web_image="$(current_update_image_id qcontrol-web)" || die "无法读取网页服务当前镜像"
            current_postgres_image="$(current_update_image_id postgres)" || die "无法读取 PostgreSQL 当前镜像"
            show_current_application_versions "$current_control_image" "$current_web_image"
            current_postgres_version="$(update_image_version "$current_postgres_image")" || die "无法读取 PostgreSQL 当前版本"
            ui_service_row PostgreSQL "当前版本：$current_postgres_version"
            show_update_targets "$BUNDLED_CONTROL_IMAGE_REF" "$BUNDLED_WEB_IMAGE_REF" "$BUNDLED_POSTGRES_IMAGE_REF"
            if [ "$BUNDLED_CONTROL_IMAGE_REF" = "ghcr.io/qimaoww/qcontrol-plane:local" ]; then
                echo "-> 本地构建模式无法检查远程镜像版本，将重新构建"
            else
                prepare_update_plan
                if [ "$UPDATE_IMAGES_CHANGED" = false ]; then
                    if [ "$FORCE" = false ]; then
                        show_no_update_result
                        exit 0
                    fi
                    echo "-> 镜像无变化，继续执行 -f 指定的密钥轮换"
                else
                    echo "-> 检测到镜像变化，开始更新"
                    pull_update_images
                fi
            fi
        elif [ -f "$ENV_FILE" ] && [ "$FORCE" = false ]; then
            echo "-> 复用已有 .env，并补齐缺失配置"
        elif [ "$FORCE" = true ]; then
            echo "-> 轮换应用密钥（保留 PostgreSQL 密码）"
        else
            echo "-> 生成部署配置写入 .env"
        fi
        prepare_bundled_env
        show_admin_token_once
        write_secret_compose_override
        start_services

        panel_url="$(local_panel_url)"
        echo "-> 等待控制面就绪..."
        if ! wait_ready "$panel_url/readyz" "$READY_TIMEOUT"; then
            show_diagnostics
            die "控制面未在 ${READY_TIMEOUT} 秒内就绪"
        fi

        [ "$ACTION" = "update" ] && result_name="更新完成" || result_name="部署完成"
        show_result "$result_name" "$panel_url" "docker compose --project-directory $WORK_DIR --env-file $ENV_FILE -f $REPO_ROOT/docker-compose.yml -f $SECRET_COMPOSE_FILE down"
        ;;
    external)
        if [ "$ACTION" = "update" ]; then
            ui_heading "更新检查"
            ui_text 2 "  更新外部 PostgreSQL 部署并复用现有配置"
            [ -z "$DATABASE_URL" ] || die "更新禁止使用 -d；QCH_DATABASE_URL 必须原样复用"
            [ -z "$ADMIN_TOKEN" ] || die "更新禁止使用 -a；管理员令牌不得轮换"
            [ -z "$DOCKER_NETWORK" ] || die "更新禁止使用 -n；Docker 网络配置必须原样复用"
            [ "$FORCE" = false ] || die "外部 PostgreSQL 更新不支持 -f；令牌和配置加密密钥不得轮换"
            validate_external_update_env
            update_external_services
            exit 0
        elif [ -f "$ENV_FILE" ] && [ "$FORCE" = false ]; then
            echo "-> 复用已有 .env，并补齐缺失配置"
            prepare_external_env
            show_admin_token_once
            configure_compose_args

            echo "-> 生成 $EXTERNAL_COMPOSE_FILE"
            write_external_compose
            validate_external_network
            start_external_services
        elif [ "$FORCE" = true ]; then
            die "外部 PostgreSQL 部署不支持 -f；不会轮换管理员令牌或配置加密密钥"
        else
            echo "-> 生成部署配置写入 .env"
            prepare_external_env
            show_admin_token_once
            configure_compose_args

            echo "-> 生成 $EXTERNAL_COMPOSE_FILE"
            write_external_compose
            validate_external_network
            start_external_services
        fi

        panel_url="$(local_panel_url)"
        if [ "$ACTION" != "update" ]; then
            echo "-> 等待应用健康和数据库就绪..."
        fi
        if [ "$ACTION" != "update" ] && ! check_external_endpoints "$panel_url"; then
            show_diagnostics
            die "应用未在 ${READY_TIMEOUT} 秒内通过 healthz 和 readyz 检查"
        fi

        show_result "部署完成" "$panel_url" "docker compose -p qcontrolhub --project-directory $WORK_DIR --env-file $ENV_FILE -f $EXTERNAL_COMPOSE_FILE down"
        ;;
esac
