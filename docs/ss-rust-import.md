# SS Rust 脚本预设与导入

对应 [qimaoww/install-ss-rust](https://github.com/qimaoww/install-ss-rust) 的 `install_ss_rust.sh`。

不新增 CN 域名屏蔽选项或域名列表下载；已有 ACL 按原样保留，不擅自删除其中的规则。

## 预设

SS Rust 优先展示 Shadowsocks 2022，默认 `2022-blake3-aes-128-gcm`、匹配长度的随机 Base64 PSK、`::` 监听和随机高位端口。保留标准 AEAD 方案及全部三种 SS2022 算法。

新增配置使用 `servers` 多端口格式，默认 TCP + UDP、300 秒超时、关闭 Fast Open，启用 TCP_NODELAY。配置页按实际作用范围拆分：

- **当前端口选项**：监听地址、端口、密码、加密方法、SIP003 插件，以及转发模式、ACL、出站绑定 IP / 网卡、Linux 出站标记、UDP 分片和上游代理的端口覆盖。端口字段编辑只修改选中条目，保留其他端口与未知字段。
- **全局与默认值**：DNS（支持字符串或自定义对象）、IPv6 优先、TCP / UDP 超时、Fast Open、TCP_NODELAY、Keepalive、安全策略是全局设置；可被端口覆盖的字段单独标注为默认值。删除端口覆盖后继承全局值，不代表禁用全局值。空上游代理链也会继承全局链。
- **完整源码**：独立入口，包含 `servers` 列表与未收录的配置字段。混合格式或重复端口标识不接受自动端口编辑，但仍可通过完整源码修正。

作用范围按 [SS Rust v1.25.0 配置解析](https://github.com/shadowsocks/shadowsocks-rust/blob/v1.25.0/crates/shadowsocks-service/src/config.rs) 与 [服务端运行逻辑](https://github.com/shadowsocks/shadowsocks-rust/blob/v1.25.0/crates/shadowsocks-service/src/server/mod.rs) 核对：`servers[].dns` 被忽略；`servers[].timeout` 虽有字段定义，但该版本实际读取顶层 `timeout`，因此不提供端口级超时控件。HTTP(S) 上游代理跳点不转发 UDP，UDP 会直连；具体能力仍由节点真实内核校验。

保存端口预设不再保存 DNS / IPv6 等全局设置；新增、修改或删除端口都保留已有全局值及其缺省状态。旧式顶层单端口配置首次端口编辑时转换为 `servers` 格式，原全局默认值、插件与当前选择保留。不新增或下载 CN 域名规则。

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
