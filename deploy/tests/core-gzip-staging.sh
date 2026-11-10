#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
stage_module=$repo_root/deploy/modules/install-core-engines/05-stage.sh

# Use a real gzip stream with a binary payload whose source has no execute bit.
# This is the format used by Mihomo releases, independent of network fixtures.
cat > "$test_root/source" <<'BINARY'
#!/bin/sh
[ "$#" -eq 1 ] && [ "$1" = -v ] || exit 9
printf '%s\n' 'Mihomo fixture version'
BINARY
chmod 0644 "$test_root/source"
gzip -c "$test_root/source" > "$test_root/mihomo.gz"
version=$(sh -ec '. "$1"; qch_stage_gzip_binary "$2" "$3" -v' sh \
  "$stage_module" "$test_root/mihomo.gz" "$test_root/mihomo")
[ "$version" = 'Mihomo fixture version' ]
[ -x "$test_root/mihomo" ]
cmp "$test_root/source" "$test_root/mihomo"

printf '%s\n' 'not a gzip stream' > "$test_root/bad.gz"
if sh -ec '. "$1"; qch_stage_gzip_binary "$2" "$3" -v' sh \
  "$stage_module" "$test_root/bad.gz" "$test_root/bad"; then
  printf '%s\n' 'accepted an invalid gzip archive' >&2; exit 1
fi

# A candidate that cannot report its version must stop the installer before
# any replacement, including when the helper runs in command substitution.
printf '%s\n' '#!/bin/sh' 'exit 7' > "$test_root/rejected-source"
gzip -c "$test_root/rejected-source" > "$test_root/rejected.gz"
if sh -ec '. "$1"; version=$(qch_stage_gzip_binary "$2" "$3" -v); touch "$4"' sh \
  "$stage_module" "$test_root/rejected.gz" "$test_root/rejected" "$test_root/install-started"; then
  printf '%s\n' 'accepted a failed binary version check' >&2; exit 1
fi
[ ! -e "$test_root/install-started" ]
printf '%s\n' 'Core gzip staging, execute permissions and version failure regressions passed'

. "$stage_module"
cat > "$test_root/ssserver" <<'BINARY'
#!/bin/sh
echo 'shadowsocks 1.25.0'
BINARY
chmod 0700 "$test_root/ssserver"
[ "$(qch_ssrust_version "$test_root/ssserver" 1.25.0 "$test_root/probe")" = 'shadowsocks 1.25.0' ]
if qch_ssrust_version "$test_root/ssserver" 1.25.1 "$test_root/probe"; then
  printf '%s\n' 'accepted wrong ss-rust release' >&2; exit 1
fi
printf '%s: /lib/libc.so.6: version `GLIBC_2.39\047 not found (required by %s)\n' "$test_root/ssserver" "$test_root/ssserver" > "$test_root/probe"
qch_ssrust_missing_glibc "$test_root/ssserver" "$test_root/probe"
printf '%s\n' 'unrelated CLI error' >> "$test_root/probe"
if qch_ssrust_missing_glibc "$test_root/ssserver" "$test_root/probe"; then
  printf '%s\n' 'unrelated failure permitted ss-rust fallback' >&2; exit 1
fi
: > "$test_root/probe"
if qch_ssrust_missing_glibc "$test_root/ssserver" "$test_root/probe"; then
  printf '%s\n' 'empty probe permitted ss-rust fallback' >&2; exit 1
fi
printf '%s\n' 'SS Rust version and conditional GLIBC fallback regressions passed'
