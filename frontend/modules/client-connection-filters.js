import { connectionSourceLabel } from "./client-connection-model.js";
import { orderNodesBySavedOrder } from "./node-order.js";

export function clientConnectionSidebar({ state, esc }) {
  const selected = state.data.connectionFilters?.agent_id || "";
  const nodes = orderNodesBySavedOrder((state.data.connectionSources || []).map(source => ({ ...source, id: source.agent_id })));
  return `<a class="context-primary ${selected ? "" : "active"}" href="#client-connections" data-connection-agent="">全部节点</a><div class="context-section-label"><span>按节点查看</span><b>${nodes.length}</b></div><nav class="context-list" aria-label="连接 IP 节点">${nodes.map(source => `<a class="${selected === source.agent_id ? "active" : ""}" href="#client-connections" data-connection-agent="${esc(source.agent_id)}"><i class="status-dot ${source.status === "ok" && source.updated_at ? "ok" : ""}"></i><span><strong>${esc(source.agent_name)}</strong><small>${esc(connectionSourceLabel(source))}</small></span></a>`).join("") || `<p>${state.data.connectionLoading ? "正在读取节点…" : "暂无节点"}</p>`}</nav>`;
}

export function clientConnectionFilters({ filters, esc, engineName }) {
  const monthly = filters.period === "month";
  const advanced = filters.include_non_public === "true";
  const select = (name, label, options) => `<label>${label}<select name="${name}"><option value="">全部</option>${options.map(([value, text]) => `<option value="${esc(value)}"${filters[name] === value ? " selected" : ""}>${esc(text)}</option>`).join("")}</select></label>`;
  const input = (name, label, type = "text", extra = "") => `<label>${label}<input name="${name}" type="${type}" value="${esc(filters[name] || "")}" ${extra}></label>`;
  const active = [monthly ? `${filters.date.slice(0, 7)} · 本月全部` : filters.date, filters.engine && engineName(filters.engine), filters.client_ip, filters.include_non_public === "true" && "包含非公网"].filter(Boolean);
  return `<details class="connection-filter-panel"><summary>筛选条件<span>${active.length ? esc(active.join(" · ")) : "日期 / 来源 IP / 内核"}</span></summary>
      <form id="connection-query" data-connection-filters>
        <input type="hidden" name="period" value="${monthly ? "month" : "day"}">
        <div class="connection-filters">
          ${select("engine", "内核", ["mihomo", "xray", "sing-box", "ss-rust"].map(e => [e, engineName(e)]))}
          ${input("client_ip", "客户端来源 IP", "search", 'placeholder="IPv4 / IPv6"')}
          ${monthly ? `<label>查询月份（本地时间）<input name="month_display" type="month" value="${esc(filters.date.slice(0, 7))}" disabled></label>` : input("date", "查询日期（本地时间）", "date", "required")}
        </div>
        <footer class="connection-filter-actions">
          <details class="connection-advanced"${advanced ? " open" : ""}><summary>更多筛选</summary><div>
            <label>来源范围<select name="include_non_public"><option value=""${filters.include_non_public === "true" ? "" : " selected"}>仅公网</option><option value="true"${filters.include_non_public === "true" ? " selected" : ""}>包含非公网</option></select></label>
          </div></details>
          <div class="connection-query-actions"><button class="button small" type="button" data-connection-recent>今天</button><button class="button small${monthly ? " primary" : ""}" type="button" data-connection-month aria-pressed="${monthly}">本月全部</button><button class="button primary small" type="submit">查询</button></div>
        </footer>
      </form>
  </details>`;
}
