#!/bin/sh
set -eu

: "${QCH_TEST_DATABASE_URL:?QCH_TEST_DATABASE_URL must point to a test PostgreSQL database}"
repo_root="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf -- "$test_dir"' EXIT HUP INT TERM

# The account-isolation release uses schema v58 and real bcrypt credentials.
# Pin the fixture: using origin/main would stop exercising schema migration.
previous_ref=e3ff68389cc75d6a29c52822bdd31af3bcd0ab23
mkdir "$test_dir/previous"
# Container CI can mount the checkout with a different owner. Trust only this
# explicitly selected checkout for this read, without changing global Git config.
git -c safe.directory="$repo_root" -C "$repo_root" archive --format=tar --output="$test_dir/previous.tar" "$previous_ref"
tar -xf "$test_dir/previous.tar" -C "$test_dir/previous"
(cd "$test_dir/previous" && go build -buildvcs=false -o "$test_dir/old-panel" ./cmd/control-plane)
(cd "$repo_root" && go build -buildvcs=false -o "$test_dir/new-panel" ./cmd/control-plane)
cd "$repo_root"
QCH_TEST_PREVIOUS_PANEL_BINARY="$test_dir/old-panel" QCH_TEST_PANEL_BINARY="$test_dir/new-panel" \
    go test -count=1 -timeout 5m ./internal/api -run '^TestPanelAuthUpgradeLifecycle$' -v
