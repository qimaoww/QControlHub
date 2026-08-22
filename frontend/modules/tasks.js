export function installTasks(ctx) {
  const {
    api,
    state,
    actions,
    can,
    esc,
    statusName,
    engineName,
    short,
    date,
    ago,
    actionName,
    statusTone,
    notify,
    confirmAction,
    shell,
    setTimer = (callback, delay) => setTimeout(callback, delay),
    clearTimer = (timer) => clearTimeout(timer),
    now = () => Date.now(),
  } = ctx;
  let latestRequest = 0;
  const settingsCacheDuration = 30_000;

  function scheduleTaskRefresh(delay) {
    clearTimer(state.taskPollTimer);
    if (state.route === "tasks") {
      state.taskPollTimer = setTimer(
        () => tasks({ background: true }),
        delay,
      );
    }
  }

  function openResultTaskIds() {
    return new Set(
      [
        ...document.querySelectorAll(
          "[data-task-id] details[data-task-result][open]",
        ),
      ]
        .map((details) => details.closest("[data-task-id]")?.dataset.taskId)
        .filter(Boolean),
    );
  }

  function taskRenderSignature(items, agents) {
    const agentNames = new Map(agents.map((agent) => [agent.id, agent.name]));
    return JSON.stringify(
      items.map((item) => [
        item.id,
        item.agent_id,
        agentNames.get(item.agent_id) || "",
        item.engine,
        item.action,
        item.status,
        item.attempt,
        item.config_id,
        item.config_version,
        item.core_version,
        item.created_at,
        item.started_at,
        item.finished_at,
        item.error,
        item.output,
      ]),
    );
  }

  function renderTaskCards(items, agents, openResults) {
    return (
      items
        .map((item) => {
          const agent = agents.find((entry) => entry.id === item.agent_id);
          const diagnostic = diagnoseTask(item);
          const tone = statusTone(item.status);
          const statusLabel = statusName(item.status);
          const taskEngine =
            item.action === "upgrade-agent" ? "qagent" : item.engine;
          const taskEngineLabel =
            item.action === "upgrade-agent"
              ? "QAgent"
              : engineName(item.engine);
          const resultOpen = openResults.has(item.id) ? " open" : "";
          return `<article id="task-${esc(item.id)}" class="audit-event task-event" data-task-id="${esc(item.id)}" data-task-status="${esc(item.status)}" aria-busy="${item.status === "pending" || item.status === "running"}"><div class="timeline-marker"><i class="${tone}"></i><span></span></div><div class="task-event-card"><header><div class="event-action"><span class="status-label ${tone}">${esc(statusLabel)}</span><strong>${esc(actionName(item.action))}</strong><small><code title="${esc(item.id)}">${esc(short(item.id))}</code> · ${item.attempt ? `第 ${item.attempt} 次执行` : "尚未开始"}</small>${item.status === "pending" && can("operator") ? `<button class="button small task-inline-action" data-cancel="${esc(item.id)}">取消任务</button>` : ""}${["failed", "canceled"].includes(item.status) && can("operator") ? `<button class="button small task-inline-action" data-retry="${esc(item.id)}">${item.config_id ? "使用当前配置重试" : "重试任务"}</button>` : ""}</div><time><b>${date(item.created_at)}</b><small data-task-age>${ago(item.created_at)}</small></time></header><div class="task-event-body"><div class="event-target"><span class="engine-badge ${esc(taskEngine)}">${esc(taskEngineLabel)}</span><span><b>节点</b><small>${esc(agent?.name || short(item.agent_id))}</small></span>${item.config_id ? `<span><b>配置</b><small>${esc(short(item.config_id))} · v${item.config_version}</small></span>` : item.core_version ? `<span><b>版本</b><small>${esc(item.core_version)}</small></span>` : ""}<span class="task-lifecycle"><b>耗时</b><small data-task-timing>${esc(taskTiming(item))}</small></span></div><div class="event-result">${diagnostic ? `<div class="task-diagnostic"><b>${esc(diagnostic.title)}</b><small>${esc(diagnostic.advice)}</small></div>` : ""}${item.error || item.output ? `<details data-task-result${resultOpen}><summary>节点结果 <span>→</span></summary>${item.error ? `<div class="task-result-block"><header><b>错误</b></header><pre class="task-error">${esc(item.error)}</pre></div>` : ""}${item.output ? `<div class="task-result-block"><header><b>输出</b></header><pre>${esc(item.output)}</pre></div>` : ""}</details>` : item.status === "pending" || item.status === "running" ? "<span>执行中</span>" : ""}</div></div></div></article>`;
        })
        .join("") ||
      '<div class="empty large"><strong>没有符合条件的任务</strong></div>'
    );
  }

  function normalizedTaskMarkup(node) {
    const clone = node.cloneNode(true);
    clone.hidden = false;
    clone
      .querySelectorAll("[data-task-age], [data-task-timing]")
      .forEach((label) => (label.textContent = ""));
    return clone.outerHTML;
  }

  function reconcileTaskTimeline(timeline, taskCards) {
    const template = document.createElement("template");
    template.innerHTML = taskCards;
    const freshCards = [...template.content.children];
    if (freshCards.some((card) => !card.dataset.taskId)) {
      timeline.replaceChildren(...freshCards);
      return;
    }

    const existingCards = new Map(
      [...timeline.querySelectorAll(":scope > [data-task-id]")].map((card) => [
        card.dataset.taskId,
        card,
      ]),
    );
    const nextCards = freshCards.map((freshCard) => {
      const existingCard = existingCards.get(freshCard.dataset.taskId);
      if (
        existingCard &&
        normalizedTaskMarkup(existingCard) ===
          normalizedTaskMarkup(freshCard)
      ) {
        return existingCard;
      }
      return freshCard;
    });

    let cursor = timeline.firstElementChild;
    nextCards.forEach((card) => {
      if (card === cursor) {
        cursor = cursor.nextElementSibling;
      } else {
        timeline.insertBefore(card, cursor);
      }
    });
    const retainedCards = new Set(nextCards);
    [...timeline.children].forEach((card) => {
      if (!retainedCards.has(card)) card.remove();
    });
  }

  function captureTaskAnchor(timeline) {
    const main = document.querySelector(".workspace-main");
    if (!main) return null;
    const mobile = window.matchMedia("(max-width: 820px)").matches;
    const scroller = mobile ? document.scrollingElement : main;
    if (!scroller || scroller.scrollTop <= 1) return null;
    const viewportTop = mobile ? 0 : main.getBoundingClientRect().top;
    const viewportBottom = mobile
      ? window.innerHeight
      : main.getBoundingClientRect().bottom;
    const anchors = [...timeline.querySelectorAll(":scope > [data-task-id]")]
      .filter((card) => {
        if (card.hidden) return false;
        const bounds = card.getBoundingClientRect();
        return bounds.bottom > viewportTop && bounds.top < viewportBottom;
      })
      .map((card) => ({
        id: card.dataset.taskId,
        top: card.getBoundingClientRect().top,
      }));
    return anchors.length ? { scroller, anchors } : null;
  }

  function restoreTaskAnchor(anchor, timeline) {
    if (!anchor?.scroller?.isConnected) return;
    const cards = new Map(
      [...timeline.querySelectorAll(":scope > [data-task-id]")].map((card) => [
        card.dataset.taskId,
        card,
      ]),
    );
    const previous = anchor.anchors.find(
      (entry) => cards.has(entry.id) && !cards.get(entry.id).hidden,
    );
    if (!previous) return;
    const delta =
      cards.get(previous.id).getBoundingClientRect().top - previous.top;
    if (Math.abs(delta) < 0.5) return;
    anchor.scroller.scrollTop += delta;
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
    const selected = field.value;
    field.innerHTML = `<option value="">全部节点</option>${agents
      .map(
        (agent) =>
          `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`,
      )
      .join("")}`;
    field.value = selected;
    field.dataset.agentSignature = signature;
  }

  function setupTaskPagination(timeline) {
    let loadMore = timeline.nextElementSibling;
    if (!loadMore?.matches(".task-load-more")) {
      loadMore = document.createElement("button");
      loadMore.className = "button task-load-more";
      loadMore.type = "button";
      loadMore.textContent = "加载更多任务";
      timeline.after(loadMore);
    }
    const rows = [...timeline.querySelectorAll(":scope > [data-task-id]")];
    rows.forEach((row) => (row.hidden = false));
    const shouldCollapse =
      rows.length > 20 &&
      window.matchMedia("(max-width: 820px)").matches &&
      timeline.dataset.mobileExpanded !== "true";
    rows.slice(shouldCollapse ? 20 : rows.length).forEach(
      (row) => (row.hidden = true),
    );
    loadMore.hidden = !shouldCollapse;
    loadMore.onclick = () => {
      rows.forEach((row) => (row.hidden = false));
      timeline.dataset.mobileExpanded = "true";
      loadMore.hidden = true;
    };
  }

  function bindTaskActions(root) {
    root.querySelectorAll("[data-cancel]").forEach(
      (button) =>
        (button.onclick = async () => {
          if (!(await confirmAction("确定取消这个待执行任务？", "取消任务")))
            return;
          try {
            await api(`/tasks/${button.dataset.cancel}`, {
              method: "DELETE",
            });
            tasks({ background: true });
          } catch (error) {
            notify(error.message, "error");
          }
        }),
    );
    root.querySelectorAll("[data-retry]").forEach(
      (button) =>
        (button.onclick = async () => {
          if (
            !(await confirmAction(
              "确定使用当前配置重新提交这个任务？",
              "重试任务",
            ))
          )
            return;
          try {
            await api(`/tasks/${button.dataset.retry}/retry`, {
              method: "POST",
            });
            tasks({ background: true });
          } catch (error) {
            notify(error.message, "error");
          }
        }),
    );
  }

  async function tasks({ background = false, settings: preloadedSettings } = {}) {
    clearTimer(state.taskPollTimer);
    if (state.confirmOpen) {
      scheduleTaskRefresh(300);
      return;
    }

    const requestID = ++latestRequest;
    const filters = state.data.taskFilters || {};
    const query = new URLSearchParams({
      limit: String(filters.limit || 100),
      ...(filters.agent_id ? { agent_id: filters.agent_id } : {}),
      ...(filters.status ? { status: filters.status } : {}),
      ...(filters.action ? { action: filters.action } : {}),
    });
    const currentTimeline = background
      ? document.querySelector("[data-task-page] .task-timeline")
      : null;
    const settingsCacheAge = now() - Number(state.data.taskSettingsLoadedAt || 0);
    const cachedSettings =
      background &&
      state.data.settings &&
      settingsCacheAge >= 0 &&
      settingsCacheAge < settingsCacheDuration
        ? state.data.settings
        : null;
    const [items, agents, settings] = await Promise.all([
      api(`/tasks?${query}`),
      api("/agents"),
      preloadedSettings || cachedSettings || api("/settings"),
    ]);
    if (requestID !== latestRequest || state.route !== "tasks") return;

    const taskCards = renderTaskCards(items, agents, openResultTaskIds());
    const signature = taskRenderSignature(items, agents);
    const pollInterval = settings.task_poll_interval_ms || 1000;
    state.data.settings = settings;
    if (settings !== cachedSettings) state.data.taskSettingsLoadedAt = now();
    if (state.confirmOpen) {
      scheduleTaskRefresh(300);
      return;
    }

    if (currentTimeline?.isConnected) {
      syncTaskAgentFilter(agents);
      if (signature !== state.data.taskRenderSignature) {
        const anchor = captureTaskAnchor(currentTimeline);
        reconcileTaskTimeline(currentTimeline, taskCards);
        setupTaskPagination(currentTimeline);
        bindTaskActions(currentTimeline);
        restoreTaskAnchor(anchor, currentTimeline);
        state.data.taskRenderSignature = signature;
      }
      updateTaskClocks(currentTimeline, items);
      scheduleTaskRefresh(pollInterval);
      return;
    }

    shell(
      `<div class="task-workspace" data-task-page><details class="task-filter-panel" open><summary><b>筛选</b><i>⌄</i></summary><div class="audit-query"><label>节点<select id="task-agent"><option value="">全部节点</option>${agents.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`).join("")}</select></label><label>状态<select id="task-status"><option value="">全部状态</option>${["pending", "running", "succeeded", "failed", "canceled"].map((status) => `<option value="${status}">${esc(statusName(status))}</option>`).join("")}</select></label><label>动作<select id="task-action"><option value="">全部动作</option>${actions.map((action) => `<option value="${action}">${esc(actionName(action))}</option>`).join("")}</select></label><label>每页数量<select id="task-limit"><option value="50">50 条</option><option value="100">100 条</option><option value="500">500 条</option></select></label><button class="button primary" type="button" data-apply-task-filter>应用筛选</button></div></details><div class="audit-live syncing" data-task-refresh-status role="status"><i></i>自动更新</div><section class="task-timeline" aria-label="任务时间线">${taskCards}</section></div>`,
      "执行记录",
    );
    document
      .querySelector("[data-apply-task-filter]")
      ?.insertAdjacentHTML(
        "afterend",
        '<a class="task-filter-reset" href="#tasks" data-reset-task-filter>重置筛选</a>',
      );
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
    syncTaskAgentFilter(agents);
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
    document
      .querySelector("[data-reset-task-filter]")
      ?.addEventListener("click", (event) => {
        event.preventDefault();
        state.data.taskFilters = {};
        tasks();
      });
    const timeline = document.querySelector(".task-timeline");
    if (timeline) {
      setupTaskPagination(timeline);
      bindTaskActions(timeline);
    }
    state.data.taskRenderSignature = signature;
    scheduleTaskRefresh(pollInterval);
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
