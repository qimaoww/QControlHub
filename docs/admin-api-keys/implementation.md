# 数据结构与鉴权接入

## 数据结构

Schema 从 80 升级到 81，复用现有迁移锁与事务，不需要手工建表。

`admin_api_keys`：

- `id`：`key_` + 随机标识；`name`：1–100 个字符；`prefix`：仅显示完整凭据前 16 字符。
- `digest`：唯一的 32 字节 SHA-256 验证摘要。密钥为 `qch_key_` + 32 随机字节的 base64url 编码（256 位随机熵）。摘要和完整 Key 不出现在元数据响应中。
- `creator_type/creator_id`：数据库用户的持久 ID，或环境管理员令牌摘要经域分离再次散列得到的注册表标识。环境令牌明文和原始认证摘要均不写入此表。
- `owner_id`：创建者原工作区；兼容环境管理员的空工作区；不会根据新 Key 生成新账号或工作区。
- `permissions/ip_allowlist`：JSONB；前者限定为现有 14 项读取权限；后者为规范化后的地址/CIDR 数组。
- `disabled/created_at/expires_at/last_used_at/revoked_at/revision`：状态与时间。`status` 是响应计算值，按撤销、停用、过期、有效的优先级展示。
- `panel_users_revoke_api_keys` 触发器：账号停用、角色变为非管理员或删除时，在同一事务永久撤销其全部 Key。重新启用/升为管理员不会复活旧 Key。

## 管理接口

需要现有管理员 Cookie + CSRF 或环境管理员 Bearer。普通用户和 API Key 不能调用；每位管理员仅管理自己创建的 Key。

| 方法 | 路径 | 结果 |
| --- | --- | --- |
| GET | `/api/v1/settings/api-keys` | `{items, read_permissions}`，不含摘要、完整 Key |
| POST | `/api/v1/settings/api-keys` | 201 `{key, secret}`；`secret` 仅此次返回 |
| PUT | `/api/v1/settings/api-keys/{id}` | 更新元数据；传完整授权和当前 `revision`，冲突 409 |
| DELETE | `/api/v1/settings/api-keys/{id}` | 永久撤销；保留元数据用于追踪；已撤销不能编辑或恢复 |
| GET | `/api/v1/api-key` | 仅 API Key 查询自身有效授权、IP 和可用读取接口 |

创建/更新请求字段：

```json
{
  "name": "AI 排查",
  "permissions": ["agents.read", "agent-config.read", "tasks.read", "core-logs.read"],
  "ip_allowlist": ["192.0.2.10", "2001:db8::/32"],
  "expires_at": "2026-12-01T00:00:00+08:00",
  "disabled": false,
  "revision": 1
}
```

创建时 `revision` 可省略。`expires_at: null` 为不设到期时间；新设到期时间必须在未来，过期 Key 可延长有效期。停用可恢复，撤销不能恢复。创建和更新都要求至少一项读取权限；未知权限、写权限和无效地址返回 400。空白名单为不限制。

## 统一鉴权

1. `validateSession` 优先匹配现有环境令牌；`qch_key_` 凭据则按摘要查数据库。旧 Cookie、环境令牌继续走现有路径。
2. 每次请求重新核对 Key 状态和创建者当前身份。数据库账号必须存在、启用且仍是管理员；环境管理员必须仍在本进程当前令牌注册表中。环境令牌移除或更换后对应 Key 不能使用。
3. 保留创建者的管理员资源身份和 `owner_id`；有效权限计算为 Key 授权与创建者现有有效权限的交集。
4. `sessionAllows` 对 API Key 只检查交集权限，忽略管理员角色的隐式全部授权；中间件同样走此逻辑。
5. 按已审阅的接口模板 `diagnosticRoutes` 再次限制读取行为。写入、Key 管理、重试编辑器、远程同步及客户端凭据导出不在清单中，即使 HTTP 方法是 GET 也不能访问。新增接口默认不可用。
6. 继续使用 `WithConfigScope`、`WithPrincipalAuth`、节点/内核访问校验和现有 SQL 资源筛选，复用配置工作区、任务及日志实例归属。没有全局系统 scope。
7. API Key 不创建 Cookie、不接受网页登录、不通过 `/auth/session` 暴露浏览器会话；Bearer 与 Cookie 同时存在时由 Bearer 身份决定，不混用账号。

401 隐藏无效、过期、停用、撤销与无效创建者的差异；有效 Key 的 IP 和权限拒绝为 403。资源不可见延续现有 404。身份验证失败沿用原有限流。

## IP 白名单

- 最多 100 条，接受 IPv4、IPv6、CIDR，拒绝主机名、端口、zone ID 和无效前缀。
- 保存时去空格、标准化 IPv6、CIDR 网络位归零、去重；`/32`、`/128` 与单地址合并。
- IPv4 映射 IPv6 地址统一 `Unmap()`；映射 CIDR 的前缀减 96 后转换为 IPv4，短于 `/96` 的混合地址空间规则拒绝。
- 空列表不限制；非空按任一规则命中；无法识别合法来源地址时不匹配。
- 使用现有 `authn.ClientIP` 和 `QCH_TRUSTED_PROXY_CIDRS`。直接来源忽略 `X-Forwarded-For`、`Forwarded`、`X-Real-IP`；仅连接对端可信时处理 XFF。
- 转发链从右向左跳过已配置代理，在第一个不可信地址停止；其左侧声称的客户端地址不能覆盖结果。多行 XFF 合并后解析。
- 合法内网和回环来源保留。可信代理的畸形转发链返回无法识别，不回退为可能被放行的代理地址；未提供 XFF 时使用连接对端。
- 每次请求查数据库，所以更改白名单、权限、停用与撤销无需重启，立即作用于后续请求。

## 脱敏、审计和最近使用

API Key 的诊断响应在统一响应适配层脱敏，包含直接编码的内核日志。解析 JSON/YAML 配置、文件内容和片段，保留字段/数组结构、引用、端口、版本、资源 ID 和错误位置；密码、私钥、令牌、认证 UUID、URL 用户凭据、查询凭据、Bearer 和完整 API Key 替换为 `[redacted]`。私钥 PEM 和日志继续复用并补全 `core.RedactDiagnostic`。脱敏后的配置用于阅读，不能用来覆盖原配置。

Key 创建、编辑、停用和撤销与既有 `audit_logs` 写入同一事务，审计失败则事务回滚。Key 认证后每次调用（包括拒绝）记录 Key ID、规范接口模板、方法、状态、来源 IP 和可用的请求 ID；已识别 Key 的过期/停用/撤销认证失败也有记录。调用审计沿用现有异步审计机制，数据库不可用或进程突然退出时不保证已经排队的记录落盘；管理操作审计具有事务保证。

完整 Key 不写入 URL、服务器请求日志、审计、错误或列表。服务器请求路径中的完整 Key 模式也做脱敏。`last_used_at` 只在成功请求后按 SQL 条件最多每 5 分钟更新一次，不是授权缓存；权限和白名单每次实时读取。
