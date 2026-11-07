import { createConnectionCache } from "../modules/client-connection-cache.js";
import { defaultConnectionFilters, connectionQuery, connectionLocationLabel, connectionSourceLabel, connectionSourceDetail } from "../modules/client-connection-model.js";
import { savedNodeOrder, orderNodesBySavedOrder } from "../modules/node-order.js";
import { redactDiagnostic } from "../modules/errors.js";
import { node, button, actions, stack, section, filterbar, field, badge, muted, notice, formData } from "./ui.js";
import { observationTable } from "./observability-table.js";
import { date } from "./operations.js";
import { sourceStatus } from "./source-status.js";
import { nodeName, nodeRegionFlag } from "./node-region.js";

const engineName = value => ({ mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "Shadowsocks Rust" })[value] || value;

export async function connectionsPage(ctx) {
  const data = ctx.state.data;
  let filters = data.analyticsConnectionFilters ||= defaultConnectionFilters();
  const cache = data.analyticsConnectionCache ||= createConnectionCache();
  let initialOrder = ctx.connectionNodeOrder;
  // Filter changes can share the route's pending order read. Once settled,
  // later refreshes continue to read the current authenticated preference.
  initialOrder?.finally(() => { initialOrder = null; }).catch(() => {});
  let sources = [], result = null, loading = true, controller, serial = 0, cursor = "", cursors = [], nextCursor = "", nodeOrder = savedNodeOrder();
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
    field("内核", "engine", filters.engine, { options: [["", "全部内核"], ...["mihomo", "xray", "sing-box", "ss-rust"].map(value => [value, engineName(value)])] })),
    field("包含非公网地址", "include_non_public", filters.include_non_public === "true", { type: "checkbox" }),
    actions(button("今日", () => {
      const recent = defaultConnectionFilters();
      controls.elements.date.value = recent.date; controls.elements.period.value = "day"; controls.elements.include_non_public.checked = false; changed();
    }), button("本月", () => { controls.elements.date.value = defaultConnectionFilters().date; controls.elements.period.value = "month"; changed(); }),
    button("清除筛选", () => {
      for (const name of ["agent_id", "engine", "client_ip"]) controls.elements[name].value = "";
      controls.elements.include_non_public.checked = false; changed();
    }), button("刷新", () => void load({ target: cursor, history: cursors, force: true }))));
  controls.prepend(node("fieldset", { className: "a-form-fields a-stack" }, controls.firstElementChild, controls.querySelector(".a-check")));
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
      return [node("code", {}, String(record.client_ip).split(/(?<=:)/).flatMap(part => [part, node("wbr")])), connectionLocationLabel(record.location), node("span", {}, nodeRegionFlag(ctx, agent), " ", agent.name),
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
  async function load({ target = "", history = [], force = false, changedScope = false } = {}) {
    controller?.abort(); controller = new AbortController();
    const signal = controller.signal, revision = ++serial;
    const current = () => ctx.current() && !signal.aborted && serial === revision;
    loading = true;
    view.root.setAttribute("aria-busy", "true");
    status.textContent = "正在读取…";
    try {
      // Refresh the same account preference used by the node page; the API
      // controls ordering before applying its signed pagination cursor.
      if (force || !sources.length) {
        const order = await (!force && initialOrder || ctx.api("/preferences/node-order", { signal }));
        if (!current()) return;
        if (JSON.stringify(nodeOrder) !== JSON.stringify(order.ids)) { target = ""; history = []; }
        nodeOrder = order.ids;
      }
      const params = connectionQuery(filters, target, nodeOrder), key = params.toString();
      const cached = cache.get(key);
      if (changedScope) { result = cached?.result || cache.preview(params); cursor = target; cursors = history; nextCursor = ""; paint(); }
      status.textContent = cached?.fresh && !force ? "已读取" : "正在读取…";
      error.textContent = "";
      const fresh = cached?.fresh && !force ? cached.result : await ctx.api(`/client-connections?${params}&locations=cached`, { signal });
      if (!current()) return;
      if (force) cache.clear();
      const fetchedAt = cached?.fresh && !force ? cached.at : Date.now();
      result = fresh; cache.set(key, result, fetchedAt, cached?.locationsAttempted && !force);
      cursor = target; cursors = history; nextCursor = result.next_cursor || "";
      if (!filters.agent_id) sources = result.sources || [];
      else sources = sources.length ? sources.map(source => result.sources?.find(value => value.agent_id === source.agent_id) || source) : result.sources || [];
      status.textContent = "已读取";
      if ((!cached?.locationsAttempted || force) && result.records?.some(record => !record.location?.non_public && (!record.location?.country_code || record.location.country_code === "CN" && !record.location.province))) {
        const locationQuery = new URLSearchParams(params);
        if (result.page_cursor) locationQuery.set("cursor", result.page_cursor);
        void ctx.api(`/client-connections?${locationQuery}&locations=only`, { signal }).then(enriched => {
          if (!current()) return;
          const locations = new Map(enriched.records.map(record => [record.id, record.location]));
          result = { ...result, records: result.records.map(record => ({ ...record, location: locations.get(record.id) || record.location })) };
          cache.set(key, result, fetchedAt, true); paint();
        }).catch(failure => { if (current() && failure.name !== "AbortError") { cache.set(key, result, fetchedAt, true); error.textContent = `属地更新失败，可刷新重试：${redactDiagnostic(failure.message)}`; } });
      }
    } catch (failure) {
      if (!current() || failure.name === "AbortError") return;
      if ([401, 403, 404].includes(failure.status)) { cache.clear(); result = null; sources = []; nextCursor = ""; }
      error.textContent = redactDiagnostic(failure.message);
      status.textContent = result && !result.preview ? "更新失败，保留上次结果" : "读取失败，请重试";
    } finally {
      if (current()) { loading = false; updateSources(); paint(); }
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
  ctx.signal.addEventListener("abort", () => { controller?.abort(); clearTimeout(timer); }, { once: true });
  ctx.onRefresh(() => load({ target: cursor, history: cursors, force: true }), { local: true });
  paint();
  // Mount usable filters while the first authenticated read is pending. The
  // table owns its busy state; unknown totals and sources are never success.
  void load({ changedScope: true });
  return stack(filterbar(controls), error, section("客户端连接", stack(summary, node("div", { className: "a-card-body a-stack" }, muted("根据内核日志观测，不代表当前在线会话数"), status), view.root,
    node("div", { className: "a-card-body" }, actions(page, previous, next))), collection.trigger));
}
