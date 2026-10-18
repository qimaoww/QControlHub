# 全站字体统一

草稿 PR：[ #300](https://github.com/qimaoww/QControlHub/pull/300)。基准提交：`4865ad98b5967ce13b215803080cef076579868b`。实现与证据见该 PR 后续提交。

保留现有主题配色、组件和业务交互；变更范围为字体与字体直接影响的换行、行号、标签和按钮排版。

## 统一规范

所有字号继续使用已有 `--ui-font-scale`，设置仍为 90% / 100% / 110%。浅色、深色共用字体层级，文字颜色沿用原主题变量。

| 角色 | 正常字号 | 字重 | 行高 |
| --- | --- | --- | --- |
| 页面标题 | 24px | 600 | 1.35 |
| 区域、弹窗标题 | 18px | 600 | 1.35 |
| 卡片标题 | 16px | 600 | 1.35 |
| 正文、导航、表格单元格 | 14px | 400 / 500 | 1.6 / 1.4 |
| 字段标签、紧凑按钮、次要正文、表头 | 13px | 400 / 500 / 600 | 1.4–1.6 |
| 辅助文字、时间、状态标签 | 12px | 400 / 500 / 600 | 1.5 |
| 统计主值 | 28px | 600 | 1.2 |
| 独立展示标题 | 32px | 600 | 1.35 |
| 日志、配置编辑器 | 13px | 400 | 1.65 |

- 字体族：保留 Inter 偏好，集中加入系统 UI、Apple/Windows 中文字体、Noto CJK 和 Linux 文泉驿回退。中文、英文与标点不再使用不同页面的负字距。
- 字重归并为 400 / 500 / 600 / 700，消除 550 / 650 / 750 / 800 / 820 / 850 等页面差异。
- 字距统一为 0；数字和时间采用 `lining-nums tabular-nums`，单位保持同行基线。
- 等宽族统一为系统等宽、SFMono、Cascadia Code、Consolas、Liberation Mono 与中文回退；关闭代码连字。
- 手机、平板竖屏的普通可编辑控件字号最低 16px，110% 时 17.6px，保持 iOS 聚焦时的布局稳定。代码使用专用字号并允许内部横向滚动。
- 未引入外部字体资源或字体下载。Chromium 实测中文回退为 WenQuanYi Zen Hei，拉丁为 DejaVu Sans，行号为 Liberation Mono；等长 `iiiiiiii`、`WWWWWWWW`、`12345678` 测量宽度一致。见 [字体与资源检查](evidence/fonts.json)。未安装字体走同一回退栈，无字体异步替换。

## 公共定义与接入

- `frontend/styles/450-typography.css` 是公共字体定义入口：`--font-body` / `--font-mono`、`--text-*`、`--weight-*`、`--leading-*`、`--tracking-normal`，以及语义标题、按钮、表单、数字和等宽区域规则。
- 原 `450-typography.css` 中的旧工作区布局移动到 `445-workspace-polish.css`，保留它原来的层叠位置。字体模块在 `manifest.json` 中统一最后加载；由原有生成器产出 `frontend/app.css`。
- 原有页面模块改用上述公共变量；相同标题角色在公共模块集中定义，清除媒体查询和后续页面里的标题字号覆盖。
- 接入登录、总览、节点与资源、配置和入站/出站、预设、证书、TCP 调优、IP 质量、客户端导出、Sub-Store、流量与配额、连接 IP、日志、任务、用户与授权、设置、导航、弹窗、提示和空状态。业务 JS 未更改。
- 配置输入框与行号共用字体、字号、行高、顶部间距；移除 `18.15px` / `22.75px` 及移动端行号专属覆盖，修复长代码滚动错位。
- 图表日期标签按自身宽度居中，首尾内收；手机编辑器操作按钮完整显示“格式化配置”，底部导航文字高度随字体增长。

### 重复与冲突清理

| 声明 | 修改前 | 修改后 |
| --- | --- | --- |
| 独立字号取值 | 37 种 | 15 种（含继承、装饰性 0 及角色变量） |
| 字重取值 | 11 种 | 5 种（含继承） |
| 行高取值 | 19 种 | 6 种 |
| 字距取值 | 13 种 | 2 种（含继承） |

997 处字号声明降为 888 处；页面中的硬编码字体尺寸改为公共角色引用。移除未定义的 `--mono` 引用、工具提示独立字体回退和编辑器多轮冲突；没有新增 `!important`。详见 [声明清单](evidence/style-inventory.json)。

## 真实前后截图

使用同一份仓库 API fixture、相同管理员/测试账号、固定时间 `2026-10-18T05:00:00Z`、相同数据与视口、100% 字号和浅/深主题。截图加载的是基准提交归档或当前生产模块；不是重绘样稿。配置与 Sub-Store 另复用已有隔离页面 fixture，报告不将其当作真实 Agent 部署证明。

- 桌面：1440 × 1000；手机：390 × 844；两种主题均有原始截图。
- 共 56 张基准/最终截图：7 个界面 × 2 个视口 × 2 个主题 × 前后。
- [桌面对比拼图](evidence/compare-desktop.png) · [手机对比拼图](evidence/compare-mobile.png)。拼图仅拼接真实截图；原图完整保留。

| 内容 | 桌面：前 / 后 | 手机：前 / 后 |
| --- | --- | --- |
| 标题、正文、统计数字 | [前](evidence/before-desktop-light-dashboard.png) / [后](evidence/after-desktop-light-dashboard.png) | [前](evidence/before-mobile-light-dashboard.png) / [后](evidence/after-mobile-light-dashboard.png) |
| 设置说明、字段、表单 | [前](evidence/before-desktop-light-settings.png) / [后](evidence/after-desktop-light-settings.png) | [前](evidence/before-mobile-light-settings.png) / [后](evidence/after-mobile-light-settings.png) |
| 筛选、表格、端口、时间 | [前](evidence/before-desktop-light-connections.png) / [后](evidence/after-desktop-light-connections.png) | [前](evidence/before-mobile-light-connections.png) / [后](evidence/after-mobile-light-connections.png) |
| 代码、行号、工具栏 | [前](evidence/before-desktop-light-config-editor.png) / [后](evidence/after-desktop-light-config-editor.png) | [前](evidence/before-mobile-light-config-editor.png) / [后](evidence/after-mobile-light-config-editor.png) |
| 日志 | [前](evidence/before-desktop-light-logs.png) / [后](evidence/after-desktop-light-logs.png) | [前](evidence/before-mobile-light-logs.png) / [后](evidence/after-mobile-light-logs.png) |
| 弹窗、混排、占位文字 | [前](evidence/before-desktop-light-certificates.png) / [后](evidence/after-desktop-light-certificates.png) | [前](evidence/before-mobile-light-certificates.png) / [后](evidence/after-mobile-light-certificates.png) |
| 登录 | [前](evidence/before-desktop-light-login.png) / [后](evidence/after-desktop-light-login.png) | [前](evidence/before-mobile-light-login.png) / [后](evidence/after-mobile-light-login.png) |

深色原图对应文件名将 `light` 换为 `dark`。采集脚本保存在 [capture](capture/)，前后计算样式与几何数据保存在 `evidence/{before,after}-{desktop,mobile}-{light,dark}.json`。

## 验证

| 检查 | 实际结果与证据 |
| --- | --- |
| 现有非浏览器必需检查 | `make non-browser-checks` 通过：格式、模块、策略、CI runner、浏览器诊断、脚本、样式、安装、重部署、quick-start、`go vet ./...`。[日志](evidence/non-browser-checks.txt) |
| 最终样式与模块 | `make styles-check frontend-module-check module-policy-test` 通过。[日志](evidence/final-style-module.txt) |
| Go 前端契约 | `go test ./frontend -skip '^TestAgentBrowserRuntimeSmoke$'` 通过。更新已有字体字符串/哈希断言以对应公共规范。[日志](evidence/frontend-contracts.txt) |
| 现有浏览器场景 | 85 个场景中 **84 个通过**，包括浅/深色、135% 扩展压力、只读与普通用户、配置、日志、表格和菜单；失败的 `config-scope` 在原始基准提交上同样失败。完整运行停止后补跑余下 4 场景，全部通过。[汇总](evidence/browser-summary.json) · [主运行](evidence/browser-final.txt) · [补跑](evidence/browser-remaining.txt) · [基准复现](evidence/baseline-config-scope.txt) |
| 编辑器最终补验 | 配置布局、入站桌面/手机共 4 场景通过，覆盖按钮尺寸修复。[日志](evidence/editor-final-regressions.txt) |
| 导航、鼠标、触摸、缩放 | 6 场景通过：桌面、平板、手机、管理员/普通用户、减弱动效、125% 浏览器缩放。[日志](evidence/dock-navigation.txt) |
| 自定义视口矩阵 | **468 组通过**：13 页面 × 桌面1440×1000、手机390×844、平板820×1180、窄屏320×740、手机横屏844×390、平板横屏1180×820 × 浅深主题 × 90/100/110%。检查页面/卡片/弹窗溢出、标题与按钮裁切、编辑器指标一致。见 `evidence/check-*.json`；日志筛选最终 36 组补验结果合入矩阵，保留 [原始补验](evidence/check-logs-final.json) 与 [汇总](evidence/matrix-summary.json)。 |
| 长内容 | **16 组通过**：四视口两主题，长标题、多行说明、中英文与连续数字，805 行/长 URL 代码格式化、内部横向滚动、行号与编辑区同步；无页面横向溢出。[结果](evidence/stress.json) |
| 弹窗与菜单 | **20 组通过**：五视口两主题、110% 字号；输入、Tab/Shift+Tab、触摸取消、焦点恢复和菜单边界。[结果](evidence/interactions.json) |
| 字体加载 | 无网络字体；拉丁等宽宽度一致，中文回退正常。[结果](evidence/fonts.json) |

捕获工具早期在部分页面渲染后被 shell 重置主题/字号；已修复采集脚本，重新采集并断言实际主题与字号与请求相同，汇总不计入早期无效采集。

发现并修复：110% 下日志筛选按钮截字，按内容宽度自动换行；135% 下总览日期轴越界；手机编辑器按钮孤字换行；编辑器行号行高不一致。IP 质量场景在任务预览已出现但刷新仍禁用时点击，导致测试超时；仅在已有测试中增加等待禁用解除，产品逻辑未改。初始失败日志保留，未把失败运行写成全绿。

既有失败：`config-scope` 断言共享配置页不应出现 `.live-engine-status`，而基准 `4865ad98` 已包含该状态。此次不修改共享实例语义或移除测试。未验证物理手机/平板、Safari/WebKit、系统键盘与地址栏变化、Windows/macOS 实际字体渲染、真实 Agent/CA/DNS 部署；本地未重跑全量后端数据库集成，远端必需 CI 单独记录。

## CI 与测试资源清理

- 本地测试结束：**2026-10-18 18:34:38 Asia/Hong_Kong（UTC+08）**。
- 两个测试服务器、两个本次浏览器会话均已关闭；34395、43711 端口均无监听。[关闭记录](evidence/shutdown.json)。未创建数据库或容器。
- 持久任务：**`qch-typography-d8f3-20261018-cleanup.timer`**，已启用且 `active/waiting`，`Persistent=true`。
- 执行时间：**2026-10-21 18:34:38 Asia/Hong_Kong（UTC+08）**，即 `2026-10-21 10:34:38 UTC`，本地测试结束后 72 小时。
- 范围：本次 `output/playwright/typography-20261018` 基准归档、固定测试目录和临时配置；`.playwright-cli` 中登记的 4 个本次文件。校验设备/inode、固定路径、文件类型与浏览器会话停止状态；目录或文件身份变化会保留并重试，后来产生的文件不在清单内。只在目录为空时移除 `.playwright-cli`。
- 截图、报告、必要的脱敏日志保存在本目录，全部保留；既有项目文件、其他会话、容器、卷、网络、镜像不在清理范围。
- 执行器和清单另存 `/var/lib/qch-typography-d8f3-20261018/`，可在聊天或工作树关闭后由 systemd 执行。错过时间后补执行，失败后一小时重试。
- [注册与执行时间](evidence/cleanup-registration.txt) · [资源清单](evidence/cleanup-manifest.json) · [只读范围核验](evidence/cleanup-scope-check.json) · [执行器](capture/cleanup.py)。

远端必需 CI（Debian/systemd、Alpine/OpenRC、升级沙箱、真实服务权限、Docker 构建）与 PR Policy 以 [PR 的检查记录](https://github.com/qimaoww/QControlHub/pull/300/checks) 为准；本地未将既有浏览器失败标记为通过。
