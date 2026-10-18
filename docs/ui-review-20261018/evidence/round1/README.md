# 近期界面统一整改

## 基准与范围

- 基准：`26a63ff1a975c93f824c13d75961504fe3eb5fc5`，main / PR #298。开始及交付前均执行 `git fetch origin main`，main 未变化。
- 分支：`codex/unify-recent-ui`。修改前用 `git archive` 保存完整基准，并先拍摄基准界面；其余对比继续从该基准读取。
- 主检查范围：#290–#298 的证书、节点共享、共享实例、配置状态、日志、流量同步、导航，以及证书选择和 TLS 表单的直接依赖。向前核对 #277–#289，实际延伸整改到 #280–#283 的 XHTTP、TLS 预设与 ACME 邮箱。
- 需求依据：PR 描述、提交差异和仓库随附验收文档。仓库没有完整保留每次聊天原文；下表按可追溯的需求记录判断，不将推断当作用户原话。22 个 PR 的描述摘要、提交及完整前端文件清单见 [PR 范围记录](evidence/pr-scope.json)。

| PR | 记录中的界面要求 | 检查页面与文件 | 本次处理 |
| --- | --- | --- | --- |
| [#298](https://github.com/qimaoww/QControlHub/pull/298) | 恢复配置状态及节点管理入口，保持原布局 | 节点、配置；`agent-view.js`、`live-config-view.js`、`shared-instance-state.js` | 保留已修复入口、状态位置及运行版本；桌面/手机回归通过 |
| [#296](https://github.com/qimaoww/QControlHub/pull/296)、[#297](https://github.com/qimaoww/QControlHub/pull/297) | 只修展开侧栏选中框漂移；撤回额外导航行为 | 侧栏、手机菜单；`shell-view.js`、`workspace-motion.js`、`700-shell-dock-rail.css` | 保留 #297 恢复的尺寸、交互和菜单，不改导航 |
| [#295](https://github.com/qimaoww/QControlHub/pull/295) | 原同步弹窗展示端口、实例、版本及阻断原因 | 流量同步、统计状态；`traffic-sync-dialog.js`、`traffic-accounting-view.js` | 删除同步副标题和整段计量内部流程说明；保留候选信息、历史、阻断原因及选择行为 |
| [#294](https://github.com/qimaoww/QControlHub/pull/294) | 证书界面复用主题和弹窗；共享 HTTP-01 与 Caddy 同步 | 证书列表、筛选、新增/导入、详情、订单、来源、账号、授权、节点共享；`certificate-view.js`、`certificate-forms.js`、`certificates.js`、`certificate-selector.js`、`agent-sharing.js`、`310-certificates.css` | 删除介绍/统计头、独立提示卡片外观和重复控件主题；复用成熟组件；精简技术说明 |
| [#293](https://github.com/qimaoww/QControlHub/pull/293) | 更新、迁移和回滚修复 | 无新增前端界面 | 排除部署实现 |
| [#292](https://github.com/qimaoww/QControlHub/pull/292) | 共享实例状态和生命周期只放节点管理；配置页负责编辑/部署 | 节点内核行、配置页与更多菜单；`agent-view.js`、`live-config-view.js`、`shared-instance-state.js` | 已使用原内核行与按钮，保留操作位置、限制、版本和未知状态；回归通过 |
| [#291](https://github.com/qimaoww/QControlHub/pull/291) | 证书功能复用列表、共享控件、弹窗和主题，移除多余介绍 | 证书及协议选择器；上述证书文件、`preset-bindings.js` | 按原有组件结构整改列表/表单/弹窗，精简重复选择摘要 |
| [#290](https://github.com/qimaoww/QControlHub/pull/290) | 授权实例过滤与日志采集状态 | 日志筛选、分页、空状态、错误；`core-log-view.js`、`core-log-dom.js`、`core-log-source-status.js`、`core-log-scope.js` | 沿用原日志控件；状态、错误和下一步具有操作价值，保留；桌面/手机回归通过 |
| [#280](https://github.com/qimaoww/QControlHub/pull/280) | XHTTP 基础字段与折叠高级设置复用现有组件 | XHTTP 独立/组合、上下行分离；`server-plan-xhttp.js` | 删除重复内部拓扑、导出实现、信任验证承诺与版本运行细节；保留端点、证书、CDN 接入及参数约束 |
| [#281](https://github.com/qimaoww/QControlHub/pull/281)、[#282](https://github.com/qimaoww/QControlHub/pull/282)、[#283](https://github.com/qimaoww/QControlHub/pull/283) | 邮箱继承、ZeroSSL 回退与简洁域名工作流，保持现有表单 | TLS 预设、系统设置；`server-plan-managed-tls.js`、`settings-view.js` | 删除字段复述和独立长说明，保留账号范围、重新部署及邮箱覆盖规则 |
| [#277](https://github.com/qimaoww/QControlHub/pull/277)、[#278](https://github.com/qimaoww/QControlHub/pull/278)、[#279](https://github.com/qimaoww/QControlHub/pull/279) | TLS 原表单及任务编辑重试工作区 | TLS、任务重试弹窗；`preset-view.js`、`task-edit-retry.js`、`535-tls-and-retry.css` | 作为成熟组件及直接依赖核对；保留编辑、校验、重试、错误原因和未保存确认；预设浏览器回归包含任务编辑重试 |
| #284–#289 | 就绪、流量、安装权限、账户并发、IP 质量错误等修复 | 相关提交及直接调用点，详见 JSON | 未将后端、部署与数据修复扩入本次生产改动 |

表中未写目录的模块文件均位于 `frontend/modules/`，样式源位于 `frontend/styles/`；JSON 保留完整路径。

生产修改限于证书界面及共享域名字段、流量同步文案、TLS/XHTTP 文案、ACME 设置文案、已有证书浏览器断言，以及被替代的局部样式。`app.css` 由原生成器生成。后端、权限、接口、路由和全局主题定义均保持基准内容。

## 复用的成熟页面与组件

| 整改位置 | 参照页面 | 实际复用 |
| --- | --- | --- |
| 证书标题与操作栏 | 用户管理 `user-view.js` | `users-workspace`、`user-admin-workspace`、`users-toolbar`、`users-title`、`users-toolbar-actions` |
| 证书列表与空状态 | 用户共享节点分配列表 | 原 `user-allocation-panel/row/node/terms/row-actions`、`user-share-empty`，删除证书对字号、颜色和列宽的覆盖 |
| 证书/账号/HTTP 授权表单 | 系统设置 `settings-view.js` | `settings-form`、`settings-field`，使用原输入框的背景、边框、字号、禁用与焦点状态 |
| 证书弹窗与滚动 | 配置通用/出站弹窗 | `config-inbound-dialog`、`config-common-dialog`、`config-outbound-dialog`、`config-inbound-heading/body`、`config-access-close` |
| 复选项、帮助折叠与底部操作 | 现有预设/配置编辑 | `plan-check`、`preset-option-panel`、`plan-fields one`、`inbound-delete-actions` |
| 证书操作、来源、错误 | 原用户分配/节点共享/反馈 | `user-allocation-tools`、`agent-share-recipient`、`alert error`、`settings-hint`、现有换行类 `cert-domains` / `task-diagnostic`；状态沿用 `status-label` |
| 共享域名输入 | 节点共享原表单 | `settings-field`、`plan-fields one`，删除证书专用输入外观 |
| TLS/XHTTP、流量、ACME 设置 | 原预设、同步、设置页面 | 保留已有 `builder-section`、`preset-option-panel`、`traffic-edit-dialog`、设置分区等结构，精简其中的文字 |

未新增样式文件、CSS 声明、内联样式、主题变量或 UI 库。`310-certificates.css` 从 **9492 字节缩减到 2324 字节**，剩余的是既有详情/记录排版与换行规则；`560-traffic-sync.css` 删除无使用者的说明样式。选择器仅删除无使用者的分支，声明与值均来自基准。全局样式源文件未修改。[CSS 复用核对](evidence/style-reuse-check.json)。

表单复用时同时修正初始化顺序：先恢复草稿，再绑定现有节点范围提示和泛域名校验。恢复后的字段会立即采用原规则，错误仍由原接口返回；没有引入新校验或放宽限制。

## 文案删减示例

| 位置 | 修改前 | 修改后 |
| --- | --- | --- |
| 证书列表 | 英文眉题、介绍“统一管理签发、授权与跨节点部署”、资源/到期统计头 | 现有工具栏标题“证书中心”及操作按钮 |
| HTTP-01 | 整段解释托管代理、临时监听、逐个 A/AAAA 检查、通知 CA 和跨节点部署 | 接入前提一句；“外部网站接入”折叠项只保留挑战路径、9080 和 Host 配置 |
| Caddy 详情 | “保持原证书的续期责任……向仍有有效授权的绑定实例同步；不会重复创建 ACME 续期订单” | 删除；已有“续期管理方”和“面板同步”字段表达状态 |
| Caddy 来源 | 长篇导入/分发流程与“无需手动上传私钥” | “Caddy 自动续期，面板同步新版本。” |
| 节点共享 | “每行一个精确域名……接受共享且启用后生效。协议部署仍按上方内核与端口权限单独检查。” | “每行一个完整域名；留空不授权，接受共享后生效。” |
| 流量同步 | 副标题及整段“待部署端口不会提前计量；旧端口保留历史……端口预留由节点所有者管理” | 删除；候选行继续显示待部署、旧端口、版本和阻断原因 |
| TLS 预设 | 重复解释默认 443、自动生成路径、Host 不修改证书域名 | 删除；字段值、标签和占位文字已表达 |
| XHTTP 高级设置 | 大段解释各版本 auto 模式、uTLS、完整导出及旧参数内部保存 | 保留最低版本、auto 建议、JSON 导出入口及填写约束 |
| ACME 设置 | 三处解释适用节点、其他 TLS 方式、邮箱继承、回退与既有任务 | “仅当前账号生效，保存后重新部署配置。”及“填写邮箱可启用 ZeroSSL 回退，单个预设可覆盖。” |
| 授权撤销确认 | 多句业务责任、影响列表及协调说明 | “撤销后停止分发和同步，现有部署保留。” |

保留了客户端信任自签证书、外部网站接入、手动 DNS 操作、API 权限、真实失败原因及撤销/删除确认。证书算法、字段约束、续期、授权、同步选择、端口版本检查与业务请求载荷保持原规则。

## 真实截图与录屏

截图均来自 Chromium 的实际生产 DOM 与 CSS，没有绘制替代界面。按每组使用相同账号、测试数据、语言、时区、主题、视口及操作顺序；固定计时基准为 `2026-10-18T05:00:00Z`。桌面 1440×1000；手机 390×844，启用 mobile / touch；平板 820×1180，启用 touch；DPR 1，语言 zh-CN，时区 Asia/Hong_Kong。精确条件见 [基准记录](evidence/baseline.json)。

前后对比使用本地固定 API fixture 和原有预设测试 fixture。预设参数来自项目 Go 生成器，渲染器与事件绑定来自生产模块。它们验证真实界面与交互，不把模拟响应作为真实签发或部署证据。真实密码/CSRF、PostgreSQL 与证书流程另有集成测试。

- 17 类状态 × 桌面/手机 × 浅/深主题，共 **68 组前后对比**。
- 平板浅/深主题 34 张补充截图。
- 交互检查的等待、长错误、禁用状态 29 张补充截图；真实证书选择器 2 张。总计 201 张 PNG；[原图与录屏 SHA-256 清单](evidence/artifacts.json)。
- 手机导入表单的滚动对比：[修改前录屏](evidence/before-mobile-scroll.webm)、[修改后录屏](evidence/after-mobile-scroll.webm)。相同 390×844 浅色主题、输入与三次 wheel 序列；记录原始视频。精简内容并复用弹窗后，本场景正文所需滚动从 64px 降至 3px。较长错误场景仍验证了底部按钮可滚动到达。
- [几何与主题测量](evidence/capture-metrics.json)、[设置页补充测量](evidence/settings-capture-metrics.json)、[交互结果](evidence/interaction-checks.json)。所有对比场景均无页面或弹窗水平溢出，实际主题与记录一致。连续 240 字符错误码与长流量诊断另外通过既有换行类验证：[证书长错误](evidence/after-mobile-light-certificate-error.png)、[流量长错误](evidence/after-mobile-light-traffic-error.png)、[深色流量错误](evidence/after-desktop-dark-traffic-error.png)。

### 代表对比：证书列表（桌面浅色）

| 修改前 | 修改后 |
| --- | --- |
| ![证书列表修改前](evidence/before-desktop-light-certificates.png) | ![证书列表修改后](evidence/after-desktop-light-certificates.png) |

### 代表对比：HTTP-01 表单（手机浅色）

| 修改前 | 修改后 |
| --- | --- |
| ![HTTP-01 修改前](evidence/before-mobile-light-certificate-http.png) | ![HTTP-01 修改后](evidence/after-mobile-light-certificate-http.png) |

### 按页面配对的原图

下表每个链接均为原始 PNG；平板和补充错误状态见其后的索引。

| 页面状态 | 桌面浅色 | 桌面深色 | 手机浅色 | 手机深色 |
| --- | --- | --- | --- | --- |
| 证书列表与筛选 | [前](evidence/before-desktop-light-certificates.png) / [后](evidence/after-desktop-light-certificates.png) | [前](evidence/before-desktop-dark-certificates.png) / [后](evidence/after-desktop-dark-certificates.png) | [前](evidence/before-mobile-light-certificates.png) / [后](evidence/after-mobile-light-certificates.png) | [前](evidence/before-mobile-dark-certificates.png) / [后](evidence/after-mobile-dark-certificates.png) |
| 新增证书 · DNS API | [前](evidence/before-desktop-light-certificate-create.png) / [后](evidence/after-desktop-light-certificate-create.png) | [前](evidence/before-desktop-dark-certificate-create.png) / [后](evidence/after-desktop-dark-certificate-create.png) | [前](evidence/before-mobile-light-certificate-create.png) / [后](evidence/after-mobile-light-certificate-create.png) | [前](evidence/before-mobile-dark-certificate-create.png) / [后](evidence/after-mobile-dark-certificate-create.png) |
| 新增证书 · HTTP-01 | [前](evidence/before-desktop-light-certificate-http.png) / [后](evidence/after-desktop-light-certificate-http.png) | [前](evidence/before-desktop-dark-certificate-http.png) / [后](evidence/after-desktop-dark-certificate-http.png) | [前](evidence/before-mobile-light-certificate-http.png) / [后](evidence/after-mobile-light-certificate-http.png) | [前](evidence/before-mobile-dark-certificate-http.png) / [后](evidence/after-mobile-dark-certificate-http.png) |
| 导入 PEM | [前](evidence/before-desktop-light-certificate-import.png) / [后](evidence/after-desktop-light-certificate-import.png) | [前](evidence/before-desktop-dark-certificate-import.png) / [后](evidence/after-desktop-dark-certificate-import.png) | [前](evidence/before-mobile-light-certificate-import.png) / [后](evidence/after-mobile-light-certificate-import.png) | [前](evidence/before-mobile-dark-certificate-import.png) / [后](evidence/after-mobile-dark-certificate-import.png) |
| 证书详情与绑定 | [前](evidence/before-desktop-light-certificate-detail.png) / [后](evidence/after-desktop-light-certificate-detail.png) | [前](evidence/before-desktop-dark-certificate-detail.png) / [后](evidence/after-desktop-dark-certificate-detail.png) | [前](evidence/before-mobile-light-certificate-detail.png) / [后](evidence/after-mobile-light-certificate-detail.png) | [前](evidence/before-mobile-dark-certificate-detail.png) / [后](evidence/after-mobile-dark-certificate-detail.png) |
| 手动 DNS 订单 | [前](evidence/before-desktop-light-certificate-dns-order.png) / [后](evidence/after-desktop-light-certificate-dns-order.png) | [前](evidence/before-desktop-dark-certificate-dns-order.png) / [后](evidence/after-desktop-dark-certificate-dns-order.png) | [前](evidence/before-mobile-light-certificate-dns-order.png) / [后](evidence/after-mobile-light-certificate-dns-order.png) | [前](evidence/before-mobile-dark-certificate-dns-order.png) / [后](evidence/after-mobile-dark-certificate-dns-order.png) |
| 使用授权 | [前](evidence/before-desktop-light-certificate-grants.png) / [后](evidence/after-desktop-light-certificate-grants.png) | [前](evidence/before-desktop-dark-certificate-grants.png) / [后](evidence/after-desktop-dark-certificate-grants.png) | [前](evidence/before-mobile-light-certificate-grants.png) / [后](evidence/after-mobile-light-certificate-grants.png) | [前](evidence/before-mobile-dark-certificate-grants.png) / [后](evidence/after-mobile-dark-certificate-grants.png) |
| Caddy 来源同步 | [前](evidence/before-desktop-light-certificate-sources.png) / [后](evidence/after-desktop-light-certificate-sources.png) | [前](evidence/before-desktop-dark-certificate-sources.png) / [后](evidence/after-desktop-dark-certificate-sources.png) | [前](evidence/before-mobile-light-certificate-sources.png) / [后](evidence/after-mobile-light-certificate-sources.png) | [前](evidence/before-mobile-dark-certificate-sources.png) / [后](evidence/after-mobile-dark-certificate-sources.png) |
| 账号与 HTTP 授权管理 | [前](evidence/before-desktop-light-certificate-accounts.png) / [后](evidence/after-desktop-light-certificate-accounts.png) | [前](evidence/before-desktop-dark-certificate-accounts.png) / [后](evidence/after-desktop-dark-certificate-accounts.png) | [前](evidence/before-mobile-light-certificate-accounts.png) / [后](evidence/after-mobile-light-certificate-accounts.png) | [前](evidence/before-mobile-dark-certificate-accounts.png) / [后](evidence/after-mobile-dark-certificate-accounts.png) |
| ACME 账号表单 | [前](evidence/before-desktop-light-certificate-acme-account.png) / [后](evidence/after-desktop-light-certificate-acme-account.png) | [前](evidence/before-desktop-dark-certificate-acme-account.png) / [后](evidence/after-desktop-dark-certificate-acme-account.png) | [前](evidence/before-mobile-light-certificate-acme-account.png) / [后](evidence/after-mobile-light-certificate-acme-account.png) | [前](evidence/before-mobile-dark-certificate-acme-account.png) / [后](evidence/after-mobile-dark-certificate-acme-account.png) |
| DNS 服务商账号表单 | [前](evidence/before-desktop-light-certificate-dns-account.png) / [后](evidence/after-desktop-light-certificate-dns-account.png) | [前](evidence/before-desktop-dark-certificate-dns-account.png) / [后](evidence/after-desktop-dark-certificate-dns-account.png) | [前](evidence/before-mobile-light-certificate-dns-account.png) / [后](evidence/after-mobile-light-certificate-dns-account.png) | [前](evidence/before-mobile-dark-certificate-dns-account.png) / [后](evidence/after-mobile-dark-certificate-dns-account.png) |
| 证书空状态 | [前](evidence/before-desktop-light-certificate-empty.png) / [后](evidence/after-desktop-light-certificate-empty.png) | [前](evidence/before-desktop-dark-certificate-empty.png) / [后](evidence/after-desktop-dark-certificate-empty.png) | [前](evidence/before-mobile-light-certificate-empty.png) / [后](evidence/after-mobile-light-certificate-empty.png) | [前](evidence/before-mobile-dark-certificate-empty.png) / [后](evidence/after-mobile-dark-certificate-empty.png) |
| 节点共享域名 | [前](evidence/before-desktop-light-node-sharing.png) / [后](evidence/after-desktop-light-node-sharing.png) | [前](evidence/before-desktop-dark-node-sharing.png) / [后](evidence/after-desktop-dark-node-sharing.png) | [前](evidence/before-mobile-light-node-sharing.png) / [后](evidence/after-mobile-light-node-sharing.png) | [前](evidence/before-mobile-dark-node-sharing.png) / [后](evidence/after-mobile-dark-node-sharing.png) |
| 流量端口同步 | [前](evidence/before-desktop-light-traffic-sync.png) / [后](evidence/after-desktop-light-traffic-sync.png) | [前](evidence/before-desktop-dark-traffic-sync.png) / [后](evidence/after-desktop-dark-traffic-sync.png) | [前](evidence/before-mobile-light-traffic-sync.png) / [后](evidence/after-mobile-light-traffic-sync.png) | [前](evidence/before-mobile-dark-traffic-sync.png) / [后](evidence/after-mobile-dark-traffic-sync.png) |
| 托管 TLS 预设 | [前](evidence/before-desktop-light-tls-preset.png) / [后](evidence/after-desktop-light-tls-preset.png) | [前](evidence/before-desktop-dark-tls-preset.png) / [后](evidence/after-desktop-dark-tls-preset.png) | [前](evidence/before-mobile-light-tls-preset.png) / [后](evidence/after-mobile-light-tls-preset.png) | [前](evidence/before-mobile-dark-tls-preset.png) / [后](evidence/after-mobile-dark-tls-preset.png) |
| XHTTP 预设与高级设置 | [前](evidence/before-desktop-light-xhttp-preset.png) / [后](evidence/after-desktop-light-xhttp-preset.png) | [前](evidence/before-desktop-dark-xhttp-preset.png) / [后](evidence/after-desktop-dark-xhttp-preset.png) | [前](evidence/before-mobile-light-xhttp-preset.png) / [后](evidence/after-mobile-light-xhttp-preset.png) | [前](evidence/before-mobile-dark-xhttp-preset.png) / [后](evidence/after-mobile-dark-xhttp-preset.png) |
| 系统设置 · ACME 邮箱 | [前](evidence/before-desktop-light-settings-acme.png) / [后](evidence/after-desktop-light-settings-acme.png) | [前](evidence/before-desktop-dark-settings-acme.png) / [后](evidence/after-desktop-dark-settings-acme.png) | [前](evidence/before-mobile-light-settings-acme.png) / [后](evidence/after-mobile-light-settings-acme.png) | [前](evidence/before-mobile-dark-settings-acme.png) / [后](evidence/after-mobile-dark-settings-acme.png) |

### 平板补充截图

| 页面状态 | 浅色 | 深色 |
| --- | --- | --- |
| 证书列表与筛选 | [截图](evidence/after-tablet-light-certificates.png) | [截图](evidence/after-tablet-dark-certificates.png) |
| 新增证书 · DNS API | [截图](evidence/after-tablet-light-certificate-create.png) | [截图](evidence/after-tablet-dark-certificate-create.png) |
| 新增证书 · HTTP-01 | [截图](evidence/after-tablet-light-certificate-http.png) | [截图](evidence/after-tablet-dark-certificate-http.png) |
| 导入 PEM | [截图](evidence/after-tablet-light-certificate-import.png) | [截图](evidence/after-tablet-dark-certificate-import.png) |
| 证书详情与绑定 | [截图](evidence/after-tablet-light-certificate-detail.png) | [截图](evidence/after-tablet-dark-certificate-detail.png) |
| 手动 DNS 订单 | [截图](evidence/after-tablet-light-certificate-dns-order.png) | [截图](evidence/after-tablet-dark-certificate-dns-order.png) |
| 使用授权 | [截图](evidence/after-tablet-light-certificate-grants.png) | [截图](evidence/after-tablet-dark-certificate-grants.png) |
| Caddy 来源同步 | [截图](evidence/after-tablet-light-certificate-sources.png) | [截图](evidence/after-tablet-dark-certificate-sources.png) |
| 账号与 HTTP 授权管理 | [截图](evidence/after-tablet-light-certificate-accounts.png) | [截图](evidence/after-tablet-dark-certificate-accounts.png) |
| ACME 账号表单 | [截图](evidence/after-tablet-light-certificate-acme-account.png) | [截图](evidence/after-tablet-dark-certificate-acme-account.png) |
| DNS 服务商账号表单 | [截图](evidence/after-tablet-light-certificate-dns-account.png) | [截图](evidence/after-tablet-dark-certificate-dns-account.png) |
| 证书空状态 | [截图](evidence/after-tablet-light-certificate-empty.png) | [截图](evidence/after-tablet-dark-certificate-empty.png) |
| 节点共享域名 | [截图](evidence/after-tablet-light-node-sharing.png) | [截图](evidence/after-tablet-dark-node-sharing.png) |
| 流量端口同步 | [截图](evidence/after-tablet-light-traffic-sync.png) | [截图](evidence/after-tablet-dark-traffic-sync.png) |
| 托管 TLS 预设 | [截图](evidence/after-tablet-light-tls-preset.png) | [截图](evidence/after-tablet-dark-tls-preset.png) |
| XHTTP 预设与高级设置 | [截图](evidence/after-tablet-light-xhttp-preset.png) | [截图](evidence/after-tablet-dark-xhttp-preset.png) |
| 系统设置 · ACME 邮箱 | [截图](evidence/after-tablet-light-settings-acme.png) | [截图](evidence/after-tablet-dark-settings-acme.png) |

### 补充状态截图（整改后）

| 条件 | 等待与禁用提交 | 长错误与滚动 | 禁用新增/空状态 |
| --- | --- | --- | --- |
| desktop / dark | [截图](evidence/after-desktop-dark-certificate-pending.png) | [截图](evidence/after-desktop-dark-certificate-error.png) | [截图](evidence/after-desktop-dark-certificate-disabled.png) |
| desktop / light | [截图](evidence/after-desktop-light-certificate-pending.png) | [截图](evidence/after-desktop-light-certificate-error.png) | [截图](evidence/after-desktop-light-certificate-disabled.png) |
| mobile / dark | [截图](evidence/after-mobile-dark-certificate-pending.png) | [截图](evidence/after-mobile-dark-certificate-error.png) | [截图](evidence/after-mobile-dark-certificate-disabled.png) |
| mobile / light | [截图](evidence/after-mobile-light-certificate-pending.png) | [截图](evidence/after-mobile-light-certificate-error.png) | [截图](evidence/after-mobile-light-certificate-disabled.png) |
| phone-landscape / dark | [截图](evidence/after-phone-landscape-dark-certificate-pending.png) | [截图](evidence/after-phone-landscape-dark-certificate-error.png) | [截图](evidence/after-phone-landscape-dark-certificate-disabled.png) |
| small-phone / light | [截图](evidence/after-small-phone-light-certificate-pending.png) | [截图](evidence/after-small-phone-light-certificate-error.png) | [截图](evidence/after-small-phone-light-certificate-disabled.png) |
| tablet / dark | [截图](evidence/after-tablet-dark-certificate-pending.png) | [截图](evidence/after-tablet-dark-certificate-error.png) | [截图](evidence/after-tablet-dark-certificate-disabled.png) |
| tablet-landscape / dark | [截图](evidence/after-tablet-landscape-dark-certificate-pending.png) | [截图](evidence/after-tablet-landscape-dark-certificate-error.png) | [截图](evidence/after-tablet-landscape-dark-certificate-disabled.png) |
| tablet / light | [截图](evidence/after-tablet-light-certificate-pending.png) | [截图](evidence/after-tablet-light-certificate-error.png) | [截图](evidence/after-tablet-light-certificate-disabled.png) |

## 实际验证

| 执行项 | 结果及证据 |
| --- | --- |
| `make frontend-module-check styles-check` | 通过；原模块行为、生成样式和样式生成器检查。[日志](evidence/modules-styles.txt) |
| `GOFLAGS=-buildvcs=false go test -count=1 ./frontend -skip '^TestAgentBrowserRuntimeSmoke$'` | 通过；前端 Go 合约。[日志](evidence/frontend-contracts.txt) |
| 18 个原浏览器场景 | 全部通过：`presets{,-mobile}`、`sharing{,-mobile}`、`traffic-layout{,-mobile}`、`shared-node{,-mobile}`、`config-inbounds{,-mobile}`、`config-layout{,-mobile}`、`capabilities-settings{,-mobile}`、`users-layout{,-mobile}`、`logs{,-mobile}`。[日志](evidence/browser-regressions.txt) |
| ACME 设置最终复核 | `capabilities-settings`、`capabilities-settings-mobile`、`capabilities-settings-readonly` 全部通过，验证原设置页及只读界面。[日志](evidence/settings-regressions.txt) |
| 真实证书浏览器与 API 集成 | 独立 PostgreSQL 17，`QCH_MULTI_USER_BROWSER=1 go test -count=1 -timeout 5m ./internal/api -run '^TestCertificate(BrowserSessions\|OrdinaryUserIsolationSharingAndDeployment)$' -v` 通过。创建 DNS 账号、自签签发、用户隔离、UI 授权、协议选择器、受保护路径、版本替换、撤销待部署授权及同浏览器切换账号均通过。[日志](evidence/certificate-integration.txt)；[桌面选择器](evidence/desktop-certificate-selector.png)、[手机选择器](evidence/mobile-certificate-selector.png) |
| 9 组定向交互检查 | 每组 19 项基础检查，另有两组流量长错误检查，共 173 项通过。桌面/手机/平板浅深主题，另加 375×812、844×390、1120×680。覆盖草稿恢复、HTTP-01 泛域名原校验、不可用节点、授权范围提示、文件上限、提交锁定、错误聚焦、PEM 保留、长错误换行、滚动、Tab/Shift+Tab、焦点恢复、筛选空状态、加密条件禁用。[结果](evidence/interaction-checks.json) |
| 真实触摸输入 | 在 hasTouch 的手机浏览器中用 `tap` 完成新增→账号管理→返回草稿→关闭→刷新；无残留弹窗。[结果](evidence/touch.txt) |
| 范围与差异检查 | `git diff --check` 通过；仅删减/复用局部样式，无新增 CSS 声明或全局主题变更；main 复核未变化 |

原成熟页面回归包括用户分配列表、设置页及只读页、配置通用/入站弹窗、节点内核管理。预设场景继续覆盖生成、编辑、保存、校验/部署提交、TLS 模式切换、XHTTP 参数保留与任务编辑重试；流量场景保留失效候选、阻断、选择与同步错误的原回归。

未验证：物理手机/平板、Safari/WebKit、系统键盘与浏览器地址栏收起；公网 CA/DNS 服务商实际签发、真实 Agent/Caddy 更新部署。本次没有重跑全量后端或基础设施测试，也没有部署生产环境。相关既有 PR 的运行证据不计入本次通过项。

## 测试环境停止与 72 小时清理

- 测试结束：2026-10-18 **13:16:55 Asia/Hong_Kong（UTC+08:00）**。
- 两个截图服务器已停止，Playwright 会话 `ui-review-20261018` / `ui-review-mobile` 已关闭；本次数据库容器已停止。端口 **43973、33345、32831** 及最终补拍端口已释放。[停止与端口核对](evidence/shutdown.json)。
- 已登记且启用持久 systemd timer：**`qch-ui-review-7d05-20261018-cleanup.timer`**。
- 执行时间：2026-10-21 **13:16:55 Asia/Hong_Kong（UTC+08:00）**，即 **05:16:55 UTC**，测试结束后 72 小时。`Persistent=true`；停机错过时会在启动后补执行。
- 清理范围：本次创建的 `qch-ui-review-20261018-db` 容器及其匿名 PostgreSQL 数据卷；`output/playwright/ui-review-20261018` 临时目录（含基准归档、临时脚本/配置/日志）和本次新建的 `.playwright-cli` 临时浏览器记录目录。核对固定容器 ID、任务标签、停止状态、挂载清单及目录设备/inode 后执行；验证失败会保留资源并重试。
- 既有容器、账号、数据库、镜像、网络及其他任务的清理计划均不在范围内。截图、录屏、报告、测量和脱敏日志保存在本目录，清理任务不删除它们。
- 注册与范围核对成功：[资源清单](evidence/cleanup-manifest.json)、[持久任务注册结果](evidence/cleanup-registration.txt)、[清理程序](capture/cleanup.py)。运行程序与清单另存 `/var/lib/qch-ui-review-7d05-20261018/`，不会依赖临时目录存续。

## 复现材料

[截图服务器](capture/server.mjs)、[固定数据](capture/runtime.mjs)、[截图步骤](capture/capture.js)、[定向交互步骤](capture/checks.js)随报告保存。`__BASE__` / `__STAGE__` / `__DEVICE__` / `__THEME__` 是运行前替换的参数；使用 Playwright CLI 的 `run-code --filename` 执行。截图服务器第一个参数为对应版本的仓库目录，预设目录用 `go run frontend/testdata/preset_plans.go` 生成到本次临时目录。复现新的环境需另建资源清单和清理时间。
