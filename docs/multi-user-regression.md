# 多用户缺陷排查与对抗性回归

基线：`8ac5bbe1`，建分支时的最新 `main`。所有账号、节点、密钥和配置均为测试夹具；使用独立 PostgreSQL 17 容器、随机测试 schema、浏览器上下文和隔离 Agent 执行环境。未连接生产控制面或用户节点。

## 账号与资源验收矩阵

“允许”同时要求操作能力和资源作用域满足；一个共享邀请不能替代账号权限。

| 身份 / 状态 | 自有节点 / 配置 | 他人未分享节点 / 配置 | 已接受的共享节点 | 账号管理 / 主机操作 |
| --- | --- | --- | --- | --- |
| 环境管理员令牌（原文或摘要） | 管理员公共工作区；无数据库用户 ID | 可管理普通可见节点及配置 | 可管理分配，不能代替接收者接受邀请 | 可管理数据库管理员；隐藏节点按现有规则仅允许目录摘要和删除 |
| 数据库管理员 | 个人节点工作区，档案管理仍按既有管理员范围 | 与环境管理员相同；不能读取所有者隐藏节点内容 | 同上 | 保护当前账号删除 / 停用；保护最后一个有效管理员入口 |
| 普通用户 A、B，具有显式业务能力 | 仅本人节点、档案、版本、模板、客户端参数和任务 | 列表不可见，直接请求拒绝且无副作用 | 仅授权内核和端口；本人工作区和实例；共享累计流量配额 | 自有主机操作仍需对应能力；不能管理账号或共享主机生命周期 |
| 仅 `tasks.execute`，无 `tasks.read` | 可在授权资源创建任务；不能查看任务 | 拒绝 | 可执行授权操作；不因创建任务获得读取能力 | 不隐含 `agents.manage` 或任何配置读写能力 |
| 仅 `tasks.read` / 仅 `agents.manage` | 仅对应能力；不能创建普通执行任务 | 拒绝 | 不扩大能力范围 | 内核能力切换沿用 `agents.manage` 专用规则 |
| 分享 pending / rejected / withdrawn | 本人其他资源继续可用 | 仍拒绝 | 尚未接受、拒绝或撤销均不能读取或执行；重新邀请需新同意 | 邀请版本校验；保留已有累计用量和端口安全约束 |
| 停用账号 / 权限已撤销的旧会话 | 旧 Cookie 返回 401；已进入的写入事务拒绝 | 拒绝 | 待执行工作取消；旧运行租约不能因重新授权复活 | 新登录按当前账号状态和显式授权判断 |

浏览器夹具为 A/B 分别创建自有节点，并在同一管理员节点分配 21001/21002 两个端口。A/B 同时持有独立 Cookie、CSRF、档案、配置版本和共享部署任务。管理员使用单独的密码登录上下文。受限权限、停用 / 恢复、管理员降级 / 删除使用额外独立夹具，避免一个场景污染另一个场景。

## 已复现并修复

| 场景 | 预期行为 | 实际问题 | 根因 | 修复 | 验证结果 |
| --- | --- | --- | --- | --- | --- |
| 请求已通过登录与 CSRF，管理员随后降级、停用或永久删除 | 在途写入不再使用旧管理员身份 | 仍可创建任务、写档案 / 节点配置 / 模板、改节点参数和他人分享 | HTTP 验证与数据库提交分离；部分写入跳过管理员账号锁，部分只有事务外检查 | 将可信会话 `auth_revision` 传至 Store；事务内锁定操作者、验证版本；节点管理在同一事务锁节点并重新授权；分配复用账号管理事务 | `TestInFlightAccountMutationCannotKeepOldAuthority`，4 类状态变化 × 7 个写入口；检查响应及节点、配置、模板、分享和队列无副作用 |
| 普通用户保存配置期间撤销配置写权限 | 拒绝旧请求，保留已保存数据 | 自有节点旧请求仍保存成功 | 原账号锁仅检查存在 / 启用，未核对请求授权版本 | 数据库会话版本统一校验；普通用户与管理员共用 | `TestOrdinaryUserInFlightSaveLosesRevokedPermission`；真实密码 / Cookie / CSRF；无新工作区 |
| 停用后重新启用，再放行旧请求 | 旧会话和请求仍失效，新登录可正常操作 | 重新启用恢复了在途管理员请求能力 | 仅判断当前角色或 disabled 无法识别撤销后重新授予 | 比较认证时与锁定后的持久化版本 | 同一在途测试的 `revoke-regrant`；另检查新登录可成功创建任务 |
| 删除用户与档案 / 模板 / 设置 / Sub-Store 创建交错 | 已提交数据按 purge 转移 / 清理；删除后的写入拒绝 | 删除后仍插入五类孤立 owner_id 记录 | 为兼容令牌身份，owner_id 无用户外键；这些入口未锁账号 | 配置、模板、个人设置和 Sub-Store 在账号锁保护的事务中写入；相关编辑 / 删除同步采用账号锁 | `TestPurgedPrincipalCannotRecreatePersonalResources` 覆盖五类记录；`TestArchiveAndTemplateCreationSerializeWithPurge` 使用 PostgreSQL 行锁和 `pg_blocking_pids` 固定顺序 |
| 取消 / 失败的配置任务之后又保存新版本，再点普通重试 | 不能悄悄切换已确认的配置版本 | 重试读取最新草稿并下发，原任务版本失去意义 | 只有自动安装任务设置 expected version | 所有配置重试绑定原版本；冲突返回 409，无排队副作用；按钮 / 确认说明显示版本 | `TestRetryDoesNotDeployANewerConfiguration` 覆盖 validate / deploy；`TestRetryRequiresDurableConfigurationRevision` 检查原快照和重复重试幂等；桌面 / 移动端真实会话验证 |
| 升级后的历史任务缺少配置版本 | 说明无法确认内容，要求核对后新提交 | version=0 会继续使用当前配置；版本字段省略时页面出现 vundefined | 0 同时是“未指定版本”兼容值；前端未区分省略的 JSON 字段 | 普通重试对缺失版本返回明确 409；页面显示版本未知及处理说明，保留有权限的编辑重试，移除无效的普通重试入口 | `TestRetryRequiresDurableConfigurationRevision` 使用真实 PostgreSQL 历史形状；`route_modules_smoke.mjs` 覆盖省略 / null / 0、有版本和非配置任务及只读账号；已有 v58 → v73 升级独立运行 |
| 管理员编辑他人档案后重试，同时删除配置所有者 | 先锁账号再修改资源；等待结束后提交准确快照 | 编辑重试先锁配置、再等待所有者账号；删除用户先锁账号、再等待配置，形成死锁环 | 编辑重试直到创建任务时才锁配置所有者 | 修改配置前按 ID 顺序锁操作者及配置所有者，沿用任务创建的锁顺序 | `TestEditedRetryLocksConfigurationOwnerBeforeResources` 以 PostgreSQL 锁屏障稳定复现；修复后三轮 `-race` 通过，并检查版本与领取快照 |

原实现的复现记录见 [baseline-failures.txt](multi-user-regression/baseline-failures.txt)。只保存失败用例名，不保存响应中的随机配置密钥。新增测试放到未修改的基线工作树运行，35 个子场景及普通用户权限撤销用例失败；历史无版本任务和编辑重试锁顺序用例另行在基线复现失败，修复后的对应测试通过。

合并前审查另外复现了历史任务卡片的 vundefined 问题：新增渲染用例在 `536cd43d` 失败，补充修复后前端模块及边界检查通过；该修复仅改变缺失版本的任务展示和操作入口。

### 业务规则冲突与修正依据

`internal/store/task_cancellation.go`、原 `TestTaskCancelRetryAndFiltersWithPostgreSQL` / `TestTaskAPIWithPostgreSQL` 以及任务页“使用当前配置重试”共同实现了“普通重试使用最新配置”，而自动安装重试固定旧版本。这与本次要求的准确快照、避免错误版本部署冲突。统一为：普通重试必须仍是原版本；编辑后重试明确生成新版本并原子提交任务；用户也可从配置页选择新版本部署。更新相关旧测试，同时保留快照脱敏和任务过滤断言。

锁顺序为账号（多个账号按 ID 排序）→ Agent → 分享 / 配置 / 任务。管理员代其他账号提交配置或编辑后重试时，一次按顺序锁操作者与配置所有者，避免修改资源后才获取所有者账号锁。显式 `WithSystemScope` 运维仍按内部信任边界运行；不能把发起请求的旧会话身份误用为系统身份的拒绝依据。

## 已验证符合预期的场景

以下项有实际执行的回归覆盖；不代表全部真实发行内核、平台和网络故障组合已验收。

| 场景 | 预期 / 验证结果 | 主要证据 |
| --- | --- | --- |
| 环境 token 原文 / 摘要、数据库同名 admin、token 角色冲突、最后管理员 | 身份来源明确；只统计真实可用入口；并发删除 / 停用 / 降级保留至少一个入口 | `admin_lifecycle*_test.go`、真实账号生命周期 API 测试 |
| 账号更新、密码首尾空白、中文密码、旧面板升级、迁移失败 | 数据与原始密码兼容；会话重登；数据库失败与凭据错误区分；失败迁移可回退旧二进制 | `make panel-auth-upgrade-test`：v58 → v73，三轮升级 / 重启；密码登录集成测试 |
| A/B 列表、详情、版本、模板、导出、任务和实时来源的作用域 | SQL 作用域、直接访问及权限反例一致；共享实例不授予宿主管理权 | `config_scope*`、`agent_access*`、`shared_engines*`、`client_profile*`、`core_logs*`、`client_connections*`、`task_sources*` 测试 |
| 邀请接受 / 拒绝 / 撤回 / 重新邀请、授权版本、端口 / 配额变化 | 同意和版本受检；累计用量保留；不同用户端口不重复；超额不继续派发 | `agent_invitations*`、`shared_ports*`、`traffic_quota_claim*`、`traffic_accuracy*` 测试 |
| 排队后撤销权限、分享 / 引擎授权改变、Agent 重连 / 重领 / 超时 | 不派发失效授权；旧租约不能回报成功；未知执行状态按现有规则处理 | `shared_core_prepare*`、`task_lifecycle*`、`task_requeue*`、`users_purge*`、`user_permission_invariant*` 测试 |
| 同节点同时保存、恢复、部署、端口竞争，多个管理员改状态 | 乐观版本 / 端口约束生效；提交顺序明确；无覆盖和错误租约复活 | Store 并发回归；关键并发场景 `-race -count=3` |
| 编辑后重试任务插入失败，部署失败与服务恢复 | 配置修订与任务原子提交；回滚恢复目标，其他实例 / 基础服务不停止 | `TestEditedRetryIsAtomicAndVersioned`、Agent shared / deployment recovery / TLS frontend 测试 |
| 普通用户首次准备共享内核 | 四内核、systemd / OpenRC 命令路径、独立配置 / 服务 / 配额；失败后可重试 | 现有首次安装真实会话浏览器矩阵；`make upgrade-sandbox-test` 在只读、断网、最小 capability 容器验证首次使用和回滚 |
| 系统监控和兼容令牌 | A 会话失效不影响 B 或内部监控；无数据库账号的历史令牌仍可写本人工作区 | 新增 `TestPrincipalRevocationPreservesOtherUsersAndMaintenance` |
| 多浏览器上下文与切换账号 | pending 不可见、accept 后生效；跨用户操作拒绝；重复任务复用；A 撤销后 B 任务可领取；A 停用返回 401；同上下文改登 B 无 A 档案 | 新增 `TestMultiUserBrowserSessions`：1440×1000、390×844；Go 检查最终队列并领取 / 完成 B 的独立实例任务 |

## 页面证据

同一轮数据库夹具、普通用户 Alice、取消的配置 v1 部署任务、已保存的 v2、相同窗口尺寸。修改前使用基线的真实前端资源，修改后使用本分支资源，后端和持久化数据相同。基线只打开确认框，没有向修复后 API 提交旧操作。

| 窗口 | 修改前 | 修改后 |
| --- | --- | --- |
| 桌面 1440×1000 | [原确认框](multi-user-regression/desktop-before.png) | [版本与冲突说明](multi-user-regression/desktop-after.png) |
| 移动 390×844 | [原确认框](multi-user-regression/mobile-before.png) | [版本与冲突说明](multi-user-regression/mobile-after.png) |

多用户测试记录：[桌面](multi-user-regression/desktop-record.txt)、[移动](multi-user-regression/mobile-record.txt)。浏览器验证冲突后的反馈、无横向溢出、新版本显式提交与后续账号切换。整体 CSS 和组件未改变。

## 复跑

本地结果：`make check`、新增桌面 / 移动浏览器矩阵、`make build`、历史面板账号升级、Agent 只读断网沙箱和所列 `-race -count=3` 并发回归均通过。最终增量用例（历史无版本任务、系统监控与令牌兼容、更新后的任务 API / Store 规则）另行复跑通过。编辑重试锁顺序补丁后，完整 Store 套件和三轮 `-race` 回归通过。首轮可选 Alpine 浏览器发现刷新与 CDP 求值竞态；测试改为等待 `Page.loadEventFired`，仅只读 DOM 轮询允许跨文档重试，写请求不自动重放；修改后桌面 / 移动流程连续三轮通过。

先为 `QCH_TEST_DATABASE_URL` 配置仅用于测试的 PostgreSQL 数据库；集成测试创建 / 删除随机 schema。数据库测试包按项目要求串行运行：独立 schema 仍共享 PostgreSQL 的 vacuum horizon，并行长事务会干扰 HOT 页面性能验收。

```sh
make check
make multi-user-browser-check
make panel-auth-upgrade-test
make upgrade-sandbox-test
go test -race -count=3 ./internal/store -run 'Test(ArchiveAndTemplateCreationSerializeWithPurge|RetryDoesNotDeployANewerConfiguration|AgentInvitationResponsesAndEditsSerialize|SharedCorePreparationSerializesUsersAndKeepsSnapshots|UnrestrictedSharedPortsConcurrentDeployment|ConcurrentAdministratorRemovalPreservesLogin)'
go test -race -count=3 ./internal/store -run '^Test(EditedRetryLocksConfigurationOwnerBeforeResources|EditedRetryIsAtomicAndVersioned)$'
```

新增浏览器检查已加入 `make browser-checks`、`make check` 和现有可选 Browser validation workflow。`QCH_MULTI_USER_ARTIFACT_DIR` 指定截图 / 记录目录；可选 `QCH_MULTI_USER_BASELINE_ROOT` 指向基线 `frontend` 目录，用相同 API 和数据采集对比。浏览器关闭后清理 profile；不把登录 Cookie、CSRF 或生成配置写入报告。

## 兼容、权限影响与回滚

- 无 schema 修改或数据迁移；沿用已存在的 `auth_revision`。配置加密、用户 ID、分享账本、配置版本和任务历史格式不变。
- 会话版本变化后的在途写入会返回 403（或账号不存在时 404）；下一次会话验证返回 401，需要重新登录。授权未变的其他用户和环境令牌继续可用。
- 普通配置重试不再隐式选新草稿。旧任务缺失版本时要求明确新建任务；编辑后重试路径继续可用。
- 回滚可部署前一控制面和前端版本，无反向迁移。已提交的合法配置 / 任务无需清理；回滚会恢复本次修复前的竞态与普通重试行为。Agent 协议和二进制不变。

## 尚未完成的验证及原因

- 没有对所有真实核心发行版本、真实 systemd / Alpine OpenRC 主机、真实流量 / nftables 内核路径和公网 ACME 故障组合做完整新部署验收。本次使用项目的执行器、服务 / 网络夹具与隔离容器；相关外网 live 测试需要专门环境变量和外部依赖。
- 新增多用户浏览器流程的 Agent 领取 / 结果是确定性测试驱动，不宣称浏览器驱动了真实远端代理进程。实际执行隔离和失败回滚由单独 Agent / sandbox 回归验证。
- 未进行长时间高负载、真实网络分区、PostgreSQL 进程崩溃 / 断电或多个控制面进程部署的端到端混沌测试；已覆盖数据库锁屏障、并发事务、插入失败回滚、会话跨 Server 验证、Agent 断线和旧租约恢复。

托管 CI 的 Debian / Alpine / 核心集成与可选完整浏览器矩阵结果见本 PR 的 checks；最终交付以提交版本对应的检查结果为准。
