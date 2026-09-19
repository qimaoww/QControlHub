import { diagnosticError } from "./errors.js";
import { reconcileView } from "./refresh.js";
import { coreSourceLabel, diagnoseTask } from "./task-model.js";
export function createTaskView({ state, actions, can, esc, statusName, engineName, short, date, ago, actionName, statusTone, shell }) {
  function renderTaskCards(items, agents, openResults) {
    return (
      items
        .map((item) => {
          const agent = agents.find((entry) => entry.id === item.agent_id);
          const diagnostic = diagnoseTask(item);
          const tone = statusTone(item.status);
          const statusLabel = statusName(item.status);
          const taskEngine =
            item.action === "upgrade-agent" || ["enable-bbr", "disable-bbr", "configure-tcp", "ip-quality"].includes(item.action) ? "qagent" : item.engine;
          const taskEngineLabel =
            item.action === "ip-quality" ? "IPQuality" : ["enable-bbr", "disable-bbr", "configure-tcp"].includes(item.action) ? "BBR / TCP" : item.action === "upgrade-agent"
              ? "QAgent"
              : engineName(item.engine);
          const resultOpen = openResults.has(item.id) ? " open" : "";
          const sourceLabel = coreSourceLabel(item.engine, item.core_version, item.core_source);
          const actionLabel = `${item.install_if_missing ? "准备内核并" : ""}${actionName(item.action)}`;
          const retryLabel = item.install_if_missing ? `重试配置 v${item.config_version}` : item.config_id ? "使用当前配置重试" : "重试任务";
          return `<article id="task-${esc(item.id)}" class="audit-event task-event" data-task-id="${esc(item.id)}" data-task-status="${esc(item.status)}" aria-busy="${item.status === "pending" || item.status === "running"}"><div class="timeline-marker"><i class="${tone}"></i><span></span></div><div class="task-event-card">
            <header><div class="event-action"><span class="status-label ${tone}">${esc(statusLabel)}</span><strong>${esc(actionLabel)}</strong><small><code title="${esc(item.id)}">${esc(short(item.id))}</code> · ${item.attempt ? `第 ${item.attempt} 次执行` : "尚未开始"}</small>
              ${item.status === "pending" && can("tasks.execute") ? `<button class="button small task-inline-action" data-cancel="${esc(item.id)}">取消任务</button>` : ""}
              ${["failed", "canceled"].includes(item.status) && can("tasks.execute") ? `<button class="button small task-inline-action" data-retry="${esc(item.id)}" data-retry-version="${item.install_if_missing ? item.config_version : ""}">${esc(retryLabel)}</button>` : ""}
            </div><time><b>${date(item.created_at)}</b><small data-task-age>${ago(item.created_at)}</small></time></header>
            <div class="task-event-body"><div class="event-target"><span class="engine-badge ${esc(taskEngine)}">${esc(taskEngineLabel)}</span><span><b>节点</b><small>${esc(agent?.name || short(item.agent_id))}</small></span>
              ${item.config_id ? `<span><b>配置</b><small>${esc(short(item.config_id))} · v${item.config_version}</small></span>` : item.core_version ? `<span><b>版本</b><small>${esc(item.core_version)}${sourceLabel ? ` · ${esc(sourceLabel)}` : ""}</small></span>` : ""}
              ${item.install_if_missing ? "<span><b>自动安装</b><small>缺少时安装稳定版；已有内核保持版本</small></span>" : ""}
              <span class="task-lifecycle"><b>耗时</b><small data-task-timing>${esc(taskTiming(item))}</small></span></div>
            <div class="event-result">${diagnostic ? `<div class="task-diagnostic"><b>${esc(diagnostic.title)}</b><small>${esc(diagnostic.advice)}</small></div>` : ""}
              ${item.error || item.output ? `<details data-task-result${resultOpen}><summary>节点结果 <span>→</span></summary>${item.error ? `<div class="task-result-block"><header><b>错误</b></header><pre class="task-error">${esc(diagnosticError(item.error))}</pre></div>` : ""}${item.output ? `<div class="task-result-block"><header><b>输出</b></header><pre>${esc(item.output)}</pre></div>` : ""}</details>` : item.status === "pending" || item.status === "running" ? "<span>执行中</span>" : ""}
            </div></div></div></article>`;
        })
        .join("") ||
      '<div class="empty large"><strong>没有符合条件的任务</strong></div>'
    );
  }

  function updateTaskClocks(timeline, items) {
    const cards = new Map(
      [...timeline.querySelectorAll(":scope > [data-task-id]")].map((card) => [
        card.dataset.taskId,
        card,
      ]),
    );
    items.forEach((item) => {
      const card = cards.get(item.id);
      if (!card) return;
      const age = card.querySelector("[data-task-age]");
      const timing = card.querySelector("[data-task-timing]");
      if (age) age.textContent = ago(item.created_at);
      if (timing) timing.textContent = taskTiming(item);
    });
  }

  function syncTaskAgentFilter(agents) {
    const field = document.querySelector("#task-agent");
    if (!field) return;
    const signature = JSON.stringify(
      agents.map((agent) => [agent.id, agent.name]),
    );
    if (field.dataset.agentSignature === signature) return;
    const fresh = field.cloneNode(false);
    fresh.innerHTML = `<option value="">全部节点</option>${agents
      .map(
        (agent) =>
          `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`,
      )
      .join("")}`;
    reconcileView(field, fresh);
    field.dataset.agentSignature = signature;
  }

  function taskTiming(task) {
    if (task.status === "pending") return "准备执行";
    if (task.status === "running")
      return task.started_at
        ? `已运行 ${ago(task.started_at).replace("前", "")}`
        : "正在启动执行";
    if (task.started_at && task.finished_at) {
      const seconds = Math.max(
        0,
        Math.round(
          (new Date(task.finished_at) - new Date(task.started_at)) / 1000,
        ),
      );
      return seconds < 60
        ? `执行 ${seconds} 秒`
        : `执行 ${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
    }
    return task.finished_at ? "未开始执行" : "时间记录不完整";
  }

  function renderTaskPage({ agents, taskCards, filters, existingTaskPage, syncFilters }) {
    shell(
      `<div class="task-workspace" data-task-page><details class="task-filter-panel" open><summary><b>筛选</b><i>⌄</i></summary><div class="audit-query"><label>节点<select id="task-agent"><option value="">全部节点</option>${agents.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`).join("")}</select></label><label>状态<select id="task-status"><option value="">全部状态</option>${["pending", "running", "succeeded", "failed", "canceled"].map((status) => `<option value="${status}">${esc(statusName(status))}</option>`).join("")}</select></label><label>动作<select id="task-action"><option value="">全部动作</option>${actions.map((action) => `<option value="${action}">${esc(actionName(action))}</option>`).join("")}</select></label><label>每页数量<select id="task-limit"><option value="50">50 条</option><option value="100">100 条</option><option value="500">500 条</option></select></label><button class="button primary" type="button" data-apply-task-filter>应用筛选</button></div></details><div class="audit-live syncing" data-task-refresh-status role="status"><i></i><span data-task-refresh-label>自动更新</span></div><section class="task-timeline" aria-label="任务时间线">${taskCards}</section></div>`,
      "执行记录",
      { viewKey: `tasks-${filters.agent_id || "all"}-${filters.status || "all"}-${filters.action || "all"}-${filters.limit || 100}` },
    );
    document
      .querySelector("[data-apply-task-filter]")
      ?.insertAdjacentHTML(
        "afterend",
        '<a class="task-filter-reset" href="#tasks" data-reset-task-filter>重置筛选</a>',
      );
    if (!existingTaskPage || syncFilters) {
      const filterValues = state.data.taskFilters || {};
      ["agent", "status", "action"].forEach((name) => {
        const field = document.querySelector(`#task-${name}`);
        if (field) {
          field.value =
            filterValues[name === "agent" ? "agent_id" : name] || "";
        }
      });
      const limitField = document.querySelector("#task-limit");
      if (limitField) limitField.value = String(filterValues.limit || 100);
    }

  }
  return { renderTaskCards, updateTaskClocks, syncTaskAgentFilter, renderTaskPage };
}
