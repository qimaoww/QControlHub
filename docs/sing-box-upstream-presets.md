# sing-box 原生预设对照与验收

> 最新部署流程、H2 / 外部反代支持、非 root Caddy 迁移及 ACME 预算见[托管 TLS 部署核对与修复](managed-tls-deployment-audit.md)。下文包含此前阶段的验收记录。

## 来源与范围

对照日期：2026-10-15。上游固定为 [233boy/sing-box @ 2d78583b5aecccb0da0148a816bc1495a284509f](https://github.com/233boy/sing-box/tree/2d78583b5aecccb0da0148a816bc1495a284509f)，实际阅读：

- [README.md](https://github.com/233boy/sing-box/blob/2d78583b5aecccb0da0148a816bc1495a284509f/README.md)：公开方案列表。
- [src/core.sh](https://github.com/233boy/sing-box/blob/2d78583b5aecccb0da0148a816bc1495a284509f/src/core.sh)：`protocol_list`、`create`、传输/TLS 字段、密码和客户端资料生成。验收以此处启用的 21 个条目为准；注释中的 direct 不计入，本面板原有端口转发继续保留。
- [src/caddy.sh](https://github.com/233boy/sing-box/blob/2d78583b5aecccb0da0148a816bc1495a284509f/src/caddy.sh)：域名证书、反向代理和 h2c 配置。

上游为 [GPL-3.0](https://github.com/233boy/sing-box/blob/2d78583b5aecccb0da0148a816bc1495a284509f/LICENSE)。此变更依据其配置行为独立实现，没有复制或分发上游安装脚本；保留以上来源及许可链接。sing-box 的字段、版本与编译选项依据 [SagerNet/sing-box v1.14.3 @ 7054cac5124465ffdb145870d7c01885566711b1](https://github.com/SagerNet/sing-box/tree/v1.14.3)。

## 完整对照清单

所有条目均已完成生成、解析、编辑、定向合并、回读、客户端导出，以及官方 sing-box 1.14.3 服务端和客户端 TCP/UDP 回环连接。表中的最低版本指功能最初提供的版本；完整验收版本是 **1.14.3**，未逐一运行历史版本。

参数缩写：`U` 为独立随机 UUID；`P` 为独立随机密码；`R` 为独立 X25519 密钥对、Short ID、公开握手目标/SNI；`T` 为证书/私钥路径、证书域名 SNI、可选 ALPN 和客户端公共信任证书；`H` 为独立 HTTP Host、路径，HTTP/H2 可指定 Method（默认客户端 PUT）。连接地址在客户端接入页指定，与 SNI 和 Host 独立。

导出缩写：`S` 为 sing-box 原生 JSON，`M` 为 Mihomo YAML，`URI` 为已有分享链接。自定义信任证书统一使用 S；无法完整表达的格式显示原因，不输出丢参数的配置。所有条目的实际连接验收使用 S；M/URI 检查生成结构，不等同于每种第三方客户端均已实测。

| 上游方案 | 面板键 / 实现结果 | 参数与传输、安全层 | 最低版本 / 编译依赖 | 客户端格式 |
| --- | --- | --- | --- | --- |
| VLESS-REALITY | `vless`，复用并校验 Flow | U、R；TCP；Vision | 1.2 / `with_utls` | S、M、VLESS URI |
| VLESS-HTTP2-REALITY | `vless-http2-reality`，新增 | U、R、H；`http` + Reality；无 Flow | 1.2 / `with_utls` | S、M；无通用 URI |
| VMess-TCP | `vmess-tcp`，新增固定方案，复用 VMess | U；TCP；无 TLS | 1.0 | S、M、VMess URI |
| VMess-WS | `vmess-ws`，新增固定方案 | U、H；`ws`；无 TLS | 1.0 | S、M、VMess URI |
| VMess-HTTP | `vmess-http`，新增 | U、H；`http`；无 TLS，实际 HTTP/1.1 | 1.0 | S；M 不支持此 HTTP/1.1 组合 |
| VMess-QUIC | `vmess-quic`，新增 | U、T；原生 `quic`；UDP 监听 | 1.0 / `with_quic` | S；M/Xray 不能等价表达此传输 |
| VMess-WS-TLS | `vmess-ws-tls`，固定入口，复用原 VMess | U、T、H；`ws` + TLS | 1.0 | S、M、VMess URI |
| VMess-H2-TLS | `vmess-h2-tls`，新增 | U、T、H；`http` + TLS，实际 H2 | 1.0 | S、M；无通用 URI |
| VMess-HTTPUpgrade-TLS | `vmess-httpupgrade-tls`，新增 | U、T、H；`httpupgrade` + TLS | 1.7 | S；M 未提供该组合 |
| VLESS-WS-TLS | `vless-ws-tls`，新增 | U、T、H；`ws` + TLS；无 Flow | 1.2 | S、M、VLESS URI |
| VLESS-H2-TLS | `vless-h2-tls`，新增 | U、T、H；`http` + TLS；无 Flow | 1.2 | S、M；无通用 URI |
| VLESS-HTTPUpgrade-TLS | `vless-httpupgrade-tls`，新增 | U、T、H；`httpupgrade` + TLS；无 Flow | 1.7 | S；M 未提供该组合 |
| Trojan-WS-TLS | `trojan-ws-tls`，新增固定入口 | P、T、H；`ws` + TLS | 1.0 | S、M、Trojan URI |
| Trojan-H2-TLS | `trojan-h2-tls`，新增 | P、T、H；`http` + TLS | 1.0 | S；M Trojan 不支持 H2 |
| Trojan-HTTPUpgrade-TLS | `trojan-httpupgrade-tls`，新增 | P、T、H；`httpupgrade` + TLS | 1.7 | S；M 未提供该组合 |
| Trojan | `trojan`，复用并补齐 SNI/ALPN | P、T；TCP + TLS | 1.0 | S、M、Trojan URI |
| TUIC | `tuic`，复用并补齐检查 | U、独立 P、T；TUIC v5，UDP 监听 | 1.4 / `with_quic` | S、M、TUIC v5 URI |
| Hysteria2 | `hysteria2`，复用并补齐检查 | P、T；QUIC，UDP 监听；默认双向 100 Mbps | 1.5 / `with_quic` | S、M、Hysteria2 URI |
| AnyTLS | `anytls`，复用并补齐检查 | P、T；TCP + TLS | 1.12 | S、M、AnyTLS URI；客户端需实现 AnyTLS |
| Shadowsocks | `shadowsocks` 新增；`ss2022` 复用 | 方法、P/PSK；TCP 与 UDP | 1.0 | S、M、SS URI；方法须被客户端支持 |
| Socks | `socks`，新增认证方案 | 独立用户名及 P；SOCKS5 TCP/UDP | 1.0 | S、M、Socks5 URI；客户端须支持 UDP ASSOCIATE |

Shadowsocks 实际验收了上游全部七个方法：

| 家族 | 方法 | 凭据 |
| --- | --- | --- |
| AEAD | `aes-128-gcm`、`aes-256-gcm`、`chacha20-ietf-poly1305`、`xchacha20-ietf-poly1305` | 随机密码；XChaCha 为扩展方法，客户端需明确支持 |
| AEAD-2022 | `2022-blake3-aes-128-gcm` | Base64 编码 16 字节随机 PSK |
| AEAD-2022 | `2022-blake3-aes-256-gcm`、`2022-blake3-chacha20-poly1305` | Base64 编码 32 字节随机 PSK |

另保留已有 `vmess`、`trojan` 自定义传输入口，以及用于回读既有 VLESS 证书配置的 `vless-tls`。新增键只属于 sing-box，不会向 Xray、Mihomo 或 Shadowsocks Rust 的服务端目录添加方案。

## 字段与安全层

- 方案键、原生协议、传输和安全层独立。sing-box 的 HTTP/H2 均使用 `transport.type=http`；未启用 TLS 时是 HTTP/1.1，启用 TLS 或 Reality 时是 H2。HTTPUpgrade 使用 `httpupgrade`，不冒充 WebSocket；QUIC 只生成 sing-box 的 `quic` 字段，不混用 Xray 的 QUIC 加密字段。依据：[V2Ray transport](https://sing-box.sagernet.org/configuration/shared/v2ray-transport/)。
- VLESS TCP Reality 生成 `xtls-rprx-vision`；HTTP2 Reality 无 Flow。TCP 证书 VLESS 可选 Vision，非 TCP 传输拒绝 Vision。用户修改凭据后，客户端同步使用同一值。
- SNI、HTTP Host、连接地址、路径和 gRPC service name 分别处理。Host 写入 HTTP `host`、HTTPUpgrade `host` 或 WS `headers.Host`。HTTP Method 保存、读取并同步到客户端。导入未指定路径时回读为 `/`。ALPN 校验 H2 必须包含 `h2`，WS/HTTPUpgrade 必须包含 `http/1.1`；QUIC 可填写 `h3`。
- 每次生成使用加密随机源；不同入站的 UUID、密码、Short ID、Reality 密钥相互独立。Reality 私钥只写服务端，客户端使用从私钥推导的公钥；客户端导出不会包含服务端私钥及服务器证书文件路径。
- 预设切换使用独立草稿。原生 TCP/QUIC 不提交残留路径、Host 或 Method，TLS 关闭时不提交证书参数，Reality 与证书 TLS 互斥。多入站修改以原 tag 定位，保留其他入站、出口、路由、DNS 和未管理的入站根字段。
- 多用户、多 HTTP Host、自定义 TLS/传输附加字段等无法由单账号预设完整表示的导入配置保留为源码编辑，拒绝通过预设覆盖，避免保存时丢失参数。支持显式删除。源码修改后，版本绑定的客户端元数据不能将其他协议或已关闭 TLS 的旧值恢复回来。

## TLS、证书与运行依赖

上游部分 WS/HTTPUpgrade 方案由 Caddy 接收公网 TLS，再反代至本地 core；H2 脚本还生成 core TLS 与 Caddy h2c 规则。WS / HTTPUpgrade / H2 方案默认采用面板托管 Caddy，H2 回环后端使用 h2c；其余方案使用 sing-box **原生 TLS 等价实现**。已有原生 TLS 配置继续兼容；托管反代提供自动证书及独立公网端口，不生成上游的伪装网站。

部署步骤：

1. 在 sing-box 原生目录中选方案，生成随机凭据；按需修改端口、路径与 Host。
2. WS / HTTPUpgrade 托管反代填写域名和公网端口，面板自动准备证书及服务。选择原生 TLS 时，使用节点现有证书管理/续期工具准备 PEM 证书与私钥，填写节点上的绝对路径；SNI 填证书覆盖的域名。连接 IP、域名或公网 NAT 地址在客户端接入页填写。
3. 公共 CA 证书使用客户端系统信任根。托管 Nginx 自动附带公共 CA；原生 TLS 的自签/私有 CA 将**公共证书**填入“客户端信任证书”，导出 S。不设置 `insecure`，不关闭主机名验证；不允许粘贴私钥或无效 PEM。信任证书作为客户端元数据单独加密、按版本及 tag 保存，不进入服务端配置。
4. 保存并校验或部署；沿用 Agent 端口冲突检查、独立出口、集中日志、流量规则、共享实例和账号权限。UDP 承载方案须开放对应 UDP 端口；TCP 与应用 UDP 支持和实际监听协议分别显示/统计。
5. 客户端接入页导出资料。HTTP/H2、HTTPUpgrade、QUIC、定制信任证书统一优先显示 S；通用 URI 订阅会明确排除此类资料。M 不支持的组合（包括 Trojan H2）显示具体原因；自定义 HTTP Method（除 PUT）也拒绝 M。Xray 不等价的传输/Host/ALPN/信任证书组合拒绝转出站。

权限沿用[内核运行权限](core-runtime-permissions.md)：私钥仅 root 和目标服务组可读，共享实例沿用各自服务身份；Agent 校验会指出路径不存在、不可读及权限问题。续期工具应保留目标服务组权限并原子替换证书/私钥；[sing-box TLS 文件路径](https://sing-box.sagernet.org/configuration/shared/tls/)自带文件变更重载，实测替换后新客户端连接成功。证书链和私钥需要匹配。

Agent 在原生 `sing-box check` 前读取该二进制自己的 `version` / `Tags:`：AnyTLS、TUIC、Hysteria2、HTTPUpgrade 和 Reality 检查功能版本；QUIC/TUIC/Hysteria2 检查 `with_quic`，Reality 检查 `with_utls`。缺少编译标签或版本过低给出功能及所需版本/标签。原生检查继续作为最终字段与证书校验。现有 WireGuard/Tailscale/OpenVPN 能力检查保留。可从任务详情和集中内核日志区分版本/编译失败、证书文件错误、端口占用、认证失败和客户端证书验证失败。

## 可复现验收

官方 Linux amd64 **sing-box 1.14.3**（revision `7054cac5124465ffdb145870d7c01885566711b1`）包含 `with_quic`、`with_utls`。CI 使用 `deploy/tests/fetch-permission-cores.sh` 固定版本及 SHA-256 下载相同二进制，强制运行完整连接矩阵。

```sh
QCH_SING_BOX_BINARY=/absolute/path/to/sing-box \
  go test -count=1 -timeout 3m -v ./internal/serverconfig \
  -run '^TestSingBox(UpstreamLiveMatrix|TLSVerificationAndRenewal)$'

# PostgreSQL URL 使用可创建隔离测试 schema 的测试数据库
QCH_TEST_DATABASE_URL='postgresql://user:password@127.0.0.1/test?sslmode=disable' \
  go test -count=1 ./internal/api \
  -run '^TestSingBoxNativePresetSaveReadExportWithPostgreSQL$'

QCH_BROWSER_SMOKE_MODES=presets,presets-mobile,config-layout,config-layout-mobile,config-inbounds,config-inbounds-mobile \
  go test -count=1 -timeout 20m -v ./frontend -run '^TestAgentBrowserRuntimeSmoke$'
make non-browser-checks
make test
```

验收项与边界：

- [x] 全部 21 项及七种 SS 方法：真实 core `check`、服务端启动、读取生成配置后导出的真实客户端、TCP 和 UDP echo 往返。
- [x] TLS、Reality TCP Vision、Reality H2 无 Flow、HTTP/1.1、H2、WS、HTTPUpgrade、QUIC 关键差异。
- [x] 代表性 UUID/密码错误拒绝；错误 SNI、无信任根拒绝；正确 CA 成功；证书及私钥更换后自动加载成功。
- [x] 生成→解析→元数据回填→修改→多入站合并→再次读取→客户端导出；保留公共配置和其他入站；高级自定义配置拒绝有损修改。
- [x] PostgreSQL/API 计划生成、新增/修改、保存版本与部署任务绑定、workspace 回读、信任证书加密元数据、共享端口规则检查。Reality API 的公网目标检查由既有专门测试覆盖。
- [x] 实际 Chromium 桌面/移动端覆盖所有内核目录的表单默认值、字段条件显示、修改保存回读、SNI 与 Host 分离及 Flow。
- [x] 普通节点、共享实例、独立出口、流量、权限、多用户隔离和既有配置运行已有全套回归；CI 继续执行实际 systemd/OpenRC 服务权限与转发测试。

连接矩阵在本机回环运行，并使用本地 TLS 1.3 握手目标替代外部 Reality 站点（只替换握手连接目的地；SNI、密钥、Flow 与传输保持生成值）。它不证明任何生产节点公网 DNS、NAT 或防火墙已正确配置。M/URI 没有逐一运行所有第三方客户端；历史最低版本没有逐版本实测。Caddy 网站伪装不属于此实现；托管反代的证书与验证范围见下文。

## 托管 TLS 反代与编辑重试（2026-10-15 补充）

VMess / VLESS / Trojan 的 **WS-TLS、HTTPUpgrade-TLS** 六个入口现在默认使用面板托管的 **Caddy 自动证书**，也可选 **Nginx 自签证书**或已有证书的原生 TLS。原有未填写 `tls_mode` 的配置继续使用原生 TLS；本次 H2 已补齐托管 Caddy，Reality、QUIC 和非 HTTP 协议保持原生实现。标准 Caddy / Nginx 的 HTTP 反代不能作为任意 TCP / UDP 协议的 TLS 终止层。

- 公网 HTTPS 端口独立可编辑，默认 **443**；客户端导出始终使用该端口。sing-box 使用独立的内部端口，固定监听 **127.0.0.1**，不引用占位证书路径。
- 填写证书域名 / TLS SNI；连接地址、HTTP Host 和反代路径分别保存。Caddy 需要该域名的 DNS 指向节点，且公网 TCP 80（HTTP-01 验证）和所选 HTTPS 端口可达；自定义 HTTPS 端口不改变 ACME 的验证端口。
- Agent 自动安装依赖并创建独立 `qagent-tls-caddy` / `qagent-tls-nginx` 服务，不接管系统现有反代配置。Caddy 从官方稳定发布下载并验证 SHA256；Nginx 从节点系统软件源安装或复用受保护程序。发行版软件源、软件包锁和端口占用失败均返回诊断。
- 所有反代进程使用非 root 的 `qcontrolhub-core` 身份，仅授予低端口监听能力。配置、叶证书私钥和部署清单位于 `/etc/qagent/tls-frontends`；运行数据、Caddy 证书缓存和 Nginx 临时文件位于 `/var/lib/qcontrolhub-tls`。systemd 的固定临时安装工具负责必要的安装写入，长期 Agent 保持现有文件系统沙箱。
- Caddy 自动管理公开受信任证书及续期。Nginx 使用面板生成的专用自签 CA 和域名证书；CA 私钥仅保存在面板的加密版本元数据中，Agent 只收到叶证书链及其私钥。客户端 sing-box JSON 携带公共 CA，验证保持开启。CA 有效期 10 年；叶证书有效期 1 年，在有效期不足 30 天时重新保存并部署会签发新叶证书并重载反代，公共 CA 不变；当前没有自动触发 Nginx 续签任务的调度器。
- 一个工作区多个 Nginx 入站复用 CA，可以共享域名与公网端口并使用不同路径。反代覆盖来源地址头，内核的来源 IP 限制和日志使用实际客户端地址。全局反代清单保留其他共享实例；同端口路径重复、跨提供方监听冲突会被拒绝。跨账号的公网端口继续遵循独占分配；共享用户还需要分配内部端口。公网端口只参与预留，流量继续由内核独立出口统计，避免重复计费。
- 部署先校验核心与反代配置，启动后验证实际 TLS 握手；证书未就绪或服务失败时恢复原核心文件和反代。停止、恢复、卸载和共享配额停机同步处理对应反代路由；修改其他实例时保留现有路由并使用服务重载。
- 托管 WS / HTTPUpgrade 路径要求为不含查询参数、通配符或配置控制字符的绝对路径；其他复杂自定义组合可使用原生 TLS 和已有证书。

执行记录中，失败或已取消的校验 / 部署任务提供“编辑后重试”：显示原任务错误和当前配置版本，编辑源码后原子保存新版本并提交同种动作。配置变化会拒绝旧版本提交；队列失败回滚保存；原记录保留。弹窗可跳转预设表单修改 TLS 终止方式，未保存关闭有确认，提交过程中禁止重复提交。

首次部署不要求预建 `/etc/qagent/tls-frontends`。Agent 将临时请求写入现有可写状态目录 `/var/lib/qcontrolhub/tls-requests`（目录 0700、文件 0600，执行后清理）；固定安装工具创建反代配置目录，并为非 root 反代服务设置应用父目录的组遍历权限。旧 Agent 单元中的只读 `/etc` 沙箱保持有效；出现 `mkdir /etc/qagent/tls-frontends: read-only file system` 的节点需更新 Agent 后重试。

证书方式、公网端口和原生 TLS 字段沿用现有表单网格，按选择显示对应说明。编辑重试沿用面板源码编辑器，支持行号、格式化、恢复原文和 JSON 语法反馈；提交期间锁定编辑和关闭操作，失败保留草稿。桌面与手机布局使用既有主题、按钮和弹窗样式。必选 TLS 不再重复展示禁用开关，证书说明使用字段帮助文本；重试入口集中为同一操作区，弹窗沿用配置页的顶部源码工具栏、底部提交栏和主题错误提示。浏览器回归在执行记录页面上下文中检查错误区宽度、工具栏位置与主题颜色，避免仅在独立预览中正常。

新增验证：sing-box **1.14.3**、Caddy **2.11.7**、Nginx **1.26.3** 的 12 种实际反代组合均通过 TCP / UDP 回环连接（UDP 为隧道内承载）；自定义 SNI、不同 HTTP Host 和非 443 公网端口均参与测试。实际 systemd 与 Alpine OpenRC 沙箱测试覆盖自动安装、443 监听、配置校验、重载、多实例保留、冲突拒绝、停止、恢复和卸载；浏览器覆盖桌面 / 手机表单和编辑重试。公开 ACME 申请未连接真实公网域名验收，Caddy 生产配置使用实际程序校验，连接测试使用本地受信任证书夹具。

参考：[Caddy 自动 HTTPS](https://caddyserver.com/docs/automatic-https)、[Caddy JSON 配置](https://caddyserver.com/docs/json/)、[Nginx WebSocket 反代](https://nginx.org/en/docs/http/websocket.html)。Caddy 使用 Apache-2.0，Nginx 使用 BSD 类许可；项目不打包两者程序，安装时从官方发布或系统软件源取得。

### Caddy 的域名接入流程（2026-10-16 补充）

以 `VLESS-WS-TLS` 为例，选择方案后只需填写域名，公网 HTTPS 端口预填 **443**，也可自定义，然后保存并部署。面板自动生成内部端口、UUID 和 WS 路径，部署链路为：

```text
客户端 → 域名:443/生成的路径 → Caddy 自动证书与反代 → 127.0.0.1:内部端口（sing-box WS）
```

sing-box 不启用 TLS，也不引用证书文件；Caddy 负责申请、续期和终止 TLS。域名须解析到节点并放行 TCP 80 与所选公网端口。ACME 邮箱沿用本账号设置，可在高级设置覆盖。该流程同样适用于 VMess / Trojan 的 WS-TLS 和 HTTPUpgrade-TLS。

内部监听、路径、凭据、独立 HTTP Host、ALPN 和证书方式移入默认折叠的「高级设置」，支持修改与重新生成。切换「已有证书 · 原生 TLS」会自动展开证书字段并恢复公网监听；切回托管反代后使用回环监听，证书路径不再提交。读取已有配置会保留原来的证书方式和参数。

客户端自动地址优先使用该入站的证书域名，端口使用公网端口；SNI、路径和凭据与服务端一致。不需要再到节点设置填写客户端地址。显式设置的入站或节点连接地址及 IPv4 / IPv6 选择仍有效；不同入站的域名不会互相继承。共享用户使用固定端口授权时，优先选择本账号获授权且未用于已有入站或反代的内部端口；没有空闲内部端口时明确提示补充分配。共享用户仍须获得内部端口及公网端口授权，不会绕过保存、部署时的占用检查。

验收：域名为唯一手填项的生成 → 保存部署任务快照 → 回读 → VLESS URI / sing-box JSON 导出通过；Caddy 2.11.7 + sing-box 1.14.3 的 VLESS WS TCP / UDP 回环连接通过，TLS 保持证书验证。桌面 / 手机和明暗主题的表单检查通过。公网 ACME 仍需在真实 DNS 与网络条件下申请，回环测试使用受信任的本地证书夹具。

### 共享账号的 TLS 编辑与删除

- 共享用户需要同时获得内部端口和公网 HTTPS 端口的授权。自己的现有公网端口可在修改域名、证书或切换 Caddy / Nginx 时继续使用；其他账号已分配或已部署的端口仍然独占。默认公网端口为 443，可修改为节点所有者允许的其他端口。
- 端口冲突返回 `409 / code=port_conflict`，区分内部 `port` 和公网 `tls_public_port`。界面保留草稿、显示原因并定位对应字段，修改后可直接重试；提交失败不会保存配置、创建任务或占用新端口。配置版本确实变化时仍要求重新读取。
- 允许删除最后一个入站并部署明确的空入站列表。共享校验和 Agent 仍拒绝动态监听、额外入口或管理接口；空配置不会创建新的流量监听。该部署删除本实例的托管 TLS 路由，并保留其他实例的路由；再次启动不会恢复已删除路由。端口授权继续保留，释放授权仍需节点所有者按已有停机和流量结算流程操作。
- OpenRC 下重复清理已停止、已取消自启动的反代服务不再报错。
- 普通和共享实例删除最后一个入站时，同时移除面板生成的固定回环统计 API，保留日志、DNS、出口和缓存等公共配置。即使此前证书申请或首次部署失败，也能仅凭已保存的入站标识完成删除、校验和部署；不要求补齐已删除入站的证书。官方 sing-box 未编译 `with_v2ray_api` 时，空实例不再因残留统计 API 报错。更新控制端后重新提交删除即可；更新 Agent 后也能清理旧版本已排队任务中的同类残留。

回归验证覆盖固定端口授权 / 不限端口授权的共享用户、同账号反复修改 TLS、其他账号占用 443、调整公网端口后重试、过期版本拒绝、多入站删除、删除最后一个入站后重新添加、Agent 空配置执行、桌面 / 手机表单。实际 sing-box 1.14.3 校验并启动空实例；systemd 和 Alpine OpenRC 实际反代验证删除路由及多实例保留。上述回环与夹具测试不代表已核查某台生产节点的 443 分配记录。

### ACME 回退与失败恢复（2026-10-16 补充）

- Caddy 的证书策略按域名保存账户邮箱，签发顺序为 **Let’s Encrypt → ZeroSSL**。预设的“ACME 邮箱（ZeroSSL 回退）”沿用已有表单样式，仅在 Caddy 模式展示，支持保存、编辑、重新生成参数和再次读取；切换 Nginx / 原生 TLS 时不提交此字段。邮箱不会进入 sing-box 服务端配置、分享链接或客户端导出。
- ZeroSSL 的 ACME 接口要求外部账户绑定（EAB）；Caddy 可凭邮箱自动获取绑定。需要填写自己的有效邮箱。旧配置未填写邮箱时仍可读取和使用 Let’s Encrypt，失败诊断会明确提示尚未启用 ZeroSSL。不会编造邮箱、关闭证书验证或自动切换自签证书。依据：[Caddy ACME issuer](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/acmeissuer.go)、[默认签发机构的邮箱要求](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/automation.go)。
- 同一域名采用同一账户；不同域名可分别设置邮箱。两家 CA 都使用公网 TCP 80 的 HTTP-01 验证；自定义公网 HTTPS 端口仍默认 443。节点必须能够通过 HTTPS 访问对应 CA，以及 ZeroSSL 的 EAB 接口；仅域名解析正确并不保证节点能连到 CA。
- 当前预算已调整为主机构 / 备用机构各 65 秒、托管执行 6 分钟、租约 12 分钟；支持 sing-box 与 Xray 托管操作。`acme_timeout=45s` 只限制轮询，不等于请求或整个机构时限。证书在替换业务前准备，回退以重载保证执行机会，详见最新部署核对文档。
- Caddy 运行日志写入 `/var/lib/qcontrolhub-tls/caddy.log`，1 MiB 轮转，保留两份历史文件，适用于 systemd 和 OpenRC。失败任务附带本次操作中的 CA 错误与签发机构信息；不会再只留下 `tls: internal error`。Caddy 配置持久化关闭，不尝试向服务用户的 `/nonexistent/.config` 写入 autosave。
- 回滚分别处理：无旧文件时停止本次服务并删除新文件；旧服务已停止时恢复文件并保持停止；旧配置为空入站时保持停止；旧配置有入站且原先运行时恢复文件并重启验证。空共享实例被心跳检查停用属于预期状态，不能再误报“恢复失败”。反代回滚成功与失败分别显示，不再输出 `<nil>`。
- `managed-tls-acme-fallback-v1` 单独声明新 Agent 能力。面板不会将携带账户邮箱的任务交给旧 Agent 并静默丢失回退策略；断线后能力降级也会拒绝继续执行，并保留“执行结果未知”的诊断。

验证包括：邮箱的生成 / 保存 / 读取 / 导出隔离、账户冲突及降级拒绝、PostgreSQL 租约边界；实际 Caddy 主 CA 返回 HTTP 503 后由备用本地测试 CA 签发，客户端显式信任该测试 CA 并验证主机名；systemd 与 OpenRC 中真实主机名验证失败后，原两个实例仍可握手；已有十二种反代组合继续通过实际 sing-box 的 TCP / UDP 回环连接。桌面与手机预设表单通过浏览器回归并人工查看。

CI 的备用本地 CA 测试验证 Caddy 的签发机构回退和 TLS 信任链，并不注册真实 ZeroSSL 账户，也不替代生产域名的公网签发验收。生产 ZeroSSL 签发需要用户提供真实账户邮箱及可用的公网 HTTP-01 路径。

#### 在设置中自行配置邮箱

进入 **系统设置 → 证书申请 → 默认 ACME 邮箱**，填写并保存，再重新部署配置。预设中的邮箱留空时，Caddy 使用配置所有者的默认邮箱；单个预设填写邮箱可覆盖默认值。两处均留空时仅使用 Let’s Encrypt。Nginx 自签证书和原生 TLS 不读取此设置。

每个账号独立保存；共享节点上的个人配置使用共享用户自己的邮箱，管理员代交配置也按配置所有者取值。个人邮箱保存在加密的账号设置中，不进入后台运行策略、内核配置或客户端导出。旧版页面省略此字段时保留原值，明确提交空值才清除默认邮箱。

设置变更不自动重启内核或反代，不改动已排队/执行中的任务。新提交的配置校验、部署及失败部署重试会把当时的有效邮箱写入加密执行快照；启动/重启已有服务沿用上次部署的策略。修改默认邮箱后请重新部署，不必重复给每个预设填写邮箱。

数据库自动迁移至 v73，旧账号缺省为空；保留原设置与配置数据。旧二进制拒绝读取较新 schema，因此回滚控制面需使用迁移前的数据库备份。回归覆盖 v72 升级、保存/读取/校验/清空、旧客户端兼容、共享用户和管理员代交、预设覆盖、Nginx 隔离、排队快照及重试、Agent 能力拒绝，以及现有主题下桌面/手机/只读设置表单。
