import { bytes, date, status } from "./operations.js";
import { listen, patch, poll } from "./live-dom.js";
import { currentTheme } from "../modules/theme-lifecycle.js";
import {
  aggregateDashboardTrafficDays, dashboardPeriodCurrent, dashboardPeriodState,
  summarizeDashboardNodes, summarizeDashboardTrend, utcMonth, panelMetricUsage,
} from "../modules/dashboard-model.js";
import { metricTrack } from "./metric-track.js";
import { orderNodesBySavedOrder } from "../modules/node-order.js";
import { node, button, actions, stack, section, table, badge, field, muted, link, query, pathID, list } from "./ui.js";

const finite = value => typeof value === "number" && Number.isFinite(value) && value >= 0;
const periodDefinitions = [
  ["today", "今日累计", "UTC 自然日"],
  ["week", "本周累计", "周一至当前 · UTC"],
  ["month", "本月累计", "UTC 自然月"],
];

function responsiveChart(days, signal) {
  const container = node("div", { className: "a-chart-container" });
  let currentDays = days, selectedDay = "", previous = "", width = 720;
  const render = () => {
    const signature = JSON.stringify([Math.round(width), currentDays]);
    if (signature === previous) return;
    previous = signature;
    if (!currentDays.some(day => day.day === selectedDay)) selectedDay = "";
    const fresh = chart(currentDays, Math.max(280, width), selectedDay, day => { selectedDay = day; });
    if (container.firstChild) patch(container.firstChild, fresh); else container.append(fresh);
  };
  render();
  container.updateDays = value => { currentDays = value; render(); };
  const observer = new ResizeObserver(entries => { width = entries[0].contentRect.width; render(); });
  observer.observe(container);
  signal.addEventListener("abort", () => observer.disconnect(), { once: true });
  return container;
}

function chart(days, width, selectedDay, selectDay) {
  const height = 280, left = 68, right = 20, top = 20, bottom = 34;
  const available = day => day.recorded !== false && day.sample_count > 0;
  const max = Math.max(3, ...days.filter(available).flatMap(day => [day.received_bytes, day.sent_bytes]));
  const points = key => days.map((day, index) => [left + index / Math.max(1, days.length - 1) * (width - left - right),
    height - bottom - day[key] / max * (height - top - bottom)]);
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  svg.setAttribute("class", "a-chart");
  svg.setAttribute("role", "group");
  svg.setAttribute("aria-label", "所选月份每日流量；实线为接收，虚线为发送；没有采集记录的日期不连线");
  const add = (tag, attrs, text) => {
    const element = document.createElementNS(svg.namespaceURI, tag);
    Object.entries(attrs).forEach(([key, value]) => element.setAttribute(key, value));
    if (text) element.textContent = text;
    svg.append(element);
    return element;
  };
  for (let index = 0; index < 4; index++) {
    const y = top + index / 3 * (height - top - bottom);
    add("path", { d: `M${left} ${y}H${width - right}`, stroke: "var(--border)", "stroke-dasharray": "3 5" });
    add("text", { x: left - 9, y: y + 4, "text-anchor": "end" }, bytes(max * (1 - index / 3)));
  }
  const xValues = points("received_bytes").map(([x]) => x);
  if (days.length) {
    for (const [key, label, stroke, dash] of [["received_bytes", "接收", "var(--ink)", ""], ["sent_bytes", "发送", "var(--soft-ink)", "6 5"]]) {
      const series = points(key), segments = [];
      let segment = [];
      days.forEach((day, index) => {
        if (available(day)) segment.push(series[index]);
        else if (segment.length) { segments.push(segment); segment = []; }
      });
      if (segment.length) segments.push(segment);
      for (const part of segments) {
        const line = part.map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
        if (!dash && part.length > 1) add("path", { d: `${line}L${part.at(-1)[0]} ${height - bottom}H${part[0][0]}Z`, fill: "var(--tint)", opacity: ".7" });
        add("path", { d: line, stroke, "stroke-width": "2.25", "stroke-linecap": "round", "stroke-linejoin": "round", "stroke-dasharray": dash, fill: "none", "data-series": key, "aria-label": label });
        if (part.length === 1) add("circle", { cx: part[0][0], cy: part[0][1], r: 3.5, fill: stroke });
      }
    }
    days.forEach((day, index) => {
      if (index % 5 === 0 || index === days.length - 1)
        add("text", { x: xValues[index], y: height - 9, "text-anchor": "middle" }, day.day.slice(5));
    });
    days.forEach((day, index) => {
      const start = index ? (xValues[index - 1] + xValues[index]) / 2 : left;
      const end = index === days.length - 1 ? width - right : (xValues[index] + xValues[index + 1]) / 2;
      const sampled = available(day);
      const label = sampled
        ? `${day.day} · UTC · 接收 ${bytes(day.received_bytes)} · 发送 ${bytes(day.sent_bytes)} · 合计 ${bytes(day.received_bytes + day.sent_bytes)}`
        : `${day.day} · UTC · ${day.recorded === false ? "无采集记录" : "采集数据不完整"}`;
      const band = add("rect", { x: start, y: top, width: end - start, height: height - top - bottom,
        fill: "currentColor", "fill-opacity": selectedDay === day.day ? ".045" : "0", "pointer-events": "all",
        "data-chart-day": day.day, "data-refresh-key": `chart-day-${day.day}`, title: label, role: "button",
        "aria-label": label, tabindex: selectedDay === day.day || !selectedDay && index === 0 ? "0" : "-1" });
      const select = owner => {
        selectDay(owner.dataset.chartDay);
        owner.closest("svg").querySelectorAll("[data-chart-day]").forEach(item => {
          item.setAttribute("fill-opacity", item === owner ? ".045" : "0"); item.setAttribute("tabindex", item === owner ? "0" : "-1");
        });
      };
      listen(band, "pointerenter", event => select(event.currentTarget));
      listen(band, "focus", event => select(event.currentTarget));
      listen(band, "pointerleave", event => {
        if (document.activeElement === event.currentTarget) return;
        selectDay(""); event.currentTarget.setAttribute("fill-opacity", "0");
      });
      listen(band, "pointerup", event => {
        event.currentTarget.focus({ preventScroll: true });
        currentTheme("analytics")?.motion?.tooltip?.(event.currentTarget);
      });
      listen(band, "keydown", event => {
        const items = [...event.currentTarget.closest("svg").querySelectorAll("[data-chart-day]")], current = items.indexOf(event.currentTarget);
        const next = ({ ArrowLeft: Math.max(0, current - 1), ArrowRight: Math.min(items.length - 1, current + 1), Home: 0, End: items.length - 1 })[event.key];
        if (next != null) {
          event.preventDefault(); items[next].focus({ preventScroll: true });
          currentTheme("analytics")?.motion?.tooltip?.(items[next]);
        } else if (["Enter", " "].includes(event.key)) { event.preventDefault(); currentTheme("analytics")?.motion?.tooltip?.(event.currentTarget); }
      });
    });
  }
  if (!days.some(available)) add("text", { x: left + (width - left - right) / 2, y: top + (height - top - bottom) / 2, "text-anchor": "middle" }, days.length ? "该时间范围暂无有效采集记录" : "暂无可显示的流量趋势");
  return svg;
}

function directionValues(receive, sent, suffix = "") {
  return node("div", { className: "a-dashboard-directions" },
    node("span", {}, muted("接收"), node("b", {}, receive == null ? "—" : `${bytes(receive)}${suffix}`)),
    node("span", {}, muted("发送"), node("b", {}, sent == null ? "—" : `${bytes(sent)}${suffix}`)));
}

function periodCard(key, title, range, value, state) {
  const usable = state === "ready";
  const message = state === "empty" ? "暂无采集记录" : state === "incomplete" ? "采集数据不完整" : state === "boundary" ? "正在切换统计周期" : state === "loading" ? "正在读取持久统计" : "统计不可用";
  return node("article", { className: "a-dashboard-period", "data-refresh-key": `period-${key}`, "data-state": state },
    node("div", { className: "a-dashboard-period-head" }, node("h3", {}, title), badge("UTC")),
    node("strong", { className: "a-dashboard-period-value" }, usable ? bytes(value.total_bytes) : "—"),
    directionValues(usable ? value.received_bytes : null, usable ? value.sent_bytes : null),
    node("div", { className: "a-dashboard-period-foot" }, muted(value?.start && state !== "boundary" ? `${value.start} 起 · ${range}` : range), !usable && muted(message)));
}

export async function dashboardPage(ctx) {
  let month = ctx.state.data.dashboardMonth || utcMonth();
  const can = permission => ctx.can(permission);
  const readings = new Map(), states = new Map(), requests = new Map(), reading = new Map();

  const periodGrid = node("div", { className: "a-dashboard-periods", "aria-label": "当前日周月累计流量" });
  const periodState = muted("正在读取流量累计");
  periodState.setAttribute("role", "status");
  const liveGrid = node("div", { className: "a-dashboard-live" });
  const businessState = muted("正在读取节点状态");
  businessState.setAttribute("role", "status");
  const overviewCard = section("运行概况", node("div", { className: "a-dashboard-overview" },
    can("traffic.read") && periodGrid, can("traffic.read") && periodState, liveGrid), businessState,
    "当前周期与实时状态分区展示；流量仅统计获授权的持久监控端口记录");
  overviewCard.classList.add("a-dashboard-hero");

  const resourceGrid = node("div", { className: "a-dashboard-resources" });
  const sampling = muted("正在读取资源采样");
  sampling.setAttribute("role", "status");
  const host = can("panel-metrics.read") && section("面板运行资源", node("div", { className: "a-card-body" }, resourceGrid), sampling,
    "控制面实际运行环境；容器部署仅反映容器可见资源");

  const plot = responsiveChart([], ctx.signal);
  const trendSummary = node("div", { className: "a-dashboard-trend-summary" });
  const trafficState = muted("正在读取流量趋势");
  trafficState.setAttribute("role", "status");
  const trafficPeriod = muted("UTC · 每日持久记录");
  const monthField = field("趋势月份", "traffic_month", month, { type: "month", max: utcMonth(), required: true });
  const monthInput = monthField.querySelector("input");
  const detailTable = table(["日期", "采集状态", "接收", "发送", "合计", "接收峰值", "发送峰值"], [], { label: "每日流量明细", paginate: 10, rowKey: row => row[0] });
  const detailPeriod = muted("");
  let trafficDialog;
  const detailsButton = button("流量明细", () => {
    if (trafficDialog?.open) return;
    trafficDialog = ctx.dialog("流量明细", stack(detailPeriod, detailTable, actions(link("流量管理", "#traffic"))), { wide: true });
  });
  const trafficCard = can("traffic.read") && section("流量趋势", node("div", { className: "a-card-body a-dashboard-trend" },
    node("div", { className: "a-dashboard-chart-head" }, trendSummary, trafficPeriod), plot,
    node("div", { className: "a-chart-meta" }, muted("实线 接收 · 虚线 发送 · 断点 无有效采集"), trafficState)),
    actions(monthField, detailsButton), "选择历史月份不会改变首屏的当前日、周、月周期");

  const trafficRanking = node("div", { className: "a-dashboard-ranking", "aria-label": "节点流量排行" });
  const rankingState = muted("正在读取节点排行");
  rankingState.setAttribute("role", "status");
  const rankingCard = can("traffic.read") && section("节点累计排行", node("div", { className: "a-card-body" }, trafficRanking), rankingState,
    "按所选月份的接收与发送合计降序排列");

  const nodeSummary = actions();
  const nodeTable = table(["节点", "运行内核"], [], { label: "节点运行状态", columnWidths: ["72%", "28%"], emptyText: "尚未添加可访问的节点", paginate: 5, rowKey: row => row[0].querySelector("a").getAttribute("href") });
  const nodes = can("agents.read") && section("节点运行状态", stack(node("div", { className: "a-card-body" }, nodeSummary), nodeTable), link("管理节点", "#node-settings"));

  function paintPeriods() {
    if (!can("traffic.read")) return;
    const traffic = readings.get("traffic"), current = traffic?.current_period || {};
    patch(periodGrid, node("div", { className: periodGrid.className, "aria-label": periodGrid.getAttribute("aria-label") },
      periodDefinitions.map(([key, title, range]) => {
        const value = current[key];
        let state = dashboardPeriodState(value);
        if (["ready", "empty", "incomplete"].includes(state) && !dashboardPeriodCurrent(key, value)) state = "boundary";
        if (!traffic && states.get("traffic") === "loading") state = "loading";
        if (states.get("traffic") === "denied") state = "unavailable";
        return periodCard(key, title, range, value, state);
      })));
    const state = states.get("traffic");
    periodState.textContent = state === "denied" ? "流量查看权限已变化，累计数据已清除" : state === "failed" ? (traffic ? "累计刷新失败 · 保留仍处当前周期的记录" : "累计读取失败 · 自动重试中") : state === "loading" && !traffic ? "正在读取持久统计" : !traffic?.current_period ? "当前累计聚合不可用" : "当前周期按 UTC 计算 · 每 30 秒核对持久记录";
  }

  function paintMetrics() {
    const metrics = readings.get("metrics") || {};
    const time = Date.parse(metrics.collected_at), collected = Number.isFinite(time) && time > 0;
    const fresh = collected && Math.abs(Date.now() - time) <= 15000;
    const valid = kind => fresh && panelMetricUsage(metrics, kind) !== null;
    const percent = kind => valid(kind) ? `${panelMetricUsage(metrics, kind).toFixed(1)}%` : "—";
    const capacity = kind => valid(kind) ? `已用 ${bytes(metrics[`${kind}_used_bytes`])} / 总量 ${bytes(metrics[`${kind}_total_bytes`])}` : !fresh && collected ? "上次采样已过期" : "本次采样不可用";
    const networkTime = Date.parse(metrics.network_sampled_at);
    const networkFresh = Number.isFinite(networkTime) && Math.abs(Date.now() - networkTime) <= 15000;
    const network = fresh && networkFresh && metrics.network_available && metrics.network_rate_available === true && finite(metrics.network_rx_bps) && finite(metrics.network_tx_bps);
    const meter = (title, kind, detail) => node("div", { className: "a-dashboard-resource", "data-refresh-key": title },
      node("div", { className: "a-dashboard-resource-head" }, node("h3", {}, title), node("strong", {}, percent(kind))),
      metricTrack(parseFloat(percent(kind)), valid(kind)), muted(detail));
    patch(resourceGrid, node("div", { className: resourceGrid.className },
      meter("CPU 使用率", "cpu", valid("cpu") ? "全部可见逻辑核心平均值" : !fresh && collected ? "上次采样已过期" : "本次采样不可用"),
      meter("内存使用率", "memory", capacity("memory")), meter("根目录磁盘", "disk", capacity("disk")),
      node("div", { className: "a-dashboard-panel-network" },
        node("div", { "data-refresh-key": "网络接收速率" }, muted("网络接收"), node("strong", {}, network ? `${bytes(metrics.network_rx_bps)}/s` : "—")),
        node("div", { "data-refresh-key": "网络发送速率" }, muted("网络发送"), node("strong", {}, network ? `${bytes(metrics.network_tx_bps)}/s` : "—")))));
    const state = states.get("metrics"), stale = collected && !fresh;
    sampling.textContent = state === "denied" ? "资源查看权限已变化，采样数据已清除" : state === "failed" ? (fresh ? "刷新失败 · 保留最近有效采样" : "资源读取失败 · 自动重试中") : !collected ? "尚无有效采样" : stale ? `采样已过期 · ${date(metrics.collected_at)}` : `最近采样 ${date(metrics.collected_at)} · 后台统一采集`;
  }

  let businessPaint, businessReady, finishBusiness;
  const queueBusiness = () => {
    if (businessPaint != null) return businessReady;
    businessReady = new Promise(resolve => { finishBusiness = resolve; });
    businessPaint = requestAnimationFrame(() => {
      businessPaint = null;
      try { if (ctx.current()) { paintBusiness(); paintPeriods(); } } finally { finishBusiness(); }
    });
    return businessReady;
  };
  ctx.signal.addEventListener("abort", () => { cancelAnimationFrame(businessPaint); finishBusiness?.(); }, { once: true });

  function paintBusiness() {
    const overview = readings.get("overview") || {};
    const agents = orderNodesBySavedOrder(list(readings.get("agents")), readings.get("order")?.ids);
    const knownNodes = readings.has("overview");
    const online = knownNodes && finite(overview.agents_online) ? overview.agents_online : null;
    const count = knownNodes && finite(overview.agents) ? overview.agents : null;
    const offline = online == null || count == null ? null : Math.max(0, count - online);
    const fleet = summarizeDashboardNodes(agents);
    const rateAllowed = can("agents.read") && can("metrics.read");
    const healthTone = count == null || count === 0 ? "" : online === count ? "ok" : online === 0 ? "bad" : "warn";
    patch(liveGrid, node("div", { className: liveGrid.className },
      node("article", { className: "a-dashboard-live-stat", "data-refresh-key": "节点在线状态" },
        node("div", { className: "a-dashboard-live-head" }, node("h3", {}, "节点在线状态"), badge(count == null ? "读取中" : count === 0 ? "暂无节点" : online === count ? "全部在线" : online === 0 ? "全部离线" : "部分离线", healthTone)),
        node("div", { className: "a-dashboard-node-count" }, node("strong", {}, online == null ? "—" : String(online)), muted(`/ ${count == null ? "—" : count} 个节点`)),
        metricTrack(count ? online / count * 100 : 0, count > 0 && online != null, false),
        muted(offline == null ? "正在读取授权节点" : count === 0 ? "当前没有获授权节点" : `离线 ${offline} · 在线与离线按现有心跳判定`)),
      rateAllowed && node("article", { className: "a-dashboard-live-stat", "data-refresh-key": "节点速率合计" },
        node("div", { className: "a-dashboard-live-head" }, node("h3", {}, "节点速率合计"), badge(readings.has("agents") ? `${fleet.sampled} 个有效采样` : "读取中")),
        node("strong", { className: "a-dashboard-rate" }, readings.has("agents") && fleet.sampled ? `${bytes(fleet.receiveBPS + fleet.sendBPS)}/s` : "—"),
        directionValues(readings.has("agents") && fleet.sampled ? fleet.receiveBPS : null, readings.has("agents") && fleet.sampled ? fleet.sendBPS : null, "/s"),
        muted(!readings.has("agents") ? "正在读取实时速率" : fleet.sampled ? `在线节点 ${online ?? "—"} · 仅计入 15 秒内的有效网络采样` : "暂无有效的在线节点采样；离线、过期和异常采样不计入"))));
    const failed = [...states].filter(([key, state]) => ["overview", "agents", "order"].includes(key) && ["failed", "denied"].includes(state));
    businessState.textContent = failed.length ? "部分节点状态不可用 · 自动重试中" : knownNodes ? "节点状态每 5 秒更新" : "正在读取节点状态";
    patch(nodeSummary, actions(...fleet.engines.map(item => badge(`${({ mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "SS Rust" })[item.engine] || item.engine} ${item.running}/${item.installed}`)),
      !fleet.engines.length && muted("暂无内核运行记录")));
    if (readings.has("agents") || states.get("agents") === "denied") nodeTable.updateRows(agents.map(agent => [
      stack(actions(node("a", { href: `#node-settings?agent=${pathID(agent.id)}` }, agent.name), status(agent.status)), muted(`最近心跳 ${date(agent.last_seen)}`)),
      (() => { const current = summarizeDashboardNodes([agent]); return current.installed ? `${current.running} / ${current.installed}` : "—"; })(),
    ]));
  }

  function paintTraffic() {
    const traffic = readings.get("traffic");
    let recordedDays = 0, incomplete = false;
    if (traffic) {
      const today = new Date().toISOString().slice(0, 10);
      const allDays = aggregateDashboardTrafficDays(traffic.days, traffic.month);
      const days = allDays.filter(day => traffic.month < today.slice(0, 7) || day.day <= today);
      const summary = summarizeDashboardTrend(days, traffic.month);
      trafficPeriod.textContent = `${traffic.month} · UTC · 已按日聚合`;
      detailPeriod.textContent = `${traffic.month} · UTC · 仅含获授权监控端口`;
      recordedDays = summary.recordedDays;
      incomplete = days.some(day => day.recorded !== false && !(day.sample_count > 0));
      const hasRecords = summary.sampledDays > 0;
      patch(trendSummary, node("div", { className: trendSummary.className },
        node("div", {}, muted(`${traffic.month} ${summary.recordedDays < summary.elapsedDays ? "已采集合计" : "月合计"}`), node("strong", {}, hasRecords ? bytes(summary.receivedBytes + summary.sentBytes) : "—")),
        node("div", {}, muted("采集覆盖"), node("b", {}, `${summary.recordedDays} / ${summary.elapsedDays} 天`)),
        node("div", {}, muted("峰值日"), node("b", {}, summary.peakDay ? `${summary.peakDay} · ${bytes(summary.peakBytes)}` : "—"))));
      detailTable.updateRows(days.map(day => {
        const usable = day.recorded !== false && day.sample_count > 0;
        return [day.day, usable ? "有效采样" : day.recorded === false ? "无采集记录" : "数据不完整", usable ? bytes(day.received_bytes) : "—", usable ? bytes(day.sent_bytes) : "—", usable ? bytes(day.received_bytes + day.sent_bytes) : "—", usable ? `${bytes(day.peak_receive_bps)}/s` : "—", usable ? `${bytes(day.peak_send_bps)}/s` : "—"];
      }));
      plot.updateDays(days);
    } else if (states.get("traffic") === "denied") {
      trendSummary.replaceChildren(); trafficRanking.replaceChildren(); plot.updateDays([]); detailTable.updateRows([]);
    }
    detailsButton.disabled = !traffic;
    const state = states.get("traffic");
    trafficState.textContent = state === "denied" ? "流量查看权限已变化" : state === "loading" ? "正在读取所选月份" : state === "failed" ? (traffic ? `月份刷新失败 · 仍显示 ${traffic.month}` : "流量读取失败 · 自动重试中") : traffic ? (incomplete ? "部分日期采集数据不完整" : recordedDays ? "每日记录已更新 · 无记录日期保持断点" : "所选月份暂无采集记录") : "尚无流量记录";
    paintPeriods();
    paintRanking();
  }

  function paintRanking() {
    const traffic = readings.get("traffic");
    if (!traffic) {
      if (states.get("traffic") === "denied") trafficRanking.replaceChildren();
      rankingState.textContent = states.get("traffic") === "denied" ? "流量查看权限已变化" : states.get("traffic") === "failed" ? "排行读取失败 · 自动重试中" : "正在读取节点排行";
      return;
    }
    const agents = list(readings.get("agents"));
    const source = list(traffic.nodes), invalid = source.some(entry => !entry.agent_id || ![entry.received_bytes, entry.sent_bytes, entry.total_bytes].every(finite));
    const ranked = source.filter(entry => entry.agent_id && [entry.received_bytes, entry.sent_bytes, entry.total_bytes].every(finite))
      .map(entry => ({ ...entry, name: entry.name || agents.find(agent => agent.id === entry.agent_id)?.name || "未命名节点" }))
      .sort((left, right) => right.total_bytes - left.total_bytes || left.agent_id.localeCompare(right.agent_id));
    const max = Math.max(0, ...ranked.map(entry => entry.total_bytes));
    patch(trafficRanking, node("div", { className: trafficRanking.className, "aria-label": "节点流量排行" }, ranked.length ? ranked.slice(0, 8).map((entry, index) =>
      node("article", { className: "a-dashboard-rank", "data-refresh-key": entry.agent_id },
        node("div", { className: "a-dashboard-rank-head" },
          node("span", { className: "a-dashboard-rank-position", "aria-label": `第 ${index + 1} 名` }, String(index + 1).padStart(2, "0")),
          node("a", { href: `#traffic?agent=${pathID(entry.agent_id)}` }, entry.name), node("strong", {}, bytes(entry.total_bytes))),
        metricTrack(max ? entry.total_bytes / max * 100 : 0, true, false),
        node("div", { className: "a-dashboard-rank-meta" }, muted(`接收 ${bytes(entry.received_bytes)} · 发送 ${bytes(entry.sent_bytes)}`), entry.sample_count > 0 ? muted(`${entry.sample_count} 次采样`) : muted("采集数据不完整")))) : muted("所选月份暂无按节点归集的采集记录")));
    const state = states.get("traffic");
    rankingState.textContent = state === "failed" ? `刷新失败 · 保留 ${traffic.month} 排行` : invalid ? "部分排行记录格式无效，未纳入展示" : ranked.length > 8 ? `${traffic.month} · 共 ${ranked.length} 个节点，显示前 8 个` : `${traffic.month} · ${ranked.length} 个有采集记录的节点`;
  }

  function read(key, path, force = false) {
    if (reading.has(key) && !force) return reading.get(key);
    const work = readResource(key, path, force);
    reading.set(key, work);
    void work.finally(() => { if (reading.get(key) === work) reading.delete(key); }).catch(() => {});
    return work;
  }

  async function readResource(key, path, force = false) {
    if (!ctx.current() || requests.has(key) && !force) return;
    requests.get(key)?.abort();
    const controller = new AbortController();
    requests.set(key, controller);
    if (!readings.has(key) || force) {
      states.set(key, "loading");
      if (key === "traffic") paintTraffic();
    }
    const requestedMonth = month;
    try {
      const value = await ctx.api(path, { signal: AbortSignal.any([ctx.signal, controller.signal]) });
      if (!ctx.current() || requests.get(key) !== controller) return;
      if (key === "traffic" && (value.month !== requestedMonth || value.timezone !== "UTC")) throw new Error("流量统计周期不匹配");
      readings.set(key, value); states.set(key, "ready");
    } catch (error) {
      if (!ctx.current() || requests.get(key) !== controller || error.name === "AbortError") return;
      if (error.status === 403) { readings.delete(key); states.set(key, "denied"); }
      else states.set(key, "failed");
    } finally {
      if (ctx.current() && requests.get(key) === controller) {
        requests.delete(key);
        if (key === "metrics") paintMetrics();
        else if (key === "traffic") paintTraffic();
        else await queueBusiness();
      }
    }
  }

  const trafficPath = () => query("/traffic-usage", { month, view: "dashboard" });
  function refreshLive() {
    const sources = [["overview", "/overview"],
      ...(can("agents.read") ? [["agents", "/agents"], ["order", "/preferences/node-order"]] : []),
      ...(can("panel-metrics.read") ? [["metrics", "/panel-metrics"]] : [])];
    return Promise.all(sources.map(([key, path]) => read(key, path)));
  }
  function refreshTraffic() { return can("traffic.read") ? read("traffic", trafficPath()) : Promise.resolve(); }
  function refresh() { return Promise.all([refreshLive(), refreshTraffic()]); }

  monthInput.addEventListener("change", () => {
    if (!monthInput.checkValidity()) return;
    month = monthInput.value;
    ctx.state.data.dashboardMonth = month;
    void read("traffic", trafficPath(), true);
  });
  ctx.onRefresh(refresh);
  ctx.signal.addEventListener("abort", () => { requests.forEach(controller => controller.abort()); trafficDialog?.close(); }, { once: true });
  paintPeriods(); paintMetrics(); paintBusiness(); paintTraffic();
  void refresh();
  poll(ctx, refreshLive, 5000);
  poll(ctx, refreshTraffic, 30000);
  return node("div", { className: "a-stack a-dashboard" }, overviewCard,
    node("div", { className: "a-dashboard-main" }, trafficCard, host),
    node("div", { className: "a-dashboard-secondary" }, rankingCard, nodes));
}
