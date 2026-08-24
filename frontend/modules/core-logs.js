import {
  bindEvent,
  createPoller,
  createRefreshChannel,
} from "./refresh.js";

export function installCoreLogs(ctx) {
  const {
    api,
    state,
    engines,
    can,
    esc,
    engineName,
    date,
    shell,
    setTimer = (callback, delay) => {
      state.coreLogPollTimer = setTimeout(callback, delay);
      return state.coreLogPollTimer;
    },
    clearTimer = (timer) => clearTimeout(timer),
  } = ctx;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "core-logs",
    getScope: () => state.navigationEpoch,
  });
  const levelName = (value) =>
    ({ debug: "调试", info: "信息", warning: "警告", error: "错误", critical: "严重" })[value] || value;

  const query = () => {
    const filters = state.data.coreLogFilters || {};
    const params = new URLSearchParams();
    if (filters.agent_id) params.set("agent_id", filters.agent_id);
    if (filters.engine) params.set("engine", filters.engine);
    if (filters.level) params.set("level", filters.level);
    if (filters.q) params.set("q", filters.q);
    params.set("limit", String(filters.limit || 200));
    return { filters, params };
  };

  const renderCoreLogs = (entries, agents, filters, syncFilters) => {
    const existingPage = document.querySelector("[data-core-log-page]");
    state.data.coreLogs = entries;
    state.data.agents = agents;
    const agentsByID = new Map(agents.map((agent) => [agent.id, agent]));
    const errorCount = entries.filter((entry) =>
      ["error", "critical"].includes(entry.level),
    ).length;
    const warningCount = entries.filter(
      (entry) => entry.level === "warning",
    ).length;
    const selectedAgent = agentsByID.get(filters.agent_id || "");
    const selectedRuntime = filters.engine
      ? selectedAgent?.runtime?.[filters.engine]
      : null;
    const sourceStates = selectedAgent
      ? Object.values(selectedAgent.runtime || {})
          .map((runtime) => runtime?.core_log_status)
          .filter(Boolean)
      : [];
    let emptyTitle = "暂无日志";
    let emptyDetail = "尚未收到符合当前筛选条件的运行记录。";
    let sourceNoticeTitle = "";
    let sourceNoticeDetail = "";
    if (filters.agent_id && !can("agents.read")) {
      emptyTitle = "无法核验采集状态";
      emptyDetail = "当前账号无权读取节点状态；历史日志仍可按现有权限查看。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (filters.agent_id && !selectedAgent) {
      emptyTitle = "节点状态不可用";
      emptyDetail = "无法读取当前所选节点的运行状态。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (selectedAgent?.status === "offline") {
      emptyTitle = "节点离线";
      emptyDetail = "当前日志采集已暂停；已有内容为保留期内的历史记录。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (selectedAgent && selectedAgent.status !== "online") {
      emptyTitle = "节点状态不可用";
      emptyDetail = "无法确认当前节点是否仍在采集日志。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (
      selectedAgent &&
      !(selectedAgent.features || []).includes("core-logs-v1")
    ) {
      emptyTitle = "此节点不支持集中日志";
      emptyDetail = "请升级 Agent 后再查看内核运行日志。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (
      selectedAgent &&
      !(selectedAgent.features || []).includes("core-log-status-v1")
    ) {
      emptyTitle = "日志状态能力不可用";
      emptyDetail = "此 Agent 可以上传日志，但无法报告当前采集来源是否正常。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (filters.engine && selectedRuntime?.installed === false) {
      emptyTitle = "内核尚未安装";
      emptyDetail = "当前节点尚未安装所选内核，因此没有可采集的运行日志。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (
      selectedAgent &&
      (selectedAgent.features || []).includes("core-log-status-v1")
    ) {
      const status =
        filters.engine
          ? selectedRuntime?.core_log_status || ""
          : sourceStates.includes("failed")
            ? "failed"
            : sourceStates.length &&
                sourceStates.every((value) => value === "waiting")
              ? "waiting"
              : "";
      if (status === "failed") {
        emptyTitle = "日志采集失败";
        emptyDetail =
          "Agent 已拒绝或无法读取该日志来源，请检查节点上的 Agent 诊断日志。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (status === "waiting") {
        emptyTitle = "等待日志来源";
        emptyDetail = "日志文件尚未创建；服务写入后会自动开始采集。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (filters.engine && !status) {
        emptyTitle = "日志状态尚未上报";
        emptyDetail = "Agent 尚未报告当前内核的采集来源状态。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (filters.engine && status === "active") {
        emptyDetail = "当前来源工作正常，尚未收到符合筛选条件的新运行记录。";
      }
    }
    const rows = entries
      .map((entry) => {
        const agent = agentsByID.get(entry.agent_id);
        return `<article class="core-log-row level-${esc(entry.level)}" data-refresh-key="core-log-${esc(entry.id)}"><time datetime="${esc(entry.logged_at)}">${esc(date(entry.logged_at))}</time><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="core-log-level">${esc(levelName(entry.level))}</span><span class="core-log-agent" title="${esc(agent?.name || entry.agent_id)}">${esc(agent?.name || entry.agent_id)}</span><pre>${esc(entry.message)}</pre></article>`;
      })
      .join("");

    const sourceNotice = sourceNoticeTitle && rows
      ? `<div class="core-log-source-notice" role="status"><strong>${esc(sourceNoticeTitle)}</strong><span>${esc(sourceNoticeDetail)}</span></div>`
      : "";
    shell(
      `<div class="core-log-workspace" data-core-log-page><header class="core-log-header"><div><p class="eyebrow">Runtime logs</p><h2>内核日志</h2></div><dl><div><dt>当前结果</dt><dd>${entries.length}</dd></div><div><dt>警告</dt><dd>${warningCount}</dd></div><div class="${errorCount ? "bad" : ""}"><dt>错误</dt><dd>${errorCount}</dd></div></dl></header><form class="core-log-filters" id="core-log-filters"><label>节点<select name="agent_id"><option value="">全部节点</option>${agents.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`).join("")}</select></label><label>内核<select name="engine"><option value="">全部内核</option>${engines.map((engine) => `<option value="${esc(engine)}">${esc(engineName(engine))}</option>`).join("")}</select></label><label>级别<select name="level"><option value="">全部级别</option>${["debug", "info", "warning", "error", "critical"].map((level) => `<option value="${level}">${levelName(level)}</option>`).join("")}</select></label><label class="core-log-search">关键词<input name="q" type="search" maxlength="120" value="${esc(filters.q || "")}" placeholder="搜索日志内容"></label><label>数量<select name="limit">${[100, 200, 500].map((limit) => `<option value="${limit}">${limit} 条</option>`).join("")}</select></label><button class="button primary" type="submit">应用</button><button class="button" type="button" data-reset-core-logs>重置</button></form><div class="core-log-status" role="status" data-core-log-refresh-status><span><i></i><span data-core-log-refresh-label>自动更新</span></span><span>面板保留 7 天</span></div><section class="core-log-stream" aria-label="内核运行日志" data-refresh-scroll>${sourceNotice}${rows || `<div class="core-log-empty"><strong>${esc(emptyTitle)}</strong><span>${esc(emptyDetail)}</span></div>`}</section></div>`,
      "内核日志",
    );

    const form = document.querySelector("#core-log-filters");
    if (form && (!existingPage || syncFilters)) {
      form.elements.agent_id.value = filters.agent_id || "";
      form.elements.engine.value = filters.engine || "";
      form.elements.level.value = filters.level || "";
      form.elements.limit.value = String(filters.limit || 200);
      bindEvent(form, "submit", async (event) => {
        event.preventDefault();
        const values = new FormData(form);
        state.data.coreLogFilters = {
          agent_id: values.get("agent_id") || "",
          engine: values.get("engine") || "",
          level: values.get("level") || "",
          q: String(values.get("q") || "").trim(),
          limit: Number(values.get("limit") || 200),
        };
        await coreLogs({ syncFilters: true });
      });
    }
    bindEvent(document.querySelector("[data-reset-core-logs]"), "click", async () => {
      state.data.coreLogFilters = {};
      await coreLogs({ syncFilters: true });
    });
    document.querySelectorAll("[data-core-log-agent]").forEach((link) => {
      bindEvent(link, "click", async (event) => {
        event.preventDefault();
        state.data.coreLogFilters = {
          ...(state.data.coreLogFilters || {}),
          agent_id: link.dataset.coreLogAgent || "",
        };
        await coreLogs({ syncFilters: true });
      });
    });
  };

  const poller = createPoller({
    run: () => coreLogs({ background: true }),
    isActive: () => state.route === "core-logs",
    delay: () => 10_000,
    setTimer,
    clearTimer,
  });

  async function coreLogs({ background = false, syncFilters = false } = {}) {
    poller.stop();
    const { filters, params } = query();
    try {
      const applied = await refresh.run(
        (signal) =>
          Promise.all([
            api(`/core-logs?${params}`, { signal }),
            can("agents.read")
              ? api("/agents", { signal })
              : Promise.resolve([]),
          ]),
        ([entries, agents]) =>
          renderCoreLogs(entries, agents, filters, syncFilters),
      );
      if (applied) poller.start();
      return applied;
    } catch (error) {
      const status = document.querySelector("[data-core-log-refresh-status]");
      if (!status && !background) {
        const permissionDenied = error.status === 403;
        shell(
          `<div class="core-log-workspace" data-core-log-page><div class="core-log-empty"><strong>${permissionDenied ? "无权查看内核日志" : "内核日志加载失败"}</strong><span>${permissionDenied ? "当前账号缺少内核日志查看权限。" : "无法读取日志数据，请稍后重试。"}</span></div></div>`,
          "内核日志",
        );
      }
      if (status) {
        status.dataset.refreshError = "1";
        status.title = error.message;
        const label = status.querySelector("[data-core-log-refresh-label]");
        if (label) label.textContent = "刷新失败，保留上次数据";
      }
      poller.start();
      return false;
    }
  }

  return coreLogs;
}
