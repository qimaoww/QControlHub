# PR #183 本地验证

日期：2026-09-12。覆盖用户配置隔离、Agent 分配与累计额度、Sub-Store 格式及新增页面。

## 真实服务

在本地 Docker 内运行 PostgreSQL 17、控制面、认证 WSS Agent、Alpine 3.22 / OpenRC / nftables、Mihomo v1.19.30、官方 Sub-Store 2.39.5；使用真实 Chromium 检查桌面和 390px 手机视口。

已通过：

- Alice / Bob 独立工作区、多个配置档案及同步组；他人配置 ID、版本和任务请求被拒绝。
- 活动内核不可跨隔离用户覆盖；管理员停机、最终结算、释放端口后可交接，原用户累计用量保留。
- 两个真实代理端口上传 1 MiB、下载 2 MiB，监听端口收发累计 3,162,878 字节；共用额度耗尽后两个端口均阻断。
- 追加额度、撤销与恢复、关闭隔离、Agent 重启均不清零；控制面离线期间继续计量和限流。
- 检查点丢失、合法但过期的检查点、首次激活记录写盘失败均停止共享内核；认证恢复后重新部署可用，累计用量继续增长。
- 实际 Sub-Store URL / Mihomo 同步、格式切换、改名及 ClashMeta 转换；双用户同时关联同一远端组只有一个成功，远端与数据库归属一致。
- 15 个 Snell / ShadowTLS / Sudoku 预设的真实 Mihomo 传输用例。
- 新增页面桌面深浅色、手机布局及账号弹窗；真实页面创建账号默认隔离，编辑与停用后数据库回读一致；配置档案、额度和同步格式页面可操作。

## 回归检查

使用独立 PostgreSQL 测试库设置 `QCH_TEST_DATABASE_URL`，完成：

```sh
GOFLAGS='-buildvcs=false -p=1' make check
make build
GOFLAGS='-buildvcs=false -p=1' go test -race ./internal/store ./internal/agent ./internal/api \
  -run 'TestSharedTraffic|TestAgentIsolation|TestMigrateV52|TestAuthenticatedUsersCannotSelect|TestSubStoreMutationsShareDatabaseLock|TestSubStoreConcurrentRelink|TestEngineCapabilityServiceLifecycle|TestTrafficUsageUpdatesStayHeapOnly' \
  -count=1
```

`make check` 包含前后端测试、浏览器冒烟、vet、格式、文档、迁移策略及安装/升级脚本检查。另验证 schema 51 → 53 升级策略。

真实传输测试需预先准备对应内核及测试目录：

```sh
QCH_LIVE_CORE_VALIDATION_TEST=1 \
QCH_LIVE_CORE_ROOT=/absolute/path/to/runtime \
GOFLAGS=-buildvcs=false \
go test ./internal/agent \
  -run '^TestMihomoSnellAndSudokuPresetsTransferRealTraffic$' -count=1 -v
```

## 边界

每 Agent 每种内核仍只有一个运行配置；隔离是面板权限和固定监听端口计费，不是 OS 沙箱。约一秒采样可能少量超额。本次真实流量与故障恢复验证针对 Linux / OpenRC / Mihomo，不代表其他操作系统和内核均已做端到端实测。

操作及升级要求见 [用户与 Agent 分配](agent-sharing.md)。
