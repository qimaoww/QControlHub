import { updateFeedback, removePresented } from "./presence-motion.js";
import { presetRoute } from "./preset-route.js";
import { liveConfigSourceDirty } from "./live-config-state.js";

export function bindLiveConfigNavigation({ state, notify, confirmAction, engineName },
  { agent, engine, accountData, sourceMode, workspaceElement, liveConfig }) {
  if (!workspaceElement) return;
  const epoch = state.navigationEpoch;
  const visible = () => accountData === state.data && epoch === state.navigationEpoch &&
    state.route === "live-config" && workspaceElement.isConnected;
  const original = { liveAgent:agent.id, liveEngine:engine, liveConfigSource:sourceMode };
  let selectionRequest = 0, activeRequest = 0;
  const frozen = new Map();
  const tabs = [...workspaceElement.querySelectorAll("[data-live-engine]")];
  const syncTabs = selected => tabs.forEach(tab => {
    const active = tab.dataset.liveEngine === selected;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-pressed", String(active));
    if (active) tab.setAttribute("aria-current", "true");
    else tab.removeAttribute("aria-current");
  });
  const switchContext = async (next, title, label) => {
    const request = ++selectionRequest;
    if (!visible()) return;
    if (workspaceElement.dataset.configRefreshing || document.querySelector('#live-config-form[data-saving="1"], .config-inbound-dialog[data-saving="1"]')) {
      notify("配置正在提交或刷新，请等待结果后再切换。", "error");
      return;
    }
    if (liveConfigSourceDirty() && !(await confirmAction("当前配置有未保存的修改，切换后将丢弃这些修改。确定切换？", title))) return;
    if (!visible() || request !== selectionRequest) return;
    activeRequest = request;
    Object.assign(state.data, next);
    workspaceElement.dataset.configSwitching = "1";
    workspaceElement.setAttribute("aria-busy", "true");
    workspaceElement.querySelectorAll(".live-config-editor, .config-workspace-tools, .config-empty-actions, .live-config-source-switch").forEach(element => {
      if (!frozen.has(element)) frozen.set(element, element.inert);
      element.inert = true;
    });
    syncTabs(state.data.liveEngine);
    removePresented(workspaceElement.querySelector(".live-engine-loading"));
    const status = document.createElement("span");
    status.className = "live-engine-loading";
    status.setAttribute("role", "status");
    updateFeedback(status, `正在切换到 ${label}…`);
    workspaceElement.querySelector(".live-config-details").append(status);
    try { await liveConfig(); }
    catch (error) {
      if (!visible() || request !== activeRequest) return;
      Object.assign(state.data, original);
      globalThis.history?.replaceState?.(null, "", presetRoute({agentId:agent.id, engine}));
      syncTabs(engine);
      notify(`${title}失败：${error.message}`, "error");
    } finally {
      if (workspaceElement.isConnected && request === activeRequest) {
        delete workspaceElement.dataset.configSwitching;
        workspaceElement.removeAttribute("aria-busy");
        removePresented(status);
        frozen.forEach((inert, element) => { element.inert = inert; });
        frozen.clear();
      }
    }
  };
  document.querySelectorAll("[data-live-agent]").forEach(link => {
    link.onclick = event => {
      event.preventDefault();
      if (link.dataset.liveAgent === state.data.liveAgent) return;
      void switchContext({liveAgent:link.dataset.liveAgent, liveEngine:"", liveConfigSource:""}, "切换节点", link.querySelector("strong")?.textContent || "节点");
    };
  });
  tabs.forEach(link => {
    link.onclick = event => {
      event.preventDefault();
      if (link.dataset.liveEngine === state.data.liveEngine) return;
      void switchContext({liveAgent:agent.id, liveEngine:link.dataset.liveEngine, liveConfigSource:""}, "切换内核", engineName(link.dataset.liveEngine));
    };
  });
  workspaceElement.querySelectorAll("[data-live-source]").forEach(button => {
    button.onclick = () => {
      if (button.dataset.liveSource === state.data.liveConfigSource) return;
      void switchContext({liveAgent:agent.id, liveEngine:engine, liveConfigSource:button.dataset.liveSource}, "切换配置来源", button.textContent.trim());
    };
  });
}
