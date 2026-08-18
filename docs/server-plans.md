# 服务端入站方案

“远程节点”中的配置入口面向服务端，不要求管理员从空白 YAML/JSON 开始编写。管理员可以先通过 WSS 读取节点白名单路径中的实际配置文件；只有文件权限安全、结构正确且通过目标节点真实内核校验时，控制面才会把它作为短期草稿载入手动源码编辑器。

结构化入站和根字段编辑明确区分“增加 / 修改 / 删除”：增加遇到同名目标会失败，修改或删除找不到现有目标也会失败，不会从修改静默退化为新增。操作只改变选中的入站或根字段，其他入站、未知字段和全局自定义内容会保留；节点编辑不提供未经校验的保存按钮，每次提交必须创建真实内核校验或部署任务。

## 当前方案

| 方案 | Mihomo | Xray | sing-box | Shadowsocks Rust | 默认随机内容 |
| --- | --- | --- | --- | --- | --- |
| Shadowsocks | 否 | 否 | 否 | 是 | 高位端口、密码、标准 AEAD 方法 |
| Shadowsocks 2022 | 是 | 是 | 是 | 是 | 高位端口、用户名、16/32 字节 Base64 PSK |
| VLESS Vision + Reality | 是 | 是 | 是 | 否 | 高位端口、UUID、X25519 密钥对、Short ID |
| VMess + WebSocket + TLS | 是 | 是 | 是 | 否 | 高位端口、UUID、WebSocket 路径 |
| Trojan + TLS | 是 | 是 | 是 | 否 | 高位端口、用户名、密码 |
| Hysteria 2 + TLS | 是 | 是（官方 `hysteria` v2） | 是 | 否 | 高位端口、用户名、密码 |
| TUIC v5 + TLS | 是 | 否 | 是 | 否 | 高位端口、UUID、密码 |
| AnyTLS | 是 | 否 | 是 | 否 | 高位端口、用户名、密码 |

随机端口来自 20000–49151。密码、PSK、UUID、路径、X25519 密钥和 Short ID 均使用 Go `crypto/rand`。点击“重新随机”会创建一套新值；页面中的所有方案字段仍可自定义。

## 生成和部署

生成器按内核输出原生格式：Mihomo 使用 `listeners` YAML，Xray 和 sing-box 使用 `inbounds` JSON，Shadowsocks Rust 使用官方单服务端 JSON，并补齐必要的转发参数。保存时执行以下检查：

- 节点必须存在并声明对应内核能力；
- 配置固定绑定到该节点和内核，不能部署到其他 Agent；
- 使用乐观版本号拒绝并发覆盖；
- 端口、标签、监听地址、密码长度、SS2022 PSK 长度、UUID、Reality 密钥配对和 TLS 必填项由服务端校验；
- Reality SNI 保存前会实时解析 DNS、拒绝私网/保留地址及 Cloudflare CNAME/IP，并固定连接解析出的公网 IP完成 TLS 1.3 证书校验，避免 DNS 重绑定与内网 SSRF；
- 已知不再适用的 Microsoft Reality SNI 即使仍能完成普通 TLS 握手也会被明确拒绝；
- 所有写操作要求有效 Web 会话和 CSRF Token；
- “校验”和“部署”任务只通过已认证的 WSS Agent 通道下发。

Agent 收到部署任务后仍会调用目标内核自身的配置检查命令。语法生成成功不代表端口一定空闲、证书路径一定存在或防火墙已经放行，因此正式部署前应先执行“保存并校验”。

## 客户端接入资料

方案保存后，页面中的“客户端接入”区可根据客户端实际访问的域名或 IP 生成分享 URI，并同时列出服务器、端口、认证、传输、TLS / Reality 等逐项参数。连接地址和 TLS ServerName 只保留在当前页面 URL，不写入内核配置，也不会成为配置版本的一部分。

当前会为 Shadowsocks 2022、VLESS、VMess、Trojan、Hysteria 2、TUIC v5 和 AnyTLS 生成对应 URI。客户端对分享格式的支持可能因产品和版本不同而变化；无法直接导入时，应使用页面列出的逐项参数。URI 和认证字段默认以密码输入框遮罩，复制时无需先显示。

客户端资料只包含连接所需的公开参数与用户凭据。Reality 服务端 Private Key、TLS 私钥路径和证书路径不会进入客户端 URI 或逐项参数。页面不会代替网络侧配置；部署完成后仍需确认 DNS 指向、证书覆盖域名，以及主机和上游防火墙已放行方案使用的 TCP / UDP 端口。

## 官方依据

- [Mihomo listeners](https://wiki.metacubex.one/config/inbound/listeners/)
- [Xray inbounds](https://xtls.github.io/config/inbound.html)
- [sing-box inbounds](https://sing-box.sagernet.org/configuration/inbound/)
- [sing-box JSON Schema](https://sing-box.sagernet.org/schema.json)

页面底部的“高级配置与完整官方目录”保留全部顶层配置、官方入站类型链接及完整源码编辑器，用于方案之外的字段和新版本选项。
