#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-install-bootstrap.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fake_bin="$test_root/bin"
install_dir="$test_root/install target"
mkdir -p "$fake_bin"

cat > "$fake_bin/git" <<'FAKE_GIT'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$QCH_INSTALL_TEST_LOG"
if [ "${1:-}" = "clone" ]; then
    target="${!#}"
    mkdir -p "$target/.git" "$target/deploy"
    cat > "$target/deploy/quick-start.sh" <<'QUICK_START'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$QCH_INSTALL_TEST_ARGS"
QUICK_START
    chmod 755 "$target/deploy/quick-start.sh"
    exit 0
fi
if [ "${1:-}" = "-C" ]; then
    shift 2
    case "${1:-}" in
        remote) printf '%s\n' 'https://github.com/qimaoww/qcontrolhub.git' ;;
        symbolic-ref) printf '%s\n' 'main' ;;
        diff|fetch|merge) ;;
        *) printf 'unexpected fake git command: %s\n' "$*" >&2; exit 1 ;;
    esac
    exit 0
fi
printf 'unexpected fake git invocation: %s\n' "$*" >&2
exit 1
FAKE_GIT
chmod 755 "$fake_bin/git"

export QCH_INSTALL_TEST_LOG="$test_root/git.log"
export QCH_INSTALL_TEST_ARGS="$test_root/quick-start.args"

PATH="$fake_bin:$PATH" QCH_INSTALL_DIR="$install_dir" \
    bash "$repo_root/deploy/install.sh" -m bundled
grep -Fq -- "--depth 1 --branch main --single-branch https://github.com/qimaoww/qcontrolhub.git $install_dir" "$QCH_INSTALL_TEST_LOG"
grep -Fxq -- "-m" "$QCH_INSTALL_TEST_ARGS"
grep -Fxq -- "bundled" "$QCH_INSTALL_TEST_ARGS"

: > "$QCH_INSTALL_TEST_LOG"
PATH="$fake_bin:$PATH" QCH_INSTALL_DIR="$install_dir" \
    bash "$repo_root/deploy/install.sh" -m external -d 'postgresql://db.example.test/qcontrolhub'
grep -Fq -- "fetch --prune origin main" "$QCH_INSTALL_TEST_LOG"
grep -Fq -- "merge --ff-only FETCH_HEAD" "$QCH_INSTALL_TEST_LOG"
grep -Fxq -- "external" "$QCH_INSTALL_TEST_ARGS"
grep -Fxq -- "postgresql://db.example.test/qcontrolhub" "$QCH_INSTALL_TEST_ARGS"

foreign_dir="$test_root/foreign"
mkdir -p "$foreign_dir"
if PATH="$fake_bin:$PATH" QCH_INSTALL_DIR="$foreign_dir" bash "$repo_root/deploy/install.sh" -h >/dev/null 2>&1; then
    printf '%s\n' 'install bootstrap accepted a foreign existing directory' >&2
    exit 1
fi

printf '%s\n' 'one-line install bootstrap regression passed'
