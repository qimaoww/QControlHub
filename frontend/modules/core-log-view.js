import { coreLogScopeKey } from "./core-log-scope.js";
import { coreLogFilterLimits } from "./core-log-preferences.js";
import { redactDiagnostic } from "./errors.js";
import { levelName, storagePolicyName } from "./core-log-model.js";
import { coreLogGuardStatus, coreLogInstanceName, coreLogSourceStatus } from "./core-log-source-status.js";
import { createCoreLogProjection } from "./core-log-projection.js";
import { createCoreLogDOM } from "./core-log-dom.js";
export function createCoreLogView(ctx) {
  const { state, engines, can, esc, engineName, date, now = Date.now } = ctx;
  const project = createCoreLogProjection(engines);
  const render = createCoreLogDOM(ctx);
  const rowMarkup = new WeakMap();
  const renderCoreLogs = (sourceEntries, agents, filters) => {
    const sources = state.data.coreLogSources || [];
    const available = sources.filter(source => !filters.agent_id || source.agent_id === filters.agent_id);
    const instances = [...new Map(available.map(source => [source.instance_id, source])).values()];
    const selectedSources = available.filter(source => !filters.instance_id || source.instance_id === filters.instance_id);
    const allowedEngines = engines.filter(engine => selectedSources.some(source => source.engine === engine));
    const instanceName = coreLogInstanceName;
    const option = (value, label, selected) => `<option value="${esc(value)}" ${value === (selected || "") ? "selected" : ""}>${esc(label)}</option>`;
    const scopeFilters = `<label class="core-log-scope-filter">节点<select name="agent_id" aria-label="日志节点">${option("", "全部授权节点", filters.agent_id)}${agents.map(agent => option(agent.id, agent.name, filters.agent_id)).join("")}</select></label><label class="core-log-scope-filter">使用用户 / 实例<select name="instance_id" aria-label="使用用户 / 实例">${option("", "全部授权实例", filters.instance_id)}${instances.map(source => option(source.instance_id, instanceName(source), filters.instance_id)).join("")}</select></label>`;
    const { entries, counts } = project(sourceEntries, filters);
    state.data.coreLogEntries = sourceEntries;
    state.data.coreLogs = entries;
    state.data.coreLogAgents = agents;
    state.data.coreLogDataScope = coreLogScopeKey(filters);
    const agentsByID = new Map(agents.map((agent) => [agent.id, agent]));
    const selectedAgent = agentsByID.get(filters.agent_id || "");
    const relevant = selectedSources.filter(source => !filters.engine || source.engine === filters.engine);
    const guard = coreLogGuardStatus(selectedAgent, filters, can);
    const scopeStatus = coreLogSourceStatus(relevant, {
      now: now(), engineName, filtered: Boolean(filters.q || filters.level),
    });
    let emptyTitle = "";
    let emptyDetail = "";
    let sourceNoticeTitle = "";
    let sourceNoticeDetail = "";
    // The source directory reports collection per node/engine/instance, so its
    // scoped states speak for the page; agent-level guards only answer what no
    // source row can (missing grants or an unknown node).
    if (!sources.length) {
      emptyTitle = guard.title;
      emptyDetail = guard.detail;
      sourceNoticeTitle = guard.title;
      sourceNoticeDetail = guard.detail;
      if (!guard.title && state.data.coreLogPhase !== "loading") {
        emptyTitle = "没有获授权的日志实例";
        emptyDetail = "当前账号无可访问的节点或实例。";
      }
    } else if (filters.agent_id && !selectedAgent) {
      emptyTitle = guard.title || "节点状态不可用";
      emptyDetail = guard.detail || "无法读取所选节点状态；历史日志仍可查看。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else {
      emptyTitle = scopeStatus.emptyTitle || (filters.agent_id || filters.instance_id || filters.engine ? "当前实例尚未产生日志" : "暂无日志");
      emptyDetail = scopeStatus.emptyDetail;
      sourceNoticeTitle = scopeStatus.noticeTitle;
      sourceNoticeDetail = scopeStatus.noticeDetail;
    }
    const pageSize = 200;
    const pageCount = Math.max(1, Math.ceil(entries.length / pageSize));
    const scope = JSON.stringify([filters.agent_id || "", filters.instance_id || "", filters.engine || "", filters.level || "", filters.q || "", filters.limit || 1000]);
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
        const source = sources.find(source => source.agent_id === entry.agent_id && source.engine === entry.engine && source.instance_id === entry.instance_id);
        const name = `${agent?.name || entry.agent_id} · ${source ? instanceName(source) : "历史来源未归属"}`;
        const cached = rowMarkup.get(entry);
        if (cached?.name === name) return cached.html;
        const html = `<article class="core-log-row level-${esc(entry.level)}" data-refresh-key="core-log-${esc(entry.id)}"><time datetime="${esc(entry.logged_at)}">${esc(date(entry.logged_at))}</time><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="core-log-level">${esc(levelName(entry.level))}</span><span class="core-log-agent" title="${esc(name)}">${esc(name)}</span><pre>${esc(redactDiagnostic(entry.message))}</pre></article>`;
        rowMarkup.set(entry, { name, html });
        return html;
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
      ...allowedEngines.map((engine) => [
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
    // Show the selected scope's transport and newest delivery only when the
    // source reported them; missing fields add nothing.
    const statusMeta = scopeStatus.meta ? ` · ${esc(scopeStatus.meta)}` : "";
    const header = `<header class="core-log-header"><div><h2>内核日志</h2><p>当前范围：<strong>${esc(scopeName)}</strong></p></div><label class="core-log-auto"><button type="button" role="switch" aria-checked="${String(autoRefresh)}" data-toggle-core-log-refresh><i></i></button><span>自动更新</span></label></header>`;
    const filtersMarkup = `<section class="core-log-filters" id="core-log-filters" aria-label="日志筛选">${scopeFilters}<div class="core-log-filter-group core-log-engine-filter"><span>内核</span><div role="group" aria-label="日志内核">${engineButtons}</div></div><div class="core-log-filter-group core-log-level-filter"><span>级别</span><div role="group" aria-label="日志级别">${levelButtons}</div></div><label class="core-log-search">关键词<input name="q" type="search" maxlength="120" value="${esc(filters.q || "")}" placeholder="搜索日志内容" autocomplete="off"></label><label class="core-log-limit" title="每种内核分别取最新日志，不共用总条数上限">每内核上限<select name="limit">${coreLogFilterLimits.map((limit) => `<option value="${limit}" ${Number(filters.limit || 1000) === limit ? "selected" : ""}>${limit} 条</option>`).join("")}</select></label><button class="button core-log-reset" type="button" data-reset-core-logs>清除筛选</button></section>`;
    const status = `<div class="core-log-status" role="status" data-core-log-refresh-status><span>显示 <strong>${entries.length}</strong> 条结果 · 已加载 ${sourceEntries.length} 条${entries.length > pageSize ? ` · 每页 ${pageSize} 条` : ""}${statusMeta}</span><span><span class="core-log-live"><i></i><span data-core-log-refresh-label>${refreshLabel}</span></span><span>${esc(storagePolicy)}</span></span></div>`;
    const toolbar = `<div class="core-log-result-toolbar"><span data-motion-region="core-log-results" data-motion-key="${esc(JSON.stringify([scope, page]))}" data-motion-live data-motion-ready="${!["loading", "preview", "cached"].includes(phase)}">${entries.length ? `当前 ${page * pageSize + 1}–${Math.min((page + 1) * pageSize, entries.length)} 条` : ""}</span>${pagination}</div>`;
    const stream = `<section class="core-log-stream" aria-label="内核运行日志" data-refresh-scroll data-refresh-key="core-log-results" data-motion-key="${esc(JSON.stringify([scope, page]))}"><header class="core-log-columns" aria-hidden="true"><span>时间</span><span>内核</span><span>级别</span><span>节点</span><span>日志内容</span></header>${sourceNotice}${rows || `<div class="core-log-empty"><strong>${esc(emptyTitle)}</strong>${emptyDetail ? `<span>${esc(emptyDetail)}</span>` : ""}</div>`}</section>`;
    const footer = `<footer class="core-log-result-footer"${pagination ? "" : " hidden"}>${pagination}</footer>`;
    render(`<div class="core-log-workspace" data-core-log-page>${header}${filtersMarkup}${status}${toolbar}${stream}${footer}</div>`, [
      [".core-log-header", header], [".core-log-filters", filtersMarkup],
      [".core-log-status", status], [".core-log-result-toolbar", toolbar],
      [".core-log-stream", stream], [".core-log-result-footer", footer],
    ]);

  };
  return renderCoreLogs;
}
