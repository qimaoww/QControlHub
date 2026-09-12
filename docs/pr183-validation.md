# PR #183 本地验证

日期：2026-09-12。本记录只列本次重新执行的检查，不沿用旧验证结论。

## 本地运行环境

独立 Docker 网络内运行 PostgreSQL 17、控制面、Web、两个不同账号自行注册的 Agent、两个官方 Sub-Store 2.39.6 实例，以及真实代理客户端和上传/下载服务。Agent 使用验证证书的 WSS、OpenRC、nftables 和 Mihomo v1.19.30。

Web 仅在本地 `https://localhost:18483` 提供测试入口。Chromium 检查 1440 × 1080 桌面及 390 × 844 手机视口；未使用生产节点或生产凭据。

## 实际服务验证

- Alice、Bob 使用相同节点名称独立注册 Agent，分别部署自己的 Mihomo 服务；各自的安装凭据、配置、面板设置和 Sub-Store 后端不互相继承。普通账号也通过浏览器“添加节点”表单创建了安装凭据。
- 未授权的节点、配置、任务快照及同步组 ID 请求被拒绝。自有节点可显式读取自己的托管配置；共享给另一用户并部署后，所有者也不能通过快照接口读取借用者的完整配置。
- 所有者明确分享端口后，借用者可同时看到自己的 Agent 和共享 Agent，并部署个人配置；不能再次分享、读取安装命令、修改宿主服务、BBR、Komari 或主机日志。
- 跨用户覆盖运行内核被拒绝。所有者停止服务并完成结算后才能交接；复用端口没有继承上一账号的私有客户端显示名称。
- 真实 SS2022 代理完成 64 KiB 上传及四次 256 KiB 下载。额度从累计 3,516,461 字节起增加 1 MiB，最终在 4,646,511 / 4,565,037 字节时阻断新连接；追加总额度后恢复。Agent 重启前后 Mihomo PID 不变，累计用量不回退。
- 停止控制面后又完成 11 次下载，Agent 继续本地计量并阻断；离线重启 Agent 没有恢复已耗尽额度，也未重启 Mihomo。控制面恢复后入账累计 8,200,828 字节，再追加 8 MiB 后连接恢复。
- 浏览器将总额度保存为 1 GiB，保留累计 8,466,348 字节。随后在额度仍充足时撤销共享，实际代理被阻断、直接节点请求被拒绝，Bob 仍可使用自有 Agent；撤销及端口释放不清零累计用量。
- 两个独立 Sub-Store 后端使用同名 `same-private-group` 并发同步 Mihomo 和 URL 格式。实际远端内容只含各自节点，远端 integration ID 与数据库归属一致。Bob 切换到 Alice 的同名组所在后端返回 `409`，其原后端身份及加密凭据保持不变。
- 另执行 15 个 Snell / ShadowTLS / Sudoku 预设的真实 Mihomo 传输用例，全部通过。

## 浏览器与界面

- 实际访问普通账号的同步、访问限制、TCP 调优、流量、日志、任务、额度、设置、手动配置和内核预设页面，未发现其他账号的私有配置或脚本运行错误。
- 借用节点不出现在主机日志选择器中。同一浏览器保存 Bob 的日志关键词、退出再登录 Alice，Alice 未继承该筛选，Bob 的记录仍单独保存。
- 通过真实页面把 Alice 的同名同步组切换为 Mihomo、保存并同步成功；远端回读确认 Alice 使用 Mihomo，Bob 仍使用 URL，互不影响。
- 共享弹窗完成真实读取、修改、保存；移除误添加的空白用户行可正常继续编辑。桌面浅色、深色和手机布局均目视检查，沿用现有主题变量、输入框和按钮，无重复说明段落。
- 手机弹窗宽 358px，标题区宽 278px，字段单列展示；弹窗、标题和保存按钮均未超出 390px 视口。
- 自动浏览器回归覆盖草稿保留、旧修订冲突、取消/确认刷新、重复提交、保存中重开、退出后旧响应、账号缓存隔离及横向溢出。

本地截图和流量结果保存在 `output/playwright/pr183-live/`，包括 `sharing-compact-light.png`、`sharing-compact-dark.png`、`sharing-compact-mobile.png`、`traffic-results.json`、`offline-results.json`、`revocation-results.json`、`substore-results.json`。该目录不提交到 Git。

## 回归检查

使用独立 PostgreSQL 测试库设置 `QCH_TEST_DATABASE_URL`，以下检查通过：

```sh
GOFLAGS='-buildvcs=false -p=1' make check
make build
QCH_SCHEMA_BASE_REF=origin/main node .github/scripts/check-schema-version.mjs
GOFLAGS='-buildvcs=false -p=1' go test -race ./internal/store ./internal/api ./internal/agent \
  -run 'TestAccountsOwn|TestOwnedAgent|TestAccountRuntime|TestAgentIsolation|TestSharedTraffic|TestMigrateV5[24]|TestTaskFailureWebhook|TestSubStore.*(Independent|Canonical|Concurrent)|TestSession|TestLegacySessions' \
  -count=1
GOFLAGS='-buildvcs=false -p=1' go test -race ./internal/api \
  -run 'TestAccountChangesInvalidateSessionsAcrossServers|TestAsyncAuditPreservesAccountScope|TestSharedProfilePreferencesDoNotTransferWithPorts' \
  -count=1
```

`make check` 包含前后端测试、全部浏览器模式、vet、格式、文档、迁移策略和安装/升级脚本检查。schema 51 → 55 策略检查通过；迁移回归保留实际部署修订、私有 Sub-Store 后端及客户端显示设置。新增测试还覆盖账号独立保留策略、跨控制面会话撤销、异步审计归属、任务失败 Webhook 发送给任务所有者而不是共享宿主所有者。

最后一次界面调整后另重跑前端模块检查、全部前端测试和相关浏览器模式，并重新构建、运行 `qcontrol-web` 镜像。

真实传输测试需预先准备对应内核及测试目录：

```sh
QCH_LIVE_CORE_VALIDATION_TEST=1 \
QCH_LIVE_CORE_ROOT=/absolute/path/to/runtime \
GOFLAGS=-buildvcs=false \
go test ./internal/agent \
  -run '^TestMihomoSnellAndSudokuPresetsTransferRealTraffic$' -count=1 -v
```

## 边界

普通账号资源始终独立，不能关闭隔离。每 Agent 每种内核仍只有一个运行配置；这里的共享是面板权限和固定监听端口计费，不是独立进程或 OS 沙箱。不可信租户需要独立虚拟机/容器及 Agent。约一秒采样可能少量超额。

本次真实流量、停机交接和故障恢复验证针对 Linux / OpenRC / Mihomo；不代表其他操作系统、所有内核和全部外部集成都已做端到端实测。

操作及升级要求见 [用户与 Agent 分配](agent-sharing.md)。
