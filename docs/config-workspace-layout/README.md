# 配置页布局与视觉融合优化

本报告记录在 [PR #308](https://github.com/qimaoww/QControlHub/pull/308)（配置工作区重构）之上，对「配置」页布局节奏、状态反馈与长内容展示的收口优化。基线为 #308 分支 `codex/config-workspace-refactor` 的 7e0d9eee，本分支 `codex/config-workspace-layout` 在其上继续。

## 一、改动摘要

只动结构与既有类名的使用方式，未新增样式规则、CSS 文件、内联样式、主题变量或 UI 库。

### 1. 页面结构（frontend/modules/live-config-view.js）

- **来源切换收敛**：来源按钮只有一个时不再渲染整块 `.live-config-source-switch`（管理员单来源时原本占满一行）。
- **状态与操作合流**：「导入文件 / 增加入站 / 增加出站」从独立的摘要灰条移入 `.live-config-details` 状态行，`.config-file-actions` 容器保留（既有浏览器断言与拖拽高亮依赖该容器）。
- **删除重复灰条**：原 `.config-file-buttons` 摘要行与紧邻的对象导航行使用同一套样式（同为 `--surface-2` 底 + 上下描边），形成两条视觉等价的分隔带。该行内的 `.config-draft-summary` 没有任何脚本引用（`config-files.js:65` 取的是 footer 内的同名元素），故整行移除。
- **空 / 错误 / 离线 / 升级态统一**：改为 `.node-config-source` 的两列结构（左列标题与说明，右列「重新读取」按钮），匹配 `frontend/styles/100-live-config-base.css:1` 的 `grid-template-columns:minmax(0,1fr) auto`。修改前该分支把标题、红字说明和按钮平铺在同一容器，说明文字被推到右列、按钮被拉成整行宽。
- **失败态不再谎报状态**：读取失败时不渲染「已保存 / 尚未部署」chip，「导入文件」也同时隐藏；「增加入站 / 增加出站」保持 #308 的条件契约（`config-inbounds` 套件依赖它们在缺内核时可点），因此在错误态仍会显示。
- **按钮可达性**：「导入文件」只在可写来源出现；入站 / 出站按钮保持 #308 的条件契约（`can("agent-config.write") && can("tasks.execute")`），缺内核时仍能进入安装向导。

### 2. 首屏读取失败（frontend/modules/live-config-page.js）

修改前，首屏读取失败会跌回 `app.js` 的全局错误边界，整页被一个裸 `.alert.error` 替换（没有页面标题、节点/内核切换和重试入口）。现在：

- 首屏（尚无任何已渲染快照）读取失败时在壳内渲染错误卡，页面结构、节点/内核切换与「重新读取」全部保留；
- 已有工作区时的刷新、切换内核失败仍原样抛回 `live-config-navigation.js` 的既有恢复路径（该路径负责提示与回滚），避免把切换失败误报成「整页不可用」；
- `configWorkspace` 可能为空后补可选链，防止失败分支再次跌回全局错误边界。

### 3. 长标题（frontend/modules/config-files.js）

对象按钮标题超过 26 字符时压缩为「前 13 字符 + … + 后 10 字符」，完整值进入 `title` 与 `aria-label`。长 tag 不再把 240px 宽的对象按钮撑到不可读。

### 4. 编辑器高度配平（frontend/styles/530-config-file-drafts.css:109）

`height: clamp(320px, calc(100dvh - 450px), 680px)` 的常量是在旧页头上标定的。新页头固定占 411px、提交区固定占 83px（合计 494px），沿用 450 会让整页恒比视口高约 44px，提交按钮永远差一点。改为 `calc(100dvh - 496px)` 并重新生成 `frontend/app.css`。

这是本次唯一的数值调整，编辑器仍有 504px 高（1440×1000 视口）。

## 二、纵向节奏（1440×1000 / 浅色 / 就绪）

| 区块 | 修改前 | 修改后 |
| --- | --- | --- |
| 页头「节点 · 内核」 | 83–145 | 83–145 |
| 状态行 | 145–195 | 145–203（承载导入 / 入站 / 出站动作） |
| 内核切换 | 195–261 | 203–269 |
| 来源切换 | 261–312（单来源仍占一整块） | 并入状态行，单来源不渲染 |
| 对象导航 | 312–371 | 269–348 |
| 配置摘要灰条 | 371–450（与对象导航同款） | 已删除 |
| 编辑器工具条 | 450–513 | 348–411 |
| 编辑器 | 513–1063（550） | 411–915（504） |
| 提交区 | 1063–1146 | 915–998 |
| 「保存并部署」按钮 | 1080–1118（视口外 118px） | 932–970（首屏可见） |

编辑器起点上移 102px，提交按钮上移 148px 并回到首屏。

### 各状态实测对照

同一脚本、同一数据采集的编辑器起点（`[data-code-input]` 的 top，px）：

| 状态 | 桌面 1440×1000 修改前 | 修改后 | 手机 390×844 修改前 | 修改后 |
| --- | --- | --- | --- | --- |
| 就绪 | 513（提交按钮在首屏外） | 411（提交按钮可见） | 851 | 720 |
| 长配置 | 532（提交按钮在首屏外） | 411（提交按钮可见） | 945 | 774 |
| 只读 | 494 | 453 | 724 | 680 |
| 读取失败 | 无编辑器，整页只剩一条红字 | 壳内错误卡 + 重新读取 | 无编辑器 | 壳内错误卡 + 重新读取 |
| 提交失败 | 编辑器被错误页替换（起点 174） | 编辑器保留（起点 411），失败信息在提交区 | 同左 | 同左 |

全部 48 次采集（2 版本 × 6 状态 × 2 设备 × 2 主题）均无横向溢出、无页面异常。

## 三、覆盖清单

### 页面与状态

| 状态 | 进入方式 | 结果 |
| --- | --- | --- |
| 就绪（托管配置） | 默认 | 6 个配置对象、3 个动作按钮、提交区可见 |
| 就绪（个人配置） | 普通账号 | 来源切换保留、动作按钮按权限收敛 |
| 只读 | `?mode=config-layout-readonly` | 来源切换 2 项、动作按钮 0、状态显示「只读」 |
| 长配置 | `?long=1` | 13 个对象、13.0 KiB、超长节点名与超长 tag 正常 |
| 读取失败 | `?state=error` | 壳内错误卡 + 重新读取，无裸 alert |
| 提交失败 | `?state=fail` + 保存并校验 | 结果区显示节点返回的错误，编辑器与草稿保留 |
| 首屏加载 | `?state=loading` | 仍为应用级过渡（本次未改，见未验证项） |
| 缺内核 / 离线 / 升级 | #308 既有套件 | 保持 #308 契约 |

### 视口、主题与账号

| 维度 | 取值 |
| --- | --- |
| 桌面 | 1440×1000 |
| 手机 | 390×844（触摸） |
| 平板横屏 | 1024×768 |
| 平板竖屏 | 768×1024 |
| 主题 | 浅色 / 深色 |
| 界面字号 | 90 / 100 / 110（设置页三档） |
| 账号 | 管理员 / 普通用户 |
| 交互 | 对象切换、源码编辑、导入文件、预览只读、内核切换确认、节点切换、刷新、菜单鼠标与键盘（ArrowDown / Enter / Escape）、焦点可见 |

## 四、修改前后截图

同一账号、同一数据、同一视口、同一主题、同一页面状态下采集。文件名中的 `before` / `after` 对应基线与本分支。

见 `evidence/screenshots/before` 与 `evidence/screenshots/after`。

## 五、交互录屏

`evidence/videos` 内含三组对照录屏（修改前 / 修改后同名场景）：

- `desktop-context-drafts`：切换配置对象 → 编辑端口 → 预览只读 → 切来源与内核（确认弹窗取消 / 接受）→ 刷新保留草稿 → 切节点往返。
- `mobile-file-save-retry-deploy`：导入文件 → 预览 → 保存失败反馈 → 重试成功 → 部署。
- `mobile-protocol-outbound`：协议与出站对象的编辑与提交链路。

## 六、验证结果

- `node --check` 覆盖改动文件；
- `node frontend/module_smoke.mjs`、`node frontend/config_workspace_smoke.mjs` 通过；
- 配置相关浏览器套件全绿：`config-layout`、`config-layout-mobile`、`config-inbounds`、`config-inbounds-mobile`、`config-restrictions`、`config-scope`、`config-migration`；
- `node scripts/generate-styles.mjs --write` 后 `frontend/app.css` 与源样式一致；
- 桌面 / 手机 / 平板、明暗主题、字号 90 / 100 / 110 下均无横向溢出、无对象按钮裁切，编辑器可聚焦并输入，输入后状态回到「未保存」；
- 键盘：对象列表 Enter 切换、焦点留在按钮；「更多」菜单 Enter / ArrowDown / Escape 与焦点归还正常；`focus-visible` 轮廓可见。

## 七、未验证项与已知限制

- 首屏加载瞬间仍是应用级空白过渡（`app.js` 的加载契约，未在本次范围内改动）。
- 平板横屏 1024×768 与字号 110% 下页头变高，提交按钮需要轻微滚动才完整显示；桌面 1440×1000 + 字号 100% 已回到首屏。
- 手机端首屏仍以页头信息为主，编辑器需要一次滚动（页头、内核列表与底部导航挤压，属既有布局）。
- 只读来源的草稿、部署确认等 #308 既有能力未改动，其行为由 #308 套件覆盖。

## 八、证据复现

静态服务器与脚本复用 `docs/config-workspace-refactor/capture`：

```bash
node docs/config-workspace-refactor/capture/server.mjs <worktree> 8902
QCH_PLAYWRIGHT_MODULE=/usr/lib/node_modules/playwright/index.js \
  node docs/config-workspace-refactor/capture/recordings.mjs http://127.0.0.1:8902/ <video-dir>
```

`capture/runtime.mjs` 的查询参数：`mode`（`config-layout` / `config-layout-readonly`）、`state`（`loading` / `error` / `fail`）、`long=1`、`kind=form`。`state=fail` 只让部署与校验任务返回失败，配置读取保持正常，便于观察提交失败反馈。

## 九、证据清单

```
evidence/
  screenshots/before/   修改前基线（就绪 / 只读 / 读取失败 / 长配置 / 提交失败 × 桌面 / 手机 × 浅色 / 深色）
  screenshots/after/    修改后同名状态，另含平板横竖屏、字号 110%、键盘焦点
  videos/before-*.mp4   修改前录屏（桌面上下文草稿、手机导入失败重试部署、手机协议出站）
  videos/after-*.mp4    修改后同名场景
  data/before-scenes.json、after-scenes.json、after-coverage.json   采集时的结构化度量
```

## 十、测试环境与清理

本轮测试使用本机静态服务器（端口 8901 / 8902）与无头 Chromium，测试结束后停止进程、关闭浏览器会话并释放端口。截图、录屏与本文档保留在仓库内；临时目录按登记的时间点清理。
