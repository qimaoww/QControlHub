import { connectionAddress, connectionSourceLabel, connectionLocationLabel } from "./client-connection-model.js";

export function createClientConnectionView({ esc, engineName, date }) {
  return function connectionView(result, filters, sources, { loading = false, error = "", hasPrevious = false } = {}) {
    const select = (name, label, options) => `<label>${label}<select name="${name}"><option value="">全部</option>${options.map(([value, text]) => `<option value="${esc(value)}"${filters[name] === value ? " selected" : ""}>${esc(text)}</option>`).join("")}</select></label>`;
    const input = (name, label, type = "text", extra = "") => `<label>${label}<input name="${name}" type="${type}" value="${esc(filters[name] || "")}" ${extra}></label>`;
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
    const warnings = sources.filter(source => source.status !== "ok" || source.truncated || !source.updated_at || Date.now() - new Date(source.updated_at).getTime() > 90000);
    const advanced = filters.protocol || filters.inbound || filters.transport;
    return `<div class="client-connections">
      <form id="connection-query" data-connection-filters class="workspace-panel connection-filter-panel">
        <div class="connection-filters">
          ${select("agent_id", "服务器", sources.map(s => [s.agent_id, s.agent_name]))}
          ${select("engine", "内核", ["mihomo", "xray", "sing-box", "ss-rust"].map(e => [e, engineName(e)]))}
          ${input("client_ip", "入站来源 IP", "search", 'placeholder="IPv4 / IPv6"')}
          ${input("port", "入站端口", "number", 'min="1" max="65535" placeholder="全部端口"')}
          ${input("since", "开始时间", "datetime-local", "required")}
          ${input("until", "结束时间", "datetime-local", "required")}
        </div>
        <footer class="connection-filter-actions">
          <details class="connection-advanced"${advanced ? " open" : ""}><summary>更多筛选</summary><div>
            ${input("protocol", "入站协议", "text", 'placeholder="vless / shadowsocks" maxlength="40"')}
            ${input("inbound", "入站名称", "text", 'placeholder="全部入站" maxlength="400"')}
            ${select("transport", "传输", [["tcp", "TCP"], ["udp", "UDP"]])}
          </div></details>
          <div class="connection-query-actions"><button class="button small" type="button" data-connection-recent${loading ? " disabled" : ""}>最近 24 小时</button><button class="button primary small" type="submit"${loading ? " disabled" : ""}>${loading ? "查询中…" : "查询"}</button></div>
        </footer>
      </form>
      ${error ? `<div class="connection-error" role="alert">${esc(error)}</div>` : ""}
      <section class="workspace-panel connection-history-panel">
        <header><h3>连接时间线</h3><div class="connection-summary"><span>独立 IP <strong>${result?.ips || 0}</strong></span><span>观测连接 <strong>${result?.flows || 0}</strong></span></div><select name="bucket" form="connection-query" data-connection-bucket aria-label="时间粒度">${[["minute", "分钟"], ["hour", "小时"], ["day", "天"]].map(([value, label]) => `<option value="${value}"${filters.bucket === value ? " selected" : ""}>${label}</option>`).join("")}</select></header>
        <div class="connection-timeline" aria-label="连接时间线">${timeline.length ? `<svg viewBox="0 0 ${width} 120" preserveAspectRatio="none" role="img" aria-label="连接数"><path class="connection-chart-grid" d="M0 10H${width}M0 60H${width}M0 110H${width}"/>${bars}</svg><div class="connection-chart-axis"><time>${esc(date(new Date(start).toISOString()))}</time><time>${esc(date(new Date(end).toISOString()))}</time></div>` : `<div class="empty">${loading ? "加载中…" : "暂无记录"}</div>`}</div>
      </section>
      <section class="workspace-panel connection-detail-panel">
        <header><h3>入站连接</h3><span class="connection-result-count">${records.length} 条</span><details class="connection-sources"><summary class="status-label ${warnings.length ? "warn" : "ok"}">${!sources.length ? (loading ? "加载中…" : "暂无节点") : warnings.length ? `${warnings.length} 台采集异常` : "采集正常"}</summary><div>${sources.map(source => `<div><strong>${esc(source.agent_name)}</strong><span title="${esc(source.detail || "")}">${esc(connectionSourceLabel(source))}</span></div>`).join("") || '<div>暂无服务器</div>'}</div></details></header>
        <div class="connection-table-scroll"><table><thead><tr><th>服务器</th><th>内核 / 入站</th><th>来源 IP : 端口</th><th>国家／地区</th><th>入站 IP : 端口</th><th>首次观测</th><th>最后观测</th></tr></thead><tbody>${records.map(row => `<tr><td><strong>${esc(row.agent_name)}</strong></td><td><div class="connection-inbound"><span class="engine-badge ${esc(row.engine)}">${esc(engineName(row.engine))}</span><strong>${esc(row.inbound)}</strong><small>${esc(row.protocol)} · ${esc(row.transport.toUpperCase())}</small></div></td><td><code>${esc(connectionAddress(row.client_ip, row.client_port))}</code></td><td>${esc(connectionLocationLabel(row.location))}</td><td><code>${esc(connectionAddress(row.local_ip, row.local_port))}</code></td><td><time>${esc(date(row.first_seen))}</time></td><td><time>${esc(date(row.last_seen))}</time></td></tr>`).join("") || `<tr><td colspan="7"><div class="empty">${loading ? "加载中…" : "暂无连接记录"}</div></td></tr>`}</tbody></table></div>
        <footer class="connection-pagination"><button class="button small" data-connection-previous${!hasPrevious || loading ? " disabled" : ""}>上一页</button><button class="button small" data-connection-next${!result?.next_before || loading ? " disabled" : ""}>下一页</button></footer>
      </section>
    </div>`;
  };
}
