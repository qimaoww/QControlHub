import { normalizeClientAccessFilters, filterClientAccessEntries, loadClientDisplayFormat } from "./client-access-model.js";
import { createClientAccessResults } from "./client-access-results.js";

export function createClientAccessView(ctx, { masonry }) {
  const { state, engines, esc, engineName, shell } = ctx;
  const renderResults = createClientAccessResults(ctx);
  return function renderClientAccess() {
    const entries = state.data.clientAccessEntries || [];
    const agents = state.data.agents || [];
    const loaded = state.data.clientAccessEntries != null;
    const displayFormat = state.data.clientDisplayFormat ?? loadClientDisplayFormat();
    state.data.clientDisplayFormat = displayFormat;
    const filters = loaded ? normalizeClientAccessFilters(entries, agents, {
      agent: state.data.accessAgent, engine: state.data.accessEngine, query: state.data.accessQuery,
    }) : { agent: state.data.accessAgent || "", engine: state.data.accessEngine || "", query: state.data.accessQuery || "" };
    state.data.accessAgent = filters.agent;
    state.data.accessEngine = filters.engine;
    state.data.accessQuery = filters.query;
    const filtered = filterClientAccessEntries(entries, filters);
    const scoped = filters.agent ? entries.filter(entry => entry.agent_id === filters.agent) : entries;
    const count = items => items.reduce((sum, entry) => sum + (entry.profiles || []).length, 0);
    const scopeName = filters.agent ? agents.find(agent => agent.id === filters.agent)?.name || scoped[0]?.agent_name || "所选节点" : "全部节点";
    const engineFilters = ["", ...engines.filter(engine => scoped.some(entry => entry.engine === engine))].map(engine =>
      `<a class="button small${filters.engine === engine ? " active" : ""}" href="#client-access" data-filter-engine="${esc(engine)}"${filters.engine === engine ? ' aria-current="true"' : ""}>${engine ? esc(engineName(engine)) : "全部内核"}<b>${count(engine ? scoped.filter(entry => entry.engine === engine) : scoped)}</b></a>`).join("");
    const busy = state.data.clientAccessLoading;
    const error = state.data.clientAccessError;
    const status = `<div class="client-access-status" role="status" data-client-load-status${error || busy ? "" : " hidden"}>${error ? `<span>${loaded ? "刷新失败，保留上次配置。" : "加载失败。"}${esc(error)}</span><button class="button small" type="button" data-refresh-client-access>重试</button>` : "正在加载客户端配置…"}</div>`;
    const toolbar = `<section class="client-access-toolbar" aria-label="客户端配置筛选"><div class="client-access-scope"><div><h1>${esc(scopeName)}</h1><p data-client-result-count role="status">${loaded ? `${new Set(filtered.map(entry => entry.agent_id)).size} 个节点 · ${count(filtered)}${filters.engine || filters.query ? ` / ${count(scoped)}` : ""} 个入站${filters.engine ? ` · ${esc(engineName(filters.engine))}` : ""}${filters.query ? ` · 搜索“${esc(filters.query)}”` : ""}` : "选择节点，查找入站并复制客户端配置"}</p></div><div class="client-access-toolbar-actions"><a class="button small" href="#substore-sync">Sub-Store 同步</a><button class="button small" type="button" data-refresh-client-access${busy ? ' disabled aria-busy="true"' : ""}>刷新</button></div></div>
      <div class="client-access-tools"><form id="client-search" class="client-access-search" role="search"><input type="search" name="q" value="${esc(filters.query)}" aria-label="搜索入站" placeholder="搜索名称、地址、协议或端口" autocomplete="off" enterkeyhint="search"><button class="button small" type="submit">搜索</button>${filters.query ? '<button class="button small" type="button" data-clear-search>清除</button>' : ""}</form><label class="client-display-format"><span>导出格式</span><select data-client-display-format aria-label="导出格式"><option value="url"${displayFormat === "url" ? " selected" : ""}>URL</option><option value="mihomo"${displayFormat === "mihomo" ? " selected" : ""}>Mihomo YAML</option></select></label></div>
      <nav aria-label="按内核筛选">${engineFilters}</nav></section>`;
    const results = loaded ? renderResults(filtered, entries, agents, filters, displayFormat) : '<div class="client-access-loading" aria-hidden="true"></div>';
    masonry.disconnect();
    shell(`<section class="client-access-workspace compact" data-client-access-page${error ? ' data-refresh-error="1"' : ""}>${toolbar}${status}<span class="visually-hidden" role="status" data-client-copy-status></span><div class="client-access-node-grid qch-swap-panel${filtered.length ? "" : " empty"}" data-refresh-key="client-results" data-motion-region="client-results" data-motion-key="${esc(JSON.stringify([filters.agent, filters.engine, filters.query, displayFormat]))}">${results}</div></section>`, "客户端配置", { viewKey: "client-access" });
  };
}
