# 合并前审查

- 审查基准：重新拉取的 `main`，`26a63ff1a975c93f824c13d75961504fe3eb5fc5`；没有新增上游差异。
- 生产提交：`08a836c0`，47 个前端文件。证据与生产修改分别提交，便于单独查看代码。
- 范围：此前明确追加的近期页面、全站下拉框、添加 / 修改入站弹窗及其直接依赖；没有后端、接口、权限或部署实现变更。
- 结论：未发现尚未解决的阻断问题。检查了菜单事件只触发一次、原生表单值与校验、禁用项、键盘 / 焦点恢复、动态刷新清理、模态顶层、59 个预设共用字段、可选高级字段错误定位、证书草稿恢复与动态字段，以及已有主题与样式复用。

## 重新执行的验证

- [8 个浏览器模式](evidence/merge-review/browser-regressions.txt)：预设、配置入站、下拉框、原用户分配页，各含桌面 / 手机，全部通过，28.542 秒；各工作进程均完成清理。
- [模块与样式生成器](evidence/merge-review/modules-styles.txt)：通过。
- [前端 Go 合约](evidence/merge-review/frontend-contracts.txt)：通过。
- `git diff --check`：通过。
- 前几轮真实截图、59 预设 / 6 种设备主题参数对照，以及 PostgreSQL / 密码 / CSRF 集成记录继续保留：[总报告](README.md)、[下拉框](dropdowns.md)、[入站弹窗](inbounds.md)。本轮没有重复无关的后端与实际节点测试。

## 合并门槛与限制

合并按仓库要求等待 PR 元数据、Debian / systemd、Alpine / OpenRC 和三个镜像构建状态全部通过，并使用 squash。CI 结果以 PR 的 Checks 页面为准。

物理设备、Safari/WebKit、Firefox、屏幕阅读器和真实节点部署未验证。缺少 popover / CSS 锚定支持时使用原生选择框，系统菜单主题仍由浏览器决定。共享选择框影响全站交互；已有定向及原页面回归覆盖上述路径。

## 测试环境

2026-10-18 17:38:28 Asia/Hong_Kong（UTC+08）完成本轮复核，测试工作进程和内部服务器均已结束，原手动环境未重新启动。[停止记录](evidence/merge-review/shutdown.json)。

持久任务 `qch-ui-review-7d05-20261018-cleanup.timer` 已重新登记为 **2026-10-21 17:38:28 Asia/Hong_Kong（UTC+08）**，测试结束后 72 小时。清理范围及保护检查不变，证据保留。[登记及执行时间](evidence/cleanup-registration.txt)。
