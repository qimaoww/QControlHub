import {
  bindEvent,
  createPoller,
  createRefreshChannel,
} from "./refresh.js";
import { createCoreLogCache } from "./core-log-cache.js";
import { accountStorage } from "./account-storage.js";
import {
  coreLogFilterLimits,
  saveCoreLogPreferences,
  savedCoreLogPreferences,
} from "./core-log-preferences.js";

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
    storage = accountStorage,
    now = Date.now,
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

  // Browser storage only seeds the selection for a fresh session. Once the
  // page owns filters in memory, a later render, poll, or in-page filter click
  // keeps using them, and every user change is written back.
  const restoreSelection = () => {
    if (state.data.coreLogFilters) return;
    const saved = savedCoreLogPreferences(storage, engines);
    state.data.coreLogFilters = saved.filters;
    state.data.coreLogAutoRefresh = saved.autoRefresh;
  };
  const rememberSelection = () => {
    saveCoreLogPreferences(
      {
        filters: state.data.coreLogFilters || {},
        autoRefresh: state.data.coreLogAutoRefresh,
      },
      storage,
      engines,
    );
  };

  const query = () => {
    const filters = { ...(state.data.coreLogFilters || {}) };
    const params = new URLSearchParams();
    if (filters.agent_id) params.set("agent_id", filters.agent_id);
    params.set("limit", String(filters.limit || 1000));
    return { filters, params };
  };

  const renderCoreLogs = (sourceEntries, agents, filters) => {
    agents = agents.filter((agent) => agent.can_manage !== false);
    const entries = filterCoreLogEntries(sourceEntries, filters);
    const counts = coreLogFilterCounts(sourceEntries, engines);
    state.data.coreLogEntries = sourceEntries;
    state.data.coreLogs = entries;
    state.data.agents = agents;
    state.data.coreLogDataScope = JSON.stringify([filters.agent_id || "", Number(filters.limit || 1000)]);
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

    const pageSize = 200;
    const pageCount = Math.max(1, Math.ceil(entries.length / pageSize));
    const scope = JSON.stringify([filters.agent_id, filters.engine, filters.level, filters.q, filters.limit]);
    if (state.data.coreLogPageScope !== scope) {
      state.data.coreLogPageScope = scope;
      state.data.coreLogPage = 0;
    }
    const page = Math.max(0, Math.min(state.data.coreLogPage || 0, pageCount - 1));
    state.data.coreLogPage = page;
    // Keep all fetched entries searchable, but bound DOM work independently
    // of the per-engine retention window (up to 8000 entries across engines).
    const rows = entries.slice(page * pageSize, (page + 1) * pageSize)
      .map((entry) => {
        const agent = agentsByID.get(entry.agent_id);
        return `<article class="core-log-row level-${esc(entry.level)}" data-refresh-key="core-log-${esc(entry.id)}"><time datetime="${esc(entry.logged_at)}">${esc(date(entry.logged_at))}</time><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="core-log-level">${esc(levelName(entry.level))}</span><span class="core-log-agent" title="${esc(agent?.name || entry.agent_id)}">${esc(agent?.name || entry.agent_id)}</span><pre>${esc(entry.message)}</pre></article>`;
      })
      .join("");
    const pagination = entries.length > pageSize
      ? `<nav class="core-log-pagination" aria-label="日志分页"><button class="button small" type="button" data-core-log-page-index="${page - 1}" ${page === 0 ? "disabled" : ""}>上一页</button><span>第 ${page + 1} / ${pageCount} 页</span><button class="button small" type="button" data-core-log-page-index="${page + 1}" ${page + 1 === pageCount ? "disabled" : ""}>下一页</button></nav>`
      : "";
    const phase = state.data.coreLogPhase;
    const refreshLabel = {
      loading: "正在加载日志…",
      preview: "已显示最新日志，正在补齐所选窗口…",
      cached: "已显示缓存，正在更新…",
      failed: "刷新失败，保留上次数据",
      incomplete: "补齐失败，当前仅显示部分日志",
    }[phase] || (state.data.coreLogAutoRefresh !== false ? "正在实时更新" : "自动更新已暂停");
    if (phase === "loading") {
      emptyTitle = "正在加载日志";
      emptyDetail = "正在读取当前节点的最新日志，请稍候。";
    }
    if (phase === "failed" && !entries.length) {
      emptyTitle = "日志加载失败";
      emptyDetail = "无法读取当前节点的日志，请稍后重试。";
    }
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
      `<div class="core-log-workspace" data-core-log-page><header class="core-log-header"><div><h2>内核日志</h2><p>当前范围：<strong>${esc(scopeName)}</strong></p></div><label class="core-log-auto"><button type="button" role="switch" aria-checked="${String(autoRefresh)}" data-toggle-core-log-refresh><i></i></button><span>自动更新</span></label></header><section class="core-log-filters" id="core-log-filters" aria-label="日志筛选"><div class="core-log-filter-group core-log-engine-filter"><span>内核</span><div role="group" aria-label="日志内核">${engineButtons}</div></div><div class="core-log-filter-group core-log-level-filter"><span>级别</span><div role="group" aria-label="日志级别">${levelButtons}</div></div><label class="core-log-search">关键词<input name="q" type="search" maxlength="120" value="${esc(filters.q || "")}" placeholder="搜索日志内容，输入即筛选" autocomplete="off"></label><label class="core-log-limit" title="每种内核分别取最新日志，不共用总条数上限">每内核上限<select name="limit">${coreLogFilterLimits.map((limit) => `<option value="${limit}" ${Number(filters.limit || 1000) === limit ? "selected" : ""}>${limit} 条</option>`).join("")}</select></label><button class="button core-log-reset" type="button" data-reset-core-logs>清除筛选</button></section><div class="core-log-status" role="status" data-core-log-refresh-status><span>显示 <strong>${entries.length}</strong> 条结果 · 已加载 ${sourceEntries.length} 条${entries.length > pageSize ? ` · 每页 ${pageSize} 条` : ""}</span><span><span class="core-log-live"><i></i><span data-core-log-refresh-label>${refreshLabel}</span></span><span>${esc(storagePolicy)} · 保留 7 天</span></span></div><div class="core-log-result-toolbar"><span>${entries.length ? `当前 ${page * pageSize + 1}–${Math.min((page + 1) * pageSize, entries.length)} 条` : ""}</span>${pagination}</div><section class="core-log-stream qch-swap-panel" aria-label="内核运行日志" data-refresh-scroll data-refresh-key="core-log-results-${esc(filters.agent_id || "all")}-${esc(filters.engine || "all")}-${esc(filters.level || "all")}"><header class="core-log-columns" aria-hidden="true"><span>时间</span><span>内核</span><span>级别</span><span>节点</span><span>日志内容</span></header>${sourceNotice}${rows || `<div class="core-log-empty"><strong>${esc(emptyTitle)}</strong><span>${esc(emptyDetail)}</span></div>`}</section>${pagination ? `<footer class="core-log-result-footer">${pagination}</footer>` : ""}</div>`,
      "内核日志",
    );

    document.querySelectorAll("[data-core-log-page-index]").forEach((button) => {
      bindEvent(button, "click", () => {
        state.data.coreLogPage = Number(button.dataset.coreLogPageIndex);
        renderCoreLogs(state.data.coreLogEntries || [], state.data.agents || [], state.data.coreLogFilters || filters);
        document.querySelector(".core-log-result-toolbar")?.scrollIntoView({ block: "nearest" });
      });
    });
    const renderLocalFilters = (patch) => {
      state.data.coreLogFilters = {
        ...(state.data.coreLogFilters || {}),
        ...patch,
      };
      rememberSelection();
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
        limit: Number(event.currentTarget.value || 1000),
      };
      rememberSelection();
      await coreLogs({ scopeChange: true });
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
        rememberSelection();
        await coreLogs({ scopeChange: true });
      });
    });
    bindEvent(document.querySelector("[data-toggle-core-log-refresh]"), "click", (event) => {
      const enabled = state.data.coreLogAutoRefresh === false;
      state.data.coreLogAutoRefresh = enabled;
      rememberSelection();
      event.currentTarget.setAttribute("aria-checked", String(enabled));
      const label = document.querySelector("[data-core-log-refresh-label]");
      if (label && (!state.data.coreLogPhase || state.data.coreLogPhase === "ready"))
        label.textContent = enabled ? "正在实时更新" : "自动更新已暂停";
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

  async function coreLogs({ background = false, scopeChange = false } = {}) {
    restoreSelection();
    poller.stop();
    const { filters, params } = query();
    const data = state.data;
    const epoch = state.navigationEpoch;
    const key = JSON.stringify([filters.agent_id || "", Number(filters.limit || 1000)]);
    const cache = data.coreLogCache ||= createCoreLogCache({ now });
    const cached = cache.get(key);
    const changed = data.coreLogDataScope !== key;
    let rendered = false;
    let previewed = false;
    let agents = can("agents.read") ? (data.agents || []).filter((agent) => agent.can_manage !== false) : [];
    const current = (signal) => !signal.aborted && state.data === data &&
      state.navigationEpoch === epoch && state.route === "core-logs";
    const paint = (entries, phase) => {
      data.coreLogPhase = phase;
      renderCoreLogs(entries, agents, state.data.coreLogFilters || filters);
      rendered = true;
    };
    // The selected sidebar item/header must change before waiting on the WAN.
    // Never show the previous node's rows under the new node's name.
    if (!background && changed && (scopeChange || filters.agent_id || cached))
      paint(cached || [], cached ? "cached" : "loading");
    try {
      const applied = await refresh.run(
        async (signal) => {
          // Node metadata is not a dependency of log delivery. Reuse it for
          // quick sidebar switches; polling still revalidates live status.
          if (can("agents.read") && (!scopeChange || !data.coreLogAgentsAt || now() - data.coreLogAgentsAt >= 10_000)) {
            void api("/agents", { signal }).then((freshAgents) => {
              if (!current(signal)) return;
              agents = freshAgents.filter((agent) => agent.can_manage !== false);
              data.agents = agents;
              data.coreLogAgentsAt = now();
              if (rendered) paint(data.coreLogEntries || [], data.coreLogPhase);
            }).catch(() => {
              // A failed metadata read must not discard successfully read logs.
              if (!current(signal)) return;
              agents = agents.map((agent) => ({ ...agent, status: "unknown" }));
              data.coreLogAgentsAt = 0;
              if (rendered) paint(data.coreLogEntries || [], data.coreLogPhase);
            });
          }
          if (changed && filters.agent_id && !cached && Number(filters.limit || 1000) > 200) {
            const previewParams = new URLSearchParams(params);
            previewParams.set("limit", "200");
            const entries = await api(`/core-logs?${previewParams}`, { signal });
            if (!current(signal)) return entries;
            // If no engine hit its cap, this is already the complete window.
            const counts = coreLogFilterCounts(entries, engines);
            if (Object.values(counts.engine).every((count) => count < 200)) return entries;
            previewed = true;
            paint(entries, "preview");
          }
          return api(`/core-logs?${params}`, { signal });
        },
        (entries) => {
          if (state.data !== data || state.navigationEpoch !== epoch || state.route !== "core-logs") return;
          cache.set(key, entries);
          paint(entries, "ready");
        },
      );
      if (applied && state.data === data && state.data.coreLogAutoRefresh !== false) poller.start();
      return applied;
    } catch (error) {
      if (state.data !== data || state.route !== "core-logs" || state.navigationEpoch !== epoch) return false;
      if ([403, 404].includes(error.status) && filters.agent_id) {
        // A saved selection can outlive ownership/access. Return to the
        // account's permitted logs instead of trapping the page on that ID.
        refresh.invalidate();
        delete data.coreLogCache;
        data.coreLogEntries = [];
        data.coreLogs = [];
        data.coreLogFilters = { ...data.coreLogFilters, agent_id: "" };
        rememberSelection();
        return coreLogs({ scopeChange: true });
      }
      const permissionDenied = error.status === 403;
      const incomplete = previewed || ["preview", "incomplete"].includes(data.coreLogPhase);
      if (permissionDenied) {
        refresh.invalidate();
        delete data.coreLogCache;
        data.coreLogEntries = [];
        data.coreLogs = [];
      }
      if (!permissionDenied && (rendered || !changed))
        paint(data.coreLogEntries || [], incomplete ? "incomplete" : "failed");
      const status = document.querySelector("[data-core-log-refresh-status]");
      if (permissionDenied || (!status && !background && !rendered)) {
        shell(
          `<div class="core-log-workspace" data-core-log-page><div class="core-log-empty"><strong>${permissionDenied ? "无权查看内核日志" : "内核日志加载失败"}</strong><span>${permissionDenied ? "当前账号缺少内核日志查看权限。" : "无法读取日志数据，请稍后重试。"}</span></div></div>`,
          "内核日志",
        );
      }
      if (status) {
        status.dataset.refreshError = "1";
        status.title = error.message;
        const label = status.querySelector("[data-core-log-refresh-label]");
        if (label) label.textContent = incomplete ? "补齐失败，当前仅显示部分日志" : "刷新失败，保留上次数据";
      }
      if (state.data.coreLogAutoRefresh !== false) poller.start();
      return false;
    }
  }

  return coreLogs;
}
