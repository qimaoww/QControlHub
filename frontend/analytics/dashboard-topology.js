import { createSVGEl } from "./dashboard-panel.js";
import { renderPacificMapSVG } from "./dashboard-map.js";
import { PACIFIC_MAP_BOUNDS } from "./pacific-map-data.js";
import { listen, patch } from "./live-dom.js";
import { badge, button, link, muted, node, section } from "./ui.js";
import { buildDashboardTopology, utcMonth } from "../modules/dashboard-model.js";
import { geoRegionDetails } from "../modules/region-model.js";
import {
  BLOCKED_COLOR,
  EGRESS_COLOR,
  IDLE_FLOW_COLOR,
  INGRESS_COLOR,
  OK_COLOR,
  assignProbeLanes,
  bridgeArchPath,
  bridgePointAt,
  buildProbePillLabel,
  buildProbeShortAlertLabel,
  buildTopologySelectionIndex,
  clusterTopologyNodes,
  createTopologyDefs,
  cubicPath,
  entityKey,
  extendedCubicPath,
  extractCityClusterLabel,
  findSameCityMembers,
  flowSpeedClass,
  formatMs,
  isProbeUnreachable,
  matchesActiveSelection,
  placeCollisionFreePill,
  reconcileSelectionKey,
  rectsOverlap,
  selectTopAmbientBadProbeIDs,
  tonePalette,
  visualUnits,
} from "./dashboard-topo-shared.js";

/** SVG traffic split bar obeying strict CSP (no inline CSS styles). */
function createCompactTrafficBarSVG(rxRatio, blocked) {
  const rxPct = Math.max(0, Math.min(100, Math.round((Number(rxRatio) || 0) * 100)));
  const svg = createSVGEl("svg", {
    class: "a-dash-topo-meter-svg",
    viewBox: "0 0 100 4",
    preserveAspectRatio: "none",
    "aria-hidden": "true",
  });
  svg.append(createSVGEl("rect", { x: 0, y: 0, width: 100, height: 4, rx: 2, class: "a-dash-topo-meter-track" }));
  if (rxPct > 0) {
    svg.append(createSVGEl("rect", {
      x: 0,
      y: 0,
      width: rxPct,
      height: 4,
      rx: 2,
      fill: blocked ? BLOCKED_COLOR : INGRESS_COLOR,
    }));
  }
  return svg;
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
  canOpenProbes = false,
  onOpenProbes = null,
  showLedger = false,
  isStandalonePage = false,
}) {
  const canView = () => ctx.can("agents.read") || ctx.can("traffic.read");
  const allowMotion = () => !(typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)")?.matches);

  const VALID_VIEW_MODES = new Set(["flow", "map"]);
  const VALID_PROBE_FILTERS = new Set(["all", "bad", "mesh", "ext"]);
  let pinnedKey = "";
  let hoveredKey = "";
  let viewMode = "flow";
  let probeFilter = "all";
  let viewModeTouched = false;
  let probeFilterTouched = false;
  let observedWidth = 0;
  let lastPaintSignature = "";
  let lastFlowGeomSignature = "";
  let lastModel = null;
  let lastClusters = [];
  let lastTrafficMeta = { readable: true, state: "ready", hasData: true, label: "" };
  let paintScheduled = false;
  let paintForced = false;
  let flowEdgeFrame = 0;
  let disposed = false;

  // Bounded viewport pan/zoom state exclusively for the Pacific map stage.
  let mapZoom = { scale: 1, tx: 0, ty: 0 };
  let suppressNextCanvasClearClick = false;

  ctx.signal.addEventListener("abort", () => {
    disposed = true;
    paintScheduled = false;
    cancelAnimationFrame(flowEdgeFrame);
    lastModel = null;
    lastClusters = [];
  }, { once: true });

  const clampZoomTransform = ({ scale, tx, ty }) => {
    const nextScale = Math.max(1, Math.min(3.2, Number(scale) || 1));
    if (nextScale <= 1.001) return { scale: 1, tx: 0, ty: 0 };
    const vw = PACIFIC_MAP_BOUNDS.width;
    const mh = PACIFIC_MAP_BOUNDS.height;
    const minTx = vw * (1 - nextScale);
    const minTy = mh * (1 - nextScale);
    return {
      scale: nextScale,
      tx: Math.max(minTx, Math.min(0, Number(tx) || 0)),
      ty: Math.max(minTy, Math.min(0, Number(ty) || 0)),
    };
  };

  const resetMapZoom = (shouldRepaint = false) => {
    const changed = mapZoom.scale !== 1 || mapZoom.tx !== 0 || mapZoom.ty !== 0;
    mapZoom = { scale: 1, tx: 0, ty: 0 };
    if (changed && shouldRepaint && viewMode === "map") paint(true);
  };

  const clearSelection = () => {
    if (!pinnedKey && !hoveredKey) return;
    pinnedKey = "";
    hoveredKey = "";
    syncHighlight();
    syncInspect();
  };

  const statusNode = muted("读取中…");
  statusNode.setAttribute("role", "status");

  const headerMeta = node("div", { className: "a-dash-topo-meta" });
  const flowModeBtn = button("拓扑流向", () => setViewMode("flow"), {
    className: "a-dash-topo-mode-btn is-active",
    "data-refresh-key": "topo-mode-flow",
    "data-topo-mode-btn": "flow",
    "aria-pressed": "true",
  });
  const mapModeBtn = button("太平洋地图", () => setViewMode("map"), {
    className: "a-dash-topo-mode-btn",
    "data-refresh-key": "topo-mode-map",
    "data-topo-mode-btn": "map",
    "aria-pressed": "false",
  });
  const modeSwitch = node("div", {
    className: "a-dash-topo-mode-switch",
    role: "group",
    "aria-label": "拓扑视图模式",
  }, flowModeBtn, mapModeBtn);

  const applyViewModeButtons = () => {
    flowModeBtn.classList.toggle("is-active", viewMode === "flow");
    mapModeBtn.classList.toggle("is-active", viewMode === "map");
    flowModeBtn.setAttribute("aria-pressed", viewMode === "flow" ? "true" : "false");
    mapModeBtn.setAttribute("aria-pressed", viewMode === "map" ? "true" : "false");
  };

  const setViewMode = (nextMode, { persist = true, repaint = true } = {}) => {
    const normalized = nextMode === "map" ? "map" : "flow";
    const changed = viewMode !== normalized;
    viewModeTouched = true;
    if (persist && changed) {
      ctx.preferences?.patch?.("dashboard", { topo_view_mode: normalized });
    }
    if (!changed) return;
    viewMode = normalized;
    if (normalized !== "map") resetMapZoom(false);
    applyViewModeButtons();
    if (repaint) paint(true);
  };

  const syncSavedTopologyPreferences = ({ repaint = false } = {}) => {
    if (disposed || !ctx.preferences?.get) return;
    const saved = ctx.preferences.get("dashboard");
    let changed = false;
    if (!viewModeTouched && VALID_VIEW_MODES.has(saved?.topo_view_mode) && saved.topo_view_mode !== viewMode) {
      viewMode = saved.topo_view_mode;
      if (viewMode !== "map") resetMapZoom(false);
      applyViewModeButtons();
      changed = true;
    }
    if (!probeFilterTouched && VALID_PROBE_FILTERS.has(saved?.topo_probe_filter) && saved.topo_probe_filter !== probeFilter) {
      probeFilter = saved.topo_probe_filter;
      changed = true;
    }
    if (changed && repaint && canView() && ctx.current() && lastPaintSignature) {
      paint(true);
    }
  };
  syncSavedTopologyPreferences({ repaint: false });
  if (ctx.preferences) {
    const unsubPrefs = ctx.preferences.subscribe?.(() => syncSavedTopologyPreferences({ repaint: true }));
    if (typeof unsubPrefs === "function") {
      ctx.signal.addEventListener("abort", unsubPrefs, { once: true });
    }
    void ctx.preferences.ready?.().then(() => syncSavedTopologyPreferences({ repaint: true }));
  }

  const detailPageLink = !isStandalonePage && canView()
    ? Object.assign(link("拓扑详情", "#topology"), {
        className: "a-button a-dash-topo-page-btn",
        "data-refresh-key": "open-topology-page",
      })
    : null;
  const openProbesBtn = canOpenProbes && onOpenProbes
    ? button("链路监控", () => onOpenProbes(""), {
        className: "a-button a-dash-topo-probe-btn",
        "data-refresh-key": "open-probes",
      })
    : null;

  const canvasHost = node("div", { className: "a-dash-topo-canvas" });
  const inspectPrimary = node("div", { className: "a-dash-topo-inspect-main" });
  const inspectDetail = node("div", { className: "a-dash-topo-inspect-detail" });
  const inspectBar = node("div", { className: "a-dash-topo-inspect" }, inspectPrimary, inspectDetail);
  const probeMatrixHost = showLedger
    ? node("div", { className: "a-dash-topo-ledger", "data-refresh-key": "topo-ledger" })
    : null;

  const activeKey = () => hoveredKey || pinnedKey;

  const nodeProbesFor = (nodeID, model) => (model?.probeLinks || []).filter(
    (p) => p.from === nodeID || (!p.isExternal && p.to === nodeID),
  );

  const pickPriorityProbe = (links) => {
    const score = { bad: 3, warn: 2, ok: 1 };
    return [...links].sort((a, b) => (score[b.tone] || 0) - (score[a.tone] || 0))[0] || null;
  };

  const selectPinnedEntity = (key) => {
    pinnedKey = key;
    hoveredKey = "";
    syncHighlight();
    syncInspect();
  };

  /** Appends consistent same-city cluster + full-name member switcher pills with active highlight. */
  const appendSameCitySwitchers = (chips, members, currentKey) => {
    if (!Array.isArray(members) || members.length <= 1) return;
    const cKey = entityKey.cluster(members.map((m) => m.id));
    chips.push(button(
      `同城聚合 (${members.length})`,
      () => selectPinnedEntity(cKey),
      {
        className: `a-dash-topo-cluster-btn ${currentKey === cKey ? "is-active" : ""}`,
        "data-cluster-key": cKey,
      },
    ));
    members.forEach((member) => {
      const memberKey = entityKey.node(member.id);
      const memberName = member.name || member.id;
      chips.push(button(
        memberName,
        () => selectPinnedEntity(memberKey),
        {
          className: `a-dash-topo-cluster-btn ${currentKey === memberKey ? "is-active" : ""}`,
          "data-cluster-member": member.id,
          "data-cluster-key": memberKey,
          title: `切换聚焦到 ${memberName}`,
        },
      ));
    });
  };

  const formatHubTrafficChips = (direction, model, tMeta) => {
    if (!tMeta.readable) return [badge("无流量权限", "warn")];
    if (!tMeta.hasData && tMeta.state === "loading") return [muted("流量读取中…")];
    if (!tMeta.hasData && tMeta.state === "failed") return [badge("流量读取失败", "warn")];
    if (direction === "ingress") {
      const out = [
        muted(`实时入站 ↓ ${bytes(model.ingress.rateBPS)}/s`),
        muted(`本月接收 ${bytes(model.ingress.monthlyBytes)}`),
      ];
      if (model.ingress.clientIPs != null) {
        out.push(muted(`24h ${model.ingress.clientIPs} IP · ${model.ingress.clientFlows} 流`));
      }
      if (tMeta.state === "partial-failed") out.push(badge("刷新失败 · 保留已有记录", "warn"));
      return out;
    }
    const out = [
      muted(`实时出站 ↑ ${bytes(model.egress.rateBPS)}/s`),
      muted(`本月发送 ${bytes(model.egress.monthlyBytes)}`),
      muted(`${model.onlineNodes}/${model.totalNodes} 节点在线`),
    ];
    if (tMeta.state === "partial-failed") out.push(badge("刷新失败 · 保留已有记录", "warn"));
    return out;
  };

  const formatNodeTrafficChips = (item, tMeta) => {
    if (!tMeta.readable) return [muted("无流量权限")];
    if (!tMeta.hasData && tMeta.state === "loading") return [muted("流量读取中…")];
    if (!tMeta.hasData && tMeta.state === "failed") return [muted("流量读取失败")];
    return [
      muted(`↓ ${bytes(item.rxBPS)}/s · ↑ ${bytes(item.txBPS)}/s`),
      muted(`本月 ${bytes(item.monthlyUsedBytes)}`),
    ];
  };

  const formatClusterTrafficChip = (members, tMeta) => {
    if (!tMeta.readable) return muted("无流量权限");
    if (!tMeta.hasData && tMeta.state === "loading") return muted("流量读取中…");
    if (!tMeta.hasData && tMeta.state === "failed") return muted("流量读取失败");
    const totalRx = members.reduce((s, m) => s + (m.rxBPS || 0), 0);
    const totalTx = members.reduce((s, m) => s + (m.txBPS || 0), 0);
    return muted(`合计 ↓ ${bytes(totalRx)}/s · ↑ ${bytes(totalTx)}/s`);
  };

  const resolveKeyDescription = (key, model, tMeta = lastTrafficMeta) => {
    if (!model || !model.nodes.length) {
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, "全网数据流拓扑"), muted(statusNode.textContent || "等待节点与流量样本…")],
        detail: [],
      };
    }

    if (key === "hub:ingress") {
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, "入站流量")],
        detail: formatHubTrafficChips("ingress", model, tMeta),
      };
    }

    if (key === "hub:egress") {
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, "出站流量")],
        detail: formatHubTrafficChips("egress", model, tMeta),
      };
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
        if (item.lossPercent != null) chips.push(muted(`丢包 ${item.lossPercent.toFixed(item.lossPercent % 1 ? 1 : 0)}%`));
        if (canOpenProbes && onOpenProbes) {
          chips.push(button("链路监控", () => onOpenProbes(item.id), { className: "a-button" }));
        }
        chips.push(button("取消聚焦", clearSelection, { className: "a-button", "data-refresh-key": "topo-unpin" }));
        return {
          primary: [node("span", { className: "a-dash-topo-inspect-title" }, item.name)],
          detail: chips,
        };
      }
    }

    const ext = model.externalTargets.find((e) => e.id === key);
    if (ext) {
      const related = model.probeLinks.filter((p) => p.isExternal && p.to === ext.id);
      const chips = [
        muted("公网目标"),
        ext.address && ext.address !== ext.name ? node("code", { className: "a-dash-topo-code" }, ext.address) : null,
        muted(`${related.length} 条探测链路`),
      ].filter(Boolean);
      const priority = pickPriorityProbe(related);
      if (canOpenProbes && onOpenProbes && priority) {
        chips.push(button("链路监控", () => onOpenProbes(priority.id), { className: "a-button" }));
      }
      chips.push(button("取消聚焦", clearSelection, { className: "a-button", "data-refresh-key": "topo-unpin" }));
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, ext.name)],
        detail: chips,
      };
    }

    if (key?.startsWith("cluster:")) {
      const memberIDs = key.slice("cluster:".length).split(",").filter(Boolean);
      const members = memberIDs.map((id) => model.nodes.find((n) => n.id === id)).filter(Boolean);
      if (members.length) {
        const primaryNode = members[0];
        const region = geoRegionDetails(primaryNode.regionCode);
        const cityName = extractCityClusterLabel(members);
        const onlineCount = members.filter((m) => m.isOnline).length;
        const memberSet = new Set(memberIDs);
        const relatedProbes = model.probeLinks.filter((p) => memberSet.has(p.from) || (!p.isExternal && memberSet.has(p.to)));
        const priorityProbe = pickPriorityProbe(relatedProbes);

        const chips = [
          badge(`${onlineCount}/${members.length} 在线`, onlineCount === members.length ? "ok" : onlineCount > 0 ? "warn" : "bad"),
          formatClusterTrafficChip(members, tMeta),
        ];
        appendSameCitySwitchers(chips, members, key);
        if (canOpenProbes && onOpenProbes && relatedProbes.length) {
          chips.push(button(`关联链路 (${relatedProbes.length})`, () => onOpenProbes(priorityProbe?.id || "")));
        }
        chips.push(button("取消聚焦", clearSelection, { className: "a-button", "data-refresh-key": "topo-unpin" }));
        return {
          primary: [
            node("span", { className: "a-dash-topo-inspect-title" }, `${region ? `${region.code} · ` : ""}${cityName} 同城聚合 (${members.length} 节点)`),
          ],
          detail: chips,
        };
      }
    }

    if (key?.startsWith("node:")) {
      const nodeID = key.slice("node:".length);
      const item = model.nodes.find((n) => n.id === nodeID);
      if (item) {
        const region = geoRegionDetails(item.regionCode);
        const relatedProbes = nodeProbesFor(item.id, model);
        const priorityProbe = pickPriorityProbe(relatedProbes);
        const chips = [
          badge(item.isOnline ? "在线" : "离线", item.isOnline ? "ok" : "bad"),
          ...formatNodeTrafficChips(item, tMeta),
        ];
        if (item.activeEngines.length) chips.push(muted(`内核 ${item.activeEngines.join(" / ")}`));
        if (item.blocked) chips.push(badge("已超限阻断", "bad"));
        if (viewMode === "map") {
          appendSameCitySwitchers(chips, findSameCityMembers(model.nodes, item.id), key);
        }
        if (canOpenProbes && onOpenProbes && relatedProbes.length) {
          chips.push(button(`关联链路 (${relatedProbes.length})`, () => onOpenProbes(priorityProbe?.id || "")));
        }
        if (ctx.can("traffic.read")) {
          chips.push(link("流量明细", `#traffic?agent=${encodeURIComponent(item.id)}`));
        }
        chips.push(button("取消聚焦", clearSelection, { className: "a-button", "data-refresh-key": "topo-unpin" }));
        return {
          primary: [
            node("span", { className: "a-dash-topo-inspect-title" }, `${region ? `${region.code} · ` : ""}${item.name}`),
            item.ip ? node("code", { className: "a-dash-topo-code" }, item.ip) : null,
          ].filter(Boolean),
          detail: chips,
        };
      }
    }

    const summaryDetail = !tMeta.readable
      ? [badge("无流量权限", "warn")]
      : (!tMeta.hasData && tMeta.state === "loading")
        ? [muted("流量读取中…")]
        : (!tMeta.hasData && tMeta.state === "failed")
          ? [badge("流量读取失败", "warn")]
          : [muted(`↓ ${bytes(model.ingress.rateBPS)}/s · ↑ ${bytes(model.egress.rateBPS)}/s`)];
    if (model.hiddenNodeCount > 0) summaryDetail.push(muted(`概览展示 ${model.nodes.length}/${model.totalNodes} 节点`));
    if (model.totalProbeLinks > 0) summaryDetail.push(muted(`探测链路 ${model.totalProbeLinks}`));
    return {
      primary: [node("span", { className: "a-dash-topo-inspect-title" }, "实时流量汇总")],
      detail: summaryDetail,
    };
  };

  const syncInspect = () => {
    const focusedClusterKey = inspectDetail.contains(document.activeElement)
      ? document.activeElement?.getAttribute?.("data-cluster-key") || ""
      : "";
    const { primary, detail } = resolveKeyDescription(activeKey(), lastModel, lastTrafficMeta);
    inspectPrimary.replaceChildren(...primary);
    inspectDetail.replaceChildren(...detail);
    if (focusedClusterKey) {
      const target = [...inspectDetail.querySelectorAll("[data-cluster-key]")].find((el) => el.getAttribute("data-cluster-key") === focusedClusterKey);
      target?.focus?.();
    }
  };

  const syncHighlight = () => {
    const sel = activeKey();
    const roots = [canvasHost.querySelector(".a-dash-topo-flow-board"), canvasHost.querySelector("svg.a-dash-topo-svg")].filter(Boolean);
    for (const root of roots) {
      root.querySelectorAll("[data-topo-peers]").forEach((el) => {
        const peers = (el.getAttribute("data-topo-peers") || "").split(" ").filter(Boolean);
        const match = matchesActiveSelection(peers, sel);
        if (el.namespaceURI === "http://www.w3.org/2000/svg") {
          const base = el.getAttribute("data-base-opacity") || "1";
          const dimOpacity = el.getAttribute("data-dim-opacity") ?? (el.classList.contains("a-dash-topo-card-node") ? "0.35" : "0.14");
          const next = match === true ? "1" : match === false ? dimOpacity : base;
          if (el.getAttribute("opacity") !== next) el.setAttribute("opacity", next);
          el.classList.toggle("is-highlighted", match === true);
        } else {
          el.classList.toggle("is-highlighted", match === true);
          el.classList.toggle("is-dimmed", match === false);
        }
      });
      root.querySelectorAll("[data-topo-entity]").forEach((el) => {
        const key = el.getAttribute("data-topo-entity") || "";
        const isClusterMemberPinned = key.startsWith("cluster:") && pinnedKey.startsWith("node:") && key.slice(8).split(",").includes(pinnedKey.slice(5));
        const isNodeInPinnedCluster = key.startsWith("node:") && pinnedKey.startsWith("cluster:") && pinnedKey.slice(8).split(",").includes(key.slice(5));
        const activeMatch = pinnedKey === key;
        const visualActive = activeMatch || isClusterMemberPinned || isNodeInPinnedCluster;
        el.setAttribute("aria-pressed", activeMatch ? "true" : "false");
        el.classList.toggle("is-active", visualActive);
        el.classList.toggle("is-hovered", Boolean(hoveredKey && hoveredKey === key));
        if (el.classList.contains("a-dash-topo-card-node")) {
          const boxEl = el.querySelector(".a-dash-topo-node-box, .a-dash-topo-ext-box");
          boxEl?.classList.toggle("is-active", visualActive || el.classList.contains("is-highlighted"));
        }
      });
    }
    probeMatrixHost?.querySelectorAll(".a-dash-topo-link-card").forEach((card) => {
      const peers = (card.getAttribute("data-topo-peers") || "").split(" ").filter(Boolean);
      const key = card.getAttribute("data-topo-entity") || "";
      const match = matchesActiveSelection(peers, sel);
      card.classList.toggle("is-highlighted", match === true);
      card.classList.toggle("is-dimmed", match === false);
      card.classList.toggle("is-active", pinnedKey === key);
      card.setAttribute("aria-pressed", pinnedKey === key ? "true" : "false");
    });
  };

  const bindInteractive = (el, entityKey, { openProbeID = "" } = {}) => {
    el.setAttribute("tabindex", "0");
    el.setAttribute("role", "button");
    el.setAttribute("data-topo-entity", entityKey);
    el.setAttribute("aria-pressed", pinnedKey === entityKey ? "true" : "false");
    listen(el, "pointerenter", () => {
      if (hoveredKey !== entityKey) {
        hoveredKey = entityKey;
        syncHighlight();
        syncInspect();
      }
    });
    listen(el, "pointerleave", () => {
      if (hoveredKey === entityKey) {
        hoveredKey = "";
        syncHighlight();
        syncInspect();
      }
    });
    const activate = (event) => {
      if (event.type === "keydown" && event.key !== "Enter" && event.key !== " ") return;
      if (event.type === "keydown") event.preventDefault();
      event.stopPropagation();
      if (openProbeID && canOpenProbes && onOpenProbes) {
        hoveredKey = "";
        syncHighlight();
        syncInspect();
        onOpenProbes(openProbeID);
        return;
      }
      selectPinnedEntity(pinnedKey === entityKey ? "" : entityKey);
    };
    listen(el, "click", activate);
    listen(el, "keydown", activate);
  };

  // Clear focus cleanly on canvas blank clicks, Escape, or mouse leaving hosts.
  listen(canvasHost, "pointerleave", () => {
    if (hoveredKey) {
      hoveredKey = "";
      syncHighlight();
      syncInspect();
    }
  });
  probeMatrixHost && listen(probeMatrixHost, "pointerleave", () => {
    if (hoveredKey) {
      hoveredKey = "";
      syncHighlight();
      syncInspect();
    }
  });
  listen(canvasHost, "click", (event) => {
    if (suppressNextCanvasClearClick) {
      suppressNextCanvasClearClick = false;
      return;
    }
    if (event.target.closest?.("[data-topo-entity], .a-dash-topo-map-tools")) return;
    clearSelection();
  });
  listen(document, "keydown", (event) => {
    if (event.key !== "Escape") return;
    if (document.querySelector("dialog[open]")) return;
    if (pinnedKey || hoveredKey) {
      clearSelection();
    } else if (viewMode === "map" && mapZoom.scale > 1.001) {
      resetMapZoom(true);
    }
  });

  const isHighlightedEdge = (peers) => matchesActiveSelection(peers, activeKey());

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

  const syncLedger = () => {
    if (!showLedger || !probeMatrixHost) return;
    const model = lastModel;
    const probesStatus = getStates()?.get?.("probes") || "";
    const links = model?.probeLinks || [];

    if (!links.length) {
      if (probesStatus === "failed") {
        probeMatrixHost.hidden = false;
        patchChildren(probeMatrixHost, [
          node("div", { className: "a-dash-topo-ledger-empty", "data-refresh-key": "ledger-failed" }, "探测链路读取失败，请刷新重试。"),
        ]);
        return;
      }
      probeMatrixHost.hidden = true;
      if (probeMatrixHost.childElementCount) probeMatrixHost.replaceChildren();
      return;
    }
    probeMatrixHost.hidden = false;

    const selIndex = buildTopologySelectionIndex(model);
    const nodeNameByID = new Map((model.nodes || []).map((n) => [n.id, n.name || n.id]));
    const extNameByID = new Map((model.externalTargets || []).map((e) => [e.id, e.name || e.address || e.id]));
    const fullNameOf = (id) => {
      const s = String(id || "");
      if (s.startsWith("ext:")) return extNameByID.get(s) || s.slice(4);
      return nodeNameByID.get(s) || s;
    };

    const badCount = links.filter((p) => p.tone === "bad" || p.tone === "warn").length;
    const meshCount = links.filter((p) => !p.isExternal).length;
    const extCount = links.length - meshCount;

    const effectiveFilter = (probeFilter === "bad" && !badCount) ? "all" : probeFilter;
    const filterDefs = [
      { id: "all", label: `全部 (${links.length})` },
      { id: "bad", label: `异常 (${badCount})` },
      { id: "mesh", label: `节点间 (${meshCount})` },
      { id: "ext", label: `公网 (${extCount})` },
    ];

    const headEl = node(
      "div",
      { className: "a-dash-topo-ledger-head", "data-refresh-key": "ledger-head" },
      node(
        "div",
        { className: "a-dash-topo-ledger-title", "data-refresh-key": "ledger-title" },
        node("strong", { "data-refresh-key": "ledger-heading" }, "探测链路列表"),
        node("span", { "data-refresh-key": "ledger-sub" }, `共 ${links.length} 条 · ${meshCount} 节点间 · ${extCount} 公网${badCount ? ` · 异常 ${badCount}` : ""}`),
      ),
      node(
        "div",
        { className: "a-dash-topo-filter-group", role: "group", "aria-label": "探测链路筛选", "data-refresh-key": "ledger-filters" },
        ...filterDefs.map((f) => button(f.label, () => {
          const changed = probeFilter !== f.id;
          probeFilterTouched = true;
          if (changed) ctx.preferences?.patch?.("dashboard", { topo_probe_filter: f.id });
          if (!changed && effectiveFilter === f.id) return;
          probeFilter = f.id;
          syncLedger();
          syncHighlight();
        }, {
          className: "a-dash-topo-filter-btn",
          "data-filter": f.id,
          "aria-pressed": effectiveFilter === f.id ? "true" : "false",
          "data-refresh-key": `ledger-filter-${f.id}`,
        })),
      ),
    );

    const filtered = links.filter((p) => {
      if (effectiveFilter === "bad") return p.tone === "bad" || p.tone === "warn";
      if (effectiveFilter === "mesh") return !p.isExternal;
      if (effectiveFilter === "ext") return Boolean(p.isExternal);
      return true;
    });

    const toneRank = { bad: 0, warn: 1, idle: 2, ok: 3 };
    const sorted = [...filtered].sort((a, b) => {
      const ra = toneRank[a.tone] ?? 2;
      const rb = toneRank[b.tone] ?? 2;
      if (ra !== rb) return ra - rb;
      if (Boolean(a.isExternal) !== Boolean(b.isExternal)) return a.isExternal ? 1 : -1;
      return (Number(b.lossPercent) || 0) - (Number(a.lossPercent) || 0) || (Number(b.latencyMS) || 0) - (Number(a.latencyMS) || 0);
    });

    const formatRttText = (probe) => {
      if (probe.running) return "探测中…";
      if (isProbeUnreachable(probe)) return probe.lossPercent != null && probe.lossPercent >= 100 ? "不可达 · 100% 丢包" : (probe.metricLabel || "不可达");
      if (probe.bidirectional && probe.revLatencyMS != null) return `去 ${formatMs(probe.latencyMS)} · 回 ${formatMs(probe.revLatencyMS)}`;
      return `${probe.isExternal ? "延迟" : "单向"} ${formatMs(probe.latencyMS)}`;
    };
    const formatLossBadge = (probe) => {
      if (probe.running) return "探测中";
      if (isProbeUnreachable(probe) && probe.lossPercent != null && probe.lossPercent >= 100) return "不可达";
      if (probe.lossPercent == null) return probe.statusLabel || "正常";
      const lossNum = probe.lossPercent.toFixed(probe.lossPercent % 1 ? 1 : 0);
      return probe.lossPercent > 0 ? `丢包 ${lossNum}%` : "无丢包";
    };

    const gridBody = !sorted.length
      ? node("div", { className: "a-dash-topo-ledger-empty", "data-refresh-key": "ledger-empty" }, "暂无匹配链路")
      : node(
          "div",
          { className: "a-dash-topo-ledger-grid", "data-refresh-key": "ledger-grid" },
          ...sorted.map((probe) => {
            const eKey = entityKey.probe(probe.id);
            const peers = selIndex.probePeers(probe);
            const arrow = probe.bidirectional ? "↔" : "→";
            const fromTitle = fullNameOf(probe.from);
            const toTitle = fullNameOf(probe.to);
            const routeTitle = `${fromTitle} ${arrow} ${toTitle}`;
            const cardEl = node(
              "button",
              {
                type: "button",
                className: "a-dash-topo-link-card",
                "data-refresh-key": `ledger-card-${probe.id}`,
                "data-tone": probe.tone || "idle",
                "data-topo-peers": peers.join(" "),
                title: `${routeTitle} · ${formatRttText(probe)} · ${formatLossBadge(probe)}`,
              },
              node(
                "div",
                { className: "a-dash-topo-link-top", "data-refresh-key": "top" },
                node(
                  "span",
                  { className: "a-dash-topo-link-route", "data-refresh-key": "route" },
                  node("span", { className: "a-dash-topo-link-ep" }, fromTitle),
                  ` ${arrow} `,
                  node("span", { className: "a-dash-topo-link-ep" }, toTitle),
                ),
                node("span", { className: "a-dash-topo-link-loss", "data-refresh-key": "loss" }, formatLossBadge(probe)),
              ),
              node(
                "div",
                { className: "a-dash-topo-link-metrics", "data-refresh-key": "metrics" },
                node("span", { className: "a-dash-topo-link-rtt", "data-refresh-key": "rtt" }, formatRttText(probe)),
                node("span", { className: "a-dash-topo-link-kind", "data-refresh-key": "kind" }, probe.isExternal ? "公网目标" : (probe.bidirectional ? "双向探测" : "单向探测")),
              ),
            );
            bindInteractive(cardEl, eKey, { openProbeID: probe.id });
            return cardEl;
          }),
        );

    patchChildren(probeMatrixHost, [headEl, gridBody]);
  };

  const keyedBadge = (text, tone, refreshKey) => {
    const el = badge(text, tone);
    el.setAttribute("data-refresh-key", refreshKey);
    return el;
  };

  const renderFlowCurve = ({ d, peers, baseOpacity, glowColor, glowWidth, stroke, strokeWidth, dash, markerEnd, pulseColor, pulseOpacity, pulseWeight = 0.5, photonColor, rateLabel, rateColor, labelPos, labelAnchor = "middle", labelLayer = null, labelOffsetY = -5, refreshKey }) => {
    const hl = isHighlightedEdge(peers);
    const opacity = hl === true ? 1 : hl === false ? 0.14 : baseOpacity;
    const g = createSVGEl("g", {
      "data-refresh-key": refreshKey,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": String(baseOpacity),
      opacity: opacity.toFixed(2),
    });
    if (glowColor) {
      g.append(createSVGEl("path", {
        d,
        fill: "none",
        stroke: glowColor,
        "stroke-width": glowWidth.toFixed(1),
        "stroke-linecap": "round",
        opacity: "0.08",
      }));
    }
    g.append(createSVGEl("path", {
      d,
      fill: "none",
      stroke,
      "stroke-width": strokeWidth.toFixed(2),
      "stroke-dasharray": dash || null,
      "stroke-linecap": "round",
      "marker-end": markerEnd,
      class: pulseColor ? "a-dash-topo-flow-active" : "a-dash-topo-flow-idle",
    }));
    if (pulseColor) {
      g.append(createSVGEl("path", {
        d,
        fill: "none",
        stroke: pulseColor,
        "stroke-width": Math.max(1.25, strokeWidth * 0.46).toFixed(2),
        "stroke-dasharray": "4 14",
        "stroke-linecap": "round",
        opacity: pulseOpacity.toFixed(2),
        class: `a-dash-topo-flow-pulse ${flowSpeedClass(pulseWeight)}`,
      }));
    }
    if (photonColor && allowMotion()) {
      const photon = createSVGEl("circle", {
        "data-refresh-key": `${refreshKey}-photon`,
        r: "2.2",
        fill: "#ffffff",
        stroke: photonColor,
        "stroke-width": "1.3",
        opacity: "0.95",
      });
      photon.append(createSVGEl("animateMotion", {
        "data-refresh-key": `${refreshKey}-motion`,
        path: d,
        dur: `${Math.max(1.0, 2.55 - (Number(pulseWeight) || 0) * 1.45).toFixed(2)}s`,
        repeatCount: "indefinite",
      }));
      g.append(photon);
    }
    if (rateLabel && labelPos) {
      const targetLayer = labelLayer || g;
      targetLayer.append(createSVGEl("text", {
        x: labelPos.x.toFixed(1),
        y: (labelPos.y + labelOffsetY).toFixed(1),
        "text-anchor": labelAnchor,
        class: "a-dash-topo-flow-label",
        fill: rateColor,
        "paint-order": "stroke",
        stroke: "#fafafa",
        "stroke-width": "3.6",
        "stroke-linejoin": "round",
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": String(baseOpacity > 0.4 ? 0.96 : 0.72),
        opacity: (hl === false ? 0.16 : baseOpacity > 0.4 ? 0.96 : 0.72).toFixed(2),
      }, rateLabel));
    }
    return g;
  };

  const createProbePillGroup = ({ probe, cx, cy, pillW, fullW = pillW, pillH, label, shortLabel = label, pal, compact = false, peers, focusOnly = false }) => {
    const eKey = entityKey.probe(probe.id);
    const hl = isHighlightedEdge(peers);
    const selected = activeKey() === eKey;
    const baseStr = focusOnly ? "0" : "1";
    const dimStr = focusOnly ? "0" : "0.22";
    const op = focusOnly ? (hl === true ? "1" : "0") : (hl === false ? dimStr : "1");
    const g = createSVGEl("g", {
      class: `a-dash-topo-probe-item ${focusOnly ? "a-dash-topo-focus-only" : ""} ${selected ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `probe-pill-${probe.id}`,
      "data-probe-id": probe.id,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": baseStr,
      "data-dim-opacity": dimStr,
      opacity: op,
    });
    g.append(createSVGEl("title", {}, `${probe.name}: ${probe.metricLabel} (${probe.statusLabel}) — 点击打开链路监控`));
    g.append(createSVGEl("rect", {
      x: (cx - pillW / 2).toFixed(1),
      y: (cy - pillH / 2).toFixed(1),
      width: pillW.toFixed(1),
      height: pillH,
      rx: (pillH / 2).toFixed(1),
      fill: pal.fill,
      stroke: pal.border,
      "stroke-width": selected || hl === true ? "1.8" : "1.1",
      class: "a-dash-topo-probe-pill",
    }));

    const dotX = cx - pillW / 2 + (compact ? 8 : 10);
    if (isProbeUnreachable(probe) && probe.tone === "bad") {
      g.append(createSVGEl("path", {
        class: "a-dash-topo-pill-dot",
        d: `M ${(dotX - 2.3).toFixed(1)} ${(cy - 2.3).toFixed(1)} L ${(dotX + 2.3).toFixed(1)} ${(cy + 2.3).toFixed(1)} M ${(dotX + 2.3).toFixed(1)} ${(cy - 2.3).toFixed(1)} L ${(dotX - 2.3).toFixed(1)} ${(cy + 2.3).toFixed(1)}`,
        fill: "none",
        stroke: pal.stroke,
        "stroke-width": "1.5",
        "stroke-linecap": "round",
      }));
    } else {
      g.append(createSVGEl("circle", { class: "a-dash-topo-pill-dot", cx: dotX.toFixed(1), cy: cy.toFixed(1), r: compact ? 2.6 : 3, fill: pal.stroke }));
    }

    g.append(createSVGEl("text", {
      x: (dotX + (compact ? 6.5 : 7.5)).toFixed(1),
      y: (cy + (compact ? 3.2 : 3.6)).toFixed(1),
      class: "a-dash-topo-pill-text",
      fill: pal.ink,
    }, label));

    bindInteractive(g, eKey, { openProbeID: probe.id });
    return g;
  };

  const appendProbeLeader = (layer, placed, pal, peers, hl, compact = false, focusOnly = false) => {
    if (!placed.shifted) return;
    const baseStr = focusOnly ? "0" : "0.86";
    const dimStr = focusOnly ? "0" : "0.18";
    const op = focusOnly ? (hl === true ? "1" : "0") : (hl === false ? "0.18" : "0.86");
    const attrs = {
      class: focusOnly ? "a-dash-topo-focus-only" : null,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": baseStr,
      "data-dim-opacity": dimStr,
      opacity: op,
    };
    layer.append(
      createSVGEl("line", {
        x1: placed.anchorX.toFixed(1),
        y1: placed.anchorY.toFixed(1),
        x2: placed.cx.toFixed(1),
        y2: placed.cy.toFixed(1),
        stroke: pal.border,
        "stroke-width": compact ? "0.95" : "1.1",
        "stroke-dasharray": "2 2",
        ...attrs,
      }),
      createSVGEl("circle", {
        cx: placed.anchorX.toFixed(1),
        cy: placed.anchorY.toFixed(1),
        r: compact ? 1.9 : 2.3,
        fill: pal.stroke,
        ...attrs,
      }),
    );
  };

  /** Renders semantic HTML flow board (guaranteed untruncated wrapped names) + underlying SVG edge layer. */
  const buildFlowBoardElement = (model, isMobile, tMeta) => {
    const selIndex = buildTopologySelectionIndex(model);

    const svgEl = createSVGEl("svg", {
      class: `a-dash-topo-svg a-dash-topo-flow-svg ${isMobile ? "is-mobile" : ""}`,
      viewBox: "0 0 600 320",
      role: "img",
      "aria-label": "服务与节点实时流量探测拓扑图",
      "data-refresh-key": "flow-edge-svg",
    });
    // Keep the SVG DOM identity stable across edge re-measurements on resize/data updates.
    svgEl.reconcileTable = () => true;

    const ingressMetricsText = !tMeta.readable
      ? "无流量权限"
      : (!tMeta.hasData && tMeta.state === "loading")
        ? "读取中…"
        : (!tMeta.hasData && tMeta.state === "failed")
          ? "读取失败"
          : `↓ ${bytes(model.ingress.rateBPS)}/s`;
    const ingressSubText = !tMeta.readable
      ? "权限受限"
      : (!tMeta.hasData && (tMeta.state === "loading" || tMeta.state === "failed"))
        ? (model.ingress.clientIPs != null ? `24h ${model.ingress.clientIPs} IP` : "等待样本")
        : `本月 ${bytes(model.ingress.monthlyBytes)}${model.ingress.clientIPs != null ? ` · ${model.ingress.clientIPs} IP` : ""}`;

    const ingressCard = node(
      "button",
      {
        type: "button",
        className: "a-dash-topo-flow-card is-hub",
        "data-refresh-key": "flow-hub-ingress",
        "data-flow-port": "hub:ingress",
        "data-topo-peers": selIndex.hubPeers.ingress.join(" "),
      },
      node("span", { className: "a-dash-topo-flow-kicker" }, "入站流量"),
      node("strong", { className: "a-dash-topo-flow-metric" }, ingressMetricsText),
      node("span", { className: "a-dash-topo-flow-sub" }, ingressSubText),
    );
    bindInteractive(ingressCard, "hub:ingress");

    const egressMetricsText = !tMeta.readable
      ? "无流量权限"
      : (!tMeta.hasData && tMeta.state === "loading")
        ? "读取中…"
        : (!tMeta.hasData && tMeta.state === "failed")
          ? "读取失败"
          : `↑ ${bytes(model.egress.rateBPS)}/s`;
    const egressSubText = !tMeta.readable
      ? `${model.onlineNodes}/${model.totalNodes} 节点在线`
      : (!tMeta.hasData && (tMeta.state === "loading" || tMeta.state === "failed"))
        ? `${model.onlineNodes}/${model.totalNodes} 节点在线`
        : `本月 ${bytes(model.egress.monthlyBytes)} · ${model.onlineNodes}/${model.totalNodes} 在线`;

    const egressCard = node(
      "button",
      {
        type: "button",
        className: "a-dash-topo-flow-card is-hub",
        "data-refresh-key": "flow-hub-egress",
        "data-flow-port": "hub:egress",
        "data-topo-peers": selIndex.hubPeers.egress.join(" "),
      },
      node("span", { className: "a-dash-topo-flow-kicker" }, "出站流量"),
      node("strong", { className: "a-dash-topo-flow-metric" }, egressMetricsText),
      node("span", { className: "a-dash-topo-flow-sub" }, egressSubText),
    );
    bindInteractive(egressCard, "hub:egress");

    const nodeCards = model.nodes.map((item) => {
      const eKey = entityKey.node(item.id);
      const peers = selIndex.nodePeers(item.id);
      const region = geoRegionDetails(item.regionCode);
      const totalRate = (item.rxBPS || 0) + (item.txBPS || 0);
      const totalMonthly = (item.monthlyRxBytes || 0) + (item.monthlyTxBytes || 0);
      const rxRatio = totalRate > 0 ? item.rxBPS / totalRate : totalMonthly > 0 ? item.monthlyRxBytes / totalMonthly : 0.5;
      const trafficLine = !tMeta.readable
        ? "无流量权限"
        : (!tMeta.hasData && tMeta.state === "loading")
          ? "读取中…"
          : (!tMeta.hasData && tMeta.state === "failed")
            ? "读取失败"
            : `↓ ${bytes(item.rxBPS)}/s · ↑ ${bytes(item.txBPS)}/s`;
      const monthLine = (!tMeta.readable || (!tMeta.hasData && (tMeta.state === "loading" || tMeta.state === "failed")))
        ? (item.ip || "")
        : `本月 ${bytes(item.monthlyUsedBytes)}`;

      const statusBadge = item.blocked
        ? badge("超限阻断", "bad")
        : badge(item.isOnline ? "在线" : "离线", item.isOnline ? "ok" : "bad");
      const engineChip = item.activeEngines.length
        ? node("span", { className: "a-dash-topo-flow-tag" }, item.activeEngines[0])
        : null;

      const cardEl = node(
        "button",
        {
          type: "button",
          className: `a-dash-topo-flow-card is-node ${item.blocked ? "is-blocked" : ""} ${!item.isOnline ? "is-offline" : ""}`,
          "data-refresh-key": `flow-node-${item.id}`,
          "data-flow-port": eKey,
          "data-topo-peers": peers.join(" "),
          title: `${item.name}${item.ip ? ` (${item.ip})` : ""}`,
        },
        node(
          "div",
          { className: "a-dash-topo-flow-node-head" },
          region ? node("span", { className: "a-dash-topo-flow-region" }, region.code) : null,
          node("strong", { className: "a-dash-topo-flow-node-name" }, item.name || item.id),
          statusBadge,
          engineChip,
        ),
        node(
          "div",
          { className: "a-dash-topo-flow-node-foot" },
          node("span", { className: "a-dash-topo-flow-rate" }, trafficLine),
          monthLine ? node("span", { className: "a-dash-topo-flow-month" }, monthLine) : null,
        ),
        tMeta.readable && tMeta.hasData ? createCompactTrafficBarSVG(rxRatio, item.blocked) : null,
      );
      bindInteractive(cardEl, eKey);
      return cardEl;
    });

    const extCards = model.externalTargets.map((ext) => {
      const eKey = entityKey.external(ext.id);
      const peers = selIndex.extPeers(ext.id);
      const showAddr = ext.address && ext.address !== ext.name;
      const cardEl = node(
        "button",
        {
          type: "button",
          className: "a-dash-topo-flow-card is-ext",
          "data-refresh-key": `flow-ext-${ext.id}`,
          "data-flow-port": eKey,
          "data-topo-peers": peers.join(" "),
          title: `${ext.name}${showAddr ? ` (${ext.address})` : ""} · ${ext.linkCount} 条链路`,
        },
        node("span", { className: "a-dash-topo-flow-kicker" }, `公网目标 · ${ext.linkCount} 链路`),
        node("strong", { className: "a-dash-topo-flow-node-name" }, ext.name || ext.address || ext.id),
        showAddr ? node("span", { className: "a-dash-topo-flow-sub" }, ext.address) : null,
      );
      bindInteractive(cardEl, eKey);
      return cardEl;
    });

    const nodesColTitle = model.hiddenNodeCount > 0
      ? `节点 (${model.nodes.length}/${model.totalNodes})`
      : `节点 (${model.nodes.length})`;

    const gridEl = node(
      "div",
      { className: "a-dash-topo-flow-grid", "data-refresh-key": "flow-grid" },
      node("div", { className: "a-dash-topo-flow-col is-ingress", "data-refresh-key": "col-ingress" },
        node("span", { className: "a-dash-topo-flow-stage" }, "入站入口"),
        ingressCard,
      ),
      node("div", { className: "a-dash-topo-flow-col is-nodes", "data-refresh-key": "col-nodes" },
        node("span", { className: "a-dash-topo-flow-stage" }, nodesColTitle),
        node("div", { className: "a-dash-topo-flow-list", "data-refresh-key": "node-list" }, ...nodeCards),
      ),
      node("div", { className: "a-dash-topo-flow-col is-egress", "data-refresh-key": "col-egress" },
        node("span", { className: "a-dash-topo-flow-stage" }, model.externalTargets.length ? `出口 · 公网 (${model.externalTargets.length})` : "出站出口"),
        node("div", { className: "a-dash-topo-flow-list", "data-refresh-key": "egress-list" }, egressCard, ...extCards),
      ),
    );

    return node("div", {
      className: `a-dash-topo-flow-board ${isMobile ? "is-mobile" : ""}`,
      "data-topo-mode": "flow",
      "data-refresh-key": "topo-flow-board",
    }, svgEl, gridEl);
  };

  /** Measures real HTML card rects relative to `.a-dash-topo-flow-board` and draws crisp aligned Bezier edges & pills. */
  const syncFlowEdges = (forceRedraw = false) => {
    if (disposed || viewMode !== "flow") return;
    const model = lastModel;
    const board = canvasHost.querySelector(".a-dash-topo-flow-board");
    const svg = board?.querySelector("svg.a-dash-topo-flow-svg");
    if (!board || !svg || !model || !model.nodes.length) return;

    const bRect = board.getBoundingClientRect();
    const w = Math.round(bRect.width || board.clientWidth || 0);
    const h = Math.round(bRect.height || board.clientHeight || 0);
    if (w < 120 || h < 60) return;

    const isMobile = w < 760;
    const ports = new Map();
    board.querySelectorAll("[data-flow-port]").forEach((el) => {
      const r = el.getBoundingClientRect();
      if (r.width <= 0 || r.height <= 0) return;
      ports.set(el.getAttribute("data-flow-port"), {
        left: r.left - bRect.left,
        right: r.right - bRect.left,
        top: r.top - bRect.top,
        bottom: r.bottom - bRect.top,
        cx: (r.left + r.right) / 2 - bRect.left,
        cy: (r.top + r.bottom) / 2 - bRect.top,
      });
    });

    const portSig = [...ports.entries()].map(([k, v]) => `${k}:${Math.round(v.left)},${Math.round(v.top)},${Math.round(v.right)},${Math.round(v.bottom)}`).join("|");
    const edgeSig = JSON.stringify([w, h, isMobile, portSig, model.ingressLinks, model.egressLinks, model.probeLinks, lastTrafficMeta.readable, lastTrafficMeta.hasData]);
    if (!forceRedraw && edgeSig === lastFlowGeomSignature) return;
    lastFlowGeomSignature = edgeSig;

    svg.setAttribute("viewBox", `0 0 ${w} ${h}`);
    svg.setAttribute("width", String(w));
    svg.setAttribute("height", String(h));

    const defs = createTopologyDefs("a-topo");
    const flowLayer = createSVGEl("g", { class: "a-dash-topo-flow-conduits" });
    const probeLayer = createSVGEl("g", { class: "a-dash-topo-flow-probes" });
    const labelLayer = createSVGEl("g", { class: "a-dash-topo-flow-labels" });
    const pillLayer = createSVGEl("g", { class: "a-dash-topo-flow-pills" });

    const selIndex = buildTopologySelectionIndex(model);
    const inHub = ports.get("hub:ingress");
    const outHub = ports.get("hub:egress");
    const occupiedFlowLabels = [];
    const reserveFlowLabel = (pos, label, anchor = "middle", bounds = null) => {
      if (!pos || !label) return false;
      const lw = Math.max(40, visualUnits(label) * 6.0 + 10);
      const lx = anchor === "end" ? pos.x - lw : anchor === "start" ? pos.x : pos.x - lw / 2;
      const box = { x: lx, y: pos.y - 13, w: lw, h: 16 };
      if (bounds && (box.x < bounds.minX || box.x + box.w > bounds.maxX)) return false;
      if (occupiedFlowLabels.some((o) => rectsOverlap(box, o, 4, 4))) return false;
      occupiedFlowLabels.push(box);
      return true;
    };
    const slotY = (port, idx, total) => {
      if (!port) return 0;
      const span = Math.max(0, (port.bottom - port.top) - 20);
      if (total <= 1 || span <= 4) return port.cy;
      return port.top + 10 + (span * idx) / (total - 1);
    };

    // 1. Ingress & Egress traffic conduits
    if (!isMobile) {
      model.ingressLinks.forEach((edge, idx) => {
        const nPort = ports.get(entityKey.node(edge.to));
        if (!inHub || !nPort) return;
        const peers = selIndex.ingressEdgePeers(edge.to);
        const startY = slotY(inHub, idx, model.ingressLinks.length);
        const d = cubicPath(inHub.right + 2, startY, nPort.left - 2, nPort.cy);
        const rawRateLabel = (lastTrafficMeta.readable && lastTrafficMeta.hasData && edge.bytesPerSec > 0)
          ? `↓ ${bytes(edge.bytesPerSec)}/s`
          : "";
        const labelCandidate = rawRateLabel ? {
          x: nPort.left - 8,
          y: startY + (nPort.cy - startY) * 0.85,
        } : null;
        const inBounds = { minX: inHub.right + 6, maxX: nPort.left - 6 };
        const rateLabel = reserveFlowLabel(labelCandidate, rawRateLabel, "end", inBounds) ? rawRateLabel : "";
        flowLayer.append(renderFlowCurve({
          d,
          peers,
          baseOpacity: edge.active ? 0.78 : 0.34,
          glowColor: edge.active ? INGRESS_COLOR : null,
          glowWidth: 5.5,
          stroke: edge.active ? "url(#a-topo-grad-rx)" : IDLE_FLOW_COLOR,
          strokeWidth: edge.active ? 1.5 + edge.weight * 1.4 : 1.1,
          dash: edge.active ? "" : "3 3",
          markerEnd: edge.active ? "url(#a-topo-arrow-rx)" : "url(#a-topo-arrow-idle)",
          pulseColor: edge.active ? INGRESS_COLOR : null,
          pulseOpacity: edge.active ? 0.42 : 0,
          pulseWeight: edge.weight,
          photonColor: edge.active && edge.bytesPerSec > 0 ? INGRESS_COLOR : null,
          rateLabel,
          labelAnchor: "end",
          rateColor: INGRESS_COLOR,
          labelPos: rateLabel ? labelCandidate : null,
          labelOffsetY: -4,
          labelLayer,
          refreshKey: `flow-in-${edge.to}`,
        }));
      });

      model.egressLinks.forEach((edge, idx) => {
        const nPort = ports.get(entityKey.node(edge.from));
        if (!outHub || !nPort) return;
        const peers = selIndex.egressEdgePeers(edge.from);
        const railOffset = edge.blocked ? 0 : 12;
        const endY = slotY(outHub, idx, model.egressLinks.length);
        const pathObj = extendedCubicPath(nPort.right + 2, nPort.right + railOffset, nPort.cy, outHub.left - 2, endY);
        const rawRateLabel = (lastTrafficMeta.readable && lastTrafficMeta.hasData && edge.bytesPerSec > 0)
          ? `↑ ${bytes(edge.bytesPerSec)}/s`
          : "";
        const labelCandidate = rawRateLabel ? {
          x: nPort.right + 14,
          y: nPort.cy - 4,
        } : null;
        const outBounds = { minX: nPort.right + 12, maxX: outHub.left - 12 };
        const canDrawAmbientRate = model.nodes.length <= 8 || idx < 3;
        const rateLabel = (canDrawAmbientRate && reserveFlowLabel(labelCandidate, rawRateLabel, "start", outBounds)) ? rawRateLabel : "";
        flowLayer.append(renderFlowCurve({
          d: pathObj.d,
          peers,
          baseOpacity: edge.blocked ? 0.85 : edge.active ? 0.72 : 0.32,
          glowColor: edge.blocked ? BLOCKED_COLOR : null,
          glowWidth: 5,
          stroke: edge.blocked ? BLOCKED_COLOR : edge.active ? "url(#a-topo-grad-tx)" : IDLE_FLOW_COLOR,
          strokeWidth: edge.blocked ? 1.8 : edge.active ? 1.3 + edge.weight * 1.2 : 1.05,
          dash: edge.blocked || !edge.active ? "4 3" : "",
          markerEnd: edge.blocked ? "url(#a-topo-arrow-bad)" : edge.active ? "url(#a-topo-arrow-tx)" : "url(#a-topo-arrow-idle)",
          pulseColor: !edge.blocked && edge.active ? EGRESS_COLOR : null,
          pulseOpacity: !edge.blocked && edge.active ? 0.36 : 0,
          pulseWeight: edge.weight,
          photonColor: !edge.blocked && edge.active && edge.bytesPerSec > 0 ? EGRESS_COLOR : null,
          rateLabel,
          labelAnchor: "start",
          rateColor: edge.blocked ? BLOCKED_COLOR : EGRESS_COLOR,
          labelPos: rateLabel ? labelCandidate : null,
          labelOffsetY: 0,
          labelLayer,
          refreshKey: `flow-out-${edge.from}`,
        }));
      });
    } else {
      // Mobile stacked spine lines down the right gutter connecting Ingress -> Nodes -> Egress
      const gutterRight = w - 12;
      model.ingressLinks.forEach((edge, idx) => {
        const nPort = ports.get(entityKey.node(edge.to));
        if (!inHub || !nPort) return;
        const peers = selIndex.ingressEdgePeers(edge.to);
        const railX = Math.max(nPort.right + 12, gutterRight - (idx % 3) * 6);
        const d = `M ${inHub.right} ${inHub.cy.toFixed(1)} L ${railX.toFixed(1)} ${inHub.cy.toFixed(1)} L ${railX.toFixed(1)} ${nPort.cy.toFixed(1)} L ${nPort.right + 2} ${nPort.cy.toFixed(1)}`;
        flowLayer.append(renderFlowCurve({
          d,
          peers,
          baseOpacity: edge.active ? 0.55 : 0.25,
          stroke: edge.active ? INGRESS_COLOR : IDLE_FLOW_COLOR,
          strokeWidth: edge.active ? 1.4 : 1.0,
          dash: edge.active ? "" : "3 3",
          markerEnd: edge.active ? "url(#a-topo-arrow-rx)" : "url(#a-topo-arrow-idle)",
          refreshKey: `m-flow-in-${edge.to}`,
        }));
      });
    }

    // 2. Internal Mesh & External Probe Curves + Collision-Free Pills
    const rowByID = new Map(model.nodes.map((n, i) => [n.id, i]));
    const laneInfo = assignProbeLanes(model.probeLinks, rowByID);
    const ambientBad = selectTopAmbientBadProbeIDs(model.probeLinks, { includeExternal: true, maxCount: isMobile ? 3 : 5 });
    const occupiedPills = [...occupiedFlowLabels];
    ports.forEach((r) => {
      occupiedPills.push({ x: r.left, y: r.top, w: r.right - r.left, h: r.bottom - r.top });
    });

    const nodeRightX = Math.max(0, ...model.nodes.map((n) => ports.get(entityKey.node(n.id))?.right || 0));
    const maxPillBounds = isMobile
      ? { minX: Math.max(8, nodeRightX + 4), maxX: w - 6, minY: 6, maxY: h - 6 }
      : { minX: Math.max(8, nodeRightX + 6), maxX: outHub ? Math.max(nodeRightX + 90, outHub.left - 6) : w - 12, minY: 6, maxY: h - 6 };

    const leaderSegments = [];
    model.probeLinks.forEach((probe) => {
      const fromPort = ports.get(entityKey.node(probe.from));
      if (!fromPort) return;
      const eKey = entityKey.probe(probe.id);
      const peers = selIndex.probePeers(probe);
      const pal = tonePalette(probe.tone);
      const hl = isHighlightedEdge(peers);
      const isBad = probe.tone === "bad";
      const isWarn = probe.tone === "warn";

      let d = "";
      let mid = null;

      if (probe.isExternal) {
        const extPort = ports.get(entityKey.external(probe.to));
        if (!extPort) return;
        if (!isMobile) {
          d = cubicPath(fromPort.right + 2, fromPort.cy, extPort.left - 2, extPort.cy);
          mid = { x: (fromPort.right + extPort.left) / 2, y: (fromPort.cy + extPort.cy) / 2 };
        } else {
          const railW = Math.max(18, (w - Math.max(fromPort.right, extPort.right)) * 0.55);
          const arch = bridgeArchPath(Math.max(fromPort.right, extPort.right) + 2, fromPort.cy, extPort.cy, railW, 0);
          d = arch.d;
          mid = bridgePointAt(Math.max(fromPort.right, extPort.right) + 2, fromPort.cy, extPort.cy, railW, 0.5, 0);
        }
      } else {
        const toPort = ports.get(entityKey.node(probe.to));
        if (!toPort) return;
        const info = laneInfo.get(probe.id) || { lane: 0, count: 1 };
        const laneCount = Math.max(1, info.count);
        const availRight = (isMobile ? w - 10 : (outHub ? outHub.left - 12 : w - 16)) - Math.max(fromPort.right, toPort.right);
        const maxBow = Math.max(22, Math.min(isMobile ? 54 : 132, availRight * 0.62));
        const bowStep = (maxBow - 16) / laneCount;
        const bow = 16 + (info.lane + 0.5) * bowStep;
        const baseX = Math.max(fromPort.right, toPort.right) + 2;
        const arch = bridgeArchPath(baseX, fromPort.cy, toPort.cy, bow, 0);
        d = arch.d;
        mid = bridgePointAt(baseX, fromPort.cy, toPort.cy, bow, 0.5, 0);
      }

      const baseOpacity = isBad ? 0.92 : isWarn ? 0.8 : probe.isExternal ? 0.6 : 0.68;
      const g = createSVGEl("g", {
        "data-refresh-key": `flow-probe-${probe.id}`,
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": String(baseOpacity),
        opacity: (hl === true ? 1 : hl === false ? 0.14 : baseOpacity).toFixed(2),
      });
      g.append(createSVGEl("path", {
        d,
        fill: "none",
        stroke: pal.stroke,
        "stroke-width": hl === true ? "2.4" : isBad ? "2.0" : "1.5",
        "stroke-dasharray": probe.running ? "6 4" : isBad ? "5 3" : probe.isExternal ? "2.5 3" : null,
        "stroke-linecap": "round",
        "marker-end": isBad ? "url(#a-topo-arrow-bad)" : isWarn ? "url(#a-topo-arrow-warn)" : probe.tone === "ok" ? "url(#a-topo-arrow-ok)" : "url(#a-topo-arrow-idle)",
        class: probe.running ? "a-dash-topo-probe-running" : null,
      }));

      const hitPath = createSVGEl("path", {
        d,
        fill: "none",
        stroke: "transparent",
        "stroke-width": "12",
        class: "a-dash-topo-probe-item",
      });
      bindInteractive(hitPath, eKey, { openProbeID: probe.id });
      g.append(hitPath);
      probeLayer.append(g);

      // Pill labeling: show ambient alerts + allow hover/pin on all pills cleanly.
      const isAmbientBad = ambientBad.has(probe.id);
      const compactLabel = buildProbePillLabel(probe, isMobile, isMobile);
      const labelText = isMobile && isAmbientBad ? buildProbeShortAlertLabel(probe) : compactLabel;
      const pillH = isMobile ? 16 : 19;
      const pillW = Math.max(isMobile ? 42 : 56, Math.min(isMobile ? 96 : 148, visualUnits(labelText) * (isMobile ? 5.6 : 6.2) + (isMobile ? 16 : 22)));
      const focusOnly = model.probeLinks.length > (isMobile ? 3 : 6) && !isAmbientBad;
      const placed = placeCollisionFreePill(mid.x, mid.y, pillW, pillH, occupiedPills, maxPillBounds, [mid], { leaderSegments });

      if (placed.shifted) {
        appendProbeLeader(pillLayer, placed, pal, peers, hl, isMobile, focusOnly);
      }
      pillLayer.append(createProbePillGroup({
        probe,
        cx: placed.cx,
        cy: placed.cy,
        pillW,
        fullW: pillW,
        pillH,
        label: labelText,
        shortLabel: labelText,
        pal,
        compact: isMobile,
        peers,
        focusOnly,
      }));
    });

    svg.replaceChildren(defs, flowLayer, probeLayer, labelLayer, pillLayer);
    syncHighlight();
  };

  const scheduleFlowEdgeSync = (force = false) => {
    cancelAnimationFrame(flowEdgeFrame);
    flowEdgeFrame = requestAnimationFrame(() => {
      flowEdgeFrame = 0;
      syncFlowEdges(force);
    });
  };

  /** Builds optional zoom/pan control overlay for the Pacific map view. */
  const attachMapViewportControls = (stageWrapper) => {
    const svg = stageWrapper.querySelector("svg.a-dash-topo-map-svg");
    if (!svg || svg.classList.contains("is-mobile")) return;

    const zoomBy = (factor, anchorX = PACIFIC_MAP_BOUNDS.width / 2, anchorY = PACIFIC_MAP_BOUNDS.height / 2) => {
      const nextScale = Math.max(1, Math.min(3.2, mapZoom.scale * factor));
      if (Math.abs(nextScale - mapZoom.scale) < 0.01) return;
      const ratio = nextScale / mapZoom.scale;
      mapZoom = clampZoomTransform({
        scale: nextScale,
        tx: anchorX - (anchorX - mapZoom.tx) * ratio,
        ty: anchorY - (anchorY - mapZoom.ty) * ratio,
      });
      paint(true);
    };

    const pctLabel = `${Math.round(mapZoom.scale * 100)}%`;
    const toolsBar = node("div", { className: "a-dash-topo-map-tools", role: "group", "aria-label": "地图缩放控制" },
      button("−", () => zoomBy(1 / 1.35), { className: "a-button", title: "缩小地图", disabled: mapZoom.scale <= 1.01, "data-refresh-key": "map-zoom-out" }),
      button(mapZoom.scale > 1.01 ? `重置 (${pctLabel})` : "100%", () => {
        clearSelection();
        resetMapZoom(true);
      }, { className: "a-button", title: "重置地图视角并清除聚焦", disabled: mapZoom.scale <= 1.01 && !pinnedKey, "data-refresh-key": "map-zoom-reset" }),
      button("+", () => zoomBy(1.35), { className: "a-button", title: "放大地图", disabled: mapZoom.scale >= 3.18, "data-refresh-key": "map-zoom-in" }),
    );
    stageWrapper.append(toolsBar);

    const clientToSvgPoint = (clientX, clientY) => {
      const rect = svg.getBoundingClientRect();
      if (!rect.width || !rect.height) return { x: PACIFIC_MAP_BOUNDS.width / 2, y: PACIFIC_MAP_BOUNDS.height / 2 };
      return {
        x: ((clientX - rect.left) / rect.width) * PACIFIC_MAP_BOUNDS.width,
        y: ((clientY - rect.top) / rect.height) * PACIFIC_MAP_BOUNDS.height,
      };
    };

    let dragging = false;
    let movedDist = 0;
    let startClient = { x: 0, y: 0 };
    let startTrans = { tx: 0, ty: 0 };

    listen(svg, "pointerdown", (e) => {
      if (e.button !== 0 || mapZoom.scale <= 1.01) return;
      if (e.target.closest?.("[data-topo-entity]")) return;
      dragging = true;
      movedDist = 0;
      startClient = { x: e.clientX, y: e.clientY };
      startTrans = { tx: mapZoom.tx, ty: mapZoom.ty };
      svg.setPointerCapture?.(e.pointerId);
    });
    listen(svg, "pointermove", (e) => {
      if (!dragging) return;
      const dxPx = e.clientX - startClient.x;
      const dyPx = e.clientY - startClient.y;
      movedDist = Math.max(movedDist, Math.hypot(dxPx, dyPx));
      const rect = svg.getBoundingClientRect();
      if (!rect.width || !rect.height) return;
      const dxSvg = (dxPx / rect.width) * PACIFIC_MAP_BOUNDS.width;
      const dySvg = (dyPx / rect.height) * PACIFIC_MAP_BOUNDS.height;
      mapZoom = clampZoomTransform({
        scale: mapZoom.scale,
        tx: startTrans.tx + dxSvg,
        ty: startTrans.ty + dySvg,
      });
      const vp = svg.querySelector(".a-dash-topo-map-viewport");
      if (vp) {
        vp.setAttribute("transform", `translate(${mapZoom.tx.toFixed(1)} ${mapZoom.ty.toFixed(1)}) scale(${mapZoom.scale.toFixed(3)})`);
      }
    });
    listen(svg, "pointerup", (e) => {
      if (!dragging) return;
      dragging = false;
      svg.releasePointerCapture?.(e.pointerId);
      if (movedDist > 5) {
        suppressNextCanvasClearClick = true;
        paint(true);
      }
    });
    listen(svg, "dblclick", (e) => {
      if (e.target.closest?.("[data-topo-entity]")) return;
      const pt = clientToSvgPoint(e.clientX, e.clientY);
      if (mapZoom.scale >= 2.5) {
        resetMapZoom(true);
      } else {
        zoomBy(1.6, pt.x, pt.y);
      }
    });
    listen(svg, "wheel", (e) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      const pt = clientToSvgPoint(e.clientX, e.clientY);
      zoomBy(e.deltaY < 0 ? 1.18 : 1 / 1.18, pt.x, pt.y);
    }, { passive: false });
  };

  const paint = (force = false) => {
    paintScheduled = false;
    paintForced = false;
    if (!canView() || disposed) return;

    const states = getStates();
    const usage = getUsage();
    const policies = getPolicies();
    const agents = getAgents();
    const probesData = getProbes();

    const canAgents = ctx.can("agents.read");
    const canTraffic = ctx.can("traffic.read");
    const canTasks = ctx.can("tasks.read");
    const agentsState = states.get("agents") || "";
    const probesState = states.get("probes") || "";
    const trafficState = states.get("traffic") || "";
    const policyState = states.get("trafficPolicies") || "";

    const trafficReadable = canTraffic && trafficState !== "denied" && policyState !== "denied";
    const hasTrafficData = Boolean(usage || policies);
    const trafficEffectiveState = !trafficReadable
      ? "denied"
      : (!hasTrafficData && (trafficState === "loading" || policyState === "loading" || (!trafficState && !policyState)))
        ? "loading"
        : (!hasTrafficData && (trafficState === "failed" || policyState === "failed"))
          ? "failed"
          : (hasTrafficData && (trafficState === "failed" || policyState === "failed"))
            ? "partial-failed"
            : "ready";

    const trafficLabel = trafficEffectiveState === "denied"
      ? "无流量权限"
      : trafficEffectiveState === "loading"
        ? "流量读取中"
        : trafficEffectiveState === "failed"
          ? "流量读取失败"
          : trafficEffectiveState === "partial-failed"
            ? "流量刷新失败 · 保留已有记录"
            : "";

    lastTrafficMeta = {
      readable: trafficReadable,
      state: trafficEffectiveState,
      hasData: hasTrafficData,
      label: trafficLabel,
    };

    const hasProbePairs = Array.isArray(probesData?.pairs) && probesData.pairs.length > 0;
    // Only narrow #dashboard to probed nodes once probes actually settled non-empty; never apply on failed/loading!
    const filterUnprobed = !isStandalonePage && canAgents && canTasks && probesState === "ready" && hasProbePairs;

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
      maxNodes: isStandalonePage ? null : 12,
      onlyProbedNodes: filterUnprobed,
    });
    lastModel = model;
    lastClusters = viewMode === "map" ? clusterTopologyNodes(model.nodes) : [];

    // Reconcile pinned/hovered keys cleanly against updated model & clusters so stale keys never dim canvas.
    if (pinnedKey) {
      pinnedKey = reconcileSelectionKey(pinnedKey, model, { clusters: lastClusters, allowCluster: viewMode === "map" });
    }
    if (hoveredKey) {
      hoveredKey = reconcileSelectionKey(hoveredKey, model, { clusters: lastClusters, allowCluster: viewMode === "map" });
    }

    const waitingKeys = [
      ...(canAgents ? ["agents"] : []),
      ...(canTraffic ? ["traffic", "trafficPolicies"] : []),
      ...(!isStandalonePage && canAgents && canTasks ? ["probes"] : []),
    ];
    const waiting = waitingKeys.some((key) => !states.has(key) || states.get(key) === "loading");
    const probesFailed = canAgents && canTasks && probesState === "failed";
    const agentsFailed = canAgents && agentsState === "failed";

    let nextStatus = "";
    if (!model.nodes.length) {
      if (waiting) nextStatus = "读取中…";
      else if (agentsFailed || (probesFailed && !hasProbePairs)) nextStatus = probesFailed && !agentsFailed ? "探测链路读取失败" : "拓扑读取失败";
      else if (canAgents && canTasks && probesState === "ready" && !hasProbePairs && !isStandalonePage) nextStatus = "未设置检测目标";
      else if (!canAgents && trafficEffectiveState === "denied") nextStatus = "无权查看拓扑数据";
      else nextStatus = isStandalonePage ? "暂无可用拓扑数据" : "暂无节点数据";
    } else if (probesFailed) {
      nextStatus = hasProbePairs ? "探测链路刷新失败 · 保留已有记录" : "探测链路读取失败";
    } else if (trafficEffectiveState === "failed" || trafficEffectiveState === "partial-failed") {
      nextStatus = trafficLabel;
    } else if (canAgents && canTasks && probesState === "ready" && !hasProbePairs && !isStandalonePage) {
      nextStatus = "未设置检测目标";
    }

    if (statusNode.textContent !== nextStatus) statusNode.textContent = nextStatus;

    const measuredWidth = observedWidth >= 200 ? observedWidth : (canvasHost.clientWidth >= 200 ? canvasHost.clientWidth : 0);
    const fallbackWidth = Math.max(300, Math.min(1120, Math.round((window.innerWidth || 1080) - (window.innerWidth > 896 ? 300 : 48))));
    const renderWidth = Math.max(280, Math.round(measuredWidth > 0 ? measuredWidth : fallbackWidth));
    const isMobile = renderWidth < 580;

    const sigModel = {
      ...model,
      egress: { ...model.egress, panelRxBPS: null, panelTxBPS: null },
    };
    const sig = JSON.stringify([renderWidth, isMobile, viewMode, mapZoom, sigModel, nextStatus, trafficEffectiveState]);
    if (!force && sig === lastPaintSignature) return;
    lastPaintSignature = sig;
    lastFlowGeomSignature = "";

    const metaBadges = [
      keyedBadge(`${model.onlineNodes}/${model.totalNodes} 在线`, model.onlineNodes ? "ok" : "bad", "topo-nodes"),
      model.probeLinks.length
        ? keyedBadge(
            model.abnormalLinks > 0 ? `链路告警 ${model.abnormalLinks}` : `探测链路 ${model.probeLinks.length}`,
            model.abnormalLinks > 0 ? "warn" : "ok",
            "topo-links",
          )
        : null,
      !isStandalonePage && (model.hiddenNodeCount > 0 || model.omittedProbeCount > 0)
        ? keyedBadge(
            `已省略 ${model.hiddenNodeCount} 节点${model.omittedProbeCount ? ` · ${model.omittedProbeCount} 链路` : ""}`,
            "",
            "topo-omitted",
          )
        : null,
    ].filter(Boolean);
    patchChildren(headerMeta, metaBadges);
    openProbesBtn?.classList?.toggle("is-alert", model.abnormalLinks > 0);

    if (model.nodes.length) {
      if (viewMode === "map") {
        const freshSvg = renderPacificMapSVG({
          width: renderWidth,
          isMobile,
          model,
          bytes,
          trafficMeta: lastTrafficMeta,
          zoomTransform: isMobile ? null : mapZoom,
          helpers: {
            allowMotion,
            activeKey,
            isHighlightedEdge,
            bindInteractive,
            createProbePillGroup,
            appendProbeLeader,
          },
        });
        const stageWrap = node("div", { className: "a-dash-topo-map-stage", "data-topo-mode": "map" }, freshSvg);
        attachMapViewportControls(stageWrap);
        if (canvasHost.firstChild && canvasHost.firstChild.getAttribute("data-topo-mode") === "map") {
          const existingTools = canvasHost.firstChild.querySelector(".a-dash-topo-map-tools");
          const existingSvg = canvasHost.firstChild.querySelector("svg.a-dash-topo-map-svg");
          const newTools = stageWrap.querySelector(".a-dash-topo-map-tools");
          if (existingSvg) patch(existingSvg, freshSvg);
          else canvasHost.replaceChildren(stageWrap);
          if (existingTools && newTools) patch(existingTools, newTools);
          else if (!existingTools && newTools) canvasHost.firstChild.append(newTools);
          else if (existingTools && !newTools) existingTools.remove();
        } else {
          canvasHost.replaceChildren(stageWrap);
        }
      } else {
        const isStackedFlow = renderWidth < 760;
        const freshBoard = buildFlowBoardElement(model, isStackedFlow, lastTrafficMeta);
        if (canvasHost.firstChild && canvasHost.firstChild.getAttribute("data-topo-mode") === "flow") {
          patch(canvasHost.firstChild, freshBoard);
        } else {
          canvasHost.replaceChildren(freshBoard);
        }
        syncFlowEdges(true);
        scheduleFlowEdgeSync(false);
      }
    } else {
      canvasHost.replaceChildren();
    }

    if (showLedger) syncLedger();
    syncHighlight();
    syncInspect();
  };

  /** Coalesces multiple poll resource arrivals in the same event-loop turn into one paint pass. */
  const schedulePaint = (force = false) => {
    if (force) paintForced = true;
    if (paintScheduled || disposed || !canView()) return;
    paintScheduled = true;
    queueMicrotask(() => {
      if (!paintScheduled || disposed) return;
      const useForce = paintForced;
      paintScheduled = false;
      paintForced = false;
      if (ctx.current()) paint(useForce);
    });
  };

  if (typeof ResizeObserver === "function") {
    let resizeFrame = 0;
    const observer = new ResizeObserver((entries) => {
      if (!canView() || !ctx.current()) return;
      const next = Math.round(entries[0]?.contentRect?.width || 0);
      if (next >= 200 && Math.abs(next - observedWidth) >= 2) {
        observedWidth = next;
        cancelAnimationFrame(resizeFrame);
        resizeFrame = requestAnimationFrame(() => {
          resizeFrame = 0;
          if (canView() && ctx.current()) paint();
        });
      } else if (viewMode === "flow" && lastModel?.nodes?.length) {
        scheduleFlowEdgeSync(false);
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
    node("div", { className: "a-card-body a-dash-topo-body" }, canvasHost, inspectBar, probeMatrixHost),
    node("div", { className: "a-actions a-dash-topo-tools" }, headerMeta, modeSwitch, detailPageLink, openProbesBtn, statusNode),
  );
  card?.classList?.add("a-dash-topo-card");

  return {
    visible: Boolean(card),
    element: card,
    paint,
    schedulePaint,
  };
}
