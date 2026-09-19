import { defaultConnectionFilters, connectionQuery } from "./client-connection-model.js";
import { createClientConnectionView } from "./client-connection-view.js";

import { bindClientConnections } from "./client-connection-bindings.js";

export function installClientConnections(ctx) {
  const { api, state, shell } = ctx;
  const view = createClientConnectionView(ctx);
  let serial = 0;
  // Account data owns all filters, cursors and results. No data survives logout.
  async function load({ before = "", cursors = [] } = {}) {
    const data = state.data, epoch = state.navigationEpoch, request = ++serial;
    const current = () => state.data === data && state.navigationEpoch === epoch && state.route === "client-connections" && request === serial;
    const filters = { ...(data.connectionFilters ||= defaultConnectionFilters()) };
    let result = null;
    const paint = (options = {}) => {
      shell(view(result, filters, data.connectionSources || [], { hasPrevious: cursors.length > 0, ...options }), "客户端连接 IP");
      bindClientConnections({
        current,
        search: filters => { data.connectionFilters = filters; void load(); },
        recent: () => { data.connectionFilters = { ...filters, ...defaultConnectionFilters() }; void load(); },
        next: () => { if (result?.next_before) void load({ before: result.next_before, cursors: [...cursors, before] }); },
        previous: () => { if (cursors.length) void load({ before: cursors.at(-1), cursors: cursors.slice(0, -1) }); },
      });
    };
    paint({ loading: true });
    try {
      result = await api(`/client-connections?${connectionQuery(filters, before)}`);
      if (!current()) return;
      // Keep all server options when an individual server is selected.
      if (!filters.agent_id) data.connectionSources = result.sources;
      else data.connectionSources = (data.connectionSources || []).map(source => result.sources.find(fresh => fresh.agent_id === source.agent_id) || source);
      paint();
    } catch (error) {
      if (current()) paint({ error: error.message });
    }
  }
  return load;
}
