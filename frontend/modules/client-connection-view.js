import { clientConnectionFilters } from "./client-connection-filters.js";
import { connectionAddress, connectionSourceLabel, connectionSourceDetail, connectionLocationLabel } from "./client-connection-model.js";

export function createClientConnectionView({ esc, engineName, date }) {
  return function connectionView(result, filters, sources, { loading = false, error = "", hasPrevious = false } = {}) {
    const records = result?.records || [], timeline = result?.timeline || [];
    const maxFlows = Math.max(1, ...timeline.map(item => item.flows));
    const width = 960, start = new Date(filters.since).getTime(), end = new Date(filters.until).getTime();
    const span = Math.max(60000, end - start);
    const interval = { minute: 60000, hour: 3600000, day: 86400000 }[filters.bucket] || 3600000;
    const barWidth = Math.max(1, Math.min(28, interval / span * width * .65));
    const bars = timeline.map(bucket => {
      const height = Math.max(2, bucket.flows / maxFlows * 100);
      const x = Math.max(0, Math.min(width - 1, (new Date(bucket.time).getTime() - start) / span * width));
      return `<g><title>${esc(date(bucket.time))} · ${bucket.flows} 连接 · ${bucket.ips} IP</title><rect x="${x}" y="${110 - height}" width="${Math.min(barWidth, width - x)}" height="${height}" rx="2"/></g>`;
    }).join("");
    const reportSources = result?.sources || [];
    const warnings = reportSources.filter(source => source.status !== "ok" || source.truncated || !source.updated_at || Date.now() - new Date(source.updated_at).getTime() > 90000);
    return `<div class="client-connections">
      <section class="workspace-panel connection-overview">
        <header><h2>客户端连接 IP</h2><span class="connection-scope">${filters.include_non_public === "true" ? "全部来源" : "仅公网来源"}</span></header>
        <div class="connection-summary"><div><span>独立 IP</span> <strong>${result ? result.ips : "—"}</strong></div><div><span>观测连接</span> <strong>${result ? result.flows : "—"}</strong></div><div><span>采集节点</span> <strong>${result ? reportSources.length : "—"}</strong></div><div><span>需关注</span> <strong class="${warnings.length ? "warn-text" : ""}">${result ? warnings.length : "—"}</strong></div></div>
        <details class="connection-sources"><summary><span>采集状态</span><span class="status-label ${warnings.length ? "warn" : "ok"}">${!result ? (loading ? "加载中…" : "未加载") : !reportSources.length ? "暂无节点" : warnings.length ? `${warnings.length} 台需关注` : "采集正常"}</span><span class="connection-source-toggle">查看详情</span></summary><div class="connection-source-list">${reportSources.map(source => `<article class="connection-source"><header><strong>${esc(source.agent_name)}</strong><span>${esc(connectionSourceLabel(source))}</span></header><div class="connection-source-detail" title="${esc(source.detail || "")}">${esc(connectionSourceDetail(source))}</div>${source.updated_at ? `<time>最近上报 ${esc(date(source.updated_at))}</time>` : ""}</article>`).join("") || '<div class="empty">暂无采集报告</div>'}</div></details>
      </section>

      ${clientConnectionFilters({ filters, loading, esc, engineName })}
      ${error ? `<div class="connection-error" role="alert">${esc(error)}</div>` : ""}
      <section class="workspace-panel connection-history-panel">
        <header><h3>连接时间线</h3><select name="bucket" form="connection-query" data-connection-bucket aria-label="时间粒度">${[["minute", "分钟"], ["hour", "小时"], ["day", "天"]].map(([value, label]) => `<option value="${value}"${filters.bucket === value ? " selected" : ""}>${label}</option>`).join("")}</select></header>
        <div class="connection-timeline" aria-label="连接时间线">${timeline.length ? `<svg viewBox="0 0 ${width} 120" preserveAspectRatio="none" role="img" aria-label="连接数"><path class="connection-chart-grid" d="M0 10H${width}M0 60H${width}M0 110H${width}"/>${bars}</svg><div class="connection-chart-axis"><time>${esc(date(new Date(start).toISOString()))}</time><time>${esc(date(new Date(end).toISOString()))}</time></div>` : `<div class="empty">${loading ? "加载中…" : "暂无记录"}</div>`}</div>
      </section>
      <section class="workspace-panel connection-detail-panel">
        <header><h3>入站连接</h3><span class="connection-result-count">本页 ${records.length} 条</span></header>
        <div class="connection-table-scroll" tabindex="0" role="region" aria-label="入站连接明细"><table><thead><tr><th>服务器</th><th>内核 / 入站</th><th>来源 IP : 端口</th><th>国家／地区</th><th>入站 IP : 端口</th><th>观测时间</th></tr></thead><tbody>${records.map(row => `<tr><td data-label="服务器"><strong>${esc(row.agent_name)}</strong></td><td data-label="内核 / 入站"><div class="connection-inbound"><span class="engine-badge ${esc(row.engine)}">${esc(engineName(row.engine))}</span><strong>${esc(row.inbound)}</strong><small>${esc(row.protocol)} · ${esc(row.transport.toUpperCase())}</small></div></td><td data-label="来源"><code>${esc(connectionAddress(row.client_ip, row.client_port))}</code></td><td data-label="国家／地区">${esc(connectionLocationLabel(row.location))}</td><td data-label="入站"><code>${esc(connectionAddress(row.local_ip, row.local_port))}</code></td><td data-label="观测时间"><div class="connection-times"><time>首次 ${esc(date(row.first_seen))}</time><time>最近 ${esc(date(row.last_seen))}</time></div></td></tr>`).join("") || `<tr><td colspan="6"><div class="empty">${loading ? "加载中…" : "暂无连接记录"}</div></td></tr>`}</tbody></table></div>
        <footer class="connection-pagination"><button class="button small" data-connection-previous${!hasPrevious || loading ? " disabled" : ""}>上一页</button><button class="button small" data-connection-next${!result?.next_before || loading ? " disabled" : ""}>下一页</button></footer>
      </section>
    </div>`;
  };
}
