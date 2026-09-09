# HTTP API

本机 Compose 默认地址为 `http://127.0.0.1:8080`；生产基线通过 Nginx TLS 对外提供 `https://qcontrolhub.example.com`，下文示例按该生产地址书写。所有请求和响应使用 UTF-8；JSON 请求应发送 `Content-Type: application/json`。

## 鉴权模型

- 管理端点 `/api/v1/*`：自动化调用使用 `Authorization: Bearer <令牌>`；SPA 使用 `/api/v1/auth/login` 建立 HttpOnly 会话 Cookie。个人账号只有 admin/user 两种身份，user 的能力由 `permissions` 列表决定；旧版低权限令牌仅作为兼容入口。
- 添加或重装节点 `/agent/v1/enroll`：`Authorization: Bearer <节点绑定的 QCH_ENROLLMENT_TOKEN>`。
- WSS `/agent/v1/connect`：仅供官方 Agent 使用，握手要求 Ed25519 签名头、时间戳和 nonce；不要用管理员令牌调用。
- `/healthz`：无鉴权，仅返回服务存活状态，不包含数据库详情或秘密。
- `/readyz`：无鉴权；仅在控制面能连接 PostgreSQL 时返回 200，不暴露数据库错误详情。
- `GET /api/v1/agent-installer`：通过 `X-QControlHub-Enrollment` 头提交有效的添加节点凭证后返回一键安装脚本。
- `GET /api/v1/agent-binary`：通过 `X-QControlHub-Enrollment` 头提交有效的添加节点凭证后返回 Agent 可执行文件。

### 身份与权限

用户管理只保留两种身份；管理员拥有全部能力，用户按 `permissions` 逐项授权。旧版低权限令牌仍兼容映射到能力集合：

| 角色 | 读取 | 任务/配置写操作 | 节点与添加命令、设置、删除配置 |
| --- | --- | --- | --- |
| user（用户，按 permissions） | 按授权能力 | 按授权能力 | — |
| admin（管理员） | ✓ | ✓ | ✓ |

权限不足时返回 `403`。同一令牌在 Web 会话与 Bearer 请求中的角色一致。

失败响应通常是：

```json
{"error":"提交的参数无效，请检查输入后重试。"}
```

`error` 是面向用户的中文提示，可能随版本优化；程序应依据 HTTP 状态码处理失败，不要匹配提示文字。登录凭据错误与会话过期都会返回 `401`，面板会分别提示“用户名、密码或管理员令牌不正确”和“登录已失效”。任务结果中的 `error` 与内核输出仍保留原始诊断，面板会为英文诊断补充中文说明。

登录返回 `403` 且提示访问地址不一致时，请核对浏览器地址的协议、域名和端口。生产环境请使用配置的 HTTPS 地址；经反向代理访问时，由管理员检查 `QCH_BEHIND_TLS_PROXY` 和 `QCH_CORS_ORIGINS`。不要为消除提示而关闭来源校验或放宽可信来源。浏览器无法连接服务器、代理返回非 JSON 错误页、响应不完整时，面板也会显示中文提示；提交操作后遇到网络错误，请先刷新确认结果，避免重复提交。

## 管理端点

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/overview` | 节点、配置和任务计数 |
| `POST` | `/api/v1/auth/login` | 使用管理令牌创建 SPA 会话，返回角色和 CSRF token |
| `GET` | `/api/v1/auth/session` | 读取当前 SPA 会话 |
| `POST` | `/api/v1/auth/logout` | 注销当前 SPA 会话 |
| `GET` | `/api/v1/agents` | 列出未撤销 Agent |
| `GET` | `/api/v1/system-tcp/parameters` | BBR / TCP 调优字段与取值范围（agents.read） |
| `GET` | `/api/v1/system-tcp/tasks` | 每个节点最新的 TCP 调优任务（tasks.read，可按 agent_id 筛选） |
| `DELETE` | `/api/v1/agents/{id}` | 永久撤销 Agent、立即断开 WSS 并终止其未完成任务 |
| `PUT` | `/api/v1/agents/{id}/name` | 修改面板显示的节点名称，不改变身份和安装凭据（agents.manage） |
| `POST` | `/api/v1/agents/{id}/enrollment-token` | 为该节点新增一条独立、可重复使用的 Agent 安装凭据；已有凭据继续有效（enrollment.manage） |
| `POST` | `/api/v1/agents/{id}/enrollment-command` | 幂等读取该节点已有且仍有效的安装命令，不创建或消费凭据（enrollment.manage） |
| `GET` | `/api/v1/agents/{id}/configs` | 列出节点已有的内核配置 |
| `GET` | `/api/v1/agents/{id}/configs/{engine}` | 读取节点绑定的内核配置 |
| `PUT` | `/api/v1/agents/{id}/configs/{engine}` | 以乐观版本锁创建或更新节点配置 |
| `GET` | `/api/v1/agents/{id}/configs/{engine}/files` | 返回 `{version, files:[{path,content}]}`，单文件内核返回一个文件 |
| `PUT` | `/api/v1/agents/{id}/configs/{engine}/files` | Xray / sing-box 按 `{name,description,version,files}` 原子保存合并配置；非法路径返回 400，版本冲突返回 409；不自动部署 |
| `GET` | `/api/v1/agents/{id}/configs/{engine}/workspace` | 读取服务端入站、字段目录和节点配置工作区数据 |
| `POST` | `/api/v1/agents/{id}/configs/{engine}/plans` | 生成带安全随机凭据的服务端入站方案；可传当前 `input` 以保留用户选择并重新生成随机字段 |
| `POST` | `/api/v1/agents/{id}/configs/{engine}/server-inbounds` | 新增、修改或删除服务端入站并创建校验/部署任务 |
| `GET` | `/api/v1/agents/{id}/configs/{engine}/fields/{key}` | 读取官方目录中的一个顶级配置字段 |
| `POST` | `/api/v1/agents/{id}/configs/{engine}/fields/{key}` | 新增、修改或删除顶级配置字段并创建任务 |
| `GET` | `/api/v1/deployments` | 列出每个节点/内核最近一次真实成功部署 |
| `GET` | `/api/v1/client-access` | 从已部署入站生成客户端连接资料 |
| `GET` | `/api/v1/access-controls` | 按节点、入站标签和端口读取大陆访问限制（agent-config.read） |
| `PUT` | `/api/v1/access-controls` | 保存单个入站的大陆来源/目标限制并创建校验或部署任务（agent-config.write + tasks.execute） |
| `GET` | `/api/v1/core-logs` | 查询面板集中保存的内核运行日志 |
| `PUT` | `/api/v1/agents/{id}/client-address` | 设置客户端访问地址、协议栈或单端口显示名称（agents.manage） |
| `GET` | `/api/v1/agents/{id}/region` | 读取手动设置或根据公网 IP 自动识别的国家/地区（agents.read） |
| `PUT` | `/api/v1/agents/{id}/region` | 保存国家/地区旗帜，空代码恢复自动识别（agents.manage） |
| `GET` | `/api/v1/regions` | 读取可选择的两位国家/地区代码列表（agents.read） |
| `GET` | `/api/v1/region-flags/{code}` | 读取统一风格的 4:3 SVG 国家/地区旗帜（agents.read） |
| `GET` | `/api/v1/agents/{id}/komari` | 读取节点关联的 Komari 服务器计费周期和流量配置（agents.read） |
| `PUT` | `/api/v1/agents/{id}/komari` | 设置或清除节点关联的 Komari 服务器 UUID（agents.manage） |
| `GET` | `/api/v1/config-catalogs/{engine}` | 读取内核官方配置字段和服务端协议目录 |
| `GET` | `/api/v1/configs` | 列出配置及正文 |
| `POST` | `/api/v1/configs` | 创建配置 |
| `PUT` | `/api/v1/configs/{id}` | 更新配置并增加版本号 |
| `DELETE` | `/api/v1/configs/{id}` | 软删除配置 |
| `GET` | `/api/v1/configs/{id}/revisions?limit=` | 列出最近 1–100 个配置修订 |
| `GET` | `/api/v1/configs/{id}/revisions/{version}` | 读取指定修订正文 |
| `POST` | `/api/v1/configs/{id}/revisions/{version}/restore` | 将指定修订恢复为新的当前修订 |
| `GET` | `/api/v1/tasks?agent_id=&status=&action=&limit=` | 按节点、状态和动作筛选任务；`limit` 为 1–500，默认 100 |
| `POST` | `/api/v1/tasks` | 创建远程任务 |
| `GET` | `/api/v1/tasks/{id}` | 读取单个任务及结果 |
| `GET` | `/api/v1/tasks/{id}/config-snapshot` | 读取已成功 `read-config` 或 `read-managed-config` 任务的短期配置快照（同时需要 `tasks.read` 与 `agent-config.read`） |
| `DELETE` | `/api/v1/tasks/{id}` | 取消尚未领取的任务 |
| `POST` | `/api/v1/tasks/{id}/retry` | 按当前配置重试失败或已取消任务 |
| `GET` | `/api/v1/enrollment-tokens` | 列出添加节点记录，不返回原始凭证（admin） |
| `POST` | `/api/v1/enrollment-tokens` | 创建节点绑定、可重复安装的添加命令 |
| `DELETE` | `/api/v1/enrollment-tokens/{id}` | 删除添加节点记录并立即使命令失效 |
| `POST` | `/api/v1/enrollment-tokens/{id}/command` | 幂等读取指定添加记录的已有命令（enrollment.manage） |
| `GET` | `/api/v1/settings` | 读取面板设置；Komari API Key 只返回已配置的掩码，不返回原文 |
| `PUT` | `/api/v1/settings` | 保存面板设置（admin） |
| `GET` | `/api/v1/audit?limit=` | 读取最近审计记录 |
| `GET` | `/api/v1/metrics/{agent_id}` | 读取节点最近 24 小时资源样本 |
| `GET` | `/api/v1/traffic-policies` | 读取所有端口流量配额及 Agent 最新计数（traffic.read） |
| `GET` | `/api/v1/traffic-endpoints` | 从节点当前保存的内核配置读取已有监听端口（traffic.read） |
| `GET` | `/api/v1/traffic-usage?month=&agent_id=&policy_id=` | 查询控制面持久化的端口每日流量（traffic.read） |
| `POST` | `/api/v1/traffic-policies` | 创建端口流量配额（traffic.manage） |
| `PUT` | `/api/v1/traffic-policies/{id}` | 更新端口、协议、周期或额度（traffic.manage） |
| `POST` | `/api/v1/traffic-policies/{id}/reset` | 立即清零当前周期并解封端口（traffic.manage） |
| `DELETE` | `/api/v1/traffic-policies/{id}` | 取消端口配额并移除 QControlHub 封禁；配置端口继续监控（traffic.manage） |
| `GET` | `/api/v1/templates` | 列出配置模板 |
| `POST` | `/api/v1/templates` | 创建配置模板 |
| `DELETE` | `/api/v1/templates/{id}` | 删除配置模板（admin） |
| `POST` | `/api/v1/templates/{id}/apply` | 渲染模板并保存到指定节点 |

| `GET` | `/api/v1/agent-installer` | 下载添加节点凭证保护的一键安装脚本 |
| `GET` | `/api/v1/agent-binary` | 下载添加节点凭证保护的 Agent 可执行文件 |

系统设置中的 `komari_url` 为 Komari 站点根地址，`komari_api_key` 可选；读取接口只返回掩码。节点的 Komari UUID 通过上面的节点接口保存。成功读取关联节点时，`GET /api/v1/agents/{id}/komari` 返回 Komari 的流量上限、按配置口径计算的 `server.traffic_used`（字节）及 `server.traffic_used_available`，并在 Komari 提供时返回 `effective_traffic_limit` / `traffic_reset_day`。前端按重置日显示实际周期日期范围，不把 `billing_cycle`（天）直接显示成“30 天”。

`GET /api/v1/agents/{id}/region` 优先返回节点的手动设置（`country_code` 和 `source: "manual"`）；否则只使用节点已验证的公网 IP，控制面向 GeoJS 查询国家/地区并缓存 48 小时（`source: "auto"`）。没有可用公网 IP 时返回空对象。前端通过同源接口读取 [lipis/flag-icons](https://github.com/lipis/flag-icons) 的统一 4:3 SVG 旗帜并由控制面缓存；该流程独立于 Komari 关联配置。

在节点设置中点击旗帜可搜索、选择国家/地区；保存后节点设置及客户端卡片左上角使用同一旗帜。`PUT /api/v1/agents/{id}/region` 接收 `{"country_code":"SG"}`，代码由 `/api/v1/regions` 提供，保存时去除首尾空格并转大写；`{"country_code":""}` 清除手动设置并恢复自动识别。缺少字段、`null` 或不支持的代码返回 400。设置持久化为节点的 `region_code` 标签，重连后保留，不修改其他标签、公网 IP 或客户端连接配置。只读用户只能查看；写入沿用管理权限、CSRF 和审计保护。

`GET /api/v1/overview` 中的 `configs` 只统计可在“配置档案”工作区跨节点下发的全局配置；`node_configs` 单独统计绑定到具体 Agent/内核的节点配置，避免将两类配置混为一个不可解释的总数。为兼容既有调用方，`tasks_pending` 仍表示 `pending + running` 的活动任务总数；`tasks_queued` 和 `tasks_running` 分别给出排队与执行中的精确数量。

### 内核日志查询

`GET /api/v1/core-logs` 的 `limit` 表示当前节点范围内**每种内核各自的日志条数上限**，默认 1000，可选 1–2000。未指定 `engine` 时，Mihomo、Xray、sing-box 和 Shadowsocks Rust 分别取最新的至多 `limit` 条，再按日志 ID 倒序合并；例如 `limit=2000` 最多返回 8000 条，而不是所有内核共用 2000 条。指定 `agent_id` 时只统计该节点；不指定时按全部节点中的内核类型分别计数，不是每个节点各分配一份额度。

Web 日志页保留完整返回结果用于筛选，每页渲染 200 条，可翻页查看；1000/2000 条是每内核查询窗口，不是日志保留总量。Agent 的单次上报批次仍为最多 32 条，以避免日志大包阻塞心跳。

可同时使用 `engine`、`level`、`q`（消息关键词）和 `before`（仅取小于该日志 ID 的记录）筛选；这些条件在每种内核截取数量之前生效。日志页的“每内核上限”控制读取和展示数量，不改变数据库的日志保留期限。

### 系统设置

`GET /api/v1/settings` 与 `PUT /api/v1/settings` 的设置对象包含：

```json
{
  "panel_name": "QControlHub",
  "panel_description": "可信远程编排",
  "task_page_size": 100,
  "task_poll_interval_ms": 600,
  "core_log_minimum_level": "debug",
  "webhook_url": "",
  "komari_url": "",
  "komari_api_key": ""
}
```

`core_log_minimum_level` 控制主控写入 PostgreSQL 的最低内核日志级别，可选 `debug`、`info`、`warning`、`error`、`critical` 或 `off`。默认 `debug` 保持升级前的全量保存行为；`off` 停止保存新日志。策略只影响设置保存后收到的新日志，已有记录仍按 7 天保留期清理。主控仍会校验并确认被过滤的 Agent 日志批次，因此调整策略不会中断 WSS 会话。旧客户端省略该字段时，主控保留当前值。

### 端口流量配额

schema 44 的策略响应增加 `accounting`：`source` 为 `core-api`、`nft-dual`，未启用双链路时可为空；`inbound` / `outbounds` / `mark` 是归属映射，`process_epoch` 标识内核进程，`client_received` / `client_sent` / `target_received` / `target_sent` 是本计量代次的四方向累计，`counters` 是恢复用原始基线。同一 `counter_epoch` 不允许更换归属口径。详情见 [端口流量统计](traffic-accounting.md)。

`GET /api/v1/traffic-endpoints` 会从节点当前保存的 Mihomo、Xray、sing-box 与 Shadowsocks Rust 配置中提取监听名称、端口和 TCP/UDP 范围。响应不会包含凭据或完整配置内容。控制面会把这些监听端口自动持久化为监控记录并同步给支持 `port-traffic-v1` 的 Agent；无需先创建配额即可持续统计、保存每日用量并显示实时速率。配额只是监控记录上的可选上限与封禁设置。同一节点同一端口只显示一张流量卡片。

`GET /api/v1/traffic-endpoints/sync` 需要 `traffic.manage`，只读预览返回 `candidates`：每项包含节点、名称、内核、端口、协议及 `kind`（`deleted` 已删除、`new` 配置中发现但未监控）。已删除的监控即使不在当前配置中也可恢复，已启用监控不列入候选。

`POST /api/v1/traffic-endpoints/sync` 同样需要 `traffic.manage`，必须提交 `{"selections":[{"agent_id":"agt_…","port":443}]}`，一次选择 1–4096 项。控制面重新校验候选并在一个事务中仅恢复/添加勾选端口；拒绝重复选择、未知端口和超过单节点 256 个监控的请求，失败不部分写入，重复提交已启用端口为无操作。省略选择或空选择返回 400，不再默认全量同步。客户端不可指定监听元数据。恢复以仅监控模式重新启用，不恢复已清除的历史及配额；已有监控的元数据、配额、统计和历史不变。在线 Agent 通过当前 WSS 会话接收更新，不强制重连触发额外发现；离线 Agent 在下次正常连接时接收策略，原有自动发现行为仍保留。

手动编辑的名称、内核归属与协议不会被自动发现覆盖。Agent 重连同样只补充监控记录，不清理旧端口；保存节点配置时仍会清理已不在配置中的自动发现、无配额记录。schema 46 支持零额度并记录元数据是否经过手动编辑，旧版本数据库会自动迁移。

创建与更新请求使用相同结构：

```json
{
  "agent_id": "agt_0123456789abcdef",
  "name": "Reality 入口",
  "engine": "xray",
  "port": 443,
  "protocol": "both",
  "cycle": "monthly",
  "cycle_anchor": "2026-08-01T00:00:00Z",
  "limit_bytes": 107374182400,
  "auto_block": true
}
```

`protocol` 为 `tcp`、`udp` 或 `both`，`cycle` 为 `monthly` 或 `yearly`。`cycle_anchor` 必须是当天或过去的 UTC 日期；月末和闰年按日历末日自动对齐。额度是接收与发送字节之和，同一节点同一端口只能配置一次。请求中的 `limit_bytes` 为 `0` 或省略时保持监控-only（`quota_enabled=false`，只统计不设上限），大于 `0` 时开启配额（`quota_enabled=true`）。自动发现记录的 `quota_enabled` 为 `false`；设置配额后变为 `true`。`auto_block` 默认为 `true`；设为 `false` 时 Agent 仍统计并上报流量，但不会因超额创建丢弃规则。未启用配额时 `auto_block` 会被强制为 `false`。取消已发现端口的配额只解除限额和封禁，不删除统计记录或历史。修改端口、协议、周期或起始日期会开始新的周期计数；只调整名称、内核归属、额度或自动封禁开关会保留当前已用流量。响应中的 `enforcement_available`、`enforcement_error`、`blocked`、当前周期与收发计数均来自 Agent 最新心跳。

`GET /api/v1/traffic-usage` 的 `month` 使用 `YYYY-MM`，省略时为当前 UTC 月；`agent_id` 和 `policy_id` 可选。控制面将策略当前用量和每日接收、发送、合计、峰值速率写入 PostgreSQL；删除单条配额不会删除已经保存的每日历史。

新版 Agent 同时上报实际采样时间 `collected_at`、本地计量代次 `counter_epoch` 和不随日历周期清零的 `lifetime_received_bytes` / `lifetime_sent_bytes`。数据库版本 43 保存对应的 `last_collected_at`、`counter_epoch`、`reported_lifetime_received_bytes` / `reported_lifetime_sent_bytes` 基线。策略响应通过 `last_collected_at` 区分采样时间和服务器的 `last_reported_at`；速率单位是 **字节/秒（B/s）**，由连续有效采样之间的增量和采样间隔计算，不使用网络到达间隔。重复、延迟和乱序样本不重复写历史，也不覆盖有效速率；采集失败仅更新健康状态并将速率置零，恢复时从最后成功的基线继续累计。超过两分钟的补报不显示为实时速率。

跨月/跨年后，周期用量按新周期显示，历史增量通过不清零的 lifetime 基线保留上一周期尚未上报的尾部流量。同一代次的 lifetime 倒退不作为计数器重启再次收费；本地状态确实丢失时生成新代次，即使恢复后的计数已经超过旧值也能识别。旧 Agent 不含新字段时继续使用原有兼容逻辑；数据库升级不会改写已有累计量、原始基线或每日历史，首次新版上报也不会把已入账的旧流量重复写入历史。

每日归属采用**有效采样结束时的 UTC 日期**，不是消息到达日期。跨午夜的采样间隔或长时间离线补报只提供总增量，不能据此恢复逐日的真实分布，不会用平均速率伪造离线日流量。端口的统计范围、升级顺序与精度边界见 [端口流量统计](traffic-accounting.md)。

删除 Agent 是不可逆的身份吊销：控制面先删除该节点的配置与修订、删除该节点的全部添加凭证、将其未完成任务标记为失败，再主动关闭当前认证 WSS；相同 Ed25519 身份的后续握手返回 `401`。如需重新注册，必须创建新的添加命令。

### 添加节点

```json
{
  "name": "edge-01"
}
```

`name` 同时是凭证绑定的节点名称。接口始终创建无有效期、可重复安装的添加节点命令；重复注册会更新原节点的密钥并复用节点 ID。创建时控制面保存用于认证的 SHA-256 摘要，并使用 `QCH_CONFIG_ENCRYPTION_KEY` 保存受保护的 AEAD 可恢复副本；专用读取接口带有 `Cache-Control: no-store`，普通列表与 Agent API 永不返回凭据。查看是幂等读，不增加记录、使用次数或轮换 secret。缺少当前密钥、密钥不匹配、密文损坏以及升级前仅有摘要的旧记录均 fail closed；旧记录仍可继续安装和删除，但因原文不可逆而无法查看。删除某条添加记录后，仅对应命令立即失效；删除节点会使该节点的全部安装命令失效。

### 修改节点名称

在节点详情的“Agent 与身份”中可保存自定义名称。`PUT /api/v1/agents/{id}/name` 接受 `{"name":"香港 · edge-01"}`，返回裁剪首尾空白后的同形对象。名称须为 1–100 个 Unicode 字符，不得包含控制字符；非法名称返回 `400`，节点不存在或已撤销返回 `404`。接口需要 `agents.manage` 权限，浏览器会话还需 CSRF，并记录 `agent.renamed` 审计。

名称只修改控制面展示元数据，不改变节点 ID、公钥、配置关联或现有 WSS 连接。旧安装命令保留原凭据绑定名称，仍可使用；心跳和使用旧命令重装都不会把自定义名称改回。改名不自动修改独立设置的客户端分享名称。

### 客户端端口显示名称

客户端页面的“修改显示参数”按入站定位名称。例如 `PUT /api/v1/agents/{id}/client-address` 的请求 `{"profile":{"engine":"ss-rust","tag":"ss-rust-1","port":20001},"name":"香港 · ATT"}` 只修改该内核、监听地址和端口的分享名称；不创建配置版本、不重启内核，也不修改其他端口。名称最多 100 个 Unicode 字符且不能包含控制字符；空名称恢复该端口的入站标签。已有节点级名称继续作为未单独命名端口的默认值。选择器必须匹配实际成功部署的修订；不存在或已变更的入站返回 `404`，不完整的选择器返回 `400`。

`address` 和 `address_mode`（`auto` / `ipv4` / `ipv6`）仍为整台节点共用的可选设置，省略表示不修改；新界面仅在用户改动它们时提交。旧客户端不携带 `profile` 时仍使用节点级名称接口。端口名称适用于所有地址族的分享值和 Sub-Store 默认名称，Sub-Store 自己设置的名称优先。名称绑定监听端点，不随 SS Rust 数组位置或标签改名转移到别的端口；改变监听地址/端口会使用新端点的设置。此接口继续要求 `agents.manage`、浏览器 CSRF 和审计记录。

### 创建配置

请求字段：

```json
{
	"version": 1,
	"name": "edge-xray",
  "description": "local smoke test",
  "engine": "xray",
  "content": "{\"log\":{\"loglevel\":\"warning\"},\"inbounds\":[],\"outbounds\":[]}"
}
```

更新配置时必须提交当前 `version`；版本不匹配会返回 `409`，防止两个编辑器静默覆盖彼此的修改。创建配置时忽略该字段。

`engine` 只能是 `mihomo`、`xray`、`sing-box` 或 `ss-rust`。Mihomo 正文必须是非空 YAML mapping；另外三类必须是非空 JSON object。正文上限 2 MiB。控制面的结构解析不能替代真实内核校验，应先创建 `validate` 任务。`ss-rust` 的结构校验基于官方 JSON 配置格式；由于 `ssserver` 没有非运行检查模式，真实 Agent 的 `validate` 不会启动服务。

使用仓库样例创建配置时，推荐用 `jq` 负责 JSON 转义，避免 shell 插值破坏正文：

```bash
jq -n \
  --arg name xray-smoke \
  --arg engine xray \
  --rawfile content examples/configs/xray-minimal.json \
  '{name:$name, engine:$engine, content:$content}' \
| curl --fail-with-body \
    -X POST \
    -H "Authorization: Bearer ${QCH_ADMIN_TOKEN}" \
    -H 'Content-Type: application/json' \
    --data-binary @- \
    https://qcontrolhub.example.com/api/v1/configs
```

### 配置修订与恢复

全局配置档案和节点绑定配置每次成功创建、更新或恢复时，都会在同一数据库事务中保留一份完整修订。修订列表按版本号倒序返回，默认 20 条，`limit` 可设为 1–100：

```text
GET /api/v1/configs/cfg_0123456789abcdef/revisions?limit=20
GET /api/v1/configs/cfg_0123456789abcdef/revisions/2
```

恢复不会把版本号倒退，也不会覆盖既有历史。调用方必须提交当前配置版本；例如当前为 v3、恢复 v1 时，会创建内容来自 v1 的新 v4：

```http
POST /api/v1/configs/cfg_0123456789abcdef/revisions/1/restore
Content-Type: application/json

{"expected_version":3}
```

`expected_version` 不匹配返回 `409`，无效版本号返回 `400`，配置或修订不存在（包括配置已删除）返回 `404`。升级到带修订历史的版本时，迁移会把每个现有活动配置的当前版本写为第一条可用修订，但无法重建升级前已经被覆盖的旧正文。

修订包含完整配置和代理凭据，应与当前配置正文使用相同的数据库、备份和访问保护。删除配置档案或撤销其所属节点时会永久删除对应修订，避免已删除凭据继续留存在修订表中。

### 列出任务

任务列表筛选条件可组合使用。`agent_id` 接受完整 Agent ID；`status` 允许 `pending`、`running`、`succeeded`、`failed`、`canceled`；`action` 允许 `validate`、`deploy`、`import-existing`、`read-config`、`read-managed-config`、`start`、`stop`、`restart`、`status`、`install`。`limit` 默认为 100，最大为 500；无效的 `status` 或 `action` 返回 `400`。

### 创建任务

部署或校验任务需要 `config_id`，且配置内核必须与任务内核相同：

```json
{
  "agent_id": "agt_0123456789abcdef",
  "action": "validate",
  "engine": "xray",
  "config_id": "cfg_0123456789abcdef"
}
```

`read-config` 与 `read-managed-config` 都不接受 `config_id`。前者在存在安全映射的系统服务时读取“可导入配置”，后者始终读取 QAgent 托管的“现有配置”；因此两份配置可在手动配置页独立预览和切换。未声明 `managed-config-read-v1` 的旧 Agent 在没有外部服务映射时仍沿用 `read-config` 读取托管配置；只有两份配置并存时才需要先升级 Agent。Agent 只会读取预先配置的绝对白名单路径，拒绝符号链接、不安全归属或权限、非 UTF-8 以及超过 2 MiB 的文件，并在返回前调用目标节点上的真实内核校验。读取结果作为短期配置快照保存，不出现在普通任务列表响应中；同一节点、内核和读取类型新的成功读取会清除上一份同类型快照。

服务动作不使用 `config_id`：

```json
{
  "agent_id": "agt_0123456789abcdef",
  "action": "status",
  "engine": "xray"
}
```

安装或切换内核版本同样不使用 `config_id`。`core_version` 可以是 `stable`、`development`，也可以是去掉 `v` 前缀的严格版本号：

```json
{
  "agent_id": "agt_0123456789abcdef",
  "action": "install",
  "engine": "sing-box",
  "core_version": "1.14.0-beta.3"
}
```

允许的 `action` 为 `validate`、`deploy`、`import-existing`、`read-config`、`read-managed-config`、`start`、`stop`、`restart`、`status`、`install`。Agent 必须在注册能力中声明对应内核。若心跳报告某内核检测到现有服务但无法安全映射，或 completed migration 在重启时不再满足原服务 inactive/disabled、托管服务 active/persistent-enabled 的完成态，控制面会对该内核的全部 action 返回 `409`，Agent 执行器也会独立拒绝已在途任务。`import-existing` 只接受该节点自己保存的可导入配置快照，并且只在 Agent 已精确识别、仍等待管理员确认的现有 Xray 或 sing-box 服务上执行；常规使用应从“手动配置”页的“可导入配置”视图提交。稳定版和自定义版本使用对应内核的官方 GitHub Release，不接受自定义 URL；某来源没有可用二进制时，对应任务失败而不会降级到另一来源或稳定版。

Mihomo `development` 安装额外接受 `core_source`，取值为 `official`（默认、推荐，省略该字段等价于 `official`）或 `mirror`（显式选择第三方 `vernesong/mihomo` Alpha 镜像）：

```json
{
  "agent_id": "agt_0123456789abcdef",
  "action": "install",
  "engine": "mihomo",
  "core_version": "development",
  "core_source": "official"
}
```

```json
{
  "agent_id": "agt_0123456789abcdef",
  "action": "install",
  "engine": "mihomo",
  "core_version": "development",
  "core_source": "mirror"
}
```

`core_source` 仅接受 Mihomo `development` 的 `official` 或 `mirror`。其他内核或版本通道携带 `core_source` 时返回 `400`；未声明 `mihomo-development-source-v1` 的旧 Agent 请求 `mirror` 时返回 `409`。每个来源独立解析并 fail-closed，不使用另一来源或稳定版兜底。

任务成功响应表示目标节点已完成对应操作；失败响应会保留节点返回的错误信息。部署任务只有在目标节点真实写入配置并成功重启服务后，才会进入节点的最新部署记录。

成功的 `read-config` 与 `read-managed-config` 任务不会在普通任务列表或任务详情中返回配置正文。读取完成后，同时具备 `tasks.read` 与 `agent-config.read` 的用户可使用 `GET /api/v1/tasks/{id}/config-snapshot` 获取 `{ "content": "..." }`；仅有 `tasks.read` 仍可查看任务记录，但读取快照会返回 `403`。当快照已被同一节点、内核和读取类型的后续成功读取清理时返回 `404`。

### 状态码

- `200`：查询或更新成功。
- `101`：Agent WebSocket 升级成功。
- `201`：配置、任务或 Agent 创建成功。
- `204`：删除成功。
- `400`：JSON、字段、内核或动作无效。
- `401`：令牌或 Agent 签名无效。
- `403`：浏览器 Origin 不在 CORS 白名单。
- `404`：目标不存在。
- `409`：重复值或任务状态冲突。
- `429`：鉴权失败次数过多。

## Agent 协议

Agent 协议端点如下：

| 方法 | 路径 | 鉴权 |
| --- | --- | --- |
| `POST` | `/agent/v1/enroll` | 注册 Bearer 令牌 |
| `GET` | `/agent/v1/connect` | Agent 签名的 WebSocket Upgrade |

WSS 握手必须协商子协议 `qcontrolhub.agent.v1`。服务端先发送只含该节点端口流量策略的 `hello`；当前连接首次完整 `heartbeat` 声明 `managed-public-ip-probe-v1` 且成功持久化后，服务端才以独立的 `public_ip_probe` 消息下发公网探针配置。零配置时该消息按族携带有序 primary/fallback：IPv4 为 `https://api.ipify.org/`、`https://4.ident.me`，IPv6 为 `https://api6.ipify.org`、`https://6.ident.me`；自定义族 endpoint 会替换该族完整默认链，禁用配置则两族为空。服务端不会复用数据库中的历史 feature，并按当前会话与已配置地址族约束 `control-plane-config` 来源；空 feature、降级或重连会清除失效来源，旧 Agent 忽略未知 fallback 字段并至少使用 primary。Agent 定期发送 `heartbeat`，心跳包含内核运行状态、主机资源以及端口配额的收发计数和封禁状态；`runtime.<engine>.existing_config_available` 表示已有服务可在手动配置页读取并迁移，`existing_config_unsupported_reason` 表示检测到已有服务但精确 argv、路径、歧义或 wrapper 安全边界不允许自动读取/接管，控制面只展示该原因并禁用相关操作。服务端下发带随机 lease ID 的 `task`，Agent 返回包含 `success` 和结果正文的 `result`，服务端确认 `result_ack`。连接压缩关闭，服务端要求 50 秒内收到消息，官方 Agent 默认每 15 秒心跳并在断线后指数退避重连。

## Webhook 事件

在设置页配置 Webhook 地址后，控制面会在以下时机向该地址 `POST` 一个 JSON 事件（`Content-Type: application/json`）：

| 事件类型 | 触发时机 |
| --- | --- |
| `task.failed` | 远程任务以失败结束 |
| `agent.offline` | 节点超过 2 分钟未心跳 |
| `agent.online` | 离线节点恢复心跳 |

事件负载示例：

```json
{
  "type": "task.failed",
  "time": "2026-08-14T02:00:00Z",
  "agent_id": "agt_0123456789abcdef",
  "agent": "shanghai-edge-01",
  "engine": "mihomo",
  "task_id": "tsk_0123456789abcdef",
  "action": "deploy",
  "error": "validation failed",
  "message": "任务失败：mihomo 在节点 shanghai-edge-01 上执行 deploy 失败"
}
```

配置了 `QCH_WEBHOOK_SECRET` 时，请求头携带 `X-QControlHub-Signature: sha256=<hex>`，值为 `HMAC-SHA256(secret, body)` 的十六进制摘要；接收端应使用常量时间比较校验签名，并只信任 `2xx` 之外按失败处理。未配置密钥时不带签名头，仅应在可信内网使用。

主机指标不会开放新的 Agent 监听端口。Agent 不上报通配、回环、组播或链路本地地址；控制面用服务器接收时间覆盖 Agent 时间戳，并校验百分比、容量、接口数量、名称和地址边界，只在 PostgreSQL 保存每个节点的最新快照。SPA 通过要求有效管理会话的 `GET /api/v1/metrics/{agent_id}` 获取历史样本；响应设置 `Cache-Control: no-store`。节点运行区只从实际部署版本生成客户端资料，优先使用受控节点标签中的入口地址，否则自动选择默认路由接口地址；配置编辑器不接受或传播客户端连接地址参数。

握手签名 canonical value 由固定字符串 `qcontrolhub-agent-v1`、大写 HTTP 方法、原始转义路径及查询串、Agent ID、Unix 秒时间戳、随机 nonce、空正文 SHA-256 十六进制摘要以换行连接。握手需要以下头：`X-QControlHub-Agent-ID`、`X-QControlHub-Timestamp`、`X-QControlHub-Nonce`、`X-QControlHub-Signature`。有效时间窗为正负 90 秒，nonce 在窗口内只能使用一次。应直接使用官方 Go Agent，避免自行实现导致编码、代理重写、lease 或防重放错误。

## 系统 BBR / TCP 调优

`GET /api/v1/agents` 的 `metrics.bbr` 返回 Agent 直接读取的系统参数，包含 `collected_at`、`kernel_release`、`congestion_control`、`default_qdisc`、`available_algorithms`、`parameters`、网卡实际 `qdiscs` 及采集错误。即使 BBR 是 SSH、脚本或其他工具开启的，也会据实显示，不依赖面板操作记录。`persistence=unmanaged` 仅表示没有本面板的托管文件，**不表示 BBR 未开启，也不表示系统没有其他持久化配置**。`configured_parameters` 是面板文件里的值，可能与当前生效值不同。

`GET /api/v1/system-tcp/parameters` 返回可编辑字段、数值范围、三元组要求和枚举值，需 `agents.read`。系统实际值允许超出编辑白名单显示；只有受支持的取值可下发。`tcp_rmem/tcp_wmem` 是三个从小到大、以空格分隔的字节数。

`GET /api/v1/system-tcp/tasks` 需 `tasks.read`，返回每个未撤销节点最新的一条 TCP 任务，支持 `agent_id` 筛选；不会因其他节点的任务历史过多而遗漏状态。响应包含动作、参数快照、状态、时间和错误，不包含完整执行输出；完整历史和输出仍从 `/tasks` 查看。

调优复用 `POST /api/v1/tasks`，需同时具备 `tasks.execute` 与 `agents.manage`，目标节点须在线并通过心跳声明 `system-bbr-v1`；任务重试使用相同权限。示例：

```json
{
  "agent_id": "agt_example",
  "engine": "",
  "action": "configure-tcp",
  "tcp_settings": {
    "net.ipv4.tcp_congestion_control": "bbr",
    "net.core.default_qdisc": "fq",
    "net.ipv4.tcp_rmem": "4096 131072 16777216"
  }
}
```

示例值仅说明格式，不是适用于所有节点的优化建议。仅提交选中的参数；未提交的系统参数和原托管项不变。`enable-bbr`、`disable-bbr` 是不接受 `tcp_settings` 的快捷任务，分别设置 `bbr + fq`、`cubic + fq`。三种任务都不能关联代理内核、配置 ID、内核版本或来源。同节点不同的未完成 TCP 调优请求返回 `409`；相同请求复用原任务。`tasks.read` 可查看参数快照和执行结果。旧版 Agent 不会认领或继续执行这些任务。

Agent 逐项写入、回读核验后合并保存到 `/etc/sysctl.d/90-qcontrolhub-bbr.conf`。常规失败会尝试恢复原值和原文件，回滚失败在任务错误中明确上报。执行成功并不代表所有已有连接切换算法；页面以采集时间及实际值为准，不将“任务已提交”显示成“参数已生效”。
## 内核能力默认值与节点开关

`GET /api/v1/settings` 返回 `default_agent_engines`（数组）。`PUT /api/v1/settings` 可随其他设置及 `revision` 一起保存该字段；`[]` 明确关闭全部新 Agent 默认能力，省略或 `null` 保留现值。需要 `settings.manage`。默认值只在新节点注册时与 Agent 声明的支持范围求交集，不批量修改已有节点，也不是禁止节点独立开启的全局限制。

Agent 对象中 `capabilities` 为当前启用能力，`supported_capabilities` 为注册声明的支持范围。`capability_transitions` 按内核返回最近一次切换的 `task_id`、`status` 和目标 `enabled`，可用于展示等待/失败状态。`PUT /api/v1/agents/{id}/capabilities/{engine}` 使用 `{"enabled":true}` 或 `{"enabled":false}` 单独修改一种能力，需要 `agents.manage`，沿用会话 CSRF 保护。不同内核的并发修改不会互相覆盖。

已安装内核：关闭提交 `stop` 任务，重新开启提交 `start` 任务，返回 HTTP 202 和 `{"enabled":当前生效值,"task_id":"..."}`，记录 `agent.capability.requested` 审计。任务成功回执与能力更新原子提交；失败、取消或超时失败不改变能力。执行期间对应内核的新任务、配置写入和重复/反向切换返回 409。离线节点上线后执行；任务页重试保留其能力切换语义。普通服务启停任务不会改变能力。

未安装内核或无需变更：返回 HTTP 200 和 `{"enabled":...}`，不创建启停任务，记录 `agent.capability.updated` 审计。参数非法/Agent 不支持返回 400，节点不存在或已撤销返回 404，已有待处理/执行中内核任务阻止切换并返回 409。

开关不会删除配置、卸载内核或取消已有流量规则。所有能力关闭的 Agent 仍可连接、上报监控和执行 Agent 级操作。重新使用原安装凭据注册会保留节点选择；若新 Agent 不再声明某种内核，该内核不再启用。全局默认能力依然只影响新节点注册，不批量启停已有服务。
