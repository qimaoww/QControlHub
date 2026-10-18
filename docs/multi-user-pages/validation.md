# 测试记录

最终本地验证全部通过，命令退出码及日志索引见 [结构化结果](validation-results.json)。下表只记录已完成的命令；首轮失败及未实测场景另行保留。

## 已完成的专项验证

- 真实账号/数据库路由审计：228 次，桌面/平板/手机；零横向溢出、脚本异常、具备权限账号的页面错误。
- 多账号真实浏览器：桌面、手机通过。新增 tasks-only、traffic-only、自有证书 HTTP 节点、证书延迟响应、无权限配置页、旧 cookie 页面绑定；原有 A/B 工作区、档案、任务、版本冲突、停用与撤销回归通过。
- 四内核真实 systemd 双实例日志生命周期：Mihomo、Xray、sing-box、ss-rust 全通过，81.39 秒。使用项目固定上游下载及 SHA-256 校验的二进制，测试运行在本次独立、无外部网络的 systemd 容器中。
- 前端模块回归、API 中文错误覆盖的针对性重跑通过。

## 首轮失败与处理

1. `make non-browser-checks test`：最初格式检查发现未提交的本地取证夹具未 gofmt；格式化后重跑。Go 全包首轮仅 `TestAPIErrorLiteralCoverage` 失败，新错误 `session changed` 缺少中文映射；已补齐“登录账号已切换，请重新登录。”，单项通过，完整全包再次运行并通过。
2. `make browser-checks`：原生矩阵的 `config-scope` 断言禁止任何 `.live-engine-status`，但 main 已有用该类显示共享配置状态的徽标。改为验证不出现实例生命周期控件，且缺少实例元数据时徽标准确显示配置待核实；该项单独重跑通过，完整矩阵再次运行并通过。
3. 后续浏览器检查发现只读配置夹具把读取权限也全部设为 false，与其测试只读编辑器的意图不符；改为显式保留读取权限。补齐未知态过程中，模块回归也捕获了紧凑卡片变量作用域错误；修复后模块检查以及 config-inbounds（桌面/手机）、bbr-mobile、config-scope 均通过，再运行完整矩阵并通过。
4. 一次本地页面审计与取证服务器重启重叠，审计被连接中断；恢复服务器后重新执行全部 228 次通过。未把该中断记为产品通过或产品缺陷。

## 必需检查

| 最终命令/专项 | 结果 | 证据 |
| --- | --- | --- |
| `make non-browser-checks` | 退出 0；格式、模块、模块边界、runner、浏览器诊断、PR/schema 策略、部署/样式生成、安装/重部署/quick-start、vet 全通过 | [摘要](evidence/logs/non-browser-checks.log) |
| `make test` | 退出 0；全 Go 包顺序执行，API 246.852s、Store 172.022s；无未修复失败 | [摘要](evidence/logs/go-test.log) |
| `make browser-checks` | 退出 0；6 个 Dock 视口、85 项原生浏览器矩阵、首次安装 4 组合、多用户/证书/日志桌面与手机全通过 | [摘要](evidence/logs/browser-checks.log) |
| 四内核真实 systemd 日志生命周期 | 退出 0；4/4 内核通过，81.39s | [摘要](evidence/logs/core-log-live.log) |
| 最后证书轮询序号保护后的模块检查 | 退出 0；证书真实浏览器阶段也在该修改后执行 | [摘要](evidence/logs/final-module-check.log) |
| 三视口页面审计及异步取证 | 228/228 页面加载检查通过；延迟、账号切换及空闲分享停用通过 | [逐页结果](evidence/route-audit.json)、[异步结果](evidence/async-evidence.json) |

Go/Store 与使用数据库的浏览器阶段顺序执行，避免 PostgreSQL 集群级 vacuum horizon 干扰。浏览器日志中的预期取消请求不视为功能失败。

GitHub CI 由 PR 触发；Debian、Alpine、升级沙箱、真实服务权限、镜像及 PR Policy 的远端结果以该提交的 PR Checks 为准。本地没有运行的远端平台检查不计为本地通过。

## 回归对应

| 修复范围 | 回归 |
| --- | --- |
| 当前角色、显式权限、节点归属、共享实例操作 | `permissions_smoke.mjs`，原生 readonly/shared-node/bbr 浏览器夹具已改用当前 user＋显式权限 |
| 跨账号 cookie 替换及延迟返回 | `session_api_smoke.mjs`、`TestBrowserReadsStayBoundToTheirSessionAfterCookieReplacement`、`multi_user_browser_session.mjs` |
| 空闲授权变更、计时器、旧响应、使用量变化不打断 | `session_watch_smoke.mjs`，真实空闲配置页停用分享录屏 |
| tasks-only、traffic-only、证书 HTTP 授权、延迟弹窗、无权限空态 | `TestMultiUserBrowserSessions` 桌面/手机；只有读任务/读流量权限且有自有节点 |
| 流量资源范围和接口兼容 | `TestTrafficPageMetadataUsesTrafficPermissionAndResourceScope`；跨用户查询、管理标志、字段最小化、本人可写、共享宿主拒绝、默认数组不变 |
| 未安装、未知、离线、保存/部署漂移、启停、失败、执行中 | `shared_instance_smoke.mjs`；真实状态截图；既有 shared instance lifecycle Store 回归 |
| 配置、同步、证书、连接与日志归属 | 既有 config scope、Sub-Store、certificate ordinary-user/HTTP/Caddy、client connection、core log isolation 回归及浏览器专项 |

## 未验证项目

详见 [报告中的验证边界](README.md#验证边界与回滚)。公共 CA/DNS、真实远端 Sub-Store、多台物理设备、本次 UI 夹具到真实 Agent/代理客户端完整闭环均未实测；不以夹具任务结果代替真实执行结果。GitHub 的 Alpine/OpenRC、实际服务权限、镜像、TLS 及升级任务仅在对应 CI 真正完成后计为远端通过。

## 合并前审查补充

[审查记录](review.md) 包含两项已修复发现。新增证书撤权用例在修复前真实浏览器断言失败（旧详情在 404 后仍保留）；修复后多用户/证书完整门禁退出 0（33.792s），桌面及手机均覆盖失权详情清理、列表刷新和 Escape 关闭后的延迟响应。

补充检查：[多用户/证书门禁](evidence/logs/review-multiuser.log)、[non-browser 全检查](evidence/logs/review-non-browser.log)、[最终模块回归](evidence/logs/review-module.log)。原完整 Go/85 项矩阵/四内核结果未被这次针对性重跑替代；生产 Go 未修改。新增录屏及原图见 [索引](evidence/index.md#审查补充证书授权撤销)。

## 远端 CI 断言补齐

`39152073275` 的 Debian Go 前端包失败：`TestAgentStructureRefreshDoesNotPrecommitComparisonMarkers` 仍查找旧的 installed 二态标志字符串。本地完整 Go 命令的前端包在后续未知态代码修改之前已经执行，未再次执行该包，导致这条契约断言漏更新；此前“当轮退出 0”不代表后续代码仍满足该断言。

已同步三态标志，并保留完整卡片不预提交结构标志、紧凑卡片才更新标志的既定约束；生产代码未因此修改。修复后完整 `./frontend` Go 包（仅排除已另跑的原生浏览器矩阵）以及 `make fmt-check` 退出 0，见 [日志](evidence/logs/ci-contract-followup.log)。最新提交重新触发全部远端 CI，合并前以新提交结果为准。

## 批量入口回归补充

真实 A/B 账号各自只有一个自有节点和一个共享节点。新增入口范围断言在修复前失败；改用可管理节点数量后，完整多用户/证书门禁退出 0（32.833s），同时保留证书撤权、Escape 延迟响应、切换账号、工作区/任务隔离回归。最终 non-browser 检查及完整前端 Go 包的结果见补充日志；最新提交再次运行全部远端 CI。

最终补充日志：[多用户/证书门禁](evidence/logs/batch-multiuser.log)、[non-browser 全检查](evidence/logs/batch-non-browser.log)、[完整前端 Go 包](evidence/logs/batch-frontend-go.log)，均退出 0。
