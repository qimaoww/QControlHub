#!/usr/bin/env bash
# release-assets.sh — name the installer assets the signed release list has to
# cover, and refuse to answer if they no longer match what the installer serves.
#
# The signed SHA256SUMS is both the verification list and the list of files a node
# fetches from /install-assets/, so drifting from it is not harmless in either
# direction: a signed path the web image does not serve stops the install with a
# 404, and an asset that is served but not signed is placed on the node unverified.
#
# Everything here is derived from deploy/remote/install-agent.sh and from git, so a
# download added to the installer or a file added to a service tree fails this check
# instead of silently falling outside the signature.
#
# Usage:
#   deploy/tests/release-assets.sh --check    verify installer and release agree
#   deploy/tests/release-assets.sh --files    print the asset files to sign
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
installer="$repo_root/deploy/remote/install-agent.sh"
[ -f "$installer" ] || { printf '%s\n' "missing installer: $installer" >&2; exit 1; }

mode=check
if [ "$#" -gt 0 ]; then
  case "$1" in
    --check) mode=check ;;
    --files) mode=files ;;
    *) printf '%s\n' "unknown argument: $1" >&2; exit 2 ;;
  esac
fi

# Tracked files the web image deliberately does not serve. A signed path that
# cannot be downloaded fails the install, so these stay out of the release:
#   deploy/quick-start.sh             bootstraps the control plane and is fetched
#                                     straight from the repository by README
#   deploy/systemd/agent.env.example  a template for operators, not an Agent asset
excluded='deploy/quick-start.sh deploy/systemd/agent.env.example'

# What a release signs: the top level of deploy/ plus the two service manager
# trees. deploy/ is the repository's tooling tree, so deploy/tests and friends are
# never assets. find rather than git keeps this usable in CI containers that mount
# the checkout under another owner, where git refuses to inspect the repository,
# and it prints paths relative to the repository root. -maxdepth is kept on its own
# find call and -printf is avoided because Alpine ships BusyBox find, which
# supports neither nested use nor -printf. Hidden files are skipped because they
# are editor or version-control leftovers.
signable="$(
  cd "$repo_root"
  {
    find deploy/systemd deploy/openrc -maxdepth 1 -type f
    find deploy -maxdepth 1 -type f
    find examples/configs -type f
  } 2>/dev/null | sort -u \
    | grep -v '/\.' \
    | while IFS= read -r tracked; do
        case " $excluded " in *" $tracked "*) continue ;; esac
        printf '%s\n' "$tracked"
      done \
    | sort
)"

# What a node downloads: the installer's scattered loop, but only the files this
# release publishes, plus every unit in each manager tree the installer uses.
scattered="$(
  awk '/^for asset in \\$/ { inside = 1; next } inside && /^do$/ { exit } inside { print }' "$installer" \
    | tr -d ' \\\t' | grep -v '^$' | sort
)"
[ -n "$scattered" ] || { printf '%s\n' 'could not read the scattered asset loop from the installer' >&2; exit 1; }

units=""
for manager in systemd openrc; do
  grep -q "install-assets/deploy/$manager/" "$installer" || continue
  tree="$(printf '%s\n' "$signable" | grep "^deploy/$manager/" || true)"
  [ -n "$tree" ] || { printf '%s\n' "the installer downloads deploy/$manager but the release publishes none of it" >&2; exit 1; }
  units="$(printf '%s\n%s\n' "$units" "$tree" | grep -v '^$' | sort)"
done
[ -n "$units" ] || { printf '%s\n' 'the installer no longer downloads any service units' >&2; exit 1; }

expected="$(printf '%s\n%s\n' "$scattered" "$units" | grep -v '^$' | sort -u)"

if [ "$expected" != "$signable" ]; then
  printf '%s\n' 'the installer download list and the release assets differ (< installer, > release):' >&2
  diff <(printf '%s\n' "$expected") <(printf '%s\n' "$signable") >&2 || true
  exit 1
fi

case "$mode" in
  files)
    # Exactly the files to sign, one path per line, relative to the repository
    # root. The Makefile turns each into an -assets flag.
    printf '%s\n' "$signable"
    ;;
  check)
    printf '%s\n' "installer assets match the release set ($(printf '%s\n' "$signable" | wc -l) files)"
    ;;
esac
