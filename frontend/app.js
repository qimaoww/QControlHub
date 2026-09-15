import { createLatestRenderScheduler } from "./modules/refresh.js";
import { createRouteModuleLoader } from "./modules/route-loader.js";
import { errorMessage } from "./modules/errors.js";
import { readPresetRoute } from "./modules/preset-route.js";
import { createDisplayHelpers } from "./modules/formatting.js";
import { createPermissionChecker } from "./modules/permissions.js";
import { createServiceActionGuard } from "./modules/agent-service-state.js";
import { createSessionAPI } from "./modules/session-api.js";
import { createShellAppearance } from "./modules/shell-appearance.js";
import { createShellFeedback } from "./modules/shell-feedback.js";
import { createShellView } from "./modules/shell-view.js";
import { createLoginPage } from "./modules/login-page.js";
import { routeForHash, routeModuleNames } from "./modules/routes.js";
import { createRouteWarmup } from "./modules/route-warmup.js";

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
  "read-managed-config",
  "import-existing",
  "upgrade-agent",
  "enable-bbr",
  "disable-bbr",
  "configure-tcp",
];
const state = {
  session: null,
  route: location.hash.slice(1) || "dashboard",
  data: {},
  navigationEpoch: 0,
  confirmResolver: null,
  confirmOpen: false,
};
let routeController = null;

const { esc, date, bytes, label, actionName, statusName, engineName, serviceStatusName, short, statusTone, percent, ago, heartbeat, conciseVersion, rate, trafficChart, renderConfigDiff } = createDisplayHelpers(state);
const can = createPermissionChecker(state);
const serviceActionDisabled = createServiceActionGuard(can);
const { scopedAPI, api, optionalAPI, ensureSession, readPanelData, invalidatePanelReads } =
  createSessionAPI({ state, renderLogin });
const { applyUIFontScale, applyTheme, toggleTheme } = createShellAppearance(state);
const { notify, confirmAction, bindConfirmationDialog } = createShellFeedback(state);
const shell = createShellView({
  app, state, can, esc, engineName, ago, engines, api,
  applyTheme, toggleTheme, bindConfirmationDialog, renderLogin,
  render: () => render(),
});
const loginPage = createLoginPage({
  app, state, esc, api, applyUIFontScale, applyTheme, toggleTheme, renderLogin,
  render: () => render(),
});

function renderLogin(message = "") {
  stopRouteWarmup();
  routeModules.peek("users")?.closeInvitation();
  scopedAPI.end();
  if (state.route === "node-settings")
    routeModules.peek("agents")?.cancelAgentInteractions();
  const confirmResolver = state.confirmResolver;
  state.confirmResolver = null;
  state.confirmOpen = false;
  confirmResolver?.(false);
  routeController?.abort();
  routeController = null;
  state.routeSignal = null;
  state.navigationEpoch += 1;
  state.route = "login";
  clearTimeout(state.taskPollTimer);
  clearTimeout(state.trafficPollTimer);
  clearTimeout(state.coreLogPollTimer);
  clearTimeout(state.agentPollTimer);
  loginPage(message);
}

const routeModules = createRouteModuleLoader({
  async dashboard() {
    const { installDashboard } = await import("./modules/dashboard.js");
    return installDashboard({
      api, state, can, esc, engineName, heartbeat, statusTone, ago, short,
      actionName, bytes, rate, shell,
    });
  },
  async "ip-quality"() {
    const { installIPQuality } = await import("./modules/ip-quality.js");
    return installIPQuality({ api, state, shell });
  },
  async agents() {
    const { installAgents } = await import("./modules/agents.js");
    return installAgents({
      api, optionalAPI, state, engines, can, esc, engineName, statusTone,
      serviceStatusName, short, date, ago, heartbeat, percent, bytes,
      conciseVersion, rate, actionName, serviceActionDisabled, trafficChart,
      renderConfigDiff, notify, confirmAction, shell,
    });
  },
  async "client-access"() {
    const { installClientAccess } = await import("./modules/client-access.js");
    return installClientAccess({
      api, state, engines, esc, engineName, short, can, notify, shell,
    });
  },
  async "substore-sync"() {
    const { installSubStoreSync } = await import("./modules/substore-sync.js");
    return installSubStoreSync({
      api, state, can, esc, engineName, notify, shell,
    });
  },
  async configs() {
    const [{ installConfigPages }, agentModule] = await Promise.all([
      import("./modules/configs.js"),
      routeModules.load("agents"),
    ]);
    return installConfigPages({
      api, optionalAPI, state, engines, can, esc, engineName, conciseVersion,
      date, ago, bytes, confirmAction, notify, shell,
      submitTask: agentModule.submitTask,
      bindCodeEditors: agentModule.bindCodeEditors,
      renderConfigDiff,
    });
  },
  async tasks() {
    const { installTasks } = await import("./modules/tasks.js");
    return installTasks({
      api, state, actions, can, esc, statusName, engineName, short, date, ago,
      actionName, statusTone, notify, confirmAction, shell,
    });
  },
  async "core-logs"() {
    const { installCoreLogs } = await import("./modules/core-logs.js");
    return installCoreLogs({
      api, state, engines, can, esc, engineName, date, shell,
    });
  },
  async traffic() {
    const { installTraffic } = await import("./modules/traffic.js");
    return installTraffic({
      api, state, can, esc, engineName, bytes, rate, percent, ago, shell,
      notify, confirmAction,
    });
  },
  async "access-control"() {
    const { installAccessControl } = await import("./modules/access-control.js");
    return installAccessControl({
      api, state, can, esc, engineName, shell, notify, confirmAction,
    });
  },
  async "system-bbr"() {
    const { installSystemBBR } = await import("./modules/system-bbr.js");
    return installSystemBBR({
      api, state, can, esc, date, shell, notify, confirmAction,
    });
  },
  async settings() {
    const { installSettings } = await import("./modules/settings.js");
    return installSettings({
      api, state, esc, date, can, shell, notify, confirmAction,
      applyUIFontScale, invalidatePanelReads,
    });
  },
  async users() {
    const { installUsers } = await import("./modules/users.js");
    return installUsers({ api, state, esc, shell, notify, confirmAction });
  },
});

const { bindNavigationPreload, scheduleRouteWarmup, stopRouteWarmup } = createRouteWarmup(routeModules);
bindNavigationPreload();

function renderRouteModule(route, module, options) {
  if (route === "agents") return module.agents(options);
  if (route === "node-settings") return module.nodeSettings(false, options);
  if (route === "agent-config") return module.agentConfig(options);
  if (route === "live-config") return module.liveConfig(options);
  if (route === "archive-config") return module.archiveConfigs(options);
  if (route === "users") return module.users(options);
  if (route === "my-quota") return module.myQuota(options);
  return module(options);
}

async function renderOnce() {
  routeModules.peek("users")?.closeInvitation();
  const previousRoute = state.route;
  routeController?.abort();
  routeController = new AbortController();
  state.routeSignal = routeController.signal;
  const renderSignal = state.routeSignal;
  scopedAPI.begin();
  clearTimeout(state.taskPollTimer);
  clearTimeout(state.trafficPollTimer);
  clearTimeout(state.coreLogPollTimer);
  clearTimeout(state.bbrPollTimer);
  clearTimeout(state.agentPollTimer);
  const presetSelection = readPresetRoute(location.hash);
  let hash = presetSelection ? "live-config" : location.hash.slice(1);
  if (presetSelection) {
    if (state.data.agentId !== presetSelection.agentId || state.data.engine !== presetSelection.engine) {
      Object.assign(state.data, {protocol:"", inboundTag:"", configField:"", configInboundField:""});
    }
    Object.assign(state.data, presetSelection);
    if (state.data.liveAgent !== presetSelection.agentId || state.data.liveEngine !== presetSelection.engine)
      state.data.liveConfigSource = "";
    Object.assign(state.data, {liveAgent:presetSelection.agentId, liveEngine:presetSelection.engine});
  }
  if (hash === "agents" || hash === "agent-config" || hash === "preset-node" || hash.startsWith("preset-node-")) {
    if (hash === "agent-config") {
      state.data.liveAgent = state.data.agentId || state.data.liveAgent;
      state.data.liveEngine = state.data.engine || state.data.liveEngine;
    } else if (hash.startsWith("preset-node-")) {
      state.data.liveAgent = hash.slice(12);
      state.data.liveEngine = "";
    }
    state.data.liveConfigSource = "";
    hash = "live-config";
  }
  if (hash === "live-config" && !location.hash.startsWith("#live-config")) {
    const params = new URLSearchParams();
    if (state.data.liveAgent) params.set("agent", state.data.liveAgent);
    if (state.data.liveEngine) params.set("engine", state.data.liveEngine);
    history.replaceState(null, "", `#live-config${params.size ? `?${params}` : ""}`);
  }
  state.route = routeForHash(hash);
  state.anchor = hash;
  if (previousRoute === "node-settings" && state.route !== previousRoute) {
    state.data.nodeBatchMode = false;
    routeModules.peek("agents")?.cancelAgentInteractions();
  }
  if (hash.startsWith("preset-node-")) state.data.selectedAgent = hash.slice(12);
  if (hash.startsWith("settings-node-")) state.data.selectedAgent = hash.slice(14);
  if (hash.startsWith("node-")) state.data.selectedAgent = hash.slice(5);
  if (
    state.route === "node-settings" &&
    (hash.startsWith("settings-node-") ||
      (hash.startsWith("node-") && hash !== "node-settings"))
  ) {
    state.data.nodeBatchMode = false;
    document.querySelectorAll("[data-open-enrollment], [data-node-batch-toggle], .node-batch-bar").forEach((element) => element.remove());
  }
  if (hash.startsWith("config-")) state.data.archiveConfigId = hash.slice(7);
  try {
    if (!state.session && !(await ensureSession())) {
      renderLogin();
      return;
    }
    if (state.session.role !== "admin") {
      const access = await api("/agent-access");
      if (renderSignal.aborted) return;
      state.data.agentAccess = access;
      if (state.route === "users" ||
        (state.route === "dashboard" && !can("overview.read")))
        state.route = "my-quota";
    } else {
      state.data.agentAccess = { isolated: false, shares: [] };
    }
    const route = state.route;
    const routeModulePromise = routeModules.load(
      routeModuleNames[route] || "dashboard",
    );
    // Keep the module fetch concurrent with API reads without reporting an
    // unhandled rejection if the network fails before those reads settle.
    void routeModulePromise.catch(() => {});
    // Start common page reads while the shell settings/overview are loading.
    // The page consumes these same promises through the render-local scope.
    // Pages with their own refresh AbortSignal must start their own request;
    // prefetching those here would create an unshareable duplicate read.
    const agentRoutes = ["dashboard", "agents", "node-settings", "archive-config"];
    if (agentRoutes.includes(state.route) && can("agents.read"))
      void api("/agents").catch(() => {});
    if (state.route === "dashboard" && can("tasks.read"))
      void api("/tasks?limit=7").catch(() => {});
    if (state.route === "settings")
      void api("/settings/deployment").catch(() => {});
    if (state.route === "agent-config" && state.data.agentId && state.data.engine && can("agent-config.read"))
      void api(`/agents/${encodeURIComponent(state.data.agentId)}/configs/${encodeURIComponent(state.data.engine)}/workspace`).catch(() => {});
    const hasSharedData =
      state.data.overview !== undefined && state.data.settings !== undefined;
    const sharedDataPromise = Promise.all([
      can("overview.read")
        ? readPanelData("overview", { live: state.route === "dashboard" })
        : Promise.resolve({}),
      can("settings.read")
        ? readPanelData("settings", { live: state.route === "settings" })
        : Promise.resolve({}),
    ]);
    if (!hasSharedData) {
      [state.data.overview, state.data.settings] = await sharedDataPromise;
    } else {
      sharedDataPromise
        .then(([overview, settings]) => {
          if (renderSignal.aborted || state.routeSignal !== renderSignal || !state.session) return;
          state.data.overview = overview;
          state.data.settings = settings;
        })
        .catch(() => {});
    }
    const routeModule = await routeModulePromise;
    if (renderSignal.aborted) return;
    await renderRouteModule(route, routeModule, {
      overview: state.data.overview,
      settings: state.data.settings,
    });
    scheduleRouteWarmup(route);
    if (state.anchor === "new-config")
      document.querySelector("#new-config")?.click();
    else if (state.anchor && state.anchor !== state.route)
      requestAnimationFrame(() =>
        document
          .getElementById(state.anchor)
          ?.scrollIntoView({ block: "start" }),
      );
  } catch (error) {
    if (error?.name === "AbortError") return;
    if (!state.session || state.route === "login") return;
    const renderedRoute = document.body.className.match(
      /(?:^|\s)page-([^\s]+)/,
    )?.[1];
    if (renderedRoute === state.route) notify(error.message, "error");
    else
      shell(
        `<section class="section"><div class="alert error">${esc(errorMessage(error.message))}</div></section>`,
        "错误",
      );
  } finally {
    scopedAPI.end();
    // A same-page load error keeps the existing editor. Always release its
    // transition indicator, but never clear a newer navigation's state.
    if (state.routeSignal === renderSignal && !renderSignal.aborted) {
      const main = app.querySelector(".workspace-main");
      if (main) main.inert = false;
      main?.classList.remove("is-route-pending");
      main?.removeAttribute("aria-busy");
    }
  }
}
const scheduleRender = createLatestRenderScheduler(renderOnce, {
  cancelActive: () => routeController?.abort(),
});
function primeRouteTransition() {
  if (!state.session) return;
  const main = app.querySelector(".workspace-main");
  if (!main) return;
  main.inert = true;
  main.classList.add("is-route-pending");
  main.setAttribute("aria-busy", "true");
}
const render = () => {
  stopRouteWarmup();
  routeModules.peek("configs")?.capturePresetDrafts();
  routeModules.peek("users")?.captureDraft();
  primeRouteTransition();
  state.navigationEpoch += 1;
  return scheduleRender();
};
window.addEventListener("hashchange", render);
window.addEventListener("beforeunload", event => {
  const configModule = routeModules.peek("configs");
  const userModule = routeModules.peek("users");
  const agentModule = routeModules.peek("agents");
  userModule?.captureDraft();
  if (!configModule?.configHasUnsavedChanges() && !userModule?.hasUnsavedChanges() && !agentModule?.sharingHasUnsavedChanges()) return;
  event.preventDefault();
  event.returnValue = "";
});
render();
