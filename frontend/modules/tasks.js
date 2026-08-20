export function installTasks(ctx) {
  const { api, state, actions, can, esc, statusName, engineName, short, date, ago, actionName, statusTone, notify, confirmAction, shell } = ctx;
async function tasks() {
  if (state.confirmOpen) {
    clearTimeout(state.taskPollTimer);
    state.taskPollTimer = setTimeout(() => tasks(), 300);
    return;
  }
  const filters = state.data.taskFilters || {};
  const query = new URLSearchParams({
    limit: String(filters.limit || 100),
    ...(filters.agent_id ? { agent_id: filters.agent_id } : {}),
    ...(filters.status ? { status: filters.status } : {}),
    ...(filters.action ? { action: filters.action } : {}),
  });
  const [items, agents, settings] = await Promise.all([
    api(`/tasks?${query}`),
    api("/agents"),
    api("/settings"),
  ]);
  const taskCards =
    items
      .map((item) => {
        const agent = agents.find((entry) => entry.id === item.agent_id);
        const diagnostic = diagnoseTask(item);
        const tone = statusTone(item.status, item.simulated);
        const statusLabel = item.simulated && item.status === "succeeded" ? "模拟完成" : statusName(item.status);
        return `<article id="task-${esc(item.id)}" class="audit-event task-event" data-task-id="${esc(item.id)}" data-task-status="${esc(item.status)}" aria-busy="${item.status === "pending" || item.status === "running"}"><div class="timeline-marker"><i class="${tone}"></i><span></span></div><div class="task-event-card"><header><div class="event-action"><span class="status-label ${tone}">${esc(statusLabel)}</span><strong>${esc(actionName(item.action))}</strong><small><code title="${esc(item.id)}">${esc(short(item.id))}</code> · ${item.attempt ? `第 ${item.attempt} 次执行` : "尚未开始"}</small>${item.status === "pending" && can("operator") ? `<button class="button small task-inline-action" data-cancel="${esc(item.id)}">取消任务</button>` : ""}${["failed", "canceled"].includes(item.status) && can("operator") ? `<button class="button small task-inline-action" data-retry="${esc(item.id)}">${item.config_id ? "使用当前配置重试" : "重试任务"}</button>` : ""}</div><time><b>${date(item.created_at)}</b><small>${ago(item.created_at)}</small></time></header><div class="task-event-body"><div class="event-target"><span class="engine-badge ${esc(item.engine)}">${esc(engineName(item.engine))}</span><span><b>节点</b><small>${esc(agent?.name || short(item.agent_id))}</small></span>${item.config_id ? `<span><b>配置</b><small>${esc(short(item.config_id))} · v${item.config_version}</small></span>` : item.core_version ? `<span><b>版本</b><small>${esc(item.core_version)}</small></span>` : ""}<span class="task-lifecycle"><b>耗时</b><small>${esc(taskTiming(item))}</small></span></div><div class="event-result">${diagnostic ? `<div class="task-diagnostic"><b>${esc(diagnostic.title)}</b><small>${esc(diagnostic.advice)}</small></div>` : ""}${item.error || item.output ? `<details><summary>节点结果 <span>→</span></summary>${item.error ? `<div class="task-result-block"><header><b>错误</b></header><pre class="task-error">${esc(item.error)}</pre></div>` : ""}${item.output ? `<div class="task-result-block"><header><b>输出</b></header><pre>${esc(item.output)}</pre></div>` : ""}</details>` : item.status === "pending" || item.status === "running" ? "<span>执行中</span>" : ""}</div></div></div></article>`;
      })
      .join("") ||
    '<div class="empty large"><strong>没有符合条件的任务</strong></div>';
  shell(
    `<div class="task-workspace" data-task-page><details class="task-filter-panel" open><summary><b>筛选</b><i>⌄</i></summary><div class="audit-query"><label>节点<select id="task-agent"><option value="">全部节点</option>${agents.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`).join("")}</select></label><label>状态<select id="task-status"><option value="">全部状态</option>${["pending", "running", "succeeded", "failed", "canceled"].map((status) => `<option value="${status}">${esc(statusName(status))}</option>`).join("")}</select></label><label>动作<select id="task-action"><option value="">全部动作</option>${actions.map((action) => `<option value="${action}">${esc(actionName(action))}</option>`).join("")}</select></label><label>每页数量<select id="task-limit"><option value="50">50 条</option><option value="100">100 条</option><option value="500">500 条</option></select></label><button class="button primary" type="button" data-apply-task-filter>应用筛选</button></div></details><div class="audit-live syncing" data-task-refresh-status role="status"><i></i>自动更新</div><section class="task-timeline" aria-label="任务时间线">${taskCards}</section></div>`,
    "执行记录",
  );
  document.querySelector("[data-apply-task-filter]")?.insertAdjacentHTML("afterend", '<a class="task-filter-reset" href="#tasks" data-reset-task-filter>重置筛选</a>');
  const filterValues = state.data.taskFilters || {};
  ["agent", "status", "action"].forEach((name) => {
    const field = document.querySelector(`#task-${name}`);
    if (field) {
      field.value = filterValues[name === "agent" ? "agent_id" : name] || "";
    }
  });
  const limitField = document.querySelector("#task-limit");
  if (limitField) limitField.value = String(filterValues.limit || 100);
  document
    .querySelector("[data-apply-task-filter]")
    ?.addEventListener("click", () => {
      state.data.taskFilters = {
        agent_id: document.querySelector("#task-agent")?.value || "",
        status: document.querySelector("#task-status")?.value || "",
        action: document.querySelector("#task-action")?.value || "",
        limit: Number(document.querySelector("#task-limit")?.value || 100),
      };
      tasks();
    });
  document.querySelectorAll("[data-task-status-filter]").forEach((link) => {
    link.onclick = (event) => {
      event.preventDefault();
      state.data.taskFilters = {
        ...(state.data.taskFilters || {}),
        status: link.dataset.taskStatusFilter,
      };
      tasks();
    };
  });
  const timeline = document.querySelector(".task-timeline");
  const loadMore = document.createElement("button");
  loadMore.className = "button task-load-more";
  loadMore.type = "button";
  loadMore.textContent = "加载更多任务";
  loadMore.hidden = true;
  if (timeline && items.length > 20 && window.matchMedia("(max-width: 820px)").matches) {
    const rows = [...timeline.querySelectorAll("[data-task-id]")];
    rows.slice(20).forEach((row) => (row.hidden = true));
    timeline.after(loadMore);
    loadMore.hidden = false;
    loadMore.onclick = () => { rows.forEach((row) => (row.hidden = false)); loadMore.hidden = true; };
  }
  document.querySelector("[data-reset-task-filter]")?.addEventListener("click", (event) => {
    event.preventDefault();
    state.data.taskFilters = {};
    tasks();
  });
  document.querySelectorAll("[data-cancel]").forEach(
    (button) =>
      (button.onclick = async () => {
        if (!(await confirmAction("确定取消这个待执行任务？", "取消任务"))) return;
        try {
          await api(`/tasks/${button.dataset.cancel}`, { method: "DELETE" });
          tasks();
        } catch (error) {
          notify(error.message, "error");
        }
      }),
  );
  document.querySelectorAll("[data-retry]").forEach(
    (button) =>
      (button.onclick = async () => {
        if (!(await confirmAction("确定使用当前配置重新提交这个任务？", "重试任务"))) return;
        try {
          await api(`/tasks/${button.dataset.retry}/retry`, { method: "POST" });
          tasks();
        } catch (error) {
          notify(error.message, "error");
        }
      }),
  );
  clearTimeout(state.taskPollTimer);
  if (state.route === "tasks") {
    state.taskPollTimer = setTimeout(
      () => tasks(),
      settings.task_poll_interval_ms || 1000,
    );
  }
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

function diagnoseTask(task) {
  if (task.status !== "failed") return null;
  const error = String(task.error || "").toLowerCase();
  if (error.includes("rolled back"))
    return {
      title: "变更失败，已自动回滚",
      advice: "旧配置或旧二进制已经恢复；先查询服务状态后再重试。",
    };
  if (error.includes("rejected the configuration"))
    return {
      title: "配置未通过真实内核校验",
      advice: "展开节点返回结果定位字段，修正后使用当前配置重试。",
    };
  if (task.action === "install")
    return {
      title: "内核安装或切换失败",
      advice: "展开结果确认下载、校验或重启阶段。",
    };
  return {
    title: "节点操作执行失败",
    advice: "展开节点返回结果并确认节点与服务状态后再重试。",
  };
}
  return tasks;
}
