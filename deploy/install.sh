#!/usr/bin/env bash
# Bootstrap QControlHub from a single remote command, then hand off to the
# repository-owned interactive deployment workflow.

set -euo pipefail

REPOSITORY_URL="https://github.com/qimaoww/qcontrolhub.git"
REPOSITORY_BRANCH="main"
INSTALL_DIR="${QCH_INSTALL_DIR:-$PWD/qcontrolhub}"

die() {
    printf '错误：%s\n' "$*" >&2
    exit 1
}

case "$INSTALL_DIR" in
    /*) ;;
    *) INSTALL_DIR="$PWD/$INSTALL_DIR" ;;
esac
case "$INSTALL_DIR" in
    *$'\n'*|*$'\r'*) die "QCH_INSTALL_DIR 不能包含换行" ;;
esac

command -v git >/dev/null 2>&1 || die "缺少依赖：git"

if [ -e "$INSTALL_DIR" ]; then
    [ -d "$INSTALL_DIR/.git" ] || die "安装目录已存在但不是 Git 仓库：$INSTALL_DIR"
    origin_url="$(git -C "$INSTALL_DIR" remote get-url origin 2>/dev/null || true)"
    case "$origin_url" in
        https://github.com/qimaoww/qcontrolhub|https://github.com/qimaoww/qcontrolhub.git|git@github.com:qimaoww/qcontrolhub.git) ;;
        *) die "安装目录不是 QControlHub 官方仓库：$INSTALL_DIR" ;;
    esac
    branch="$(git -C "$INSTALL_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
    [ "$branch" = "$REPOSITORY_BRANCH" ] || die "安装目录必须位于 main 分支，当前为：${branch:-detached}"
    git -C "$INSTALL_DIR" diff --quiet || die "安装目录包含未提交修改，请先处理后再运行"
    git -C "$INSTALL_DIR" diff --cached --quiet || die "安装目录包含已暂存修改，请先处理后再运行"
    echo "-> 更新 QControlHub：$INSTALL_DIR"
    git -C "$INSTALL_DIR" fetch --prune origin "$REPOSITORY_BRANCH"
    git -C "$INSTALL_DIR" merge --ff-only FETCH_HEAD
else
    echo "-> 下载 QControlHub：$INSTALL_DIR"
    mkdir -p "$(dirname "$INSTALL_DIR")"
    git clone --depth 1 --branch "$REPOSITORY_BRANCH" --single-branch "$REPOSITORY_URL" "$INSTALL_DIR"
fi

quick_start="$INSTALL_DIR/deploy/quick-start.sh"
[ -x "$quick_start" ] || die "部署脚本不存在或不可执行：$quick_start"
exec "$quick_start" "$@"
