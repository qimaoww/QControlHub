#!/bin/sh
# Only pinned official test assets; every cache hit and download is verified.
set -eu
source_url=${1:?release URL required}
destination=${2:?destination required}
digest=${3:?SHA-256 required}
asset_id=${4:?GitHub asset ID required}
case "$source_url" in https://github.com/*/*/releases/download/*/*) ;; *) exit 1 ;; esac
case "$digest" in *[!0-9a-f]*|'') exit 1 ;; esac
[ "${#digest}" -eq 64 ]
case "$asset_id" in *[!0-9]*|''|0) exit 1 ;; esac
repository=${source_url#https://github.com/}
repository=${repository%%/releases/download/*}
cache=${QCH_OFFICIAL_ASSET_CACHE:-$(dirname "$0")/../../.ci-cache/official-assets}
mkdir -p "$cache" "$(dirname "$destination")"
cached="$cache/$digest"
verify() { printf '%s  %s\n' "$digest" "$1" | sha256sum -c -; }
if [ -f "$cached" ] && verify "$cached"; then
    cp "$cached" "$destination"
    exit 0
fi
partial=$(mktemp "$cache/.download.XXXXXX")
trap 'rm -f "$partial"' EXIT HUP INT TERM
for route in "$source_url" "https://api.github.com/repos/$repository/releases/assets/$asset_id"; do
    if curl -q --fail --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 10 --max-time 45 --retry 2 --retry-max-time 90 \
        --max-filesize 268435456 --header 'Accept: application/octet-stream' \
        "$route" --output "$partial"; then
        # A successful response with a wrong checksum is never accepted or cached.
        verify "$partial"
        mv -f "$partial" "$cached"
        cp "$cached" "$destination"
        exit 0
    fi
done
exit 1
