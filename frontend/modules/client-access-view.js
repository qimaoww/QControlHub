import { normalizeClientAccessFilters, filterClientAccessEntries } from "./client-access-model.js";
import { createClientAccessResults } from "./client-access-results.js";
export function createClientAccessView(ctx, { masonry }) {
  const { state, engines, esc, engineName, shell } = ctx;
  const renderResults = createClientAccessResults(ctx);
  function renderClientAccess() {
    const entries = state.data.clientAccessEntries || [];
    const agents = state.data.agents || [];
    const filters = normalizeClientAccessFilters(entries, agents, {
      agent: state.data.accessAgent,
      engine: state.data.accessEngine,
      query: state.data.accessQuery,
    });
    state.data.accessAgent = filters.agent;
    state.data.accessEngine = filters.engine;
    state.data.accessQuery = filters.query;

    const filtered = filterClientAccessEntries(entries, filters);
    const scopedEntries = filters.agent
      ? entries.filter((entry) => entry.agent_id === filters.agent)
      : entries;
    const scopedProfiles = scopedEntries.reduce(
      (total, entry) => total + (entry.profiles || []).length,
      0,
    );
    const results = renderResults(filtered, entries, agents, filters);
    const relevantEngines = new Set(scopedEntries.map((entry) => entry.engine));
    const engineFilters = engines
      .filter((engine) => relevantEngines.has(engine))
      .map((engine) => {
        const count = scopedEntries
          .filter((entry) => entry.engine === engine)
          .reduce((total, entry) => total + (entry.profiles || []).length, 0);
        return `<a class="${filters.engine === engine ? "active" : ""}" href="#client-access" data-filter-engine="${esc(engine)}">${esc(engineName(engine))}<b>${count}</b></a>`;
      })
      .join("");
    const filtersMarkup = scopedEntries.length
      ? `<section class="client-access-toolbar" aria-label="客户端配置筛选"><nav aria-label="按内核筛选"><a class="${filters.engine ? "" : "active"}" href="#client-access" data-filter-engine="">全部<b>${scopedProfiles}</b></a>${engineFilters}</nav><div><a class="button small" href="#substore-sync">Sub-Store 同步</a><button class="button small" type="button" data-refresh-client-access>刷新</button><details class="client-access-search-menu" ${filters.query ? "open" : ""}><summary>${filters.query ? `搜索：${esc(filters.query)}` : "搜索"}</summary><form id="client-search"><input type="search" name="q" value="${esc(filters.query)}" aria-label="搜索入站" placeholder="节点、地址、协议或入站" autocomplete="off"><button class="button primary small" type="submit">搜索</button>${filters.query ? '<button class="button small" type="button" data-clear-search>清除</button>' : ""}</form></details></div></section>`
      : '<section class="client-access-toolbar empty"><button class="button small" type="button" data-refresh-client-access>刷新</button></section>';
    masonry.disconnect();
    shell(
      `<section class="client-access-workspace compact" data-client-access-page><h1 class="visually-hidden">客户端配置</h1>${filtersMarkup}<div class="client-access-node-grid qch-swap-panel${filtered.length ? "" : " empty"}" data-refresh-key="client-results-${esc(filters.agent || "all")}-${esc(filters.engine || "all")}-${esc(filters.query || "all")}">${results}</div></section>`,
      "客户端配置",
      { viewKey: `client-access-${filters.agent || "all"}-${filters.engine || "all"}-${filters.query || "all"}` },
    );
  }

  return renderClientAccess;
}
