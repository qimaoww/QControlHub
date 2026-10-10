import { orderNodesBySavedOrder } from "./node-order.js";
import {
  ipQualityToday, ipQualityLatest, ipQualitySummary, ipQualityBlockReason, ipQualityFeature,
  ipQualityNodeList, ipQualityStatusName, ipQualityStatusTone,
} from "./ip-quality-model.js";
import { createIPQualityReportView } from "./ip-quality-report-view.js";
import { ipQualityAddressMarkup } from "./ip-quality-address-view.js";
import { createIPQualityArchiveView } from "./ip-quality-archive-view.js";
import { createIPQualityCalendarView } from "./ip-quality-calendar-view.js";
import { dockIcons } from "./shell-icons.js";
import { agentPresenceMarkup } from "./agent-presence.js";

export function createIPQualityView({ shell, state, esc, date: formatDate }) {
  const reportView = createIPQualityReportView({ esc });
  const archiveView = createIPQualityArchiveView({ esc, date: formatDate });
  const calendarView = createIPQualityCalendarView({ esc });
  const icon = (name) => `<svg viewBox="0 0 24 24" aria-hidden="true">${dockIcons[name]}</svg>`;
  return ({ date, timezone, history, availableDates, month, agents = [], loading = false, error = "", submitting, editable, readFailed }) => {
    const today = ipQualityToday(), isToday = date === today, isLatest = date === ipQualityLatest;
    const canOperate = isLatest || isToday;
    const records = history?.records || [], schedules = history?.schedules || [];
    const summary = ipQualitySummary(records);
    const recordMap = new Map(records.map((item) => [item.agent_id, item]));
    const scheduleMap = new Map(schedules.map((item) => [item.agent_id, item]));
    const nodes = ipQualityNodeList(orderNodesBySavedOrder(agents), records, canOperate, state.data.ipQualityAgent,
      history ? "" : readFailed ? "读取失败" : "正在读取记录", isLatest);
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
      const reportRecord = record?.result ? record : isLatest ? record?.last_successful : null;
      const reports = reportRecord?.result?.reports || [];
      const runButton = canOperate && editable(agent) ? `<button type="button" class="button small primary" data-ip-quality-run="${esc(agent.id)}"${busy || loading || readFailed || !history || reason ? " disabled" : ""}>${esc(busy ? "正在提交…" : reason || "立即检测")}</button>` : "";
      const controls = canOperate && editable(agent) ? `<div class="ip-quality-actions" role="group" aria-label="节点检测操作">
        <button type="button" class="button small" data-ip-quality-schedule="${esc(agent.id)}" data-enabled="${Boolean(schedule?.enabled)}" aria-pressed="${Boolean(schedule?.enabled)}"${busy || loading || readFailed || !history || (!schedule?.enabled && !agent.features?.includes(ipQualityFeature)) ? " disabled" : ""}>每日检测：${schedule?.enabled ? "开" : "关"}</button>
        ${schedule?.enabled ? `<small>下次：${esc(formatDate(schedule.next_run_at))}</small>` : ""}
      </div>` : "";
      const addresses = reports.length ? `<dl class="ip-quality-addresses">${reports.map((report) => `<div class="${String(report.Head?.IP || "").includes(":") ? "ip-quality-address-v6" : "ip-quality-address-v4"}"><dt><span class="ip-family ${String(report.Head?.IP || "").includes(":") ? "v6" : "v4"}">${String(report.Head?.IP || "").includes(":") ? "IPv6" : "IPv4"}</span> 出口地址</dt><dd title="${esc(report.Head?.IP)}">${ipQualityAddressMarkup(report.Head?.IP, esc)}</dd></div>`).join("")}<div><dt>${reportRecord === record ? "检测时间" : "上次成功检测"}</dt><dd><time datetime="${esc(reportRecord.finished_at)}">${esc(formatDate(reportRecord.finished_at))}</time></dd></div></dl>` : "";
      const retained = reportRecord && reportRecord !== record;
      const progress = retained ? `<p class="ip-quality-result-note" role="status">${["pending", "running"].includes(record.status) ? "新检测正在排队或执行，以下保留上次成功报告。" : "最新检测未成功，以下显示上次成功报告。"}</p>` : "";
      const attempt = record && (!reports.length || retained) ? `<p class="ip-quality-no-report">最近发起：${esc(formatDate(record.created_at))}</p>` : "";
      return `<section class="workspace-panel ip-quality-node-panel ${tone}" data-ip-quality-panel="${esc(agent.id)}" data-refresh-key="ip-quality-${esc(agent.id)}" aria-busy="${loading}">
        <header><div class="ip-quality-node-title"><span class="node-avatar" aria-hidden="true">${icon("server")}</span><div><h3>${esc(agent.name)}</h3>${canOperate ? `<small class="ip-quality-node-meta">${agentPresenceMarkup(agent.status)}${reports.length ? `<span>${reports.length} 个地址族</span>` : ""}</small>` : ""}</div></div><div class="ip-quality-node-tools"><span class="status-label ip-quality-badge ${tone}"><i></i>${record ? esc(ipQualityStatusName(record.status)) : !history ? readFailed ? "读取失败" : "读取中" : "未检测"}</span>${runButton}</div></header>
        ${controls}
        <div class="ip-quality-node-body">
          ${progress}
          ${addresses}
          ${attempt}
          ${record?.error ? `<p class="alert error ip-quality-error" role="alert">${esc(record.error)}</p>` : ""}
          ${reports.length ? "" : `<p class="ip-quality-no-report">${record ? "等待有效检测结果；失败或缺失数据不会记为低风险。" : !history ? readFailed ? "检测记录读取失败，可点击刷新重试。" : "正在读取这台节点的检测记录…" : isLatest ? "还没有检测记录，可点击立即检测生成首次报告。" : isToday ? "当天没有检测记录，可选择其他节点或发起检测。" : "当天没有检测记录。"}</p>`}
          ${reportRecord ? archiveView(reportRecord) : ""}
          ${reports.length ? `<details class="ip-quality-details" data-refresh-key="report-${esc(reportRecord.task_id)}"><summary>详细数据 <span>⌄</span></summary>${reports.map(reportView).join("")}</details>` : ""}
        </div>
      </section>`;
    };
    const issue = error ? `<div class="alert error ip-quality-alert" role="alert">${esc(error)}${history ? " · 显示缓存，操作已暂停" : ""}</div>` : "";
    const picker = calendarView({ date, timezone, dates: availableDates || history?.dates || [], month, loading, readFailed });
    const emptyTitle = loading ? "正在读取检测记录…" : error ? "无法读取检测记录" : canOperate ? "没有可查看的自有节点" : "当天没有检测记录";
    const emptyNote = error ? "可点击刷新重试。" : loading ? "节点列表和检测记录将逐步显示。" : canOperate ? "IP 检测仅面向节点所有者和有权限的管理员；共享不授予主机检测权限。" : "可选择其他日期，或回到最新结果发起检测。";
    const count = (value) => history ? value : "—";
    shell(`<div class="ip-quality-workspace">
      <section class="ip-quality-controls">
        <header class="ip-quality-head"><div><h2>IP 质量</h2><p class="ip-quality-view-note">${isLatest ? "各节点最近一次检测，保留已有质量报告。" : `${esc(date)} 的检测记录`}</p></div>${picker}</header>
        <section class="ip-quality-summary" aria-label="${isLatest ? "最新检测概览" : "当日检测概览"}">
          <div><span title="${isLatest ? "每个节点最近一次检测，不限日期" : "每个节点当天的最新记录"}">检测节点</span><strong>${count(summary.total)}</strong></div>
          <div><span>已完成</span><strong>${count(summary.succeeded)}</strong></div>
          <div><span>排队 / 检测中</span><strong>${count(summary.running)}</strong></div>
          <div><span>失败 / 取消</span><strong>${count(summary.failed)}</strong></div>
        </section>
      </section>
      ${issue}
      <div data-motion-region="ip-quality-results" data-motion-key="${esc(JSON.stringify([date, nodes.selected?.id || ""]))}" data-motion-ready="${!loading}">${nodes.selected ? nodePanel(nodes.selected) : `<div class="empty large"><strong>${emptyTitle}</strong><p>${emptyNote}</p></div>`}</div>
      <p class="ip-quality-source"><a href="https://github.com/xykt/IPQuality" target="_blank" rel="noopener noreferrer">xykt/IPQuality</a> · AGPL-3.0 · 仅显示自有节点的检测结果</p>
    </div>`, "IP 质量", { viewKey: "ip-quality" });
  };
}
