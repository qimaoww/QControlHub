import { orderNodesBySavedOrder } from "./node-order.js";

import { utcMonth, aggregateDashboardTrafficDays, taskActivity } from "./dashboard-model.js";
// Keep the shared edge between stacked segments square so the two colors
// touch without the small gap created by two independently rounded rects.
const trafficBarPath = (x, top, bottom, width, corners) => {
  const right = x + width;
  const height = Math.max(0, bottom - top);
  const radius = Math.min(2, width / 2, height / 2);
  const topLeft = corners.includes("top-left") ? radius : 0;
  const topRight = corners.includes("top-right") ? radius : 0;
  const bottomRight = corners.includes("bottom-right") ? radius : 0;
  const bottomLeft = corners.includes("bottom-left") ? radius : 0;
  return [
    `M ${x + topLeft} ${top}`,
    `H ${right - topRight}`,
    topRight ? `Q ${right} ${top} ${right} ${top + topRight}` : `V ${top}`,
    `V ${bottom - bottomRight}`,
    bottomRight ? `Q ${right} ${bottom} ${right - bottomRight} ${bottom}` : `H ${right}`,
    `H ${x + bottomLeft}`,
    bottomLeft ? `Q ${x} ${bottom} ${x} ${bottom - bottomLeft}` : `H ${x}`,
    `V ${top + topLeft}`,
    topLeft ? `Q ${x} ${top} ${x + topLeft} ${top}` : `H ${x}`,
    "Z",
  ].join(" ");
};

const trafficBarSegments = (x, top, middle, bottom, width = 14) => {
  const hasSent = middle > top;
  const hasReceived = bottom > middle;
  if (!hasSent && !hasReceived) return "";

  if (hasSent && hasReceived) {
    return `<path class="traffic-bar-sent" d="${trafficBarPath(x, top, middle, width, ["top-left", "top-right"])}"></path><path class="traffic-bar-received" d="${trafficBarPath(x, middle, bottom, width, ["bottom-left", "bottom-right"])}"></path>`;
  }

  const path = trafficBarPath(x, top, bottom, width, [
    "top-left",
    "top-right",
    "bottom-left",
    "bottom-right",
  ]);
  return `<path class="${hasSent ? "traffic-bar-sent" : "traffic-bar-received"}" d="${path}"></path>`;
};

export function createDashboardView(ctx, panelMetrics) {
  const {
    esc, engineName, heartbeat, statusTone, ago, short, actionName, shell,
    can = () => false,
    bytes = (value) => `${Number(value || 0)} B`,
    rate = (value) => `${Number(value || 0)} B/s`,
  } = ctx;
  return ({ overview, agents, tasks, trafficUsage, trafficMonth }) => {
  const dailyTraffic = aggregateDashboardTrafficDays(trafficUsage.days, trafficMonth);
  const receivedTraffic = dailyTraffic.reduce((sum, day) => sum + day.received_bytes, 0);
  const sentTraffic = dailyTraffic.reduce((sum, day) => sum + day.sent_bytes, 0);
  const usedTraffic = receivedTraffic + sentTraffic;
  const maxTrafficDay = Math.max(1, ...dailyTraffic.map((day) => day.used_bytes));
  const trafficChartHeight = 112;
  const trafficChartWidth = Math.max(20, dailyTraffic.length * 20);
  const trafficBars = dailyTraffic.map((day, index) => {
    const usedHeight = Math.round(day.used_bytes / maxTrafficDay * trafficChartHeight);
    const receivedHeight = Math.round(day.received_bytes / maxTrafficDay * trafficChartHeight);
    const sentHeight = Math.max(0, usedHeight - receivedHeight);
    const x = index * 20 + 3;
    const receivedY = trafficChartHeight - receivedHeight;
    const sentY = receivedY - sentHeight;
    return `<g><title>${esc(day.day)} · 接收 ${esc(bytes(day.received_bytes))} · 发送 ${esc(bytes(day.sent_bytes))} · 合计 ${esc(bytes(day.used_bytes))}</title>${trafficBarSegments(x, sentY, receivedY, trafficChartHeight)}</g>`;
  }).join("");
  const trafficAxis = dailyTraffic.map((day, index) => {
    const dayNumber = index + 1;
    return `<span>${index % 5 === 0 || index === dailyTraffic.length - 1 ? `${dayNumber}日` : ""}</span>`;
  }).join("");
  const [trafficYear, trafficMonthNumber] = trafficMonth.split("-").map(Number);
  const trafficMonthLabel = `${trafficYear}年${String(trafficMonthNumber).padStart(2, "0")}月`;
  const trafficMonthOptions = Array.from({ length: 12 }, (_, index) => `<button type="button" data-dashboard-month-option data-month-index="${index + 1}">${index + 1}月</button>`).join("");
  const trafficDetailRows = dailyTraffic.map((day) => `<tr><td>${esc(day.day)}</td><td>${bytes(day.received_bytes)}</td><td>${bytes(day.sent_bytes)}</td><td><b>${bytes(day.used_bytes)}</b></td><td>${rate(day.peak_receive_bps)} / ${rate(day.peak_send_bps)}</td></tr>`).join("");
  const trafficHistory = can("traffic.read") ? `<section class="traffic-history dashboard-traffic-history qch-swap-panel" id="traffic-usage" data-refresh-key="dashboard-traffic-${esc(trafficMonth)}">
    <header class="dashboard-traffic-head"><div class="dashboard-traffic-title"><h3>节点流量</h3><small>UTC 自然日</small></div><div class="dashboard-traffic-month"><details class="dashboard-month-picker" data-dashboard-traffic-month><summary aria-label="选择流量月份"><b data-dashboard-month-summary>${esc(trafficMonthLabel)}</b><i>⌄</i></summary><div class="dashboard-month-popover"><header><button type="button" data-dashboard-month-year-shift="-1" aria-label="上一年">‹</button><strong data-dashboard-month-year>${trafficYear}</strong><button type="button" data-dashboard-month-year-shift="1" aria-label="下一年">›</button></header><div class="dashboard-month-grid">${trafficMonthOptions}</div><footer><button type="button" data-dashboard-current-month>回到本月</button></footer></div></details></div></header>
    <div class="dashboard-traffic-summary"><div class="dashboard-traffic-total"><span>${trafficMonth === utcMonth() ? "本月累计" : "当月累计"}</span><strong>${bytes(usedTraffic)}</strong></div><dl><div><dt><i class="received" aria-hidden="true"></i>接收</dt><dd>${bytes(receivedTraffic)}</dd></div><div><dt><i class="sent" aria-hidden="true"></i>发送</dt><dd>${bytes(sentTraffic)}</dd></div></dl></div>
    <div class="traffic-chart-legend"><span class="received">接收</span><span class="sent">发送</span></div>
    <div class="traffic-month-chart dashboard-traffic-chart"><svg viewBox="0 0 ${trafficChartWidth} ${trafficChartHeight}" preserveAspectRatio="none" role="img" aria-label="${esc(trafficMonth)} 每日接收和发送流量图">${trafficBars}</svg>${usedTraffic ? "" : '<span class="dashboard-traffic-empty">当月暂无流量记录</span>'}</div><div class="dashboard-traffic-axis" aria-hidden="true">${trafficAxis}</div>
    <footer class="dashboard-traffic-actions"><button class="button small" type="button" data-dashboard-traffic-details>查看 ${dailyTraffic.length} 天明细</button><a href="#traffic">管理流量配额 →</a></footer>
    <dialog class="traffic-edit-dialog dashboard-traffic-dialog" data-dashboard-traffic-dialog aria-labelledby="dashboard-traffic-dialog-title"><header><span class="traffic-edit-icon" aria-hidden="true">↕</span><div><p class="eyebrow">每日用量</p><h2 id="dashboard-traffic-dialog-title">${esc(trafficMonth)} 流量明细</h2><p>接收、发送和峰值按 UTC 自然日汇总</p></div><button class="deploy-command-close" type="button" data-dashboard-traffic-close aria-label="关闭流量明细弹窗">×</button></header><div class="dashboard-traffic-detail-body"><table><thead><tr><th>日期</th><th>接收</th><th>发送</th><th>合计</th><th>接收 / 发送峰值</th></tr></thead><tbody>${trafficDetailRows}</tbody></table></div><footer class="dashboard-traffic-dialog-actions"><span>共 ${dailyTraffic.length} 个自然日</span><button class="button" type="button" data-dashboard-traffic-close>关闭</button></footer></dialog>
  </section>` : "";
  const fleet =
    orderNodesBySavedOrder(agents)
      .slice(0, 7)
      .map(
        (agent) =>
          `<a href="#settings-node-${esc(agent.id)}" data-dashboard-agent="${esc(agent.id)}"><span class="node-avatar ${statusTone(agent.status)}" aria-hidden="true">●</span><span class="fleet-node-info"><strong title="${esc(agent.name)}">${esc(agent.name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)}</small><span class="fleet-engines">${(agent.capabilities || []).map((engine) => `<em class="${esc(engine)}">${esc(engineName(engine))}</em>`).join("")}</span></span><span class="status-label ${statusTone(agent.status)}">${agent.status === "online" ? "在线" : "离线"}</span><time>${esc(heartbeat(agent.last_seen))}</time><i aria-hidden="true">›</i></a>`,
      )
      .join("") ||
    '<div class="empty compact"><strong>还没有节点</strong></div>';
  const agentNames = new Map(agents.map((agent) => [agent.id, agent.name]));
  const activity =
    taskActivity(tasks)
      .map(
        ({ task, count }) => {
          const taskEngineLabel = task.action === "upgrade-agent" ? "QAgent" : engineName(task.engine);
          const agentLabel = agentNames.get(task.agent_id) || short(task.agent_id);
          return `<a href="#tasks" data-dashboard-task="${esc(task.id)}"><i class="status-dot ${statusTone(task.status)}"></i><span><strong>${esc(actionName(task.action))}</strong><small title="${esc(agentLabel)}">${esc(agentLabel)} · ${esc(taskEngineLabel)}${count > 1 ? ` · 连续 ${count} 次` : ""}</small></span><time>${esc(ago(task.created_at))}</time><b aria-hidden="true">›</b></a>`;
        },
      )
      .join("") ||
    '<div class="empty compact"><strong>还没有任务</strong></div>';
  const stats = [
    { label: "在线节点", value: `${esc(overview.agents_online || 0)}<em> / ${esc(overview.agents || 0)}</em>`, href: "#node-settings", permission: "agents.read", tone: "green", icon: '<circle cx="12" cy="12" r="8"/><path d="M8 12h2l1.3-3 2.1 6 1.4-3H17"/>' },
    { label: "节点配置", value: esc(overview.node_configs || 0), href: "#live-config", permission: "agent-config.read", tone: "blue", icon: '<path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h5"/>' },
    { label: "活动任务", value: esc(overview.tasks_pending || 0), href: "#tasks", permission: "tasks.read", status: "pending", tone: "amber", icon: '<path d="M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"/>' },
    { label: "失败任务", value: esc(overview.tasks_failed || 0), href: "#tasks", permission: "tasks.read", status: "failed", tone: "red", icon: '<path d="M12 3.5 21 20H3zM12 9v5M12 17.5h.01"/>' },
  ].map((stat) => {
    const allowed = can(stat.permission);
    return `<${allowed ? "a" : "div"} class="dashboard-stat"${allowed ? ` href="${stat.href}" aria-label="查看${stat.label}"${stat.status ? ` data-dashboard-status="${stat.status}"` : ""}` : ""}><span class="stat-icon ${stat.tone}"><svg viewBox="0 0 24 24" aria-hidden="true">${stat.icon}</svg></span><div><small>${stat.label}</small><strong>${stat.value}</strong></div>${allowed ? '<i aria-hidden="true">↗</i>' : ""}</${allowed ? "a" : "div"}>`;
  }).join("");
  const host = can("panel-metrics.read") ? panelMetrics.render() : "";
  const monitoring = host || trafficHistory ? `<div class="dashboard-monitoring${host && trafficHistory ? " has-two-panels" : ""}">${host}${trafficHistory}</div>` : "";
  const fleetPanel = can("agents.read") ? `<section class="workspace-panel fleet-overview" id="fleet"><header><div class="dashboard-section-heading"><h3>节点状态</h3></div><a href="#node-settings">全部 ${esc(overview.agents || 0)} 个 →</a></header><div class="fleet-overview-list">${fleet}</div></section>` : "";
  const activityPanel = can("tasks.read") ? `<section class="workspace-panel recent-tasks" id="activity"><header><div class="dashboard-section-heading"><h3>最近任务</h3></div><a href="#tasks">全部 →</a></header><div>${activity}</div></section>` : "";
  shell(`<div class="dashboard-workspace">
    <section class="dashboard-head" id="summary"><div><h2>运行总览</h2></div><span class="trust-badge ${!overview.agents ? "inactive" : overview.agents_online === overview.agents ? "" : "warn"}"><i></i>${!overview.agents ? "等待节点接入" : overview.agents_online === overview.agents ? "全部在线" : `${esc(overview.agents_online)} / ${esc(overview.agents)} 在线`}</span></section>
    <nav class="ops-stats" aria-label="运行概览快捷入口">${stats}</nav>
    ${monitoring}
    ${fleetPanel || activityPanel ? `<div class="dashboard-columns${fleetPanel && activityPanel ? "" : " single-panel"}">${fleetPanel}${activityPanel}</div>` : ""}
  </div>`, "总览");
  // CSSOM updates are allowed by the production CSP; inline HTML styles are not.
  document.querySelector(".dashboard-traffic-axis")?.style.setProperty(
    "grid-template-columns", `repeat(${dailyTraffic.length},minmax(0,1fr))`,
  );

    return { trafficYear };
  };
}
