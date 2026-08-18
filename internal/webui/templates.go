package webui

// Asset cache-busting versions, centralized so that a CSS or JS bump touches one
// constant instead of every <link>/<script> tag. Bump the relevant one (usually
// appCSSCacheVersion on a visual change); the tests assert the rendered URLs.
const (
	themeJSCacheVersion       = "ui-clarity-6"
	appCSSCacheVersion        = "ui-desktop-63"
	agentConfigJSCacheVersion = "ui-functional-28"
)

const pageTemplates = `
{{define "login"}}
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="description" content="登录 · {{panelName .Settings}}">
  <title>登录 · {{panelName .Settings}}</title>
  <link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Crect width='64' height='64' rx='16' fill='%234f46e5'/%3E%3Ctext x='32' y='41' text-anchor='middle' font-family='sans-serif' font-size='23' font-weight='700' fill='white'%3ERF%3C/text%3E%3C/svg%3E">
  <script src="/assets/theme.js?v=` + themeJSCacheVersion + `"></script><link rel="stylesheet" href="/assets/app.css?v=` + appCSSCacheVersion + `">
</head>
<body class="login-body">
  <button class="theme-toggle login-theme-toggle" type="button" data-theme-toggle aria-label="切换颜色主题"><span data-theme-icon aria-hidden="true">☀</span></button>
  <main class="login-shell compact-login">
    <section class="login-card"><a href="/login" class="brand login-card-brand"><span class="brand-mark large">RF</span><strong>{{panelName .Settings}}</strong></a><div class="login-card-head"><h1>登录</h1></div>{{if .Error}}<div class="alert error">{{.Error}}</div>{{end}}<form method="post" action="/login" class="stack-form"><input class="visually-hidden" type="text" name="username" value="admin" autocomplete="username" tabindex="-1" aria-hidden="true"><label>管理令牌<input type="password" name="token" autocomplete="current-password" autofocus required></label><button class="button primary" type="submit">登录</button></form></section>
  </main>
</body></html>
{{end}}

{{define "app"}}
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="description" content="{{.Title}} · {{panelName .Settings}}">
  <title>{{.Title}} · {{panelName .Settings}}</title>
  <link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Crect width='64' height='64' rx='16' fill='%234f46e5'/%3E%3Ctext x='32' y='41' text-anchor='middle' font-family='sans-serif' font-size='23' font-weight='700' fill='white'%3ERF%3C/text%3E%3C/svg%3E">
  <script src="/assets/theme.js?v=` + themeJSCacheVersion + `"></script><link rel="stylesheet" href="/assets/app.css?v=` + appCSSCacheVersion + `">
</head>
<body class="app-body page-{{.Active}}">
<div class="desktop-app">
  <aside class="app-dock">
    <a class="dock-logo" href="/" aria-label="{{panelName .Settings}} 总览"><span>RF</span></a>
    <nav class="dock-nav" aria-label="主导航">
      <a class="{{if eq .Active "dashboard"}}active{{end}}" href="/" title="总览"><svg viewBox="0 0 24 24"><path d="M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z"/></svg><span class="dock-label">总览</span></a>
      <a class="{{if or (eq .Active "agents") (eq .Active "agent-config")}}active{{end}}" href="/agents" title="节点"><svg viewBox="0 0 24 24"><rect x="4" y="3.5" width="16" height="6" rx="2"/><rect x="4" y="14.5" width="16" height="6" rx="2"/><path d="M8 6.5h.01M8 17.5h.01M12 6.5h5M12 17.5h5"/></svg><span class="dock-label">节点</span><b data-online-count {{if not .Overview.AgentsOnline}}hidden{{end}}>{{.Overview.AgentsOnline}}</b></a>
      <a class="{{if eq .Active "client-access"}}active{{end}}" href="/client-access" title="客户端配置"><svg viewBox="0 0 24 24"><path d="M7 4.5h10a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-11a2 2 0 0 1 2-2Z"/><path d="M8.5 8.5h7M8.5 12h7M8.5 15.5h3"/><circle cx="16.5" cy="15.5" r="1"/></svg><span class="dock-label">客户端</span></a>
      <a class="{{if or (eq .Active "live-config") (eq .Active "configs")}}active{{end}}" href="/configs" title="配置"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h5"/></svg><span class="dock-label">配置</span>{{if .Overview.NodeConfigs}}<b>{{.Overview.NodeConfigs}}</b>{{end}}</a>
      <a class="{{if eq .Active "tasks"}}active{{end}}" href="/tasks" title="任务"><svg viewBox="0 0 24 24"><path d="M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"/></svg><span class="dock-label">任务</span><b class="hot" data-task-active-count {{if not .Overview.TasksPending}}hidden{{end}}>{{.Overview.TasksPending}}</b></a>
      <a class="{{if eq .Active "settings"}}active{{end}}" href="/settings" title="设置"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1a1.7 1.7 0 0 0 1.9.3A1.7 1.7 0 0 0 10 3V2.8h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1z"/></svg><span class="dock-label">设置</span></a>
    </nav>
    <div class="dock-tools"><button type="button" data-theme-toggle aria-label="切换颜色主题" title="切换主题"><svg viewBox="0 0 24 24"><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6 7 7M17 17l1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4"/><circle cx="12" cy="12" r="4"/></svg><span class="dock-label">主题</span></button><form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" aria-label="退出登录" title="退出登录"><svg viewBox="0 0 24 24"><path d="M10 4H5v16h5M14 8l4 4-4 4M8 12h10"/></svg><span class="dock-label">退出</span></button></form></div>
  </aside>

  <aside class="context-sidebar">
    <header class="context-brand"><a href="/"><span class="brand-mark">RF</span><strong>{{panelName .Settings}}</strong></a></header>

    {{if eq .Active "dashboard"}}
      <nav class="context-menu" aria-label="总览目录"><a class="active" href="#summary"><span>01</span>运行概览</a><a href="#fleet"><span>02</span>节点状态</a><a href="#activity"><span>03</span>最近活动</a></nav>
      <section class="context-metrics"><div><span>在线 / 全部节点</span><b>{{.Overview.AgentsOnline}} / {{.Overview.Agents}}</b></div><div><span>节点版本 / 独立档案</span><b>{{.Overview.NodeConfigs}} / {{.Overview.Configs}}</b></div><div><span>准备中 / 执行中</span><b>{{.Overview.TasksQueued}} / {{.Overview.TasksRunning}}</b></div></section>
    {{else if eq .Active "agents"}}
      <a class="context-primary" href="/client-access">客户端配置 →</a>
      <div class="context-section-label"><span>已接入节点</span><b>{{len .Agents}}</b></div>
      <nav class="context-list" aria-label="节点列表">{{range .Agents}}<a class="{{if eq .ID $.SelectedAgentID}}active{{end}}" href="/agents?node={{.ID}}#node-{{.ID}}" data-context-agent="{{.ID}}"><i class="status-dot {{statusClass .Status}}" data-context-agent-dot></i><span><strong>{{.Name}}</strong><small>{{.OS}} / {{.Arch}}</small></span><em data-context-agent-label>{{if eq .Status "online"}}在线{{else}}离线{{end}}</em></a>{{else}}<p>还没有节点</p>{{end}}</nav>
    {{else if eq .Active "client-access"}}{{$accessPage := .ClientAccessPage}}
      <a class="context-back" href="/agents">← 返回节点</a>
      <a class="context-primary {{if and $accessPage (not $accessPage.AgentID)}}active{{end}}" href="/client-access">全部客户端配置</a>
      <div class="context-section-label"><span>按节点查看</span><b>{{len .Agents}}</b></div>
      <nav class="context-list" aria-label="客户端配置节点">{{range .Agents}}<a class="{{if and $accessPage (eq .ID $accessPage.AgentID)}}active{{end}}" href="/client-access?agent_id={{.ID}}{{if and $accessPage $accessPage.Engine}}&amp;engine={{$accessPage.Engine}}{{end}}"><i class="status-dot {{statusClass .Status}}"></i><span><strong>{{.Name}}</strong><small>{{len .Capabilities}} 个支持内核</small></span><em>{{if eq .Status "online"}}在线{{else}}离线{{end}}</em></a>{{else}}<p>还没有节点</p>{{end}}</nav>
    {{else if eq .Active "live-config"}}{{$live := .LiveConfigPage}}
      <div class="context-section-label"><span>选择节点</span><b>{{len .Agents}}</b></div>
      <nav class="context-list" aria-label="配置节点">{{range .Agents}}<a class="{{if and $live (eq .ID $live.Agent.ID)}}active{{end}}" href="/configs?node={{.ID}}"><i class="status-dot {{statusClass .Status}}"></i><span><strong>{{.Name}}</strong><small>{{len .Capabilities}} 个支持内核</small></span><em>{{if eq .Status "online"}}在线{{else}}离线{{end}}</em></a>{{else}}<p>还没有节点</p>{{end}}</nav>
      {{if $live}}<div class="context-section-label"><span>选择内核</span><b>{{len $live.InstalledEngines}}</b></div><nav class="context-list config-context-list" aria-label="切换手动配置内核">{{range $live.InstalledEngines}}<a class="{{if eq . $live.Engine}}active{{end}}" href="/configs?node={{$live.Agent.ID}}&engine={{.}}"><span class="context-engine {{.}}">{{engineName .}}</span><span><strong>{{engineName .}}</strong><small>节点实际文件</small></span></a>{{end}}</nav>{{end}}
    {{else if eq .Active "configs"}}
      <a class="context-primary" href="/configs/archive?new=1">＋ 新建配置档案</a>
      <div class="context-section-label"><span>配置档案</span><b>{{len .Configs}}</b></div>
      <nav class="context-list config-context-list" aria-label="配置档案">{{range .Configs}}<a class="{{if eq .ID $.FormConfig.ID}}active{{end}}" href="/configs/archive?id={{.ID}}"><span class="context-engine {{.Engine}}">{{engineName .Engine}}</span><span><strong>{{.Name}}</strong><small>v{{.Version}} · {{ago .UpdatedAt}}</small></span></a>{{else}}<p>还没有保存的配置</p>{{end}}</nav>
    {{else if eq .Active "tasks"}}
      <nav class="context-menu task-context-menu" aria-label="任务状态"><a class="{{if not .TaskStatusFilter}}active{{end}}" href="/tasks">全部任务</a><a class="{{if eq .TaskStatusFilter "pending"}}active{{end}}" href="/tasks?status=pending">准备中</a><a class="{{if eq .TaskStatusFilter "running"}}active{{end}}" href="/tasks?status=running">执行中</a><a class="{{if eq .TaskStatusFilter "succeeded"}}active{{end}}" href="/tasks?status=succeeded">成功</a><a class="{{if eq .TaskStatusFilter "failed"}}active{{end}}" href="/tasks?status=failed">失败</a><a class="{{if eq .TaskStatusFilter "canceled"}}active{{end}}" href="/tasks?status=canceled">已取消</a></nav>
    {{else if eq .Active "agent-config"}}{{$configPage := .AgentConfigPage}}
      <a class="context-back" href="/agents?node={{$configPage.Agent.ID}}">← 返回节点</a>
      <div class="context-section-label"><span>选择内核</span><b>{{len $configPage.Agent.Capabilities}}</b></div>
      <nav class="context-list engine-context-list" aria-label="切换内核">{{range $configPage.Agent.Capabilities}}<a class="{{if eq . $configPage.Catalog.Engine}}active{{end}}" href="/agents/{{$configPage.Agent.ID}}/config/{{.}}"><span class="context-engine {{.}}">{{engineName .}}</span><span><strong>{{engineName .}}</strong><small>服务端入站</small></span></a>{{end}}</nav>
      <ol class="context-steps"><li class="active"><b>1</b><span>选择入站</span></li><li><b>2</b><span>编辑参数</span></li><li><b>3</b><span>校验或部署</span></li></ol>
    {{else if eq .Active "settings"}}
      <nav class="context-menu" aria-label="设置目录"><a class="active" href="#identity"><span>01</span>面板标识</a><a href="#defaults"><span>02</span>操作默认值</a><a href="#synchronization"><span>03</span>状态同步</a></nav>
    {{end}}

  </aside>

  <section class="workspace-shell">
    <header class="workspace-topbar"><div class="workspace-route"><span>{{panelName .Settings}}</span><i>/</i><b>{{.Title}}</b><i class="role-badge role-{{.Role}}">{{roleName .Role}}</i></div><div class="workspace-actions"><span class="sync-state {{if not .Overview.AgentsOnline}}inactive{{end}}" data-sync-state><i></i><span data-sync-label>{{if .Overview.AgentsOnline}}{{.Overview.AgentsOnline}} 个节点在线{{else}}等待节点连接{{end}}</span></span>{{if eq .Active "dashboard"}}<a class="button small" href="/agents">管理节点</a>{{else if eq .Active "agents"}}<a class="button small" href="/client-access">客户端配置</a>{{if roleAtLeast .Role "admin"}}<a class="button small" href="#enrollment">注册节点</a>{{end}}{{else if eq .Active "client-access"}}<a class="button small" href="/agents">返回节点</a>{{else if eq .Active "configs"}}<a class="button small" href="/configs">节点实际配置</a>{{else if eq .Active "tasks"}}<a class="button small task-refresh-link" href="/tasks?agent_id={{.TaskAgentFilter}}&status={{.TaskStatusFilter}}&action={{.TaskActionFilter}}&limit={{.TaskLimit}}">刷新</a>{{else if eq .Active "agent-config"}}<a class="button small" href="/agents?node={{.AgentConfigPage.Agent.ID}}">返回节点</a>{{end}}<details class="mobile-account-menu"><summary aria-label="打开账户菜单">•••</summary><div><button type="button" data-theme-toggle>切换主题</button><form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">退出登录</button></form></div></details></div></header>
    <main class="workspace-main">
      {{if .Notice}}<div class="alert success{{if .FocusTaskID}} task-feedback pending{{end}}" {{if .FocusTaskID}}data-task-feedback="{{.FocusTaskID}}" data-auto-load-source="{{if or (eq .Active "agent-config") (eq .Active "live-config")}}1{{else}}0{{end}}" role="status" aria-live="polite" aria-busy="true"{{end}}>{{if .FocusTaskID}}<i data-task-feedback-dot></i>{{end}}<span data-task-feedback-message>{{.Notice}}</span>{{if .FocusTaskID}}<a data-task-feedback-link href="/tasks?task={{.FocusTaskID}}#task-{{.FocusTaskID}}" {{if or (eq .Active "agent-config") (eq .Active "live-config")}}hidden{{end}}>查看任务详情 →</a>{{end}}</div>{{end}}{{if .Error}}<div class="alert error">{{.Error}}</div>{{end}}
      {{if eq .Active "dashboard"}}{{template "dashboard-page" .}}{{end}}
      {{if eq .Active "agents"}}{{template "agents-page" .}}{{end}}
      {{if eq .Active "client-access"}}{{template "client-access-page" .}}{{end}}
      {{if eq .Active "agent-config"}}{{template "agent-config-page" .}}{{end}}
      {{if eq .Active "live-config"}}{{template "live-config-page" .}}{{end}}
      {{if eq .Active "configs"}}{{template "configs-page" .}}{{end}}
      {{if eq .Active "tasks"}}{{template "tasks-page" .}}{{end}}
      {{if eq .Active "settings"}}{{template "settings-page" .}}{{end}}
    </main>
  </section>
</div>
<dialog class="confirm-dialog" data-confirm-dialog aria-labelledby="confirm-dialog-title" aria-describedby="confirm-dialog-message">
  <div class="confirm-dialog-card">
    <span class="confirm-dialog-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 3.5 21 20H3zM12 9v5M12 17.5h.01"/></svg></span>
    <div><p class="eyebrow">操作确认</p><h2 id="confirm-dialog-title">确认继续？</h2><p id="confirm-dialog-message" data-confirm-message></p></div>
    <footer><button class="button" type="button" data-confirm-cancel>取消</button><button class="button danger-confirm" type="button" data-confirm-accept>确认继续</button></footer>
  </div>
</dialog>
<script src="/assets/agent-config.js?v=` + agentConfigJSCacheVersion + `" defer></script>
</body></html>
{{end}}

{{define "dashboard-page"}}
  <section class="dashboard-head" id="summary"><h2>运行总览</h2>{{if not .Overview.Agents}}<span class="trust-badge inactive"><i></i>等待节点接入</span>{{else if eq .Overview.AgentsOnline .Overview.Agents}}<span class="trust-badge"><i></i>全部在线</span>{{else}}<span class="trust-badge warn"><i></i>{{.Overview.AgentsOnline}} / {{.Overview.Agents}} 在线</span>{{end}}</section>
  <nav class="ops-stats" aria-label="运行概览快捷入口"><a href="/agents" aria-label="查看在线节点"><span class="stat-icon green"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/><path d="M8 12h2l1.3-3 2.1 6 1.4-3H17"/></svg></span><div><small>在线节点</small><strong>{{.Overview.AgentsOnline}}<em>/{{.Overview.Agents}}</em></strong></div></a><a href="/configs" aria-label="打开节点实际配置"><span class="stat-icon blue"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h5"/></svg></span><div><small>节点配置</small><strong>{{.Overview.NodeConfigs}}</strong></div></a><a href="/tasks?status=pending" aria-label="查看活动任务"><span class="stat-icon amber"><svg viewBox="0 0 24 24"><path d="M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"/></svg></span><div><small>活动任务</small><strong>{{.Overview.TasksPending}}</strong></div></a><a href="/tasks?status=failed" aria-label="查看失败任务"><span class="stat-icon red"><svg viewBox="0 0 24 24"><path d="M12 3.5 21 20H3zM12 9v5M12 17.5h.01"/></svg></span><div><small>失败任务</small><strong>{{.Overview.TasksFailed}}</strong></div></a></nav>
  <div class="dashboard-columns">
    <section class="workspace-panel fleet-overview" id="fleet"><header><h3>节点</h3><a href="/agents">全部 →</a></header><div class="fleet-overview-list">{{range .Agents}}<a href="/agents?node={{.ID}}"><span class="node-avatar">●</span><span><strong>{{.Name}}</strong><small>{{.OS}} / {{.Arch}}</small><span class="fleet-engines">{{range .Capabilities}}<em class="{{.}}">{{engineName .}}</em>{{end}}</span></span><span class="status-label {{statusClass .Status}}">{{if eq .Status "online"}}在线{{else}}离线{{end}}</span><time>{{heartbeat .LastSeen}}</time><i>›</i></a>{{else}}<div class="empty compact"><strong>还没有节点</strong><p>请先注册节点。</p></div>{{end}}</div></section>
    <section class="workspace-panel recent-tasks" id="activity"><header><h3>最近任务</h3><a href="/tasks">全部 →</a></header><div>{{range $group := taskActivity .Tasks 7}}{{$task := $group.Task}}<a href="/tasks?task={{$task.ID}}#task-{{$task.ID}}"><i class="status-dot {{if and $task.Simulated (eq $task.Status "succeeded")}}warn{{else}}{{statusClass $task.Status}}{{end}}"></i><span><strong>{{actionName $task.Action}}</strong><small>{{engineName $task.Engine}} · {{if and $task.Simulated (eq $task.Status "succeeded")}}模拟完成{{else}}{{short $task.AgentID}}{{end}}{{if gt $group.Count 1}} · 连续 {{$group.Count}} 次{{end}}</small></span><time>{{ago $task.CreatedAt}}</time><b>›</b></a>{{else}}<div class="empty compact"><strong>还没有任务</strong></div>{{end}}</div></section>
  </div>
{{end}}
`
