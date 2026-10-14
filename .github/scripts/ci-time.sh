#!/bin/sh
set -eu
name=$1
shift
started=$(date +%s)
status=0
"$@" || status=$?
elapsed=$(( $(date +%s) - started ))
printf 'ci phase %s: %ss, status=%s\n' "$name" "$elapsed" "$status"
if [ -n "${QCH_CI_REPORT_DIR:-}" ]; then
  mkdir -p "$QCH_CI_REPORT_DIR"
  printf '%s\t%s\t%s\n' "$name" "$elapsed" "$status" >> "$QCH_CI_REPORT_DIR/phases.tsv"
fi
exit "$status"
