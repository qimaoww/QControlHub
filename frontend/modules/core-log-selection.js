import { accountStorage } from "./account-storage.js";
import { saveCoreLogPreferences, savedCoreLogPreferences } from "./core-log-preferences.js";
export function createCoreLogSelection({ state, engines, storage = accountStorage }) {
  // Browser storage only seeds the selection for a fresh session. Once the
  // page owns filters in memory, a later render, poll, or in-page filter click
  // keeps using them, and every user change is written back.
  const restoreSelection = () => {
    if (state.data.coreLogFilters) return;
    const saved = savedCoreLogPreferences(storage, engines);
    state.data.coreLogFilters = saved.filters;
    state.data.coreLogAutoRefresh = saved.autoRefresh;
  };
  const rememberSelection = () => {
    saveCoreLogPreferences(
      {
        filters: state.data.coreLogFilters || {},
        autoRefresh: state.data.coreLogAutoRefresh,
      },
      storage,
      engines,
    );
  };

  const query = () => {
    const filters = { ...(state.data.coreLogFilters || {}) };
    const params = new URLSearchParams();
    if (filters.agent_id) params.set("agent_id", filters.agent_id);
    params.set("limit", String(filters.limit || 1000));
    return { filters, params };
  };

  return { restoreSelection, rememberSelection, query };
}
