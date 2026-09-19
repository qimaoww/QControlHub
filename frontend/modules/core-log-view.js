import { coreLogFilterLimits } from "./core-log-preferences.js";
import { filterCoreLogEntries, coreLogFilterCounts, levelName, storagePolicyName } from "./core-log-model.js";
import { coreLogSourceStatus } from "./core-log-source-status.js";
export function createCoreLogView({ state, engines, can, esc, engineName, date, shell }) {
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
    let { emptyTitle, emptyDetail, sourceNoticeTitle, sourceNoticeDetail } = coreLogSourceStatus(selectedAgent, filters, can);
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
      emptyDetail = "";
    }
    if (phase === "failed" && !entries.length) {
      emptyTitle = "日志加载失败";
      emptyDetail = "请稍后重试。";
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
    const retentionDays = Number(state.data.settings?.core_log_retention_days);
    const storagePolicy = can("settings.read")
      ? [
          storagePolicyName(state.data.settings?.core_log_minimum_level),
          Number.isInteger(retentionDays) && retentionDays > 0 ? `保留 ${retentionDays} 天` : "",
        ].filter(Boolean).join(" · ")
      : "保存策略不可见";
    shell(
      `<div class="core-log-workspace" data-core-log-page><header class="core-log-header"><div><h2>内核日志</h2><p>当前范围：<strong>${esc(scopeName)}</strong></p></div><label class="core-log-auto"><button type="button" role="switch" aria-checked="${String(autoRefresh)}" data-toggle-core-log-refresh><i></i></button><span>自动更新</span></label></header><section class="core-log-filters" id="core-log-filters" aria-label="日志筛选"><div class="core-log-filter-group core-log-engine-filter"><span>内核</span><div role="group" aria-label="日志内核">${engineButtons}</div></div><div class="core-log-filter-group core-log-level-filter"><span>级别</span><div role="group" aria-label="日志级别">${levelButtons}</div></div><label class="core-log-search">关键词<input name="q" type="search" maxlength="120" value="${esc(filters.q || "")}" placeholder="搜索日志内容" autocomplete="off"></label><label class="core-log-limit" title="每种内核分别取最新日志，不共用总条数上限">每内核上限<select name="limit">${coreLogFilterLimits.map((limit) => `<option value="${limit}" ${Number(filters.limit || 1000) === limit ? "selected" : ""}>${limit} 条</option>`).join("")}</select></label><button class="button core-log-reset" type="button" data-reset-core-logs>清除筛选</button></section><div class="core-log-status" role="status" data-core-log-refresh-status><span>显示 <strong>${entries.length}</strong> 条结果 · 已加载 ${sourceEntries.length} 条${entries.length > pageSize ? ` · 每页 ${pageSize} 条` : ""}</span><span><span class="core-log-live"><i></i><span data-core-log-refresh-label>${refreshLabel}</span></span><span>${esc(storagePolicy)}</span></span></div><div class="core-log-result-toolbar"><span>${entries.length ? `当前 ${page * pageSize + 1}–${Math.min((page + 1) * pageSize, entries.length)} 条` : ""}</span>${pagination}</div><section class="core-log-stream qch-swap-panel" aria-label="内核运行日志" data-refresh-scroll data-refresh-key="core-log-results-${esc(filters.agent_id || "all")}-${esc(filters.engine || "all")}-${esc(filters.level || "all")}"><header class="core-log-columns" aria-hidden="true"><span>时间</span><span>内核</span><span>级别</span><span>节点</span><span>日志内容</span></header>${sourceNotice}${rows || `<div class="core-log-empty"><strong>${esc(emptyTitle)}</strong>${emptyDetail ? `<span>${esc(emptyDetail)}</span>` : ""}</div>`}</section>${pagination ? `<footer class="core-log-result-footer">${pagination}</footer>` : ""}</div>`,
      "内核日志",
    );

  };
  return renderCoreLogs;
}
