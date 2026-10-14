#!/bin/sh
# install-core-engines.sh — 下载并安装 mihomo / xray / sing-box / Shadowsocks Rust 官方 linux-amd64 二进制
# 用法（root）： sh deploy/remote/install-core-engines.sh
# 安装路径与 agent 默认 EngineSpec 一致：/usr/local/lib/qagent/cores/{mihomo,xray,sing-box,ssserver}
set -eu
