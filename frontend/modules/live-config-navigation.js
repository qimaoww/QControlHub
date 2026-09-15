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
  document.querySelectorAll("[data-live-agent]").forEach(
    (link) =>
      (link.onclick = async (event) => {
        event.preventDefault();
        if (link.dataset.liveAgent === agent.id || !(await confirmSwitch("切换节点"))) return;
        state.data.liveAgent = link.dataset.liveAgent;
        state.data.liveEngine = "";
        state.data.liveConfigSource = "";
        liveConfig();
      }),
  );
  let engineSwitch = 0;
  const frozenForSwitch = new Map();
  document.querySelectorAll("[data-live-engine]").forEach(
    (link) =>
      (link.onclick = async (event) => {
        event.preventDefault();
        if (link.dataset.liveEngine === state.data.liveEngine) return;
        if (!(await confirmSwitch("切换内核"))) return;
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
        workspaceElement.querySelector(".live-engine-loading")?.remove();
        const status = document.createElement("span");
        status.className = "live-engine-loading";
        status.setAttribute("role", "status");
        status.textContent = `正在切换到 ${engineName(link.dataset.liveEngine)}…`;
        workspaceElement.querySelector(".live-config-details").append(status);
        try { await liveConfig(); }
        catch (error) {
          if (accountData !== state.data || engineSwitch !== switchRequest || state.data.liveEngine !== link.dataset.liveEngine) return;
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
            workspaceElement.removeAttribute("aria-busy"); status.remove();
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
        if (button.dataset.liveSource === sourceMode) return;
        if (!(await confirmSwitch("切换配置来源"))) return;
        if (accountData !== state.data) return;
        state.data.liveConfigSource = button.dataset.liveSource;
        liveConfig();
      }),
  );

}
