import {
  bindEvent,
  createPoller,
  createRefreshChannel,
} from "./refresh.js";

const visibleLevel = (level) => {
  if (["error", "critical"].includes(level)) return "error";
  if (level === "warning") return "warning";
  return "info";
};

export function filterCoreLogEntries(entries, filters = {}) {
  const keyword = String(filters.q || "").trim().toLowerCase();
  return (entries || []).filter((entry) => {
    if (filters.engine && entry.engine !== filters.engine) return false;
    if (filters.level && visibleLevel(entry.level) !== filters.level) return false;
    return !keyword || String(entry.message || "").toLowerCase().includes(keyword);
  });
}

export function coreLogFilterCounts(entries, engines = []) {
  const engine = Object.fromEntries(engines.map((value) => [value, 0]));
  const level = { info: 0, warning: 0, error: 0 };
  (entries || []).forEach((entry) => {
    if (Object.hasOwn(engine, entry.engine)) engine[entry.engine] += 1;
    level[visibleLevel(entry.level)] += 1;
  });
  return { total: (entries || []).length, engine, level };
}

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
  const storagePolicyName = (value) =>
    ({
      debug: "保存全部级别",
      info: "保存信息及以上",
      warning: "保存警告及以上",
      error: "保存错误及以上",
      critical: "仅保存严重错误",
      off: "已停止保存新日志",
    })[value] || "保存策略不可见";

  const query = () => {
    const filters = state.data.coreLogFilters || {};
    const params = new URLSearchParams();
    if (filters.agent_id) params.set("agent_id", filters.agent_id);
    params.set("limit", String(filters.limit || 200));
    return { filters, params };
  };

  const renderCoreLogs = (sourceEntries, agents, filters) => {
    const entries = filterCoreLogEntries(sourceEntries, filters);
    const counts = coreLogFilterCounts(sourceEntries, engines);
    state.data.coreLogEntries = sourceEntries;
    state.data.coreLogs = entries;
    state.data.agents = agents;
    const agentsByID = new Map(agents.map((agent) => [agent.id, agent]));
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
    const engineButtons = [
      ["", "全部", counts.total],
      ...engines.map((engine) => [
        engine,
        engine === "ss-rust" ? "SS Rust" : engineName(engine),
        counts.engine[engine] || 0,
      ]),
    ]
      .map(
        ([value, label, count]) =>
          `<button type="button" name="engine" value="${esc(value)}" data-core-log-engine aria-pressed="${String((filters.engine || "") === value)}"><span>${esc(label)}</span><b>${count}</b></button>`,
      )
      .join("");
    const levelButtons = [
      ["", "全部", counts.total],
      ["info", "信息", counts.level.info],
      ["warning", "警告", counts.level.warning],
      ["error", "错误", counts.level.error],
    ]
      .map(
        ([value, label, count]) =>
          `<button type="button" name="level" value="${esc(value)}" data-core-log-level aria-pressed="${String((filters.level || "") === value)}"><span>${esc(label)}</span><b>${count}</b></button>`,
      )
      .join("");
    const autoRefresh = state.data.coreLogAutoRefresh !== false;
    const scopeName = selectedAgent?.name || (filters.agent_id ? filters.agent_id : "全部节点");
    const storagePolicy = can("settings.read")
      ? storagePolicyName(state.data.settings?.core_log_minimum_level)
      : "保存策略不可见";
    shell(
      `<div class="core-log-workspace" data-core-log-page><header class="core-log-header"><div><h2>内核日志</h2><p>当前范围：<strong>${esc(scopeName)}</strong></p></div><label class="core-log-auto"><button type="button" role="switch" aria-checked="${String(autoRefresh)}" data-toggle-core-log-refresh><i></i></button><span>自动更新</span></label></header><section class="core-log-filters" id="core-log-filters" aria-label="日志筛选"><div class="core-log-filter-group core-log-engine-filter"><span>内核</span><div role="group" aria-label="日志内核">${engineButtons}</div></div><div class="core-log-filter-group core-log-level-filter"><span>级别</span><div role="group" aria-label="日志级别">${levelButtons}</div></div><label class="core-log-search">关键词<input name="q" type="search" maxlength="120" value="${esc(filters.q || "")}" placeholder="搜索日志内容，输入即筛选" autocomplete="off"></label><label class="core-log-limit">数量<select name="limit">${[100, 200, 500].map((limit) => `<option value="${limit}" ${Number(filters.limit || 200) === limit ? "selected" : ""}>${limit} 条</option>`).join("")}</select></label><button class="button core-log-reset" type="button" data-reset-core-logs>清除筛选</button></section><div class="core-log-status" role="status" data-core-log-refresh-status><span>显示 <strong>${entries.length}</strong> 条结果</span><span><span class="core-log-live"><i></i><span data-core-log-refresh-label>${autoRefresh ? "正在实时更新" : "自动更新已暂停"}</span></span><span>${esc(storagePolicy)} · 保留 7 天</span></span></div><section class="core-log-stream" aria-label="内核运行日志" data-refresh-scroll><header class="core-log-columns" aria-hidden="true"><span>时间</span><span>内核</span><span>级别</span><span>节点</span><span>日志内容</span></header>${sourceNotice}${rows || `<div class="core-log-empty"><strong>${esc(emptyTitle)}</strong><span>${esc(emptyDetail)}</span></div>`}</section></div>`,
      "内核日志",
    );

    const renderLocalFilters = (patch) => {
      state.data.coreLogFilters = {
        ...(state.data.coreLogFilters || {}),
        ...patch,
      };
      renderCoreLogs(
        state.data.coreLogEntries || [],
        state.data.agents || [],
        state.data.coreLogFilters,
      );
    };
    document.querySelectorAll("[data-core-log-engine]").forEach((button) => {
      bindEvent(button, "click", () => renderLocalFilters({ engine: button.value }));
    });
    document.querySelectorAll("[data-core-log-level]").forEach((button) => {
      bindEvent(button, "click", () => renderLocalFilters({ level: button.value }));
    });
    bindEvent(document.querySelector('#core-log-filters input[name="q"]'), "input", (event) => {
      renderLocalFilters({ q: event.currentTarget.value });
    });
    bindEvent(document.querySelector('#core-log-filters select[name="limit"]'), "change", async (event) => {
      state.data.coreLogFilters = {
        ...(state.data.coreLogFilters || {}),
        limit: Number(event.currentTarget.value || 200),
      };
      await coreLogs({ syncFilters: true });
    });
    bindEvent(document.querySelector("[data-reset-core-logs]"), "click", () => {
      const search = document.querySelector('#core-log-filters input[name="q"]');
      if (search) search.value = "";
      renderLocalFilters({ engine: "", level: "", q: "" });
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
    bindEvent(document.querySelector("[data-toggle-core-log-refresh]"), "click", (event) => {
      const enabled = state.data.coreLogAutoRefresh === false;
      state.data.coreLogAutoRefresh = enabled;
      event.currentTarget.setAttribute("aria-checked", String(enabled));
      const label = document.querySelector("[data-core-log-refresh-label]");
      if (label) label.textContent = enabled ? "正在实时更新" : "自动更新已暂停";
      if (enabled) poller.start();
      else poller.stop();
    });
  };

  const poller = createPoller({
    run: () => coreLogs({ background: true }),
    isActive: () =>
      state.route === "core-logs" && state.data.coreLogAutoRefresh !== false,
    delay: () => 10_000,
    setTimer,
    clearTimer,
  });

  async function coreLogs({ background = false } = {}) {
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
          renderCoreLogs(
            entries,
            agents,
            state.data.coreLogFilters || filters,
          ),
      );
      if (applied && state.data.coreLogAutoRefresh !== false) poller.start();
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
      if (state.data.coreLogAutoRefresh !== false) poller.start();
      return false;
    }
  }

  return coreLogs;
}
