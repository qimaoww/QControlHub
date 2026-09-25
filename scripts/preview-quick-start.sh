#!/usr/bin/env bash
# Local sample output only: no Docker commands, network access or state writes.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$repo_root/deploy/modules/quick-start/40-update-rollback.sh"
source "$repo_root/deploy/modules/quick-start/60-menu.sh"
WORK_DIR=/opt/qcontrolhub
ENV_FILE="$WORK_DIR/.env"
SECRET_DIR="$WORK_DIR/.secrets"

# These are illustrative image labels, not installed or published versions.
update_image_version() {
    case "$1" in
        sample-control) printf 'a4d8c2e71930' ;;
        sample-web) printf 'e2f641a098bc' ;;
        *) return 1 ;;
    esac
}

preview_update() {
    local state="$1" target=sample-control version=a4d8c2e71930
    ui_heading "更新检查"
    ui_text 2 "  更新内置 PostgreSQL 部署并复用现有配置"
    show_current_application_versions sample-control sample-web
    ui_service_row PostgreSQL "当前版本：镜像 c69214df85ab"
    show_update_targets ghcr.io/qimaoww/qcontrol-plane:latest ghcr.io/qimaoww/qcontrol-web:latest postgres:17-alpine
    ui_section "正在检查更新（拉取目标镜像）..."
    ui_text 2 "  [预览] 此处省略 Docker 拉取输出"
    ui_section "镜像对比结果"
    if [ "$state" = update ]; then
        target=sample-next
        version=b7f913c084d2
    fi
    show_target_image_version control-plane sample-control "$target" "$version"
    show_target_image_version qcontrol-web sample-web sample-web e2f641a098bc
    show_target_image_version PostgreSQL sample-postgres sample-postgres '镜像 c69214df85ab'
    if [ "$state" = unchanged ]; then
        show_no_update_result
    else
        printf '\n-> 检测到镜像变化，开始更新\n'
    fi
}

preview_screen() {
    case "$1" in
        menu) show_action_menu; printf '  请选择 [1-4] > \n' ;;
        mode) show_mode_menu; printf '  请选择 [1-2] > \n' ;;
        update|unchanged) preview_update "$1" ;;
        result) show_result 更新完成 http://127.0.0.1:8080 "docker compose --project-directory $WORK_DIR --env-file $ENV_FILE -f $WORK_DIR/docker-compose.yml down" ;;
        *) printf '用法：bash scripts/preview-quick-start.sh [menu|mode|update|unchanged|result|all]\n' >&2; exit 2 ;;
    esac
}

printf '布局预览 · 示例数据，不执行部署\n'
if [ "${1:-menu}" = all ]; then
    for screen in menu mode update unchanged result; do
        preview_screen "$screen"
    done
else
    preview_screen "${1:-menu}"
fi
