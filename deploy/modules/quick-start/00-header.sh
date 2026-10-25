#!/usr/bin/env bash
# QControlHub 一键交互式部署脚本（Linux）
#
# 提供两种部署方式：
#   bundled  — Docker Compose 内置 PostgreSQL + 控制面和网页服务（从零开始）
#   external — 连接已有 PostgreSQL，部署控制面和网页服务
#
# 用法：
#   ./deploy/quick-start.sh                          # 交互式选择部署方式
#   ./deploy/quick-start.sh -m bundled               # 全套部署（内置 PostgreSQL + 控制面和网页服务）
#   ./deploy/quick-start.sh -m external -d 'postgresql://user:pass@db:5432/qcontrolhub?sslmode=verify-full'

set -euo pipefail
