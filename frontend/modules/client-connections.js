import { createConnectionCache } from "./client-connection-cache.js";
import { defaultConnectionFilters, connectionQuery } from "./client-connection-model.js";
import { createClientConnectionView } from "./client-connection-view.js";
import { migrateLegacyNodeOrder, savedNodeOrder } from "./node-order.js";

import { bindClientConnections } from "./client-connection-bindings.js";

export function installClientConnections(ctx) {
  const { api, state, shell } = ctx;
  const view = createClientConnectionView(ctx);
  let serial = 0, controller;
  let lastNodeOrder = "";
  // Account data owns all filters, cursors and results. No data survives logout.
  async function load({ cursor = "", cursors = [], force = false } = {}) {
    const nodeOrder = savedNodeOrder();
    const orderKey = JSON.stringify(nodeOrder);
    if (cursor && lastNodeOrder && orderKey !== lastNodeOrder) {
      cursor = "";
      cursors = [];
    }
    lastNodeOrder = orderKey;
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    const data = state.data, epoch = state.navigationEpoch, request = ++serial;
    const current = () => state.data === data && state.navigationEpoch === epoch && state.route === "client-connections" && request === serial;
    const filters = { ...(data.connectionFilters ||= defaultConnectionFilters()) };
    const cache = data.connectionCache ||= createConnectionCache();
    let result = null;
    const paint = (options = {}) => {
      data.connectionLoading = Boolean(options.loading);
      if (result?.sources && !result.preview) {
        if (!filters.agent_id) data.connectionSources = result.sources;
        else data.connectionSources = (data.connectionSources || result.sources).map(source => result.sources.find(fresh => fresh.agent_id === source.agent_id) || source);
      }
      shell(view(result, filters, data.connectionSources || [], { hasPrevious: cursors.length > 0, page: cursors.length + 1, filtersOpen: data.connectionFiltersOpen ?? true, ...options }), "客户端连接 IP");
      bindClientConnections({
        current,
        filterToggle: open => { data.connectionFiltersOpen = open; },
        refresh: () => { void load({ cursor, cursors, force: true }); },
        search: values => { data.connectionFilters = { ...filters, ...values }; void load(); },
        selectAgent: agent_id => { data.connectionFilters = { ...filters, agent_id }; void load(); },
        month: values => { data.connectionFilters = { ...filters, ...values, date: defaultConnectionFilters().date, period: "month" }; void load(); },
        recent: () => { data.connectionFilters = { ...filters, ...defaultConnectionFilters() }; void load(); },
        next: () => { if (result?.next_cursor) void load({ cursor: result.next_cursor, cursors: [...cursors, cursor] }); },
        previous: () => { if (cursors.length) void load({ cursor: cursors.at(-1), cursors: cursors.slice(0, -1) }); },
      });
    };
    try {
      const query = connectionQuery(filters, cursor, nodeOrder), key = query.toString();
      const cached = cache.get(key);
      result = cached?.result || cache.preview(query);
      let fetchedAt = cached?.at;
      let locationsAttempted = cached?.locationsAttempted;
      if (!force && cached?.fresh) {
        paint();
      } else {
        paint({ loading: true });
        result = await api(`/client-connections?${query}&locations=cached`, { signal });
        if (!current()) return;
        if (!filters.agent_id && result.sources) {
          migrateLegacyNodeOrder(result.sources.map(source => ({ id: source.agent_id })));
          if (JSON.stringify(savedNodeOrder()) !== orderKey) return load();
        }
        if (force) cache.clear();
        fetchedAt = Date.now();
        locationsAttempted = false;
        cache.set(key, result, fetchedAt);
        paint();
      }
      // Location lookup never holds up the history, filters or pagination.
      if (!locationsAttempted && result.records?.some(row => !row.location?.non_public && (!row.location?.country_code || row.location.country_code === "CN" && !row.location.province))) {
        const locationQuery = new URLSearchParams(query);
        // Pin enrichment to the same engine/node/IP position as the visible page.
        if (result.page_cursor) locationQuery.set("cursor", result.page_cursor);
        void api(`/client-connections?${locationQuery}&locations=only`, { signal }).then(enriched => {
          if (!current()) return;
          const locations = new Map(enriched.records.map(row => [row.id, row.location]));
          result.records = result.records.map(row => ({ ...row, location: locations.get(row.id) || row.location }));
          cache.set(key, result, fetchedAt, true);
          paint();
        }).catch(() => { if (current() && !signal.aborted) cache.set(key, result, fetchedAt, true); });
      }
    } catch (error) {
      if (current()) {
        if (error.status === 401 || error.status === 403) {
          result = null;
          cache.clear();
          delete data.connectionSources;
        }
        paint({ error: result && !result.preview ? `更新失败，保留上次结果：${error.message}` : error.message });
      }
    }
  }
  return load;
}
