import { createConnectionCache, connectionSessionStorage, connectionRetention } from "../modules/client-connection-cache.js";
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

const engineName = value => ({ mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "Shadowsocks Rust" })[value] || value;

export async function connectionsPage(ctx) {
  const data = ctx.state.data;
  const cache = data.analyticsConnectionCache ||= createConnectionCache({ storage: connectionSessionStorage() });
  // No storage candidate means no extra cold-start gate. The lightweight
  // scope read overlaps module/order initialization and the first history API.
  if (cache.hasStored() || cache.scope) {
    try {
      const verified = await ctx.connectionScope;
      if (!ctx.current()) return;
      cache.restore(verified.scope);
    } catch (failure) { if (ctx.current()) cache.clear(); throw failure; }
  }
  const today = defaultConnectionFilters().date;
  const restored = cache.selection?.day === today ? cache.selection : null;
  let filters = data.analyticsConnectionFilters ||= restored?.filters || defaultConnectionFilters();
  let initialOrder = ctx.connectionNodeOrder;
  // Filter changes can share the route's pending order read. Once settled,
  // later refreshes continue to read the current authenticated preference.
  initialOrder?.finally(() => { initialOrder = null; }).catch(() => {});
  let sources = restored?.sources || [], result = null, resultAt = 0, expiryTimer, loading = true, forcePending = false, controller, serial = 0, cursor = restored?.cursor || "", cursors = restored?.cursors || [], nextCursor = "", nodeOrder = restored?.nodeOrder || savedNodeOrder();
  let regionCodes = data.analyticsConnectionRegions || [], regionState = regionCodes.length ? "ready" : "loading";
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
    const select = controls.elements.country_code, value = select.value;
    select.title = geoRegionDetails(value)?.name || "全部国家/地区";
    const codes = new Set([...regionCodes, value, ...(result?.records || []).filter(row => !row.location?.non_public).map(row => row.location?.country_code)]);
    const regions = [...codes].map(geoRegionDetails).filter(Boolean).sort((a, b) => a.name.localeCompare(b.name, "zh-CN"));
    const signature = JSON.stringify([regions.map(region => region.code), regionState]);
    if (select.dataset.options === signature) return;
    select.replaceChildren(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")), node("option", { value: "" }, "全部国家/地区"),
      ...regions.map(region => node("option", { value: region.code }, node("span", { className: "a-probe-name" }, regionFlag(region, true), node("span", {}, region.name)))),
      regionState !== "ready" && node("option", { disabled: true }, regionState === "loading" ? "正在加载…" : "国家/地区加载失败，刷新重试"));
    select.dataset.options = signature; select.value = value;
  }
  async function loadRegions() {
    regionState = "loading"; updateRegions();
    try {
      const codes = await ctx.api("/regions", { signal: ctx.signal });
      if (!ctx.current()) return;
      regionCodes = data.analyticsConnectionRegions = Array.isArray(codes) ? codes : [];
      regionState = regionCodes.length ? "ready" : "failed";
    } catch (failure) {
      if (!ctx.current() || failure.name === "AbortError") return;
      regionState = "failed";
    }
    updateRegions();
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
      return [node("code", {}, String(record.client_ip).split(/(?<=:)/).flatMap(part => [part, node("wbr")])), node("span", { className: "a-probe-name" }, region && regionFlag(region, true), node("span", {}, connectionLocationLabel(record.location))), node("span", {}, nodeRegionFlag(ctx, agent), " ", agent.name),
        badge(engineName(record.engine)), ports.map(port => port || "—").join(" · "), stack(muted(`最近 ${date(record.last_seen)}`), muted(`首次 ${date(record.first_seen)}`))];
    }, result ? "暂无记录" : loading ? "正在读取…" : "读取失败，请重试");
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
  function scheduleExpiry() {
    clearTimeout(expiryTimer);
    if (!result) return;
    const at = resultAt;
    expiryTimer = setTimeout(() => {
      if (!ctx.current() || !result || resultAt !== at) return;
      result = null; nextCursor = "";
      error.textContent = "结果已过期，请刷新";
      paint();
    }, Math.max(0, at + connectionRetention - Date.now()));
  }
  async function load({ target = "", history = [], force = false, changedScope = false } = {}) {
    if (force && forcePending) return;
    if (force) forcePending = true;
    if (force && regionState === "failed") void loadRegions();
    controller?.abort(); controller = new AbortController();
    const signal = controller.signal, revision = ++serial;
    const current = () => ctx.current() && !signal.aborted && serial === revision;
    loading = true;
    view.root.setAttribute("aria-busy", "true");
    status.textContent = "正在读取…";
    try {
      // Refresh the same account preference used by the node page; the API
      // controls ordering before applying its signed pagination cursor.
      if (force || initialOrder || !sources.length) {
        const order = await (!force && initialOrder || ctx.api("/preferences/node-order", { signal }));
        if (!current()) return;
        if (JSON.stringify(nodeOrder) !== JSON.stringify(order.ids)) { target = ""; history = []; changedScope = true; }
        nodeOrder = order.ids;
      }
      const params = connectionQuery(filters, target, nodeOrder), key = params.toString();
      const cached = cache.get(key);
      if (changedScope) { result = cached?.result || cache.preview(params); resultAt = cached?.at || result?.snapshotAt || 0; cursor = target; cursors = history; nextCursor = ""; }
      if (result && (Date.now() < resultAt || Date.now() - resultAt >= connectionRetention)) result = null;
      status.textContent = cached?.fresh && !force ? "已读取" : result ? "上次结果 · 正在更新…" : "正在读取…";
      error.textContent = "";
      paint(); scheduleExpiry();
      const fresh = cached?.fresh && !force ? cached.result : await ctx.api(`/client-connections?${params}&locations=cached`, { signal });
      if (!current()) return;
      const fetchedAt = cached?.fresh && !force ? cached.at : Date.now();
      if (!cached?.fresh || force) { cache.bind(fresh.cache_scope); cache.set(key, fresh, fetchedAt, false); }
      result = fresh; resultAt = fetchedAt;
      cursor = target; cursors = history; nextCursor = result.next_cursor || "";
      if (!filters.agent_id) sources = result.sources || [];
      else sources = sources.length ? sources.map(source => result.sources?.find(value => value.agent_id === source.agent_id) || source) : result.sources || [];
      cache.select({ filters: { ...filters }, cursor, cursors, nodeOrder, day: today, sources: sources.map(source => ({ agent_id: source.agent_id, agent_name: source.agent_name })) });
      status.textContent = "已读取";
      if ((!cached?.locationsAttempted || force) && result.records?.some(record => !record.location?.non_public && (!record.location?.country_code || record.location.country_code === "CN" && !record.location.province))) {
        cache.set(key, result, fetchedAt, true);
        const locationQuery = new URLSearchParams(params);
        if (result.page_cursor) locationQuery.set("cursor", result.page_cursor);
        void ctx.api(`/client-connections?${locationQuery}&locations=only`, { signal }).then(enriched => {
          if (!current() || !result) return;
          if (enriched.cache_scope && cache.scope && enriched.cache_scope !== cache.scope) {
            cache.clear(); result = null; sources = []; nextCursor = "";
            error.textContent = "访问范围已变化，请刷新"; updateSources(); paint(); return;
          }
          const locations = new Map(enriched.records.map(record => [record.id, record.location]));
          result = { ...result, records: result.records.map(record => ({ ...record, location: locations.get(record.id) || record.location })) };
          cache.set(key, result, fetchedAt, true); paint();
        }).catch(failure => {
          if (!current() || failure.name === "AbortError") return;
          if ([401, 403, 404, 409].includes(failure.status)) {
            cache.clear(); result = null; sources = []; nextCursor = ""; updateSources(); paint();
          } else if (result && Date.now() - fetchedAt < connectionRetention) cache.set(key, result, fetchedAt, true);
          error.textContent = `属地更新失败，可刷新重试：${redactDiagnostic(failure.message)}`;
        });
      }
    } catch (failure) {
      if (!current() || failure.name === "AbortError") return;
      if ([401, 403, 404, 409].includes(failure.status)) { cache.clear(); result = null; sources = []; nextCursor = ""; }
      if (result && (Date.now() < resultAt || Date.now() - resultAt >= connectionRetention)) result = null;
      error.textContent = redactDiagnostic(failure.message);
      status.textContent = result && !result.preview ? "更新失败，保留上次结果" : "读取失败，请重试";
    } finally {
      if (force) forcePending = false;
      if (current()) {
        loading = false; updateSources(); updateRegions(); paint();
        scheduleExpiry();
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
    delay: () => 10_000,
    async run() {
      try {
        const verified = await ctx.api("/client-connections?scope_only=true");
        if (!ctx.current() || !cache.scope || verified.scope === cache.scope) return;
        controller?.abort(); serial++; cache.clear(); result = null; sources = []; nextCursor = "";
        updateSources(); paint();
        void load({ changedScope: true });
      } catch (failure) {
        if (ctx.current() && [401, 403].includes(failure.status)) {
          controller?.abort(); serial++; cache.clear(); result = null; sources = []; nextCursor = "";
          loading = false; error.textContent = redactDiagnostic(failure.message); updateSources(); paint();
        }
      }
    },
  });
  const visibility = () => document.hidden ? scopeWatch.stop() : void scopeWatch.trigger();
  document.addEventListener("visibilitychange", visibility, { signal: ctx.signal });
  window.addEventListener("focus", () => void scopeWatch.trigger(), { signal: ctx.signal });
  scopeWatch.start();
  ctx.signal.addEventListener("abort", () => { controller?.abort(); clearTimeout(timer); clearTimeout(expiryTimer); scopeWatch.stop(); cache.flush(); }, { once: true });
  ctx.onRefresh(() => load({ target: cursor, history: cursors, force: true }), { local: true });
  updateSources(); updateRegions(); paint();
  if (regionState !== "ready") void loadRegions();
  // Mount usable filters while the first authenticated read is pending. The
  // table owns its busy state; unknown totals and sources are never success.
  void load({ target: cursor, history: cursors, changedScope: true });
  return stack(filterbar(controls), error, section("客户端连接", stack(summary, node("div", { className: "a-card-body a-stack" }, muted("根据内核日志观测，不代表当前在线会话数"), status), view.root,
    node("div", { className: "a-card-body" }, actions(page, previous, next))), collection.trigger));
}
