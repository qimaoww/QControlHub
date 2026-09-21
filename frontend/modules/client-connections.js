import { defaultConnectionFilters, connectionQuery } from "./client-connection-model.js";
import { createClientConnectionView } from "./client-connection-view.js";

import { bindClientConnections } from "./client-connection-bindings.js";

export function installClientConnections(ctx) {
  const { api, state, shell } = ctx;
  const view = createClientConnectionView(ctx);
  let serial = 0, controller;
  // Account data owns all filters, cursors and results. No data survives logout.
  async function load({ before = "", cursors = [] } = {}) {
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    const data = state.data, epoch = state.navigationEpoch, request = ++serial;
    const current = () => state.data === data && state.navigationEpoch === epoch && state.route === "client-connections" && request === serial;
    const filters = { ...(data.connectionFilters ||= defaultConnectionFilters()) };
    let result = null;
    const paint = (options = {}) => {
      data.connectionLoading = Boolean(options.loading);
      shell(view(result, filters, data.connectionSources || [], { hasPrevious: cursors.length > 0, page: cursors.length + 1, ...options }), "客户端连接 IP");
      bindClientConnections({
        current,
        refresh: () => { void load({ before, cursors }); },
        search: values => { data.connectionFilters = { ...filters, ...values }; void load(); },
        selectAgent: agent_id => { data.connectionFilters = { ...filters, agent_id }; void load(); },
        recent: () => { data.connectionFilters = { ...filters, ...defaultConnectionFilters() }; void load(); },
        next: () => { if (result?.next_before) void load({ before: result.next_before, cursors: [...cursors, before] }); },
        previous: () => { if (cursors.length) void load({ before: cursors.at(-1), cursors: cursors.slice(0, -1) }); },
      });
    };
    try {
      const query = connectionQuery(filters, before), key = query.toString();
      result = data.connectionCache?.key === key ? data.connectionCache.result : null;
      paint({ loading: true });
      result = await api(`/client-connections?${query}&locations=cached`, { signal });
      if (!current()) return;
      // Keep all server options when an individual server is selected.
      if (!filters.agent_id) data.connectionSources = result.sources;
      else data.connectionSources = (data.connectionSources || []).map(source => result.sources.find(fresh => fresh.agent_id === source.agent_id) || source);
      data.connectionCache = { key, result };
      paint();
      // Location lookup never holds up the history, filters or pagination.
      if (result.records?.some(row => !row.location?.non_public)) {
        const locationQuery = new URLSearchParams(query);
        // Pin enrichment to this page even if a heartbeat inserts newer rows.
        if (result.records[0]?.id) locationQuery.set("before", String(result.records[0].id + 1));
        void api(`/client-connections?${locationQuery}&locations=only`, { signal }).then(enriched => {
          if (!current()) return;
          const locations = new Map(enriched.records.map(row => [row.id, row.location]));
          result.records = result.records.map(row => ({ ...row, location: locations.get(row.id) || row.location }));
          data.connectionCache = { key, result };
          paint();
        }).catch(() => {});
      }
    } catch (error) {
      if (current()) {
        result = null;
        delete data.connectionCache;
        paint({ error: error.message });
      }
    }
  }
  return load;
}
