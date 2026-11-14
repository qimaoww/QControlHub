import { createSVGEl } from "./dashboard-panel.js";
import { renderPacificMapSVG } from "./dashboard-map.js";
import { PACIFIC_MAP_BOUNDS } from "./pacific-map-data.js";
import { listen, patch } from "./live-dom.js";
import { badge, button, muted, node, section } from "./ui.js";
import { buildDashboardTopology, utcMonth } from "../modules/dashboard-model.js";
import { geoRegionDetails } from "../modules/region-model.js";
import {
  BLOCKED_COLOR,
  IDLE_FLOW_COLOR,
  OK_COLOR,
  assignProbeLanes,
  bridgeArchPath,
  bridgePointAt,
  buildBidirectionalPillLines,
  buildProbePillLabel,
  buildProbeShortAlertLabel,
  buildTopologySelectionIndex,
  clusterTopologyNodes,
  createTopologyDefs,
  entityKey,
  extractCityClusterLabel,
  filterTopologyModelByGroup,
  findSameCityMembers,
  formatMs,
  isProbeUnreachable,
  matchesActiveSelection,
  placeCollisionFreePill,
  planGroupTopologyGraph,
  reconcileSelectionKey,
  tonePalette,
  visualUnits,
} from "./dashboard-topo-shared.js";

const fmtLossShort = (loss) => {
  if (typeof loss !== "number" || !Number.isFinite(loss)) return "—";
  const rounded = loss.toFixed(loss % 1 ? 1 : 0);
  return `${rounded}%`;
};

const formatDirRttShort = (dir, prefix = "") => {
  if (!dir) return `${prefix}待采样`;
  if (dir.status?.tone === "bad" && (!dir.reached || Number(dir.lossPercent) >= 100)) {
    return `${prefix}不可达`;
  }
  const ms = typeof dir.latencyMS === "number" && Number.isFinite(dir.latencyMS) ? formatMs(dir.latencyMS) : "—";
  const loss = typeof dir.lossPercent === "number" && dir.lossPercent > 0 ? ` · ${fmtLossShort(dir.lossPercent)}` : "";
  return `${prefix}${ms}${loss}`;
};

const formatChainRttText = (item) => {
  if (!item) return "—";
  if (item.running) return "探测中…";
  if (!item.availabilityReady && item.latencyMS == null && item.revLatencyMS == null) {
    return item.reason || "暂不可用";
  }
  if (item.bidirectional) {
    return `${formatDirRttShort(item.forward, "去 ")} · ${formatDirRttShort(item.reverse, "回 ")}`;
  }
  if (isProbeUnreachable(item)) {
    return item.lossPercent != null && item.lossPercent >= 100 ? "不可达 · 100% 丢包" : (item.metricLabel || "不可达");
  }
  return `${item.isExternal ? "延迟" : "单向"} ${formatMs(item.latencyMS)}`;
};

const formatLossBadgeText = (item) => {
  if (!item) return "待采样";
  if (item.running) return "探测中";
  if (isProbeUnreachable(item) && item.lossPercent != null && item.lossPercent >= 100) return "不可达";
  if (item.lossPercent == null) return item.statusLabel || "待采样";
  return item.lossPercent > 0 ? `丢包 ${fmtLossShort(item.lossPercent)}` : "0% 丢包";
};

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
  const canExecuteProbes = () => Boolean(canOpenProbes && onOpenProbes && ctx.can("tasks.execute"));
  const allowMotion = () => !(typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)")?.matches);

  const VALID_VIEW_MODES = new Set(["flow", "map"]);
  const VALID_PROBE_FILTERS = new Set(["all", "bad", "chain", "mesh", "ext"]);
  let pinnedKey = "";
  let hoveredKey = "";
  let viewMode = "flow";
  let probeFilter = "all";
  let selectedGroupId = "";
  let viewModeTouched = false;
  let probeFilterTouched = false;
  let groupTouched = false;
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

  const setSelectedGroup = (nextGroupId, { persist = true, repaint = true } = {}) => {
    const normalized = String(nextGroupId || "").slice(0, 64);
    const changed = selectedGroupId !== normalized;
    groupTouched = true;
    if (persist && changed) {
      ctx.preferences?.patch?.("dashboard", { topo_group: normalized });
    }
    if (!changed) return;
    selectedGroupId = normalized;
    pinnedKey = "";
    hoveredKey = "";
    lastFlowGeomSignature = "";
    if (repaint && canView() && ctx.current()) paint(true);
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
    if (!groupTouched && typeof saved?.topo_group === "string" && saved.topo_group.length <= 64 && saved.topo_group !== selectedGroupId) {
      selectedGroupId = saved.topo_group;
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
    ? node("a", {
        className: "a-button a-dash-topo-page-btn",
        href: "#topology",
        "data-refresh-key": "open-topology-page",
      }, "拓扑详情")
    : null;
  const addChainBtn = canExecuteProbes()
    ? button("新建全程监测", () => onOpenProbes("", { create: true }), {
        className: "a-button a-dash-topo-add-btn",
        tone: "primary",
        "data-refresh-key": "add-probe-chain",
      })
    : null;
  const openProbesBtn = canOpenProbes && onOpenProbes
    ? button("链路监控", () => onOpenProbes(""), {
        className: "a-button a-dash-topo-probe-btn",
        "data-refresh-key": "open-probes",
      })
    : null;

  const groupBarHost = node("div", {
    className: "a-dash-topo-group-bar",
    role: "tablist",
    "aria-label": "监测分组筛选",
    "data-refresh-key": "topo-group-bar",
    hidden: true,
  });
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

  const nodeChainsFor = (nodeID, model) => (model?.probeChains || []).filter(
    (ch) => (ch.endpoints || []).some((ep) => ep.kind === "node" && ep.id === nodeID),
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

  const summarizeLatencyPair = (items = []) => {
    const fwdVals = items.map((p) => p.latencyMS).filter((v) => typeof v === "number" && Number.isFinite(v) && v >= 0);
    const revVals = items.filter((p) => p.bidirectional).map((p) => p.revLatencyMS).filter((v) => typeof v === "number" && Number.isFinite(v) && v >= 0);
    const chips = [];
    if (fwdVals.length) {
      const avgFwd = fwdVals.reduce((s, v) => s + v, 0) / fwdVals.length;
      chips.push(muted(`均值去程 ${formatMs(avgFwd)}`));
    }
    if (revVals.length) {
      const avgRev = revVals.reduce((s, v) => s + v, 0) / revVals.length;
      chips.push(muted(`均值回程 ${formatMs(avgRev)}`));
    }
    return chips;
  };

  const resolveKeyDescription = (key, model) => {
    if (!model || !model.nodes.length) {
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, "网络延迟监测拓扑"), muted(statusNode.textContent || "等待节点与监测链路…")],
        detail: [],
      };
    }

    if (key?.startsWith("probe:")) {
      const linkID = key.slice("probe:".length);
      const chain = (model.probeChains || []).find((c) => c.id === linkID);
      if (chain) {
        const arrow = chain.bidirectional ? " ⇄ " : " → ";
        const routeStr = (chain.endpoints || []).map((ep) => ep.name || ep.address || ep.id).join(arrow);
        const chips = [
          badge(chain.statusLabel, chain.tone),
          chain.groupName ? badge(chain.groupName, "") : null,
          chain.isMultiHop ? badge(`全程 ${chain.endpoints.length} 站`, "") : null,
          muted(routeStr),
          chain.latencyMS != null ? muted(`去程 ${chain.latencyMS.toFixed(1)} ms${chain.fwdLossPercent > 0 ? ` (${fmtLossShort(chain.fwdLossPercent)}丢包)` : ""}`) : null,
          chain.bidirectional && chain.revLatencyMS != null ? muted(`回程 ${chain.revLatencyMS.toFixed(1)} ms${chain.revLossPercent > 0 ? ` (${fmtLossShort(chain.revLossPercent)}丢包)` : ""}`) : null,
          chain.lossPercent != null ? muted(chain.lossPercent > 0 ? `最高丢包 ${fmtLossShort(chain.lossPercent)}` : "0% 丢包") : null,
        ].filter(Boolean);
        if (chain.isMultiHop && chain.hops?.length) {
          chain.hops.forEach((h, idx) => {
            chips.push(muted(`第${idx + 1}段 ${h.fromName}⇄${h.toName}: ${formatChainRttText(h)}`));
          });
        }
        if (canExecuteProbes()) {
          chips.push(button("编辑 / 增减节点", () => onOpenProbes(chain.id, { edit: true }), { className: "a-button" }));
        }
        if (canOpenProbes && onOpenProbes) {
          chips.push(button("链路监控", () => onOpenProbes(chain.id), { className: "a-button" }));
        }
        chips.push(button("取消聚焦", clearSelection, { className: "a-button", "data-refresh-key": "topo-unpin" }));
        return {
          primary: [node("span", { className: "a-dash-topo-inspect-title" }, chain.name)],
          detail: chips,
        };
      }

      const item = model.probeLinks.find((p) => p.id === linkID);
      if (item) {
        const targetProbeID = item.probeID || item.id;
        const chips = [
          badge(item.statusLabel, item.tone),
          item.groupName ? badge(item.groupName, "") : null,
          muted(`${item.sourceAddr || item.fromName} ${item.bidirectional ? "⇄" : "→"} ${item.targetAddr || item.toName}`),
          item.latencyMS != null ? muted(`去程 ${item.latencyMS.toFixed(1)} ms${item.fwdLossPercent > 0 ? ` (${fmtLossShort(item.fwdLossPercent)}丢包)` : ""}`) : null,
          item.bidirectional && item.revLatencyMS != null ? muted(`回程 ${item.revLatencyMS.toFixed(1)} ms${item.revLossPercent > 0 ? ` (${fmtLossShort(item.revLossPercent)}丢包)` : ""}`) : null,
          item.lossPercent != null ? muted(item.lossPercent > 0 ? `丢包 ${fmtLossShort(item.lossPercent)}` : "0% 丢包") : null,
        ].filter(Boolean);
        if (canExecuteProbes()) {
          chips.push(button("编辑链路", () => onOpenProbes(targetProbeID, { edit: true }), { className: "a-button" }));
        }
        if (canOpenProbes && onOpenProbes) {
          chips.push(button("链路监控", () => onOpenProbes(targetProbeID), { className: "a-button" }));
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
        muted("公网延迟目标"),
        ext.address && ext.address !== ext.name ? node("code", { className: "a-dash-topo-code" }, ext.address) : null,
        muted(`${related.length} 条监测链路`),
        ...summarizeLatencyPair(related),
      ].filter(Boolean);
      const priority = pickPriorityProbe(related);
      if (canOpenProbes && onOpenProbes && priority) {
        chips.push(button("链路监控", () => onOpenProbes(priority.probeID || priority.id), { className: "a-button" }));
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
        const badRelated = relatedProbes.filter((p) => p.tone === "bad" || p.tone === "warn").length;
        const priorityProbe = pickPriorityProbe(relatedProbes);

        const chips = [
          badge(`${onlineCount}/${members.length} 在线`, onlineCount === members.length ? "ok" : onlineCount > 0 ? "warn" : "bad"),
          muted(relatedProbes.length ? `监测段 ${relatedProbes.length}${badRelated ? ` · 异常 ${badRelated}` : ""}` : "暂无监测链路"),
          ...summarizeLatencyPair(relatedProbes),
        ];
        appendSameCitySwitchers(chips, members, key);
        if (canOpenProbes && onOpenProbes && relatedProbes.length) {
          chips.push(button(`关联链路 (${relatedProbes.length})`, () => onOpenProbes(priorityProbe?.probeID || priorityProbe?.id || "")));
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
        const relatedSegments = nodeProbesFor(item.id, model);
        const relatedChains = nodeChainsFor(item.id, model);
        const priorityProbe = pickPriorityProbe(relatedSegments);
        const chips = [
          badge(item.isOnline ? "在线" : "离线", item.isOnline ? "ok" : "bad"),
          muted(relatedChains.length ? `参与 ${relatedChains.length} 条链路 / ${relatedSegments.length} 段` : "暂未纳入监测链路"),
          ...summarizeLatencyPair(relatedSegments),
        ];
        if (item.activeEngines.length) chips.push(muted(`内核 ${item.activeEngines.join(" / ")}`));
        if (item.blocked) chips.push(badge("已超限阻断", "bad"));
        if (viewMode === "map") {
          appendSameCitySwitchers(chips, findSameCityMembers(model.nodes, item.id), key);
        }
        if (canOpenProbes && onOpenProbes && relatedSegments.length) {
          chips.push(button(`关联链路 (${relatedChains.length || relatedSegments.length})`, () => onOpenProbes(priorityProbe?.probeID || priorityProbe?.id || "")));
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

    const chainCount = (model.probeChains || []).length || model.probeLinks.length;
    const multiHopCount = (model.probeChains || []).filter((c) => c.isMultiHop).length;
    const summaryDetail = [
      muted(`${model.onlineNodes}/${model.totalNodes} 节点在线`),
      muted(`监测链路 ${chainCount} 条${multiHopCount ? ` (含 ${multiHopCount} 条多跳)` : ""}`),
      ...summarizeLatencyPair(model.probeChains?.length ? model.probeChains : model.probeLinks),
      model.abnormalLinks > 0 ? badge(`告警 ${model.abnormalLinks}`, "bad") : (chainCount > 0 ? badge("全链正常", "ok") : null),
    ].filter(Boolean);
    if (model.hiddenNodeCount > 0) summaryDetail.push(muted(`概览展示 ${model.nodes.length}/${model.totalNodes} 节点`));
    return {
      primary: [node("span", { className: "a-dash-topo-inspect-title" }, "全网延迟监测汇总")],
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
    if (document.querySelector("dialog[open], .a-tooltip")) return;
    const focusEl = event.target instanceof Element && event.target !== document.body && event.target !== document.documentElement
      ? event.target
      : document.activeElement;
    const ownerCard = focusEl?.closest?.("section.a-card, .a-dash-topo-card");
    if (ownerCard && ownerCard !== card) return;
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
    const chains = model?.probeChains?.length
      ? model.probeChains
      : (model?.probeLinks || []).map((p) => ({
          ...p,
          isMultiHop: false,
          endpoints: [
            { kind: "node", id: p.from, name: p.fromName || p.from },
            { kind: p.isExternal ? "ext" : "node", id: p.to, name: p.toName || p.to },
          ],
          hops: [p],
          availabilityReady: true,
        }));

    if (!chains.length) {
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
    const badCount = chains.filter((c) => c.tone === "bad" || c.tone === "warn").length;
    const chainCount = chains.filter((c) => c.isMultiHop).length;
    const meshCount = chains.filter((c) => !c.isExternal && !c.isMultiHop).length;
    const extCount = chains.filter((c) => c.isExternal).length;

    const effectiveFilter = (probeFilter === "bad" && !badCount) ? "all" : probeFilter;
    const filterDefs = [
      { id: "all", label: `全部 (${chains.length})` },
      { id: "bad", label: `异常 (${badCount})` },
      { id: "chain", label: `多跳全程 (${chainCount})` },
      { id: "mesh", label: `双节点 (${meshCount})` },
      { id: "ext", label: `公网 (${extCount})` },
    ];

    const summaryParts = [`共 ${chains.length} 条`];
    if (chainCount) summaryParts.push(`${chainCount} 全程多跳`);
    if (meshCount) summaryParts.push(`${meshCount} 节点间`);
    if (extCount) summaryParts.push(`${extCount} 公网`);
    if (badCount) summaryParts.push(`异常 ${badCount}`);

    const headEl = node(
      "div",
      { className: "a-dash-topo-ledger-head", "data-refresh-key": "ledger-head" },
      node(
        "div",
        { className: "a-dash-topo-ledger-title", "data-refresh-key": "ledger-title" },
        node("strong", { "data-refresh-key": "ledger-heading" }, "探测链路列表"),
        node("span", { "data-refresh-key": "ledger-sub" }, summaryParts.join(" · ")),
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

    const filtered = chains.filter((c) => {
      if (effectiveFilter === "bad") return c.tone === "bad" || c.tone === "warn";
      if (effectiveFilter === "chain") return Boolean(c.isMultiHop);
      if (effectiveFilter === "mesh") return !c.isExternal && !c.isMultiHop;
      if (effectiveFilter === "ext") return Boolean(c.isExternal);
      return true;
    });

    const toneRank = { bad: 0, warn: 1, idle: 2, ok: 3 };
    const sorted = [...filtered].sort((a, b) => {
      const ra = toneRank[a.tone] ?? 2;
      const rb = toneRank[b.tone] ?? 2;
      if (ra !== rb) return ra - rb;
      if (Boolean(a.isMultiHop) !== Boolean(b.isMultiHop)) return a.isMultiHop ? -1 : 1;
      if (Boolean(a.isExternal) !== Boolean(b.isExternal)) return a.isExternal ? 1 : -1;
      return (Number(b.lossPercent) || 0) - (Number(a.lossPercent) || 0) || (Number(b.latencyMS) || 0) - (Number(a.latencyMS) || 0);
    });

    const gridBody = !sorted.length
      ? node("div", { className: "a-dash-topo-ledger-empty", "data-refresh-key": "ledger-empty" }, "暂无匹配链路")
      : node(
          "div",
          { className: "a-dash-topo-ledger-grid", "data-refresh-key": "ledger-grid" },
          ...sorted.map((chain) => {
            const eKey = entityKey.probe(chain.id);
            const peers = selIndex.chainPeers ? selIndex.chainPeers(chain.id) : [eKey];
            const arrow = chain.bidirectional ? " ⇄ " : " → ";
            const epNames = (chain.endpoints || []).map((ep) => ep.name || ep.address || ep.id);
            const routeTitle = epNames.join(arrow);
            const rttText = formatChainRttText(chain);
            const lossText = formatLossBadgeText(chain);
            const routeParts = [];
            epNames.forEach((epName, idx) => {
              if (idx > 0) routeParts.push(arrow);
              routeParts.push(node("span", { className: "a-dash-topo-link-ep" }, epName));
            });

            const kindParts = [];
            if (chain.groupName) kindParts.push(chain.groupName);
            kindParts.push(chain.isMultiHop ? `全程 ${epNames.length} 站` : (chain.isExternal ? "公网目标" : (chain.bidirectional ? "去/回双程" : "单向探测")));

            const cardEl = node(
              "button",
              {
                type: "button",
                className: `a-dash-topo-link-card${chain.isMultiHop ? " is-multihop" : ""}`,
                "data-refresh-key": `ledger-card-${chain.id}`,
                "data-tone": chain.tone || "idle",
                "data-topo-peers": peers.join(" "),
                title: `${routeTitle} · ${rttText} · ${lossText}`,
              },
              node(
                "div",
                { className: "a-dash-topo-link-top", "data-refresh-key": "top" },
                node("span", { className: "a-dash-topo-link-route", "data-refresh-key": "route" }, ...routeParts),
                node("span", { className: "a-dash-topo-link-loss", "data-refresh-key": "loss" }, lossText),
              ),
              node(
                "div",
                { className: "a-dash-topo-link-metrics", "data-refresh-key": "metrics" },
                node("span", { className: "a-dash-topo-link-rtt", "data-refresh-key": "rtt" }, rttText),
                node("span", { className: "a-dash-topo-link-kind", "data-refresh-key": "kind" }, kindParts.join(" · ")),
              ),
              chain.isMultiHop && chain.hops?.length ? node(
                "div",
                { className: "a-dash-topo-link-hops", "data-refresh-key": "hops" },
                ...chain.hops.map((hop, hIdx) => node(
                  "span",
                  { className: "a-dash-topo-link-hop-chip", "data-tone": hop.tone || "idle" },
                  `${hIdx + 1}. ${hop.fromName}⇄${hop.toName} ${formatChainRttText(hop)}`,
                )),
              ) : null,
            );
            bindInteractive(cardEl, eKey, { openProbeID: chain.id });
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

  const createProbePillGroup = ({ probe, cx, cy, pillW, fullW = pillW, pillH, label, shortLabel = label, lines = null, pal, compact = false, peers, focusOnly = false }) => {
    const eKey = entityKey.probe(probe.id);
    const hl = isHighlightedEdge(peers);
    const selected = activeKey() === eKey;
    const baseStr = focusOnly ? "0" : "1";
    const dimStr = focusOnly ? "0" : "0.22";
    const op = focusOnly ? (hl === true ? "1" : "0") : (hl === false ? dimStr : "1");
    const isTwoLine = Boolean(lines && lines.line1);
    const rx = isTwoLine ? Math.min(9, pillH / 2) : (pillH / 2);
    const g = createSVGEl("g", {
      class: `a-dash-topo-probe-item ${isTwoLine ? "is-twoline" : ""} ${focusOnly ? "a-dash-topo-focus-only" : ""} ${selected ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `probe-pill-${probe.id}`,
      "data-probe-id": probe.id,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": baseStr,
      "data-dim-opacity": dimStr,
      opacity: op,
    });
    const tipTitle = isTwoLine
      ? `${probe.fromName} ${probe.bidirectional ? "⇄" : "→"} ${probe.toName}: ${lines.fullLabel} — 点击打开链路监控`
      : `${probe.name}: ${probe.metricLabel} (${probe.statusLabel}) — 点击打开链路监控`;
    g.append(createSVGEl("title", {}, tipTitle));
    g.append(createSVGEl("rect", {
      x: (cx - pillW / 2).toFixed(1),
      y: (cy - pillH / 2).toFixed(1),
      width: pillW.toFixed(1),
      height: pillH,
      rx: rx.toFixed(1),
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
      g.append(createSVGEl("circle", { class: "a-dash-topo-pill-dot", cx: dotX.toFixed(1), cy: cy.toFixed(1), r: compact ? 2.5 : 3, fill: pal.stroke }));
    }

    if (isTwoLine) {
      const tx = (dotX + (compact ? 6.5 : 7.5)).toFixed(1);
      g.append(
        createSVGEl("text", {
          x: tx,
          y: (cy - (compact ? 2.4 : 2.9)).toFixed(1),
          class: `a-dash-topo-pill-text is-primary ${compact ? "is-compact" : ""}`,
          fill: pal.ink,
        }, lines.line1),
        createSVGEl("text", {
          x: tx,
          y: (cy + (compact ? 8.1 : 9.1)).toFixed(1),
          class: `a-dash-topo-pill-text is-sub ${compact ? "is-compact" : ""}`,
          fill: pal.ink,
        }, lines.line2 || ""),
      );
    } else {
      g.append(createSVGEl("text", {
        x: (dotX + (compact ? 6.5 : 7.5)).toFixed(1),
        y: (cy + (compact ? 3.2 : 3.6)).toFixed(1),
        class: "a-dash-topo-pill-text",
        fill: pal.ink,
      }, label));
    }

    bindInteractive(g, eKey, { openProbeID: probe.probeID || probe.id });
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

  /** Renders grouped connected latency topology graphs (nodes deduplicated per group + bidirectional SVG conduits + collision-free pills). */
  const buildFlowBoardElement = (model, isMobile) => {
    const selIndex = buildTopologySelectionIndex(model);

    const svgEl = createSVGEl("svg", {
      class: `a-dash-topo-svg a-dash-topo-flow-svg ${isMobile ? "is-mobile" : ""}`,
      viewBox: "0 0 600 320",
      role: "img",
      "aria-label": "分组全链路延迟监测拓扑图",
      "data-refresh-key": "flow-edge-svg",
    });
    // Keep the SVG DOM identity stable across edge re-measurements on resize/data updates.
    svgEl.reconcileTable = () => true;

    const buildNodeTile = (item, opts = {}) => {
      const eKey = entityKey.node(item.id);
      const peers = selIndex.nodePeers(item.id);
      const region = geoRegionDetails(item.regionCode);
      const statusBadge = item.blocked
        ? badge("超限阻断", "bad")
        : badge(item.isOnline ? "在线" : "离线", item.isOnline ? "ok" : "bad");
      const stat = opts.stat || null;
      const addrText = opts.address || item.ip || "";
      const statSummary = stat && stat.degreeInGroup > 0
        ? `${stat.degreeInGroup}链${stat.avgLatencyMS != null ? ` · 均${formatMs(stat.avgLatencyMS)}` : ""}`
        : "";

      const cardEl = node(
        "button",
        {
          type: "button",
          className: `a-dash-topo-flow-card is-node ${opts.isHub ? "is-hub" : ""} ${item.blocked ? "is-blocked" : ""} ${!item.isOnline ? "is-offline" : ""} ${stat?.badCount > 0 ? "has-bad-link" : ""}`,
          "data-refresh-key": opts.refreshKey || `flow-node-${item.id}`,
          ...(opts.flowPort ? { "data-flow-port": opts.flowPort } : {}),
          "aria-pressed": pinnedKey === eKey ? "true" : "false",
          "data-topo-peers": peers.join(" "),
          title: `${item.name}${addrText ? ` (${addrText})` : ""}`,
        },
        node(
          "div",
          { className: "a-dash-topo-flow-node-head" },
          region ? node("span", { className: "a-dash-topo-flow-region" }, region.code) : null,
          node("strong", { className: "a-dash-topo-flow-node-name" }, item.name || item.id),
          statusBadge,
        ),
        (addrText || statSummary || stat?.badCount > 0) ? node(
          "div",
          { className: "a-dash-topo-flow-node-foot" },
          addrText ? node("span", { className: "a-dash-topo-flow-addr" }, addrText) : null,
          statSummary ? node("span", { className: "a-dash-topo-flow-role" }, statSummary) : null,
          stat?.badCount > 0 ? node("span", { className: "a-dash-topo-flow-role is-bad" }, `告警 ${stat.badCount}`) : null,
        ) : null,
      );
      bindInteractive(cardEl, eKey);
      return cardEl;
    };

    const buildExtTile = (ext, opts = {}) => {
      const eKey = entityKey.external(ext.id);
      const peers = selIndex.extPeers(ext.id);
      const showAddr = ext.address && ext.address !== ext.name;
      const stat = opts.stat || null;
      const cardEl = node(
        "button",
        {
          type: "button",
          className: `a-dash-topo-flow-card is-ext ${stat?.badCount > 0 ? "has-bad-link" : ""}`,
          "data-refresh-key": opts.refreshKey || `flow-ext-${ext.id}`,
          ...(opts.flowPort ? { "data-flow-port": opts.flowPort } : {}),
          "aria-pressed": pinnedKey === eKey ? "true" : "false",
          "data-topo-peers": peers.join(" "),
          title: `${ext.name}${showAddr ? ` (${ext.address})` : ""}`,
        },
        node("div", { className: "a-dash-topo-flow-node-head" },
          node("span", { className: "a-dash-topo-flow-kicker" }, "公网出口"),
          node("strong", { className: "a-dash-topo-flow-node-name" }, ext.name || ext.address || ext.id),
        ),
        (showAddr || stat?.degreeInGroup > 0) ? node(
          "div",
          { className: "a-dash-topo-flow-node-foot" },
          showAddr ? node("span", { className: "a-dash-topo-flow-addr" }, ext.address) : null,
          stat?.degreeInGroup > 0 ? node("span", { className: "a-dash-topo-flow-role" }, `${stat.degreeInGroup} 探测源`) : null,
          stat?.badCount > 0 ? node("span", { className: "a-dash-topo-flow-role is-bad" }, `告警 ${stat.badCount}`) : null,
        ) : null,
      );
      bindInteractive(cardEl, eKey);
      return cardEl;
    };

    const renderedGroups = model.activeGroup
      ? [model.activeGroup]
      : (model.probeGroups?.length
          ? model.probeGroups
          : [{
              id: "unmonitored",
              title: "网络节点",
              kind: "nodes",
              chains: [],
              links: [],
              nodes: model.nodes,
              externalTargets: model.externalTargets || [],
              stats: { total: 0, hopCount: 0, nodeCount: model.nodes.length, extCount: 0, okCount: 0, badCount: 0, runningCount: 0, avgFwdMS: null, avgRevMS: null },
            }]);

    const nodeByID = new Map(model.nodes.map((n) => [n.id, n]));
    const extByID = new Map((model.externalTargets || []).map((e) => [e.id, e]));

    const groupEls = renderedGroups.map((group) => {
      const plan = planGroupTopologyGraph(group, { maxCols: isMobile ? 1 : 4, stacked: isMobile });
      const multiHopChains = (group.chains || []).filter((ch) => ch.isMultiHop || (ch.endpoints?.length || 0) >= 3);

      const groupBadges = [];
      if (group.kind === "nodes") {
        groupBadges.push(badge(`${plan.vertices.length} 节点`, ""));
      } else {
        groupBadges.push(badge(`${group.chains.length} 条 · ${plan.vertices.length} 节点`, ""));
        if (group.stats?.badCount > 0) groupBadges.push(badge(`告警 ${group.stats.badCount}`, "bad"));
        else if (group.stats?.okCount > 0) groupBadges.push(badge(`正常 ${group.stats.okCount}`, "ok"));
        if (group.stats?.avgFwdMS != null) {
          const revStr = group.stats.avgRevMS != null ? ` · 均回 ${formatMs(group.stats.avgRevMS)}` : "";
          groupBadges.push(muted(`均去 ${formatMs(group.stats.avgFwdMS)}${revStr}`));
        }
      }
      if (canExecuteProbes() && group.kind !== "nodes") {
        groupBadges.push(button("+ 本组新建", () => onOpenProbes("", {
          create: true,
          groupName: group.kind === "named" ? group.title : "",
        }), {
          className: "a-button a-dash-topo-group-add",
          "data-refresh-key": `group-add-${group.id}`,
        }));
      }

      const chainStripsEl = multiHopChains.length
        ? node(
            "div",
            { className: "a-dash-topo-chain-strips", "data-refresh-key": `chain-strips-${group.id}` },
            ...multiHopChains.map((chain) => {
              const chainKey = entityKey.probe(chain.id);
              const chainPeers = selIndex.chainPeers ? selIndex.chainPeers(chain.id) : [chainKey];
              const arrow = chain.bidirectional ? " ⇄ " : " → ";
              const epNames = (chain.endpoints || []).map((ep) => ep.name || ep.address || ep.id);
              const routeText = epNames.join(arrow);
              const rttText = formatChainRttText(chain);
              const lossText = formatLossBadgeText(chain);

              const focusBtn = node(
                "button",
                {
                  type: "button",
                  className: "a-dash-topo-chain-strip-focus",
                  "data-refresh-key": `strip-focus-${chain.id}`,
                  "data-topo-peers": chainPeers.join(" "),
                  title: `聚焦整条链路: ${routeText}`,
                },
                badge(`全程 ${epNames.length} 站`, chain.tone || ""),
                node("strong", { className: "a-dash-topo-chain-title" }, chain.name),
                node("span", { className: "a-dash-topo-chain-path" }, routeText),
                node("span", { className: "a-dash-topo-chain-strip-kpis" },
                  badge(rttText, chain.tone || ""),
                  badge(lossText, chain.tone || ""),
                ),
              );
              bindInteractive(focusBtn, chainKey);

              return node(
                "div",
                {
                  className: "a-dash-topo-chain-strip",
                  "data-refresh-key": `chain-strip-${chain.id}`,
                  "data-tone": chain.tone || "idle",
                  "data-topo-peers": chainPeers.join(" "),
                },
                focusBtn,
                canExecuteProbes()
                  ? button("增减节点", () => onOpenProbes(chain.id, { edit: true }), {
                      className: "a-button a-dash-topo-chain-edit",
                      "data-refresh-key": `strip-edit-${chain.id}`,
                    })
                  : null,
              );
            }),
          )
        : null;

      const graphCells = plan.vertices.map((v) => {
        const portID = `${group.id}::${v.key}`;
        let tileEl;
        if (v.kind === "ext") {
          const extObj = extByID.get(v.id) || v.ext || { id: v.id, name: v.label, address: "", linkCount: v.degreeInGroup };
          tileEl = buildExtTile(extObj, {
            refreshKey: `graph-${group.id}-${v.key}`,
            flowPort: portID,
            stat: v,
          });
        } else {
          const liveNode = nodeByID.get(v.id) || v.node || {
            id: v.id,
            name: v.label || v.id,
            regionCode: "",
            ip: "",
            isOnline: Boolean(v.isOnline),
            blocked: false,
            activeEngines: [],
          };
          tileEl = buildNodeTile(liveNode, {
            refreshKey: `graph-${group.id}-${v.key}`,
            flowPort: portID,
            isHub: v.isHub,
            stat: v,
          });
        }
        return node(
          "div",
          {
            className: "a-dash-topo-graph-cell",
            "data-refresh-key": `cell-${group.id}-${v.key}`,
            "data-graph-col": String(v.col),
            "data-graph-row": String(v.row),
          },
          tileEl,
        );
      });

      const graphStage = node(
        "div",
        {
          className: `a-dash-topo-group-graph ${isMobile ? "is-stacked" : ""}`,
          "data-refresh-key": `group-graph-${group.id}`,
          "data-flow-group": group.id,
          "data-group-layout": plan.layout,
          "data-graph-cols": String(plan.cols),
        },
        node(
          "div",
          {
            className: "a-dash-topo-graph-grid",
            "data-refresh-key": `graph-grid-${group.id}`,
            "data-graph-cols": String(plan.cols),
            "data-graph-rows": String(plan.rows),
            "data-graph-stacked": isMobile ? "true" : "false",
          },
          ...graphCells,
        ),
        group.kind === "nodes" && !(model.probeChains || []).length
          ? node("p", { className: "a-muted a-dash-topo-empty-hint" }, "暂未配置延迟监测链路；点击右上角「新建全程监测」可建立支持 A-B-C-D 的双向全链路监测。")
          : null,
      );

      return node(
        "section",
        {
          className: `a-dash-topo-group is-${group.kind}`,
          "data-refresh-key": `topo-group-${group.id}`,
          "data-group-kind": group.kind,
        },
        node(
          "header",
          { className: "a-dash-topo-group-head", "data-refresh-key": "group-head" },
          node("div", { className: "a-dash-topo-group-title" }, node("h3", {}, group.title)),
          node("div", { className: "a-dash-topo-group-meta a-actions" }, ...groupBadges),
        ),
        chainStripsEl,
        graphStage,
      );
    });

    const groupsContainer = node(
      "div",
      { className: "a-dash-topo-groups", "data-refresh-key": "flow-groups" },
      ...groupEls,
    );

    return node("div", {
      className: `a-dash-topo-flow-board ${isMobile ? "is-mobile" : ""}`,
      "data-topo-mode": "flow",
      "data-refresh-key": "topo-flow-board",
    }, svgEl, groupsContainer);
  };

  /** Draws deduplicated connected SVG graph edges + bidirectional arrows + collision-free RTT/loss pills per group. */
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
    const groupContainers = new Map();
    board.querySelectorAll("[data-flow-group]").forEach((el) => {
      const r = el.getBoundingClientRect();
      if (r.width <= 0 || r.height <= 0) return;
      groupContainers.set(el.getAttribute("data-flow-group"), {
        el,
        left: r.left - bRect.left,
        right: r.right - bRect.left,
        top: r.top - bRect.top,
        bottom: r.bottom - bRect.top,
        width: r.width,
        height: r.height,
        stacked: el.classList.contains("is-stacked") || el.getAttribute("data-graph-cols") === "1",
      });
    });

    const ports = new Map();
    board.querySelectorAll("[data-flow-port]").forEach((el) => {
      const r = el.getBoundingClientRect();
      if (r.width <= 0 || r.height <= 0) return;
      const cell = el.closest(".a-dash-topo-graph-cell");
      const portId = el.getAttribute("data-flow-port") || "";
      const sepIdx = portId.indexOf("::");
      const groupId = sepIdx >= 0 ? portId.slice(0, sepIdx) : "";
      ports.set(portId, {
        portId,
        groupId,
        col: Number(cell?.getAttribute("data-graph-col") || 0),
        row: Number(cell?.getAttribute("data-graph-row") || 0),
        left: r.left - bRect.left,
        right: r.right - bRect.left,
        top: r.top - bRect.top,
        bottom: r.bottom - bRect.top,
        w: r.width,
        h: r.height,
        cx: (r.left + r.right) / 2 - bRect.left,
        cy: (r.top + r.bottom) / 2 - bRect.top,
      });
    });

    const portSig = [...ports.entries()].map(([k, v]) => `${k}:${Math.round(v.left)},${Math.round(v.top)},${Math.round(v.right)},${Math.round(v.bottom)}`).join("|");
    const renderedGroups = model.activeGroup ? [model.activeGroup] : (model.probeGroups || []);
    const edgeSig = JSON.stringify([w, h, isMobile, portSig, renderedGroups.map((g) => g.id), model.probeChains]);
    if (!forceRedraw && edgeSig === lastFlowGeomSignature) return;
    lastFlowGeomSignature = edgeSig;

    svg.setAttribute("viewBox", `0 0 ${w} ${h}`);
    svg.setAttribute("width", String(w));
    svg.setAttribute("height", String(h));

    const defs = createTopologyDefs("a-topo");
    const edgeLayer = createSVGEl("g", { class: "a-dash-topo-flow-probes" });
    const leaderLayer = createSVGEl("g", { class: "a-dash-topo-flow-leaders" });
    const pillLayer = createSVGEl("g", { class: "a-dash-topo-flow-pills" });
    const selIndex = buildTopologySelectionIndex(model);

    const arrowForTone = (tone) => (
      tone === "bad" ? "url(#a-topo-arrow-bad)"
        : tone === "warn" ? "url(#a-topo-arrow-warn)"
          : tone === "ok" ? "url(#a-topo-arrow-ok)"
            : "url(#a-topo-arrow-idle)"
    );

    const drawDirSegment = (d, dirTone, running, peers, hl, refreshKey) => {
      const pal = tonePalette(dirTone);
      const isBad = dirTone === "bad";
      const isWarn = dirTone === "warn";
      const baseOpacity = isBad ? 0.92 : isWarn ? 0.85 : dirTone === "ok" ? 0.78 : 0.48;
      const g = createSVGEl("g", {
        "data-refresh-key": refreshKey,
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": String(baseOpacity),
        opacity: (hl === true ? 1 : hl === false ? 0.14 : baseOpacity).toFixed(2),
      });
      g.append(createSVGEl("path", {
        d,
        fill: "none",
        stroke: pal.stroke,
        "stroke-width": hl === true ? "2.15" : isBad ? "1.9" : "1.55",
        "stroke-dasharray": running ? "5 4" : isBad ? "4 3" : null,
        "stroke-linecap": "round",
        "marker-end": arrowForTone(dirTone),
        class: running ? "a-dash-topo-probe-running" : null,
      }));
      if (allowMotion() && !running && dirTone === "ok") {
        g.append(createSVGEl("path", {
          d,
          fill: "none",
          stroke: pal.stroke,
          "stroke-width": "1.35",
          "stroke-dasharray": "4 14",
          "stroke-linecap": "round",
          opacity: "0.42",
          class: "a-dash-topo-flow-pulse is-speed-fast",
        }));
      }
      edgeLayer.append(g);
    };

    const cubicPt = (x0, y0, c1x, c1y, c2x, c2y, x3, y3, t) => {
      const mt = 1 - t;
      return {
        x: mt * mt * mt * x0 + 3 * mt * mt * t * c1x + 3 * mt * t * t * c2x + t * t * t * x3,
        y: mt * mt * mt * y0 + 3 * mt * mt * t * c1y + 3 * mt * t * t * c2y + t * t * t * y3,
      };
    };

    const allocateSpanLanes = (items) => {
      const lanes = [];
      const res = new Map();
      const sorted = [...items].sort((a, b) => a.span - b.span || a.min - b.min || a.max - b.max);
      for (const item of sorted) {
        let idx = lanes.findIndex((occ) => occ.every((o) => item.max <= o.min || item.min >= o.max));
        if (idx < 0) {
          idx = lanes.length;
          lanes.push([]);
        }
        lanes[idx].push({ min: item.min, max: item.max });
        res.set(item.hop.id, { lane: idx, count: 0 });
      }
      const total = Math.max(1, lanes.length);
      res.forEach((v) => { v.count = total; });
      return res;
    };

    renderedGroups.forEach((group) => {
      const gBox = groupContainers.get(group.id);
      if (!gBox) return;
      const hops = (group.links && group.links.length)
        ? group.links
        : (group.chains || []).flatMap((c) => c.hops || []);
      if (!hops.length) return;

      const groupPorts = [...ports.values()].filter((p) => p.groupId === group.id);
      const occupied = groupPorts.map((p) => ({ x: p.left, y: p.top, w: p.w, h: p.h }));
      const pillBounds = {
        minX: gBox.left + 6,
        maxX: gBox.right - 6,
        minY: gBox.top + 6,
        maxY: gBox.bottom - 6,
      };

      const validEdges = [];
      const pairCounter = new Map();
      for (const hop of hops) {
        const uKey = entityKey.node(hop.from);
        const vKey = hop.isExternal ? entityKey.external(hop.to) : entityKey.node(hop.to);
        const uPort = ports.get(`${group.id}::${uKey}`);
        const vPort = ports.get(`${group.id}::${vKey}`);
        if (!uPort || !vPort) continue;
        const pairKey = uKey < vKey ? `${uKey}|${vKey}` : `${vKey}|${uKey}`;
        const list = pairCounter.get(pairKey) || [];
        const pairIdx = list.length;
        list.push(hop.id);
        pairCounter.set(pairKey, list);
        validEdges.push({ hop, uKey, vKey, uPort, vPort, pairKey, pairIdx });
      }
      if (!validEdges.length) return;

      if (gBox.stacked) {
        const maxCardRight = Math.max(...groupPorts.map((p) => p.right));
        const laneItems = validEdges.map((e) => ({
          hop: e.hop,
          min: Math.min(e.uPort.row, e.vPort.row),
          max: Math.max(e.uPort.row, e.vPort.row),
          span: Math.abs(e.vPort.row - e.uPort.row),
        }));
        const lanes = allocateSpanLanes(laneItems);
        const xEdge = maxCardRight + 2;
        const availBow = Math.max(24, gBox.right - 12 - xEdge);

        validEdges.forEach(({ hop, uPort, vPort }) => {
          const peers = selIndex.probePeers(hop);
          const hl = isHighlightedEdge(peers);
          const fwdTone = hop.forward?.status?.tone || hop.tone || "";
          const revTone = hop.reverse?.status?.tone || hop.tone || "";
          const laneInfo = lanes.get(hop.id) || { lane: 0, count: 1 };
          const bowStep = Math.min(18, Math.max(10, (availBow - 16) / Math.max(1, laneInfo.count)));
          const bow = Math.min(availBow - 4, 18 + laneInfo.lane * bowStep);
          const sep = hop.bidirectional ? 3 : 0;

          const fwdArch = bridgeArchPath(xEdge, uPort.cy - sep, vPort.cy - sep, bow, 4);
          drawDirSegment(fwdArch.d, fwdTone, hop.running, peers, hl, `seg-fwd-${hop.id}`);
          if (hop.bidirectional) {
            const revArch = bridgeArchPath(xEdge, vPort.cy + sep, uPort.cy + sep, Math.min(availBow, bow + 6), 4);
            drawDirSegment(revArch.d, revTone, hop.running, peers, hl, `seg-rev-${hop.id}`);
          }

          const lines = buildBidirectionalPillLines(hop);
          const pal = tonePalette(hop.tone || fwdTone);
          const textW = Math.max(visualUnits(lines.line1) * 5.7, visualUnits(lines.line2) * 5.1);
          const pillW = Math.round(Math.min(Math.max(82, availBow + 10), Math.max(76, textW + 22)));
          const pillH = 26;
          const midPt = bridgePointAt(xEdge, uPort.cy, vPort.cy, bow, 0.5, 4);
          const anchors = [0.5, 0.36, 0.64, 0.25, 0.75].map((t) => bridgePointAt(xEdge, uPort.cy, vPort.cy, bow, t, 4));
          const placed = placeCollisionFreePill(midPt.x, midPt.y, pillW, pillH, occupied, pillBounds, anchors, { padX: 3, padY: 3 });
          appendProbeLeader(leaderLayer, placed, pal, peers, hl, true);
          pillLayer.append(createProbePillGroup({
            probe: hop,
            cx: placed.cx,
            cy: placed.cy,
            pillW,
            pillH,
            lines,
            pal,
            compact: true,
            peers,
          }));
        });
        return;
      }

      // Desktop connected graph: fan incident edges along each card's border walls.
      const wallBuckets = new Map();
      const pushWall = (portKey, side, edgeObj, partnerPort) => {
        const bkKey = `${portKey}|${side}`;
        if (!wallBuckets.has(bkKey)) wallBuckets.set(bkKey, []);
        wallBuckets.get(bkKey).push({ edgeId: edgeObj.hop.id, cy: partnerPort.cy, cx: partnerPort.cx });
      };

      validEdges.forEach((edgeObj) => {
        const { uPort, vPort } = edgeObj;
        if (uPort.col === vPort.col) {
          if (Math.abs(uPort.row - vPort.row) === 1) {
            const uIsTop = uPort.row < vPort.row;
            pushWall(uPort.portId, uIsTop ? "bottom" : "top", edgeObj, vPort);
            pushWall(vPort.portId, uIsTop ? "top" : "bottom", edgeObj, uPort);
          } else {
            pushWall(uPort.portId, "right", edgeObj, vPort);
            pushWall(vPort.portId, "right", edgeObj, uPort);
          }
        } else {
          const goingRight = vPort.col > uPort.col;
          pushWall(uPort.portId, goingRight ? "right" : "left", edgeObj, vPort);
          pushWall(vPort.portId, goingRight ? "left" : "right", edgeObj, uPort);
        }
      });

      const fanOffsets = new Map();
      wallBuckets.forEach((items, bkKey) => {
        const isVertWall = bkKey.endsWith("|top") || bkKey.endsWith("|bottom");
        items.sort((a, b) => (isVertWall ? a.cx - b.cx : a.cy - b.cy) || String(a.edgeId).localeCompare(String(b.edgeId)));
        const count = items.length;
        items.forEach((item, idx) => {
          const span = Math.min(34, Math.max(0, (count - 1) * 11));
          const val = count > 1 ? -span / 2 + (idx / (count - 1)) * span : 0;
          fanOffsets.set(`${bkKey}|${item.edgeId}`, val);
        });
      });

      const sameColNonAdj = [];
      validEdges.forEach((e) => {
        if (e.uPort.col === e.vPort.col && Math.abs(e.uPort.row - e.vPort.row) >= 2) {
          sameColNonAdj.push({
            hop: e.hop,
            min: Math.min(e.uPort.row, e.vPort.row),
            max: Math.max(e.uPort.row, e.vPort.row),
            span: Math.abs(e.vPort.row - e.uPort.row),
          });
        }
      });
      const sameColLanes = allocateSpanLanes(sameColNonAdj);

      validEdges.forEach(({ hop, uPort, vPort, pairKey, pairIdx }) => {
        const peers = selIndex.probePeers(hop);
        const hl = isHighlightedEdge(peers);
        const fwdTone = hop.forward?.status?.tone || hop.tone || "";
        const revTone = hop.reverse?.status?.tone || hop.tone || "";
        const totalInPair = (pairCounter.get(pairKey) || []).length;
        const pairShift = totalInPair > 1 ? (pairIdx - (totalInPair - 1) / 2) * 14 : 0;

        let anchors = [];
        let midPt = { x: (uPort.cx + vPort.cx) / 2, y: (uPort.cy + vPort.cy) / 2 };

        if (uPort.col === vPort.col) {
          if (Math.abs(uPort.row - vPort.row) === 1) {
            const goingDown = vPort.row > uPort.row;
            const uOff = fanOffsets.get(`${uPort.portId}|${goingDown ? "bottom" : "top"}|${hop.id}`) || 0;
            const vOff = fanOffsets.get(`${vPort.portId}|${goingDown ? "top" : "bottom"}|${hop.id}`) || 0;
            const sep = hop.bidirectional ? 4.2 : 0;
            const x1 = uPort.cx + uOff + pairShift - sep;
            const x2 = vPort.cx + vOff + pairShift - sep;
            const y1 = goingDown ? uPort.bottom + 2 : uPort.top - 2;
            const y2 = goingDown ? vPort.top - 3 : vPort.bottom + 3;
            const dy = Math.max(16, Math.abs(y2 - y1) * 0.45) * (goingDown ? 1 : -1);
            drawDirSegment(
              `M ${x1.toFixed(1)} ${y1.toFixed(1)} C ${x1.toFixed(1)} ${(y1 + dy).toFixed(1)}, ${x2.toFixed(1)} ${(y2 - dy).toFixed(1)}, ${x2.toFixed(1)} ${y2.toFixed(1)}`,
              fwdTone,
              hop.running,
              peers,
              hl,
              `seg-fwd-${hop.id}`,
            );
            if (hop.bidirectional) {
              const rx1 = uPort.cx + uOff + pairShift + sep;
              const rx2 = vPort.cx + vOff + pairShift + sep;
              const ryStart = goingDown ? vPort.top - 2 : vPort.bottom + 2;
              const ryEnd = goingDown ? uPort.bottom + 3 : uPort.top - 3;
              drawDirSegment(
                `M ${rx2.toFixed(1)} ${ryStart.toFixed(1)} C ${rx2.toFixed(1)} ${(ryStart - dy).toFixed(1)}, ${rx1.toFixed(1)} ${(ryEnd + dy).toFixed(1)}, ${rx1.toFixed(1)} ${ryEnd.toFixed(1)}`,
                revTone,
                hop.running,
                peers,
                hl,
                `seg-rev-${hop.id}`,
              );
            }
            const tList = [0.5, 0.35, 0.65, 0.25, 0.75];
            anchors = tList.map((t) => cubicPt(x1, y1, x1, y1 + dy, x2, y2 - dy, x2, y2, t));
            midPt = anchors[0];
          } else {
            // Non-adjacent cards in the same column: arch smoothly into the right gutter of the column.
            const uOff = fanOffsets.get(`${uPort.portId}|right|${hop.id}`) || 0;
            const vOff = fanOffsets.get(`${vPort.portId}|right|${hop.id}`) || 0;
            const xEdge = Math.max(uPort.right, vPort.right) + 2;
            const laneInfo = sameColLanes.get(hop.id) || { lane: 0, count: 1 };
            const availGap = Math.max(26, gBox.right - 14 - xEdge);
            const bow = Math.min(availGap - 4, 24 + laneInfo.lane * 16);
            const sep = hop.bidirectional ? 3 : 0;
            const fwdArch = bridgeArchPath(xEdge, uPort.cy + uOff - sep, vPort.cy + vOff - sep, bow, 4);
            drawDirSegment(fwdArch.d, fwdTone, hop.running, peers, hl, `seg-fwd-${hop.id}`);
            if (hop.bidirectional) {
              const revArch = bridgeArchPath(xEdge, vPort.cy + vOff + sep, uPort.cy + uOff + sep, Math.min(availGap, bow + 6), 4);
              drawDirSegment(revArch.d, revTone, hop.running, peers, hl, `seg-rev-${hop.id}`);
            }
            anchors = [0.5, 0.35, 0.65, 0.25, 0.75].map((t) => bridgePointAt(xEdge, uPort.cy + uOff, vPort.cy + vOff, bow, t, 4));
            midPt = anchors[0];
          }
        } else {
          const goingRight = vPort.col > uPort.col;
          const dirSign = goingRight ? 1 : -1;
          const uOff = fanOffsets.get(`${uPort.portId}|${goingRight ? "right" : "left"}|${hop.id}`) || 0;
          const vOff = fanOffsets.get(`${vPort.portId}|${goingRight ? "left" : "right"}|${hop.id}`) || 0;
          const sep = hop.bidirectional ? 3.8 : 0;

          const x1 = goingRight ? uPort.right + 2 : uPort.left - 2;
          const x2 = goingRight ? vPort.left - 3 : vPort.right + 3;
          const y1 = uPort.cy + uOff + pairShift - sep;
          const y2 = vPort.cy + vOff + pairShift - sep;

          // Check whether this edge skips a column containing cards that cross its vertical band.
          const minCol = Math.min(uPort.col, vPort.col);
          const maxCol = Math.max(uPort.col, vPort.col);
          const midCards = maxCol - minCol >= 2
            ? groupPorts.filter((p) => p.col > minCol && p.col < maxCol)
            : [];
          const bandMin = Math.min(y1, y2) - 18;
          const bandMax = Math.max(y1, y2) + 18;
          const blockedMid = midCards.some((c) => c.bottom >= bandMin && c.top <= bandMax);

          let c1x;
          let c1y;
          let c2x;
          let c2y;
          if (blockedMid) {
            const topEdge = Math.min(...midCards.map((c) => c.top));
            const botEdge = Math.max(...midCards.map((c) => c.bottom));
            const groupMidY = (gBox.top + gBox.bottom) / 2;
            const useTop = ((y1 + y2) / 2) <= groupMidY;
            const bypassY = useTop
              ? Math.max(gBox.top + 16, topEdge - 28 - Math.abs(pairIdx) * 14)
              : Math.min(gBox.bottom - 16, botEdge + 28 + Math.abs(pairIdx) * 14);
            const spanX = Math.abs(x2 - x1);
            c1x = x1 + dirSign * Math.max(40, spanX * 0.28);
            c2x = x2 - dirSign * Math.max(40, spanX * 0.28);
            c1y = bypassY;
            c2y = bypassY;
          } else {
            const dx = Math.max(26, Math.abs(x2 - x1) * 0.45) * dirSign;
            c1x = x1 + dx;
            c2x = x2 - dx;
            c1y = y1;
            c2y = y2;
          }

          drawDirSegment(
            `M ${x1.toFixed(1)} ${y1.toFixed(1)} C ${c1x.toFixed(1)} ${c1y.toFixed(1)}, ${c2x.toFixed(1)} ${c2y.toFixed(1)}, ${x2.toFixed(1)} ${y2.toFixed(1)}`,
            fwdTone,
            hop.running,
            peers,
            hl,
            `seg-fwd-${hop.id}`,
          );
          if (hop.bidirectional) {
            const rxStart = goingRight ? vPort.left - 2 : vPort.right + 2;
            const rxEnd = goingRight ? uPort.right + 3 : uPort.left - 3;
            const ryStart = vPort.cy + vOff + pairShift + sep;
            const ryEnd = uPort.cy + uOff + pairShift + sep;
            const rc1y = blockedMid ? c2y + 7 : ryStart;
            const rc2y = blockedMid ? c1y + 7 : ryEnd;
            drawDirSegment(
              `M ${rxStart.toFixed(1)} ${ryStart.toFixed(1)} C ${c2x.toFixed(1)} ${rc1y.toFixed(1)}, ${c1x.toFixed(1)} ${rc2y.toFixed(1)}, ${rxEnd.toFixed(1)} ${ryEnd.toFixed(1)}`,
              revTone,
              hop.running,
              peers,
              hl,
              `seg-rev-${hop.id}`,
            );
          }
          anchors = [0.5, 0.36, 0.64, 0.25, 0.75].map((t) => cubicPt(x1, y1, c1x, c1y, c2x, c2y, x2, y2, t));
          midPt = anchors[0];
        }

        const lines = buildBidirectionalPillLines(hop);
        const pal = tonePalette(hop.tone || fwdTone);
        const textW = Math.max(visualUnits(lines.line1) * 6.2, visualUnits(lines.line2) * 5.4);
        const pillW = Math.round(Math.max(84, Math.min(188, textW + 24)));
        const pillH = 29;
        const placed = placeCollisionFreePill(midPt.x, midPt.y, pillW, pillH, occupied, pillBounds, anchors, { padX: 4, padY: 3 });
        appendProbeLeader(leaderLayer, placed, pal, peers, hl, false);
        pillLayer.append(createProbePillGroup({
          probe: hop,
          cx: placed.cx,
          cy: placed.cy,
          pillW,
          pillH,
          lines,
          pal,
          compact: false,
          peers,
        }));
      });
    });

    svg.replaceChildren(defs, edgeLayer, leaderLayer, pillLayer);
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

    const rawModel = buildDashboardTopology({
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
    const probeGroups = rawModel.probeGroups || [];
    const effectiveGroupId = selectedGroupId && probeGroups.some((g) => g.id === selectedGroupId) ? selectedGroupId : "";
    const model = effectiveGroupId ? filterTopologyModelByGroup(rawModel, effectiveGroupId) : rawModel;
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
      ...(canAgents && canTasks ? ["probes"] : []),
    ];
    const waiting = waitingKeys.some((key) => !states.has(key) || states.get(key) === "loading");
    const probesFailed = canAgents && canTasks && probesState === "failed";
    const agentsFailed = canAgents && agentsState === "failed";

    let nextStatus = "";
    if (!model.nodes.length) {
      if (waiting) nextStatus = "读取中…";
      else if (agentsFailed || (probesFailed && !hasProbePairs)) nextStatus = probesFailed && !agentsFailed ? "探测链路读取失败" : "拓扑读取失败";
      else if (canAgents && canTasks && probesState === "ready" && !hasProbePairs && !isStandalonePage) nextStatus = "未设置检测目标";
      else if (!canAgents) nextStatus = "无权查看拓扑数据";
      else nextStatus = isStandalonePage ? "暂无可用拓扑数据" : "暂无节点数据";
    } else if (probesFailed) {
      nextStatus = hasProbePairs ? "探测链路刷新失败 · 保留已有记录" : "探测链路读取失败";
    } else if (canAgents && canTasks && probesState === "ready" && !hasProbePairs && !isStandalonePage) {
      nextStatus = "未设置检测目标";
    }

    if (statusNode.textContent !== nextStatus) statusNode.textContent = nextStatus;

    const measuredWidth = observedWidth >= 200 ? observedWidth : (canvasHost.clientWidth >= 200 ? canvasHost.clientWidth : 0);
    const fallbackWidth = Math.max(300, Math.min(1120, Math.round((window.innerWidth || 1080) - (window.innerWidth > 896 ? 300 : 48))));
    const renderWidth = Math.max(280, Math.round(measuredWidth > 0 ? measuredWidth : fallbackWidth));
    const isMobile = renderWidth < 580;

    const sigModel = {
      nodes: model.nodes,
      externalTargets: model.externalTargets,
      probeLinks: model.probeLinks,
      probeChains: model.probeChains,
      probeGroups: model.probeGroups,
      hiddenNodeCount: model.hiddenNodeCount,
      omittedProbeCount: model.omittedProbeCount,
      abnormalLinks: model.abnormalLinks,
    };
    const sig = JSON.stringify([renderWidth, isMobile, viewMode, effectiveGroupId, mapZoom, sigModel, nextStatus]);
    if (!force && sig === lastPaintSignature) return;
    lastPaintSignature = sig;
    lastFlowGeomSignature = "";

    if (probeGroups.length) {
      groupBarHost.hidden = false;
      const totalChains = (rawModel.probeChains || []).length;
      const totalBad = rawModel.abnormalLinks || 0;
      const allTab = button(
        [
          node("span", { className: `a-dash-topo-group-dot is-${totalBad > 0 ? "bad" : totalChains > 0 ? "ok" : "idle"}`, "aria-hidden": "true" }),
          node("span", { className: "a-dash-topo-group-pill-label" }, "全部分组"),
          node("span", { className: "a-dash-topo-group-pill-count" }, `${probeGroups.length}组 · ${totalChains || rawModel.nodes.length}`),
        ],
        () => setSelectedGroup(""),
        {
          className: `a-dash-topo-group-pill ${effectiveGroupId === "" ? "is-active" : ""}`,
          role: "tab",
          "aria-selected": effectiveGroupId === "" ? "true" : "false",
          "data-group-id": "all",
          "data-refresh-key": "group-tab-all",
        },
      );
      const groupTabs = probeGroups.map((g) => {
        const gTone = g.stats?.badCount > 0 ? "bad" : g.stats?.okCount > 0 ? "ok" : "idle";
        const gVertices = g.kind === "nodes" ? g.nodes.length : (g.nodes.length + (g.externalTargets?.length || 0));
        const countLabel = g.kind === "nodes" ? `${g.nodes.length}点` : `${g.chains.length}链 · ${gVertices}点`;
        return button(
          [
            node("span", { className: `a-dash-topo-group-dot is-${gTone}`, "aria-hidden": "true" }),
            node("span", { className: "a-dash-topo-group-pill-label" }, g.title),
            node("span", { className: "a-dash-topo-group-pill-count" }, countLabel),
            g.stats?.badCount > 0
              ? node("span", { className: "a-dash-topo-group-pill-alert" }, `告警 ${g.stats.badCount}`)
              : null,
          ],
          () => setSelectedGroup(g.id),
          {
            className: `a-dash-topo-group-pill ${effectiveGroupId === g.id ? "is-active" : ""}`,
            role: "tab",
            "aria-selected": effectiveGroupId === g.id ? "true" : "false",
            "data-group-id": g.id,
            "data-refresh-key": `group-tab-${g.id}`,
          },
        );
      });
      patchChildren(groupBarHost, [allTab, ...groupTabs]);
    } else {
      groupBarHost.hidden = true;
      if (groupBarHost.childElementCount) groupBarHost.replaceChildren();
    }

    const chainCount = (model.probeChains || []).length;
    const multiHopCount = (model.probeChains || []).filter((c) => c.isMultiHop).length;
    const totalDenom = effectiveGroupId ? model.nodes.length : model.totalNodes;
    const metaBadges = [
      keyedBadge(`${model.onlineNodes}/${totalDenom} 在线`, model.onlineNodes ? "ok" : "bad", "topo-nodes"),
      probeGroups.length > 0
        ? keyedBadge(effectiveGroupId && model.activeGroup ? `分组: ${model.activeGroup.title}` : `${probeGroups.length} 个分组`, "", "topo-groups")
        : null,
      chainCount
        ? keyedBadge(
            model.abnormalLinks > 0 ? `链路告警 ${model.abnormalLinks}` : `监测 ${chainCount} 条${multiHopCount ? ` (${multiHopCount} 全程)` : ""}`,
            model.abnormalLinks > 0 ? "warn" : "ok",
            "topo-links",
          )
        : null,
      !isStandalonePage && !effectiveGroupId && (model.hiddenNodeCount > 0 || model.omittedProbeCount > 0)
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
        const freshBoard = buildFlowBoardElement(model, isStackedFlow);
        if (canvasHost.firstChild && canvasHost.firstChild.getAttribute("data-topo-mode") === "flow") {
          patch(canvasHost.firstChild, freshBoard);
        } else {
          canvasHost.replaceChildren(freshBoard);
        }
        syncFlowEdges(true);
        scheduleFlowEdgeSync(true);
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
    "网络延迟监测拓扑",
    node("div", { className: "a-card-body a-dash-topo-body" }, groupBarHost, canvasHost, inspectBar, probeMatrixHost),
    node("div", { className: "a-actions a-dash-topo-tools" }, headerMeta, modeSwitch, detailPageLink, addChainBtn, openProbesBtn, statusNode),
  );
  card?.classList?.add("a-dash-topo-card");

  return {
    visible: Boolean(card),
    element: card,
    paint,
    schedulePaint,
  };
}
