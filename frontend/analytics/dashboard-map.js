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
  formatMapCityLabel,
  isProbeUnreachable,
  placeCollisionFreePill,
  quadPointAt,
  rectsOverlap,
  selectTopAmbientBadProbeIDs,
  tonePalette,
  visualUnits,
} from "./dashboard-topo-shared.js";


/** Computes non-overlapping tag candidates ranked away from dense neighbor clusters using vector repulsion. */
function buildDensityAwareTagCandidates(cluster, allClusters, boxW, boxH, r, bounds) {
  const ax = cluster.geoX;
  const ay = cluster.geoY;

  let repX = 0;
  let repY = 0;
  for (const other of allClusters) {
    if (other.key === cluster.key) continue;
    const dx = ax - other.geoX;
    const dy = ay - other.geoY;
    const dist = Math.hypot(dx, dy);
    if (dist > 0.1 && dist < 155) {
      const weight = 1 / (dist * dist + 90);
      repX += (dx / dist) * weight;
      repY += (dy / dist) * weight;
    }
  }

  if (ax - bounds.minX < 95) repX += 0.018;
  if (bounds.maxX - ax < 95) repX -= 0.018;
  if (ay - bounds.minY < 58) repY += 0.018;
  if (bounds.maxY - ay < 58) repY -= 0.018;

  const baseAngle = (Math.abs(repX) + Math.abs(repY)) > 0.0005
    ? Math.atan2(repY, repX)
    : -Math.PI * 0.22;

  const angleOffsets = [0, Math.PI / 4, -Math.PI / 4, Math.PI / 2, -Math.PI / 2, (3 * Math.PI) / 4, -(3 * Math.PI) / 4, Math.PI];
  const rings = [r + 12, r + 26, r + 44, r + 66];
  const candidates = [];

  for (let ringIdx = 0; ringIdx < rings.length; ringIdx++) {
    const dist = rings[ringIdx];
    for (let dirIdx = 0; dirIdx < angleOffsets.length; dirIdx++) {
      const theta = baseAngle + angleOffsets[dirIdx];
      const ux = Math.cos(theta);
      const uy = Math.sin(theta);
      const cx = ax + ux * (dist + boxW * 0.34);
      const cy = ay + uy * (dist + boxH * 0.32);
      const bx = Math.max(bounds.minX, Math.min(bounds.maxX - boxW, cx - boxW / 2));
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
 * Computes bounded multi-row dock chip positions inside `[padX, width - padX]`, guaranteeing zero
 * horizontal overflow regardless of how many external targets exist.
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
 * Evaluates shortest geodesic curvature (including cross-seam edge wrapping when `|dx| > span/2`
 * between Eurasia and Eastern Americas) using purely coordinate geometry.
 */
function computeCorridorLanes(probeLinks, coordByID, chipByID, allBeacons, mapBounds, scaleX = 1, scaleY = 1, zoomTransform = null) {
  const pairGroups = new Map();
  probeLinks.forEach((p) => {
    const k = p.isExternal ? `${p.from}=>${p.to}` : [p.from, p.to].sort().join("--");
    if (!pairGroups.has(k)) pairGroups.set(k, []);
    pairGroups.get(k).push(p);
  });

  const leftEdge = PACIFIC_MAP_BOUNDS.padX * scaleX;
  const rightEdge = (PACIFIC_MAP_BOUNDS.width - PACIFIC_MAP_BOUNDS.padX) * scaleX;
  const cylSpan = Math.max(120, rightEdge - leftEdge);
  const topBound = 32 * scaleY;
  const bottomBound = (PACIFIC_MAP_BOUNDS.height - 20) * scaleY;
  const minY = 28 * scaleY;
  const maxY = (PACIFIC_MAP_BOUNDS.height - 16) * scaleY;

  const zScale = zoomTransform && zoomTransform.scale > 1.001 ? zoomTransform.scale : 1;
  const zTx = zScale > 1 ? (zoomTransform.tx || 0) : 0;
  const zTy = zScale > 1 ? (zoomTransform.ty || 0) : 0;
  const toLocalPort = (sx, sy) => ({
    x: zScale > 1 ? (sx - zTx) / zScale : sx,
    y: zScale > 1 ? (sy - zTy) / zScale : sy,
  });

  const placedMids = [];
  const geomByProbeID = new Map();

  for (const [groupKey, groupProbes] of pairGroups.entries()) {
    const first = groupProbes[0];
    const pA = coordByID.get(first.from);
    if (!pA) continue;

    if (first.isExternal) {
      const chip = chipByID.get(first.to);
      if (!chip) continue;
      const targetScreenY = zScale > 1 ? PACIFIC_MAP_BOUNDS.height * scaleY : chip.y;
      const port = toLocalPort(chip.cx, targetScreenY);
      const dx = port.x - pA.x;
      const dy = port.y - pA.y;
      const len = Math.hypot(dx, dy) || 1;
      const nx = -dy / len;
      const ny = dx / len;
      groupProbes.forEach((probe, idx) => {
        const sign = idx === 0 ? 1 : (idx % 2 === 1 ? -1 : 1);
        const mag = (16 + Math.floor((idx + 1) / 2) * 14) / zScale;
        const cx = (pA.x + port.x) / 2 + nx * mag * sign;
        const cy = Math.max(minY, Math.min(port.y - 6 / zScale, (pA.y + port.y) / 2 + ny * mag * sign));
        const d = `M ${pA.x.toFixed(1)} ${pA.y.toFixed(1)} Q ${cx.toFixed(1)} ${cy.toFixed(1)}, ${port.x.toFixed(1)} ${port.y.toFixed(1)}`;
        const mid = quadPointAt(pA.x, pA.y, cx, cy, port.x, port.y, 0.5);
        geomByProbeID.set(probe.id, { d, segments: [{ x1: pA.x, y1: pA.y, cx, cy, x2: port.x, y2: port.y }], mid, wrapped: false });
      });
      continue;
    }

    const pB = coordByID.get(first.to);
    if (!pB) continue;

    const rawDx = pB.x - pA.x;
    const useWrap = Math.abs(rawDx) > cylSpan * 0.52;

    if (useWrap) {
      const wrapDx = rawDx > 0 ? rawDx - cylSpan : rawDx + cylSpan;
      const exitEdgeA = wrapDx < 0 ? leftEdge : rightEdge;
      const enterEdgeB = wrapDx < 0 ? rightEdge : leftEdge;
      const spanA = Math.abs(exitEdgeA - pA.x);
      const spanB = Math.abs(pB.x - enterEdgeB);
      const totalWrap = Math.max(1, spanA + spanB);
      const tSeam = spanA / totalWrap;
      const seamYBase = Math.max(topBound, Math.min(bottomBound, pA.y + (pB.y - pA.y) * tSeam));

      groupProbes.forEach((probe, idx) => {
        const from = coordByID.get(probe.from);
        const to = coordByID.get(probe.to);
        if (!from || !to) return;
        const pDx = to.x - from.x;
        const pWrapDx = pDx > 0 ? pDx - cylSpan : pDx + cylSpan;
        const eA = pWrapDx < 0 ? leftEdge : rightEdge;
        const eB = pWrapDx < 0 ? rightEdge : leftEdge;
        const dA = Math.abs(eA - from.x);
        const dB = Math.abs(to.x - eB);
        const slotSign = idx % 2 === 0 ? -1 : 1;
        const slotMag = Math.floor((idx + 1) / 2) * 18;
        const seamY = Math.max(topBound, Math.min(bottomBound, seamYBase + slotSign * slotMag));
        const bowA = (16 + dA * 0.14 + idx * 8) * slotSign;
        const bowB = (16 + dB * 0.14 + idx * 8) * slotSign;
        const ctrlAx = (from.x + eA) / 2;
        const ctrlAy = Math.max(minY, Math.min(maxY, (from.y + seamY) / 2 + bowA));
        const ctrlBx = (eB + to.x) / 2;
        const ctrlBy = Math.max(minY, Math.min(maxY, (seamY + to.y) / 2 + bowB));
        const seg1 = { x1: from.x, y1: from.y, cx: ctrlAx, cy: ctrlAy, x2: eA, y2: seamY };
        const seg2 = { x1: eB, y1: seamY, cx: ctrlBx, cy: ctrlBy, x2: to.x, y2: to.y };
        const primaryHalf = dA >= dB ? seg1 : seg2;
        const mid = quadPointAt(primaryHalf.x1, primaryHalf.y1, primaryHalf.cx, primaryHalf.cy, primaryHalf.x2, primaryHalf.y2, 0.5);
        placedMids.push(mid);
        geomByProbeID.set(probe.id, {
          d: `M ${seg1.x1.toFixed(1)} ${seg1.y1.toFixed(1)} Q ${seg1.cx.toFixed(1)} ${seg1.cy.toFixed(1)}, ${seg1.x2.toFixed(1)} ${seg1.y2.toFixed(1)} M ${seg2.x1.toFixed(1)} ${seg2.y1.toFixed(1)} Q ${seg2.cx.toFixed(1)} ${seg2.cy.toFixed(1)}, ${seg2.x2.toFixed(1)} ${seg2.y2.toFixed(1)}`,
          segments: [seg1, seg2],
          seamMarks: [{ x: eA, y: seamY }, { x: eB, y: seamY }],
          mid,
          wrapped: true,
        });
      });
      continue;
    }

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

    const baseBow = Math.min(82 * scaleY, Math.max(16 * scaleY, chordLen * 0.15));
    const scoreCandidate = (sign, mag) => {
      const cx = (pA.x + pB.x) / 2 + nx * sign * mag;
      const cy = Math.max(minY, Math.min(maxY, (pA.y + pB.y) / 2 + ny * sign * mag));
      let penalty = sign < 0 ? 4 : 0;
      for (const sampleT of [0.25, 0.5, 0.75]) {
        const pt = quadPointAt(pA.x, pA.y, cx, cy, pB.x, pB.y, sampleT);
        if (pt.x < mapBounds.minX + 12 || pt.x > mapBounds.maxX - 12 || pt.y < minY + 6 || pt.y > maxY - 6) {
          penalty += 55;
        }
        for (const beacon of allBeacons) {
          if (Math.hypot(beacon.x - pA.x, beacon.y - pA.y) < 3 || Math.hypot(beacon.x - pB.x, beacon.y - pB.y) < 3) continue;
          const bDist = Math.hypot(pt.x - beacon.x, pt.y - beacon.y);
          if (bDist < 22) penalty += (22 - bDist) * 2.2;
        }
      }
      const mid = quadPointAt(pA.x, pA.y, cx, cy, pB.x, pB.y, 0.5);
      for (const prevMid of placedMids) {
        const mDist = Math.hypot(mid.x - prevMid.x, mid.y - prevMid.y);
        if (mDist < 24) penalty += (24 - mDist) * 1.4;
      }
      return { cx, cy, mid, penalty };
    };

    const primaryForward = scoreCandidate(1, baseBow);
    const primaryReverse = scoreCandidate(-1, baseBow);
    const dominantSign = primaryForward.penalty <= primaryReverse.penalty ? 1 : -1;

    groupProbes.forEach((probe, idx) => {
      const from = coordByID.get(probe.from);
      const to = coordByID.get(probe.to);
      if (!from || !to) return;
      const sign = groupProbes.length === 1
        ? dominantSign
        : (idx % 2 === 0 ? dominantSign : -dominantSign);
      const laneTier = Math.floor(idx / 2);
      const mag = baseBow + laneTier * (18 * scaleY);
      const cx = (from.x + to.x) / 2 + nx * sign * mag;
      const cy = Math.max(minY, Math.min(maxY, (from.y + to.y) / 2 + ny * sign * mag));
      const d = `M ${from.x.toFixed(1)} ${from.y.toFixed(1)} Q ${cx.toFixed(1)} ${cy.toFixed(1)}, ${to.x.toFixed(1)} ${to.y.toFixed(1)}`;
      const mid = quadPointAt(from.x, from.y, cx, cy, to.x, to.y, 0.5);
      placedMids.push(mid);
      geomByProbeID.set(probe.id, {
        d,
        segments: [{ x1: from.x, y1: from.y, cx, cy, x2: to.x, y2: to.y }],
        mid,
        wrapped: false,
        groupKey,
      });
    });
  }

  return geomByProbeID;
}

/**
 * Paper & Ink Pacific Ocean canvas + land vectors (neutral warm-paper canvas & crisp hairline contours).
 */
function appendPacificBackdrop(svg, defs, { width, mapHeight, clipId, graticuleTransform = "" }) {
  const backdropSig = `${width}:${mapHeight}:${clipId}:${graticuleTransform}`;
  const clip = createSVGEl("clipPath", { id: clipId, "data-refresh-key": "pacific-clip", "data-clip-sig": backdropSig });
  clip.append(createSVGEl("rect", { x: 0, y: 0, width, height: mapHeight, rx: 8 }));
  clip.reconcileTable = (fresh) => fresh?.getAttribute?.("data-clip-sig") === clip.getAttribute("data-clip-sig");
  defs.append(clip);

  const backdrop = createSVGEl("g", {
    class: "a-dash-topo-map-backdrop",
    "clip-path": `url(#${clipId})`,
    "data-refresh-key": "pacific-backdrop",
    "data-backdrop-sig": backdropSig,
  });
  backdrop.reconcileTable = (fresh) => fresh?.getAttribute?.("data-backdrop-sig") === backdrop.getAttribute("data-backdrop-sig");

  const ocean = createSVGEl("rect", { x: 0, y: 0, width, height: mapHeight, fill: "#f7f7f8" });
  const graticuleG = createSVGEl("g", {
    fill: "none",
    stroke: BORDER_COLOR,
    "stroke-width": "0.65",
    "stroke-dasharray": "2.5 3.5",
    transform: graticuleTransform || null,
  });
  PACIFIC_GRATICULES.meridians.forEach((m) => graticuleG.append(createSVGEl("path", { d: m.d })));
  PACIFIC_GRATICULES.parallels.forEach((p) => graticuleG.append(createSVGEl("path", { d: p.d })));

  const landPath = createSVGEl("path", {
    d: PACIFIC_LAND_PATH,
    fill: "#eaecef",
    stroke: "#d4d4d8",
    "stroke-width": "0.85",
    "stroke-linejoin": "round",
    transform: graticuleTransform || null,
  });
  backdrop.append(ocean, graticuleG, landPath);
  svg.append(defs, backdrop);
}

function buildMapHudLatencyLine(model, compact = false) {
  const chains = model?.probeChains || [];
  const links = model?.probeLinks || [];
  const routeCount = chains.length || links.length;
  const rtts = links.map((p) => p.latencyMS).filter((v) => typeof v === "number" && Number.isFinite(v) && v >= 0);
  const avgRtt = rtts.length ? (rtts.reduce((s, v) => s + v, 0) / rtts.length) : null;
  const bad = Number(model?.abnormalLinks || 0);
  if (model?.singleGroup) {
    const g = model.singleGroup;
    const avgTxt = avgRtt != null ? `均 ${avgRtt.toFixed(avgRtt >= 100 ? 0 : 1)}ms` : (g.overall?.statusBadge?.label || "待采样");
    return compact
      ? `${g.nodes.length} 站 · ${avgTxt}`
      : `${g.groupName} · ${g.hopCount} 跳 · ${avgTxt}`;
  }
  if (compact) {
    if (!routeCount) return "暂无延迟监测";
    return bad > 0
      ? `监测 ${routeCount} 组 · 告警 ${bad}`
      : avgRtt != null
        ? `监测 ${routeCount} 组 · 均 ${avgRtt.toFixed(avgRtt >= 100 ? 0 : 1)}ms`
        : `监测 ${routeCount} 组`;
  }
  if (!routeCount) return `在线 ${model?.onlineNodes || 0}/${model?.totalNodes || 0} · 暂无监测组`;
  const parts = [`监测 ${routeCount} 组`, `${links.length} 段`];
  if (avgRtt != null) parts.push(`均值 ${avgRtt.toFixed(avgRtt >= 100 ? 0 : 1)}ms`);
  if (bad > 0) parts.push(`告警 ${bad}`);
  else parts.push("全链正常");
  return parts.join(" · ");
}

const clusterHealthTone = (cluster) => {
  const anyIssue = cluster.members.some((m) => !m.node.isOnline || Boolean(m.node.issue));
  const anyOnline = cluster.members.some((m) => m.node.isOnline);
  return anyIssue ? BAD_COLOR : anyOnline ? OK_COLOR : IDLE_FLOW_COLOR;
};

function renderMobilePacificMap({ width, model, helpers }) {
  const w = Math.max(300, Math.min(620, Math.round(width)));
  const mapHeight = Math.round(Math.max(210, Math.min(268, w * 0.68)));
  const scaleX = w / PACIFIC_MAP_BOUNDS.width;
  const scaleY = mapHeight / PACIFIC_MAP_BOUNDS.height;
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
    "aria-label": "太平洋海缆网络延迟拓扑图",
  });

  const defsPrefix = helpers.defsPrefix || "a-topo";
  const clipId = `${helpers.clipId || (singleMode ? "a-topo-map-clip-single" : "a-topo-map-clip-all")}-mob`;
  const defs = createTopologyDefs(defsPrefix);
  const bgGroupTransform = `scale(${scaleX.toFixed(4)} ${scaleY.toFixed(4)})`;
  appendPacificBackdrop(svg, defs, { width: w, mapHeight, clipId, graticuleTransform: bgGroupTransform });

  const clusters = clusterTopologyNodes(model.nodes, { radiusPx: 7.5, scaleX, scaleY });
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
    { minX: 8, maxX: w - 8, minY: 28, maxY: mapHeight - 8 },
    scaleX,
    scaleY,
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
    { x: 0, y: 0, w, h: 24 },
    ...clusters.map((c) => ({ x: c.x - 10, y: c.y - 10, w: 20, h: 20 })),
  ];

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
    const rawTagText = cluster.isCluster
      ? `${cluster.label} ×${cluster.members.length}`
      : (cluster.primary.geo.matched ? cluster.label : clipLabel(cluster.primary.node.name, 9));
    const tagText = clipLabel(rawTagText, 11);
    const tagW = Math.max(32, Math.min(86, visualUnits(tagText) * 5.6 + 9));
    const tagH = 14.5;
    const r = cluster.isCluster ? 7.5 : 5;
    const candidates = buildDensityAwareTagCandidates(cluster, clusters, tagW, tagH, r, {
      minX: 6, maxX: w - 6, minY: 25, maxY: mapHeight - 6,
    });
    const pick = candidates.find((c) => !occupiedTags.some((o) => rectsOverlap({ x: c.x, y: c.y, w: tagW, h: tagH }, o, 2)));
    if (!pick) return;
    occupiedTags.push({ x: pick.x, y: pick.y, w: tagW, h: tagH });
    const g = createSVGEl("g", {
      class: `a-dash-topo-node a-dash-topo-map-tag ${helpers.activeKey() === key ? "is-active" : ""}`,
      "data-refresh-key": `m-tag-${key}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      opacity: hl === false ? "0.24" : "1",
    });
    g.append(
      createSVGEl("rect", {
        x: pick.x.toFixed(1),
        y: pick.y.toFixed(1),
        width: tagW.toFixed(1),
        height: tagH,
        rx: 3.5,
        fill: "#ffffff",
        stroke: anyIssue ? BAD_COLOR : anyOnline ? "#d4d4d8" : BORDER_COLOR,
        "stroke-width": "0.9",
        class: "a-dash-topo-card",
      }),
      createSVGEl("text", {
        x: (pick.x + 4.5).toFixed(1),
        y: (pick.y + 10.6).toFixed(1),
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
    const baseOpacity = singleMode ? 0.9 : (isBad ? 0.9 : isWarn ? 0.8 : probe.isExternal ? 0.52 : 0.48);
    const opacity = hl === true ? 1 : hl === false ? 0.14 : baseOpacity;

    const g = createSVGEl("g", {
      "data-refresh-key": `m-map-arc-${probe.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": String(baseOpacity),
      opacity: opacity.toFixed(2),
    });
    const strokeW = (hl === true ? 2.4 : singleMode ? 2.0 : isBad ? 2.0 : 1.4).toFixed(2);
    g.append(createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: pal.stroke,
      "stroke-width": strokeW,
      "stroke-dasharray": probe.running ? "5 3" : isBad ? "4 3" : probe.isExternal ? "2 3" : null,
      "stroke-linecap": "round",
      "marker-end": isBad ? `url(#${defsPrefix}-arrow-bad)` : isWarn ? `url(#${defsPrefix}-arrow-warn)` : probe.tone === "ok" ? `url(#${defsPrefix}-arrow-ok)` : `url(#${defsPrefix}-arrow-idle)`,
      class: probe.running ? "a-dash-topo-probe-running" : null,
    }));

    if (geom.seamMarks) {
      geom.seamMarks.forEach((mk) => {
        g.append(createSVGEl("circle", { cx: mk.x.toFixed(1), cy: mk.y.toFixed(1), r: 2.6, fill: pal.stroke }));
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
      : buildProbePillLabel(probe, !singleMode, true);
    const pillW = Math.max(44, Math.min(128, visualUnits(label) * 6.0 + 18));
    const pillH = 16;
    const placed = placeCollisionFreePill(geom.mid.x, geom.mid.y, pillW, pillH, occupiedTags, {
      minX: 8, maxX: w - 8, minY: 28, maxY: mapHeight - 10,
    }, [geom.mid]);
    if (placed.shifted) {
      helpers.appendProbeLeader(pillLayer, placed, pal, peers, hl, true, !isAmbient, probe.id);
    }
    pillLayer.append(helpers.createProbePillGroup({
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
    }));
  });

  clusters.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const anyOnline = cluster.members.some((m) => m.node.isOnline);
    const active = helpers.activeKey() === key;
    const hl = helpers.isHighlightedEdge(peers);
    const toneColor = clusterHealthTone(cluster);
    const r = cluster.isCluster ? Math.min(8.5, 5.2 + cluster.members.length * 0.9) : 4.8;

    const g = createSVGEl("g", {
      class: `a-dash-topo-node a-dash-topo-map-tag ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `m-beacon-${key}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      opacity: hl === false ? "0.24" : "1",
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
        "data-dim-opacity": "0.08",
        opacity: hl === false ? "0.08" : "0.38",
      }));
    }
    g.append(createSVGEl("circle", { cx: cluster.x.toFixed(1), cy: cluster.y.toFixed(1), r: 15, fill: "transparent" }));
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
      opacity: hl === false ? "0.24" : "1",
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

  const hudLayer = createSVGEl("g", { class: "a-dash-topo-map-hud", "data-refresh-key": "map-layer-hud" });
  hudLayer.append(
    createSVGEl("text", { x: 10, y: 15, class: "a-dash-topo-map-coord" }, `158°E · ${model.onlineNodes}/${model.totalNodes} 在线`),
    createSVGEl("text", { x: w - 10, y: 15, "text-anchor": "end", class: "a-dash-topo-micro", fill: SOFT_INK_COLOR }, buildMapHudLatencyLine(model, true)),
  );

  svg.append(linkLayer, pulseLayer, beaconLayer, tagLayer, pillLayer, dockLayer, hudLayer);
  return svg;
}

/**
 * Renders the 1160px Paper & Ink Pacific Ocean topology SVG (desktop) or compact mobile map SVG.
 * Pure network-monitoring semantics: online/diagnostic capability beacons, multi-hop numbered arcs,
 * latency/loss pills, seam-wrapping across 22°W, external target dock, and inspect-only clicks.
 */
export function renderPacificMapSVG({ width, isMobile, model, zoomTransform = null, helpers }) {
  if (isMobile) {
    return renderMobilePacificMap({ width, model, helpers });
  }

  const vw = PACIFIC_MAP_BOUNDS.width;
  const mapHeight = PACIFIC_MAP_BOUNDS.height;
  const singleMode = Boolean(model?.singleGroup);

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
    "aria-label": "太平洋海缆网络延迟拓扑图",
  });

  const defsPrefix = helpers.defsPrefix || "a-topo";
  const clipId = helpers.clipId || (singleMode ? "a-topo-map-clip-single" : "a-topo-map-clip-all");
  const defs = createTopologyDefs(defsPrefix);
  const zoomStr = zoomTransform && zoomTransform.scale > 1.001
    ? `translate(${zoomTransform.tx.toFixed(1)} ${zoomTransform.ty.toFixed(1)}) scale(${zoomTransform.scale.toFixed(3)})`
    : "";

  const clip = createSVGEl("clipPath", { id: clipId, "data-refresh-key": "pacific-clip", "data-clip-sig": `${vw}:${mapHeight}:${clipId}` });
  clip.append(createSVGEl("rect", { x: 0, y: 0, width: vw, height: mapHeight, rx: 8 }));
  clip.reconcileTable = (fresh) => fresh?.getAttribute?.("data-clip-sig") === clip.getAttribute("data-clip-sig");
  defs.append(clip);
  svg.append(defs);

  const stageClipGroup = createSVGEl("g", {
    "clip-path": `url(#${clipId})`,
    "data-refresh-key": "pacific-stage-clip",
  });
  const zoomGroup = createSVGEl("g", {
    class: "a-dash-topo-map-viewport",
    "data-refresh-key": "pacific-viewport",
    transform: zoomStr || null,
  });

  const backdropSig = `${vw}:${mapHeight}`;
  const backdrop = createSVGEl("g", {
    class: "a-dash-topo-map-backdrop",
    "data-refresh-key": "pacific-backdrop",
    "data-backdrop-sig": backdropSig,
  });
  backdrop.reconcileTable = (fresh) => fresh?.getAttribute?.("data-backdrop-sig") === backdrop.getAttribute("data-backdrop-sig");
  const ocean = createSVGEl("rect", { x: 0, y: 0, width: vw, height: mapHeight, fill: "#f7f7f8" });
  const graticuleG = createSVGEl("g", { fill: "none", stroke: BORDER_COLOR, "stroke-width": "0.65", "stroke-dasharray": "2.5 3.5" });
  PACIFIC_GRATICULES.meridians.forEach((m) => graticuleG.append(createSVGEl("path", { d: m.d })));
  PACIFIC_GRATICULES.parallels.forEach((p) => graticuleG.append(createSVGEl("path", { d: p.d })));
  const landPath = createSVGEl("path", { d: PACIFIC_LAND_PATH, fill: "#eaecef", stroke: "#d4d4d8", "stroke-width": "0.85", "stroke-linejoin": "round" });
  backdrop.append(ocean, graticuleG, landPath);

  const clusters = clusterTopologyNodes(model.nodes);
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
    { minX: 16, maxX: vw - 16, minY: 28, maxY: mapHeight - 16 },
    1,
    1,
    zoomTransform,
  );

  const probeLayer = createSVGEl("g", { class: "a-dash-topo-map-probes", "data-refresh-key": "map-layer-probes" });
  const pulseLayer = createSVGEl("g", { class: "a-dash-topo-map-pulses", "aria-hidden": "true", "data-refresh-key": "map-layer-pulses" });
  const beaconLayer = createSVGEl("g", { class: "a-dash-topo-map-beacons", "data-refresh-key": "map-layer-beacons" });
  const tagLayer = createSVGEl("g", { class: "a-dash-topo-map-tags", "data-refresh-key": "map-layer-tags" });
  const pillLayer = createSVGEl("g", { class: "a-dash-topo-map-pills", "data-refresh-key": "map-layer-pills" });

  const staticOccupied = [
    { x: 0, y: 0, w: 300, h: 30 },
    { x: vw - 380, y: 0, w: 380, h: 30 },
    ...((!zoomTransform || zoomTransform.scale <= 1.01) ? [{ x: vw - 188, y: 28, w: 178, h: 36 }] : []),
  ];
  clusters.forEach((c) => {
    const br = c.isCluster ? Math.min(11, 6.2 + c.members.length * 1.1) : 6.2;
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

  const tagBounds = { minX: 20, maxX: vw - 20, minY: 32, maxY: mapHeight - 12 };
  const hiddenClusterCount = [];

  tagPriority.forEach((cluster) => {
    const key = cluster.key;
    const peers = cluster.isCluster ? selIndex.clusterPeers(key) : selIndex.nodePeers(cluster.primary.node.id);
    const hl = helpers.isHighlightedEdge(peers);
    const active = helpers.activeKey() === key;
    const anyOnline = cluster.members.some((m) => m.node.isOnline);
    const anyIssue = cluster.members.some((m) => !m.node.isOnline || Boolean(m.node.issue));
    const r = cluster.isCluster ? Math.min(11, 6.2 + cluster.members.length * 1.1) : 5.8;

    const baseCity = cluster.isCluster
      ? `${cluster.label} ×${cluster.members.length}`
      : (cluster.primary.geo.matched ? cluster.label : formatMapCityLabel(cluster.primary.node.name, cluster.primary.node));
    const roleSuffix = singleMode && !cluster.isCluster && cluster.primary.node.role
      ? ` (${cluster.primary.node.role})`
      : "";
    const displayTag = clipLabel(`${baseCity}${roleSuffix}`, 18);
    const tagW = Math.max(44, Math.min(154, visualUnits(displayTag) * 6.4 + 14));
    const tagH = 19;

    const candidates = buildDensityAwareTagCandidates(cluster, clusters, tagW, tagH, r, tagBounds);
    let chosen = null;
    let bestScore = Infinity;
    for (const c of candidates) {
      const box = { x: c.x, y: c.y, w: tagW, h: tagH };
      if (staticOccupied.some((o) => rectsOverlap(box, o, 3, 2.5))) continue;
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
      opacity: hl === false ? "0.22" : "1",
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
        "stroke-width": "0.9",
        "stroke-dasharray": "2 2",
      }));
    }

    g.append(
      createSVGEl("rect", {
        x: chosen.x.toFixed(1),
        y: chosen.y.toFixed(1),
        width: tagW.toFixed(1),
        height: tagH,
        rx: 5,
        fill: "#ffffff",
        stroke: active ? INK_COLOR : anyIssue ? BAD_COLOR : anyOnline ? "#d4d4d8" : BORDER_COLOR,
        "stroke-width": active ? "1.6" : "1",
        class: "a-dash-topo-card",
      }),
      createSVGEl("text", {
        x: (chosen.x + 7).toFixed(1),
        y: (chosen.y + 13).toFixed(1),
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
    const r = cluster.isCluster ? Math.min(11, 6.2 + cluster.members.length * 1.1) : 5.8;

    const g = createSVGEl("g", {
      class: `a-dash-topo-node a-dash-topo-map-tag ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `map-beacon-${key}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      opacity: hl === false ? "0.22" : "1",
    });

    if (anyOnline && helpers.allowMotion()) {
      pulseLayer.append(createSVGEl("circle", {
        cx: cluster.x.toFixed(1),
        cy: cluster.y.toFixed(1),
        r: (r + 6.5).toFixed(1),
        fill: "none",
        stroke: toneColor,
        "stroke-width": "1.1",
        class: "a-dash-topo-beacon-pulse",
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "0.42",
        "data-dim-opacity": "0.08",
        opacity: hl === false ? "0.08" : "0.42",
      }));
    }

    g.append(createSVGEl("circle", { cx: cluster.x.toFixed(1), cy: cluster.y.toFixed(1), r: (r + 7).toFixed(1), fill: "transparent" }));
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
        y: (cluster.y + 3.4).toFixed(1),
        "text-anchor": "middle",
        class: "a-dash-topo-micro",
        fill: toneColor,
      }, String(cluster.members.length)));
    }
    g.append(createSVGEl("title", {}, cluster.members.map((m) => m.node.name).join(" / ")));
    helpers.bindInteractive(g, key);
    beaconLayer.append(g);
  });

  // Decide which pills stay visible natively: single-group mode or small slices show all; larger meshes highlight alerts first
  const showAllAmbient = singleMode || model.probeLinks.length <= 7;
  const ambientSet = showAllAmbient
    ? new Set(model.probeLinks.map((p) => p.id))
    : selectTopAmbientBadProbeIDs(model.probeLinks, { includeExternal: true, maxCount: 6 });

  const totalBadCount = model.probeLinks.filter((p) => p.tone === "bad" || p.tone === "warn").length;
  const shownAlertCount = model.probeLinks.filter((p) => (p.tone === "bad" || p.tone === "warn") && ambientSet.has(p.id)).length;
  const unshownAlertCount = Math.max(0, totalBadCount - shownAlertCount);

  const pillBounds = { minX: 20, maxX: vw - 20, minY: 32, maxY: mapHeight - 12 };
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
    const baseOpacity = singleMode ? 0.92 : (isBad ? 0.92 : isWarn ? 0.84 : probe.isExternal ? 0.5 : 0.48);
    const opacity = hl === true ? 1 : hl === false ? 0.14 : baseOpacity;

    const g = createSVGEl("g", {
      "data-refresh-key": `map-arc-${probe.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": String(baseOpacity),
      opacity: opacity.toFixed(2),
    });

    const strokeW = (hl === true ? 2.5 : singleMode ? 2.0 : isBad ? 2.0 : 1.5).toFixed(2);
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

    if (geom.seamMarks) {
      geom.seamMarks.forEach((mk) => {
        g.append(createSVGEl("path", {
          d: `M ${mk.x.toFixed(1)} ${(mk.y - 3.5).toFixed(1)} L ${(mk.x + 3.5).toFixed(1)} ${mk.y.toFixed(1)} L ${mk.x.toFixed(1)} ${(mk.y + 3.5).toFixed(1)} L ${(mk.x - 3.5).toFixed(1)} ${mk.y.toFixed(1)} Z`,
          fill: "#ffffff",
          stroke: pal.stroke,
          "stroke-width": "1.3",
        }));
      });
    }

    if (helpers.allowMotion() && !isProbeUnreachable(probe) && !probe.unavailableReverse && (probe.tone === "ok" || hl === true)) {
      const spd = flowSpeedClass(probe.latencyMS);
      geom.segments.forEach((seg, segIdx) => {
        const segD = `M ${seg.x1.toFixed(1)} ${seg.y1.toFixed(1)} Q ${seg.cx.toFixed(1)} ${seg.cy.toFixed(1)}, ${seg.x2.toFixed(1)} ${seg.y2.toFixed(1)}`;
        g.append(createSVGEl("path", {
          d: segD,
          fill: "none",
          stroke: pal.stroke,
          "stroke-width": "1.5",
          "stroke-dasharray": "4 16",
          opacity: "0.48",
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
    const fullLabel = buildProbePillLabel(probe, false, false);
    const pillLabel = (!singleMode && isAmbient && model.probeLinks.length > 7)
      ? buildProbePillLabel(probe, true, false)
      : fullLabel;
    const pillH = 20;
    const fullW = Math.max(58, Math.min(168, visualUnits(pillLabel) * 6.3 + 22));

    const pillOccupied = isAmbient ? staticOccupied : [...staticOccupied];
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

    pillLayer.append(helpers.createProbePillGroup({
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
    }));
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
        opacity: hl === false ? "0.24" : "1",
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
  stageClipGroup.append(zoomGroup);

  const hudLayer = createSVGEl("g", { class: "a-dash-topo-map-hud", "data-refresh-key": "map-layer-hud" });
  hudLayer.append(
    createSVGEl("text", { x: 16, y: 20, class: "a-dash-topo-map-coord" }, `158°E PACIFIC · ${clusters.length} 区域节点`),
    createSVGEl("text", { x: vw - 16, y: 20, "text-anchor": "end", class: "a-dash-topo-micro", fill: SOFT_INK_COLOR }, buildMapHudLatencyLine(model, false)),
  );
  if (unshownAlertCount > 0 || hiddenClusterCount.length > 0) {
    const parts = [];
    if (unshownAlertCount > 0) parts.push(`另有 ${unshownAlertCount} 项告警见下方监测分组台账`);
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
