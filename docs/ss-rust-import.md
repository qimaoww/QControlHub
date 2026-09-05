# SS Rust 脚本预设与导入

对应 [qimaoww/install-ss-rust](https://github.com/qimaoww/install-ss-rust) 的 `install_ss_rust.sh`。

不新增 CN 域名屏蔽选项或域名列表下载；已有 ACL 按原样保留，不擅自删除其中的规则。

## 预设

SS Rust 优先展示 Shadowsocks 2022，默认 `2022-blake3-aes-128-gcm`、匹配长度的随机 Base64 PSK、`::` 监听和随机高位端口。保留标准 AEAD 方案及全部三种 SS2022 算法。

新增配置使用 `servers` 多端口格式，默认 TCP + UDP、300 秒超时、关闭 Fast Open，启用 TCP_NODELAY。出站绑定 IP 是端口级选项，留空删除端口覆盖值；DNS 和 IPv6 优先是全局选项，新端口方案继承已有全局值。修改已有多端口配置保留其他端口、自定义字段、ACL、转发模式、超时及全局性能设置。自定义 DNS 对象通过源码编辑，DNS 文本框留空不会清除已有对象。

注意：[官方 v1.25.0 配置结构](https://github.com/shadowsocks/shadowsocks-rust/blob/v1.25.0/crates/shadowsocks-service/src/config.rs) 仅支持根字段 `dns`，不支持 `servers[].dns`。原脚本写入的端口级 DNS 会被官方内核忽略；导入原样保留该字段，但不将其提升为全局 DNS，以免改变其他端口行为。需要指定解析器时请设置全局 DNS。

## 导入已有安装

1. 更新并重启节点 QAgent。Agent 启动时只读发现 systemd 的活动 `shadowsocks-rust.service`，识别 `/usr/local/bin/ssserver -c /etc/shadowsocks-rust/config.json`，以及可选的 `--acl /etc/shadowsocks-rust/block_cn.acl`。
2. 在节点的 SS Rust 卡片点击“可导入”，读取系统服务配置快照。
3. 检查后点击“手动导入并迁移”。只有此操作才会复制二进制和配置、停止并禁用旧服务、启动并启用 `qagent-shadowsocks-rust.service`。失败恢复原有二进制、配置和服务状态；重复提交已完成的导入不会重复切换服务。

如果旧节点曾报 `refusing to disable an unrecognized qagent-shadowsocks-rust.service`，且旧缓存脚本没有 `qagent_ssrust_`，应先更新控制面到包含安装资源包的构建，再在面板升级 Agent。新版 Agent 会使用自身内置的匹配资源，不再需要手工替换缓存脚本。确认原服务仍活动、托管服务未运行后，重新读取快照并导入；若仍报安全校验错误，检查节点返回结果，不要删除服务或跳过校验。

ACL 会复制到 `/etc/qagent/shadowsocks-rust/install-ss-rust/<摘要>/block_cn.acl`，并绑定到每个使用该策略的端口，避免被 QAgent 的全局 ACL 参数覆盖。此目录在旧 Agent 沙箱的既有可写范围内，文件由 root 所有、仅服务组可读；不再尝试写入 Agent 无权修改的内核状态目录。原有 ACL 和配置不删除；新增端口继承导入的全局 ACL。快照路径包含配置和 ACL 内容摘要，保存后原文件变化会要求重新读取快照。未知 ACL 路径、符号链接、不安全文件权限、额外启动参数和插件进程均拒绝自动迁移。

SS Rust 没有不启动服务的离线检查模式：导入前检查 JSON、文件权限及精确服务映射，启动后确认服务稳定，失败回滚；不声称已完成真实内核离线校验。

## 边界与回退

- 脚本的入站 CN 防火墙、自有 nftables / iptables 对象及 `shadowsocks-rust-inbound-cn-block.service` 保持原状，不归 QAgent 管理。它们仍按旧配置的端口工作；修改端口前需由管理员在原脚本关闭旧入站规则，再按需要配置面板入站控制。
- 脚本 `.env` 日志等级不迁移；QAgent 使用集中日志与 `RUST_LOG=info`。ACL 是导入时的独立副本，后续原脚本更新 ACL 不会同步到 QAgent。
- 此脚本导入只支持 systemd；不扩展到 OpenRC、自定义 wrapper 参数或其他安装脚本布局。
- 成功迁移后如需人工回退，先停止并禁用 `qagent-shadowsocks-rust.service`，再启用和启动原 `shadowsocks-rust.service`；原脚本文件仍在。面板完成标记需要另行核对，不应在两个服务同时运行时重试导入。
