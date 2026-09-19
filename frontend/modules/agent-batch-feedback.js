import { batchCoreVersionLabel } from "./agent-batch.js";

const actions = {
  "upgrade-agent": { title: "批量更新 Agent", submit: "更新 Agent", hint: "更新后自动重连，节点会短暂离线。", tone: "primary" },
  install: { title: "批量更新内核", submit: "更新内核", hint: "从官方 Release 下载并校验，完成后目标服务会重启。", tone: "primary" },
  restart: { title: "批量重启服务", submit: "重启服务", hint: "重启会短暂中断所选节点的现有连接。", tone: "danger" },
  stop: { title: "批量停止服务", submit: "停止服务", hint: "现有连接会立即中断，需再次启动服务才能恢复。", tone: "danger" },
  start: { title: "批量启动服务", submit: "启动服务", hint: "启动所选节点的目标内核服务。", tone: "primary" },
  status: { title: "批量查询状态", submit: "查询状态", hint: "读取服务运行状态，不会重启或中断连接。", tone: "primary" },
};

export function batchActionFeedback(action) {
  return actions[action] || actions.status;
}

export function batchConfirmation(options, agents, engineName) {
  const feedback = batchActionFeedback(options.action);
  const details = [["执行动作", feedback.submit]];
  if (options.engine) details.push(["目标内核", engineName(options.engine)]);
  if (options.core_version) details.push(["目标版本", batchCoreVersionLabel(options.core_version)]);
  return {
    title: feedback.title,
    tone: feedback.tone,
    details,
    targets: agents.map((agent) => agent.name || agent.id),
  };
}

export function batchResultsMarkup(items, total, options, { esc, engineName }) {
  const success = items.filter((item) => item.ok).length;
  const failure = items.length - success;
  const pending = items.length < total;
  const context = [batchActionFeedback(options.action).submit, options.engine && engineName(options.engine), options.core_version && batchCoreVersionLabel(options.core_version)].filter(Boolean).join(" · ");
  return `<header><div><b>${pending ? "正在提交批量任务" : "批量提交结果"}</b><small>${esc(context)}</small></div><a href="#tasks" class="batch-tasks-link">查看任务 →</a></header>
    <p class="batch-result-summary" data-batch-result-summary role="status">${pending ? `已处理 ${items.length}/${total} 个节点` : `${success} 个已提交 · ${failure} 个失败`}</p>
    ${pending ? `<progress class="batch-progress" value="${items.length}" max="${total}" aria-label="批量提交进度"></progress>` : `<p class="batch-result-note" data-batch-result-note>${success ? "任务已进入队列，执行结果请在任务页查看。" : "本次未提交任何任务，请检查失败原因后重试。"}</p>`}
    <div class="batch-result-list">${items.map((item) => `<div class="batch-result-row ${item.ok ? "ok" : "error"}"><span><b>${esc(item.agent?.name || item.agent?.id || "节点")}</b><small>${item.ok ? `任务 ${esc(item.task?.id || "已提交")}` : esc(item.error?.message || "提交失败")}</small></span><span class="batch-result-status">${item.ok ? "已提交" : "提交失败"}</span>${item.ok ? "" : `<button type="button" class="button small" data-batch-retry="${esc(item.agent?.id || "")}" data-batch-retry-action="${esc(options.action)}" data-batch-retry-engine="${esc(options.engine || "")}" ${pending ? "disabled" : ""}>重试</button>`}</div>`).join("")}</div>`;
}

export function updateBatchResultSummary(form) {
  const results = form.querySelector("[data-batch-results]");
  const summary = results?.querySelector("[data-batch-result-summary]");
  const note = results?.querySelector("[data-batch-result-note]");
  if (note && results.querySelector(".batch-result-row.ok")) note.textContent = "任务已进入队列，执行结果请在任务页查看。";
  if (summary) summary.textContent = `${results.querySelectorAll(".batch-result-row.ok").length} 个已提交 · ${results.querySelectorAll(".batch-result-row.error").length} 个失败`;
}
