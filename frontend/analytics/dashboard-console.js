import { patch } from "./live-dom.js";
import { createSVGEl } from "./dashboard-panel.js";
import { node } from "./ui.js";

// Presentation primitives of the simplified dashboard: tabular figures,
// readouts, smooth progress bars, the quiet UTC meta strip and the 4-card
// executive KPI strip. Every value arrives already computed.
const NBSP = String.fromCharCode(160);
const NO_SUBS = Object.freeze([]);

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

// Clean continuous rounded progress bar using native <progress> (zero inline
// styles for strict CSP and distortion-free rounded caps at every width).
export function progressBar(percent, tone = "", className = "") {
  const numeric = typeof percent === "number" && Number.isFinite(percent)
    ? Math.max(0, Math.min(100, percent))
    : 0;
  const normalizedTone = tone === "ok" || tone === "is-ok"
    ? "is-ok"
    : tone === "warn" || tone === "is-warn"
      ? "is-warn"
      : tone === "bad" || tone === "is-bad"
        ? "is-bad"
        : "";
  const classes = ["a-con-progress", normalizedTone, className].filter(Boolean).join(" ");
  return node("progress", {
    className: classes,
    max: "100",
    value: numeric.toFixed(1),
    "aria-hidden": "true",
  });
}

// Backwards-compatible alias used by quota/telemetry call sites.
export function segmentMeter(percent, _segments = 20, tone = "") {
  return progressBar(percent, tone);
}

// Recent samples of one live reading, oldest first. Gaps are skipped.
function traceViz(viz) {
  const values = (viz.values || NO_SUBS).filter(value => typeof value === "number" && Number.isFinite(value));
  if (values.length < 2) return null;
  const svg = createSVGEl("svg", { class: "a-con-trace", viewBox: "0 0 120 22", preserveAspectRatio: "none", "aria-hidden": "true" });
  svg.append(createSVGEl("line", { x1: 0, y1: 21.5, x2: 120, y2: 21.5, class: "a-con-trace-base", "vector-effect": "non-scaling-stroke" }));
  const peak = Math.max(1, ...values) * 1.25;
  const points = values.map((value, index) => `${(index / (values.length - 1) * 120).toFixed(1)} ${(20 - value / peak * 18).toFixed(1)}`);
  svg.append(
    createSVGEl("path", { d: `M0 22 L${points.join(" L")} L120 22 Z`, class: "a-con-trace-area" }),
    createSVGEl("path", { d: `M${points.join(" L")}`, class: "a-con-trace-line", "vector-effect": "non-scaling-stroke" }));
  return svg;
}

function barViz(viz) {
  return progressBar(viz.pct, viz.tone || "", "is-kpi");
}

const VIZ = { bar: barViz, trace: traceViz };

function headAccessory(cell) {
  if (cell.pill) {
    const toneClass = cell.pill.tone ? ` ${cell.pill.tone}` : "";
    if (cell.pill.href) {
      return node("a", {
        className: `a-dash-kpi-pill is-link${toneClass}`,
        href: cell.pill.href,
        "data-refresh-key": "pill",
      }, cell.pill.text);
    }
    return node("span", {
      className: `a-dash-kpi-pill${toneClass}`,
      "data-refresh-key": "pill",
    }, cell.pill.text);
  }
  if (cell.token) {
    const toneClass = cell.tone ? ` ${cell.tone}` : "";
    return node("span", { className: `a-con-token${toneClass}`, "data-refresh-key": "token" }, cell.token);
  }
  return null;
}

function subNode(item, index) {
  const className = item.tone ? `a-con-sub-item ${item.tone}` : "a-con-sub-item";
  return node("span", { className, "data-refresh-key": `sub-${index}` }, item.text);
}

// One modular KPI card of the top executive overview strip.
export function consoleCell(cell, instrument = null) {
  const draw = cell.viz ? VIZ[cell.viz.type] : null;
  const renderedViz = draw ? draw(cell.viz) : instrument;
  const subs = (cell.subs || NO_SUBS).map(subNode);
  const jump = cell.href ? node("a", { className: "a-con-jump", href: cell.href, "data-refresh-key": "jump" }, cell.hrefLabel) : null;
  return node("article", {
    className: "a-card a-dash-kpi",
    "data-refresh-key": cell.key,
    "data-state": cell.state || "ready",
  },
    node("div", { className: "a-dash-kpi-head", "data-refresh-key": "head" },
      node("span", { className: "a-con-label", "data-refresh-key": "label" }, cell.label),
      headAccessory(cell),
      jump),
    figure(cell.figure, "a-con-fig a-dash-kpi-fig"),
    node("div", { className: "a-dash-kpi-foot", "data-refresh-key": "foot" },
      subs.length ? node("div", { className: "a-dash-kpi-subs", "data-refresh-key": "subs" }, subs) : null,
      renderedViz ? node("div", { className: "a-dash-kpi-viz", "data-refresh-key": "viz" }, renderedViz) : null));
}

const renderCells = (cells) => node("section", {
  className: "a-dash-kpis",
  "aria-label": "关键指标概览",
}, cells.map(cell => consoleCell(cell)));

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

// Top 4-card executive KPI strip.
export function createLiveCells() {
  const element = node("section", { className: "a-dash-kpis", "aria-label": "关键指标概览" });
  return { element, paint: guarded(element, renderCells) };
}
