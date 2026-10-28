# 只读 API

管理员在系统设置创建 API Key，配置有效期、读取权限和可选来源 IP 白名单。Key 原文仅创建时显示；调用使用 `Authorization: Bearer <API_KEY>`。API Key 不用于网页登录或写操作，响应中的配置凭据会脱敏。

## 连接与示例

API Base URL 为 `https://panel.example.com/api/v1`，替换为实际地址。先请求 `/api-key`，核对有效权限、来源 IP、工作区和 `endpoints`。读取范围同时受 Key 授权、创建者当前权限及资源可见性限制。

提供 [Python 标准库诊断示例](api-diagnose.sh)，通过环境变量读取凭据，按已授权接口查询节点、配置状态、任务及日志：

```sh
export QCH_API_BASE=https://panel.example.com/api/v1
# 通过运行环境或密钥管理器设置 QCH_API_KEY。
sh docs/api-diagnose.sh
# 也可显式指定节点及内核：
sh docs/api-diagnose.sh agt_0000000000000001 mihomo
```

脚本需要 Python 3，命令从仓库根目录运行。

## 读取接口

以下均为 GET，路径相对于 API Base URL；实际可用接口以 `/api-key` 返回值为准。

| 权限 | 接口 |
| --- | --- |
| 无附加权限 | `/api-key` |
| `overview.read` | `/overview` |
| `agents.read` | `/agents`、`/agent-directory` |
| `deployments.read` | `/deployments` |
| `catalogs.read` | `/config-catalogs/{engine}` |
| `agent-config.read` | `/agents/{id}/configs`、`/agents/{id}/configs/{engine}` 及其 `/state`、`/workspace`、`/files`、`/fields/{key}` |
| `configs.read` | `/configs`、`/configs/{id}/revisions`、`/configs/{id}/revisions/{version}` |
| `tasks.read` | `/tasks`、`/tasks/{id}`、`/system-tcp/tasks` |
| `tasks.read` + `agent-config.read` | `/tasks/{id}/config-snapshot`，限已成功的配置读取任务 |
| `settings.read` | `/settings`、`/settings/deployment` |
| `audit.read` | `/audit` |
| `metrics.read` | `/metrics/{id}` |
| `panel-metrics.read` | `/panel-metrics` |
| `core-logs.read` | `/core-log-sources`、`/core-logs`、`/client-connections` |
| `traffic.read` | `/traffic-policies`、`/traffic-endpoints`、`/traffic-usage` |
| `templates.read` | `/templates` |

## 筛选与分页

内核标识为 `mihomo`、`xray`、`sing-box`、`ss-rust`。时间使用带时区的 RFC3339，查询中的 `+` 需 URL 编码。

| 接口 | 参数与分页 |
| --- | --- |
| `/tasks` | `agent_id`、`status`、`action`、`limit`；默认 100，最大 500，无页码或游标 |
| `/core-log-sources` | 返回可读节点、实例、内核与采集状态；`main` 为主实例，`shr_…` 为共享实例 |
| `/core-logs` | `agent_id`、`instance_id`、`user_id`、`engine`、`level`、`q`、`limit`、`before`；默认 1000，最大 2000，按内核限额；取上一页最小日志 ID 作为 `before` 并保持资源筛选 |
| `/audit` | `limit` 默认 50，最大 200，无游标 |
| `/metrics/{id}` | `since` 默认最近 24 小时；`limit` 默认 1500，最大 5000 |
| `/traffic-usage` | `month=YYYY-MM`、`agent_id`、`policy_id` |
| `/client-connections` | 节点、内核、协议、入站、传输、客户端 IP、`since`、`until`、`limit`、`cursor`；按 `next_cursor` 翻页并保留筛选；`locations=cached` 使用已有位置，`timeline=false` 省略时间线 |

`/agents`、`/configs`、`/deployments`、`/templates` 返回数组，无额外分页参数。

## 错误定位

- 401：凭据无效、过期、停用、撤销或创建者失效。
- 403：来源 IP、接口或读取权限不允许。
- 400：参数无效；404：资源不存在、不可见或无可用快照；429：认证失败限流。
- 500：保留 `request_id` 或 `X-Request-ID`，由管理员查询控制面日志；业务错误可能只含 `error`，部分接口另带 `code`、`stage`。

定位时保留节点、实例、配置版本、任务和日志 ID。Key 调用审计使用 `key_…` 标识，记录方法、接口、状态及来源 IP；`last_used_at` 成功调用时最多每 5 分钟更新一次。
