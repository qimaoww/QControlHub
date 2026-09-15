import { taskRenderSignature } from "./task-model.js";
import { openResultTaskIds, captureTaskAnchor, reconcileTaskTimeline, restoreTaskAnchor, setupTaskPagination } from "./task-timeline.js";
import { createTaskView } from "./task-view.js";
import { createTaskBindings } from "./task-bindings.js";
export { coreSourceName, coreSourceLabel } from "./task-model.js";

import { createRefreshChannel } from "./refresh.js";

export function installTasks(ctx) {
  const {
    api,
    state,
    setTimer = (callback, delay) => setTimeout(callback, delay),
    clearTimer = (timer) => clearTimeout(timer),
    now = () => Date.now(),
  } = ctx;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "tasks",
    getScope: () => state.navigationEpoch,
  });
  const { renderTaskCards, updateTaskClocks, syncTaskAgentFilter, renderTaskPage } = createTaskView(ctx);
  const { bindTaskActions, bindTaskFilters } = createTaskBindings(ctx, { tasks });
  const settingsCacheDuration = 30_000;
  // The task list changes on every tick, but the node names and the panel
  // settings it renders with do not. Re-reading both per poll tripled the load
  // of a page that already polls, so they ride the same short cache.
  const agentsCacheDuration = 30_000;

  function scheduleTaskRefresh(delay) {
    clearTimer(state.taskPollTimer);
    if (state.route === "tasks") {
      state.taskPollTimer = setTimer(
        () => tasks({ background: true }),
        delay,
      );
    }
  }

  async function tasks({
    background = false,
    settings: preloadedSettings,
    syncFilters = false,
  } = {}) {
    clearTimer(state.taskPollTimer);
    if (state.confirmOpen) {
      scheduleTaskRefresh(300);
      return;
    }

    const filters = state.data.taskFilters || {};
    const query = new URLSearchParams({
      limit: String(filters.limit || 100),
      ...(filters.agent_id ? { agent_id: filters.agent_id } : {}),
      ...(filters.status ? { status: filters.status } : {}),
      ...(filters.action ? { action: filters.action } : {}),
    });
    const existingTaskPage = document.querySelector("[data-task-page]");
    const currentTimeline = background
      ? existingTaskPage?.querySelector(".task-timeline")
      : null;
    const settingsCacheAge = now() - Number(state.data.taskSettingsLoadedAt || 0);
    const cachedSettings =
      background &&
      state.data.settings &&
      settingsCacheAge >= 0 &&
      settingsCacheAge < settingsCacheDuration
        ? state.data.settings
        : null;
    const agentsCacheAge = now() - Number(state.data.taskAgentsLoadedAt || 0);
    const cachedAgents =
      background &&
      Array.isArray(state.data.taskAgents) &&
      agentsCacheAge >= 0 &&
      agentsCacheAge < agentsCacheDuration
        ? state.data.taskAgents
        : null;
    let payload;
    let applied;
    try {
      applied = await refresh.run(
        (signal) =>
          Promise.all([
            api(`/tasks?${query}`, { signal }),
            cachedAgents || api("/agents", { signal }),
            preloadedSettings ||
              cachedSettings ||
              api("/settings", { signal }),
          ]),
        (value) => {
          payload = value;
        },
      );
    } catch (error) {
      const status = document.querySelector("[data-task-refresh-status]");
      if (!status && !background) throw error;
      if (status) {
        status.dataset.refreshError = "1";
        status.classList.add("poll-error");
        status.title = error.message;
        const label = status.querySelector("[data-task-refresh-label]");
        if (label) label.textContent = "刷新失败，保留上次数据";
      }
      scheduleTaskRefresh(
        state.data.settings?.task_poll_interval_ms || 1000,
      );
      return false;
    }
    if (!applied) return;
    const refreshStatus = document.querySelector("[data-task-refresh-status]");
    refreshStatus?.removeAttribute("data-refresh-error");
    refreshStatus?.removeAttribute("title");
    refreshStatus?.classList.remove("poll-error");
    const refreshLabel = refreshStatus?.querySelector("[data-task-refresh-label]");
    if (refreshLabel) refreshLabel.textContent = "自动更新";
    const [items, agents, settings] = payload;

    const taskCards = renderTaskCards(items, agents, openResultTaskIds());
    const signature = taskRenderSignature(items, agents);
    const pollInterval = settings.task_poll_interval_ms || 1000;
    state.data.settings = settings;
    if (settings !== cachedSettings) state.data.taskSettingsLoadedAt = now();
    if (agents !== cachedAgents) {
      state.data.taskAgents = agents;
      state.data.taskAgentsLoadedAt = now();
    }
    if (state.confirmOpen) {
      scheduleTaskRefresh(300);
      return;
    }

    if (currentTimeline?.isConnected) {
      syncTaskAgentFilter(agents);
      if (signature !== state.data.taskRenderSignature) {
        const anchor = captureTaskAnchor(currentTimeline);
        reconcileTaskTimeline(currentTimeline, taskCards);
        setupTaskPagination(currentTimeline);
        bindTaskActions(currentTimeline);
        restoreTaskAnchor(anchor, currentTimeline);
        state.data.taskRenderSignature = signature;
      }
      updateTaskClocks(currentTimeline, items);
      scheduleTaskRefresh(pollInterval);
      return;
    }

    renderTaskPage({ agents, taskCards, filters, existingTaskPage, syncFilters });
    syncTaskAgentFilter(agents);
    bindTaskFilters();
    const timeline = document.querySelector(".task-timeline");
    if (timeline) {
      setupTaskPagination(timeline);
      bindTaskActions(timeline);
    }
    state.data.taskRenderSignature = signature;
    scheduleTaskRefresh(pollInterval);
  }

  return tasks;
}
