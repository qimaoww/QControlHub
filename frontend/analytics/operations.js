import { bindPreferenceFields } from "./preferences.js";
import { readRegion, loadingTable, dialogContext, preparedDialog } from "./loading.js";
import { quotaInvitations } from "./invitations.js";
import { listen, patch, poll } from "./live-dom.js";
import { currentTheme } from '../modules/theme-lifecycle.js';
import { formatDate } from "./appearance.js";
import { bindSourceEditorTools } from "./source-editor.js";
import { readableConfigSource } from '../modules/code-format.js';
import { editAgentSharing } from "./sharing.js";
import { aggregateDashboardTrafficDays, mergePanelMetricsHistory, summarizeDashboardNodes, summarizeDashboardTraffic, summarizeDashboardTrafficPeaks, utcMonth } from "../modules/dashboard-model.js";
import { metricTrack } from './metric-track.js';
import { orderNodesBySavedOrder, readOrderedNodes } from "../modules/node-order.js";
import { diagnosticError, redactDiagnostic } from "../modules/errors.js";
import { coreSourceLabel, taskRetryState, taskTiming } from "../modules/task-model.js";
import { autoFilter, filterbar, node, button, actions, stack, grid, section, table, badge, field, code, muted, link, query, pathID, list, notice, copyText } from "./ui.js";

const byteFormatters = [0, 1].map(maximumFractionDigits => new Intl.NumberFormat("zh-CN", { maximumFractionDigits }));
export const bytes = value => {
  const amount = Math.max(0, Number(value) || 0);
  const exponent = amount ? Math.max(0, Math.min(4, Math.floor(Math.log(amount) / Math.log(1024)))) : 0;
  return `${byteFormatters[exponent ? 1 : 0].format(amount / 1024 ** exponent)}\u00a0${["B", "KB", "MB", "GB", "TB"][exponent]}`;
};
export const date = formatDate;
export const status = value => badge(({ online: "在线", offline: "离线", active: "运行中", inactive: "已停止", unknown: "未知", activating: "启动中", deactivating: "停止中", pending: "准备中", running: "执行中", succeeded: "成功", failed: "失败", canceled: "已取消", accepted: "已接受", rejected: "已拒绝" })[value] || value || "未知",
  ["online", "active", "succeeded", "accepted"].includes(value) ? "ok" : ["pending", "running"].includes(value) ? "warn" : ["offline", "failed"].includes(value) ? "bad" : "");
export const actionName = value => ({
  validate: "校验配置", deploy: "部署并重启", start: "启动服务", stop: "停止服务", restart: "重启服务", status: "查询状态",
  install: "安装内核", uninstall: "卸载内核", "read-config": "读取可导入配置", "read-managed-config": "读取托管配置",
  "panel-failure":"面板失败", "import-existing": "导入现有服务", "upgrade-agent": "升级 Agent", "enable-bbr": "启用 BBR",
  "disable-bbr": "关闭 BBR", "configure-tcp": "配置 TCP", "ip-quality": "IP 质量检测", "network-diagnostic":"网络诊断",
})[value] || value;

function responsiveChart(days, signal) {
  const container = node("div", { className: "a-chart-container" });
  let currentDays = days, selectedDay = '', previous = "", width = Math.max(280, innerWidth - (innerWidth > 896 ? 256 : 0) - 96);
  const render = () => {
    const signature = JSON.stringify([Math.round(width), currentDays]);
    if (signature === previous) return;
    previous = signature;
    if (!currentDays.some(day => day.day === selectedDay)) selectedDay = '';
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
  const height = 224, left = 62, right = 18, top = 16, bottom = 26;
  const max = Math.max(3, ...days.flatMap(day => [day.received_bytes, day.sent_bytes]));
  const points = key => days.map((day, index) => [left + index / Math.max(1, days.length - 1) * (width - left - right),
    height - bottom - day[key] / max * (height - top - bottom)]);
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  svg.setAttribute("class", "a-chart");
  svg.setAttribute("role", "group");
  svg.setAttribute("aria-label", "所选月份每日流量：实线为接收，虚线为发送");
  const add = (tag, attrs, text, parent = svg) => {
    const element = document.createElementNS(svg.namespaceURI, tag);
    Object.entries(attrs).forEach(([key, value]) => element.setAttribute(key, value));
    if (text) element.textContent = text;
    parent.append(element);
    return element;
  };
  for (let i = 0; i < 4; i++) {
    const y = top + i / 3 * (height - top - bottom);
    add("path", { d: `M${left} ${y}H${width - right}`, stroke: i === 3 ? "#d4d4d8" : "#e4e4e7", "stroke-dasharray": i === 3 ? "" : "3 4" });
    add("text", { x: left - 8, y: y + 4, "text-anchor": "end" }, bytes(max * (1 - i / 3)));
  }
  if (days.length) {
    const rxPoints = points('received_bytes');
    const txPoints = points('sent_bytes');
    days.forEach((day, index) => {
      if (index % 5 === 0 || index === days.length - 1) {
        const vx = rxPoints[index][0].toFixed(1);
        add("line", { x1: vx, y1: top, x2: vx, y2: height - bottom, stroke: "#efeff1", "stroke-dasharray": "2 3" });
        add("text", { x: vx, y: height - 6, "text-anchor": "middle" }, day.day.slice(5));
      }
    });
    const defs = add("defs", {});
    const grad = add("linearGradient", { id: "a-chart-rx-grad", x1: "0", y1: "0", x2: "0", y2: "1" }, "", defs);
    add("stop", { offset: "0%", "stop-color": "#18181b", "stop-opacity": "0.12" }, "", grad);
    add("stop", { offset: "100%", "stop-color": "#18181b", "stop-opacity": "0.01" }, "", grad);
    for (const [key, label, stroke, dash] of [['received_bytes', '接收', '#18181b', ''], ['sent_bytes', '发送', '#62626b', '5 4']]) {
      const series = points(key);
      const line = series.map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
      if (!dash) add("path", { d: `${line}L${series.at(-1)[0]} ${height - bottom}H${left}Z`, fill: "url(#a-chart-rx-grad)" });
      add("path", { d: line, stroke, "stroke-width": dash ? "1.6" : "2", 'stroke-dasharray': dash, fill: "none", 'data-series': key, 'aria-label': label });
      if (days.length === 1) add('circle', { cx: series[0][0], cy: series[0][1], r: 3, fill: stroke });
    }
    let peakIdx = 0;
    days.forEach((d, idx) => {
      if ((d.received_bytes + d.sent_bytes) > (days[peakIdx].received_bytes + days[peakIdx].sent_bytes)) peakIdx = idx;
    });
    if (days[peakIdx] && (days[peakIdx].received_bytes + days[peakIdx].sent_bytes) > 0) {
      add("circle", { cx: rxPoints[peakIdx][0].toFixed(1), cy: rxPoints[peakIdx][1].toFixed(1), r: 3.5, fill: "#fff", stroke: "#18181b", "stroke-width": "2" });
    }
    const crosshair = add("line", { class: "a-chart-crosshair", x1: 0, x2: 0, y1: top, y2: height - bottom, opacity: "0" });
    const rxDot = add("circle", { class: "a-chart-dot-rx", cx: 0, cy: 0, r: 3.5, opacity: "0" });
    const txDot = add("circle", { class: "a-chart-dot-tx", cx: 0, cy: 0, r: 3.5, opacity: "0" });
    const updateCrosshair = (idx) => {
      if (idx == null || idx < 0 || idx >= days.length) {
        crosshair.setAttribute("opacity", "0");
        rxDot.setAttribute("opacity", "0");
        txDot.setAttribute("opacity", "0");
        return;
      }
      const x = rxPoints[idx][0].toFixed(1);
      crosshair.setAttribute("x1", x);
      crosshair.setAttribute("x2", x);
      crosshair.setAttribute("opacity", "1");
      rxDot.setAttribute("cx", x);
      rxDot.setAttribute("cy", rxPoints[idx][1].toFixed(1));
      rxDot.setAttribute("opacity", "1");
      txDot.setAttribute("cx", x);
      txDot.setAttribute("cy", txPoints[idx][1].toFixed(1));
      txDot.setAttribute("opacity", "1");
    };
    const initialIdx = selectedDay ? days.findIndex(d => d.day === selectedDay) : -1;
    updateCrosshair(initialIdx);

    const xValues = rxPoints.map(([x]) => x);
    days.forEach((day, index) => {
      const start = index ? (xValues[index - 1] + xValues[index]) / 2 : left;
      const end = index === days.length - 1 ? width - right : (xValues[index] + xValues[index + 1]) / 2;
      const label = `${day.day} · UTC · 接收 ${bytes(day.received_bytes)} · 发送 ${bytes(day.sent_bytes)} · 合计 ${bytes(day.received_bytes + day.sent_bytes)}`;
      const band = add('rect', { x: start, y: top, width: end - start, height: height - top - bottom,
        fill: 'currentColor', 'fill-opacity': selectedDay === day.day ? '.035' : '0', 'pointer-events': 'all',
        'data-chart-day': day.day, 'data-refresh-key': `chart-day-${day.day}`, title: label, role: 'button',
        'aria-label': label, tabindex: selectedDay === day.day || !selectedDay && index === 0 ? '0' : '-1' });
      const select = owner => {
        selectDay(owner.dataset.chartDay);
        updateCrosshair(index);
        owner.closest('svg').querySelectorAll('[data-chart-day]').forEach(item => {
          item.setAttribute('fill-opacity', item === owner ? '.035' : '0'); item.setAttribute('tabindex', item === owner ? '0' : '-1');
        });
      };
      listen(band, 'pointerenter', event => select(event.currentTarget));
      listen(band, 'focus', event => select(event.currentTarget));
      listen(band, 'pointerleave', event => {
        if (document.activeElement === event.currentTarget) return;
        selectDay(''); updateCrosshair(-1); event.currentTarget.setAttribute('fill-opacity', '0');
      });
      listen(band, 'pointerup', event => {
        event.currentTarget.focus({ preventScroll: true });
        currentTheme('analytics')?.motion?.tooltip?.(event.currentTarget);
      });
      listen(band, 'keydown', event => {
        const items = [...event.currentTarget.closest('svg').querySelectorAll('[data-chart-day]')], current = items.indexOf(event.currentTarget);
        const next = ({ ArrowLeft: Math.max(0, current - 1), ArrowRight: Math.min(items.length - 1, current + 1), Home: 0, End: items.length - 1 })[event.key];
        if (next != null) {
          event.preventDefault(); items[next].focus({ preventScroll: true });
          currentTheme('analytics')?.motion?.tooltip?.(items[next]);
        } else if (['Enter', ' '].includes(event.key)) { event.preventDefault(); currentTheme('analytics')?.motion?.tooltip?.(event.currentTarget); }
      });
    });
  }
  return svg;
}

import { createPanelHostController } from "./dashboard-panel.js";
import { createTrafficRankingCard, trafficSplitBar } from "./dashboard-ranking.js";
import { createDashboardProbesSection } from "./dashboard-probes.js";
import { createEdgeTelemetryController, paintNodeTelemetryMatrix } from "./dashboard-telemetry.js";

export async function dashboardPage(ctx) {
  let month = ctx.state.data.dashboardMonth || utcMonth();
  const can = permission => ctx.can(permission);
  const readings = new Map();
  const states = new Map();
  const requests = new Map();
  let panelHistory = [];
  let panelRuntime = null;
  let edgeLastFetch = 0;

  const cmdCell = (title, valueEl, bodyEl, dotClass = "") => node("div", { className: "a-dash-cmd-cell", "data-refresh-key": title },
    node("div", { className: "a-dash-cmd-head" },
      dotClass ? node("span", { className: `a-dash-dot ${dotClass}`, "data-refresh-key": "dot" }) : null,
      node("span", { className: "a-muted", "data-refresh-key": "label" }, title)),
    node("div", { className: "a-dash-cmd-metric", "data-refresh-key": "metric" }, valueEl),
    node("div", { className: "a-dash-cmd-foot", "data-refresh-key": "foot" }, bodyEl));
  const overviewGrid = node("div", { className: "a-dash-cmd-grid" });

  const hostController = can("panel-metrics.read") ? createPanelHostController(ctx, { bytes, date }) : null;
  function paintMetrics() {
    const m = readings.get("metrics") || {};
    const status = states.get("metrics");
    if (status === "denied") panelHistory = [];
    hostController?.paint({ m, history: panelHistory, runtime: panelRuntime, status });
  }

  const host = hostController?.element || false;
  const businessState = muted("读取中…");
  businessState.setAttribute("role", "status");
  const overviewCard = section("运行概况", overviewGrid, businessState);
  const nodeSummary = actions();
  const nodeMatrix = node("div", { className: "a-dash-matrix", "aria-label": "节点运行状态" });
  const nodes = can("agents.read") && section("节点运行状态", nodeMatrix, nodeSummary);

  const plot = responsiveChart([], ctx.signal);
  const trafficSummary = (receive, sent, summary = {}, peaks = {}, trafficMonth = month) => node("div", { className: "a-dash-traffic-ledger" },
    node("div", { className: "a-dash-ledger-cell is-primary", "data-refresh-key": "月累计流量" },
      node("div", { className: "a-dash-ledger-head" }, muted("月累计流量")),
      node("strong", {}, receive == null ? "—" : bytes(receive + sent)),
      node("div", { className: "a-dash-ledger-foot" },
        node("div", { className: "a-dash-ledger-duplex" },
          node("span", { className: "a-dash-rx" }, `↓ ${receive == null ? "—" : bytes(receive)}`),
          node("span", { className: "a-dash-tx" }, `↑ ${sent == null ? "—" : bytes(sent)}`)),
        trafficSplitBar(receive || 0, sent || 0, (receive || 0) + (sent || 0)))),
    node("div", { className: "a-dash-ledger-cell", "data-refresh-key": "UTC 今日" },
      node("div", { className: "a-dash-ledger-head" }, muted(summary.isCurrentMonth === false ? "统计月份" : "UTC 今日")),
      node("strong", {}, summary.isCurrentMonth === false ? trafficMonth : summary.todayBytes == null ? "—" : bytes(summary.todayBytes)),
      node("div", { className: "a-dash-ledger-foot" }, muted(summary.isCurrentMonth === false ? "历史归档" : trafficMonth))),
    node("div", { className: "a-dash-ledger-cell", "data-refresh-key": "日均用量" },
      node("div", { className: "a-dash-ledger-head" }, muted("日均用量")),
      node("strong", {}, summary.averageBytes == null ? "—" : bytes(summary.averageBytes)),
      node("div", { className: "a-dash-ledger-foot" }, muted(summary.elapsedDays ? `共 · ${summary.elapsedDays} 天` : "—"))),
    node("div", { className: "a-dash-ledger-cell", "data-refresh-key": "峰值带宽" },
      node("div", { className: "a-dash-ledger-head" }, muted("日峰值")),
      node("strong", {}, summary.peakDay ? `${bytes(summary.peakBytes)}` : "—"),
      node("div", { className: "a-dash-ledger-foot" },
        summary.peakDay
          ? [
              muted(summary.peakDay),
              node("div", { className: "a-dash-ledger-duplex" },
                node("span", { className: "a-dash-rx" }, `↓ ${bytes(peaks.peakReceiveBPS || 0)}/s`),
                node("span", { className: "a-dash-tx" }, `↑ ${bytes(peaks.peakSendBPS || 0)}/s`)),
            ]
          : muted("—"))),
  );
  const trafficTotals = trafficSummary();
  const trafficState = muted("读取中…");
  trafficState.setAttribute("role", "status");
  const trafficPeriod = badge(`${month} · UTC`);

  const monthField = field("月份", "traffic_month", month, { type: "month", max: utcMonth(), required: true });
  monthField.classList.add("a-dash-inline-field");
  const monthInput = monthField.querySelector("input");
  monthInput.addEventListener("change", () => {
    if (!monthInput.checkValidity()) return;
    month = monthInput.value;
    ctx.state.data.dashboardMonth = month;
    void read("traffic", query("/traffic-usage", { month }), true);
  });
  bindPreferenceFields(ctx, 'dashboard', [monthField], { restore: () => {
    if (!monthInput.checkValidity()) return;
    month = monthInput.value; ctx.state.data.dashboardMonth = month;
    void read('traffic', query('/traffic-usage', { month }), true);
  } });

  const detailTable = table(["日期", "接收", "发送", "合计", "接收峰值", "发送峰值"], [], { label: "每日流量明细", paginate: 10, rowKey: row => row[0] });
  const detailPeriod = muted("");
  let trafficDialog;
  const detailsButton = button("流量明细", () => {
    if (trafficDialog?.open) return;
    trafficDialog = ctx.dialog("流量明细", stack(detailPeriod, detailTable, actions(link("流量管理", "#traffic"))), { wide: true });
  });

  const rankingCard = can("traffic.read") ? createTrafficRankingCard(ctx, {
    bytes,
    getUsage: () => readings.get("traffic"),
    getUsageState: () => states.get("traffic"),
    getPolicies: () => readings.get("trafficPolicies"),
    getPoliciesState: () => states.get("trafficPolicies"),
    getAgents: () => orderNodesBySavedOrder(list(readings.get("agents")), readings.get("order")?.ids),
    getOrderIDs: () => readings.get("order")?.ids || [],
    getMonth: () => month,
  }) : null;

  const trafficCard = can("traffic.read") && section("流量概况", node("div", { className: "a-dash-traffic-wrap" },
    node("div", { className: "a-dash-subbar a-dash-traffic-bar" }, monthField, detailsButton),
    trafficTotals,
    node("div", { className: "a-card-body a-dash-traffic-plot" }, plot,
      node("div", { className: "a-chart-meta" },
        node("span", { className: "a-chart-legend" },
          node("span", { className: "a-chart-key-rx" }),
          muted("接收"),
          node("span", { className: "a-chart-key-tx" }),
          muted("发送")),
        trafficPeriod)),
  ), trafficState);
  trafficCard?.classList?.add("a-dash-traffic-card");

  const probesSection = createDashboardProbesSection(ctx, {
    date,
    getOrderIDs: () => readings.get("order")?.ids || [],
    getProbes: () => readings.get("probes"),
    getProbesState: () => states.get("probes"),
    reloadProbes: () => read("probes", "/dashboard/probes", true),
  });

  const edgeTelemetry = createEdgeTelemetryController(ctx, {
    readEdgeState: key => ({ data: readings.get(key), state: states.get(key) }),
  });

  let businessPaint;
  let businessReady, finishBusiness;
  const queueBusiness = () => {
    if (businessPaint != null) return businessReady;
    businessReady = new Promise(resolve => { finishBusiness = resolve; });
    businessPaint = requestAnimationFrame(() => {
      businessPaint = null;
      try {
        if (ctx.current()) {
          paintBusiness();
          rankingCard?.paint();
          probesSection.paint();
          edgeTelemetry.paint();
        }
      } finally { finishBusiness(); }
    });
    return businessReady;
  };
  ctx.signal.addEventListener('abort', () => { cancelAnimationFrame(businessPaint); finishBusiness?.(); }, { once: true });

  const coreBusinessKeys = new Set(["overview", "agents", "order"]);
  const keyed = (key, el) => {
    if (el?.setAttribute) el.setAttribute("data-refresh-key", key);
    return el;
  };
  let lastOverviewSignature = "";
  function paintBusiness() {
    const overview = readings.get("overview") || {};
    const agents = orderNodesBySavedOrder(list(readings.get("agents")), readings.get("order")?.ids);
    const failed = [...states].filter(([key, state]) => coreBusinessKeys.has(key) && ["failed", "denied"].includes(state));
    const waiting = ["overview", ...(can("agents.read") ? ["agents"] : [])].some(key => !readings.has(key) && (!states.has(key) || states.get(key) === "loading"));
    const nextBusinessText = waiting ? "读取中…" : failed.length ? "部分状态异常" : "";
    const knownNodes = readings.has("overview") || readings.has("agents");
    const online = knownNodes ? overview.agents_online ?? agents.filter(agent => agent.status === "online").length : "—";
    const count = knownNodes ? overview.agents ?? agents.length : "—";
    const fleet = summarizeDashboardNodes(agents);
    const knownRuntime = readings.has('agents');
    const pendingTotal = knownNodes ? Number(overview.tasks_pending || 0) + Number(overview.tasks_queued || 0) : 0;
    const runningTasks = knownNodes ? Number(overview.tasks_running || 0) : 0;
    const failedTasks = knownNodes ? Number(overview.tasks_failed || 0) : 0;
    const activeTasks = knownNodes ? pendingTotal + runningTasks : "—";
    const canAgents = can("agents.read");
    const canMetrics = can("metrics.read");
    const canTasks = can("tasks.read");

    const overviewSig = JSON.stringify([
      nextBusinessText, knownNodes, online, count, fleet, knownRuntime,
      pendingTotal, runningTasks, failedTasks, activeTasks,
      overview.node_configs ?? null, overview.configs ?? null,
      canAgents, canMetrics, canTasks,
    ]);

    if (overviewSig !== lastOverviewSignature) {
      lastOverviewSignature = overviewSig;
      if (businessState.textContent !== nextBusinessText) businessState.textContent = nextBusinessText;
      businessState.hidden = !nextBusinessText;

      const engineChips = knownRuntime && fleet.engines.length
        ? node("div", { className: "a-dash-engine-chips", "data-refresh-key": "chips" }, ...fleet.engines.map(e =>
            node("span", { className: "a-dash-engine-chip", "data-refresh-key": e.engine },
              node("span", { className: `a-dash-dot ${e.running > 0 ? "is-ok" : "is-muted"}` }),
              `${({ mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "SS" })[e.engine] || e.engine} ${e.running}/${e.installed}`)))
        : keyed("empty", muted(knownRuntime ? "未安装内核" : "读取中…"));

      const taskMeta = knownNodes
        ? actions(
            keyed("counts", muted(`运行 ${runningTasks} · 排队 ${pendingTotal}`)),
            failedTasks > 0 && canTasks
              ? node("a", { className: "a-badge bad", href: "#tasks?status=failed", "data-refresh-key": "fail" }, `失败 ${failedTasks}`)
              : keyed("fail", failedTasks > 0 ? badge(`失败 ${failedTasks}`, "bad") : badge("无失败", "ok")),
          )
        : keyed("empty", muted("读取中…"));

      const nodePct = knownNodes && count > 0 ? Math.round((online / count) * 100) : 0;
      const nodeDot = !knownNodes ? "" : online === count && count > 0 ? "is-ok" : online > 0 ? "is-warn" : "is-bad";

      patch(overviewGrid, node("div", { className: overviewGrid.className },
        cmdCell("在线节点",
          node("strong", { className: "a-dash-cmd-val" }, knownNodes ? `${online}/${count}` : "—"),
          stack(
            knownNodes ? keyed("track", metricTrack(nodePct, true, false)) : null,
            keyed("meta", muted(knownNodes ? `在线率 ${nodePct}% · 离线 ${Math.max(0, count - online)}` : "读取中…")),
          ),
          nodeDot),
        canAgents
          ? cmdCell("运行内核",
              actions(
                keyed("running", node("strong", { className: "a-dash-cmd-val" }, knownRuntime ? String(fleet.running) : "—")),
                knownRuntime ? keyed("installed", muted(`/ ${fleet.installed}`)) : null,
              ),
              engineChips,
              knownRuntime ? (fleet.running > 0 ? "is-ok" : "is-muted") : "")
          : cmdCell("节点配置", node("strong", { className: "a-dash-cmd-val" }, String(overview.node_configs ?? "—")), muted("已授权节点配置")),
        cmdCell("调度任务",
          node("strong", { className: "a-dash-cmd-val" }, activeTasks === "—" ? "—" : String(activeTasks)),
          taskMeta,
          failedTasks > 0 ? "is-bad" : runningTasks > 0 ? "is-warn" : knownNodes ? "is-ok" : ""),
        canAgents && canMetrics
          ? cmdCell("全网实时吞吐",
              node("strong", { className: "a-dash-cmd-val" }, fleet.sampled ? `↓ ${bytes(fleet.receiveBPS)}/s` : "—"),
              stack(
                fleet.sampled ? keyed("split", trafficSplitBar(fleet.receiveBPS, fleet.sendBPS, fleet.receiveBPS + fleet.sendBPS)) : null,
                node("div", { className: "a-dash-duplex", "data-refresh-key": "duplex" },
                  node("span", { className: "a-dash-tx" }, fleet.sampled ? `↑ ${bytes(fleet.sendBPS)}/s` : "↑ —"),
                  muted(fleet.sampled ? `${fleet.sampled} 节点` : "无采样")),
              ),
              fleet.sampled ? "is-ok" : "is-muted")
          : cmdCell("配置档案", node("strong", { className: "a-dash-cmd-val" }, String(overview.configs ?? "—")), muted(`节点配置 ${overview.node_configs ?? "—"}`)),
      ));
    }

    if (canAgents) {
      paintNodeTelemetryMatrix({
        ctx,
        nodeSummary,
        matrixHost: nodeMatrix,
        agents,
        agentsReady: readings.has("agents") || states.get("agents") === "denied",
        canMetrics,
        bytes,
        date,
        status,
      });
    }
  }

  let lastTrafficSignature = "";
  function paintTraffic() {
    const traffic = readings.get("traffic");
    const trafficStatus = states.get("traffic") || "";
    const today = new Date().toISOString().slice(0, 10);
    const statusText = trafficStatus === "denied"
      ? "无流量权限"
      : trafficStatus === "loading"
        ? "读取中…"
        : trafficStatus === "failed"
          ? (traffic ? "刷新失败 · 保留已有记录" : "读取失败")
          : traffic ? (list(traffic.days).length ? "" : "暂无记录") : "暂无记录";
    const sig = JSON.stringify([trafficStatus, statusText, Boolean(traffic), traffic ? [traffic.month, today, traffic.days] : null]);
    if (sig === lastTrafficSignature) return;
    lastTrafficSignature = sig;

    if (traffic) {
      const days = aggregateDashboardTrafficDays(traffic.days, traffic.month).filter(day => day.day <= today);
      const summary = summarizeDashboardTraffic(days, traffic.month);
      const peaks = summarizeDashboardTrafficPeaks(days);
      trafficPeriod.textContent = `${traffic.month} · UTC`;
      detailPeriod.textContent = `${traffic.month} · UTC`;
      const receive = days.reduce((sum, day) => sum + day.received_bytes, 0), sent = days.reduce((sum, day) => sum + day.sent_bytes, 0);
      patch(trafficTotals, trafficSummary(receive, sent, summary, peaks, traffic.month));
      detailTable.updateRows(days.map(day => [day.day, bytes(day.received_bytes), bytes(day.sent_bytes), bytes(day.received_bytes + day.sent_bytes), `${bytes(day.peak_receive_bps)}/s`, `${bytes(day.peak_send_bps)}/s`]));
      plot.updateDays(days);
      rankingCard?.paint();
    } else if (trafficStatus === "denied") {
      trafficTotals.replaceChildren(); plot.updateDays([]); detailTable.updateRows([]);
      rankingCard?.paint();
    }
    detailsButton.disabled = !traffic;
    if (trafficState.textContent !== statusText) trafficState.textContent = statusText;
    trafficState.hidden = !statusText;
  }

  const reading = new Map();
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
    if (!readings.has(key)) {
      states.set(key, "loading");
      if (key === "traffic") paintTraffic();
    }
    const requestedMonth = month;
    try {
      const value = await ctx.api(path, { signal: AbortSignal.any([ctx.signal, controller.signal]) });
      if (!ctx.current() || requests.get(key) !== controller) return;
      if (key === "traffic" && value.month !== requestedMonth) throw new Error("流量月份不匹配");
      readings.set(key, value); states.set(key, "ready");
      if (key === "metrics") {
        panelHistory = mergePanelMetricsHistory(value, panelHistory, 60);
        panelRuntime = value?.panel_runtime || null;
      }
    } catch (error) {
      if (!ctx.current() || requests.get(key) !== controller || error.name === "AbortError") return;
      if (error.status === 403) { readings.delete(key); states.set(key, "denied"); }
      else states.set(key, "failed");
    } finally {
      if (ctx.current() && requests.get(key) === controller) {
        requests.delete(key);
        if (key === "metrics") paintMetrics();
        else if (key === "traffic") paintTraffic();
        else if (key === "trafficPolicies") rankingCard?.paint();
        else if (key === "probes") probesSection.paint();
        else if (key === "connections" || key === "certificates" || key === "ipQuality") edgeTelemetry.paint();
        else await queueBusiness();
      }
    }
  }

  function refresh(manual = false) {
    const shouldPoll = (key) => manual || states.get(key) !== "denied";
    const sources = [
      ["overview", "/overview"],
      ...(can("agents.read") ? [["agents", "/agents"], ["order", "/preferences/node-order"]] : []),
      ...(can("panel-metrics.read") && shouldPoll("metrics") ? [["metrics", "/panel-metrics"]] : []),
      ...(can("traffic.read") && shouldPoll("traffic") ? [["traffic", query("/traffic-usage", { month })]] : []),
      ...(can("traffic.read") && shouldPoll("trafficPolicies") ? [["trafficPolicies", "/traffic-policies?view=page"]] : []),
      ...(can("agents.read") && can("tasks.read") && shouldPoll("probes") ? [["probes", "/dashboard/probes"]] : []),
    ];
    const now = Date.now();
    if (manual || !edgeLastFetch || now - edgeLastFetch >= 30000) {
      edgeLastFetch = now;
      if (can("core-logs.read") && shouldPoll("connections")) sources.push(["connections", "/client-connections?limit=1&timeline=true"]);
      if (can("certificates.read") && shouldPoll("certificates")) sources.push(["certificates", "/certificates"]);
      if (can("agents.read") && shouldPoll("ipQuality")) sources.push(["ipQuality", "/ip-quality"]);
    }
    return Promise.all(sources.map(([key, path]) => read(key, path)));
  }

  ctx.onRefresh(() => refresh(true));
  ctx.signal.addEventListener("abort", () => { requests.forEach(controller => controller.abort()); trafficDialog?.close(); }, { once: true });
  paintMetrics(); paintTraffic(); paintBusiness();
  probesSection.paint(); edgeTelemetry.paint();
  void refresh(true);
  poll(ctx, () => refresh(false), () => {
    const pairs = readings.get("probes")?.pairs;
    return Array.isArray(pairs) && pairs.some(p => p.availability?.running || ["preparing", "running", "stopping"].includes(p.active_state)) ? 2000 : 5000;
  });
  const bentoRow = (className, ...items) => {
    const visible = items.filter(Boolean);
    return visible.length
      ? node("div", { className: visible.length === 1 ? `${className} is-single` : className }, ...visible)
      : null;
  };

  return node("div", { className: "a-dash-deck" },
    overviewCard,
    bentoRow("a-dash-bento-primary", host, trafficCard),
    probesSection.element,
    rankingCard?.element,
    nodes,
    edgeTelemetry.visible && edgeTelemetry.element,
  );
}

function taskEngine(task) {
  if (task.action === 'network-diagnostic') return '节点网络';
  if (task.action === 'panel-failure') return '面板';
  if (task.action === 'upgrade-agent') return 'QAgent';
  if (task.action === 'ip-quality') return 'IPQuality';
  if (['enable-bbr', 'disable-bbr', 'configure-tcp'].includes(task.action)) return 'BBR / TCP';
  return task.engine;
}

function taskContext(ctx, task, targetAgent) {
  if (task.action === "network-diagnostic") return stack(muted(`节点网络测量 · ${date(task.created_at)}`),link("查看测量结果", `#network-tools?measurement=${pathID(task.id)}`));
  if (task.action === "panel-failure") return stack(muted(`面板操作 · ${date(task.created_at)}`),muted("未进入 Agent 执行"));
  const agent = targetAgent?.id === task.agent_id ? targetAgent : ctx.can('agents.read') && ctx.state.data.taskAgents?.find(agent => agent.id === task.agent_id);
  const source = coreSourceLabel(task.engine, task.core_version, task.core_source);
  return stack(muted(`节点 ${agent?.name || task.agent_id} · ${date(task.created_at)}`),
    muted(task.attempt ? `第 ${task.attempt} 次执行` : '尚未开始'),
    muted(`${task.engine || '来源未返回内核'} · ${task.shared_instance ? `共享实例 ${task.shared_traffic_id || '来源未返回实例标识'}` : '主实例'}`),
    !task.config_id && task.core_version && muted(`内核版本 ${task.core_version}${source ? ` · ${source}` : ''}`),
    task.install_if_missing && muted('缺少时安装稳定版；已有内核保持版本'));
}

export function taskDetail(ctx, taskID, {title = "任务详情", targetAgent} = {}) {
  if (!ctx.can("tasks.read")) return;
  let task = {id:taskID,status:"loading",error:"",output:""};
  let diagnosticSignature = "";
  if (!ctx.current()) return;
  const state = muted("正在读取任务…"), failure = notice(diagnosticError(task.error || ""));
  const output = code("");
  output.hidden = !output.textContent;
  const diagnosis = stack();
  const previousAttempts = actions();
  const historyDialogs = new Set();
  let diagnosisSignature;
  function paintDiagnosis() {
    const value = taskRetryState(task).diagnostic;
    const signature = JSON.stringify(value);
    if (signature === diagnosisSignature) return;
    diagnosisSignature = signature;
    patch(diagnosis, stack(value && ["title", "advice", "rollback"].map(key => value[key] && muted(redactDiagnostic(value[key])))));
    diagnosis.hidden = !value;
  }
  paintDiagnosis();
  const timing = muted(taskTiming(task));
  const context = stack(table(['项目', '内容'], [['节点', '正在读取…'], ['执行对象', '正在读取…'], ['创建时间', '正在读取…']], { label: '任务信息', paginate: 0 }));
  const engineLabel = badge(taskEngine(task)), actionLabel = badge(actionName(task.action));
  const heading = actions(state, engineLabel, actionLabel);
  const errorSummary = () => {
    const text=(task.action === "panel-failure" ? `面板操作失败：${redactDiagnostic(task.error || "来源未返回具体原因")}` : diagnosticError(task.error || "")).split("\n")[0];
    return text.slice(0,300)+(text.length>300 ? "…" : "");
  };
  const content = stack(heading,
    context, timing, failure, diagnosis, previousAttempts, output);
  const box = ctx.dialog(title, content, { wide: true });
  box.classList.add("a-task-dialog");
  let timer, reading = false;
  const controller = new AbortController();
  const signal = AbortSignal.any([ctx.signal, controller.signal]);
  const stop = () => { clearTimeout(timer); controller.abort(); };
  const active = () => ctx.current() && box.open;
  const refresh = button("刷新结果", () => void initialRead.load());
  refresh.addEventListener('mutationend', () => { refresh.disabled = task.status === 'denied'; });
  content.insertBefore(actions(refresh,button("复制诊断",event=>void ctx.mutate(event.currentTarget,async()=>{
    await copyText([taskID, failure.textContent, output.textContent].filter(Boolean).join("\n\n"));
    ctx.announce("诊断已复制");
  }))), output);
  async function read({ automatic = false } = {}) {
    if (reading || !active()) return;
    reading = true;
    try {
      // Completed output is immutable until its task metadata changes. Keep
      // independently authorizing this resource without transferring its log.
      const terminal = ['succeeded', 'failed', 'canceled'].includes(task.status);
      if (automatic && terminal) {
        const state = await ctx.api(`/tasks/${pathID(taskID)}?view=status`, { signal });
        if (!active()) return;
        if (diagnosticSignature && ['status', 'attempt', 'finished_at', 'error'].every(key => state[key] === task[key])) {
          failure.textContent = errorSummary();
          return;
        }
      }
      const latest = await ctx.api(`/tasks/${pathID(taskID)}`, { signal });
      if (!active()) return;
      task = latest;
      engineLabel.textContent = taskEngine(task);
      actionLabel.textContent = actionName(task.action);
      timing.textContent = taskTiming(task);
      patch(context, taskContext(ctx, task, targetAgent));
      paintDiagnosis();
      const next = status(task.status);
      if (state.textContent !== next.textContent) { state.textContent = next.textContent; state.className = next.className; }
      const error = errorSummary();
      if (failure.textContent !== error) failure.textContent = error;
      let text = redactDiagnostic(task.output);
      const signature = JSON.stringify([task.status,task.attempt,task.finished_at]);
      if (["succeeded","failed","canceled"].includes(task.status) && diagnosticSignature !== signature) {
        const diagnostic = await ctx.api(`/tasks/${pathID(taskID)}/diagnostics`, {signal});
        if (!active()) return;
        const metadata = diagnostic.attempts || [];
        const attempts = [{...metadata.find(item=>item.attempt===task.attempt),attempt:task.attempt,error:diagnostic.error,output:diagnostic.output,source:diagnostic.source}];
        previousAttempts.replaceChildren(...metadata.filter(item=>item.attempt!==task.attempt).map(item=>button(`查看第 ${item.attempt} 次记录`, event=>void ctx.mutate(event.currentTarget,async()=>{
          const previous=await ctx.api(`/tasks/${pathID(taskID)}/diagnostics?attempt=${item.attempt}`,{signal});
          if (!active()) return;
          const detail=[`领取次数：${item.attempt}`,`来源：${previous.source}`,previous.error,previous.output].filter(Boolean).join("\n\n");
          const history=ctx.dialog(`任务记录 · 第 ${item.attempt} 次`,stack(code(detail),actions(button("复制诊断",event=>void ctx.mutate(event.currentTarget,()=>copyText(detail))))),{wide:true});
          history.classList.add('a-task-dialog');
          historyDialogs.add(history);
          history.addEventListener('close',()=>historyDialogs.delete(history),{once:true});
          box.addEventListener('close',()=>history.close(),{once:true});
        }))));
        text = [diagnostic.occurrences>1 && `发生次数：${diagnostic.occurrences} · 最近发生：${date(diagnostic.last_failure_at)}`,
          diagnostic.retry_of && `重试来源：${diagnostic.retry_of}`,
          ...attempts.map(item => [task.action!=="panel-failure" && `执行尝试：${item.attempt}`,
            item.executed_attempt>0 && item.executed_attempt!==item.attempt && `Agent 返回此前第 ${item.executed_attempt} 次领取时的执行结果；本次为重传核对。`,
            item.source==="legacy-agent" && "历史或旧 Agent 记录：来源未提供完整性信息；已丢失的内容无法恢复。",
            item.error && `错误详情\n${redactDiagnostic(item.error)}`,item.output && `相关输出\n${redactDiagnostic(item.output)}`].filter(Boolean).join("\n\n"))].filter(Boolean).join("\n\n");
        diagnosticSignature = signature;
      } else if (diagnosticSignature === signature) text = output.textContent;
      if (output.textContent !== text) {
        const follow = output.scrollHeight-output.scrollTop-output.clientHeight<24;
        const selection = getSelection(), previous = output.firstChild;
        const selected = previous?.nodeType === Node.TEXT_NODE && selection?.anchorNode === previous && selection.focusNode === previous
          ? [selection.anchorOffset, selection.focusOffset] : null;
        // A completed receipt adds diagnostic context before the live output.
        // Keep the selected output text selected when that prefix is inserted.
        const selectionShift = selected && previous.data ? Math.max(0, text.indexOf(previous.data)) : 0;
        if (previous?.nodeType === Node.TEXT_NODE) previous.data = text;
        else output.textContent = text;
        if (selected && output.firstChild) selection.setBaseAndExtent(output.firstChild, Math.min(selected[0] + selectionShift, text.length), output.firstChild, Math.min(selected[1] + selectionShift, text.length));
        output.hidden = !text;
        if(follow)output.scrollTop=output.scrollHeight;
      }
    } catch (error) {
      if (active() && [403, 404].includes(error.status)) {
        for (const history of historyDialogs) history.close();
        previousAttempts.replaceChildren();
        output.textContent = ''; output.hidden = true; state.textContent = '无权读取';
        timing.textContent = '';
        context.replaceChildren();
        heading.replaceChildren(state);
        failure.textContent = '无权读取此任务。';
        task = { status: 'denied' }; refresh.disabled = true; stop();
        paintDiagnosis();
      }
      throw error;
    } finally { reading = false; }
  }
  async function poll() {
    try { if (!document.hidden && active()) await read({ automatic: true }); }
    catch (error) { if (active() && error.name !== "AbortError") failure.textContent = error.message; }
    finally { if (active() && task.status !== 'denied') timer=setTimeout(poll,2000); }
  }
  box.addEventListener("close", () => { stop(); ctx.signal.removeEventListener("abort", stop); }, {once:true});
  ctx.signal.addEventListener("abort", stop, {once:true});
  const initialRead = readRegion(dialogContext(ctx, box), '任务详情', () => read(), () => {});
  content.prepend(initialRead.element);
  void initialRead.load().finally(() => { if (active() && task.status !== 'denied') timer=setTimeout(poll,2000); });
}

export async function tasksPage(ctx) {
  const pageSize = ctx.state.data.settings?.task_page_size;
  const filters = ctx.state.data.taskFilters || { limit: String([50, 100, 500].includes(pageSize) ? pageSize : 100) };
  let settingsLoadedAt = ctx.state.data.settings ? Date.now() : -Infinity;
  let preferencesAdopted = Boolean(ctx.state.data.settings || ctx.state.data.taskFilters), queryTouched = false;
  const pollDelay = () => {
    const value = ctx.state.data.settings?.task_poll_interval_ms;
    return [600, 1000, 2000, 5000].includes(value) ? value : 1000;
  };
  let readSequence = 0, filtering = false, tasksLoaded = false;
  let agents = [], agentsLoadedAt = -Infinity, lastTasks = [], agentsPending, agentsVerified = false;
  const agentField = field('节点', 'agent_id', filters.agent_id || '', { options: [['', '全部节点'], ...(filters.agent_id ? [[filters.agent_id, filters.agent_id]] : [])] });
  const agentPicker = agentField.querySelector('select');
  let agentSignature;
  function syncAgents() {
    let selected = agentPicker.value;
    const choices = ctx.can('agents.read') ? agents : [...new Set(lastTasks.map(task => task.agent_id))].map(id => ({ id, name: id }));
    if (selected && agentsVerified && !choices.some(agent => agent.id === selected)) {
      selected = ''; agentPicker.value = ''; preferenceFields.save({ agent_id: '' });
      queueMicrotask(() => { if (ctx.current()) filter.requestSubmit(); });
    }
    const options = [['', '全部节点'], ...choices.map(agent => [agent.id, agent.name])];
    if (selected && !choices.some(agent => agent.id === selected)) options.push([selected, selected]);
    const signature = JSON.stringify(options);
    if (signature === agentSignature || agentPicker.matches(':open') && ctx.can('agents.read') && agents.length) return;
    agentPicker.replaceChildren(agentPicker.querySelector('button'), ...options.map(([value, label]) => node('option', { value }, label)));
    agentPicker.value = selected;
    agentSignature = signature;
  }
  const filter = autoFilter(ctx, [
    node("div", { className: "a-form-grid" },
      field("状态", "status", filters.status || "", { options: [["", "全部"], ["pending", "准备中"], ["running", "执行中"], ["succeeded", "成功"], ["failed", "失败"], ["canceled", "已取消"]] }),
      agentField,
      field("操作", "action", filters.action || "", { options: [["", "全部操作"], ...["validate", "deploy", "install", "uninstall", "start", "stop", "restart", "status", "upgrade-agent", "network-diagnostic", "ip-quality", "enable-bbr", "disable-bbr", "configure-tcp", "panel-failure"].map(action => [action, actionName(action)])] }),
      field("条数", "limit", filters.limit || "100", { type: "number", min: 1, max: 500 })),
  ], async (values, _, request) => {
    const sequence = ++readSequence; filtering = true;
    try {
      const tasks = await request.api(query("/tasks", values));
      if (!request.current() || sequence !== readSequence) return;
      ctx.state.data.taskFilters = values; draw(tasks);
    } finally { if (sequence === readSequence) filtering = false; }
  }, { status: "", agent_id: "", action: "", limit: "100" });
  const preferenceFields = bindPreferenceFields(ctx, 'tasks', [filter], { restore: () => {
    preferencesAdopted = true; queryTouched = true;
    ctx.state.data.taskFilters = Object.fromEntries(new FormData(filter));
    filter.requestSubmit();
  } });
  filter.addEventListener('reset', () => queueMicrotask(() => preferenceFields.save(Object.fromEntries(new FormData(filter)))));
  filter.addEventListener('input', () => { queryTouched = true; });
  filter.addEventListener('change', () => { queryTouched = true; });
  const results = node("div");
  const taskTable = table(["操作", "节点", "执行对象", "状态", "创建时间", "操作"], [], { label: "任务", rowKey: row => row.taskID });
  results.append(taskTable);
  const refreshState = muted('正在实时更新'); refreshState.setAttribute('role', 'status');
  function editRetry(task) {
    return preparedDialog(ctx, '编辑后重试', ['当前配置'], local => editRetryLoaded(local, task, ctx), { submit: '保存并重试' });
  }
  async function editRetryLoaded(ctx, task, parentContext) {
    const resource = await ctx.api(`/tasks/${pathID(task.id)}/retry-editor`);
    if (!ctx.current()) return;
    const { config } = resource;
    config.content = readableConfigSource(config.content, config.engine);
    const form = ctx.form([
      muted(`${config.name} · ${date(config.updated_at)}`),
      task.error && notice(diagnosticError(task.error)),
      config.agent_id && actions(link("在实时配置中编辑", `#live-config?agent=${pathID(config.agent_id)}&engine=${pathID(config.engine)}`)),
      node("div", { className: "a-editor" }, field("当前配置", "content", config.content, { multiline: 18, required: true, spellcheck: false })),
    ], `保存并重试${task.action === "deploy" ? "部署" : "校验"}`, async values => {
      if (!await ctx.confirm("保存当前修改并重新执行任务？部署会重启对应内核。", "保存并重试") || !ctx.current()) return;
      const result = await ctx.api(`/tasks/${pathID(task.id)}/edit-retry`, { method: "POST", body: { content: values.content, expected_version: config.version } });
      if (!ctx.current()) return;
      ctx.setDirty(false); box.close(); ctx.announce("配置已保存，重试任务已提交");
      await taskDetail(parentContext, result.task.id);
    });
    const syncDraft = () => ctx.setDirty(form.elements.content.value !== config.content);
    form.addEventListener("input", syncDraft);
    bindSourceEditorTools(ctx, form, () => config.engine, () => config.content, syncDraft, true);
    const box = ctx.dialog("编辑后重试", form, { wide: true });
    box.classList.add("a-task-dialog");
    box.onRequestClose = async () => {
      if (form.elements.content.value !== config.content && !await ctx.confirm("放弃未保存的配置修改？", "放弃更改") || !ctx.current()) return;
      ctx.setDirty(false); box.close();
    };
  }
  function draw(tasks) {
    lastTasks = list(tasks); syncAgents();
    taskTable.updateRows(lastTasks.map(task => Object.assign(() => {
      const retryState = taskRetryState(task);
      return [
        actionName(task.action), agents.find(agent => agent.id === task.agent_id)?.name || task.agent_id || (task.action === "network-diagnostic" ? "节点网络" : "面板"), taskEngine(task), stack(actions(status(task.status)), muted(taskTiming(task)), retryState.missingConfigVersion && muted("缺少配置历史关联")), date(task.created_at),
        node('div', { className: 'a-actions', 'data-task-id': task.id }, button("详情", () => void ctx.mutate(null, () => taskDetail(ctx, task.id))),
          ctx.can("tasks.execute") && retryState.canRetry && button(task.config_id ? "重试配置" : "重试任务", async event => {
            const control = event.currentTarget;
            if (!await ctx.confirm(task.config_id ? `确定重试原任务关联配置？${task.install_if_missing ? "仅缺少内核时安装稳定版，已安装的内核不会切换版本。" : ""}配置已有更新时会拒绝本次重试，请选择编辑后重试或重新部署。` : "重新执行此任务？请先核对原任务结果。", "重试任务") || !ctx.current()) return;
            await ctx.mutate(control, async () => {
              const retry = await ctx.api(`/tasks/${pathID(task.id)}/retry`, { method: "POST", body: task.config_id ? { expected_config_version: task.config_version } : {} });
              ctx.announce("已提交重试任务");
              await taskDetail(ctx, retry.id);
            });
          }, { "data-retry": task.id }),
          ctx.can("tasks.execute") && retryState.canEditRetry &&
            (ctx.can("agent-config.write") || ctx.can("configs.write")) && button("编辑后重试", () => editRetry(task)),
          ctx.can("tasks.execute") && ["pending", "running"].includes(task.status) && button("取消", async event => {
            const control = event.currentTarget;
            if (!await ctx.confirm("取消此任务？已在节点执行的操作可能仍需核实结果。", "取消任务") || !ctx.current()) return;
            await ctx.mutate(control, async () => {
              await ctx.api(`/tasks/${pathID(task.id)}`, { method: "DELETE" });
              ctx.announce("已提交取消请求");
              draw(await ctx.api(query("/tasks", ctx.state.data.taskFilters || filters)));
            });
          }, { tone: "danger" })),
      ];
    }, { taskID: task.id })));
  }
  async function refresh() {
    if (filtering) return;
    const sequence = ++readSequence;
    try {
      const updateAgents = async () => {
        if (!ctx.can('agents.read')) { agents = []; agentsVerified = false; delete ctx.state.data.taskAgents; return; }
        if (Date.now() - agentsLoadedAt < 30000) return;
        if (agentsPending) return agentsPending;
        agentsPending = (async () => {
        try {
          const latest = await readOrderedNodes(ctx.api);
          if (!ctx.current()) return;
          agents = latest; agentsVerified = true; ctx.state.data.taskAgents = agents; syncAgents(); if (lastTasks.length) draw(lastTasks);
        } catch (error) {
          if (error.name === 'AbortError') throw error;
          if (!ctx.current()) return;
          if (error.status === 403) { agents = []; agentsVerified = false; delete ctx.state.data.taskAgents; }
        }
        agentsLoadedAt = Date.now();
        })().finally(() => { agentsPending = null; });
        return agentsPending;
      };
      const updateSettings = async () => {
        if (!ctx.can('settings.read') || Date.now() - settingsLoadedAt < 30000) return;
        try {
          const settings = await ctx.api('/settings');
          if (!ctx.current()) return;
          if (!preferencesAdopted) {
            preferencesAdopted = true;
            if (!queryTouched && !ctx.state.data.taskFilters && [50, 100, 500].includes(settings.task_page_size)) {
              filters.limit = filter.elements.limit.value = String(settings.task_page_size);
              filter.requestSubmit();
            }
          }
        }
        catch (error) { if (error.name === 'AbortError') throw error; }
        if (ctx.current()) settingsLoadedAt = Date.now();
      };
      void updateAgents().catch(() => {});
      void updateSettings().catch(() => {});
      const tasks = await ctx.api(query('/tasks', ctx.state.data.taskFilters || filters));
      if (!ctx.current() || sequence !== readSequence) return;
      draw(tasks); tasksLoaded = true; refreshState.textContent = '正在实时更新';
    } catch (error) {
      if (ctx.current() && sequence === readSequence) {
        if (error.status === 403) { draw([]); refreshState.textContent = '任务访问权限已变化'; }
        else refreshState.textContent = tasksLoaded ? '更新失败，保留上次数据' : '任务读取失败，请重试';
      }
      throw error;
    }
  }
  taskTable.setLoading(true);
  const initialRead = readRegion(ctx, '任务', refresh, () => {}, {view:taskTable});
  void initialRead.load();
  ctx.onRefresh(initialRead.load);
  poll(ctx, refresh, pollDelay);
  return stack(filterbar(filter), initialRead.element, refreshState, section(null, results));
}

export async function quotaPage(ctx) {
  const invitations = quotaInvitations(ctx);
  const managedTable = table(["节点", "状态", "操作"], [], { label: "可管理的共享 Agent", rowKey: row => row[2].dataset.sharingAgent });
  const management = section("共享 Agent 设置", managedTable);
  let sequence = 0;
  async function readManaged() {
    const expected = ++sequence;
    const managed = ctx.can("agents.read") ? (await readOrderedNodes(ctx.api)).filter(agent => agent.can_manage !== false && ctx.can("agents.manage", agent)) : [];
    if (!ctx.current() || sequence !== expected) return;
    managedTable.updateRows(managed.map(agent => [node("strong", {}, agent.name), status(agent.status),
      button("共享设置", () => editAgentSharing(ctx, agent), { "data-sharing-agent": agent.id })]));
    management.hidden = !managed.length;
  }
  managedTable.setLoading(true);
  const managedRead = readRegion(ctx, '可管理的共享节点', readManaged, () => {}, {view:managedTable});
  void managedRead.load();
  ctx.onRefresh(() => Promise.all([invitations.refresh(), managedRead.load()]));
  return stack(managedRead.element, management, invitations);
}
