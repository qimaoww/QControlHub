# 服务端入站方案

配置页保留节点侧栏、顶部内核栏、公共/入站文件切换、源码编辑和合并预览。点击文件卡片只切换源码或选中目标，不自动打开弹窗。“＋ 增加入站”独立放在源码工具栏的“合并预览”左侧，始终显示，不受是否首次配置或当前文件选择影响；没有合并预览的内核也在同一工具栏提供该按钮。“限制”旁的操作菜单随选择切换：选中入站时提供“修改入站 / 删除入站”，新增和修改复用预设表单弹窗，删除先确认当前选中的标签与端口；选中公共配置时始终只提供“增加通用配置项 / 修改通用配置项 / 删除通用配置项”。合并预览不视为公共配置，也不可修改或删除入站。高级字段、版本历史、已部署差异及客户端入口位于源码编辑区下方，不再提供独立的内核预设导航。

通用配置弹窗使用配置项下拉选择与字段表单：增加只列出尚未设置的项，修改和删除只列出已设置的项，操作类型由菜单确定。删除先只读展示当前值，再确认所选字段与校验/部署影响；不会删除整个公共文件。切换字段保留各自草稿，草稿按节点、内核、版本、字段和操作隔离。入站结构（包括 Mihomo 顶层监听器、sing-box `endpoints`）和包含成套出口的 `outbounds` 不作为通用字段操作，仍通过入站、源码或高级字段编辑；SS Rust 只展示全局与可覆盖默认值，不混入端口字段。

管理员可以先通过 WSS 读取节点白名单路径中的实际配置文件；只有文件权限安全、结构正确且通过目标节点真实内核校验时，控制面才会把它作为短期快照载入源码编辑器。源码有未保存修改，或节点快照与数据库当前版本不同，须先保存并核对源码，才能使用入站或字段操作。旧 `#agents`、`#preset-node-{id}`、`#agent-config?agent=...&engine=...` 链接兼容跳转到配置页；`#node-{id}` 仍打开节点设置。

结构化入站和根字段编辑明确区分“增加 / 修改 / 删除”：增加遇到同名目标会失败，修改或删除找不到现有目标也会失败，不会从修改静默退化为新增。操作只改变选中的入站或根字段，其他入站、未知字段和全局自定义内容会保留；入站与字段表单提交必须创建校验或部署任务，普通账号仍可独立保存自己的源码草稿。

## 当前方案

| 方案 | Mihomo | Xray | sing-box | Shadowsocks Rust | 默认随机内容 |
| --- | --- | --- | --- | --- | --- |
| Shadowsocks | 否 | 否 | 否 | 是 | 高位端口、密码、标准 AEAD 方法 |
| Shadowsocks 2022 | 是 | 是 | 是 | 是 | 高位端口、用户名、16/32 字节 Base64 PSK |
| VLESS-Vision-uTLS-REALITY | 是 | 是 | 是 | 否 | 高位端口、UUID、X25519 密钥对、Short ID |
| VLESS-XHTTP-uTLS-REALITY | 是 | 是 | 否 | 否 | 高位端口、UUID、随机 XHTTP 路径、X25519 密钥对、Short ID |
| VLESS+ENC+TCP（不启用 TLS / Reality） | 是 | 是 | 否 | 否 | 高位端口、UUID、VLESS-ENC X25519 密钥对 |
| VLESS-ENC-TCP-Vision-uTLS-REALITY | 是 | 是 | 否 | 否 | 高位端口、UUID、VLESS-ENC X25519 密钥对、Reality X25519 密钥对、Short ID |
| VLESS-ENC-XHTTP-Vision-uTLS-REALITY | 是 | 是 | 否 | 否 | 高位端口、UUID、随机 XHTTP 路径、VLESS-ENC X25519 密钥对、Reality X25519 密钥对、Short ID |
| VMess + WebSocket + TLS | 是 | 是 | 是 | 否 | 高位端口、UUID、WebSocket 路径 |
| Trojan + TLS | 是 | 是 | 是 | 否 | 高位端口、用户名、密码 |
| Hysteria 2 + TLS | 是 | 是（官方 `hysteria` v2） | 是 | 否 | 高位端口、用户名、密码 |
| TUIC v5 + TLS | 是 | 否 | 是 | 否 | 高位端口、UUID、密码 |
| AnyTLS | 是 | 否 | 是 | 否 | 高位端口、用户名、密码 |
| Snell v5 | 是 | 否 | 否 | 否 | 高位端口、PSK；默认 UDP、连接复用和客户端 TCP Fast Open |
| Snell v5 + ShadowTLS v3 | 是 | 否 | 否 | 否 | Snell v5 参数、独立 ShadowTLS 用户与强密码；默认可信握手目标和严格模式 |
| Mieru | 是 | 否 | 否 | 否 | 高位端口、随机用户名与密码；默认 TCP，可选 UDP，导出匹配的 Mihomo YAML |
| Sudoku | 是 | 否 | 否 | 否 | 高位端口、上游兼容 Ed25519 Master Public / Available Private 分割密钥、AEAD、5–15% Padding、HTTPMask |
| WireGuard | 否 | 是 | 是（原生 endpoint） | 否 | 高位 UDP 端口、独立的服务端/客户端 X25519 密钥对、预共享密钥 |
| 端口转发 | 是（`tunnel` listener） | 是（`tunnel` inbound） | 是（`direct` inbound） | 否 | 高位监听端口；默认转发到 `127.0.0.1:80`，可选 TCP、UDP 或双协议 |

随机端口来自 20000–49151。密码、PSK、UUID、路径、X25519 密钥和 Short ID 均使用 Go `crypto/rand`。点击“重新生成参数”会直接读取当前表单并只替换随机字段，不会重载页面或恢复协议默认值；例如当前选择 SS2022 AES-128 时会保留该方法并生成匹配的 16 字节 PSK，端口转发方案会保留当前目标地址、目标端口和网络协议。标签、端口、用户名、凭据、路径、Reality 密钥对和 Short ID 也提供就地生成按钮，其中密钥对始终原子更新 Public Key 与 Private Key。页面中的所有方案字段仍可自定义。

Mihomo 与 Xray 的 VLESS-ENC 预设使用独立的 X25519 密钥对：服务端配置只保存 `decryption` 私有值，客户端分享资料只导出 `encryption` 公开值。其中的“VLESS+ENC+TCP”不生成 TLS、Reality 与 Vision Flow：Xray 只在启用 VLESS Encryption 时才允许 `security: "none"`，Mihomo 的 vless listener 也把 `decryption` 视为与证书、Reality 同级的必要项，因此这类入站可以裸跑原生 TCP。sing-box 官方内核的 VLESS inbound 没有 `decryption` 字段，因此不提供该方案。Reality 的 ML-DSA-65 只在 Xray 上提供：Xray 自定义 `minClientVer` 和可选的 `mldsa65Seed`，Mihomo 的 `reality-config`、`reality-opts` 与 sing-box 的 `tls.reality` 都没有对应字段，预设因此只在 Xray 的 Reality 方案上显示 ML-DSA-65 字段；Mihomo 客户端 YAML 不写入该参数，但节点照常同步，因为服务端启用它不影响不校验的客户端，需要校验的客户端仍可使用分享 URL 的 `pqv`。启用 ML-DSA-65 时，保存前会按 `xray tls ping` 的口径计算 target 实际发送的 DER 证书链总长度，要求严格大于 3500 bytes，并要求协商 `X25519MLKEM768`。不满足时拒绝保存；未启用 ML-DSA-65 的普通 Reality 不受此限制。

Mihomo 把后量子密钥交换放在显式开关后面，因此同步格式会为每个 Reality 节点写入 `reality-opts.support-x25519mlkem768: true`：它只表示客户端愿意使用 X25519MLKEM768，target 支持时 Reality 就会协商该混合组，不支持时照常回退 X25519；非 Reality 节点不会出现 `reality-opts`。该字段与 ML-DSA-65 无关，后者需要在 Reality 握手时校验后量子签名，Mihomo 没有对应实现。

Xray Reality 的 `minClientVer` 始终显式写入，默认 `0.0.0`。Xray v26.7.11 起省略该字段会默认要求客户端版本不低于 `26.3.27`，而 Mihomo 在 REALITY ClientHello 中固定声明 `1.8.2`，握手会在进入 VLESS 之前被拒绝并报 `REALITY authentication failed`；显式写入可跨版本避开这一默认值，v26.9.9 已在代码中注释掉该默认值与相关告警，此时省略与显式 `0.0.0` 等价。Reality 目标域名还应避开 `.ru`、`.ir`、`.cn` 后缀与 `apple`、`icloud`、`microsoft`：v26.9.9 会为这类 target 输出“增加 IP 被 GFW 封锁概率”的告警，默认的 `www.amazon.com` 不在其中。

Mieru 预设提供单端口、单用户的 TCP / UDP 服务端，无需 TLS 证书；客户端同时支持代理 TCP 与 UDP 流量。传输默认为 TCP，重新生成参数时保留所选传输。多用户、端口范围、`traffic-pattern` 和 `user-hint-is-mandatory` 等自定义配置保留在完整源码中，不作为预设回读。需要支持 Mieru listener 的 Mihomo 版本。

Snell 预设只生成 Mihomo 当前支持的 v5，不提供旧版本或 v6 字段。ShadowTLS 方案固定 v3，PSK 与 ShadowTLS 密码相互独立，服务端启用严格模式，客户端不生成证书校验绕过。Sudoku 预设只提供 `chacha20-poly1305` 和 `aes-128-gcm`，不提供无 AEAD 的 `none`；服务端只保存 Master Public Key，64 字节 Available Private Key 按配置版本和入站标签单独加密保存，仅用于生成客户端 YAML。HTTPMask 服务端固定使用上游推荐的 `auto`，客户端可选经过 Mihomo 双端真实流量验证的 `stream`、`poll`、`auto` 或 `ws`；当前 Mihomo 1.19.30 的 `legacy`、`custom-table` 与 `custom-tables` 虽能通过配置检查，但双端传输会失败或损坏响应，预设因此不提供。四种内置 Table Type、raw TCP、纯/压缩下行、两种安全 AEAD 与原生 `multiplex` 均已验证；原生复用不与通用 SMux 或 TCP Brutal 叠加。

### WireGuard 与原生端点

WireGuard 预设面向单客户端服务端：Xray 使用原生 `wireguard` inbound，sing-box 使用
`system: false` 的用户态 `wireguard` endpoint。sing-box 需 1.11+，且二进制同时包含
`with_wireguard`、`with_gvisor`；Agent 先检查实际版本和构建标签，再执行原生配置检查。
sing-box 只提供 `listen_port`，固定监听所有接口，不提供可被忽略的监听地址选项。
预设不创建系统网卡、不调整主机路由，也不自动放行防火墙。

默认客户端地址为 `10.66.66.2/32`、客户端路由为 `0.0.0.0/0`；sing-box 服务端隧道地址为
`10.66.66.1/24`。可配置一个 IPv4 和/或一个 IPv6 客户端地址，须使用 `/32` 或 `/128`，
sing-box 服务端须配置对应地址族且不能与客户端使用同一地址。启用 IPv6 时 MTU 至少为 1280。
预共享密钥可留空；客户端 Keepalive 默认 25 秒，明确填写 0 会关闭并在保存、回填和导出时保留。
Keepalive 不写入没有固定远端地址的服务端 peer。
早期草稿若生成了非零的 `peers[].persistent_keepalive_interval`，需先在源码编辑器删除该
服务端字段再保存，才能重新使用预设表单；客户端 Keepalive 仍从匹配的加密元数据恢复。

客户端私钥只按配置版本和标签单独加密保存，不进入节点配置或 Agent 任务。
服务端私钥、客户端公钥、PSK、地址和 MTU 以源配置为准；客户端元数据只补充匹配当前 peer
的私钥、客户端路由和 Keepalive。源配置轮换 peer 公钥后，旧私钥不再自动回填；
须提供匹配私钥或重新生成密钥对。恢复历史版本会恢复该版本对应的客户端元数据。
密钥对的就地生成按钮原子更新公私钥，修改、改名、删除及任务创建与版本检查在同一事务内完成。

sing-box 的入站和端点共用页面操作，但保留原生列表：端点及其专用出口位于
`endpoints/<tag>.json`，普通入站位于 `inbounds/<tag>.json`。跨列表重复标签或非零监听端口
会拒绝。多 peer、带远端拨号地址、系统接口或其他自定义端点不转换成可编辑预设，
须使用完整源码编辑；无法证明独立出口归属的配置不会被宣称支持双链路计量。
用户态服务端 WireGuard 会生成实际的按端口标记出口，并支持与普通入站混合配置。
Agent 新端点分片使用不可变的 `sources-v4-<sha256>`，旧 `sources-v3-*` 不改写；
校验失败不激活配置，重启失败恢复上一份运行配置。

Tailscale、OpenVPN Server 暂不开放预设、登录或客户端导出入口。原生端点可作为源码被识别和
分片保留，但这不表示其部署、登录状态、证书生命周期或中继计量已受支持；这些能力仍待后续验收。

## 生成和部署

生成器按内核输出原生格式：Mihomo 使用 `listeners` YAML，Xray 和 sing-box 使用 `inbounds` JSON，sing-box WireGuard 使用原生 `endpoints` JSON。Shadowsocks Rust 的单入站片段在新增时合并为官方 `servers` 多端口 JSON；向旧单端口配置新增时自动保留旧端口并转换为多端口，重复端口会拒绝。端口转发分别生成 Mihomo `tunnel` listener、Xray `tunnel` inbound 和 sing-box `direct` inbound，并把统一的 TCP / UDP 选择转换为各内核的原生字段。保存时执行以下检查：

- 节点必须存在并声明对应内核能力；
- 配置固定绑定到该节点和内核，不能部署到其他 Agent；
- 使用乐观版本号拒绝并发覆盖；
- 端口、标签、监听地址、密码长度、SS2022 PSK 长度、UUID、Reality 密钥配对和 TLS 必填项由服务端校验；
- Reality SNI 保存前会实时解析 DNS、拒绝私网/保留地址及 Cloudflare CNAME/IP，并固定连接解析出的公网 IP完成 TLS 1.3 证书校验，避免 DNS 重绑定与内网 SSRF；
- 已知不再适用的 Microsoft Reality SNI 即使仍能完成普通 TLS 握手也会被明确拒绝；
- 所有写操作要求有效 Web 会话和 CSRF Token；
- “校验”和“部署”任务只通过已认证的 WSS Agent 通道下发。

Agent 收到部署任务后仍会调用目标内核自身的配置检查命令。语法生成成功不代表端口一定空闲、证书路径一定存在或防火墙已经放行，因此正式部署前应先执行“保存并校验”。

### 缺少内核时自动安装

增加入站并提交时，若所选内核未安装，会先安装官方最新稳定版，再继续原先选择的校验或部署。已安装内核保持原版本，不自动升级或降级；切换稳定版、开发版或自定义版本仍到节点设置操作。修改、删除、通用配置项和高级字段保存不会触发安装；通用配置项操作需要已有配置，提交需要内核已安装且节点在线。

自动安装需要节点管理权、配置写入与任务执行权限，以及 Agent 声明 `preset-auto-install-v1`。共享节点使用者不能借此安装主机内核；离线、旧 Agent 或不安全的现有服务会明确阻止提交。系统服务导入仍须单独确认，不会因新增预设自动接管。

配置、修订、关联元数据和带 `install_if_missing` 的任务在同一事务提交；任务始终绑定本次版本，关闭页面不会中断安装链。安装失败不继续执行配置，已保存版本可供修正。重试会再次检查本机二进制，已安装则跳过安装；如果配置已产生新版本，拒绝重试旧任务，须从当前配置重新提交。

## 按节点与端口限制大陆访问

独立的“访问限制”页面按节点、内核、入站标签和实际监听端口管理两个开关：“禁止此入站访问大陆目标”和“禁止大陆来源连接此入站”。Mihomo、Xray 与 sing-box 均使用内核原生入站路由规则实现，不创建节点级全局防火墙规则，因此不会影响 QAgent 控制连接、内核更新、DNS 或同节点其他端口。新建 VLESS-XHTTP-Reality 与 VLESS-ENC-XHTTP-Reality-Vision 方案默认启用目标大陆限制；已有配置保持原状，由管理员明确保存后生效。

WireGuard 支持目标限制，但不提供公网来源限制：内核路由看到的是隧道内地址，不能据此判断
客户端公网来源。该来源开关不可新启用，旧规则可关闭；需要公网来源限制时须在节点防火墙配置。

IPv4 地址使用 [misakaio/chnroutes2](https://github.com/misakaio/chnroutes2) 每小时更新的 BGP 聚合列表；该项目不提供 IPv6，因此 IPv6 使用 [gaoyifan/china-operator-ip](https://github.com/gaoyifan/china-operator-ip) 的每日 BGP 列表补齐。控制端下载后逐条校验 CIDR，最多缓存一小时；Mihomo 使用只含 CIDR 的远程 rule provider，Xray 和 sing-box 在保存时嵌入同一批已校验地址。规则只按 IP 地址匹配，不把域名后缀当作国家归属。

## 客户端接入资料

方案保存后，页面中的“客户端接入”区可根据客户端实际访问的域名或 IP 生成可复制的客户端配置，并同时列出服务器、端口、认证、传输、TLS / Reality 等逐项参数。连接地址和 TLS ServerName 只保留在当前页面 URL，不写入内核配置，也不会成为配置版本的一部分。

Shadowsocks 2022、VLESS、VMess、Trojan、Hysteria 2、TUIC v5 和 AnyTLS 使用各协议的分享 URI。Snell v5 与 Snell v5 + ShadowTLS v3 没有通用 URI，按 `jinqians/snell.sh` 和 Surge 的实际客户端格式生成单行 Surge 配置；Mieru 和 Sudoku 按 Mihomo 原生字段生成单行流式 YAML。Sub-Store 会逐行识别 URI、Surge 和 Mihomo 节点，因此这两类原生配置也可在同步页面选择、按地址模式重命名并与普通 URI 一起同步；最终导出目标仍须支持相应协议。端口转发是节点侧监听与目标映射，不生成代理客户端分享资料。客户端对分享格式的支持可能因产品和版本不同而变化；无法直接导入时，应使用页面列出的逐项参数。分享值和认证字段默认以密码输入框遮罩，复制时无需先显示。

客户端资料只包含连接所需的公开参数与用户凭据。Reality 服务端 Private Key、TLS 私钥路径和证书路径不会进入客户端 URI 或逐项参数。页面不会代替网络侧配置；部署完成后仍需确认 DNS 指向、证书覆盖域名，以及主机和上游防火墙已放行方案使用的 TCP / UDP 端口。

WireGuard 的原生导出是包含 `[Interface]`、`[Peer]` 的客户端配置文本，不是分享 URI；
其中包含客户端私钥及可选 PSK，应按敏感凭据保存。导出不会包含服务端私钥。

## 官方依据

- [Mihomo listeners](https://wiki.metacubex.one/config/inbound/listeners/)
- [Mihomo tunnel listener](https://wiki.metacubex.one/config/inbound/listeners/tunnel/)
- [Mihomo Snell listener](https://wiki.metacubex.one/config/inbound/listeners/snell/)
- [Mihomo Snell proxy](https://wiki.metacubex.one/config/proxies/snell/)
- [Mihomo Mieru listener](https://wiki.metacubex.one/config/inbound/listeners/mieru/)
- [Mihomo Mieru proxy](https://wiki.metacubex.one/config/proxies/mieru/)
- [Mihomo Sudoku listener](https://wiki.metacubex.one/config/inbound/listeners/sudoku/)
- [Mihomo Sudoku proxy](https://wiki.metacubex.one/config/proxies/sudoku/)
- [SUDOKU-ASCII upstream configuration](https://github.com/SUDOKU-ASCII/sudoku/blob/main/configs/README.zh_CN.md)
- [Xray inbounds](https://xtls.github.io/config/inbound.html)
- [Xray tunnel inbound](https://xtls.github.io/config/inbounds/tunnel.html)
- [Xray WireGuard inbound](https://xtls.github.io/config/inbounds/wireguard.html)
- [sing-box inbounds](https://sing-box.sagernet.org/configuration/inbound/)
- [sing-box direct inbound](https://sing-box.sagernet.org/configuration/inbound/direct/)
- [sing-box WireGuard endpoint](https://sing-box.sagernet.org/configuration/endpoint/wireguard/)
- [sing-box JSON Schema](https://sing-box.sagernet.org/schema.json)

页面底部的“高级字段”入口保留完整字段目录和官方参考链接；方案之外的字段与新版本选项也可直接在配置页源码中编辑。

### 出站操作

Xray 与 sing-box 的配置工具栏在入站操作旁提供出站操作。先选择一个入站，再增加并绑定出站、绑定已有出站，或修改/删除当前绑定；公共配置和合并预览不能操作入站绑定。弹窗沿用当前字段编辑器，显示入站标签、端口与对应的统计 mark。

新增出站有三种来源：

- 协议预设：直连、VLESS、VMess、Trojan、Shadowsocks / 2022、SOCKS5 和 HTTP；sing-box 另提供 Hysteria 2、TUIC 和 AnyTLS。按协议填写连接、认证、传输与 TLS / Reality 参数，并查看生成的 JSON。
- 其他节点入站：需有 `client-access.read` 权限，只列出有权读取的已部署客户端资料，不能选择当前节点。不兼容的协议或安全参数不可选择；目标使用各入站自己的连接地址。刷新保留原选择，目标消失时要求重新选择，不自动换绑。
- 高级 JSON：直接填写出站对象。修改已有出站默认进入此模式，不通过预设回填而丢失自定义字段；保留配置中的大整数。

节点选择只复制当时的客户端连接参数，不修改目标节点，也不自动跟随其凭据、地址或端口变更；变更后须重新选择并保存部署。服务端私钥、证书路径和解密密钥不会作为客户端出站导出。面板排除本节点，但不推断跨节点的完整转发拓扑，需自行避免循环转发。

绑定作为所选入站的兜底路由，已有条件分流与限制保持原顺序；遇到共享多入站绑定或全局拦截兜底，须先在公共配置中明确调整。修改和删除只允许本入站独占的非默认出站模板，删除后恢复全局路由；默认或共享模板需在公共配置中管理。自动生成的统计出口不列入手动选择器。

操作通过源码原子保存接口校验版本并提交任务。显式绑定始终按入站端口生成独立出口及 `0x51430000 | port` 标记（Xray socket mark / sing-box `routing_mark`），直连和带原生 API 的 sing-box 也不例外。先升级控制面和 Agent，再保存部署；保存并校验不改变节点运行配置，部署成功后才能使用新路由。已有草稿会阻止打开出站操作；保存期间防止重复提交，版本冲突或结果不确定时保留草稿并要求重新读取核对。

节点运行快照与面板保存配置无需逐字一致即可打开配置项和限制编辑器；编辑基于面板保存版本，提交仍检查版本冲突。后台任务完成不会刷新仍打开的配置弹窗，未保存源码和出站草稿保留离页提醒。

配置页保持原布局。已选节点的列表与工作区读取并行；内核切换允许连续选择，以最后一次选择为准，旧响应不会覆盖新页面，切换期间旧编辑器不可操作。源码保存期间禁用重复提交和目标切换；若源码已保存但任务提交或页面刷新失败，显示已保存版本并保留当前内容，重新读取核对后才能再次提交。节点读取任务前五次轮询间隔为 200ms，随后恢复 600ms，减少短任务已完成后的等待。
