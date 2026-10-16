# sing-box 托管 TLS 部署核对与修复

核对日期：2026-10-16；项目基线：`1786489a3aad922903ef32fbefbb7e10a5ae9dd0`。
上游：[233boy/sing-box，commit 2d78583b5aecccb0da0148a816bc1495a284509f](https://github.com/233boy/sing-box/tree/2d78583b5aecccb0da0148a816bc1495a284509f)。重点阅读 `src/core.sh`、`src/caddy.sh`、`src/systemd.sh`，以及安装入口、下载、配置模板、修改和删除脚本。没有执行上游安装脚本或接管用户安装的反代。

## 截图问题的影响范围

| 日志 / 状态 | 性质 | 对部署的影响 |
| --- | --- | --- |
| sing-box check 通过、服务 active | 配置有效、进程运行 | 不能证明 Caddy 已取得证书或代理路径可用。 |
| 请求 Let's Encrypt directory 超时 | 本次签发的直接失败原因 | 尚未完成 CA 协议，不能取得证书。需排查节点出站 DNS、TCP 443 和 TLS；截图不能确定是路由、防火墙还是 CA 故障。 |
| `unable to get instance ID`、`mkdir /nonexistent: read-only file system` | 默认应用目录错误的环境告警 | 自定义证书 storage 与应用目录独立；storage 正确不代表 `instance.uuid` 可写。告警需要修复，但不能据此把 directory 超时归为证书存储失败。 |
| 未配置 ACME 邮箱 | 备用签发条件缺失 | 项目仅配置 LE；Caddy 2.11.7 的 ZeroSSL 自动 EAB 绑定需要邮箱。这不是 LE 网络超时的成因。 |
| `tls: internal error` | 最终握手症状 | 无可用证书时的下游错误，必须结合本次 CA 原始错误判因。 |
| 恢复此前配置和服务状态 | 失败后的事务恢复 | 分别报告原始错误、反代和核心恢复结果，不把回滚成功记成部署成功。 |

依据：[Caddy instance ID](https://github.com/caddyserver/caddy/blob/v2.11.7/caddy.go)、[TLS 应用](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/tls.go)、[ACME issuer / ZeroSSL EAB](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddytls/acmeissuer.go)、[CertMagic ACME 超时](https://github.com/caddyserver/certmagic/blob/v0.25.6/acmeissuer.go)。`acme_timeout` 是挑战轮询时限，不是 directory 请求或单机构完整签发时限。

## 上游对照表

“原状”指本 PR 前的项目实现；合理差异继续保留。

| 上游行为 | 项目原状 | 差异 | 修复或保留理由 |
| --- | --- | --- | --- |
| 预设随机凭据、路径和内部端口，TLS 默认 443 | 原生预设、表单默认值、版本元数据 | WS / HTTPUpgrade 已托管，H2 仅原生 | H2 补齐默认 Caddy、生成、校验、Agent 能力门槛、部署和导出；旧版无 `tls_mode` 继续原生。 |
| Cloudflare trace 先 IPv4 后 IPv6；按可用族查 DoH 并比较 IP | 域名格式与端口检查 | 缺少 NAT / 双栈 / CDN 说明 | 服务账号下查 A/AAAA，拒绝空或非公网记录，报告接口和 CDN；允许单栈。NAT 下不因接口 IP 与 DNS 不等误拒绝。 |
| 443 公网、回环内部端口，检查端口 | 独立公网端口、127.0.0.1 后端、跨用户授权 | HTTP-01 的 80 与 HTTPS 端口不同 | 保留端口授权；预检 80 与公网监听，内部端口不能占用 80；明确 bind 不证明公网可达。 |
| 包管理器装依赖、GitHub 下载，脚本下载重试 | Caddy 官方 release + SHA256、核心安装器、Nginx 系统包 | 项目校验更严格 | 保留；复用 Caddy 检查 ACME/storage/反代模块、真实配置校验和系统 CA，软件源、包锁、能力错误给出处理入口。 |
| 自动 TLS、已有证书 / 无自动 TLS | Caddy、Nginx 私有 CA、原生证书 | 外部代理缺独立编辑/导出模式 | 新增外部 TLS 终止：核心回环明文，公网参数独立；不管理外部服务。保留原生证书及客户端信任。 |
| WS / HTTPUpgrade 路径反代 | 已有 Upgrade 反代 | active/TLS 不等于传输可用 | H1 → 明文 HTTP/1.1，真实 101 探测及导出客户端 TCP/UDP 往返。 |
| H2 使用 h2c 后端 | H2 原生 TLS | 无托管 H2 | Caddy 公网 h2 → 回环 h2c；保存 Method、Host、SNI、路径。Nginx HTTP 反代不承诺 h2c，H2 不展示 Nginx。 |
| root systemd / OpenRC 服务 | 非 root、最小能力、只读系统目录 | 不能直接复制 root 模板 | 保留隔离；显式 XDG/HOME 和受管目录；只迁移完整匹配旧模板，拒绝覆盖自定义配置或 drop-in。 |
| Caddy 自动签发与续期 | 自定义 storage、LE → ZeroSSL、加密快照 | XDG 遗漏；轮询时限不能保证备用执行 | 保留 storage；新增应用目录；65 秒后目标域名切备用，成功恢复正常续期链；证书、账户、锁不删除。 |
| 写入、校验、启动/重启 | check、重启再验证 TLS、失败回滚 | 首次签发失败已中断旧业务 | 检查 → 先签证书且保留旧路由 → 应用 → TLS/协议验证 → 提交；签发失败不替换核心。 |
| 导出地址、端口、SNI、Host、路径 | URI / sing-box / Mihomo 能力判断及元数据 | 新 H2 / 外部模式需一致 | 明文后端不改变客户端 TLS，公网端口独立、Host/SNI 分离；不能无损表达的格式继续明确拒绝。 |
| 修改、删除配置与服务 | 多用户清单及生命周期/回滚 | 精确恢复共享代理运行/启用状态 | 快照旧配置和 init 状态；恢复路由、启停与自启动；最后提交清单；保留他人证书和路由。 |
| 可配置伪装网站 | 不自动生成伪装站点 | 产品范围不同 | 保留：不接管用户站点或添加未请求的网站内容。 |

## 运行目录、迁移与持久化

| 用途 | 路径 / 配置 | 权限与持久化 |
| --- | --- | --- |
| 配置、清单、Nginx 叶证书 | `/etc/qagent/tls-frontends` | root 写，服务组读取必要配置，清单 0600；长期 Agent 的 `/etc` 仍只读，固定工具写入。 |
| 应用根、HOME、工作目录 | `/var/lib/qcontrolhub-tls` | 非 root 服务读写，目录 0750。 |
| 默认应用数据 | `XDG_DATA_HOME=/var/lib/qcontrolhub-tls/data` | `data/caddy/instance.uuid` 持久保留。 |
| 默认应用配置 | `XDG_CONFIG_HOME=/var/lib/qcontrolhub-tls/config` | 显式可写；仍关闭 Caddy autosave，项目配置为来源。 |
| 证书、ACME 账户、锁及续期状态 | `storage.root=/var/lib/qcontrolhub-tls/caddy` | **原路径不变**，不移动到 XDG 目录、不清理、不重新生成账户。 |
| 日志、管理 Socket | 根下 `caddy.log`、`caddy-admin.sock` | 日志 1 MiB、保留两份；本地 Unix Socket；仅提取本次告警与错误。 |
| Nginx PID / 临时文件 | 根下 `nginx.pid`、`*_temp` | 保留既有隔离。 |

systemd 保留 `ProtectSystem=strict`、`ProtectHome=yes`、`ReadWritePaths=/var/lib/qcontrolhub-tls`、PrivateTmp/PrivateDevices、NoNewPrivileges 和唯一的 CAP_NET_BIND_SERVICE。OpenRC 使用相同账号、XDG/HOME、工作目录、umask 和能力限制；不声称 OpenRC 完全等价于 systemd 的 mount 沙箱。

已有节点更新控制端和 Agent 后：

1. Agent 启动及部署前检查已有清单与服务。仅完整匹配项目旧模板的文件自动替换，原子部署保留 `.bak`；systemd daemon-reload 后，运行的服务重启一次以获得新环境。停止的服务不启动，自启动状态不变。
2. storage、账户和锁路径不变。迁移中断留下 `caddy-runtime.pending`，下次继续 reload/restart，完成后清除；重复执行幂等。
3. 自定义 unit/init 脚本或 systemd drop-in 不覆盖，启动告警、部署明确失败。管理员先备份，将自管代理放入独立服务，恢复项目生成模板，再重启 Agent/重试；不要删除证书 storage。
4. 排查实际环境可用 `systemctl show qagent-tls-caddy.service -p Environment -p User -p ReadWritePaths`，并核对进程环境；OpenRC 核对脚本及进程。只看 JSON storage 不足以证明 XDG 配置正确。

## 阶段、预算与回退

1. **依赖和环境检查**：本机端口、程序能力、服务定义和实际服务账号文件创建/删除权限、反代配置校验；最多 120 秒，受任务总时限约束。
2. **配置生成及校验**：真实 sing-box check；无效配置不触发签发。
3. **证书准备**：检查缓存证书域名、有效期、系统信任链、私钥配对；无有效证书时加载 automation 与旧提交的 Caddy HTTP 路由，不添加新 HTTPS 监听、不停止旧 Nginx、不替换核心。HTTP-01 使用 TCP 80。结束后恢复原反代状态，保留取得的证书。
4. **服务应用**：原子备份核心并重启，再应用反代路由。
5. **TLS 与协议检查**：本机入口受信任 TLS/域名，WS / Upgrade 的 101 或 H2 的 HTTP/2 200，使用真实 Host/路径/方法。探测不发送隧道凭据；完整鉴权/出站由实际客户端测试覆盖。
6. **提交**：最后写清单并提交关联。失败分别恢复代理与核心，报告最终状态。

| 范围 | 预算 / 行为 |
| --- | --- |
| 出站 directory 预检 | 每请求 6 秒，拨号/TLS 各 3 秒；最多两家机构；子进程 18 秒；不用环境 HTTP 代理。 |
| Caddy HTTP 请求 | 当前依赖默认 30 秒，保留库内部协议重试；`acme_timeout` 不改变这一超时。 |
| 挑战轮询 | `acme_timeout=45s`。 |
| 主 / 备用 | 各共享 65 秒，多域名共用窗口。主机构可自动回退；窗口耗尽时重载目标域名为 ZeroSSL-only，取消旧工作并给予备用独立窗口。无邮箱只有 LE 窗口。 |
| 重试 / 退避 | Caddy/CertMagic 内部处理；项目每任务最多一次强制备用切换，不额外循环或反复注册。限流提示遵循 Retry-After；后台续期由 Caddy 管理。 |
| Agent / 租约 | 托管操作 6 分钟，可选首次核心安装另给 4 分钟；租约 12 分钟覆盖安装、执行、恢复。 |
| 回滚 | 代理和核心各独立 30 秒；工具父进程在截止后额外等 40 秒，systemd 工具最长 405 秒，避免先杀工具破坏恢复。 |

邮箱优先级：**预设非空覆盖 → 配置所有者账号默认 → 空值**。账号设置、队列快照已有加密链路；不改排队任务、不进入核心源码或客户端。共享用户、管理员代交均按配置所有者取值。ZeroSSL 自动 EAB 需要真实邮箱及绑定 API 可达；directory 检查不注册账户，也不证明 EAB 成功。

成功恢复 LE → ZeroSSL 续期链，同 storage 证书复用。有效缓存允许 ACME 出站失败时告警继续，明确 DNS 错误仍提前拒绝。网络超时、DNS、挑战、账户、限流、权限分别分类；实例 ID warn 不掩盖 CA error。

## 网络验证范围和使用入口

- **本机监听**：bind 发现端口占用，不证明公网防火墙、安全组或 NAT 正确。
- **节点出站**：真实服务 UID、同 XDG/HOME、宿主网络查询 A/AAAA、访问 directory，验证 DNS/TCP/TLS/响应。子进程未进入 systemd mount namespace；文件权限另外验证，init 沙箱由真实容器测试。
- **公网挑战**：只有 CA 的实际 HTTP-01 证明该时刻入站可达。预检明确未验证公网入站；AAAA 也需正确回源。Cloudflare 提示核对挑战、DNS only 或自管 TLS。NAT 下不强制 DNS 等于接口 IP。
- **操作入口**：账号邮箱在“系统设置 → 证书申请”；预设覆盖在“高级设置 → ACME 邮箱”；网络受限选“已有证书 · 原生 TLS”或“外部反代 · 自行管理 TLS”。原生需可读 PEM 证书与私钥；外部模式自行管理公网代理、证书、续期。

兼容范围：VMess/VLESS/Trojan 的 WS、HTTPUpgrade、HTTP/H2 支持 Caddy；前两者另支持 Nginx 私有 CA。H2 公网 ALPN h2 → h2c，WS/Upgrade 是 HTTP/1.1。Reality、QUIC、TUIC、Hysteria2、AnyTLS 等保持原生范围；HTTP 反代不用于任意 TCP/UDP TLS 终止。

外部模式保存公网端口、SNI、Host、路径，核心仅监听 127.0.0.1。管理员可在自己的 Caddy 服务中使用（替换实际值）：

```caddyfile
proxy.example.com {
    reverse_proxy /your-path 127.0.0.1:20001
}
```

H2 使用 `reverse_proxy /your-path h2c://127.0.0.1:20001`，客户端 ALPN h2。仅支持 HTTP/1.1 回源的 CDN 不满足此条件。外部入口须同机或经受信任隧道访问回环，项目不把内部监听改为公网。Agent 只校验核心，不声称探测了自管外部 TLS。导出保持公网 TLS，Host/SNI 分离；不能无损表达的格式拒绝，建议 sing-box JSON。

## 恢复与版本回滚

签发失败发生在核心替换前，恢复原代理内容/启停/自启动，证书和账户保留供重试。应用失败先恢复代理，再恢复核心文件及启停；新装无旧配置则清除新文件并停止，原先停止的实例保持停止。清单只成功后写入，其他用户路由与证书不删除。

界面复用任务组件、折叠日志和主题，摘要优先显示阶段、原因、修复入口、回滚；长内容换行、日志容器滚动。回滚失败显示需核对节点最终状态，不宣称恢复成功。

撤销代码时保留整个数据根、账户和清单。新版 unit 的 XDG 环境会被旧 Agent 完整模板校验视为修改，不能无提示降级。必要时先停相关任务、备份定义与配置，恢复迁移的 `.bak`，执行 daemon-reload（或恢复 OpenRC 脚本）并按原状态启动；旧问题可能重现。优先前向修复或临时使用原生/外部 TLS。本 PR 不新增数据库迁移。

## 实际验证记录

Linux amd64，Go 1.25.12、Node 22.23.2、PostgreSQL 17；Docker Debian systemd / Alpine OpenRC；真实 sing-box **1.14.3**、Caddy **2.11.7**、Nginx **1.26.3**。

| 验证 | 结果 | 边界 |
| --- | --- | --- |
| 全套 | `make non-browser-checks`、带隔离 PostgreSQL 的 `make test`、`make browser-checks` 通过 | 测试环境，无生产状态。 |
| 真实代理/导出 | Caddy 9（3 协议 × WS/Upgrade/H2）+ Nginx 6 组合；实际 check、导出客户端 TCP/UDP 往返、Host/SNI 分离、非 443 端口通过 | UDP 在隧道内承载；未逐 CDN 测试。 |
| 本地 ACME | 主 directory 503 / 挂起后重载备用；真实 HTTP-01、账户、订单、CSR 签名、下载、可信 TLS 和恢复策略后证书复用通过 | 不注册 ZeroSSL；模拟 CA 不验证 JWS 签名，Caddy 仍真实走 ACME。测试缩短窗口，生产预算另有断言。 |
| 证书准备顺序 | 旧进程占用 HTTPS 端口时签发成功，落盘后才释放并应用新监听；另断言旧路由保留 | 证明 HTTP-01 可独立于新 HTTPS 监听。 |
| 非 root / 迁移 | 两种 init 实际账号可写 XDG/storage；旧模板、Agent 启动修复、幂等、重启、自定义拒绝、ID/证书/CA 状态保持通过 | Alpine 只读 cgroup 环境提示与 TLS 结果分别记录。 |
| 节点重启 | systemd 容器真实 restart 后自动启动，实例 ID、证书 SHA256 相同 | OpenRC 验证服务重启，未声称整机重启。 |
| 恢复/共享 | 两种 init 的 TLS 失败、原多实例握手、停止/恢复/删除、端口冲突测试通过 | 未逐写入点断电测试。 |
| 邮箱/快照 | 有/无邮箱、默认/覆盖、共享所有者、加密快照、能力门槛及租约回归通过 | 真实 EAB 仍需公网账号。 |
| 地址族 | IPv4-only、IPv6-only、双栈 DNS 及非法记录单元测试通过 | 没有真实单栈公网节点，未声称其挑战实测。 |
| UI | Chromium 桌面/手机、主题、H2/外部模式、诊断通过；人工 1365/375px 查看，无文档横向溢出，日志在容器内 | 本地夹具，无真实用户凭据。 |

**未执行公网 LE/ZeroSSL 签发**：没有可管理的真实域名与生产节点。实际部署应记录节点地址族、DNS/CDN/NAT、80/HTTPS 映射、directory 结果、实际机构与握手。本地 CA/init 测试验证流程、回退和恢复，不推断生产公网可达。

复现：

```sh
make non-browser-checks
QCH_TEST_DATABASE_URL='postgresql://user:password@127.0.0.1/test?sslmode=disable' make test
make browser-checks
QCH_CADDY_BINARY=/absolute/path/to/caddy \
QCH_SING_BOX_BINARY=/absolute/path/to/sing-box \
QCH_NGINX_BINARY=/absolute/path/to/nginx \
go test -count=1 -timeout 3m -v ./internal/agent \
  -run '^TestManagedTLS(ProxyLiveMatrix|IssuerFallbackLive)$'
make managed-tls-systemd-test managed-tls-openrc-test
```
