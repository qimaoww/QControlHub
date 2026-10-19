import { bindEvent } from "./refresh.js";
import { presetRoute } from "./preset-route.js";
import { updateFeedback, removePresented } from "./presence-motion.js";

export function bindLiveConfigNavigation({state, notify, confirmAction, engineName}, {
  agent, engine, accountData, sourceMode, workspaceElement, liveConfig, workspace, workspaces,
}) {
  const epoch = state.navigationEpoch;
  const visible = () => accountData === state.data && epoch === state.navigationEpoch &&
    !state.routeSignal?.aborted && state.route === "live-config" && workspaceElement?.isConnected;
  let selectionRequest = 0;
  const frozen = new Map();
  const change = async (next, title) => {
    const target = {agentId:next.agentId || agent.id, engine:next.engine ?? engine, sourceMode:next.sourceMode ?? sourceMode};
    if (target.agentId === state.data.liveAgent && target.engine === state.data.liveEngine && target.sourceMode === state.data.liveConfigSource) return;
    const request = ++selectionRequest;
    if (workspace.submitting || document.querySelector('#live-config-form[data-saving="1"], .config-inbound-dialog[data-saving="1"]')) {
      notify("配置正在提交，请等待保存结果后再切换。", "error"); return;
    }
    workspaces.capture();
    if (workspaces.dirty() && !(await confirmAction("当前工作区有未保存修改。切换后会保留草稿，确定切换？", title))) return;
    if (!visible() || request !== selectionRequest) return;
    Object.assign(state.data, {liveAgent:target.agentId, liveEngine:target.engine, liveConfigSource:target.sourceMode});
    workspaceElement.setAttribute("aria-busy", "true");
    workspaceElement.querySelectorAll(".live-config-editor, .config-file-actions, .config-workspace-tools, .live-config-source-switch").forEach(element => {
      if (!frozen.has(element)) frozen.set(element, element.inert);
      element.inert = true;
    });
    workspaceElement.querySelectorAll("[data-live-engine]").forEach(tab => {
      const active = tab.dataset.liveEngine === target.engine;
      tab.classList.toggle("active", active); tab.setAttribute("aria-pressed", String(active));
    });
    removePresented(workspaceElement.querySelector(".live-engine-loading"));
    const status = document.createElement("span"); status.className = "live-engine-loading"; status.setAttribute("role", "status");
    updateFeedback(status, target.engine ? `正在加载 ${engineName(target.engine)}…` : "正在切换节点…");
    workspaceElement.querySelector(".live-config-details")?.append(status);
    try { await liveConfig(); }
    catch (error) {
      if (!visible() || request !== selectionRequest) return;
      Object.assign(state.data, {liveAgent:agent.id, liveEngine:engine, liveConfigSource:sourceMode});
      globalThis.history?.replaceState?.(null, "", presetRoute({agentId:agent.id, engine, sourceMode}));
      workspaceElement.querySelectorAll("[data-live-engine]").forEach(tab => {
        const active = tab.dataset.liveEngine === engine;
        tab.classList.toggle("active", active); tab.setAttribute("aria-pressed", String(active));
      });
      notify(`${title}失败：${error.message}`, "error");
    } finally {
      if (workspaceElement.isConnected && request === selectionRequest) {
        workspaceElement.removeAttribute("aria-busy"); removePresented(status);
        frozen.forEach((inert, element) => { element.inert = inert; }); frozen.clear();
      }
    }
  };
  document.querySelectorAll("[data-live-agent]").forEach(link => bindEvent(link, "click", event => {
    event.preventDefault();
    if (link.dataset.liveAgent !== state.data.liveAgent) void change({agentId:link.dataset.liveAgent, engine:"", sourceMode:""}, "切换节点");
  }));
  workspaceElement?.querySelectorAll("[data-live-engine]").forEach(button => bindEvent(button, "click", () => {
    if (button.dataset.liveEngine !== state.data.liveEngine) void change({engine:button.dataset.liveEngine, sourceMode:""}, "切换内核");
  }));
  workspaceElement?.querySelectorAll("[data-live-source]").forEach(button => bindEvent(button, "click", () => {
    void change({sourceMode:button.dataset.liveSource}, "切换配置来源");
  }));
}
