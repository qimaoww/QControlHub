import { saveCoreLogPreferences, savedCoreLogPreferences } from "./core-log-preferences.js";
export function createCoreLogSelection({ state, engines, storage }) {
  // The active page is seeded by server account preferences. Explicit storage
  // remains available to isolated model fixtures, never implicitly inherited.
  const restoreSelection = () => {
    if (state.data.coreLogFilters) return;
    const saved = storage ? savedCoreLogPreferences(storage, engines) : { filters: {}, autoRefresh: true };
    state.data.coreLogFilters = saved.filters;
    state.data.coreLogAutoRefresh = saved.autoRefresh;
  };
  const rememberSelection = () => {
    if (!storage) return;
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
    if (filters.instance_id) params.set("instance_id", filters.instance_id);
    if (filters.user_id) params.set("user_id", filters.user_id);
    params.set("limit", String(filters.limit || 1000));
    return { filters, params };
  };

  return { restoreSelection, rememberSelection, query };
}
