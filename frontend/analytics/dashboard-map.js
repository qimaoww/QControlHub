import { geoRegionDetails } from "../modules/region-model.js";
import { createSVGEl } from "./dashboard-panel.js";
import { PACIFIC_GRATICULES, PACIFIC_LAND_PATH, PACIFIC_MAP_BOUNDS } from "./pacific-map-data.js";
import {
  BAD_COLOR,
  BORDER_COLOR,
  IDLE_FLOW_COLOR,
  INK_COLOR,
  OK_COLOR,
  RUNNING_COLOR,
  SOFT_INK_COLOR,
  WARN_COLOR,
  buildProbePillLabel,
  buildProbeShortAlertLabel,
  buildTopologySelectionIndex,
  clipLabel,
  clusterTopologyNodes,
  flowSpeedClass,
  formatLoss,
  formatMsCompact,
  isProbeRunning,
  isProbeUnreachable,
  placeCollisionFreePill,
  quadPointAt,
  rectsOverlap,
  resolveNodeGeoPoint,
  selectTopAmbientBadProbeIDs,
  tonePalette,
  visualUnits,
} from "./dashboard-topo-shared.js";

export const WORLD_SPAN = PACIFIC_MAP_BOUNDS.width - PACIFIC_MAP_BOUNDS.padX * 2;
export const MAX_MAP_SCALE = 6.0;

const WIDE_ASCII_RE = /^[MWGOQDHNUCSKRBAZ%@]$/;
const NARROW_ASCII_RE = /^[./\s,|:\-·•()[\]]$/;

/** Estimates rendered SVG pixel width for bold Noto Sans SC + tabular-nums labels. */
function estimateSvgTextPx(text, fontSizePx = 11) {
  let px = 0;
  for (const ch of String(text || "")) {
    if (ch.charCodeAt(0) > 255) {
      px += fontSizePx * 1.04;
    } else if (WIDE_ASCII_RE.test(ch)) {
      px += fontSizePx * 0.72;
    } else if (NARROW_ASCII_RE.test(ch)) {
      px += fontSizePx * 0.40;
    } else {
      px += fontSizePx * 0.62;
    }
  }
  return px;
}

function clipTextToPx(text, fontSizePx, maxPx) {
  const s = String(text || "");
  if (estimateSvgTextPx(s, fontSizePx) <= maxPx) return s;
  const ellW = estimateSvgTextPx("…", fontSizePx);
  let acc = "";
  let w = 0;
  for (const ch of s) {
    const cw = estimateSvgTextPx(ch, fontSizePx);
    if (w + cw + ellW > maxPx) return acc ? `${acc}…` : "…";
    acc += ch;
    w += cw;
  }
  return acc || s;
}

export function getMapWrapSpan(width = PACIFIC_MAP_BOUNDS.width, scale = 1) {
  const vw = Math.max(100, Number(width) || PACIFIC_MAP_BOUNDS.width);
  return WORLD_SPAN * (vw / PACIFIC_MAP_BOUNDS.width) * (Number(scale) || 1);
}

export function getMapViewportDimensions({ width = PACIFIC_MAP_BOUNDS.width, isMobile = false } = {}) {
  if (!isMobile) {
    return { width: PACIFIC_MAP_BOUNDS.width, mapHeight: PACIFIC_MAP_BOUNDS.height };
  }
  const w = Math.max(300, Math.min(620, Math.round(Number(width) || 360)));
  const mapHeight = Math.round(Math.max(216, Math.min(276, w * 0.69)));
  return { width: w, mapHeight };
}

/**
 * Clamps map viewport transform: horizontally wraps seamlessly across 360deg WORLD_SPAN,
 * and vertically enforces strict top/bottom boundaries so poles never leave the viewport.
 */
export function clampMapZoomTransform(z, width = PACIFIC_MAP_BOUNDS.width, height = PACIFIC_MAP_BOUNDS.height, maxScale = MAX_MAP_SCALE) {
  const vw = Math.max(100, Number(width) || PACIFIC_MAP_BOUNDS.width);
  const vh = Math.max(100, Number(height) || PACIFIC_MAP_BOUNDS.height);
  const scale = Math.max(1, Math.min(maxScale, Number(z?.scale) || 1));
  const wrapSpan = getMapWrapSpan(vw, scale);
  const rawTx = Number(z?.tx) || 0;
  let tx = 0;
  if (wrapSpan > 0) {
    const mod = ((rawTx % wrapSpan) + wrapSpan) % wrapSpan;
    tx = Math.abs(mod) < 0.001 || Math.abs(mod - wrapSpan) < 0.001 ? 0 : mod - wrapSpan;
  }
  const minTy = vh * (1 - scale);
  const ty = scale <= 1.001 ? 0 : Math.max(minTy, Math.min(0, Number(z?.ty) || 0));
  return { scale, tx, ty };
}

function getSafeViewportBox(vw, vh, { isMobile = false, hasChainTotal = false, hasExternal = false } = {}) {
  if (isMobile) {
    const left = 26;
    const right = Math.max(left + 80, vw - 26);
    const top = 30;
    const bottom = Math.max(top + 80, vh - (hasChainTotal ? 34 : 26) - (hasExternal ? 6 : 0));
    return {
      left,
      right,
      top,
      bottom,
      width: right - left,
      height: bottom - top,
      cx: (left + right) / 2,
      cy: (top + bottom) / 2,
    };
  }
  const left = 68;
  const right = Math.max(left + 200, vw - 76);
  const top = 42;
  const bottom = Math.max(top + 140, vh - (hasChainTotal ? 58 : 40) - (hasExternal ? 10 : 0));
  return {
    left,
    right,
    top,
    bottom,
    width: right - left,
    height: bottom - top,
    cx: (left + right) / 2,
    cy: (top + bottom) / 2,
  };
}

function circularHorizontalSpan(pts, baseWrapSpan) {
  const sortedX = [...pts].map((p) => p.x).sort((a, b) => a - b);
  let maxGap = -1;
  let maxGapIdx = 0;
  for (let i = 0; i < sortedX.length; i++) {
    const next = i + 1 < sortedX.length ? sortedX[i + 1] : sortedX[0] + baseWrapSpan;
    const gap = next - sortedX[i];
    if (gap > maxGap) {
      maxGap = gap;
      maxGapIdx = i;
    }
  }
  const arcStart = maxGapIdx + 1 < sortedX.length ? sortedX[maxGapIdx + 1] : sortedX[0];
  const dx = Math.max(0, baseWrapSpan - maxGap);
  const cx = arcStart + dx / 2;

  let minY = Infinity;
  let maxY = -Infinity;
  for (const p of pts) {
    if (p.y < minY) minY = p.y;
    if (p.y > maxY) maxY = p.y;
  }
  return {
    dx,
    cx,
    dy: Math.max(0, maxY - minY),
    cy: (minY + maxY) / 2,
  };
}

/**
 * Computes an automatic centered focus zoom transform for any map view (single group or filtered domain).
 * Deduces a safe viewport frame excluding HUD, top-right controls, bottom-left total badge, and bottom-right legend,
 * and adds label padding around peripheral nodes so labels stay cleanly inside the visible stage.
 */
export function computeMapFocusZoom(model, { width = PACIFIC_MAP_BOUNDS.width, height, mapHeight, isMobile = false } = {}) {
  const vw = Math.max(100, Number(width) || PACIFIC_MAP_BOUNDS.width);
  const vh = Math.max(100, Number(height ?? mapHeight) || PACIFIC_MAP_BOUNDS.height);
  if (!model || !Array.isArray(model.nodes) || !model.nodes.length) {
    return { scale: 1, tx: 0, ty: 0 };
  }
  const baseScaleX = vw / PACIFIC_MAP_BOUNDS.width;
  const baseScaleY = vh / PACIFIC_MAP_BOUNDS.height;
  const baseWrapSpan = WORLD_SPAN * baseScaleX;
  let unmatchedIdx = 0;
  const pts = model.nodes.map((n) => {
    const pre = resolveNodeGeoPoint(n, 0);
    const geo = pre.matched ? pre : resolveNodeGeoPoint(n, unmatchedIdx++);
    const bx = geo.x * baseScaleX;
    const modX = ((bx % baseWrapSpan) + baseWrapSpan) % baseWrapSpan;
    return { id: n.id, x: modX, y: geo.y * baseScaleY };
  });

  const safe = getSafeViewportBox(vw, vh, {
    isMobile,
    hasChainTotal: Boolean(model.singleGroup),
    hasExternal: Boolean(model.externalTargets?.length),
  });
  const maxAutoScale = isMobile ? 3.1 : 3.6;

  if (pts.length === 1) {
    const p = pts[0];
    const scale = isMobile ? 2.2 : 2.5;
    const targetY = model.externalTargets?.length ? (safe.top + safe.height * 0.43) : safe.cy;
    return clampMapZoomTransform({
      scale,
      tx: safe.cx - p.x * scale,
      ty: targetY - p.y * scale,
    }, vw, vh, MAX_MAP_SCALE);
  }

  const { dx, cx, dy, cy } = circularHorizontalSpan(pts, baseWrapSpan);

  const padX = isMobile ? (vw * 0.19 + 22) : (vw * 0.15 + 46);
  const padY = isMobile ? (vh * 0.19 + 16) : (vh * 0.16 + 28);
  const spanX = Math.max(safe.width * 0.14, dx + padX);
  const spanY = Math.max(safe.height * 0.16, dy + padY);
  const fitScale = Math.min(safe.width / spanX, safe.height / spanY);
  const scale = Math.max(1, Math.min(maxAutoScale, Math.round(fitScale * 100) / 100));

  if (scale <= 1.05 && dx > baseWrapSpan * 0.68) {
    return { scale: 1, tx: 0, ty: 0 };
  }
  const effScale = scale <= 1.05 ? 1 : scale;
  return clampMapZoomTransform({
    scale: effScale,
    tx: safe.cx - cx * effScale,
    ty: effScale <= 1.001 ? 0 : safe.cy - cy * effScale,
  }, vw, vh, MAX_MAP_SCALE);
}

export const computeGroupMapFocusZoom = computeMapFocusZoom;

/** Checks horizontal-wrapped rectangle overlap against screen/world obstacle boxes. */
function wrapsOverlap(box, occupied, wrapSpan, padX = 3, padY = 2.5) {
  if (!wrapSpan || wrapSpan <= 0) {
    return occupied.some((o) => rectsOverlap(box, o, padX, padY));
  }
  for (const o of occupied) {
    if (o.screenFixed) {
      if (rectsOverlap(box, o, padX, padY)) return true;
      continue;
    }
    if (
      rectsOverlap(box, o, padX, padY)
      || rectsOverlap({ ...box, x: box.x - wrapSpan }, o, padX, padY)
      || rectsOverlap({ ...box, x: box.x + wrapSpan }, o, padX, padY)
    ) {
      return true;
    }
  }
  return false;
}

function countWrappedOverlaps(box, zones, wrapSpan, padX = 1.5, padY = 1.5) {
  let count = 0;
  for (const o of zones) {
    if (o.screenFixed || !wrapSpan || wrapSpan <= 0) {
      if (rectsOverlap(box, o, padX, padY)) count += 1;
      continue;
    }
    if (
      rectsOverlap(box, o, padX, padY)
      || rectsOverlap({ ...box, x: box.x - wrapSpan }, o, padX, padY)
      || rectsOverlap({ ...box, x: box.x + wrapSpan }, o, padX, padY)
    ) {
      count += 1;
    }
  }
  return count;
}

/**
 * Generates midpoint protection zones (at least 14px radius -> 28x28) and uniform curve
 * sampling boxes on t in [0.12, 0.88] so node tags and pills avoid cutting across arcs.
 */
function buildArcAvoidanceZones(arcGeom, probeLinks, {
  midRadius = 14,
  sampleSize = 14,
  sampleStep = 0.08,
  tStart = 0.12,
  tEnd = 0.88,
} = {}) {
  const midZones = [];
  const arcZones = [];
  const midByProbeID = new Map();
  const halfSample = sampleSize / 2;

  for (const probe of (probeLinks || [])) {
    const geom = arcGeom.get(probe.id);
    if (!geom) continue;
    const screenFixed = Boolean(probe.isExternal);
    const midBox = {
      x: geom.mid.x - midRadius,
      y: geom.mid.y - midRadius,
      w: midRadius * 2,
      h: midRadius * 2,
      probeID: probe.id,
      kind: "mid",
      screenFixed,
    };
    midZones.push(midBox);
    midByProbeID.set(probe.id, midBox);

    for (const seg of (geom.segments || [])) {
      for (let t = tStart; t <= tEnd + 1e-6; t += sampleStep) {
        const clampedT = Math.min(tEnd, t);
        const pt = quadPointAt(seg.x1, seg.y1, seg.cx, seg.cy, seg.x2, seg.y2, clampedT);
        arcZones.push({
          x: pt.x - halfSample,
          y: pt.y - halfSample,
          w: sampleSize,
          h: sampleSize,
          probeID: probe.id,
          kind: "arc",
          screenFixed,
        });
      }
    }
  }

  return { midZones, arcZones, midByProbeID };
}

function normalizeAngle(rad) {
  let a = rad % (Math.PI * 2);
  if (a >= Math.PI) a -= Math.PI * 2;
  if (a < -Math.PI) a += Math.PI * 2;
  return a;
}

function angleDiff(a, b) {
  return Math.abs(Math.atan2(Math.sin(a - b), Math.cos(a - b)));
}

function computeClusterEdgeAngles(clusters, probeLinks, arcGeom) {
  const clusterByNodeID = new Map();
  for (const cl of clusters) {
    for (const m of cl.members) {
      clusterByNodeID.set(m.node.id, cl.key);
    }
  }
  const anglesByCluster = new Map();
  for (const cl of clusters) anglesByCluster.set(cl.key, []);

  const pushAngle = (key, dx, dy) => {
    if (!key || !anglesByCluster.has(key)) return;
    if (Math.hypot(dx, dy) < 0.05) return;
    anglesByCluster.get(key).push(normalizeAngle(Math.atan2(dy, dx)));
  };

  for (const probe of (probeLinks || [])) {
    const geom = arcGeom.get(probe.id);
    if (!geom || !geom.segments?.length) continue;
    const firstSeg = geom.segments[0];
    const lastSeg = geom.segments[geom.segments.length - 1];

    const fromKey = clusterByNodeID.get(probe.from);
    if (fromKey) {
      const dx = Math.hypot(firstSeg.cx - firstSeg.x1, firstSeg.cy - firstSeg.y1) > 0.2
        ? firstSeg.cx - firstSeg.x1
        : firstSeg.x2 - firstSeg.x1;
      const dy = Math.hypot(firstSeg.cx - firstSeg.x1, firstSeg.cy - firstSeg.y1) > 0.2
        ? firstSeg.cy - firstSeg.y1
        : firstSeg.y2 - firstSeg.y1;
      pushAngle(fromKey, dx, dy);
    }

    if (!probe.isExternal) {
      const toKey = clusterByNodeID.get(probe.to);
      if (toKey) {
        const dx = Math.hypot(lastSeg.cx - lastSeg.x2, lastSeg.cy - lastSeg.y2) > 0.2
          ? lastSeg.cx - lastSeg.x2
          : lastSeg.x1 - lastSeg.x2;
        const dy = Math.hypot(lastSeg.cx - lastSeg.x2, lastSeg.cy - lastSeg.y2) > 0.2
          ? lastSeg.cy - lastSeg.y2
          : lastSeg.y1 - lastSeg.y2;
        pushAngle(toKey, dx, dy);
      }
    }
  }
  return anglesByCluster;
}

/**
 * Computes tangent-sector-aware, edge-anchored tag candidates ranked away from neighbor
 * clusters and incident link exit vectors.
 */
function buildDensityAwareTagCandidates(cluster, allClusters, boxW, boxH, r, bounds, wrapSpan = 0, edgeAngles = []) {
  const ax = cluster.x;
  const ay = cluster.y;

  let repX = 0;
  let repY = 0;
  for (const other of allClusters) {
    if (other.key === cluster.key) continue;
    const rawDx = ax - other.x;
    const dx = wrapSpan > 0
      ? ((((rawDx % wrapSpan) + wrapSpan * 1.5) % wrapSpan) - wrapSpan * 0.5)
      : rawDx;
    const dy = ay - other.y;
    const dist = Math.hypot(dx, dy);
    if (dist > 0.1 && dist < 165) {
      const weight = 1 / (dist * dist + 90);
      repX += (dx / dist) * weight;
      repY += (dy / dist) * weight;
    }
  }

  if (wrapSpan > 0 && cluster.worldX != null) {
    if (cluster.worldX < 120) repX += 0.025;
    if (wrapSpan - cluster.worldX < 120) repX -= 0.025;
  }
  if (ay - bounds.minY < 62) repY += 0.018;
  if (bounds.maxY - ay < 62) repY -= 0.018;

  const hasRepulsion = (Math.abs(repX) + Math.abs(repY)) > 0.0005;
  const repAngle = hasRepulsion ? Math.atan2(repY, repX) : -Math.PI * 0.25;

  let gapCenter = repAngle;
  if (edgeAngles.length === 1) {
    gapCenter = normalizeAngle(edgeAngles[0] + Math.PI);
  } else if (edgeAngles.length > 1) {
    const sorted = [...edgeAngles].map(normalizeAngle).sort((a, b) => a - b);
    let maxGap = -1;
    let bestStart = sorted[0];
    for (let i = 0; i < sorted.length; i++) {
      const next = i + 1 < sorted.length ? sorted[i + 1] : sorted[0] + Math.PI * 2;
      const gap = next - sorted[i];
      if (gap > maxGap) {
        maxGap = gap;
        bestStart = sorted[i];
      }
    }
    gapCenter = normalizeAngle(bestStart + maxGap / 2);
  }

  const baseAngle = edgeAngles.length ? gapCenter : repAngle;
  const rawAngleList = [
    baseAngle,
    baseAngle + Math.PI / 6,
    baseAngle - Math.PI / 6,
    baseAngle + Math.PI / 3,
    baseAngle - Math.PI / 3,
    baseAngle + Math.PI / 2,
    baseAngle - Math.PI / 2,
    baseAngle + (2 * Math.PI) / 3,
    baseAngle - (2 * Math.PI) / 3,
    baseAngle + (5 * Math.PI) / 6,
    baseAngle - (5 * Math.PI) / 6,
    baseAngle + Math.PI,
    0,
    Math.PI,
    Math.PI / 2,
    -Math.PI / 2,
  ];
  const uniqueAngles = [];
  for (const a of rawAngleList) {
    const norm = normalizeAngle(a);
    if (!uniqueAngles.some((prev) => angleDiff(prev, norm) < 0.14)) {
      uniqueAngles.push(norm);
    }
  }

  const rings = [r + 10, r + 22, r + 37, r + 54];
  const candidates = [];

  for (let ringIdx = 0; ringIdx < rings.length; ringIdx++) {
    const dist = rings[ringIdx];
    for (let dirIdx = 0; dirIdx < uniqueAngles.length; dirIdx++) {
      const theta = uniqueAngles[dirIdx];
      const ux = Math.cos(theta);
      const uy = Math.sin(theta);
      const anchorX = ax + ux * dist;
      const anchorY = ay + uy * dist;

      const horizontalVariants = [];
      if (ux > 0.25) {
        // Rightward sector: anchor left edge outside node and extend right
        horizontalVariants.push({
          bx: Math.max(ax + r + 3.5, anchorX),
          by: anchorY - boxH / 2,
          alignBias: 0,
        });
      } else if (ux < -0.25) {
        // Leftward sector: anchor right edge outside node and extend left
        horizontalVariants.push({
          bx: Math.min(ax - r - 3.5, anchorX) - boxW,
          by: anchorY - boxH / 2,
          alignBias: 0,
        });
      } else {
        // Top/bottom quadrants: generate center, left-aligned, and right-aligned variants
        const baseY = uy < 0 ? anchorY - boxH : anchorY;
        horizontalVariants.push(
          { bx: anchorX - boxW / 2, by: baseY, alignBias: 0 },
          { bx: ax + 2, by: baseY, alignBias: 3.5 },
          { bx: ax - 2 - boxW, by: baseY, alignBias: 3.5 },
        );
      }

      const inViewportX = bounds.minX != null && bounds.maxX != null && ax >= bounds.minX - 18 && ax <= bounds.maxX + 18;
      for (const variant of horizontalVariants) {
        const bx = variant.bx;
        const by = Math.max(bounds.minY, Math.min(bounds.maxY - boxH, variant.by));
        if (inViewportX && bx < bounds.minX) continue;
        if (inViewportX && bx + boxW > bounds.maxX) continue;

        const boxCx = bx + boxW / 2;
        const boxCy = by + boxH / 2;
        const boxTheta = Math.atan2(boxCy - ay, boxCx - ax);

        let edgeSectorPenalty = 0;
        for (const ea of edgeAngles) {
          const d1 = angleDiff(theta, ea);
          const d2 = angleDiff(boxTheta, ea);
          const dMin = Math.min(d1, d2);
          if (dMin < 0.49) {
            edgeSectorPenalty += 135 + (0.49 - dMin) * 160;
          }
        }
        if (edgeAngles.length) {
          edgeSectorPenalty += angleDiff(boxTheta, gapCenter) * 7.5;
        }
        if (hasRepulsion) {
          edgeSectorPenalty += angleDiff(boxTheta, repAngle) * 5.0;
        }

        const nearEdgeX = Math.max(bx, Math.min(bx + boxW, ax));
        const nearEdgeY = Math.max(by, Math.min(by + boxH, ay));
        const edgeDist = Math.hypot(nearEdgeX - ax, nearEdgeY - ay);
        const leader = ringIdx >= 1 && edgeDist > r + 9;

        candidates.push({
          x: bx,
          y: by,
          edgeX: nearEdgeX,
          edgeY: nearEdgeY,
          leader,
          sectorPenalty: ringIdx * 20 + dirIdx * 1.1 + variant.alignBias + edgeSectorPenalty,
        });
      }
    }
  }

  candidates.sort((a, b) => a.sectorPenalty - b.sectorPenalty);
  return candidates;
}

function pickBestTagPlacement(candidates, tagW, tagH, hardOccupied, arcZones, wrapSpan, padX, padY) {
  let bestZeroArc = null;
  let bestFallback = null;
  let bestFallbackScore = Infinity;

  for (const c of candidates) {
    const box = { x: c.x, y: c.y, w: tagW, h: tagH };
    if (wrapsOverlap(box, hardOccupied, wrapSpan, padX, padY)) continue;

    const arcHits = arcZones.length ? countWrappedOverlaps(box, arcZones, wrapSpan, 1.5, 1.5) : 0;
    if (arcHits === 0) {
      if (!bestZeroArc || c.sectorPenalty < bestZeroArc.sectorPenalty) {
        bestZeroArc = { ...c, box };
      }
    } else {
      const score = arcHits * 150 + c.sectorPenalty;
      if (score < bestFallbackScore) {
        bestFallbackScore = score;
        bestFallback = { ...c, box };
      }
    }
  }
  return bestZeroArc || bestFallback;
}

function placeProbePillSmart(geom, pillW, pillH, hardOccupied, midZones, unreachMidZones, arcZones, pillBounds, {
  isAmbient = false,
  leaderSegments = null,
} = {}) {
  const tiers = [
    [...hardOccupied, ...midZones, ...arcZones],
    [...hardOccupied, ...midZones],
    [...hardOccupied, ...unreachMidZones],
    [...hardOccupied],
  ];

  const isBoxClear = (placed, obstacles) => {
    const box = { x: placed.cx - pillW / 2, y: placed.cy - pillH / 2, w: pillW, h: pillH };
    return !obstacles.some((o) => rectsOverlap(box, o, 2.5, 2));
  };

  let chosen = null;
  for (let i = 0; i < tiers.length; i++) {
    const segCopy = leaderSegments ? [...leaderSegments] : undefined;
    const trial = placeCollisionFreePill(
      geom.mid.x,
      geom.mid.y,
      pillW,
      pillH,
      [...tiers[i]],
      pillBounds,
      [geom.mid],
      { leaderSegments: segCopy, reserveW: pillW },
    );
    const mustAvoid = i <= 1 ? [...hardOccupied, ...midZones] : i === 2 ? [...hardOccupied, ...unreachMidZones] : hardOccupied;
    if (isBoxClear(trial, mustAvoid) || i === tiers.length - 1) {
      chosen = trial;
      break;
    }
  }

  if (chosen) {
    const box = { x: chosen.cx - pillW / 2, y: chosen.cy - pillH / 2, w: pillW, h: pillH };
    if (isAmbient) hardOccupied.push(box);
    if (leaderSegments && Math.hypot(chosen.cx - geom.mid.x, chosen.cy - geom.mid.y) > 4) {
      leaderSegments.push({ a: geom.mid, b: { x: chosen.cx, y: chosen.cy } });
    }
  }
  return chosen;
}

/**
 * Computes bounded multi-row dock chip positions inside `[padX, width - padX]`.
 */
function placeExternalDockTargets(targets, dockWidth, padX, startY, { minChipW = 132, maxChipW = 224, chipH = 34, chipGap = 8, rowGap = 8, maxUnits = 20 } = {}) {
  const list = Array.isArray(targets) ? targets : [];
  if (!list.length) return { chips: [], rowCount: 0, bottomY: startY };

  const ranked = [...list].sort((a, b) => (b.linkCount || 0) - (a.linkCount || 0) || String(a.id).localeCompare(String(b.id)));
  const availW = Math.max(minChipW, dockWidth - padX * 2);
  const cols = Math.max(1, Math.min(ranked.length, Math.floor((availW + chipGap) / (minChipW + chipGap))));
  const chipW = Math.min(maxChipW, Math.max(96, Math.floor((availW - chipGap * (cols - 1)) / cols)));
  const rowCount = Math.ceil(ranked.length / cols);
  const chips = [];

  for (let row = 0; row < rowCount; row++) {
    const rowItems = ranked.slice(row * cols, (row + 1) * cols);
    const totalRowW = rowItems.length * chipW + Math.max(0, rowItems.length - 1) * chipGap;
    const rowStartX = Math.max(padX, (dockWidth - totalRowW) / 2);
    const y = startY + row * (chipH + rowGap);
    rowItems.forEach((ext, colIdx) => {
      const x = rowStartX + colIdx * (chipW + chipGap);
      const rawTitle = String(ext.name || ext.address || ext.id || "");
      const subText = ext.address && ext.address !== rawTitle ? ext.address : "";
      const maxTitlePx = Math.max(48, chipW - 18);
      chips.push({
        ext,
        x,
        y,
        w: chipW,
        h: chipH,
        cx: x + chipW / 2,
        cy: y + chipH / 2,
        title: clipTextToPx(rawTitle, 11, maxTitlePx),
        fullTitle: rawTitle,
        subText: clipTextToPx(subText || `${ext.linkCount || 1} 链路`, 10.5, maxTitlePx),
      });
    });
  }

  return {
    chips,
    rowCount,
    bottomY: startY + rowCount * chipH + Math.max(0, rowCount - 1) * rowGap,
  };
}

/**
 * Evaluates continuous shortest geodesic curvature across cylindrical 360deg world space
 * (so Atlantic/Pacific links draw unbroken arcs across the wrapped world tiles).
 */
function computeCorridorLanes(probeLinks, coordByID, chipByID, allBeacons, mapBounds, { wrapSpan = WORLD_SPAN, bowScaleY = 1 } = {}) {
  const pairGroups = new Map();
  probeLinks.forEach((p) => {
    const k = p.isExternal ? `${p.from}=>${p.to}` : [p.from, p.to].sort().join("--");
    if (!pairGroups.has(k)) pairGroups.set(k, []);
    pairGroups.get(k).push(p);
  });

  const cylSpan = Math.max(120, wrapSpan);
  const minY = mapBounds.minY;
  const maxY = mapBounds.maxY;

  const placedMids = [];
  const geomByProbeID = new Map();

  for (const [groupKey, groupProbes] of pairGroups.entries()) {
    const first = groupProbes[0];
    const pA = coordByID.get(first.from);
    if (!pA) continue;

    if (first.isExternal) {
      const chip = chipByID.get(first.to);
      if (!chip) continue;
      const port = { x: chip.cx, y: Math.min(maxY + 12, chip.y) };
      const dx = port.x - pA.x;
      const dy = port.y - pA.y;
      const len = Math.hypot(dx, dy) || 1;
      const nx = -dy / len;
      const ny = dx / len;
      groupProbes.forEach((probe, idx) => {
        const sign = idx === 0 ? 1 : (idx % 2 === 1 ? -1 : 1);
        const mag = 16 + Math.floor((idx + 1) / 2) * 14;
        const cx = (pA.x + port.x) / 2 + nx * mag * sign;
        const cy = Math.max(minY, Math.min(port.y - 6, (pA.y + port.y) / 2 + ny * mag * sign));
        const d = `M ${pA.x.toFixed(1)} ${pA.y.toFixed(1)} Q ${cx.toFixed(1)} ${cy.toFixed(1)}, ${port.x.toFixed(1)} ${port.y.toFixed(1)}`;
        const mid = quadPointAt(pA.x, pA.y, cx, cy, port.x, port.y, 0.5);
        geomByProbeID.set(probe.id, {
          d,
          segments: [{ x1: pA.x, y1: pA.y, cx, cy, x2: port.x, y2: port.y }],
          mid,
          port,
          wrapped: false,
          groupKey,
        });
      });
      continue;
    }

    const pBRaw = coordByID.get(first.to);
    if (!pBRaw) continue;

    const shortestDx = ((((pBRaw.x - pA.x) % cylSpan) + cylSpan * 1.5) % cylSpan) - cylSpan * 0.5;
    const pB = { x: pA.x + shortestDx, y: pBRaw.y };
    const chordDx = pB.x - pA.x;
    const chordDy = pB.y - pA.y;
    const chordLen = Math.hypot(chordDx, chordDy);

    if (chordLen < 12) {
      groupProbes.forEach((probe, idx) => {
        const offset = 18 + idx * 9;
        const cx = pA.x;
        const cy = Math.max(minY, pA.y - offset);
        const d = `M ${(pA.x - 4).toFixed(1)} ${pA.y.toFixed(1)} Q ${cx.toFixed(1)} ${(cy - 10).toFixed(1)}, ${(pA.x + 4).toFixed(1)} ${pA.y.toFixed(1)}`;
        geomByProbeID.set(probe.id, {
          d,
          segments: [{ x1: pA.x - 4, y1: pA.y, cx, cy: cy - 10, x2: pA.x + 4, y2: pA.y }],
          mid: { x: cx, y: cy },
          wrapped: false,
          groupKey,
        });
      });
      continue;
    }

    const ux = chordDx / chordLen;
    const uy = chordDy / chordLen;
    let nx = -uy;
    let ny = ux;
    if (ny > 0.1 || (Math.abs(ny) <= 0.1 && nx > 0)) {
      nx = -nx;
      ny = -ny;
    }

    const baseBow = Math.min(82 * bowScaleY, Math.max(16 * bowScaleY, chordLen * 0.15));
    const scoreCandidate = (sign, mag) => {
      const cx = (pA.x + pB.x) / 2 + nx * sign * mag;
      const cy = Math.max(minY, Math.min(maxY, (pA.y + pB.y) / 2 + ny * sign * mag));
      let penalty = sign < 0 ? 4 : 0;
      for (const sampleT of [0.25, 0.5, 0.75]) {
        const pt = quadPointAt(pA.x, pA.y, cx, cy, pB.x, pB.y, sampleT);
        if (pt.y < minY + 6 || pt.y > maxY - 6) {
          penalty += 55;
        }
        for (const beacon of allBeacons) {
          const bDistA = Math.hypot(beacon.x - pA.x, beacon.y - pA.y);
          const bDistB = Math.hypot(beacon.x - pB.x, beacon.y - pB.y);
          if (bDistA < 3 || bDistB < 3) continue;
          const bdxRaw = pt.x - beacon.x;
          const bdx = Math.abs((((bdxRaw % cylSpan) + cylSpan * 1.5) % cylSpan) - cylSpan * 0.5);
          const bDist = Math.hypot(bdx, pt.y - beacon.y);
          if (bDist < 22) penalty += (22 - bDist) * 2.2;
        }
      }
      const mid = quadPointAt(pA.x, pA.y, cx, cy, pB.x, pB.y, 0.5);
      for (const prevMid of placedMids) {
        const mdxRaw = mid.x - prevMid.x;
        const mdx = Math.abs((((mdxRaw % cylSpan) + cylSpan * 1.5) % cylSpan) - cylSpan * 0.5);
        const mDist = Math.hypot(mdx, mid.y - prevMid.y);
        if (mDist < 24) penalty += (24 - mDist) * 1.4;
      }
      return { cx, cy, mid, penalty };
    };

    const primaryForward = scoreCandidate(1, baseBow);
    const primaryReverse = scoreCandidate(-1, baseBow);
    const dominantSign = primaryForward.penalty <= primaryReverse.penalty ? 1 : -1;

    groupProbes.forEach((probe, idx) => {
      const fromCoord = coordByID.get(probe.from);
      const toCoordRaw = coordByID.get(probe.to);
      if (!fromCoord || !toCoordRaw) return;
      const pShortDx = ((((toCoordRaw.x - fromCoord.x) % cylSpan) + cylSpan * 1.5) % cylSpan) - cylSpan * 0.5;
      const toCoord = { x: fromCoord.x + pShortDx, y: toCoordRaw.y };

      const sign = groupProbes.length === 1
        ? dominantSign
        : (idx % 2 === 0 ? dominantSign : -dominantSign);
      const laneTier = Math.floor(idx / 2);
      const mag = baseBow + laneTier * (18 * bowScaleY);
      const cx = (fromCoord.x + toCoord.x) / 2 + nx * sign * mag;
      const cy = Math.max(minY, Math.min(maxY, (fromCoord.y + toCoord.y) / 2 + ny * sign * mag));
      const d = `M ${fromCoord.x.toFixed(1)} ${fromCoord.y.toFixed(1)} Q ${cx.toFixed(1)} ${cy.toFixed(1)}, ${toCoord.x.toFixed(1)} ${toCoord.y.toFixed(1)}`;
      const mid = quadPointAt(fromCoord.x, fromCoord.y, cx, cy, toCoord.x, toCoord.y, 0.5);
      placedMids.push(mid);
      geomByProbeID.set(probe.id, {
        d,
        segments: [{ x1: fromCoord.x, y1: fromCoord.y, cx, cy, x2: toCoord.x, y2: toCoord.y }],
        mid,
        wrapped: false,
        groupKey,
      });
    });
  }

  return geomByProbeID;
}

/**
 * Fixed ocean fill + dot-grid layer placed beneath the panning world viewport so dragging
 * never reveals empty white margins along viewport edges.
 */
function buildStaticOceanLayer(defs, { width, mapHeight, clipId }) {
  const oceanSig = `${width}:${mapHeight}:${clipId}`;
  const clip = createSVGEl("clipPath", { id: clipId, "data-refresh-key": "pacific-clip", "data-clip-sig": oceanSig });
  clip.append(createSVGEl("rect", { x: 0, y: 0, width, height: mapHeight, rx: 8 }));
  clip.reconcileTable = (fresh) => fresh?.getAttribute?.("data-clip-sig") === clip.getAttribute("data-clip-sig");
  defs.append(clip);

  const patID = `${clipId}-grid`;
  const gridPat = createSVGEl("pattern", {
    id: patID,
    "data-refresh-key": "pacific-grid",
    width: "24",
    height: "24",
    patternUnits: "userSpaceOnUse",
  });
  gridPat.append(createSVGEl("circle", { cx: "1", cy: "1", r: "0.75", fill: "#c8c8d0", opacity: "0.52" }));
  defs.append(gridPat);

  const oceanBase = createSVGEl("g", {
    class: "a-dash-topo-map-ocean",
    "data-refresh-key": "pacific-ocean-base",
    "data-ocean-sig": oceanSig,
  });
  oceanBase.reconcileTable = (fresh) => fresh?.getAttribute?.("data-ocean-sig") === oceanBase.getAttribute("data-ocean-sig");
  oceanBase.append(
    createSVGEl("rect", { x: 0, y: 0, width, height: mapHeight, fill: "#f7f7f9" }),
    createSVGEl("rect", { x: 0, y: 0, width, height: mapHeight, fill: `url(#${patID})` }),
  );
  return oceanBase;
}

/**
 * Paper & Ink Pacific Ocean tiled land & graticule contours (rendered inside panning zoomGroup).
 */
function buildPacificBackdropGroup(defs, { width, mapHeight, clipId, bgTransform = "", strokeComp = 1, isMobile = false }) {
  const backdropSig = `${width}:${mapHeight}:${clipId}:${bgTransform}:${strokeComp.toFixed(2)}:${isMobile ? "m" : "d"}`;
  const backdrop = createSVGEl("g", {
    class: "a-dash-topo-map-backdrop",
    "data-refresh-key": "pacific-backdrop",
    "data-backdrop-sig": backdropSig,
  });
  backdrop.reconcileTable = (fresh) => fresh?.getAttribute?.("data-backdrop-sig") === backdrop.getAttribute("data-backdrop-sig");

  const comp = Math.max(0.3, strokeComp);
  const worldID = `${clipId}-world`;
  const worldG = createSVGEl("g", { id: worldID, "data-refresh-key": "pacific-world" });
  const graticuleG = createSVGEl("g", {
    fill: "none",
    stroke: BORDER_COLOR,
    "stroke-width": (0.7 / comp).toFixed(3),
    "stroke-dasharray": `${(2.5 / comp).toFixed(2)} ${(3.5 / comp).toFixed(2)}`,
  });
  PACIFIC_GRATICULES.meridians.forEach((m) => graticuleG.append(createSVGEl("path", { d: m.d })));
  PACIFIC_GRATICULES.parallels.forEach((p) => graticuleG.append(createSVGEl("path", { d: p.d })));

  const landPath = createSVGEl("path", {
    d: PACIFIC_LAND_PATH,
    fill: "#eaecef",
    stroke: "#d4d4d8",
    "stroke-width": (0.85 / comp).toFixed(3),
    "stroke-linejoin": "round",
  });
  worldG.append(graticuleG, landPath);
  defs.append(worldG);

  const geoLayer = createSVGEl("g", {
    transform: bgTransform || null,
  });
  for (const copyIdx of [-2, -1, 0, 1, 2]) {
    geoLayer.append(createSVGEl("use", {
      href: `#${worldID}`,
      x: String(copyIdx * WORLD_SPAN),
    }));
  }
  backdrop.append(geoLayer);

  if (!isMobile) {
    const ticksG = createSVGEl("g", { stroke: "#d4d4d8", "stroke-width": "0.8" });
    for (let copyIdx = -1; copyIdx <= 1; copyIdx++) {
      for (let tx = 116; tx < WORLD_SPAN; tx += 116) {
        const x = copyIdx * WORLD_SPAN + tx;
        ticksG.append(createSVGEl("line", { x1: x, y1: mapHeight - 4, x2: x, y2: mapHeight }));
      }
    }
    backdrop.append(ticksG);
  }
  return backdrop;
}

function resolveClusterFlagInfo(cluster) {
  for (const m of (cluster?.members || [])) {
    const rawRC = m?.node?.regionCode || m?.node?.region_code || m?.node?.region || m?.geo?.country || "";
    const details = geoRegionDetails(rawRC);
    if (details?.flagCode) return details;
  }
  return null;
}

function appendMapTagFlag(g, flagInfo, x, y, w, h) {
  if (!flagInfo?.flagCode) return;
  const rx = 1.6;
  g.append(
    createSVGEl("rect", {
      x: x.toFixed(1),
      y: y.toFixed(1),
      width: w.toFixed(1),
      height: h.toFixed(1),
      rx: String(rx),
      fill: "#f4f4f5",
    }),
    createSVGEl("image", {
      href: `/api/v1/region-flags/${flagInfo.flagCode.toLowerCase()}`,
      x: x.toFixed(1),
      y: y.toFixed(1),
      width: w.toFixed(1),
      height: h.toFixed(1),
      preserveAspectRatio: "xMidYMid slice",
      class: "a-dash-topo-map-flag",
    }),
    createSVGEl("rect", {
      x: x.toFixed(1),
      y: y.toFixed(1),
      width: w.toFixed(1),
      height: h.toFixed(1),
      rx: String(rx),
      fill: "none",
      stroke: "#d4d4d8",
      "stroke-width": "1",
    }),
  );
}

function resolveMapNodeLabel(cluster, singleGroup = null) {
  if (!cluster?.members?.length) return "-";
  if (!cluster.isCluster) {
    return String(cluster.primary?.node?.name || cluster.label || "-").trim();
  }
  const names = cluster.members.map((m) => String(m?.node?.name || "").trim()).filter(Boolean);
  if (singleGroup && names.length <= 2) {
    return names.join(" / ");
  }
  const primaryName = names[0] || cluster.label;
  return `${primaryName} ×${cluster.members.length}`;
}

function resolveChainDirectionMetric(g, dirKey) {
  const overall = g?.overall?.[dirKey] || null;
  if (overall?.available) return overall;
  const hops = g?.hops || [];
  if (!hops.length) return overall;
  const dirs = hops.map((h) => h?.[dirKey]).filter(Boolean);
  if (dirs.length !== hops.length) return overall;
  const allReached = dirs.every((d) => d.available && d.reached && d.avgMS != null && Number.isFinite(d.avgMS));
  if (allReached) {
    const sumMS = dirs.reduce((s, d) => s + Number(d.avgMS), 0);
    const worstLoss = Math.max(...dirs.map((d) => Number(d.lossPercent || 0)));
    return { available: true, reached: true, avgMS: sumMS, lossPercent: worstLoss, state: "succeeded" };
  }
  return overall;
}

function formatHudDirLatency(dir, running, ready) {
  if (running && (!dir || !dir.available)) return "探测中";
  if (!ready && (!dir || !dir.available)) return "未就绪";
  if (!dir || !dir.available) return "待测";
  if (dir.state === "running" || dir.state === "preparing") return "探测中";
  if (dir.state === "failed" || dir.error) return "失败";
  if (!dir.reached || (dir.lossPercent ?? 0) >= 100) return "不可达";
  return formatMsCompact(dir.avgMS);
}

function summarizeSingleChainTotals(g, dirMode) {
  const ready = g.availability?.ready !== false;
  const running = Boolean(g.running);
  const fwd = resolveChainDirectionMetric(g, "forward");
  const rev = g.bidirectional ? resolveChainDirectionMetric(g, "reverse") : null;
  const worstLoss = g.overall?.worstLossPercent;

  let summaryText = "";
  let compactText = "";
  let showLoss = true;
  if (running && !fwd?.available && !rev?.available) {
    summaryText = "探测中…";
    compactText = "探测中…";
    showLoss = false;
  } else if (!ready && !fwd?.available && !rev?.available) {
    summaryText = g.availability?.reason || "暂不可用";
    compactText = "未就绪";
    showLoss = false;
  } else if (dirMode === "reverse") {
    if (!g.bidirectional) {
      summaryText = "单向无回程";
      compactText = "单向无回程";
      showLoss = false;
    } else {
      const revTxt = formatHudDirLatency(rev, running, ready);
      summaryText = `回 ${revTxt}`;
      compactText = `回${revTxt}`;
    }
  } else if (dirMode === "forward" || !g.bidirectional) {
    const fwdTxt = formatHudDirLatency(fwd, running, ready);
    summaryText = `去 ${fwdTxt}`;
    compactText = `去${fwdTxt}`;
  } else {
    const fwdTxt = formatHudDirLatency(fwd, running, ready);
    const revTxt = formatHudDirLatency(rev, running, ready);
    if (!fwd?.available && !rev?.available) {
      summaryText = fwdTxt;
      compactText = fwdTxt;
      showLoss = false;
    } else if (fwdTxt === "探测中" && revTxt === "探测中") {
      summaryText = "探测中…";
      compactText = "探测中…";
      showLoss = false;
    } else {
      summaryText = `去 ${fwdTxt} / 回 ${revTxt}`;
      compactText = `去${fwdTxt}/回${revTxt}`;
    }
  }

  const displayLoss = dirMode === "forward"
    ? (fwd?.lossPercent ?? worstLoss)
    : dirMode === "reverse"
      ? (rev?.lossPercent ?? worstLoss)
      : worstLoss;
  return { summaryText, compactText, showLoss, displayLoss };
}

function buildChainTotalOverlay(g, dirMode, { mapHeight, compact, occupiedList, maxWidth = null }) {
  if (!g) return null;
  const { summaryText, compactText, showLoss, displayLoss } = summarizeSingleChainTotals(g, dirMode);
  const lossVal = showLoss && displayLoss != null && Number.isFinite(Number(displayLoss)) ? Number(displayLoss) : null;
  const lossBadge = lossVal != null
    ? (lossVal > 0 ? ` · 丢包${compact ? "" : " "}${formatLoss(lossVal)}` : (compact ? "" : " · 0% 丢包"))
    : "";

  const rawTone = g.running ? "running" : (g.overall?.statusBadge?.tone || "");
  const pal = tonePalette(rawTone);

  if (compact) {
    const hopTag = g.isMultiHop ? `${g.hopCount}跳` : "全链";
    const capW = maxWidth != null ? Math.max(94, Math.min(240, maxWidth)) : 228;
    const availTxtPx = capW - 16;
    let rawLabel = `${hopTag} · ${compactText}${lossBadge}`;
    if (estimateSvgTextPx(rawLabel, 9.5) > availTxtPx) {
      const compactLoss = lossVal != null && lossVal > 0 ? ` ${formatLoss(lossVal)}` : "";
      const shortened = compactText.replace(/ms\/回/, "/回");
      const alt = `${hopTag} ${shortened}${compactLoss}`;
      if (estimateSvgTextPx(alt, 9.5) <= availTxtPx) {
        rawLabel = alt;
      } else {
        const ultraShort = `${hopTag} ${shortened.replace(/去|回/g, "")}${compactLoss}`;
        rawLabel = estimateSvgTextPx(ultraShort, 9.5) < estimateSvgTextPx(alt, 9.5) ? ultraShort : alt;
      }
    }
    const label = clipTextToPx(rawLabel, 9.5, availTxtPx);
    const h = 21;
    const x = 8;
    const y = mapHeight - h - 6;
    const w = Math.max(88, Math.min(capW, Math.ceil(estimateSvgTextPx(label, 9.5) + 16)));
    occupiedList.push({ x, y, w, h, screenFixed: true });

    const grp = createSVGEl("g", { class: "a-dash-topo-chain-total", "data-refresh-key": "map-chain-total" });
    grp.append(
      createSVGEl("rect", {
        x: x.toFixed(1), y: y.toFixed(1), width: w.toFixed(1), height: h,
        rx: 5, fill: "rgba(255,255,255,0.95)", stroke: pal.border, "stroke-width": "1",
      }),
      createSVGEl("rect", {
        x: x.toFixed(1), y: y.toFixed(1), width: 3, height: h,
        rx: 1.5, fill: pal.stroke,
      }),
      createSVGEl("text", {
        x: (x + 7.5).toFixed(1),
        y: (y + 13.8).toFixed(1),
        class: "a-dash-topo-chain-total-value is-mobile",
        fill: INK_COLOR,
      }, label),
    );
    return grp;
  }

  const titleLine = g.isMultiHop
    ? `全链路总延迟 · ${g.hopCount} 跳合计`
    : "全链路延迟 · 端到端";
  const capDeskW = 440;
  const valueLine = clipTextToPx(`${summaryText}${lossBadge}`, 12, capDeskW - 24);
  const h = 38;
  const x = 14;
  const y = mapHeight - h - 10;
  const w = Math.max(
    164,
    Math.min(
      capDeskW,
      Math.ceil(Math.max(estimateSvgTextPx(titleLine, 9.5), estimateSvgTextPx(valueLine, 12)) + 24),
    ),
  );
  occupiedList.push({ x, y, w, h, screenFixed: true });

  const grp = createSVGEl("g", { class: "a-dash-topo-chain-total", "data-refresh-key": "map-chain-total" });
  grp.append(
    createSVGEl("rect", {
      x: x.toFixed(1), y: y.toFixed(1), width: w.toFixed(1), height: h,
      rx: 6, fill: "rgba(255,255,255,0.95)", stroke: pal.border, "stroke-width": "1.1",
      class: "a-dash-topo-chain-total-box",
    }),
    createSVGEl("rect", {
      x: x.toFixed(1), y: y.toFixed(1), width: 3.5, height: h,
      rx: 1.75, fill: pal.stroke,
    }),
    createSVGEl("text", {
      x: (x + 11).toFixed(1),
      y: (y + 14.5).toFixed(1),
      class: "a-dash-topo-chain-total-kicker",
      fill: SOFT_INK_COLOR,
    }, titleLine),
    createSVGEl("text", {
      x: (x + 11).toFixed(1),
      y: (y + 30.2).toFixed(1),
      class: "a-dash-topo-chain-total-value",
      fill: INK_COLOR,
    }, valueLine),
  );
  return grp;
}

export function buildMapHudLatencyLine(model, compact = false) {
  const chains = model?.probeChains || [];
  const links = model?.probeLinks || [];
  const routeCount = chains.length || links.length;
  const rtts = links.map((p) => p.latencyMS).filter((v) => typeof v === "number" && Number.isFinite(v) && v >= 0);
  const avgRtt = rtts.length ? (rtts.reduce((s, v) => s + v, 0) / rtts.length) : null;
  const bad = Number(model?.abnormalLinks || 0);
  if (model?.singleGroup) {
    const g = model.singleGroup;
    const dirMode = model?.metricDir === "forward" || model?.metricDir === "reverse" ? model.metricDir : "both";
    const { summaryText, compactText, showLoss, displayLoss } = summarizeSingleChainTotals(g, dirMode);
    const lossText = showLoss && displayLoss != null && Number.isFinite(Number(displayLoss))
      ? ` · ${compact ? "丢包" : "丢包 "}${formatLoss(Number(displayLoss))}`
      : "";
    if (compact) {
      return `${g.nodes.length}站 · ${compactText}${lossText}`;
    }
    const hopLabel = g.isMultiHop ? `${g.hopCount}跳总延迟` : `${g.hopCount}跳`;
    return `${g.groupName} · ${hopLabel} · ${summaryText}${lossText}`;
  }
  if (compact) {
    if (!routeCount) return "暂无延迟监测";
    return bad > 0
      ? `${routeCount} 组 · 告警 ${bad}`
      : avgRtt != null
        ? `${routeCount} 组 · 均 ${avgRtt.toFixed(avgRtt >= 100 ? 0 : 1)}ms`
        : `${routeCount} 组`;
  }
  if (!routeCount) return `在线 ${model?.onlineNodes || 0}/${model?.totalNodes || 0} · 暂无监测组`;
  const parts = [`监测 ${routeCount} 组`, `${links.length} 段`];
  if (avgRtt != null) parts.push(`均 ${avgRtt.toFixed(avgRtt >= 100 ? 0 : 1)}ms`);
  if (bad > 0) parts.push(`告警 ${bad}`);
  else parts.push("全链正常");
  return parts.join(" · ");
}

const clusterHealthTone = (cluster) => {
  const anyIssue = cluster.members.some((m) => !m.node.isOnline || Boolean(m.node.issue));
  const anyOnline = cluster.members.some((m) => m.node.isOnline);
  return anyIssue ? BAD_COLOR : anyOnline ? OK_COLOR : IDLE_FLOW_COLOR;
};

function appendProgressiveWavePair(pulseLayer, segD, {
  pal,
  spd,
  drawStep = 0,
  isAmbient = false,
  isWarn = false,
  isRunning = false,
  peers,
  hl,
  isExternal = false,
  compact = false,
  refreshKey,
}) {
  const stepCls = `is-draw-step-${Math.max(0, Math.min(11, Number(drawStep) || 0))}`;
  const ambCls = isAmbient ? " is-ambient-wave" : "";
  const warnCls = isWarn ? " is-warn" : "";
  const runCls = isRunning ? " is-running" : "";
  const hlCls = hl === true ? " is-highlighted" : "";

  const waveW = compact ? 2.05 : 2.35;
  const tipW = compact ? 2.55 : 2.9;
  const baseWaveOp = isAmbient ? "0.78" : (isWarn ? "0.94" : "0.96");
  const baseTipOp = isAmbient ? "0.86" : "0.98";

  pulseLayer.append(
    createSVGEl("path", {
      d: segD,
      pathLength: "100",
      fill: "none",
      stroke: pal.stroke,
      "stroke-width": waveW.toFixed(2),
      "stroke-dasharray": "104 114",
      "stroke-dashoffset": "0",
      "stroke-linecap": "round",
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": baseWaveOp,
      "data-dim-opacity": "0.14",
      opacity: hl === true ? "1" : (hl === false ? "0.14" : baseWaveOp),
      ...(isExternal ? { "data-ext-probe": "1" } : {}),
      class: `a-dash-topo-arc-wave ${spd} ${stepCls}${ambCls}${warnCls}${runCls}${hlCls}`,
      "data-refresh-key": `${refreshKey}-wave`,
    }),
    createSVGEl("path", {
      d: segD,
      pathLength: "100",
      fill: "none",
      stroke: pal.pulse,
      "stroke-width": tipW.toFixed(2),
      "stroke-dasharray": "6 116",
      "stroke-dashoffset": "-106",
      "stroke-linecap": "round",
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": baseTipOp,
      "data-dim-opacity": "0.16",
      opacity: hl === true ? "1" : (hl === false ? "0.16" : baseTipOp),
      ...(isExternal ? { "data-ext-probe": "1" } : {}),
      class: `a-dash-topo-arc-tip ${spd} ${stepCls}${ambCls}${warnCls}${runCls}${hlCls}`,
      "data-refresh-key": `${refreshKey}-tip`,
    }),
  );
}

function renderClusterTagElement({
  cluster,
  boxX,
  boxY,
  tagW,
  tagH,
  flagInfo,
  flagW,
  flagH,
  displayTag,
  active,
  hl,
  peers,
  compact,
  hoverOnly = false,
  helpers,
}) {
  const key = cluster.key;
  const anyIssue = cluster.members.some((m) => !m.node.isOnline || Boolean(m.node.issue));
  const anyOnline = cluster.members.some((m) => m.node.isOnline);
  const fullTip = cluster.members.map((m) => `${m.node.name}${m.node.issue ? ` (${m.node.issue})` : ""}`).join(" / ");
  const leftPad = compact ? 4.5 : 5.5;
  const flagSlot = flagInfo ? flagW + (compact ? 5 : 6) : 0;

  const baseClass = hoverOnly
    ? "a-dash-topo-node a-dash-topo-map-tag is-hover-only"
    : `a-dash-topo-node a-dash-topo-map-tag ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`;

  const attrs = hoverOnly
    ? {
        class: baseClass,
        "data-refresh-key": `${compact ? "m-" : "map-"}tag-${key}`,
        "data-hover-tag-for": key,
        opacity: "0",
        "aria-hidden": "true",
      }
    : {
        class: baseClass,
        "data-refresh-key": `${compact ? "m-" : "map-"}tag-${key}`,
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "1",
        "data-dim-opacity": "0.32",
        opacity: hl === false ? "0.32" : "1",
        "aria-label": fullTip,
      };

  const g = createSVGEl("g", attrs);
  g.append(
    createSVGEl("rect", {
      x: boxX.toFixed(1),
      y: boxY.toFixed(1),
      width: tagW.toFixed(1),
      height: tagH,
      rx: compact ? 4 : 4.5,
      fill: "#ffffff",
      stroke: active ? INK_COLOR : anyIssue ? BAD_COLOR : anyOnline ? "#d4d4d8" : BORDER_COLOR,
      "stroke-width": active ? (compact ? "1.5" : "1.7") : (compact ? "0.9" : "1"),
      class: "a-dash-topo-card",
    }),
  );

  if (flagInfo) {
    appendMapTagFlag(g, flagInfo, boxX + leftPad, boxY + (tagH - flagH) / 2, flagW, flagH);
  }

  g.append(
    createSVGEl("text", {
      x: (boxX + (compact ? 4.5 : 6.5) + flagSlot).toFixed(1),
      y: (boxY + (compact ? 11.4 : 13.8)).toFixed(1),
      class: "a-dash-topo-map-tag-text",
      fill: anyIssue ? BAD_COLOR : anyOnline ? INK_COLOR : SOFT_INK_COLOR,
    }, displayTag),
  );

  g.append(createSVGEl("title", {}, fullTip));
  if (!hoverOnly) {
    helpers.bindInteractive(g, key);
  }
  return g;
}

function renderClusterBeaconElement({
  cluster,
  r,
  active,
  hl,
  peers,
  singleMode,
  compact,
  runningNodeIDs = null,
  pulseLayer,
  beaconLayer,
  helpers,
}) {
  const key = cluster.key;
  const cx = cluster.x;
  const cy = cluster.y;
  const anyOnline = cluster.members.some((m) => m.node.isOnline);
  const anyRunning = Boolean(runningNodeIDs && cluster.members.some((m) => runningNodeIDs.has(m.node.id)));
  const toneColor = clusterHealthTone(cluster);

  if (anyOnline && helpers.allowMotion()) {
    pulseLayer.append(createSVGEl("circle", {
      cx: cx.toFixed(1),
      cy: cy.toFixed(1),
      r: (r + (compact ? 5.5 : 6.8)).toFixed(1),
      fill: "none",
      stroke: anyRunning ? RUNNING_COLOR : toneColor,
      "stroke-width": compact ? "1" : "1.1",
      class: `a-dash-topo-beacon-pulse${anyRunning ? " is-running" : ""}`,
      "data-refresh-key": `${compact ? "m-" : "map-"}beacon-pulse-${key}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": anyRunning ? "0.62" : "0.42",
      "data-dim-opacity": "0.14",
      opacity: hl === false ? "0.14" : (anyRunning ? "0.62" : "0.42"),
    }));
  }

  const g = createSVGEl("g", {
    class: `a-dash-topo-node a-dash-topo-map-tag ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
    "data-refresh-key": `${compact ? "m-" : "map-"}beacon-${key}`,
    "data-topo-peers": peers.join(" "),
    "data-base-opacity": "1",
    "data-dim-opacity": "0.32",
    opacity: hl === false ? "0.32" : "1",
    "aria-label": cluster.members.map((m) => m.node.name).join(" / "),
  });

  g.append(createSVGEl("circle", {
    cx: cx.toFixed(1),
    cy: cy.toFixed(1),
    r: (r + (compact ? 8 : 7.5)).toFixed(1),
    fill: "transparent",
  }));

  if (singleMode || active || hl === true) {
    g.append(createSVGEl("circle", {
      cx: cx.toFixed(1),
      cy: cy.toFixed(1),
      r: (r + (compact ? 2.8 : 3.4)).toFixed(1),
      fill: "none",
      stroke: active ? INK_COLOR : toneColor,
      "stroke-width": compact ? "0.85" : "0.95",
      "stroke-dasharray": "2 2",
      opacity: "0.65",
    }));
  }

  g.append(createSVGEl("circle", {
    cx: cx.toFixed(1),
    cy: cy.toFixed(1),
    r: r.toFixed(1),
    fill: "#ffffff",
    stroke: active ? INK_COLOR : toneColor,
    "stroke-width": active || hl === true ? (compact ? "2.4" : "2.6") : (compact ? "1.8" : "2.1"),
    class: "a-dash-topo-card",
  }));

  if (cluster.isCluster) {
    g.append(createSVGEl("text", {
      x: cx.toFixed(1),
      y: (cy + (compact ? 3.0 : 3.5)).toFixed(1),
      "text-anchor": "middle",
      class: "a-dash-topo-micro",
      fill: toneColor,
    }, String(cluster.members.length)));
  }

  g.append(createSVGEl("title", {}, cluster.members.map((m) => m.node.name).join(" / ")));
  helpers.bindInteractive(g, key);
  beaconLayer.append(g);
}

function renderLinksAndPills({
  model,
  arcGeom,
  selIndex,
  singleMode,
  dirMode,
  ambientSet,
  staticOccupied,
  midZones,
  unreachMidZones,
  arcZones,
  pillBoundsBase,
  pillLimitX,
  compact,
  defs,
  helpers,
  probeLayer,
  pulseLayer,
  leaderLayer,
  pillLayer,
}) {
  const leaderSegments = [];
  const chainIdxByProbeID = new Map();
  (model.probeChains || []).forEach((ch, cIdx) => {
    (ch.hops || []).forEach((h) => chainIdxByProbeID.set(h.id, cIdx));
  });

  // In single multi-hop view, keep a shared cycle speed across hops of the same chain
  // so sequential relay phases stay locked across infinite repetitions.
  let sharedChainSpeed = "";
  if (singleMode && model.singleGroup?.isMultiHop) {
    const lats = model.probeLinks
      .map((p) => (dirMode === "reverse" && p.bidirectional ? (p.revLatencyMS ?? p.latencyMS) : p.latencyMS))
      .filter((v) => typeof v === "number" && Number.isFinite(v) && v > 0);
    const avgHopLat = lats.length ? (lats.reduce((s, v) => s + v, 0) / lats.length) : null;
    sharedChainSpeed = model.singleGroup.running ? "is-speed-fast" : flowSpeedClass(avgHopLat);
  }

  const motionOn = helpers.allowMotion();
  const defsPrefix = helpers.defsPrefix || "a-topo";

  const renderOrder = [...model.probeLinks].sort((a, b) => {
    const score = { ok: 1, "": 1, idle: 1, running: 2, warn: 2, bad: 3 };
    return (score[a.tone] || 0) - (score[b.tone] || 0);
  });

  renderOrder.forEach((probe) => {
    const geom = arcGeom.get(probe.id);
    if (!geom) return;

    const eKey = `probe:${probe.id}`;
    const peers = selIndex.probePeers(probe);
    const hl = helpers.isHighlightedEdge(peers);
    const probeRunning = isProbeRunning(probe);
    const fullUnreachable = isProbeUnreachable(probe);
    const effectiveTone = probeRunning ? "running" : (probe.tone || "");
    const pal = tonePalette(effectiveTone);
    const isBad = effectiveTone === "bad";
    const isWarn = effectiveTone === "warn";

    const reverseOnly = dirMode === "reverse" && probe.bidirectional && !probe.unavailableReverse;
    const hopCount = Math.max(1, Number(probe.hopCount || 1));
    const hopIndex = Number(probe.hopIndex || 0);
    const hopSeq = reverseOnly ? Math.max(0, hopCount - 1 - hopIndex) : hopIndex;
    const chainIdx = chainIdxByProbeID.get(probe.id) ?? 0;
    const drawStep = singleMode
      ? Math.min(11, hopCount > 6 ? hopSeq : hopSeq * 2)
      : Math.min(11, (chainIdx % 5) + hopSeq);

    const activeLat = reverseOnly ? (probe.revLatencyMS ?? probe.latencyMS) : probe.latencyMS;
    const spd = probeRunning
      ? "is-speed-fast"
      : (sharedChainSpeed || flowSpeedClass(activeLat));

    const canWave = motionOn && !fullUnreachable && !isBad && !probe.unavailableReverse;
    const isAmbientWave = canWave && !singleMode && hl !== true && !probeRunning && !isWarn;

    const baseOpacity = singleMode ? 0.78 : (isBad ? 0.84 : isWarn ? 0.78 : probe.isExternal ? 0.54 : 0.56);
    const opacity = hl === true ? 1 : hl === false ? 0.26 : baseOpacity;

    const g = createSVGEl("g", {
      "data-refresh-key": `${compact ? "m-" : ""}map-arc-${probe.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": String(baseOpacity),
      "data-dim-opacity": "0.26",
      ...(probe.isExternal ? { "data-ext-probe": "1" } : {}),
      opacity: opacity.toFixed(2),
    });

    const strokeW = (
      hl === true
        ? (compact ? 2.4 : 2.6)
        : singleMode
          ? (compact ? 1.85 : 2.0)
          : isBad
            ? (compact ? 1.9 : 2.0)
            : (compact ? 1.4 : 1.55)
    ).toFixed(2);

    const orderedSegments = reverseOnly ? [...geom.segments].reverse() : geom.segments;
    const basePathD = reverseOnly
      ? orderedSegments.map((s) => `M ${s.x2.toFixed(1)} ${s.y2.toFixed(1)} Q ${s.cx.toFixed(1)} ${s.cy.toFixed(1)}, ${s.x1.toFixed(1)} ${s.y1.toFixed(1)}`).join(" ")
      : geom.d;
    const dash = probeRunning ? "2.5 3.5" : isBad ? (compact ? "4 3" : "5 3") : probe.isExternal ? "2 3" : null;
    const dirSuffix = reverseOnly ? "rev" : "fwd";
    const runSuffix = probeRunning ? "run" : "idle";
    const drawKeySuffix = `${probe.id}-${dirSuffix}-${runSuffix}`;

    let maskAttr = null;
    if (dash && motionOn && defs) {
      const maskID = `${defsPrefix}-${compact ? "m-" : ""}arc-mask-${probe.id}`;
      const maskEl = createSVGEl("mask", {
        id: maskID,
        "data-refresh-key": `${compact ? "m-" : ""}map-arc-mask-${drawKeySuffix}`,
        maskUnits: "userSpaceOnUse",
        maskContentUnits: "userSpaceOnUse",
        x: "-3200",
        y: "-400",
        width: "8400",
        height: "2200",
      });
      maskEl.append(createSVGEl("path", {
        d: basePathD,
        pathLength: "100",
        fill: "none",
        stroke: "#ffffff",
        "stroke-width": (Number(strokeW) + 6).toFixed(2),
        "stroke-linecap": "round",
        "stroke-dashoffset": "0",
        class: `a-dash-topo-arc-draw ${spd} is-draw-step-${drawStep}${probeRunning ? " is-running" : ""}`,
        "data-refresh-key": `${compact ? "m-" : ""}map-arc-mask-path-${drawKeySuffix}`,
      }));
      defs.append(maskEl);
      maskAttr = `url(#${maskID})`;
    }

    const railClasses = ["a-dash-topo-arc-rail"];
    if (motionOn) {
      if (!dash) {
        railClasses.push("is-draw-track", spd, `is-draw-step-${drawStep}`);
      }
      if (canWave) railClasses.push("is-flow-active");
      if (isAmbientWave) railClasses.push("is-ambient-track");
      if (probeRunning) railClasses.push("is-running");
    }

    g.append(createSVGEl("path", {
      d: basePathD,
      ...(!dash ? { pathLength: "100", "stroke-dashoffset": "0" } : {}),
      fill: "none",
      stroke: pal.stroke,
      "stroke-width": strokeW,
      "stroke-dasharray": dash,
      "stroke-linecap": "round",
      ...(maskAttr ? { mask: maskAttr } : {}),
      class: railClasses.join(" "),
      "data-refresh-key": `${compact ? "m-" : ""}map-arc-rail-${drawKeySuffix}`,
    }));

    if (canWave) {
      orderedSegments.forEach((seg, segIdx) => {
        const segD = reverseOnly
          ? `M ${seg.x2.toFixed(1)} ${seg.y2.toFixed(1)} Q ${seg.cx.toFixed(1)} ${seg.cy.toFixed(1)}, ${seg.x1.toFixed(1)} ${seg.y1.toFixed(1)}`
          : `M ${seg.x1.toFixed(1)} ${seg.y1.toFixed(1)} Q ${seg.cx.toFixed(1)} ${seg.cy.toFixed(1)}, ${seg.x2.toFixed(1)} ${seg.y2.toFixed(1)}`;
        appendProgressiveWavePair(pulseLayer, segD, {
          pal,
          spd,
          drawStep,
          isAmbient: isAmbientWave,
          isWarn,
          isRunning: probeRunning,
          peers,
          hl,
          isExternal: probe.isExternal,
          compact,
          refreshKey: `${compact ? "m-" : ""}map-wave-${drawKeySuffix}-${segIdx}`,
        });
      });
    }

    const hit = createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: "transparent",
      "stroke-width": compact ? "14" : "12",
      class: "a-dash-topo-probe-item",
      "data-refresh-key": `${compact ? "m-" : ""}map-arc-hit-${probe.id}`,
    });
    helpers.bindInteractive(hit, eKey);
    g.append(hit);
    probeLayer.append(g);

    const isAmbient = ambientSet.has(probe.id);
    const rawPillLabel = !singleMode && compact && isAmbient && hl !== true
      ? buildProbeShortAlertLabel(probe)
      : buildProbePillLabel(probe, true, compact, dirMode);

    const pillFontPx = compact ? 9.2 : 10.5;
    const pillLeftPad = compact ? 13 : 16.5;
    const pillRightPad = compact ? 9 : 11.5;
    const maxPillW = compact ? 184 : 244;
    const pillLabel = clipTextToPx(rawPillLabel, pillFontPx, maxPillW - pillLeftPad - pillRightPad);
    const pillH = compact ? 15.5 : 19.5;
    const rawPillW = Math.ceil(pillLeftPad + estimateSvgTextPx(pillLabel, pillFontPx) + pillRightPad);
    const pillW = Math.max(compact ? 42 : 48, Math.min(maxPillW, rawPillW));

    let pMinX = probe.isExternal ? pillLimitX.min : Math.max(pillLimitX.min, geom.mid.x - (compact ? 170 : 210));
    let pMaxX = probe.isExternal ? pillLimitX.max : Math.min(pillLimitX.max, geom.mid.x + (compact ? 170 : 210));
    if (pMaxX - pMinX < pillW) {
      pMinX = pillLimitX.min;
      pMaxX = pillLimitX.max;
    }
    const pillBounds = { ...pillBoundsBase, minX: pMinX, maxX: pMaxX };

    const placed = placeProbePillSmart(
      geom,
      pillW,
      pillH,
      staticOccupied,
      midZones,
      unreachMidZones,
      arcZones,
      pillBounds,
      { isAmbient, leaderSegments },
    );

    const pillDrawStep = isAmbient && motionOn ? drawStep : null;

    if (placed && (placed.shifted || Math.hypot(placed.cx - geom.mid.x, placed.cy - geom.mid.y) > 15)) {
      helpers.appendProbeLeader(
        leaderLayer,
        { ...placed, shifted: true },
        pal,
        peers,
        hl,
        compact,
        !isAmbient,
        probe.id,
        pillDrawStep,
      );
    }

    const pillEl = helpers.createProbePillGroup({
      probe,
      cx: placed.cx,
      cy: placed.cy,
      pillW,
      fullW: pillW,
      pillH,
      label: pillLabel,
      pal,
      compact,
      peers,
      focusOnly: !isAmbient,
      drawStep: pillDrawStep,
    });
    if (probe.isExternal) pillEl.setAttribute("data-ext-probe", "1");
    pillLayer.append(pillEl);
  });
}

/**
 * Duplicates world-space topology layers at horizontal offsets ±wrapSpan so circular dragging
 * never clips nodes/arcs off one edge before entering cleanly from the opposite edge.
 */
function appendWrapTileCopies(zoomGroup, layerGroups, { wrapSpan, viewportWidth, helpers }) {
  if (!wrapSpan || wrapSpan <= 0) return;
  for (const tileK of [-1, 1]) {
    const shiftX = tileK * wrapSpan;
    const tileG = createSVGEl("g", {
      class: "a-dash-topo-map-wrap-tile",
      "data-refresh-key": `map-tile-${tileK}`,
      "data-map-tile": String(tileK),
      transform: `translate(${shiftX.toFixed(2)} 0)`,
      "aria-hidden": "true",
    });
    for (const srcLayer of layerGroups) {
      if (!srcLayer) continue;
      const clonedLayer = srcLayer.cloneNode(true);
      clonedLayer.removeAttribute("data-refresh-key");
      clonedLayer.querySelectorAll("[data-ext-probe='1']").forEach((extEl) => extEl.remove());
      clonedLayer.querySelectorAll("[data-refresh-key]").forEach((child) => child.removeAttribute("data-refresh-key"));
      clonedLayer.querySelectorAll("[data-topo-entity]").forEach((entityEl) => {
        const key = entityEl.getAttribute("data-topo-entity");
        if (key) {
          helpers.bindInteractive(entityEl, key);
          entityEl.setAttribute("tabindex", "-1");
        }
      });
      if (viewportWidth > 0) {
        // Strip wrapped card/pill/beacon clones that straddle viewport edges (e.g. when wrapSpan <= viewportWidth)
        // so static edge borders never show sliced duplicate tags or half beacons.
        clonedLayer.querySelectorAll(".a-dash-topo-map-tag, .a-dash-topo-probe-item").forEach((cardEl) => {
          const rect = cardEl.querySelector("rect.a-dash-topo-card, rect.a-dash-topo-probe-pill");
          if (rect) {
            const rx = parseFloat(rect.getAttribute("x") || "");
            const rw = parseFloat(rect.getAttribute("width") || "");
            if (!Number.isFinite(rx) || !Number.isFinite(rw)) return;
            const left = rx + shiftX;
            const right = left + rw;
            if ((left < 4 && right > -2) || (left < viewportWidth + 2 && right > viewportWidth - 4)) {
              cardEl.remove();
            }
            return;
          }
          const circle = cardEl.querySelector("circle.a-dash-topo-card");
          if (circle) {
            const cx = parseFloat(circle.getAttribute("cx") || "");
            const cr = parseFloat(circle.getAttribute("r") || "5");
            if (!Number.isFinite(cx)) return;
            const sx = cx + shiftX;
            if ((sx - cr < 4 && sx + cr > -2) || (sx - cr < viewportWidth + 2 && sx + cr > viewportWidth - 4)) {
              cardEl.remove();
            }
          }
        });
      }
      tileG.append(clonedLayer);
    }
    if (viewportWidth > 0) zoomGroup.append(tileG);
  }
}

function renderMobilePacificMap({ width, model, zoomTransform = null, helpers }) {
  const { width: w, mapHeight } = getMapViewportDimensions({ width, isMobile: true });
  const baseScaleX = w / PACIFIC_MAP_BOUNDS.width;
  const baseScaleY = mapHeight / PACIFIC_MAP_BOUNDS.height;
  const z = clampMapZoomTransform(zoomTransform, w, mapHeight, MAX_MAP_SCALE);
  const effScaleX = baseScaleX * z.scale;
  const effScaleY = baseScaleY * z.scale;
  const wrapSpan = getMapWrapSpan(w, z.scale);
  const singleMode = Boolean(model?.singleGroup);
  const dirMode = model?.metricDir === "forward" || model?.metricDir === "reverse" ? model.metricDir : "both";

  const dockArea = placeExternalDockTargets(model.externalTargets, w, 10, mapHeight + 8, {
    minChipW: Math.min(138, Math.floor((w - 28) / 2)),
    maxChipW: 204,
    chipH: 30,
    chipGap: 6,
    rowGap: 6,
    maxUnits: 18,
  });
  const totalHeight = dockArea.rowCount > 0 ? dockArea.bottomY + 10 : mapHeight;

  const svg = createSVGEl("svg", {
    class: "a-dash-topo-svg a-dash-topo-map-svg is-mobile",
    viewBox: `0 0 ${w} ${totalHeight}`,
    role: "img",
    "data-topo-mode": "map",
    "aria-label": "网络监测地理拓扑图",
  });

  const defsPrefix = helpers.defsPrefix || "a-topo";
  const clipId = `${helpers.clipId || (singleMode ? "a-topo-map-clip-single" : "a-topo-map-clip-all")}-mob`;
  const defs = createSVGEl("defs", { "data-refresh-key": `${defsPrefix}-defs` });
  const bgGroupTransform = `translate(${z.tx.toFixed(2)} ${z.ty.toFixed(2)}) scale(${effScaleX.toFixed(4)} ${effScaleY.toFixed(4)})`;
  const oceanBase = buildStaticOceanLayer(defs, { width: w, mapHeight, clipId });
  const backdrop = buildPacificBackdropGroup(defs, {
    width: w,
    mapHeight,
    clipId,
    bgTransform: bgGroupTransform,
    strokeComp: Math.min(6.0, Math.max(0.45, Math.max(effScaleX, effScaleY))),
    isMobile: true,
  });
  svg.append(defs);

  const stageClipGroup = createSVGEl("g", {
    "clip-path": `url(#${clipId})`,
    "data-refresh-key": "pacific-stage-clip",
  });
  const zoomGroup = createSVGEl("g", {
    class: "a-dash-topo-map-viewport",
    "data-refresh-key": "pacific-viewport",
  });

  const clusters = clusterTopologyNodes(model.nodes, {
    radiusPx: 5.0,
    scaleX: effScaleX,
    scaleY: effScaleY,
    offsetX: z.tx,
    offsetY: z.ty,
    worldWrapSpan: wrapSpan,
    viewportWidth: w,
  });
  const selIndex = buildTopologySelectionIndex(model, clusters);
  const coordByID = new Map();
  clusters.forEach((cl) => cl.members.forEach((m) => coordByID.set(m.node.id, { x: cl.x, y: cl.y })));

  const chipByID = new Map();
  dockArea.chips.forEach((chip) => chipByID.set(chip.ext.id, chip));

  const arcGeom = computeCorridorLanes(
    model.probeLinks,
    coordByID,
    chipByID,
    clusters,
    { minX: 8, maxX: w - 8, minY: 30, maxY: mapHeight - 10 },
    { wrapSpan, bowScaleY: baseScaleY },
  );
  const { midZones, arcZones } = buildArcAvoidanceZones(arcGeom, model.probeLinks, {
    midRadius: 14,
    sampleSize: 11,
    sampleStep: 0.09,
  });
  const unreachMidZones = midZones.filter((mz) => {
    const p = model.probeLinks.find((item) => item.id === mz.probeID);
    return p && (isProbeUnreachable(p) || p.tone === "bad");
  });
  const edgeAnglesByCluster = computeClusterEdgeAngles(clusters, model.probeLinks, arcGeom);

  const linkLayer = createSVGEl("g", { class: "a-dash-topo-map-links", "data-refresh-key": "map-layer-links" });
  const pulseLayer = createSVGEl("g", { class: "a-dash-topo-map-pulses", "aria-hidden": "true", "data-refresh-key": "map-layer-pulses" });
  const leaderLayer = createSVGEl("g", { class: "a-dash-topo-map-leaders", "data-refresh-key": "map-layer-leaders" });
  const beaconLayer = createSVGEl("g", { class: "a-dash-topo-map-beacons", "data-refresh-key": "map-layer-beacons" });
  const tagLayer = createSVGEl("g", { class: "a-dash-topo-map-tags", "data-refresh-key": "map-layer-tags" });
  const pillLayer = createSVGEl("g", { class: "a-dash-topo-map-pills", "data-refresh-key": "map-layer-pills" });

  const hudLayer = createSVGEl("g", {
    class: "a-dash-topo-map-hud",
    "clip-path": `url(#${clipId})`,
    "data-refresh-key": "map-layer-hud",
  });

  const ambientSet = singleMode
    ? new Set(model.probeLinks.map((p) => p.id))
    : selectTopAmbientBadProbeIDs(model.probeLinks, { includeExternal: true, maxCount: 4 });
  const totalBadCount = model.probeLinks.filter((p) => p.tone === "bad" || p.tone === "warn").length;
  const shownAlertCount = model.probeLinks.filter((p) => (p.tone === "bad" || p.tone === "warn") && ambientSet.has(p.id)).length;
  const unshownAlertCount = Math.max(0, totalBadCount - shownAlertCount);

  const occupiedTags = [
    { x: 0, y: 0, w, h: 26, screenFixed: true },
    { x: Math.max(0, w - 122), y: 24, w: 118, h: 30, screenFixed: true },
    ...clusters.map((c) => ({ x: c.x - 9, y: c.y - 9, w: 18, h: 18 })),
  ];
  const mobChainTotalBadge = singleMode
    ? buildChainTotalOverlay(model.singleGroup, dirMode, {
        mapHeight,
        compact: true,
        occupiedList: occupiedTags,
        maxWidth: Math.max(120, w - 20),
      })
    : null;

  const tagHardOccupied = [...occupiedTags, ...midZones];

  const sortedForTags = [...clusters].sort((a, b) => {
    const aPinned = helpers.activeKey() === a.key ? 1 : 0;
    const bPinned = helpers.activeKey() === b.key ? 1 : 0;
    if (bPinned !== aPinned) return bPinned - aPinned;
    const aCl = a.isCluster ? 1 : 0;
    const bCl = b.isCluster ? 1 : 0;
    if (bCl !== aCl) return bCl - aCl;
    const aIss = a.members.some((m) => !m.node.isOnline || Boolean(m.node.issue)) ? 1 : 0;
    const bIss = b.members.some((m) => !m.node.isOnline || Boolean(m.node.issue)) ? 1 : 0;
    if (bIss !== aIss) return bIss - aIss;
    return b.members.length - a.members.length;
  });

  const tagBounds = { minX: 8, maxX: w - 8, minY: 27, maxY: mapHeight - 6 };
  const hiddenClusterCount = [];
  const tagSpecs = new Map();

  sortedForTags.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const hl = helpers.isHighlightedEdge(peers);
    const active = helpers.activeKey() === key;
    const rawTagText = resolveMapNodeLabel(cluster, singleMode ? model.singleGroup : null);
    const flagInfo = resolveClusterFlagInfo(cluster);
    const flagW = flagInfo ? 12 : 0;
    const flagH = 8.5;
    const flagSlot = flagInfo ? flagW + 5 : 0;
    const leftPad = 4.5;
    const rightPad = 5.5;
    const maxMobTagW = 156;
    const tagText = clipTextToPx(rawTagText, 9.2, maxMobTagW - leftPad - flagSlot - rightPad);
    const tagW = Math.max(42, Math.min(maxMobTagW, Math.ceil(leftPad + flagSlot + estimateSvgTextPx(tagText, 9.2) + rightPad)));
    const tagH = 16;
    const r = cluster.isCluster ? Math.min(7.5, 4.8 + cluster.members.length * 0.8) : 4.2;
    const candidates = buildDensityAwareTagCandidates(
      cluster,
      clusters,
      tagW,
      tagH,
      r,
      tagBounds,
      wrapSpan,
      edgeAnglesByCluster.get(key) || [],
    );
    tagSpecs.set(key, { cluster, peers, hl, active, flagInfo, flagW, flagH, tagText, tagW, tagH, candidates });

    const chosen = pickBestTagPlacement(candidates, tagW, tagH, tagHardOccupied, arcZones, wrapSpan, 2, 2);
    if (!chosen) {
      hiddenClusterCount.push(cluster);
      return;
    }

    occupiedTags.push(chosen.box);
    tagHardOccupied.push(chosen.box);

    if (chosen.leader) {
      leaderLayer.append(createSVGEl("line", {
        "data-refresh-key": `m-tag-leader-${key}`,
        x1: cluster.x.toFixed(1),
        y1: cluster.y.toFixed(1),
        x2: chosen.edgeX.toFixed(1),
        y2: chosen.edgeY.toFixed(1),
        stroke: "#94a3b8",
        "stroke-width": "0.9",
        "stroke-dasharray": "2 2",
        "stroke-linecap": "round",
        class: "a-dash-topo-tag-leader",
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "0.86",
        "data-dim-opacity": "0.22",
        opacity: hl === false ? "0.22" : "0.86",
      }));
    }

    tagLayer.append(renderClusterTagElement({
      cluster,
      boxX: chosen.x,
      boxY: chosen.y,
      tagW,
      tagH,
      flagInfo,
      flagW,
      flagH,
      displayTag: tagText,
      active,
      hl,
      peers,
      compact: true,
      hoverOnly: false,
      helpers,
    }));
  });

  hiddenClusterCount.forEach((cluster) => {
    const spec = tagSpecs.get(cluster.key);
    if (!spec) return;
    const rawCand = spec.candidates[0] || {
      x: cluster.x - spec.tagW / 2,
      y: cluster.y - spec.tagH - 10,
    };
    const fallbackX = Math.max(tagBounds.minX, Math.min(tagBounds.maxX - spec.tagW, rawCand.x));
    const fallbackY = Math.max(tagBounds.minY, Math.min(tagBounds.maxY - spec.tagH, rawCand.y));
    tagLayer.append(renderClusterTagElement({
      cluster,
      boxX: fallbackX,
      boxY: fallbackY,
      tagW: spec.tagW,
      tagH: spec.tagH,
      flagInfo: spec.flagInfo,
      flagW: spec.flagW,
      flagH: spec.flagH,
      displayTag: spec.tagText,
      active: false,
      hl: null,
      peers: spec.peers,
      compact: true,
      hoverOnly: true,
      helpers,
    }));
  });

  const runningNodeIDs = new Set();
  for (const p of (model.probeLinks || [])) {
    if (isProbeRunning(p)) {
      if (p.from) runningNodeIDs.add(p.from);
      if (p.to && !p.isExternal) runningNodeIDs.add(p.to);
    }
  }

  renderLinksAndPills({
    model,
    arcGeom,
    selIndex,
    singleMode,
    dirMode,
    ambientSet,
    staticOccupied: occupiedTags,
    midZones,
    unreachMidZones,
    arcZones,
    pillBoundsBase: { minY: 28, maxY: mapHeight - 8 },
    pillLimitX: { min: 8, max: w - 8 },
    compact: true,
    defs,
    helpers,
    probeLayer: linkLayer,
    pulseLayer,
    leaderLayer,
    pillLayer,
  });

  clusters.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const active = helpers.activeKey() === key;
    const hl = helpers.isHighlightedEdge(peers);
    const r = cluster.isCluster ? Math.min(7.5, 4.8 + cluster.members.length * 0.8) : 4.2;

    renderClusterBeaconElement({
      cluster,
      r,
      active,
      hl,
      peers,
      singleMode,
      compact: true,
      runningNodeIDs,
      pulseLayer,
      beaconLayer,
      helpers,
    });
  });

  const dockLayer = createSVGEl("g", { class: "a-dash-topo-map-dock", "data-refresh-key": "map-layer-dock" });
  dockArea.chips.forEach((chip) => {
    const extID = chip.ext.id;
    const peers = selIndex.extPeers(extID);
    const hl = helpers.isHighlightedEdge(peers);
    const active = helpers.activeKey() === extID;

    const g = createSVGEl("g", {
      class: `a-dash-topo-dock-chip ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `m-dock-${extID}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      "data-dim-opacity": "0.34",
      opacity: hl === false ? "0.34" : "1",
      "aria-label": `公网目标 ${chip.fullTitle} · ${chip.ext.linkCount || 1} 链路`,
    });
    g.append(
      createSVGEl("rect", {
        x: chip.x.toFixed(1),
        y: chip.y.toFixed(1),
        width: chip.w.toFixed(1),
        height: chip.h,
        rx: 6,
        fill: "#ffffff",
        stroke: active ? INK_COLOR : "#d4d4d8",
        "stroke-width": active ? "1.6" : "1",
        class: "a-dash-topo-card",
      }),
      createSVGEl("circle", {
        cx: chip.cx.toFixed(1),
        cy: chip.y.toFixed(1),
        r: 2.2,
        fill: "#ffffff",
        stroke: SOFT_INK_COLOR,
        "stroke-width": "1.1",
      }),
      createSVGEl("text", { x: (chip.x + 8).toFixed(1), y: (chip.y + 14.5).toFixed(1), class: "a-dash-topo-map-tag-text", fill: INK_COLOR }, chip.title),
      createSVGEl("text", { x: (chip.x + 8).toFixed(1), y: (chip.y + 25.5).toFixed(1), class: "a-dash-topo-micro", fill: SOFT_INK_COLOR }, chip.subText),
    );
    g.append(createSVGEl("title", {}, `${chip.fullTitle} (${chip.ext.linkCount || 1} 链路)`));
    helpers.bindInteractive(g, extID);
    dockLayer.append(g);
  });

  zoomGroup.append(backdrop, linkLayer, pulseLayer, leaderLayer, beaconLayer, tagLayer, pillLayer);
  appendWrapTileCopies(zoomGroup, [linkLayer, pulseLayer, leaderLayer, beaconLayer, tagLayer, pillLayer], {
    wrapSpan,
    viewportWidth: w,
    helpers,
  });
  stageClipGroup.append(oceanBase, zoomGroup);

  const mobLeftText = clipTextToPx(
    singleMode
      ? `${model.onlineNodes}/${model.totalNodes} 在线 · ${model.singleGroup.hopCount}跳`
      : `${model.onlineNodes}/${model.totalNodes} 在线 · ${model.probeChains.length}组`,
    9.5,
    w * 0.56 - 14,
  );
  const mobRightText = clipTextToPx(
    singleMode
      ? (model.singleGroup?.overall?.statusBadge?.label || "拖拽平移")
      : buildMapHudLatencyLine(model, true),
    9.5,
    w * 0.42 - 14,
  );
  hudLayer.append(
    createSVGEl("rect", { x: 0, y: 0, width: w, height: 24, fill: "rgba(255,255,255,0.90)" }),
    createSVGEl("line", { x1: 0, y1: 24, x2: w, y2: 24, stroke: BORDER_COLOR, "stroke-width": "0.8" }),
    createSVGEl("text", { x: 10, y: 15.5, class: "a-dash-topo-map-coord" }, mobLeftText),
    createSVGEl("text", { x: w - 10, y: 15.5, "text-anchor": "end", class: "a-dash-topo-hud-metric", fill: singleMode ? SOFT_INK_COLOR : INK_COLOR }, mobRightText),
  );
  if (mobChainTotalBadge) hudLayer.append(mobChainTotalBadge);

  svg.append(stageClipGroup, dockLayer, hudLayer);
  return svg;
}

/**
 * Renders the 1160px Paper & Ink Pacific Ocean topology SVG (desktop) or compact mobile map SVG.
 * Pure network-monitoring semantics: two-layer health beacons, continuous wrapped arcs & dual tracks,
 * tangent-sector & arc-aware placement, leader-layer callouts, public dock gates, and seamless wrapping.
 */
export function renderPacificMapSVG({ width, isMobile, model, zoomTransform = null, helpers }) {
  if (isMobile) {
    return renderMobilePacificMap({ width, model, zoomTransform, helpers });
  }

  const vw = PACIFIC_MAP_BOUNDS.width;
  const mapHeight = PACIFIC_MAP_BOUNDS.height;
  const singleMode = Boolean(model?.singleGroup);
  const dirMode = model?.metricDir === "forward" || model?.metricDir === "reverse" ? model.metricDir : "both";
  const z = clampMapZoomTransform(zoomTransform, vw, mapHeight, MAX_MAP_SCALE);
  const wrapSpan = getMapWrapSpan(vw, z.scale);

  const dockArea = placeExternalDockTargets(model.externalTargets, vw, 18, mapHeight + 10, {
    minChipW: 136,
    maxChipW: 224,
    chipH: 36,
    chipGap: 8,
    rowGap: 8,
    maxUnits: 21,
  });
  const totalHeight = dockArea.rowCount > 0 ? dockArea.bottomY + 14 : mapHeight;

  const svg = createSVGEl("svg", {
    class: "a-dash-topo-svg a-dash-topo-map-svg",
    viewBox: `0 0 ${vw} ${totalHeight}`,
    role: "img",
    "data-topo-mode": "map",
    "aria-label": "网络监测地理拓扑图",
  });

  const defsPrefix = helpers.defsPrefix || "a-topo";
  const clipId = helpers.clipId || (singleMode ? "a-topo-map-clip-single" : "a-topo-map-clip-all");
  const defs = createSVGEl("defs", { "data-refresh-key": `${defsPrefix}-defs` });
  const hasTransform = Math.abs(z.tx) > 0.05 || Math.abs(z.ty) > 0.05 || z.scale > 1.001;
  const bgTransform = hasTransform
    ? `translate(${z.tx.toFixed(2)} ${z.ty.toFixed(2)}) scale(${z.scale.toFixed(4)})`
    : "";
  const oceanBase = buildStaticOceanLayer(defs, { width: vw, mapHeight, clipId });
  const backdrop = buildPacificBackdropGroup(defs, {
    width: vw,
    mapHeight,
    clipId,
    bgTransform,
    strokeComp: z.scale,
    isMobile: false,
  });
  svg.append(defs);

  const stageClipGroup = createSVGEl("g", {
    "clip-path": `url(#${clipId})`,
    "data-refresh-key": "pacific-stage-clip",
  });
  const zoomGroup = createSVGEl("g", {
    class: "a-dash-topo-map-viewport",
    "data-refresh-key": "pacific-viewport",
  });

  const clusters = clusterTopologyNodes(model.nodes, {
    radiusPx: 4.5,
    scaleX: z.scale,
    scaleY: z.scale,
    offsetX: z.tx,
    offsetY: z.ty,
    worldWrapSpan: wrapSpan,
    viewportWidth: vw,
  });
  const selIndex = buildTopologySelectionIndex(model, clusters);
  const coordByID = new Map();
  clusters.forEach((cl) => cl.members.forEach((m) => coordByID.set(m.node.id, { x: cl.x, y: cl.y })));

  const chipByID = new Map();
  dockArea.chips.forEach((chip) => chipByID.set(chip.ext.id, chip));

  const arcGeom = computeCorridorLanes(
    model.probeLinks,
    coordByID,
    chipByID,
    clusters,
    { minX: 16, maxX: vw - 16, minY: 34, maxY: mapHeight - 16 },
    { wrapSpan, bowScaleY: 1 },
  );
  const { midZones, arcZones } = buildArcAvoidanceZones(arcGeom, model.probeLinks, {
    midRadius: 14,
    sampleSize: 14,
    sampleStep: 0.08,
  });
  const unreachMidZones = midZones.filter((mz) => {
    const p = model.probeLinks.find((item) => item.id === mz.probeID);
    return p && (isProbeUnreachable(p) || p.tone === "bad");
  });
  const edgeAnglesByCluster = computeClusterEdgeAngles(clusters, model.probeLinks, arcGeom);

  const probeLayer = createSVGEl("g", { class: "a-dash-topo-map-probes", "data-refresh-key": "map-layer-probes" });
  const pulseLayer = createSVGEl("g", { class: "a-dash-topo-map-pulses", "aria-hidden": "true", "data-refresh-key": "map-layer-pulses" });
  const leaderLayer = createSVGEl("g", { class: "a-dash-topo-map-leaders", "data-refresh-key": "map-layer-leaders" });
  const beaconLayer = createSVGEl("g", { class: "a-dash-topo-map-beacons", "data-refresh-key": "map-layer-beacons" });
  const tagLayer = createSVGEl("g", { class: "a-dash-topo-map-tags", "data-refresh-key": "map-layer-tags" });
  const pillLayer = createSVGEl("g", { class: "a-dash-topo-map-pills", "data-refresh-key": "map-layer-pills" });

  const hudLayer = createSVGEl("g", {
    class: "a-dash-topo-map-hud",
    "clip-path": `url(#${clipId})`,
    "data-refresh-key": "map-layer-hud",
  });

  const showAllAmbient = singleMode || model.probeLinks.length <= 7;
  const ambientSet = showAllAmbient
    ? new Set(model.probeLinks.map((p) => p.id))
    : selectTopAmbientBadProbeIDs(model.probeLinks, { includeExternal: true, maxCount: 6 });
  const totalBadCount = model.probeLinks.filter((p) => p.tone === "bad" || p.tone === "warn").length;
  const shownAlertCount = model.probeLinks.filter((p) => (p.tone === "bad" || p.tone === "warn") && ambientSet.has(p.id)).length;
  const unshownAlertCount = Math.max(0, totalBadCount - shownAlertCount);

  const staticOccupied = [
    { x: 0, y: 0, w: vw, h: 30, screenFixed: true },
    { x: vw - 220, y: 28, w: 210, h: 36, screenFixed: true },
  ];
  const deskChainTotalBadge = singleMode
    ? buildChainTotalOverlay(model.singleGroup, dirMode, { mapHeight, compact: false, occupiedList: staticOccupied })
    : null;
  if (unshownAlertCount > 0 || clusters.length > 6) {
    staticOccupied.push({ x: 8, y: mapHeight - 28, w: 320, h: 22, screenFixed: true });
  }
  clusters.forEach((c) => {
    const br = c.isCluster ? Math.min(9.5, 5.6 + c.members.length * 0.9) : 5.2;
    staticOccupied.push({ x: c.x - br - 3, y: c.y - br - 3, w: (br + 3) * 2, h: (br + 3) * 2 });
  });

  const tagHardOccupied = [...staticOccupied, ...midZones];

  const tagPriority = [...clusters].sort((a, b) => {
    const aPinned = helpers.activeKey() === a.key ? 1 : 0;
    const bPinned = helpers.activeKey() === b.key ? 1 : 0;
    if (bPinned !== aPinned) return bPinned - aPinned;
    const aIssue = a.members.some((m) => !m.node.isOnline || Boolean(m.node.issue)) ? 1 : 0;
    const bIssue = b.members.some((m) => !m.node.isOnline || Boolean(m.node.issue)) ? 1 : 0;
    if (bIssue !== aIssue) return bIssue - aIssue;
    const aOnline = a.members.some((m) => m.node.isOnline) ? 1 : 0;
    const bOnline = b.members.some((m) => m.node.isOnline) ? 1 : 0;
    if (bOnline !== aOnline) return bOnline - aOnline;
    return b.members.length - a.members.length;
  });

  const tagBounds = { minX: 12, maxX: vw - 12, minY: 32, maxY: mapHeight - 14 };
  const hiddenClusterCount = [];
  const tagSpecs = new Map();

  tagPriority.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const hl = helpers.isHighlightedEdge(peers);
    const active = helpers.activeKey() === key;
    const r = cluster.isCluster ? Math.min(9.0, 5.6 + cluster.members.length * 0.85) : 5.0;

    const baseTagText = resolveMapNodeLabel(cluster, singleMode ? model.singleGroup : null);
    const flagInfo = resolveClusterFlagInfo(cluster);
    const flagW = flagInfo ? 14 : 0;
    const flagH = 10;
    const flagSlot = flagInfo ? flagW + 6 : 0;
    const leftPad = 5.5;
    const rightPad = 7;
    const maxDeskTagW = 198;
    const displayTag = clipTextToPx(baseTagText, 11, maxDeskTagW - leftPad - flagSlot - rightPad);
    const tagW = Math.max(50, Math.min(maxDeskTagW, Math.ceil(leftPad + flagSlot + estimateSvgTextPx(displayTag, 11) + rightPad)));
    const tagH = 20;

    const candidates = buildDensityAwareTagCandidates(
      cluster,
      clusters,
      tagW,
      tagH,
      r,
      tagBounds,
      wrapSpan,
      edgeAnglesByCluster.get(key) || [],
    );
    tagSpecs.set(key, { cluster, peers, hl, active, flagInfo, flagW, flagH, displayTag, tagW, tagH, candidates });

    const chosen = pickBestTagPlacement(candidates, tagW, tagH, tagHardOccupied, arcZones, wrapSpan, 3, 2.5);
    if (!chosen) {
      hiddenClusterCount.push(cluster);
      return;
    }

    staticOccupied.push(chosen.box);
    tagHardOccupied.push(chosen.box);

    if (chosen.leader) {
      leaderLayer.append(createSVGEl("line", {
        "data-refresh-key": `map-tag-leader-${key}`,
        x1: cluster.x.toFixed(1),
        y1: cluster.y.toFixed(1),
        x2: chosen.edgeX.toFixed(1),
        y2: chosen.edgeY.toFixed(1),
        stroke: "#94a3b8",
        "stroke-width": "0.95",
        "stroke-dasharray": "2.2 2.2",
        "stroke-linecap": "round",
        class: "a-dash-topo-tag-leader",
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "0.86",
        "data-dim-opacity": "0.22",
        opacity: hl === false ? "0.22" : "0.86",
      }));
    }

    tagLayer.append(renderClusterTagElement({
      cluster,
      boxX: chosen.x,
      boxY: chosen.y,
      tagW,
      tagH,
      flagInfo,
      flagW,
      flagH,
      displayTag,
      active,
      hl,
      peers,
      compact: false,
      hoverOnly: false,
      helpers,
    }));
  });

  hiddenClusterCount.forEach((cluster) => {
    const spec = tagSpecs.get(cluster.key);
    if (!spec) return;
    const rawCand = spec.candidates[0] || {
      x: cluster.x - spec.tagW / 2,
      y: cluster.y - spec.tagH - 12,
    };
    const fallbackX = Math.max(tagBounds.minX, Math.min(tagBounds.maxX - spec.tagW, rawCand.x));
    const fallbackY = Math.max(tagBounds.minY, Math.min(tagBounds.maxY - spec.tagH, rawCand.y));
    tagLayer.append(renderClusterTagElement({
      cluster,
      boxX: fallbackX,
      boxY: fallbackY,
      tagW: spec.tagW,
      tagH: spec.tagH,
      flagInfo: spec.flagInfo,
      flagW: spec.flagW,
      flagH: spec.flagH,
      displayTag: spec.displayTag,
      active: false,
      hl: null,
      peers: spec.peers,
      compact: false,
      hoverOnly: true,
      helpers,
    }));
  });

  const runningNodeIDs = new Set();
  for (const p of (model.probeLinks || [])) {
    if (isProbeRunning(p)) {
      if (p.from) runningNodeIDs.add(p.from);
      if (p.to && !p.isExternal) runningNodeIDs.add(p.to);
    }
  }

  renderLinksAndPills({
    model,
    arcGeom,
    selIndex,
    singleMode,
    dirMode,
    ambientSet,
    staticOccupied,
    midZones,
    unreachMidZones,
    arcZones,
    pillBoundsBase: { minY: 34, maxY: mapHeight - 14 },
    pillLimitX: { min: 18, max: vw - 18 },
    compact: false,
    defs,
    helpers,
    probeLayer,
    pulseLayer,
    leaderLayer,
    pillLayer,
  });

  clusters.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const hl = helpers.isHighlightedEdge(peers);
    const active = helpers.activeKey() === key;
    const r = cluster.isCluster ? Math.min(9.0, 5.6 + cluster.members.length * 0.85) : 5.0;

    renderClusterBeaconElement({
      cluster,
      r,
      active,
      hl,
      peers,
      singleMode,
      compact: false,
      runningNodeIDs,
      pulseLayer,
      beaconLayer,
      helpers,
    });
  });

  const activeSel = helpers.activeKey();
  const dockLayer = createSVGEl("g", { class: "a-dash-topo-map-dock", "data-refresh-key": "map-layer-dock" });
  if (dockArea.rowCount > 0) {
    dockLayer.append(createSVGEl("line", { x1: 18, y1: mapHeight + 2, x2: vw - 18, y2: mapHeight + 2, stroke: BORDER_COLOR, "stroke-width": "1" }));
    dockArea.chips.forEach((chip) => {
      const extID = chip.ext.id;
      const peers = selIndex.extPeers(extID);
      const hl = helpers.isHighlightedEdge(peers);
      const active = activeSel === extID;

      const g = createSVGEl("g", {
        class: `a-dash-topo-dock-chip ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
        "data-refresh-key": `dock-${extID}`,
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "1",
        "data-dim-opacity": "0.34",
        opacity: hl === false ? "0.34" : "1",
        "aria-label": `公网目标 ${chip.fullTitle}${chip.ext.address ? ` (${chip.ext.address})` : ""} · ${chip.ext.linkCount || 1} 链路`,
      });
      g.append(
        createSVGEl("rect", {
          x: chip.x.toFixed(1),
          y: chip.y.toFixed(1),
          width: chip.w.toFixed(1),
          height: chip.h,
          rx: 7,
          fill: "#ffffff",
          stroke: active ? INK_COLOR : "#d4d4d8",
          "stroke-width": active ? "1.8" : "1",
          class: "a-dash-topo-card",
        }),
        createSVGEl("circle", {
          cx: chip.cx.toFixed(1),
          cy: chip.y.toFixed(1),
          r: 2.5,
          fill: "#ffffff",
          stroke: SOFT_INK_COLOR,
          "stroke-width": "1.2",
        }),
        createSVGEl("text", { x: (chip.x + 10).toFixed(1), y: (chip.y + 15).toFixed(1), class: "a-dash-topo-map-tag-text", fill: INK_COLOR }, chip.title),
        createSVGEl("text", { x: (chip.x + 10).toFixed(1), y: (chip.y + 28).toFixed(1), class: "a-dash-topo-ext-sub", fill: SOFT_INK_COLOR }, chip.subText),
      );
      g.append(createSVGEl("title", {}, `${chip.fullTitle}${chip.ext.address ? ` (${chip.ext.address})` : ""} · ${chip.ext.linkCount || 1} 链路`));
      helpers.bindInteractive(g, extID);
      dockLayer.append(g);
    });
  }

  zoomGroup.append(backdrop, probeLayer, pulseLayer, leaderLayer, beaconLayer, tagLayer, pillLayer);
  appendWrapTileCopies(zoomGroup, [probeLayer, pulseLayer, leaderLayer, beaconLayer, tagLayer, pillLayer], {
    wrapSpan,
    viewportWidth: vw,
    helpers,
  });
  stageClipGroup.append(oceanBase, zoomGroup);

  const deskLeftText = clipTextToPx(
    singleMode
      ? `${model.onlineNodes}/${model.totalNodes} 节点在线 · ${model.singleGroup.hopCount} 跳链路`
      : `${model.onlineNodes}/${model.totalNodes} 节点在线 · ${model.probeChains.length} 监测组`,
    10.5,
    380,
  );
  const deskRightText = clipTextToPx(
    singleMode
      ? "拖拽左右循环 · 双击或 Ctrl+滚轮缩放"
      : buildMapHudLatencyLine(model, false),
    11,
    390,
  );
  hudLayer.append(
    createSVGEl("rect", { x: 0, y: 0, width: vw, height: 28, fill: "rgba(255,255,255,0.92)" }),
    createSVGEl("line", { x1: 0, y1: 28, x2: vw, y2: 28, stroke: BORDER_COLOR, "stroke-width": "0.9" }),
    createSVGEl("text", { x: 16, y: 18, class: "a-dash-topo-map-coord" }, deskLeftText),
    createSVGEl("text", { x: vw - 16, y: 18, "text-anchor": "end", class: singleMode ? "a-dash-topo-map-hint" : "a-dash-topo-hud-metric", fill: singleMode ? SOFT_INK_COLOR : INK_COLOR }, deskRightText),
  );
  if (deskChainTotalBadge) hudLayer.append(deskChainTotalBadge);
  if (unshownAlertCount > 0 || hiddenClusterCount.length > 0) {
    const parts = [];
    if (unshownAlertCount > 0) parts.push(`另有 ${unshownAlertCount} 项告警 · 可切换总览查看全量`);
    if (hiddenClusterCount.length > 0) parts.push(`可放大查看 ${hiddenClusterCount.length} 处密集城市标签`);
    hudLayer.append(createSVGEl("text", {
      x: 16,
      y: mapHeight - 10,
      class: "a-dash-topo-micro",
      fill: unshownAlertCount > 0 ? BAD_COLOR : SOFT_INK_COLOR,
    }, parts.join(" · ")));
  }

  svg.append(stageClipGroup, dockLayer, hudLayer);
  return svg;
}
