import { renderChainGraph } from "./dashboard-chain-graph.js";
import { patch } from "./live-dom.js";
import { actions, badge, button, empty, field, muted, node, section } from "./ui.js";
import { probeDirectionStatus } from "../modules/dashboard-model.js";

const activeStates = new Set(["preparing", "running", "stopping", "cleanup_pending"]);
const ms = value => typeof value === "number" && Number.isFinite(value) ? `${value.toFixed(1)} ms` : "—";
const loss = value => typeof value === "number" && Number.isFinite(value) ? `${Number(value.toFixed(1))}%` : "—";
const keyed = (key, element) => { element.setAttribute("data-refresh-key", key); return element; };
const endpoint = ep => node("div", { className: "a-stack a-dash-topo-inspect-title" }, node("strong", {}, ep.name || ep.address || "节点"), muted(ep.name === ep.address ? "" : ep.address || ""));

function direction(dir, label) {
  const status = probeDirectionStatus(dir);
  return node("div", { className: "a-stack" },
    node("span", { className: "a-field-label" }, label),
    node("strong", {}, dir?.reached ? ms(dir.avg_ms) : status.label),
    muted(dir ? `丢包 ${loss(dir.loss_percent)}` : "无采样"));
}
function groupStatus(pair) {
  if (activeStates.has(pair.active_state) || pair.availability?.running) {
    return { label: ({ preparing: "准备中", stopping: "停止中", cleanup_pending: "清理中" })[pair.active_state] || "探测中", tone: "warn" };
  }
  if (pair.availability?.ready === false) return { label: "不可用", tone: "warn" };
  if (pair.latest?.state === "canceled") return { label: "已停止", tone: "" };
  const hops = pair.latest?.hops || [];
  const dirs = hops.length ? hops.flatMap(hop => [hop.forward, ...(pair.bidirectional ? [hop.reverse] : [])]) : [pair.latest?.forward, ...(pair.bidirectional ? [pair.latest?.reverse] : [])];
  const statuses = dirs.map(dir => probeDirectionStatus(dir));
  const bad = statuses.filter(s => s.tone === "bad").length;
  if (bad) return { label: bad < statuses.length ? "部分异常" : "异常", tone: "bad" };
  if (pair.latest?.state === "failed") return { label: "探测失败", tone: "bad" };
  if (statuses.some(s => s.tone === "warn")) return { label: "丢包", tone: "warn" };
  return { label: statuses.every(s => s.tone === "ok") ? "正常" : "待采样", tone: statuses.every(s => s.tone === "ok") ? "ok" : "" };
}

export function createDashboardTopologySection(ctx, { getProbes, getStates, probeController, isStandalonePage = false }) {
  const canView = ctx.can("agents.read") && ctx.can("tasks.read");
  const canExecute = ctx.can("tasks.execute");
  const page = isStandalonePage ? "topology" : "dashboard";
  let selected = "", touched = false, signature = "", optionsSignature = "", scheduled = false;
  let prefsReady = !ctx.preferences, disposed = false, graphWidth = 900;
  const groupField = field("监测分组", "topo_group", "", { options: [["", "读取中…"]] });
  const select = groupField.querySelector("select");
  select.disabled = true;
  const status = node("span", { className: "a-muted", role: "status" });
  const body = node("div", { className: "a-card-body" }, empty("读取中…"));
  const tools = actions(
    !isStandalonePage ? node("a", { className: "a-button", href: "#topology" }, "拓扑详情") : null,
    canExecute ? button("添加分组", () => probeController.open("", { create: true }), { "data-refresh-key": "add-group", disabled: true }) : null);
  const card = canView ? section("网络延迟监测拓扑", node("div", {}, node("div", { className: "a-filterbar a-card-body" }, groupField, status), body), tools) : null;
  card?.classList.add("a-dash-topo-card");

  let resizeFrame = 0;
  const observer = new ResizeObserver(entries => {
    const width = Math.round(entries[0]?.contentRect.width || 0);
    if (width >= 280 && Math.abs(width - graphWidth) >= 2) {
      graphWidth = width; cancelAnimationFrame(resizeFrame);
      resizeFrame = requestAnimationFrame(() => { resizeFrame = 0; schedulePaint(); });
    }
  });
  observer.observe(body);
  ctx.signal.addEventListener("abort", () => { observer.disconnect(); cancelAnimationFrame(resizeFrame); }, { once: true });
  const persist = id => ctx.preferences?.patch(page, { topo_group: id });
  select.addEventListener("change", () => {
    if (!prefsReady || disposed || !ctx.current()) return;
    if (select.value === selected) return;
    selected = select.value; touched = true; persist(selected); paint();
  });
  const syncPreferences = () => {
    if (disposed || !ctx.current()) return;
    prefsReady = ctx.preferences?.isLoaded?.() ?? true;
    if (prefsReady && !touched) selected = ctx.preferences?.get(page)?.topo_group || "";
    paint();
  };
  const unsubscribe = ctx.preferences?.subscribe(syncPreferences);
  ctx.signal.addEventListener("abort", () => { disposed = true; unsubscribe?.(); }, { once: true });
  void ctx.preferences?.ready().then(syncPreferences);

  function paint() {
    if (!canView || disposed || !ctx.current()) return;
    const state = getStates().get("probes") || "loading";
    const pairs = state === "denied" ? [] : getProbes()?.pairs || [];
    const loaded = state === "ready";
    select.disabled = !prefsReady || !loaded || !pairs.length;
    const add = tools.querySelector('[data-refresh-key="add-group"]');
    if (add) add.disabled = !loaded;
    // Resolve legacy name-based selections once both authoritative reads succeed.
    // The chain ID survives renames and prevents shared names/addresses merging.
    if (prefsReady && loaded) {
      const saved = selected;
      if (!pairs.some(pair => pair.id === selected)) {
        selected = pairs.find(pair => `named:${pair.group_name}` === saved)?.id || pairs[0]?.id || "";
      }
      if (selected !== saved) { touched = true; persist(selected); }
    }
    const optionSig = JSON.stringify(pairs.map(pair => [pair.id, pair.group_name || pair.name]));
    if (optionSig !== optionsSignature) {
      optionsSignature = optionSig;
      const customButton = select.querySelector("button");
      select.replaceChildren(...(customButton ? [customButton] : []),
        ...(pairs.length ? pairs.map(pair => node("option", { value: pair.id }, pair.group_name || pair.name || "监测分组")) : [node("option", { value: "" }, loaded ? "暂无分组" : "读取中…")]));
    }
    if (select.value !== selected) select.value = selected;
    const pair = prefsReady ? pairs.find(pair => pair.id === selected) : null;
    const message = state === "denied" ? "无链路读取权限" : state === "failed" ? (pairs.length ? "链路刷新失败，保留上次数据" : "链路读取失败") : !prefsReady ? "偏好尚未就绪" : state === "loading" ? "读取中…" : "";
    status.textContent = message;
    const sig = JSON.stringify([graphWidth, state, prefsReady, selected, pair, pair && probeController.busy(pair.id)]);
    if (sig === signature) return;
    signature = sig;
    if (!pair) {
      patch(body, node("div", { className: "a-card-body" }, empty(message || "暂无监测分组")));
      return;
    }
    const raw = pair.nodes?.length >= 2 ? pair.nodes : [pair.node1, pair.node2].filter(Boolean);
    const endpoints = raw.map((ep, i) => ({ ...ep, ...pair.chain?.[i], name: pair.chain?.[i]?.name || ep.name || (i === 0 ? pair.node1_name : i === raw.length - 1 ? pair.node2_name : "") || ep.address }));
    const running = activeStates.has(pair.active_state) || pair.availability?.running;
    const busy = probeController.busy(pair.id);
    const info = groupStatus(pair);
    const operations = canExecute ? actions(
      button(running ? "停止" : "运行", event => void probeController.mutate(pair, event.currentTarget, running ? "stop" : "run"), { disabled: busy || !loaded || (running ? ["stopping", "cleanup_pending"].includes(pair.active_state) : pair.availability?.ready === false), "data-refresh-key": "run-stop" }),
      button(pair.auto_enabled ? "暂停自动" : "开启自动", event => void probeController.mutate(pair, event.currentTarget, "toggle"), { disabled: busy || !loaded, "data-refresh-key": "auto" }),
      button("编辑", () => probeController.open(pair.id, { edit: true }), { disabled: busy || !loaded, "data-refresh-key": "edit" }),
      button("删除", event => void probeController.mutate(pair, event.currentTarget, "delete"), { tone: "danger", disabled: busy || !loaded, "data-refresh-key": "delete" })) : null;
    const hopRows = endpoints.slice(0, -1).map((from, i) => {
      const sample = pair.latest?.hops?.find(hop => hop.hop_index === i);
      const fwd = sample?.forward || (endpoints.length === 2 ? pair.latest?.forward : null);
      const rev = sample?.reverse || (endpoints.length === 2 ? pair.latest?.reverse : null);
      return keyed(`${pair.id}-hop-${i}`, node("div", { className: "a-tools-measurement" },
        node("div", { className: "a-tools-parameters" },
          node("div", { className: "a-stack" }, node("span", { className: "a-field-label" }, "源节点"), endpoint(from)),
          node("div", { className: "a-stack" }, node("span", { className: "a-field-label" }, "目标节点"), endpoint(endpoints[i + 1])),
          direction(fwd, "去程"), pair.bidirectional ? direction(rev, "回程") : null)));
    });
    const summary = node("dl", { className: "a-tools-metrics" },
      node("div", {}, node("dt", {}, "整组状态"), node("dd", {}, badge(info.label, info.tone))),
      node("div", {}, node("dt", {}, "节点 / 跳数"), node("dd", {}, `${endpoints.length} / ${Math.max(0, endpoints.length - 1)}`)),
      node("div", {}, node("dt", {}, "自动探测"), node("dd", {}, pair.auto_enabled ? `${pair.auto_interval_seconds} 秒` : "已暂停")),
      node("div", {}, node("dt", {}, "最近采样"), node("dd", {}, pair.latest?.sampled_at ? new Date(pair.latest.sampled_at).toLocaleString("zh-CN") : "无采样历史")));
    const history = node("div", { className: "a-table-scroll", tabindex: "0", "aria-label": "历史采样" }, node("table", {},
      node("thead", {}, node("tr", {}, ["采样时间", "状态", "去程延迟", "去程丢包", ...(pair.bidirectional ? ["回程延迟", "回程丢包"] : [])].map(label => node("th", { scope: "col" }, label)))),
      node("tbody", {}, (pair.history || []).map(sample => keyed(sample.id || sample.sampled_at, node("tr", {},
        [new Date(sample.sampled_at).toLocaleString("zh-CN"), ({ succeeded: "完成", failed: "失败", canceled: "已停止" })[sample.state] || "已采样", sample.forward?.reached ? ms(sample.forward.avg_ms) : "—", loss(sample.forward?.loss_percent), ...(pair.bidirectional ? [sample.reverse?.reached ? ms(sample.reverse.avg_ms) : "—", loss(sample.reverse?.loss_percent)] : [])].map(value => node("td", {}, value))))))));
    patch(body, node("div", { className: "a-card-body" },
      keyed(`${pair.id}-header`, node("div", { className: "a-filterbar" }, badge(pair.bidirectional ? "双向链路" : "单向链路"), operations)),
      summary,
      endpoints.length >= 2 ? renderChainGraph(pair, endpoints, graphWidth) : null,
      pair.availability?.reason ? keyed("reason", node("div", { className: "a-notice", role: "status" }, pair.availability.reason)) : null,
      pair.latest?.error ? keyed("error", node("div", { className: "a-notice", role: "status" }, pair.latest.error)) : null,
      endpoints.length < 2 ? empty("链路未配置") : node("div", { className: "a-stack", "data-refresh-key": `${pair.id}-route` }, node("div", { className: "a-stack", "aria-label": "逐跳测量" }, hopRows)), isStandalonePage ? node("div", { className: "a-tools-measurement", "data-refresh-key": `${pair.id}-history` }, node("h3", {}, "历史采样"), pair.history?.length ? history : empty("无采样历史")) : null));
  }
  function schedulePaint() {
    if (scheduled || disposed) return;
    scheduled = true;
    queueMicrotask(() => { scheduled = false; paint(); });
  }
  return { visible: Boolean(card), element: card, paint, schedulePaint };
}
