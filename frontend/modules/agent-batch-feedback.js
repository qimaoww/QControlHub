import { batchCoreVersionLabel } from "./agent-batch.js";

const actions = {
  "upgrade-agent": { title: "更新 Agent", submit: "更新 Agent", hint: "更新期间短暂离线。", tone: "primary" },
  install: { title: "更新内核", submit: "更新内核", hint: "更新后服务重启。", tone: "primary" },
  restart: { title: "重启服务", submit: "重启服务", hint: "连接会短暂中断。", tone: "danger" },
  stop: { title: "停止服务", submit: "停止服务", hint: "连接会立即中断。", tone: "danger" },
  start: { title: "启动服务", submit: "启动服务", hint: "", tone: "primary" },
  status: { title: "查询状态", submit: "查询状态", hint: "", tone: "primary" },
};

export function batchActionFeedback(action) {
  return actions[action] || actions.status;
}

export function batchConfirmation(options, agents, engineName) {
  const feedback = batchActionFeedback(options.action);
  const target = [
    `${agents.length} 个节点`,
    options.engine && engineName(options.engine),
    options.core_version && batchCoreVersionLabel(options.core_version),
  ].filter(Boolean).join(" · ");
  return { title: feedback.title, tone: feedback.tone, message: [target, feedback.hint].filter(Boolean).join("\n") };
}

function resultText(success, failure) {
  return `已提交 ${success}${failure ? `，失败 ${failure}` : ""}`;
}

export function batchResultsMarkup(items, total, options, { esc, engineName }) {
  const failed = items.filter((item) => !item.ok);
  const pending = items.length < total;
  const context = [batchActionFeedback(options.action).submit, options.engine && engineName(options.engine), options.core_version && batchCoreVersionLabel(options.core_version)].filter(Boolean).join(" · ");
  return `<p class="batch-result-summary" data-batch-result-summary data-total="${total}" role="status" title="${esc(context)}">${pending ? `提交中 ${items.length}/${total}` : resultText(total - failed.length, failed.length)}</p>
    ${failed.length ? `<div class="batch-result-list">${failed.map((item) => `<div class="batch-result-row error"><span><b>${esc(item.agent?.name || item.agent?.id || "节点")}</b><small>${esc(item.error?.message || "提交失败")}</small></span><button type="button" class="button small" data-batch-retry="${esc(item.agent?.id || "")}" data-batch-retry-action="${esc(options.action)}" data-batch-retry-engine="${esc(options.engine || "")}" title="${esc(context)}" ${pending ? "disabled" : ""}>重试</button></div>`).join("")}</div>` : ""}`;
}

export function updateBatchResultSummary(form) {
  const results = form.querySelector("[data-batch-results]");
  const summary = results.querySelector("[data-batch-result-summary]");
  const failure = results.querySelectorAll("[data-batch-retry]").length;
  summary.textContent = resultText(Number(summary.dataset.total) - failure, failure);
}
