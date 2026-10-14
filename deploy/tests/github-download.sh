#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
mkdir "$test_root/bin"
export QCH_DOWNLOAD_TEST_ROOT=$test_root
cat > "$test_root/bin/curl" <<'FAKE'
#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$QCH_DOWNLOAD_TEST_ROOT/requests"
probe=; resolve=; url=; output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --head) probe=1; shift ;;
        --resolve) resolve=$2; shift 2 ;;
        --output) output=$2; shift 2 ;;
        https://*) url=$1; shift ;;
        *) shift ;;
    esac
done
if [ -n "$probe" ]; then
    case "$resolve" in
        *':1.1.1.1'|*':8.8.8.8') case "$QCH_DOWNLOAD_TEST_NETWORK" in ipv4|dual-stack) exit 0 ;; *) exit 1 ;; esac ;;
        *'['*) case "$QCH_DOWNLOAD_TEST_NETWORK" in ipv6-only|dual-stack|nat64-dns64|ipv6-github-direct|dns-failure) exit 0 ;; *) exit 1 ;; esac ;;
    esac
    case "$url" in
        https://api.github.com/*) case "$QCH_DOWNLOAD_TEST_NETWORK" in nat64-dns64|ipv6-github-direct) exit 0 ;; *) exit 1 ;; esac ;;
        https://v6.gh-proxy.org/*) [ "$QCH_DOWNLOAD_TEST_NETWORK" != dns-failure ]; exit $? ;;
    esac
    exit 1
fi
case "$url" in
    */redirect) printf '302\nhttps://raw.githubusercontent.com/o/r/ref/file'; exit 0 ;;
    */proxy-redirect) printf '302\nhttps://v6.gh-proxy.org/https://raw.githubusercontent.com/o/r/ref/file'; exit 0 ;;
    */bad-redirect) printf '302\nhttps://other.example/file'; exit 0 ;;
    */missing) printf partial > "$output"; printf '404\n'; exit 0 ;;
    */fail) printf partial > "$output"; exit 28 ;;
esac
printf resource > "$output"
printf '200\n'
FAKE
cat > "$test_root/bin/ip" <<'FAKE'
#!/bin/sh
exit 0
FAKE
cat > "$test_root/bin/getent" <<'FAKE'
#!/bin/sh
[ "$QCH_DOWNLOAD_TEST_NETWORK" = nat64-dns64 ] || exit 2
printf '%s\n' '64:ff9b::c000:aa ipv4only.arpa'
FAKE
chmod 0755 "$test_root/bin/curl" "$test_root/bin/ip" "$test_root/bin/getent"
export PATH="$test_root/bin:$PATH"
unset HTTPS_PROXY https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy
. "$repo_root/deploy/modules/shared/github-download.sh"

for scenario in ipv4 dual-stack ipv6-only nat64-dns64 ipv6-github-direct offline dns-failure; do
    export QCH_DOWNLOAD_TEST_NETWORK=$scenario
    qch_github_expires=0
    : > "$test_root/requests"
    qch_github_detect
    expected=$scenario
    case "$scenario" in offline|dns-failure) expected=unknown ;; esac
    [ "$qch_github_mode" = "$expected" ] || { printf 'network %s -> %s\n' "$scenario" "$qch_github_mode" >&2; exit 1; }
    qch_github_download 'https://api.github.com/repos/o/r/releases?per_page=5&page=2' "$test_root/resource"
    qch_github_download 'https://github.com/o/r/redirect' "$test_root/resource"
    qch_github_download 'https://github.com/o/r/proxy-redirect' "$test_root/resource"
    [ "$(cat "$test_root/resource")" = resource ]
    [ "$(rg_count=0; while IFS= read -r line; do case "$line" in *--resolve*) rg_count=$((rg_count+1));; esac; done < "$test_root/requests"; printf '%s' "$rg_count")" -eq 4 ]
    if [ "$scenario" = ipv6-only ]; then
        grep -Fq 'https://v6.gh-proxy.org/https://api.github.com/repos/o/r/releases?per_page=5&page=2' "$test_root/requests"
    fi
done

for failure in bad-redirect missing fail; do
    printf preserved > "$test_root/resource"
    if qch_github_download "https://github.com/o/r/$failure" "$test_root/resource"; then
        printf 'accepted failed download %s\n' "$failure" >&2; exit 1
    fi
    [ "$(cat "$test_root/resource")" = preserved ]
    [ -z "$(find "$test_root" -name '*.download.*' -print)" ]
done
if grep -Fq -- '--output' "$test_root/requests" && grep -Fq -- 'https://other.example' "$test_root/requests"; then
    printf '%s\n' 'requested an untrusted redirect' >&2; exit 1
fi
printf preserved > "$test_root/resource"
if qch_github_download https://github.com/o/r/file "$test_root/resource" 1; then exit 1; fi
[ "$(cat "$test_root/resource")" = preserved ]

export HTTPS_PROXY=http://proxy.example:8080 NO_PROXY=github.com,.githubusercontent.com
qch_github_egress_proxy https://api.github.com/repos/o/r && exit 1
qch_github_egress_proxy https://raw.githubusercontent.com/o/r/f && exit 1
qch_github_egress_proxy https://elsewhere.example/file
export NO_PROXY='*'
qch_github_egress_proxy https://api.github.com/repos/o/r && exit 1

# curl excludes both the apex and subdomains for a leading dot. Unlike Go's
# proxy matcher, curl treats *.host and host:port exclusions as literal names.
# Keep channel selection consistent with the actual curl process's routing.
qch_github_mode=ipv6-only
qch_github_expires=$(( $(date +%s) + 300 ))
for exclusion in .github.com '*.github.com' github.com:443; do
    export NO_PROXY=$exclusion
    : > "$test_root/requests"
    qch_github_download https://github.com/o/r/file "$test_root/resource"
    case "$exclusion" in
        .github.com)
            qch_github_egress_proxy https://github.com/o/r && exit 1
            grep -Fq 'https://v6.gh-proxy.org/https://github.com/o/r/file' "$test_root/requests" ;;
        *)
            qch_github_egress_proxy https://github.com/o/r
            if grep -Fq 'https://v6.gh-proxy.org/' "$test_root/requests"; then exit 1; fi ;;
    esac
done
printf '%s\n' 'GitHub download network, cache, metadata, redirect and failure regressions passed'
