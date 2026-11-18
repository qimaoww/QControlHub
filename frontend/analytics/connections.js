import { allEngines } from "../modules/runtime-defaults.js";
import { bindPreferenceFields } from "./preferences.js";
import { createConnectionCache, sameConnectionValue } from "../modules/client-connection-cache.js";
import { requestCoreLogJSON as boundedRead } from "../modules/core-log-read.js";
import { createPoller } from "../modules/poller.js";
import { defaultConnectionFilters, connectionQuery, connectionLocationLabel, connectionSourceLabel, connectionSourceDetail } from "../modules/client-connection-model.js";
import { savedNodeOrder, orderNodesBySavedOrder } from "../modules/node-order.js";
import { geoRegionDetails } from "../modules/region-model.js";
import { redactDiagnostic } from "../modules/errors.js";
import { node, button, actions, stack, section, filterbar, field, badge, muted, notice, formData } from "./ui.js";
import { observationTable } from "./observability-table.js";
import { date } from "./operations.js";
import { sourceStatus } from "./source-status.js";
import { nodeName, nodeRegionFlag, regionFlag } from "./node-region.js";

const connectionPollDelay = 10_000;
const groupKeys = new WeakMap();
const connectionKey = record => {
  let key = groupKeys.get(record);
  if (!key) { key = JSON.stringify([record.agent_id, record.engine, record.client_ip]); groupKeys.set(record, key); }
  return key;
};
export function mergeConnectionUpdate(previous, update) {
  if (!update.delta) return update;
  if (!previous || !Array.isArray(update.record_keys)) throw new Error("结果需要重新读取，请重试。");
  const records = new Map(previous.records.map(record => [connectionKey(record), record]));
  for (const record of update.records) records.set(connectionKey(record), record);
  const next = update.record_keys.map(key => records.get(key));
  if (next.some(record => !record)) throw new Error("结果需要重新读取，请重试。");
  return {...update, delta: false, records: next};
}

const engineName = value => ({ mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "Shadowsocks Rust" })[value] || value;

export async function connectionsPage(ctx) {
  const data = ctx.state.data;
  const cache = data.analyticsConnectionCache ||= createConnectionCache();
  let scopeReady = false;
  const scopePending = ctx.connectionScope || boundedRead(ctx.api, "/client-connections?scope_only=true", { signal: ctx.signal });
  const scopeRestore = scopePending.then(verified => cache.restore(verified.scope, () => ctx.current() && !scopeReady));
  void scopeRestore.catch(() => {});
  let filters = data.analyticsConnectionFilters ||= defaultConnectionFilters();
  let initializing = true, queryTouched = false;
  let initialOrder = ctx.connectionNodeOrder, nodeOrderReady = false;
  const initialNodeOrder = initialOrder || boundedRead(ctx.api, "/preferences/node-order", { signal: ctx.signal });
  void initialNodeOrder.catch(() => {});
  const absentLocations = new Map();
  // Filter changes can share the route's pending order read. Once settled,
  // later refreshes continue to read the current authenticated preference.

  let sources = [], result = null, loading = true, reading = false, failures = 0, forcePending = false, controller, serial = 0, cursor = "", cursors = [], nextCursor = "", nodeOrder = savedNodeOrder();
  const status = muted("正在读取…"), error = notice(""), summary = node("div", { className: "a-card-body a-actions" });
  const collection = sourceStatus(ctx, { marker: "data-connection-sources" });
  collection.trigger.dataset.connectionSourceStatus = "";
  const view = observationTable(["客户端来源 IP", "国家 / 地区", "节点", "内核", "入站端口", "观测时间"], "客户端来源 IP 明细");
  const previous = button("上一页", () => { const target = cursors.at(-1); if (target !== undefined) void load({ target, history: cursors.slice(0, -1) }); });
  const next = button("下一页", () => { if (nextCursor) void load({ target: nextCursor, history: [...cursors, cursor] }); });
  const page = muted("");
  const controls = node("form", { className: "a-stack a-filter-form" }, node("div", { className: "a-form-grid" },
    field("节点", "agent_id", filters.agent_id, { options: [["", "全部节点"], ...(filters.agent_id ? [[filters.agent_id, "正在读取…"]] : [])] }),
    field("日期", "date", filters.date, { type: "date", required: true }),
    field("查询范围", "period", filters.period, { options: [["day", "当天"], ["month", "所在月份"]] }),
    field("客户端来源 IP", "client_ip", filters.client_ip, { type: "search" }),
    field("国家/地区", "country_code", filters.country_code, { options: [["", "全部国家/地区"], ...(filters.country_code ? [[filters.country_code, geoRegionDetails(filters.country_code)?.name || filters.country_code]] : [])] }),
    field("内核", "engine", filters.engine, { options: [["", "全部内核"], ...allEngines.map(value => [value, engineName(value)])] })),
    field("包含非公网地址", "include_non_public", filters.include_non_public === "true", { type: "checkbox" }),
    actions(button("今日", () => {
      const recent = defaultConnectionFilters();
      controls.elements.date.value = recent.date; controls.elements.period.value = "day"; controls.elements.include_non_public.checked = false; preferenceFields.save({ time_mode: "today", date: recent.date, period: "day", include_non_public: false }); changed();
    }), button("本月", () => { controls.elements.date.value = defaultConnectionFilters().date; controls.elements.period.value = "month"; preferenceFields.save({ time_mode: "month", date: controls.elements.date.value, period: "month" }); changed(); }),
    button("清除筛选", () => {
      for (const name of ["agent_id", "engine", "client_ip", "country_code"]) controls.elements[name].value = "";
      controls.elements.include_non_public.checked = false; preferenceFields.save({ agent_id: "", engine: "", client_ip: "", country_code: "", include_non_public: false }); changed();
    }), button("刷新", () => { void ctx.preferences?.flush(); void ctx.preferences?.ready(); void load({ target: cursor, history: cursors, force: true }); })));
  controls.prepend(node("fieldset", { className: "a-form-fields a-stack" }, controls.firstElementChild, controls.querySelector(".a-check")));
  function updateRegions() {
    const select = controls.elements.country_code, value = filters.country_code || "";
    // A pending/failed query has no new country list yet. Keep its selected
    // filter available until an authoritative response can replace it.
    const regions = ([...new Set([...(result?.country_codes || []), ...(value ? [value] : [])])]).map(geoRegionDetails).filter(Boolean).sort((a, b) => a.name.localeCompare(b.name, "zh-CN"));
    const signature = JSON.stringify(regions.map(region => region.code));
    if (select.dataset.options !== signature) {
      select.replaceChildren(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")), node("option", { value: "" }, "全部国家/地区"),
        ...regions.map(region => node("option", { value: region.code }, node("span", { className: "a-probe-name" }, regionFlag(region, true), node("span", {}, region.name)))));
      select.dataset.options = signature;
    }
    select.value = regions.some(region => region.code === value) ? value : "";
    select.title = geoRegionDetails(select.value)?.name || "全部国家/地区";
  }
  function updateSources() {
    const select = controls.elements.agent_id, value = filters.agent_id || "";
    const ordered = orderNodesBySavedOrder(sources.map(source => ({ ...source, id: source.agent_id })), nodeOrder);
    const available = new Set(ordered.map(source => source.agent_id));
    const options = ordered.map(source => [source.agent_id, source.agent_name || source.agent_id]);
    if (value && !ordered.some(source => source.agent_id === value)) options.push([value, "所选节点不可用"]);
    const signature = JSON.stringify(options);
    if (select.dataset.options !== signature) {
      select.replaceChildren(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")), node("option", { value: "" }, "全部节点"), ...options.map(([id, name]) => node("option", { value: id }, available.has(id) ? nodeName(ctx, { id, name }) : name)));
      select.dataset.options = signature;
    }
    select.value = value;
  }
  function paint() {
    updateRegions();
    const records = result?.records || [], reportSources = result?.sources || [];
    const warnings = reportSources.filter(source => source.status !== "ok" || source.truncated || !source.updated_at);
    const totals = result?.preview ? "结果待更新" : `独立 IP ${result?.ips ?? "—"} · 观测连接 ${result?.flows ?? "—"} · 采集节点 ${result ? reportSources.length : "—"} · 需关注 ${result ? warnings.length : "—"}`;
    if (summary.textContent !== totals) summary.replaceChildren(...totals.split(" · ").map((total, index) => node("span", {}, `${index ? " · " : ""}${total}`)));
    status.hidden = !result;
    view.root.setAttribute("aria-busy", String(loading));
    collection.trigger.disabled = !result;
    view.update(records, record => {
      const ports = [...new Set((record.endpoints?.length ? record.endpoints : [record]).map(endpoint => endpoint.local_port || 0))].sort((a, b) => a - b);
      const agent = { id: record.agent_id, name: record.agent_name || record.agent_id };
      const region = !record.location?.non_public && geoRegionDetails(record.location?.country_code);
      return [node("code", {}, String(record.client_ip).split(/(?<=:)/).flatMap(part => [part, node("wbr")])), node("span", { className: "a-probe-name" }, region && regionFlag(region, true), node("span", {}, connectionLocationLabel(record.location))), node("span", { className: "a-probe-name" }, nodeRegionFlag(ctx, agent), node("span", {}, agent.name)),
        badge(engineName(record.engine)), ports.map(port => port || "—").join(" · "), stack(muted(`最近 ${date(record.last_seen)}`), muted(`首次 ${date(record.first_seen)}`))];
    }, result ? "暂无记录" : loading ? "正在读取…" : "读取失败，请重试", connectionKey);
    collection.update(reportSources.map(source => ({
      agentID: source.agent_id, agentName: source.agent_name || source.agent_id,
      engine: filters.engine ? engineName(filters.engine) : "全部内核", state: connectionSourceLabel(source),
      attention: source.status !== "ok" || source.truncated || !source.updated_at,
      tone: source.status === "unavailable" ? "bad" : source.status !== "ok" || source.truncated || !source.updated_at ? "warn" : "ok",
      detail: source.status !== "ok" || source.truncated || !source.updated_at ? redactDiagnostic(source.detail || connectionSourceDetail(source)) : "",
      transport: source.truncated ? "日志窗口不完整" : "", lastSeen: source.updated_at ? date(source.updated_at) : "",
    })), { scope: `${filters.date} · ${filters.period === "month" ? "所在月份" : "当天"} · ${controls.elements.agent_id.selectedOptions[0]?.textContent || "全部节点"}` });
    page.textContent = result ? `第 ${cursors.length + 1} 页 · 本页 ${records.length} 条` : "";
    previous.disabled = !cursors.length; next.disabled = !nextCursor;
  }
  async function load({ target = "", history = [], force = false, changedScope = false, verifiedRestore = false } = {}) {
    if (force && forcePending || reading && !changedScope && target === cursor) return;
    reading = true;
    if (force) forcePending = true;
    controller?.abort(); controller = new AbortController();
    const signal = controller.signal, revision = ++serial;
    const current = () => ctx.current() && !signal.aborted && serial === revision;
    loading = true;
    view.root.setAttribute("aria-busy", "true");
    status.textContent = "正在读取…";
    try {
      if (!current()) return;
      // Refresh the same account preference used by the node page; the API
      // controls ordering before applying its signed pagination cursor.
      if (force || initialOrder || !nodeOrderReady) {
        const pendingOrder = !force && (initializing ? initialNodeOrder : initialOrder); initialOrder = null;
        const order = await (pendingOrder || boundedRead(ctx.api, "/preferences/node-order", { signal }));
        if (!current()) return;
        if (!sameConnectionValue(nodeOrder, order.ids)) { target = ""; history = []; changedScope = true; }
        nodeOrder = order.ids; nodeOrderReady = true;
      }
      const params = connectionQuery(filters, target, nodeOrder), key = params.toString();
      const observedAt = performance.timeOrigin + performance.now();
      const entry = scopeReady && cache.get(key), cached = Array.isArray(entry?.result?.country_codes) ? entry : null;
      if (changedScope || target !== cursor) { result = cached?.result || (scopeReady ? cache.preview(params) : null); cursor = target; cursors = history; nextCursor = result?.next_cursor || ""; }

      status.textContent = result ? "上次结果 · 正在更新…" : "正在读取…";
      error.textContent = "";
      paint();
      if (verifiedRestore && cached?.historical && cached.fresh && result === cached.result) {
        // Scope already checked this route. Keep the sealed page responsive
        // and let the next authenticated poll confirm late observations.
        status.textContent = "已读取";
        return;
      }
      const sync = new URLSearchParams(params);
      sync.set("sync", "true");
      if (cached?.result.sync_token) sync.set("sync_token", cached.result.sync_token);
      let etag = "";
      const path = () => `/client-connections?${sync}&locations=cached`;
      const onResponse = response => { etag = response.headers?.get("ETag") || ""; };
      let response = await boundedRead(ctx.api, path(), {
        signal, onResponse,
        ...(cached?.etag ? { conditional: { etag: cached.etag, value: cached.result } } : {}),
      });
      if (!current()) return;
      if (cache.scope && response.cache_scope !== cache.scope) {
        cache.clear(); result = null; sources = []; nextCursor = "";
      }
      // A group can enter this cursor page after another group disappears.
      // Re-read the bounded page when no local baseline contains that group.
      if (response.delta) {
        const known = new Set([...(cached?.result.records || []), ...response.records].map(connectionKey));
        if (response.record_keys?.some(key => !known.has(key))) {
          sync.delete("sync_token");
          response = await boundedRead(ctx.api, path(), { signal, onResponse });
        }
      }
      if (!current()) return;
      const previousResult = cached && cached.result.cache_scope === response.cache_scope ? cached.result : null;
      let fresh = mergeConnectionUpdate(previousResult, response);
      // Deferred geography is extra data for the exact record. Preserve its
      // retry time only while the server's geography still agrees.
      if (fresh !== previousResult && previousResult) {
        const before = new Map(previousResult.records.map(record => [connectionKey(record), record]));
        fresh = { ...fresh, records: fresh.records.map(record => {
          const prior = before.get(connectionKey(record));
          return prior?.location_retry_at && !record.location_retry_at && sameConnectionValue(record.location, prior.location)
            ? { ...record, location_retry_at: prior.location_retry_at } : record;
        }) };
      }
      // A successful history response includes every accessible selected node,
      // even when that node has no logs. Failed reads never enter this path.
      if (filters.agent_id && Array.isArray(fresh.sources) && !fresh.sources.some(source => source.agent_id === filters.agent_id)) {
        controls.elements.agent_id.value = ''; preferenceFields.save({ agent_id: '' });
        queueMicrotask(changed); return;
      }
      const fetchedAt = Date.now();
      cache.bind(fresh.cache_scope); scopeReady = true;
      const unchanged = Boolean(previousResult && sameConnectionValue({ ...previousResult, sync_token: undefined, record_keys: undefined },
        { ...fresh, sync_token: undefined, record_keys: undefined }));
      cache.set(key, fresh, fetchedAt, { observedAt, etag, unchanged });
      failures = 0;
      result = fresh;
      cursor = target; cursors = history; nextCursor = result.next_cursor || "";
      if (!filters.agent_id) sources = result.sources || [];
      else sources = sources.length ? sources.map(source => result.sources?.find(value => value.agent_id === source.agent_id) || source) : result.sources || [];
      cache.select({ filters: { ...filters }, cursor, cursors, nodeOrder, sources: sources.map(source => ({ agent_id: source.agent_id, agent_name: source.agent_name })) }, observedAt);
      status.textContent = "已读取";
      for (const [ip, retryAt] of absentLocations) if (retryAt <= Date.now()) absentLocations.delete(ip);
      const missing = result.records.filter(record => !record.location?.non_public && (!record.location?.country_code || record.location.country_code === "CN" && !record.location.province)
        && Date.now() >= Math.max(Date.parse(record.location_retry_at) || 0, absentLocations.get(record.client_ip) || 0));
      if (missing.length) {
        const locationQuery = new URLSearchParams(params);
        locationQuery.set("sync", "true");
        if (result.page_cursor) locationQuery.set("cursor", result.page_cursor);
        void boundedRead(ctx.api, `/client-connections?${locationQuery}&locations=only`, { signal }).then(enriched => {
          if (!current() || !result) return;
          if (enriched.cache_scope && cache.scope && enriched.cache_scope !== cache.scope) {
            cache.clear(); result = null; sources = []; nextCursor = "";
            error.textContent = "访问范围已变化，请刷新"; updateSources(); paint(); return;
          }
          const locations = new Map(enriched.records.map(record => [connectionKey(record), record]));
          const records = result.records.map(record => {
            const match = locations.get(connectionKey(record));
            if (!match) return record;
            const retryAt = Date.parse(match.location_retry_at);
            if (Number.isFinite(retryAt) && retryAt > Date.now()) absentLocations.set(record.client_ip, retryAt);
            return { ...record, location: match.location, ...(match.location_retry_at ? { location_retry_at: match.location_retry_at } : {}) };
          });
          if (sameConnectionValue(records, result.records)) return;
          result = { ...result, records };
          result.country_codes = [...new Set([...(result.country_codes || []), ...result.records.filter(record => !record.location?.non_public).map(record => record.location?.country_code).filter(Boolean)])].sort();
          // This page has data outside the original ETag. Keep the sync
          // position for real changes, but do not validate it as that entity.
          cache.set(key, result, fetchedAt, { observedAt: observedAt + 0.001 }); paint();
        }).catch(failure => {
          if (!current() || failure.name === "AbortError") return;
          if ([401, 403, 404, 409].includes(failure.status)) {
            cache.clear(); result = null; sources = []; nextCursor = ""; updateSources(); paint();
          } else {
            for (const record of missing) absentLocations.set(record.client_ip, Date.now() + connectionPollDelay);
          }
          error.textContent = `属地更新失败：${redactDiagnostic(failure.message)}`;
        });
      }
    } catch (failure) {
      if (!current() || failure.name === "AbortError") return;
      if ([401, 403, 404, 409].includes(failure.status)) { cache.clear(); result = null; sources = []; nextCursor = ""; }

      failures++;
      error.textContent = redactDiagnostic(failure.message);
      status.textContent = result && !result.preview ? "更新失败，保留上次结果" : "读取失败，请重试";
    } finally {
      if (force) forcePending = false;
      if (current()) {
        reading = false; loading = false; updateSources(); paint();
      }
    }
  }
  function changed() {
    if (!controls.checkValidity()) return;
    filters = { ...formData(controls), include_non_public: controls.elements.include_non_public.checked ? "true" : "" };
    data.analyticsConnectionFilters = filters;
    void load({ changedScope: true });
  }
  let timer;
  controls.addEventListener("submit", event => { event.preventDefault(); clearTimeout(timer); changed(); });
  controls.addEventListener("change", () => { clearTimeout(timer); changed(); });
  for (const event of ['input', 'change']) controls.addEventListener(event, () => { queryTouched = true; }, { capture: true });
  controls.elements.client_ip.addEventListener("input", event => { clearTimeout(timer); controller?.abort(); serial++; if (!event.isComposing) timer = setTimeout(changed, 250); });
  controls.elements.client_ip.addEventListener("compositionend", () => { clearTimeout(timer); timer = setTimeout(changed, 250); });
  // A history confirmation revalidates the session and live grants itself.
  // Avoid a separate scope request on each polling tick.
  const scopeWatch = createPoller({
    isActive: () => ctx.current() && !document.hidden,
    delay: () => Math.min(120_000, connectionPollDelay * 2 ** Math.min(failures, 4)),
    run: () => load({ target: cursor, history: cursors }),
  });
  const visibility = () => document.hidden ? scopeWatch.stop() : void scopeWatch.trigger();
  document.addEventListener("visibilitychange", visibility, { signal: ctx.signal });
  window.addEventListener("focus", () => void scopeWatch.trigger(), { signal: ctx.signal });

  ctx.signal.addEventListener("abort", () => { controller?.abort(); clearTimeout(timer); scopeWatch.stop(); cache.flush(); }, { once: true });
  ctx.onRefresh(() => load({ target: cursor, history: cursors, force: true }), { local: true });
  updateSources(); paint();
  // Mount usable filters while the first authenticated read is pending. The
  // table owns its busy state; unknown totals and sources are never success.
  async function initialize() {
    // Preferences select the query, ordering selects its cursor, and scope
    // gates only cached data. Start all three together and cap the cache wait.
    let timer;
    const grace = new Promise(resolve => { timer = setTimeout(resolve, 100); });
    const restoration = Promise.race([scopeRestore.then(() => { scopeReady = true; }), grace]);
    try {
      const [, order] = await Promise.all([preferenceFields.ready, initialNodeOrder, restoration]);
      if (!ctx.current()) return;
      initializing = false;
      initialOrder = null; nodeOrder = order.ids; nodeOrderReady = true;
    } catch (failure) {
      if (ctx.current()) { loading = false; error.textContent = redactDiagnostic(failure.message); paint(); }
      return;
    } finally { clearTimeout(timer); }
    filters = { ...formData(controls), include_non_public: controls.elements.include_non_public.checked ? "true" : "" };
    data.analyticsConnectionFilters = filters;
    const position = scopeReady ? cache.selection : null;
    // The server owns filters. A verified same-scope cache may retain only a
    // reading position whose query still matches those restored controls.
    if (!queryTouched && position && sameConnectionValue(position.nodeOrder, nodeOrder) && ['date', 'period', 'agent_id', 'engine', 'client_ip', 'country_code', 'include_non_public'].every(name => (position.filters[name] || '') === (filters[name] || ''))) {
      cursor = position.cursor; cursors = position.cursors; sources = position.sources;
    }
    void load({ target: cursor, history: cursors, changedScope: true, verifiedRestore: scopeReady });
    scopeWatch.start();
  }
  const preferenceFields = bindPreferenceFields(ctx, 'connections', [controls], {
    valid: (name, value) => {
      if (name !== 'client_ip' || !value) return true;
      if (/^(?:\d{1,3}\.){3}\d{1,3}$/.test(value)) return value.split('.').every(part => Number(part) <= 255 && String(Number(part)) === part);
      try { return !value.includes('%') && new URL(`http://[${value}]/`).hostname.startsWith('['); } catch { return false; }
    },
    normalize: saved => ({ ...saved, ...(saved.time_mode && saved.time_mode !== 'absolute' ? { date: defaultConnectionFilters().date, period: saved.time_mode === 'month' ? 'month' : 'day' } : {}) }),
    restore: () => { if (!initializing) changed(); },
  });
  controls.elements.date.addEventListener('change', () => preferenceFields.save({ time_mode: 'absolute' }));
  controls.elements.period.addEventListener('change', () => {
    const mode = ctx.preferences?.get('connections').time_mode;
    if (mode && mode !== 'absolute') preferenceFields.save({ time_mode: controls.elements.period.value === 'month' ? 'month' : 'today' });
  });
  void initialize();
  return stack(filterbar(controls), error, section("客户端连接", stack(summary, node("div", { className: "a-card-body a-stack" }, muted("根据内核日志观测，不代表当前在线会话数"), status), view.root,
    node("div", { className: "a-card-body" }, actions(page, previous, next))), collection.trigger));
}
