import { bindEvent, reconcileView } from "./refresh.js";
import { setStorageAccount } from "./account-storage.js";
import { dockIcons } from "./shell-icons.js";
import { createShellContext } from "./shell-context.js";

export function createShellView(ctx) {
  const { app, state, can, esc, api, applyTheme, toggleTheme, render, renderLogin, bindConfirmationDialog } = ctx;
  const contextMarkup = createShellContext(ctx);
function shell(content, title, { viewKey = state.route } = {}) {
  const previousMain = document.querySelector(".workspace-main");
  const previousRoute = document.body.className.match(/(?:^|\s)page-([^\s]+)/)?.[1];
  const pendingShares = (state.data.agentAccess?.shares || []).filter(share => share.enabled && share.status === "pending").length;
  const links = [
    ["dashboard", "总览", dockIcons.layoutDashboard],
    ["ip-quality", "IP 质量", dockIcons.chart, true],
    ["node-settings", "节点", dockIcons.server],
    ["live-config", "配置", dockIcons.fileCode],
    ["client-access", "客户端", dockIcons.monitorSmartphone],
    ["substore-sync", "同步", dockIcons.refreshCw, true],
    ["system-bbr", "TCP 调优", dockIcons.sliders, true],
    ["traffic", "流量", dockIcons.chart, true],
    ["core-logs", "日志", dockIcons.logs, true],
    ["tasks", "任务", dockIcons.listChecks, true],
    [state.session.role === "admin" ? "users" : "my-quota", state.session.role === "admin" ? "用户" : "共享", dockIcons.users, true],
  ];
  const linkPermissions = {
    dashboard: "overview.read",
    "ip-quality": "agents.read",
    agents: "agents.read",
    "node-settings": "agents.read",
    "client-access": "client-access.read",
    "substore-sync": "client-access.read",
    "access-control": "agent-config.read",
    "system-bbr": "system-bbr.read",
    "live-config": "agent-config.read",
    tasks: "tasks.read",
    "core-logs": "core-logs.read",
    traffic: "traffic.read",
    settings: "settings.read",
    users: "users.manage",
    "my-quota": "agent-access.read",
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
  const activeDockRoute = (id) =>
    state.route === id ||
    (state.route === "agent-config" && id === "live-config") ||
    (state.route === "archive-config" && id === "live-config");
  const nodeOverviewActions =
    state.route === "node-settings" && state.data.nodeView !== "detail"
      ? `${can("host.manage") && (state.data.agents || []).length > 1 ? `<button class="button small ${state.data.nodeBatchMode ? "primary" : ""}" type="button" data-node-batch-toggle aria-pressed="${state.data.nodeBatchMode ? "true" : "false"}">${state.data.nodeBatchMode ? "退出批量" : "批量操作"}</button>` : ""}${can("enrollment.manage") ? '<button class="button small" type="button" data-open-enrollment>添加节点</button>' : ""}${state.session.role === "admin" ? '<button class="button small" type="button" data-open-agent-directory>其他用户节点</button>' : ""}`
      : "";
  const topAction =
    state.route === "dashboard"
      ? (can("agents.read") ? '<a class="button small" href="#node-settings">节点设置</a>' : "")
      : state.route === "agents"
        ? ""
        : state.route === "node-settings"
          ? nodeOverviewActions
          : state.route === "client-access"
            ? ""
            : state.route === "substore-sync"
              ? '<a class="button small" href="#client-access">客户端配置</a>'
            : state.route === "archive-config"
              ? '<a class="button small" href="#live-config">节点实际配置</a>'
              : state.route === "agent-config"
                ? '<a class="button small" href="#live-config">返回配置</a>'
                : state.route === "tasks"
                  ? '<button id="refresh" class="button small task-refresh-link" type="button">刷新</button>'
                  : "";
  const navigationMarkup = links
    .map(([id, text, icon, mobileSecondary]) => {
      const active = activeDockRoute(id);
      return `<a class="${active ? "active" : ""}${mobileSecondary ? " dock-mobile-secondary" : ""}" href="#${id}" title="${text}" ${active ? 'aria-current="page"' : ""}><svg viewBox="0 0 24 24" aria-hidden="true">${icon}</svg><span class="dock-label">${text}</span>${id === "agents" ? `<b data-online-count ${overview.agents_online ? "" : "hidden"}>${overview.agents_online || 0}</b>` : ""}${id === "live-config" && overview.node_configs ? `<b>${overview.node_configs}</b>` : ""}${id === "tasks" ? `<b class="hot" data-task-active-count ${overview.tasks_pending ? "" : "hidden"}>${overview.tasks_pending || 0}</b>` : ""}${id === "my-quota" && pendingShares ? `<b aria-label="${pendingShares} 个待接受邀请">${pendingShares}</b>` : ""}</a>`;
    })
    .join("");
  const settingsActive = activeDockRoute("settings");
  const settingsDockLink = can(linkPermissions.settings)
    ? `<a class="dock-settings ${settingsActive ? "active" : ""}" href="#settings" title="设置" ${settingsActive ? 'aria-current="page"' : ""}><svg viewBox="0 0 24 24" aria-hidden="true">${dockIcons.settings}</svg><span class="dock-label">设置</span></a>`
    : "";
  const mobileMoreRoutes = [
    ["substore-sync", "Sub-Store 同步"],
    ["ip-quality", "IP 质量检测"],
    ["access-control", "访问限制"],
    ["system-bbr", "BBR / TCP 调优"],
    ["traffic", "流量"],
    ["core-logs", "日志"],
    ["tasks", "任务"],
    ["settings", "设置"],
    [state.session.role === "admin" ? "users" : "my-quota", state.session.role === "admin" ? "用户" : `共享与额度${pendingShares ? ` · ${pendingShares}` : ""}`],
  ];
  const mobileMoreActive = mobileMoreRoutes.some(([id]) => activeDockRoute(id));
  const mobileMoreLinks = mobileMoreRoutes
    .filter(([id]) => can(linkPermissions[id]))
    .map(([id, text]) => `<a class="${activeDockRoute(id) ? "active" : ""}" href="#${id}" ${activeDockRoute(id) ? 'aria-current="page"' : ""}>${text}</a>`)
    .join("");
  const mobileAccountMarkup = `<details class="mobile-account-menu"><summary class="${mobileMoreActive ? "active" : ""}" aria-label="打开更多导航与账户操作" title="更多"><svg viewBox="0 0 24 24" aria-hidden="true">${dockIcons.more}</svg><span>更多</span></summary><div>${mobileMoreLinks}<button type="button" id="mobile-theme-toggle">切换主题</button><button type="button" id="mobile-logout">退出登录</button></div></details>`;
  document.title = `${title} · ${panelName}`;
  const routeChanged = !previousMain || previousRoute !== state.route;
  const workspaceKey = `workspace-${viewKey}`;
  const viewChanged =
    !previousMain || previousMain.dataset.refreshKey !== workspaceKey;
  const firstScreenClass = !previousMain ? " first-screen" : "";
  const motionClass =
    routeChanged || viewChanged ? ` page-enter${firstScreenClass}` : "";
  const contextKey = `${state.route}|${state.data.selectedAgent || ""}|${state.data.agentId || ""}|${state.data.engine || ""}|${state.data.liveAgent || ""}|${state.data.liveEngine || ""}`;
  const contextChanged = routeChanged || state.data.contextMotionKey !== contextKey;
  state.data.contextMotionKey = contextKey;
  const contextMotionClass = contextChanged ? " context-enter" : "";
  const markup = `<div class="desktop-app"><aside class="app-dock"><a class="dock-logo" href="#dashboard" aria-label="${esc(panelName)} 总览"><span>QH</span></a><nav class="dock-nav" aria-label="主导航">${navigationMarkup}</nav><div class="dock-tools">${settingsDockLink}<button id="theme-toggle" data-theme-toggle type="button" aria-label="切换颜色主题" title="切换主题"><svg viewBox="0 0 24 24" aria-hidden="true">${dockIcons.sun}</svg><span class="dock-label">主题</span></button><button id="logout" type="button" aria-label="退出登录" title="退出登录"><svg viewBox="0 0 24 24" aria-hidden="true">${dockIcons.logOut}</svg><span class="dock-label">退出</span></button></div>${mobileAccountMarkup}</aside><aside class="context-sidebar${contextMotionClass}" data-refresh-key="context-${esc(contextKey)}"><header class="context-brand"><a href="#dashboard"><span class="brand-mark">QH</span><strong>${esc(panelName)}</strong></a></header>${context}</aside><section class="workspace-shell"><header class="workspace-topbar"><div class="workspace-route"><span>${esc(panelName)}</span><i>/</i><b>${esc(title)}</b><i class="role-badge role-${esc(state.session.role)}">${esc(roleName)}</i></div><div class="workspace-actions"><span class="sync-state ${overview.agents_online ? "" : "inactive"}" data-sync-state><i></i><span data-sync-label>${overview.agents_online ? `${overview.agents_online} 个节点在线` : "等待节点连接"}</span></span>${topAction}</div></header><main class="workspace-main${motionClass}" data-refresh-key="workspace-${esc(viewKey)}">${content}</main></section></div><dialog class="confirm-dialog" data-confirm-dialog aria-labelledby="confirm-dialog-title" aria-describedby="confirm-dialog-message"><div class="confirm-dialog-card"><span class="confirm-dialog-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 3.5 21 20H3zM12 9v5M12 17.5h.01"/></svg></span><div><p class="eyebrow">操作确认</p><h2 id="confirm-dialog-title" data-confirm-title>确认继续？</h2><p id="confirm-dialog-message" data-confirm-message></p></div><footer><button class="button" type="button" data-confirm-cancel>取消</button><button class="button danger-confirm" type="button" data-confirm-accept>确认继续</button></footer></div></dialog>`;
  const currentView = app.querySelector(":scope > .desktop-app");
  if (previousMain && previousRoute === state.route && currentView) {
    const template = document.createElement("template");
    template.innerHTML = markup;
    const metrics = { inserted: 0, removed: 0, replaced: 0, updated: 0 };
    reconcileView(currentView, template.content.firstElementChild, { metrics });
    state.data.refreshMetrics ||= {};
    state.data.refreshMetrics[state.route] = metrics;
  } else {
    app.innerHTML = markup;
  }
  const renderedMain = app.querySelector(".workspace-main");
  if (renderedMain) renderedMain.inert = false;
  renderedMain?.classList.remove("is-route-pending");
  renderedMain?.removeAttribute("aria-busy");
  applyTheme();
  document.querySelector("#logout").onclick = async () => {
    try {
      await api("/auth/logout", { method: "POST" });
    } catch {}
    state.session = null;
    setStorageAccount(null);
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
  bindEvent(document, "click", (event) => {
    const menu = document.querySelector(".mobile-account-menu[open]");
    if (menu && !menu.contains(event.target)) menu.open = false;
  });
  bindEvent(document, "keydown", (event) => {
    if (event.key !== "Escape") return;
    const menu = document.querySelector(".mobile-account-menu[open]");
    if (menu) menu.open = false;
  });
  document.querySelectorAll("[data-context-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.selectedAgent = link.dataset.contextAgent;
    };
  });
  bindConfirmationDialog();
}

  return shell;
}
