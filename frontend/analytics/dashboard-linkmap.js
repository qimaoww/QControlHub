import { probeHistorySeries } from "../modules/dashboard-model.js";
import { geoRegionDetails } from "../modules/region-model.js";
import { createSVGEl } from "./dashboard-panel.js";
import { clipLabel, formatDirBadgeMetric, resolveNodeGeoPoint, visualUnits } from "./dashboard-topo-shared.js";
import { PACIFIC_LAND_PATH, PACIFIC_MAP_BOUNDS, projectPacificGeo } from "./pacific-map-data.js";

// Flagship 3D Topology Globe shared by #dashboard and #topology.
// Ceramic-white dot-matrix 3D sphere + architectural twin-wall conduits +
// dual-layer optical laser sweep + regional cluster zoom unfolding.
const RAD = Math.PI / 180;
const NORTH = Object.freeze([0, 0, 1]);
const HORIZON = 16;
const ANCHOR = 0.56;
const SIN_MAX = 0.766;
const SIN_MIN = 0.06;
const GLOW = 26;
const VEIL = 42;
const DOT_PITCH = 4.35;
const DRAFT_PITCH = 7.2;
const NEAR_DEPTH = 0.52;
const MID_DEPTH = 0.25;
const ARC_STEPS = 52;
const FLOAT_X = 96;
const FLOAT_Y = 44;
const MASK_STEP = 0.25;
const MASK_COLS = 1440;
const MASK_ROWS = 560;
const MASK_LAT_MIN = -56;
const MASK_LAT_MAX = 84;
const MASK_SCALE = 2;
const PIP = 9;
const NAME_UNITS = 18;
const UNIT_PX = 6.5;
const FLAG_W = 14;
const FLAG_H = 10;
const FLAG_GAP = 4;
const STEP_W = 14;
const STEP_GAP = 4;
const FLEET_FLAG_W = 12;
const FLEET_FLAG_H = 8.5;
const FLEET_FLAG_GAP = 3.5;
const TRACE_SAMPLES = 40;
const SHADE_ID = "a-lm-shade";
const GLOW_ID = "a-lm-glow";
const VEIL_ID = "a-lm-veil";
const STAGGER_SLOTS = 6;
const COMPARE_COLOR_SLOTS = 4;
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

const DEFAULT_LOOK = Object.freeze(geoVector(26, 108));

export function compareColorClass(slot = 0) {
  const norm = ((Number(slot) || 0) % COMPARE_COLOR_SLOTS + COMPARE_COLOR_SLOTS) % COMPARE_COLOR_SLOTS;
  return `is-cmp-${norm}`;
}

function shortNodeName(name) {
  const s = String(name || "").trim();
  const idx = s.indexOf("·");
  if (idx > 0) return s.slice(0, idx).trim();
  return s;
}

// Only mark real latency/loss degradation, not merely offline historical nodes.
function markToneForDir(dir) {
  if (!dir || !dir.available) return "";
  if (dir.state === "failed" || dir.error || !dir.reached || (dir.lossPercent ?? 0) >= 100) return "bad";
  if ((dir.lossPercent ?? 0) > 0 || (isNumber(dir.avgMS) && dir.avgMS >= 220)) return "warn";
  return "";
}

function resolveConduitTone({ combinedMark, isL1, pinnedSlot, isIdle = false }) {
  if (combinedMark === "bad") return "is-tone-bad";
  if (combinedMark === "warn") return "is-tone-warn";
  if (isIdle) return "is-tone-idle";
  if (isL1 && pinnedSlot != null) return compareColorClass(pinnedSlot);
  return isL1 ? "is-tone-accent" : "is-tone-quiet";
}

function titleElement(text) {
  const element = createSVGEl("title");
  element.textContent = text;
  return element;
}

export function linkMapSize(width, { minHeight = 220, maxHeight = 320, ratio = 0.36 } = {}) {
  const w = Math.round(clamp(Number(width) || 0, 280, 1600));
  return { width: w, height: Math.round(clamp(w * ratio, minHeight, maxHeight)) };
}

function resolveNodeGeo(item) {
  if (!item || item.kind === "ext") return { vector: null, flag: null };
  const geo = resolveNodeGeoPoint({ name: item.name, id: item.agentID, regionCode: item.region }, 0);
  const rawRegion = String(item.region || item.regionCode || item.region_code || "").trim().toUpperCase();
  const flagCodeSrc = (rawRegion === "CN" && (geo?.country === "HK" || geo?.country === "MO"))
    ? geo.country
    : (rawRegion || geo?.country || "");
  const flag = geoRegionDetails(flagCodeSrc);
  return {
    vector: geo.matched ? geoVector(geo.lat, geo.lon) : null,
    flag: flag?.flagCode ? flag : null,
  };
}

function locateNodes(nodes) {
  return nodes.map(resolveNodeGeo);
}

// Computes the minimax spherical bounding center of unique vectors so dense
// regional clusters never pull distant trans-oceanic endpoints behind the limb.
function sphericalCenter(vectors) {
  if (!vectors.length) return DEFAULT_LOOK;
  const unique = [];
  for (const v of vectors) {
    if (!unique.some(u => dot3(u, v) > 0.999999)) unique.push(v);
  }
  if (unique.length === 1) return unique[0];
  let center = unit3(unique.reduce((sum, vec) => mix3(sum, 1, vec, 1), [0, 0, 0])) || unique[0];
  if (unique.length === 2) return center;
  for (let step = 0; step < 20; step++) {
    let worst = unique[0];
    let minDot = dot3(center, worst);
    for (let i = 1; i < unique.length; i++) {
      const d = dot3(center, unique[i]);
      if (d < minDot) { minDot = d; worst = unique[i]; }
    }
    const weight = 0.36 / (1 + step * 0.25);
    center = unit3(mix3(center, 1, worst, weight)) || center;
  }
  return center;
}

function makeView(camera, size) {
  const look = camera.look, radius = camera.radius;
  const east = unit3(cross3(NORTH, look)) || [1, 0, 0];
  const north = cross3(look, east);
  const anchorY = size.height * ANCHOR;
  const sin = clamp(1 - (anchorY - HORIZON) / radius, SIN_MIN, SIN_MAX);
  const cos = Math.sqrt(1 - sin * sin);
  // Keep globe horizon pinned at HORIZON on global views, and keep `look`
  // centered at `anchorY` during deep regional zoom into close city clusters.
  const cy = Math.min(HORIZON + radius, anchorY + radius * sin);
  return {
    width: size.width,
    height: size.height,
    radius,
    cx: size.width / 2,
    cy,
    right: east,
    up: mix3(north, cos, look, sin),
    front: mix3(look, cos, north, -sin),
  };
}

function project(view, point, lift = 1) {
  const reach = view.radius * lift;
  return { x: view.cx + reach * dot3(point, view.right), y: view.cy - reach * dot3(point, view.up), z: lift * dot3(point, view.front) };
}

function fits(view, vectors, minZ = 0.14) {
  const side = clamp(view.width * 0.12, 36, 112);
  return vectors.every((vector) => {
    const p = project(view, vector);
    return p.z > minZ && p.x >= side && p.x <= view.width - side && p.y >= HORIZON + 28 && p.y <= view.height - 24;
  });
}

function cameraBounds(size) {
  const floor = Math.max(92, (size.height - HORIZON - 16) / 2);
  const ceiling = Math.max(size.width * 3.4, ((size.height * ANCHOR - HORIZON) / (1 - SIN_MAX)) * 3.2, 2400);
  return { floor, ceiling };
}

export function linkMapCamera(group, width, sizeOpts = undefined) {
  const size = linkMapSize(width, sizeOpts);
  const vectors = locateNodes(group.nodes).map(entry => entry.vector).filter(Boolean);
  const look = sphericalCenter(vectors);
  const { floor, ceiling } = cameraBounds(size);
  let maxSpread = 0;
  for (let i = 0; i < vectors.length; i++) {
    for (let j = i + 1; j < vectors.length; j++) {
      maxSpread = Math.max(maxSpread, Math.acos(clamp(dot3(vectors[i], vectors[j]), -1, 1)));
    }
  }
  const minZ = maxSpread > 2.0 ? 0.06 : 0.14;
  // Close regional routes (e.g. Frankfurt<->Amsterdam, HK<->Taipei) zoom in proportionally
  // so nearby stations open up to a clean ~200-280px span instead of huddling in the middle.
  const regionalTarget = maxSpread > 0 && maxSpread < 0.38
    ? Math.min(ceiling, Math.max(size.height * 2.6, Math.min(size.width * 0.44, 280) / Math.max(0.055, maxSpread)))
    : (size.height * ANCHOR - HORIZON) / (1 - SIN_MAX);
  let radius = Math.max(floor, regionalTarget);
  while (radius > floor && !fits(makeView({ look, radius }, size), vectors, minZ)) radius *= 0.94;
  return { look, radius: Math.max(radius, floor) };
}

export function clusterFocusCamera3D(spec, width, sizeOpts = undefined) {
  const raw = String(spec || "");
  if (!raw.startsWith("3d:")) return null;
  const parts = raw.slice(3).split(",").map(Number);
  if (parts.length < 4 || parts.some(v => !Number.isFinite(v))) return null;
  const look = unit3([parts[0], parts[1], parts[2]]);
  if (!look) return null;
  const spread = clamp(parts[3], 0.025, 0.45);
  const size = linkMapSize(width, sizeOpts);
  const { floor, ceiling } = cameraBounds(size);
  const targetSpan = clamp(Math.min(size.width * 0.36, size.height * 0.56), 150, 260);
  const radius = clamp(targetSpan / Math.max(0.055, spread), floor * 1.65, ceiling);
  return { look, radius };
}

export function fleetLinkCamera(groups, width, sizeOpts = undefined) {
  const size = linkMapSize(width, sizeOpts);
  const byID = new Map();
  for (const group of groups || NO_VALUES) {
    for (const item of group.nodes || NO_VALUES) {
      if (item.kind === "ext" || byID.has(item.agentID)) continue;
      const { vector } = resolveNodeGeo(item);
      if (vector) byID.set(item.agentID, vector);
    }
  }
  const vectors = [...byID.values()];
  const { floor, ceiling } = cameraBounds(size);
  if (!vectors.length) return { look: DEFAULT_LOOK, radius: floor };
  let look = vectors.length <= 8
    ? sphericalCenter(vectors)
    : (unit3(vectors.reduce((sum, vec) => mix3(sum, 1, vec, 1), [0, 0, 0])) || DEFAULT_LOOK);
  const clampedZ = clamp(look[2], -0.45, 0.72);
  const horiz = Math.hypot(look[0], look[1]);
  if (horiz > 1e-6) {
    const scale = Math.sqrt(Math.max(0, 1 - clampedZ * clampedZ)) / horiz;
    look = [look[0] * scale, look[1] * scale, clampedZ];
  }
  let radius = Math.min(ceiling, (size.height * ANCHOR - HORIZON) / (1 - SIN_MAX));
  const fitsFleet = (view) => {
    const side = clamp(view.width * 0.08, 28, 72);
    let inside = 0;
    for (const vec of vectors) {
      const p = project(view, vec);
      if (p.z > 0.08 && p.x >= side && p.x <= view.width - side && p.y >= HORIZON + 24 && p.y <= view.height - 26) inside++;
    }
    return inside >= Math.ceil(vectors.length * (vectors.length <= 6 ? 1 : 0.85));
  };
  while (radius > floor && !fitsFleet(makeView({ look, radius }, size))) radius *= 0.93;
  return { look, radius: Math.max(radius, floor) };
}

export function rotateLinkCamera(camera, dx, dy, width) {
  if (!camera) return { look: DEFAULT_LOOK, radius: 180 };
  const sensitivity = 1.65 / Math.max(160, camera.radius, (Number(width) || 600) * 0.45);
  const dLon = -dx * sensitivity;
  const dLat = dy * sensitivity * 0.88;
  const cos = Math.cos(dLon), sin = Math.sin(dLon);
  const spun = [
    camera.look[0] * cos - camera.look[1] * sin,
    camera.look[0] * sin + camera.look[1] * cos,
    camera.look[2],
  ];
  const lat = clamp(Math.asin(clamp(spun[2], -1, 1)) + dLat, -58 * RAD, 66 * RAD);
  const lon = Math.atan2(spun[1], spun[0]);
  return {
    look: [Math.cos(lat) * Math.cos(lon), Math.cos(lat) * Math.sin(lon), Math.sin(lat)],
    radius: camera.radius,
  };
}

export function zoomLinkCamera(camera, factor, size) {
  if (!camera) return { look: DEFAULT_LOOK, radius: 180 };
  const { floor, ceiling } = cameraBounds(size || { width: 800, height: 320 });
  return { look: camera.look, radius: clamp(camera.radius * factor, floor, ceiling) };
}

export function sameLinkCamera(a, b) {
  return Boolean(a && b) && Math.abs(a.radius - b.radius) < 0.5 && dot3(a.look, b.look) > 0.999999;
}

export function blendLinkCamera(from, to, t) {
  const look = unit3(mix3(from.look, 1 - t, to.look, t)) || to.look;
  const turn = Math.acos(clamp(dot3(from.look, to.look), -1, 1));
  const pull = 1 - 0.26 * Math.sin(Math.PI * t) * clamp(turn / 1.2, 0, 1);
  return { look, radius: from.radius * Math.pow(to.radius / from.radius, t) * pull };
}

export function openingLinkCamera(camera) {
  const cos = Math.cos(-0.6), sin = Math.sin(-0.6);
  const look = [camera.look[0] * cos - camera.look[1] * sin, camera.look[0] * sin + camera.look[1] * cos, camera.look[2]];
  return { look, radius: camera.radius * 0.68 };
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
  let major = "", minor = "";
  for (let lon = 0; lon < 360; lon += 30) {
    const seg = polyline(view, 56, t => geoVector(-84 + t * 168, lon));
    if (lon === 0 || lon === 180) major += seg;
    else minor += seg;
  }
  for (let lat = -60; lat <= 60; lat += 30) {
    const seg = polyline(view, 120, t => geoVector(lat, t * 360));
    if (lat === 0) major += seg;
    else minor += seg;
  }
  return { major, minor };
}

function stop(offset, name) {
  return createSVGEl("stop", { offset, class: name });
}

function buildGlowFilter(filterId, size, stdDev = 1.6) {
  const filter = createSVGEl("filter", {
    id: filterId,
    filterUnits: "userSpaceOnUse",
    x: -40,
    y: -40,
    width: size.width + 80,
    height: size.height + 80,
  });
  const blur = createSVGEl("feGaussianBlur", { stdDeviation: String(stdDev), result: "blur" });
  const merge = createSVGEl("feMerge");
  merge.append(createSVGEl("feMergeNode", { in: "blur" }), createSVGEl("feMergeNode", { in: "SourceGraphic" }));
  filter.append(blur, merge);
  return filter;
}

// Studio-lit ceramic-white 3D dot-matrix sphere with optical sweep glow filter.
function backdrop(view, draft, idPrefix = "") {
  const pfx = idPrefix || "lm";
  const shadeId = `${pfx}-${SHADE_ID}`;
  const glowId = `${pfx}-${GLOW_ID}`;
  const veilId = `${pfx}-${VEIL_ID}`;
  const arcGlowId = `${pfx}-arcglow`;
  const cx = fixed(view.cx), cy = fixed(view.cy), radius = fixed(view.radius);

  const shade = createSVGEl("radialGradient", { id: shadeId, cx: "0.38", cy: "0.22", r: "0.76" });
  shade.append(stop(0, "a-lm-stop-paper"), stop(0.66, "a-lm-stop-mid"), stop(1, "a-lm-stop-rim"));
  const glow = createSVGEl("radialGradient", { id: glowId });
  glow.append(stop(Number((view.radius / (view.radius + GLOW)).toFixed(4)), "a-lm-stop-glow"), stop(1, "a-lm-stop-clear"));
  const veil = createSVGEl("linearGradient", { id: veilId, x1: 0, y1: 0, x2: 0, y2: 1 });
  veil.append(stop(0, "a-lm-stop-veil"), stop(1, "a-lm-stop-veil-end"));

  const defs = createSVGEl("defs", { "data-refresh-key": `lm-defs-${pfx}` });
  defs.append(shade, glow, veil, buildGlowFilter(arcGlowId, view, 1.6));

  const grat = graticule(view);
  const dots = landDots(view, draft ? DRAFT_PITCH : DOT_PITCH);
  const globe = createSVGEl("g", { "aria-hidden": "true", "data-refresh-key": "lm-globe" });
  globe.append(
    createSVGEl("circle", { cx, cy, r: fixed(view.radius + GLOW), fill: `url(#${glowId})` }),
    createSVGEl("circle", { cx, cy, r: radius, fill: `url(#${shadeId})` }),
    createSVGEl("path", { d: grat.minor, class: "a-lm-grid" }),
    createSVGEl("path", { d: grat.major, class: "a-lm-grid is-major" }),
    createSVGEl("path", { d: dots.far, class: "a-lm-land is-far" }),
    createSVGEl("path", { d: dots.mid, class: "a-lm-land is-mid" }),
    createSVGEl("path", { d: dots.near, class: "a-lm-land" }),
    createSVGEl("circle", { cx, cy, r: radius, class: "a-lm-limb" }),
    createSVGEl("rect", { x: 0, y: view.height - VEIL, width: view.width, height: VEIL, fill: `url(#${veilId})` }),
  );
  return { defs, globe, arcGlowId };
}

function landing(view, vector) {
  const p = project(view, vector);
  if (p.z >= 0.05) return { x: p.x, y: p.y, on: true };
  const dx = p.x - view.cx, dy = p.y - view.cy;
  const reach = view.radius * 0.985 / Math.max(1, Math.hypot(dx, dy));
  return { x: view.cx + dx * reach, y: view.cy + dy * reach, on: false };
}

function beside(anchor, item, view, way) {
  if (item.kind === "ext") return { x: anchor.x < view.width / 2 ? view.width - FLOAT_X : FLOAT_X, y: FLOAT_Y, on: false };
  const step = clamp(view.width * 0.24, 84, 220);
  const next = anchor.x + way * step;
  const side = next > view.width - 64 || next < 64 ? -way : way;
  return { x: anchor.x + side * step, y: anchor.y, on: false };
}

// Preserves every city's true geographic coordinate on the 3D globe; applies only
// a minimal radial micro-offset when distinct agents share the exact same city (< 11px).
function nudgeCoLocated(spots, view, { clampToFrame = true } = {}) {
  const placed = spots.map(spot => ({
    x: clampToFrame ? clamp(spot.x, 22, view.width - 22) : spot.x,
    y: clampToFrame ? clamp(spot.y, 20, view.height - 20) : spot.y,
    on: spot.on,
  }));
  placed.forEach((spot, index) => {
    let ring = 0;
    while (ring < 10) {
      const clash = placed.slice(0, index).some(other => other.on === spot.on && Math.hypot(other.x - spot.x, other.y - spot.y) < 11);
      if (!clash) break;
      const angle = (ring * Math.PI) / 4 + Math.PI / 6;
      const step = ring < 4 ? 10.5 : 15;
      const nx = spot.x + Math.cos(angle) * step;
      const ny = spot.y + Math.sin(angle) * step;
      spot.x = clampToFrame ? clamp(nx, 22, view.width - 22) : nx;
      spot.y = clampToFrame ? clamp(ny, 20, view.height - 20) : ny;
      ring++;
    }
  });
  return placed;
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
  return nudgeCoLocated(spots, view);
}

function arcPoints(view, a, b, lift, sway = 0) {
  const omega = Math.acos(clamp(dot3(a, b), -1, 1));
  const sinOmega = Math.sin(omega);
  const side = Math.abs(sway) > 1e-5 ? unit3(cross3(a, b)) : null;
  const points = new Array(ARC_STEPS + 1);
  for (let index = 0; index <= ARC_STEPS; index++) {
    const t = index / ARC_STEPS;
    let point = sinOmega < 1e-6
      ? a
      : mix3(a, Math.sin((1 - t) * omega) / sinOmega, b, Math.sin(t * omega) / sinOmega);
    if (side) {
      const bent = unit3(mix3(point, 1, side, sway * Math.sin(Math.PI * t)));
      if (bent) point = bent;
    }
    const env = lift > 0 ? Math.pow(Math.sin(Math.PI * t), 0.86) : 0;
    points[index] = project(view, point, 1 + lift * env);
  }
  return points;
}

function isPointVisible(view, p, { strictLimb = false } = {}) {
  const dist = Math.hypot(p.x - view.cx, p.y - view.cy);
  if (strictLimb) {
    return p.z >= 0.01 && dist <= view.radius - 0.5;
  }
  if (p.z >= 0.01) return true;
  return dist >= view.radius && dist <= view.radius * 1.18 && p.z > -0.32;
}

function clampToLimb(view, a, b) {
  const r = view.radius - 0.5;
  const da = Math.hypot(a.x - view.cx, a.y - view.cy);
  const db = Math.hypot(b.x - view.cx, b.y - view.cy);
  if (Math.abs(db - da) > 1e-4 && ((da <= r && db > r) || (da > r && db <= r))) {
    const t = clamp((r - da) / (db - da), 0, 1);
    return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t, z: a.z + (b.z - a.z) * t };
  }
  const dx = b.x - view.cx, dy = b.y - view.cy;
  const len = Math.max(1, Math.hypot(dx, dy));
  return { x: view.cx + (dx / len) * r, y: view.cy + (dy / len) * r, z: 0.01 };
}

function buildVisiblePath(view, points, { strictLimb = false } = {}) {
  let d = "", pen = false;
  const visible = [];
  const limbExits = [];
  const inFrame = (p) => p.x >= 4 && p.x <= view.width - 4 && p.y >= 6 && p.y <= view.height - 6;
  for (let i = 0; i < points.length; i++) {
    const p = points[i];
    const seen = isPointVisible(view, p, { strictLimb }) && inFrame(p);
    if (seen) {
      if (!pen && i > 0 && strictLimb) {
        const edge = clampToLimb(view, points[i - 1], p);
        if (inFrame(edge)) {
          d += `M${fixed(edge.x)} ${fixed(edge.y)}`;
          visible.push(edge);
          limbExits.push(edge);
          pen = true;
        }
      }
      d += `${pen ? "L" : "M"}${fixed(p.x)} ${fixed(p.y)}`;
      visible.push(p);
    } else if (pen && i > 0) {
      if (strictLimb) {
        const edge = clampToLimb(view, points[i - 1], p);
        if (inFrame(edge)) {
          d += `L${fixed(edge.x)} ${fixed(edge.y)}`;
          visible.push(edge);
          limbExits.push(edge);
        }
      } else {
        limbExits.push(points[i - 1]);
      }
    }
    pen = seen;
  }
  return { d, visible, limbExits };
}

function boundsOf(points) {
  if (!points.length) return { x: 0, y: 0, width: 1, height: 1 };
  const xs = points.map(p => p.x), ys = points.map(p => p.y);
  const left = Math.min(...xs) - 10, top = Math.min(...ys) - 10;
  return { x: fixed(left), y: fixed(top), width: fixed(Math.max(...xs) + 10 - left), height: fixed(Math.max(...ys) + 10 - top) };
}

function sampleCurveGeometry(points, fromPt, toPt) {
  if (!points || points.length < 2) return { samples: NO_VALUES, mid: null, early: null };
  const n = points.length;
  const chordMidX = (fromPt.x + toPt.x) / 2;
  const chordMidY = (fromPt.y + toPt.y) / 2;
  const pointInfoAt = (frac) => {
    const idx = clamp(Math.round((n - 1) * frac), 1, n - 2);
    const p = points[idx];
    const prev = points[Math.max(0, idx - 2)];
    const next = points[Math.min(n - 1, idx + 2)];
    const dx = next.x - prev.x, dy = next.y - prev.y;
    const len = Math.hypot(dx, dy) || 1;
    const ux = dx / len, uy = dy / len;
    let nx = -uy, ny = ux;
    const outX = p.x - chordMidX, outY = p.y - chordMidY;
    if (nx * outX + ny * outY < 0) { nx = -nx; ny = -ny; }
    return { x: p.x, y: p.y, ux, uy, nx, ny };
  };
  const samples = [];
  const step = Math.max(1, Math.floor((n - 1) / 14));
  for (let i = 1; i < n - 1; i += step) {
    const p = points[i];
    if (Math.hypot(p.x - fromPt.x, p.y - fromPt.y) < 13) continue;
    if (Math.hypot(p.x - toPt.x, p.y - toPt.y) < 13) continue;
    samples.push({ x: p.x, y: p.y });
  }
  return {
    samples,
    mid: n >= 3 ? pointInfoAt(0.5) : null,
    early: n >= 5 ? pointInfoAt(0.32) : null,
  };
}

// Single bounded great-circle arch with proportional short-hop rise and true 3D lateral sway.
function archShape(view, a, b, { altitudeScale = 1, strictLimb = false, sway = 0 } = {}) {
  const mid = unit3(mix3(a, 1, b, 1));
  if (!mid || dot3(a, b) > 0.999999) return null;
  const omega = Math.acos(clamp(dot3(a, b), -1, 1));
  const from = project(view, a), to = project(view, b), ground = project(view, mid);
  const span = Math.hypot(to.x - from.x, to.y - from.y);
  const minRise = Math.min(12 * altitudeScale, Math.max(3.5, span * 0.28 * altitudeScale));
  const baseRise = clamp(span * 0.23 * altitudeScale, minRise, 70);
  const rise = Math.max(Math.min(8, span * 0.24 * altitudeScale), Math.min(baseRise, Math.max(12, ground.y - 18)));
  const upness = Math.sqrt(Math.max(0, 1 - dot3(mid, view.front) ** 2));
  const rawMaxLift = omega > 1.7 ? 0.24 : omega > 1.2 ? 0.34 : 0.46;
  const maxLift = strictLimb ? Math.min(rawMaxLift, 0.14) : rawMaxLift;
  let lift = clamp(rise / (view.radius * Math.max(upness, 0.36)), 0.014, maxLift);

  let outer = arcPoints(view, a, b, lift, sway);
  for (let attempt = 0; attempt < 4; attempt++) {
    const minY = Math.min(...outer.filter(p => p.z > -0.25).map(p => p.y));
    if (!Number.isFinite(minY) || minY >= 12) break;
    lift *= 0.72;
    outer = arcPoints(view, a, b, lift, sway);
  }

  const built = buildVisiblePath(view, outer, { strictLimb });
  if (built.visible.length < 2) return null;
  const revBuilt = buildVisiblePath(view, [...outer].reverse(), { strictLimb });
  const trkBuilt = buildVisiblePath(view, arcPoints(view, a, b, 0, sway), { strictLimb: true });
  const midIdx = Math.floor(built.visible.length / 2);
  const apex = built.visible[midIdx];
  const geom = sampleCurveGeometry(built.visible, from, to);
  return {
    span,
    path: built.d,
    forwardWake: revBuilt.d,
    reverseWake: built.d,
    track: trkBuilt.d,
    hit: built.d,
    apex,
    samples: geom.samples,
    mid: geom.mid,
    early: geom.early,
    limbExits: built.limbExits,
    box: boundsOf(built.visible),
  };
}

// Graceful curve when an endpoint is an external public IP / off-globe target.
function lensShape(from, to) {
  const dx = to.x - from.x, dy = to.y - from.y;
  const length = Math.max(1, Math.hypot(dx, dy));
  const bow = clamp(length * 0.14, 10, 30);
  const flip = dx < 0 || (dx === 0 && dy > 0) ? -1 : 1;
  const nx = flip * dy / length * bow, ny = -flip * dx / length * bow;
  const mx = (from.x + to.x) / 2, my = (from.y + to.y) / 2;
  const ctrlX = mx + 2 * nx, ctrlY = my + 2 * ny;
  const start = `${fixed(from.x)} ${fixed(from.y)}`, end = `${fixed(to.x)} ${fixed(to.y)}`;
  const ctrl = `${fixed(ctrlX)} ${fixed(ctrlY)}`;
  const path = `M${start}Q${ctrl} ${end}`;
  const apex = { x: mx + nx, y: my + ny };
  const curvePts = [];
  for (let i = 0; i <= 16; i++) {
    const t = i / 16, mt = 1 - t;
    curvePts.push({
      x: mt * mt * from.x + 2 * mt * t * ctrlX + t * t * to.x,
      y: mt * mt * from.y + 2 * mt * t * ctrlY + t * t * to.y,
    });
  }
  const geom = sampleCurveGeometry(curvePts, from, to);
  return {
    span: length,
    path,
    forwardWake: `M${end}Q${ctrl} ${start}`,
    reverseWake: path,
    track: "",
    hit: path,
    apex,
    samples: geom.samples,
    mid: geom.mid,
    early: geom.early,
    limbExits: NO_VALUES,
    box: boundsOf([from, to, apex]),
  };
}

function railTone(dir) {
  const tone = dir?.status?.tone || "";
  if (tone === "bad" || tone === "warn") return tone;
  return tone === "ok" ? "" : "idle";
}

function railMark(apex, tone, { compact = false } = {}) {
  if (!apex || (tone !== "bad" && tone !== "warn")) return NO_VALUES;
  const x = apex.x, y = apex.y;
  const r = compact ? 5.0 : 6.0;
  const d = compact ? 2.6 : 3.1;
  const cross = `M${fixed(x - d)} ${fixed(y - d)}L${fixed(x + d)} ${fixed(y + d)}M${fixed(x - d)} ${fixed(y + d)}L${fixed(x + d)} ${fixed(y - d)}`;
  const wedge = `M${fixed(x)} ${fixed(y - d - 0.8)}L${fixed(x + d + 0.8)} ${fixed(y + d)}L${fixed(x - d - 0.8)} ${fixed(y + d)}Z`;
  return [
    createSVGEl("circle", { cx: fixed(x), cy: fixed(y), r, class: "a-lm-bed" }),
    createSVGEl("path", { d: tone === "bad" ? cross : wedge, class: `a-lm-mark ${tone}` }),
  ];
}

function hopDirectionArrow(shape, lineColor, { hasMark = false } = {}) {
  if (!shape || shape.span < 34) return NO_VALUES;
  const pt = hasMark ? shape.early : shape.mid;
  if (!pt) return NO_VALUES;
  const { x, y, ux, uy } = pt;
  const px = -uy, py = ux;
  const tipX = x + ux * 3.1, tipY = y + uy * 3.1;
  const tailX = x - ux * 2.5, tailY = y - uy * 2.5;
  const wing = 2.65;
  const lX = tailX + px * wing, lY = tailY + py * wing;
  const rX = tailX - px * wing, rY = tailY - py * wing;
  const d = `M${fixed(lX)} ${fixed(lY)}L${fixed(tipX)} ${fixed(tipY)}L${fixed(rX)} ${fixed(rY)}`;
  return [
    createSVGEl("path", { d, class: "a-lm-dir-halo", "aria-hidden": "true" }),
    createSVGEl("path", { d, class: `a-lm-dir-arrow ${lineColor}`.trim(), "aria-hidden": "true" }),
  ];
}

function hopPillElement(apex, text, view, colorClass, hasMark, {
  alwaysVisible = false,
  avoidBoxes = null,
  arcBoxes = null,
  recordBox = false,
  compact = false,
  normal = null,
  maxAlwaysVisibleScore = 150,
} = {}) {
  if (!apex || !text) return null;
  const unitPx = compact ? 5.2 : 5.8;
  const w = Math.max(compact ? 38 : 46, Math.round(visualUnits(text) * unitPx + (compact ? 11 : 14)));
  const h = compact ? 16 : 19;
  const maxX = Math.max(6, view.width - w - 6);
  const maxY = Math.max(6, view.height - h - 6);
  const centerX = clamp(apex.x - w / 2, 6, maxX);
  const aboveY = hasMark ? apex.y - h - 10 : apex.y - h - 7;
  const belowY = hasMark ? apex.y + 12 : apex.y + 8;

  const candidates = [];
  if (normal) {
    const padDist = hasMark ? 20 : 15;
    for (const dist of [padDist, padDist + 8]) {
      candidates.push({
        x: apex.x + normal.nx * dist - w / 2,
        y: apex.y + normal.ny * dist - h / 2,
      });
    }
    candidates.push(
      { x: apex.x - normal.nx * padDist - w / 2, y: apex.y - normal.ny * padDist - h / 2 },
    );
  }
  candidates.push(
    { x: centerX, y: aboveY },
    { x: centerX, y: belowY },
    { x: centerX, y: aboveY - 16 },
    { x: centerX, y: belowY + 16 },
    { x: apex.x + 12, y: apex.y - h / 2 },
    { x: apex.x - w - 12, y: apex.y - h / 2 },
    { x: apex.x + 12, y: belowY },
    { x: apex.x - w - 12, y: belowY },
  );

  let best = null;
  for (let idx = 0; idx < candidates.length; idx++) {
    const raw = candidates[idx];
    const px = clamp(raw.x, 6, maxX);
    const py = clamp(raw.y, 6, maxY);
    const box = { x1: px - 2, y1: py - 2, x2: px + w + 2, y2: py + h + 2 };
    let score = idx * 5;
    if (Math.abs(px - raw.x) > 4 || Math.abs(py - raw.y) > 4) score += 160;
    if (avoidBoxes?.length) {
      for (const other of avoidBoxes) score += overlapArea(box, other) * 32;
    }
    if (arcBoxes?.length) {
      for (const seg of arcBoxes) score += overlapArea(box, seg) * 18;
    }
    if (!best || score < best.score) best = { px, py, box, score };
    if (score === idx * 5) break;
  }
  if (!best) return null;
  const shown = alwaysVisible && best.score < maxAlwaysVisibleScore;
  if (recordBox && shown && avoidBoxes) avoidBoxes.push(best.box);
  const cls = `a-lm-hop-pill ${colorClass}${compact ? " is-chain-pill" : ""}${shown ? " is-shown" : ""}`.replace(/\s+/g, " ").trim();
  const g = createSVGEl("g", { class: cls, "aria-hidden": "true" });
  g.append(
    createSVGEl("rect", { x: fixed(best.px), y: fixed(best.py), width: w, height: h, rx: compact ? 8 : 9.5, class: "a-lm-hop-pill-bg" }),
    createSVGEl("text", { x: fixed(best.px + w / 2), y: fixed(best.py + (compact ? 11.2 : 13)), "text-anchor": "middle", class: "a-lm-hop-pill-text" }, text),
  );
  return g;
}

function isDirReachable(dir) {
  return Boolean(dir?.available && dir.reached && isNumber(dir.avgMS) && (dir.lossPercent ?? 0) < 100);
}

function motionPace(dir, group, hop) {
  const probing = Boolean(group.running && hop.ready);
  if (probing || dir.avgMS <= 60) return "is-quick";
  if (dir.avgMS <= 160) return "is-steady";
  return "is-slow";
}

// Architectural hollow twin-wall conduit across every health & hierarchy state:
// overpass clearance halo + outer conduit wall + hollow optical channel + caution spine.
function renderConduitLayers({
  shape,
  lineColor = "is-tone-accent",
  isL1 = true,
  isBroken = false,
  isAlertHop = false,
  tierClass = "is-l1",
  isChainHop = false,
}) {
  if (!shape?.path) return NO_VALUES;
  const out = [];
  const tier = isL1 ? "is-l1" : tierClass;
  const shortCls = !isL1 && shape.span < 48 ? " is-short" : "";
  const chainCls = isL1 && isChainHop ? " is-chain-hop" : "";
  const baseRailCls = isL1
    ? `a-lm-rail ${lineColor}${isBroken ? " is-broken" : ""}${chainCls}`.trim()
    : `a-lm-fleet-rail ${tierClass} ${lineColor}${isBroken ? " is-broken" : ""}${isAlertHop ? " is-alert-hop" : ""}`.trim();

  if (isL1 && shape.track) {
    out.push(createSVGEl("path", { d: shape.track, class: "a-lm-track" }));
  }
  if (isL1) {
    out.push(createSVGEl("path", { d: shape.path, class: `a-lm-halo${chainCls}` }));
  } else {
    out.push(createSVGEl("path", { d: shape.path, class: `a-lm-conduit-halo ${tier}${shortCls}`.trim() }));
  }
  out.push(
    createSVGEl("path", { d: shape.path, class: `${baseRailCls} is-conduit-outer${shortCls}` }),
    createSVGEl("path", { d: shape.path, class: `a-lm-line-cut ${tier} ${lineColor}${isBroken ? " is-broken" : ""}${shortCls}${chainCls}`.trim() }),
  );
  if (isL1 && lineColor === "is-tone-warn" && !isBroken) {
    out.push(createSVGEl("path", { d: shape.path, class: "a-lm-conduit-warn-dash" }));
  }
  return out;
}

// Dual-layer optical laser sweep traveling inside the twin-wall conduit channel:
// soft gaussian aura + focused optical beam + bidirectional return echo + crimson fault pulse.
function renderLaserSweepLayers({
  shape,
  group,
  hop,
  showFwd = true,
  showRev = false,
  forwardTone = "",
  reverseTone = "",
  lineColor = "is-tone-accent",
  isL1 = true,
  isBroken = false,
  staggerSlot = 0,
  arcGlowId = "",
}) {
  if (!shape?.path || lineColor === "is-tone-idle") return NO_VALUES;
  const out = [];
  const glowAttr = arcGlowId ? { filter: `url(#${arcGlowId})` } : {};
  const modeClass = isL1 ? "is-l1" : "is-ambient";
  const slot = ((staggerSlot % STAGGER_SLOTS) + STAGGER_SLOTS) % STAGGER_SLOTS;
  const staggerClass = slot > 0 ? `is-stagger-${slot}` : "";

  let emitted = false;
  const emitSweep = (wakeD, rec, tone, slotOffset, isRev = false) => {
    if (!wakeD || !isDirReachable(rec)) return;
    emitted = true;
    const pace = motionPace(rec, group, hop);
    const s = (((staggerSlot + slotOffset) % STAGGER_SLOTS) + STAGGER_SLOTS) % STAGGER_SLOTS;
    const stg = s > 0 ? `is-stagger-${s}` : "";
    const revCls = isRev ? "is-rev" : "";
    const strokeTone = lineColor || tone;
    out.push(
      createSVGEl("path", {
        d: wakeD,
        pathLength: 100,
        class: `a-lm-wave-glow ${pace} ${modeClass} ${stg} ${strokeTone} ${revCls}`.replace(/\s+/g, " ").trim(),
        ...glowAttr,
      }),
      createSVGEl("path", {
        d: wakeD,
        pathLength: 100,
        class: `a-lm-wave-band ${pace} ${modeClass} ${stg} ${strokeTone} ${revCls}`.replace(/\s+/g, " ").trim(),
      }),
    );
  };

  if (showFwd) emitSweep(shape.forwardWake, hop.forward, forwardTone, 0, false);
  if (showRev && isL1) emitSweep(shape.reverseWake, hop.reverse, reverseTone, 3, true);
  if (!emitted && (isBroken || lineColor === "is-tone-bad")) {
    out.push(createSVGEl("path", {
      d: shape.path,
      pathLength: 100,
      class: `a-lm-wave-fault ${modeClass} ${staggerClass}`.replace(/\s+/g, " ").trim(),
      ...glowAttr,
    }));
  }
  return out;
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
  const {
    view,
    group,
    spots,
    vectors,
    hopShapes,
    isMultiHop = false,
    focus,
    metricDir = "both",
    arcGlowId = "",
  } = scene;
  const from = spots[index], to = spots[index + 1];
  const showFwd = metricDir !== "reverse";
  const showRev = hop.bidirectional && metricDir !== "forward";
  const shape = hopShapes?.[index]
    || (from.on && to.on ? archShape(view, vectors[index], vectors[index + 1], { altitudeScale: isMultiHop ? 0.7 : 1 }) : null)
    || lensShape(from, to);
  const flags = { running: group.running, ready: hop.ready };
  const key = `hop:${index}`;
  const outbound = formatDirBadgeMetric(hop.forward, flags);
  const inbound = hop.bidirectional ? formatDirBadgeMetric(hop.reverse, flags) : "";
  let label = `${hop.from.name} 至 ${hop.to.name}，${outbound}`;
  if (hop.bidirectional) label = `${hop.from.name} 与 ${hop.to.name}，去程 ${outbound}，回程 ${inbound}`;
  const isFocusedHop = focus.key === key;
  const compactChainPill = isMultiHop && group.hops.length <= 6 && !isFocusedHop;
  const pillText = metricDir === "reverse" && hop.bidirectional
    ? `← ${inbound}`
    : compactChainPill
      ? outbound
      : hop.bidirectional && metricDir === "both" && outbound !== inbound
        ? `${outbound} / ${inbound}`
        : outbound;

  const element = createSVGEl("g", {
    class: `${focus.dimHop(index) ? "a-lm-hop is-dim" : "a-lm-hop"}${isMultiHop ? " is-chain-hop" : ""}`,
    tabindex: 0,
    role: "button",
    "aria-pressed": String(isFocusedHop),
    "aria-label": label,
    "data-lm-key": key,
    "data-refresh-key": `lm-hop-${index}`,
  });
  const forwardTone = railTone(hop.forward), reverseTone = railTone(hop.reverse);
  const fwdMark = showFwd ? markToneForDir(hop.forward) : "";
  const revMark = showRev ? markToneForDir(hop.reverse) : "";
  const combinedMark = (fwdMark === "bad" || revMark === "bad") ? "bad" : (fwdMark || revMark);
  const isIdle = !combinedMark && (!showFwd || !isDirReachable(hop.forward)) && (!showRev || !isDirReachable(hop.reverse));
  const lineColor = resolveConduitTone({ combinedMark, isL1: true, pinnedSlot: null, isIdle });

  element.append(titleElement(label), createSVGEl("rect", { ...shape.box, rx: 6, class: "a-lm-focus" }));
  element.append(...renderConduitLayers({
    shape,
    lineColor,
    isL1: true,
    isBroken: combinedMark === "bad",
    isAlertHop: Boolean(combinedMark),
    tierClass: "is-l1",
    isChainHop: isMultiHop,
  }));
  element.append(...renderLaserSweepLayers({
    shape,
    group,
    hop,
    showFwd,
    showRev,
    forwardTone,
    reverseTone,
    lineColor,
    isL1: true,
    isBroken: combinedMark === "bad",
    staggerSlot: isMultiHop ? ((STAGGER_SLOTS - (index % STAGGER_SLOTS)) % STAGGER_SLOTS) : index,
    arcGlowId,
  }));
  if (isMultiHop) {
    element.append(...hopDirectionArrow(shape, lineColor, { hasMark: Boolean(combinedMark) }));
  }
  if (combinedMark) element.append(...railMark(shape.apex, combinedMark, { compact: shape.span < 44 }));
  const showRestingPill = isMultiHop && group.hops.length <= 6 && shape.span >= 36;
  const pill = hopPillElement(shape.apex, pillText, view, lineColor, Boolean(combinedMark), {
    alwaysVisible: showRestingPill || isFocusedHop,
    avoidBoxes: scene.pillAvoidBoxes,
    arcBoxes: scene.arcObstacles,
    recordBox: showRestingPill,
    compact: compactChainPill,
    normal: shape.mid,
    maxAlwaysVisibleScore: isFocusedHop ? 9999 : 150,
  });
  if (pill) element.append(pill);
  element.append(createSVGEl("path", { d: shape.hit, class: "a-lm-hit is-line" }));
  return element;
}

function pipElement(item, x, y, size = PIP, chainRole = "") {
  const roleCls = chainRole ? ` is-chain-${chainRole}` : "";
  if (item.kind === "ext") {
    const r = size * 0.56;
    const d = `M${x} ${fixed(y - r)}L${fixed(x + r)} ${y}L${x} ${fixed(y + r)}L${fixed(x - r)} ${y}Z`;
    const g = createSVGEl("g", { class: `a-lm-beacon is-ext${roleCls}` });
    g.append(
      createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.72), class: "a-lm-pip-halo is-ext" }),
      createSVGEl("path", { d, class: "a-lm-pip is-ext" }),
      createSVGEl("circle", { cx: x, cy: y, r: 1.25, class: "a-lm-pip-core is-ext" }),
    );
    return g;
  }
  // Treat offline stations calmly as `is-off` (even when `issue` notes "节点离线")
  // so healthy historical green links are never overpowered by jarring red targets.
  let state = "";
  if (!item.online) state = " is-off";
  else if (item.issue) state = " is-bad";
  const g = createSVGEl("g", { class: `a-lm-beacon${state}${roleCls}` });
  if (chainRole === "start") {
    g.append(
      createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.78), class: `a-lm-pip-halo${state}` }),
      createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.48), class: `a-lm-pip is-round is-chain-start${state}` }),
      createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.24), class: `a-lm-pip-core is-chain-start${state}` }),
    );
    return g;
  }
  if (chainRole === "end") {
    g.append(
      createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.8), class: `a-lm-pip-halo${state}` }),
      createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.52), class: `a-lm-pip is-round is-chain-end${state}` }),
      createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.23), class: `a-lm-pip-core is-chain-end${state}` }),
    );
    return g;
  }
  const scale = chainRole === "relay" ? 0.86 : 1;
  g.append(
    createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.64 * scale), class: `a-lm-pip-halo${state}` }),
    createSVGEl("circle", { cx: x, cy: y, r: fixed(size * 0.42 * scale), class: `a-lm-pip is-round${state}${roleCls}` }),
    createSVGEl("circle", { cx: x, cy: y, r: state === " is-off" ? 1.1 : (chainRole === "relay" ? 1.2 : 1.4), class: `a-lm-pip-core${state}` }),
  );
  return g;
}

// Appends an optional multi-hop step chip, crisp country/region flag and haloed typography cleanly floating over the light sphere.
function appendGlobeFlagAndName(parent, {
  flag,
  text,
  x,
  y,
  flagW,
  flagH,
  flagGap,
  textClass,
  isExt = false,
  stepNum = 0,
  chainRole = "",
}) {
  let textX = x;
  if (stepNum > 0) {
    const isEndpoint = chainRole === "start" || chainRole === "end";
    const stepCls = isEndpoint ? "is-endpoint" : "is-relay";
    const cx = textX + STEP_W / 2;
    parent.append(
      createSVGEl("circle", {
        cx: fixed(cx),
        cy: fixed(y),
        r: fixed(STEP_W / 2 + 1),
        class: "a-lm-bed",
      }),
      createSVGEl("circle", {
        cx: fixed(cx),
        cy: fixed(y),
        r: fixed(STEP_W / 2),
        class: `a-lm-step-bg ${stepCls}`,
      }),
      createSVGEl("text", {
        x: fixed(cx),
        y: fixed(y + 3.2),
        "text-anchor": "middle",
        class: `a-lm-step-num ${stepCls}`,
      }, String(stepNum)),
    );
    textX += STEP_W + STEP_GAP;
  }
  if (flag?.flagCode) {
    const fy = y - flagH / 2;
    parent.append(
      createSVGEl("rect", {
        x: fixed(textX - 1),
        y: fixed(fy - 1),
        width: fixed(flagW + 2),
        height: fixed(flagH + 2),
        rx: 2,
        class: "a-lm-bed",
      }),
      createSVGEl("rect", {
        x: fixed(textX),
        y: fixed(fy),
        width: fixed(flagW),
        height: fixed(flagH),
        rx: 1.5,
        class: "a-lm-flag-bg",
      }),
      createSVGEl("image", {
        href: `/api/v1/region-flags/${flag.flagCode.toLowerCase()}`,
        x: fixed(textX),
        y: fixed(fy),
        width: fixed(flagW),
        height: fixed(flagH),
        preserveAspectRatio: "xMidYMid slice",
        class: "a-lm-flag",
      }),
      createSVGEl("rect", {
        x: fixed(textX),
        y: fixed(fy),
        width: fixed(flagW),
        height: fixed(flagH),
        rx: 1.5,
        class: "a-lm-flag-frame",
      }),
    );
    textX += flagW + flagGap;
  }
  parent.append(createSVGEl("text", {
    x: fixed(textX),
    y: fixed(y + 3.8),
    "text-anchor": "start",
    class: isExt ? `${textClass} is-ext` : textClass,
  }, text));
}

function overlapArea(a, b) {
  const ix = Math.max(0, Math.min(a.x2, b.x2) - Math.max(a.x1, b.x1));
  if (ix <= 0) return 0;
  const iy = Math.max(0, Math.min(a.y2, b.y2) - Math.max(a.y1, b.y1));
  return ix * iy;
}

// Computes an outward escape unit vector away from close neighboring pins (< radius px)
// so dense clusters fan their labels outward without colliding with pins or arcs.
function computeClusterOutwardVec(index, spots, radius = 56) {
  const spot = spots[index];
  if (!spot) return { awayVec: null, nearestDist: Infinity, crowded: false };
  let sumX = 0, sumY = 0, count = 0, nearestDist = Infinity;
  for (let j = 0; j < spots.length; j++) {
    if (j === index || !spots[j]) continue;
    const dx = spot.x - spots[j].x, dy = spot.y - spots[j].y;
    const d = Math.hypot(dx, dy);
    if (d < nearestDist) nearestDist = d;
    if (d < radius) {
      const weight = 1 / Math.max(8, d);
      sumX += (d > 1e-3 ? dx / d : Math.cos(index * 2.1)) * weight;
      sumY += (d > 1e-3 ? dy / d : Math.sin(index * 2.1)) * weight;
      count++;
    }
  }
  if (!count) return { awayVec: null, nearestDist, crowded: false };
  const len = Math.hypot(sumX, sumY);
  const awayVec = len > 1e-4
    ? { x: sumX / len, y: sumY / len }
    : { x: Math.cos((index * Math.PI * 2) / Math.max(2, count + 1)), y: Math.sin((index * Math.PI * 2) / Math.max(2, count + 1)) };
  return { awayVec, nearestDist, crowded: true };
}

function computeChainOutwardVec(index, spots, hopShapes) {
  const spot = spots[index];
  if (!spot) return null;
  const neighbors = [];
  if (index > 0 && spots[index - 1]) neighbors.push(spots[index - 1]);
  if (index < spots.length - 1 && spots[index + 1]) neighbors.push(spots[index + 1]);
  const prevMid = index > 0 ? hopShapes?.[index - 1]?.mid : null;
  const nextMid = index < spots.length - 1 ? hopShapes?.[index]?.mid : null;
  if (prevMid) neighbors.push(prevMid);
  if (nextMid) neighbors.push(nextMid);
  let sx = 0, sy = 0;
  for (const nb of neighbors) {
    const dx = spot.x - nb.x, dy = spot.y - nb.y;
    const d = Math.hypot(dx, dy);
    if (d > 1e-3) {
      const w = 1 / Math.max(18, d);
      sx += (dx / d) * w;
      sy += (dy / d) * w;
    }
  }
  const len = Math.hypot(sx, sy);
  return len > 1e-4 ? { x: sx / len, y: sy / len } : null;
}

function solveLabelPlacement({
  spot,
  totalW,
  labelH,
  view,
  preferSide,
  occupied,
  arcObstacles = null,
  skipObstacleIndex = -1,
  pipCount = 0,
  awayVec = null,
  isCrowded = false,
}) {
  const padX = 4;
  const padTop = 8;
  const padBot = 6;
  const halfH = labelH / 2;
  const rightX = spot.x + 11;
  const leftX = spot.x - 11 - totalW;
  const centerX = clamp(spot.x - totalW / 2, padX, Math.max(padX, view.width - totalW - padX));

  const sides = [];
  if (isCrowded && awayVec) {
    const baseAngle = Math.atan2(awayVec.y, awayVec.x);
    for (const dist of [18, 28, 42]) {
      for (const delta of [0, 0.42, -0.42, 0.85, -0.85]) {
        const ang = baseAngle + delta;
        const ux = Math.cos(ang), uy = Math.sin(ang);
        const cx = spot.x + ux * dist;
        const cy = spot.y + uy * dist;
        const x = ux >= 0.25 ? cx : (ux <= -0.25 ? cx - totalW : cx - totalW / 2);
        sides.push({ x, cy });
      }
    }
  }

  const baseSides = preferSide < 0
    ? [
        { x: leftX, cy: spot.y },
        { x: rightX, cy: spot.y },
        { x: centerX, cy: spot.y + 19 },
        { x: centerX, cy: spot.y - 19 },
        { x: leftX, cy: spot.y - 16 },
        { x: leftX, cy: spot.y + 16 },
        { x: rightX, cy: spot.y - 16 },
        { x: rightX, cy: spot.y + 16 },
        { x: centerX, cy: spot.y - 34 },
        { x: centerX, cy: spot.y + 34 },
        { x: leftX - 12, cy: spot.y - 28 },
        { x: rightX + 12, cy: spot.y + 28 },
        { x: leftX - 18, cy: spot.y + 28 },
        { x: rightX + 18, cy: spot.y - 28 },
      ]
    : preferSide > 0
      ? [
          { x: rightX, cy: spot.y },
          { x: leftX, cy: spot.y },
          { x: centerX, cy: spot.y + 19 },
          { x: centerX, cy: spot.y - 19 },
          { x: rightX, cy: spot.y - 16 },
          { x: rightX, cy: spot.y + 16 },
          { x: leftX, cy: spot.y - 16 },
          { x: leftX, cy: spot.y + 16 },
          { x: centerX, cy: spot.y - 34 },
          { x: centerX, cy: spot.y + 34 },
          { x: rightX + 12, cy: spot.y - 28 },
          { x: leftX - 12, cy: spot.y + 28 },
          { x: rightX + 18, cy: spot.y + 28 },
          { x: leftX - 18, cy: spot.y - 28 },
        ]
      : [
          { x: centerX, cy: spot.y + 19 },
          { x: centerX, cy: spot.y - 19 },
          { x: rightX, cy: spot.y },
          { x: leftX, cy: spot.y },
          { x: rightX, cy: spot.y - 16 },
          { x: leftX, cy: spot.y - 16 },
          { x: rightX, cy: spot.y + 16 },
          { x: leftX, cy: spot.y + 16 },
          { x: centerX, cy: spot.y - 34 },
          { x: centerX, cy: spot.y + 34 },
        ];
  sides.push(...baseSides);

  let best = null;
  for (let idx = 0; idx < sides.length; idx++) {
    const raw = sides[idx];
    const x = clamp(raw.x, padX, Math.max(padX, view.width - totalW - padX));
    const cy = clamp(raw.cy, padTop + halfH, Math.max(padTop + halfH, view.height - padBot - halfH));
    const box = { x1: x - 2, y1: cy - halfH - 2, x2: x + totalW + 2, y2: cy + halfH + 2 };
    let score = idx * 3;
    if (Math.abs(x - raw.x) > 4 || Math.abs(cy - raw.cy) > 4) score += 280;
    for (let o = 0; o < occupied.length; o++) {
      if (o === skipObstacleIndex) continue;
      const weight = o < pipCount ? 72 : 30;
      score += overlapArea(box, occupied[o]) * weight;
    }
    if (arcObstacles?.length) {
      for (let a = 0; a < arcObstacles.length; a++) {
        score += overlapArea(box, arcObstacles[a]) * 28;
      }
    }
    if (!best || score < best.score) best = { x, cy, box, score };
    if (score === idx * 3) break;
  }
  return best;
}

// Draws a crisp cartographic bent callout pointer with anchor dot when a label fans away (> 13px).
function labelLeaderElement(spot, placement, totalW, labelH) {
  const anchorX = spot.x < placement.x
    ? placement.x - 2
    : spot.x > placement.x + totalW
      ? placement.x + totalW + 2
      : clamp(spot.x, placement.x + 6, placement.x + totalW - 6);
  const anchorY = (spot.x >= placement.x && spot.x <= placement.x + totalW)
    ? (spot.y < placement.cy ? placement.cy - labelH / 2 - 1.5 : placement.cy + labelH / 2 + 1.5)
    : placement.cy;
  const dx = anchorX - spot.x, dy = anchorY - spot.y;
  const dist = Math.hypot(dx, dy);
  if (dist <= 13) return null;
  const ux = dx / dist, uy = dy / dist;
  const sx = spot.x + ux * 5.8, sy = spot.y + uy * 5.8;
  let d = `M${fixed(sx)} ${fixed(sy)}L${fixed(anchorX)} ${fixed(anchorY)}`;
  if (Math.abs(dy) >= 8 && Math.abs(dx) >= 16) {
    const shelf = Math.min(8, Math.abs(dx) * 0.32);
    const elbowX = anchorX + (dx > 0 ? -shelf : shelf);
    d = `M${fixed(sx)} ${fixed(sy)}L${fixed(elbowX)} ${fixed(anchorY)}L${fixed(anchorX)} ${fixed(anchorY)}`;
  }
  const g = createSVGEl("g", { class: "a-lm-leader-wrap", "aria-hidden": "true" });
  g.append(
    createSVGEl("path", { d, class: "a-lm-leader is-callout" }),
    createSVGEl("circle", { cx: fixed(anchorX), cy: fixed(anchorY), r: 1.4, class: "a-lm-leader-dot" }),
  );
  return g;
}

function preferredSide(index, spots) {
  const spot = spots[index];
  const pulls = [spots[index - 1], spots[index + 1]].filter(Boolean).map(other => other.x - spot.x).filter(dx => Math.abs(dx) >= 12);
  const pull = pulls.reduce((sum, dx) => sum + Math.sign(dx), 0);
  if (pull === 0 && pulls.length) return 0;
  return pull > 0 ? -1 : 1;
}

function nodeElement(scene, item, index, occupied) {
  const { view, spots, flags, focus, isMultiHop = false, hopShapes = null, arcObstacles = null } = scene;
  const x = fixed(spots[index].x), y = fixed(spots[index].y);
  const key = `node:${index}`;
  const pressed = focus.key === key;
  const flag = flags[index];
  const chainRole = isMultiHop
    ? (index === 0 ? "start" : (index === spots.length - 1 ? "end" : "relay"))
    : "";
  const stepNum = isMultiHop ? index + 1 : 0;
  let label = isMultiHop ? `第 ${stepNum} 站 · ${item.role} ${item.name}` : `${item.role} ${item.name}`;
  if (item.issue) label += `，${item.issue}`;
  else if (item.kind !== "ext" && !item.online) label += "，离线";
  const tagH = 17;
  const rawDisplay = view.width < 440 ? shortNodeName(item.name) : item.name;
  const name = clipLabel(rawDisplay, view.width < 440 ? 10 : NAME_UNITS);
  const textW = visualUnits(name) * UNIT_PX;
  const stepW = stepNum > 0 ? STEP_W + STEP_GAP : 0;
  const totalW = stepW + (flag ? FLAG_W + FLAG_GAP : 0) + textW;
  const prox = computeClusterOutwardVec(index, spots, 56);
  const chainAway = isMultiHop ? computeChainOutwardVec(index, spots, hopShapes) : null;
  const awayVec = chainAway || prox.awayVec;
  const isCrowded = Boolean(chainAway) || prox.crowded;
  const isTight = prox.nearestDist < 26;
  const prefer = awayVec
    ? (awayVec.x < -0.2 ? -1 : awayVec.x > 0.2 ? 1 : 0)
    : preferredSide(index, spots);
  const placement = solveLabelPlacement({
    spot: spots[index],
    totalW,
    labelH: tagH,
    view,
    preferSide: prefer,
    occupied,
    arcObstacles,
    skipObstacleIndex: index,
    pipCount: spots.length,
    awayVec,
    isCrowded,
  });
  occupied.push(placement.box);

  const roleCls = chainRole ? ` is-chain-${chainRole}` : "";
  const element = createSVGEl("g", {
    class: `a-lm-node${focus.dimNode(index) ? " is-dim" : ""}${isTight ? " is-tight" : ""}${roleCls}`,
    tabindex: 0,
    role: "button",
    "aria-pressed": String(pressed),
    "aria-label": label,
    "data-lm-key": key,
    "data-refresh-key": `lm-node-${index}`,
  });
  element.append(titleElement(label));
  if (((item.kind !== "ext" && item.online && !item.issue && (!isMultiHop || chainRole === "start" || chainRole === "end")) || pressed)) {
    element.append(createSVGEl("circle", { cx: x, cy: y, r: isTight ? 4 : 5, class: pressed ? "a-lm-ping is-focus" : "a-lm-ping", "vector-effect": "non-scaling-stroke" }));
  }
  const leader = labelLeaderElement(spots[index], placement, totalW, tagH);
  if (leader) element.append(leader);
  const pipSize = isTight ? PIP * 0.82 : PIP;
  element.append(
    createSVGEl("circle", { cx: x, cy: y, r: isTight ? 4.0 : (chainRole === "relay" ? 4.2 : 5.0), class: "a-lm-bed" }),
    createSVGEl("circle", { cx: x, cy: y, r: isTight ? 7.0 : 8.4, class: "a-lm-ring" }),
    pipElement(item, x, y, pipSize, chainRole),
  );
  appendGlobeFlagAndName(element, {
    flag,
    text: name,
    x: placement.x,
    y: placement.cy,
    flagW: FLAG_W,
    flagH: FLAG_H,
    flagGap: FLAG_GAP,
    textClass: "a-lm-name",
    isExt: item.kind === "ext",
    stepNum,
    chainRole,
  });
  const hitHalf = isTight ? 10 : 18;
  element.append(createSVGEl("rect", { x: fixed(x - hitHalf), y: fixed(y - hitHalf), width: hitHalf * 2, height: hitHalf * 2, class: "a-lm-hit" }));
  return element;
}

export function renderLinkMap(group, {
  width = 880,
  sizeOpts = undefined,
  focusKey = "",
  camera = null,
  draft = false,
  metricDir = "both",
  idPrefix = "",
} = {}) {
  const size = linkMapSize(width, sizeOpts);
  const view = makeView(camera || linkMapCamera(group, width, sizeOpts), size);
  const located = locateNodes(group.nodes);
  const vectors = located.map(entry => entry.vector);
  const flags = located.map(entry => entry.flag);
  const spots = place(view, vectors, group.nodes);
  const bd = backdrop(view, draft, idPrefix);
  const isMultiHop = (group.hops?.length || 0) >= 2;

  // Pre-compute hop curves first so node labels & latency pills actively avoid crossing any arc.
  const hopShapes = group.hops.map((_, index) => {
    const from = spots[index], to = spots[index + 1];
    return (from.on && to.on ? archShape(view, vectors[index], vectors[index + 1], { altitudeScale: isMultiHop ? 0.7 : 1 }) : null)
      || lensShape(from, to);
  });
  const arcObstacles = [];
  for (const shape of hopShapes) {
    for (const pt of (shape?.samples || NO_VALUES)) {
      arcObstacles.push({ x1: pt.x - 5.5, y1: pt.y - 5.5, x2: pt.x + 5.5, y2: pt.y + 5.5 });
    }
  }

  const scene = {
    view,
    group,
    vectors,
    flags,
    spots,
    hopShapes,
    arcObstacles,
    isMultiHop,
    focus: focusState(focusKey),
    metricDir,
    arcGlowId: bd.arcGlowId,
  };
  const svg = createSVGEl("svg", {
    class: `a-lm is-pannable${isMultiHop ? " is-multihop" : ""}`,
    viewBox: `0 0 ${size.width} ${size.height}`,
    role: "group",
    "aria-label": `${group.groupName} 链路图`,
    "data-refresh-key": "lm-svg",
  });
  const occupied = spots.map(s => ({ x1: s.x - 10, y1: s.y - 10, x2: s.x + 10, y2: s.y + 10 }));
  const nodes = createSVGEl("g", { "data-refresh-key": "lm-nodes" });
  nodes.append(...group.nodes.map((item, index) => nodeElement(scene, item, index, occupied)));
  scene.pillAvoidBoxes = occupied;

  const hops = createSVGEl("g", { "data-refresh-key": "lm-hops" });
  hops.append(...group.hops.map((hop, index) => hopElement(scene, hop, index)));

  svg.append(bd.defs, bd.globe, hops, nodes);
  return svg;
}

export function extractPublicTargets(groups) {
  const map = new Map();
  for (const group of groups || NO_VALUES) {
    const extNode = (group.nodes || NO_VALUES).find(item => item.kind === "ext");
    if (!extNode) continue;
    const key = extNode.address || extNode.name;
    if (!key) continue;
    const tone = group.overall?.statusBadge?.tone || "";
    const metricText = formatDirBadgeMetric(group.overall?.forward, { running: group.running, ready: group.availability?.ready });
    const entry = map.get(key);
    if (!entry) {
      map.set(key, {
        address: extNode.address || key,
        name: extNode.name || key,
        groupName: group.groupName,
        groupNames: [group.groupName],
        tone,
        metricText,
        group,
      });
    } else {
      entry.groupNames.push(group.groupName);
      if (tone === "bad" || (tone === "warn" && entry.tone !== "bad")) {
        entry.tone = tone;
        entry.metricText = metricText;
        entry.groupName = group.groupName;
        entry.group = group;
      }
    }
  }
  return [...map.values()];
}

function toneRank(tone) {
  if (tone === "bad") return 3;
  if (tone === "warn") return 2;
  if (tone === "running") return 1;
  return 0;
}

// Panoramic 3D globe showing all visible groups with hover-invariant corridor layout,
// overpass separation, concentric beacons, optical laser sweep, and regional cluster zoom.
export function renderFleetLinkMap(groups, {
  width = 880,
  sizeOpts = undefined,
  focusGroupName = "",
  hoverGroupName = "",
  pinnedNodeID = "",
  camera = null,
  draft = false,
  metricDir = "both",
  idPrefix = "fleet",
} = {}) {
  const size = linkMapSize(width, sizeOpts);
  const view = makeView(camera || fleetLinkCamera(groups, width, sizeOpts), size);
  const svg = createSVGEl("svg", {
    class: "a-lm is-fleet is-pannable",
    viewBox: `0 0 ${size.width} ${size.height}`,
    role: "group",
    "aria-label": "全球链路延迟 3D 地球全景图",
    "data-refresh-key": "lm-svg",
  });

  const highlightName = hoverGroupName || focusGroupName || "";
  const hasHighlight = Boolean(highlightName || pinnedNodeID);

  const nodeMap = new Map();
  const rawSpots = [];
  const recKeys = [];
  for (const group of groups || NO_VALUES) {
    for (const item of group.nodes || NO_VALUES) {
      if (item.kind === "ext" || !item.agentID) continue;
      let rec = nodeMap.get(item.agentID);
      if (!rec) {
        const { vector, flag } = resolveNodeGeo(item);
        if (!vector) continue;
        const spot = landing(view, vector);
        rec = {
          agentID: item.agentID,
          name: item.name,
          address: item.address,
          online: Boolean(item.online),
          issue: item.issue || "",
          vec: vector,
          flag,
          spot,
          groupNames: new Set(),
          degree: 0,
        };
        nodeMap.set(item.agentID, rec);
        recKeys.push(rec);
        rawSpots.push(spot);
      }
      rec.groupNames.add(group.groupName);
      rec.degree++;
      if (item.online) rec.online = true;
      if (item.issue && !rec.issue) rec.issue = item.issue;
    }
  }
  const nudged = nudgeCoLocated(rawSpots, size, { clampToFrame: false });
  recKeys.forEach((rec, i) => { rec.spot = nudged[i]; });

  const bd = backdrop(view, draft, idPrefix);
  const shapeForHop = ({ fromRec, toRec, isL1, crossesLimb, laneScale, sway = 0 }) => archShape(view, fromRec.vec, toRec.vec, {
    altitudeScale: isL1 ? 1.04 : laneScale,
    strictLimb: !isL1 && crossesLimb,
    sway,
  });

  const pinnedSlotMap = new Map();
  if (pinnedNodeID) {
    let slot = 0;
    for (const g of groups || NO_VALUES) {
      if ((g.nodes || NO_VALUES).some(n => n.agentID === pinnedNodeID)) {
        pinnedSlotMap.set(g.groupName, slot++);
      }
    }
  }

  const groupOrderMap = new Map((groups || NO_VALUES).map((g, idx) => [g.id || g.groupName, idx]));
  const ordered = [...(groups || NO_VALUES)].sort((a, b) => {
    const aHL = a.groupName === highlightName ? 10 : 0;
    const bHL = b.groupName === highlightName ? 10 : 0;
    if (aHL !== bHL) return aHL - bHL;
    return toneRank(a.overall?.statusBadge?.tone) - toneRank(b.overall?.statusBadge?.tone);
  });

  const bgLayer = createSVGEl("g", { "data-refresh-key": "lm-fleet-bg" });
  const fgLayer = createSVGEl("g", { "data-refresh-key": "lm-fleet-fg" });
  const activeNodeIDs = new Set();
  const hoverPillBoxes = [...nodeMap.values()]
    .filter(r => r.spot.on)
    .map(r => ({ x1: r.spot.x - 14, y1: r.spot.y - 12, x2: r.spot.x + 14, y2: r.spot.y + 12 }));
  let ambientBudget = 18;

  const placedApexes = [];
  const inSafeBounds = (pt) => pt && pt.x >= 14 && pt.x <= size.width - 14 && pt.y >= 14 && pt.y <= size.height - 14;
  const minDistToPlaced = (apex, placed) => {
    if (!apex || !placed.length) return Infinity;
    let best = Infinity;
    for (const p of placed) {
      const d = Math.hypot(apex.x - p.x, apex.y - p.y);
      if (d < best) best = d;
    }
    return best;
  };

  // Phase 1: Deterministic, hover-invariant corridor layout across all visible city pairs
  // guarantees 0.00px geometry drift when hovering any corridor (zero pointer flicker).
  const allCorridors = new Map();
  ordered.forEach((group) => {
    const groupIdx = groupOrderMap.get(group.id || group.groupName) ?? 0;
    group.hops.forEach((hop) => {
      const fromRec = nodeMap.get(hop.from?.agentID);
      const toRec = nodeMap.get(hop.to?.agentID);
      if (!fromRec || !toRec || (!fromRec.spot.on && !toRec.spot.on)) return;
      const aID = String(fromRec.agentID || ""), bID = String(toRec.agentID || "");
      const pairKey = aID < bID ? `${aID}::${bID}` : `${bID}::${aID}`;
      const fwdMark = metricDir !== "reverse" ? markToneForDir(hop.forward) : "";
      const revMark = (hop.bidirectional && metricDir !== "forward") ? markToneForDir(hop.reverse) : "";
      const comb = (fwdMark === "bad" || revMark === "bad") ? "bad" : (fwdMark || revMark);
      const idle = !comb && !isDirReachable(hop.forward) && (!hop.bidirectional || !isDirReachable(hop.reverse));
      const sev = comb === "bad" ? 3 : comb === "warn" ? 2 : idle ? 0 : 1;
      const crossesLimb = !fromRec.spot.on || !toRec.spot.on;
      const existing = allCorridors.get(pairKey);
      if (!existing) {
        allCorridors.set(pairKey, {
          pairKey,
          canonA: aID < bID ? fromRec : toRec,
          canonB: aID < bID ? toRec : fromRec,
          crossesLimb,
          sev,
          groupIdx,
        });
      } else if (sev > existing.sev) {
        existing.sev = sev;
      }
    });
  });

  const corridorLayout = new Map();
  const baseAlt = 0.94;
  [...allCorridors.values()]
    .sort((a, b) => a.sev - b.sev || a.pairKey.localeCompare(b.pairKey))
    .forEach((cor) => {
      const base = shapeForHop({
        fromRec: cor.canonA,
        toRec: cor.canonB,
        isL1: false,
        crossesLimb: cor.crossesLimb,
        laneScale: baseAlt,
        sway: 0,
      });
      if (!base || !base.path || !base.apex) {
        corridorLayout.set(cor.pairKey, { laneScale: baseAlt, sway: 0 });
        return;
      }
      const minGap = 16;
      const baseDist = minDistToPlaced(base.apex, placedApexes);
      if (baseDist >= minGap || base.span < 20) {
        placedApexes.push(base.apex);
        corridorLayout.set(cor.pairKey, { laneScale: baseAlt, sway: 0 });
        return;
      }
      let bestParams = { laneScale: baseAlt, sway: 0 };
      let bestApex = base.apex;
      let bestScore = baseDist + (inSafeBounds(base.apex) ? 0 : -40);
      const swayStep = clamp(0.068 + 7 / Math.max(70, base.span), 0.072, 0.145);
      for (const slot of [1, -1, 2, -2, 3, -3]) {
        const params = {
          laneScale: clamp(baseAlt + Math.abs(slot) * 0.12, 0.72, 1.36),
          sway: slot * swayStep,
        };
        const cand = shapeForHop({
          fromRec: cor.canonA,
          toRec: cor.canonB,
          isL1: false,
          crossesLimb: cor.crossesLimb,
          ...params,
        });
        if (!cand || !cand.path || !cand.apex) continue;
        const dist = minDistToPlaced(cand.apex, placedApexes);
        const score = dist + (inSafeBounds(cand.apex) ? 0 : -40);
        if (score > bestScore) {
          bestScore = score;
          bestParams = params;
          bestApex = cand.apex;
        }
        if (dist >= minGap && inSafeBounds(cand.apex)) break;
      }
      placedApexes.push(bestApex);
      corridorLayout.set(cor.pairKey, bestParams);
    });

  const shapeForStableCorridor = ({ fromRec, toRec, crossesLimb, extraSlot = 0 }) => {
    const aID = String(fromRec.agentID || ""), bID = String(toRec.agentID || "");
    const pairKey = aID < bID ? `${aID}::${bID}` : `${bID}::${aID}`;
    const reversed = aID > bID;
    const lay = corridorLayout.get(pairKey) || { laneScale: baseAlt, sway: 0 };
    const dirSign = reversed ? -1 : 1;
    let sway = lay.sway * dirSign;
    let laneScale = lay.laneScale;
    if (extraSlot !== 0) {
      const fanSlot = (extraSlot % 2 === 1 ? 1 : -1) * Math.ceil(extraSlot / 2);
      sway += fanSlot * 0.085;
      laneScale = clamp(laneScale + Math.abs(fanSlot) * 0.09, 0.72, 1.38);
    }
    return shapeForHop({ fromRec, toRec, isL1: false, crossesLimb, laneScale, sway });
  };

  // Phase 2: Partition into deduplicated background corridors and active foreground hops.
  const bgCorridors = new Map();
  const fgEntries = [];

  ordered.forEach((group) => {
    const groupIdx = groupOrderMap.get(group.id || group.groupName) ?? 0;
    const connectsPinned = Boolean(pinnedNodeID && group.nodes.some(n => n.agentID === pinnedNodeID));
    const isL1 = group.groupName === highlightName || connectsPinned;
    const pinnedSlot = connectsPinned ? (pinnedSlotMap.get(group.groupName) ?? null) : null;

    if (isL1) {
      for (const item of group.nodes) {
        if (item.kind !== "ext" && item.agentID) activeNodeIDs.add(item.agentID);
      }
    }

    group.hops.forEach((hop, hopIdx) => {
      const fromRec = nodeMap.get(hop.from?.agentID);
      const toRec = nodeMap.get(hop.to?.agentID);
      if (!fromRec || !toRec || (!fromRec.spot.on && !toRec.spot.on)) return;
      const crossesLimb = !fromRec.spot.on || !toRec.spot.on;

      const showFwd = metricDir !== "reverse";
      const showRev = isL1 && hop.bidirectional && metricDir !== "forward";
      const fwdMark = showFwd ? markToneForDir(hop.forward) : "";
      const revMark = showRev ? markToneForDir(hop.reverse) : "";
      const combinedMark = (fwdMark === "bad" || revMark === "bad") ? "bad" : (fwdMark || revMark);
      const bgRevMark = (hop.bidirectional && metricDir !== "forward") ? markToneForDir(hop.reverse) : "";
      const bgCombinedMark = (fwdMark === "bad" || bgRevMark === "bad") ? "bad" : (fwdMark || bgRevMark);
      const isIdle = !combinedMark && !isDirReachable(hop.forward) && (!hop.bidirectional || !isDirReachable(hop.reverse));
      const bgIdle = !bgCombinedMark && !isDirReachable(hop.forward) && (!hop.bidirectional || !isDirReachable(hop.reverse));

      if (isL1) {
        fgEntries.push({ group, groupIdx, hop, hopIdx, fromRec, toRec, crossesLimb, showFwd, showRev, combinedMark, isIdle, pinnedSlot });
        return;
      }

      const aID = String(fromRec.agentID || ""), bID = String(toRec.agentID || "");
      const pairKey = aID < bID ? `${aID}::${bID}` : `${bID}::${aID}`;
      const sev = bgCombinedMark === "bad" ? 3 : bgCombinedMark === "warn" ? 2 : bgIdle ? 0 : 1;
      const existing = bgCorridors.get(pairKey);
      if (!existing) {
        bgCorridors.set(pairKey, {
          pairKey,
          group,
          groupIdx,
          hop,
          hopIdx,
          fromRec,
          toRec,
          crossesLimb,
          bgCombinedMark,
          bgIdle,
          sev,
          groupNames: new Set([group.groupName]),
        });
      } else {
        existing.groupNames.add(group.groupName);
        if (sev > existing.sev) {
          existing.group = group;
          existing.groupIdx = groupIdx;
          existing.hop = hop;
          existing.hopIdx = hopIdx;
          existing.fromRec = fromRec;
          existing.toRec = toRec;
          existing.crossesLimb = crossesLimb;
          existing.bgCombinedMark = bgCombinedMark;
          existing.bgIdle = bgIdle;
          existing.sev = sev;
        }
      }
    });
  });

  // Paint deduplicated background corridors sorted idle -> ok -> warn -> bad.
  const sortedCorridors = [...bgCorridors.values()].sort((a, b) => a.sev - b.sev || a.groupIdx - b.groupIdx);
  sortedCorridors.forEach((rec) => {
    const { group, groupIdx, hop, hopIdx, fromRec, toRec, crossesLimb, bgCombinedMark, bgIdle, groupNames } = rec;
    const shape = shapeForStableCorridor({ fromRec, toRec, crossesLimb });
    if (!shape || !shape.path) return;

    const broken = bgCombinedMark === "bad";
    const isAlertHop = bgCombinedMark === "bad" || bgCombinedMark === "warn";
    const tierClass = hasHighlight ? "is-dim" : (isAlertHop ? "is-l2" : "is-l3");
    const bgCol = resolveConduitTone({ combinedMark: bgCombinedMark, isL1: false, pinnedSlot: null, isIdle: bgIdle });
    const forwardTone = railTone(hop.forward);
    const reverseTone = railTone(hop.reverse);
    const chosenDir = metricDir === "reverse" && hop.bidirectional ? hop.reverse : hop.forward;
    const flags = { running: group.running, ready: hop.ready };
    const metricLabel = hop.bidirectional
      ? `去程 ${formatDirBadgeMetric(hop.forward, flags)}，回程 ${formatDirBadgeMetric(hop.reverse, flags)}`
      : formatDirBadgeMetric(hop.forward, flags);
    const sharedPrefix = groupNames.size > 1 ? `[${groupNames.size} 条路由共用 · 代表：${group.groupName}] ` : `${group.groupName} · `;
    const label = `${sharedPrefix}${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name}，${metricLabel}`;

    const el = createSVGEl("g", {
      class: `a-lm-fleet-hop ${tierClass} ${bgCol}`,
      tabindex: !hasHighlight ? 0 : -1,
      role: "button",
      "aria-pressed": String(group.groupName === focusGroupName),
      "aria-label": label,
      "data-lm-group": group.groupName,
      "data-lm-hop": String(hopIdx),
      "data-refresh-key": `lm-fcor-${rec.pairKey}`,
    });
    el.append(titleElement(label));
    el.append(...renderConduitLayers({
      shape,
      lineColor: bgCol,
      isL1: false,
      isBroken: broken,
      isAlertHop,
      tierClass,
    }));
    if (crossesLimb && !hasHighlight) {
      for (const exit of shape.limbExits.slice(0, 1)) {
        el.append(createSVGEl("circle", { cx: fixed(exit.x), cy: fixed(exit.y), r: 2.3, class: `a-lm-limb-exit ${bgCol}` }));
      }
    }
    if (!hasHighlight && !crossesLimb && ((ambientBudget > 0 && isDirReachable(chosenDir)) || broken)) {
      if (!broken) ambientBudget--;
      const isRevOnly = chosenDir === hop.reverse && metricDir === "reverse";
      el.append(...renderLaserSweepLayers({
        shape,
        group,
        hop,
        showFwd: !isRevOnly,
        showRev: isRevOnly,
        forwardTone,
        reverseTone,
        lineColor: bgCol,
        isL1: false,
        isBroken: broken,
        staggerSlot: groupIdx + hopIdx,
        arcGlowId: bd.arcGlowId,
      }));
    }
    if (!hasHighlight && !crossesLimb && broken && shape.span >= 44) {
      el.append(...railMark(shape.apex, "bad", { compact: true }));
    }
    el.append(createSVGEl("path", { d: shape.path, class: "a-lm-hit is-line" }));
    bgLayer.append(el);
  });

  // Paint active foreground L1 hops on identical stable corridor geometry.
  const fgPairUsage = new Map();
  fgEntries.forEach((rec) => {
    const { group, hop, hopIdx, fromRec, toRec, crossesLimb, showFwd, showRev, combinedMark, isIdle, pinnedSlot } = rec;
    const aID = String(fromRec.agentID || ""), bID = String(toRec.agentID || "");
    const pairKey = aID < bID ? `${aID}::${bID}` : `${bID}::${aID}`;
    const extraSlot = fgPairUsage.get(pairKey) || 0;
    fgPairUsage.set(pairKey, extraSlot + 1);
    const shape = shapeForStableCorridor({ fromRec, toRec, crossesLimb, extraSlot });
    if (!shape || !shape.path) return;

    const lineCol = resolveConduitTone({ combinedMark, isL1: true, pinnedSlot, isIdle });
    const forwardTone = railTone(hop.forward);
    const reverseTone = railTone(hop.reverse);
    const flags = { running: group.running, ready: hop.ready };
    const metricLabel = hop.bidirectional
      ? `去程 ${formatDirBadgeMetric(hop.forward, flags)}，回程 ${formatDirBadgeMetric(hop.reverse, flags)}`
      : formatDirBadgeMetric(hop.forward, flags);
    const label = `${group.groupName} · ${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name}，${metricLabel}`;

    const el = createSVGEl("g", {
      class: `a-lm-fleet-hop is-l1 ${lineCol}`,
      tabindex: 0,
      role: "button",
      "aria-pressed": String(group.groupName === focusGroupName),
      "aria-label": label,
      "data-lm-group": group.groupName,
      "data-lm-hop": String(hopIdx),
      "data-refresh-key": `lm-fhop-${group.id}-${hopIdx}`,
    });
    el.append(titleElement(label));
    el.append(...renderConduitLayers({
      shape,
      lineColor: lineCol,
      isL1: true,
      isBroken: combinedMark === "bad",
      isAlertHop: Boolean(combinedMark),
      tierClass: "is-l1",
    }));
    el.append(...renderLaserSweepLayers({
      shape,
      group,
      hop,
      showFwd,
      showRev,
      forwardTone,
      reverseTone,
      lineColor: lineCol,
      isL1: true,
      isBroken: combinedMark === "bad",
      staggerSlot: hopIdx,
      arcGlowId: bd.arcGlowId,
    }));
    if (combinedMark) el.append(...railMark(shape.apex, combinedMark, { compact: shape.span < 44 }));
    if (hoverGroupName && group.groupName === hoverGroupName && group.hops.length <= 4) {
      const outText = formatDirBadgeMetric(hop.forward, flags);
      const inText = hop.bidirectional ? formatDirBadgeMetric(hop.reverse, flags) : "";
      const pillText = metricDir === "reverse" && hop.bidirectional
        ? `← ${inText}`
        : hop.bidirectional && metricDir === "both" && outText !== inText
          ? `${outText} / ${inText}`
          : outText;
      const pill = hopPillElement(shape.apex, pillText, size, lineCol, Boolean(combinedMark), {
        alwaysVisible: true,
        avoidBoxes: hoverPillBoxes,
        recordBox: true,
      });
      if (pill) el.append(pill);
    }
    el.append(createSVGEl("path", { d: shape.path, class: "a-lm-hit is-line" }));
    fgLayer.append(el);
  });

  const nodesLayer = createSVGEl("g", { "data-refresh-key": "lm-fleet-nodes" });
  const nodeList = [...nodeMap.values()]
    .filter(rec => rec.spot.on)
    .sort((a, b) => {
      const aAct = (a.agentID === pinnedNodeID ? 20 : activeNodeIDs.has(a.agentID) ? 10 : 0);
      const bAct = (b.agentID === pinnedNodeID ? 20 : activeNodeIDs.has(b.agentID) ? 10 : 0);
      if (aAct !== bAct) return aAct - bAct;
      return a.degree - b.degree;
    });

  // Detect tight regional clusters (pairwise spacing <= 28px) for compact pins,
  // outward radial callout labels, and 1-click `+N` zoom-expand chips.
  const CLUSTER_LINK_PX = 28;
  const clusterVisited = new Array(nodeList.length).fill(false);
  const tightNodeSet = new Set();
  const tightClusters = [];
  for (let i = 0; i < nodeList.length; i++) {
    if (clusterVisited[i]) continue;
    const comp = [];
    const queue = [i];
    clusterVisited[i] = true;
    while (queue.length) {
      const cur = queue.shift();
      comp.push(cur);
      for (let j = 0; j < nodeList.length; j++) {
        if (clusterVisited[j]) continue;
        const d = Math.hypot(nodeList[cur].spot.x - nodeList[j].spot.x, nodeList[cur].spot.y - nodeList[j].spot.y);
        if (d <= CLUSTER_LINK_PX) {
          clusterVisited[j] = true;
          queue.push(j);
        }
      }
    }
    if (comp.length >= 2) {
      const members = comp.map(idx => nodeList[idx]);
      members.forEach(m => tightNodeSet.add(m.agentID));
      const cx = members.reduce((s, m) => s + m.spot.x, 0) / members.length;
      const cy = members.reduce((s, m) => s + m.spot.y, 0) / members.length;
      const maxR = members.reduce((mx, m) => Math.max(mx, Math.hypot(m.spot.x - cx, m.spot.y - cy)), 0);
      const vecs = members.map(m => m.vec).filter(Boolean);
      const center = unit3(vecs.reduce((sum, v) => mix3(sum, 1, v, 1), [0, 0, 0]));
      let spread = 0.04;
      for (let a = 0; a < vecs.length; a++) {
        for (let b = a + 1; b < vecs.length; b++) {
          spread = Math.max(spread, Math.acos(clamp(dot3(vecs[a], vecs[b]), -1, 1)));
        }
      }
      const spec = center ? `3d:${center[0].toFixed(4)},${center[1].toFixed(4)},${center[2].toFixed(4)},${spread.toFixed(4)}` : "";
      tightClusters.push({
        key: members.map(m => m.agentID).sort().join("+"),
        members,
        cx,
        cy,
        r: clamp(maxR + 11, 15, 34),
        spec,
      });
    }
  }

  const clusterLayer = createSVGEl("g", { "data-refresh-key": "lm-fleet-clusters" });
  const clusterChipsLayer = createSVGEl("g", { "data-refresh-key": "lm-fleet-cluster-chips" });
  const clusterObstacles = [];
  if (!hasHighlight && tightClusters.length) {
    for (const cl of tightClusters) {
      if (!cl.spec) continue;
      const names = cl.members.map(m => shortNodeName(m.name)).join("、");
      const title = `近距区域集群（${names}，共 ${cl.members.length} 站）· 点击平滑放大展开`;
      const ringG = createSVGEl("g", {
        class: "a-lm-cluster-wrap",
        "data-lm-cluster": cl.spec,
        "data-refresh-key": `lm-cluster-ring-${cl.key}`,
      });
      ringG.append(
        titleElement(title),
        createSVGEl("circle", { cx: fixed(cl.cx), cy: fixed(cl.cy), r: fixed(cl.r), class: "a-lm-cluster-zone" }),
      );
      clusterLayer.append(ringG);

      const chipX = clamp(cl.cx + cl.r * 0.72, 16, size.width - 34);
      const chipY = clamp(cl.cy - cl.r * 0.72, 14, size.height - 16);
      const chipW = 24, chipH = 14;
      const chipG = createSVGEl("g", {
        class: "a-lm-cluster-chip",
        tabindex: 0,
        role: "button",
        "aria-label": title,
        "data-lm-cluster": cl.spec,
        "data-refresh-key": `lm-cluster-chip-${cl.key}`,
      });
      chipG.append(
        titleElement(title),
        createSVGEl("rect", {
          x: fixed(chipX - chipW / 2),
          y: fixed(chipY - chipH / 2),
          width: chipW,
          height: chipH,
          rx: 7,
          class: "a-lm-cluster-chip-bg",
        }),
        createSVGEl("text", {
          x: fixed(chipX),
          y: fixed(chipY + 3.4),
          "text-anchor": "middle",
          class: "a-lm-cluster-chip-text",
        }, `+${cl.members.length}`),
      );
      clusterChipsLayer.append(chipG);
      clusterObstacles.push({
        x1: chipX - chipW / 2 - 2,
        y1: chipY - chipH / 2 - 2,
        x2: chipX + chipW / 2 + 2,
        y2: chipY + chipH / 2 + 2,
      });
    }
  }

  const allSpots = nodeList.map(rec => rec.spot);
  const occupied = [
    ...nodeList.map(rec => ({
      x1: rec.spot.x - 7.5,
      y1: rec.spot.y - 7.5,
      x2: rec.spot.x + 7.5,
      y2: rec.spot.y + 7.5,
    })),
    ...clusterObstacles,
  ];
  const idleMaxLabels = size.width >= 900 ? 12 : size.width >= 520 ? 9 : 6;
  let idlePlacedCount = 0;
  const labelPlacements = new Map();

  for (let i = nodeList.length - 1; i >= 0; i--) {
    const rec = nodeList[i];
    const isAct = rec.agentID === pinnedNodeID || activeNodeIDs.has(rec.agentID);
    if (hasHighlight && !isAct) continue;
    const isTight = tightNodeSet.has(rec.agentID);
    if (!isAct && idlePlacedCount >= idleMaxLabels) continue;
    const fw = isAct ? FLAG_W : FLEET_FLAG_W;
    const fg = isAct ? FLAG_GAP : FLEET_FLAG_GAP;
    const tagH = isAct ? 17 : 15;
    const short = isAct
      ? clipLabel(size.width < 440 ? shortNodeName(rec.name) : rec.name, NAME_UNITS)
      : clipLabel(shortNodeName(rec.name), 12);
    const textW = visualUnits(short) * (isAct ? UNIT_PX : 6.0);
    const totalW = (rec.flag ? fw + fg : 0) + textW;
    const prox = computeClusterOutwardVec(i, allSpots, 54);
    const prefer = prox.awayVec
      ? (prox.awayVec.x < -0.2 ? -1 : prox.awayVec.x > 0.2 ? 1 : 0)
      : (rec.spot.x > size.width * 0.65 ? -1 : 1);
    const solved = solveLabelPlacement({
      spot: rec.spot,
      totalW,
      labelH: tagH,
      view: size,
      preferSide: prefer,
      occupied,
      skipObstacleIndex: i,
      pipCount: nodeList.length,
      awayVec: prox.awayVec,
      isCrowded: prox.crowded,
    });
    const maxScore = isTight ? 90 : 38;
    if (!isAct && solved.score >= maxScore) continue;
    const boxPad = isAct ? 4 : (isTight ? 5 : 9);
    occupied.push({ x1: solved.box.x1 - boxPad, y1: solved.box.y1 - boxPad, x2: solved.box.x2 + boxPad, y2: solved.box.y2 + boxPad });
    labelPlacements.set(rec.agentID, { short, totalW, labelH: tagH, placement: solved, isTight });
    if (!isAct) idlePlacedCount++;
  }

  for (const rec of nodeList) {
    const x = fixed(rec.spot.x), y = fixed(rec.spot.y);
    const isPinned = rec.agentID === pinnedNodeID;
    const isActive = isPinned || activeNodeIDs.has(rec.agentID);
    const isDim = hasHighlight && !isActive;
    const isTight = tightNodeSet.has(rec.agentID);
    const label = `${rec.name}${rec.issue ? `，${rec.issue}` : (!rec.online ? "，离线" : "")} · 参与 ${rec.groupNames.size} 个监测组`;
    const g = createSVGEl("g", {
      class: `a-lm-node a-lm-fleet-node${isDim ? " is-dim" : ""}${isActive ? " is-active" : ""}${isTight ? " is-tight" : ""}`,
      tabindex: 0,
      role: "button",
      "aria-pressed": String(isPinned),
      "aria-label": label,
      "data-lm-node": rec.agentID,
      "data-refresh-key": `lm-fnode-${rec.agentID}`,
    });
    g.append(titleElement(label));
    if (isPinned || (rec.online && !rec.issue && isActive)) {
      g.append(createSVGEl("circle", { cx: x, cy: y, r: isTight ? 4 : 5, class: isPinned ? "a-lm-ping is-focus" : "a-lm-ping", "vector-effect": "non-scaling-stroke" }));
    }
    const placed = labelPlacements.get(rec.agentID);
    if (placed && (isActive || isTight)) {
      const leader = labelLeaderElement(rec.spot, placed.placement, placed.totalW, placed.labelH);
      if (leader) g.append(leader);
    }
    if (isActive) {
      g.append(
        createSVGEl("circle", { cx: x, cy: y, r: isTight ? 4.0 : 4.7, class: "a-lm-bed" }),
        createSVGEl("circle", { cx: x, cy: y, r: isTight ? 6.8 : 8.2, class: "a-lm-ring" }),
        pipElement({ kind: "agent", online: rec.online, issue: rec.issue }, x, y, isTight ? PIP * 0.82 : PIP),
      );
    } else {
      g.append(
        createSVGEl("circle", { cx: x, cy: y, r: isTight ? 3.2 : 3.8, class: "a-lm-bed" }),
        createSVGEl("circle", { cx: x, cy: y, r: isTight ? 6.0 : 7.2, class: "a-lm-ring" }),
        createSVGEl("circle", { cx: x, cy: y, r: isTight ? 2.35 : 2.8, class: rec.online ? "a-lm-fleet-dot is-on" : "a-lm-fleet-dot" }),
      );
    }
    if (placed) {
      appendGlobeFlagAndName(g, {
        flag: rec.flag,
        text: placed.short,
        x: placed.placement.x,
        y: placed.placement.cy,
        flagW: isActive ? FLAG_W : FLEET_FLAG_W,
        flagH: isActive ? FLAG_H : FLEET_FLAG_H,
        flagGap: isActive ? FLAG_GAP : FLEET_FLAG_GAP,
        textClass: isActive ? "a-lm-name" : "a-lm-name is-fleet-sub",
      });
    }
    const hitHalf = isTight ? 8.5 : 14;
    g.append(createSVGEl("rect", { x: fixed(x - hitHalf), y: fixed(y - hitHalf), width: hitHalf * 2, height: hitHalf * 2, class: "a-lm-hit" }));
    nodesLayer.append(g);
  }

  svg.append(bd.defs, bd.globe, clusterLayer, bgLayer, fgLayer, nodesLayer, clusterChipsLayer);
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

function traceScale(values) {
  const low = Math.min(...values), high = Math.max(...values);
  const spread = Math.max(high - low, high * 0.1, 1);
  return { low: Math.max(0, low - spread), high: high + spread * 0.25 };
}

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
