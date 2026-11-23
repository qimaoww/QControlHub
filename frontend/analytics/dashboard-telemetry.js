import {
  isClockSyncAbnormal,
  panelMetricUsage,
  selectMostActiveNodes,
  summarizeClientConnectionsPulse,
  summarizeExpiringCertificates,
  summarizeIPQualityFleet,
} from "../modules/dashboard-model.js";
import { figure, progressBar } from "./dashboard-console.js";
import { createSVGEl } from "./dashboard-panel.js";
import { patch } from "./live-dom.js";
import { nodeMetrics } from "./node-metrics.js";
import { nodeRegionFlag } from "./node-region.js";
import { node, pathID, section } from "./ui.js";

// The active node grid and unified edge status card of the simplified
// dashboard. Both repaint through keyed patches so focus and scroll survive.
const RACK_UNITS = 8;
const NO_ITEMS = Object.freeze([]);
const BLANK = ["—", ""];
const WAIT_NOTES = { failed: "读取失败", denied: "无权限" };

const el = (tag, className, ...children) => node(tag, { className }, children);
const keyed = (key, element) => { element.setAttribute("data-refresh-key", key); return element; };
const token = (key, text, tone = "") => keyed(key, el("span", tone ? `a-con-token ${tone}` : "a-con-token", text));
const meterTone = (value) => value >= 90 ? "is-bad" : value >= 75 ? "is-warn" : "";

// One clean continuous meter row for an online node tile.
function unitMeter(key, label, value) {
  const known = value != null;
  const tone = known ? meterTone(value) : "";
  return node("div", { className: "a-rack-meter", "data-refresh-key": key },
    el("span", "a-con-label", label),
    progressBar(known ? value : 0, tone, "is-slim"),
    node("strong", { className: tone, "data-refresh-key": "value" }, known ? `${value.toFixed(1)}%` : "—"));
}

function unitRate(agent, online, bytes) {
  const metrics = agent.metrics;
  const sampled = online && metrics?.network_available && metrics.network_rx_bps != null && metrics.network_tx_bps != null;
  if (!sampled) return node("div", { className: "a-rack-rate", "data-refresh-key": "rate" }, el("span", "a-con-note", "暂无速率"));
  return node("div", { className: "a-rack-rate", "data-refresh-key": "rate" },
    el("span", "a-con-rx", `↓ ${bytes(metrics.network_rx_bps)}/s`),
    el("span", "a-con-tx", `↑ ${bytes(metrics.network_tx_bps)}/s`));
}

function rackUnit(context, agent) {
  const { ctx, bytes, date, canMetrics } = context;
  const online = agent.status === "online";
  const skew = isClockSyncAbnormal(agent.metrics?.clock_sync);
  const state = online ? (skew ? "skew" : "online") : "offline";
  const heartbeat = agent.metrics?.collected_at || agent.last_seen;

  const head = node("div", { className: "a-rack-head", "data-refresh-key": "head" },
    el("span", online ? "a-node-dot is-on" : "a-node-dot"),
    nodeRegionFlag(ctx, agent),
    node("a", {
      className: "a-rack-name",
      href: `#node-settings?agent=${pathID(agent.id)}`,
      title: agent.name || agent.id,
    }, agent.name || agent.id),
    online ? null : keyed("state", el("span", "a-rack-pill", "离线")),
    skew ? keyed("skew", el("span", "a-rack-pill warn", "时钟异常")) : null);

  // Online nodes display clean slim CPU/Mem bars and live rate; offline nodes
  // collapse cleanly to last-seen + trend action without empty meter clutter.
  const meters = (online && canMetrics) ? [
    unitMeter("cpu", "CPU", panelMetricUsage(agent.metrics, "cpu")),
    unitMeter("mem", "内存", panelMetricUsage(agent.metrics, "memory")),
  ] : NO_ITEMS;

  const leftFoot = online
    ? (canMetrics ? unitRate(agent, online, bytes) : keyed("online-note", el("span", "a-con-note", "在线")))
    : keyed("hb", el("span", "a-con-note", heartbeat ? `最后在线 ${date(heartbeat)}` : "未同步"));

  const trend = canMetrics ? node("button", {
    type: "button",
    className: "a-con-jump",
    title: "24 小时趋势",
    "data-refresh-key": "trend",
    onclick: () => nodeMetrics(ctx, agent),
  }, "趋势") : null;

  const foot = node("div", { className: "a-rack-foot", "data-refresh-key": "foot" }, leftFoot, trend);
  return node("li", { className: "a-rack-unit", "data-refresh-key": agent.id, "data-status": state }, head, meters, foot);
}

const rackSignatures = new WeakMap();

export function paintNodeRack({ ctx, summaryHost, rackHost, agents, agentsReady, canMetrics, bytes, date }) {
  const visible = selectMostActiveNodes(agents, { limit: RACK_UNITS });
  const skewed = agents.filter(agent => isClockSyncAbnormal(agent.metrics?.clock_sync)).length;
  const online = agents.filter(agent => agent.status === "online").length;
  const beats = visible.map(agent => agent.metrics?.collected_at || agent.last_seen || "");
  const signature = JSON.stringify([agentsReady, agents.length, online, skewed, canMetrics, visible, beats]);
  if (rackSignatures.get(rackHost) === signature) return;
  rackSignatures.set(rackHost, signature);

  patch(summaryHost, node("div", { className: "a-actions a-con-tools" },
    agents.length > visible.length ? token("top", `活跃 Top ${visible.length}`) : null,
    skewed > 0 ? token("skew", `时钟异常 ${skewed}`, "warn") : null,
    node("a", { className: "a-button", href: "#node-settings", "data-refresh-key": "manage-all" }, "管理节点")));

  const context = { ctx, bytes, date, canMetrics };
  const units = visible.map(agent => rackUnit(context, agent));
  const wait = agents.length ? null : keyed("empty", el("li", "a-rack-empty a-muted", agentsReady ? "暂无节点" : "读取中…"));
  patch(rackHost, node("ol", { className: visible.length >= 4 ? "a-rack is-grid-4" : "a-rack", "aria-label": "节点运行状态" }, units, wait));
}

// Compact 24h inbound connection pulse bars.
function pulseBars(buckets) {
  const slice = buckets.slice(-24);
  const svg = createSVGEl("svg", {
    class: "a-edge-pulse",
    viewBox: "0 0 96 28",
    preserveAspectRatio: "none",
    role: "img",
    "aria-label": "近 24 小时入站连接",
  });
  svg.append(createSVGEl("line", { x1: 0, y1: 27.5, x2: 96, y2: 27.5, class: "a-con-trace-base", "vector-effect": "non-scaling-stroke" }));
  const peak = Math.max(1, ...slice.map(item => item.flows));
  const offset = 24 - slice.length;
  slice.forEach((item, index) => {
    const size = item.flows > 0 ? Math.max(2.5, item.flows / peak * 24) : 1.5;
    const tone = item.flows <= 0 ? "is-zero" : item.flows === peak ? "is-peak" : "";
    svg.append(createSVGEl("rect", {
      x: (offset + index) * 4,
      y: (27 - size).toFixed(1),
      width: 2.8,
      height: size.toFixed(1),
      rx: 1,
      class: `a-edge-bar ${tone}`,
    }));
  });
  return svg;
}

function edgeRowNode(model, viz = null) {
  const pillNode = model.pill
    ? node("span", { className: `a-edge-pill ${model.pill.tone || ""}`, "data-refresh-key": "pill" }, model.pill.text)
    : null;
  const subs = (model.subs || NO_ITEMS).map((item, idx) =>
    node("span", { className: item.tone ? `a-con-sub-item ${item.tone}` : "a-con-sub-item", "data-refresh-key": `sub-${idx}` }, item.text));
  return node("div", {
    className: "a-edge-item",
    "data-refresh-key": model.key,
    "data-state": model.state || "ready",
  },
    node("div", { className: "a-edge-item-main", "data-refresh-key": "main" },
      node("div", { className: "a-edge-item-head", "data-refresh-key": "head" },
        node("span", { className: "a-con-label", "data-refresh-key": "label" }, model.label),
        pillNode),
      figure(model.figure, "a-con-fig a-edge-fig"),
      subs.length ? node("div", { className: "a-edge-item-subs", "data-refresh-key": "subs" }, subs) : null),
    viz ? node("div", { className: "a-edge-item-viz", "data-refresh-key": "viz" }, viz) : null,
    model.href ? node("a", { className: "a-con-jump a-edge-jump", href: model.href, "data-refresh-key": "jump" }, model.hrefLabel) : null);
}

function waitingModel(key, label, href, hrefLabel, state) {
  const note = WAIT_NOTES[state] || "读取中…";
  return {
    key, label, href, hrefLabel,
    state: WAIT_NOTES[state] ? state : "loading",
    figure: BLANK,
    subs: [{ text: note, tone: WAIT_NOTES[state] ? "bad" : "" }],
  };
}

function connectionRow(data, state) {
  if (!data) return [waitingModel("connections", "入站连接", "#client-connections", "明细", state), null];
  const pulse = summarizeClientConnectionsPulse(data);
  const slice = pulse.buckets.slice(-24);
  const subs = [{ text: `独立 IP ${pulse.ips}${slice.length ? ` · 近 ${slice.length}h` : ""}`, tone: "" }];
  return [{
    key: "connections",
    label: "入站连接",
    href: "#client-connections",
    hrefLabel: "明细",
    figure: [String(pulse.flows), "\u00a0活跃流"],
    subs,
  }, pulseBars(pulse.buckets)];
}

function certificateRow(data, state) {
  if (!data) return [waitingModel("certificates", "证书", "#certificates", "管理", state), null];
  const summary = summarizeExpiringCertificates(data);
  let pill = null;
  if (summary.abnormal) pill = { text: `异常 ${summary.abnormal}`, tone: "bad" };
  else if (summary.expiringSoon) pill = { text: `14 天内到期 ${summary.expiringSoon}`, tone: "warn" };

  const subs = [];
  if (summary.expiringSoon && summary.abnormal) subs.push({ text: `14 天内到期 ${summary.expiringSoon}`, tone: "warn" });
  const urgent = summary.urgent.slice(0, 2);
  if (urgent.length) {
    for (const item of urgent) {
      subs.push({
        text: item.abnormal ? `${item.name} · 异常` : `${item.name} · ${item.daysLeft ?? 0} 天`,
        tone: item.abnormal ? "bad" : "warn",
      });
    }
  } else if (!summary.abnormal && !summary.expiringSoon) {
    subs.push({ text: summary.totalOwned > 0 ? "状态正常" : "暂无托管证书", tone: "" });
  }

  return [{
    key: "certificates",
    label: "证书",
    pill,
    href: "#certificates",
    hrefLabel: "管理",
    figure: [String(summary.totalOwned), "\u00a0有效"],
    subs,
  }, null];
}

function qualityRow(data, state) {
  if (!data) return [waitingModel("ipQuality", "IP 质量", "#ip-quality", "记录", state), null];
  const summary = summarizeIPQualityFleet(data);
  const pass = summary.total ? summary.succeeded / summary.total * 100 : 0;
  const subs = [];
  if (summary.total) subs.push({ text: `通过率 ${Math.round(pass)}%`, tone: "" });
  if (summary.failed) subs.push({ text: `异常 ${summary.failed}`, tone: "bad" });
  if (summary.running) subs.push({ text: `进行中 ${summary.running}`, tone: "" });
  if (!summary.total) subs.push({ text: "暂无检测记录", tone: "" });

  const viz = summary.total
    ? node("div", { className: "a-edge-quality-viz" },
        node("span", { className: pass >= 90 ? "a-edge-pct is-ok" : pass >= 70 ? "a-edge-pct" : "a-edge-pct is-warn" }, `${Math.round(pass)}%`),
        progressBar(pass, pass >= 90 ? "is-ok" : pass >= 70 ? "" : "is-warn", "is-edge"))
    : null;

  return [{
    key: "ipQuality",
    label: "IP 质量",
    href: "#ip-quality",
    hrefLabel: "记录",
    figure: [String(summary.total), "\u00a0已检测"],
    subs,
  }, viz];
}

const EDGE_SOURCES = [
  ["connections", "core-logs.read", connectionRow],
  ["certificates", "certificates.read", certificateRow],
  ["ipQuality", "agents.read", qualityRow],
];

// Inbound connections, certificates and IP quality stacked cleanly inside one
// calm card beside Panel Host (expands gracefully if shown alone).
export function createEdgeTelemetryController(ctx, { readEdgeState }) {
  const sources = EDGE_SOURCES.filter(entry => ctx.can(entry[1]));
  const hosts = new Map(sources.map(entry => [entry[0], node("div", { className: "a-edge-item", "data-refresh-key": entry[0] })]));
  const listEl = node("div", { className: "a-edge-list" }, [...hosts.values()]);
  const element = section("边缘状态", listEl);
  element.classList.add("a-edge-card");
  const signatures = new Map();

  function paint() {
    for (const [key, , build] of sources) {
      const { data, state } = readEdgeState(key);
      const [model, viz] = build(data, state || "");
      const signature = JSON.stringify([model, viz?.outerHTML || ""]);
      if (signatures.get(key) === signature) continue;
      signatures.set(key, signature);
      patch(hosts.get(key), edgeRowNode(model, viz));
    }
  }

  return { element, visible: sources.length > 0, paint };
}
