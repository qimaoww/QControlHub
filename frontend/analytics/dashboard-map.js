import { createSVGEl } from "./dashboard-panel.js";
import { geoRegionDetails } from "../modules/region-model.js";
import {
  PACIFIC_GRATICULES,
  PACIFIC_LAND_PATH,
  PACIFIC_MAP_BOUNDS,
} from "./pacific-map-data.js";
import {
  BLOCKED_COLOR,
  EGRESS_COLOR,
  IDLE_FLOW_COLOR,
  INGRESS_COLOR,
  OK_COLOR,
  WARN_COLOR,
  buildPeerProbeTags,
  buildProbePillLabel,
  buildProbeShortAlertLabel,
  clipLabel,
  createTopologyDefs,
  formatMapCityLabel,
  formatMs,
  placeCollisionFreePill,
  quadPointAt,
  resolveNodeGeoPoint,
  selectTopAmbientBadProbeIDs,
  tonePalette,
  visualUnits,
} from "./dashboard-topo-shared.js";

let lastBeaconSig = "";
let lastBeaconCoords = [];
const backdropCache = new Map();

/**
 * Resolves and relaxes beacon coordinates tightly around real city locations, caching
 * converged coordinates by node ID/name/region signature so unchanged polls skip O(N^2) passes.
 */
function resolveAndRelaxBeacons(nodes) {
  const sig = (nodes || []).map((n) => `${n.id}\t${n.name || ""}\t${n.regionCode || ""}`).join("\n");
  if (sig && sig === lastBeaconSig && lastBeaconCoords.length === nodes.length) {
    return lastBeaconCoords.map((c, i) => ({
      node: nodes[i],
      geoX: c.geoX,
      geoY: c.geoY,
      x: c.x,
      y: c.y,
      matched: c.matched,
      source: c.source,
    }));
  }

  let unknownCounter = 0;
  const raw = nodes.map((node, index) => {
    const geo = resolveNodeGeoPoint(node, unknownCounter);
    if (!geo.matched) unknownCounter += 1;
    return { node, geoX: geo.x, geoY: geo.y, matched: geo.matched, source: geo.source, index };
  });

  const coordGroups = new Map();
  for (const item of raw) {
    const key = `${Math.round(item.geoX)}_${Math.round(item.geoY)}`;
    if (!coordGroups.has(key)) coordGroups.set(key, []);
    coordGroups.get(key).push(item);
  }

  const items = raw.map((item) => {
    const key = `${Math.round(item.geoX)}_${Math.round(item.geoY)}`;
    const group = coordGroups.get(key) || [item];
    const idx = group.indexOf(item);
    let seedX = item.geoX;
    let seedY = item.geoY;
    if (group.length > 1) {
      const stepAngle = ((idx * 2 * Math.PI) / group.length) + Math.PI / 6;
      const rad = Math.min(8.5, 5.2 + group.length * 0.8);
      seedX += Math.cos(stepAngle) * rad;
      seedY += Math.sin(stepAngle) * rad;
    }
    return {
      node: item.node,
      geoX: item.geoX,
      geoY: item.geoY,
      x: seedX,
      y: seedY,
      matched: item.matched,
      source: item.source,
    };
  });

  const minDist = 15.5;
  const springPull = 0.36;
  const maxDrift = 13.5;
  for (let iter = 0; iter < 32; iter += 1) {
    for (let i = 0; i < items.length; i += 1) {
      for (let j = i + 1; j < items.length; j += 1) {
        const a = items[i];
        const b = items[j];
        let dx = b.x - a.x;
        let dy = b.y - a.y;
        let dist = Math.hypot(dx, dy);
        if (dist < 0.01) {
          dx = 1;
          dy = 0.4;
          dist = Math.hypot(dx, dy);
        }
        if (dist < minDist) {
          const push = ((minDist - dist) / dist) * 0.48;
          a.x -= dx * push;
          a.y -= dy * push;
          b.x += dx * push;
          b.y += dy * push;
        }
      }
    }
    for (const item of items) {
      item.x += (item.geoX - item.x) * springPull;
      item.y += (item.geoY - item.y) * springPull;
      const dx = item.x - item.geoX;
      const dy = item.y - item.geoY;
      const drift = Math.hypot(dx, dy);
      if (drift > maxDrift) {
        item.x = item.geoX + (dx / drift) * maxDrift;
        item.y = item.geoY + (dy / drift) * maxDrift;
      }
      item.x = Math.max(46, Math.min(PACIFIC_MAP_BOUNDS.width - 46, item.x));
      item.y = Math.max(44, Math.min(PACIFIC_MAP_BOUNDS.height - 26, item.y));
    }
  }

  lastBeaconSig = sig;
  lastBeaconCoords = items.map((c) => ({
    geoX: c.geoX,
    geoY: c.geoY,
    x: c.x,
    y: c.y,
    matched: c.matched,
    source: c.source,
  }));
  return items;
}

/**
 * Groups co-located map beacons (e.g. multiple instances inside Hong Kong) into a single
 * city cluster with a composite `cluster:id1,id2,...` entityKey so hovering/pinning illuminates all members.
 */
function clusterCoLocatedBeacons(beacons, linkCountByNode) {
  const sorted = [...beacons].sort((a, b) => a.geoX - b.geoX || a.geoY - b.geoY);
  const clusters = [];
  for (const item of sorted) {
    const hostCluster = clusters.find((c) => Math.hypot(c.geoX - item.geoX, c.geoY - item.geoY) < 4.5);
    if (hostCluster) {
      hostCluster.members.push(item);
      const n = hostCluster.members.length;
      hostCluster.anchorX = ((hostCluster.anchorX * (n - 1)) + item.x) / n;
      hostCluster.anchorY = ((hostCluster.anchorY * (n - 1)) + item.y) / n;
    } else {
      clusters.push({
        geoX: item.geoX,
        geoY: item.geoY,
        anchorX: item.x,
        anchorY: item.y,
        members: [item],
      });
    }
  }

  return clusters.map((cluster) => {
    const scored = [...cluster.members].sort((a, b) => {
      const aLinks = linkCountByNode.get(a.node.id) || 0;
      const bLinks = linkCountByNode.get(b.node.id) || 0;
      if (bLinks !== aLinks) return bLinks - aLinks;
      const aHasSep = /[·|]/.test(String(a.node.name || "")) ? 1 : 0;
      const bHasSep = /[·|]/.test(String(b.node.name || "")) ? 1 : 0;
      if (bHasSep !== aHasSep) return bHasSep - aHasSep;
      const aLoad = (a.node.rxBPS || 0) + (a.node.txBPS || 0);
      const bLoad = (b.node.rxBPS || 0) + (b.node.txBPS || 0);
      return bLoad - aLoad;
    });
    const primary = scored[0];
    const cityFromSep = scored
      .map((m) => formatMapCityLabel(m.node.name, m.node))
      .find((lbl, idx) => /[·|]/.test(String(scored[idx].node.name || "")) && /[\u4e00-\u9fff]/.test(lbl));
    let cityBase = cityFromSep || formatMapCityLabel(primary.node.name, primary.node);
    if (cluster.members.length > 1 && !cityFromSep) {
      const rc = String(primary.node.regionCode || "").trim().toUpperCase();
      const region = rc ? geoRegionDetails(rc) : null;
      if (primary.source === "country" && region?.name) {
        cityBase = region.name;
      } else {
        cityBase = cityBase.replace(/-[a-z0-9]{1,3}$/i, "");
      }
    }
    const shortCity = clipLabel(cityBase, cluster.members.length > 1 ? 10 : 14);
    const label = cluster.members.length > 1 ? `${shortCity} ×${cluster.members.length}` : shortCity;
    const entityKey = cluster.members.length > 1
      ? `cluster:${cluster.members.map((m) => m.node.id).join(",")}`
      : `node:${primary.node.id}`;
    return {
      ...cluster,
      primary,
      entityKey,
      refreshID: cluster.members.length > 1 ? cluster.members.map((m) => m.node.id).join("-") : primary.node.id,
      x: cluster.anchorX,
      y: cluster.anchorY,
      label,
      isOnline: cluster.members.some((m) => m.node.isOnline),
      blocked: cluster.members.some((m) => m.node.blocked),
      engineCount: cluster.members.reduce((sum, m) => sum + (m.node.activeEngines?.length || 0), 0),
    };
  });
}

/**
 * Docks external public targets into a structured bottom rail (`公网目标`)
 * sorted horizontally by source node longitude so ocean views stay pristine while focus rays lock onto fixed ports.
 */
function placeExternalDockTargets(externalTargets, probeLinks, nodePosMap, dockArea) {
  const slots = new Map();
  if (!externalTargets.length) return { dockBox: null, slots };

  const ranked = externalTargets.map((ext) => {
    const linked = probeLinks.filter((p) => p.isExternal && p.to === ext.id);
    const pts = linked.map((p) => nodePosMap.get(p.from)).filter(Boolean);
    const avgSrcX = pts.length ? pts.reduce((s, p) => s + p.x, 0) / pts.length : dockArea.x + dockArea.w / 2;
    return { ext, linked, avgSrcX };
  }).sort((a, b) => a.avgSrcX - b.avgSrcX || a.ext.address.localeCompare(b.ext.address));

  const titleW = 84;
  const padRight = 16;
  const chipGap = 10;
  const chipAreaX = dockArea.x + titleW;
  const chipAreaW = Math.max(120, dockArea.w - titleW - padRight);
  const chipW = Math.min(196, Math.max(76, Math.floor((chipAreaW - (ranked.length - 1) * chipGap) / ranked.length)));
  const totalChipsW = ranked.length * chipW + Math.max(0, ranked.length - 1) * chipGap;
  const startX = chipAreaX + Math.max(0, Math.round((chipAreaW - totalChipsW) / 2));
  const chipH = 24;
  const chipY = dockArea.y + Math.round((dockArea.h - chipH) / 2);

  ranked.forEach((entry, idx) => {
    const chipX = startX + idx * (chipW + chipGap);
    const portX = chipX + chipW / 2;
    const portY = chipY;
    slots.set(entry.ext.id, {
      ext: entry.ext,
      linked: entry.linked,
      x: portX,
      y: portY,
      chipX,
      chipY,
      chipW,
      chipH,
      portY,
    });
  });

  return { dockBox: dockArea, slots };
}

/**
 * Classifies probes into corridor lanes so parallel pairs and trans-Pacific cables
 * separate cleanly across dedicated Pacific ocean altitudes instead of colliding.
 */
function buildArcLanes(probeLinks, posLookup) {
  const pairBuckets = new Map();
  const transPacific = [];

  for (const probe of probeLinks) {
    const pA = posLookup.get(probe.from);
    const pB = posLookup.get(probe.to);
    if (!pA || !pB) continue;

    const key = [probe.from, probe.to].sort().join("|");
    if (!pairBuckets.has(key)) pairBuckets.set(key, []);
    pairBuckets.get(key).push(probe);

    const minX = Math.min(pA.x, pB.x);
    const maxX = Math.max(pA.x, pB.x);
    if (!probe.isExternal && minX >= 400 && minX <= 575 && maxX >= 750 && maxX <= 980) {
      transPacific.push({ probe, midY: (pA.y + pB.y) / 2 });
    }
  }

  transPacific.sort((a, b) => a.midY - b.midY || a.probe.id.localeCompare(b.probe.id));
  const pacificSlotByID = new Map();
  transPacific.forEach((entry, idx) => {
    pacificSlotByID.set(entry.probe.id, { idx, count: transPacific.length });
  });

  const laneInfo = new Map();
  pairBuckets.forEach((group) => {
    group.forEach((probe, idx) => {
      laneInfo.set(probe.id, {
        lane: idx,
        total: group.length,
        pacificSlot: pacificSlotByID.get(probe.id) || null,
      });
    });
  });
  return laneInfo;
}

/**
 * Builds an organically bowed quadratic Bezier arc aligned to real Pacific ocean and Eurasia corridors.
 */
function buildMapArcGeometry(pA, pB, laneMeta, isExternal = false) {
  const dx = pB.x - pA.x;
  const dy = pB.y - pA.y;
  const len = Math.max(8, Math.hypot(dx, dy));
  const nx = -dy / len;
  const ny = dx / len;
  const midX = (pA.x + pB.x) / 2;
  const midY = (pA.y + pB.y) / 2;

  const laneOffset = laneMeta?.lane || 0;
  const totalLanes = laneMeta?.total || 1;
  const centered = laneOffset - (totalLanes - 1) / 2;

  let sag;
  const pacificSlot = laneMeta?.pacificSlot;
  if (isExternal) {
    const baseCurve = Math.min(44, Math.max(14, len * 0.08)) + centered * 14;
    const towardOceanSign = midX < 650 ? 1 : -1;
    const sign = nx !== 0 ? (nx * towardOceanSign >= 0 ? 1 : -1) : 1;
    sag = baseCurve * sign;
  } else if (pacificSlot) {
    const pattern = [-58, 22, -26, 48];
    const chosen = pattern[pacificSlot.idx % pattern.length];
    const upSign = ny < 0 ? 1 : -1;
    sag = chosen * upSign + centered * 20;
  } else if (len < 20) {
    const seScore = nx * 0.58 + ny * 0.81;
    const outSign = seScore >= 0 ? 1 : -1;
    sag = outSign * (24 + Math.abs(centered) * 9);
  } else if (pA.x >= 395 && pA.x <= 550 && pB.x >= 395 && pB.x <= 550 && pA.y >= 160 && pA.y <= 345 && pB.y >= 160 && pB.y <= 345) {
    if (len > 125) {
      sag = (nx >= 0 ? 34 : -34) + centered * 16;
    } else if (len > 62) {
      sag = (nx <= 0 ? 18 : -18) + centered * 14;
    } else {
      sag = (ny <= 0 ? 7 : -7) + centered * 10;
    }
  } else if (midX > 430 && midX < 540 && dy > 95 && dx > 95) {
    sag = (nx < 0 ? 28 : -28) + centered * 18;
  } else if (len < 36) {
    sag = (ny <= 0 ? 1 : -1) * Math.max(5.5, len * 0.24) + centered * 9;
  } else {
    const baseArc = Math.min(62, Math.max(12, len * 0.14));
    const poleSign = midY < 245 ? (ny <= 0 ? 1 : -1) : (ny >= 0 ? 1 : -1);
    sag = baseArc * poleSign + centered * 22;
  }

  const cx = Math.max(32, Math.min(PACIFIC_MAP_BOUNDS.width - 32, midX + nx * sag));
  const cy = Math.max(24, Math.min(isExternal ? 546 : 538, midY + ny * sag));

  const trimA = len < 20 ? 4.2 : 7.4;
  const trimB = isExternal ? 4.5 : (len < 20 ? 4.6 : 8.2);
  const distA = Math.max(trimA + 1, Math.hypot(cx - pA.x, cy - pA.y));
  const distB = Math.max(trimB + 1, Math.hypot(cx - pB.x, cy - pB.y));
  const tStartX = pA.x + (cx - pA.x) * (trimA / distA);
  const tStartY = pA.y + (cy - pA.y) * (trimA / distA);
  const tEndX = pB.x + (cx - pB.x) * (trimB / distB);
  const tEndY = pB.y + (cy - pB.y) * (trimB / distB);

  const d = `M ${tStartX.toFixed(1)} ${tStartY.toFixed(1)} Q ${cx.toFixed(1)} ${cy.toFixed(1)}, ${tEndX.toFixed(1)} ${tEndY.toFixed(1)}`;
  return {
    d,
    pointAt: (t) => quadPointAt(tStartX, tStartY, cx, cy, tEndX, tEndY, t),
  };
}

/** Draws a crisp SVG cluster/city tag pill beside its geographic beacon(s) with clean callout stem. */
function buildNodeTagGroup({ entityKey, refreshID, cx, cy, tagW, tagH, label, isOnline, blocked, engineCount, active, hl, peers, anchorX, anchorY, tooltip, bindInteractive }) {
  const g = createSVGEl("g", {
    class: `a-dash-topo-node a-dash-topo-map-tag ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
    "data-refresh-key": `map-tag-${refreshID}`,
    "data-topo-peers": (peers || [entityKey]).join(" "),
    "data-base-opacity": "1",
    opacity: hl === false ? "0.26" : "1",
  });
  if (tooltip) g.append(createSVGEl("title", {}, tooltip));

  const vx = cx - anchorX;
  const vy = cy - anchorY;
  const centerDist = Math.hypot(vx, vy);
  const halfW = tagW / 2;
  const halfH = tagH / 2;
  const edgeDx = Math.max(0, Math.abs(vx) - halfW);
  const edgeDy = Math.max(0, Math.abs(vy) - halfH);
  const gapDist = Math.hypot(edgeDx, edgeDy);
  if (centerDist > 7.5 && gapDist > 2.5) {
    const startX = anchorX + (vx / centerDist) * 6.6;
    const startY = anchorY + (vy / centerDist) * 6.6;
    g.append(createSVGEl("line", {
      x1: startX.toFixed(1),
      y1: startY.toFixed(1),
      x2: cx.toFixed(1),
      y2: cy.toFixed(1),
      stroke: active ? INGRESS_COLOR : "#a1a1aa",
      "stroke-width": active || hl === true ? "1.3" : "0.95",
      opacity: "0.82",
    }));
  }

  const fill = blocked ? "#fff2f3" : "#ffffff";
  const border = active ? INGRESS_COLOR : blocked ? "#f2cdd2" : isOnline ? "#d4d4d8" : "#e4e4e7";
  g.append(createSVGEl("rect", {
    x: (cx - tagW / 2).toFixed(1),
    y: (cy - tagH / 2).toFixed(1),
    width: tagW.toFixed(1),
    height: tagH,
    rx: "6",
    fill,
    stroke: border,
    "stroke-width": active || hl === true ? "1.6" : "1",
    class: "a-dash-topo-card",
  }));

  const dotColor = blocked ? BLOCKED_COLOR : !isOnline ? IDLE_FLOW_COLOR : OK_COLOR;
  g.append(createSVGEl("circle", {
    cx: (cx - tagW / 2 + 8).toFixed(1),
    cy: cy.toFixed(1),
    r: "2.8",
    fill: dotColor,
  }));

  g.append(createSVGEl("text", {
    x: (cx - tagW / 2 + 14).toFixed(1),
    y: (cy + 3.4).toFixed(1),
    class: "a-dash-topo-map-tag-text",
    fill: isOnline ? "#18181b" : "#71717a",
  }, label));

  bindInteractive(g, entityKey);
  return g;
}

function buildStaticBackdropTemplates({ frameY = 0, viewH = 548 } = {}) {
  const w = PACIFIC_MAP_BOUNDS.width;
  const clip = createSVGEl("clipPath", { id: "a-topo-map-rect-clip", "data-refresh-key": "map-rect-clip" });
  clip.append(createSVGEl("rect", { x: "0", y: "0", width: String(w), height: String(viewH) }));

  const backdrop = createSVGEl("g", {
    "data-refresh-key": "map-backdrop",
    transform: frameY ? `translate(0 ${frameY})` : null,
  });

  backdrop.append(createSVGEl("rect", {
    x: "0",
    y: "0",
    width: String(w),
    height: String(viewH),
    fill: "#fafafa",
  }));

  const graticuleG = createSVGEl("g", { "clip-path": "url(#a-topo-map-rect-clip)" });
  PACIFIC_GRATICULES.parallels.forEach((p, idx) => {
    graticuleG.append(createSVGEl("path", {
      d: p.d,
      fill: "none",
      stroke: p.lat === 0 ? "#e4e4e7" : "#f0f0f2",
      "stroke-width": p.lat === 0 ? "0.85" : "0.7",
      "stroke-dasharray": p.lat === 0 ? "5 4" : "2.5 4.5",
      opacity: p.lat === 0 ? "0.55" : "0.45",
      "data-refresh-key": `lat-${idx}`,
    }));
  });
  PACIFIC_GRATICULES.meridians.forEach((m, idx) => {
    graticuleG.append(createSVGEl("path", {
      d: m.d,
      fill: "none",
      stroke: m.lon === 180 ? "#e4e4e7" : "#f0f0f2",
      "stroke-width": m.lon === 180 ? "0.85" : "0.7",
      "stroke-dasharray": "2.5 4.5",
      opacity: m.lon === 180 ? "0.55" : "0.45",
      "data-refresh-key": `lon-${idx}`,
    }));
  });

  graticuleG.append(createSVGEl("path", {
    d: PACIFIC_LAND_PATH,
    fill: "#e8e8ec",
    stroke: "#d4d4d8",
    "stroke-width": "0.8",
    "stroke-linejoin": "round",
  }));

  backdrop.append(graticuleG);
  return { clip, backdrop };
}

/** Reuses cached static Natural Earth Pacific backdrop clones and bypasses diff scans on unchanged subtrees. */
function appendPacificBackdrop(svg, defs, { frameY = 0, viewH = 548 } = {}) {
  const key = `${frameY}_${viewH}`;
  let tpl = backdropCache.get(key);
  if (!tpl) {
    tpl = buildStaticBackdropTemplates({ frameY, viewH });
    backdropCache.set(key, tpl);
  }
  const clip = tpl.clip.cloneNode(true);
  const backdrop = tpl.backdrop.cloneNode(true);
  clip.reconcileTable = () => true;
  backdrop.reconcileTable = () => true;
  defs.append(clip);
  svg.append(backdrop);
}

/**
 * Generates latitude-ordered, sector-separated candidates around each city cluster so dense regions
 * (Europe, East Asia, North America) fan outward without crossing callout leader stems.
 */
function buildCityTagCandidates(cluster, allClusters, tagW, tagH, bounds) {
  const { x, y } = cluster;
  const localClusters = allClusters.filter((c) => c !== cluster && Math.hypot(c.x - x, c.y - y) < 38);
  const allLocal = [cluster, ...localClusters].sort((a, b) => a.y - b.y || a.x - b.x);
  const clusterSize = allLocal.length;
  const latRank = allLocal.indexOf(cluster);
  const clusterCx = allLocal.reduce((s, c) => s + c.x, 0) / clusterSize;
  const clusterCy = allLocal.reduce((s, c) => s + c.y, 0) / clusterSize;
  const outDeg = localClusters.length > 0 ? (Math.atan2(y - clusterCy, x - clusterCx) * 180) / Math.PI : null;

  let baseAngles;
  if (clusterSize >= 2) {
    const relX = x - clusterCx;
    const relY = y - clusterCy;
    if (latRank === 0 && (clusterSize === 2 || relY <= -1.5)) {
      baseAngles = relX < -1 ? [-115, -145, -80, 175, -45] : [-85, -55, -120, 10, -155];
    } else if (latRank === clusterSize - 1 && (clusterSize === 2 || relY >= 1.5)) {
      baseAngles = relX < -1 ? [115, 145, 80, 175, 45] : [85, 55, 120, 10, 155];
    } else if (relX <= 0) {
      baseAngles = [175, -150, 145, -120, 110, 180];
    } else {
      baseAngles = [5, -30, 35, -65, 70, 0];
    }
  } else if (x < 235 && y < 220) {
    baseAngles = [-145, 165, -75, 15, -110, 105, 180, -25];
  } else if (x < 235 && y >= 360) {
    baseAngles = [0, -40, 40, -90, 90, 180];
  } else if (x >= 235 && x < 305) {
    baseAngles = [180, -135, -90, 135, 0, 90];
  } else if (x >= 305 && x < 385) {
    baseAngles = [95, 145, 25, -85, 180, 55];
  } else if (x >= 385 && x < 435 && y >= 285) {
    baseAngles = [65, 110, 145, -140, 175];
  } else if (x >= 435 && x < 484 && y >= 205 && y < 285) {
    baseAngles = [15, 55, -20, -90, 125, 175];
  } else if (x >= 480 && x < 514 && y >= 150 && y < 205) {
    baseAngles = [-95, -60, -130, 170, 125, -20];
  } else if (x >= 514 && x < 580 && y >= 155 && y < 225) {
    baseAngles = [5, -30, 35, -70, 75, 145];
  } else if (x >= 500 && x < 640 && y >= 390) {
    baseAngles = [0, -45, -90, 180, -135, 30];
  } else if (x >= 760 && x < 865 && y < 240) {
    baseAngles = [-130, -90, 175, 85, 40, -45];
  } else if (x >= 865 && y < 240) {
    baseAngles = [0, -55, 45, -110, 180, 120];
  } else if (x >= 940 && y >= 300) {
    baseAngles = [175, -145, 140, -100, -175, 105];
  } else {
    baseAngles = [0, -90, 180, 90, -45, 45, -135, 135];
  }

  const normDiff = (a, b) => Math.abs(((a - b + 540) % 360) - 180);
  const orderedAngles = clusterSize <= 1 && outDeg != null && (x < 225 || x >= 760)
    ? [...baseAngles].sort((a, b) => normDiff(a, outDeg) - normDiff(b, outDeg))
    : baseAngles;

  const sweep = [-180, -150, -120, -90, -60, -30, 0, 30, 60, 90, 120, 150];
  const allAngles = [...orderedAngles, ...sweep.filter((a) => !orderedAngles.includes(a))];
  const gaps = [11, 17, 24, 33, 44];
  const minCenterX = bounds.minX + tagW / 2;
  const maxCenterX = bounds.maxX - tagW / 2;
  const minCenterY = bounds.minY + tagH / 2;
  const maxCenterY = bounds.maxY - tagH / 2;
  const maxReach = Math.max(tagW / 2 + 46, 84);
  const candidates = [];

  for (const gap of gaps) {
    for (const deg of allAngles) {
      const rad = (deg * Math.PI) / 180;
      const cx = Math.max(minCenterX, Math.min(maxCenterX, x + Math.cos(rad) * (tagW / 2 + gap)));
      const cy = Math.max(minCenterY, Math.min(maxCenterY, y + Math.sin(rad) * (tagH / 2 + gap)));
      if (Math.hypot(cx - x, cy - y) <= maxReach) {
        candidates.push({ x: cx, y: cy });
      }
    }
  }
  return candidates.length ? candidates : [{ x: Math.max(minCenterX, Math.min(maxCenterX, x)), y: Math.max(minCenterY, Math.min(maxCenterY, y)) }];
}

function selectMotionProbeIDs(probeLinks, maxCount) {
  const candidates = probeLinks.filter((p) => !p.isExternal && p.tone !== "bad");
  candidates.sort((a, b) => {
    const score = (p) => (p.running ? 3 : p.tone === "warn" ? 2 : 1);
    const diff = score(b) - score(a);
    if (diff !== 0) return diff;
    return String(a.id || "").localeCompare(String(b.id || ""));
  });
  return new Set(candidates.slice(0, maxCount).map((p) => p.id));
}

/**
 * Renders the Pacific-centered World Map topology SVG for both desktop and compact mobile views,
 * featuring cached globe backdrop, ambient fault callouts + focus-only full route pills, and 公网探测坞.
 */
export function renderPacificMapSVG({ isMobile, model, bytes, helpers }) {
  const {
    allowMotion,
    activeKey,
    isHighlightedEdge,
    bindInteractive,
    createProbePillGroup,
    appendProbeLeader,
  } = helpers;

  const peerProbesByNode = buildPeerProbeTags(model);
  const externalByNode = new Map();
  const linkCountByNode = new Map();
  for (const probe of model.probeLinks) {
    linkCountByNode.set(probe.from, (linkCountByNode.get(probe.from) || 0) + 1);
    if (!probe.isExternal) {
      linkCountByNode.set(probe.to, (linkCountByNode.get(probe.to) || 0) + 1);
    } else {
      if (!externalByNode.has(probe.from)) externalByNode.set(probe.from, new Set());
      externalByNode.get(probe.from).add(probe.to);
    }
  }

  const beacons = resolveAndRelaxBeacons(model.nodes);
  const posLookup = new Map();
  beacons.forEach((b) => posLookup.set(b.node.id, { x: b.x, y: b.y, type: "node", data: b }));

  const width = PACIFIC_MAP_BOUNDS.width;
  const mapOffsetY = 34;
  const mapViewH = 548;
  const dockArea = { x: 0, y: mapViewH, w: width, h: 42 };
  const dock = placeExternalDockTargets(model.externalTargets, model.probeLinks, posLookup, dockArea);
  dock.slots.forEach((slot, id) => posLookup.set(id, { x: slot.x, y: slot.y, type: "ext", data: slot }));
  const height = mapOffsetY + mapViewH + (dock.dockBox ? dockArea.h : 0);

  if (isMobile) {
    return renderMobilePacificMap({
      model,
      beacons,
      dock,
      posLookup,
      peerProbesByNode,
      externalByNode,
      bytes,
      helpers,
    });
  }

  const svg = createSVGEl("svg", {
    viewBox: `0 0 ${width} ${height}`,
    width: "100%",
    class: "a-dash-topo-svg a-dash-topo-map-svg",
    "data-refresh-key": "topo-svg-map",
    "data-topo-mode": "map",
    role: "img",
    "aria-label": "太平洋中心全网节点与探测拓扑地图",
  });

  const defs = createTopologyDefs();
  svg.append(defs);

  const staticOccupied = [];
  const totalRateRX = bytes(model.ingress.rateBPS);
  const totalRateTX = bytes(model.egress.rateBPS);
  const hudG = createSVGEl("g", { "data-refresh-key": "map-hud" });
  hudG.append(
    createSVGEl("rect", { x: "0", y: "0", width: String(width), height: String(mapOffsetY), fill: "#f4f4f5" }),
    createSVGEl("line", { x1: "0", y1: String(mapOffsetY), x2: String(width), y2: String(mapOffsetY), stroke: "#e4e4e7", "stroke-width": "1" }),
    createSVGEl("circle", { cx: "20", cy: "17", r: "3", fill: INGRESS_COLOR }),
    createSVGEl("text", { x: "28", y: "20.5", "font-size": "10.5", class: "a-dash-topo-micro", fill: "#18181b" }, `入站接收 ↓ ${totalRateRX}/s · 本月 ${bytes(model.ingress.monthlyBytes)}`),
    createSVGEl("line", { x1: "252", y1: "10", x2: "252", y2: "24", stroke: "#e4e4e7", "stroke-width": "1" }),
    createSVGEl("circle", { cx: "268", cy: "17", r: "3", fill: EGRESS_COLOR }),
    createSVGEl("text", { x: "276", y: "20.5", "font-size": "10.5", class: "a-dash-topo-micro", fill: "#52525b" }, `出站发送 ↑ ${totalRateTX}/s · 本月 ${bytes(model.egress.monthlyBytes)}`),
    createSVGEl("circle", { cx: String(width - 180), cy: "17", r: "3", fill: OK_COLOR }),
    createSVGEl("text", { x: String(width - 173), y: "20.5", "font-size": "10.5", class: "a-dash-topo-micro", fill: "#52525b" }, "正常"),
    createSVGEl("circle", { cx: String(width - 134), cy: "17", r: "3", fill: WARN_COLOR }),
    createSVGEl("text", { x: String(width - 127), y: "20.5", "font-size": "10.5", class: "a-dash-topo-micro", fill: "#52525b" }, "波动"),
    createSVGEl("circle", { cx: String(width - 88), cy: "17", r: "3", fill: BLOCKED_COLOR }),
    createSVGEl("text", { x: String(width - 81), y: "20.5", "font-size": "10.5", class: "a-dash-topo-micro", fill: "#52525b" }, "异常"),
    createSVGEl("circle", { cx: String(width - 42), cy: "17", r: "3", fill: IDLE_FLOW_COLOR }),
    createSVGEl("text", { x: String(width - 35), y: "20.5", "font-size": "10.5", class: "a-dash-topo-micro", fill: "#71717a" }, "离线"),
  );

  const mapViewportG = createSVGEl("g", {
    "data-refresh-key": "map-viewport",
    transform: `translate(0 ${mapOffsetY})`,
  });
  appendPacificBackdrop(mapViewportG, defs, { viewH: mapViewH });

  if (dock.dockBox) staticOccupied.push(dock.dockBox);
  beacons.forEach((b) => {
    staticOccupied.push({ x: b.x - 6.8, y: b.y - 6.8, w: 13.6, h: 13.6 });
  });

  const geoTetherLayer = createSVGEl("g", { "data-refresh-key": "map-tethers" });
  const arcLayer = createSVGEl("g", { "data-refresh-key": "map-arcs" });
  const beaconLayer = createSVGEl("g", { "data-refresh-key": "map-beacons" });
  const dockLayer = createSVGEl("g", { "data-refresh-key": "map-dock" });
  const tagLayer = createSVGEl("g", { "data-refresh-key": "map-tags" });
  const leaderLayer = createSVGEl("g", { "data-refresh-key": "map-leaders" });
  const pillLayer = createSVGEl("g", { "data-refresh-key": "map-pills" });

  // 1. Cross-ocean mesh arcs (visible in overview) + focus-only public target rays
  const laneInfo = buildArcLanes(model.probeLinks, posLookup);
  const arcGeometry = new Map();
  const motionProbeIDs = allowMotion() ? selectMotionProbeIDs(model.probeLinks, 6) : new Set();

  for (const probe of model.probeLinks) {
    const pA = posLookup.get(probe.from);
    const pB = posLookup.get(probe.to);
    if (!pA || !pB) continue;

    const laneMeta = laneInfo.get(probe.id) || { lane: 0, total: 1 };
    const geom = buildMapArcGeometry(pA, pB, laneMeta, probe.isExternal);
    arcGeometry.set(probe.id, geom);

    const peers = [`probe:${probe.id}`, `node:${probe.from}`, probe.to];
    const hl = isHighlightedEdge(peers);
    const pal = tonePalette(probe.tone);
    const isWarnOrBad = probe.tone === "warn" || probe.tone === "bad";

    let baseStr;
    let dimStr;
    let curOpacity;
    if (probe.isExternal) {
      baseStr = "0";
      dimStr = "0";
      curOpacity = hl === true ? "1" : "0";
    } else {
      const defOp = probe.tone === "bad" ? 0.86 : probe.tone === "warn" ? 0.78 : 0.42;
      baseStr = defOp.toFixed(2);
      dimStr = "0.14";
      curOpacity = hl === true ? "1" : hl === false ? "0.14" : baseStr;
    }
    const edgeW = hl === true ? 2.4 : isWarnOrBad ? 1.75 : 1.25;

    const g = createSVGEl("g", {
      class: probe.isExternal ? "a-dash-topo-focus-only" : null,
      "data-refresh-key": `map-arc-${probe.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": baseStr,
      "data-dim-opacity": dimStr,
      opacity: curOpacity,
    });
    g.append(createSVGEl("title", {}, `${probe.name}: ${probe.metricLabel} (${probe.statusLabel}) — 点击打开链路监控`));

    g.append(createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: "#fafafa",
      "stroke-width": (edgeW + 2.0).toFixed(1),
      "stroke-linecap": "round",
      opacity: "0.72",
    }));

    const markerName = probe.tone === "ok"
      ? "url(#a-topo-arrow-ok)"
      : probe.tone === "warn"
        ? "url(#a-topo-arrow-warn)"
        : probe.tone === "bad"
          ? "url(#a-topo-arrow-bad)"
          : "url(#a-topo-arrow-idle)";

    g.append(createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: pal.stroke,
      "stroke-width": edgeW.toFixed(2),
      "stroke-linecap": "round",
      "stroke-dasharray": probe.tone === "bad" ? "5 4" : probe.running ? "4 4" : null,
      "marker-end": markerName,
      "marker-start": probe.bidirectional && probe.tone !== "bad" ? markerName : null,
    }));

    if (motionProbeIDs.has(probe.id)) {
      const dur = probe.tone === "ok" ? "2.5s" : "3.2s";
      const photon = createSVGEl("circle", {
        r: hl === true ? "2.5" : "2.0",
        fill: "#ffffff",
        stroke: pal.stroke,
        "stroke-width": "1.25",
        opacity: "0.95",
      });
      photon.append(createSVGEl("animateMotion", { path: geom.d, dur, repeatCount: "indefinite" }));
      g.append(photon);
    }

    bindInteractive(g, `probe:${probe.id}`, { openProbeID: probe.id });
    arcLayer.append(g);
  }

  // 2. Individual geographic node beacons
  for (const item of beacons) {
    const { node, x, y, geoX, geoY } = item;
    const entityKey = `node:${node.id}`;
    const probePeers = peerProbesByNode.get(node.id) || [];
    const extPeers = Array.from(externalByNode.get(node.id) || []);
    const peers = [entityKey, ...probePeers, ...extPeers];
    const hl = isHighlightedEdge(peers);
    const active = activeKey() === entityKey;
    const engineCount = node.activeEngines.length;
    const coreColor = node.blocked ? BLOCKED_COLOR : !node.isOnline ? IDLE_FLOW_COLOR : OK_COLOR;

    if (Math.hypot(x - geoX, y - geoY) > 4) {
      geoTetherLayer.append(
        createSVGEl("line", {
          x1: geoX.toFixed(1),
          y1: geoY.toFixed(1),
          x2: x.toFixed(1),
          y2: y.toFixed(1),
          stroke: "#a1a1aa",
          "stroke-width": "0.85",
          "stroke-dasharray": "1.5 1.5",
          opacity: "0.65",
        }),
        createSVGEl("circle", { cx: geoX.toFixed(1), cy: geoY.toFixed(1), r: "1.5", fill: "#71717a", opacity: "0.7" }),
      );
    }

    const g = createSVGEl("g", {
      class: `a-dash-topo-node a-dash-topo-beacon ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `map-beacon-${node.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      opacity: hl === false ? "0.24" : "1",
    });
    const tipParts = [node.name, node.isOnline ? "在线" : "离线"];
    if (node.rxBPS || node.txBPS) tipParts.push(`↓${bytes(node.rxBPS)}/s ↑${bytes(node.txBPS)}/s`);
    g.append(createSVGEl("title", {}, tipParts.join(" · ")));

    if ((active || hl === true || node.blocked) && allowMotion()) {
      g.append(createSVGEl("circle", {
        cx: x.toFixed(1),
        cy: y.toFixed(1),
        r: "6.4",
        fill: "none",
        stroke: coreColor,
        "stroke-width": "0.85",
        class: "a-dash-topo-beacon-pulse",
      }));
    }

    g.append(createSVGEl("circle", {
      cx: x.toFixed(1),
      cy: y.toFixed(1),
      r: active || hl === true ? "8.0" : "5.8",
      fill: coreColor,
      opacity: active || hl === true ? "0.22" : "0.14",
    }));

    g.append(createSVGEl("circle", {
      cx: x.toFixed(1),
      cy: y.toFixed(1),
      r: active || hl === true ? "4.4" : "3.5",
      fill: coreColor,
      stroke: active ? INGRESS_COLOR : "#ffffff",
      "stroke-width": active ? "1.8" : "1.5",
    }));

    bindInteractive(g, entityKey);
    beaconLayer.append(g);
  }

  // 3. Docked Public Target Rail (`公网目标`) across full-width footer bar
  if (dock.dockBox) {
    const db = dock.dockBox;
    dockLayer.append(
      createSVGEl("rect", {
        x: db.x,
        y: db.y,
        width: db.w,
        height: db.h,
        fill: "#f4f4f5",
      }),
      createSVGEl("line", {
        x1: db.x,
        y1: db.y,
        x2: db.x + db.w,
        y2: db.y,
        stroke: "#e4e4e7",
        "stroke-width": "1",
      }),
      createSVGEl("text", {
        x: db.x + 16,
        y: db.y + 25,
        class: "a-dash-topo-ext-kicker",
        fill: "#52525b",
      }, "公网目标"),
    );

    for (const [extID, slot] of dock.slots.entries()) {
      const linkedProbes = slot.linked;
      const hasBad = linkedProbes.some((p) => p.tone === "bad");
      const hasWarn = linkedProbes.some((p) => p.tone === "warn");
      const statusColor = hasBad ? BLOCKED_COLOR : hasWarn ? WARN_COLOR : OK_COLOR;
      const peers = [
        extID,
        ...linkedProbes.map((p) => `probe:${p.id}`),
        ...linkedProbes.map((p) => `node:${p.from}`),
      ];
      const hl = isHighlightedEdge(peers);
      const active = activeKey() === extID;

      const reachedLatencies = linkedProbes
        .filter((p) => p.latencyMS != null && !(p.lossPercent >= 100 && p.latencyMS === 0))
        .map((p) => p.latencyMS);
      const bestLatency = reachedLatencies.length ? Math.min(...reachedLatencies) : null;
      const summaryText = hasBad && bestLatency == null
        ? "不可达"
        : bestLatency != null
          ? formatMs(bestLatency)
          : `${linkedProbes.length} 链路`;
      const summaryColor = hasBad ? BLOCKED_COLOR : hasWarn ? WARN_COLOR : "#71717a";
      const summaryW = slot.chipW >= 136 ? Math.round(visualUnits(summaryText) * 5.8 + 8) : 0;
      const ipAvailW = slot.chipW - 22 - summaryW - (summaryW ? 6 : 4);

      const g = createSVGEl("g", {
        class: `a-dash-topo-node a-dash-topo-dock-chip ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
        "data-refresh-key": `map-dock-${extID}`,
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "1",
        opacity: hl === false ? "0.28" : "1",
      });
      g.append(createSVGEl("title", {}, `公网目标 · ${slot.ext.address} (${linkedProbes.length} 条探测链路)`));

      g.append(
        createSVGEl("rect", {
          x: slot.chipX,
          y: slot.chipY,
          width: slot.chipW,
          height: slot.chipH,
          rx: "6",
          fill: "#ffffff",
          stroke: active ? INGRESS_COLOR : "#d4d4d8",
          "stroke-width": active || hl === true ? "1.6" : "1",
          class: "a-dash-topo-card",
        }),
        createSVGEl("circle", {
          cx: (slot.chipX + 10).toFixed(1),
          cy: (slot.chipY + 12).toFixed(1),
          r: "2.8",
          fill: statusColor,
        }),
        createSVGEl("text", {
          x: slot.chipX + 17,
          y: slot.chipY + 15.5,
          "font-size": "10.5",
          class: "a-dash-topo-ext-sub",
          fill: "#18181b",
        }, clipLabel(slot.ext.address, Math.max(8, Math.floor(ipAvailW / 6.3)))),
      );
      if (summaryW > 0) {
        g.append(createSVGEl("text", {
          x: slot.chipX + slot.chipW - 8,
          y: slot.chipY + 15.2,
          "text-anchor": "end",
          class: "a-dash-topo-micro",
          "font-size": "9.8",
          fill: summaryColor,
        }, summaryText));
      }

      bindInteractive(g, extID);
      dockLayer.append(g);
    }
  }

  // 4. Clustered compact city tags (`香港 ×3` with non-crossing latitude-ordered callout stems)
  const clusters = clusterCoLocatedBeacons(beacons, linkCountByNode);
  const clusterKeyByNodeID = new Map();
  for (const cluster of clusters) {
    if (cluster.members.length > 1) {
      cluster.members.forEach((m) => clusterKeyByNodeID.set(m.node.id, cluster.entityKey));
    }
  }

  const tagBounds = { minX: 16, maxX: width - 16, minY: 14, maxY: 534 };
  const placedTagLeaders = [];
  const sortedClusters = [...clusters].sort((a, b) => a.y - b.y || visualUnits(a.label) - visualUnits(b.label));

  for (const cluster of sortedClusters) {
    const tagW = Math.max(50, Math.round(visualUnits(cluster.label) * 6.3 + 20));
    const tagH = 19;
    const candidates = buildCityTagCandidates(cluster, clusters, tagW, tagH, tagBounds);

    const placed = placeCollisionFreePill(
      candidates[0].x,
      candidates[0].y,
      tagW,
      tagH,
      staticOccupied,
      tagBounds,
      candidates,
      { padX: 3, padY: 2.5, leaderPoints: [{ x: cluster.x, y: cluster.y }], leaderSegments: placedTagLeaders },
    );

    const peerSet = new Set([cluster.entityKey]);
    const tipLines = [];
    for (const member of cluster.members) {
      const m = member.node;
      peerSet.add(`node:${m.id}`);
      (peerProbesByNode.get(m.id) || []).forEach((p) => peerSet.add(p));
      (externalByNode.get(m.id) || []).forEach((extID) => peerSet.add(extID));
      tipLines.push(`${m.name} (${m.isOnline ? "在线" : "离线"}) · ↓${bytes(m.rxBPS)}/s ↑${bytes(m.txBPS)}/s`);
    }
    const peers = Array.from(peerSet);
    const hl = isHighlightedEdge(peers);
    const sel = activeKey();
    const active = sel === cluster.entityKey || cluster.members.some((m) => sel === `node:${m.node.id}`);

    tagLayer.append(buildNodeTagGroup({
      entityKey: cluster.entityKey,
      refreshID: cluster.refreshID,
      cx: placed.cx,
      cy: placed.cy,
      tagW,
      tagH,
      label: cluster.label,
      isOnline: cluster.isOnline,
      blocked: cluster.blocked,
      engineCount: cluster.engineCount,
      active,
      hl,
      anchorX: cluster.x,
      anchorY: cluster.y,
      peers,
      tooltip: tipLines.join("\n"),
      bindInteractive,
    }));
  }

  // 5. Tiered probe pills: Top-5 severe internal `bad` faults stay ambient (short badge -> full on hover), others focusOnly
  const nodeLabelByID = new Map(model.nodes.map((n) => [n.id, formatMapCityLabel(n.name, n) || n.id]));
  const shortNodeLabel = (id) => {
    const s = String(id || "");
    if (s.startsWith("ext:")) return s.slice(4);
    return nodeLabelByID.get(s) || formatMapCityLabel(s);
  };

  const ambientBadIDs = selectTopAmbientBadProbeIDs(model.probeLinks, { includeExternal: false, maxCount: 5 });
  const pillBounds = { minX: 16, maxX: width - 16, minY: 14, maxY: 536 };
  const cohortBoxes = new Map();
  const occupiedForCohort = (keys) => {
    const merged = [...staticOccupied];
    for (const k of keys) {
      const arr = cohortBoxes.get(k);
      if (arr) merged.push(...arr);
    }
    return merged;
  };
  const recordCohortBox = (keys, box) => {
    for (const k of keys) {
      if (!cohortBoxes.has(k)) cohortBoxes.set(k, []);
      cohortBoxes.get(k).push(box);
    }
  };

  const orderedProbes = [...model.probeLinks].sort((a, b) => {
    const aAmb = ambientBadIDs.has(a.id) ? 0 : 1;
    const bAmb = ambientBadIDs.has(b.id) ? 0 : 1;
    if (aAmb !== bAmb) return aAmb - bAmb;
    const rank = (tone) => (tone === "bad" ? 0 : tone === "warn" ? 1 : 2);
    return rank(a.tone) - rank(b.tone);
  });

  for (const probe of orderedProbes) {
    const geom = arcGeometry.get(probe.id);
    if (!geom) continue;
    const peers = [`probe:${probe.id}`, `node:${probe.from}`, probe.to];
    const hl = isHighlightedEdge(peers);
    const pal = tonePalette(probe.tone);

    const pA = posLookup.get(probe.from);
    const pB = posLookup.get(probe.to);
    const arcChordLen = pA && pB ? Math.hypot(pB.x - pA.x, pB.y - pA.y) : 0;

    const metricPart = buildProbePillLabel(probe, false, true).replace(/(\d+(?:\.\d+)?)ms\//, "$1/");
    const routeArrow = probe.bidirectional ? "↔" : "→";
    const routePrefix = `${clipLabel(shortNodeLabel(probe.from), 12)}${routeArrow}${clipLabel(shortNodeLabel(probe.to), 16)}`;
    const fullLabel = clipLabel(`${routePrefix} · ${metricPart}`, 38);
    const compactRoute = `${clipLabel(shortNodeLabel(probe.from), 10)}${routeArrow}${clipLabel(shortNodeLabel(probe.to), 10)}`;
    const shortLabel = `${compactRoute} · ${buildProbeShortAlertLabel(probe)}`;
    const pillH = 17;
    const fullW = Math.max(76, Math.round(visualUnits(fullLabel) * 5.8 + 20));
    const shortW = Math.max(66, Math.round(visualUnits(shortLabel) * 5.8 + 20));
    const isAmbientCandidate = ambientBadIDs.has(probe.id) && arcChordLen >= 62;

    const endKeys = [`node:${probe.from}`, probe.to.startsWith("ext:") ? probe.to : `node:${probe.to}`];
    if (clusterKeyByNodeID.has(probe.from)) endKeys.push(clusterKeyByNodeID.get(probe.from));
    if (!probe.isExternal && clusterKeyByNodeID.has(probe.to)) endKeys.push(clusterKeyByNodeID.get(probe.to));

    const midPt = geom.pointAt(0.5);
    const anchors = [0.5, 0.36, 0.64, 0.24, 0.76].map((t) => geom.pointAt(t));
    const ambientAnchors = [0.5, 0.42, 0.58, 0.34, 0.66].map((t) => geom.pointAt(t));

    let renderedAmbient = false;
    if (isAmbientCandidate) {
      const occupiedLenBefore = staticOccupied.length;
      const placed = placeCollisionFreePill(
        midPt.x,
        midPt.y,
        shortW,
        pillH,
        staticOccupied,
        pillBounds,
        ambientAnchors,
        { padX: 3, padY: 3 },
      );
      const anchorDist = Math.hypot(placed.cx - placed.anchorX, placed.cy - placed.anchorY);
      if (anchorDist <= 28) {
        renderedAmbient = true;
        appendProbeLeader(leaderLayer, placed, pal, peers, hl, true, false);
        pillLayer.append(createProbePillGroup({
          probe,
          cx: placed.cx,
          cy: placed.cy,
          pillW: shortW,
          fullW: Math.max(shortW, fullW),
          pillH,
          label: fullLabel,
          shortLabel,
          pal,
          compact: true,
          peers,
          focusOnly: false,
          ambientAlert: true,
        }));
      } else {
        staticOccupied.length = occupiedLenBefore;
      }
    }

    if (!renderedAmbient) {
      const placed = placeCollisionFreePill(
        midPt.x,
        midPt.y,
        fullW,
        pillH,
        occupiedForCohort(endKeys),
        pillBounds,
        anchors,
        { padX: 3, padY: 3 },
      );
      recordCohortBox(endKeys, { x: placed.cx - fullW / 2, y: placed.cy - pillH / 2, w: fullW, h: pillH });
      appendProbeLeader(leaderLayer, placed, pal, peers, hl, true, true);
      pillLayer.append(createProbePillGroup({
        probe,
        cx: placed.cx,
        cy: placed.cy,
        pillW: fullW,
        pillH,
        label: fullLabel,
        pal,
        compact: true,
        peers,
        focusOnly: true,
      }));
    }
  }

  mapViewportG.append(geoTetherLayer, arcLayer, beaconLayer, dockLayer, tagLayer, leaderLayer, pillLayer);
  svg.append(hudG, mapViewportG);
  return svg;
}

/**
 * Renders the compact Mobile Pacific Map SVG (summary strip + clipped Pacific map + bottom public dock strip),
 * delegating full probe telemetry details cleanly to the standalone topology page link list.
 */
function renderMobilePacificMap({ model, beacons, dock, posLookup, peerProbesByNode, externalByNode, bytes, helpers }) {
  const {
    allowMotion,
    activeKey,
    isHighlightedEdge,
    bindInteractive,
  } = helpers;

  const mapScale = 342 / PACIFIC_MAP_BOUNDS.width;
  const mapTop = 24;
  const mapViewH = 548;
  const mapHeightPx = Math.round(mapViewH * mapScale);
  const dockSlots = Array.from(dock.slots.values());
  const dockRowTop = mapTop + mapHeightPx;
  const totalHeight = dockSlots.length ? dockRowTop + 30 : mapTop + mapHeightPx;

  const svg = createSVGEl("svg", {
    viewBox: `0 0 342 ${totalHeight}`,
    width: "100%",
    class: "a-dash-topo-svg a-dash-topo-map-svg",
    "data-refresh-key": "topo-svg-map",
    "data-topo-mode": "map",
    role: "img",
    "aria-label": "太平洋中心全网节点与探测拓扑地图",
  });

  const defs = createTopologyDefs();
  svg.append(defs);

  svg.append(
    createSVGEl("rect", { x: "0", y: "0", width: "342", height: String(mapTop), fill: "#f4f4f5" }),
    createSVGEl("line", { x1: "0", y1: String(mapTop), x2: "342", y2: String(mapTop), stroke: "#e4e4e7", "stroke-width": "1" }),
    createSVGEl("text", { x: "10", y: "15.5", class: "a-dash-topo-micro", fill: "#52525b" },
      `入站 ↓ ${bytes(model.ingress.rateBPS)}/s · 出站 ↑ ${bytes(model.egress.rateBPS)}/s`),
  );

  const mapGroup = createSVGEl("g", {
    "data-refresh-key": "mob-map-viewport",
    transform: `translate(0 ${mapTop}) scale(${mapScale.toFixed(5)})`,
  });
  appendPacificBackdrop(mapGroup, defs, { viewH: mapViewH });

  const laneInfo = buildArcLanes(model.probeLinks, posLookup);
  const motionProbeIDs = allowMotion() ? selectMotionProbeIDs(model.probeLinks, 4) : new Set();

  for (const probe of model.probeLinks) {
    const pA = posLookup.get(probe.from);
    const pB = posLookup.get(probe.to);
    if (!pA || !pB) continue;
    const laneMeta = laneInfo.get(probe.id) || { lane: 0, total: 1 };
    const geom = buildMapArcGeometry(pA, pB, laneMeta, probe.isExternal);
    const peers = [`probe:${probe.id}`, `node:${probe.from}`, probe.to];
    const hl = isHighlightedEdge(peers);
    const pal = tonePalette(probe.tone);

    const baseStr = probe.isExternal ? "0" : (probe.tone === "bad" ? "0.86" : probe.tone === "warn" ? "0.80" : "0.48");
    const dimStr = probe.isExternal ? "0" : "0.14";
    const curOpacity = probe.isExternal
      ? (hl === true ? "1" : "0")
      : (hl === true ? "1" : hl === false ? "0.14" : baseStr);

    const g = createSVGEl("g", {
      class: probe.isExternal ? "a-dash-topo-focus-only" : null,
      "data-refresh-key": `mob-map-arc-${probe.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": baseStr,
      "data-dim-opacity": dimStr,
      opacity: curOpacity,
    });
    g.append(createSVGEl("path", {
      d: geom.d,
      fill: "none",
      stroke: pal.stroke,
      "stroke-width": hl === true ? "5.6" : "3.8",
      "stroke-linecap": "round",
      "stroke-dasharray": probe.tone === "bad" ? "10 8" : null,
    }));
    if (motionProbeIDs.has(probe.id)) {
      const dot = createSVGEl("circle", { r: "5.0", fill: "#ffffff", stroke: pal.stroke, "stroke-width": "2.4" });
      dot.append(createSVGEl("animateMotion", { path: geom.d, dur: "2.5s", repeatCount: "indefinite" }));
      g.append(dot);
    }
    bindInteractive(g, `probe:${probe.id}`, { openProbeID: probe.id });
    mapGroup.append(g);
  }

  for (const item of beacons) {
    const { node, x, y } = item;
    const entityKey = `node:${node.id}`;
    const extPeers = Array.from(externalByNode.get(node.id) || []);
    const peers = [entityKey, ...(peerProbesByNode.get(node.id) || []), ...extPeers];
    const hl = isHighlightedEdge(peers);
    const active = activeKey() === entityKey;
    const coreColor = node.blocked ? BLOCKED_COLOR : !node.isOnline ? IDLE_FLOW_COLOR : OK_COLOR;
    const g = createSVGEl("g", {
      class: `a-dash-topo-node ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `mob-beacon-${node.id}`,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": "1",
      opacity: hl === false ? "0.24" : "1",
    });
    g.append(
      createSVGEl("circle", { cx: x.toFixed(1), cy: y.toFixed(1), r: active ? "18" : "13", fill: "#ffffff", stroke: active ? INGRESS_COLOR : coreColor, "stroke-width": "3.6" }),
      createSVGEl("circle", { cx: x.toFixed(1), cy: y.toFixed(1), r: "6.8", fill: coreColor }),
    );
    bindInteractive(g, entityKey);
    mapGroup.append(g);
  }

  svg.append(mapGroup);

  if (dockSlots.length) {
    svg.append(
      createSVGEl("rect", { x: "0", y: String(dockRowTop), width: "342", height: "30", fill: "#f4f4f5" }),
      createSVGEl("line", { x1: "0", y1: String(dockRowTop), x2: "342", y2: String(dockRowTop), stroke: "#e4e4e7", "stroke-width": "1" }),
    );
    const gap = 4;
    const availW = 330 - (dockSlots.length - 1) * gap;
    const rawWidths = dockSlots.map((slot) => Math.max(46, slot.ext.address.length * 4.9 + 14));
    const sumRaw = rawWidths.reduce((s, v) => s + v, 0);
    const chipWidths = rawWidths.map((rw) => Math.floor((rw / sumRaw) * availW));
    let curX = 6;
    const chipY = dockRowTop + 5;
    dockSlots.forEach((slot, idx) => {
      const chipW = idx === dockSlots.length - 1 ? Math.max(40, 336 - curX) : chipWidths[idx];
      const extID = slot.ext.id;
      const peers = [extID, ...slot.linked.map((p) => `probe:${p.id}`), ...slot.linked.map((p) => `node:${p.from}`)];
      const hl = isHighlightedEdge(peers);
      const active = activeKey() === extID;
      const cx = curX;
      curX += chipW + gap;
      const isLongIP = slot.ext.address.length > 9;

      const g = createSVGEl("g", {
        class: `a-dash-topo-node a-dash-topo-dock-chip ${active ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
        "data-refresh-key": `mob-dock-${extID}`,
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": "1",
        opacity: hl === false ? "0.28" : "1",
      });
      g.append(
        createSVGEl("rect", {
          x: cx,
          y: chipY,
          width: chipW,
          height: 20,
          rx: "5",
          fill: "#ffffff",
          stroke: active ? INGRESS_COLOR : "#d4d4d8",
          "stroke-width": active || hl === true ? "1.4" : "0.95",
          class: "a-dash-topo-card",
        }),
        createSVGEl("text", {
          x: (cx + chipW / 2).toFixed(1),
          y: (chipY + 13.0).toFixed(1),
          "text-anchor": "middle",
          class: "a-dash-topo-micro",
          "font-size": isLongIP ? "8.2" : "9.2",
          fill: "#18181b",
        }, clipLabel(slot.ext.address, Math.max(8, Math.floor((chipW - 6) / (isLongIP ? 4.7 : 5.3))))),
      );
      bindInteractive(g, extID);
      svg.append(g);
    });
  }

  return svg;
}
