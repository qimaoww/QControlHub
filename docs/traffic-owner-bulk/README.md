# 流量页：确认部署、用户归属和批量操作

## 基准与对比条件

基准提交：`8834b33d2fad0539f935b14976e2892ddbd38a71`，开始修改前 fetch 的最新 main，含 PR #303 入站删除和监控退役修复。分支：`codex/traffic-deployment-owner-bulk`。

真实 Chromium + HTTP API + PostgreSQL，使用隔离的管理员、Alice、Bob 测试账号。截图前后使用同一个管理员账号和同一份数据库快照。桌面 1440×1000，手机 390×844、触屏；额外检查平板 820×1180。主题分别配对为浅色、深色，页面为流量页默认状态、顶部滚动位置。

部署由本地 Go 夹具模拟 Agent 的任务领取及版本化成功回执，包含共享实例身份；没有对生产节点执行操作。这些录屏验证的是 API、持久状态、权限与真实页面更新，不能作为生产内核部署或网络计费证据。

## 实现

### 部署资格

- schema 80→81 为基础/共享实例增加持久确认标志。成功部署、导入或已验证的实例生效观察才设置确认；首次任务领取不再把尝试部署的配置写成生效配置。
- 生命周期执行中的“不确定”与上一份已确认配置分别处理：首次失败/排队/执行/仅草稿/仅校验无当前端点；已有生效配置在更新失败、回滚、停止、离线期间保留。状态显示节点离线、服务停止或等待确认。
- `AppliedTrafficEndpoints`、当前策略查询与 `mergeTrafficPorts` 校验同一节点、内核、配置及生效版本。策略创建或启用本身不能证明部署成功。无部署凭据的策略无法经合并重新出现。归档配置部署到节点时，也从实际实例记录取节点身份。
- 成功删除监听仍执行 main 的准确实例退役与 generation 规则，最后一个入站为空集合。当前列表排除退役监听，历史、累计额度和预留遵循原有规则。
- 升级/重连/策略读取在节点锁内修正非共享残留，停用不在确认监听集合中的采集，清零速率、保留用量/历史/配额；共享预留的保护和计费限制保留。

### 用户归属

服务端按认证配置范围、配置 owner、基础/共享实例记录及分享授权生成 `owner_id`、`owner_name`、`is_current_user`。节点所有者不会替代端口创建用户。兼容环境管理员令牌的空 owner 身份、旧普通令牌、数据库管理员及普通用户账号。

页面默认与重置均为当前用户，全部沿用服务端既有可见范围；在卡片辅助文字标识所属用户。卡片、计数、汇总、内核/端口选项、节点侧栏计数、同步候选与批量目标使用同一用户范围。账号切换沿用现有 session 数据清理。

### 批量操作

- 复用节点批量栏、复选框、配额弹窗样式和操作确认组件；批量期间关闭卡片拖动。
- 使用策略稳定 ID；全选只选当前筛选中符合单项权限的记录，共享计费记录不可改。取消配额仅选已有个人配额。
- 删除监控记录及其历史、清零当前周期并解封、设置公共配额字段、取消配额均复用单项事务函数。公共字段仅包括明确勾选的额度、周期、起始日期、自动封禁；保留每条记录身份与归属。
- 每项独立验证当前账号、节点管理权、共享限制、记录启用状态及确认部署身份。返回成功、失败、未执行及逐项原因；失败重试只提交失败 ID。
- 请求 ID、载荷摘要与逐项事务结果持久保存，并发或丢失 HTTP 响应后重放不会再次清零。UI 提交/确认期间防重复；网络结果未知时使用相同请求 ID 获取已有结果。
- 筛选变化清空选择；刷新保留有效选择，删除或失权目标移除；完成后重取列表、汇总及选项。

## 真实对比

| 视口 / 主题 | 修改前 | 修改后 |
| --- | --- | --- |
| 桌面浅色 | [before](evidence/before-desktop-light.png) | [after](evidence/after-desktop-light.png) |
| 桌面深色 | [before](evidence/before-desktop-dark.png) | [after](evidence/after-desktop-dark.png) |
| 手机浅色 | [before](evidence/before-mobile-light.png) | [after](evidence/after-mobile-light.png) |
| 手机深色 | [before](evidence/before-mobile-dark.png) | [after](evidence/after-mobile-dark.png) |

- [全部用户归属](evidence/after-desktop-all.png)
- [公共配额字段选择](evidence/after-desktop-batch-quota.png)、[逐项结果](evidence/after-desktop-batch-result.png)
- [手机卡片](evidence/after-mobile-cards.png)、[手机批量结果](evidence/after-mobile-batch-result.png)、[平板深色](evidence/after-tablet-dark.png)
- [桌面用户筛选、全选、刷新保留及批量配额录屏](evidence/user-filter-batch-desktop.webm)
- [手机批量录屏](evidence/user-filter-batch-mobile.webm)
- [部署删除后自动刷新录屏](evidence/deployment-auto-refresh.webm)、[删除后页面](evidence/after-deployment-delete.png)、[任务及端点结果](logs/deployment-delete.jsonl)

## 验证

本地执行现有检查与构建，最终全量结果见下表。没有新增或修改 GitHub workflow、job 或门禁。

| 检查 | 结果 |
| --- | --- |
| `make non-browser-checks` | 通过，包含模块、格式、schema/PR 规则、部署脚本与 vet |
| `make build`、最终 `make fmt-check vet build` | 通过 |
| 最终 `make frontend-module-check styles-check` | 通过 |
| `make test` | 通过，含 Agent 237s、API 250s、Store 157s 及全部其他包 |
| 最终 `go test -count=1 -timeout 10m -p 1 ./internal/store ./internal/api -run 'Traffic\|WSS'` | 通过：Store 25.248s、API 7.435s；包含最终迁移/归档/身份修复 |
| `make core-install-browser-check multi-user-browser-check core-log-browser-check` | 全部通过，包含桌面/手机与证书会话 |
| 流量布局四模式（普通/密集 × 桌面/手机） | 全部通过 |
| `make frontend-browser-check` | 失败：既有 `config-scope` 断言；在未修改基准 main 上独立复现同一错误 |

已保留[全量 Go 测试](logs/test-full-final.log)、[最终流量回归](logs/final-traffic-tests.log)、[构建](logs/final-build.log)、[完整浏览器输出](logs/browser-check.log)、[基准 main 复现](logs/baseline-config-scope.log)、[流量四模式](logs/traffic-browser-final.log)、[真实账号验收](logs/browser-acceptance.json)及[其他浏览器检查](logs/api-browser.log)。配置页错误为 `.live-engine-status` 标签被旧断言认为泄漏，不在本次修改的配置页代码中。相关流量模式及选择动效模式通过，没有绕过或修改此断言。

首次全量 Store/API 运行时，旧 WSS 夹具仅创建配额却期待已有监控，因新的部署约束失败；已将夹具改为先完成真实任务部署，独立 WSS 回归随后通过。

已完成的针对性验证：

- Store：草稿、配额、校验成功、首次失败、pending/running、首次成功、更新失败保留旧版本、停止、离线、持久重开、三用户共享实例及旧管理员身份；schema 80 升级时的首次失败与旧生效配置、归档部署目标、同端口其他内核残留。
- Store：批量四种操作、公共字段保留、相邻端口隔离、数据库约束注入单项失败、只重试失败、并发重复请求只清零一次、同请求 ID 不同载荷拒绝、共享/无权限/不存在/未部署目标未执行。
- API：认证归属、管理员不会继承普通用户端口、逐项权限、请求重放、删除 tombstone。
- 前端模块：仅确认策略与端点合并、跨实例旧策略排除、默认/全部、归属标签、筛选清空、刷新裁剪选择、自动刷新端点集合。
- 真实浏览器：Alice 自有及受分享节点共 2 个本人端口，Bob 1 个；管理员本人 2/全部 5；Alice 全部保持授权 2；账号切换隔离；共享记录不可选；同步候选随用户范围从 6→2。

未验证：生产部署、真实节点断网/回滚故障注入、生产累计计费流量、实体手机与平板。相应状态由任务/数据库回归及 Chromium 触屏视口验证；不声称完成实体设备或生产验证。

## 清理

- 本地测试结束：2026-10-19 02:25:28，Asia/Shanghai（UTC+8）。
- 持久任务标识：`qch-traffic-owner-bulk-20261019`。
- 执行时间：**2026-10-22 02:26:00，Asia/Shanghai（UTC+8）**；UTC 2026-10-21 18:26。按分钟调度，不早于测试结束后 72 小时；失败保留任务逐分钟重试。
- 登记：`/etc/cron.d/qch-traffic-owner-bulk-20261019`，实际脚本与清单在 `/var/lib/qcontrolhub-task-cleanup/qch-traffic-owner-bulk-20261019/`；cron 服务 active、enabled，脚本已验证到期前不执行删除。
- 范围：本次专用 `qch-traffic-owner-bulk-20261019-db` 容器及唯一匿名 PostgreSQL 卷（包含本次账号/节点/实例/测试数据库）；本工作树的 `output/playwright/traffic-owner-bulk` 与 `/tmp/qch-traffic-owner-bulk-baseline`。
- 容器 ID、名称、任务标签、卷 ID/创建时间以及目录所有权标记逐项匹配后才清理。拒绝删除后来重新运行的容器或身份变更资源。
- 已停止所有本次测试进程、关闭两个本次浏览器会话、停止专用数据库，55591/55592 均无监听；构建二进制已移除。
- 报告、截图、录屏和已脱敏必要日志在 `docs/traffic-owner-bulk/` 单独保留；执行回执将写入持久登记目录 `receipt.json`。

[清理代码](cleanup/cleanup.py) / [脱敏登记清单](cleanup/schedule.json)。主机 cron 持久登记成功；未创建 GitHub CI 或检查项。
