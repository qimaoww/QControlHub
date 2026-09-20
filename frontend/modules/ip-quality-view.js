import { ipQualityToday, ipQualitySummary, ipQualityBlockReason, ipQualityFeature, ipQualityValue } from "./ip-quality-model.js";
import { createIPQualityReportView } from "./ip-quality-report-view.js";
import { createIPQualityArchiveView } from "./ip-quality-archive-view.js";

const statusNames = { succeeded: "已完成", pending: "等待执行", running: "检测中", failed: "检测失败", canceled: "已取消" };
const statusTones = { succeeded: "good", pending: "pending", running: "pending", failed: "poor", canceled: "warning" };

export function createIPQualityView({ shell, esc, date: formatDate }) {
  const reportView = createIPQualityReportView({ esc });
  const archiveView = createIPQualityArchiveView({ esc, date: formatDate });
  return ({ date, timezone, history, agents = [], loading = false, error = "", submitting, editable, readFailed }) => {
    const today = ipQualityToday(), isToday = date === today;
    const records = history?.records || [], schedules = history?.schedules || [];
    const summary = ipQualitySummary(records);
    const recordMap = new Map(records.map((item) => [item.agent_id, item]));
    const scheduleMap = new Map(schedules.map((item) => [item.agent_id, item]));
    const visibleAgents = isToday ? agents : agents.filter((agent) => recordMap.has(agent.id));
    const cards = visibleAgents.map((agent) => {
      const record = recordMap.get(agent.id), schedule = scheduleMap.get(agent.id);
      const tone = statusTones[record?.status] || "unknown";
      const busy = submitting.has(agent.id), reason = ipQualityBlockReason(agent, record);
      const reports = record?.result?.reports || [];
      const controls = isToday && editable(agent) ? `<footer class="ip-quality-actions" role="group" aria-label="节点检测操作">
        <button type="button" class="button small primary" data-ip-quality-run="${esc(agent.id)}"${busy || loading || readFailed || reason ? " disabled" : ""}>${esc(busy ? "正在提交…" : reason || "立即检测")}</button>
        <button type="button" class="button small" data-ip-quality-schedule="${esc(agent.id)}" data-enabled="${Boolean(schedule?.enabled)}" aria-pressed="${Boolean(schedule?.enabled)}"${busy || loading || readFailed || (!schedule?.enabled && !agent.features?.includes(ipQualityFeature)) ? " disabled" : ""}>每日检测：${schedule?.enabled ? "开" : "关"}</button>
        ${schedule?.enabled ? `<small>下次：${esc(formatDate(schedule.next_run_at))}</small>` : ""}
      </footer>` : "";
      return `<article class="workspace-panel ip-quality-node-card ${tone}" data-refresh-key="ip-quality-${esc(agent.id)}" data-ip-quality-agent="${esc(agent.id)}">
        <header><div><strong>${esc(agent.name)}</strong>${isToday ? `<small>${agent.status === "online" ? "在线" : "离线"}</small>` : ""}</div><span class="status-label ip-quality-badge ${tone}"><i></i>${statusNames[record?.status] || "未检测"}</span></header>
        ${reports.length || record?.error ? `<div class="ip-quality-node-body">${reports.length ? `<dl class="ip-quality-addresses">${reports.map((report) => `<div><dt>${String(report.Head?.IP || "").includes(":") ? "IPv6" : "IPv4"} 出口</dt><dd title="${esc(report.Head?.IP)}">${esc(ipQualityValue(report.Head?.IP))}</dd></div>`).join("")}<div><dt>完成时间</dt><dd>${esc(formatDate(record.finished_at))}</dd></div></dl>` : ""}
        ${record?.error ? `<p class="alert error ip-quality-error" role="alert">${esc(record.error)}</p>` : ""}
        </div>` : ""}${controls}
        ${reports.length || record?.archives?.length ? `<div class="ip-quality-node-body">
        ${archiveView(record)}
        ${reports.length ? `<details class="ip-quality-details" data-refresh-key="report-${esc(record.task_id)}"><summary>详细数据 <span>⌄</span></summary>${reports.map(reportView).join("")}</details>` : ""}
        </div>` : ""}
      </article>`;
    }).join("");
    const issue = error ? `<div class="alert error ip-quality-alert" role="alert">${esc(error)}${history ? " · 显示缓存，操作已暂停" : ""}</div>` : "";
    const emptyTitle = loading ? "正在读取检测记录…" : error ? "无法读取检测记录" : isToday ? "暂无节点" : "当天没有检测记录";
    shell(`<div class="ip-quality-workspace">
      <section class="workspace-panel ip-quality-controls">
      <header class="ip-quality-head"><h2>IP 质量</h2>
        <div class="ip-quality-date-picker"><button type="button" class="button small" data-ip-quality-day="-1" aria-label="前一天">‹</button><label class="ip-quality-date"><span title="${esc(timezone)}">检测日期</span><input type="date" data-ip-quality-date value="${esc(date)}" max="${esc(today)}"></label><button type="button" class="button small" data-ip-quality-day="1" aria-label="后一天"${date >= today ? " disabled" : ""}>›</button></div></header>
      <section class="ip-quality-summary" aria-label="当日检测概览">
        <div><span title="每个节点当天的最新记录">检测节点</span><strong>${summary.total}</strong></div>
        <div><span>已完成</span><strong class="good-text">${summary.succeeded}</strong></div>
        <div><span>排队 / 检测中</span><strong>${summary.running}</strong></div>
        <div><span>失败 / 取消</span><strong class="warn-text">${summary.failed}</strong></div>
      </section>
      </section>
      ${issue}<section class="ip-quality-toolbar"><div><h3>${isToday ? "节点" : "历史记录"}</h3></div><div class="ip-quality-toolbar-actions">${!isToday ? '<button type="button" class="button small" data-ip-quality-today>回到今天</button>' : ""}<button type="button" class="button small" data-ip-quality-refresh${loading ? " disabled" : ""}>${loading ? "正在读取…" : "刷新"}</button></div></section>
      <div class="ip-quality-grid" aria-busy="${loading}">${cards || `<div class="empty large"><strong>${emptyTitle}</strong></div>`}</div>
      <p class="ip-quality-source"><a href="https://github.com/xykt/IPQuality" target="_blank" rel="noopener noreferrer">xykt/IPQuality</a> · AGPL-3.0</p>
    </div>`, "IP 质量", { viewKey: `ip-quality-${date}` });
  };
}
