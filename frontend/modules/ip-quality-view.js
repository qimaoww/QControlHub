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
        ${schedule?.enabled ? `<small>下次检查：${esc(formatDate(schedule.next_run_at))} · 离线时等待重连</small>` : ""}
      </footer>` : "";
      return `<article class="workspace-panel ip-quality-node-card ${tone}" data-refresh-key="ip-quality-${esc(agent.id)}" data-ip-quality-agent="${esc(agent.id)}">
        <header><div><strong>${esc(agent.name)}</strong><small>${isToday ? (agent.status === "online" ? "节点在线" : "节点离线") : "该日检测记录"} · ${reports.length ? `${reports.length} 个地址族` : "无有效报告"}</small></div><span class="status-label ip-quality-badge ${tone}"><i></i>${statusNames[record?.status] || "当天未检测"}</span></header>
        <div class="ip-quality-node-body">${reports.length ? `<dl class="ip-quality-addresses">${reports.map((report) => `<div><dt>${String(report.Head?.IP || "").includes(":") ? "IPv6" : "IPv4"} 出口</dt><dd title="${esc(report.Head?.IP)}">${esc(ipQualityValue(report.Head?.IP))}</dd></div>`).join("")}<div><dt>完成时间</dt><dd>${esc(formatDate(record.finished_at))}</dd></div></dl>` : `<p class="ip-quality-no-report">${record ? (["pending", "running"].includes(record.status) ? "任务尚未完成，等待节点返回检测报告。" : "本次检测未生成有效报告。") : "今天尚未检测。运行检测后，报告将保存在这里。"}</p>`}
        ${record?.error ? `<p class="alert error ip-quality-error" role="alert">${esc(record.error)}</p>` : ""}
        </div>${controls}
        ${reports.length || record?.archives?.length ? `<div class="ip-quality-node-body">
        ${archiveView(record)}
        ${reports.length ? `<details class="ip-quality-details" data-refresh-key="report-${esc(record.task_id)}"><summary>查看完整报告 <span>⌄</span></summary>${reports.map(reportView).join("")}</details>` : ""}
        </div>` : ""}
      </article>`;
    }).join("");
    const issue = error ? `<div class="alert error ip-quality-alert" role="alert">${esc(error)}${history ? " · 以下为上次成功读取的记录，操作已暂停，请重新读取。" : ""}</div>` : "";
    const emptyTitle = loading ? "正在读取检测记录…" : error ? "无法读取检测记录" : isToday ? "没有可查看的自有节点" : "当天没有检测记录";
    shell(`<div class="ip-quality-workspace">
      <section class="workspace-panel ip-quality-controls">
      <header class="ip-quality-head"><div><h2>IP 质量检测</h2><p>查看节点出口 IP、风险评分与服务连通性</p></div>
        <div class="ip-quality-date-picker"><button type="button" class="button small" data-ip-quality-day="-1" aria-label="前一天">‹</button><label class="ip-quality-date"><span>检测日期 · ${esc(timezone)}</span><input type="date" data-ip-quality-date value="${esc(date)}" max="${esc(today)}"></label><button type="button" class="button small" data-ip-quality-day="1" aria-label="后一天"${date >= today ? " disabled" : ""}>›</button></div></header>
      <section class="ip-quality-summary" aria-label="当日检测概览">
        <div><span>当日记录</span><strong>${summary.total}</strong><small>个节点 · 每节点最新一次</small></div>
        <div><span>已完成</span><strong class="good-text">${summary.succeeded}</strong><small>个节点</small></div>
        <div><span>排队 / 检测中</span><strong>${summary.running}</strong><small>个节点</small></div>
        <div><span>失败 / 取消</span><strong class="warn-text">${summary.failed}</strong><small>个节点</small></div>
      </section>
      </section>
      ${issue}<section class="ip-quality-toolbar"><div><h3>${isToday ? "今日节点" : esc(date) + " · 历史记录"}</h3><span>${isToday ? "每日检测默认关闭，可按节点启用" : "仅查看该日记录，发起检测和修改每日计划请回到今天"}</span></div><div class="ip-quality-toolbar-actions">${!isToday ? '<button type="button" class="button small" data-ip-quality-today>回到今天</button>' : ""}<button type="button" class="button small" data-ip-quality-refresh${loading ? " disabled" : ""}>${loading ? "正在读取…" : "刷新记录"}</button></div></section>
      <div class="ip-quality-grid" aria-busy="${loading}">${cards || `<div class="empty large"><strong>${emptyTitle}</strong><p>${loading || error ? "请稍候或重新读取。" : isToday ? "IP 检测仅面向节点所有者和有权限的管理员；共享不授予主机检测权限。" : "可选择其他日期查看，或回到今天发起新的检测。"}</p></div>`}</div>
      <p class="ip-quality-source">按任务提交日归档 · 延迟、丢包和 DNS / WebRTC 泄漏未检测。<br>检测程序：<a href="https://github.com/xykt/IPQuality" target="_blank" rel="noopener noreferrer">xykt/IPQuality</a>（AGPL-3.0）· 检测在节点上以隐私模式运行（不上传报告）；面板根据节点回传的报告原文自行绘制图片并存入数据库。结果仅供参考。</p>
    </div>`, "IP 质量", { viewKey: `ip-quality-${date}` });
  };
}
