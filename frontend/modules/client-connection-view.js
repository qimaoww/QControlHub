import { clientConnectionTable } from "./client-connection-table.js";
import { clientConnectionFilters } from "./client-connection-filters.js";
import { connectionSourceLabel, connectionSourceDetail } from "./client-connection-model.js";

export function createClientConnectionView({ esc, engineName, date }) {
  return function connectionView(result, filters, sources, { loading = false, error = "", hasPrevious = false, page = 1 } = {}) {
    const records = result?.records || [];
    const reportSources = result?.sources || [];
    const scope = filters.agent_id ? sources.find(source => source.agent_id === filters.agent_id)?.agent_name || "所选节点" : "全部节点";
    const warnings = reportSources.filter(source => source.status !== "ok" || source.truncated || !source.updated_at);
    return `<div class="client-connections">
      <header class="core-log-header connection-page-header"><div><h2>连接 IP</h2><span class="connection-scope">当前范围：<strong>${esc(scope)}</strong><i>·</i>${filters.include_non_public === "true" ? "全部来源" : "仅公网来源"}</span></div><button class="button small" type="button" data-connection-refresh${loading ? " disabled" : ""}>${loading ? "正在读取…" : "刷新"}</button></header>
      <section class="connection-overview" aria-label="连接概览">
        <div class="connection-summary"><div><span>独立 IP</span> <strong>${result?.ips ?? "—"}</strong></div><div><span>观测连接</span> <strong>${result?.flows ?? "—"}</strong></div><div><span>采集节点</span> <strong>${result ? reportSources.length : "—"}</strong></div><div><span>需关注</span> <strong class="${warnings.length ? "warn-text" : ""}">${result ? warnings.length : "—"}</strong></div></div>
        <details class="connection-sources"><summary><span>采集状态</span><span class="status-label ${warnings.length ? "warn" : "ok"}">${!result ? (loading ? "加载中…" : "未加载") : !reportSources.length ? "暂无节点" : warnings.length ? `${warnings.length} 台需关注` : "日志已读取"}</span><span class="connection-source-toggle">查看详情</span></summary><div class="connection-source-list">${reportSources.map(source => `<article class="connection-source"><header><strong>${esc(source.agent_name)}</strong><span>${esc(connectionSourceLabel(source))}</span></header><div class="connection-source-detail" title="${esc(source.detail || "")}">${esc(connectionSourceDetail(source))}</div>${source.updated_at ? `<time>最近日志 ${esc(date(source.updated_at))}</time>` : ""}</article>`).join("") || '<div class="empty">暂无内核日志</div>'}</div></details>
      </section>

      ${error ? `<div class="connection-error" role="alert">${esc(error)}</div>` : ""}
      <section class="workspace-panel connection-detail-panel">
        <header><h3>客户端来源 IP</h3><span class="connection-result-count">${loading ? result ? "正在更新…" : "正在读取…" : result?.preview ? `已显示 ${records.length} 条，结果待更新` : `本页 ${records.length} 条`}</span></header>
        ${clientConnectionFilters({ filters, esc, engineName })}
        ${clientConnectionTable({ records, loading, esc, engineName, date })}
        <footer class="connection-pagination"><span>第 ${page} 页</span><button class="button small" data-connection-previous${!hasPrevious ? " disabled" : ""}>上一页</button><button class="button small" data-connection-next${!result?.next_cursor ? " disabled" : ""}>下一页</button></footer>
      </section>
    </div>`;
  };
}
