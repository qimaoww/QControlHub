import { connectionLocationLabel } from "./client-connection-model.js";

export function clientConnectionTable({ records, loading, esc, engineName, date }) {
  const ports = row => {
    const values = [...new Set((row.endpoints?.length ? row.endpoints : [row]).map(endpoint => endpoint.local_port || 0))].sort((a, b) => a - b);
    return `<div class="connection-port-list">${values.map(port => port ? `<code>${esc(port)}</code>` : '<span class="connection-port-missing" title="日志中未识别到入站端口">—</span>').join("")}</div>`;
  };
  const times = row => `<div class="connection-times"><time datetime="${esc(row.last_seen || "")}">最近 ${esc(date(row.last_seen))}</time><time datetime="${esc(row.first_seen || "")}">首次 ${esc(date(row.first_seen))}</time></div>`;
  return `<div class="connection-table-scroll" tabindex="0" role="region" aria-label="客户端来源 IP 明细"><table>
    <thead><tr><th>客户端来源 IP</th><th>国家／地区</th><th>节点</th><th>内核</th><th>入站端口</th><th>观测时间</th></tr></thead>
    <tbody>${records.map(row => `<tr class="connection-ip-row" data-refresh-key="connection-${esc(row.id)}"><td data-label="来源 IP"><code>${esc(row.client_ip)}</code></td><td data-label="国家／地区">${esc(connectionLocationLabel(row.location))}</td><td data-label="节点"><strong>${esc(row.agent_name)}</strong></td><td data-label="内核"><span class="engine-badge ${esc(row.engine)}">${esc(engineName(row.engine))}</span></td><td data-label="入站端口">${ports(row)}</td><td data-label="观测时间">${times(row)}</td></tr>`).join("") || `<tr><td colspan="6"><div class="empty">${loading ? "加载中…" : "暂无连接记录"}</div></td></tr>`}</tbody>
  </table></div>`;
}
