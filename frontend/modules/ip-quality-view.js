import { orderNodesBySavedOrder } from "./node-order.js";
import {
  ipQualityToday, ipQualitySummary, ipQualityBlockReason, ipQualityFeature,
  ipQualityNodeList, ipQualityStatusName, ipQualityStatusTone,
} from "./ip-quality-model.js";
import { createIPQualityReportView } from "./ip-quality-report-view.js";
import { ipQualityAddressMarkup } from "./ip-quality-address-view.js";
import { createIPQualityArchiveView } from "./ip-quality-archive-view.js";

export function createIPQualityView({ shell, state, esc, date: formatDate }) {
  const reportView = createIPQualityReportView({ esc });
  const archiveView = createIPQualityArchiveView({ esc, date: formatDate });
  return ({ date, timezone, history, agents = [], loading = false, error = "", submitting, editable, readFailed }) => {
    const today = ipQualityToday(), isToday = date === today;
    const records = history?.records || [], schedules = history?.schedules || [];
    const summary = ipQualitySummary(records);
    const recordMap = new Map(records.map((item) => [item.agent_id, item]));
    const scheduleMap = new Map(schedules.map((item) => [item.agent_id, item]));
    const nodes = ipQualityNodeList(orderNodesBySavedOrder(agents), records, isToday, state.data.ipQualityAgent,
      history ? "" : readFailed ? "读取失败" : "正在读取记录");
    // The context sidebar renders this projection, so it can never offer a node
    // the detail panel refuses to show. Only a completed read may publish an
    // empty list: a foreground or failed read has no history yet, and clearing
    // the sidebar there would blink it away and drop the remembered node.
    state.data.agents = agents;
    if (nodes.items.length || history !== undefined) {
      state.data.ipQualityNodes = nodes.items;
      if (nodes.selected) state.data.ipQualityAgent = nodes.selected.id;
    }
    const nodePanel = (agent) => {
      const record = recordMap.get(agent.id), schedule = scheduleMap.get(agent.id);
      const tone = record ? ipQualityStatusTone(record.status) : !history ? readFailed ? "poor" : "pending" : "unknown";
      const busy = submitting.has(agent.id), reason = ipQualityBlockReason(agent, record);
      const reports = record?.result?.reports || [];
      const controls = isToday && editable(agent) ? `<footer class="ip-quality-actions" role="group" aria-label="节点检测操作">
        <button type="button" class="button small primary" data-ip-quality-run="${esc(agent.id)}"${busy || loading || readFailed || !history || reason ? " disabled" : ""}>${esc(busy ? "正在提交…" : reason || "立即检测")}</button>
        <button type="button" class="button small" data-ip-quality-schedule="${esc(agent.id)}" data-enabled="${Boolean(schedule?.enabled)}" aria-pressed="${Boolean(schedule?.enabled)}"${busy || loading || readFailed || !history || (!schedule?.enabled && !agent.features?.includes(ipQualityFeature)) ? " disabled" : ""}>每日检测：${schedule?.enabled ? "开" : "关"}</button>
        ${schedule?.enabled ? `<small>下次：${esc(formatDate(schedule.next_run_at))}</small>` : ""}
      </footer>` : "";
      const addresses = reports.length ? `<dl class="ip-quality-addresses">${reports.map((report) => `<div class="${String(report.Head?.IP || "").includes(":") ? "ip-quality-address-v6" : "ip-quality-address-v4"}"><dt>${String(report.Head?.IP || "").includes(":") ? "IPv6" : "IPv4"} 出口</dt><dd title="${esc(report.Head?.IP)}">${ipQualityAddressMarkup(report.Head?.IP, esc)}</dd></div>`).join("")}<div><dt>完成时间</dt><dd>${esc(formatDate(record.finished_at))}</dd></div></dl>` : "";
      return `<section class="workspace-panel ip-quality-node-panel ${tone}" data-ip-quality-panel="${esc(agent.id)}" data-refresh-key="ip-quality-${esc(agent.id)}" aria-busy="${loading}">
        <header><div><strong>${esc(agent.name)}</strong>${isToday ? `<small>${agent.status === "online" ? "在线" : "离线"}${reports.length ? ` · ${reports.length} 个地址族` : ""}</small>` : ""}</div><span class="status-label ip-quality-badge ${tone}"><i></i>${record ? esc(ipQualityStatusName(record.status)) : !history ? readFailed ? "读取失败" : "读取中" : "未检测"}</span></header>
        <div class="ip-quality-node-body">
          ${addresses}
          ${record?.error ? `<p class="alert error ip-quality-error" role="alert">${esc(record.error)}</p>` : ""}
          ${reports.length ? "" : `<p class="ip-quality-no-report">${record ? "等待有效检测结果；失败或缺失数据不会记为低风险。" : !history ? readFailed ? "检测记录读取失败，可点击刷新重试。" : "正在读取这台节点的检测记录…" : isToday ? "当天没有检测记录，可选择其他节点或发起检测。" : "当天没有检测记录。"}</p>`}
          ${record ? archiveView(record) : ""}
          ${reports.length ? `<details class="ip-quality-details" data-refresh-key="report-${esc(record.task_id)}"><summary>详细数据 <span>⌄</span></summary>${reports.map(reportView).join("")}</details>` : ""}
        </div>
        ${controls}
      </section>`;
    };
    const issue = error ? `<div class="alert error ip-quality-alert" role="alert">${esc(error)}${history ? " · 显示缓存，操作已暂停" : ""}</div>` : "";
    const picker = `<div class="ip-quality-date-picker">${isToday ? "" : '<button type="button" class="button small" data-ip-quality-today>回到今天</button>'}<button type="button" class="button small" data-ip-quality-day="-1" aria-label="前一天">‹</button><label class="ip-quality-date"><span title="${esc(timezone)}">检测日期</span><input type="date" data-ip-quality-date value="${esc(date)}" max="${esc(today)}"></label><button type="button" class="button small" data-ip-quality-day="1" aria-label="后一天"${date >= today ? " disabled" : ""}>›</button><button type="button" class="button small" data-ip-quality-refresh${loading ? " disabled" : ""}>${loading ? "正在读取…" : "刷新"}</button></div>`;
    const emptyTitle = loading ? "正在读取检测记录…" : error ? "无法读取检测记录" : isToday ? "没有可查看的自有节点" : "当天没有检测记录";
    const emptyNote = error ? "可点击刷新重试。" : loading ? "节点列表和检测记录将逐步显示。" : isToday ? "IP 检测仅面向节点所有者和有权限的管理员；共享不授予主机检测权限。" : "可选择其他日期，或在今天发起检测。";
    const count = (value) => history ? value : "—";
    shell(`<div class="ip-quality-workspace">
      <section class="workspace-panel ip-quality-controls">
        <header class="ip-quality-head"><h2>IP 质量</h2>${picker}</header>
        <section class="ip-quality-summary" aria-label="当日检测概览">
          <div><span title="每个节点当天的最新记录">检测节点</span><strong>${count(summary.total)}</strong></div>
          <div><span>已完成</span><strong class="good-text">${count(summary.succeeded)}</strong></div>
          <div><span>排队 / 检测中</span><strong>${count(summary.running)}</strong></div>
          <div><span>失败 / 取消</span><strong class="warn-text">${count(summary.failed)}</strong></div>
        </section>
      </section>
      ${issue}
      ${nodes.selected ? nodePanel(nodes.selected) : `<div class="empty large"><strong>${emptyTitle}</strong><p>${emptyNote}</p></div>`}
      <p class="ip-quality-source"><a href="https://github.com/xykt/IPQuality" target="_blank" rel="noopener noreferrer">xykt/IPQuality</a> · AGPL-3.0 · 仅显示自有节点的检测结果</p>
    </div>`, "IP 质量", { viewKey: `ip-quality-${date}` });
  };
}
