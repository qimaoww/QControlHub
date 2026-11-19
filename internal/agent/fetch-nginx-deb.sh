# Executed in a fresh helper-owned temporary directory. APT_CONFIG is loaded
# before apt.conf.d, so host update hooks, mirrors and pins are left alone.
set -eu
suite=$2
family=$1
operation=$3
current=$4
arch=$(dpkg --print-architecture)

mkdir empty lists
: > status
printf 'deb [arch=%s signed-by=%s/signing.asc] https://nginx.org/packages/mainline/%s %s nginx\n' "$arch" "$PWD" "$family" "$suite" > sources.list
cat > apt.conf <<CONF
Dir::Etc::parts "$PWD/empty";
Dir::Etc::main "/dev/null";
Dir::Etc::sourcelist "$PWD/sources.list";
Dir::Etc::sourceparts "$PWD/empty";
Dir::Etc::preferences "/dev/null";
Dir::Etc::preferencesparts "$PWD/empty";
Dir::State::lists "$PWD/lists";
Dir::State::status "$PWD/status";
Dir::Cache::pkgcache "";
Dir::Cache::srcpkgcache "";
Acquire::Retries "2";
Acquire::Languages "none";
CONF
export APT_CONFIG="$PWD/apt.conf"
apt-get --error-on=any update >&2
candidate=$(apt-cache policy nginx | awk '$1 == "Candidate:" {print $2}')
[ -n "$candidate" ] && [ "$candidate" != "(none)" ] || { echo "no official package for $suite:$arch" >&2; exit 1; }
printf '%s' "$candidate" > revision
[ "$operation" = check ] && exit 0
if [ -n "$current" ] && ! dpkg --compare-versions "$candidate" ge "$current"; then
    touch newer-installed
    exit 0
fi
apt-get download "nginx=$candidate" >&2
set -- ./*.deb
[ "$#" -eq 1 ]
[ "$(dpkg-deb -f "$1" Package)" = nginx ]
[ "$(dpkg-deb -f "$1" Architecture)" = "$arch" ]
revision=$(dpkg-deb -f "$1" Version)
[ "$revision" = "$candidate" ]
dpkg-deb -x "$1" extracted
