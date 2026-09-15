const DEMO_NODES = [
  { node: "东京-01", ip: "103.21.244.18", region: "日本东京", score: 96, latency: 42, loss: 0, status: "excellent", checked_at: "09:32", asn: "AS2516 KDDI", risk: "低风险", unlock: "Netflix / Disney+" },
  { node: "新加坡-02", ip: "45.76.148.21", region: "新加坡", score: 89, latency: 86, loss: 0.4, status: "good", checked_at: "09:31", asn: "AS20473 Vultr", risk: "低风险", unlock: "Netflix" },
  { node: "洛杉矶-03", ip: "172.82.155.7", region: "美国洛杉矶", score: 74, latency: 182, loss: 1.8, status: "warning", checked_at: "09:30", asn: "AS63949 Linode", risk: "中风险", unlock: "部分受限" },
  { node: "法兰克福-04", ip: "45.12.33.90", region: "德国法兰克福", score: 61, latency: 228, loss: 3.2, status: "poor", checked_at: "09:29", asn: "AS24940 Hetzner", risk: "高风险", unlock: "未解锁" },
];

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);
const dayLabel = (value) => new Intl.DateTimeFormat("zh-CN", { month: "long", day: "numeric", weekday: "short" }).format(new Date(`${value}T00:00:00`));
const localDate = (date) => [date.getFullYear(), String(date.getMonth() + 1).padStart(2, "0"), String(date.getDate()).padStart(2, "0")].join("-");

export function demoIPQuality(date) {
  const offset = (Number(date?.slice(-2)) || 0) % 7;
  return {
    date,
    checked_at: `${date} 09:32 UTC+8`,
    summary: { total: DEMO_NODES.length, excellent: 1, good: 1, warning: 1, poor: 1, average_score: 80 + (offset % 3) },
    nodes: DEMO_NODES.map((node, index) => ({ ...node, score: Math.max(0, node.score - (offset && index === 2 ? 2 : 0)) })),
    demo: true,
  };
}

const statusText = { excellent: "优秀", good: "良好", warning: "需关注", poor: "较差" };

export function createIPQualityView(ctx) {
  const shell = ctx.shell;
  return ({ date, data, loading = false, error = "" }) => {
    const payload = data || demoIPQuality(date);
    const summary = payload.summary || {};
    const nodes = Array.isArray(payload.nodes) ? payload.nodes : [];
    const cards = nodes.map((node) => {
      const status = node.status || (node.score >= 90 ? "excellent" : node.score >= 75 ? "good" : node.score >= 60 ? "warning" : "poor");
      return `<article class="ip-quality-node-card ${esc(status)}"><header><div><strong>${esc(node.node || node.name || "未命名节点")}</strong><small>${esc(node.region || "未知地区")}</small></div><span class="ip-quality-badge ${esc(status)}"><i></i>${esc(statusText[status] || status)}</span></header><div class="ip-quality-card-score"><div><b>${esc(node.score ?? "—")}</b><span>/ 100</span></div><div class="ip-quality-score-track"><span style="width:${Math.max(0, Math.min(100, Number(node.score) || 0))}%"></span></div></div><dl><div><dt>出口 IP</dt><dd>${esc(node.ip || "—")}</dd></div><div><dt>延迟</dt><dd>${esc(node.latency ?? "—")} ms</dd></div><div><dt>丢包率</dt><dd>${esc(node.loss ?? "—")}%</dd></div><div><dt>检测时间</dt><dd>${esc(node.checked_at || "—")}</dd></div></dl><details class="ip-quality-details"><summary>查看完整报告 <span>⌄</span></summary><div><p><span>ASN / 运营商</span><b>${esc(node.asn || "—")}</b></p><p><span>风险等级</span><b>${esc(node.risk || "—")}</b></p><p><span>流媒体解锁</span><b>${esc(node.unlock || "—")}</b></p><p><span>DNS / WebRTC</span><b>正常</b></p></div></details></article>`;
    }).join("");
    const issue = error ? `<div class="alert warning ip-quality-alert">当前为布局演示数据 · 真实检测接口尚未接入</div>` : "";
    shell(`<div class="ip-quality-workspace"><header class="ip-quality-head"><div><p class="eyebrow">IPQuality · 日期历史</p><h2>IP 质量检测</h2><p>按日期查看节点出口 IP 的连通性、延迟与风险评分。</p></div><div class="ip-quality-date-picker"><button type="button" class="button small" data-ip-quality-day="-1" aria-label="前一天">‹</button><label class="ip-quality-date"><span>检测日期</span><input type="date" data-ip-quality-date value="${esc(date)}" max="${esc(localDate(new Date()))}"></label><button type="button" class="button small" data-ip-quality-day="1" aria-label="后一天">›</button></div></header>${issue}<section class="ip-quality-summary"><div><span>检测节点</span><strong>${esc(summary.total ?? nodes.length)}</strong><small>个节点</small></div><div><span>平均评分 <em>演示</em></span><strong>${esc(summary.average_score ?? "—")}</strong><small>/ 100</small></div><div><span>优秀节点</span><strong class="good-text">${esc(summary.excellent ?? 0)}</strong><small>个</small></div><div><span>需关注</span><strong class="warn-text">${esc((summary.warning ?? 0) + (summary.poor ?? 0))}</strong><small>个</small></div></section><section class="ip-quality-toolbar"><div><h3>${esc(dayLabel(date))}</h3><span>${esc(payload.checked_at || "数据更新时间未知")}</span></div><button type="button" class="button small" data-ip-quality-refresh>重新读取</button></section>${loading ? '<div class="empty large"><strong>正在读取检测结果…</strong></div>' : `<div class="ip-quality-grid">${cards || '<div class="empty large"><strong>当天没有检测记录</strong><p>选择其他日期查看历史数据。</p></div>'}</div>`}</div>`, "IP 质量");
  };
}
