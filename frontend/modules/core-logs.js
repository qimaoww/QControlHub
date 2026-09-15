import { coreLogFilterCounts } from "./core-log-model.js";
import { createCoreLogSelection } from "./core-log-selection.js";
import { createCoreLogView } from "./core-log-view.js";
import { createCoreLogBindings } from "./core-log-bindings.js";
export { filterCoreLogEntries, coreLogFilterCounts } from "./core-log-model.js";
import { createPoller, createRefreshChannel } from "./refresh.js";
import { createCoreLogCache } from "./core-log-cache.js";
import { accountStorage } from "./account-storage.js";

export function installCoreLogs(ctx) {
  const {
    api,
    state,
    engines,
    can,
    shell,
    storage = accountStorage,
    now = Date.now,
    setTimer = (callback, delay) => {
      state.coreLogPollTimer = setTimeout(callback, delay);
      return state.coreLogPollTimer;
    },
    clearTimer = (timer) => clearTimeout(timer),
  } = ctx;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "core-logs",
    getScope: () => state.navigationEpoch,
  });
  const poller = createPoller({
    run: () => coreLogs({ background: true }),
    isActive: () =>
      state.route === "core-logs" && state.data.coreLogAutoRefresh !== false,
    delay: () => 10_000,
    setTimer,
    clearTimer,
  });

  const { restoreSelection, rememberSelection, query } = createCoreLogSelection({ state, engines, storage });
  const view = createCoreLogView(ctx);
  const bind = createCoreLogBindings(ctx, { renderCoreLogs, rememberSelection, coreLogs, poller });
  function renderCoreLogs(entries, agents, filters) { view(entries, agents, filters); bind(filters); }

  async function coreLogs({ background = false, scopeChange = false } = {}) {
    restoreSelection();
    poller.stop();
    const { filters, params } = query();
    const data = state.data;
    const epoch = state.navigationEpoch;
    const key = JSON.stringify([filters.agent_id || "", Number(filters.limit || 1000)]);
    const cache = data.coreLogCache ||= createCoreLogCache({ now });
    const cached = cache.get(key);
    const changed = data.coreLogDataScope !== key;
    let rendered = false;
    let previewed = false;
    let agents = can("agents.read") ? (data.agents || []).filter((agent) => agent.can_manage !== false) : [];
    const current = (signal) => !signal.aborted && state.data === data &&
      state.navigationEpoch === epoch && state.route === "core-logs";
    const paint = (entries, phase) => {
      data.coreLogPhase = phase;
      renderCoreLogs(entries, agents, state.data.coreLogFilters || filters);
      rendered = true;
    };
    // The selected sidebar item/header must change before waiting on the WAN.
    // Never show the previous node's rows under the new node's name.
    if (!background && changed && (scopeChange || filters.agent_id || cached))
      paint(cached || [], cached ? "cached" : "loading");
    try {
      const applied = await refresh.run(
        async (signal) => {
          // Node metadata is not a dependency of log delivery. Reuse it for
          // quick sidebar switches; polling still revalidates live status.
          if (can("agents.read") && (!scopeChange || !data.coreLogAgentsAt || now() - data.coreLogAgentsAt >= 10_000)) {
            void api("/agents", { signal }).then((freshAgents) => {
              if (!current(signal)) return;
              agents = freshAgents.filter((agent) => agent.can_manage !== false);
              data.agents = agents;
              data.coreLogAgentsAt = now();
              if (rendered) paint(data.coreLogEntries || [], data.coreLogPhase);
            }).catch(() => {
              // A failed metadata read must not discard successfully read logs.
              if (!current(signal)) return;
              agents = agents.map((agent) => ({ ...agent, status: "unknown" }));
              data.coreLogAgentsAt = 0;
              if (rendered) paint(data.coreLogEntries || [], data.coreLogPhase);
            });
          }
          if (changed && filters.agent_id && !cached && Number(filters.limit || 1000) > 200) {
            const previewParams = new URLSearchParams(params);
            previewParams.set("limit", "200");
            const entries = await api(`/core-logs?${previewParams}`, { signal });
            if (!current(signal)) return entries;
            // If no engine hit its cap, this is already the complete window.
            const counts = coreLogFilterCounts(entries, engines);
            if (Object.values(counts.engine).every((count) => count < 200)) return entries;
            previewed = true;
            paint(entries, "preview");
          }
          return api(`/core-logs?${params}`, { signal });
        },
        (entries) => {
          if (state.data !== data || state.navigationEpoch !== epoch || state.route !== "core-logs") return;
          cache.set(key, entries);
          paint(entries, "ready");
        },
      );
      if (applied && state.data === data && state.data.coreLogAutoRefresh !== false) poller.start();
      return applied;
    } catch (error) {
      if (state.data !== data || state.route !== "core-logs" || state.navigationEpoch !== epoch) return false;
      if ([403, 404].includes(error.status) && filters.agent_id) {
        // A saved selection can outlive ownership/access. Return to the
        // account's permitted logs instead of trapping the page on that ID.
        refresh.invalidate();
        delete data.coreLogCache;
        data.coreLogEntries = [];
        data.coreLogs = [];
        data.coreLogFilters = { ...data.coreLogFilters, agent_id: "" };
        rememberSelection();
        return coreLogs({ scopeChange: true });
      }
      const permissionDenied = error.status === 403;
      const incomplete = previewed || ["preview", "incomplete"].includes(data.coreLogPhase);
      if (permissionDenied) {
        refresh.invalidate();
        delete data.coreLogCache;
        data.coreLogEntries = [];
        data.coreLogs = [];
      }
      if (!permissionDenied && (rendered || !changed))
        paint(data.coreLogEntries || [], incomplete ? "incomplete" : "failed");
      const status = document.querySelector("[data-core-log-refresh-status]");
      if (permissionDenied || (!status && !background && !rendered)) {
        shell(
          `<div class="core-log-workspace" data-core-log-page><div class="core-log-empty"><strong>${permissionDenied ? "无权查看内核日志" : "内核日志加载失败"}</strong><span>${permissionDenied ? "当前账号缺少内核日志查看权限。" : "无法读取日志数据，请稍后重试。"}</span></div></div>`,
          "内核日志",
        );
      }
      if (status) {
        status.dataset.refreshError = "1";
        status.title = error.message;
        const label = status.querySelector("[data-core-log-refresh-label]");
        if (label) label.textContent = incomplete ? "补齐失败，当前仅显示部分日志" : "刷新失败，保留上次数据";
      }
      if (state.data.coreLogAutoRefresh !== false) poller.start();
      return false;
    }
  }

  return coreLogs;
}
