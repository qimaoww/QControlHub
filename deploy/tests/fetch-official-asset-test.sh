#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir "$work/bin"
export QCH_OFFICIAL_ASSET_CACHE="$work/cache" QCH_ASSET_FIXTURE="$work"
cat > "$work/bin/curl" <<'FAKE'
#!/bin/sh
set -eu
url= output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        https://*) url=$1; shift ;;
        --output) output=$2; shift 2 ;;
        *) shift ;;
    esac
done
printf '%s\n' "$url" >> "$QCH_ASSET_FIXTURE/requests"
case "$url" in https://github.com/*) printf partial > "$output"; exit 22 ;; esac
case "${QCH_ASSET_FAILURE:-}" in
    network) printf partial > "$output"; exit 28 ;;
    corrupt) printf corrupted > "$output" ;;
    *) printf verified > "$output" ;;
esac
FAKE
chmod 0755 "$work/bin/curl"
export PATH="$work/bin:$PATH"
url=https://github.com/owner/repo/releases/download/v1/fixture.tar.gz
digest=$(printf verified | sha256sum | cut -d ' ' -f 1)
fetch() { sh "$root/fetch-official-asset.sh" "$url" "$work/asset" "$digest" 123; }
fetch
[ "$(cat "$work/asset")" = verified ]
[ "$(wc -l < "$work/requests")" -eq 2 ]
grep -qx 'https://api.github.com/repos/owner/repo/releases/assets/123' "$work/requests"
# A cache hit performs no network request, but a corrupt cache must be replaced.
fetch
[ "$(wc -l < "$work/requests")" -eq 2 ]
printf corrupted > "$QCH_OFFICIAL_ASSET_CACHE/$digest"
fetch
[ "$(cat "$work/asset")" = verified ]
[ "$(wc -l < "$work/requests")" -eq 4 ]
for failure in network corrupt; do
    rm "$QCH_OFFICIAL_ASSET_CACHE/$digest"
    printf preserved > "$work/asset"
    if QCH_ASSET_FAILURE=$failure fetch; then exit 1; fi
    [ "$(cat "$work/asset")" = preserved ]
    [ ! -e "$QCH_OFFICIAL_ASSET_CACHE/$digest" ]
    [ -z "$(find "$QCH_OFFICIAL_ASSET_CACHE" -name '.download.*' -print)" ]
    fetch
done
printf '%s\n' 'Official asset fallback, cache validation and failure preservation passed'
