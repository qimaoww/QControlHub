# sing-box 原生预设对照与验收

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

上游部分 WS/HTTPUpgrade 方案由 Caddy 接收公网 TLS，再反代至本地 core；H2 脚本还生成 core TLS 与 Caddy h2c 规则。WS / HTTPUpgrade 方案默认采用下文的面板托管反代，其余方案使用 sing-box **原生 TLS 等价实现**。已有原生 TLS 配置继续兼容；托管反代提供自动证书及独立公网端口，不生成上游的伪装网站。

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

VMess / VLESS / Trojan 的 **WS-TLS、HTTPUpgrade-TLS** 六个入口现在默认使用面板托管的 **Caddy 自动证书**，也可选 **Nginx 自签证书**或已有证书的原生 TLS。原有未填写 `tls_mode` 的配置继续使用原生 TLS；H2、Reality、QUIC 和非 HTTP 协议保持原有原生实现。标准 Caddy / Nginx 的 HTTP 反代不能作为任意 TCP / UDP 协议的 TLS 终止层。

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

证书方式、公网端口和原生 TLS 字段沿用现有表单网格，按选择显示对应说明。编辑重试沿用面板源码编辑器，支持行号、格式化、恢复原文和 JSON 语法反馈；提交期间锁定编辑和关闭操作，失败保留草稿。桌面与手机布局使用既有主题、按钮和弹窗样式。

新增验证：sing-box **1.14.3**、Caddy **2.11.7**、Nginx **1.26.3** 的 12 种实际反代组合均通过 TCP / UDP 回环连接（UDP 为隧道内承载）；自定义 SNI、不同 HTTP Host 和非 443 公网端口均参与测试。实际 systemd 与 Alpine OpenRC 沙箱测试覆盖自动安装、443 监听、配置校验、重载、多实例保留、冲突拒绝、停止、恢复和卸载；浏览器覆盖桌面 / 手机表单和编辑重试。公开 ACME 申请未连接真实公网域名验收，Caddy 生产配置使用实际程序校验，连接测试使用本地受信任证书夹具。

参考：[Caddy 自动 HTTPS](https://caddyserver.com/docs/automatic-https)、[Caddy JSON 配置](https://caddyserver.com/docs/json/)、[Nginx WebSocket 反代](https://nginx.org/en/docs/http/websocket.html)。Caddy 使用 Apache-2.0，Nginx 使用 BSD 类许可；项目不打包两者程序，安装时从官方发布或系统软件源取得。

### 共享账号的 TLS 编辑与删除

- 共享用户需要同时获得内部端口和公网 HTTPS 端口的授权。自己的现有公网端口可在修改域名、证书或切换 Caddy / Nginx 时继续使用；其他账号已分配或已部署的端口仍然独占。默认公网端口为 443，可修改为节点所有者允许的其他端口。
- 端口冲突返回 `409 / code=port_conflict`，区分内部 `port` 和公网 `tls_public_port`。界面保留草稿、显示原因并定位对应字段，修改后可直接重试；提交失败不会保存配置、创建任务或占用新端口。配置版本确实变化时仍要求重新读取。
- 允许删除最后一个入站并部署明确的空入站列表。共享校验和 Agent 仍拒绝动态监听、额外入口或管理接口；空配置不会创建新的流量监听。该部署删除本实例的托管 TLS 路由，并保留其他实例的路由；再次启动不会恢复已删除路由。端口授权继续保留，释放授权仍需节点所有者按已有停机和流量结算流程操作。
- OpenRC 下重复清理已停止、已取消自启动的反代服务不再报错。

回归验证覆盖固定端口授权 / 不限端口授权的共享用户、同账号反复修改 TLS、其他账号占用 443、调整公网端口后重试、过期版本拒绝、多入站删除、删除最后一个入站后重新添加、Agent 空配置执行、桌面 / 手机表单。实际 sing-box 1.14.3 校验并启动空实例；systemd 和 Alpine OpenRC 实际反代验证删除路由及多实例保留。上述回环与夹具测试不代表已核查某台生产节点的 443 分配记录。
