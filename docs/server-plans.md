# 服务端入站方案

“远程节点”中的配置入口面向服务端，不要求管理员从空白 YAML/JSON 开始编写。管理员可以先通过 WSS 读取节点白名单路径中的实际配置文件；只有文件权限安全、结构正确且通过目标节点真实内核校验时，控制面才会把它作为短期草稿载入手动源码编辑器。

结构化入站和根字段编辑明确区分“增加 / 修改 / 删除”：增加遇到同名目标会失败，修改或删除找不到现有目标也会失败，不会从修改静默退化为新增。操作只改变选中的入站或根字段，其他入站、未知字段和全局自定义内容会保留；节点编辑不提供未经校验的保存按钮，每次提交必须创建真实内核校验或部署任务。

## 当前方案

| 方案 | Mihomo | Xray | sing-box | Shadowsocks Rust | 默认随机内容 |
| --- | --- | --- | --- | --- | --- |
| Shadowsocks | 否 | 否 | 否 | 是 | 高位端口、密码、标准 AEAD 方法 |
| Shadowsocks 2022 | 是 | 是 | 是 | 是 | 高位端口、用户名、16/32 字节 Base64 PSK |
| VLESS Vision + Reality | 是 | 是 | 是 | 否 | 高位端口、UUID、X25519 密钥对、Short ID |
| VLESS-ENC + TCP + Reality + Vision | 是 | 是 | 否 | 否 | 高位端口、UUID、VLESS-ENC X25519 密钥对、Reality X25519 密钥对、Short ID |
| VLESS-ENC + XHTTP + Reality + Vision | 是 | 是 | 否 | 否 | 高位端口、UUID、随机 XHTTP 路径、VLESS-ENC X25519 密钥对、Reality X25519 密钥对、Short ID |
| VMess + WebSocket + TLS | 是 | 是 | 是 | 否 | 高位端口、UUID、WebSocket 路径 |
| Trojan + TLS | 是 | 是 | 是 | 否 | 高位端口、用户名、密码 |
| Hysteria 2 + TLS | 是 | 是（官方 `hysteria` v2） | 是 | 否 | 高位端口、用户名、密码 |
| TUIC v5 + TLS | 是 | 否 | 是 | 否 | 高位端口、UUID、密码 |
| AnyTLS | 是 | 否 | 是 | 否 | 高位端口、用户名、密码 |
| Snell v5 | 是 | 否 | 否 | 否 | 高位端口、PSK；默认 UDP、连接复用和客户端 TCP Fast Open |
| Snell v5 + ShadowTLS v3 | 是 | 否 | 否 | 否 | Snell v5 参数、独立 ShadowTLS 用户与强密码；默认可信握手目标和严格模式 |
| Sudoku | 是 | 否 | 否 | 否 | 高位端口、上游兼容 Ed25519 Master Public / Available Private 分割密钥、AEAD、5–15% Padding、HTTPMask |
| 端口转发 | 是（`tunnel` listener） | 是（`tunnel` inbound） | 是（`direct` inbound） | 否 | 高位监听端口；默认转发到 `127.0.0.1:80`，可选 TCP、UDP 或双协议 |

随机端口来自 20000–49151。密码、PSK、UUID、路径、X25519 密钥和 Short ID 均使用 Go `crypto/rand`。点击“重新生成参数”会直接读取当前表单并只替换随机字段，不会重载页面或恢复协议默认值；例如当前选择 SS2022 AES-128 时会保留该方法并生成匹配的 16 字节 PSK，端口转发方案会保留当前目标地址、目标端口和网络协议。标签、端口、用户名、凭据、路径、Reality 密钥对和 Short ID 也提供就地生成按钮，其中密钥对始终原子更新 Public Key 与 Private Key。页面中的所有方案字段仍可自定义。

Mihomo 与 Xray 的 VLESS-ENC 预设使用独立的 X25519 密钥对：服务端配置只保存 `decryption` 私有值，客户端分享资料只导出 `encryption` 公开值。Xray Reality 还可自定义 `minClientVer` 和可选的 `mldsa65Seed`；启用 ML-DSA-65 时，保存前会按 `xray tls ping` 的口径计算 target 实际发送的 DER 证书链总长度，要求严格大于 3500 bytes，并要求协商 `X25519MLKEM768`。不满足时拒绝保存；未启用 ML-DSA-65 的普通 Reality 不受此限制。

Snell 预设只生成 Mihomo 当前支持的 v5，不提供旧版本或 v6 字段。ShadowTLS 方案固定 v3，PSK 与 ShadowTLS 密码相互独立，服务端启用严格模式，客户端不生成证书校验绕过。Sudoku 预设只提供 `chacha20-poly1305` 和 `aes-128-gcm`，不提供无 AEAD 的 `none`；服务端只保存 Master Public Key，64 字节 Available Private Key 按配置版本和入站标签单独加密保存，仅用于生成客户端 YAML。HTTPMask 服务端固定使用上游推荐的 `auto`，客户端可选经过 Mihomo 双端真实流量验证的 `stream`、`poll`、`auto` 或 `ws`；当前 Mihomo 1.19.30 的 `legacy`、`custom-table` 与 `custom-tables` 虽能通过配置检查，但双端传输会失败或损坏响应，预设因此不提供。四种内置 Table Type、raw TCP、纯/压缩下行、两种安全 AEAD 与原生 `multiplex` 均已验证；原生复用不与通用 SMux 或 TCP Brutal 叠加。

## 生成和部署

生成器按内核输出原生格式：Mihomo 使用 `listeners` YAML，Xray 和 sing-box 使用 `inbounds` JSON，Shadowsocks Rust 使用官方单服务端 JSON。端口转发分别生成 Mihomo `tunnel` listener、Xray `tunnel` inbound 和 sing-box `direct` inbound，并把统一的 TCP / UDP 选择转换为各内核的原生字段。保存时执行以下检查：

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

当前会为 Shadowsocks 2022、VLESS、VMess、Trojan、Hysteria 2、TUIC v5 和 AnyTLS 生成对应 URI；Snell v5、Snell v5 + ShadowTLS v3 与 Sudoku 生成可直接放入 Mihomo `proxies` 的单节点 YAML。端口转发是节点侧监听与目标映射，不生成代理客户端分享资料。客户端对分享格式的支持可能因产品和版本不同而变化；无法直接导入时，应使用页面列出的逐项参数。URI、YAML 和认证字段默认以密码输入框遮罩，复制时无需先显示。

客户端资料只包含连接所需的公开参数与用户凭据。Reality 服务端 Private Key、TLS 私钥路径和证书路径不会进入客户端 URI 或逐项参数。页面不会代替网络侧配置；部署完成后仍需确认 DNS 指向、证书覆盖域名，以及主机和上游防火墙已放行方案使用的 TCP / UDP 端口。

## 官方依据

- [Mihomo listeners](https://wiki.metacubex.one/config/inbound/listeners/)
- [Mihomo tunnel listener](https://wiki.metacubex.one/config/inbound/listeners/tunnel/)
- [Mihomo Snell listener](https://wiki.metacubex.one/config/inbound/listeners/snell/)
- [Mihomo Snell proxy](https://wiki.metacubex.one/config/proxies/snell/)
- [Mihomo Sudoku listener](https://wiki.metacubex.one/config/inbound/listeners/sudoku/)
- [Mihomo Sudoku proxy](https://wiki.metacubex.one/config/proxies/sudoku/)
- [SUDOKU-ASCII upstream configuration](https://github.com/SUDOKU-ASCII/sudoku/blob/main/configs/README.zh_CN.md)
- [Xray inbounds](https://xtls.github.io/config/inbound.html)
- [Xray tunnel inbound](https://xtls.github.io/config/inbounds/tunnel.html)
- [sing-box inbounds](https://sing-box.sagernet.org/configuration/inbound/)
- [sing-box direct inbound](https://sing-box.sagernet.org/configuration/inbound/direct/)
- [sing-box JSON Schema](https://sing-box.sagernet.org/schema.json)

页面底部的“高级配置与完整官方目录”保留全部顶层配置、官方入站类型链接及完整源码编辑器，用于方案之外的字段和新版本选项。
