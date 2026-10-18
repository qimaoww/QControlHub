# 本地验证记录

## 环境与基准

- 基准：`8834b33d2fad0539f935b14976e2892ddbd38a71`，修改前记录并提交。
- 本机专用 PostgreSQL 17 容器：`qch-api-keys-pr304-db`，标签 `qch.task=api-keys-pr304`；数据库 `qch_api_keys`，与业务库隔离。
- 控制面实际监听 `127.0.0.1:38584`；前端验证代理 `38585`；基准前端 `38586`。基准前端来自上述提交的原文件，使用同一测试控制面。
- Chromium 浏览器真实操作和截图；桌面 1440×1000，手机 390×844，平板 834×1112。手机和平板使用触屏/粗指针模拟，分别验证浅色和深色。没有在实体 iOS/Android 设备上验证。
- Agent 上报及内核结果由独立测试库中的 fixture 模拟；HTTP、数据库迁移、浏览器、凭据验证和控制面进程重启均实际执行。未连接生产 Agent，未运行真实代理内核，也未部署真实多层反向代理。

## 验收覆盖

| 项目 | 结果与证据 |
| --- | --- |
| 创建、查询授权、编辑、过期、停用、撤销 | API 集成测试通过；实际 HTTP 创建 201、元数据 200、停用后 401；浏览器编辑/停用/启用/撤销通过，见 [UI 记录](evidence/ui-interactions.txt) |
| 读取/写入边界 | 只读 HTTP 状态、配置、任务、日志读取 200；提交任务及读取 Key 管理列表 403；集成测试还验证具有副作用的 GET、授权交集和管理员隐式全权限路径 |
| 创建者变化 | 集成测试验证账户停用、重新启用、降级、删除、环境管理员令牌轮换及当前权限减少；旧 Key 不获得其他账号身份 |
| IPv4/IPv6/CIDR、内网、回环、映射地址 | `internal/core` 和 `internal/authn` 测试通过，含非法格式、去重、规范化及空地址拒绝 |
| 直接、可信代理、多层、伪造头 | 使用真实 HTTP Handler 的远端地址与请求头组合测试通过；直接来源忽略伪造头，可信链从右向左判定，畸形链拒绝。未搭建外部代理网络 |
| 白名单立即生效 | 同一运行进程中，编辑后下次实际 HTTP 请求立即从 200 变 403，改回后 200 |
| 多 Key/账号/共享实例归属 | 数据库集成测试通过：两个 Key、管理员自有/分享节点、用户私有隐藏节点、配置、版本、文件、任务、共享实例日志、筛选、详情及不同 Key 权限 |
| 存储/展示/脱敏 | 摘要长 32 字节，前缀 16 字符；同一环境管理员的 4 把测试 Key 只关联 1 个创建者与 1 个原工作区，见 [实际 SQL 检查](evidence/storage.json)。集成测试核对列表/审计不含完整 Key，配置 JSON/YAML、文件、任务快照、错误、日志和 PEM 凭据被脱敏 |
| 面板重启后持久化 | 实际停止并重启控制面，原 Key 的 14 项授权、白名单和工作区仍有效，见 [重启结果](evidence/restart.json) |
| 按 AI 文档完成流程 | 实际执行 `diagnose.sh`，11 次 HTTP 请求依次读取授权、版本、节点、部署、实例状态、配置、任务列表、失败任务详情、日志源、日志、审计；退出 0；重启后再次成功。验证脚本断言每个必要路径都实际调用，并检查 fixture 凭据未出现在输出 |

实际 HTTP 的 21 次请求及状态见 [http-verification.json](evidence/http-verification.json)。文档示例输出见 [首次执行](evidence/diagnostic.jsonl) 和 [重启后执行](evidence/diagnostic-after-restart.jsonl)。其中节点、任务、日志是模拟上报的数据，不应解读为真实内核故障复现。

## 测试命令与结果

| 命令 | 结果 |
| --- | --- |
| `QCH_TEST_DATABASE_URL=… go test -buildvcs=false ./internal/core ./internal/authn ./internal/api ./internal/store` | core/authn/API 全部通过；store 的既有性能测试 `TestTrafficUsageUpdatesStayHeapOnly` 失败：200 次上报后表增长 524288 字节。原始结果摘录见 [记录](evidence/db-regressions-summary.log) |
| 同一数据库单独重跑 `TestTrafficUsageUpdatesStayHeapOnly` | 通过（1.336 秒）；不能据此将首次完整 store 回归记为通过，见 [记录](evidence/traffic-isolated.log) |
| `go test -buildvcs=false ./internal/api -run '^Test(APIKey\|.*Session\|.*Identity\|.*Token\|.*Login\|.*Role\|.*Permission)' -count=1`，使用专用数据库 | 通过，49.594 秒；见 [记录](evidence/auth-regressions.log) |
| 最终 `go test -buildvcs=false ./internal/api -run '^TestAPIKey' -count=1`，使用专用数据库 | 通过，3.314 秒；见 [记录](evidence/api-key-final.log) |
| `node frontend/module_smoke.mjs` | 通过，见 [记录](evidence/frontend-module.log) |
| `go test -buildvcs=false ./frontend -run 'Settings\|Shell\|Style'` | 通过，见 [记录](evidence/frontend-contract.log) |
| `go test -buildvcs=false ./frontend` | 未全部通过：既有 `config-scope` 浏览器场景报告配置页出现运行状态/生命周期控件。完整运行 380.323 秒，见 [记录](evidence/frontend-go.log) |
| 基准前端 `QCH_BROWSER_SMOKE_MODE=config-scope node agents_browser_smoke.mjs` | 同一断言失败，证明该问题基准已存在，见 [基准复现](evidence/frontend-baseline-config-scope.log) |
| `go vet` 修改的四个后端包、`node scripts/generate-styles.mjs --check`、`git diff --check` | 通过 |
| `go build -buildvcs=false ./cmd/control-plane` | 通过，构建产物用于实际 HTTP 和重启验证 |

此环境 Go 的 VCS stamping 无法取到仓库状态，所以本地 Go 命令使用 `-buildvcs=false`。未添加、修改 GitHub CI。

## 真实界面对比

截图没有完整 Key。创建后的一次性凭据弹窗已实际操作，为避免将测试凭据写入截图，不保留其内容截图。

| 设备/主题 | 修改前 | 修改后 | 创建弹窗 |
| --- | --- | --- | --- |
| 桌面浅色 | [前](evidence/before-desktop-light.png) | [后](evidence/after-desktop-light.png) | [创建](evidence/create-desktop-light.png) |
| 桌面深色 | [前](evidence/before-desktop-dark.png) | [后](evidence/after-desktop-dark.png) | [创建](evidence/create-desktop-dark.png) |
| 手机浅色 | [前](evidence/before-mobile-light.png) | [后](evidence/after-mobile-light.png) | [创建](evidence/create-mobile-light.png) |
| 手机深色 | [前](evidence/before-mobile-dark.png) | [后](evidence/after-mobile-dark.png) | [创建](evidence/create-mobile-dark.png) |
| 平板浅色 | [前](evidence/before-tablet-light.png) | [后](evidence/after-tablet-light.png) | [创建](evidence/create-tablet-light.png) |
| 平板深色 | [前](evidence/before-tablet-dark.png) | [后](evidence/after-tablet-dark.png) | [创建](evidence/create-tablet-dark.png) |

布局检查记录：[桌面](evidence/desktop-layout-results.txt)、[触屏](evidence/touch-layout-results.txt)、[桌面弹窗](evidence/desktop-dialog-results.txt)、[触屏弹窗](evidence/touch-dialog-results.txt)。验证水平无溢出、弹窗标题和提交按钮可见。

## 测试环境清理

测试进程、浏览器及容器在验证结束时停止并释放端口。72 小时后的持久清理任务、绝对执行时间、时区和精确范围见 [登记](evidence/cleanup.json)。该任务只处理本次专用容器/卷和测试目录；截图、报告及脱敏日志保存在本目录中。
