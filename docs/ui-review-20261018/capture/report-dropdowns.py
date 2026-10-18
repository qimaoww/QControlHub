from pathlib import Path
import hashlib,json,subprocess,re
root=Path('docs/ui-review-20261018');e=root/'evidence/dropdowns';baseline=Path('output/playwright/ui-review-20261018/round4/before/frontend')
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
main=subprocess.check_output(['git','rev-parse','origin/main'],text=True).strip()
tracked=subprocess.check_output(['git','diff','--binary','--','frontend'])
for f in ['frontend/modules/select-menu.js','frontend/browser/select-menus.mjs']:
 r=subprocess.run(['git','diff','--no-index','--binary','--','/dev/null',f],capture_output=True);assert r.returncode==1;tracked+=r.stdout
(e/'production.patch').write_bytes(tracked)
r=subprocess.run(['git','diff','--no-index','--binary','--',str(baseline),'frontend'],capture_output=True);assert r.returncode==1;(e/'turn-only.patch').write_bytes(r.stdout)
def tree(p):
 h=hashlib.sha256()
 for f in sorted(p.rglob('*')):
  if f.is_file():h.update(str(f.relative_to(p)).encode()+b'\0'+f.read_bytes()+b'\0')
 return h.hexdigest()
(e/'baseline.json').write_text(json.dumps({'main':main,'head':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'before':'previous delivered working tree, main plus before-production.patch','before_patch_sha256':sha(e/'before-production.patch'),'before_frontend_tree_sha256':tree(baseline),'after_frontend_tree_sha256':tree(Path('frontend')),'time_zone':'Asia/Shanghai','fixture_time':'2026-10-18T05:00:00Z','viewports':{'desktop':[1440,1000],'mobile':[390,844],'tablet':[820,1180]},'mobile_and_tablet':'hasTouch=true, isMobile=true; screenshots are viewport captures, not fullPage','comparison':'same page-specific fixture account, data, theme, viewport and steps; real raster captures only'},ensure_ascii=False,indent=2)+'\n')
before={p.name:p for p in (baseline/'styles').glob('*.css')};after={p.name:p for p in Path('frontend/styles').glob('*.css')};variables=lambda d:set(re.findall(r'(--[\w-]+)\s*:', ''.join(p.read_text() for p in d.values())))
(e/'style-audit.json').write_text(json.dumps({'new_css_files':sorted(after.keys()-before.keys()),'new_css_custom_property_definitions':sorted(variables(after)-variables(before)),'source_open_braces_before':sum(p.read_text().count('{') for p in before.values()),'source_open_braces_after':sum(p.read_text().count('{') for p in after.values()),'adapter_has_inline_styles':bool(re.search(r'\.style\b|style=',Path('frontend/modules/select-menu.js').read_text())),'menu_rule_note':'Existing configuration-menu rules extended; its old mobile positioning override moved to a shared non-popover override. No new rule block appended. Existing typography/size rule moved from client format to shared select control.'},indent=2)+'\n')
scope=json.loads((e/'scope.json').read_text());scope['total_module_count']=len({a['file'] for a in scope['modules']}|{a['file'] for a in scope['dynamic_selects']});(e/'scope.json').write_text(json.dumps(scope,ensure_ascii=False,indent=2)+'\n')
text='''# 全站下拉框统一整改

![一图修改前后对照](evidence/dropdowns/before-after.png)

## 范围与基准

- 最新 main / HEAD：`MAIN`（PR #298），交付前重新 fetch，main 未变化。近期 PR、原始需求依据与历史文案删减仍见 [总报告](README.md)。
- 本轮按“下拉框全部优化”扩至全站：**28 个模块，74 处 select 模板、3 处动态创建点和 1 处 Reality 域名建议入口**。[文件清单](evidence/dropdowns/scope.json)。动态协议、通用配置项及证书选择器均由同一委托处理覆盖。
- 修改前是第三轮交付工作树，保存完整前端快照及其相对 main 的补丁；不是把已经修改的页面充作修改前。[基准及树校验值](evidence/dropdowns/baseline.json) · [修改前补丁](evidence/dropdowns/before-production.patch) · [本轮差异](evidence/dropdowns/turn-only.patch) · [累计生产差异](evidence/dropdowns/production.patch)。
- 每对使用相同页面的测试账号、数据、主题、视口和操作。桌面 1440×1000；手机 390×844、平板 820×1180，后两者启用 touch。测试数据来自既有浏览器夹具，证书接口补充固定的脱敏数据；集成测试另用真实 PostgreSQL、密码与 CSRF 会话。

## 实际修改及复用

| 位置 | 修改与现有组件 / 样式 |
| --- | --- |
| 全站收起的选择框 | 保留原生 select、字段名、值、校验与事件；将现有客户端格式选择框的 13px 尺寸规则归到共享控件，沿用 `010-shell-controls.css` / `130-workflow-theme.css`；触摸设备沿用现有 44px / 16px 规则 |
| 展开的选项列表 | 共享适配层使用配置页实际的 `config-inbound-menu-items`、按钮结构、`notice-message` 换行；复用其背景、边框、圆角、阴影、悬停、焦点和禁用状态，选中态复用配置文件按钮的 `--accent` / `--accent-soft` 状态 |
| 弹窗、窄屏、长列表 | 使用原生 popover 顶层及 CSS 锚定，复用并整理既有菜单定位规则；菜单可向上展开、内部滚动，适配横屏；没有位置内联样式。修正实看手机截图时发现的页面滚动偏移及延迟 scroll 事件误关闭 |
| 动态表单及自动刷新 | 委托绑定覆盖后续挂载的选择框；`refresh.js` 只保留当前展开菜单拥有的 ARIA 状态，后台重绘不会主动关闭不变的菜单；选项、禁用状态或页面移除时释放菜单 |
| Reality 域名建议 | 沿用原 input / datalist 的候选值，使用相同菜单；支持过滤、键盘接受和取消，允许自由输入域名；失焦恢复原 datalist 绑定 |
| 重复局部样式 | 清理 20 处局部尺寸、字体与内边距覆盖，移除配置文件选择框的独立等宽字体 / 小圆角；保留页面所需的布局约束。[整理清单](evidence/dropdowns/style-consolidation.json) |

没有新增样式文件、UI 库、主题变量或适配层内联样式；调整 / 迁移既有规则块，样式源括号数从 4781 减至 4776。[样式审计](evidence/dropdowns/style-audit.json)。全站范围由本次明确追加的要求授权。原样式合约的基础 hash 已随共享控件整理更新，仍锁定基础样式，生成一致性检查保留。本轮没有新增产品说明文案；前几轮删减示例见总报告。

## 截图与实看

**108 组状态对照**：102 组选择框展开对照、6 组域名建议输入状态对照；另有 36 组收起状态对照。共 **288 张成对原图 + 2 张真实后端集成截图 + 1 张合成图 = 291 张 PNG**。

已亲自查看桌面、手机、平板浅深主题的整组图片，并放大查看手机 HTTP-01 离线选项、流量弹窗、TLS、Reality 建议以及平板的长协议菜单；最终合成图也已打开检查。合成图只裁剪、拼接真实原图，文字放在产品画面之外。[裁剪范围](evidence/dropdowns/comparison-crops.json)。

说明：基准浏览器未能把原生 datalist 建议浮层捕获进截图，6 组 Reality 修改前原图保留实际捕获到的输入框画面；本轮主题建议浮层已真实捕获，未重绘或补画。未将这些基准图称为原生浮层的完整对照。

主矩阵逐项检查字体、控件高度、打开及 Escape 不改值；整改后额外断言菜单不越出视口。[主矩阵数据](evidence/dropdowns/capture-checks.json) · [动态协议桌面](evidence/dropdowns/protocol-desktop-before-after.json) / [触摸设备](evidence/dropdowns/protocol-touch-before-after.json) · [Reality 桌面](evidence/dropdowns/reality-desktop-before-after.json) / [触摸设备](evidence/dropdowns/reality-touch-before-after.json)。

### 展开状态原图索引

每格分别链接修改前 / 修改后；完整视口图片保留页面上下文。

'''.replace('MAIN',main)
states=[('证书筛选','certificate-filters'),('HTTP-01 验证节点 / 离线禁用','certificate-node'),('DNS 服务商','certificate-provider'),('流量端点筛选','traffic-filters'),('流量周期弹窗','traffic-cycle'),('系统时区','settings'),('内核日志节点','logs'),('配置高级字段','config-field'),('动态创建的入站协议','config-protocol'),('客户端显示格式','client-format'),('客户端地址弹窗','client-address'),('Sub-Store 同步地址','substore-address'),('连接 IP 内核','connections'),('任务状态','tasks'),('节点批量动作','batch'),('TLS 证书方式','tls'),('XHTTP CDN','xhttp'),('Reality 域名建议','reality-domain')]
text+='| 页面 / 状态 | 桌面浅色 | 桌面深色 | 手机浅色 | 手机深色 | 平板浅色 | 平板深色 |\n| --- | --- | --- | --- | --- | --- | --- |\n'
for title,state in states:
 cells=[]
 for device in ['desktop','mobile','tablet']:
  for theme in ['light','dark']:
   files=[f'{stage}-{device}-{theme}-{state}.png' for stage in ['before','after']]
   assert all((e/f).exists() for f in files),files
   cells.append(' / '.join(f'[{label}](evidence/dropdowns/{f})' for label,f in zip(['前','后'],files)))
 text+='| '+title+' | '+' | '.join(cells)+' |\n'
text+='''
### 收起状态与滚动

收起状态同时保留字号 / 高度 / 排版对照：

| 页面 | 桌面浅色 | 桌面深色 | 手机浅色 | 手机深色 | 平板浅色 | 平板深色 |
| --- | --- | --- | --- | --- | --- | --- |
'''
for title,state in [s for s in states if s[1] in ['certificate-filters','traffic-filters','settings','client-format','tls','xhttp']]:
 cells=[]
 for device in ['desktop','mobile','tablet']:
  for theme in ['light','dark']:
   files=[f'{stage}-{device}-{theme}-{state}-closed.png' for stage in ['before','after']];assert all((e/f).exists() for f in files)
   cells.append(' / '.join(f'[{label}](evidence/dropdowns/{f})' for label,f in zip(['前','后'],files)))
 text+='| '+title+' | '+' | '.join(cells)+' |\n'
text+='''
深色横屏 844×390 的任务状态菜单包含长列表，实际录制 End / Home / Escape 操作，整改后列表在视口内滚动：[修改前录屏](evidence/dropdowns/before-dropdown-scroll.webm) / [修改后录屏](evidence/dropdowns/after-dropdown-scroll.webm) · [记录](evidence/dropdowns/scroll-checks.json)。

## 实际检查、回归与限制

- **32 个浏览器模式全部通过**，涵盖共享菜单、配置入站、原配置操作菜单及动效、预设 / 任务重试、证书直接依赖、流量普通及密集布局、节点批量、客户端、Sub-Store、连接 IP、日志、用户、TCP 调优、设置及只读状态。[最终日志](evidence/dropdowns/browser-regressions-final.txt)。
- 新增共享组件浏览器回归覆盖：原生表单取值 / required、每次选择仅一次 input / change、同值无重复事件、禁用选项及 optgroup、隐藏项、键盘跳过禁用项、搜索、End/Home、Tab 接受、Escape 取消、表单 reset、后台重绘、动态选项及路由移除、100 项长名称、页面已滚动时的弹窗定位、浅深主题、域名过滤及自由输入。最后的分组语义调整后，桌面 / 手机再次通过。[定向日志](evidence/dropdowns/select-menu-regressions.txt)。
- 真实触摸操作在浅深主题均通过：筛选并重置、切换 HTTP-01 后挂载节点、离线节点保持禁用、选择节点回写原字段；选项点击区域均为 44px。[数据](evidence/dropdowns/touch-interactions.json)。
- 真实 PostgreSQL / 密码及 CSRF 会话证书测试通过，含桌面 / 手机创建、账号、普通用户隔离、共享与协议选择、实例任务、版本替换及撤销，8.610s。[日志](evidence/dropdowns/certificate-integration.txt)。
- 模块检查、样式生成及四项生成器测试、前端 Go 合约、`git diff --check` 均通过。[模块](evidence/dropdowns/module-check.txt) · [样式](evidence/dropdowns/styles-check.txt) · [Go 合约](evidence/dropdowns/frontend-tests.txt)。
- 已执行的浏览器为 Chromium，手机 / 平板为触摸模拟；物理设备、Safari/WebKit、Firefox、屏幕阅读器、公网 CA/DNS 签发与真实 Agent/Caddy 部署未验证。缺少 popover / CSS 锚定能力的浏览器保留原生选择行为，其系统浮层配色未统一；该兼容分支未作实机验证。

## 测试环境清理

环境进程、浏览器会话和端口已关闭；测试数据库保留为停止状态。持久任务标识、最新 72 小时执行时间、时区、精确资源范围及注册结果见 [总报告的清理记录](README.md#测试环境清理) 与 [清理清单](evidence/cleanup-manifest.json)。仅清理本任务数据库容器及其匿名卷、两处带 inode 保护的本任务暂存目录；保留本目录内截图、录屏、报告与脱敏日志，以及已有其他数据。
'''
(root/'dropdowns.md').write_text(text)
print('report generated; dropdown PNG',len(list(e.glob('*.png'))))
