import { bindPreferenceFields } from "./preferences.js";
import { sourceStatus } from "./source-status.js";
import { redactDiagnostic } from "../modules/errors.js";
import { savedNodeOrder, orderNodesBySavedOrder } from '../modules/node-order.js';
import { coreLogFilterLimits } from "../modules/core-log-preferences.js";
import { createCoreLogSelection } from '../modules/core-log-selection.js';
import { coreLogScopeKey, normalizeCoreLogScope, coreLogSourceAgents } from "../modules/core-log-scope.js";
import { readCoreLogWindow, requestCoreLogJSON } from "../modules/core-log-read.js";
import { createCoreLogCache } from "../modules/core-log-cache.js";
import { filterCoreLogEntries, coreLogFilterCounts, levelName, coreLogStoragePolicy } from "../modules/core-log-model.js";
import { coreLogInstanceName, coreLogSourceState, coreLogSourceStatus } from "../modules/core-log-source-status.js";
import { createPoller } from "../modules/poller.js";
import { node, button, actions, stack, section, filterbar, field, muted, notice, formData, badge } from "./ui.js";
import { observationStream } from "./observability-table.js";
import { date } from "./operations.js";
export { connectionsPage } from "./connections.js";
const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const engineName = engine => ({ mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "Shadowsocks Rust" })[engine] || engine;
const instanceLabel = source => source?.username && source.instance_id !== "main" ? `${source.username} · 共享实例` : coreLogInstanceName(source);

export async function logsPage(ctx) {
  const data = ctx.state.data;
  const selection = createCoreLogSelection({ state: ctx.state, engines });
  selection.restoreSelection();

  const target = new URLSearchParams(location.hash.split('?')[1] || '');
  if (/^agt_[0-9a-f]{16}$/.test(target.get('agent') || '') && /^shr_[0-9a-f]{16}$/.test(target.get('instance') || '')) {
    data.coreLogFilters = { ...data.coreLogFilters, agent_id: target.get('agent'), instance_id: target.get('instance'),
      engine: engines.includes(target.get('engine')) ? target.get('engine') : '' };
  }
  let filters = { limit: 1000, ...data.coreLogFilters }, automatic = data.coreLogAutoRefresh !== false;
  let entries = [], sources = [], page = 0, anchor = null, serial = 0, controller, pendingRead, failures = 0, incomplete = false, readState = "loading";
  let signature = data.analyticsLogAccessSignature || "", scope = "", queryFrame, cache = data.analyticsLogCache ||= createCoreLogCache();
  let nodeOrder = savedNodeOrder(), sourceUnavailable = false;
  const status = muted("正在读取日志…"); status.setAttribute("data-log-refresh-label", "");
  const error = notice("");
  const collection = sourceStatus(ctx, { marker: "data-log-sources", lastSeenLabel: "最近日志", lastSeenEmpty: "尚无日志" });
  const sourceHeading = collection.trigger;
  sourceHeading.dataset.logSourceStatus = "";
  const view = observationStream("内核日志");
  const count = muted("");
  const previous = button("上一页", () => { page--; anchor = null; draw(); });
  const next = button("下一页", () => { page++; anchor = null; draw(); });
  const pageTools = actions(previous, next);
  let automaticTouched = false;
  const toggle = button(automatic ? "暂停更新" : "自动更新", () => {
    automaticTouched = true; automatic = !automatic; ctx.preferences?.patch('logs', { automatic }); remember(); toggle.textContent = automatic ? "暂停更新" : "自动更新";
    if (automatic) void poller.trigger(); else poller.stop();
    if (!status.hasAttribute("aria-busy") && !failures) status.textContent = automatic ? "正在实时更新" : "自动更新已暂停";
  });
  const controls = node("form", { className: "a-stack a-filter-form a-auto-filter", "data-log-filters": "" }, node("div", { className: "a-form-grid" },
    field("节点", "agent_id", filters.agent_id, { options: [["", "全部获授权节点"]] }),
    field("使用用户 / 实例", "instance_id", filters.instance_id, { options: [["", "全部授权实例"]] }),
    field("内核", "engine", filters.engine, { options: [["", "全部内核"], ...engines] }),
    field("级别", "level", filters.level, { options: [["", "全部级别"], ["info", "信息"], ["warning", "警告"], ["error", "错误"]] }),
    field("关键词", "q", filters.q, { type: "search", maxlength: 120 }),
    field("每内核上限", "limit", filters.limit, { options: coreLogFilterLimits.map(value => [String(value), `${value} 条`]) })),
    actions(button("清除筛选", () => {
      for (const name of ["engine", "level", "q"]) controls.elements[name].value = "";
      preferenceFields.save({ engine: '', level: '', q: '' }); changed();
    }), toggle, button("刷新", () => { if (automatic) void poller.trigger(); else void load(); })));
  controls.prepend(node("fieldset", { className: "a-form-fields a-stack" }, controls.firstElementChild));
  function remember() {
    if (!ctx.current()) return;
    data.coreLogFilters = { ...filters }; data.coreLogAutoRefresh = automatic;

  }
  function options(name, values) {
    const select = controls.elements[name], value = filters[name] || "";
    const key = JSON.stringify(values);
    if (select.dataset.options !== key) {
      select.replaceChildren(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")), ...values.map(([id, text]) => node("option", { value: id }, text)));
      select.dataset.options = key;
    }
    if (select.value !== value) select.value = value;
  }
  function sourceOptions() {
    options("agent_id", [["", "全部获授权节点"], ...orderNodesBySavedOrder(coreLogSourceAgents(sources), nodeOrder).map(agent => [agent.id, agent.name || agent.id])]);
    const available = sources.filter(source => !filters.agent_id || source.agent_id === filters.agent_id);
    options("instance_id", [["", "全部授权实例"], ...new Map(available.map(source => [source.instance_id, [source.instance_id, instanceLabel(source)]])).values()]);
    options("engine", [["", "全部内核"], ...engines.filter(engine => available.some(source => source.engine === engine && (!filters.instance_id || source.instance_id === filters.instance_id))).map(engine => [engine, engine])]);
    const relevant = available.filter(source => !filters.instance_id || source.instance_id === filters.instance_id).filter(source => !filters.engine || source.engine === filters.engine);
    if (sourceUnavailable) { collection.unavailable(); return; }
    collection.update(relevant.map(source => {
      const state = coreLogSourceState(source);
      if (source.status === "failed" && state.detail) void ctx.recordFailure?.(Object.assign(new Error(state.detail), {agentID:source.agent_id,engine:source.engine,resource:`/core-log-sources/${source.agent_id}/${source.instance_id}/${source.engine}`}), `日志采集 · ${source.name || source.agent_id} · ${source.engine}`, "log_collection");
      const attention = ["failed", "offline", "unsupported", "not-installed", "status-unavailable"].includes(source.status);
      return { agentID: source.agent_id, agentName: source.name || source.agent_id,
        instanceID: source.instance_id, instance: instanceLabel(source), engine: engineName(source.engine),
        state: state.key === "active" ? "采集正常" : state.key === "ready" ? "就绪，等待日志" : state.title,
        tone: source.status === "failed" ? "bad" : attention ? "warn" : state.key === "active" ? "ok" : "",
        attention, transport: state.transport, detail: redactDiagnostic(state.detail), lastSeen: state.lastAt ? date(state.lastAt) : "" };
    }), { scope: [controls.elements.agent_id.selectedOptions[0]?.textContent, controls.elements.instance_id.selectedOptions[0]?.textContent,
      filters.engine ? engineName(filters.engine) : "全部内核"].filter(Boolean).join(" · "),
      policy: coreLogStoragePolicy(data.settings, ctx.can('settings.read')) });
  }

  function draw() {
    const counts = coreLogFilterCounts(entries, engines);
    for (const option of controls.elements.engine.options) {
      const label = option.value ? `${engineName(option.value)} · ${counts.engine[option.value] || 0}` : `全部内核 · ${counts.total}`;
      if (option.textContent !== label) option.textContent = label;
    }
    const visible = filterCoreLogEntries(entries, filters);
    if (page && anchor) { const position = visible.findIndex(record => record.id === anchor); if (position >= 0) page = Math.floor(position / 200); }
    page = Math.max(0, Math.min(page, Math.ceil(visible.length / 200) - 1));
    let start = page * 200;
    if (page && anchor) { const position = visible.findIndex(record => record.id === anchor); if (position >= 0) start = position; }
    const records = visible.slice(start, start + 200);
    anchor = page ? records[0]?.id : null;
    const relevant = sources.filter(source => (!filters.agent_id || source.agent_id === filters.agent_id) &&
      (!filters.instance_id || source.instance_id === filters.instance_id) && (!filters.engine || source.engine === filters.engine));
    const collectionState = coreLogSourceStatus(relevant, { engineName, filtered: Boolean(filters.q || filters.level) });
    view.update(records, record => {
      const source = sources.find(source => source.agent_id === record.agent_id && source.engine === record.engine && source.instance_id === record.instance_id);
      const engine = engineName(record.engine);
      const heading = actions(
        node("p", {}, node("strong", {}, source?.name || record.agent_id)),
        badge(`内核 · ${engine}`), node("p", { className: "a-muted" }, source ? instanceLabel(source) : coreLogInstanceName(record)),
        badge(levelName(record.level), ["error", "critical"].includes(record.level) ? "bad" : record.level === "warning" ? "warn" : ""),
        node("time", { className: "a-muted", datetime: record.logged_at }, date(record.logged_at)));
      return [heading,
        node("pre", { className: "a-code a-log-message", tabindex: "0", "aria-label": `${source?.name || record.agent_id} · ${engine} · 日志正文` }, redactDiagnostic(record.message))];
    }, !entries.length && readState === "loading" ? "正在读取日志…" : !entries.length && readState === "failed" ? "日志读取失败" :
      entries.length ? "没有匹配日志" : sourceUnavailable ? "暂无日志" : !relevant.length ? "没有获授权的日志实例" : collectionState.emptyTitle || "暂无匹配日志");
    count.textContent = `${visible.length} 条日志${visible.length !== entries.length ? ` · 已加载 ${entries.length} 条` : ""}${visible.length > 200 ? ` · 第 ${page + 1} / ${Math.ceil(visible.length / 200)} 页` : ""} · 信息 ${counts.level.info} · 警告 ${counts.level.warning} · 错误 ${counts.level.error}`;
    previous.disabled = page === 0; next.disabled = start + 200 >= visible.length; pageTools.hidden = visible.length <= 200;
  }
  function load() {
    // Initial, manual and scheduled refreshes share the current read. A target
    // change explicitly releases it below before starting the replacement.
    if (pendingRead) return pendingRead;
    const read = readLogs().finally(() => { if (pendingRead === read) pendingRead = null; });
    pendingRead = read;
    return read;
  }
  function showReadFailure(failure) {
    failures++; readState = "failed";
    if ([401, 403, 404].includes(failure.status)) { entries = []; sources = []; incomplete = false; data.analyticsLogAccessSignature = signature = ""; data.analyticsLogCache = cache = createCoreLogCache(); sourceOptions(); }
    error.textContent = redactDiagnostic(failure.message);
    status.textContent = entries.length ? incomplete ? "补齐失败，当前仅显示部分日志" : "刷新失败，保留上次数据" : "读取失败，请重试";
    draw();
  }
  async function readLogs() {
    controller?.abort(); controller = new AbortController();
    const signal = controller.signal, revision = ++serial;
    const current = () => ctx.current() && !signal.aborted && serial === revision;
    status.setAttribute("aria-busy", "true");
    readState = "loading";
    error.textContent = "";
    status.textContent = entries.length ? incomplete ? "已显示部分日志，正在补齐…" : "正在刷新日志…" : "正在读取日志…";
    draw();
    const directoryController = new AbortController();
    const abortDirectory = () => directoryController.abort();
    signal.addEventListener("abort", abortDirectory, { once: true });
    let windowComplete = false;
    try {
      // Ordering is presentation metadata. Its latency or failure must never
      // gate an authorized log read, including on first entry and retry.
      void ctx.api('/preferences/node-order', { signal }).then(order => {
        if (current() && Array.isArray(order?.ids)) { nodeOrder = order.ids; sourceOptions(); }
      }).catch(() => {});
      const key = coreLogScopeKey(filters), candidate = cache.preview(key);
      let fresh = false, directoryReady = false, sourceFailure, denied, verifyPreview;
      const previewReady = !entries.length && candidate?.entries.length && filters.limit > 200
        ? new Promise(resolve => { verifyPreview = resolve; }) : null;
      const windowController = new AbortController();
      const abortWindow = () => windowController.abort();
      signal.addEventListener("abort", abortWindow, { once: true });
      let grants;
      const identity = record => JSON.stringify([record.agent_id, record.engine, record.instance_id || "main"]);
      const authorized = value => {
        if (!directoryReady) return value;
        const allowed = record => grants.has(identity(record)) && (!record.owner_id || grants.get(identity(record)) === record.owner_id);
        return value.every(allowed) ? value : value.filter(allowed);
      };
      const display = (value, partial) => {
        if (!current() || denied || coreLogScopeKey(filters) !== key) return;
        fresh = true; scope = key; entries = authorized(value); incomplete = partial; readState = partial ? "loading" : "ready";
        error.textContent = ""; draw();
        status.textContent = partial ? "已显示最新日志，正在补齐所选窗口…" : directoryReady
          ? automatic ? "正在实时更新" : "自动更新已暂停" : "日志已加载，正在读取来源状态…";
      };
      const readWindow = () => {
        const params = new URLSearchParams({ limit: String(filters.limit) });
        for (const name of ["agent_id", "instance_id", "user_id"]) if (filters[name]) params.set(name, filters[name]);
        return readCoreLogWindow(ctx.api, params, { signal: windowController.signal, previous: entries,
          preview: !entries.length && !candidate?.entries.length && filters.limit > 200, previewReady, engines,
          onPreview(value) {
            if (entries.length >= value.length && entries.length) return;
            display(value, true);
            if (current() && directoryReady && !denied) cache.set(key, entries, { complete: false });
          } });
      };
      // Network windows carry their own live server authorization. Only reuse
      // memory snapshots after the directory verifies their current grants.
      const windowRead = readWindow().then(value => { display(value, false); return value; }, failure => {
        if (current() && [401, 403, 404].includes(failure.status)) {
          entries = []; incomplete = false; cache = data.analyticsLogCache = createCoreLogCache(); draw();
        }
        throw failure;
      });
      // The directory verifies memory snapshots and supplies presentation
      // metadata. A network window has independent server authorization and
      // must be able to finish while this bounded request is still pending.
      void requestCoreLogJSON(ctx.api, "/core-log-sources", { signal: directoryController.signal, timeout: 8000 }).then(receivedSources => {
        if (!current() || directoryController.signal.aborted) return;
        const access = JSON.stringify(receivedSources.map(source => [source.agent_id, source.instance_id, source.engine, source.owner_id, source.grant_revision]));
        if (access !== signature) {
          cache = data.analyticsLogCache = createCoreLogCache();
          if (!fresh) { entries = []; incomplete = false; }
        }
        data.analyticsLogAccessSignature = signature = access; sources = receivedSources;
        sourceUnavailable = false; grants = new Map(sources.map(source => [identity(source), source.owner_id])); directoryReady = true;
        if (fresh) entries = authorized(entries);
        const normalized = normalizeCoreLogScope(filters, sources);
        const cleared = Object.fromEntries(['agent_id', 'instance_id', 'engine'].filter(name => normalized[name] !== filters[name]).map(name => [name, normalized[name]]));
        filters = normalized; remember(); sourceOptions();
        if (Object.keys(cleared).length) ctx.preferences?.patch('logs', cleared);
        if (coreLogScopeKey(filters) !== key) {
          entries = []; incomplete = false; readState = "loading"; abortWindow();
          if (windowComplete) void Promise.resolve(pendingRead).then(() => { if (current()) void load(); });
        }
        else if (!fresh && scope !== key) {
          const cached = cache.preview(key);
          scope = key; entries = cached?.entries || []; incomplete = cached ? !cached.complete : false; page = 0; anchor = null;
          if (entries.length) status.textContent = incomplete ? "已显示部分日志，正在补齐…" : "已显示缓存，正在刷新…";
        }
        if (windowComplete && coreLogScopeKey(filters) === key) {
          cache.set(key, entries);
          status.textContent = automatic ? "正在实时更新" : "自动更新已暂停";
        }
        draw();
      }).catch(failure => {
        if (!current() || directoryController.signal.aborted) return;
        sourceFailure = failure; sourceUnavailable = true;
        if ([401, 403, 404].includes(failure.status)) {
          denied = failure; abortWindow();
          if (windowComplete) showReadFailure(failure);
        } else {
          collection.unavailable();
          if (windowComplete) {
            failures++; error.textContent = `来源状态读取失败，请刷新重试。${failure.message}`;
            status.textContent = automatic ? "正在实时更新" : "自动更新已暂停";
          }
        }
      }).finally(() => {
        signal.removeEventListener("abort", abortDirectory);
        verifyPreview?.(!entries.length);
      });
      let result;
      try {
        const window = await windowRead.then(value => ({ status: "fulfilled", value }), reason => ({ status: "rejected", reason }));
        if (!current()) return;
        if (denied) throw denied;
        if (coreLogScopeKey(filters) !== key) {
          // A deleted/revoked saved target normalizes to the remaining grants.
          // Never accept the old target's late response or its validator.
          scope = ""; entries = []; incomplete = false;
          const params = new URLSearchParams({ limit: String(filters.limit) });
          for (const name of ["agent_id", "instance_id", "user_id"]) if (filters[name]) params.set(name, filters[name]);
          result = await readCoreLogWindow(ctx.api, params, { signal, preview: filters.limit > 200, engines,
            onPreview(value) { if (current()) { entries = authorized(value); incomplete = true; cache.set(coreLogScopeKey(filters), entries, { complete: false }); error.textContent = ""; draw(); status.textContent = "已显示最新日志，正在补齐所选窗口…"; } } });
        } else {
          if (window.status === "rejected") {
            if (fresh && incomplete && directoryReady) cache.set(key, entries, { complete: false });
            throw window.reason;
          }
          result = window.value;
        }
      } finally {
        signal.removeEventListener("abort", abortWindow);
        abortWindow();
      }
      if (!current()) return;
      windowComplete = true;
      // A directory that finished after the network window may reflect a grant
      // revoked during that read. Do not retain its protected rows in memory.
      result = authorized(result);
      entries = result; incomplete = false; readState = "ready";
      scope = coreLogScopeKey(filters);
      if (directoryReady) cache.set(scope, result);
      failures = sourceFailure ? failures + 1 : 0;
      error.textContent = sourceFailure ? `来源状态读取失败，请刷新重试。${sourceFailure.message}` : ""; draw();
      status.textContent = directoryReady || sourceFailure ? automatic ? "正在实时更新" : "自动更新已暂停" : "日志已加载，正在读取来源状态…";
    } catch (failure) {
      if (!current() || failure.name === "AbortError") return;
      showReadFailure(failure);
    } finally {
      if (!windowComplete) abortDirectory();
      if (current()) status.removeAttribute("aria-busy");
    }
  }
  const poller = createPoller({ run: load, isActive: () => ctx.current() && automatic && !document.hidden, delay: () => Math.min(60000, 10000 * 2 ** failures) });
  ctx.onRefresh(() => automatic ? poller.trigger() : load(), { local: true });
  function changed() {
    const before = coreLogScopeKey(filters);
    filters = { ...formData(controls), limit: Number(controls.elements.limit.value) };
    page = 0; anchor = null; remember();
    if (before !== coreLogScopeKey(filters)) { controller?.abort(); serial++; pendingRead = null; scope = ""; entries = []; incomplete = false; readState = "loading"; draw(); void load(); }
    else { sourceOptions(); draw(); }
  }
  controls.addEventListener("submit", event => { event.preventDefault(); changed(); });
  controls.addEventListener("change", changed);
  controls.elements.q.addEventListener("input", event => {
    cancelAnimationFrame(queryFrame);
    if (!event.isComposing) queryFrame = requestAnimationFrame(changed);
  });
  controls.elements.q.addEventListener("compositionstart", () => cancelAnimationFrame(queryFrame));
  controls.elements.q.addEventListener("compositionend", () => { cancelAnimationFrame(queryFrame); changed(); });
  document.addEventListener("visibilitychange", () => { if (document.hidden) poller.stop(); else if (automatic) void poller.trigger(); }, { signal: ctx.signal });
  ctx.signal.addEventListener("abort", () => { cancelAnimationFrame(queryFrame); controller?.abort(); poller.stop(); }, { once: true });
  const preferenceFields = bindPreferenceFields(ctx, 'logs', [controls], {
    explicit: target.has('agent') ? ['agent_id', 'instance_id', 'engine'] : [],
    restore: () => changed(),
  });
  void ctx.preferences?.ready().then(() => {
    if (!ctx.current() || automaticTouched) return;
    const saved = ctx.preferences.get('logs');
    if (Object.hasOwn(saved, 'automatic')) {
      automatic = saved.automatic; remember(); toggle.textContent = automatic ? '暂停更新' : '自动更新';
      if (automatic) poller.start(); else poller.stop();
    }
  });
  void load().then(() => { if (ctx.current()) poller.start(); });
  return stack(filterbar(controls), error,
    section("日志记录", stack(node("div", { className: "a-card-body" }, actions(status, count)), view.root), actions(sourceHeading, pageTools)));
}
