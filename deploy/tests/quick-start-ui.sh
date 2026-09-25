#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Source only rendering functions: this test never needs Docker or deployment state.
source "$repo_root/deploy/modules/quick-start/40-update-rollback.sh"
source "$repo_root/deploy/modules/quick-start/60-menu.sh"

export COLUMNS=72
WORK_DIR='/opt/qcontrolhub download'
ENV_FILE="$WORK_DIR/.env"
SECRET_DIR="$WORK_DIR/.secrets"
menu="$(show_action_menu)"
grep -Fq -- "$WORK_DIR" <<< "$menu"
grep -Fq '[2] 更新现有部署' <<< "$menu"
grep -Fq '保留配置、密钥和数据库卷' <<< "$menu"
if [[ "$menu" == *$'\033'* ]]; then
    printf '%s\n' 'redirected menu contains ANSI escapes' >&2
    exit 1
fi

# Narrow terminals stack service names above values; invalid widths stay bounded.
expected=$'  control-plane\n    当前版本：sample-build'
[ "$(COLUMNS=40 ui_service_row control-plane '当前版本：sample-build')" = "$expected" ]
[ "$(COLUMNS=40 ui_rule | wc -L)" -eq 40 ]
[ "$(COLUMNS=9999 ui_width)" -eq 72 ]
[ "$(COLUMNS=bad ui_width)" -eq 72 ]
[ "$(COLUMNS=0 ui_width)" -eq 72 ]

# Status follows image identity, even when version labels happen to match.
grep -Fq '[无变化]' <<< "$(show_target_image_version control-plane sha256:a sha256:a same-label)"
grep -Fq '[有更新]' <<< "$(show_target_image_version control-plane sha256:a sha256:b same-label)"
grep -Fq '[待启动]' <<< "$(show_target_image_version control-plane '' sha256:b same-label)"

# Replacing the final subcommand must not corrupt a directory containing 'down'.
stop_cmd="docker compose --project-directory '$WORK_DIR' down"
result="$(show_result 更新完成 http://127.0.0.1:8080 "$stop_cmd")"
grep -Fq -- "docker compose --project-directory '$WORK_DIR' logs -f" <<< "$result"
grep -Fq -- "$stop_cmd" <<< "$result"

# A new token appears once and is cleared; later summaries never redisplay it.
output="$(
    ADMIN_TOKEN_TO_DISPLAY='preview-token-only-once'
    show_admin_token_once
    [ -z "$ADMIN_TOKEN_TO_DISPLAY" ]
    show_admin_token_once
    show_result 部署完成 http://127.0.0.1:8080 "$stop_cmd"
)"
[ "$(grep -Fc preview-token-only-once <<< "$output")" -eq 1 ]
printf '%s\n' 'quick-start terminal layout regression passed'
