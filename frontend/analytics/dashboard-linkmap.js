import { probeHistorySeries } from "../modules/dashboard-model.js";
import { createSVGEl } from "./dashboard-panel.js";
import { clipLabel, formatDirBadgeMetric, resolveNodeGeoPoint, visualUnits } from "./dashboard-topo-shared.js";
import { PACIFIC_LAND_PATH, PACIFIC_MAP_BOUNDS } from "./pacific-map-data.js";

// The dashboard link map. One monitored link is drawn over a dot-matrix land
// backdrop and the frame is fitted to it, so there is nothing to pan or zoom.
// Figures stay in the rail beside the map; the map shows where the link runs
// and which direction is in trouble.
const WORLD_SPAN = PACIFIC_MAP_BOUNDS.width - 2 * PACIFIC_MAP_BOUNDS.padX;
const WORLD_HEIGHT = PACIFIC_MAP_BOUNDS.height;
const WORLD_COPIES = Object.freeze([-WORLD_SPAN, 0, WORLD_SPAN]);
const MIN_VISIBLE = 190;
const DOT_PITCH = 4;
const DOT_SIZE = 2;
const PIP = 9;
const NAME_UNITS = 18;
const UNIT_PX = 6.5;
const TRACE_SAMPLES = 40;
const DOTS_ID = "a-lm-dots";
const LAND_ID = "a-lm-land";
const NO_VALUES = Object.freeze([]);

const clamp = (value, low, high) => Math.max(low, Math.min(high, value));
const fixed = (value) => Number(value.toFixed(1));
const isNumber = (value) => typeof value === "number" && Number.isFinite(value);

function titleElement(text) {
  const element = createSVGEl("title");
  element.textContent = text;
  return element;
}

// The stage is drawn at the pixel size it is shown at, so type and strokes
// keep their size at every width; only the height is bounded.
export function linkMapSize(width) {
  const w = Math.round(clamp(Number(width) || 0, 280, 1600));
  return { width: w, height: Math.round(clamp(w * 0.34, 200, 300)) };
}

// World position of every node that has a place on the map. Longitudes are
// unwrapped along the chain so each hop takes the short way round.
function locate(nodes) {
  let previous = null;
  return nodes.map((item) => {
    if (item.kind === "ext") return null;
    const geo = resolveNodeGeoPoint({ name: item.name, id: item.agentID, regionCode: item.region }, 0);
    if (!geo.matched) return null;
    let x = geo.x;
    if (previous != null) {
      while (x - previous > WORLD_SPAN / 2) x -= WORLD_SPAN;
      while (previous - x > WORLD_SPAN / 2) x += WORLD_SPAN;
    }
    previous = x;
    return { x, y: geo.y };
  });
}

function frameAt(cx, cy, scale, width, height) {
  const tall = WORLD_HEIGHT * scale;
  let ty = height / 2 - cy * scale;
  if (tall <= height) ty = (height - tall) / 2;
  else ty = clamp(ty, height - tall, 0);
  return { scale, tx: Math.round(width / 2 - cx * scale), ty: Math.round(ty) };
}

// Fits the frame to the located nodes: the link spans about half the stage,
// and the frame never closes in further than MIN_VISIBLE world pixels.
function fitFrame(points, width, height) {
  const located = points.filter(Boolean);
  const floor = width / WORLD_SPAN;
  if (!located.length) return frameAt(PACIFIC_MAP_BOUNDS.width / 2, WORLD_HEIGHT * 0.42, floor, width, height);
  const xs = located.map(point => point.x), ys = located.map(point => point.y);
  const left = Math.min(...xs), right = Math.max(...xs), top = Math.min(...ys), bottom = Math.max(...ys);
  const spanX = Math.max(right - left, 1), spanY = Math.max(bottom - top, 1);
  const fit = Math.min(width * 0.5 / spanX, height * 0.46 / spanY);
  const whole = Math.min((width - 96) / spanX, (height - 76) / spanY);
  const scale = Math.min(width / MIN_VISIBLE, whole, Math.max(fit, floor));
  return frameAt((left + right) / 2, (top + bottom) / 2, scale, width, height);
}

// Nodes in the same city share a point; set them side by side instead.
function spread(spots) {
  spots.forEach((spot, index) => {
    for (let pass = 0; pass < spots.length; pass++) {
      const clash = spots.slice(0, index).some(other => Math.abs(other.x - spot.x) < 22 && Math.abs(other.y - spot.y) < 18);
      if (!clash) break;
      spot.x += 26;
    }
  });
  return spots;
}

// Stage position of every node. A node without a place on the map, such as
// a public target, is set beside its neighbour in the chain.
function place(points, frame, width, height) {
  const step = clamp(width * 0.24, 84, 220);
  const spots = points.map(point => point ? { x: frame.tx + point.x * frame.scale, y: frame.ty + point.y * frame.scale } : null);
  const first = spots.findIndex(Boolean);
  if (first < 0) return spots.map((_, index) => ({ x: width * (index + 1) / (spots.length + 1), y: height / 2 }));
  for (let index = first - 1; index >= 0; index--) spots[index] = { x: spots[index + 1].x - step, y: spots[index + 1].y };
  for (let index = first + 1; index < spots.length; index++) {
    if (spots[index]) continue;
    const anchor = spots[index - 1];
    const side = anchor.x + step > width - 64 ? -1 : 1;
    spots[index] = { x: anchor.x + side * step, y: anchor.y };
  }
  return spread(spots.map(spot => ({ x: clamp(spot.x, 30, width - 30), y: clamp(spot.y, 30, height - 30) })));
}

// Each direction bows to the left of its own travel, so a two-way hop reads
// as a loop: out along one side, back along the other.
function hopShape(from, to, twoWay) {
  const dx = to.x - from.x, dy = to.y - from.y;
  const length = Math.max(1, Math.hypot(dx, dy));
  const bow = clamp(length * 0.17, 12, 40) * (twoWay ? 1 : 0.6);
  const nx = dy / length * bow, ny = -dx / length * bow;
  const mx = (from.x + to.x) / 2, my = (from.y + to.y) / 2;
  const start = `${fixed(from.x)} ${fixed(from.y)}`, end = `${fixed(to.x)} ${fixed(to.y)}`;
  const out = `${fixed(mx + 2 * nx)} ${fixed(my + 2 * ny)}`, back = `${fixed(mx - 2 * nx)} ${fixed(my - 2 * ny)}`;
  const xs = [from.x, to.x, mx + nx, mx - nx], ys = [from.y, to.y, my + ny, my - ny];
  const left = Math.min(...xs) - 10, top = Math.min(...ys) - 10;
  return {
    forward: `M${start}Q${out} ${end}`,
    reverse: `M${start}Q${back} ${end}`,
    hit: twoWay ? `M${start}Q${out} ${end}Q${back} ${start}Z` : `M${start}Q${out} ${end}`,
    forwardApex: { x: mx + nx, y: my + ny },
    reverseApex: { x: mx - nx, y: my - ny },
    box: { x: fixed(left), y: fixed(top), width: fixed(Math.max(...xs) + 10 - left), height: fixed(Math.max(...ys) + 10 - top) },
  };
}

function railTone(dir) {
  const tone = dir?.status?.tone || "";
  if (tone === "bad" || tone === "warn") return tone;
  return tone === "ok" ? "" : "idle";
}

// A break in the rail: a cross where the direction fails, a wedge where it degrades.
function railMark(apex, tone) {
  if (tone !== "bad" && tone !== "warn") return NO_VALUES;
  const x = apex.x, y = apex.y;
  const cross = `M${fixed(x - 3.5)} ${fixed(y - 3.5)}L${fixed(x + 3.5)} ${fixed(y + 3.5)}M${fixed(x - 3.5)} ${fixed(y + 3.5)}L${fixed(x + 3.5)} ${fixed(y - 3.5)}`;
  const wedge = `M${fixed(x)} ${fixed(y - 4.5)}L${fixed(x + 4.5)} ${fixed(y + 3.5)}L${fixed(x - 4.5)} ${fixed(y + 3.5)}Z`;
  return [
    createSVGEl("circle", { cx: fixed(x), cy: fixed(y), r: 7.5, class: "a-lm-bed" }),
    createSVGEl("path", { d: tone === "bad" ? cross : wedge, class: `a-lm-mark ${tone}` }),
  ];
}

function focusState(focusKey) {
  const nodeIndex = focusKey.startsWith("node:") ? Number(focusKey.slice(5)) : -1;
  const hopIndex = focusKey.startsWith("hop:") ? Number(focusKey.slice(4)) : -1;
  return {
    key: focusKey,
    dimHop: (index) => (hopIndex >= 0 && index !== hopIndex) || (nodeIndex >= 0 && index !== nodeIndex && index !== nodeIndex - 1),
    dimNode: (index) => hopIndex >= 0 && index !== hopIndex && index !== hopIndex + 1,
  };
}

function hopElement(group, hop, index, spots, focus) {
  const shape = hopShape(spots[index], spots[index + 1], hop.bidirectional);
  const flags = { running: group.running, ready: hop.ready };
  const key = `hop:${index}`;
  const outbound = formatDirBadgeMetric(hop.forward, flags);
  let label = `${hop.from.name} 至 ${hop.to.name}，${outbound}`;
  if (hop.bidirectional) label = `${hop.from.name} 与 ${hop.to.name}，去程 ${outbound}，回程 ${formatDirBadgeMetric(hop.reverse, flags)}`;
  const element = createSVGEl("g", {
    class: focus.dimHop(index) ? "a-lm-hop is-dim" : "a-lm-hop",
    tabindex: 0,
    role: "button",
    "aria-pressed": String(focus.key === key),
    "aria-label": label,
    "data-lm-key": key,
    "data-refresh-key": `lm-hop-${index}`,
  });
  const forwardTone = railTone(hop.forward), reverseTone = railTone(hop.reverse);
  element.append(
    titleElement(label),
    createSVGEl("rect", { ...shape.box, rx: 6, class: "a-lm-focus" }),
    createSVGEl("path", { d: shape.forward, class: `a-lm-rail is-fwd ${forwardTone}`.trim() }));
  if (hop.bidirectional) element.append(createSVGEl("path", { d: shape.reverse, class: `a-lm-rail is-rev ${reverseTone}`.trim() }));
  element.append(...railMark(shape.forwardApex, forwardTone));
  if (hop.bidirectional) element.append(...railMark(shape.reverseApex, reverseTone));
  element.append(createSVGEl("path", { d: shape.hit, class: hop.bidirectional ? "a-lm-hit is-lens" : "a-lm-hit is-line" }));
  return element;
}

// Names sit on the side away from the link; a relay between two neighbours
// is named below its pip.
function nameSide(index, spots, width, units) {
  const spot = spots[index];
  const pulls = [spots[index - 1], spots[index + 1]].filter(Boolean).map(other => other.x - spot.x).filter(dx => Math.abs(dx) >= 12);
  const pull = pulls.reduce((sum, dx) => sum + Math.sign(dx), 0);
  const room = units * UNIT_PX + 16;
  let side = pull > 0 ? -1 : 1;
  if (pull === 0 && pulls.length) side = 0;
  if (side > 0 && spot.x + room > width - 4) side = -1;
  if (side < 0 && spot.x - room < 4) side = spot.x + room > width - 4 ? 0 : 1;
  return side;
}

function pipElement(item, x, y) {
  if (item.kind === "ext") {
    const d = `M${x} ${fixed(y - 6)}L${fixed(x + 6)} ${y}L${x} ${fixed(y + 6)}L${fixed(x - 6)} ${y}Z`;
    return createSVGEl("path", { d, class: "a-lm-pip is-ext" });
  }
  let state = "";
  if (item.issue) state = " is-bad";
  else if (!item.online) state = " is-off";
  return createSVGEl("rect", { x: fixed(x - PIP / 2), y: fixed(y - PIP / 2), width: PIP, height: PIP, class: `a-lm-pip${state}` });
}

function nodeElement(item, index, spots, focus, size) {
  const x = fixed(spots[index].x), y = fixed(spots[index].y);
  const key = `node:${index}`;
  let label = `${item.role} ${item.name}`;
  if (item.issue) label += `，${item.issue}`;
  else if (item.kind !== "ext" && !item.online) label += "，离线";
  const name = clipLabel(item.name, NAME_UNITS);
  const side = nameSide(index, spots, size.width, visualUnits(name));
  let nameX = x + side * 13, nameY = y + 4, anchor = side < 0 ? "end" : "start";
  if (side === 0) { nameX = x; anchor = "middle"; nameY = y + 34 > size.height ? y - 15 : y + 25; }
  const element = createSVGEl("g", {
    class: focus.dimNode(index) ? "a-lm-node is-dim" : "a-lm-node",
    tabindex: 0,
    role: "button",
    "aria-pressed": String(focus.key === key),
    "aria-label": label,
    "data-lm-key": key,
    "data-refresh-key": `lm-node-${index}`,
  });
  element.append(
    titleElement(label),
    createSVGEl("rect", { x: fixed(x - 8), y: fixed(y - 8), width: 16, height: 16, class: "a-lm-bed" }),
    createSVGEl("rect", { x: fixed(x - 10), y: fixed(y - 10), width: 20, height: 20, rx: 3, class: "a-lm-ring" }),
    pipElement(item, x, y),
    createSVGEl("text", { x: fixed(nameX), y: fixed(nameY), "text-anchor": anchor, class: "a-lm-name" }, name),
    createSVGEl("rect", { x: fixed(x - 20), y: fixed(y - 20), width: 40, height: 40, class: "a-lm-hit" }));
  return element;
}

// Land is one path filled with a dot pattern. The pattern is sized against
// the frame scale, so the dots keep one pitch on screen at every zoom.
function backdrop(frame) {
  const pattern = createSVGEl("pattern", { id: DOTS_ID, width: DOT_PITCH / frame.scale, height: DOT_PITCH / frame.scale, patternUnits: "userSpaceOnUse" });
  pattern.append(createSVGEl("rect", { width: DOT_SIZE / frame.scale, height: DOT_SIZE / frame.scale, class: "a-lm-dot" }));
  const defs = createSVGEl("defs", { "data-refresh-key": "lm-defs" });
  defs.append(pattern, createSVGEl("path", { id: LAND_ID, d: PACIFIC_LAND_PATH }));
  const world = createSVGEl("g", { transform: `translate(${frame.tx} ${frame.ty}) scale(${frame.scale})`, "aria-hidden": "true", "data-refresh-key": "lm-world" });
  for (const offset of WORLD_COPIES) world.append(createSVGEl("use", { href: `#${LAND_ID}`, x: offset, fill: `url(#${DOTS_ID})` }));
  return [defs, world];
}

// The link of one monitoring group on the map. Hops and nodes are buttons
// keyed "hop:<index>" and "node:<index>"; the caller owns what a press does.
export function renderLinkMap(group, { width = 880, focusKey = "" } = {}) {
  const size = linkMapSize(width);
  const frame = fitFrame(locate(group.nodes), size.width, size.height);
  const spots = place(locate(group.nodes), frame, size.width, size.height);
  const focus = focusState(focusKey);
  const svg = createSVGEl("svg", {
    class: "a-lm",
    viewBox: `0 0 ${size.width} ${size.height}`,
    role: "group",
    "aria-label": `${group.groupName} 链路图`,
    "data-refresh-key": "lm-svg",
  });
  const hops = createSVGEl("g", { "data-refresh-key": "lm-hops" });
  hops.append(...group.hops.map((hop, index) => hopElement(group, hop, index, spots, focus)));
  const nodes = createSVGEl("g", { "data-refresh-key": "lm-nodes" });
  nodes.append(...group.nodes.map((item, index) => nodeElement(item, index, spots, focus, size)));
  svg.append(...backdrop(frame), hops, nodes);
  return svg;
}

function tracePath(series, count, scale) {
  let d = "", pen = false;
  series.forEach((value, index) => {
    if (!isNumber(value)) { pen = false; return; }
    const x = count > 1 ? index / (count - 1) * 120 : 120;
    d += `${pen ? "L" : "M"}${fixed(x)} ${fixed(25 - (value - scale.low) / (scale.high - scale.low) * 22)}`;
    pen = true;
  });
  return d;
}

// The trace is scaled to the spread of its samples, with a floor on that
// spread so a steady link draws as a level line, not as noise.
function traceScale(values) {
  const low = Math.min(...values), high = Math.max(...values);
  const spread = Math.max(high - low, high * 0.1, 1);
  return { low: Math.max(0, low - spread), high: high + spread * 0.25 };
}

// Recent samples of both directions on one scale, oldest first: outbound
// solid, return dashed. Returns null until two samples exist.
export function renderLinkTrace(group) {
  const forward = probeHistorySeries(group.history, "forward").slice(-TRACE_SAMPLES).map(sample => sample.avgMS);
  const reverse = group.bidirectional ? probeHistorySeries(group.history, "reverse").slice(-TRACE_SAMPLES).map(sample => sample.avgMS) : NO_VALUES;
  const values = [...forward, ...reverse].filter(isNumber);
  if (Math.max(forward.filter(isNumber).length, reverse.filter(isNumber).length) < 2) return null;
  const scale = traceScale(values);
  const count = Math.max(forward.length, reverse.length);
  const svg = createSVGEl("svg", { class: "a-con-trace a-lm-trace", viewBox: "0 0 120 28", preserveAspectRatio: "none", "aria-hidden": "true" });
  svg.append(
    createSVGEl("line", { x1: 0, y1: 27.5, x2: 120, y2: 27.5, class: "a-con-trace-base", "vector-effect": "non-scaling-stroke" }),
    createSVGEl("path", { d: tracePath(forward, count, scale), class: "a-con-trace-line", "vector-effect": "non-scaling-stroke" }));
  if (reverse.length) svg.append(createSVGEl("path", { d: tracePath(reverse, count, scale), class: "a-con-trace-line a-lm-trace-rev", "vector-effect": "non-scaling-stroke" }));
  return { svg, count };
}
