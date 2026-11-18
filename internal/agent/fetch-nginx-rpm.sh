set -eu
arch=$(rpm --eval '%{_arch}')
base=https://nginx.org/packages/mainline/$1/$2
[ "$1" = sles ] || base="$base/$arch"
mkdir repo db
cat > dnf.conf <<CONF
[main]
cachedir=$PWD/cache
persistdir=$PWD/state
logdir=$PWD
reposdir=$PWD/repo
plugins=0
[nginx-mainline]
name=nginx.org mainline
baseurl=$base
enabled=1
gpgcheck=1
CONF
# Select only this repository and architecture. Authenticate the RPM in a
# temporary RPM database before reading or extracting its payload.
url=$(dnf -q -c "$PWD/dnf.conf" repoquery --available --latest-limit=1 --arch="$arch" --location nginx)
case "$url" in "$base"/*.rpm) ;; *) echo 'missing official nginx RPM' >&2; exit 1 ;; esac
curl -fsS --proto '=https' "$url" -o nginx.rpm
rpm --dbpath "$PWD/db" --import "$PWD/signing.asc"
rpmkeys --dbpath "$PWD/db" --checksig --verbose nginx.rpm > signature
grep -Eq '(Signature|signature).*: OK' signature
[ "$(rpm -qp --qf '%{NAME} %{ARCH}' nginx.rpm)" = "nginx $arch" ]
revision=$(rpm -qp --qf '%{VERSION}-%{RELEASE}' nginx.rpm)
printf '%s' "$revision" > revision
[ "$3" = check ] && exit 0
case "$4" in *[!a-zA-Z0-9.~_+-]*) exit 1 ;; esac
if [ -n "$4" ] && [ "$(rpm --eval "%{lua:print(rpm.vercmp('$revision','$4'))}")" = -1 ]; then
    echo "refusing downgrade from $4 to $revision" >&2
    exit 1
fi
mkdir extracted
rpm2cpio nginx.rpm > payload
(cd extracted && cpio --quiet -id './usr/sbin/nginx' < ../payload)
