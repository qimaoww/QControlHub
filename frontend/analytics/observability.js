import { sourceStatus } from "./source-status.js";
import { redactDiagnostic } from "../modules/errors.js";
import { accountStorage } from "../modules/account-storage.js";
import { savedNodeOrder, orderNodesBySavedOrder } from '../modules/node-order.js';
import { coreLogFilterLimits } from "../modules/core-log-preferences.js";
import { createCoreLogSelection } from '../modules/core-log-selection.js';
import { coreLogScopeKey, normalizeCoreLogScope, coreLogSourceAgents } from "../modules/core-log-scope.js";
import { readCoreLogWindow } from "../modules/core-log-read.js";
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
  const selection = createCoreLogSelection({ state: ctx.state, engines, storage: accountStorage });
  selection.restoreSelection();
  const target = new URLSearchParams(location.hash.split('?')[1] || '');
  if (/^agt_[0-9a-f]{16}$/.test(target.get('agent') || '') && /^shr_[0-9a-f]{16}$/.test(target.get('instance') || '')) {
    data.coreLogFilters = { ...data.coreLogFilters, agent_id: target.get('agent'), instance_id: target.get('instance'),
      engine: engines.includes(target.get('engine')) ? target.get('engine') : '' };
  }
  let filters = { limit: 1000, ...data.coreLogFilters }, automatic = data.coreLogAutoRefresh !== false;
  let entries = [], sources = [], page = 0, anchor = null, serial = 0, controller, pendingRead, failures = 0, incomplete = false, readState = "loading";
  let signature = data.analyticsLogAccessSignature || "", scope = "", queryFrame, cache = data.analyticsLogCache ||= createCoreLogCache();
  let nodeOrder = savedNodeOrder();
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
  const toggle = button(automatic ? "暂停更新" : "自动更新", () => {
    automatic = !automatic; remember(); toggle.textContent = automatic ? "暂停更新" : "自动更新";
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
      changed();
    }), toggle, button("刷新", () => { if (automatic) void poller.trigger(); else void load(); })));
  controls.prepend(node("fieldset", { className: "a-form-fields a-stack" }, controls.firstElementChild));
  function remember() {
    if (!ctx.current()) return;
    data.coreLogFilters = { ...filters }; data.coreLogAutoRefresh = automatic;
    selection.rememberSelection();
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
    collection.update(relevant.map(source => {
      const state = coreLogSourceState(source);
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
        badge(`内核 · ${engine}`), node("p", { className: "a-muted" }, source ? instanceLabel(source) : record.instance_id || "主实例"),
        badge(levelName(record.level), ["error", "critical"].includes(record.level) ? "bad" : record.level === "warning" ? "warn" : ""),
        node("time", { className: "a-muted", datetime: record.logged_at }, date(record.logged_at)));
      return [heading,
        node("pre", { className: "a-code a-log-message", tabindex: "0", "aria-label": `${source?.name || record.agent_id} · ${engine} · 日志正文` }, redactDiagnostic(record.message))];
    }, !entries.length && readState === "loading" ? "正在读取日志…" : !entries.length && readState === "failed" ? "日志读取失败" :
      entries.length ? "没有匹配日志" : !relevant.length ? "没有获授权的日志实例" : collectionState.emptyTitle || "暂无匹配日志");
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
  async function readLogs() {
    controller?.abort(); controller = new AbortController();
    const signal = controller.signal, revision = ++serial;
    const current = () => ctx.current() && !signal.aborted && serial === revision;
    status.setAttribute("aria-busy", "true");
    readState = "loading";
    status.textContent = entries.length ? incomplete ? "已显示部分日志，正在补齐…" : "正在刷新日志…" : "正在读取日志…";
    draw();
    try {
      // Ordering is presentation metadata. Its latency or failure must never
      // gate an authorized log read, including on first entry and retry.
      void ctx.api('/preferences/node-order', { signal }).then(order => {
        if (current() && Array.isArray(order?.ids)) { nodeOrder = order.ids; sourceOptions(); }
      }).catch(() => {});
      const receivedSources = await ctx.api("/core-log-sources", { signal });
      if (!current()) return;
      const access = JSON.stringify(receivedSources.map(source => [source.agent_id, source.instance_id, source.engine, source.owner_id, source.grant_revision]));
      if (access !== signature) { cache = data.analyticsLogCache = createCoreLogCache(); entries = []; incomplete = false; draw(); }
      data.analyticsLogAccessSignature = signature = access; sources = receivedSources;
      filters = normalizeCoreLogScope(filters, sources); remember(); sourceOptions();
      const key = coreLogScopeKey(filters), changedScope = scope !== key;
      if (changedScope) {
        const cached = cache.preview(key);
        scope = key; entries = cached?.entries || []; incomplete = cached ? !cached.complete : false; page = 0; anchor = null; draw();
        if (entries.length) status.textContent = incomplete ? "已显示部分日志，正在补齐…" : "已显示缓存，正在刷新…";
      }
      const params = new URLSearchParams({ limit: String(filters.limit) });
      for (const name of ["agent_id", "instance_id", "user_id"]) if (filters[name]) params.set(name, filters[name]);
      const result = await readCoreLogWindow(ctx.api, params, { signal, previous: entries, preview: !entries.length && filters.limit > 200, engines,
        onPreview(value) { if (current()) { entries = value; incomplete = true; cache.set(key, value, { complete: false }); error.textContent = ""; draw(); status.textContent = "已显示最新日志，正在补齐所选窗口…"; } } });
      if (!current()) return;
      entries = result; incomplete = false; readState = "ready"; cache.set(key, result); failures = 0; error.textContent = ""; draw();
      status.textContent = automatic ? "正在实时更新" : "自动更新已暂停";
    } catch (failure) {
      if (!current() || failure.name === "AbortError") return;
      failures++; readState = "failed";
      if ([401, 403, 404].includes(failure.status)) { entries = []; sources = []; incomplete = false; data.analyticsLogAccessSignature = signature = ""; data.analyticsLogCache = cache = createCoreLogCache(); draw(); sourceOptions(); }
      error.textContent = redactDiagnostic(failure.message);
      status.textContent = entries.length ? incomplete ? "补齐失败，当前仅显示部分日志" : "刷新失败，保留上次数据" : "读取失败，请重试";
      draw();
    } finally { if (current()) status.removeAttribute("aria-busy"); }
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
  void load().then(() => { if (ctx.current()) poller.start(); });
  return stack(filterbar(controls), error,
    section("日志记录", stack(node("div", { className: "a-card-body" }, actions(status, count)), view.root), actions(sourceHeading, pageTools)));
}
