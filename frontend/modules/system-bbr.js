import { createSystemBBRView } from "./system-bbr-view.js";
import { createSystemBBREditor } from "./system-bbr-editor.js";
export { systemBBRFeature, systemBBRActions, validateTCPSelection, systemBBRState } from "./system-bbr-model.js";

import { bindEvent, createPoller, createRefreshChannel } from "./refresh.js";

export function installSystemBBR(ctx) {
  const { api, state, can, shell, notify,
    setTimer = (fn, delay) => (state.bbrPollTimer = setTimeout(fn, delay)),
    clearTimer = clearTimeout } = ctx;
  // One account-scoped record is shared by loading, rendering and editing.
  const lifecycle = {
    submitting: new Set(), localTasks: new Map(), accountData: state.data,
    drafts: (state.data.bbrDrafts ||= {}), editorErrors: new Map(),
    backdropStarts: new WeakSet(), rules: null, lastAgents: null, refreshFailed: false,
  };
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "system-bbr",
    getScope: () => state.navigationEpoch,
  });
  const editable = (agent) => agent?.can_manage !== false && can("agents.manage", agent) && can("tasks.execute");
  const selectedID = () => state.anchor?.startsWith("system-bbr-agent-")
    ? state.anchor.slice("system-bbr-agent-".length) : "";
  const view = createSystemBBRView(ctx, { lifecycle, editable, selectedID });
  const editor = createSystemBBREditor(ctx, { lifecycle, editable, render, systemBBR });
  function render(agents) { editor.bind(view(agents)); }
  const poller = createPoller({ run: () => systemBBR({ background: true }),
    isActive: () => state.route === "system-bbr", delay: () => 5000, setTimer, clearTimer });

  async function systemBBR({ background = false } = {}) {
    poller.stop();
    const data = state.data, epoch = state.navigationEpoch;
    if (lifecycle.accountData !== data) {
      lifecycle.accountData = data;
      lifecycle.drafts = (data.bbrDrafts ||= {});
      lifecycle.localTasks.clear();
      lifecycle.editorErrors.clear();
      lifecycle.submitting.clear();
      lifecycle.lastAgents = null;
      lifecycle.refreshFailed = false;
      refresh.invalidate();
    }
    try {
      const params = selectedID() ? `?agent_id=${encodeURIComponent(selectedID())}` : "";
      const applied = await refresh.run((signal) => Promise.all([
        api("/agents", { signal }),
        lifecycle.rules ? Promise.resolve(lifecycle.rules) : api("/system-tcp/parameters", { signal }),
        can("tasks.read") ? api(`/system-tcp/tasks${params}`, { signal }) : Promise.resolve([]),
      ]), ([agents, parameters, tasks]) => {
        if (data !== state.data) return;
        lifecycle.rules = parameters;
        lifecycle.lastAgents = agents;
        lifecycle.refreshFailed = false;
        if (can("tasks.read")) {
          // The response is authoritative for this scope, including tasks
          // removed by retention; never keep an obsolete busy state forever.
          if (selectedID()) lifecycle.localTasks.delete(selectedID());
          else lifecycle.localTasks.clear();
          for (const task of tasks) lifecycle.localTasks.set(task.agent_id, task);
        }
        render(agents);
      });
      if (applied) poller.start();
      return applied;
    } catch (error) {
      if (data !== state.data || epoch !== state.navigationEpoch || state.route !== "system-bbr" || error.name === "AbortError") return false;
      if (!background && !document.querySelector(".bbr-dialog[open]")) notify(error.message, "error");
      lifecycle.refreshFailed = true;
      if (lifecycle.lastAgents) render(lifecycle.lastAgents);
      else shell('<div class="empty large"><strong>无法读取系统 BBR 状态</strong><p>请检查节点查看权限或稍后刷新重试。</p><button class="button" data-bbr-retry>重试</button></div>', "系统 BBR");
      bindEvent(document.querySelector("[data-bbr-retry]"), "click", () => systemBBR());
      poller.start();
      return false;
    }
  }
  return systemBBR;
}
