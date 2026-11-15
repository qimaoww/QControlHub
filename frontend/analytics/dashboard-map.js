import { geoRegionDetails } from "../modules/region-model.js";
import { createSVGEl } from "./dashboard-panel.js";
import { PACIFIC_GRATICULES, PACIFIC_LAND_PATH, PACIFIC_MAP_BOUNDS } from "./pacific-map-data.js";
import {
  BAD_COLOR,
  BORDER_COLOR,
  IDLE_FLOW_COLOR,
  INK_COLOR,
  OK_COLOR,
  SOFT_INK_COLOR,
  buildProbePillLabel,
  buildProbeShortAlertLabel,
  buildTopologySelectionIndex,
  clipLabel,
  clusterTopologyNodes,
  createTopologyDefs,
  flowSpeedClass,
  formatLoss,
  formatMsCompact,
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

/**
 * Computes an automatic centered focus zoom transform for any map view (single group or filtered domain).
 * Uses circular shortest-arc horizontal span so border regions (Western Europe, Americas, transatlantic)
 * always center cleanly inside the viewport at a comfortable scale.
 */
export function computeMapFocusZoom(model, { width = PACIFIC_MAP_BOUNDS.width, height = PACIFIC_MAP_BOUNDS.height, isMobile = false } = {}) {
  const vw = Math.max(100, Number(width) || PACIFIC_MAP_BOUNDS.width);
  const vh = Math.max(100, Number(height) || PACIFIC_MAP_BOUNDS.height);
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

  const maxAutoScale = isMobile ? 3.1 : 3.6;

  if (pts.length === 1) {
    const p = pts[0];
    const scale = isMobile ? 2.2 : 2.5;
    const targetY = (model.externalTargets?.length ? 0.44 : 0.50) * vh;
    return clampMapZoomTransform({
      scale,
      tx: vw * 0.5 - p.x * scale,
      ty: targetY - p.y * scale,
    }, vw, vh, MAX_MAP_SCALE);
  }

  // Shortest circular arc on [0, baseWrapSpan)
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
  const dy = maxY - minY;
  const cy = (minY + maxY) / 2;

  const padX = vw * (isMobile ? 0.28 : 0.24);
  const padY = vh * (isMobile ? 0.34 : 0.25);
  const spanX = Math.max(vw * 0.14, dx + padX);
  const spanY = Math.max(vh * 0.16, dy + padY);
  const fitScale = Math.min(vw / spanX, vh / spanY);
  const scale = Math.max(1, Math.min(maxAutoScale, Math.round(fitScale * 100) / 100));

  if (scale <= 1.05 && dx > baseWrapSpan * 0.68) {
    return { scale: 1, tx: 0, ty: 0 };
  }
  const effScale = scale <= 1.05 ? 1 : scale;
  const targetY = vh * (isMobile ? 0.49 : 0.50);
  return clampMapZoomTransform({
    scale: effScale,
    tx: vw * 0.5 - cx * effScale,
    ty: effScale <= 1.001 ? 0 : targetY - cy * effScale,
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

/** Computes non-overlapping tag candidates ranked away from neighbor clusters, unconstrained horizontally for world wrapping. */
function buildDensityAwareTagCandidates(cluster, allClusters, boxW, boxH, r, bounds, wrapSpan = 0) {
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

  const baseAngle = (Math.abs(repX) + Math.abs(repY)) > 0.0005
    ? Math.atan2(repY, repX)
    : -Math.PI * 0.25;

  const angleOffsets = [0, Math.PI / 4, -Math.PI / 4, Math.PI / 2, -Math.PI / 2, (3 * Math.PI) / 4, -(3 * Math.PI) / 4, Math.PI];
  const rings = [r + 11, r + 24, r + 40, r + 58];
  const candidates = [];

  for (let ringIdx = 0; ringIdx < rings.length; ringIdx++) {
    const dist = rings[ringIdx];
    for (let dirIdx = 0; dirIdx < angleOffsets.length; dirIdx++) {
      const theta = baseAngle + angleOffsets[dirIdx];
      const ux = Math.cos(theta);
      const uy = Math.sin(theta);
      const cx = ax + ux * (dist + boxW * 0.34);
      const cy = ay + uy * (dist + boxH * 0.32);
      const bx = cx - boxW / 2;
      const by = Math.max(bounds.minY, Math.min(bounds.maxY - boxH, cy - boxH / 2));
      candidates.push({
        x: bx,
        y: by,
        leader: ringIdx >= 2,
        sectorPenalty: ringIdx * 18 + dirIdx * 1.4,
      });
    }
  }
  return candidates;
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
      chips.push({
        ext,
        x,
        y,
        w: chipW,
        h: chipH,
        cx: x + chipW / 2,
        cy: y + chipH / 2,
        title: clipLabel(rawTitle, maxUnits),
        fullTitle: rawTitle,
        subText: subText ? clipLabel(subText, Math.max(12, maxUnits - 2)) : `${ext.linkCount || 1} 链路`,
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
        geomByProbeID.set(probe.id, { d, segments: [{ x1: pA.x, y1: pA.y, cx, cy, x2: port.x, y2: port.y }], mid, wrapped: false });
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
        geomByProbeID.set(probe.id, { d, segments: [{ x1: pA.x - 4, y1: pA.y, cx, cy: cy - 10, x2: pA.x + 4, y2: pA.y }], mid: { x: cx, y: cy }, wrapped: false });
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
  const worldG = createSVGEl("g", { id: worldID });
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
      "stroke-width": "0.6",
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

function buildChainTotalOverlay(g, dirMode, { mapHeight, compact, occupiedList }) {
  if (!g) return null;
  const { summaryText, compactText, showLoss, displayLoss } = summarizeSingleChainTotals(g, dirMode);
  const lossVal = showLoss && displayLoss != null && Number.isFinite(Number(displayLoss)) ? Number(displayLoss) : null;
  const lossBadge = lossVal != null
    ? (lossVal > 0 ? ` · 丢包${compact ? "" : " "}${formatLoss(lossVal)}` : (compact ? "" : " · 0% 丢包"))
    : "";

  const pal = tonePalette(g.overall?.statusBadge?.tone || "");

  if (compact) {
    const hopTag = g.isMultiHop ? `${g.hopCount}跳总计` : "全链";
    const label = `${hopTag} · ${compactText}${lossBadge}`;
    const h = 21;
    const x = 8;
    const y = mapHeight - h - 7;
    const w = Math.max(94, Math.min(238, visualUnits(label) * 5.2 + 16));
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
  const valueLine = `${summaryText}${lossBadge}`;
  const h = 38;
  const x = 14;
  const y = mapHeight - h - 11;
  const w = Math.max(
    158,
    Math.min(
      348,
      Math.max(visualUnits(titleLine) * 5.5, visualUnits(valueLine) * 6.5) + 24,
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
  const defs = createTopologyDefs(defsPrefix);
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
    { minX: 8, maxX: w - 8, minY: 32, maxY: mapHeight - 10 },
    { wrapSpan, bowScaleY: baseScaleY },
  );

  const linkLayer = createSVGEl("g", { class: "a-dash-topo-map-links", "data-refresh-key": "map-layer-links" });
  const pulseLayer = createSVGEl("g", { class: "a-dash-topo-map-pulses", "aria-hidden": "true", "data-refresh-key": "map-layer-pulses" });
  const beaconLayer = createSVGEl("g", { class: "a-dash-topo-map-beacons", "data-refresh-key": "map-layer-beacons" });
  const tagLayer = createSVGEl("g", { class: "a-dash-topo-map-tags", "data-refresh-key": "map-layer-tags" });
  const pillLayer = createSVGEl("g", { class: "a-dash-topo-map-pills", "data-refresh-key": "map-layer-pills" });

  const ambientSet = singleMode
    ? new Set(model.probeLinks.map((p) => p.id))
    : selectTopAmbientBadProbeIDs(model.probeLinks, { includeExternal: true, maxCount: 4 });
  const occupiedTags = [
    { x: 0, y: 0, w, h: 26, screenFixed: true },
    { x: Math.max(0, w - 122), y: 24, w: 118, h: 30, screenFixed: true },
    ...clusters.map((c) => ({ x: c.x - 9, y: c.y - 9, w: 18, h: 18 })),
  ];
  const mobDirMode = model?.metricDir === "forward" || model?.metricDir === "reverse" ? model.metricDir : "both";
  const mobChainTotalBadge = singleMode ? buildChainTotalOverlay(model.singleGroup, mobDirMode, { mapHeight, compact: true, occupiedList: occupiedTags }) : null;

  const sortedForTags = [...clusters].sort((a, b) => {
    const aCl = a.isCluster ? 1 : 0;
    const bCl = b.isCluster ? 1 : 0;
    if (bCl !== aCl) return bCl - aCl;
    const aIssue = a.members.some((m) => !m.node.isOnline || Boolean(m.node.issue)) ? 1 : 0;
    const bIssue = b.members.some((m) => !m.node.isOnline || Boolean(m.node.issue)) ? 1 : 0;
    if (bIssue !== aIssue) return bIssue - aIssue;
    return b.members.length - a.members.length;
  });

  sortedForTags.slice(0, 10).forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const hl = helpers.isHighlightedEdge(peers);
    const anyIssue = cluster.members.some((m) => !m.node.isOnline || Boolean(m.node.issue));
    const anyOnline = cluster.members.some((m) => m.node.isOnline);
    const rawTagText = resolveMapNodeLabel(cluster, singleMode ? model.singleGroup : null);
    const flagInfo = resolveClusterFlagInfo(cluster);
    const flagW = flagInfo ? 12 : 0;
    const flagH = 8.5;
    const flagSlot = flagInfo ? flagW + 4 : 0;
    const tagText = clipLabel(rawTagText, 24);
    const tagW = Math.max(40, Math.min(168, visualUnits(tagText) * 5.3 + 10 + flagSlot));
    const tagH = 16;
    const r = cluster.isCluster ? 6.5 : 4.2;
    const candidates = buildDensityAwareTagCandidates(cluster, clusters, tagW, tagH, r, {
      minY: 28, maxY: mapHeight - 6,
    }, wrapSpan);
    const pick = candidates.find((c) => !wrapsOverlap({ x: c.x, y: c.y, w: tagW, h: tagH }, occupiedTags, wrapSpan, 2, 2));
    if (!pick) return;
    occupiedTags.push({ x: pick.x, y: pick.y, w: tagW, h: tagH });
    const g = createSVGEl("g", {
      class: `a-dash-topo-node a-dash-topo-map-tag ${helpers.activeKey() === key ? "is-active" : ""}`,
      "data-refresh-key": `m-tag-${key}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      "data-dim-opacity": "0.32",
      opacity: hl === false ? "0.32" : "1",
    });
    g.append(
      createSVGEl("rect", {
        x: pick.x.toFixed(1),
        y: pick.y.toFixed(1),
        width: tagW.toFixed(1),
        height: tagH,
        rx: 4,
        fill: "#ffffff",
        stroke: anyIssue ? BAD_COLOR : anyOnline ? "#d4d4d8" : BORDER_COLOR,
        "stroke-width": "0.9",
        class: "a-dash-topo-card",
      }),
    );
    if (flagInfo) {
      appendMapTagFlag(g, flagInfo, pick.x + 4, pick.y + (tagH - flagH) / 2, flagW, flagH);
    }
    g.append(
      createSVGEl("text", {
        x: (pick.x + 4.5 + flagSlot).toFixed(1),
        y: (pick.y + 11.4).toFixed(1),
        class: "a-dash-topo-map-tag-text",
        fill: anyIssue ? BAD_COLOR : anyOnline ? INK_COLOR : SOFT_INK_COLOR,
      }, tagText),
    );
    helpers.bindInteractive(g, key);
    tagLayer.append(g);
  });

  model.probeLinks.forEach((probe) => {
    const geom = arcGeom.get(probe.id);
    if (!geom) return;
    const peers = selIndex.probePeers(probe);
    const hl = helpers.isHighlightedEdge(peers);
    const isBad = probe.tone === "bad";
    const isWarn = probe.tone === "warn";
    const pal = tonePalette(probe.tone);
    const baseOpacity = singleMode ? 0.92 : (isBad ? 0.9 : isWarn ? 0.8 : probe.isExternal ? 0.54 : 0.50);
    const opacity = hl === true ? 1 : hl === false ? 0.30 : baseOpacity;

    const g = createSVGEl("g", {
      "data-refresh-key": `m-map-arc-${probe.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": String(baseOpacity),
      "data-dim-opacity": "0.30",
      ...(probe.isExternal ? { "data-ext-probe": "1" } : {}),
      opacity: opacity.toFixed(2),
    });
    const strokeW = (hl === true ? 2.4 : singleMode ? 2.0 : isBad ? 2.0 : 1.4).toFixed(2);
    g.append(createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: pal.stroke,
      "stroke-width": strokeW,
      "stroke-dasharray": probe.running ? "6 4" : isBad ? "4 3" : probe.isExternal ? "2 3" : null,
      "stroke-linecap": "round",
      "marker-end": isBad ? `url(#${defsPrefix}-arrow-bad)` : isWarn ? `url(#${defsPrefix}-arrow-warn)` : probe.tone === "ok" ? `url(#${defsPrefix}-arrow-ok)` : `url(#${defsPrefix}-arrow-idle)`,
      class: probe.running ? "a-dash-topo-probe-running" : null,
    }));

    if (helpers.allowMotion() && !isProbeUnreachable(probe) && !probe.unavailableReverse && !probe.running && (singleMode || hl === true || probe.tone === "ok")) {
      const spd = flowSpeedClass(probe.latencyMS);
      geom.segments.forEach((seg, segIdx) => {
        const segD = `M ${seg.x1.toFixed(1)} ${seg.y1.toFixed(1)} Q ${seg.cx.toFixed(1)} ${seg.cy.toFixed(1)}, ${seg.x2.toFixed(1)} ${seg.y2.toFixed(1)}`;
        pulseLayer.append(createSVGEl("path", {
          d: segD,
          fill: "none",
          stroke: pal.stroke,
          "stroke-width": "1.5",
          "stroke-dasharray": isWarn ? "3 17" : "5 15",
          "data-topo-peers": peers.join(" "),
          "data-base-opacity": "0.52",
          "data-dim-opacity": "0.18",
          opacity: hl === false ? "0.18" : "0.52",
          ...(probe.isExternal ? { "data-ext-probe": "1" } : {}),
          class: `a-dash-topo-flow-pulse ${spd}`,
          "data-refresh-key": `m-map-pulse-${probe.id}-${segIdx}`,
        }));
      });
    }

    const hitPath = createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: "transparent",
      "stroke-width": "14",
      class: "a-dash-topo-probe-item",
    });
    helpers.bindInteractive(hitPath, `probe:${probe.id}`);
    g.append(hitPath);
    linkLayer.append(g);

    const isAmbient = ambientSet.has(probe.id);
    const label = !singleMode && isAmbient && hl !== true
      ? buildProbeShortAlertLabel(probe)
      : buildProbePillLabel(probe, true, true);
    const pillW = Math.max(40, Math.min(116, visualUnits(label) * 5.4 + 16));
    const pillH = 15.5;
    const pillBounds = probe.isExternal
      ? { minX: 8, maxX: w - 8, minY: 32, maxY: mapHeight - 10 }
      : { minX: geom.mid.x - 160, maxX: geom.mid.x + 160, minY: 32, maxY: mapHeight - 10 };
    const placed = placeCollisionFreePill(geom.mid.x, geom.mid.y, pillW, pillH, occupiedTags, pillBounds, [geom.mid]);
    if (placed.shifted) {
      helpers.appendProbeLeader(pillLayer, placed, pal, peers, hl, true, !isAmbient, probe.id);
    }
    const pillEl = helpers.createProbePillGroup({
      probe,
      cx: placed.cx,
      cy: placed.cy,
      pillW,
      fullW: pillW,
      pillH,
      label,
      pal,
      compact: true,
      peers,
      focusOnly: !isAmbient,
    });
    if (probe.isExternal) pillEl.setAttribute("data-ext-probe", "1");
    pillLayer.append(pillEl);
  });

  clusters.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const anyOnline = cluster.members.some((m) => m.node.isOnline);
    const active = helpers.activeKey() === key;
    const hl = helpers.isHighlightedEdge(peers);
    const toneColor = clusterHealthTone(cluster);
    const r = cluster.isCluster ? Math.min(7.5, 4.8 + cluster.members.length * 0.8) : 4.2;

    const g = createSVGEl("g", {
      class: `a-dash-topo-node a-dash-topo-map-tag ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `m-beacon-${key}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      "data-dim-opacity": "0.32",
      opacity: hl === false ? "0.32" : "1",
    });

    if (anyOnline && helpers.allowMotion()) {
      pulseLayer.append(createSVGEl("circle", {
        cx: cluster.x.toFixed(1),
        cy: cluster.y.toFixed(1),
        r: (r + 5.5).toFixed(1),
        fill: "none",
        stroke: toneColor,
        "stroke-width": "1",
        class: "a-dash-topo-beacon-pulse",
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "0.38",
        "data-dim-opacity": "0.12",
        opacity: hl === false ? "0.12" : "0.38",
      }));
    }
    g.append(createSVGEl("circle", { cx: cluster.x.toFixed(1), cy: cluster.y.toFixed(1), r: 15, fill: "transparent" }));
    if (singleMode || active || hl === true) {
      g.append(createSVGEl("circle", {
        cx: cluster.x.toFixed(1),
        cy: cluster.y.toFixed(1),
        r: (r + 2.8).toFixed(1),
        fill: "none",
        stroke: active ? INK_COLOR : toneColor,
        "stroke-width": "0.85",
        "stroke-dasharray": "2 2",
        opacity: "0.62",
      }));
    }
    g.append(createSVGEl("circle", {
      cx: cluster.x.toFixed(1),
      cy: cluster.y.toFixed(1),
      r: r.toFixed(1),
      fill: "#ffffff",
      stroke: active ? INK_COLOR : toneColor,
      "stroke-width": active || hl === true ? "2.4" : "1.8",
      class: "a-dash-topo-card",
    }));
    if (cluster.isCluster) {
      g.append(createSVGEl("text", {
        x: cluster.x.toFixed(1),
        y: (cluster.y + 3).toFixed(1),
        "text-anchor": "middle",
        class: "a-dash-topo-micro",
        fill: toneColor,
      }, String(cluster.members.length)));
    }
    g.append(createSVGEl("title", {}, cluster.isCluster ? `${cluster.label} (${cluster.members.length} 节点)` : cluster.primary.node.name));
    helpers.bindInteractive(g, key);
    beaconLayer.append(g);
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
    });
    g.append(
      createSVGEl("rect", { x: chip.x.toFixed(1), y: chip.y.toFixed(1), width: chip.w.toFixed(1), height: chip.h, rx: 6, fill: "#ffffff", stroke: active ? INK_COLOR : "#d4d4d8", "stroke-width": active ? "1.6" : "1", class: "a-dash-topo-card" }),
      createSVGEl("text", { x: (chip.x + 8).toFixed(1), y: (chip.y + 14.5).toFixed(1), class: "a-dash-topo-map-tag-text", fill: INK_COLOR }, chip.title),
      createSVGEl("text", { x: (chip.x + 8).toFixed(1), y: (chip.y + 25.5).toFixed(1), class: "a-dash-topo-micro", fill: SOFT_INK_COLOR }, chip.subText),
    );
    g.append(createSVGEl("title", {}, `${chip.fullTitle} (${chip.ext.linkCount || 1} 链路)`));
    helpers.bindInteractive(g, extID);
    dockLayer.append(g);
  });

  zoomGroup.append(backdrop, linkLayer, pulseLayer, beaconLayer, tagLayer, pillLayer);
  appendWrapTileCopies(zoomGroup, [linkLayer, pulseLayer, beaconLayer, tagLayer, pillLayer], {
    wrapSpan,
    viewportWidth: w,
    helpers,
  });
  stageClipGroup.append(oceanBase, zoomGroup);

  const hudLayer = createSVGEl("g", {
    class: "a-dash-topo-map-hud",
    "clip-path": `url(#${clipId})`,
    "data-refresh-key": "map-layer-hud",
  });
  const mobLeftText = singleMode
    ? `${model.onlineNodes}/${model.totalNodes} 在线 · ${model.singleGroup.hopCount}跳`
    : `${model.onlineNodes}/${model.totalNodes} 在线 · ${model.probeChains.length}组`;
  const mobRightText = singleMode
    ? (model.singleGroup?.overall?.statusBadge?.label || "拖拽平移")
    : buildMapHudLatencyLine(model, true);
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
 * Pure network-monitoring semantics: online/diagnostic capability beacons, continuous wrapped arcs,
 * clean latency/loss pills, end-to-end total latency badge, external dock, and seamless horizontal wrapping.
 */
export function renderPacificMapSVG({ width, isMobile, model, zoomTransform = null, helpers }) {
  if (isMobile) {
    return renderMobilePacificMap({ width, model, zoomTransform, helpers });
  }

  const vw = PACIFIC_MAP_BOUNDS.width;
  const mapHeight = PACIFIC_MAP_BOUNDS.height;
  const singleMode = Boolean(model?.singleGroup);
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
  const defs = createTopologyDefs(defsPrefix);
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

  const probeLayer = createSVGEl("g", { class: "a-dash-topo-map-probes", "data-refresh-key": "map-layer-probes" });
  const pulseLayer = createSVGEl("g", { class: "a-dash-topo-map-pulses", "aria-hidden": "true", "data-refresh-key": "map-layer-pulses" });
  const beaconLayer = createSVGEl("g", { class: "a-dash-topo-map-beacons", "data-refresh-key": "map-layer-beacons" });
  const tagLayer = createSVGEl("g", { class: "a-dash-topo-map-tags", "data-refresh-key": "map-layer-tags" });
  const pillLayer = createSVGEl("g", { class: "a-dash-topo-map-pills", "data-refresh-key": "map-layer-pills" });

  const staticOccupied = [
    { x: 0, y: 0, w: vw, h: 30, screenFixed: true },
    { x: vw - 220, y: 28, w: 210, h: 36, screenFixed: true },
  ];
  const deskDirMode = model?.metricDir === "forward" || model?.metricDir === "reverse" ? model.metricDir : "both";
  const deskChainTotalBadge = singleMode ? buildChainTotalOverlay(model.singleGroup, deskDirMode, { mapHeight, compact: false, occupiedList: staticOccupied }) : null;
  clusters.forEach((c) => {
    const br = c.isCluster ? Math.min(9.5, 5.6 + c.members.length * 0.9) : 5.2;
    staticOccupied.push({ x: c.x - br - 3, y: c.y - br - 3, w: (br + 3) * 2, h: (br + 3) * 2 });
  });

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

  const tagBounds = { minY: 32, maxY: mapHeight - 14 };
  const hiddenClusterCount = [];

  tagPriority.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const hl = helpers.isHighlightedEdge(peers);
    const active = helpers.activeKey() === key;
    const anyOnline = cluster.members.some((m) => m.node.isOnline);
    const anyIssue = cluster.members.some((m) => !m.node.isOnline || Boolean(m.node.issue));
    const r = cluster.isCluster ? Math.min(9.0, 5.6 + cluster.members.length * 0.85) : 5.0;

    const baseTagText = resolveMapNodeLabel(cluster, singleMode ? model.singleGroup : null);
    const flagInfo = resolveClusterFlagInfo(cluster);
    const flagW = flagInfo ? 14 : 0;
    const flagH = 10;
    const flagSlot = flagInfo ? flagW + 5 : 0;
    const displayTag = clipLabel(baseTagText, 28);
    const tagW = Math.max(48, Math.min(220, visualUnits(displayTag) * 6.2 + 14 + flagSlot));
    const tagH = 20;

    const candidates = buildDensityAwareTagCandidates(cluster, clusters, tagW, tagH, r, tagBounds, wrapSpan);
    let chosen = null;
    let bestScore = Infinity;
    for (const c of candidates) {
      const box = { x: c.x, y: c.y, w: tagW, h: tagH };
      if (wrapsOverlap(box, staticOccupied, wrapSpan, 3, 2.5)) continue;
      if (c.sectorPenalty < bestScore) {
        bestScore = c.sectorPenalty;
        chosen = { ...c, box };
      }
    }

    if (!chosen) {
      hiddenClusterCount.push(cluster);
      return;
    }

    staticOccupied.push(chosen.box);
    const g = createSVGEl("g", {
      class: `a-dash-topo-node a-dash-topo-map-tag ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `map-tag-${key}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      "data-dim-opacity": "0.32",
      opacity: hl === false ? "0.32" : "1",
    });

    if (chosen.leader) {
      const lx = Math.max(chosen.x, Math.min(chosen.x + tagW, cluster.x));
      const ly = Math.max(chosen.y, Math.min(chosen.y + tagH, cluster.y));
      g.append(createSVGEl("line", {
        x1: cluster.x.toFixed(1),
        y1: cluster.y.toFixed(1),
        x2: lx.toFixed(1),
        y2: ly.toFixed(1),
        stroke: "#a1a1aa",
        "stroke-width": "0.95",
        "stroke-dasharray": "2 2",
      }));
    }

    g.append(
      createSVGEl("rect", {
        x: chosen.x.toFixed(1),
        y: chosen.y.toFixed(1),
        width: tagW.toFixed(1),
        height: tagH,
        rx: 4.5,
        fill: "#ffffff",
        stroke: active ? INK_COLOR : anyIssue ? BAD_COLOR : anyOnline ? "#d4d4d8" : BORDER_COLOR,
        "stroke-width": active ? "1.7" : "1",
        class: "a-dash-topo-card",
      }),
    );
    if (flagInfo) {
      appendMapTagFlag(g, flagInfo, chosen.x + 5.5, chosen.y + (tagH - flagH) / 2, flagW, flagH);
    }
    g.append(
      createSVGEl("text", {
        x: (chosen.x + 6.5 + flagSlot).toFixed(1),
        y: (chosen.y + 13.8).toFixed(1),
        class: "a-dash-topo-map-tag-text",
        fill: anyIssue ? BAD_COLOR : anyOnline ? INK_COLOR : SOFT_INK_COLOR,
      }, displayTag),
    );
    g.append(createSVGEl("title", {}, cluster.members.map((m) => `${m.node.name}${m.node.issue ? ` (${m.node.issue})` : ""}`).join(" / ")));
    helpers.bindInteractive(g, key);
    tagLayer.append(g);
  });

  clusters.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const hl = helpers.isHighlightedEdge(peers);
    const active = helpers.activeKey() === key;
    const anyOnline = cluster.members.some((m) => m.node.isOnline);
    const toneColor = clusterHealthTone(cluster);
    const r = cluster.isCluster ? Math.min(9.0, 5.6 + cluster.members.length * 0.85) : 5.0;

    const g = createSVGEl("g", {
      class: `a-dash-topo-node a-dash-topo-map-tag ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `map-beacon-${key}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      "data-dim-opacity": "0.32",
      opacity: hl === false ? "0.32" : "1",
    });

    if (anyOnline && helpers.allowMotion()) {
      pulseLayer.append(createSVGEl("circle", {
        cx: cluster.x.toFixed(1),
        cy: cluster.y.toFixed(1),
        r: (r + 6.8).toFixed(1),
        fill: "none",
        stroke: toneColor,
        "stroke-width": "1.1",
        class: "a-dash-topo-beacon-pulse",
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "0.42",
        "data-dim-opacity": "0.14",
        opacity: hl === false ? "0.14" : "0.42",
      }));
    }

    g.append(createSVGEl("circle", { cx: cluster.x.toFixed(1), cy: cluster.y.toFixed(1), r: (r + 7.5).toFixed(1), fill: "transparent" }));
    if (singleMode || active || hl === true) {
      g.append(createSVGEl("circle", {
        cx: cluster.x.toFixed(1),
        cy: cluster.y.toFixed(1),
        r: (r + 3.4).toFixed(1),
        fill: "none",
        stroke: active ? INK_COLOR : toneColor,
        "stroke-width": "0.95",
        "stroke-dasharray": "2 2",
        opacity: "0.65",
      }));
    }
    g.append(createSVGEl("circle", {
      cx: cluster.x.toFixed(1),
      cy: cluster.y.toFixed(1),
      r: r.toFixed(1),
      fill: "#ffffff",
      stroke: active ? INK_COLOR : toneColor,
      "stroke-width": active || hl === true ? "2.6" : "2.1",
      class: "a-dash-topo-card",
    }));

    if (cluster.isCluster) {
      g.append(createSVGEl("text", {
        x: cluster.x.toFixed(1),
        y: (cluster.y + 3.5).toFixed(1),
        "text-anchor": "middle",
        class: "a-dash-topo-micro",
        fill: toneColor,
      }, String(cluster.members.length)));
    }
    g.append(createSVGEl("title", {}, cluster.members.map((m) => m.node.name).join(" / ")));
    helpers.bindInteractive(g, key);
    beaconLayer.append(g);
  });

  const showAllAmbient = singleMode || model.probeLinks.length <= 7;
  const ambientSet = showAllAmbient
    ? new Set(model.probeLinks.map((p) => p.id))
    : selectTopAmbientBadProbeIDs(model.probeLinks, { includeExternal: true, maxCount: 6 });

  const totalBadCount = model.probeLinks.filter((p) => p.tone === "bad" || p.tone === "warn").length;
  const shownAlertCount = model.probeLinks.filter((p) => (p.tone === "bad" || p.tone === "warn") && ambientSet.has(p.id)).length;
  const unshownAlertCount = Math.max(0, totalBadCount - shownAlertCount);

  const leaderSegments = [];
  const activeSel = helpers.activeKey();

  const renderOrder = [...model.probeLinks].sort((a, b) => {
    const score = { ok: 1, idle: 1, warn: 2, bad: 3 };
    return (score[a.tone] || 0) - (score[b.tone] || 0);
  });

  renderOrder.forEach((probe) => {
    const geom = arcGeom.get(probe.id);
    if (!geom) return;

    const eKey = `probe:${probe.id}`;
    const peers = selIndex.probePeers(probe);
    const hl = helpers.isHighlightedEdge(peers);
    const pal = tonePalette(probe.tone);
    const isBad = probe.tone === "bad";
    const isWarn = probe.tone === "warn";
    const baseOpacity = singleMode ? 0.94 : (isBad ? 0.92 : isWarn ? 0.86 : probe.isExternal ? 0.56 : 0.52);
    const opacity = hl === true ? 1 : hl === false ? 0.28 : baseOpacity;

    const g = createSVGEl("g", {
      "data-refresh-key": `map-arc-${probe.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": String(baseOpacity),
      "data-dim-opacity": "0.28",
      ...(probe.isExternal ? { "data-ext-probe": "1" } : {}),
      opacity: opacity.toFixed(2),
    });

    const strokeW = (hl === true ? 2.6 : singleMode ? 2.15 : isBad ? 2.0 : 1.5).toFixed(2);
    g.append(createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: pal.stroke,
      "stroke-width": strokeW,
      "stroke-dasharray": probe.running ? "6 4" : isBad ? "5 3" : probe.isExternal ? "2 3" : null,
      "stroke-linecap": "round",
      "marker-end": isBad ? `url(#${defsPrefix}-arrow-bad)` : isWarn ? `url(#${defsPrefix}-arrow-warn)` : probe.tone === "ok" ? `url(#${defsPrefix}-arrow-ok)` : `url(#${defsPrefix}-arrow-idle)`,
      class: probe.running ? "a-dash-topo-probe-running" : null,
    }));

    if (helpers.allowMotion() && !isProbeUnreachable(probe) && !probe.unavailableReverse && !probe.running && (singleMode || hl === true || probe.tone === "ok")) {
      const spd = flowSpeedClass(probe.latencyMS);
      geom.segments.forEach((seg, segIdx) => {
        const segD = `M ${seg.x1.toFixed(1)} ${seg.y1.toFixed(1)} Q ${seg.cx.toFixed(1)} ${seg.cy.toFixed(1)}, ${seg.x2.toFixed(1)} ${seg.y2.toFixed(1)}`;
        pulseLayer.append(createSVGEl("path", {
          d: segD,
          fill: "none",
          stroke: pal.stroke,
          "stroke-width": "1.6",
          "stroke-dasharray": isWarn ? "3 17" : "5 15",
          "data-topo-peers": peers.join(" "),
          "data-base-opacity": "0.55",
          "data-dim-opacity": "0.18",
          opacity: hl === false ? "0.18" : "0.55",
          ...(probe.isExternal ? { "data-ext-probe": "1" } : {}),
          class: `a-dash-topo-flow-pulse ${spd}`,
          "data-refresh-key": `map-pulse-${probe.id}-${segIdx}`,
        }));
      });
    }

    const hit = createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: "transparent",
      "stroke-width": "12",
      class: "a-dash-topo-probe-item",
    });
    helpers.bindInteractive(hit, eKey);
    g.append(hit);
    probeLayer.append(g);

    const isAmbient = ambientSet.has(probe.id);
    const pillLabel = buildProbePillLabel(probe, true, false);
    const pillH = 19.5;
    const fullW = Math.max(46, Math.min(138, visualUnits(pillLabel) * 6.0 + 18));

    const pillOccupied = isAmbient ? staticOccupied : [...staticOccupied];
    const pillBounds = probe.isExternal
      ? { minX: 20, maxX: vw - 20, minY: 34, maxY: mapHeight - 14 }
      : { minX: geom.mid.x - 190, maxX: geom.mid.x + 190, minY: 34, maxY: mapHeight - 14 };
    const placed = placeCollisionFreePill(
      geom.mid.x,
      geom.mid.y,
      fullW,
      pillH,
      pillOccupied,
      pillBounds,
      [geom.mid],
      { leaderSegments, reserveW: fullW },
    );

    if (placed.shifted || isAmbient) {
      if (Math.hypot(placed.cx - geom.mid.x, placed.cy - geom.mid.y) > 16) {
        helpers.appendProbeLeader(pillLayer, placed, pal, peers, hl, false, !isAmbient, probe.id);
      }
    }

    const pillEl = helpers.createProbePillGroup({
      probe,
      cx: placed.cx,
      cy: placed.cy,
      pillW: fullW,
      fullW,
      pillH,
      label: pillLabel,
      pal,
      compact: false,
      peers,
      focusOnly: !isAmbient,
    });
    if (probe.isExternal) pillEl.setAttribute("data-ext-probe", "1");
    pillLayer.append(pillEl);
  });

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
        createSVGEl("circle", { cx: chip.cx.toFixed(1), cy: chip.y.toFixed(1), r: 2.5, fill: "#ffffff", stroke: SOFT_INK_COLOR, "stroke-width": "1.2" }),
        createSVGEl("text", { x: (chip.x + 10).toFixed(1), y: (chip.y + 15).toFixed(1), class: "a-dash-topo-map-tag-text", fill: INK_COLOR }, chip.title),
        createSVGEl("text", { x: (chip.x + 10).toFixed(1), y: (chip.y + 28).toFixed(1), class: "a-dash-topo-ext-sub", fill: SOFT_INK_COLOR }, chip.subText),
      );
      g.append(createSVGEl("title", {}, `${chip.fullTitle}${chip.ext.address ? ` (${chip.ext.address})` : ""} · ${chip.ext.linkCount || 1} 链路`));
      helpers.bindInteractive(g, extID);
      dockLayer.append(g);
    });
  }

  zoomGroup.append(backdrop, probeLayer, pulseLayer, beaconLayer, tagLayer, pillLayer);
  appendWrapTileCopies(zoomGroup, [probeLayer, pulseLayer, beaconLayer, tagLayer, pillLayer], {
    wrapSpan,
    viewportWidth: vw,
    helpers,
  });
  stageClipGroup.append(oceanBase, zoomGroup);

  const hudLayer = createSVGEl("g", {
    class: "a-dash-topo-map-hud",
    "clip-path": `url(#${clipId})`,
    "data-refresh-key": "map-layer-hud",
  });
  const deskLeftText = singleMode
    ? `${model.onlineNodes}/${model.totalNodes} 节点在线 · ${model.singleGroup.hopCount} 跳链路`
    : `${model.onlineNodes}/${model.totalNodes} 节点在线 · ${model.probeChains.length} 监测组`;
  const deskRightText = singleMode
    ? "拖拽左右循环 · 双击或 Ctrl+滚轮缩放"
    : buildMapHudLatencyLine(model, false);
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
