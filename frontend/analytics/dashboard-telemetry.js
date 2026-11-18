import {
  isClockSyncAbnormal,
  panelMetricUsage,
  selectMostActiveNodes,
  summarizeClientConnectionsPulse,
  summarizeExpiringCertificates,
  summarizeIPQualityFleet,
} from "../modules/dashboard-model.js";
import { consoleCell, segmentMeter } from "./dashboard-console.js";
import { createSVGEl } from "./dashboard-panel.js";
import { patch } from "./live-dom.js";
import { nodeMetrics } from "./node-metrics.js";
import { nodeRegionFlag } from "./node-region.js";
import { node, pathID } from "./ui.js";

// The node rack and the edge instruments of the dashboard. Both repaint
// through keyed patches, so focus and scroll survive a refresh.
const RACK_UNITS = 8;
const METER_SEGMENTS = 20;
const NO_ITEMS = Object.freeze([]);
const BLANK = ["—", ""];
const WAIT_NOTES = { failed: "读取失败", denied: "无权限" };

const el = (tag, className, ...children) => node(tag, { className }, children);
const keyed = (key, element) => { element.setAttribute("data-refresh-key", key); return element; };
const token = (key, text, tone = "") => keyed(key, el("span", tone ? `a-con-token ${tone}` : "a-con-token", text));
const meterTone = (value) => value >= 90 ? "is-bad" : value >= 75 ? "is-warn" : "";

// One usage meter of a rack unit. A stale or missing sample shows an empty
// meter and no figure instead of a zero.
function unitMeter(key, label, value) {
  const known = value != null;
  return node("div", { className: "a-rack-meter", "data-refresh-key": key },
    el("span", "a-con-label", label),
    known ? segmentMeter(value, METER_SEGMENTS, meterTone(value)) : segmentMeter(0, METER_SEGMENTS),
    node("strong", { "data-refresh-key": "value" }, known ? `${value.toFixed(1)}%` : "—"));
}

function unitRate(agent, online, bytes) {
  const metrics = agent.metrics;
  const sampled = online && metrics?.network_available && metrics.network_rx_bps != null && metrics.network_tx_bps != null;
  if (!sampled) return node("div", { className: "a-rack-rate", "data-refresh-key": "rate" }, el("span", "a-con-note", "—"));
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
    el("span", online ? "a-con-pip is-on" : "a-con-pip"),
    nodeRegionFlag(ctx, agent),
    node("a", { className: "a-rack-name", href: `#node-settings?agent=${pathID(agent.id)}`, title: agent.name || agent.id }, agent.name || agent.id),
    online ? null : token("state", "离线", "bad"),
    skew ? token("skew", "时钟异常", "warn") : null);
  const meters = canMetrics ? [
    unitMeter("cpu", "CPU", online ? panelMetricUsage(agent.metrics, "cpu") : null),
    unitMeter("mem", "内存", online ? panelMetricUsage(agent.metrics, "memory") : null),
    unitRate(agent, online, bytes),
  ] : NO_ITEMS;
  // A live unit needs no timestamp; an offline one shows when it was last seen.
  const seen = online ? null : keyed("hb", el("span", "a-con-note", heartbeat ? date(heartbeat) : "未同步"));
  const trend = canMetrics ? node("button", {
    type: "button", className: "a-con-jump", title: "24 小时趋势", "data-refresh-key": "trend", onclick: () => nodeMetrics(ctx, agent),
  }, "趋势") : null;
  const foot = node("div", { className: "a-rack-foot", "data-refresh-key": "foot" }, seen, trend);
  return node("li", { className: "a-rack-unit", "data-refresh-key": agent.id, "data-status": state }, head, meters, foot);
}

const rackSignatures = new WeakMap();

// Rack of the most active nodes: status, usage meters and the live rate of
// each. Offline nodes keep their unit but show no live figures.
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
  patch(rackHost, node("ol", { className: "a-rack", "aria-label": "节点运行状态" }, units, wait));
}

// Hourly inbound flows of the last day. The busiest hour is solid; an hour
// without a flow keeps a baseline tick.
function pulseBars(buckets) {
  const slice = buckets.slice(-24);
  const svg = createSVGEl("svg", { class: "a-edge-pulse", viewBox: "0 0 96 24", preserveAspectRatio: "none", role: "img", "aria-label": "近 24 小时入站连接" });
  svg.append(createSVGEl("line", { x1: 0, y1: 23.5, x2: 96, y2: 23.5, class: "a-con-trace-base", "vector-effect": "non-scaling-stroke" }));
  const peak = Math.max(1, ...slice.map(item => item.flows));
  const offset = 24 - slice.length;
  slice.forEach((item, index) => {
    const size = item.flows > 0 ? Math.max(2, item.flows / peak * 22) : 1;
    const tone = item.flows <= 0 ? "is-zero" : item.flows === peak ? "is-peak" : "";
    svg.append(createSVGEl("rect", { x: (offset + index) * 4, y: (24 - size).toFixed(1), width: 3, height: size.toFixed(1), class: `a-edge-bar ${tone}` }));
  });
  return svg;
}

function waitingCell(key, label, href, state) {
  const note = WAIT_NOTES[state] || "读取中…";
  return { key, label, href, hrefLabel: "明细", state: WAIT_NOTES[state] ? state : "loading", figure: BLANK, subs: [{ text: note, tone: WAIT_NOTES[state] ? "bad" : "" }] };
}

function connectionCell(data, state) {
  if (!data) return [waitingCell("connections", "入站连接", "#client-connections", state), null];
  const pulse = summarizeClientConnectionsPulse(data);
  const slice = pulse.buckets.slice(-24);
  const subs = [{ text: `IP ${pulse.ips}`, tone: "" }];
  const cell = { key: "connections", label: "入站连接", href: "#client-connections", hrefLabel: "明细", token: slice.length ? `${slice.length}h` : "", figure: [String(pulse.flows), " 活跃流"], subs };
  return [cell, pulseBars(pulse.buckets)];
}

function certificateCell(data, state) {
  if (!data) return [waitingCell("certificates", "证书", "#certificates", state), null];
  const summary = summarizeExpiringCertificates(data);
  const subs = [];
  if (summary.expiringSoon) subs.push({ text: `14 天内到期 ${summary.expiringSoon}`, tone: "warn" });
  if (summary.abnormal) subs.push({ text: `异常 ${summary.abnormal}`, tone: "bad" });
  const rows = summary.urgent.slice(0, 2).map(item => node("li", { className: "a-edge-row", "data-refresh-key": String(item.id || item.name) },
    node("span", { className: "a-edge-row-name", title: item.name }, item.name),
    el("span", item.abnormal ? "a-con-token bad" : "a-con-token warn", item.abnormal ? "异常" : `${item.daysLeft ?? 0} 天`)));
  const cell = { key: "certificates", label: "证书", href: "#certificates", hrefLabel: "管理", figure: [String(summary.totalOwned), " 有效"], subs };
  return [cell, rows.length ? el("ul", "a-edge-rows", rows) : null];
}

function qualityCell(data, state) {
  if (!data) return [waitingCell("ipQuality", "IP 质量", "#ip-quality", state), null];
  const summary = summarizeIPQualityFleet(data);
  const pass = summary.total ? summary.succeeded / summary.total * 100 : 0;
  const subs = [];
  if (summary.failed) subs.push({ text: `异常 ${summary.failed}`, tone: "bad" });
  if (summary.running) subs.push({ text: `进行中 ${summary.running}`, tone: "" });
  const cell = { key: "ipQuality", label: "IP 质量", href: "#ip-quality", hrefLabel: "记录", token: summary.total ? `通过 ${Math.round(pass)}%` : "", figure: [String(summary.total), " 已检测"], subs };
  return [cell, summary.total ? segmentMeter(pass, 24) : null];
}

const EDGE_SOURCES = [
  ["connections", "core-logs.read", connectionCell],
  ["certificates", "certificates.read", certificateCell],
  ["ipQuality", "agents.read", qualityCell],
];

// Inbound connections, certificates and IP quality as three instruments in
// one strip. Each one keeps its last reading while a refresh fails.
export function createEdgeTelemetryController(ctx, { readEdgeState }) {
  const sources = EDGE_SOURCES.filter(entry => ctx.can(entry[1]));
  const hosts = new Map(sources.map(entry => [entry[0], node("div", { className: "a-con-cell a-edge-cell", "data-refresh-key": entry[0] })]));
  const element = node("section", { className: "a-card a-edge", "aria-label": "边缘状态" }, [...hosts.values()]);
  const signatures = new Map();

  function paint() {
    for (const [key, , build] of sources) {
      const { data, state } = readEdgeState(key);
      const [cell, viz] = build(data, state || "");
      const signature = JSON.stringify([cell, viz?.outerHTML || ""]);
      if (signatures.get(key) === signature) continue;
      signatures.set(key, signature);
      const fresh = consoleCell(cell, viz);
      fresh.classList.add("a-edge-cell");
      patch(hosts.get(key), fresh);
    }
  }

  return { element, visible: sources.length > 0, paint };
}
