# 鉴权与安全基线

QControlHub 的安全边界包括管理员、控制面、PostgreSQL、反向代理、Agent 主机和四个被管理内核。任何一处管理员令牌、添加节点凭证、Agent 私钥或配置数据库泄露，都应按基础设施凭据泄露处理。

## 身份与鉴权

### 管理 API

- `/api/v1/*` 要求 `Authorization: Bearer <QCH_ADMIN_TOKEN>`。
- 控制面只保存令牌的 SHA-256 摘要用于恒定时间比较，不把令牌写入数据库。
- 同一来源连续失败会触发内存限速；Nginx 示例额外限制登录和注册入口。
- 控制面个人账号只有 admin（管理员）和 user（用户）两种身份。管理员拥有全部能力；用户通过 `permissions` 能力集合逐项授权，Bearer API 与 Web 会话使用同一套能力校验。旧版 operator/auditor/readonly 令牌仅作为兼容入口映射为用户能力集合。
- 没有 OIDC 或 MFA。需要多人操作时，应把访问进一步放在 VPN、零信任网关或带 MFA 的上游访问代理之后。

### 独立 SPA 控制台

- 登录使用同一个管理员令牌，成功后生成随机的 12 小时服务端会话；页面由独立 `qcontrol-web` 静态容器提供，所有数据通过 `/api/v1` 获取。
- Cookie 设置 `HttpOnly`、`SameSite=Strict`；原生 TLS 或 `QCH_BEHIND_TLS_PROXY=true` 会启用 `Secure` 并使用 `__Host-` 前缀。
- 所有写操作要求会话内独立 CSRF token。
- 登录 POST 校验 Origin/Fetch Metadata；除静态 CSS 外的 Web 响应设置 `Cache-Control: no-store`。
- 控制面重启会清空内存会话并要求重新登录。这也意味着当前应运行单实例；多副本会导致会话不一致。

### Agent

1. 管理员可为节点生成多条随机的添加凭证；控制面保存 SHA-256 认证摘要和受保护的 AEAD 可恢复副本，具备权限且经过审计的操作者可重复查看有效命令。凭证绑定节点名称、可重复安装且相互独立，删除某条添加记录只会使对应命令失效。
2. Agent 本机生成 Ed25519 密钥对；服务器只保存公钥，私钥以 `0600` 写入 Agent 状态文件。
3. 后续 WSS 握手签名覆盖协议版本、HTTP 方法、转义后的路径与查询串、Agent ID、Unix 时间、随机 nonce 和空正文 SHA-256，避免跨身份或跨协议复用签名。
4. 控制面验证握手签名、拒绝超过正负 90 秒的时间戳，并把 nonce 存入 PostgreSQL 以阻止重放；心跳、任务和带 lease ID 的结果在该已认证连接中传输。
5. Agent 要求服务端协商 `qcontrolhub.agent.v1` 子协议；在控制台删除 Agent 会撤销其公钥身份、终止尚未完成的任务并立即关闭既有连接，原身份之后的握手一律被拒绝。

主机指标复用已认证 WSS 心跳，不开放额外监听端口。Linux Agent 只读取内核提供的 `/proc/stat`、`/proc/meminfo`、路由与网卡字节计数，并读取根文件系统容量；不采集进程列表、文件名或配置正文。控制面会校验指标范围、使用服务器接收时间盖章并仅保存最新快照，前端轮询接口仍要求有效的 HttpOnly 会话。

端口流量配额同样通过已认证 WSS 同步，不开放管理端口。QAgent 只在专用 `inet qcontrolhub` nftables 表中创建带策略 ID 注释的计数/丢弃规则，不接受控制面传入 nftables 表达式，也不刷新管理员已有规则。生产单元的 `CAP_NET_ADMIN` 用于这一固定操作；端口、协议、策略数量、计数范围及策略归属会在控制面和 Agent 两端校验。配额状态写入 QAgent 私有状态目录并使用原子替换。

所有 Agent 主机必须运行可靠的 NTP/chrony。时钟偏差超过 90 秒会导致合法请求被拒绝。

## 密钥要求与轮换

- `QCH_ADMIN_TOKEN` 至少 32 字节，推荐 `openssl rand -hex 32`；添加节点凭证由控制面使用 CSPRNG 生成。
- `QCH_CONFIG_ENCRYPTION_KEY` 必须使用独立的高熵 secret；它既保护配置正文，也保护可重复读取的添加节点凭据。缺失时创建/读取可恢复命令会 fail closed。
- 轮换加密密钥时先把新密钥设为 `QCH_CONFIG_ENCRYPTION_KEY`，并将旧密钥按新到旧顺序放入逗号分隔的 `QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS`。确认旧密文均已自然重写或删除后再移除旧密钥；日志和错误不会打印密钥或凭据。
- PostgreSQL 密码和管理员令牌应进入密码管理器或密钥管理服务，不能提交到 Git。
- 轮换管理员令牌：更新控制面环境并重启；重启同时使现有 Web 会话失效。
- 每个添加节点凭证只能注册它绑定的节点名称；重装会原位替换旧公钥并关闭旧连接。凭证无有效期，必须在不再需要重装时删除添加记录。
- Agent 完成注册后，从 `/etc/qcontrolhub/agent.env` 删除 `QCH_ENROLLMENT_TOKEN`，降低主机进程环境和备份中的暴露面。
- 若 Agent 私钥疑似泄露，清除远端状态文件并重新执行该节点的添加命令，控制面会替换旧公钥；若添加凭证也疑似泄露，应先删除添加记录再生成新命令。

## 传输与网络

- 远程 Agent 默认要求 WSS。`QCH_ALLOW_HTTP=true` 只应出现在本机或隔离测试网络；非本机明文连接必须额外设置 `QCH_ALLOW_INSECURE_LIVE=true`，否则拒绝连接。
- 私有/自签名 TLS 使用 `QCH_TLS_CA_FILE` 加载受保护的 CA PEM；Agent 仍会执行完整证书链、有效期和主机名校验，不提供 `insecure-skip-verify`。
- Compose 默认只把控制面和 PostgreSQL绑定到 `127.0.0.1`。公网仅开放 Nginx 的 443 端口。
- `QCH_CORS_ORIGINS` 是精确、逗号分隔的来源白名单，不支持通配符需求；同源 Web UI 无需配置 CORS。
- `QCH_TRUSTED_PROXY_CIDRS` 必须精确列出完整反向代理链：控制面直接看到的 `qcontrol-web` 固定地址，以及转发链中位于真实客户端右侧的宿主 Nginx gateway。控制面仅在直接 TCP 对端受信时解析 `X-Forwarded-For`，再从右向左剥离其余受信跳点，避免公网客户端伪造限流或 Agent 来源身份；禁止信任整个私网。
- Compose 内部 PostgreSQL URL 使用 `sslmode=disable`，并通过 `QCH_ALLOW_INSECURE_DATABASE=true` 显式豁免，因为流量仅位于单机内部 Docker 网络。外部或托管 PostgreSQL 必须将该开关设为 `false`，使用 `sslmode=verify-full` 并配置可信 CA。
- 防火墙应只允许管理员可信网络访问控制台；Agent 只需要向控制面发起出站 443 连接，不需要入站端口。

## 主机权限

Agent 需要写入固定内核配置路径、调用 `systemctl`，并用 `CAP_NET_ADMIN` 管理专用端口配额表，systemd 示例因此以 root 运行。该单元使用只读系统视图和 `ReadWritePaths` 限定可写目录，但 root Agent 仍然属于高价值进程。四个内核继续以专用非 root 用户运行，只获得监听低端口所需的 `CAP_NET_BIND_SERVICE`；Agent 只会为固定 `qagent-*` 服务名同步这一项能力，不修改自定义服务：

- 只从可信构建产物安装 Agent，限制二进制和环境文件为 root 可写。
- 只启用实际安装的内核，并核对服务名和绝对配置路径。
- Agent 接入后所有校验、部署和服务操作均为真实执行；请先确认节点权限、内核路径和 systemd 单元，再从面板提交任务。
- QControlHub 不执行控制面提供的任意 shell 命令；动作被限制为固定枚举，内核与目标路径来自本机配置。
- `read-config` 读取安全映射的外部服务配置，`read-managed-config` 独立读取 QAgent 托管配置；两者都只访问对应内核预先固定的绝对路径，拒绝符号链接、非普通文件、不安全归属或写权限、非 UTF-8 和超过 2 MiB 的内容，并在返回前调用该内核的配置检查命令。控制面把正文存为不出现在普通任务响应中的短期快照，并按节点、内核和读取类型分别轮换。
- 内核安装任务只携带 `stable`、`development` 或严格版本号，不接受 URL、路径和命令。Agent 只访问四个硬编码官方 GitHub 仓库，拒绝非 GitHub 跳转，并要求发行资产带有可验证的 SHA-256 摘要。
- Agent 只执行绝对路径、root 所有且不可由组/其他用户写入的二进制；systemd unit 名必须通过严格字符白名单。
- Agent 通过 `os.Root` 固定配置目录，拒绝符号链接、非普通文件、非当前 UID 所有或组/其他用户可写的状态/配置目录；部署备份限制为 3 份且服务重启失败会回滚。

## 数据保护

配置正文可能包含 UUID、密码、证书私钥或上游认证信息。当前配置正文在 PostgreSQL 中不是字段级加密，因此必须：

- 加密数据库磁盘、快照和备份，并严格限制数据库账户和备份读取权限。
- 定期测试备份恢复；备份至少包含 PostgreSQL 数据卷或一致性逻辑备份。
- 限制管理员终端缓存、浏览器配置粘贴记录和日志采集范围。
- 不在任务输出中返回秘密。内核校验器输出会进入任务审计，提交配置前确认其错误信息不会泄露敏感值。

## 上线核对表

- [ ] 管理员令牌由 CSPRNG 生成并已安全保存；不再需要重装的添加节点记录已经删除。
- [ ] `QCH_BEHIND_TLS_PROXY=true`、`QCH_ALLOW_INSECURE_HTTP=false`，控制面只发布到回环地址。
- [ ] `QCH_ALLOW_INSECURE_DATABASE=true` 只用于同机隔离 Compose 网络；外部数据库设为 `false` 并验证证书。
- [ ] HTTPS 证书有效，TLS 1.2/1.3 可用，HTTP 自动跳转 HTTPS。
- [ ] PostgreSQL 不对公网开放；外部数据库使用证书校验。
- [ ] `.env`、Agent 环境文件和状态文件权限为 `0600`。
- [ ] Agent 主机时钟同步，添加节点凭证已在注册后从环境文件删除。
- [ ] 防火墙、备份、监控与令牌轮换流程已验证。
