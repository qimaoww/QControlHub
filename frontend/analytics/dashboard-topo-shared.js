import { createSVGEl } from "./dashboard-panel.js";
import { geoRegionDetails } from "../modules/region-model.js";
import { projectPacificGeo } from "./pacific-map-data.js";

export const INGRESS_COLOR = "#18181b";
export const EGRESS_COLOR = "#71717a";
export const BLOCKED_COLOR = "#a41a2b";
export const IDLE_FLOW_COLOR = "#a1a1aa";
export const OK_COLOR = "#155731";
export const WARN_COLOR = "#b45309";

export function visualUnits(text) {
  let units = 0;
  for (const ch of String(text || "")) {
    units += ch.charCodeAt(0) > 255 ? 1.85 : 1;
  }
  return units;
}

export function clipLabel(text, maxUnits) {
  const value = String(text || "");
  let units = 0;
  let out = "";
  for (const ch of value) {
    const step = ch.charCodeAt(0) > 255 ? 1.85 : 1;
    if (units + step > maxUnits) return out ? `${out}…` : "…";
    units += step;
    out += ch;
  }
  return out;
}

export function tonePalette(tone) {
  if (tone === "ok") return { stroke: "#155731", fill: "#eef9f0", border: "#c9e8d2", ink: "#155731" };
  if (tone === "warn") return { stroke: "#b45309", fill: "#fff8e9", border: "#efddbb", ink: "#754511" };
  if (tone === "bad") return { stroke: "#a41a2b", fill: "#fff2f3", border: "#f2cdd2", ink: "#a41a2b" };
  return { stroke: "#71717a", fill: "#f4f4f5", border: "#e4e4e7", ink: "#52525b" };
}

export function flowDur(weight) {
  return `${Math.max(1.0, 2.55 - (Number(weight) || 0) * 1.45).toFixed(2)}s`;
}

export function formatMs(ms) {
  const num = Number(ms);
  return `${num.toFixed(num >= 100 ? 0 : 1)}ms`;
}

export function isProbeUnreachable(probe) {
  return probe.latencyMS == null || (probe.lossPercent != null && probe.lossPercent >= 100 && Number(probe.latencyMS) === 0);
}

export function buildProbePillLabel(probe, compact = false, dense = false) {
  if (probe.running) return "探测中…";
  if (isProbeUnreachable(probe)) {
    return probe.lossPercent != null && probe.lossPercent >= 100 ? "不可达 · 100%" : probe.metricLabel;
  }
  const slash = dense ? "/" : " / ";
  const rtt = !compact && probe.bidirectional && probe.revLatencyMS != null
    ? `${formatMs(probe.latencyMS)}${slash}${formatMs(probe.revLatencyMS)}`
    : formatMs(probe.latencyMS);
  if (probe.lossPercent == null) return rtt;
  const lossNum = probe.lossPercent.toFixed(probe.lossPercent % 1 ? 1 : 0);
  if (compact) return probe.lossPercent > 0 ? `${rtt} · ${lossNum}%` : rtt;
  return probe.lossPercent > 0 ? `${rtt} · 丢包${lossNum}%` : `${rtt} · 0%`;
}

/** Concise ambient alert text for severe faults surfaced on default overview canvases. */
export function buildProbeShortAlertLabel(probe) {
  if (probe.running) return "探测中…";
  if (isProbeUnreachable(probe)) return "不可达";
  if (probe.lossPercent != null && probe.lossPercent > 0) {
    const lossNum = probe.lossPercent.toFixed(probe.lossPercent % 1 ? 1 : 0);
    return `丢包${lossNum}%`;
  }
  return probe.metricLabel || "异常";
}

/** Selects up to `maxCount` highest-severity `bad` probe IDs for ambient canvas callouts. */
export function selectTopAmbientBadProbeIDs(probeLinks, { includeExternal = true, maxCount = 5 } = {}) {
  const candidates = (probeLinks || []).filter((p) => p.tone === "bad" && (includeExternal || !p.isExternal));
  candidates.sort((a, b) => {
    const aUnreach = isProbeUnreachable(a) ? 1 : 0;
    const bUnreach = isProbeUnreachable(b) ? 1 : 0;
    if (bUnreach !== aUnreach) return bUnreach - aUnreach;
    const lossDiff = (Number(b.lossPercent) || 0) - (Number(a.lossPercent) || 0);
    if (lossDiff !== 0) return lossDiff;
    const latDiff = (Number(b.latencyMS) || 0) - (Number(a.latencyMS) || 0);
    if (latDiff !== 0) return latDiff;
    return String(a.id || "").localeCompare(String(b.id || ""));
  });
  return new Set(candidates.slice(0, Math.max(0, maxCount)).map((p) => p.id));
}

export function cubicPath(x1, y1, x2, y2) {
  const dx = Math.max(26, Math.abs(x2 - x1) * 0.48);
  return `M ${x1.toFixed(1)} ${y1.toFixed(1)} C ${(x1 + dx).toFixed(1)} ${y1.toFixed(1)}, ${(x2 - dx).toFixed(1)} ${y2.toFixed(1)}, ${x2.toFixed(1)} ${y2.toFixed(1)}`;
}

export function cubicPointAt(x1, y1, x2, y2, t = 0.5) {
  const dx = Math.max(26, Math.abs(x2 - x1) * 0.48);
  const cx1 = x1 + dx;
  const cx2 = x2 - dx;
  const mt = 1 - t;
  const x = mt * mt * mt * x1 + 3 * mt * mt * t * cx1 + 3 * mt * t * t * cx2 + t * t * t * x2;
  const y = mt * mt * mt * y1 + 3 * mt * mt * t * y1 + 3 * mt * t * t * y2 + t * t * t * y2;
  return { x, y };
}

export function extendedCubicPath(x0, railX, y1, x2, y2) {
  if (!(railX > x0 + 1)) {
    return {
      d: cubicPath(x0, y1, x2, y2),
      pointAt: (t) => cubicPointAt(x0, y1, x2, y2, t),
    };
  }
  const dx = Math.max(26, Math.abs(x2 - railX) * 0.48);
  const d = `M ${x0.toFixed(1)} ${y1.toFixed(1)} L ${railX.toFixed(1)} ${y1.toFixed(1)} C ${(railX + dx).toFixed(1)} ${y1.toFixed(1)}, ${(x2 - dx).toFixed(1)} ${y2.toFixed(1)}, ${x2.toFixed(1)} ${y2.toFixed(1)}`;
  return {
    d,
    pointAt: (t) => cubicPointAt(railX, y1, x2, y2, t),
  };
}

export function bridgeArchPath(x0, yA, yB, bow, railOffset = 0) {
  const sx = x0 + Math.max(0, railOffset);
  const apexX = sx + bow;
  const yMid = (yA + yB) / 2;
  const dir = yB >= yA ? 1 : -1;
  const c1 = Math.max(24, bow * 0.64);
  const c2 = Math.max(12, Math.abs(yB - yA) * 0.24);
  const lead = railOffset > 0 ? [`M ${x0.toFixed(1)} ${yA.toFixed(1)}`, `L ${sx.toFixed(1)} ${yA.toFixed(1)}`] : [`M ${sx.toFixed(1)} ${yA.toFixed(1)}`];
  const tail = railOffset > 0 ? [`L ${x0.toFixed(1)} ${yB.toFixed(1)}`] : [`L ${sx.toFixed(1)} ${yB.toFixed(1)}`];
  const d = [
    ...lead,
    `C ${(sx + c1).toFixed(1)} ${yA.toFixed(1)}, ${apexX.toFixed(1)} ${(yMid - dir * c2).toFixed(1)}, ${apexX.toFixed(1)} ${yMid.toFixed(1)}`,
    `C ${apexX.toFixed(1)} ${(yMid + dir * c2).toFixed(1)}, ${(sx + c1).toFixed(1)} ${yB.toFixed(1)}, ${sx.toFixed(1)} ${yB.toFixed(1)}`,
    ...(railOffset > 0 ? tail : []),
  ].join(" ");
  return { d, apexX, apexY: yMid };
}

export function bridgePointAt(x0, yA, yB, bow, t = 0.5, railOffset = 0) {
  const sx = x0 + Math.max(0, railOffset);
  const apexX = sx + bow;
  const yMid = (yA + yB) / 2;
  const dir = yB >= yA ? 1 : -1;
  const c1 = Math.max(24, bow * 0.64);
  const c2 = Math.max(12, Math.abs(yB - yA) * 0.24);
  const evalSeg = (p0x, p0y, p1x, p1y, p2x, p2y, p3x, p3y, u) => {
    const mu = 1 - u;
    return {
      x: mu * mu * mu * p0x + 3 * mu * mu * u * p1x + 3 * mu * u * u * p2x + u * u * u * p3x,
      y: mu * mu * mu * p0y + 3 * mu * mu * u * p1y + 3 * mu * u * u * p2y + u * u * u * p3y,
    };
  };
  if (t <= 0.5) {
    return evalSeg(sx, yA, sx + c1, yA, apexX, yMid - dir * c2, apexX, yMid, Math.max(0, t * 2));
  }
  return evalSeg(apexX, yMid, apexX, yMid + dir * c2, sx + c1, yB, sx, yB, Math.min(1, (t - 0.5) * 2));
}

export function quadPointAt(x1, y1, cx, cy, x2, y2, t) {
  const mt = 1 - t;
  return {
    x: mt * mt * x1 + 2 * mt * t * cx + t * t * x2,
    y: mt * mt * y1 + 2 * mt * t * cy + t * t * y2,
  };
}

export function assignProbeLanes(probes, rowByID) {
  const internal = probes
    .filter((p) => !p.isExternal && rowByID.has(p.from) && rowByID.has(p.to))
    .map((p) => {
      const rA = rowByID.get(p.from);
      const rB = rowByID.get(p.to);
      return {
        probe: p,
        min: Math.min(rA, rB),
        max: Math.max(rA, rB),
        span: Math.abs(rB - rA),
      };
    })
    .sort((a, b) => a.span - b.span || a.min - b.min || a.max - b.max);

  const lanes = [];
  const laneOf = new Map();
  for (const item of internal) {
    let idx = lanes.findIndex((occupied) => occupied.every((o) => item.max <= o.min || item.min >= o.max));
    if (idx < 0) {
      idx = lanes.length;
      lanes.push([]);
    }
    lanes[idx].push({ min: item.min, max: item.max });
    laneOf.set(item.probe.id, { lane: idx, count: 0 });
  }
  const maxLanes = Math.max(1, lanes.length);
  laneOf.forEach((v) => { v.count = maxLanes; });
  return laneOf;
}

export function rectsOverlap(a, b, padX = 4, padY = padX) {
  return a.x - padX < b.x + b.w + padX
    && a.x + a.w + padX > b.x - padX
    && a.y - padY < b.y + b.h + padY
    && a.y + a.h + padY > b.y - padY;
}

function pointsClose(p, q, eps = 0.5) {
  return Math.abs(p.x - q.x) <= eps && Math.abs(p.y - q.y) <= eps;
}

function cross2d(o, a, b) {
  return (a.x - o.x) * (b.y - o.y) - (a.y - o.y) * (b.x - o.x);
}

function onSegment2d(p, q, r) {
  return q.x <= Math.max(p.x, r.x) + 1e-6 && q.x >= Math.min(p.x, r.x) - 1e-6
    && q.y <= Math.max(p.y, r.y) + 1e-6 && q.y >= Math.min(p.y, r.y) - 1e-6;
}

/** Tests whether two 2D line segments cross, ignoring shared endpoints. */
export function segmentsIntersect(a1, a2, b1, b2) {
  if (!a1 || !a2 || !b1 || !b2) return false;
  if (pointsClose(a1, b1) || pointsClose(a1, b2) || pointsClose(a2, b1) || pointsClose(a2, b2)) {
    return false;
  }
  const d1 = cross2d(b1, b2, a1);
  const d2 = cross2d(b1, b2, a2);
  const d3 = cross2d(a1, a2, b1);
  const d4 = cross2d(a1, a2, b2);

  if (((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) && ((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0))) {
    return true;
  }
  if (Math.abs(d1) < 1e-6 && onSegment2d(b1, a1, b2)) return true;
  if (Math.abs(d2) < 1e-6 && onSegment2d(b1, a2, b2)) return true;
  if (Math.abs(d3) < 1e-6 && onSegment2d(a1, b1, a2)) return true;
  if (Math.abs(d4) < 1e-6 && onSegment2d(a1, b2, a2)) return true;
  return false;
}

function nearestAnchorFor(cx, cy, anchorList) {
  let best = anchorList[0];
  let bestD = Infinity;
  for (const a of anchorList) {
    const d = Math.hypot(cx - a.x, cy - a.y);
    if (d < bestD) {
      bestD = d;
      best = a;
    }
  }
  return { anchor: best, dist: bestD };
}

export function placeCollisionFreePill(targetX, targetY, w, h, occupied, bounds, anchors = null, opts = {}) {
  const padX = opts.padX ?? 3;
  const padY = opts.padY ?? 2.5;
  const reserveW = Math.max(w, opts.reserveW ?? w);
  const reserveH = Math.max(h, opts.reserveH ?? h);
  const minCX = bounds.minX + reserveW / 2;
  const maxCX = Math.max(minCX, bounds.maxX - reserveW / 2);
  const minCY = bounds.minY + reserveH / 2;
  const maxCY = Math.max(minCY, bounds.maxY - reserveH / 2);
  const clampPoint = (px, py) => ({
    cx: Math.max(minCX, Math.min(maxCX, px)),
    cy: Math.max(minCY, Math.min(maxCY, py)),
  });
  const anchorList = Array.isArray(anchors) && anchors.length ? anchors : [{ x: targetX, y: targetY }];
  const leaderPoints = Array.isArray(opts.leaderPoints) && opts.leaderPoints.length ? opts.leaderPoints : anchorList;
  const leaderSegments = Array.isArray(opts.leaderSegments) ? opts.leaderSegments : null;
  const singleLeaderOrigin = leaderSegments && leaderPoints.length === 1 ? leaderPoints[0] : null;
  const localDx = [0, 10, -10, 22, -22, 34, -34];
  const localDy = [0, -8, 8, -16, 16];

  const boxFits = (box) => !occupied.some((o) => rectsOverlap(box, o, padX, padY));
  const leaderClear = (cx, cy) => !singleLeaderOrigin
    || !leaderSegments.some((seg) => segmentsIntersect(singleLeaderOrigin, { x: cx, y: cy }, seg.a, seg.b));

  const finalize = (chosen, fallback) => {
    const picked = chosen || fallback;
    occupied.push(picked.box);
    const { anchor: nearA, dist } = nearestAnchorFor(picked.cx, picked.cy, leaderPoints);
    if (singleLeaderOrigin && Math.hypot(picked.cx - singleLeaderOrigin.x, picked.cy - singleLeaderOrigin.y) > 4) {
      leaderSegments.push({ a: singleLeaderOrigin, b: { x: picked.cx, y: picked.cy } });
    }
    return { cx: picked.cx, cy: picked.cy, anchorX: nearA.x, anchorY: nearA.y, shifted: dist > 18 };
  };

  let fallback = null;
  let boxOnlyMatch = null;
  for (const anchor of anchorList) {
    for (const dy of localDy) {
      for (const dx of localDx) {
        const { cx, cy } = clampPoint(anchor.x + dx, anchor.y + dy);
        const box = { x: cx - reserveW / 2, y: cy - reserveH / 2, w: reserveW, h: reserveH };
        if (!fallback) fallback = { cx, cy, box };
        if (boxFits(box)) {
          if (!boxOnlyMatch) boxOnlyMatch = { cx, cy, box };
          if (leaderClear(cx, cy)) return finalize({ cx, cy, box }, fallback);
        }
      }
    }
  }

  // Stage 2: Expanding minimum-distance ring search around every anchor across the available corridor
  const spanY = Math.max(240, Math.round(maxCY - minCY));
  const stepY = Math.max(10, Math.round(reserveH * 0.55));
  const stepX = Math.max(14, Math.round(reserveW * 0.22));
  let best = null;
  let bestScore = Infinity;

  for (let absDy = 0; absDy <= spanY; absDy += stepY) {
    const dySigns = absDy === 0 ? [0] : [-absDy, absDy];
    if (best && absDy * absDy >= bestScore) break;
    for (const dy of dySigns) {
      for (const base of anchorList) {
        const py = Math.max(minCY, Math.min(maxCY, base.y + dy));
        for (let px = minCX; px <= maxCX + 0.5; px += stepX) {
          const cx = Math.min(maxCX, px);
          const cy = py;
          const box = { x: cx - reserveW / 2, y: cy - reserveH / 2, w: reserveW, h: reserveH };
          if (!boxFits(box)) continue;
          const score = Math.pow((cx - base.x) * 0.62, 2) + Math.pow(cy - base.y, 2);
          if (!boxOnlyMatch) boxOnlyMatch = { cx, cy, box };
          if (leaderClear(cx, cy) && score < bestScore) {
            bestScore = score;
            best = { cx, cy, box };
          }
        }
      }
    }
  }

  // Stage 3: Full-canvas sweep safeguard to prevent pill collisions under extreme density
  if (!best) {
    for (let cy = minCY; cy <= maxCY + 0.5; cy += 10) {
      for (let cx = minCX; cx <= maxCX + 0.5; cx += 12) {
        const clampedX = Math.min(maxCX, cx);
        const clampedY = Math.min(maxCY, cy);
        const box = { x: clampedX - reserveW / 2, y: clampedY - reserveH / 2, w: reserveW, h: reserveH };
        if (!boxFits(box)) continue;
        if (!boxOnlyMatch) boxOnlyMatch = { cx: clampedX, cy: clampedY, box };
        if (!leaderClear(clampedX, clampedY)) continue;
        const { dist } = nearestAnchorFor(clampedX, clampedY, leaderPoints);
        if (dist < bestScore) {
          bestScore = dist;
          best = { cx: clampedX, cy: clampedY, box };
        }
      }
    }
  }

  return finalize(best || boxOnlyMatch, fallback);
}

export function createTopologyDefs() {
  const defs = createSVGEl("defs", { "data-refresh-key": "topo-defs" });

  const addGrad = (id, coords, stops) => {
    const grad = createSVGEl("linearGradient", { id, "data-refresh-key": id, ...coords });
    stops.forEach(([offset, color, opacity]) => {
      grad.append(createSVGEl("stop", { offset, "stop-color": color, "stop-opacity": String(opacity) }));
    });
    defs.append(grad);
  };

  addGrad("a-topo-grad-rx", { x1: "0%", y1: "0%", x2: "100%", y2: "0%" }, [["0%", "#18181b", 0.90], ["100%", "#3f3f46", 0.72]]);
  addGrad("a-topo-grad-tx", { x1: "0%", y1: "0%", x2: "100%", y2: "0%" }, [["0%", "#52525b", 0.82], ["100%", "#71717a", 0.65]]);
  addGrad("a-topo-grad-bar", { x1: "0%", y1: "0%", x2: "100%", y2: "0%" }, [["0%", "#18181b", 0.92], ["100%", "#52525b", 0.78]]);

  const addArrow = (id, color) => {
    const marker = createSVGEl("marker", {
      id,
      "data-refresh-key": id,
      viewBox: "0 0 10 10",
      refX: "8.2",
      refY: "5",
      markerWidth: "5.2",
      markerHeight: "5.2",
      orient: "auto-start-reverse",
    });
    marker.append(createSVGEl("path", { d: "M 0 1.8 L 8.5 5 L 0 8.2 z", fill: color }));
    defs.append(marker);
  };

  addArrow("a-topo-arrow-rx", INGRESS_COLOR);
  addArrow("a-topo-arrow-tx", EGRESS_COLOR);
  addArrow("a-topo-arrow-ok", OK_COLOR);
  addArrow("a-topo-arrow-warn", WARN_COLOR);
  addArrow("a-topo-arrow-bad", BLOCKED_COLOR);
  addArrow("a-topo-arrow-idle", IDLE_FLOW_COLOR);

  return defs;
}

export function buildPeerProbeTags(model) {
  const map = new Map();
  for (const n of model.nodes) map.set(n.id, []);
  for (const ext of (model.externalTargets || [])) map.set(ext.id, []);
  for (const p of model.probeLinks) {
    const tag = `probe:${p.id}`;
    map.get(p.from)?.push(tag);
    map.get(p.to)?.push(tag);
    if (p.isExternal) {
      map.get(p.from)?.push(p.to);
      map.get(p.to)?.push(`node:${p.from}`);
    }
  }
  return map;
}

/** Matches an element's peer tags against a single entity key or a `cluster:id1,id2,...` composite key. */
export function matchesActiveSelection(peers, sel) {
  if (!sel) return null;
  if (peers.includes(sel)) return true;
  if (sel.startsWith("cluster:")) {
    const memberIDs = sel.slice("cluster:".length).split(",").filter(Boolean);
    if (memberIDs.some((id) => peers.includes(`node:${id}`))) return true;
  }
  return false;
}

const HUB_GEO_KEYWORDS = Object.freeze([
  // China Mainland, HK, TW
  { countries: ["CN"], keys: ["上海", "shanghai", "sha", "pvg"], lat: 31.23, lon: 121.47 },
  { countries: ["CN"], keys: ["北京", "beijing", "pek", "bjs"], lat: 39.90, lon: 116.40 },
  { countries: ["CN"], keys: ["广州", "guangzhou", "can"], lat: 23.13, lon: 113.26 },
  { countries: ["CN"], keys: ["深圳", "shenzhen", "szx"], lat: 22.54, lon: 114.06 },
  { countries: ["CN"], keys: ["杭州", "hangzhou", "hgh"], lat: 30.27, lon: 120.15 },
  { countries: ["CN"], keys: ["成都", "chengdu", "ctu"], lat: 30.57, lon: 104.07 },
  { countries: ["HK"], keys: ["香港", "hong kong", "hongkong", "hkg", "hk-"], lat: 22.32, lon: 114.17 },
  { countries: ["TW"], keys: ["台北", "臺北", "taipei", "tpe", "tw-"], lat: 25.03, lon: 121.57 },
  { countries: ["TW"], keys: ["高雄", "kaohsiung"], lat: 22.63, lon: 120.30 },
  // East Asia
  { countries: ["JP"], keys: ["东京", "東京", "tokyo", "nrt", "hnd", "tyo", "jp-"], lat: 35.68, lon: 139.69 },
  { countries: ["JP"], keys: ["大阪", "osaka", "kix"], lat: 34.69, lon: 135.50 },
  { countries: ["KR"], keys: ["首尔", "seoul", "icn", "sel", "kr-"], lat: 37.57, lon: 126.98 },
  { countries: ["KR"], keys: ["釜山", "busan"], lat: 35.18, lon: 129.08 },
  // SE & South Asia
  { countries: ["SG"], keys: ["新加坡", "singapore", "sin", "sg-"], lat: 1.35, lon: 103.82 },
  { countries: ["MY"], keys: ["吉隆坡", "kuala lumpur", "kul", "my-"], lat: 3.14, lon: 101.69 },
  { countries: ["TH"], keys: ["曼谷", "bangkok", "bkk"], lat: 13.76, lon: 100.50 },
  { countries: ["VN"], keys: ["胡志明", "ho chi minh", "sgn"], lat: 10.82, lon: 106.63 },
  { countries: ["VN"], keys: ["河内", "hanoi", "han"], lat: 21.03, lon: 105.85 },
  { countries: ["ID"], keys: ["雅加达", "jakarta", "cgk"], lat: -6.21, lon: 106.85 },
  { countries: ["PH"], keys: ["马尼拉", "manila", "mnl"], lat: 14.60, lon: 120.98 },
  { countries: ["IN"], keys: ["孟买", "mumbai", "bom", "in-"], lat: 19.08, lon: 72.88 },
  { countries: ["IN"], keys: ["新德里", "delhi", "del"], lat: 28.61, lon: 77.21 },
  { countries: ["IN"], keys: ["班加罗尔", "bangalore", "blr"], lat: 12.97, lon: 77.59 },
  // Middle East
  { countries: ["AE"], keys: ["迪拜", "dubai", "dxb", "ae-"], lat: 25.20, lon: 55.27 },
  { countries: ["SA"], keys: ["利雅得", "riyadh"], lat: 24.71, lon: 46.68 },
  { countries: ["IL"], keys: ["特拉维夫", "tel aviv", "tlv"], lat: 32.09, lon: 34.78 },
  { countries: ["TR"], keys: ["伊斯坦布尔", "istanbul", "ist"], lat: 41.01, lon: 28.98 },
  // Oceania
  { countries: ["AU"], keys: ["悉尼", "sydney", "syd", "au-"], lat: -33.87, lon: 151.21 },
  { countries: ["AU"], keys: ["墨尔本", "melbourne", "mel"], lat: -37.81, lon: 144.96 },
  { countries: ["NZ"], keys: ["奥克兰", "auckland", "akl"], lat: -36.85, lon: 174.76 },
  // Europe
  { countries: ["DE"], keys: ["法兰克福", "frankfurt", "fra", "de-"], lat: 50.11, lon: 8.68 },
  { countries: ["DE"], keys: ["慕尼黑", "munich", "muc"], lat: 48.14, lon: 11.58 },
  { countries: ["DE"], keys: ["柏林", "berlin", "ber"], lat: 52.52, lon: 13.40 },
  { countries: ["GB", "UK"], keys: ["伦敦", "london", "lhr", "lon", "uk-", "gb-"], lat: 51.51, lon: -0.13 },
  { countries: ["NL"], keys: ["阿姆斯特丹", "amsterdam", "ams", "nl-"], lat: 52.37, lon: 4.90 },
  { countries: ["FR"], keys: ["巴黎", "paris", "cdg", "par", "fr-"], lat: 48.86, lon: 2.35 },
  { countries: ["IE"], keys: ["都柏林", "dublin", "dub"], lat: 53.35, lon: -6.26 },
  { countries: ["CH"], keys: ["苏黎世", "zurich", "zrh"], lat: 47.37, lon: 8.54 },
  { countries: ["SE"], keys: ["斯德哥尔摩", "stockholm", "arn"], lat: 59.33, lon: 18.07 },
  { countries: ["FI"], keys: ["赫尔辛基", "helsinki", "hel"], lat: 60.17, lon: 24.94 },
  { countries: ["ES"], keys: ["马德里", "madrid", "mad"], lat: 40.42, lon: -3.70 },
  { countries: ["IT"], keys: ["米兰", "milan", "mxp"], lat: 45.46, lon: 9.19 },
  { countries: ["RU"], keys: ["莫斯科", "moscow", "mow"], lat: 55.76, lon: 37.62 },
  // North America
  { countries: ["US"], keys: ["圣何塞", "san jose", "sjc", "硅谷", "us-10"], lat: 37.34, lon: -121.89 },
  { countries: ["US"], keys: ["旧金山", "san francisco", "sfo"], lat: 37.77, lon: -122.42 },
  { countries: ["US"], keys: ["洛杉矶", "los angeles", "lax", "us-04"], lat: 34.05, lon: -118.24 },
  { countries: ["US"], keys: ["西雅图", "seattle", "sea"], lat: 47.61, lon: -122.33 },
  { countries: ["US"], keys: ["达拉斯", "dallas", "dfw"], lat: 32.78, lon: -96.80 },
  { countries: ["US"], keys: ["芝加哥", "chicago", "ord"], lat: 41.88, lon: -87.63 },
  { countries: ["US"], keys: ["纽约", "new york", "nyc", "jfk", "ewr"], lat: 40.71, lon: -74.01 },
  { countries: ["US"], keys: ["阿什本", "ashburn", "iad", "弗吉尼亚"], lat: 39.04, lon: -77.49 },
  { countries: ["US"], keys: ["迈阿密", "miami", "mia"], lat: 25.76, lon: -80.19 },
  { countries: ["CA"], keys: ["多伦多", "toronto", "yyz", "ca-"], lat: 43.65, lon: -79.38 },
  { countries: ["CA"], keys: ["温哥华", "vancouver", "yvr"], lat: 49.28, lon: -123.12 },
  // South America & Africa
  { countries: ["BR"], keys: ["圣保罗", "sao paulo", "gru", "br-"], lat: -23.55, lon: -46.63 },
  { countries: ["AR"], keys: ["布宜诺斯艾利斯", "buenos aires", "eze"], lat: -34.60, lon: -58.38 },
  { countries: ["ZA"], keys: ["约翰内斯堡", "johannesburg", "jnb", "za-"], lat: -26.20, lon: 28.05 },
  { countries: ["ZA"], keys: ["开普敦", "cape town", "cpt"], lat: -33.92, lon: 18.42 },
]);

const REGION_CENTROIDS = Object.freeze({
  CN: [31.23, 121.47],
  HK: [22.32, 114.17],
  MO: [22.20, 113.54],
  TW: [25.03, 121.57],
  JP: [35.68, 139.69],
  KR: [37.57, 126.98],
  SG: [1.35, 103.82],
  MY: [3.14, 101.69],
  TH: [13.76, 100.50],
  VN: [10.82, 106.63],
  ID: [-6.21, 106.85],
  PH: [14.60, 120.98],
  IN: [19.08, 72.88],
  AE: [25.20, 55.27],
  SA: [24.71, 46.68],
  IL: [32.09, 34.78],
  TR: [41.01, 28.98],
  RU: [55.76, 37.62],
  GB: [51.51, -0.13],
  UK: [51.51, -0.13],
  DE: [50.11, 8.68],
  FR: [48.86, 2.35],
  NL: [52.37, 4.90],
  IE: [53.35, -6.26],
  CH: [47.37, 8.54],
  SE: [59.33, 18.07],
  FI: [60.17, 24.94],
  ES: [40.42, -3.70],
  IT: [45.46, 9.19],
  PL: [52.23, 21.01],
  US: [37.34, -121.89],
  CA: [43.65, -79.38],
  MX: [19.43, -99.13],
  BR: [-23.55, -46.63],
  AR: [-34.60, -58.38],
  CL: [-33.45, -70.67],
  AU: [-33.87, 151.21],
  NZ: [-36.85, 174.76],
  ZA: [-26.20, 28.05],
  EG: [30.04, 31.24],
  NG: [6.52, 3.38],
  KE: [-1.29, 36.82],
});

const GEO_REGEX_CACHE = new Map();
const escapeRegExp = (str) => str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

export function matchesGeoKeyword(haystack, kw) {
  const source = String(haystack || "");
  const needle = String(kw || "");
  if (!needle) return false;
  if (/[^\x00-\x7f]/.test(needle)) {
    return source.toLowerCase().includes(needle.toLowerCase());
  }
  let rx = GEO_REGEX_CACHE.get(needle);
  if (!rx) {
    const escaped = escapeRegExp(needle);
    rx = needle.endsWith("-")
      ? new RegExp(`(?:^|[^a-z0-9])${escaped}[a-z0-9]`, "i")
      : new RegExp(`(?:^|[^a-z0-9])${escaped}(?:$|[^a-z0-9])`, "i");
    GEO_REGEX_CACHE.set(needle, rx);
  }
  return rx.test(source);
}

const normCountryCode = (code) => {
  const c = String(code || "").trim().toUpperCase();
  return c === "UK" ? "GB" : c;
};

/**
 * Resolves a geographic point (projected onto the 1160x560 Pacific map) for an agent node.
 * Respects `regionCode` country boundaries when matching city/IATA keywords so short tokens
 * never misclassify nodes across countries, falling back to global keyword search only when
 * `regionCode` is absent or unmapped.
 */
export function resolveNodeGeoPoint(node, fallbackIndex = 0) {
  const haystack = `${node?.name || ""} ${node?.id || ""}`;
  const rawRC = String(node?.regionCode || node?.region_code || "").trim().toUpperCase();
  const normRC = normCountryCode(rawRC);

  if (normRC) {
    for (const hub of HUB_GEO_KEYWORDS) {
      if (!hub.countries.some((c) => normCountryCode(c) === normRC)) continue;
      if (hub.keys.some((kw) => matchesGeoKeyword(haystack, kw))) {
        const pt = projectPacificGeo(hub.lat, hub.lon);
        return { x: pt.x, y: pt.y, matched: true, source: "city", label: hub.keys[0], country: hub.countries[0] };
      }
    }
    const centroid = REGION_CENTROIDS[rawRC] || REGION_CENTROIDS[normRC];
    if (centroid) {
      const region = geoRegionDetails(rawRC);
      const pt = projectPacificGeo(centroid[0], centroid[1]);
      return { x: pt.x, y: pt.y, matched: true, source: "country", label: region?.name || rawRC, country: normRC };
    }
  }

  for (const hub of HUB_GEO_KEYWORDS) {
    if (hub.keys.some((kw) => matchesGeoKeyword(haystack, kw))) {
      const pt = projectPacificGeo(hub.lat, hub.lon);
      return { x: pt.x, y: pt.y, matched: true, source: "city", label: hub.keys[0], country: hub.countries[0] };
    }
  }

  const region = rawRC ? geoRegionDetails(rawRC) : null;
  const col = fallbackIndex % 3;
  const row = Math.floor(fallbackIndex / 3);
  return {
    x: 136 + col * 48,
    y: 462 + row * 28,
    matched: false,
    source: "fallback",
    label: region?.name || "待定位",
    country: normRC,
  };
}

/**
 * Formats a concise map/pill label for an agent node, converting noisy machine hostnames
 * (e.g. `berlin-01` or `qch-tools-installed-a`) into readable Chinese city/country labels.
 */
export function formatMapCityLabel(name, node = null) {
  const raw = String(name || "").trim();
  if (!raw && !node) return "-";
  const baseName = raw || String(node?.name || node?.id || "");
  const parts = baseName.split(/\s*[·|]\s*/);
  if (parts.length > 1) return parts[0].trim();
  if (/[\u4e00-\u9fff]/.test(baseName)) return baseName;

  if (node) {
    const geo = resolveNodeGeoPoint(node, 0);
    if (geo.source === "city" && /[\u4e00-\u9fff]/.test(geo.label)) {
      return geo.label;
    }
    const rc = String(node.regionCode || node.region_code || "").trim().toUpperCase();
    const region = rc ? geoRegionDetails(rc) : null;
    if (region?.name) {
      const suffixMatch = baseName.match(/(?:^qch-tools-installed|[-_])([a-z0-9]{1,3})$/i);
      const suffix = suffixMatch ? `-${suffixMatch[1]}` : "";
      return `${region.name}${suffix}`;
    }
  }

  return baseName.replace(/^qch-tools-installed-/, "节点-");
}
