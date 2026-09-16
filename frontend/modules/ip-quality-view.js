import { ipQualityToday, ipQualitySummary, ipQualityBlockReason, ipQualityFeature, ipQualityValue } from "./ip-quality-model.js";
import { createIPQualityReportView } from "./ip-quality-report-view.js";

const statusNames = { succeeded: "已完成", pending: "等待执行", running: "检测中", failed: "检测失败", canceled: "已取消" };
const statusTones = { succeeded: "good", pending: "pending", running: "pending", failed: "poor", canceled: "warning" };

export function createIPQualityView({ shell, esc, date: formatDate }) {
  const reportView = createIPQualityReportView({ esc });
  return ({ date, timezone, history, agents = [], loading = false, error = "", submitting, editable, readFailed }) => {
    const records = history?.records || [], schedules = history?.schedules || [];
    const summary = ipQualitySummary(records);
    const recordMap = new Map(records.map((item) => [item.agent_id, item]));
    const scheduleMap = new Map(schedules.map((item) => [item.agent_id, item]));
    const cards = agents.map((agent) => {
      const record = recordMap.get(agent.id), schedule = scheduleMap.get(agent.id);
      const tone = statusTones[record?.status] || "unknown";
      const busy = submitting.has(agent.id), reason = ipQualityBlockReason(agent, record);
      const reports = record?.result?.reports || [];
      const controls = editable(agent) ? `<footer class="ip-quality-actions">
        <button type="button" class="button small primary" data-ip-quality-run="${esc(agent.id)}"${busy || loading || readFailed || reason ? " disabled" : ""}>${esc(busy ? "正在提交…" : reason || "立即检测")}</button>
        <button type="button" class="button small" data-ip-quality-schedule="${esc(agent.id)}" data-enabled="${Boolean(schedule?.enabled)}" aria-pressed="${Boolean(schedule?.enabled)}"${busy || loading || readFailed || (!schedule?.enabled && !agent.features?.includes(ipQualityFeature)) ? " disabled" : ""}>每日检测：${schedule?.enabled ? "开" : "关"}</button>
        ${schedule?.enabled ? `<small>下次检查：${esc(formatDate(schedule.next_run_at))} · 离线时等待重连</small>` : ""}
      </footer>` : "";
      return `<article class="ip-quality-node-card ${tone}" data-refresh-key="ip-quality-${esc(agent.id)}" data-ip-quality-agent="${esc(agent.id)}">
        <header><div><strong>${esc(agent.name)}</strong><small>${agent.status === "online" ? "节点在线" : "节点离线"} · ${reports.length ? `${reports.length} 个地址族` : "无有效报告"}</small></div><span class="ip-quality-badge ${tone}"><i></i>${statusNames[record?.status] || "当天未检测"}</span></header>
        ${reports.length ? `<dl class="ip-quality-addresses">${reports.map((report) => `<div><dt>${String(report.Head?.IP || "").includes(":") ? "IPv6" : "IPv4"} 出口</dt><dd title="${esc(report.Head?.IP)}">${esc(ipQualityValue(report.Head?.IP))}</dd></div>`).join("")}<div><dt>完成时间</dt><dd>${esc(formatDate(record.finished_at))}</dd></div></dl>` : `<p class="ip-quality-no-report">${record ? "等待有效检测结果；失败或缺失数据不会记为低风险。" : "当天没有检测记录，可选择其他日期或发起检测。"}</p>`}
        ${record?.error ? `<p class="alert error ip-quality-error" role="alert">${esc(record.error)}</p>` : ""}
        ${reports.length ? `<details class="ip-quality-details" data-refresh-key="report-${esc(record.task_id)}"><summary>查看完整报告 <span>⌄</span></summary>${reports.map(reportView).join("")}</details>` : ""}
        ${controls}
      </article>`;
    }).join("");
    const issue = error ? `<div class="alert error ip-quality-alert" role="alert">${esc(error)}${history ? " · 以下为上次成功读取的记录，操作已暂停，请重新读取。" : ""}</div>` : "";
    shell(`<div class="ip-quality-workspace">
      <header class="ip-quality-head"><div><p class="eyebrow">IPQuality · 真实检测历史</p><h2>IP 质量检测</h2><p>查看出口 IP、数据库风险、媒体解锁与邮件连通性。风险分数保留各数据源原值，不合成总分。</p></div>
        <div class="ip-quality-date-picker"><button type="button" class="button small" data-ip-quality-day="-1" aria-label="前一天">‹</button><label class="ip-quality-date"><span>检测日期 · ${esc(timezone)}</span><input type="date" data-ip-quality-date value="${esc(date)}" max="${esc(ipQualityToday())}"></label><button type="button" class="button small" data-ip-quality-day="1" aria-label="后一天"${date >= ipQualityToday() ? " disabled" : ""}>›</button></div></header>
      ${issue}<section class="ip-quality-summary" aria-label="当日检测概览">
        <div><span>当日记录</span><strong>${summary.total}</strong><small>个节点 · 每节点最新一次</small></div>
        <div><span>已完成</span><strong class="good-text">${summary.succeeded}</strong><small>个节点</small></div>
        <div><span>排队 / 检测中</span><strong>${summary.running}</strong><small>个节点</small></div>
        <div><span>失败 / 取消</span><strong class="warn-text">${summary.failed}</strong><small>个节点</small></div>
      </section>
      <section class="ip-quality-toolbar"><div><h3>${esc(date)}</h3><span>按任务提交日归档 · 每日检测默认关闭 · 延迟、丢包和 DNS / WebRTC 泄漏未检测</span></div><button type="button" class="button small" data-ip-quality-refresh${loading ? " disabled" : ""}>${loading ? "正在读取…" : "重新读取"}</button></section>
      <div class="ip-quality-grid" aria-busy="${loading}">${cards || `<div class="empty large"><strong>${loading ? "正在读取检测记录…" : error ? "无法读取检测记录" : "没有可查看的自有节点"}</strong><p>${loading || error ? "请稍候或重新读取。" : "IP 检测仅面向节点所有者和有权限的管理员；共享不授予主机检测权限。"}</p></div>`}</div>
      <p class="ip-quality-source">检测程序：<a href="https://github.com/xykt/IPQuality" target="_blank" rel="noopener noreferrer">xykt/IPQuality</a>（AGPL-3.0）· 使用隐私模式，不生成在线分享报告；仍需访问第三方检测服务。结果仅供参考。</p>
    </div>`, "IP 质量", { viewKey: `ip-quality-${date}` });
  };
}
