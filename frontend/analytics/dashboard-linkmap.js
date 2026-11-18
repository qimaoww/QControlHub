import { probeHistorySeries } from "../modules/dashboard-model.js";
import { createSVGEl } from "./dashboard-panel.js";
import { clipLabel, formatDirBadgeMetric, resolveNodeGeoPoint, visualUnits } from "./dashboard-topo-shared.js";
import { PACIFIC_LAND_PATH, PACIFIC_MAP_BOUNDS, projectPacificGeo } from "./pacific-map-data.js";

// The dashboard link map. One monitored link is drawn on a dot-matrix globe.
// The camera looks down at the link from the south with the horizon beyond
// it, so each direction rises off the surface as its own arch. Figures stay
// in the rail beside the map; the map shows where the link runs, which way
// each direction travels and which one is in trouble.
const RAD = Math.PI / 180;
const NORTH = Object.freeze([0, 0, 1]);
const HORIZON = 18;
const ANCHOR = 0.56;
const SIN_MAX = 0.766;
const SIN_MIN = 0.3;
const GLOW = 22;
const VEIL = 44;
const DOT_PITCH = 4.8;
const DRAFT_PITCH = 7.6;
const NEAR_DEPTH = 0.5;
const MID_DEPTH = 0.24;
const ARC_STEPS = 48;
const FLOAT_X = 96;
const FLOAT_Y = 38;
const MASK_STEP = 0.25;
const MASK_COLS = 1440;
const MASK_ROWS = 560;
const MASK_LAT_MIN = -56;
const MASK_LAT_MAX = 84;
const MASK_SCALE = 2;
const PIP = 9;
const NAME_UNITS = 18;
const UNIT_PX = 6.5;
const TRACE_SAMPLES = 40;
const SHADE_ID = "a-lm-shade";
const GLOW_ID = "a-lm-glow";
const VEIL_ID = "a-lm-veil";
const COMET_PARTS = Object.freeze(["is-wake", "is-body", "is-head"]);
const NO_VALUES = Object.freeze([]);

const clamp = (value, low, high) => Math.max(low, Math.min(high, value));
const fixed = (value) => Number(value.toFixed(1));
const isNumber = (value) => typeof value === "number" && Number.isFinite(value);
const dot3 = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
const cross3 = (a, b) => [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]];
const mix3 = (a, ka, b, kb) => [a[0] * ka + b[0] * kb, a[1] * ka + b[1] * kb, a[2] * ka + b[2] * kb];

function unit3(vector) {
  const length = Math.hypot(vector[0], vector[1], vector[2]);
  return length < 1e-9 ? null : [vector[0] / length, vector[1] / length, vector[2] / length];
}

function geoVector(lat, lon) {
  const phi = lat * RAD, lam = lon * RAD;
  return [Math.cos(phi) * Math.cos(lam), Math.cos(phi) * Math.sin(lam), Math.sin(phi)];
}

const DEFAULT_LOOK = Object.freeze(geoVector(30, 120));

function titleElement(text) {
  const element = createSVGEl("title");
  element.textContent = text;
  return element;
}

// The stage is drawn at the pixel size it is shown at, so type and strokes
// keep their size at every width; only the height is bounded.
export function linkMapSize(width) {
  const w = Math.round(clamp(Number(width) || 0, 280, 1600));
  return { width: w, height: Math.round(clamp(w * 0.36, 220, 320)) };
}

// Place on the globe of every node that has one, as a unit vector.
function locate(nodes) {
  return nodes.map((item) => {
    if (item.kind === "ext") return null;
    const geo = resolveNodeGeoPoint({ name: item.name, id: item.agentID, regionCode: item.region }, 0);
    return geo.matched ? geoVector(geo.lat, geo.lon) : null;
  });
}

// The camera is a point to look at and a globe radius in stage pixels. The
// top of the limb stays HORIZON below the stage top, so a small radius shows
// the whole globe and a large one a low pass under its horizon.
function makeView(camera, size) {
  const look = camera.look, radius = camera.radius;
  const east = unit3(cross3(NORTH, look)) || [1, 0, 0];
  const north = cross3(look, east);
  const sin = clamp(1 - (size.height * ANCHOR - HORIZON) / radius, SIN_MIN, SIN_MAX);
  const cos = Math.sqrt(1 - sin * sin);
  return {
    width: size.width,
    height: size.height,
    radius,
    cx: size.width / 2,
    cy: HORIZON + radius,
    right: east,
    up: mix3(north, cos, look, sin),
    front: mix3(look, cos, north, -sin),
  };
}

function project(view, point, lift = 1) {
  const reach = view.radius * lift;
  return { x: view.cx + reach * dot3(point, view.right), y: view.cy - reach * dot3(point, view.up), z: lift * dot3(point, view.front) };
}

function fits(view, vectors) {
  const side = clamp(view.width * 0.14, 44, 120);
  return vectors.every((vector) => {
    const p = project(view, vector);
    return p.z > 0.12 && p.x >= side && p.x <= view.width - side && p.y >= HORIZON + 36 && p.y <= view.height - 30;
  });
}

// Closest camera that still holds every placed node of the group.
export function linkMapCamera(group, width) {
  const size = linkMapSize(width);
  const vectors = locate(group.nodes).filter(Boolean);
  let look = DEFAULT_LOOK;
  if (vectors.length) look = unit3(vectors.reduce((sum, vector) => mix3(sum, 1, vector, 1), [0, 0, 0])) || vectors[0];
  // The smallest globe is the largest that still shows whole.
  const floor = (size.height - HORIZON - 14) / 2;
  let radius = (size.height * ANCHOR - HORIZON) / (1 - SIN_MAX);
  while (radius > floor && !fits(makeView({ look, radius }, size), vectors)) radius *= 0.94;
  return { look, radius: Math.max(radius, floor) };
}

export function sameLinkCamera(a, b) {
  return Boolean(a && b) && Math.abs(a.radius - b.radius) < 0.5 && dot3(a.look, b.look) > 0.999999;
}

// A camera part of the way from one to another. A long turn pulls back
// from the globe on the way, so the move reads as a flight, not a slide.
export function blendLinkCamera(from, to, t) {
  const look = unit3(mix3(from.look, 1 - t, to.look, t)) || to.look;
  const turn = Math.acos(clamp(dot3(from.look, to.look), -1, 1));
  const pull = 1 - 0.3 * Math.sin(Math.PI * t) * clamp(turn / 1.2, 0, 1);
  return { look, radius: from.radius * Math.pow(to.radius / from.radius, t) * pull };
}

// Where the camera starts from when the map first appears: further out and
// a good way west of the link.
export function openingLinkCamera(camera) {
  const cos = Math.cos(-0.7), sin = Math.sin(-0.7);
  const look = [camera.look[0] * cos - camera.look[1] * sin, camera.look[0] * sin + camera.look[1] * cos, camera.look[2]];
  return { look, radius: camera.radius * 0.62 };
}

function maskRow(mask, pixels, stride, row) {
  const lat = MASK_LAT_MIN + (row + 0.5) * MASK_STEP;
  const mid = projectPacificGeo(lat, PACIFIC_MAP_BOUNDS.lon0);
  const perDegree = (projectPacificGeo(lat, PACIFIC_MAP_BOUNDS.lon0 + 90).x - mid.x) / 90;
  const line = Math.round(mid.y * MASK_SCALE) * stride;
  for (let col = 0; col < MASK_COLS; col++) {
    const delta = ((col + 0.5) * MASK_STEP - PACIFIC_MAP_BOUNDS.lon0 + 540) % 360 - 180;
    const px = Math.round((mid.x + delta * perDegree) * MASK_SCALE);
    if (pixels[(line + px) * 4 + 3] > 127) mask[row * MASK_COLS + col] = 1;
  }
}

// Land as a quarter-degree bitmap, read once from the map's own land path.
// Without a canvas the globe is drawn without land.
function buildLandMask() {
  const mask = new Uint8Array(MASK_COLS * MASK_ROWS);
  if (typeof document === "undefined" || typeof Path2D === "undefined") return mask;
  const canvas = document.createElement("canvas");
  canvas.width = PACIFIC_MAP_BOUNDS.width * MASK_SCALE;
  canvas.height = PACIFIC_MAP_BOUNDS.height * MASK_SCALE;
  const pen = canvas.getContext("2d", { willReadFrequently: true });
  if (!pen) return mask;
  pen.scale(MASK_SCALE, MASK_SCALE);
  pen.fill(new Path2D(PACIFIC_LAND_PATH));
  const pixels = pen.getImageData(0, 0, canvas.width, canvas.height).data;
  for (let row = 0; row < MASK_ROWS; row++) maskRow(mask, pixels, canvas.width, row);
  return mask;
}

let landMask = null;
let dotCache = { key: "", near: "", mid: "", far: "" };

// One parallel of land dots. Dots are spaced evenly on the sphere, so they
// close up towards the limb the way the surface does.
function dotRow(out, view, mask, lat, step, row) {
  const cosPhi = Math.cos(lat * RAD), sinPhi = Math.sin(lat * RAD);
  const count = Math.max(1, Math.round(360 * cosPhi / step));
  const turn = 2 * Math.PI / count, cosTurn = Math.cos(turn), sinTurn = Math.sin(turn);
  const start = (row % 2) * turn / 2;
  const base = Math.min(MASK_ROWS - 1, Math.floor((lat - MASK_LAT_MIN) / MASK_STEP)) * MASK_COLS;
  const { right, up, front, radius, cx, cy, width, height } = view;
  let c = Math.cos(start), s = Math.sin(start);
  for (let index = 0; index < count; index++) {
    const z = cosPhi * (front[0] * c + front[1] * s) + sinPhi * front[2];
    const x = cx + radius * (cosPhi * (right[0] * c + right[1] * s) + sinPhi * right[2]);
    const y = cy - radius * (cosPhi * (up[0] * c + up[1] * s) + sinPhi * up[2]);
    const col = Math.floor((start + index * turn) / (2 * Math.PI) * MASK_COLS) % MASK_COLS;
    const next = c * cosTurn - s * sinTurn;
    s = s * cosTurn + c * sinTurn;
    c = next;
    if (z < 0.03 || x < -2 || x > width + 2 || y < -2 || y > height + 2 || !mask[base + col]) continue;
    const mark = `M${Math.round(x * 10) / 10} ${Math.round(y * 10) / 10}h.01`;
    if (z > NEAR_DEPTH) out.near += mark;
    else if (z > MID_DEPTH) out.mid += mark;
    else out.far += mark;
  }
}

// Land dots in view, in three depth bands. The last view is kept, so an
// ordinary refresh does not walk the globe again.
function landDots(view, pitch) {
  const key = [pitch, view.width, view.height, view.radius.toFixed(2), ...view.front, ...view.right].map(Number).map(n => n.toFixed(4)).join(" ");
  if (dotCache.key === key) return dotCache;
  if (!landMask) landMask = buildLandMask();
  const step = clamp(pitch / view.radius / RAD, 0.3, 6);
  const out = { key, near: "", mid: "", far: "" };
  let row = 0;
  for (let lat = MASK_LAT_MIN + step / 2; lat < MASK_LAT_MAX; lat += step) dotRow(out, view, landMask, lat, step, row++);
  dotCache = out;
  return out;
}

function polyline(view, count, pointAt) {
  let d = "", pen = false;
  for (let index = 0; index <= count; index++) {
    const p = project(view, pointAt(index / count));
    const seen = p.z > 0 && p.x > -40 && p.x < view.width + 40 && p.y > -40 && p.y < view.height + 40;
    if (seen) d += `${pen ? "L" : "M"}${fixed(p.x)} ${fixed(p.y)}`;
    pen = seen;
  }
  return d;
}

function graticule(view) {
  let d = "";
  for (let lon = 0; lon < 360; lon += 30) d += polyline(view, 56, t => geoVector(-84 + t * 168, lon));
  for (let lat = -60; lat <= 60; lat += 30) d += polyline(view, 120, t => geoVector(lat, t * 360));
  return d;
}

function stop(offset, name) {
  return createSVGEl("stop", { offset, class: name });
}

// The globe: glow beyond the limb, shaded disc, graticule, land and the
// limb itself, with a veil that lets the globe fade out at the stage foot.
// A draft has fewer land dots; it is what a camera in flight draws.
function backdrop(view, draft) {
  const cx = fixed(view.cx), cy = fixed(view.cy), radius = fixed(view.radius);
  const shade = createSVGEl("radialGradient", { id: SHADE_ID });
  shade.append(stop(0.6, "a-lm-stop-paper"), stop(1, "a-lm-stop-rim"));
  const glow = createSVGEl("radialGradient", { id: GLOW_ID });
  glow.append(stop(Number((view.radius / (view.radius + GLOW)).toFixed(4)), "a-lm-stop-glow"), stop(1, "a-lm-stop-clear"));
  const veil = createSVGEl("linearGradient", { id: VEIL_ID, x1: 0, y1: 0, x2: 0, y2: 1 });
  veil.append(stop(0, "a-lm-stop-veil"), stop(1, "a-lm-stop-paper"));
  const defs = createSVGEl("defs", { "data-refresh-key": "lm-defs" });
  defs.append(shade, glow, veil);
  const dots = landDots(view, draft ? DRAFT_PITCH : DOT_PITCH);
  const globe = createSVGEl("g", { "aria-hidden": "true", "data-refresh-key": "lm-globe" });
  globe.append(
    createSVGEl("circle", { cx, cy, r: fixed(view.radius + GLOW), fill: `url(#${GLOW_ID})` }),
    createSVGEl("circle", { cx, cy, r: radius, fill: `url(#${SHADE_ID})` }),
    createSVGEl("path", { d: graticule(view), class: "a-lm-grid" }),
    createSVGEl("path", { d: dots.far, class: "a-lm-land is-far" }),
    createSVGEl("path", { d: dots.mid, class: "a-lm-land is-mid" }),
    createSVGEl("path", { d: dots.near, class: "a-lm-land" }),
    createSVGEl("circle", { cx, cy, r: radius, class: "a-lm-limb" }),
    createSVGEl("rect", { x: 0, y: view.height - VEIL, width: view.width, height: VEIL, fill: `url(#${VEIL_ID})` }));
  return [defs, globe];
}

// Stage position of a placed node. One that has turned behind the globe
// is held on the limb.
function landing(view, vector) {
  const p = project(view, vector);
  if (p.z >= 0.05) return { x: p.x, y: p.y, on: true };
  const dx = p.x - view.cx, dy = p.y - view.cy;
  const reach = view.radius * 0.985 / Math.max(1, Math.hypot(dx, dy));
  return { x: view.cx + dx * reach, y: view.cy + dy * reach, on: false };
}

// A node without a place on the globe. A public target floats in the corner
// away from its neighbour; anything else is set beside its neighbour.
function beside(anchor, item, view, way) {
  if (item.kind === "ext") return { x: anchor.x < view.width / 2 ? view.width - FLOAT_X : FLOAT_X, y: FLOAT_Y, on: false };
  const step = clamp(view.width * 0.24, 84, 220);
  const next = anchor.x + way * step;
  const side = next > view.width - 64 || next < 64 ? -way : way;
  return { x: anchor.x + side * step, y: anchor.y, on: false };
}

// Nodes in the same city share a point; set them side by side instead.
function spread(spots) {
  spots.forEach((spot, index) => {
    for (let pass = 0; pass < spots.length; pass++) {
      const clash = spots.slice(0, index).some(other => Math.abs(other.x - spot.x) < 40 && Math.abs(other.y - spot.y) < 18);
      if (!clash) break;
      spot.x += 56;
      spot.on = false;
    }
  });
  return spots;
}

function place(view, vectors, nodes) {
  const { width, height } = view;
  const spots = vectors.map(vector => vector ? landing(view, vector) : null);
  const first = spots.findIndex(Boolean);
  if (first < 0) return spots.map((_, index) => ({ x: width * (index + 1) / (spots.length + 1), y: height * ANCHOR, on: false }));
  for (let index = first - 1; index >= 0; index--) spots[index] = beside(spots[index + 1], nodes[index], view, -1);
  for (let index = first + 1; index < spots.length; index++) {
    if (!spots[index]) spots[index] = beside(spots[index - 1], nodes[index], view, 1);
  }
  return spread(spots.map(spot => ({ x: clamp(spot.x, 30, width - 30), y: clamp(spot.y, 24, height - 26), on: spot.on })));
}

// Points of a great-circle run from a to b that rises off the surface by
// `lift` globe radii at its middle.
function arcPoints(view, a, b, lift) {
  const omega = Math.acos(clamp(dot3(a, b), -1, 1));
  const sinOmega = Math.sin(omega);
  const points = new Array(ARC_STEPS + 1);
  for (let index = 0; index <= ARC_STEPS; index++) {
    const t = index / ARC_STEPS;
    const point = mix3(a, Math.sin((1 - t) * omega) / sinOmega, b, Math.sin(t * omega) / sinOmega);
    points[index] = project(view, point, 1 + lift * Math.sin(Math.PI * t));
  }
  return points;
}

// A point is drawn unless the globe is in front of it.
function linePath(view, points) {
  let d = "", pen = false;
  for (const p of points) {
    const seen = p.z >= 0 || Math.hypot(p.x - view.cx, p.y - view.cy) >= view.radius;
    if (seen) d += `${pen ? "L" : "M"}${fixed(p.x)} ${fixed(p.y)}`;
    pen = seen;
  }
  return d;
}

function boundsOf(points) {
  const xs = points.map(p => p.x), ys = points.map(p => p.y);
  const left = Math.min(...xs) - 10, top = Math.min(...ys) - 10;
  return { x: fixed(left), y: fixed(top), width: fixed(Math.max(...xs) + 10 - left), height: fixed(Math.max(...ys) + 10 - top) };
}

// A hop between two placed nodes: the outbound direction is the high arch,
// the return the low one, and a track on the surface marks the ground under
// both. Each wake path runs against its direction; comets are drawn on it.
function archShape(view, a, b, twoWay) {
  const mid = unit3(mix3(a, 1, b, 1));
  if (!mid || dot3(a, b) > 0.999999) return null;
  const from = project(view, a), to = project(view, b), ground = project(view, mid);
  const span = Math.hypot(to.x - from.x, to.y - from.y);
  const rise = Math.max(12, Math.min(clamp(span * 0.32, 26, 92), ground.y - 14));
  const upness = Math.sqrt(Math.max(0, 1 - dot3(mid, view.front) ** 2));
  const lift = clamp(rise / (view.radius * Math.max(upness, 0.3)), 0.02, 0.7);
  const outer = arcPoints(view, a, b, lift), inner = arcPoints(view, b, a, lift * 0.5);
  const forward = linePath(view, outer), reverse = linePath(view, inner);
  return {
    forward,
    reverse,
    forwardWake: linePath(view, [...outer].reverse()),
    reverseWake: linePath(view, [...inner].reverse()),
    track: linePath(view, arcPoints(view, a, b, 0)),
    hit: twoWay ? forward + reverse : forward,
    forwardApex: outer[ARC_STEPS / 2],
    reverseApex: inner[ARC_STEPS / 2],
    box: boundsOf(twoWay ? [...outer, ...inner] : outer),
  };
}

// A hop with an end that has no place on the globe is drawn flat: the
// outbound direction bows upwards and the return the other way.
function lensShape(from, to, twoWay) {
  const dx = to.x - from.x, dy = to.y - from.y;
  const length = Math.max(1, Math.hypot(dx, dy));
  const bow = clamp(length * 0.17, 12, 40) * (twoWay ? 1 : 0.6);
  const flip = dx < 0 || (dx === 0 && dy > 0) ? -1 : 1;
  const nx = flip * dy / length * bow, ny = -flip * dx / length * bow;
  const mx = (from.x + to.x) / 2, my = (from.y + to.y) / 2;
  const start = `${fixed(from.x)} ${fixed(from.y)}`, end = `${fixed(to.x)} ${fixed(to.y)}`;
  const out = `${fixed(mx + 2 * nx)} ${fixed(my + 2 * ny)}`, back = `${fixed(mx - 2 * nx)} ${fixed(my - 2 * ny)}`;
  const forward = `M${start}Q${out} ${end}`, reverse = `M${end}Q${back} ${start}`;
  const forwardApex = { x: mx + nx, y: my + ny }, reverseApex = { x: mx - nx, y: my - ny };
  return {
    forward,
    reverse,
    forwardWake: `M${end}Q${out} ${start}`,
    reverseWake: `M${start}Q${back} ${end}`,
    track: "",
    hit: twoWay ? forward + reverse : forward,
    forwardApex,
    reverseApex,
    box: boundsOf(twoWay ? [from, to, forwardApex, reverseApex] : [from, to, forwardApex]),
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

// A comet runs along a direction that is carrying probes, at a pace set by
// its latency. It is the only cue for which way a rail travels.
function comets(wake, group, hop, dir, tone) {
  const live = hop.ready && !group.running && dir?.available && dir.reached && isNumber(dir.avgMS);
  if (!live || !wake) return NO_VALUES;
  let pace = "is-slow";
  if (dir.avgMS <= 60) pace = "is-quick";
  else if (dir.avgMS <= 160) pace = "is-steady";
  return COMET_PARTS.map(part => createSVGEl("path", { d: wake, pathLength: 100, class: `a-lm-comet ${part} ${pace} ${tone}`.trim() }));
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

function hopElement(scene, hop, index) {
  const { view, group, spots, vectors, focus } = scene;
  const from = spots[index], to = spots[index + 1];
  const arch = from.on && to.on ? archShape(view, vectors[index], vectors[index + 1], hop.bidirectional) : null;
  const shape = arch || lensShape(from, to, hop.bidirectional);
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
  element.append(titleElement(label), createSVGEl("rect", { ...shape.box, rx: 6, class: "a-lm-focus" }));
  if (shape.track) element.append(createSVGEl("path", { d: shape.track, class: "a-lm-track" }));
  element.append(createSVGEl("path", { d: shape.forward, class: `a-lm-rail is-fwd ${forwardTone}`.trim() }));
  if (hop.bidirectional) element.append(createSVGEl("path", { d: shape.reverse, class: `a-lm-rail is-rev ${reverseTone}`.trim() }));
  element.append(...comets(shape.forwardWake, group, hop, hop.forward, forwardTone));
  if (hop.bidirectional) element.append(...comets(shape.reverseWake, group, hop, hop.reverse, reverseTone));
  element.append(...railMark(shape.forwardApex, forwardTone));
  if (hop.bidirectional) element.append(...railMark(shape.reverseApex, reverseTone));
  element.append(createSVGEl("path", { d: shape.hit, class: "a-lm-hit is-line" }));
  return element;
}

// Names sit on the side away from the link; a relay between two neighbours,
// or a name with no room on its own side, goes below the pip instead of
// across the link.
function nameSide(index, spots, width, units) {
  const spot = spots[index];
  const pulls = [spots[index - 1], spots[index + 1]].filter(Boolean).map(other => other.x - spot.x).filter(dx => Math.abs(dx) >= 12);
  const pull = pulls.reduce((sum, dx) => sum + Math.sign(dx), 0);
  const room = units * UNIT_PX + 16;
  let side = pull > 0 ? -1 : 1;
  if (pull === 0 && pulls.length) side = 0;
  if (side > 0 && spot.x + room > width - 4) side = pulls.length ? 0 : -1;
  if (side < 0 && spot.x - room < 4) side = 0;
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

function nodeElement(scene, item, index) {
  const { view, spots, focus } = scene;
  const x = fixed(spots[index].x), y = fixed(spots[index].y);
  const key = `node:${index}`;
  let label = `${item.role} ${item.name}`;
  if (item.issue) label += `，${item.issue}`;
  else if (item.kind !== "ext" && !item.online) label += "，离线";
  const name = clipLabel(item.name, NAME_UNITS);
  const units = visualUnits(name);
  const side = nameSide(index, spots, view.width, units);
  const half = units * UNIT_PX / 2 + 4;
  let nameX = x + side * 13, nameY = y + 4, anchor = side < 0 ? "end" : "start";
  if (side === 0) { nameX = clamp(x, half, view.width - half); anchor = "middle"; nameY = y + 34 > view.height ? y - 15 : y + 25; }
  const element = createSVGEl("g", {
    class: focus.dimNode(index) ? "a-lm-node is-dim" : "a-lm-node",
    tabindex: 0,
    role: "button",
    "aria-pressed": String(focus.key === key),
    "aria-label": label,
    "data-lm-key": key,
    "data-refresh-key": `lm-node-${index}`,
  });
  element.append(titleElement(label));
  // An online node pings; the ring is only shown where motion is allowed.
  if (item.kind !== "ext" && item.online && !item.issue) element.append(createSVGEl("circle", { cx: x, cy: y, r: 5, class: "a-lm-ping", "vector-effect": "non-scaling-stroke" }));
  element.append(
    createSVGEl("rect", { x: fixed(x - 8), y: fixed(y - 8), width: 16, height: 16, class: "a-lm-bed" }),
    createSVGEl("rect", { x: fixed(x - 10), y: fixed(y - 10), width: 20, height: 20, rx: 3, class: "a-lm-ring" }),
    pipElement(item, x, y),
    createSVGEl("text", { x: fixed(nameX), y: fixed(nameY), "text-anchor": anchor, class: "a-lm-name" }, name),
    createSVGEl("rect", { x: fixed(x - 20), y: fixed(y - 20), width: 40, height: 40, class: "a-lm-hit" }));
  return element;
}

// The link of one monitoring group on the globe. Hops and nodes are buttons
// keyed "hop:<index>" and "node:<index>"; the caller owns what a press does
// and may pass a camera of its own to move the view.
export function renderLinkMap(group, { width = 880, focusKey = "", camera = null, draft = false } = {}) {
  const size = linkMapSize(width);
  const view = makeView(camera || linkMapCamera(group, width), size);
  const vectors = locate(group.nodes);
  const scene = { view, group, vectors, spots: place(view, vectors, group.nodes), focus: focusState(focusKey) };
  const svg = createSVGEl("svg", {
    class: "a-lm",
    viewBox: `0 0 ${size.width} ${size.height}`,
    role: "group",
    "aria-label": `${group.groupName} 链路图`,
    "data-refresh-key": "lm-svg",
  });
  const hops = createSVGEl("g", { "data-refresh-key": "lm-hops" });
  hops.append(...group.hops.map((hop, index) => hopElement(scene, hop, index)));
  const nodes = createSVGEl("g", { "data-refresh-key": "lm-nodes" });
  nodes.append(...group.nodes.map((item, index) => nodeElement(scene, item, index)));
  svg.append(...backdrop(view, draft), hops, nodes);
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
