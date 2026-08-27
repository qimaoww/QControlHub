#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-quick-start-bootstrap.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fake_bin="$test_root/bin"
install_dir="$test_root/install target"
mkdir -p "$fake_bin"

cat > "$fake_bin/curl" <<'FAKE_CURL'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$QCH_BOOTSTRAP_TEST_LOG"
url=""
output=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output="$2"; shift 2 ;;
        http*) url="$1"; shift ;;
        *) shift ;;
    esac
done
case "$url" in
    */deploy/quick-start.sh)
        cat > "$output" <<'QUICK_START'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$QCH_BOOTSTRAP_TEST_ARGS"
QUICK_START
        ;;
    */docker-compose.yml)
        printf '%s\n' 'name: qcontrolhub' 'services: {}' > "$output"
        ;;
    *) printf 'unexpected download URL: %s\n' "$url" >&2; exit 1 ;;
esac
FAKE_CURL
chmod 755 "$fake_bin/curl"

export QCH_BOOTSTRAP_TEST_LOG="$test_root/git.log"
export QCH_BOOTSTRAP_TEST_ARGS="$test_root/quick-start.args"

PATH="$fake_bin:$PATH" QCH_INSTALL_DIR="$install_dir" \
    bash <(cat "$repo_root/deploy/quick-start.sh") -m bundled
grep -Fq -- 'https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/deploy/quick-start.sh' "$QCH_BOOTSTRAP_TEST_LOG"
grep -Fq -- 'https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/docker-compose.yml' "$QCH_BOOTSTRAP_TEST_LOG"
[ -f "$install_dir/.qcontrolhub-quick-start" ]
[ -f "$install_dir/docker-compose.yml" ]
[ ! -e "$install_dir/.git" ]
grep -Fxq -- "-m" "$QCH_BOOTSTRAP_TEST_ARGS"
grep -Fxq -- "bundled" "$QCH_BOOTSTRAP_TEST_ARGS"

: > "$QCH_BOOTSTRAP_TEST_LOG"
PATH="$fake_bin:$PATH" QCH_INSTALL_DIR="$install_dir" \
    bash <(cat "$repo_root/deploy/quick-start.sh") -m external -d 'postgresql://db.example.test/qcontrolhub'
grep -Fq -- 'https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/deploy/quick-start.sh' "$QCH_BOOTSTRAP_TEST_LOG"
grep -Fq -- 'https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/docker-compose.yml' "$QCH_BOOTSTRAP_TEST_LOG"
grep -Fxq -- "external" "$QCH_BOOTSTRAP_TEST_ARGS"
grep -Fxq -- "postgresql://db.example.test/qcontrolhub" "$QCH_BOOTSTRAP_TEST_ARGS"

foreign_dir="$test_root/foreign"
mkdir -p "$foreign_dir"
printf '%s\n' 'unrelated' > "$foreign_dir/keep.txt"
if PATH="$fake_bin:$PATH" QCH_INSTALL_DIR="$foreign_dir" \
    bash <(cat "$repo_root/deploy/quick-start.sh") -h >/dev/null 2>&1; then
    printf '%s\n' 'streamed quick-start accepted a foreign existing directory' >&2
    exit 1
fi

# Existing installations created by the previous Git-based bootstrap are
# accepted, but only the two runtime files are refreshed; no fetch or clone is
# performed.
legacy_git_dir="$test_root/legacy git install"
mkdir -p "$legacy_git_dir/.git"
cat > "$fake_bin/git" <<'FAKE_GIT'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$QCH_BOOTSTRAP_GIT_LOG"
if [ "${1:-}" = "-C" ] && [ "${3:-}" = "remote" ]; then
    printf '%s\n' 'https://github.com/qimaoww/qcontrolhub.git'
    exit 0
fi
if [ "${1:-}" = "-C" ] && [ "${3:-}" = "symbolic-ref" ]; then
    printf '%s\n' 'main'
    exit 0
fi
if [ "${1:-}" = "-C" ] && [ "${3:-}" = "diff" ]; then
    exit 0
fi
printf 'unexpected fake git invocation: %s\n' "$*" >&2
exit 1
FAKE_GIT
chmod 755 "$fake_bin/git"
export QCH_BOOTSTRAP_GIT_LOG="$test_root/git.log"
PATH="$fake_bin:$PATH" QCH_INSTALL_DIR="$legacy_git_dir" \
    bash <(cat "$repo_root/deploy/quick-start.sh") -m bundled
grep -Fq -- 'remote get-url origin' "$QCH_BOOTSTRAP_GIT_LOG"
if grep -Eq -- 'clone|fetch|merge|pull' "$QCH_BOOTSTRAP_GIT_LOG"; then
    printf '%s\n' 'streamed quick-start performed a Git checkout update' >&2
    exit 1
fi

# Re-running from inside a standalone install directory updates that directory
# instead of creating qcontrolhub/qcontrolhub.
: > "$QCH_BOOTSTRAP_TEST_LOG"
(
    cd "$install_dir"
    PATH="$fake_bin:$PATH" bash <(cat "$repo_root/deploy/quick-start.sh") -m bundled
)
[ ! -e "$install_dir/qcontrolhub" ]
grep -Fq -- 'https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/docker-compose.yml' "$QCH_BOOTSTRAP_TEST_LOG"

printf '%s\n' 'streamed quick-start bootstrap regression passed'
