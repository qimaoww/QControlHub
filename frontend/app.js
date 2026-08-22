import { installDashboard } from "./modules/dashboard.js";
import { installAgents } from "./modules/agents.js";
import { installClientAccess } from "./modules/client-access.js";
import { installConfigPages } from "./modules/configs.js";
import { installCoreLogs } from "./modules/core-logs.js";
import { installTasks } from "./modules/tasks.js";
import { installTraffic } from "./modules/traffic.js";
import { installSettings } from "./modules/settings.js";

const app = document.querySelector("#app");
const themeStorageKey = "qcontrolhub-color-theme";
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
  "upgrade-agent",
];
const state = {
  session: null,
  route: location.hash.slice(1) || "dashboard",
  data: {},
  busy: false,
  confirmResolver: null,
  confirmOpen: false,
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
    "upgrade-agent": "升级 Agent",
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
    online: "在线",
    offline: "离线",
    pending: "准备中",
    running: "执行中",
    succeeded: "成功",
    active: "运行中",
    inactive: "已停止",
    activating: "启动中",
    deactivating: "停止中",
    failed: "失败",
  })[value] ||
  value ||
  "未知";
const short = (value) => String(value || "").slice(0, 12);
const statusTone = (value) => {
  if (["online", "succeeded", "active"].includes(value)) return "ok";
  if (
    ["pending", "running", "activating", "deactivating"].includes(
      value,
    )
  )
    return "warn";
  if (["offline", "failed", "inactive"].includes(value)) return "bad";
  return "muted";
};
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
const heartbeat = (value) => (value ? `心跳 ${ago(value)}` : "尚未心跳");
const conciseVersion = (engine, value) => {
  const match = String(value || "").match(
    /\b(?:v?\d+(?:\.\d+){1,5}(?:[-.][0-9A-Za-z]+)*|(?:alpha|beta|dev|rc|pre|nightly|stable)-?[0-9A-Za-z]{6,})/i,
  );
  if (!match) return `${engineName(engine)} 内核版本未知`;
  const token = /^\d/.test(match[0]) ? `v${match[0]}` : match[0];
  return `${engineName(engine)} 内核 ${token}`;
};
const taskActivity = (items, limit = 7) => {
  const groups = [];
  for (const task of items) {
    const previous = groups.at(-1);
    if (
      previous &&
      previous.task.action === task.action &&
      previous.task.agent_id === task.agent_id &&
      previous.task.engine === task.engine &&
      previous.task.status === task.status
    )
      previous.count += 1;
    else groups.push({ task, count: 1 });
    if (groups.length >= limit) break;
  }
  return groups;
};
const serviceActionDisabled = (action, online, installed, serviceStatus) => {
  if (!online || !installed || !can("tasks.execute")) return true;
  if (action === "start")
    return ["active", "activating"].includes(serviceStatus);
  if (action === "stop")
    return ["inactive", "deactivating"].includes(serviceStatus);
  if (action === "restart")
    return ["inactive", "activating", "deactivating"].includes(serviceStatus);
  return false;
};
const installedEngineCount = (agent) =>
  (agent?.capabilities || []).filter(
    (engine) => agent.runtime?.[engine]?.installed,
  ).length;
const rate = (value) => `${bytes(value)}/s`;

function trafficChart(samples) {
  if (!Array.isArray(samples) || samples.length < 2) return "";
  const width = 480;
  const height = 64;
  const pad = 3;
  const peak = Math.max(
    1,
    ...samples.flatMap((sample) => [sample.rx_rate_bps, sample.tx_rate_bps]),
  );
  const points = (field) =>
    samples
      .map((sample, index) => {
        const x = pad + (index * (width - 2 * pad)) / (samples.length - 1);
        const y =
          height -
          pad -
          (Number(sample[field] || 0) / peak) * (height - 2 * pad);
        return `${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(" ");
  const latest = samples.at(-1);
  return `<svg class="metric-trend-chart" viewBox="0 0 ${width} ${height}" role="img" aria-label="最近 24 小时上下行速率趋势"><polyline class="trend-line trend-rx" points="${points("rx_rate_bps")}"></polyline><polyline class="trend-line trend-tx" points="${points("tx_rate_bps")}"></polyline></svg><dl class="metric-trend-legend"><div><i class="trend-dot trend-rx"></i><span>下载</span><b>${esc(rate(latest.rx_rate_bps))}</b></div><div><i class="trend-dot trend-tx"></i><span>上传</span><b>${esc(rate(latest.tx_rate_bps))}</b></div></dl>`;
}

function renderConfigDiff(savedContent, deployedContent) {
  const split = (value) =>
    String(value || "")
      .replaceAll("\r\n", "\n")
      .replace(/\n$/, "")
      .split("\n");
  const before = split(deployedContent);
  const after = split(savedContent);
  if (before.join("\n") === after.join("\n")) return "";
  let prefix = 0;
  while (
    prefix < before.length &&
    prefix < after.length &&
    before[prefix] === after[prefix]
  )
    prefix += 1;
  let suffix = 0;
  while (
    suffix < before.length - prefix &&
    suffix < after.length - prefix &&
    before[before.length - suffix - 1] === after[after.length - suffix - 1]
  )
    suffix += 1;
  const rows = [];
  before.slice(Math.max(0, prefix - 2), prefix).forEach((line) =>
    rows.push(`<span class="diff-context"></span>${esc(line)}`),
  );
  before
    .slice(prefix, before.length - suffix)
    .forEach((line) => rows.push(`<span class="diff-remove">- </span>${esc(line)}`));
  after
    .slice(prefix, after.length - suffix)
    .forEach((line) => rows.push(`<span class="diff-add">+ </span>${esc(line)}`));
  after.slice(after.length - suffix, after.length - Math.max(0, suffix - 2)).forEach(
    (line) => rows.push(`<span class="diff-context"></span>${esc(line)}`),
  );
  return `<pre class="config-diff" aria-label="配置差异">${rows.join("\n")}\n</pre>`;
}
const rolePermissions = {
  admin: new Set([
    "overview.read", "agents.read", "agents.manage", "client-access.read",
    "deployments.read", "catalogs.read", "agent-config.read", "agent-config.write",
    "configs.read", "configs.write", "configs.delete", "configs.restore",
    "tasks.read", "tasks.execute", "enrollment.manage", "settings.read",
    "settings.manage", "audit.read", "metrics.read", "traffic.read", "traffic.manage", "users.manage",
    "core-logs.read",
    "templates.read", "templates.write", "templates.delete",
  ]),
  operator: new Set([
    "overview.read", "agents.read", "deployments.read", "client-access.read",
    "catalogs.read", "agent-config.read", "agent-config.write", "configs.read",
    "configs.write", "tasks.read", "tasks.execute", "settings.read", "audit.read",
    "metrics.read", "traffic.read", "traffic.manage", "templates.read", "templates.write",
    "core-logs.read",
  ]),
  auditor: new Set([
    "overview.read", "agents.read", "deployments.read", "tasks.read",
    "settings.read", "audit.read", "metrics.read", "traffic.read",
    "core-logs.read",
  ]),
  readonly: new Set([
    "overview.read", "agents.read", "deployments.read", "client-access.read",
    "catalogs.read", "agent-config.read", "configs.read", "tasks.read",
    "settings.read", "audit.read", "metrics.read", "traffic.read", "templates.read",
    "core-logs.read",
  ]),
};
const roleRanks = { readonly: 1, auditor: 1, operator: 2, admin: 3 };
const can = (capability) => {
  if (capability === "operator") capability = "tasks.execute";
  const role = state.session?.role;
  if (role === "admin") return true;
  if (role === "user") return (state.session?.permissions || []).includes(capability);
  if (capability in roleRanks)
    return (roleRanks[role] || 0) >= (roleRanks[capability] || 0);
  return Boolean(rolePermissions[role]?.has(capability));
};

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
    state.data = {};
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
    state.data = {};
    return false;
  }
}

function storedTheme() {
  try {
    const value = localStorage.getItem(themeStorageKey);
    return value === "light" || value === "dark" ? value : "";
  } catch {
    return "";
  }
}

function applyTheme(theme = storedTheme() || (matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark")) {
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
  const nextLabel = theme === "light" ? "切换为深色主题" : "切换为浅色主题";
  document.querySelectorAll("[data-theme-toggle]").forEach((button) => {
    button.setAttribute("aria-label", nextLabel);
    button.setAttribute("title", nextLabel);
    const icon = button.querySelector("[data-theme-icon]");
    if (icon) icon.textContent = theme === "light" ? "☾" : "☀";
  });
}

function toggleTheme() {
  const next =
    document.documentElement.dataset.theme === "light" ? "dark" : "light";
  try {
    localStorage.setItem(themeStorageKey, next);
  } catch {}
  applyTheme(next);
}

function renderLogin(message = "") {
  document.body.className = "login-body";
  document.title = "登录 · QControlHub";
  app.style.display = "contents";
  app.innerHTML = `<button class="theme-toggle login-theme-toggle" type="button" data-theme-toggle aria-label="切换颜色主题"><span data-theme-icon aria-hidden="true">☀</span></button><main class="login-shell compact-login"><section class="login-card"><a class="brand login-card-brand" href="#dashboard"><span class="brand-mark large">QH</span><strong>QControlHub</strong></a><div class="login-card-head"><h1>登录</h1></div>${message ? `<div class="alert error">${esc(message)}</div>` : ""}<form id="login-form" class="stack-form"><label>用户名<input name="username" type="text" value="admin" autocomplete="username" autofocus required maxlength="64" pattern="[A-Za-z0-9._\\-]+"></label><label>密码 / 管理令牌<input name="token" type="password" autocomplete="current-password" required minlength="12"></label><small class="settings-hint">可使用管理员令牌，或使用管理员创建的个人账号密码登录。</small><button class="button primary" type="submit">登录</button></form></section></main>`;
  applyTheme();
  document.querySelector("[data-theme-toggle]").onclick = toggleTheme;
  document
    .querySelector("#login-form")
    .addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      const token = form.get("token");
      const button = event.currentTarget.querySelector("button");
      button.disabled = true;
      try {
        const session = await api("/auth/login", {
          method: "POST",
          body: JSON.stringify({ username: form.get("username"), token }),
        });
        state.data = {};
        state.session = session;
        location.hash = "#dashboard";
        await render();
      } catch (error) {
        renderLogin(error.message);
      }
    });
}

function notify(message, tone = "success") {
  const main = document.querySelector(".workspace-main");
  if (!main) return;
  main.querySelector("[data-spa-notice]")?.remove();
  const notice = document.createElement("div");
  notice.className = `alert ${tone}`;
  notice.dataset.spaNotice = "";
  notice.setAttribute("role", tone === "error" ? "alert" : "status");
  notice.textContent = message;
  main.prepend(notice);
  notice.scrollIntoView({ block: "nearest" });
}

function confirmAction(message, label = "确认继续") {
  const dialog = document.querySelector("[data-confirm-dialog]");
  if (!dialog?.showModal) return Promise.resolve(window.confirm(message));
  dialog.querySelector("[data-confirm-message]").textContent = message;
  dialog.querySelector("[data-confirm-accept]").textContent = label;
  state.confirmOpen = true;
  dialog.showModal();
  return new Promise((resolve) => {
    state.confirmResolver = resolve;
  });
}

function shell(content, title) {
  const previousMain = document.querySelector(".workspace-main");
  const previousRoute = document.body.className.match(/(?:^|\s)page-([^\s]+)/)?.[1];
  const preservedScroll = previousMain && previousRoute === state.route
    ? { top: previousMain.scrollTop, left: previousMain.scrollLeft }
    : null;
  const links = [
    ["dashboard", "总览", '<path d="M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z"/>'],
    [
      "agents",
      "内核预设",
      '<rect x="4" y="3.5" width="16" height="6" rx="2"/><rect x="4" y="14.5" width="16" height="6" rx="2"/><path d="M8 6.5h.01M8 17.5h.01M12 6.5h5M12 17.5h5"/>',
    ],
    [
      "node-settings",
      "节点设置",
      '<path d="M12 3.5a2 2 0 0 1 2 2v.4a6.8 6.8 0 0 1 1.6.9l.4-.2a2 2 0 1 1 2 3.5l-.4.2a7 7 0 0 1 0 1.8l.4.2a2 2 0 1 1-2 3.5l-.4-.2a6.8 6.8 0 0 1-1.6.9v.4a2 2 0 1 1-4 0v-.4a6.8 6.8 0 0 1-1.6-.9l-.4.2a2 2 0 1 1-2-3.5l.4-.2a7 7 0 0 1 0-1.8l-.4-.2a2 2 0 1 1 2-3.5l.4.2a6.8 6.8 0 0 1 1.6-.9v-.4a2 2 0 0 1 2-2Z"/><circle cx="12" cy="12" r="2.4"/>',
    ],
    [
      "client-access",
      "客户端",
      '<path d="M7 4.5h10a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-11a2 2 0 0 1 2-2Z"/><path d="M8.5 8.5h7M8.5 12h7M8.5 15.5h3"/><circle cx="16.5" cy="15.5" r="1"/>',
    ],
    ["live-config", "配置", '<path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h5"/>'],
    ["tasks", "任务", '<path d="M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"/>'],
    ["core-logs", "日志", '<path d="M4 5h16M4 10h16M4 15h10M4 20h7"/>'],
    ["traffic", "流量", '<path d="M4 17V7M8 14V5M12 19V9M16 15V3M20 18V11"/><path d="M3 20.5h18"/>'],
    [
      "settings",
      "设置",
      '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1a1.7 1.7 0 0 0 1.9.3A1.7 1.7 0 0 0 10 3V2.8h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1z"/>',
    ],
  ];
  const linkPermissions = {
    dashboard: "overview.read",
    agents: "agents.read",
    "node-settings": "agents.read",
    "client-access": "client-access.read",
    "live-config": "agent-config.read",
    tasks: "tasks.read",
    "core-logs": "core-logs.read",
    traffic: "traffic.read",
    settings: "settings.read",
  };
  links.splice(0, links.length, ...links.filter(([id]) => can(linkPermissions[id])));
  app.style.display = "";
  document.body.className = `app-body page-${state.route}${state.route === "node-settings" ? " page-agents no-context" : ""}`;
  applyTheme();
  const context = contextMarkup(title);
  const overview = state.data.overview || {};
  const panelName = state.data.settings?.panel_name || "QControlHub";
  const roleName = { admin: "管理员", user: "用户", operator: "用户", auditor: "用户", readonly: "用户" }[
    state.session.role
  ];
  const topAction =
    state.route === "dashboard"
      ? '<a class="button small" href="#node-settings">节点设置</a>'
      : state.route === "agents"
        ? ""
        : state.route === "node-settings"
          ? can("enrollment.manage")
            ? '<a class="button small" href="#enrollment">添加节点</a>'
            : ""
          : state.route === "client-access"
            ? '<a class="button small" href="#node-settings">返回节点设置</a>'
            : state.route === "archive-config"
              ? '<a class="button small" href="#live-config">节点实际配置</a>'
              : state.route === "agent-config"
                ? '<a class="button small" href="#agents">返回内核预设</a>'
                : state.route === "tasks"
                  ? '<button id="refresh" class="button small task-refresh-link" type="button">刷新</button>'
                  : state.route === "traffic" && can("traffic.manage")
                    ? '<a class="button small" href="#traffic-new">添加端口配额</a>'
                  : "";
  document.title = `${title} · ${panelName}`;
  app.innerHTML = `<div class="desktop-app"><aside class="app-dock"><a class="dock-logo" href="#dashboard" aria-label="${esc(panelName)} 总览"><span>QH</span></a><nav class="dock-nav" aria-label="主导航">${links.map(([id, text, icon]) => `<a class="${state.route === id || (state.route === "agent-config" && id === "agents") || (state.route === "archive-config" && id === "live-config") ? "active" : ""}" href="#${id}" title="${text}"><svg viewBox="0 0 24 24">${icon}</svg><span class="dock-label">${text}</span>${id === "agents" ? `<b data-online-count ${overview.agents_online ? "" : "hidden"}>${overview.agents_online || 0}</b>` : ""}${id === "live-config" && overview.node_configs ? `<b>${overview.node_configs}</b>` : ""}${id === "tasks" ? `<b class="hot" data-task-active-count ${overview.tasks_pending ? "" : "hidden"}>${overview.tasks_pending || 0}</b>` : ""}</a>`).join("")}</nav><div class="dock-tools"><button id="theme-toggle" data-theme-toggle type="button" aria-label="切换颜色主题" title="切换主题"><svg viewBox="0 0 24 24"><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6 7 7M17 17l1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4"/><circle cx="12" cy="12" r="4"/></svg><span class="dock-label">主题</span></button><button id="logout" type="button" aria-label="退出登录" title="退出登录"><svg viewBox="0 0 24 24"><path d="M10 4H5v16h5M14 8l4 4-4 4M8 12h10"/></svg><span class="dock-label">退出</span></button></div></aside><aside class="context-sidebar"><header class="context-brand"><a href="#dashboard"><span class="brand-mark">QH</span><strong>${esc(panelName)}</strong></a></header>${context}</aside><section class="workspace-shell"><header class="workspace-topbar"><div class="workspace-route"><span>${esc(panelName)}</span><i>/</i><b>${esc(title)}</b><i class="role-badge role-${esc(state.session.role)}">${esc(roleName)}</i></div><div class="workspace-actions"><span class="sync-state ${overview.agents_online ? "" : "inactive"}" data-sync-state><i></i><span data-sync-label>${overview.agents_online ? `${overview.agents_online} 个节点在线` : "等待节点连接"}</span></span>${topAction}</div></header><main class="workspace-main">${content}</main></section></div><dialog class="confirm-dialog" data-confirm-dialog aria-labelledby="confirm-dialog-title" aria-describedby="confirm-dialog-message"><div class="confirm-dialog-card"><span class="confirm-dialog-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 3.5 21 20H3zM12 9v5M12 17.5h.01"/></svg></span><div><p class="eyebrow">操作确认</p><h2 id="confirm-dialog-title">确认继续？</h2><p id="confirm-dialog-message" data-confirm-message></p></div><footer><button class="button" type="button" data-confirm-cancel>取消</button><button class="button danger-confirm" type="button" data-confirm-accept>确认继续</button></footer></div></dialog>`;
  if (preservedScroll) {
    const nextMain = document.querySelector(".workspace-main");
    requestAnimationFrame(() => {
      nextMain?.scrollTo({
        top: preservedScroll.top,
        left: preservedScroll.left,
        behavior: "auto",
      });
    });
  }
  applyTheme();
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
    state.data = {};
    renderLogin();
  };
  if (document.querySelector("#refresh"))
    document.querySelector("#refresh").onclick = () => render();
  document.querySelector("#theme-toggle").onclick = toggleTheme;
  document.querySelector("#mobile-theme-toggle").onclick =
    document.querySelector("#theme-toggle").onclick;
  document.querySelector("#mobile-logout").onclick =
    document.querySelector("#logout").onclick;
  document.querySelectorAll("[data-context-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.selectedAgent = link.dataset.contextAgent;
    };
  });
  const confirmDialog = document.querySelector("[data-confirm-dialog]");
  const finishConfirm = (accepted) => {
    if (!state.confirmResolver) return;
    const resolve = state.confirmResolver;
    state.confirmResolver = null;
    state.confirmOpen = false;
    confirmDialog.close();
    resolve(accepted);
  };
  confirmDialog.querySelector("[data-confirm-cancel]").onclick = () =>
    finishConfirm(false);
  confirmDialog.querySelector("[data-confirm-accept]").onclick = () =>
    finishConfirm(true);
  confirmDialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    finishConfirm(false);
  });
}

function contextMarkup(title) {
  if (state.route === "dashboard")
    return `<nav class="context-menu" aria-label="总览目录"><a class="active" href="#summary"><span>01</span>运行概览</a><a href="#fleet"><span>02</span>节点状态</a><a href="#activity"><span>03</span>最近活动</a></nav><section class="context-metrics"><div><span>在线 / 全部节点</span><b>${state.data.overview?.agents_online || 0} / ${state.data.overview?.agents || 0}</b></div><div><span>节点版本 / 独立档案</span><b>${state.data.overview?.node_configs || 0} / ${state.data.overview?.configs || 0}</b></div><div><span>准备中 / 执行中</span><b>${state.data.overview?.tasks_queued || 0} / ${state.data.overview?.tasks_running || 0}</b></div></section>`;
  if (state.route === "agents") {
    const items = state.data.agents || [];
    return `<div class="context-section-label"><span>内核配置预设</span><b>${items.length}</b></div><nav class="context-list" aria-label="节点内核预设">${items.map((agent) => `<a class="${state.data.selectedAgent === agent.id ? "active" : ""}" href="#node-${esc(agent.id)}" data-context-agent="${esc(agent.id)}"><span class="context-engine">${(agent.capabilities || []).length}</span><span><strong>${esc(agent.name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "node-settings") {
    // The node overview cards replace the per-node sidebar list; the whole
    // context column is hidden on this route via the no-context body class.
    return "";
  }
  if (state.route === "client-access") {
    const items = state.data.agents || [];
    const entries = state.data.clientAccessEntries || [];
    return `<a class="context-back" href="#node-settings">← 返回节点设置</a><a class="context-primary ${state.data.accessAgent ? "" : "active"}" href="#client-access" data-access-agent="">全部客户端配置</a><div class="context-section-label"><span>按节点查看</span><b>${items.length}</b></div><nav class="context-list" aria-label="客户端配置节点">${items.map((agent) => { const profiles = entries.filter((entry) => entry.agent_id === agent.id).reduce((total, entry) => total + (entry.profiles || []).length, 0); return `<a class="${state.data.accessAgent === agent.id ? "active" : ""}" href="#client-access" data-access-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${profiles ? `${profiles} 个客户端入站` : "尚无客户端配置"}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`; }).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "live-config") {
    const items = state.data.agents || [];
    const selected = items.find((agent) => agent.id === state.data.liveAgent);
    const capabilities = (selected?.capabilities || []).filter(
      (engine) => selected.runtime?.[engine]?.installed,
    );
    return `<div class="context-section-label"><span>选择节点</span><b>${items.length}</b></div><nav class="context-list" aria-label="配置节点">${items.map((agent) => { const installed = installedEngineCount(agent); return `<a class="${agent.id === state.data.liveAgent ? "active" : ""}" href="#live-config" data-live-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${installed ? `${installed} 个已安装内核` : "尚未安装内核"}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`; }).join("") || "<p>还没有节点</p>"}</nav>${selected ? `<div class="context-section-label"><span>选择内核</span><b>${capabilities.length}</b></div><nav class="context-list config-context-list">${capabilities.map((engine) => `<a class="${engine === state.data.liveEngine ? "active" : ""}" href="#live-config" data-live-engine="${esc(engine)}"><span class="context-engine ${esc(engine)}">${esc(engineName(engine))}</span><span><strong>${esc(engineName(engine))}</strong><small>节点实际文件</small></span></a>`).join("")}</nav>` : ""}<a class="context-primary" href="#archive-config">配置档案 →</a>`;
  }
  if (state.route === "archive-config") {
    const items = state.data.configs || [];
    return `${can("configs.write") ? '<a class="context-primary" id="new-config" href="#new-config">＋ 新建配置档案</a>' : ""}<div class="context-section-label"><span>配置档案</span><b>${items.length}</b></div><nav class="context-list config-context-list">${items.map((item) => `<a class="${item.id === state.data.archiveConfigId ? "active" : ""}" href="#archive-config" data-archive-config="${esc(item.id)}"><span class="context-engine ${esc(item.engine)}">${esc(engineName(item.engine))}</span><span><strong>${esc(item.name)}</strong><small>v${item.version} · ${esc(ago(item.updated_at))}</small></span></a>`).join("") || "<p>还没有保存的配置</p>"}</nav><a class="context-primary" href="#live-config">← 节点实际配置</a>`;
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
  if (state.route === "core-logs") {
    const agents = state.data.agents || [];
    const selected = state.data.coreLogFilters?.agent_id || "";
    return `<a class="context-primary ${selected ? "" : "active"}" href="#core-logs" data-core-log-agent="">全部节点日志</a><div class="context-section-label"><span>按节点查看</span><b>${agents.length}</b></div><nav class="context-list" aria-label="内核日志节点">${agents.map((agent) => `<a class="${selected === agent.id ? "active" : ""}" href="#core-logs" data-core-log-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${(agent.features || []).includes("core-logs-v1") ? "集中日志已启用" : "需升级 Agent"}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "traffic") {
    const agents = state.data.agents || [];
    const policies = state.data.trafficPolicies || [];
    return `${can("traffic.manage") ? '<a class="context-primary" href="#traffic-new">＋ 添加端口配额</a>' : ""}<div class="context-section-label"><span>按节点查看</span><b>${agents.length}</b></div><nav class="context-list" aria-label="端口流量节点">${agents.map((agent) => { const count = policies.filter((policy) => policy.agent_id === agent.id).length; const blocked = policies.filter((policy) => policy.agent_id === agent.id && policy.blocked).length; return `<a href="#traffic-agent-${esc(agent.id)}" data-context-traffic-agent="${esc(agent.id)}"><i class="status-dot ${blocked ? "bad" : agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${count ? `${count} 个端口${blocked ? ` · ${blocked} 个封禁` : ""}` : "尚未监控端口"}</small></span></a>`; }).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "settings")
    return `<nav class="context-menu" aria-label="设置目录"><a class="active" href="#identity"><span>01</span>面板标识</a><a href="#defaults"><span>02</span>操作默认值</a><a href="#synchronization"><span>03</span>状态同步</a><a href="#notifications"><span>04</span>事件通知</a>${can("users.manage") ? '<a href="#users"><span>05</span>用户管理</a>' : ""}</nav>`;
  const agent = (state.data.agents || []).find(
    (item) => item.id === state.data.agentId,
  );
  const caps = agent?.capabilities || engines;
  const installed = installedEngineCount(agent);
  return `<a class="context-back" href="#agents">← 返回内核预设</a><div class="context-section-label"><span>选择内核</span><b>${installed}/${caps.length}</b></div><nav class="context-list engine-context-list">${caps.map((engine) => `<a class="${state.data.engine === engine ? "active" : ""}" href="#agent-config" data-engine-select="${esc(engine)}"><span class="context-engine ${esc(engine)}">${esc(engineName(engine))}</span><span><strong>${esc(engineName(engine))}</strong><small>${agent?.runtime?.[engine]?.installed ? "服务端入站" : "尚未安装"}</small></span></a>`).join("")}</nav><ol class="context-steps"><li class="active"><b>1</b><span>选择入站</span></li><li><b>2</b><span>编辑参数</span></li><li><b>3</b><span>校验或部署</span></li></ol>`;
}

const dashboard = installDashboard({ api, state, esc, engineName, heartbeat, statusTone, ago, short, actionName, shell });

const agentModule = installAgents({ api, optionalAPI, state, engines, can, esc, engineName, statusTone, serviceStatusName, short, date, ago, heartbeat, percent, bytes, conciseVersion, rate, actionName, serviceActionDisabled, trafficChart, renderConfigDiff, notify, confirmAction, shell });
const { agents, nodeSettings, submitTask, bindCodeEditors, showCommand } = agentModule;

const clientAccess = installClientAccess({ api, state, engines, esc, engineName, short, can, notify, shell });

const configModule = installConfigPages({ api, optionalAPI, state, engines, can, esc, engineName, conciseVersion, date, ago, bytes, confirmAction, notify, shell, submitTask, bindCodeEditors });
const { agentConfig, liveConfig, archiveConfigs } = configModule;

const tasks = installTasks({ api, state, actions, can, esc, statusName, engineName, short, date, ago, actionName, statusTone, notify, confirmAction, shell });
const coreLogs = installCoreLogs({ api, state, engines, can, esc, engineName, date, shell });
const traffic = installTraffic({ api, state, can, esc, engineName, bytes, rate, percent, ago, shell, notify, confirmAction });
const settings = installSettings({ api, state, esc, date, can, shell, notify, confirmAction });

async function render() {
  if (state.busy) return;
  clearTimeout(state.taskPollTimer);
  clearTimeout(state.trafficPollTimer);
  clearTimeout(state.coreLogPollTimer);
  state.busy = true;
  const hash = location.hash.slice(1);
  const routeMap = {
    summary: "dashboard",
    fleet: "dashboard",
    activity: "dashboard",
    enrollment: "node-settings",
    "client-access": "client-access",
    identity: "settings",
    defaults: "settings",
    synchronization: "settings",
    notifications: "settings",
    users: "settings",
    "preset-node": "agents",
    "settings-node": "node-settings",
    "new-config": "archive-config",
    templates: "archive-config",
    archive: "archive-config",
    "traffic-new": "traffic",
  };
  state.route = [
    "dashboard",
    "agents",
    "node-settings",
    "agent-config",
    "client-access",
    "live-config",
    "archive-config",
    "tasks",
    "core-logs",
    "traffic",
    "settings",
  ].includes(hash)
    ? hash
    : routeMap[hash] ||
      (hash.startsWith("preset-node-")
        ? "agents"
        : hash.startsWith("settings-node-")
          ? "node-settings"
            : hash.startsWith("node-")
              ? "node-settings"
              : hash.startsWith("traffic-agent-")
                ? "traffic"
            : hash.startsWith("config-")
              ? "archive-config"
              : "dashboard");
  state.anchor = hash;
  if (hash.startsWith("preset-node-")) state.data.selectedAgent = hash.slice(12);
  if (hash.startsWith("settings-node-")) state.data.selectedAgent = hash.slice(14);
  if (hash.startsWith("node-")) state.data.selectedAgent = hash.slice(5);
  if (hash.startsWith("config-")) state.data.archiveConfigId = hash.slice(7);
  try {
    if (!state.session && !(await ensureSession())) {
      renderLogin();
      return;
    }
    [state.data.overview, state.data.settings] = await Promise.all([
      can("overview.read") ? api("/overview") : Promise.resolve({}),
      can("settings.read") ? api("/settings") : Promise.resolve({}),
    ]);
    const pages = {
      dashboard,
      agents,
      "node-settings": nodeSettings,
      "client-access": clientAccess,
      "agent-config": agentConfig,
      "live-config": liveConfig,
      "archive-config": archiveConfigs,
      tasks,
      "core-logs": coreLogs,
      traffic,
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
