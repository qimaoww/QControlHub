set -eu
arch=$(apk --print-arch)
mkdir keys root
mv nginx_signing.rsa.pub keys/
printf '%s\n' "https://nginx.org/packages/mainline/alpine/v$2/main" > repos
official_apk() {
    apk --root "$PWD/root" --arch "$arch" --no-cache --keys-dir "$PWD/keys" --repositories-file "$PWD/repos" "$@"
}
official_apk fetch nginx
set -- "$3" "$4" ./nginx-*.apk
[ "$#" -eq 3 ]
official_apk verify "$3"
metadata=$(tar -xOzf "$3" .PKGINFO)
field() { printf '%s\n' "$metadata" | sed -n "s/^$1 = //p"; }
[ "$(field pkgname)" = nginx ] && [ "$(field arch)" = "$arch" ] || exit 1
revision=$(field pkgver)
printf '%s' "$revision" > revision
[ "$1" = check ] && exit 0
if [ -n "$2" ] && [ "$(apk version -t "$revision" "$2")" = '<' ]; then
    echo "refusing downgrade from $2 to $revision" >&2
    exit 1
fi
mkdir -p extracted/usr/sbin
tar -xOzf "$3" usr/sbin/nginx > extracted/usr/sbin/nginx
