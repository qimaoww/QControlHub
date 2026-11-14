import { createSVGEl } from "./dashboard-panel.js";
import { listen, patch } from "./live-dom.js";
import { badge, link, muted, node, section } from "./ui.js";
import { buildDashboardTopology, utcMonth } from "../modules/dashboard-model.js";
import { geoRegionDetails } from "../modules/region-model.js";

const ACTIVE_FLOW_COLOR = "#1d4ed8";
const EGRESS_FLOW_COLOR = "#0f766e";
const IDLE_FLOW_COLOR = "#94a3b8";

function visualUnits(text) {
  let units = 0;
  for (const ch of String(text || "")) {
    units += ch.charCodeAt(0) > 255 ? 1.85 : 1;
  }
  return units;
}

function clipLabel(text, maxUnits) {
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

function tonePalette(tone) {
  if (tone === "ok") return { stroke: "#15803d", fill: "#ecfdf5", border: "#86efac", text: "#14532d" };
  if (tone === "warn") return { stroke: "#b45309", fill: "#fffbeb", border: "#fcd34d", text: "#78350f" };
  if (tone === "bad") return { stroke: "#b91c1c", fill: "#fef2f2", border: "#fca5a5", text: "#7f1d1d" };
  return { stroke: "#64748b", fill: "#f8fafc", border: "#cbd5e1", text: "#334155" };
}

function cubicPath(x1, y1, x2, y2) {
  const dx = Math.max(24, Math.abs(x2 - x1) * 0.48);
  return `M ${x1.toFixed(1)} ${y1.toFixed(1)} C ${(x1 + dx).toFixed(1)} ${y1.toFixed(1)}, ${(x2 - dx).toFixed(1)} ${y2.toFixed(1)}, ${x2.toFixed(1)} ${y2.toFixed(1)}`;
}

function probeArcGeometry(x, y1, y2, bow) {
  const midY = (y1 + y2) / 2;
  const peakX = x + bow;
  const ctrlX = x + bow * 1.32;
  const d = `M ${x.toFixed(1)} ${y1.toFixed(1)} C ${ctrlX.toFixed(1)} ${y1.toFixed(1)}, ${ctrlX.toFixed(1)} ${y2.toFixed(1)}, ${x.toFixed(1)} ${y2.toFixed(1)}`;
  return { d, apexX: peakX, apexY: midY };
}

function createTopologyDefs() {
  const defs = createSVGEl("defs", { "data-refresh-key": "topo-defs" });

  const addGrad = (id, c1, c2) => {
    const grad = createSVGEl("linearGradient", {
      id,
      "data-refresh-key": id,
      x1: "0%",
      y1: "0%",
      x2: "100%",
      y2: "0%",
    });
    grad.append(
      createSVGEl("stop", { offset: "0%", "stop-color": c1, "stop-opacity": "0.85" }),
      createSVGEl("stop", { offset: "100%", "stop-color": c2, "stop-opacity": "0.65" }),
    );
    defs.append(grad);
  };

  addGrad("a-topo-grad-rx", "#2563eb", "#0284c7");
  addGrad("a-topo-grad-tx", "#0d9488", "#15803d");

  const addArrow = (id, color) => {
    const marker = createSVGEl("marker", {
      id,
      "data-refresh-key": id,
      viewBox: "0 0 10 10",
      refX: "8.2",
      refY: "5",
      markerWidth: "5.5",
      markerHeight: "5.5",
      orient: "auto-start-reverse",
    });
    marker.append(createSVGEl("path", { d: "M 0 1.8 L 8.5 5 L 0 8.2 z", fill: color }));
    defs.append(marker);
  };

  addArrow("a-topo-arrow-ok", "#15803d");
  addArrow("a-topo-arrow-warn", "#b45309");
  addArrow("a-topo-arrow-bad", "#b91c1c");
  addArrow("a-topo-arrow-idle", "#64748b");
  addArrow("a-topo-arrow-rx", ACTIVE_FLOW_COLOR);
  addArrow("a-topo-arrow-tx", EGRESS_FLOW_COLOR);

  return defs;
}

export function createDashboardTopologySection(ctx, {
  bytes,
  getAgents,
  getOrderIDs,
  getUsage,
  getPolicies,
  getProbes,
  getMetrics,
  getConnections,
  getStates,
  getMonth = utcMonth,
}) {
  const canView = () => ctx.can("agents.read") || ctx.can("traffic.read");

  let pinnedKey = "";
  let hoveredKey = "";
  let observedWidth = 0;
  let lastPaintSignature = "";
  let lastModel = null;

  const statusNode = muted("读取中…");
  statusNode.setAttribute("role", "status");

  const headerMeta = node("div", { className: "a-dash-topo-meta" });
  const canvasHost = node("div", { className: "a-dash-topo-canvas" });
  const mobileLinksHost = node("div", { className: "a-dash-topo-mobile-links" });
  const inspectPrimary = node("div", { className: "a-dash-topo-inspect-main" });
  const inspectDetail = node("div", { className: "a-dash-topo-inspect-detail" });
  const inspectBar = node("div", { className: "a-dash-topo-inspect" }, inspectPrimary, inspectDetail);

  const activeKey = () => hoveredKey || pinnedKey;

  const resolveKeyDescription = (key, model) => {
    if (!model || !model.nodes.length) {
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, "全网数据流拓扑"), muted("等待节点与流量样本…")],
        detail: [],
      };
    }

    if (key === "hub:ingress") {
      const chips = [
        muted(`实时入站 ↓ ${bytes(model.ingress.rateBPS)}/s`),
        muted(`本月接收 ${bytes(model.ingress.monthlyBytes)}`),
      ];
      if (model.ingress.clientIPs != null) {
        chips.push(muted(`24h 活跃 IP ${model.ingress.clientIPs} · 连接 ${model.ingress.clientFlows}`));
      }
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, "入站与客户端接入")],
        detail: chips,
      };
    }

    if (key === "hub:egress" || key === "hub:control") {
      const chips = [
        muted(`实时出站 ↑ ${bytes(model.egress.rateBPS)}/s`),
        muted(`本月发送 ${bytes(model.egress.monthlyBytes)}`),
        muted(`运行内核 ${model.runningEngines}`),
      ];
      if (model.egress.panelRxBPS != null) {
        chips.push(muted(`主控 ↓ ${bytes(model.egress.panelRxBPS)}/s · ↑ ${bytes(model.egress.panelTxBPS)}/s`));
      }
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, key === "hub:control" ? "主控面汇聚" : "出站转发汇聚")],
        detail: chips,
      };
    }

    if (key?.startsWith("ext:")) {
      const target = model.externalTargets.find((item) => item.id === key);
      if (target) {
        return {
          primary: [node("span", { className: "a-dash-topo-inspect-title" }, `外网探针目标 · ${target.name}`)],
          detail: [muted(`地址 ${target.address}`), muted(`关联链路 ${target.linkCount} 条`)],
        };
      }
    }

    if (key?.startsWith("probe:")) {
      const linkID = key.slice("probe:".length);
      const item = model.probeLinks.find((p) => p.id === linkID);
      if (item) {
        const chips = [
          badge(item.statusLabel, item.tone),
          muted(`${item.sourceAddr || item.from} ${item.bidirectional ? "↔" : "→"} ${item.targetAddr || item.to}`),
        ];
        if (item.latencyMS != null) chips.push(muted(`去程 ${item.latencyMS.toFixed(1)} ms`));
        if (item.revLatencyMS != null) chips.push(muted(`回程 ${item.revLatencyMS.toFixed(1)} ms`));
        if (item.lossPercent != null) chips.push(muted(`峰值丢包 ${item.lossPercent.toFixed(item.lossPercent % 1 ? 1 : 0)}%`));
        return {
          primary: [node("span", { className: "a-dash-topo-inspect-title" }, item.name)],
          detail: chips,
        };
      }
    }

    if (key?.startsWith("node:")) {
      const nodeID = key.slice("node:".length);
      const item = model.nodes.find((n) => n.id === nodeID);
      if (item) {
        const region = geoRegionDetails(item.regionCode);
        const chips = [
          badge(item.isOnline ? "在线" : "离线", item.isOnline ? "ok" : "bad"),
          muted(`实时 ↓ ${bytes(item.rxBPS)}/s · ↑ ${bytes(item.txBPS)}/s`),
          muted(`本月 ${bytes(item.monthlyUsedBytes)}`),
        ];
        if (item.activeEngines.length) chips.push(muted(`内核 ${item.activeEngines.join(" / ")}`));
        if (item.portCount > 0) chips.push(muted(`策略端口 ${item.portCount}`));
        if (item.blocked) chips.push(badge("已超限阻断", "bad"));
        if (ctx.can("traffic.read")) {
          chips.push(link("流量明细", `#traffic?agent=${encodeURIComponent(item.id)}`));
        }
        return {
          primary: [
            node("span", { className: "a-dash-topo-inspect-title" }, `${region ? `${region.code} · ` : ""}${item.name}`),
            item.ip ? node("code", { className: "a-dash-topo-code" }, item.ip) : null,
          ].filter(Boolean),
          detail: chips,
        };
      }
    }

      const parts = [
      muted(`实时吞吐 ↓ ${bytes(model.ingress.rateBPS)}/s · ↑ ${bytes(model.egress.rateBPS)}/s`),
      muted(`在线节点 ${model.onlineNodes}/${model.totalNodes}`),
    ];
    if (model.probeLinks.length) parts.push(muted(`链路实测 ${model.probeLinks.length} 条`));
    if (model.hiddenNodeCount > 0) parts.push(muted(`显示活跃 Top ${model.nodes.length}（其余 ${model.hiddenNodeCount} 个未展开）`));
    return {
      primary: [node("span", { className: "a-dash-topo-inspect-title" }, "全网实时流向")],
      detail: [...parts, muted("选择节点或链路查看明细")],
    };
  };

  const syncInspect = () => {
    const { primary, detail } = resolveKeyDescription(activeKey(), lastModel);
    inspectPrimary.replaceChildren(...primary);
    inspectDetail.replaceChildren(...detail);
  };

  const bindInteractive = (el, entityKey) => {
    el.setAttribute("tabindex", "0");
    el.setAttribute("role", "button");
    el.setAttribute("data-topo-entity", entityKey);
    el.setAttribute("aria-pressed", pinnedKey === entityKey ? "true" : "false");
    listen(el, "pointerenter", () => {
      if (hoveredKey !== entityKey) {
        hoveredKey = entityKey;
        syncInspect();
      }
    });
    listen(el, "pointerleave", () => {
      if (hoveredKey === entityKey) {
        hoveredKey = "";
        syncInspect();
      }
    });
    const toggle = (event) => {
      if (event.type === "keydown" && event.key !== "Enter" && event.key !== " ") return;
      if (event.type === "keydown") event.preventDefault();
      pinnedKey = pinnedKey === entityKey ? "" : entityKey;
      hoveredKey = "";
      paint(true);
    };
    listen(el, "click", toggle);
    listen(el, "keydown", toggle);
  };

  const isHighlightedEdge = (entityA, entityB, linkEntityKey = "") => {
    const sel = activeKey();
    if (!sel) return null;
    if (linkEntityKey && sel === linkEntityKey) return true;
    return sel === entityA || sel === entityB;
  };

  const patchChildren = (host, items) => {
    const prev = [...host.children];
    if (
      prev.length === items.length &&
      items.every((el, idx) => prev[idx] && prev[idx].getAttribute("data-refresh-key") === el.getAttribute("data-refresh-key"))
    ) {
      items.forEach((el, idx) => patch(prev[idx], el));
      return;
    }
    host.replaceChildren(...items);
  };

  const keyedBadge = (text, tone, refreshKey) => {
    const el = badge(text, tone);
    el.setAttribute("data-refresh-key", refreshKey);
    return el;
  };

  const renderDesktopSVG = (width, model) => {
    const padX = 12;
    const padY = 14;
    const nodeH = 54;
    const nodeGap = 11;
    const sideW = Math.round(Math.max(136, Math.min(186, width * 0.19)));
    const nodeW = Math.round(Math.max(178, Math.min(252, width * 0.25)));
    const leftX = padX;
    const rightX = width - padX - sideW;
    const interior = Math.max(nodeW + 76, rightX - (leftX + sideW));
    const leftGap = Math.max(46, Math.round((interior - nodeW) * 0.37));
    const nodeX = Math.min(rightX - nodeW - 44, leftX + sideW + leftGap);
    const rightGap = Math.max(44, rightX - (nodeX + nodeW));

    const nodesCount = model.nodes.length;
    const nodesStackH = nodesCount ? nodesCount * nodeH + (nodesCount - 1) * nodeGap : nodeH;

    const egressCardH = 74;
    const controlCardH = 60;
    const extCardH = 40;
    const rightGapV = 10;
    const extItems = model.externalTargets;
    const rightStackH = egressCardH + rightGapV + controlCardH + (extItems.length ? rightGapV + extItems.length * extCardH + (extItems.length - 1) * 8 : 0);
    const ingressCardH = model.ingress.clientIPs != null ? 104 : 84;

    const totalH = Math.max(186, Math.max(nodesStackH, rightStackH, ingressCardH) + padY * 2);
    const ingressY = Math.round((totalH - ingressCardH) / 2);
    const nodesStartY = Math.round((totalH - nodesStackH) / 2);
    const rightStartY = Math.round((totalH - rightStackH) / 2);

    const svg = createSVGEl("svg", {
      class: "a-dash-topo-svg",
      "data-refresh-key": "topo-svg",
      viewBox: `0 0 ${width} ${totalH}`,
      role: "img",
      "aria-label": "全网节点数据流拓扑图",
    });
    svg.append(createTopologyDefs());

    const nodeGeom = new Map();
    model.nodes.forEach((item, index) => {
      const y = nodesStartY + index * (nodeH + nodeGap);
      nodeGeom.set(item.id, { x: nodeX, y, w: nodeW, h: nodeH, cy: y + nodeH / 2, item });
    });

    const extGeom = new Map();
    let extCursorY = rightStartY + egressCardH + rightGapV + controlCardH + rightGapV;
    for (const ext of extItems) {
      extGeom.set(ext.id, { x: rightX, y: extCursorY, w: sideW, h: extCardH, cy: extCursorY + extCardH / 2, item: ext });
      extCursorY += extCardH + 8;
    }

    const egressBox = { x: rightX, y: rightStartY, w: sideW, h: egressCardH, cy: rightStartY + egressCardH / 2 };
    const controlBox = { x: rightX, y: rightStartY + egressCardH + rightGapV, w: sideW, h: controlCardH, cy: rightStartY + egressCardH + rightGapV + controlCardH / 2 };
    const ingressBox = { x: leftX, y: ingressY, w: sideW, h: ingressCardH, cy: ingressY + ingressCardH / 2 };

    const flowLayer = createSVGEl("g", { "data-refresh-key": "flow-layer" });
    const probeLayer = createSVGEl("g", { "data-refresh-key": "probe-layer" });
    const entityLayer = createSVGEl("g", { "data-refresh-key": "entity-layer" });

    // 1. Ingress -> Nodes ribbons
    model.ingressLinks.forEach((edge, idx) => {
      const target = nodeGeom.get(edge.to);
      if (!target) return;
      const spreadY = model.ingressLinks.length > 1
        ? ingressBox.y + 18 + (idx / (model.ingressLinks.length - 1)) * (ingressBox.h - 36)
        : ingressBox.cy;
      const d = cubicPath(ingressBox.x + ingressBox.w, spreadY, target.x - 2, target.cy - 4);
      const hl = isHighlightedEdge("hub:ingress", `node:${edge.to}`, `edge:${edge.id}`);
      const strokeW = edge.active ? 1.6 + edge.weight * 2.4 : 1.3;
      const opacity = hl === true ? 0.92 : hl === false ? 0.16 : (edge.active ? 0.62 : 0.34);

      const g = createSVGEl("g", { "data-refresh-key": `edge-${edge.id}` });
      g.append(createSVGEl("path", {
        d,
        fill: "none",
        stroke: edge.active ? "url(#a-topo-grad-rx)" : IDLE_FLOW_COLOR,
        "stroke-width": strokeW.toFixed(2),
        "stroke-dasharray": edge.active ? null : "4 4",
        opacity: opacity.toFixed(2),
        "marker-end": edge.active ? "url(#a-topo-arrow-rx)" : "url(#a-topo-arrow-idle)",
        class: edge.active ? "a-dash-topo-flow-active" : "a-dash-topo-flow-idle",
      }));
      if (edge.active) {
        g.append(createSVGEl("path", {
          d,
          fill: "none",
          stroke: "#eff6ff",
          "stroke-width": Math.max(1.2, strokeW * 0.45).toFixed(2),
          "stroke-dasharray": "5 9",
          opacity: (hl === false ? 0.1 : 0.72).toFixed(2),
          class: "a-dash-topo-flow-pulse",
        }));
      }
      if (edge.bytesPerSec > 0 && leftGap >= 72) {
        const mx = (ingressBox.x + ingressBox.w + target.x) / 2;
        const my = (spreadY + target.cy - 4) / 2 - 5;
        g.append(createSVGEl("text", {
          x: mx.toFixed(1),
          y: my.toFixed(1),
          "text-anchor": "middle",
          class: "a-dash-topo-flow-label",
          fill: ACTIVE_FLOW_COLOR,
          opacity: hl === false ? "0.2" : "0.9",
        }, `↓ ${bytes(edge.bytesPerSec)}/s`));
      }
      flowLayer.append(g);
    });

    // 2. Nodes -> Egress ribbons
    model.egressLinks.forEach((edge, idx) => {
      const src = nodeGeom.get(edge.from);
      if (!src) return;
      const spreadY = model.egressLinks.length > 1
        ? egressBox.y + 14 + (idx / (model.egressLinks.length - 1)) * (egressBox.h - 28)
        : egressBox.cy;
      const d = cubicPath(src.x + src.w, src.cy + 5, egressBox.x - 2, spreadY);
      const hl = isHighlightedEdge(`node:${edge.from}`, "hub:egress", `edge:${edge.id}`);
      const strokeW = edge.active && !edge.blocked ? 1.5 + edge.weight * 2.2 : 1.3;
      const opacity = hl === true ? 0.9 : hl === false ? 0.14 : (edge.active ? 0.56 : 0.3);

      const g = createSVGEl("g", { "data-refresh-key": `edge-${edge.id}` });
      g.append(createSVGEl("path", {
        d,
        fill: "none",
        stroke: edge.blocked ? "#b91c1c" : edge.active ? "url(#a-topo-grad-tx)" : IDLE_FLOW_COLOR,
        "stroke-width": strokeW.toFixed(2),
        "stroke-dasharray": edge.active && !edge.blocked ? null : "4 4",
        opacity: opacity.toFixed(2),
        "marker-end": edge.blocked ? "url(#a-topo-arrow-bad)" : edge.active ? "url(#a-topo-arrow-tx)" : "url(#a-topo-arrow-idle)",
        class: edge.active && !edge.blocked ? "a-dash-topo-flow-active" : "a-dash-topo-flow-idle",
      }));
      if (edge.active && !edge.blocked) {
        g.append(createSVGEl("path", {
          d,
          fill: "none",
          stroke: "#ecfdf5",
          "stroke-width": Math.max(1.2, strokeW * 0.45).toFixed(2),
          "stroke-dasharray": "5 9",
          opacity: (hl === false ? 0.1 : 0.72).toFixed(2),
          class: "a-dash-topo-flow-pulse",
        }));
      }
      flowLayer.append(g);
    });

    // 3. Probe Links (Node <-> Node arcs & Node -> External target connectors)
    let internalArcIdx = 0;
    model.probeLinks.forEach((probe) => {
      const pal = tonePalette(probe.tone);
      const marker = probe.tone === "ok"
        ? "url(#a-topo-arrow-ok)"
        : probe.tone === "warn"
          ? "url(#a-topo-arrow-warn)"
          : probe.tone === "bad"
            ? "url(#a-topo-arrow-bad)"
            : "url(#a-topo-arrow-idle)";

      const src = nodeGeom.get(probe.from);
      if (!src) return;
      const entityKey = `probe:${probe.id}`;
      const hl = isHighlightedEdge(`node:${probe.from}`, probe.isExternal ? probe.to : `node:${probe.to}`, entityKey);
      const opacity = hl === false ? 0.2 : 1;

      const g = createSVGEl("g", {
        class: "a-dash-topo-probe-item",
        "data-refresh-key": `probe-${probe.id}`,
        opacity: opacity.toFixed(2),
      });
      g.append(createSVGEl("title", {}, `${probe.name}: ${probe.metricLabel} (${probe.statusLabel})`));

      let d = "";
      let labelX = 0;
      let labelY = 0;

      if (probe.isExternal) {
        const ext = extGeom.get(probe.to);
        if (!ext) return;
        d = cubicPath(src.x + src.w, src.cy - 6, ext.x - 2, ext.cy);
        labelX = (src.x + src.w + ext.x) / 2;
        labelY = (src.cy - 6 + ext.cy) / 2;
      } else {
        const dst = nodeGeom.get(probe.to);
        if (!dst) return;
        const bowStep = Math.min(26, Math.max(16, (rightGap - 40) / Math.max(1, model.probeLinks.length)));
        const bow = Math.min(rightGap - 18, 34 + internalArcIdx * bowStep);
        internalArcIdx += 1;
        const yA = src.cy + (src.cy < dst.cy ? 4 : -4);
        const yB = dst.cy + (src.cy < dst.cy ? -4 : 4);
        const geom = probeArcGeometry(src.x + src.w, yA, yB, bow);
        d = geom.d;
        labelX = Math.min(rightX - 44, geom.apexX);
        labelY = geom.apexY;
      }

      g.append(
        createSVGEl("path", {
          d,
          fill: "none",
          stroke: "#ffffff",
          "stroke-width": "5",
          "stroke-linecap": "round",
          opacity: "0.78",
        }),
        createSVGEl("path", {
          d,
          fill: "none",
          stroke: pal.stroke,
          "stroke-width": activeKey() === entityKey ? "2.6" : "2",
          "stroke-dasharray": probe.running ? "6 4" : probe.tone === "bad" ? "4 3" : null,
          "marker-end": marker,
          "marker-start": probe.bidirectional && !probe.isExternal ? marker : null,
        }),
        createSVGEl("path", {
          d,
          fill: "none",
          stroke: "transparent",
          "stroke-width": "14",
        }),
      );

      const pillText = clipLabel(probe.metricLabel, 18);
      const pillW = Math.max(60, Math.min(136, visualUnits(pillText) * 6.9 + 20));
      const pillH = 20;
      g.append(
        createSVGEl("rect", {
          x: (labelX - pillW / 2).toFixed(1),
          y: (labelY - pillH / 2).toFixed(1),
          width: pillW.toFixed(1),
          height: pillH,
          rx: 999,
          fill: pal.fill,
          stroke: activeKey() === entityKey ? pal.text : pal.border,
          "stroke-width": activeKey() === entityKey ? "1.6" : "1",
        }),
        createSVGEl("text", {
          x: labelX.toFixed(1),
          y: (labelY + 3.5).toFixed(1),
          "text-anchor": "middle",
          class: "a-dash-topo-pill-text",
          fill: pal.text,
        }, pillText),
      );

      bindInteractive(g, entityKey);
      probeLayer.append(g);
    });

    // 4. Entities: Ingress Box
    {
      const key = "hub:ingress";
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === key ? "is-active" : ""}`,
        "data-refresh-key": "hub-ingress",
      });
      g.append(
        createSVGEl("rect", {
          x: ingressBox.x,
          y: ingressBox.y,
          width: ingressBox.w,
          height: ingressBox.h,
          rx: 10,
          class: "a-dash-topo-hub-box",
        }),
        createSVGEl("rect", {
          x: ingressBox.x,
          y: ingressBox.y + 8,
          width: 3.5,
          height: ingressBox.h - 16,
          rx: 2,
          fill: ACTIVE_FLOW_COLOR,
        }),
        createSVGEl("text", { x: ingressBox.x + 12, y: ingressBox.y + 21, class: "a-dash-topo-hub-kicker" }, "入站与接入"),
        createSVGEl("text", { x: ingressBox.x + 12, y: ingressBox.y + 42, class: "a-dash-topo-hub-metric" }, `↓ ${bytes(model.ingress.rateBPS)}/s`),
        createSVGEl("text", { x: ingressBox.x + 12, y: ingressBox.y + 60, class: "a-dash-topo-hub-sub" }, `月接收 ${bytes(model.ingress.monthlyBytes)}`),
      );
      if (model.ingress.clientIPs != null) {
        g.append(createSVGEl("text", { x: ingressBox.x + 12, y: ingressBox.y + 80, class: "a-dash-topo-hub-sub" }, `24h ${model.ingress.clientIPs} IP · ${model.ingress.clientFlows} 流`));
      }
      bindInteractive(g, key);
      entityLayer.append(g);
    }

    // 5. Entities: Nodes
    const maxNameChars = Math.max(9, Math.floor((nodeW - 56) / 6.8));
    model.nodes.forEach((item) => {
      const box = nodeGeom.get(item.id);
      if (!box) return;
      const key = `node:${item.id}`;
      const selected = activeKey() === key;
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${selected ? "is-active" : ""} ${item.isOnline ? "is-online" : "is-offline"}`,
        "data-refresh-key": `node-${item.id}`,
      });
      g.append(createSVGEl("title", {}, `${item.name} · ↓ ${bytes(item.rxBPS)}/s · ↑ ${bytes(item.txBPS)}/s`));
      g.append(
        createSVGEl("rect", {
          x: box.x,
          y: box.y,
          width: box.w,
          height: box.h,
          rx: 9,
          class: `a-dash-topo-node-box ${selected ? "is-active" : ""}`,
        }),
        createSVGEl("circle", {
          cx: box.x + 13,
          cy: box.y + 19,
          r: 4,
          fill: item.blocked ? "#b91c1c" : item.isOnline ? "#15803d" : "#94a3b8",
        }),
      );
      const region = geoRegionDetails(item.regionCode);
      if (region?.code) {
        g.append(createSVGEl("text", { x: box.x + 22, y: box.y + 22.5, class: "a-dash-topo-node-region" }, region.code));
      }
      const nameOffsetX = region?.code ? 43 : 23;
      g.append(
        createSVGEl("text", { x: box.x + nameOffsetX, y: box.y + 22.5, class: "a-dash-topo-node-name" }, clipLabel(item.name, maxNameChars)),
        createSVGEl("text", { x: box.x + 12, y: box.y + 41, class: "a-dash-topo-node-sub" }, `↓ ${bytes(item.rxBPS)}/s  ↑ ${bytes(item.txBPS)}/s`),
      );
      if (item.activeEngines.length && box.w >= 200) {
        const engineBadge = clipLabel(item.activeEngines[0], 8);
        const bw = Math.max(36, engineBadge.length * 6.4 + 10);
        g.append(
          createSVGEl("rect", { x: box.x + box.w - bw - 8, y: box.y + 10, width: bw, height: 16, rx: 4, class: "a-dash-topo-engine-pill" }),
          createSVGEl("text", { x: box.x + box.w - bw / 2 - 8, y: box.y + 21, "text-anchor": "middle", class: "a-dash-topo-engine-text" }, engineBadge),
        );
      }
      bindInteractive(g, key);
      entityLayer.append(g);
    });

    // 6. Entities: Egress & Control Plane & External Targets
    {
      const key = "hub:egress";
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === key ? "is-active" : ""}`,
        "data-refresh-key": "hub-egress",
      });
      g.append(
        createSVGEl("rect", { x: egressBox.x, y: egressBox.y, width: egressBox.w, height: egressBox.h, rx: 10, class: "a-dash-topo-hub-box" }),
        createSVGEl("rect", { x: egressBox.x + egressBox.w - 3.5, y: egressBox.y + 8, width: 3.5, height: egressBox.h - 16, rx: 2, fill: EGRESS_FLOW_COLOR }),
        createSVGEl("text", { x: egressBox.x + 12, y: egressBox.y + 21, class: "a-dash-topo-hub-kicker" }, "出站转发汇聚"),
        createSVGEl("text", { x: egressBox.x + 12, y: egressBox.y + 42, class: "a-dash-topo-hub-metric" }, `↑ ${bytes(model.egress.rateBPS)}/s`),
        createSVGEl("text", { x: egressBox.x + 12, y: egressBox.y + 60, class: "a-dash-topo-hub-sub" }, `月发送 ${bytes(model.egress.monthlyBytes)}`),
      );
      bindInteractive(g, key);
      entityLayer.append(g);
    }

    {
      const key = "hub:control";
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === key ? "is-active" : ""}`,
        "data-refresh-key": "hub-control",
      });
      const hostRate = model.egress.panelRxBPS != null
        ? `↓ ${bytes(model.egress.panelRxBPS)}/s`
        : `${model.onlineNodes}/${model.totalNodes} 在线`;
      g.append(
        createSVGEl("rect", { x: controlBox.x, y: controlBox.y, width: controlBox.w, height: controlBox.h, rx: 9, class: "a-dash-topo-hub-box is-subtle" }),
        createSVGEl("text", { x: controlBox.x + 12, y: controlBox.y + 20, class: "a-dash-topo-hub-kicker" }, "主控与调度面"),
        createSVGEl("text", { x: controlBox.x + 12, y: controlBox.y + 41, class: "a-dash-topo-node-sub" }, `${hostRate} · ${model.runningEngines} 内核`),
      );
      bindInteractive(g, key);
      entityLayer.append(g);
    }

    extGeom.forEach((ext) => {
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === ext.item.id ? "is-active" : ""}`,
        "data-refresh-key": `ext-${ext.item.address}`,
      });
      g.append(createSVGEl("title", {}, `外网探测目标 ${ext.item.name} (${ext.item.address})`));
      g.append(
        createSVGEl("rect", { x: ext.x, y: ext.y, width: ext.w, height: ext.h, rx: 8, class: "a-dash-topo-ext-box" }),
        createSVGEl("text", { x: ext.x + 10, y: ext.y + 17, class: "a-dash-topo-ext-kicker" }, "外网探测目标"),
        createSVGEl("text", { x: ext.x + 10, y: ext.y + 32, class: "a-dash-topo-node-sub" }, clipLabel(ext.item.name, Math.max(10, Math.floor((ext.w - 20) / 7)))),
      );
      bindInteractive(g, ext.item.id);
      entityLayer.append(g);
    });

    svg.append(flowLayer, probeLayer, entityLayer);
    return svg;
  };

  const renderMobileSVG = (width, model) => {
    const padX = 10;
    const padY = 10;
    const w = Math.max(260, width);
    const inBusX = padX + 8;
    const outBusX = w - padX - 8;
    const nodeX = padX + 24;
    const nodeW = Math.max(160, outBusX - nodeX - 16);
    const headerH = 54;
    const nodeH = 50;
    const nodeGap = 9;
    const nodesTop = padY + headerH + 20;
    const nodesCount = model.nodes.length;
    const nodesTotalH = nodesCount ? nodesCount * nodeH + (nodesCount - 1) * nodeGap : nodeH;
    const footerTop = nodesTop + nodesTotalH + 20;
    const footerH = 56;
    const totalH = footerTop + footerH + padY;

    const svg = createSVGEl("svg", {
      class: "a-dash-topo-svg is-mobile",
      "data-refresh-key": "topo-svg",
      viewBox: `0 0 ${w} ${totalH}`,
      role: "img",
      "aria-label": "全网节点数据流拓扑图",
    });
    svg.append(createTopologyDefs());

    const flowLayer = createSVGEl("g", { "data-refresh-key": "m-flow-layer" });
    const entityLayer = createSVGEl("g", { "data-refresh-key": "m-entity-layer" });

    const firstCY = nodesTop + nodeH / 2;
    const lastCY = nodesTop + (Math.max(1, nodesCount) - 1) * (nodeH + nodeGap) + nodeH / 2;

    // Left vertical ingress trunk & Right vertical egress trunk
    flowLayer.append(
      createSVGEl("path", {
        d: `M ${inBusX} ${padY + headerH} L ${inBusX} ${lastCY.toFixed(1)}`,
        fill: "none",
        stroke: ACTIVE_FLOW_COLOR,
        "stroke-width": "2.2",
        opacity: "0.55",
      }),
      createSVGEl("path", {
        d: `M ${outBusX} ${firstCY.toFixed(1)} L ${outBusX} ${footerTop}`,
        fill: "none",
        stroke: EGRESS_FLOW_COLOR,
        "stroke-width": "2.2",
        opacity: "0.55",
      }),
    );

    // Top ingress bar
    {
      const g = createSVGEl("g", { "data-refresh-key": "m-hub-ingress", class: "a-dash-topo-card-node" });
      const info = model.ingress.clientIPs != null
        ? `月收 ${bytes(model.ingress.monthlyBytes)} · ${model.ingress.clientIPs} IP`
        : `月收 ${bytes(model.ingress.monthlyBytes)}`;
      g.append(
        createSVGEl("rect", { x: padX, y: padY, width: w - padX * 2, height: headerH, rx: 9, class: "a-dash-topo-hub-box" }),
        createSVGEl("text", { x: padX + 12, y: padY + 20, class: "a-dash-topo-hub-kicker" }, "入站与客户端接入"),
        createSVGEl("text", { x: padX + 12, y: padY + 40, class: "a-dash-topo-hub-metric" }, `↓ ${bytes(model.ingress.rateBPS)}/s`),
        createSVGEl("text", { x: w - padX - 12, y: padY + 40, "text-anchor": "end", class: "a-dash-topo-hub-sub" }, info),
      );
      bindInteractive(g, "hub:ingress");
      entityLayer.append(g);
    }

    // Node stack tapped from both trunks
    const maxChars = Math.max(8, Math.floor((nodeW - 56) / 6.8));
    model.nodes.forEach((item, index) => {
      const y = nodesTop + index * (nodeH + nodeGap);
      const cy = y + nodeH / 2;
      flowLayer.append(
        createSVGEl("path", {
          d: `M ${inBusX} ${cy.toFixed(1)} L ${nodeX - 2} ${cy.toFixed(1)}`,
          fill: "none",
          stroke: item.isOnline ? ACTIVE_FLOW_COLOR : IDLE_FLOW_COLOR,
          "stroke-width": "1.8",
          opacity: item.isOnline ? "0.7" : "0.35",
          "marker-end": item.isOnline ? "url(#a-topo-arrow-rx)" : "url(#a-topo-arrow-idle)",
        }),
        createSVGEl("path", {
          d: `M ${nodeX + nodeW} ${cy.toFixed(1)} L ${outBusX - 2} ${cy.toFixed(1)}`,
          fill: "none",
          stroke: item.blocked ? "#b91c1c" : item.isOnline ? EGRESS_FLOW_COLOR : IDLE_FLOW_COLOR,
          "stroke-width": "1.8",
          opacity: item.isOnline ? "0.7" : "0.35",
          "marker-end": item.blocked ? "url(#a-topo-arrow-bad)" : item.isOnline ? "url(#a-topo-arrow-tx)" : "url(#a-topo-arrow-idle)",
        }),
      );

      const key = `node:${item.id}`;
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === key ? "is-active" : ""}`,
        "data-refresh-key": `m-node-${item.id}`,
      });
      g.append(
        createSVGEl("rect", { x: nodeX, y, width: nodeW, height: nodeH, rx: 8, class: `a-dash-topo-node-box ${activeKey() === key ? "is-active" : ""}` }),
        createSVGEl("circle", { cx: nodeX + 12, cy: y + 18, r: 4, fill: item.blocked ? "#b91c1c" : item.isOnline ? "#15803d" : "#94a3b8" }),
        createSVGEl("text", { x: nodeX + 22, y: y + 21.5, class: "a-dash-topo-node-name" }, clipLabel(item.name, maxChars)),
        createSVGEl("text", { x: nodeX + 12, y: y + 38, class: "a-dash-topo-node-sub" }, `↓ ${bytes(item.rxBPS)}/s · ↑ ${bytes(item.txBPS)}/s`),
      );
      bindInteractive(g, key);
      entityLayer.append(g);
    });

    // Bottom egress + control summary bar
    {
      const g = createSVGEl("g", { "data-refresh-key": "m-hub-egress", class: "a-dash-topo-card-node" });
      g.append(
        createSVGEl("rect", { x: padX, y: footerTop, width: w - padX * 2, height: footerH, rx: 9, class: "a-dash-topo-hub-box" }),
        createSVGEl("text", { x: padX + 12, y: footerTop + 20, class: "a-dash-topo-hub-kicker" }, "出站汇聚与主控面"),
        createSVGEl("text", { x: padX + 12, y: footerTop + 40, class: "a-dash-topo-hub-metric" }, `↑ ${bytes(model.egress.rateBPS)}/s`),
        createSVGEl("text", { x: w - padX - 12, y: footerTop + 40, "text-anchor": "end", class: "a-dash-topo-hub-sub" }, `月发 ${bytes(model.egress.monthlyBytes)} · ${model.onlineNodes}/${model.totalNodes} 在线`),
      );
      bindInteractive(g, "hub:egress");
      entityLayer.append(g);
    }

    svg.append(flowLayer, entityLayer);
    return svg;
  };

  const paint = (force = false) => {
    if (!canView()) return;

    const states = getStates();
    const usage = getUsage();
    const policies = getPolicies();
    const agents = getAgents();
    const probesData = getProbes();

    const model = buildDashboardTopology({
      agents: Array.isArray(agents) ? agents : [],
      pageAgents: policies?.agents || [],
      orderIDs: getOrderIDs(),
      usageRows: usage?.days || [],
      policies: policies?.policies || [],
      probes: probesData?.pairs || [],
      metrics: getMetrics(),
      connections: getConnections(),
      month: getMonth(),
    });
    lastModel = model;

    const waitingKeys = [
      ...(ctx.can("agents.read") ? ["agents"] : []),
      ...(ctx.can("traffic.read") ? ["traffic", "trafficPolicies"] : []),
    ];
    const waiting = waitingKeys.some((key) => !states.has(key) || states.get(key) === "loading");
    const nextStatus = waiting && !model.nodes.length
      ? "读取中…"
      : !model.nodes.length
        ? "暂无可用拓扑数据"
        : "";
    if (statusNode.textContent !== nextStatus) statusNode.textContent = nextStatus;

    const measuredWidth = observedWidth > 0 ? observedWidth : (canvasHost.clientWidth || 0);
    const fallbackWidth = Math.max(300, Math.min(1120, Math.round((window.innerWidth || 1080) - (window.innerWidth > 896 ? 300 : 48))));
    const renderWidth = Math.max(260, Math.round(measuredWidth > 0 ? measuredWidth : fallbackWidth));
    const isMobile = renderWidth < 560;

    const sig = JSON.stringify([renderWidth, isMobile, model, pinnedKey, nextStatus]);
    if (!force && sig === lastPaintSignature) return;
    lastPaintSignature = sig;

    const metaBadges = [
      keyedBadge(`${model.onlineNodes}/${model.totalNodes} 节点在线`, model.onlineNodes ? "ok" : "bad", "topo-nodes"),
      model.probeLinks.length
        ? keyedBadge(
            model.abnormalLinks > 0 ? `${model.abnormalLinks} 条链路告警` : `${model.probeLinks.length} 条实测链路`,
            model.abnormalLinks > 0 ? "warn" : "ok",
            "topo-links",
          )
        : null,
    ].filter(Boolean);
    patchChildren(headerMeta, metaBadges);

    if (model.nodes.length) {
      const freshSvg = isMobile ? renderMobileSVG(renderWidth, model) : renderDesktopSVG(renderWidth, model);
      if (canvasHost.firstChild) patch(canvasHost.firstChild, freshSvg);
      else canvasHost.append(freshSvg);
    } else {
      canvasHost.replaceChildren();
    }

    if (isMobile && model.probeLinks.length) {
      mobileLinksHost.hidden = false;
      patchChildren(
        mobileLinksHost,
        model.probeLinks.map((probe) => {
          const entityKey = `probe:${probe.id}`;
          const active = activeKey() === entityKey;
          const chip = node("button", {
            type: "button",
            className: `a-dash-topo-chip ${active ? "is-active" : ""}`,
            "data-refresh-key": `m-probe-${probe.id}`,
            "aria-pressed": active ? "true" : "false",
          },
          node("span", { className: `a-dash-topo-chip-dot is-${probe.tone || "idle"}` }),
          node("span", { className: "a-dash-topo-chip-name" }, probe.name),
          node("span", { className: "a-dash-topo-chip-val" }, probe.metricLabel));
          listen(chip, "click", () => {
            pinnedKey = pinnedKey === entityKey ? "" : entityKey;
            hoveredKey = "";
            paint(true);
          });
          return chip;
        }),
      );
    } else {
      mobileLinksHost.hidden = true;
      mobileLinksHost.replaceChildren();
    }

    syncInspect();
  };

  if (typeof ResizeObserver === "function") {
    let resizeFrame = 0;
    const observer = new ResizeObserver((entries) => {
      if (!canView() || !ctx.current()) return;
      const next = Math.round(entries[0]?.contentRect?.width || 0);
      if (next > 0 && Math.abs(next - observedWidth) >= 2) {
        observedWidth = next;
        cancelAnimationFrame(resizeFrame);
        resizeFrame = requestAnimationFrame(() => {
          resizeFrame = 0;
          if (canView() && ctx.current()) paint();
        });
      }
    });
    observer.observe(canvasHost);
    ctx.signal.addEventListener("abort", () => {
      cancelAnimationFrame(resizeFrame);
      observer.disconnect();
    }, { once: true });
  }

  const card = canView() && section(
    "全网数据流拓扑",
    node("div", { className: "a-card-body a-dash-topo-body" }, canvasHost, mobileLinksHost, inspectBar),
    node("div", { className: "a-actions a-dash-topo-tools" }, headerMeta, statusNode),
  );
  card?.classList?.add("a-dash-topo-card");

  return {
    visible: Boolean(card),
    element: card,
    paint,
  };
}
