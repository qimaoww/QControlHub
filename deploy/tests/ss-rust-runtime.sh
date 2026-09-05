#!/bin/sh
set -eu
# Official v1.25.0 musl binaries; isolated from the host network and services.
fixture=$(mktemp -d /tmp/qch-ss-rust-native.XXXXXX)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
archive=shadowsocks-v1.25.0.x86_64-unknown-linux-musl.tar.xz
curl --fail --location --retry 3 --max-time 120 "https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.25.0/$archive" -o "$fixture/$archive"
printf '%s  %s\n' 8439bf43c324b0fc273e663d0b1f8926fd8f666cbd1e0fd59b35096f0e778e92 "$fixture/$archive" | sha256sum -c -
tar -xf "$fixture/$archive" -C "$fixture" ssserver sslocal
chmod 0755 "$fixture" "$fixture/ssserver" "$fixture/sslocal"
docker run --rm --read-only --network none --cap-drop ALL \
  --tmpfs /tmp:exec,mode=1777 -e GOCACHE=/tmp/go-cache \
  -v "$fixture:/native" -v "$(pwd):/src:ro" -v "$(go env GOMODCACHE):/go/pkg/mod:ro" \
  -w /src golang:1.25-bookworm go test -c -o /native/runtime.test ./internal/serverconfig
docker run --rm --read-only --network none --user 65534:65534 --cap-drop ALL \
  --tmpfs /tmp:exec,mode=1777 -e QCH_SS_RUST_NATIVE_BIN=/native \
  -v "$fixture:/native:ro" golang:1.25-bookworm /native/runtime.test -test.run '^TestSSRustNativeFieldScopes$' -test.v
