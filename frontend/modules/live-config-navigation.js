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
  const engineSelect = workspaceElement.querySelector("[data-live-engine-select]");
  const sourceSelect = workspaceElement.querySelector("[data-live-source-select]");
  const syncSelection = () => {
    const selected = state.data.liveEngine || engine;
    if (engineSelect) engineSelect.value = selected;
    if (sourceSelect) sourceSelect.value = state.data.liveConfigSource || sourceMode;
  };
  const switchContext = async (next, title, label) => {
    const request = ++selectionRequest;
    if (!visible()) return;
    if (workspaceElement.dataset.configRefreshing || document.querySelector('#live-config-form[data-saving="1"], .config-inbound-dialog[data-saving="1"]')) {
      notify("配置正在提交或刷新，请等待结果后再切换。", "error");
      syncSelection();
      return;
    }
    syncSelection();
    if (liveConfigSourceDirty() && !(await confirmAction("当前配置有未保存的修改，切换后将丢弃这些修改。确定切换？", title))) {
      if (visible() && request === selectionRequest) syncSelection();
      return;
    }
    if (!visible() || request !== selectionRequest) return;
    activeRequest = request;
    Object.assign(state.data, next);
    workspaceElement.dataset.configSwitching = "1";
    workspaceElement.setAttribute("aria-busy", "true");
    workspaceElement.querySelectorAll(".live-config-editor, .config-workspace-tools, .config-empty-actions").forEach(element => {
      if (!frozen.has(element)) frozen.set(element, element.inert);
      element.inert = true;
    });
    if (engineSelect && state.data.liveAgent !== agent.id) {
      if (!frozen.has(engineSelect)) frozen.set(engineSelect, engineSelect.inert);
      engineSelect.inert = true;
    }
    if (sourceSelect && (state.data.liveAgent !== agent.id || state.data.liveEngine !== engine)) {
      const field = sourceSelect.closest("label");
      if (!frozen.has(field)) frozen.set(field, field.inert);
      field.inert = true;
    }
    syncSelection();
    removePresented(workspaceElement.querySelector(".live-engine-loading"));
    const status = document.createElement("span");
    status.className = "live-engine-loading";
    status.setAttribute("role", "status");
    updateFeedback(status, ` · 正在切换到 ${label}…`);
    workspaceElement.querySelector("[data-live-context-feedback]").append(status);
    try { await liveConfig(); }
    catch (error) {
      if (!visible() || request !== activeRequest) return;
      Object.assign(state.data, original);
      globalThis.history?.replaceState?.(null, "", presetRoute({agentId:agent.id, engine}));
      syncSelection();
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
  if (engineSelect) engineSelect.onchange = () => {
    if (state.data.liveAgent !== agent.id) return syncSelection();
    if (engineSelect.value === state.data.liveEngine) return;
    void switchContext({liveAgent:agent.id, liveEngine:engineSelect.value, liveConfigSource:""}, "切换内核", engineName(engineSelect.value));
  };
  if (sourceSelect) sourceSelect.onchange = () => {
    if (state.data.liveAgent !== agent.id || state.data.liveEngine !== engine) return syncSelection();
    if (sourceSelect.value === state.data.liveConfigSource) return;
    void switchContext({liveAgent:agent.id, liveEngine:engine, liveConfigSource:sourceSelect.value}, "切换配置来源", sourceSelect.selectedOptions[0].textContent);
  };
  document.querySelectorAll("[data-live-agent]").forEach(link => {
    link.onclick = event => {
      event.preventDefault();
      if (link.dataset.liveAgent === state.data.liveAgent) return;
      void switchContext({liveAgent:link.dataset.liveAgent, liveEngine:"", liveConfigSource:""}, "切换节点", link.querySelector("strong")?.textContent || "节点");
    };
  });
}
