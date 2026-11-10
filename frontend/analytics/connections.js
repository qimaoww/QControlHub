import { createConnectionCache, connectionSessionStorage } from "../modules/client-connection-cache.js";
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

const connectionKey = record => JSON.stringify([record.agent_id, record.engine, record.client_ip]);
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
  const cache = data.analyticsConnectionCache ||= createConnectionCache({ storage: connectionSessionStorage() });
  let scopeReady = !(cache.hasStored() || cache.scope), scopePending = ctx.connectionScope;
  async function verifyScope() {
    const pending = scopePending ||= boundedRead(ctx.api, '/client-connections?scope_only=true', {signal:ctx.signal});
    try {
      const verified = await pending;
      if (!ctx.current()) throw new DOMException('页面已切换', 'AbortError');
      if (!scopeReady) { cache.restore(verified.scope); scopeReady = true; }
      return verified;
    } catch (error) { cache.clear(); scopeReady = false; throw error; }
    finally { if (scopePending === pending) scopePending = null; }
  }
  const today = defaultConnectionFilters().date;
  const restored = null;
  let filters = data.analyticsConnectionFilters ||= restored?.filters || defaultConnectionFilters();
  let initialOrder = ctx.connectionNodeOrder;
  // Filter changes can share the route's pending order read. Once settled,
  // later refreshes continue to read the current authenticated preference.

  let sources = restored?.sources || [], result = null, loading = true, reading = false, failures = 0, forcePending = false, controller, serial = 0, cursor = restored?.cursor || "", cursors = restored?.cursors || [], nextCursor = "", nodeOrder = restored?.nodeOrder || savedNodeOrder();
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
    field("内核", "engine", filters.engine, { options: [["", "全部内核"], ...["mihomo", "xray", "sing-box", "ss-rust"].map(value => [value, engineName(value)])] })),
    field("包含非公网地址", "include_non_public", filters.include_non_public === "true", { type: "checkbox" }),
    actions(button("今日", () => {
      const recent = defaultConnectionFilters();
      controls.elements.date.value = recent.date; controls.elements.period.value = "day"; controls.elements.include_non_public.checked = false; changed();
    }), button("本月", () => { controls.elements.date.value = defaultConnectionFilters().date; controls.elements.period.value = "month"; changed(); }),
    button("清除筛选", () => {
      for (const name of ["agent_id", "engine", "client_ip", "country_code"]) controls.elements[name].value = "";
      controls.elements.include_non_public.checked = false; changed();
    }), button("刷新", () => void load({ target: cursor, history: cursors, force: true }))));
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
  async function load({ target = "", history = [], force = false, changedScope = false } = {}) {
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
      if (!scopeReady) await verifyScope();
      if (!current()) return;
      // Refresh the same account preference used by the node page; the API
      // controls ordering before applying its signed pagination cursor.
      if (force || initialOrder || !sources.length) {
        const pendingOrder = !force && initialOrder; initialOrder = null;
        const order = await (pendingOrder || boundedRead(ctx.api, "/preferences/node-order", { signal }));
        if (!current()) return;
        if (JSON.stringify(nodeOrder) !== JSON.stringify(order.ids)) { target = ""; history = []; changedScope = true; }
        nodeOrder = order.ids;
      }
      const params = connectionQuery(filters, target, nodeOrder), key = params.toString();
      const entry = cache.get(key), cached = Array.isArray(entry?.result?.country_codes) ? entry : null;
      if (changedScope || target !== cursor) { result = cached?.result || cache.preview(params); cursor = target; cursors = history; nextCursor = result?.next_cursor || ""; }

      status.textContent = result ? "上次结果 · 正在更新…" : "正在读取…";
      error.textContent = "";
      paint();
      const sync = new URLSearchParams(params);
      sync.set("sync", "true");
      if (cached?.result.sync_token) sync.set("sync_token", cached.result.sync_token);
      let response = await boundedRead(ctx.api, `/client-connections?${sync}&locations=cached`, { signal });
      // A group can re-enter this cursor page after another group disappears.
      // If its old value is absent locally, obtain this bounded page once.
      const known = new Set([...(cached?.result.records || []), ...response.records].map(connectionKey));
      if (response.delta && response.record_keys?.some(key => !known.has(key))) {
        sync.delete("sync_token");
        response = await boundedRead(ctx.api, `/client-connections?${sync}&locations=cached`, { signal });
      }
      const fresh = mergeConnectionUpdate(cached?.result, response);
      fresh.location_retry_at = cached?.result.location_retry_at || 0;
      if (!current()) return;
      const fetchedAt = Date.now();
      cache.bind(fresh.cache_scope); cache.set(key, fresh, fetchedAt, cached?.locationsAttempted || false);
      failures = 0;
      result = fresh;
      cursor = target; cursors = history; nextCursor = result.next_cursor || "";
      if (!filters.agent_id) sources = result.sources || [];
      else sources = sources.length ? sources.map(source => result.sources?.find(value => value.agent_id === source.agent_id) || source) : result.sources || [];
      cache.select({ filters: { ...filters }, cursor, cursors, nodeOrder, day: today, sources: sources.map(source => ({ agent_id: source.agent_id, agent_name: source.agent_name })) });
      status.textContent = "已读取";
      if ((!cached?.locationsAttempted || force || Date.now() >= fresh.location_retry_at || fresh.records.some(record => !cached?.result.records.some(old => connectionKey(old) === connectionKey(record)))) && result.records?.some(record => !record.location?.non_public && (!record.location?.country_code || record.location.country_code === "CN" && !record.location.province))) {
        result.location_retry_at = Date.now() + 60_000;
        cache.set(key, result, fetchedAt, true);
        const locationQuery = new URLSearchParams(params);
        if (result.page_cursor) locationQuery.set("cursor", result.page_cursor);
        void boundedRead(ctx.api, `/client-connections?${locationQuery}&locations=only`, { signal }).then(enriched => {
          if (!current() || !result) return;
          if (enriched.cache_scope && cache.scope && enriched.cache_scope !== cache.scope) {
            cache.clear(); result = null; sources = []; nextCursor = "";
            error.textContent = "访问范围已变化，请刷新"; updateSources(); paint(); return;
          }
          const locations = new Map(enriched.records.map(record => [record.id, record.location]));
          result = { ...result, records: result.records.map(record => ({ ...record, location: locations.get(record.id) || record.location })) };
          result.country_codes = [...new Set([...(result.country_codes || []), ...result.records.filter(record => !record.location?.non_public).map(record => record.location?.country_code).filter(Boolean)])];
          cache.set(key, result, fetchedAt, true); paint();
        }).catch(failure => {
          if (!current() || failure.name === "AbortError") return;
          if ([401, 403, 404, 409].includes(failure.status)) {
            cache.clear(); result = null; sources = []; nextCursor = ""; updateSources(); paint();
          } else if (result) cache.set(key, result, fetchedAt, true);
          error.textContent = `属地更新失败，可刷新重试：${redactDiagnostic(failure.message)}`;
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
  controls.elements.client_ip.addEventListener("input", event => { clearTimeout(timer); controller?.abort(); serial++; if (!event.isComposing) timer = setTimeout(changed, 250); });
  controls.elements.client_ip.addEventListener("compositionend", () => { clearTimeout(timer); timer = setTimeout(changed, 250); });
  // Recheck authority without repeating history aggregates. This also handles
  // an administrator losing an owner-hidden/revoked node in an idle tab.
  const scopeWatch = createPoller({
    isActive: () => ctx.current() && !document.hidden,
    delay: () => Math.min(120_000, 10_000 * 2 ** Math.min(failures, 4)),
    async run() {
      try {
        const verified = await boundedRead(ctx.api, "/client-connections?scope_only=true", { signal: ctx.signal });
        if (!ctx.current()) return;
        if (!cache.scope || verified.scope === cache.scope) {
          await load({ target: cursor, history: cursors }); return;
        }
        controller?.abort(); serial++; cache.clear(); result = null; sources = []; nextCursor = "";
        updateSources(); paint();
        void load({ changedScope: true });
      } catch (failure) {
        if (ctx.current() && [401, 403].includes(failure.status)) {
          controller?.abort(); serial++; cache.clear(); result = null; sources = []; nextCursor = "";
          reading = false; loading = false; error.textContent = redactDiagnostic(failure.message); updateSources(); paint();
        }
      }
    },
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
    const before = JSON.stringify(formData(controls));
    if (cache.hasStored() || cache.scope) {
      try {
        await verifyScope();
        if (!ctx.current()) return;
        const selection = cache.selection;
        if (selection && before === JSON.stringify(formData(controls))) {
          filters = data.analyticsConnectionFilters = selection.filters;
          sources = selection.sources || []; nodeOrder = selection.nodeOrder || nodeOrder;
          cursor = selection.cursor || ""; cursors = selection.cursors || [];
          updateSources(); updateRegions();
          for (const [name, value] of Object.entries(filters)) {
            const control = controls.elements.namedItem(name);
            if (control) { if (control.type === 'checkbox') control.checked = value === 'true'; else control.value = value; }
          }
        }
      } catch (failure) {
        if (!ctx.current()) return;
        cache.clear(); loading = false; error.textContent = redactDiagnostic(failure.message); paint();
        return;
      }
    }
    if (!ctx.current()) return;
    void load({ target: cursor, history: cursors, changedScope: true });
    scopeWatch.start();
  }
  void initialize();
  return stack(filterbar(controls), error, section("客户端连接", stack(summary, node("div", { className: "a-card-body a-stack" }, muted("根据内核日志观测，不代表当前在线会话数"), status), view.root,
    node("div", { className: "a-card-body" }, actions(page, previous, next))), collection.trigger));
}
