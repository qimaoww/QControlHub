import { batchCoreVersionLabel } from "./agent-batch.js";

const actions = {
  "upgrade-agent": { title: "批量更新 Agent", submit: "更新 Agent", hint: "更新期间节点会短暂离线。", tone: "primary" },
  install: { title: "批量更新内核", submit: "更新内核", hint: "更新完成后，目标服务会重启。", tone: "primary" },
  restart: { title: "批量重启服务", submit: "重启服务", hint: "重启会短暂中断所选节点的现有连接。", tone: "danger" },
  stop: { title: "批量停止服务", submit: "停止服务", hint: "现有连接会立即中断，需重新启动服务恢复。", tone: "danger" },
  start: { title: "批量启动服务", submit: "启动服务", hint: "", tone: "primary" },
  status: { title: "批量查询状态", submit: "查询状态", hint: "", tone: "primary" },
};

export function batchActionFeedback(action) {
  return actions[action] || actions.status;
}

export function batchConfirmation(options, agents, engineName) {
  const feedback = batchActionFeedback(options.action);
  const details = [];
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
  const collapsed = !pending && failure === 0;
  return `<header><b>${esc(context)}</b><div class="batch-result-actions"><a href="#tasks" class="batch-tasks-link">任务 ↗</a>${pending ? "" : `<button type="button" class="batch-result-toggle" data-batch-results-toggle aria-expanded="${!collapsed}" aria-controls="batch-result-details">${collapsed ? "展开" : "收起"}</button>`}</div></header>
    <p class="batch-result-summary" data-batch-result-summary role="status">${pending ? `已处理 ${items.length}/${total} 个节点` : `${success} 个已提交 · ${failure} 个失败`}</p>
    ${pending ? `<progress class="batch-progress" value="${items.length}" max="${total}" aria-label="批量提交进度"></progress>` : ""}
    <div id="batch-result-details" data-batch-result-details ${collapsed ? "hidden" : ""}>
      <div class="batch-result-filters" data-batch-result-filters ${pending || !failure ? "hidden" : ""}>
        <button type="button" data-batch-result-filter="all" aria-pressed="false">全部 (${items.length})</button>
        <button type="button" data-batch-result-filter="error" aria-pressed="true">失败 (${failure})</button>
      </div>
      <div class="batch-result-list" data-result-filter="${!pending && failure ? "error" : "all"}">${items.map((item) => `<div class="batch-result-row ${item.ok ? "ok" : "error"}"><span><b title="${esc(item.agent?.name || item.agent?.id || "节点")}">${esc(item.agent?.name || item.agent?.id || "节点")}</b><small ${item.ok ? "hidden" : ""}>${item.ok ? "" : esc(item.error?.message || "提交失败")}</small></span><span class="batch-result-status">${item.ok ? "已提交" : "提交失败"}</span>${item.ok ? "" : `<button type="button" class="button small" data-batch-retry="${esc(item.agent?.id || "")}" data-batch-retry-action="${esc(options.action)}" data-batch-retry-engine="${esc(options.engine || "")}" ${pending ? "disabled" : ""}>重试</button>`}</div>`).join("")}</div>
    </div>`;
}

function setResultFilter(results, filter) {
  results.querySelector(".batch-result-list").dataset.resultFilter = filter;
  results.querySelectorAll("[data-batch-result-filter]").forEach((button) => {
    button.setAttribute("aria-pressed", String(button.dataset.batchResultFilter === filter));
  });
}

export function bindBatchResultControls(results) {
  const toggle = results?.querySelector("[data-batch-results-toggle]");
  if (toggle) toggle.onclick = () => {
    const details = results.querySelector("[data-batch-result-details]");
    details.hidden = !details.hidden;
    toggle.setAttribute("aria-expanded", String(!details.hidden));
    toggle.textContent = details.hidden ? "展开" : "收起";
  };
  results?.querySelectorAll("[data-batch-result-filter]").forEach((button) => {
    button.onclick = () => setResultFilter(results, button.dataset.batchResultFilter);
  });
}

export function updateBatchResultSummary(form) {
  const results = form.querySelector("[data-batch-results]");
  if (!results) return;
  const success = results.querySelectorAll(".batch-result-row.ok").length;
  const failure = results.querySelectorAll(".batch-result-row.error").length;
  results.querySelector("[data-batch-result-summary]").textContent = `${success} 个已提交 · ${failure} 个失败`;
  results.querySelector('[data-batch-result-filter="error"]').textContent = `失败 (${failure})`;
  results.querySelector("[data-batch-result-filters]").hidden = failure === 0;
  if (!failure) setResultFilter(results, "all");
}
