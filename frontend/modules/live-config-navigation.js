import { updateFeedback, removePresented } from "./presence-motion.js";
import { presetRoute } from "./preset-route.js";

export function bindLiveConfigNavigation({ state, notify, confirmAction, engineName },
  { agent, engine, accountData, sourceMode, current, workspaceElement, liveConfig }) {
  const confirmSwitch = async (title) => {
    if (document.querySelector('#live-config-form[data-saving="1"]')) {
      notify("配置正在保存，请等待提交结果后再切换。", "error");
      return false;
    }
    const editor = document.querySelector("#live-config-form [data-code-editor]");
    const input = editor?.querySelector("[data-code-input]");
    const dirty = editor?.configFileController?.dirty() ?? (input && !input.readOnly && input.value !== current?.content);
    return !dirty || await confirmAction("当前配置有未保存的修改，切换后将丢弃这些修改。确定切换？", title);
  };
  const epoch = state.navigationEpoch;
  const visible = () => accountData === state.data && epoch === state.navigationEpoch &&
    state.route === "live-config" && workspaceElement.isConnected;
  let selectionRequest = 0;
  document.querySelectorAll("[data-live-agent]").forEach(
    (link) =>
      (link.onclick = async (event) => {
        event.preventDefault();
        if (link.dataset.liveAgent === state.data.liveAgent) return;
        const request = ++selectionRequest;
        if (!(await confirmSwitch("切换节点")) || !visible() || request !== selectionRequest) return;
        state.data.liveAgent = link.dataset.liveAgent;
        state.data.liveEngine = "";
        state.data.liveConfigSource = "";
        try { await liveConfig(); }
        catch (error) {
          if (!visible() || request !== selectionRequest) return;
          Object.assign(state.data, { liveAgent: agent.id, liveEngine: engine, liveConfigSource: sourceMode });
          globalThis.history?.replaceState?.(null, "", presetRoute({ agentId: agent.id, engine }));
          notify(`切换节点失败：${error.message}`, "error");
        }
      }),
  );
  let engineSwitch = 0;
  const frozenForSwitch = new Map();
  document.querySelectorAll("[data-live-engine]").forEach(
    (link) =>
      (link.onclick = async (event) => {
        event.preventDefault();
        if (link.dataset.liveEngine === state.data.liveEngine) return;
        const request = ++selectionRequest;
        if (!(await confirmSwitch("切换内核")) || !visible() || request !== selectionRequest) return;
        if (accountData !== state.data || !workspaceElement.isConnected || link.dataset.liveEngine === state.data.liveEngine) return;
        const switchRequest = ++engineSwitch;
        const previousSource = sourceMode;
        state.data.liveEngine = link.dataset.liveEngine;
        state.data.liveConfigSource = "";
        workspaceElement.setAttribute("aria-busy", "true");
        workspaceElement.querySelectorAll(".live-config-editor, .config-workspace-tools, .config-empty-actions, .live-config-source-switch").forEach(element => {
          if (!frozenForSwitch.has(element)) frozenForSwitch.set(element, element.inert);
          element.inert = true;
        });
        const tabs = [...workspaceElement.querySelectorAll("[data-live-engine]")];
        tabs.forEach(tab => {
          tab.classList.toggle("active", tab === link);
          tab.setAttribute("aria-pressed", String(tab === link));
        });
        removePresented(workspaceElement.querySelector(".live-engine-loading"));
        const status = document.createElement("span");
        status.className = "live-engine-loading";
        status.setAttribute("role", "status");
        updateFeedback(status, `正在切换到 ${engineName(link.dataset.liveEngine)}…`);
        workspaceElement.querySelector(".live-config-details").append(status);
        try { await liveConfig(); }
        catch (error) {
          if (!visible() || engineSwitch !== switchRequest || request !== selectionRequest || state.data.liveEngine !== link.dataset.liveEngine) return;
          state.data.liveEngine = engine;
          state.data.liveConfigSource = previousSource;
          if (globalThis.history?.replaceState) globalThis.history.replaceState(null, "", presetRoute({agentId:agent.id, engine}));
          tabs.forEach(tab => {
            const active = tab.dataset.liveEngine === engine;
            tab.classList.toggle("active", active);
            tab.setAttribute("aria-pressed", String(active));
          });
          notify(`切换内核失败：${error.message}`, "error");
        } finally {
          if (workspaceElement.isConnected && engineSwitch === switchRequest) {
            workspaceElement.removeAttribute("aria-busy"); removePresented(status);
            frozenForSwitch.forEach((inert, element) => { element.inert = inert; });
            frozenForSwitch.clear();
            tabs.forEach(tab => { tab.disabled = false; });
          }
        }
      }),
  );
  document.querySelectorAll("[data-live-source]").forEach(
    (button) =>
      (button.onclick = async () => {
        if (button.dataset.liveSource === state.data.liveConfigSource) return;
        const request = ++selectionRequest;
        if (!(await confirmSwitch("切换配置来源")) || !visible() || request !== selectionRequest) return;
        state.data.liveConfigSource = button.dataset.liveSource;
        try { await liveConfig(); }
        catch (error) {
          if (!visible() || request !== selectionRequest) return;
          state.data.liveConfigSource = sourceMode;
          notify(`切换配置来源失败：${error.message}`, "error");
        }
      }),
  );

}
