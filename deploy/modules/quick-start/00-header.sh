#!/usr/bin/env bash
# QControlHub 一键交互式部署脚本（Linux）
#
# 提供两种部署方式：
#   bundled  — Docker Compose 内置 PostgreSQL + 控制面（从零开始）
#   external — 连接已有 PostgreSQL，仅部署控制面容器
#
# 用法：
#   ./deploy/quick-start.sh                          # 交互式选择部署方式
#   ./deploy/quick-start.sh -m bundled               # 全套部署（内置 PostgreSQL + 控制面）
#   ./deploy/quick-start.sh -m external -d 'postgresql://user:pass@db:5432/qcontrolhub?sslmode=verify-full'

set -euo pipefail
