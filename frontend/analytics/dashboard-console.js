import { patch } from "./live-dom.js";
import { createSVGEl } from "./dashboard-panel.js";
import { node } from "./ui.js";

// Presentation primitives of the dashboard console: figures, readouts, the
// status line and the small instruments of the live band. Every value
// arrives already computed; nothing here reads or derives data.
const NBSP = String.fromCharCode(160);
const PIP_LIMIT = 48;
const NO_SUBS = Object.freeze([]);

const span = (className, ...children) => node("span", { className }, children);

// Splits a formatted quantity such as "12.4 GB" into its number and unit.
export function quantityParts(text, suffix = "") {
  const [value, unit = ""] = String(text).split(NBSP);
  return [value, unit ? `${NBSP}${unit}${suffix}` : suffix];
}

// The number stays in <strong> and the unit beside it, so a growing figure
// never pushes its unit onto a second line.
export function figure(parts, className = "a-con-fig") {
  const [value, unit] = parts;
  return node("span", { className, "data-refresh-key": "figure" },
    node("strong", { "data-refresh-key": "value" }, value),
    unit ? node("span", { className: "a-con-unit", "data-refresh-key": "unit" }, unit) : null);
}

// A labelled numeric readout: label, figure and an optional note line.
export function readout(key, label, parts, note = "", tone = "") {
  return node("div", { className: tone ? `a-con-readout ${tone}` : "a-con-readout", "data-refresh-key": key },
    node("span", { className: "a-con-label", "data-refresh-key": "label" }, label),
    figure(parts),
    note ? node("span", { className: "a-con-note", "data-refresh-key": "note" }, note) : null);
}

// Segmented ratio. The reading is the count of lit segments, so it does not
// depend on a colour; the tone only marks a threshold.
export function segmentMeter(percent, segments, tone = "") {
  const lit = Math.round(Math.max(0, Math.min(100, percent)) / 100 * segments);
  const on = tone ? `a-con-seg is-on ${tone}` : "a-con-seg is-on";
  const svg = createSVGEl("svg", { class: "a-con-meter", viewBox: `0 0 ${segments * 5 - 1} 8`, preserveAspectRatio: "none", "aria-hidden": "true" });
  for (let index = 0; index < segments; index++) {
    svg.append(createSVGEl("rect", { x: index * 5, y: 0, width: 4, height: 8, class: index < lit ? on : "a-con-seg" }));
  }
  return svg;
}

// One pip per node while the fleet is small; lit pips are online. Larger
// fleets fall back to a segmented ratio so the cell stays bounded.
function pipsViz(viz) {
  const total = Math.max(0, Number(viz.total) || 0), on = Math.max(0, Math.min(total, Number(viz.on) || 0));
  if (!total) return null;
  if (total > PIP_LIMIT) return segmentMeter(on / total * 100, 32);
  const pips = Array.from({ length: total }, (_, index) => span(index < on ? "a-con-pip is-on" : "a-con-pip"));
  return node("div", { className: "a-con-pips", "aria-hidden": "true" }, pips);
}

// Recent samples of one live reading, oldest first. Gaps are skipped.
function traceViz(viz) {
  const values = (viz.values || NO_SUBS).filter(value => typeof value === "number" && Number.isFinite(value));
  const svg = createSVGEl("svg", { class: "a-con-trace", viewBox: "0 0 120 24", preserveAspectRatio: "none", "aria-hidden": "true" });
  svg.append(createSVGEl("line", { x1: 0, y1: 23.5, x2: 120, y2: 23.5, class: "a-con-trace-base", "vector-effect": "non-scaling-stroke" }));
  if (values.length < 2) return svg;
  // Headroom above the peak keeps a steady reading off the top edge.
  const peak = Math.max(1, ...values) * 1.25;
  const points = values.map((value, index) => `${(index / (values.length - 1) * 120).toFixed(1)} ${(22 - value / peak * 20).toFixed(1)}`);
  svg.append(
    createSVGEl("path", { d: `M0 24 L${points.join(" L")} L120 24 Z`, class: "a-con-trace-area" }),
    createSVGEl("path", { d: `M${points.join(" L")}`, class: "a-con-trace-line", "vector-effect": "non-scaling-stroke" }));
  return svg;
}

const VIZ = { pips: pipsViz, trace: traceViz };

function tokenNode(cell) {
  if (!cell.token) return null;
  return node("span", { className: cell.tone ? `a-con-token ${cell.tone}` : "a-con-token", "data-refresh-key": "token" }, cell.token);
}

function subNode(item, index) {
  const className = item.tone ? `a-con-sub-item ${item.tone}` : "a-con-sub-item";
  return node("span", { className, "data-refresh-key": `sub-${index}` }, item.text);
}

// One reading of the console: label, figure, a short breakdown and its
// instrument. The instrument is drawn from the model, or passed in.
export function consoleCell(cell, instrument = null) {
  const draw = cell.viz ? VIZ[cell.viz.type] : null;
  const jump = cell.href ? node("a", { className: "a-con-jump", href: cell.href, "data-refresh-key": "jump" }, cell.hrefLabel) : null;
  return node("div", { className: "a-con-cell", "data-refresh-key": cell.key, "data-state": cell.state || "ready" },
    node("div", { className: "a-con-cell-head", "data-refresh-key": "head" },
      node("span", { className: "a-con-label", "data-refresh-key": "label" }, cell.label),
      tokenNode(cell), jump),
    figure(cell.figure, "a-con-fig a-con-cell-fig"),
    node("div", { className: "a-con-cell-sub", "data-refresh-key": "sub" }, (cell.subs || NO_SUBS).map(subNode)),
    node("div", { className: "a-con-cell-viz", "data-refresh-key": "viz" }, draw ? draw(cell.viz) : instrument));
}

function stripItem(item) {
  const key = item.key;
  if (item.strong != null) return node("strong", { "data-refresh-key": key }, item.strong);
  if (item.href) return node("a", { className: `a-con-token ${item.tone || ""}`, href: item.href, "data-refresh-key": key }, item.text);
  if (item.tone) return node("span", { className: `a-con-token ${item.tone}`, "data-refresh-key": key }, item.text);
  return node("span", { className: "a-con-strip-text", "data-refresh-key": key }, item.text);
}

function stripGroup(group) {
  return node("div", { className: "a-con-strip-group", "data-refresh-key": group.key },
    node("span", { className: "a-con-label", "data-refresh-key": "label" }, group.label),
    group.items.map(stripItem));
}

function renderStatus(model) {
  const status = node("span", { className: "a-con-token warn", role: "status", hidden: !model.status, "data-refresh-key": "status" }, model.status || "");
  return node("div", { className: "a-con-status", role: "group", "aria-label": "运行概况" },
    node("span", { className: "a-con-clock", "data-refresh-key": "clock" }, model.clock),
    model.groups.map(stripGroup),
    node("span", { className: "a-con-strip-end", "data-refresh-key": "end" }, status));
}

const renderCells = (cells) => node("div", { className: "a-con-cells" }, cells.map(cell => consoleCell(cell)));

// Repaints a stable host only when its model changed.
function guarded(element, render) {
  let signature = "";
  return (model) => {
    const next = JSON.stringify(model);
    if (next === signature) return;
    signature = next;
    patch(element, render(model));
  };
}

// The slim line above the console: UTC clock, engines and tasks.
export function createStatusLine() {
  const element = node("div", { className: "a-con-status", role: "group", "aria-label": "运行概况" });
  return { element, paint: guarded(element, renderStatus) };
}

// Live fleet readings shown beside the panel host.
export function createLiveCells() {
  const element = node("div", { className: "a-con-cells" });
  return { element, paint: guarded(element, renderCells) };
}
