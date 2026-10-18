# QControlHub AI 只读排查接入

将本文件和管理员提供的 API Base URL 交给 AI。凭据通过运行环境注入。

## 连接

- API Base URL：`https://panel.example.com/api/v1`（占位地址，换成实际面板地址）。
- 认证：`Authorization: Bearer <API_KEY>`。
- Key 不支持网页登录、创建任务、修改配置或导出客户端连接凭据。
- 先请求 `GET /api-key`：返回当前 Key 标识、有效权限、来源 IP、白名单、工作区及可用 `endpoints`。以后续响应中的接口清单为准；未授权的接口返回 403。
- 有白名单时，请从允许的出口访问。不要自行添加转发头来改变来源。
- 本说明使用的 ID、域名和时间均为占位值。请从实际响应取得 ID。

```bash
export QCH_API_BASE='https://panel.example.com/api/v1'
# 在运行环境或密钥管理器中设置 QCH_API_KEY；不把实际 Key 写进文档或脚本。
export QCH_API_KEY='<API_KEY>'
curl --fail-with-body --silent --show-error \
  --config <(printf 'header = "Authorization: Bearer %s"\n' "$QCH_API_KEY") \
  "$QCH_API_BASE/api-key"
```

上例使用 Bash。随附的 `diagnose.sh` 通过 Python 标准库发起请求，适用于无 Bash 进程替换的环境；凭据不出现在 URL 或 curl 命令参数中。

## 默认预设的现有读取接口

所有路径均相对于 API Base URL；`{id}` 等是路径参数。HTTP 方法为 GET。

| 权限 | 接口 |
| --- | --- |
| 无附加权限 | `/api-key`（只返回本 Key 的授权信息） |
| `overview.read` | `/overview` |
| `agents.read` | `/agents`、`/agent-directory` |
| `deployments.read` | `/deployments` |
| `catalogs.read` | `/config-catalogs/{engine}` |
| `agent-config.read` | `/agents/{id}/configs`、`/agents/{id}/configs/{engine}` 及其 `/state`、`/workspace`、`/files`、`/fields/{key}` |
| `configs.read` | `/configs`、`/configs/{id}/revisions`、`/configs/{id}/revisions/{version}` |
| `tasks.read` | `/tasks`、`/tasks/{id}`、`/system-tcp/tasks` |
| `tasks.read` + `agent-config.read` | `/tasks/{id}/config-snapshot` |
| `settings.read` | `/settings`、`/settings/deployment` |
| `audit.read` | `/audit` |
| `metrics.read` | `/metrics/{id}`（节点 ID） |
| `panel-metrics.read` | `/panel-metrics` |
| `core-logs.read` | `/core-log-sources`、`/core-logs`、`/client-connections` |
| `traffic.read` | `/traffic-policies`、`/traffic-endpoints`、`/traffic-usage` |
| `templates.read` | `/templates` |

只读预设授予上述 14 项现有权限。管理员可减少授权；Key 权限、创建者当前权限和现有资源可见范围同时约束响应。账户管理员在现有权限模型中持有全部权限；降为普通用户后 Key 被撤销。

`/agent-directory` 沿用现有“其他用户节点”摘要规则，可能显示被隐藏节点的摘要；这不会授予其配置、指标、任务或日志访问。其他列表、详情和日志筛选沿用对应资源的既有规则。

## 参数、分页和时间

- `engine`：`mihomo`、`xray`、`sing-box`、`ss-rust`。
- 时间字段为带时区的 ISO 8601 / RFC3339，例如 `2026-10-19T02:00:00+08:00`；在查询参数里对 `+` 做 URL 编码，或使用 `curl --get --data-urlencode`。
- `/tasks`：`agent_id`、`status`（`pending/running/succeeded/failed/canceled`）、`action`、`limit`；默认 100，最大 500。返回数组，现有接口没有页码或游标，不要假定存在完整历史分页。
- `/core-log-sources`：返回允许读取的节点、实例、内核和采集状态。实例 `main` 为主实例，`shr_…` 为共享实例。
- `/core-logs`：`agent_id`、`instance_id`、`user_id`、`engine`、`level`（`debug/info/warning/error/critical`）、`q`、`limit`（默认 1000，1–2000，按内核限额）、`before`（正整数日志 ID）。取上一页最小日志 ID 作为 `before`；带相同资源筛选继续读取。每个实例/内核窗口独立，不要把多源结果当作单一总量限制。
- `/audit`：`limit`（默认 50，1–200），返回最新记录，无分页游标。
- `/metrics/{id}`：`since`（RFC3339，默认最近 24 小时）、`limit`（默认 1500，1–5000）。
- `/traffic-usage`：`month=YYYY-MM`、`agent_id`、`policy_id`。
- `/client-connections`：`agent_id`、`engine`、`protocol`、`inbound`、`transport`、`client_ip`、`since`、`until`、`limit`、`cursor` 等；按返回的 `next_cursor` 继续查询，并保持筛选一致。排查时用 `locations=cached` 避免触发外部位置解析，`timeline=false` 可省略时间线。
- `/agents`、`/configs`、`/deployments` 和 `/templates` 返回现有数组，没有额外新增的分页参数。
- 无资源访问权时通常返回 404；不要通过修改 ID 或筛选条件探测其他账号数据。

## 推荐排查顺序

1. `/api-key` 核对权限、到期时间、来源 IP 与工作区；`/settings/deployment` 核对面板和安装包版本。
2. `/agents` 找到节点 ID，检查 `status`、`version`、`capabilities`、`features`、`runtime`；没有 `metrics.read` 时节点指标为空。
3. `/agents/{id}/configs/{engine}/state` 检查内核/实例状态；`/deployments` 对照实际部署的配置 ID、版本和任务。
4. `/agents/{id}/configs/{engine}` 或 `/workspace` 查看当前工作区配置；`/files`、`/fields/{key}`（字段名从内核 catalog 获取）用于聚焦结构。当前工作区配置与其他用户共享实例的配置是不同资源，按响应 ID 关联。
5. `/tasks?agent_id=…&status=failed&limit=50` 找失败任务，再 `/tasks/{id}` 查看动作、状态、尝试次数、输出和错误。只有成功的 `read-config` / `read-managed-config` 任务可读取 `/config-snapshot`，其他任务返回 404。Key 可以读取已有快照；需要新采集时交由管理员执行。
6. `/configs/{id}/revisions` 对照变更历史，再按版本读取详情。保留配置 ID、版本、部署任务 ID 和引用关系。
7. `/core-log-sources` 确定可读的 `agent_id`、`instance_id`、`engine`；用这组筛选读 `/core-logs`，再结合 `/traffic-usage`、`/client-connections` 和 `/audit` 检查时间线。
8. 输出结论时区分已观察的事实和推测，附节点、实例、配置版本、任务及日志 ID。脱敏字段为 `[redacted]`，不要尝试还原凭据。

## curl 筛选示例

```bash
curl --fail-with-body --silent --show-error \
  --config <(printf 'header = "Authorization: Bearer %s"\n' "$QCH_API_KEY") \
  --get "$QCH_API_BASE/tasks" \
  --data-urlencode 'agent_id=agt_0000000000000001' \
  --data-urlencode 'status=failed' --data-urlencode 'limit=50'

curl --fail-with-body --silent --show-error \
  --config <(printf 'header = "Authorization: Bearer %s"\n' "$QCH_API_KEY") \
  --get "$QCH_API_BASE/core-logs" \
  --data-urlencode 'agent_id=agt_0000000000000001' \
  --data-urlencode 'instance_id=main' --data-urlencode 'engine=mihomo' \
  --data-urlencode 'level=error' --data-urlencode 'limit=100'
```

## 错误和定位

- 401：无效、过期、停用、撤销、创建者无效均使用相同认证错误；联系管理员确认 Key 状态。
- 403：认证有效，但来源 IP 或读取接口/权限被拒绝。检查响应 `error` 和 `/api-key`；IP 不允许时该查询也被拒绝。
- 400：参数或格式无效；404：资源不存在、不可见或没有可用快照；409：管理时版本冲突；429：认证失败过多，等待限流窗口。
- 500：保留响应 `request_id` 和 `X-Request-ID`，交给管理员搜索控制面日志。现有 `code`（例如 `task_schema_incompatible`）和 `stage` 字段会保留；普通业务错误通常只有 `error`，不要假定每个错误都带 `code` 或请求 ID。
- 任务 ID 为 `tsk_…`，配置 ID 为 `cfg_…`，节点 ID 为 `agt_…`。Key 调用审计的 `target` 是 `key_…`，`actor` 为 `key:key_…`，`detail` 包含 HTTP 方法、接口模板和状态码；来源在 `remote_ip`，500 时还会带请求 ID。
- `last_used_at` 仅为成功调用更新，同一 Key 最多每 5 分钟写一次；调用审计逐次记录允许或拒绝结果。
