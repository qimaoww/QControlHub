import { bindEvent } from "./refresh.js";

export function createCoreLogBindings({ state }, { renderCoreLogs, rememberSelection, coreLogs, poller }) {
  return (filters) => {
    document.querySelectorAll("[data-core-log-page-index]").forEach((button) => {
      bindEvent(button, "click", () => {
        state.data.coreLogPage = Number(button.dataset.coreLogPageIndex);
        renderCoreLogs(state.data.coreLogEntries || [], state.data.agents || [], state.data.coreLogFilters || filters);
        document.querySelector(".core-log-result-toolbar")?.scrollIntoView({ block: "nearest" });
      });
    });
    const renderLocalFilters = (patch) => {
      state.data.coreLogFilters = {
        ...(state.data.coreLogFilters || {}),
        ...patch,
      };
      rememberSelection();
      renderCoreLogs(
        state.data.coreLogEntries || [],
        state.data.agents || [],
        state.data.coreLogFilters,
      );
    };
    document.querySelectorAll("[data-core-log-engine]").forEach((button) => {
      bindEvent(button, "click", () => renderLocalFilters({ engine: button.value }));
    });
    document.querySelectorAll("[data-core-log-level]").forEach((button) => {
      bindEvent(button, "click", () => renderLocalFilters({ level: button.value }));
    });
    bindEvent(document.querySelector('#core-log-filters input[name="q"]'), "input", (event) => {
      renderLocalFilters({ q: event.currentTarget.value });
    });
    bindEvent(document.querySelector('#core-log-filters select[name="limit"]'), "change", async (event) => {
      state.data.coreLogFilters = {
        ...(state.data.coreLogFilters || {}),
        limit: Number(event.currentTarget.value || 1000),
      };
      rememberSelection();
      await coreLogs({ scopeChange: true });
    });
    bindEvent(document.querySelector("[data-reset-core-logs]"), "click", () => {
      const search = document.querySelector('#core-log-filters input[name="q"]');
      if (search) search.value = "";
      renderLocalFilters({ engine: "", level: "", q: "" });
    });
    document.querySelectorAll("[data-core-log-agent]").forEach((link) => {
      bindEvent(link, "click", async (event) => {
        event.preventDefault();
        state.data.coreLogFilters = {
          ...(state.data.coreLogFilters || {}),
          agent_id: link.dataset.coreLogAgent || "",
        };
        rememberSelection();
        await coreLogs({ scopeChange: true });
      });
    });
    bindEvent(document.querySelector("[data-toggle-core-log-refresh]"), "click", (event) => {
      const enabled = state.data.coreLogAutoRefresh === false;
      state.data.coreLogAutoRefresh = enabled;
      rememberSelection();
      event.currentTarget.setAttribute("aria-checked", String(enabled));
      const label = document.querySelector("[data-core-log-refresh-label]");
      if (label && (!state.data.coreLogPhase || state.data.coreLogPhase === "ready"))
        label.textContent = enabled ? "正在实时更新" : "自动更新已暂停";
      if (enabled) poller.start();
      else poller.stop();
    });
  };

}
