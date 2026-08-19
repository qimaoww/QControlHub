const app = document.querySelector("#app");
const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const actions = [
  "validate",
  "deploy",
  "start",
  "stop",
  "restart",
  "status",
  "install",
  "read-config",
];
const state = {
  session: null,
  route: location.hash.slice(1) || "dashboard",
  data: {},
  busy: false,
};

const esc = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (char) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        char
      ],
  );
const date = (value) => (value ? new Date(value).toLocaleString() : "-");
const bytes = (value) => {
  const n = Number(value || 0);
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(
    Math.floor(Math.log(n) / Math.log(1024)),
    units.length - 1,
  );
  return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${units[i]}`;
};
const label = (value) => String(value || "").replaceAll("_", " ");
const actionName = (value) =>
  ({
    validate: "校验配置",
    deploy: "部署并重启",
    start: "启动服务",
    stop: "停止服务",
    restart: "重启服务",
    status: "查询状态",
    install: "安装或升级内核",
    "read-config": "读取当前配置",
  })[value] || label(value);
const statusName = (value) =>
  ({
    pending: "准备中",
    running: "执行中",
    succeeded: "成功",
    failed: "失败",
    canceled: "已取消",
  })[value] || label(value);
const engineName = (value) =>
  ({
    mihomo: "Mihomo",
    xray: "Xray",
    "sing-box": "sing-box",
    "ss-rust": "Shadowsocks Rust",
  })[value] || value;
const serviceStatusName = (value) =>
  ({
    active: "运行中",
    inactive: "已停止",
    activating: "启动中",
    deactivating: "停止中",
    failed: "失败",
  })[value] ||
  value ||
  "未知";
const short = (value) => String(value || "").slice(0, 12);
const percent = (used, total) =>
  total > 0
    ? Math.min(100, Math.max(0, (Number(used) / Number(total)) * 100))
    : 0;
const ago = (value) => {
  const elapsed = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(elapsed)) return "未知";
  if (elapsed < 60_000) return "刚刚";
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`;
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`;
  return `${Math.floor(elapsed / 86_400_000)} 天前`;
};
const can = (role) =>
  (({ readonly: 1, operator: 2, admin: 3 })[state.session?.role] || 0) >=
  ({ readonly: 1, operator: 2, admin: 3 }[role] || 0);

async function api(path, options = {}) {
  const headers = {
    Accept: "application/json",
    ...(options.body ? { "Content-Type": "application/json" } : {}),
    ...(options.headers || {}),
  };
  if (
    state.session?.csrf_token &&
    options.method &&
    !["GET", "HEAD", "OPTIONS"].includes(options.method)
  )
    headers["X-QControlHub-CSRF"] = state.session.csrf_token;
  const response = await fetch(`/api/v1${path}`, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  if (response.status === 401) {
    state.session = null;
    renderLogin();
    throw new Error("登录已失效");
  }
  if (!response.ok) {
    let body = {};
    try {
      body = await response.json();
    } catch {}
    const error = new Error(body.error || `请求失败 (${response.status})`);
    error.status = response.status;
    throw error;
  }
  if (response.status === 204) return null;
  return response.json();
}

async function optionalAPI(path) {
  try {
    return await api(path);
  } catch (error) {
    if (error.status === 404) return null;
    throw error;
  }
}

async function ensureSession() {
  try {
    state.session = await api("/auth/session");
    return true;
  } catch {
    state.session = null;
    return false;
  }
}

function renderLogin(message = "") {
  document.body.className = "login-body";
  app.innerHTML = `<main class="login-shell compact-login"><section class="login-card"><a class="brand login-card-brand" href="#dashboard"><span class="brand-mark large">QH</span><strong>QControlHub</strong></a><div class="login-card-head"><h1>登录</h1></div>${message ? `<div class="alert error">${esc(message)}</div>` : ""}<form id="login-form" class="stack-form"><input class="visually-hidden" type="text" name="username" value="admin" autocomplete="username" tabindex="-1" aria-hidden="true"><label>管理令牌<input name="token" type="password" autocomplete="current-password" autofocus required minlength="32"></label><button class="button primary" type="submit">登录</button></form></section></main>`;
  document
    .querySelector("#login-form")
    .addEventListener("submit", async (event) => {
      event.preventDefault();
      const token = new FormData(event.currentTarget).get("token");
      const button = event.currentTarget.querySelector("button");
      button.disabled = true;
      try {
        state.session = await api("/auth/login", {
          method: "POST",
          body: JSON.stringify({ token }),
        });
        location.hash = "#dashboard";
        await render();
      } catch (error) {
        renderLogin(error.message);
      }
    });
}

function shell(content, title) {
  const links = [
    ["dashboard", "总览", "M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z"],
    [
      "agents",
      "节点",
      "M4 5h16v5H4zM4 14h16v5H4zM8 7.5h.01M8 16.5h.01M12 7.5h5M12 16.5h5",
    ],
    [
      "client-access",
      "客户端",
      "M7 4.5h10a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-11a2 2 0 0 1 2-2ZM8.5 8.5h7M8.5 12h7M8.5 15.5h3",
    ],
    ["live-config", "配置", "M7 3.5h7l4 4v13H7zM14 3.5v4h4M10 12h5M10 16h5"],
    ["tasks", "任务", "M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"],
    [
      "settings",
      "设置",
      "M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6 7 7M17 17l1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8",
    ],
  ];
  document.body.className = `app-body page-${state.route}`;
  document.documentElement.dataset.theme =
    localStorage.getItem("qcontrolhub-theme") || "light";
  const context = contextMarkup(title);
  const overview = state.data.overview || {};
  const panelName = state.data.settings?.panel_name || "QControlHub";
  const roleName = { admin: "管理员", operator: "操作员", readonly: "只读" }[
    state.session.role
  ];
  const topAction =
    state.route === "dashboard"
      ? '<a class="button small" href="#agents">管理节点</a>'
      : state.route === "agents"
        ? `<a class="button small" href="#client-access">客户端配置</a>${can("admin") ? '<a class="button small" href="#enrollment">添加节点</a>' : ""}`
        : state.route === "client-access"
          ? '<a class="button small" href="#agents">返回节点</a>'
          : state.route === "live-config"
            ? '<a class="button small" href="#archive-config">配置档案</a>'
            : state.route === "archive-config"
              ? '<a class="button small" href="#live-config">节点实际配置</a>'
              : state.route === "agent-config"
                ? '<a class="button small" href="#agents">返回节点</a>'
                : "";
  document.title = `${title} · ${panelName}`;
  app.innerHTML = `<div class="desktop-app"><aside class="app-dock"><a class="dock-logo" href="#dashboard" aria-label="${esc(panelName)} 总览"><span>QH</span></a><nav class="dock-nav" aria-label="主导航">${links.map(([id, text, path]) => `<a class="${state.route === id || (state.route === "agent-config" && id === "agents") || (state.route === "archive-config" && id === "live-config") ? "active" : ""}" href="#${id}" title="${text}"><svg viewBox="0 0 24 24"><path d="${path}"/></svg><span class="dock-label">${text}</span>${id === "agents" && overview.agents_online ? `<b>${overview.agents_online}</b>` : ""}${id === "live-config" && overview.node_configs ? `<b>${overview.node_configs}</b>` : ""}${id === "tasks" && overview.tasks_pending ? `<b class="hot">${overview.tasks_pending}</b>` : ""}</a>`).join("")}</nav><div class="dock-tools"><button id="theme-toggle" type="button" aria-label="切换颜色主题" title="切换主题"><svg viewBox="0 0 24 24"><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6 7 7M17 17l1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4"/><circle cx="12" cy="12" r="4"/></svg><span class="dock-label">主题</span></button><button id="logout" type="button" aria-label="退出登录" title="退出登录"><svg viewBox="0 0 24 24"><path d="M10 4H5v16h5M14 8l4 4-4 4M8 12h10"/></svg><span class="dock-label">退出</span></button></div></aside><aside class="context-sidebar"><header class="context-brand"><a href="#dashboard"><span class="brand-mark">QH</span><strong>${esc(panelName)}</strong></a></header>${context}</aside><section class="workspace-shell"><header class="workspace-topbar"><div class="workspace-route"><span>${esc(panelName)}</span><i>/</i><b>${esc(title)}</b><i class="role-badge role-${esc(state.session.role)}">${esc(roleName)}</i></div><div class="workspace-actions"><span class="sync-state ${overview.agents_online ? "" : "inactive"}"><i></i><span>${overview.agents_online ? `${overview.agents_online} 个节点在线` : "等待节点连接"}</span></span>${topAction}<button id="refresh" class="button small" type="button">刷新</button></div></header><main class="workspace-main">${content}</main></section></div>`;
  document
    .querySelector(".workspace-actions")
    ?.insertAdjacentHTML(
      "beforeend",
      '<details class="mobile-account-menu"><summary aria-label="打开账户菜单">•••</summary><div><button type="button" id="mobile-theme-toggle">切换主题</button><button type="button" id="mobile-logout">退出登录</button></div></details>',
    );
  document.querySelector("#logout").onclick = async () => {
    try {
      await api("/auth/logout", { method: "POST" });
    } catch {}
    state.session = null;
    renderLogin();
  };
  document.querySelector("#refresh").onclick = () => render();
  document.querySelector("#theme-toggle").onclick = () => {
    const next =
      document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    localStorage.setItem("qcontrolhub-theme", next);
  };
  document.querySelector("#mobile-theme-toggle").onclick =
    document.querySelector("#theme-toggle").onclick;
  document.querySelector("#mobile-logout").onclick =
    document.querySelector("#logout").onclick;
}

function contextMarkup(title) {
  if (state.route === "dashboard")
    return `<nav class="context-menu" aria-label="总览目录"><a class="active" href="#summary"><span>01</span>运行概览</a><a href="#fleet"><span>02</span>节点状态</a><a href="#activity"><span>03</span>最近活动</a></nav><section class="context-metrics"><div><span>在线 / 全部节点</span><b>${state.data.overview?.agents_online || 0} / ${state.data.overview?.agents || 0}</b></div><div><span>节点版本 / 独立档案</span><b>${state.data.overview?.node_configs || 0} / ${state.data.overview?.configs || 0}</b></div><div><span>准备中 / 执行中</span><b>${state.data.overview?.tasks_queued || 0} / ${state.data.overview?.tasks_running || 0}</b></div></section>`;
  if (state.route === "agents") {
    const items = state.data.agents || [];
    return `<a class="context-primary" href="#client-access">客户端配置 →</a><div class="context-section-label"><span>已接入节点</span><b>${items.length}</b></div><nav class="context-list" aria-label="节点列表">${items.map((agent) => `<a class="${state.data.selectedAgent === agent.id ? "active" : ""}" href="#node-${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "client-access") {
    const items = state.data.agents || [];
    return `<a class="context-back" href="#agents">← 返回节点</a><a class="context-primary ${state.data.accessAgent ? "" : "active"}" href="#client-access" data-access-agent="">全部客户端配置</a><div class="context-section-label"><span>按节点查看</span><b>${items.length}</b></div><nav class="context-list" aria-label="客户端配置节点">${items.map((agent) => `<a class="${state.data.accessAgent === agent.id ? "active" : ""}" href="#client-access" data-access-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${(agent.capabilities || []).length} 个支持内核</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "live-config") {
    const items = state.data.agents || [];
    const selected = items.find((agent) => agent.id === state.data.liveAgent);
    const capabilities = selected?.capabilities || [];
    return `<div class="context-section-label"><span>选择节点</span><b>${items.length}</b></div><nav class="context-list" aria-label="配置节点">${items.map((agent) => `<a class="${agent.id === state.data.liveAgent ? "active" : ""}" href="#live-config" data-live-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${(agent.capabilities || []).length} 个支持内核</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`).join("") || "<p>还没有节点</p>"}</nav>${selected ? `<div class="context-section-label"><span>选择内核</span><b>${capabilities.length}</b></div><nav class="context-list config-context-list">${capabilities.map((engine) => `<a class="${engine === state.data.liveEngine ? "active" : ""}" href="#live-config" data-live-engine="${esc(engine)}"><span class="context-engine ${esc(engine)}">${esc(engineName(engine))}</span><span><strong>${esc(engineName(engine))}</strong><small>节点实际文件</small></span></a>`).join("")}</nav>` : ""}<a class="context-primary" href="#archive-config">配置档案 →</a>`;
  }
  if (state.route === "archive-config") {
    const items = state.data.configs || [];
    return `${can("operator") ? '<a class="context-primary" href="#new-config">＋ 新建配置档案</a>' : ""}<div class="context-section-label"><span>配置档案</span><b>${items.length}</b></div><nav class="context-list config-context-list">${items.map((item) => `<a href="#config-${esc(item.id)}"><span class="context-engine ${esc(item.engine)}">${esc(engineName(item.engine))}</span><span><strong>${esc(item.name)}</strong><small>v${item.version}</small></span></a>`).join("") || "<p>还没有保存的配置</p>"}</nav><a class="context-primary" href="#live-config">← 节点实际配置</a>`;
  }
  if (state.route === "tasks")
    return `<nav class="context-menu task-context-menu" aria-label="任务状态">${[
      ["", "全部任务"],
      ["pending", "准备中"],
      ["running", "执行中"],
      ["succeeded", "成功"],
      ["failed", "失败"],
      ["canceled", "已取消"],
    ]
      .map(
        ([status, text]) =>
          `<a class="${(state.data.taskFilters?.status || "") === status ? "active" : ""}" href="#tasks" data-task-status-filter="${status}">${text}</a>`,
      )
      .join("")}</nav>`;
  if (state.route === "settings")
    return `<nav class="context-menu" aria-label="设置目录"><a class="active" href="#identity"><span>01</span>面板标识</a><a href="#defaults"><span>02</span>操作默认值</a><a href="#synchronization"><span>03</span>状态同步</a><a href="#notifications"><span>04</span>事件通知</a><a href="#audit"><span>05</span>最近操作</a></nav>`;
  const agent = (state.data.agents || []).find(
    (item) => item.id === state.data.agentId,
  );
  const caps = agent?.capabilities || engines;
  return `<a class="context-back" href="#agents">← 返回节点</a><div class="context-section-label"><span>选择内核</span><b>${caps.length}</b></div><nav class="context-list engine-context-list">${caps.map((engine) => `<a class="${state.data.engine === engine ? "active" : ""}" href="#agent-config" data-engine-select="${esc(engine)}"><span class="context-engine ${esc(engine)}">${esc(engineName(engine))}</span><span><strong>${esc(engineName(engine))}</strong><small>服务端入站</small></span></a>`).join("")}</nav><ol class="context-steps"><li class="active"><b>1</b><span>选择入站</span></li><li><b>2</b><span>编辑参数</span></li><li><b>3</b><span>校验或部署</span></li></ol>`;
}

async function dashboard() {
  const [overview, agents, tasks] = await Promise.all([
    api("/overview"),
    api("/agents"),
    api("/tasks?limit=7"),
  ]);
  state.data.overview = overview;
  state.data.agents = agents;
  const fleet =
    agents
      .slice(0, 7)
      .map(
        (agent) =>
          `<a href="#agents"><span class="node-avatar">●</span><span><strong>${esc(agent.name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)}</small><span class="fleet-engines">${(agent.capabilities || []).map((engine) => `<em class="${esc(engine)}">${esc(engine)}</em>`).join("")}</span></span><span class="status-label ${agent.status === "online" ? "ok" : "muted"}">${agent.status === "online" ? "在线" : "离线"}</span><time>${date(agent.last_seen)}</time><i>›</i></a>`,
      )
      .join("") ||
    '<div class="empty compact"><strong>还没有节点</strong><p>请先注册节点。</p></div>';
  const activity =
    tasks
      .map(
        (task) =>
          `<a href="#tasks"><i class="status-dot ${task.status === "succeeded" ? "ok" : task.status === "failed" ? "bad" : task.status === "running" ? "warn" : ""}"></i><span><strong>${esc(actionName(task.action))}</strong><small>${esc(engineName(task.engine))} · ${esc(task.agent_id)}</small></span><time>${date(task.created_at)}</time><b>›</b></a>`,
      )
      .join("") ||
    '<div class="empty compact"><strong>还没有任务</strong></div>';
  shell(
    `<section class="dashboard-head" id="summary"><h2>运行总览</h2><span class="trust-badge ${overview.agents && overview.agents_online === overview.agents ? "" : "warn"}"><i></i>${overview.agents ? `${overview.agents_online} / ${overview.agents} 在线` : "等待节点接入"}</span></section><nav class="ops-stats" aria-label="运行概览快捷入口"><a href="#agents"><span class="stat-icon green">●</span><div><small>在线节点</small><strong>${overview.agents_online}<em>/${overview.agents}</em></strong></div></a><a href="#live-config"><span class="stat-icon blue">□</span><div><small>节点配置</small><strong>${overview.node_configs}</strong></div></a><a href="#tasks"><span class="stat-icon amber">⚡</span><div><small>活动任务</small><strong>${overview.tasks_pending}</strong></div></a><a href="#tasks"><span class="stat-icon red">!</span><div><small>失败任务</small><strong>${overview.tasks_failed}</strong></div></a></nav><div class="dashboard-columns"><section class="workspace-panel fleet-overview" id="fleet"><header><h3>节点</h3><a href="#agents">全部 →</a></header><div class="fleet-overview-list">${fleet}</div></section><section class="workspace-panel recent-tasks" id="activity"><header><h3>最近任务</h3><a href="#tasks">全部 →</a></header><div>${activity}</div></section></div>`,
    "总览",
  );
}

async function agents() {
  const [agents, deployments, accessEntries, settings, tokens] =
    await Promise.all([
      api("/agents"),
      api("/deployments"),
      api("/client-access"),
      api("/settings"),
      can("admin") ? api("/enrollment-tokens") : Promise.resolve([]),
    ]);
  state.data.agents = agents;
  state.data.settings = settings;
  state.data.selectedAgent ||= agents[0]?.id || "";
  const overview = await api("/overview");
  state.data.overview = overview;

  const savedConfigs = (
    await Promise.all(
      agents.map((agent) =>
        api(`/agents/${encodeURIComponent(agent.id)}/configs`),
      ),
    )
  ).flat();
  const configByService = new Map(
    savedConfigs.map((config) => [
      `${config.agent_id}|${config.engine}`,
      config,
    ]),
  );
  const deploymentByService = new Map(
    deployments.map((item) => [`${item.agent_id}|${item.engine}`, item]),
  );
  const accessByService = new Map(
    accessEntries.map((item) => [`${item.agent_id}|${item.engine}`, item]),
  );

  const tokenRows =
    tokens
      .map(
        (token) =>
          `<article><div><strong>${esc(token.name)}</strong><small>${token.revoked_at ? "已吊销" : token.used_count >= token.max_uses ? "已使用" : `有效至 ${date(token.expires_at)}`} · 已用 ${token.used_count}/${token.max_uses} 次</small></div>${!token.revoked_at && token.used_count < token.max_uses ? `<button type="button" data-revoke="${esc(token.id)}">吊销</button>` : ""}</article>`,
      )
      .join("") || "";

  const nodeCards = agents
    .map((agent) => {
      const metrics = agent.metrics || {};
      const services = (agent.capabilities || [])
        .map((engine) => {
          const key = `${agent.id}|${engine}`;
          const runtime = agent.runtime?.[engine] || {};
          const deployed = deploymentByService.get(key);
          const saved = configByService.get(key);
          const access = accessByService.get(key);
          const drift =
            saved &&
            (!deployed ||
              deployed.config_id !== saved.id ||
              deployed.config_version < saved.version);
          const firstProfile = access?.profiles?.[0];
          const port = firstProfile?.profile?.fields?.find(
            (field) => field.label === "端口",
          )?.value;
          const endpoint =
            access && port
              ? `${access.address}:${port}`
              : access?.address || "";
          return `<article class="service-card service-${esc(engine)}">
            <div class="service-card-main">
              <div class="service-overview"><header><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${runtime.service_status === "active" ? "ok" : runtime.service_status === "failed" ? "bad" : ""}"><i></i><b>${esc(serviceStatusName(runtime.service_status))}</b></span></header><div class="service-version"><span class="service-version-label"><small>已安装版本</small><button class="service-version-toggle" type="button" data-open-version-form>切换版本</button></span><strong title="${esc(runtime.version || "未检测到二进制")}">${esc(runtime.installed ? runtime.version : "未检测到二进制")}</strong></div></div>
              <div class="service-deployment"><dl class="service-facts"><div><dt>已部署配置</dt><dd>${deployed?.config_version ? `v${deployed.config_version}` : "—"}</dd></div><div><dt>已保存配置</dt><dd>${saved?.version ? `v${saved.version}` : "—"}</dd></div></dl>${drift ? `<div class="deployment-drift"><span>${deployed ? "已保存版本尚未部署" : "已保存配置尚未部署"}</span><b>待部署 v${saved.version}</b></div>` : ""}<div class="service-endpoint ${endpoint ? "" : "empty"}">${endpoint ? `<span><b>${esc(firstProfile?.protocol || "客户端入站")}</b><small>${esc(firstProfile?.profile?.format || "已部署配置")}</small></span><code>${esc(endpoint)}</code>` : `<b>${deployed ? "自定义配置" : saved ? "尚未部署" : "尚未配置"}</b>`}</div></div>
              <div class="service-primary-action">${drift ? `<button class="button service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">查看配置</button><button class="button primary" type="button" data-deploy="${esc(agent.id)}" data-engine="${esc(engine)}" data-config-id="${esc(saved.id)}">部署 v${saved.version}</button>` : `<button class="button primary service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">配置 <span>→</span></button>`}</div>
            </div>
            <details class="runtime-drawer"><summary><span><b>管理服务</b></span><i>＋</i></summary><div class="runtime-drawer-body"><div class="service-actions">${["status", "start", "restart", "stop"].map((action) => `<button class="button small ${action === "stop" ? "danger-button" : ""}" type="button" data-task-agent="${esc(agent.id)}" data-task-engine="${esc(engine)}" data-task-action="${action}" ${agent.status !== "online" || !can("operator") ? "disabled" : ""}>${esc(actionName(action))}</button>`).join("")}</div></div></details>
            <details class="runtime-drawer version-drawer"><summary><span><b>版本切换</b><small>安装或切换内核版本</small></span><i>＋</i></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset><label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" ${agent.status !== "online" || !can("operator") ? "disabled" : ""}>${runtime.installed ? "升级或切换版本" : "安装内核"}</button><small>${runtime.installed ? "官方 Release · SHA-256 校验" : "首次安装前需准备安全目录与 systemd 单元"}</small></form></div></details>
            ${access?.profiles?.length ? `<a class="service-client-access" href="#client-access" data-client-agent="${esc(agent.id)}" data-client-engine="${esc(engine)}"><span><b>客户端配置</b><small>${esc(access.source)} · ${esc(access.address)}</small></span><strong>${access.profiles.length} 个入站 <i>→</i></strong></a>` : ""}
          </article>`;
        })
        .join("");
      const labels = Object.entries(agent.labels || {})
        .map(([key, value]) => `<span>${esc(key)}=${esc(value)}</span>`)
        .join("");
      return `<details class="machine-workspace" id="node-${esc(agent.id)}" name="node-workspace" ${agent.id === state.data.selectedAgent ? "open" : ""}><summary class="machine-header"><div class="machine-identity">${can("operator") ? `<label class="batch-select" title="选择此节点参与批量操作"><input type="checkbox" data-batch-checkbox value="${esc(agent.id)}"><span></span></label>` : ""}<span class="machine-avatar">●</span><span><strong>${esc(agent.name)}</strong><code>${esc(agent.os)} / ${esc(agent.arch)} · ${esc(short(agent.id))}</code></span></div><section class="machine-resource-summary" aria-label="资源监控"><div><span>CPU</span><strong>${metrics.cpu_available ? `${Number(metrics.cpu_percent).toFixed(1)}%` : "等待采集"}</strong><progress max="100" value="${metrics.cpu_available ? Number(metrics.cpu_percent) : 0}"></progress></div><div><span>内存</span><strong>${metrics.memory_available ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}` : "等待采集"}</strong><progress max="100" value="${percent(metrics.memory_used_bytes, metrics.memory_total_bytes)}"></progress></div><div><span>磁盘</span><strong>${metrics.disk_available ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}` : "等待采集"}</strong><progress max="100" value="${percent(metrics.disk_used_bytes, metrics.disk_total_bytes)}"></progress></div><div class="machine-resource-network"><span>网络</span><strong>↓ ${metrics.network_available ? `${bytes(metrics.network_rx_bps)}/s` : "等待采集"} · ↑ ${metrics.network_available ? `${bytes(metrics.network_tx_bps)}/s` : "等待采集"}</strong><small>累计 ↓ ${metrics.network_available ? bytes(metrics.network_rx_bytes) : "—"} · ↑ ${metrics.network_available ? bytes(metrics.network_tx_bytes) : "—"}</small></div><span class="machine-resource-live"></span></section><div class="machine-state"><span class="status-dot ${agent.status === "online" ? "ok" : ""}"></span><span><b>${agent.status === "online" ? "在线" : "离线"}</b><small>${ago(agent.last_seen)}</small></span></div><i class="machine-chevron" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m7 10 5 5 5-5"/></svg></i></summary><div class="machine-body"><section class="service-canvas"><header class="service-canvas-head"><h2>节点内核</h2><span>${(agent.capabilities || []).length} 个内核</span></header><div class="service-grid">${services}</div></section><details class="machine-profile node-inspector"><summary class="node-inspector-summary"><span><b>节点身份</b><small>身份信息 · 指标趋势</small></span><i>＋</i></summary><div class="node-inspector-body"><section class="node-identity-panel"><h2>${esc(agent.name)}</h2><dl class="identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>Agent 版本</dt><dd>${esc(agent.version || "未知")}</dd></div><div><dt>注册时间</dt><dd>${date(agent.enrolled_at)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl>${labels ? `<div class="labels">${labels}</div>` : ""}<footer class="node-identity-refresh"><span>${metrics.collected_at ? ago(metrics.collected_at) : "等待资源数据"}</span><button type="button" data-agent-refresh>刷新</button></footer></section><section class="metric-trend-empty"><span>⌁</span><b>指标趋势</b><small>打开节点详情后可按需加载最近 24 小时的上下行速率。</small></section></div></details><footer class="machine-footer"><span><i>●</i><b>节点身份已验证</b></span>${can("admin") ? `<details><summary>节点管理</summary><button type="button" data-delete="${esc(agent.id)}">移除节点并吊销身份</button></details>` : ""}</footer></div></details>`;
    })
    .join("");

  const enrollment = can("admin")
    ? `<details class="enrollment-sheet" id="enrollment" data-has-agents="${agents.length ? 1 : 0}" ${agents.length ? "" : "open"}><summary><b>＋ 添加节点</b><i>＋</i></summary><div class="enrollment-sheet-body"><form class="access-form" id="enrollment-form"><label>节点名称<input name="name" maxlength="100" required autocomplete="off" placeholder="例如 shanghai-edge-01"></label><label>命令有效期<select name="ttl_minutes">${[10, 15, 30, 60].map((value) => `<option value="${value}" ${value === settings.enrollment_ttl_minutes ? "selected" : ""}>${value === 60 ? "1 小时" : `${value} 分钟`}</option>`).join("")}</select></label><button class="button primary" type="submit">生成一键部署命令</button></form><p class="enrollment-security-note"><b>命令只显示一次</b><span>命令包含短期单次注册码，请只在目标 Linux 服务器上执行。</span></p>${tokenRows ? `<details class="access-history"><summary>添加记录（${tokens.length}）</summary><div>${tokenRows}</div></details>` : ""}</div></details>`
    : "";
  const batch =
    agents.length && can("operator")
      ? `<form class="batch-toolbar" id="batch-form"><span class="batch-toolbar-title">批量操作</span><label>内核<select name="engine">${engines.map((engine) => `<option value="${engine}">${esc(engineName(engine))}</option>`).join("")}</select></label><label>动作<select name="action"><option value="restart">重启服务</option><option value="status">查询状态</option><option value="start">启动服务</option><option value="stop">停止服务</option></select></label><button class="button small" type="submit" disabled>执行</button><small data-batch-count>未选择节点</small></form>`
      : "";
  shell(
    `${enrollment}${batch}${nodeCards ? `<section class="machine-stack">${nodeCards}</section>` : '<div class="empty large"><strong>还没有节点</strong><p>请先添加节点。</p></div>'}`,
    "节点",
  );
  bindAgentPage();
}

function bindAgentPage() {
  document.querySelectorAll(".machine-workspace").forEach((item) => {
    item.addEventListener("toggle", () => {
      if (item.open) state.data.selectedAgent = item.id.replace(/^node-/, "");
    });
  });
  document.querySelectorAll("[data-config]").forEach((button) => {
    button.onclick = () => {
      state.data.agentId = button.dataset.config;
      state.data.engine = button.dataset.engine;
      location.hash = "#agent-config";
    };
  });
  document.querySelectorAll("[data-client-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.accessAgent = link.dataset.clientAgent;
      state.data.accessEngine = link.dataset.clientEngine;
    };
  });
  document.querySelectorAll("[data-task-action]").forEach((button) => {
    button.onclick = () =>
      submitTask({
        agent_id: button.dataset.taskAgent,
        engine: button.dataset.taskEngine,
        action: button.dataset.taskAction,
      });
  });
  document.querySelectorAll("[data-deploy]").forEach((button) => {
    button.onclick = () =>
      submitTask({
        agent_id: button.dataset.deploy,
        engine: button.dataset.engine,
        action: "deploy",
        config_id: button.dataset.configId,
      });
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    form.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(form);
      const channel = values.get("release_channel");
      const version =
        channel === "custom" ? values.get("custom_version") : channel;
      await submitTask({
        agent_id: form.dataset.versionAgent,
        engine: form.dataset.versionEngine,
        action: "install",
        core_version: version,
      });
    };
  });
  document.querySelectorAll("[data-open-version-form]").forEach((button) => {
    button.onclick = () => {
      const drawer = button
        .closest(".service-card")
        ?.querySelector(".version-drawer");
      if (drawer) drawer.open = true;
    };
  });
  document.querySelectorAll("[data-delete]").forEach((button) => {
    button.onclick = async () => {
      if (!confirm("确定移除此节点并永久吊销其身份？")) return;
      await api(`/agents/${encodeURIComponent(button.dataset.delete)}`, {
        method: "DELETE",
      });
      await agents();
    };
  });
  document.querySelectorAll("[data-revoke]").forEach((button) => {
    button.onclick = async () => {
      if (!confirm("确定吊销这个添加命令？")) return;
      await api(
        `/enrollment-tokens/${encodeURIComponent(button.dataset.revoke)}`,
        {
          method: "DELETE",
        },
      );
      await agents();
    };
  });
  const batchForm = document.querySelector("#batch-form");
  const updateBatch = () => {
    const count = document.querySelectorAll(
      "[data-batch-checkbox]:checked",
    ).length;
    const button = batchForm?.querySelector("button[type=submit]");
    if (button) button.disabled = count === 0;
    const label = batchForm?.querySelector("[data-batch-count]");
    if (label)
      label.textContent = count ? `已选择 ${count} 个节点` : "未选择节点";
  };
  document
    .querySelectorAll("[data-batch-checkbox]")
    .forEach((input) => (input.onchange = updateBatch));
  if (batchForm)
    batchForm.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(batchForm);
      const selected = [
        ...document.querySelectorAll("[data-batch-checkbox]:checked"),
      ];
      await Promise.all(
        selected.map((input) =>
          api("/tasks", {
            method: "POST",
            body: JSON.stringify({
              agent_id: input.value,
              engine: values.get("engine"),
              action: values.get("action"),
            }),
          }),
        ),
      );
      alert(`已提交 ${selected.length} 个任务`);
      location.hash = "#tasks";
    };
  const enrollmentForm = document.querySelector("#enrollment-form");
  if (enrollmentForm)
    enrollmentForm.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(enrollmentForm);
      const name = String(values.get("name") || "").trim();
      const created = await api("/enrollment-tokens", {
        method: "POST",
        body: JSON.stringify({
          name,
          ttl_minutes: Number(values.get("ttl_minutes")),
          max_uses: 1,
        }),
      });
      const command = `curl -fsSL https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/deploy/remote/install-agent.sh | sudo bash -s -- ${location.origin} '${created.token}' '${name.replaceAll("'", "'\\''")}'`;
      showCommand(command);
    };
}

async function submitTask(payload) {
  try {
    await api("/tasks", { method: "POST", body: JSON.stringify(payload) });
    alert("任务已提交");
    return true;
  } catch (error) {
    alert(error.message);
    return false;
  }
}

function showCommand(command) {
  const wrap = document.createElement("div");
  wrap.className = "modal-backdrop";
  wrap.innerHTML = `<section class="modal"><div class="section-head"><div><p class="eyebrow">Linux · systemd</p><h2>一键部署 Agent</h2></div><button class="icon-button" type="button" data-close>×</button></div><p class="enrollment-security-note"><b>仅显示一次</b><span>在目标 Linux 服务器以 root 权限执行。</span></p><textarea class="code-editor-input" rows="7" readonly data-command>${esc(command)}</textarea><div class="form-actions"><button class="button" type="button" data-close>关闭</button><button class="button primary" type="button" data-copy-command>复制命令</button></div></section>`;
  document.body.append(wrap);
  wrap
    .querySelectorAll("[data-close]")
    .forEach((button) => (button.onclick = () => wrap.remove()));
  wrap.querySelector("[data-copy-command]").onclick = async () => {
    await navigator.clipboard.writeText(command);
    wrap.querySelector("[data-copy-command]").textContent = "已复制";
  };
}

async function clientAccess() {
  const [entries, agents, overview, settings] = await Promise.all([
    api("/client-access"),
    api("/agents"),
    api("/overview"),
    api("/settings"),
  ]);
  state.data.agents = agents;
  state.data.overview = overview;
  state.data.settings = settings;
  const selectedAgent = state.data.accessAgent || "";
  const selectedEngine = state.data.accessEngine || "";
  const query = String(state.data.accessQuery || "")
    .trim()
    .toLowerCase();
  const filtered = entries.filter((entry) => {
    if (selectedAgent && entry.agent_id !== selectedAgent) return false;
    if (selectedEngine && entry.engine !== selectedEngine) return false;
    if (!query) return true;
    return [
      entry.agent_name,
      entry.agent_id,
      entry.engine,
      entry.address,
      entry.source,
      ...(entry.profiles || []).flatMap((profile) => [
        profile.tag,
        profile.protocol,
        profile.profile?.format,
      ]),
    ]
      .join(" ")
      .toLowerCase()
      .includes(query);
  });
  const totalProfiles = filtered.reduce(
    (total, entry) => total + (entry.profiles || []).length,
    0,
  );
  const totalNodes = new Set(filtered.map((entry) => entry.agent_id)).size;
  const results =
    filtered
      .map((entry, entryIndex) => {
        const agent = agents.find((item) => item.id === entry.agent_id) || {};
        const profiles = (entry.profiles || [])
          .map((item, profileIndex) => {
            const inputID = `client-share-${entryIndex}-${profileIndex}`;
            const fields = (item.profile?.fields || [])
              .map(
                (field, fieldIndex) =>
                  `<div><span>${esc(field.label)}</span>${field.secret ? `<form class="secret-value-control" action="#"><input id="client-field-${entryIndex}-${profileIndex}-${fieldIndex}" type="password" readonly autocomplete="off" spellcheck="false" value="${esc(field.value)}"><button type="button" data-secret-visibility>显示</button><button type="button" data-copy-target="#client-field-${entryIndex}-${profileIndex}-${fieldIndex}">复制</button></form>` : `<code title="${esc(field.value)}">${esc(field.value)}</code>`}</div>`,
              )
              .join("");
            return `<article class="client-profile-card"><header><span><b>${esc(item.protocol)}</b><small>${esc(item.tag)} · ${esc(item.profile?.format)}</small></span><span class="status-label warn">含凭据</span></header><form class="secret-value-control client-share-control" action="#"><input id="${inputID}" type="password" readonly autocomplete="off" spellcheck="false" value="${esc(item.profile?.uri)}"><button type="button" data-secret-visibility>显示</button><button type="button" data-copy-target="#${inputID}">复制</button></form><details class="client-parameter-menu"><summary>逐项参数 <i>展开</i></summary><div class="client-parameters">${fields}</div></details></article>`;
          })
          .join("");
        return `<article class="client-access-entry"><header class="client-access-entry-head"><div class="client-access-node"><span class="node-avatar">●</span><span><strong>${esc(entry.agent_name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)} · ${esc(short(entry.agent_id))}</small></span></div><div class="client-access-engine"><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="status-label ${agent.status === "online" ? "ok" : "muted"}">${agent.status === "online" ? "在线" : "离线"}</span></div></header><div class="client-access-entry-meta"><span><small>连接地址</small><code>${esc(entry.address)}</code></span><span><small>地址来源</small><strong>${esc(entry.source)}</strong></span><a href="#agent-config" data-config-agent="${esc(entry.agent_id)}" data-config-engine="${esc(entry.engine)}">服务端配置 →</a></div><div class="client-access-profile-grid">${profiles}</div></article>`;
      })
      .join("") ||
    '<section class="client-access-empty-state"><span>⌁</span><h2>没有匹配的客户端配置</h2><p>客户端信息只会从已部署且可解析的入站生成。请调整筛选条件，或先在节点内核中完成配置与部署。</p><a class="button primary" href="#agents">返回节点</a></section>';
  const agentFilters = agents
    .map(
      (agent) =>
        `<button class="${selectedAgent === agent.id ? "active" : ""}" type="button" data-filter-agent="${esc(agent.id)}">${esc(agent.name)}</button>`,
    )
    .join("");
  const engineFilters = engines
    .map(
      (engine) =>
        `<button class="${selectedEngine === engine ? "active" : ""}" type="button" data-filter-engine="${esc(engine)}">${esc(engineName(engine))}</button>`,
    )
    .join("");
  shell(
    `<section class="client-access-workspace"><header class="client-access-hero"><div><p class="eyebrow">Client access</p><h1>客户端配置</h1><p>集中查看已部署入站生成的客户端连接信息。凭据默认隐藏，只在本页按需显示或复制。</p></div><dl class="client-access-summary"><div><dt>可用节点</dt><dd>${totalNodes}</dd></div><div><dt>客户端入站</dt><dd>${totalProfiles}</dd></div></dl></header><section class="client-access-filter-panel" aria-label="客户端配置筛选"><form class="client-access-search" id="client-search"><label><span>搜索入站</span><input type="search" name="q" value="${esc(state.data.accessQuery || "")}" placeholder="节点、地址、协议或入站名称" autocomplete="off"></label><button class="button primary" type="submit">搜索</button>${query ? '<button class="button" type="button" data-clear-search>清除搜索</button>' : ""}</form><div class="client-access-filter-row"><span>节点</span><nav aria-label="按节点筛选"><button class="${selectedAgent ? "" : "active"}" type="button" data-filter-agent="">全部节点</button>${agentFilters}</nav></div><div class="client-access-filter-row"><span>内核</span><nav aria-label="按内核筛选"><button class="${selectedEngine ? "" : "active"}" type="button" data-filter-engine="">全部内核</button>${engineFilters}</nav></div></section><div class="client-access-results-head"><span>当前结果</span><strong>${filtered.length} 组内核配置</strong></div><div class="client-access-entry-grid">${results}</div></section>`,
    "客户端配置",
  );
  bindClientAccessPage();
}

function bindClientAccessPage() {
  document
    .querySelectorAll("[data-filter-agent], [data-access-agent]")
    .forEach((button) => {
      button.onclick = (event) => {
        event.preventDefault();
        state.data.accessAgent =
          button.dataset.filterAgent ?? button.dataset.accessAgent;
        clientAccess();
      };
    });
  document.querySelectorAll("[data-filter-engine]").forEach((button) => {
    button.onclick = () => {
      state.data.accessEngine = button.dataset.filterEngine;
      clientAccess();
    };
  });
  document
    .querySelector("#client-search")
    ?.addEventListener("submit", (event) => {
      event.preventDefault();
      state.data.accessQuery = new FormData(event.currentTarget).get("q");
      clientAccess();
    });
  document
    .querySelector("[data-clear-search]")
    ?.addEventListener("click", () => {
      state.data.accessQuery = "";
      clientAccess();
    });
  document.querySelectorAll("[data-secret-visibility]").forEach((button) => {
    button.onclick = () => {
      const input = button.parentElement.querySelector("input");
      const reveal = input.type === "password";
      input.type = reveal ? "text" : "password";
      button.textContent = reveal ? "隐藏" : "显示";
    };
  });
  document.querySelectorAll("[data-copy-target]").forEach((button) => {
    button.onclick = async () => {
      const input = document.querySelector(button.dataset.copyTarget);
      await navigator.clipboard.writeText(input?.value || "");
      button.textContent = "已复制";
    };
  });
  document.querySelectorAll("[data-config-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.agentId = link.dataset.configAgent;
      state.data.engine = link.dataset.configEngine;
    };
  });
}

async function agentConfig() {
  const agents = state.data.agents || (await api("/agents"));
  state.data.agents = agents;
  const agent = agents.find((item) => item.id === state.data.agentId);
  if (!agent) return void (location.hash = "#agents");
  const engine = state.data.engine || agent.capabilities?.[0] || engines[0];
  state.data.engine = engine;
  const base = `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}`;
  const workspace = await api(`${base}/workspace`);
  const config = workspace.config;
  let selectedInbound = (workspace.inbounds || []).find(
    (input) => input.tag === state.data.inboundTag,
  );
  const selectedProtocolKey =
    state.data.protocol ||
    selectedInbound?.protocol ||
    workspace.protocols[0]?.key;
  const protocol = workspace.protocols.find(
    (item) => item.key === selectedProtocolKey,
  );
  let plan = selectedInbound;
  const planKey = `${agent.id}|${engine}|${selectedProtocolKey}`;
  state.data.serverPlans ||= {};
  if (!plan && can("operator") && protocol) {
    plan = state.data.serverPlans[planKey];
    if (!plan) {
      plan = await api(`${base}/plans`, {
        method: "POST",
        body: JSON.stringify({ protocol: selectedProtocolKey }),
      });
      state.data.serverPlans[planKey] = plan;
    }
  }
  plan ||= {
    protocol: selectedProtocolKey,
    listen: "0.0.0.0",
    port: protocol?.default_port || 443,
    transport: "raw",
  };
  const operation = selectedInbound ? "modify" : "add";
  const fields = workspace.catalog.fields || [];
  const selectedField =
    fields.find((field) => field.key === state.data.configField) || fields[0];
  state.data.configField = selectedField?.key || "";
  let fieldValue = { present: false, fragment: "" };
  if (config && selectedField)
    fieldValue = await api(
      `${base}/fields/${encodeURIComponent(selectedField.key)}`,
    );
  const revisions = config
    ? await api(`/configs/${encodeURIComponent(config.id)}/revisions?limit=50`)
    : [];
  const inboundNav = (workspace.inbounds || [])
    .map(
      (input) =>
        `<a class="${selectedInbound?.tag === input.tag ? "active" : ""}" href="#agent-config" data-inbound="${esc(input.tag)}"><span><strong>${esc(input.tag)}</strong><small>${esc(input.listen)}:${input.port}</small></span></a>`,
    )
    .join("");
  const protocolNav = workspace.protocols
    .map(
      (item) =>
        `<a class="${item.key === selectedProtocolKey ? "active" : ""}" href="#agent-config" data-protocol="${esc(item.key)}"><b>${esc(item.badge)}</b><span><strong>${esc(item.name)}</strong></span></a>`,
    )
    .join("");
  const methods = (protocol?.methods || [])
    .map(
      (method) =>
        `<option value="${esc(method)}" ${method === plan.method ? "selected" : ""}>${esc(method)}</option>`,
    )
    .join("");
  const transports = (protocol?.transports || ["raw"])
    .map(
      (transport) =>
        `<option value="${esc(transport)}" ${transport === plan.transport ? "selected" : ""}>${transport === "raw" ? "Raw / TCP" : transport === "websocket" ? "WebSocket" : "gRPC"}</option>`,
    )
    .join("");
  const security = protocol?.uses_reality
    ? `<input type="hidden" name="reality_enabled" value="1"><section class="builder-section security-section" id="security"><header><span class="section-number">04</span><strong>Reality</strong></header><div><div class="plan-fields two"><label>目标域名 / ServerName<input name="reality_server_name" list="reality-presets" required value="${esc(plan.reality_server_name)}"><datalist id="reality-presets">${workspace.reality_presets.map((value) => `<option value="${esc(value)}">`).join("")}</datalist><small>校验公网 DNS；拒绝 Cloudflare 与非公网地址。</small></label><label>Short ID<input name="reality_short_id" required value="${esc(plan.reality_short_id)}"></label></div><div class="plan-fields one"><label>客户端 Public Key<input name="reality_public_key" required value="${esc(plan.reality_public_key)}"></label><label class="secret-input">服务端 Private Key<span class="secret-value-control"><input type="password" name="reality_private_key" required value="${esc(plan.reality_private_key)}"><button type="button" data-secret-visibility>显示</button></span></label></div></div></section>`
    : protocol?.supports_tls
      ? `<input type="hidden" name="reality_enabled" value="0"><section class="builder-section security-section" id="security"><header><span class="section-number">04</span><strong>TLS</strong></header><div><label class="tls-switch"><input type="checkbox" name="tls_enabled" value="1" ${plan.tls_enabled || protocol.requires_tls ? "checked" : ""} ${protocol.requires_tls ? "disabled" : ""}><strong>${protocol.requires_tls ? "TLS" : "启用 TLS"}</strong></label><div class="plan-fields two"><label>证书路径<input name="certificate_path" value="${esc(plan.certificate_path)}"></label><label>私钥路径<input name="private_key_path" value="${esc(plan.private_key_path)}"></label></div><p class="validation-note">私钥仅目标内核服务组可读。</p></div></section>`
      : '<input type="hidden" name="reality_enabled" value="0"><input type="hidden" name="tls_enabled" value="0">';
  const sourceStudio = config
    ? `<details class="source-studio"><summary>完整源码</summary><form id="source-config-form"><div class="form-grid"><label>配置名称<input name="name" maxlength="100" required value="${esc(config.name)}"></label><label>说明<input name="description" maxlength="300" value="${esc(config.description)}"></label></div><textarea name="content" spellcheck="false" required>${esc(config.content)}</textarea><footer><div><button class="button" type="submit" data-source-intent="validate">保存源码并校验</button><button class="button primary" type="submit" data-source-intent="deploy">保存源码并部署</button></div></footer></form></details>`
    : "";
  const revisionTimeline = config
    ? `<details class="revision-timeline node-revision-timeline" id="revisions"><summary><b>版本历史</b><strong>${revisions.length} 个版本</strong></summary><div class="timeline-body"><nav>${revisions.map((revision) => `<span class="${revision.version === config.version ? "current" : ""}"><i></i><span><b>v${revision.version}</b><strong>${esc(revision.name)}</strong><small>${ago(revision.updated_at)}${revision.version === config.version ? " · 当前" : ""}</small></span></span>`).join("")}</nav><div class="timeline-placeholder">当前 v${config.version}</div></div></details>`
    : "";
  shell(
    `<section class="config-command"><header class="config-command-head"><div><span class="config-command-icon">⌘</span><div><p class="eyebrow">Server recipe</p><h1>${esc(agent.name)} · ${esc(workspace.catalog.name)}</h1><span>${esc(protocol?.name)}${selectedInbound ? ` · ${esc(selectedInbound.tag)}` : " · 新入站"}</span></div></div><div class="config-command-state"><span class="status-label ${config ? "ok" : "warn"}">${config ? "已读取" : "新方案"}</span><span class="recipe-version"><b>${config ? `v${config.version}` : "草稿"}</b><small>${esc(workspace.catalog.format)}</small></span><a href="${esc(protocol?.docs)}" target="_blank" rel="noopener noreferrer">文档 ↗</a></div></header><details class="config-hierarchy-menu" open><summary><b>切换入站 / 协议</b><i>＋</i></summary><div class="config-command-selectors">${inboundNav ? `<section class="inbound-browser config-selector"><header><span><b>入站</b><small>${workspace.inbounds.length} 个</small></span><button class="button small" type="button" data-new-inbound>＋ 新增</button></header><nav>${inboundNav}</nav></section>` : ""}<section class="protocol-browser config-selector"><header><span><b>协议</b><small>${workspace.protocols.length} 种</small></span></header><nav>${protocolNav}</nav></section></div></details></section><article class="recipe-workspace"><form class="server-form" id="server-plan-form"><div class="config-mutation"><label>操作<select name="operation">${selectedInbound ? `<option value="modify">修改 · ${esc(selectedInbound.tag)}</option><option value="add">新增入站</option><option value="delete">删除 · ${esc(selectedInbound.tag)}</option>` : '<option value="add">新增入站</option>'}</select></label></div><div class="builder-layout"><nav class="builder-index"><a href="#listen"><b>01</b><strong>监听</strong></a><a href="#identity"><b>02</b><strong>认证</strong></a>${protocol?.transport_config ? '<a href="#transport"><b>03</b><strong>传输</strong></a>' : ""}${protocol?.uses_reality || protocol?.supports_tls ? '<a href="#security"><b>04</b><strong>安全</strong></a>' : ""}</nav><div class="builder-sections"><section class="builder-section" id="listen"><header><span class="section-number">01</span><strong>监听</strong></header><div class="plan-fields three"><label>入站标签<input name="tag" maxlength="64" required value="${esc(plan.tag)}"></label><label>监听地址<input name="listen" required value="${esc(plan.listen)}"></label><label>监听端口<input type="number" name="port" min="1" max="65535" required value="${Number(plan.port)}"></label></div></section><section class="builder-section" id="identity"><header><span class="section-number">02</span><strong>认证</strong></header><div><div class="plan-fields two">${protocol?.ignores_username ? '<input type="hidden" name="username" value="default">' : `<label>用户名或备注<input name="username" maxlength="64" required value="${esc(plan.username)}"></label>`}<label class="secret-input">${esc(protocol?.credential_label || "凭据")}<span class="secret-value-control"><input type="password" name="credential" required value="${esc(plan.credential)}"><button type="button" data-secret-visibility>显示</button></span></label>${protocol?.secondary_credential_label ? `<label class="secret-input">${esc(protocol.secondary_credential_label)}<span class="secret-value-control"><input type="password" name="secondary_credential" required value="${esc(plan.secondary_credential)}"><button type="button" data-secret-visibility>显示</button></span></label>` : '<input type="hidden" name="secondary_credential" value="">'}</div>${methods ? `<div class="plan-fields one"><label>加密方式<select name="method">${methods}</select></label></div>` : '<input type="hidden" name="method" value="">'}</div></section>${protocol?.transport_config ? `<section class="builder-section" id="transport"><header><span class="section-number">03</span><strong>传输</strong></header><div class="plan-fields two"><label>传输<select name="transport">${transports}</select></label><label>路径 / ServiceName<input name="transport_path" value="${esc(plan.transport_path)}"></label></div></section>` : '<input type="hidden" name="transport" value="raw"><input type="hidden" name="transport_path" value="">'}${security}</div></div><footer class="builder-actions compact"><div><button class="button" type="button" data-regenerate>重新生成参数</button><button class="button" type="submit" data-plan-intent="validate" ${agent.status !== "online" ? "disabled" : ""}>保存并校验</button><button class="button primary" type="submit" data-plan-intent="deploy" ${agent.status !== "online" ? "disabled" : ""}>保存并部署</button></div></footer></form></article>${revisionTimeline}<details class="advanced-studio" id="advanced"><summary><b>全局字段与源码</b><i>＋</i></summary><div class="advanced-studio-body"><nav class="field-rail"><header><b>全局配置项</b><small>${fields.length}</small></header>${fields.map((field) => `<a class="${field.key === selectedField?.key ? "active" : ""}" href="#agent-config" data-config-field="${esc(field.key)}"><i class="${workspace.present_fields[field.key] ? "present" : ""}"></i><span><strong>${esc(field.label)}</strong><code>${esc(field.key)}</code></span><small>${esc(field.kind)}</small></a>`).join("")}</nav><section class="field-canvas"><header><div><h2>${esc(selectedField?.label)}</h2><code>${esc(selectedField?.key)}</code></div><a href="${esc(selectedField?.docs)}" target="_blank" rel="noopener noreferrer">文档 ↗</a></header>${config && selectedField ? `<form id="field-form"><div class="field-mutation"><label>操作<select name="mutation">${fieldValue.present ? '<option value="modify">修改字段</option><option value="delete">删除字段</option>' : '<option value="add">新增字段</option>'}</select></label></div><label>${esc(workspace.catalog.format)} 字段值<textarea name="fragment" spellcheck="false">${esc(fieldValue.fragment)}</textarea></label><footer><div><button class="button" type="submit" data-field-intent="validate">保存并校验</button><button class="button primary" type="submit" data-field-intent="deploy">保存并部署</button></div></footer></form>${sourceStudio}` : '<div class="empty compact"><strong>先创建一个服务端入站</strong></div>'}</section><aside class="official-rail"><header><b>官方文档</b><small>${workspace.catalog.topic_count}</small></header>${workspace.catalog.topic_groups.map((group) => `<details><summary>${esc(group.name)} <b>${group.topics.length}</b></summary><div>${group.topics.map((topic) => `<a href="${esc(topic.docs)}" target="_blank" rel="noopener noreferrer">${esc(topic.label)} ↗</a>`).join("")}</div></details>`).join("")}</aside></div></details>`,
    "节点配置",
  );
  bindAgentConfigPage({
    agent,
    engine,
    workspace,
    protocol,
    plan,
    selectedInbound,
    selectedField,
    fieldValue,
    base,
  });
}

function bindAgentConfigPage(ctx) {
  document.querySelectorAll("[data-engine-select]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        state.data.engine = link.dataset.engineSelect;
        state.data.protocol = "";
        state.data.inboundTag = "";
        agentConfig();
      }),
  );
  document.querySelectorAll("[data-inbound]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        const input = ctx.workspace.inbounds.find(
          (item) => item.tag === link.dataset.inbound,
        );
        state.data.inboundTag = input.tag;
        state.data.protocol = input.protocol;
        agentConfig();
      }),
  );
  document.querySelectorAll("[data-protocol]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        state.data.protocol = link.dataset.protocol;
        state.data.inboundTag = "";
        agentConfig();
      }),
  );
  document
    .querySelector("[data-new-inbound]")
    ?.addEventListener("click", () => {
      state.data.inboundTag = "";
      state.data.protocol = ctx.workspace.protocols[0]?.key;
      agentConfig();
    });
  document.querySelectorAll("[data-config-field]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        state.data.configField = link.dataset.configField;
        agentConfig();
      }),
  );
  document.querySelectorAll("[data-secret-visibility]").forEach(
    (button) =>
      (button.onclick = () => {
        const input = button.parentElement.querySelector("input");
        input.type = input.type === "password" ? "text" : "password";
        button.textContent = input.type === "password" ? "显示" : "隐藏";
      }),
  );
  document
    .querySelector("[data-regenerate]")
    ?.addEventListener("click", async () => {
      delete state.data.serverPlans[
        `${ctx.agent.id}|${ctx.engine}|${ctx.protocol.key}`
      ];
      state.data.inboundTag = "";
      await agentConfig();
    });
  document
    .querySelector("#server-plan-form")
    ?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      const input = {
        protocol: ctx.protocol.key,
        tag: form.get("tag"),
        listen: form.get("listen"),
        port: Number(form.get("port")),
        username: form.get("username"),
        credential: form.get("credential"),
        secondary_credential: form.get("secondary_credential"),
        method: form.get("method"),
        flow: ctx.protocol.uses_reality ? "xtls-rprx-vision" : "",
        transport: form.get("transport"),
        transport_path: form.get("transport_path"),
        tls_enabled:
          form.get("tls_enabled") === "1" || ctx.protocol.requires_tls,
        certificate_path: form.get("certificate_path") || "",
        private_key_path: form.get("private_key_path") || "",
        reality_enabled: form.get("reality_enabled") === "1",
        reality_private_key: form.get("reality_private_key") || "",
        reality_public_key: form.get("reality_public_key") || "",
        reality_short_id: form.get("reality_short_id") || "",
        reality_server_name: form.get("reality_server_name") || "",
      };
      try {
        await api(`${ctx.base}/server-inbounds`, {
          method: "POST",
          body: JSON.stringify({
            operation: form.get("operation"),
            original_tag: ctx.selectedInbound?.tag || "",
            expected_version: ctx.workspace.config?.version || 0,
            name:
              ctx.workspace.config?.name ||
              `${ctx.agent.name} · ${engineName(ctx.engine)}`,
            description: `${ctx.protocol.name} 服务端入站，由 QControlHub 方案生成`,
            intent: event.submitter?.dataset.planIntent || "validate",
            input,
          }),
        });
        state.data.inboundTag = input.tag;
        alert("配置已保存，任务已提交");
        await agentConfig();
      } catch (error) {
        alert(error.message);
      }
    });
  document
    .querySelector("#field-form")
    ?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      try {
        await api(
          `${ctx.base}/fields/${encodeURIComponent(ctx.selectedField.key)}`,
          {
            method: "POST",
            body: JSON.stringify({
              mutation: form.get("mutation"),
              fragment: form.get("fragment"),
              expected_version: ctx.workspace.config.version,
              name: ctx.workspace.config.name,
              description: ctx.workspace.config.description,
              intent: event.submitter?.dataset.fieldIntent || "validate",
            }),
          },
        );
        alert("字段已保存，任务已提交");
        await agentConfig();
      } catch (error) {
        alert(error.message);
      }
    });
  document
    .querySelector("#source-config-form")
    ?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      try {
        const saved = await api(`${ctx.base}`, {
          method: "PUT",
          body: JSON.stringify({
            agent_id: ctx.agent.id,
            engine: ctx.engine,
            name: form.get("name"),
            description: form.get("description"),
            content: form.get("content"),
            version: ctx.workspace.config.version,
          }),
        });
        await api("/tasks", {
          method: "POST",
          body: JSON.stringify({
            agent_id: ctx.agent.id,
            engine: ctx.engine,
            action: event.submitter?.dataset.sourceIntent || "validate",
            config_id: saved.id,
          }),
        });
        alert("源码已保存，任务已提交");
        await agentConfig();
      } catch (error) {
        alert(error.message);
      }
    });
}

async function liveConfig() {
  const agents = state.data.agents || (await api("/agents"));
  state.data.agents = agents;
  if (
    !state.data.liveAgent ||
    !agents.some((agent) => agent.id === state.data.liveAgent)
  ) {
    state.data.liveAgent = agents[0]?.id || "";
  }
  const agent = agents.find((item) => item.id === state.data.liveAgent);
  if (!agent) {
    shell(
      '<section class="empty large live-config-empty"><strong>没有可读取的节点配置</strong><p>请先添加节点并让节点上线。</p><a class="button primary" href="#agents">前往节点管理</a></section>',
      "节点实际配置",
    );
    return;
  }
  if (
    !state.data.liveEngine ||
    !agent.capabilities?.includes(state.data.liveEngine)
  ) {
    state.data.liveEngine = agent.capabilities?.[0] || engines[0];
  }
  const engine = state.data.liveEngine;
  const configWorkspace = await api(
    `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}/workspace`,
  );
  const current = configWorkspace.config || null;
  const language = engine === "mihomo" ? "YAML" : "JSON";
  shell(
    `<article class="live-config-workspace"><header class="editor-toolbar"><h2>${esc(agent.name)} · ${esc(engineName(engine))}</h2><div class="editor-toolbar-state"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><b>${current?.version ? `v${current.version}` : "未保存"}</b></div></header>${current ? `<form class="live-config-editor" id="live-config-form"><section class="code-workspace" data-code-editor data-code-language="${language}"><header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true">▱</span><b>${engine === "mihomo" ? "config.yaml" : "config.json"}</b></div><div class="code-editor-meta"><span class="code-language">${language}</span><span data-code-status>已读取</span></div></header><div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true">1</aside><textarea class="code-editor-input" name="content" data-code-input spellcheck="false" required>${esc(current.content)}</textarea></div><footer><span><i class="code-status-dot"></i><span>已读取</span></span><div><button class="button code-reset" type="button" data-live-reset>恢复原文</button><button class="button" type="submit" data-live-intent="validate">校验修改</button><button class="button primary" type="submit" data-live-intent="deploy">保存并部署</button></div></footer></section><aside class="live-config-inspector"><dl><div><dt>节点</dt><dd>${esc(agent.name)}</dd></div><div><dt>系统</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>内核</dt><dd>${esc(engineName(engine))}</dd></div><div><dt>来源</dt><dd>控制面版本</dd></div></dl></aside><input type="hidden" name="name" value="${esc(current.name)}"><input type="hidden" name="description" value="${esc(current.description)}"><input type="hidden" name="version" value="${current.version}"></form>` : `<section class="node-config-source"><h2>${agent.status === "online" ? "正在读取配置" : "节点离线"}</h2><span class="status-label warn">${agent.status === "online" ? "尚未保存节点配置" : "无法读取"}</span>${agent.status === "online" ? '<button class="button primary" type="button" data-read-current>读取当前配置</button>' : ""}</section>`}</article>`,
    "节点实际配置",
  );
  document.querySelectorAll("[data-live-agent]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        state.data.liveAgent = link.dataset.liveAgent;
        state.data.liveEngine = "";
        liveConfig();
      }),
  );
  document.querySelectorAll("[data-live-engine]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        state.data.liveEngine = link.dataset.liveEngine;
        liveConfig();
      }),
  );
  document
    .querySelector("[data-read-current]")
    ?.addEventListener("click", async () => {
      await submitTask({ agent_id: agent.id, engine, action: "read-config" });
      await liveConfig();
    });
  document.querySelector("[data-live-reset]")?.addEventListener("click", () => {
    document.querySelector("[data-code-input]").value = current.content;
  });
  document
    .querySelector("#live-config-form")
    ?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      const intent = event.submitter?.dataset.liveIntent || "validate";
      try {
        const saved = await api(
          `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}`,
          {
            method: "PUT",
            body: JSON.stringify({
              agent_id: agent.id,
              engine,
              name: form.get("name"),
              description: form.get("description"),
              content: form.get("content"),
              version: Number(form.get("version")),
            }),
          },
        );
        if (intent === "deploy")
          await submitTask({
            agent_id: agent.id,
            engine,
            action: "deploy",
            config_id: saved.id,
          });
        else
          await submitTask({
            agent_id: agent.id,
            engine,
            action: "validate",
            config_id: saved.id,
          });
        await liveConfig();
      } catch (error) {
        alert(error.message);
      }
    });
}

async function archiveConfigs() {
  const [items, templates, agents] = await Promise.all([
    api("/configs"),
    api("/templates"),
    api("/agents"),
  ]);
  state.data.configs = items;
  state.data.agents = agents;
  const isNew = state.data.newConfig || items.length === 0;
  let selected = items.find((item) => item.id === state.data.archiveConfigId);
  if (!selected && !isNew) selected = items[0];
  const formConfig = selected || {
    id: "",
    name: "新配置",
    description: "",
    engine: "mihomo",
    content:
      "mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: info\nproxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n",
    version: 0,
  };
  state.data.archiveConfigId = formConfig.id;
  const revisions = formConfig.id
    ? await api(
        `/configs/${encodeURIComponent(formConfig.id)}/revisions?limit=50`,
      )
    : [];
  let preview = null;
  if (formConfig.id && state.data.revisionVersion) {
    preview = await optionalAPI(
      `/configs/${encodeURIComponent(formConfig.id)}/revisions/${state.data.revisionVersion}`,
    );
  }
  const deployAgents = agents.filter(
    (agent) =>
      agent.status === "online" &&
      (agent.capabilities || []).includes(formConfig.engine),
  );
  const templateCards =
    templates
      .map(
        (item) =>
          `<article class="template-card"><header><span class="engine-badge ${esc(item.engine)}">${esc(engineName(item.engine))}</span><h4>${esc(item.name)}</h4><small>${ago(item.updated_at)}</small></header><pre>${esc(item.content)}</pre>${
            can("operator")
              ? `<footer><form data-template-apply="${esc(item.id)}"><label>应用至<select name="agent_id" required><option value="">选择在线节点</option>${agents
                  .filter(
                    (agent) =>
                      agent.status === "online" &&
                      (agent.capabilities || []).includes(item.engine),
                  )
                  .map(
                    (agent) =>
                      `<option value="${esc(agent.id)}">${esc(agent.name)} · ${esc(engineName(item.engine))}</option>`,
                  )
                  .join(
                    "",
                  )}</select></label><button class="button small" type="submit">应用</button></form>${can("admin") ? `<button class="button small danger-button" type="button" data-delete-template="${esc(item.id)}">删除</button>` : ""}</footer>`
              : ""
          }</article>`,
      )
      .join("") ||
    '<p class="template-empty">还没有模板。新建模板后可按节点变量生成配置。</p>';
  const archiveNav = `<div class="config-archive-switcher"><label>配置档案<select id="archive-select">${items.map((item) => `<option value="${esc(item.id)}" ${item.id === formConfig.id ? "selected" : ""}>${esc(item.name)} · ${esc(engineName(item.engine))} v${item.version}</option>`).join("")}</select></label>${can("operator") ? '<button class="button" type="button" id="new-config">＋ 新建配置档案</button>' : ""}</div>`;
  const revisionTimeline = formConfig.id
    ? `<details class="revision-timeline" ${preview ? "open" : ""}><summary><b>版本历史</b><strong>${revisions.length} 个版本</strong></summary><div class="timeline-body"><nav aria-label="配置修订历史">${revisions.map((revision) => `<button class="${preview?.version === revision.version ? "active" : ""} ${revision.version === formConfig.version ? "current" : ""}" type="button" data-revision="${revision.version}"><i></i><span><b>v${revision.version}</b><strong>${esc(revision.name)}</strong><small>${ago(revision.updated_at)}${revision.version === formConfig.version ? " · 当前" : ""}</small></span></button>`).join("")}</nav>${preview ? `<section class="timeline-preview"><header><div><b>v${preview.version} · ${esc(preview.name)}</b><small>${esc(engineName(preview.engine))} · ${date(preview.updated_at)}</small></div>${preview.version === formConfig.version ? '<span class="status-label ok">当前版本</span>' : ""}</header><textarea readonly>${esc(preview.content)}</textarea>${can("admin") && preview.version !== formConfig.version ? `<button class="button" type="button" data-restore-revision="${preview.version}">以此版本创建新版本</button>` : ""}</section>` : '<div class="timeline-placeholder">选择版本</div>'}</div></details>`
    : "";
  const delivery = formConfig.id
    ? `<section class="delivery-bar"><div><span class="delivery-icon">⚡</span><h3>校验或部署</h3></div><form id="archive-delivery"><label>目标节点<select name="agent_id" required><option value="">${deployAgents.length ? `选择在线且支持 ${esc(engineName(formConfig.engine))} 的节点` : `没有在线且支持 ${esc(engineName(formConfig.engine))} 的节点`}</option>${deployAgents.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)} · 在线</option>`).join("")}</select></label><label>执行方式<select name="action"><option value="validate">仅校验，不写入</option><option value="deploy">部署并重启</option></select></label><button class="button primary" type="submit" ${!deployAgents.length || !can("operator") ? "disabled" : ""}>提交任务</button></form></section>`
    : "";
  shell(
    `${archiveNav}<article class="config-workspace"><header class="editor-toolbar"><h2>${esc(formConfig.name)}</h2><div class="editor-toolbar-state"><span class="engine-badge ${esc(formConfig.engine)}">${esc(engineName(formConfig.engine))}</span><b>${isNew ? "草稿" : `v${formConfig.version}`}</b></div></header><form class="config-editor-grid" id="archive-form"><section class="code-workspace" data-code-editor data-code-language="${formConfig.engine === "mihomo" ? "YAML" : "JSON"}"><header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon">▱</span><b>${formConfig.engine === "mihomo" ? "config.yaml" : "config.json"}</b></div><div class="code-editor-meta"><span class="code-language">${formConfig.engine === "mihomo" ? "YAML" : "JSON"}</span><span data-code-status>${isNew ? "草稿" : "已保存"}</span><span data-code-bytes>${bytes(new Blob([formConfig.content]).size)}</span></div></header><div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true">1</aside><textarea class="code-editor-input" name="content" spellcheck="false" required ${can("operator") ? "" : "readonly"}>${esc(formConfig.content)}</textarea></div><footer><span><i class="code-status-dot"></i><span></span></span><div><button class="button" type="button" data-archive-reset>恢复原文</button>${can("operator") ? `<button class="button primary" type="submit">${isNew ? "创建配置档案" : "保存新版本"}</button>` : ""}</div></footer></section><aside class="config-inspector"><header><h3>属性</h3></header><label>名称<input name="name" maxlength="100" required value="${esc(formConfig.name)}" ${can("operator") ? "" : "readonly"}></label><label>内核<select name="engine" ${isNew && can("operator") ? "" : "disabled"}>${engines.map((engine) => `<option value="${engine}" ${engine === formConfig.engine ? "selected" : ""}>${esc(engineName(engine))} · ${engine === "mihomo" ? "YAML" : "JSON"}</option>`).join("")}</select></label><label>说明<textarea class="description-input" name="description" maxlength="300" placeholder="填写用途、节点或变更说明" ${can("operator") ? "" : "readonly"}>${esc(formConfig.description || "")}</textarea></label></aside></form>${delivery}${revisionTimeline}${can("admin") && formConfig.id ? '<footer class="config-danger"><span><b>删除配置档案</b><small>相关任务记录会保留，配置档案删除后无法恢复。</small></span><button type="button" data-remove="' + esc(formConfig.id) + '">删除配置</button></footer>' : ""}</article><section class="template-workspace" id="templates"><header class="template-head"><h3>配置模板</h3><span>用 {{node_name}}、{{node_id}}、{{lan_ip}}、{{random_port}} 占位符，按节点批量生成配置。</span></header>${can("operator") ? '<details class="template-create" ' + (!templates.length ? "open" : "") + '><summary><b>＋ 新建模板</b></summary><form id="template-form"><label>模板名称<input name="name" maxlength="100" required></label><label>内核<select name="engine">' + engines.map((engine) => `<option value="${engine}">${esc(engineName(engine))}</option>`).join("") + '</select></label><label class="template-content-field">模板正文<textarea name="content" spellcheck="false" required></textarea></label><button class="button primary" type="submit">保存模板</button></form></details>' : ""}<div class="template-grid">${templateCards}</div></section>`,
    "配置档案",
  );
  document
    .querySelector("#archive-select")
    ?.addEventListener("change", (event) => {
      state.data.newConfig = false;
      state.data.archiveConfigId = event.target.value;
      state.data.revisionVersion = 0;
      archiveConfigs();
    });
  document.querySelector("#new-config")?.addEventListener("click", () => {
    state.data.newConfig = true;
    state.data.archiveConfigId = "";
    state.data.revisionVersion = 0;
    archiveConfigs();
  });
  document
    .querySelector("[data-archive-reset]")
    ?.addEventListener("click", () => {
      document.querySelector('#archive-form textarea[name="content"]').value =
        formConfig.content;
    });
  document
    .querySelector("#archive-form")
    ?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      const payload = {
        name: form.get("name"),
        description: form.get("description"),
        engine: form.get("engine") || formConfig.engine,
        content: form.get("content"),
        version: formConfig.version,
      };
      try {
        const saved = await api(
          formConfig.id
            ? `/configs/${encodeURIComponent(formConfig.id)}`
            : "/configs",
          {
            method: formConfig.id ? "PUT" : "POST",
            body: JSON.stringify(payload),
          },
        );
        state.data.newConfig = false;
        state.data.archiveConfigId = saved.id;
        await archiveConfigs();
      } catch (error) {
        alert(error.message);
      }
    });
  document
    .querySelector("#archive-delivery")
    ?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      await api("/tasks", {
        method: "POST",
        body: JSON.stringify({
          agent_id: form.get("agent_id"),
          engine: formConfig.engine,
          action: form.get("action"),
          config_id: formConfig.id,
        }),
      });
      alert("任务已提交");
      location.hash = "#tasks";
    });
  document.querySelectorAll("[data-revision]").forEach(
    (button) =>
      (button.onclick = () => {
        state.data.revisionVersion = Number(button.dataset.revision);
        archiveConfigs();
      }),
  );
  document
    .querySelector("[data-restore-revision]")
    ?.addEventListener("click", async (event) => {
      if (
        !confirm(
          `确定以 v${event.currentTarget.dataset.restoreRevision} 的内容创建新版本？`,
        )
      )
        return;
      await api(
        `/configs/${encodeURIComponent(formConfig.id)}/revisions/${event.currentTarget.dataset.restoreRevision}/restore`,
        {
          method: "POST",
          body: JSON.stringify({ expected_version: formConfig.version }),
        },
      );
      state.data.revisionVersion = 0;
      await archiveConfigs();
    });
  document.querySelectorAll("[data-remove]").forEach(
    (button) =>
      (button.onclick = async () => {
        if (!confirm("确认删除配置？")) return;
        try {
          await api(`/configs/${button.dataset.remove}`, { method: "DELETE" });
          state.data.archiveConfigId = "";
          state.data.newConfig = false;
          archiveConfigs();
        } catch (error) {
          alert(error.message);
        }
      }),
  );
  document
    .querySelector("#template-form")
    ?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      try {
        await api("/templates", {
          method: "POST",
          body: JSON.stringify({
            name: form.get("name"),
            engine: form.get("engine"),
            content: form.get("content"),
          }),
        });
        archiveConfigs();
      } catch (error) {
        alert(error.message);
      }
    });
  document.querySelectorAll("[data-delete-template]").forEach(
    (button) =>
      (button.onclick = async () => {
        if (!confirm("确认删除模板？")) return;
        try {
          await api(`/templates/${button.dataset.deleteTemplate}`, {
            method: "DELETE",
          });
          archiveConfigs();
        } catch (error) {
          alert(error.message);
        }
      }),
  );
  document.querySelectorAll("[data-template-apply]").forEach(
    (form) =>
      (form.onsubmit = async (event) => {
        event.preventDefault();
        const agentID = new FormData(form).get("agent_id");
        try {
          await api(`/templates/${form.dataset.templateApply}/apply`, {
            method: "POST",
            body: JSON.stringify({ agent_id: agentID }),
          });
          alert("模板已应用");
        } catch (error) {
          alert(error.message);
        }
      }),
  );
}

async function tasks() {
  const filters = state.data.taskFilters || {};
  const query = new URLSearchParams({
    limit: String(filters.limit || 100),
    ...(filters.agent_id ? { agent_id: filters.agent_id } : {}),
    ...(filters.status ? { status: filters.status } : {}),
    ...(filters.action ? { action: filters.action } : {}),
  });
  const [items, agents, settings] = await Promise.all([
    api(`/tasks?${query}`),
    api("/agents"),
    api("/settings"),
  ]);
  const taskCards =
    items
      .map((item) => {
        const agent = agents.find((entry) => entry.id === item.agent_id);
        const diagnostic = diagnoseTask(item);
        return `<article id="task-${esc(item.id)}" class="audit-event task-event" data-task-id="${esc(item.id)}" data-task-status="${esc(item.status)}" aria-busy="${item.status === "pending" || item.status === "running"}"><div class="timeline-marker"><i class="${item.status === "succeeded" ? "ok" : item.status === "failed" ? "bad" : item.status === "running" ? "warn" : ""}"></i><span></span></div><div class="task-event-card"><header><div class="event-action"><span class="status-label ${item.status === "succeeded" ? "ok" : item.status === "failed" ? "bad" : item.status === "running" ? "warn" : "muted"}">${esc(statusName(item.status))}</span><strong>${esc(actionName(item.action))}</strong><small><code title="${esc(item.id)}">${esc(short(item.id))}</code> · ${item.attempt ? `第 ${item.attempt} 次执行` : "尚未开始"}</small>${item.status === "pending" && can("operator") ? `<button data-cancel="${esc(item.id)}">取消任务</button>` : ""}${["failed", "canceled"].includes(item.status) && can("operator") ? `<button data-retry="${esc(item.id)}">${item.config_id ? "使用当前配置重试" : "重试任务"}</button>` : ""}</div><time><b>${date(item.created_at)}</b><small>${ago(item.created_at)}</small></time></header><div class="task-event-body"><div class="event-target"><span class="engine-badge ${esc(item.engine)}">${esc(engineName(item.engine))}</span><span><b>节点</b><small>${esc(agent?.name || short(item.agent_id))}</small></span>${item.config_id ? `<span><b>配置</b><small>${esc(short(item.config_id))} · v${item.config_version}</small></span>` : item.core_version ? `<span><b>版本</b><small>${esc(item.core_version)}</small></span>` : ""}<span class="task-lifecycle"><b>耗时</b><small>${esc(taskTiming(item))}</small></span></div><div class="event-result">${diagnostic ? `<div class="task-diagnostic"><b>${esc(diagnostic.title)}</b><small>${esc(diagnostic.advice)}</small></div>` : ""}${item.error || item.output ? `<details><summary>节点结果 <span>→</span></summary>${item.error ? `<div class="task-result-block"><header><b>错误</b></header><pre class="task-error">${esc(item.error)}</pre></div>` : ""}${item.output ? `<div class="task-result-block"><header><b>输出</b></header><pre>${esc(item.output)}</pre></div>` : ""}</details>` : item.status === "pending" || item.status === "running" ? "<span>执行中</span>" : ""}</div></div></div></article>`;
      })
      .join("") ||
    '<div class="empty large"><strong>没有符合条件的任务</strong></div>';
  shell(
    `<div class="task-workspace" data-task-page><details class="task-filter-panel" open><summary><b>筛选</b><i>⌄</i></summary><div class="audit-query"><label>节点<select id="task-agent"><option value="">全部节点</option>${agents.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`).join("")}</select></label><label>状态<select id="task-status"><option value="">全部状态</option>${["pending", "running", "succeeded", "failed", "canceled"].map((status) => `<option value="${status}">${esc(statusName(status))}</option>`).join("")}</select></label><label>动作<select id="task-action"><option value="">全部动作</option>${actions.map((action) => `<option value="${action}">${esc(actionName(action))}</option>`).join("")}</select></label><label>每页数量<select id="task-limit"><option value="50">50 条</option><option value="100">100 条</option><option value="500">500 条</option></select></label><button class="button primary" type="button" data-apply-task-filter>应用筛选</button></div></details><div class="audit-live" role="status"><i></i>自动更新</div><section class="task-timeline" aria-label="任务时间线">${taskCards}</section></div>`,
    "任务",
  );
  const filterValues = state.data.taskFilters || {};
  ["agent", "status", "action"].forEach((name) => {
    const field = document.querySelector(`#task-${name}`);
    if (field) {
      field.value = filterValues[name === "agent" ? "agent_id" : name] || "";
      field.onchange = () => {
        state.data.taskFilters = {
          agent_id: document.querySelector("#task-agent")?.value || "",
          status: document.querySelector("#task-status")?.value || "",
          action: document.querySelector("#task-action")?.value || "",
        };
        tasks();
      };
    }
  });
  const limitField = document.querySelector("#task-limit");
  if (limitField) limitField.value = String(filterValues.limit || 100);
  document
    .querySelector("[data-apply-task-filter]")
    ?.addEventListener("click", () => {
      state.data.taskFilters = {
        agent_id: document.querySelector("#task-agent")?.value || "",
        status: document.querySelector("#task-status")?.value || "",
        action: document.querySelector("#task-action")?.value || "",
        limit: Number(document.querySelector("#task-limit")?.value || 100),
      };
      tasks();
    });
  document.querySelectorAll("[data-task-status-filter]").forEach((link) => {
    link.onclick = (event) => {
      event.preventDefault();
      state.data.taskFilters = {
        ...(state.data.taskFilters || {}),
        status: link.dataset.taskStatusFilter,
      };
      tasks();
    };
  });
  document.querySelectorAll("[data-cancel]").forEach(
    (button) =>
      (button.onclick = async () => {
        try {
          await api(`/tasks/${button.dataset.cancel}`, { method: "DELETE" });
          render();
        } catch (error) {
          alert(error.message);
        }
      }),
  );
  document.querySelectorAll("[data-retry]").forEach(
    (button) =>
      (button.onclick = async () => {
        try {
          await api(`/tasks/${button.dataset.retry}/retry`, { method: "POST" });
          render();
        } catch (error) {
          alert(error.message);
        }
      }),
  );
  clearTimeout(state.taskPollTimer);
  if (state.route === "tasks") {
    state.taskPollTimer = setTimeout(
      () => tasks(),
      settings.task_poll_interval_ms || 1000,
    );
  }
}

function taskTiming(task) {
  if (task.status === "pending") return "准备执行";
  if (task.status === "running")
    return task.started_at
      ? `已运行 ${ago(task.started_at).replace("前", "")}`
      : "正在启动执行";
  if (task.started_at && task.finished_at) {
    const seconds = Math.max(
      0,
      Math.round(
        (new Date(task.finished_at) - new Date(task.started_at)) / 1000,
      ),
    );
    return seconds < 60
      ? `执行 ${seconds} 秒`
      : `执行 ${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
  }
  return task.finished_at ? "未开始执行" : "时间记录不完整";
}

function diagnoseTask(task) {
  if (task.status !== "failed") return null;
  const error = String(task.error || "").toLowerCase();
  if (error.includes("rolled back"))
    return {
      title: "变更失败，已自动回滚",
      advice: "旧配置或旧二进制已经恢复；先查询服务状态后再重试。",
    };
  if (error.includes("rejected the configuration"))
    return {
      title: "配置未通过真实内核校验",
      advice: "展开节点返回结果定位字段，修正后使用当前配置重试。",
    };
  if (task.action === "install")
    return {
      title: "内核安装或切换失败",
      advice: "展开结果确认下载、校验或重启阶段。",
    };
  return {
    title: "节点操作执行失败",
    advice: "展开节点返回结果并确认节点与服务状态后再重试。",
  };
}
async function settings() {
  const [item, auditLogs] = await Promise.all([
    api("/settings"),
    api("/audit?limit=100"),
  ]);
  state.data.settings = item;
  const auditRows =
    auditLogs
      .map(
        (entry) =>
          `<li><time>${date(entry.acted_at)}</time><span class="audit-action">${esc(entry.action)}</span>${entry.target ? `<code>${esc(entry.target)}</code>` : ""}${entry.detail ? `<small>${esc(entry.detail)}</small>` : ""}<em>${esc(entry.remote_ip || "-")}</em></li>`,
      )
      .join("") || '<li class="muted">暂无操作记录</li>';
  const readOnly = can("admin") ? "" : "disabled";
  shell(
    `<div class="settings-workspace"><header class="settings-hero"><h2>系统设置</h2></header><form class="settings-form" id="settings-form"><section class="settings-section" id="identity"><header><span class="settings-section-number">01</span><h3>面板标识</h3></header><div class="settings-grid"><label class="settings-field"><span>面板名称</span><input name="panel_name" value="${esc(item.panel_name)}" maxlength="40" required ${readOnly}></label><label class="settings-field"><span>面板说明</span><input name="panel_description" value="${esc(item.panel_description)}" maxlength="120" ${readOnly}></label></div></section><section class="settings-section" id="defaults"><header><span class="settings-section-number">02</span><h3>操作默认值</h3></header><div class="settings-grid"><label class="settings-field"><span>入网码默认有效期</span><select name="enrollment_ttl_minutes" ${readOnly}>${[10, 15, 30, 60].map((value) => `<option value="${value}" ${value === item.enrollment_ttl_minutes ? "selected" : ""}>${value === 60 ? "1 小时" : `${value} 分钟`}</option>`).join("")}</select></label><label class="settings-field"><span>任务默认显示数量</span><select name="task_page_size" ${readOnly}>${[50, 100, 500].map((value) => `<option value="${value}" ${value === item.task_page_size ? "selected" : ""}>${value} 条</option>`).join("")}</select></label></div></section><section class="settings-section" id="synchronization"><header><span class="settings-section-number">03</span><h3>状态同步</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>任务状态刷新频率</span><select name="task_poll_interval_ms" ${readOnly}>${[600, 1000, 2000, 5000].map((value) => `<option value="${value}" ${value === item.task_poll_interval_ms ? "selected" : ""}>${value < 1000 ? "0.6 秒" : `${value / 1000} 秒`}</option>`).join("")}</select></label></div></section><section class="settings-section" id="notifications"><header><span class="settings-section-number">04</span><h3>事件通知</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>Webhook 地址</span><input name="webhook_url" type="url" value="${esc(item.webhook_url)}" maxlength="500" placeholder="https://example.com/hooks/qcontrolhub（留空禁用）" ${readOnly}></label><p class="settings-hint">任务失败、节点离线或恢复在线时，控制面会发送带 HMAC-SHA256 签名的 JSON 事件。</p></div></section><section class="settings-section" id="audit"><header><span class="settings-section-number">05</span><h3>最近操作</h3></header><ol class="audit-list">${auditRows}</ol></section>${can("admin") ? '<footer class="settings-savebar"><div><button class="button" type="button" data-reset-settings>恢复默认值</button><button class="button primary" type="submit">保存设置</button></div></footer>' : '<p class="settings-hint">当前角色仅可查看设置与操作记录。</p>'}</form></div>`,
    "设置",
  );
  document.querySelector("#settings-form").onsubmit = async (event) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    try {
      await api("/settings", {
        method: "PUT",
        body: JSON.stringify({
          panel_name: form.get("panel_name"),
          panel_description: form.get("panel_description"),
          enrollment_ttl_minutes: Number(form.get("enrollment_ttl_minutes")),
          task_page_size: Number(form.get("task_page_size")),
          task_poll_interval_ms: Number(form.get("task_poll_interval_ms")),
          webhook_url: form.get("webhook_url"),
        }),
      });
      alert("设置已保存");
    } catch (error) {
      alert(error.message);
    }
  };
  document
    .querySelector("[data-reset-settings]")
    ?.addEventListener("click", async () => {
      if (!confirm("确定恢复系统默认设置？")) return;
      await api("/settings", {
        method: "PUT",
        body: JSON.stringify({
          panel_name: "QControlHub",
          panel_description: "可信远程编排",
          enrollment_ttl_minutes: 15,
          task_page_size: 100,
          task_poll_interval_ms: 600,
          webhook_url: "",
        }),
      });
      await settings();
    });
}

async function render() {
  if (state.busy) return;
  clearTimeout(state.taskPollTimer);
  state.busy = true;
  const hash = location.hash.slice(1);
  const routeMap = {
    summary: "dashboard",
    fleet: "dashboard",
    activity: "dashboard",
    enrollment: "agents",
    "client-access": "client-access",
    identity: "settings",
    defaults: "settings",
    synchronization: "settings",
    notifications: "settings",
    "new-config": "archive-config",
    templates: "archive-config",
    archive: "archive-config",
  };
  state.route = [
    "dashboard",
    "agents",
    "agent-config",
    "client-access",
    "live-config",
    "archive-config",
    "tasks",
    "settings",
  ].includes(hash)
    ? hash
    : routeMap[hash] ||
      (hash.startsWith("node-")
        ? "agents"
        : hash.startsWith("config-")
          ? "archive-config"
          : "dashboard");
  state.anchor = hash;
  try {
    if (!state.session && !(await ensureSession())) {
      renderLogin();
      return;
    }
    [state.data.overview, state.data.settings] = await Promise.all([
      api("/overview"),
      api("/settings"),
    ]);
    const pages = {
      dashboard,
      agents,
      "client-access": clientAccess,
      "agent-config": agentConfig,
      "live-config": liveConfig,
      "archive-config": archiveConfigs,
      tasks,
      settings,
    };
    await (pages[state.route] || dashboard)();
    if (state.anchor === "new-config")
      document.querySelector("#new-config")?.click();
    else if (state.anchor && state.anchor !== state.route)
      requestAnimationFrame(() =>
        document
          .getElementById(state.anchor)
          ?.scrollIntoView({ block: "start" }),
      );
  } catch (error) {
    shell(
      `<section class="section"><div class="alert error">${esc(error.message)}</div></section>`,
      "错误",
    );
  } finally {
    state.busy = false;
  }
}
window.addEventListener("hashchange", render);
render();
