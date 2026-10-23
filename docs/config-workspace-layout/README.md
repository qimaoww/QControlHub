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

这是第一轮唯一的数值调整，编辑器仍有 504px 高（1440×1000 视口）。

### 5. 全状态视觉收口（第二轮）

第二轮把「配置」页从 6 个状态扩到 15 个可复现状态逐一走查（就绪、长配置、只读、导入快照、离线、不可迁移、需升级、读取失败、空节点、加载、未保存、提交中、保存成功、校验成功、失败），改动仍然只落在结构、既有类名的使用与文案上，未新增样式规则。

- **无编辑器状态的动作收敛**：「导入文件 / 增加入站 / 增加出站」只在编辑器存在（`current`）时渲染。离线、不可迁移、需升级、读取失败四态此前会残留点不动的按钮，现在随编辑器一起消失。
- **离线优先于读取失败**：`liveConfigPhase` 把 `agent.status !== "online"` 提到 `source?.error` 之前。离线节点读取必然失败，此前会被报成「读取配置失败」，现在显示「节点离线」，并提供同一个「重新读取」。
- **需升级态补说明**：该卡片补一句「当前 Agent 版本不支持读取托管配置，升级后可在此编辑。」，并复用「重新读取」按钮，与读取失败卡片保持同一结构。
- **提交结果与按钮同行**：`[data-workspace-result]` 从提交区最后一行移到最前，footer 的 100% 行规则加 `:not([data-workspace-result])` 让它与按钮同排。提交中 / 成功 / 失败反馈不再把提交区顶出视口（提交后 footer 90px，页面仍 1000px 高，按钮与结果同屏）。
- **只读页脚不再重复**：`.config-draft-summary` 去掉「只读」文案（工具栏 `[data-code-status]` 已显示只读），只读页脚由 84px 收到 33px。
- **提交 toast 去重**：`submitTask` 增加 `options.quiet`，配置页校验 / 部署不再弹全局「任务已提交」toast——内联结果区已含「校验已提交 · 查看任务」与终态，失败时也不会再出现绿勾提示与红色错误并列。
- **页脚高度**：就绪态 83 → 71（去掉空结果行占位），提交后 90，只读 33。

### 6. 报错统一为「中文故障说明 + 原始日志」（第三轮）

此前配置页的报错只有一半信息：读取失败在 `requestJSON` 里被翻译成中文状态说明后，原始日志被丢弃；任务失败则直接显示 Agent 的英文输出。现在统一成两段式：

- **中文故障说明**：`frontend/modules/errors.js` 的 `diagnosticError` 新增端口占用（`bind: address already in use`）、内核启动失败、JSON / YAML 解析失败、不支持的字段等规则，其余沿用证书、权限、磁盘、DNS 等既有分类；未命中时给出通用排查句。
- **原始日志**：脱敏后的原文照常输出，前缀「原始错误（供排查）：」，不做二次改写。
- **HTTP 层不再丢日志**：`requestJSON` 构造错误时附带 `body.error`（仅当它不是中文时），读取失败卡片、提交结果与通知都会同时带两类信息。
- **渲染**：无编辑器卡片用 `<small>` 分两行；提交区结果由 `preset-status.js` 的 `fillStatusText` 用 `<br>` 分行，不写内联样式，长度随内容自适应。
- **断言同步**：`frontend/browser/config-source.mjs`、`frontend/config_migration_browser_runtime.mjs` 改为断言「包含中文说明 + 原始日志，且原始文本只出现一次」。

例如部署失败时提交区显示：

```
端口已被占用：该端口已有进程在监听，请更换端口或停止占用进程后重试。
原始错误（供排查）：listen tcp 0.0.0.0:23001: bind: address already in use
```

读取失败（英文操作日志）时错误卡片显示：

```
无法连接后端服务，请稍后重试，或联系管理员检查反向代理与后端服务。
原始错误（供排查）：read workspace: dial tcp 10.0.4.21:7801: connect: connection refused
```


### 7. 配置操作衔接（第四轮）

本轮只处理操作之间的衔接与收尾，不新增任何样式：

- **删除后落在相邻对象**（`frontend/modules/config-inbounds.js`）：删除前记录位置，成功后选中前一个（没有则后一个）入站，不再跳回「完整源码」。
- **离开页面再回来不留悬挂进度**（`frontend/modules/live-config-page.js`）：重新渲染时给仍在运行的任务重新挂接轮询；保存阶段被中断、没有任务可跟踪的操作改为「上次操作未完成，请重新提交」，草稿与输入保持不变。
- **新增与修改后仍定位该对象**：`applyInboundMutation` 把新对象写回 `liveSelectedInbound`，本轮验证了新增后可直接编辑、保存后选中与版本同步。
- **空对象收尾**：删除全部入站后对象列表只剩公共配置、出站、完整源码和预览，顶部「增加入站」入口仍可用。

### 8. 手动编辑配置源码的体验（第五轮）

只改输入与反馈，不动 2 MiB 上限、校验时机与提交拦截：

- **语法错误给位置**：`frontend/modules/config-source-editor.js` 与 `frontend/modules/code-editor.js` 解析 `JSON.parse` 的 `position` / `line column`，输出「JSON 语法错误（第 N 行第 M 列）：原因」，并在状态区追加「跳到第 N 行」按钮，点击即把光标与滚动落到出错位置；解析器不给位置时（内容被截断）按末尾行计算。
- **按键不重建行号**：`code-editor.js` 缓存行数，只有行数变化才重建行号节点。
- **改写可撤销**：恢复当前对象与格式化改为 `execCommand("insertText")` 写入，`Ctrl+Z` / `Ctrl+Shift+Z` 能回到改写前；恢复动作由 `config-files.js` 的 `display({history:true})` 走同一条路径。
- **回车继承缩进**：`keydown` 里按当前行缩进插入换行，Tab / Shift+Tab 行为不变。
- **移动端不改造引号**：编辑器统一设置 `autocapitalize="off"`、`autocorrect="off"`、`autocomplete="off"`、`spellcheck="false"`。
- **空内容与超限给恢复动作**：空内容提示点「恢复当前对象」取回已保存内容；超限说明超出多少字节、约从第几行起超限，并指明可改用「导入文件」。

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
| 提交区 | 1063–1146 | 915–986 |
| 「保存并部署」按钮 | 1080–1118（视口外 118px） | 932–970（首屏可见） |

编辑器起点上移 102px，提交按钮上移 148px 并回到首屏。

### 各状态实测对照

同一脚本、同一数据采集的编辑器起点（`[data-code-input]` 的 top，px）：

| 状态 | 桌面 1440×1000 基线（#308） | 修改后 | 手机 390×844 基线 | 修改后 |
| --- | --- | --- | --- | --- |
| 就绪 | 513（提交按钮在首屏外） | 411（提交按钮可见，页脚 71） | 851 | 720 |
| 长配置 | 532（提交按钮在首屏外） | 411（提交按钮可见） | 945 | 774 |
| 只读 | 494 | 453（页脚 33，无重复「只读」） | 724 | 680 |
| 导入快照 | 494 | 402 | 760 | 665 |
| 读取失败 | 整页跌回错误边界：只剩一条红字，无页头与节点切换 | 壳内错误卡 + 重新读取，状态行无动作按钮 | 同左 | 同左 |
| 离线 | 整页红字「节点离线，暂时无法读取配置」 | 「节点离线」卡片 + 重新读取 | 同左 | 同左 |
| 不可迁移 | 说明卡片（无操作） | 说明卡片（无操作，状态行不再残留动作按钮） | 同左 | 同左 |
| 需升级 | 只有标题，无说明与操作 | 说明 + 重新读取 | 同左 | 同左 |
| 空节点 | 全站空态卡片 | 同左（未改） | 同左 | 同左 |
| 提交中 | 结果独占一行，页脚 83px | 结果与按钮同排，页脚 90px，首屏可见 | 页脚增高 44px | 结果与按钮两行，均在视口内 |
| 提交失败 | 编辑器被错误页替换（起点 174） | 编辑器保留（起点 411），错误与按钮同排 | 同左 | 同左 |

第一轮 48 次、第二轮 60 次采集（15 状态 × 2 设备 × 2 主题）均无横向溢出、无页面异常。

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
| 离线 | `?state=offline` | 「节点离线」卡片 + 重新读取，无动作按钮 |
| 英文原始错误 | `?state=error-en` | 中文故障说明 + 「原始错误（供排查）」两行 |
| 不可迁移 | `?state=unsupported` | 说明卡片（不可自动迁移的字段），无动作按钮 |
| 需升级 | `?state=upgrade` | 说明卡片 + 重新读取 |
| 导入快照 | `?state=import` | 只读快照、页脚「导入并迁移」、单来源不渲染切换 |
| 空节点 | `?state=empty` | 全站空态卡片 + 前往节点设置 |
| 未保存 | 直接输入 | 页脚「有未保存修改」，按钮可用 |
| 提交中 | `?state=slow` + 保存草稿 | 页脚「正在保存…」，三个按钮禁用 |
| 保存成功 | 保存草稿 | 页脚「保存成功」，按钮恢复 |
| 校验成功 | 保存并校验 | 页脚「校验成功 · 查看任务」 |
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

### 第四轮补充

- 删除当前对象后（1440×1000，`?ops=1`，同一账号与数据）：`evidence/screenshots/ops-before/delete-selection.png`（跳回「完整源码」）对比 `ops-after/delete-selection.png`（落到前一个入站）。
- 提交中切换页面再返回：`ops-before/switch-back.png`（停在「正在保存…」）对比 `ops-after/switch-back.png`（提示上次操作未完成，草稿保留）。
- 新增后自动选中：`ops-after/add-selected.png`；删空入站后的对象列表：`ops-after/empty-inbounds.png`。

## 五、交互录屏

`evidence/videos` 内含三组对照录屏（修改前 / 修改后同名场景）：

- `desktop-context-drafts`：切换配置对象 → 编辑端口 → 预览只读 → 切来源与内核（确认弹窗取消 / 接受）→ 刷新保留草稿 → 切节点往返。
- `mobile-file-save-retry-deploy`：导入文件 → 预览 → 保存失败反馈 → 重试成功 → 部署。
- `mobile-protocol-outbound`：协议与出站对象的编辑与提交链路。

### 第四轮补充

- `evidence/recordings/ops-before.webm` / `ops-after.webm`：新增入站 → 编辑并保存 → 删除当前对象 → 落到相邻对象的连续操作。

### 第五轮补充

- `evidence/recordings/editor-desktop-error.webm`：桌面连续编辑、语法错误出现、点击跳转并在出错处修正。
- `evidence/recordings/editor-format-undo.webm`：格式化后用 `Ctrl+Z` 回到格式化前。
- `evidence/recordings/editor-mobile-quotes.webm`：390×844 触摸视口下的引号输入。

## 六、验证结果

- 第五轮手动编辑（`?ops=1&long=1`，507 行 / 13.0 KiB）：语法错误提示「第 507 行第 2 列」并可一键跳转；30 次连续 `input` 共 65 ms（修改前 67 ms），约 2.2 ms/键、无掉帧；恢复当前对象与格式化后 `Ctrl+Z` 都能回到操作前；空内容与超限给出恢复动作；移动端编辑器 `autocapitalize` / `autocorrect` 均为 off。

- 第四轮操作走查（1440×1000，`?ops=1` 夹具，同一账号与数据）：新增入站 → 落到新对象（v8→v9）；改源码保存 → 选中不变、v9→v10；删除当前对象 → 选中前一个入站；连续删除到没有入站 → 空列表且「增加入站」可用；提交中切到其他页面再回来 → 不残留「正在保存…」，草稿保留；删除弹窗取消 → 选中与焦点回到原菜单。

- `node --check` 覆盖改动文件；
- `node frontend/module_smoke.mjs`、`node frontend/config_workspace_smoke.mjs` 通过；
- 配置相关浏览器套件全绿：`config-layout`、`config-layout-mobile`、`config-inbounds`、`config-inbounds-mobile`、`config-restrictions`、`config-scope`、`config-migration`；
- `node scripts/generate-styles.mjs --write` 后 `frontend/app.css` 与源样式一致；
- 桌面 / 手机 / 平板、明暗主题、字号 90 / 100 / 110 下均无横向溢出、无对象按钮裁切，编辑器可聚焦并输入，输入后状态回到「未保存」；
- 键盘：对象列表 Enter 切换、焦点留在按钮；「更多」菜单 Enter / ArrowDown / Escape 与焦点归还正常；`focus-visible` 轮廓可见。
- 第二轮状态走查：15 个状态 × 桌面 / 手机 × 浅色 / 深色共 60 次采集，全部 `overflowX=false`、无页面异常，页脚与提交按钮在提交中 / 成功 / 失败时同屏。
- 第三轮报错格式：`errors_smoke`、`module_smoke`、`config_workspace_smoke`、7 个配置浏览器套件全部通过；`?state=error-en` 与 `?state=fail` 截图确认中文说明 + 原始日志两行同屏。
- 第二轮回归：`module_smoke`、`config_workspace_smoke`、`generate-styles --check`、`go test ./frontend -run 'TestStyles|TestConfig'` 通过；`QCH_BROWSER_SMOKE_MODES=config-layout,config-layout-mobile,config-inbounds,config-inbounds-mobile,config-restrictions,config-scope,config-migration` 的 `TestAgentBrowserRuntimeSmoke` 通过（25.8s）。

## 七、未验证项与已知限制

- 第四轮的真机任务失败重试与部署失败回滚未在真实节点复跑：夹具把任务直接置为终态，覆盖的是状态机与渲染，不覆盖后端真实失败路径。
- 被分享节点用户的配置写入路径未单独采集，沿用 #308 的权限矩阵与套件。
- 第五轮的真实手机软键盘弹出高度只在 390×844 触摸视口下模拟，未接真机；内核侧对配置的实际校验仍依赖真实节点后端。

- 首屏加载瞬间仍是应用级空白过渡（`app.js` 的加载契约，未在本次范围内改动）。
- 平板横屏 1024×768 与字号 110% 下页头变高，提交按钮需要轻微滚动才完整显示；桌面 1440×1000 + 字号 100% 已回到首屏。
- 手机端首屏仍以页头信息为主，编辑器需要一次滚动（页头、内核列表与底部导航挤压，属既有布局）。
- 只读来源的草稿、部署确认等 #308 既有能力未改动，其行为由 #308 套件覆盖。
- 对象较多时（`?long=1` 的 13 个对象）对象导航为横向滚动，选中项会自动滚入视野，列表左端因此可能出现被裁切一半的按钮，这是横向滚动列表的固有表现。
- 配置版本冲突（`conflict`）提示没有单独构造采集，其渲染路径与 `alert` 错误一致，由 #308 套件覆盖。
- 原始日志较长（多行内核输出）时错误卡片会随内容变高，未做折叠或截断，便于直接复制排查。

## 八、证据复现

静态服务器与脚本复用 `docs/config-workspace-refactor/capture`：

```bash
node docs/config-workspace-refactor/capture/server.mjs <worktree> 8902
QCH_PLAYWRIGHT_MODULE=/usr/lib/node_modules/playwright/index.js \
  node docs/config-workspace-refactor/capture/recordings.mjs http://127.0.0.1:8902/ <video-dir>
```

`capture/runtime.mjs` 的查询参数：`mode`（`config-layout` / `config-layout-readonly`）、`state`（`loading` / `error` / `fail` / `offline` / `unsupported` / `upgrade` / `import` / `empty` / `slow`）、`long=1`、`kind=form`。`state=fail` 只让部署与校验任务返回失败，配置读取保持正常；`state=offline` 让工作区读取返回 503；`state=slow` 延迟写请求 8 秒以便观察提交中布局。

## 九、证据清单

```
evidence/
  screenshots/before/   修改前基线（就绪 / 只读 / 读取失败 / 长配置 / 提交失败 × 桌面 / 手机 × 浅色 / 深色）
  screenshots/after/    修改后同名状态，另含平板横竖屏、字号 110%、键盘焦点
  videos/before-*.mp4   修改前录屏（桌面上下文草稿、手机导入失败重试部署、手机协议出站）
  videos/after-*.mp4    修改后同名场景
  data/before-scenes.json、after-scenes.json、after-coverage.json   采集时的结构化度量
  screenshots/states-before/   第二轮：15 个状态 × 桌面 / 手机（浅色）基线
  screenshots/states-after/    第二轮：同状态、同视口的修改后结果
  data/before-states.json、after-states.json   第二轮各状态的几何度量
```

## 十、测试环境与清理

两轮测试都使用本机静态服务器（端口 8901 / 8902）与无头 Chromium。每轮结束时都已停止两个静态服务器、结束测试浏览器会话，并确认 8901 / 8902 端口不再监听。第二轮的状态复采同样如此。截图、录屏、度量 JSON 与本文档随分支入库保留。

延时清理任务（测试结束后 72 小时执行）：

| 项目 | 值 |
| --- | --- |
| 任务标识 | `automation_57e7a7e6-df15-4def-9154-b586651ea934` |
| 执行时间 | 2026-10-22 19:09（测试结束 2026-10-19 19:09 起 72 小时） |
| 时区 | Asia/Hong_Kong |
| 权限 | danger-full-access（仅删除下列范围） |

第四轮（配置操作衔接）的测试环境同样在本轮结束时停止、关闭浏览器并释放端口；临时捕获在 `/root/preview/ops-before`、`ops-after`、`ops2-before`、`ops2-after`、`video-*`，入库材料保留在 `evidence/screenshots/ops-before`、`ops-after` 与 `evidence/recordings`。

| 项目 | 值 |
| --- | --- |
| 任务标识 | `automation_2b458eb2-4f21-49c8-856a-fc180c0e6503` |
| 执行时间 | 2026-10-23 18:45（测试结束 2026-10-20 18:45 起 72 小时） |
| 时区 | Asia/Hong_Kong |
| 范围 | 停止 8901/8902 预览服务与测试浏览器，删除 `/root/preview` 下本轮临时捕获，保留仓库 evidence |

清理范围：本次布局测试的两个工作树（`/root/.codex/worktrees/pr308-layout/qcontrolhub`、基线 `/root/preview/pr308-before`）、`/root/preview` 下本次采集的截图与探针脚本、`/tmp/videos-before`、`/tmp/videos-after` 与本轮日志文件，并在主仓库执行 `git worktree prune`。

保留范围：`docs/config-workspace-layout/` 下的报告与 `evidence/`（截图、录屏、度量），以及远端分支 `codex/config-workspace-layout`。不属于本轮测试的进程与临时目录不在清理范围内。
