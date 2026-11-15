import {
  buildProbeGroupModels,
  buildProbeTopologyModel,
  buildSingleGroupTopologyModel,
  domainGroupForProbeGroup,
  filterAndSortProbeGroups,
  resolveSelectedProbeGroup,
  summarizeProbeGroups,
} from "../modules/dashboard-model.js";
import { renderPacificMapSVG } from "./dashboard-map.js";
import { createSVGEl } from "./dashboard-panel.js";
import {
  clusterTopologyNodes,
  entityKey as topoEntityKey,
  formatDirBadgeMetric,
  formatLoss,
  formatMs,
  formatMsCompact,
  isProbeUnreachable,
  matchesActiveSelection,
  reconcileSelectionKey,
  renderHopConduitSVG,
  renderProbeHistoryChart,
  summarizeGroupMetricLine,
} from "./dashboard-topo-shared.js";
import { PACIFIC_MAP_BOUNDS } from "./pacific-map-data.js";
import { listen, patch } from "./live-dom.js";
import { nodeRegionFlag } from "./node-region.js";
import { badge, button, empty, muted, node, notice, section, table } from "./ui.js";

const VALID_DASH_VIEW_MODES = new Set(["flow", "map"]);
const VALID_TOPO_VIEW_MODES = new Set(["overview", "flow", "map", "focus"]);
const VALID_TOPO_DOMAINS = new Set([
  "all",
  "domain:multihop",
  "domain:apac",
  "domain:emea",
  "domain:amer",
  "domain:cross",
  "domain:external",
]);
const VALID_TOPO_FOCUS_VIZ = new Set(["flow", "map"]);
const VALID_TOPO_FILTERS = new Set(["all", "abnormal", "running", "multihop", "paused"]);
const VALID_TOPO_SORTS = new Set(["default", "abnormal", "latency_desc", "loss_desc", "name_asc"]);
const VALID_TOPO_DIRS = new Set(["both", "forward", "reverse"]);

function normalizeDomainPref(value) {
  const s = String(value || "").trim();
  if (!s || s === "all") return "all";
  return VALID_TOPO_DOMAINS.has(s) ? s : "all";
}

function patchChildren(host, items) {
  const normalized = items.filter(Boolean);
  const fresh = node(host.tagName.toLowerCase(), { className: host.className }, ...normalized);
  for (const attr of host.attributes) {
    if (attr.name !== "class" && !fresh.hasAttribute(attr.name)) {
      fresh.setAttribute(attr.name, attr.value);
    }
  }
  patch(host, fresh);
}

const keyed = (key, el) => {
  if (el?.setAttribute) el.setAttribute("data-refresh-key", key);
  return el;
};

const keyedBadge = (text, tone, refreshKey) => keyed(refreshKey, badge(text, tone));

function directionLabel(dir) {
  if (dir === "forward") return "去程";
  if (dir === "reverse") return "回程";
  return "双向";
}

function groupKindBadgeText(group) {
  if (group.isExternal) return "自定义 IP";
  if (group.isMultiHop) return `多跳 ${group.nodes.length} 站`;
  return "双节点";
}

function clampZoomTransform(z) {
  const scale = Math.max(1, Math.min(3.2, Number(z?.scale) || 1));
  if (scale <= 1.001) return { scale: 1, tx: 0, ty: 0 };
  const vw = PACIFIC_MAP_BOUNDS.width;
  const vh = PACIFIC_MAP_BOUNDS.height;
  const minTx = vw * (1 - scale);
  const minTy = vh * (1 - scale);
  return {
    scale,
    tx: Math.max(minTx, Math.min(0, Number(z?.tx) || 0)),
    ty: Math.max(minTy, Math.min(0, Number(z?.ty) || 0)),
  };
}

export function createDashboardTopologySection(ctx, {
  date,
  getAgents,
  getOrderIDs,
  getProbes,
  getStates,
  probeController = null,
  isStandalonePage = false,
}) {
  const canReadProbes = () => ctx.can("agents.read") && ctx.can("tasks.read");
  const canViewSurface = () => (isStandalonePage ? (ctx.can("agents.read") || ctx.can("traffic.read")) : canReadProbes());
  const canExecuteProbes = () => Boolean(probeController?.canExecute && canReadProbes());
  const allowMotion = () => {
    if (document.documentElement.dataset.themeMotion === "off") return false;
    return !window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches;
  };

  let disposed = false;
  let paintScheduled = false;
  let paintForced = false;
  let lastPaintSignature = "";
  let observedWidth = 0;
  let chartWidth = isStandalonePage ? 640 : 480;

  // Dashboard single-group preferences (page = "dashboard")
  let dashGroupName = "";
  let dashViewMode = "flow";
  let dashShowHistory = false;
  let dashGroupTouched = false;
  let dashViewTouched = false;
  let dashHistoryTouched = false;

  // Standalone #topology workspace preferences (page = "topology")
  let topoViewMode = "overview";
  let topoDomainGroup = "all";
  let topoSelectedGroup = "";
  let topoFocusViz = "flow";
  let topoStatusFilter = "all";
  let topoSortBy = "default";
  let topoMetricDir = "both";
  let topoViewTouched = false;
  let topoDomainTouched = false;
  let topoGroupTouched = false;
  let topoFocusVizTouched = false;
  let topoFilterTouched = false;
  let topoSortTouched = false;
  let topoDirTouched = false;

  // Ephemeral inspection & map viewport state (never persisted)
  let focusedItemKey = "";
  let flowInspectKey = ""; // `${group.id}::node:${idx}` or `${group.id}::hop:${idx}` in standalone flow list
  let mapPinnedKey = "";
  let mapHoveredKey = "";
  let mapZoom = { scale: 1, tx: 0, ty: 0 };
  let suppressNextMapClick = false;
  let lastActiveGroup = null;
  let lastGroups = [];
  let lastFleetModel = null;
  let lastSingleMapModel = null;

  ctx.signal.addEventListener("abort", () => {
    disposed = true;
    paintScheduled = false;
    lastActiveGroup = null;
    lastFleetModel = null;
    lastSingleMapModel = null;
  }, { once: true });

  const syncSavedPreferences = ({ repaint = false } = {}) => {
    if (disposed || !ctx.preferences?.get) return;
    let changed = false;

    if (!isStandalonePage) {
      const saved = ctx.preferences.get("dashboard") || {};
      if (!dashGroupTouched && typeof saved.topo_group === "string" && saved.topo_group.length <= 40 && saved.topo_group !== dashGroupName) {
        dashGroupName = saved.topo_group;
        focusedItemKey = "";
        changed = true;
      }
      if (!dashViewTouched && VALID_DASH_VIEW_MODES.has(saved.topo_view_mode) && saved.topo_view_mode !== dashViewMode) {
        dashViewMode = saved.topo_view_mode;
        changed = true;
      }
      if (!dashHistoryTouched && typeof saved.topo_show_history === "boolean" && saved.topo_show_history !== dashShowHistory) {
        dashShowHistory = saved.topo_show_history;
        changed = true;
      }
    } else {
      const saved = ctx.preferences.get("topology") || {};
      if (!topoViewTouched && VALID_TOPO_VIEW_MODES.has(saved.view_mode) && saved.view_mode !== topoViewMode) {
        topoViewMode = saved.view_mode;
        focusedItemKey = "";
        flowInspectKey = "";
        mapPinnedKey = "";
        mapHoveredKey = "";
        changed = true;
      }
      if (!topoDomainTouched && saved.domain_group !== undefined) {
        const nextDomain = normalizeDomainPref(saved.domain_group);
        if (nextDomain !== topoDomainGroup) {
          topoDomainGroup = nextDomain;
          mapPinnedKey = "";
          mapHoveredKey = "";
          changed = true;
        }
      }
      if (!topoGroupTouched && typeof saved.selected_group === "string" && saved.selected_group.length <= 40 && saved.selected_group !== topoSelectedGroup) {
        topoSelectedGroup = saved.selected_group;
        focusedItemKey = "";
        changed = true;
      }
      if (!topoFocusVizTouched && VALID_TOPO_FOCUS_VIZ.has(saved.focus_viz) && saved.focus_viz !== topoFocusViz) {
        topoFocusViz = saved.focus_viz;
        changed = true;
      }
      if (!topoFilterTouched && VALID_TOPO_FILTERS.has(saved.status_filter) && saved.status_filter !== topoStatusFilter) {
        topoStatusFilter = saved.status_filter;
        changed = true;
      }
      if (!topoSortTouched && VALID_TOPO_SORTS.has(saved.sort_by) && saved.sort_by !== topoSortBy) {
        topoSortBy = saved.sort_by;
        changed = true;
      }
      if (!topoDirTouched && VALID_TOPO_DIRS.has(saved.metric_dir) && saved.metric_dir !== topoMetricDir) {
        topoMetricDir = saved.metric_dir;
        changed = true;
      }
    }

    if (changed && repaint && canViewSurface() && ctx.current() && lastPaintSignature) {
      paint(true);
    }
  };

  syncSavedPreferences({ repaint: false });
  if (ctx.preferences) {
    const unsub = ctx.preferences.subscribe?.(() => syncSavedPreferences({ repaint: true }));
    if (typeof unsub === "function") {
      ctx.signal.addEventListener("abort", unsub, { once: true });
    }
    void ctx.preferences.ready?.().then(() => syncSavedPreferences({ repaint: true }));
  }

  const selectDashboardGroup = (groupName, { persist = true } = {}) => {
    const next = String(groupName || "").trim().slice(0, 40);
    dashGroupTouched = true;
    const changed = dashGroupName !== next;
    if (persist && changed) {
      ctx.preferences?.patch?.("dashboard", { topo_group: next });
    }
    dashGroupName = next;
    focusedItemKey = "";
    mapHoveredKey = "";
    mapZoom = { scale: 1, tx: 0, ty: 0 };
    if (changed) paint(true);
  };

  const setDashboardViewMode = (mode) => {
    const normalized = mode === "map" ? "map" : "flow";
    dashViewTouched = true;
    const changed = dashViewMode !== normalized;
    dashViewMode = normalized;
    mapHoveredKey = "";
    if (changed) {
      ctx.preferences?.patch?.("dashboard", { topo_view_mode: normalized });
      paint(true);
    }
  };

  const toggleDashboardHistory = () => {
    dashHistoryTouched = true;
    dashShowHistory = !dashShowHistory;
    ctx.preferences?.patch?.("dashboard", { topo_show_history: dashShowHistory });
    paint(true);
  };

  const setTopoViewMode = (mode, { focusGroupName = null, focusItem = "" } = {}) => {
    const normalized = VALID_TOPO_VIEW_MODES.has(mode) ? mode : "overview";
    const patchFields = {};
    topoViewTouched = true;
    if (topoViewMode !== normalized) {
      topoViewMode = normalized;
      patchFields.view_mode = normalized;
    }
    if (focusGroupName != null) {
      const nextGroup = String(focusGroupName || "").trim().slice(0, 40);
      topoGroupTouched = true;
      if (topoSelectedGroup !== nextGroup) {
        topoSelectedGroup = nextGroup;
        patchFields.selected_group = nextGroup;
      }
    }
    focusedItemKey = focusItem || "";
    flowInspectKey = "";
    mapPinnedKey = "";
    mapHoveredKey = "";
    mapZoom = { scale: 1, tx: 0, ty: 0 };
    if (Object.keys(patchFields).length) {
      ctx.preferences?.patch?.("topology", patchFields);
    }
    paint(true);
  };

  const setTopoDomainGroup = (domainID) => {
    const normalized = normalizeDomainPref(domainID);
    topoDomainTouched = true;
    const changed = topoDomainGroup !== normalized;
    topoDomainGroup = normalized;
    if (changed) {
      mapPinnedKey = "";
      mapHoveredKey = "";
      focusedItemKey = "";
      flowInspectKey = "";
      ctx.preferences?.patch?.("topology", { domain_group: normalized });
      paint(true);
    }
  };

  const setTopoSelectedGroup = (groupName, { persist = true } = {}) => {
    const next = String(groupName || "").trim().slice(0, 40);
    topoGroupTouched = true;
    const changed = topoSelectedGroup !== next;
    if (persist && changed) {
      ctx.preferences?.patch?.("topology", { selected_group: next });
    }
    topoSelectedGroup = next;
    focusedItemKey = "";
    mapHoveredKey = "";
    mapZoom = { scale: 1, tx: 0, ty: 0 };
    if (changed) paint(true);
  };

  const setTopoFocusViz = (viz) => {
    const normalized = viz === "map" ? "map" : "flow";
    topoFocusVizTouched = true;
    const changed = topoFocusViz !== normalized;
    topoFocusViz = normalized;
    mapHoveredKey = "";
    if (changed) {
      ctx.preferences?.patch?.("topology", { focus_viz: normalized });
      paint(true);
    }
  };

  const setTopoFilter = (filter) => {
    if (!VALID_TOPO_FILTERS.has(filter)) return;
    topoFilterTouched = true;
    const changed = topoStatusFilter !== filter;
    topoStatusFilter = filter;
    if (changed) {
      mapPinnedKey = "";
      mapHoveredKey = "";
      flowInspectKey = "";
      ctx.preferences?.patch?.("topology", { status_filter: filter });
      paint(true);
    }
  };

  const setTopoSort = (sortBy) => {
    if (!VALID_TOPO_SORTS.has(sortBy)) return;
    topoSortTouched = true;
    const changed = topoSortBy !== sortBy;
    topoSortBy = sortBy;
    if (changed) {
      ctx.preferences?.patch?.("topology", { sort_by: sortBy });
      paint(true);
    }
  };

  const setTopoMetricDir = (dir) => {
    if (!VALID_TOPO_DIRS.has(dir)) return;
    topoDirTouched = true;
    const changed = topoMetricDir !== dir;
    topoMetricDir = dir;
    if (changed) {
      ctx.preferences?.patch?.("topology", { metric_dir: dir });
      paint(true);
    }
  };

  const statusNode = muted("读取中…");
  statusNode.setAttribute("role", "status");
  const headerBadges = node("div", { className: "a-topo-head-badges" });
  const headerActions = node("div", { className: "a-actions a-topo-head-actions" });
  const bodyHost = node("div", { className: "a-card-body a-topo-body" });

  listen(document, "keydown", (event) => {
    if (event.key !== "Escape") return;
    if (document.querySelector("dialog[open], .a-tooltip")) return;
    if (focusedItemKey || flowInspectKey || mapPinnedKey || mapHoveredKey) {
      focusedItemKey = "";
      flowInspectKey = "";
      mapPinnedKey = "";
      mapHoveredKey = "";
      paint(true);
    } else if (mapZoom.scale > 1.001) {
      mapZoom = { scale: 1, tx: 0, ty: 0 };
      paint(true);
    }
  });

  if (typeof ResizeObserver === "function") {
    let resizeFrame = 0;
    const observer = new ResizeObserver((entries) => {
      if (!canViewSurface() || !ctx.current()) return;
      const next = Math.round(entries[0]?.contentRect?.width || 0);
      if (next >= 220 && Math.abs(next - observedWidth) >= 8) {
        observedWidth = next;
        chartWidth = Math.max(240, Math.min(960, next - 36));
        cancelAnimationFrame(resizeFrame);
        resizeFrame = requestAnimationFrame(() => {
          resizeFrame = 0;
          if (canViewSurface() && ctx.current()) paint();
        });
      }
    });
    observer.observe(bodyHost);
    ctx.signal.addEventListener("abort", () => {
      cancelAnimationFrame(resizeFrame);
      observer.disconnect();
    }, { once: true });
  }

  const toggleFocus = (itemKey) => {
    focusedItemKey = focusedItemKey === itemKey ? "" : itemKey;
    paint(true);
  };

  const formatStatsLine = (dir) => {
    if (!dir || !dir.available) return "待采样";
    if (dir.state === "running" || dir.state === "preparing") return "探测中";
    if (dir.state === "failed" || dir.error) return dir.error || "探测失败";
    if (!dir.reached || (dir.lossPercent ?? 0) >= 100) return "不可达 · 100% 丢包";
    const parts = [`平均 ${formatMs(dir.avgMS)}`];
    if (dir.minMS != null && dir.maxMS != null) parts.push(`范围 ${dir.minMS.toFixed(1)}–${dir.maxMS.toFixed(1)} ms`);
    if (dir.stdevMS != null) parts.push(`抖动 ${dir.stdevMS.toFixed(1)} ms`);
    parts.push(`丢包 ${formatLoss(dir.lossPercent)}`);
    return parts.join(" · ");
  };

  const renderNodeCard = (group, ep, { activeItemKey = focusedItemKey, onSelect = null, keyScope = "chain" } = {}) => {
    const itemKey = `node:${ep.index}`;
    const isFocused = activeItemKey === itemKey;
    const isOfflineOrErr = Boolean(ep.issue);
    const dotTone = ep.kind === "ext" ? "ok" : isOfflineOrErr ? "bad" : ep.online ? "ok" : "bad";
    const flag = ep.kind === "ext" ? null : nodeRegionFlag(ctx, { id: ep.agentID, region_code: ep.region });

    return node("button", {
      type: "button",
      className: `a-topo-node-card${ep.kind === "ext" ? " is-ext" : ""}${isOfflineOrErr ? " is-issue" : ""}${isFocused ? " is-active" : ""}`,
      "data-refresh-key": `${keyScope}-node-${group.id}-${ep.index}`,
      "data-topo-node": ep.agentID || ep.address || String(ep.index),
      "aria-pressed": isFocused ? "true" : "false",
      title: `${ep.name}${ep.address ? ` (${ep.address})` : ""}${ep.issue ? ` · ${ep.issue}` : ""}`,
      onclick: () => (onSelect ? onSelect(itemKey) : toggleFocus(itemKey)),
    },
      node("div", { className: "a-topo-node-top" },
        node("span", { className: `a-topo-dot is-${dotTone}`, "aria-hidden": "true" }),
        flag,
        !flag && ep.region ? node("span", { className: "a-topo-region-tag" }, ep.region) : null,
        node("span", { className: "a-topo-role-tag" }, ep.role),
        ep.shared ? badge("共享", "") : null,
      ),
      node("strong", { className: "a-topo-node-name" }, ep.name),
      node("div", { className: "a-topo-node-sub" },
        ep.address ? node("span", { className: "a-topo-node-addr" }, ep.address) : null,
        ep.issue ? node("span", { className: "a-topo-node-issue" }, ep.issue) : null,
      ),
    );
  };

  const renderHopConnector = (group, hop, metricDir, { activeItemKey = focusedItemKey, onSelect = null, keyScope = "chain" } = {}) => {
    const itemKey = `hop:${hop.hopIndex}`;
    const isFocused = activeItemKey === itemKey;
    const showFwd = metricDir !== "reverse";
    const showRev = hop.bidirectional && metricDir !== "forward";

    const fwdLabel = formatDirBadgeMetric(hop.forward, {
      prefix: hop.bidirectional ? "去 " : "",
      running: group.running,
      ready: hop.ready,
    });
    const revLabel = hop.bidirectional
      ? formatDirBadgeMetric(hop.reverse, {
          prefix: "回 ",
          running: group.running,
          ready: hop.ready,
        })
      : (metricDir === "reverse" ? "单向（无回程）" : "");

    const lossText = group.running
      ? "测量中"
      : hop.worstLossPercent != null
        ? (hop.worstLossPercent > 0 ? `丢包 ${formatLoss(hop.worstLossPercent)}` : "0% 丢包")
        : (hop.unavailableReason || hop.statusLabel);

    const conduit = renderHopConduitSVG({
      forward: hop.forward,
      reverse: hop.reverse,
      bidirectional: hop.bidirectional,
      running: group.running,
      tone: hop.tone,
      metricDir,
      vertical: false,
    });
    const conduitVert = renderHopConduitSVG({
      forward: hop.forward,
      reverse: hop.reverse,
      bidirectional: hop.bidirectional,
      running: group.running,
      tone: hop.tone,
      metricDir,
      vertical: true,
    });

    return node("button", {
      type: "button",
      className: `a-topo-hop-slot${isFocused ? " is-active" : ""}`,
      "data-refresh-key": `${keyScope}-hop-${hop.id}`,
      "data-tone": hop.tone || "idle",
      "data-hop-index": String(hop.hopIndex),
      "aria-pressed": isFocused ? "true" : "false",
      title: `${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name} · ${lossText}`,
      onclick: () => (onSelect ? onSelect(itemKey) : toggleFocus(itemKey)),
    },
      node("div", { className: "a-topo-hop-rail" }, conduit, conduitVert),
      node("div", { className: "a-topo-hop-pill", "data-tone": hop.tone || "idle" },
        showFwd ? node("span", { className: "a-topo-hop-rtt" }, fwdLabel) : null,
        (showRev || metricDir === "reverse") ? node("span", { className: "a-topo-hop-rtt is-rev" }, revLabel) : null,
        node("span", { className: "a-topo-hop-loss" }, lossText),
      ),
    );
  };

  const renderSingleChainVisual = (group, metricDir, opts = {}) => {
    const scope = opts.keyScope || "chain";
    const steps = group.nodes.map((ep, idx) => {
      const hop = idx < group.hops.length ? group.hops[idx] : null;
      return node("div", {
        className: `a-topo-chain-step${hop ? "" : " is-terminal"}`,
        "data-refresh-key": `${scope}-step-${group.id}-${ep.index}`,
      },
        keyed(`slot-node-${ep.index}`, renderNodeCard(group, ep, opts)),
        hop ? keyed(`slot-hop-${hop.hopIndex}`, renderHopConnector(group, hop, metricDir, opts)) : null,
      );
    });
    return node("div", {
      className: `a-topo-chain-stage${group.isMultiHop ? " is-multihop" : ""}`,
      "data-refresh-key": `${scope}-stage-${group.id}`,
      "data-node-count": String(group.nodes.length),
    }, ...steps);
  };

  const renderInspectStrip = (group, metricDir, { activeItemKey = focusedItemKey, onClear = null, extraActions = [] } = {}) => {
    const clearBtn = activeItemKey
      ? button("取消聚焦", () => (onClear ? onClear() : toggleFocus(activeItemKey)), {
          className: "a-button a-topo-mini-btn",
          "data-refresh-key": "topo-unfocus",
        })
      : null;

    if (activeItemKey.startsWith("node:")) {
      const idx = Number(activeItemKey.slice(5));
      const ep = group.nodes[idx];
      if (ep) {
        const inHop = idx > 0 ? group.hops[idx - 1] : null;
        const outHop = idx < group.hops.length ? group.hops[idx] : null;
        return node("div", { className: "a-topo-inspect", "data-refresh-key": "topo-inspect" },
          node("div", { className: "a-topo-inspect-main" },
            badge(ep.role, ep.issue ? "bad" : ep.online ? "ok" : "bad"),
            node("strong", {}, ep.name),
            ep.address ? node("code", { className: "a-dash-topo-code" }, ep.address) : null,
            ep.shared ? badge("共享实例", "") : null,
            ep.issue ? badge(ep.issue, "bad") : null,
          ),
          node("div", { className: "a-topo-inspect-detail" },
            inHop ? muted(`入站 (${inHop.from.name}): ${formatDirBadgeMetric(inHop.forward, { running: group.running, ready: inHop.ready })}`) : null,
            outHop ? muted(`出站 (${outHop.to.name}): ${formatDirBadgeMetric(outHop.forward, { running: group.running, ready: outHop.ready })}`) : null,
            ...extraActions,
            clearBtn,
          ),
        );
      }
    }

    if (activeItemKey.startsWith("hop:")) {
      const idx = Number(activeItemKey.slice(4));
      const hop = group.hops[idx];
      if (hop) {
        return node("div", { className: "a-topo-inspect", "data-refresh-key": "topo-inspect" },
          node("div", { className: "a-topo-inspect-main" },
            badge(`跳段 ${idx + 1}/${group.hopCount}`, hop.tone || ""),
            node("strong", {}, `${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name}`),
            hop.unavailableReason ? badge(hop.unavailableReason, "warn") : null,
          ),
          node("div", { className: "a-topo-inspect-detail" },
            metricDir !== "reverse" ? muted(`去程: ${formatStatsLine(hop.forward)}`) : null,
            hop.bidirectional && metricDir !== "forward" ? muted(`回程: ${formatStatsLine(hop.reverse)}`) : null,
            !hop.bidirectional && metricDir === "reverse" ? muted("该跳段仅配置单向测量") : null,
            ...extraActions,
            clearBtn,
          ),
        );
      }
    }

    const sampledText = group.overall.sampledAt ? `采样 ${date(group.overall.sampledAt)}` : "尚未采样";
    return node("div", { className: "a-topo-inspect", "data-refresh-key": "topo-inspect" },
      node("div", { className: "a-topo-inspect-main" },
        badge(group.overall.statusBadge.label, group.overall.statusBadge.tone),
        node("strong", {}, group.isMultiHop ? "全链路聚合指标" : "链路指标"),
        group.overall.error ? node("span", { className: "a-topo-err-inline" }, group.overall.error) : null,
      ),
      node("div", { className: "a-topo-inspect-detail" },
        metricDir !== "reverse" ? muted(`去程: ${formatStatsLine(group.overall.forward)}`) : null,
        group.bidirectional && metricDir !== "forward" ? muted(`回程: ${formatStatsLine(group.overall.reverse)}`) : null,
        !group.bidirectional && metricDir === "reverse" ? muted("当前链路为单向探测") : null,
        muted(sampledText),
        ...extraActions,
      ),
    );
  };

  // ==================== Pacific Map Integration & Live Highlighting ====================

  const resolveSingleFocusKeyToMapKey = (group, focusKey, model) => {
    if (!group || !focusKey) return "";
    if (focusKey.startsWith("node:")) {
      const idx = Number(focusKey.slice(5));
      const ep = group.nodes[idx];
      if (!ep) return "";
      if (ep.kind === "ext") return topoEntityKey.external(ep.address);
      const clusters = clusterTopologyNodes(model?.nodes || []);
      const hitCluster = clusters.find((cl) => cl.isCluster && cl.members.some((m) => m.node.id === ep.agentID));
      return hitCluster ? hitCluster.key : topoEntityKey.node(ep.agentID);
    }
    if (focusKey.startsWith("hop:")) {
      const idx = Number(focusKey.slice(4));
      const hop = group.hops[idx];
      return hop ? topoEntityKey.probe(hop.id) : "";
    }
    return "";
  };

  const applyMapEntityToSingleFocus = (group, entityKey) => {
    if (!group || !entityKey) {
      focusedItemKey = "";
      paint(true);
      return;
    }
    if (entityKey.startsWith("probe:")) {
      const hopID = entityKey.slice(6);
      const hop = group.hops.find((h) => h.id === hopID);
      if (hop) {
        const nextKey = `hop:${hop.hopIndex}`;
        focusedItemKey = focusedItemKey === nextKey ? "" : nextKey;
        paint(true);
      }
      return;
    }
    if (entityKey.startsWith("ext:")) {
      const addr = entityKey.slice(4);
      const epIdx = group.nodes.findIndex((ep) => ep.kind === "ext" && ep.address === addr);
      if (epIdx >= 0) {
        const nextKey = `node:${epIdx}`;
        focusedItemKey = focusedItemKey === nextKey ? "" : nextKey;
        paint(true);
      }
      return;
    }
    if (entityKey.startsWith("node:") || entityKey.startsWith("cluster:")) {
      const ids = entityKey.startsWith("cluster:") ? entityKey.slice(8).split(",").filter(Boolean) : [entityKey.slice(5)];
      const epIdx = group.nodes.findIndex((ep) => ep.kind !== "ext" && ids.includes(ep.agentID));
      if (epIdx >= 0) {
        const nextKey = `node:${epIdx}`;
        focusedItemKey = focusedItemKey === nextKey ? "" : nextKey;
        paint(true);
      }
    }
  };

  const renderFleetMapInspect = (model) => {
    const activeSel = mapHoveredKey || mapPinnedKey;
    const clearBtn = mapPinnedKey
      ? button("取消聚焦", () => {
          mapPinnedKey = "";
          mapHoveredKey = "";
          syncLiveMapSelection();
        }, { className: "a-button a-topo-mini-btn", "data-refresh-key": "map-unpin" })
      : null;

    if (activeSel?.startsWith("probe:")) {
      const hopID = activeSel.slice(6);
      const hop = (model?.probeLinks || []).find((p) => p.id === hopID);
      if (hop) {
        const ownerGroup = lastGroups.find((g) => g.id === hop.probeID || g.groupName === hop.groupName) || null;
        return node("div", { className: "a-topo-inspect", "data-refresh-key": "topo-map-inspect" },
          node("div", { className: "a-topo-inspect-main" },
            badge(hop.groupName, hop.tone || ""),
            hop.isMultiHop ? badge(`跳段 ${hop.hopIndex + 1}/${hop.hopCount}`, "") : null,
            node("strong", {}, `${hop.fromName} ${hop.bidirectional ? "⇄" : "→"} ${hop.toName}`),
            hop.unavailableReason ? badge(hop.unavailableReason, "warn") : null,
          ),
          node("div", { className: "a-topo-inspect-detail" },
            muted(hop.metricLabel || hop.statusLabel),
            ownerGroup ? button("聚焦分析", () => setTopoViewMode("focus", { focusGroupName: ownerGroup.groupName, focusItem: `hop:${hop.hopIndex}` }), {
              className: "a-button a-topo-mini-btn",
              "data-refresh-key": "map-focus-group",
            }) : null,
            ownerGroup && canExecuteProbes() ? button("编辑", () => probeController.openEdit(ownerGroup.raw), {
              className: "a-button a-topo-mini-btn",
              "data-refresh-key": "map-edit-group",
            }) : null,
            clearBtn,
          ),
        );
      }
    }

    if (activeSel?.startsWith("ext:")) {
      const ext = (model?.externalTargets || []).find((e) => e.id === activeSel);
      if (ext) {
        const relGroups = (ext.groupNames || []).slice(0, 3);
        return node("div", { className: "a-topo-inspect", "data-refresh-key": "topo-map-inspect" },
          node("div", { className: "a-topo-inspect-main" },
            badge("公网目标", ""),
            node("strong", {}, ext.name || ext.address),
            ext.address && ext.address !== ext.name ? node("code", { className: "a-dash-topo-code" }, ext.address) : null,
          ),
          node("div", { className: "a-topo-inspect-detail" },
            muted(`关联 ${ext.linkCount || relGroups.length} 条链路`),
            ...relGroups.map((gName) => button(gName, () => setTopoViewMode("focus", { focusGroupName: gName }), {
              className: "a-button a-topo-mini-btn",
              "data-refresh-key": `map-rel-${gName}`,
            })),
            clearBtn,
          ),
        );
      }
    }

    if (activeSel?.startsWith("node:") || activeSel?.startsWith("cluster:")) {
      const ids = activeSel.startsWith("cluster:") ? activeSel.slice(8).split(",").filter(Boolean) : [activeSel.slice(5)];
      const matchedNodes = ids.map((id) => (model?.nodes || []).find((n) => n.id === id)).filter(Boolean);
      if (matchedNodes.length) {
        const first = matchedNodes[0];
        const onlineCount = matchedNodes.filter((n) => n.isOnline).length;
        const groupNames = [...new Set(matchedNodes.flatMap((n) => n.groupNames || []))];
        const title = matchedNodes.length > 1
          ? `同城节点聚合 (${matchedNodes.map((n) => n.name).join(" / ")})`
          : first.name;
        return node("div", { className: "a-topo-inspect", "data-refresh-key": "topo-map-inspect" },
          node("div", { className: "a-topo-inspect-main" },
            badge(onlineCount === matchedNodes.length ? "在线" : onlineCount > 0 ? "部分离线" : "离线", onlineCount === matchedNodes.length ? "ok" : onlineCount > 0 ? "warn" : "bad"),
            node("strong", {}, title),
            matchedNodes.length === 1 && first.ip ? node("code", { className: "a-dash-topo-code" }, first.ip) : null,
            matchedNodes.length === 1 && first.issue ? badge(first.issue, "bad") : null,
          ),
          node("div", { className: "a-topo-inspect-detail" },
            muted(`参与 ${groupNames.length} 个监测分组`),
            ...groupNames.slice(0, 3).map((gName) => button(gName, () => setTopoViewMode("focus", { focusGroupName: gName }), {
              className: "a-button a-topo-mini-btn",
              "data-refresh-key": `map-node-grp-${gName}`,
            })),
            clearBtn,
          ),
        );
      }
    }

    const stats = model?.stats || { total: 0, hopCount: 0, alertCount: 0, avgFwdMS: null, avgRevMS: null };
    return node("div", { className: "a-topo-inspect", "data-refresh-key": "topo-map-inspect" },
      node("div", { className: "a-topo-inspect-main" },
        badge(stats.alertCount > 0 ? `告警 ${stats.alertCount}` : "运行正常", stats.alertCount > 0 ? "bad" : "ok"),
        node("strong", {}, "太平洋网络延迟全景"),
      ),
      node("div", { className: "a-topo-inspect-detail" },
        muted(`${model?.onlineNodes || 0}/${model?.totalNodes || 0} 节点在线`),
        muted(`${stats.total} 监测组 · ${stats.hopCount} 跳段`),
        stats.avgFwdMS != null ? muted(`去程均值 ${formatMs(stats.avgFwdMS)}`) : null,
        stats.avgRevMS != null ? muted(`回程均值 ${formatMs(stats.avgRevMS)}`) : null,
        muted("点击节点或连线查看详情"),
      ),
    );
  };

  const syncLiveMapSelection = () => {
    const svg = bodyHost.querySelector("svg.a-dash-topo-map-svg");
    if (!svg) return;
    const isSingle = Boolean(svg.closest(".a-topo-single-stage"));
    const activeSel = isSingle
      ? (mapHoveredKey || resolveSingleFocusKeyToMapKey(lastActiveGroup, focusedItemKey, lastSingleMapModel))
      : (mapHoveredKey || mapPinnedKey);
    const pinnedSel = isSingle
      ? resolveSingleFocusKeyToMapKey(lastActiveGroup, focusedItemKey, lastSingleMapModel)
      : mapPinnedKey;

    svg.querySelectorAll("[data-topo-peers]").forEach((el) => {
      const peers = (el.getAttribute("data-topo-peers") || "").split(" ").filter(Boolean);
      const match = matchesActiveSelection(peers, activeSel);
      const base = el.getAttribute("data-base-opacity") || "1";
      const dim = el.getAttribute("data-dim-opacity") ?? "0.14";
      const next = match === true ? "1" : match === false ? dim : base;
      if (el.getAttribute("opacity") !== next) el.setAttribute("opacity", next);
      el.classList.toggle("is-highlighted", match === true);
    });
    svg.querySelectorAll("[data-topo-entity]").forEach((el) => {
      const key = el.getAttribute("data-topo-entity") || "";
      const active = pinnedSel === key;
      el.setAttribute("aria-pressed", active ? "true" : "false");
      el.classList.toggle("is-active", active);
      el.classList.toggle("is-hovered", Boolean(mapHoveredKey && mapHoveredKey === key));
    });

    if (!isSingle && lastFleetModel) {
      const oldInspect = bodyHost.querySelector('[data-refresh-key="topo-map-inspect"]');
      if (oldInspect) patch(oldInspect, renderFleetMapInspect(lastFleetModel));
    }
  };

  const createMapHelpers = ({ singleGroup = null, model = null, clipId = "a-topo-map-clip", defsPrefix = "a-topo" } = {}) => {
    const isSingle = Boolean(singleGroup);
    const activeKey = () => (
      isSingle
        ? (mapHoveredKey || resolveSingleFocusKeyToMapKey(singleGroup, focusedItemKey, model))
        : (mapHoveredKey || mapPinnedKey)
    );
    const pinnedOnlyKey = () => (
      isSingle
        ? resolveSingleFocusKeyToMapKey(singleGroup, focusedItemKey, model)
        : mapPinnedKey
    );
    const isHighlightedEdge = (peers) => matchesActiveSelection(peers, activeKey());

    const bindInteractive = (el, key) => {
      el.setAttribute("tabindex", "0");
      el.setAttribute("role", "button");
      el.setAttribute("data-topo-entity", key);
      el.setAttribute("aria-pressed", pinnedOnlyKey() === key ? "true" : "false");

      listen(el, "pointerenter", () => {
        if (mapHoveredKey !== key) {
          mapHoveredKey = key;
          syncLiveMapSelection();
        }
      });
      listen(el, "pointerleave", () => {
        if (mapHoveredKey === key) {
          mapHoveredKey = "";
          syncLiveMapSelection();
        }
      });
      const activate = (event) => {
        if (event.type === "keydown" && event.key !== "Enter" && event.key !== " ") return;
        if (event.type === "keydown") event.preventDefault();
        event.stopPropagation();
        mapHoveredKey = "";
        if (isSingle) {
          applyMapEntityToSingleFocus(singleGroup, key);
        } else {
          mapPinnedKey = mapPinnedKey === key ? "" : key;
          syncLiveMapSelection();
        }
      };
      listen(el, "click", activate);
      listen(el, "keydown", activate);
    };

    const appendProbeLeader = (layer, placed, pal, peers, hl, compact = false, focusOnly = false, probeID = "") => {
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
          ...(probeID ? { "data-refresh-key": `probe-leader-line-${probeID}` } : {}),
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
          ...(probeID ? { "data-refresh-key": `probe-leader-dot-${probeID}` } : {}),
          cx: placed.anchorX.toFixed(1),
          cy: placed.anchorY.toFixed(1),
          r: compact ? 1.9 : 2.3,
          fill: pal.stroke,
          ...attrs,
        }),
      );
    };

    const createProbePillGroup = ({ probe, cx, cy, pillW, pillH, label, pal, compact = false, peers, focusOnly = false }) => {
      const eKey = topoEntityKey.probe(probe.id);
      const hl = isHighlightedEdge(peers);
      const selected = pinnedOnlyKey() === eKey;
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
      const tipText = probe.groupName
        ? `${probe.groupName} · ${probe.fromName} ${probe.bidirectional ? "⇄" : "→"} ${probe.toName}: ${probe.metricLabel}`
        : `${probe.name}: ${probe.metricLabel}`;
      g.append(createSVGEl("title", {}, tipText));
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
        g.append(createSVGEl("circle", {
          class: "a-dash-topo-pill-dot",
          cx: dotX.toFixed(1),
          cy: cy.toFixed(1),
          r: compact ? 2.5 : 3,
          fill: pal.stroke,
        }));
      }
      g.append(createSVGEl("text", {
        x: (dotX + (compact ? 6.5 : 7.5)).toFixed(1),
        y: (cy + (compact ? 3.2 : 3.6)).toFixed(1),
        class: "a-dash-topo-pill-text",
        fill: pal.ink,
      }, label));
      bindInteractive(g, eKey);
      return g;
    };

    return {
      clipId,
      defsPrefix,
      allowMotion,
      activeKey,
      isHighlightedEdge,
      bindInteractive,
      createProbePillGroup,
      appendProbeLeader,
    };
  };

  const buildMapStageElement = ({ model, singleGroup = null, scopeKey = "map", isMobile = false, width = 880 } = {}) => {
    const clipId = `a-topo-clip-${scopeKey}`;
    const defsPrefix = `a-topo-${scopeKey}`;
    const svg = renderPacificMapSVG({
      width,
      isMobile,
      model,
      zoomTransform: isMobile ? null : mapZoom,
      helpers: createMapHelpers({ singleGroup, model, clipId, defsPrefix }),
    });
    svg.setAttribute("data-refresh-key", `map-svg-${scopeKey}`);

    const stage = node("div", {
      className: "a-dash-topo-map-stage",
      "data-topo-mode": "map",
      "data-refresh-key": `map-stage-${scopeKey}`,
    }, svg);

    listen(svg, "pointerleave", () => {
      if (mapHoveredKey) {
        mapHoveredKey = "";
        syncLiveMapSelection();
      }
    });
    listen(svg, "click", (event) => {
      if (suppressNextMapClick) {
        suppressNextMapClick = false;
        return;
      }
      if (event.target.closest?.("[data-topo-entity], .a-dash-topo-map-tools")) return;
      mapHoveredKey = "";
      if (singleGroup) {
        if (focusedItemKey) {
          focusedItemKey = "";
          paint(true);
        }
      } else if (mapPinnedKey) {
        mapPinnedKey = "";
        syncLiveMapSelection();
      }
    });

    if (!isMobile) {
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

      const hasSelection = Boolean(singleGroup ? focusedItemKey : mapPinnedKey);
      const pctLabel = `${Math.round(mapZoom.scale * 100)}%`;
      const toolsBar = node("div", {
        className: "a-dash-topo-map-tools",
        role: "group",
        "aria-label": "地图缩放控制",
        "data-refresh-key": `map-tools-${scopeKey}`,
      },
        button("−", () => zoomBy(1 / 1.35), {
          className: "a-button",
          title: "缩小地图",
          disabled: mapZoom.scale <= 1.01,
          "data-refresh-key": "map-zoom-out",
        }),
        button(mapZoom.scale > 1.01 ? `重置 (${pctLabel})` : "100%", () => {
          mapHoveredKey = "";
          mapZoom = { scale: 1, tx: 0, ty: 0 };
          if (singleGroup) focusedItemKey = "";
          else mapPinnedKey = "";
          paint(true);
        }, {
          className: "a-button",
          title: "重置地图视角",
          disabled: mapZoom.scale <= 1.01 && !hasSelection,
          "data-refresh-key": "map-zoom-reset",
        }),
        button("+", () => zoomBy(1.35), {
          className: "a-button",
          title: "放大地图",
          disabled: mapZoom.scale >= 3.18,
          "data-refresh-key": "map-zoom-in",
        }),
      );
      stage.append(toolsBar);

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
          suppressNextMapClick = true;
          paint(true);
        }
      });
      listen(svg, "dblclick", (e) => {
        if (e.target.closest?.("[data-topo-entity]")) return;
        const pt = clientToSvgPoint(e.clientX, e.clientY);
        if (mapZoom.scale >= 2.5) {
          mapZoom = { scale: 1, tx: 0, ty: 0 };
          paint(true);
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
    }

    return node("div", {
      className: "a-topo-map-wrap",
      "data-refresh-key": `map-wrap-${scopeKey}`,
    }, stage);
  };

  // ==================== Group Controls & Workspaces ====================

  const renderGroupActionButtons = (group, { includeFocusBtn = false, includeDelete = false } = {}) => {
    const items = [];
    if (includeFocusBtn) {
      items.push(button("聚焦分析", () => setTopoViewMode("focus", { focusGroupName: group.groupName }), {
        className: "a-button a-topo-mini-btn",
        "data-refresh-key": `focus-${group.id}`,
      }));
    }
    if (canExecuteProbes()) {
      items.push(
        group.running
          ? button("停止", (e) => void probeController.mutatePair(group.raw, e.currentTarget, "stop"), {
              className: "a-button a-topo-mini-btn",
              "data-refresh-key": `stop-${group.id}`,
            })
          : button("立即探测", (e) => void probeController.mutatePair(group.raw, e.currentTarget, "run"), {
              className: "a-button a-topo-mini-btn",
              disabled: !group.availability.ready,
              title: group.availability.reason || "立即发起一次手动探测",
              "data-refresh-key": `run-${group.id}`,
            }),
        button(group.autoEnabled ? "暂停自动" : "开启自动", (e) => void probeController.mutatePair(group.raw, e.currentTarget, "toggle"), {
          className: "a-button a-topo-mini-btn",
          "data-refresh-key": `toggle-${group.id}`,
        }),
        button("编辑", () => probeController.openEdit(group.raw), {
          className: "a-button a-topo-mini-btn",
          "data-refresh-key": `edit-${group.id}`,
        }),
      );
      if (includeDelete) {
        items.push(button("删除", (e) => void probeController.mutatePair(group.raw, e.currentTarget, "delete"), {
          className: "a-button a-topo-mini-btn",
          tone: "danger",
          "data-refresh-key": `delete-${group.id}`,
        }));
      }
    }
    return items;
  };

  const renderHistorySection = (group, metricDir, gradPrefix) => {
    const chart = renderProbeHistoryChart(group.history, {
      bidirectional: group.bidirectional,
      metricDir,
      width: chartWidth,
      height: isStandalonePage ? 88 : 66,
      gradKey: `${gradPrefix}-${group.id}`,
    });
    if (!chart) {
      return node("div", { className: "a-topo-history-empty", "data-refresh-key": `history-empty-${group.id}` },
        muted(group.history.length ? "已有 1 次采样记录，完成第 2 次采样后显示延迟走势" : "暂无历史采样趋势"),
      );
    }
    const legend = group.bidirectional && metricDir === "both"
      ? "实线 去程 · 虚线 回程 · 红点 丢包/不可达"
      : `近 ${chart.sampleCount} 次采样 · 峰值 ${formatMs(chart.peakMS)}`;
    return node("div", { className: "a-topo-history-box", "data-refresh-key": `history-box-${group.id}` },
      keyed("svg", chart.svg),
      node("div", { className: "a-chart-meta" },
        muted(legend),
        muted(group.overall.sampledAt ? `最新 ${date(group.overall.sampledAt)}` : ""),
      ),
    );
  };

  const renderGroupSwitcher = (groups, activeGroup, onSelect, keyPrefix, metricDir = "both") => {
    if (!groups.length) return null;
    return node("div", {
      className: "a-topo-switcher",
      role: "tablist",
      "aria-label": "监测分组切换",
      "data-refresh-key": `${keyPrefix}-switcher`,
    },
      ...groups.map((g) => {
        const active = Boolean(activeGroup && g.id === activeGroup.id);
        const metricText = metricDir === "reverse"
          ? (g.bidirectional
              ? formatDirBadgeMetric(g.overall.reverse, { running: g.running, ready: g.availability.ready })
              : "单向")
          : formatDirBadgeMetric(
              g.overall.forward,
              { running: g.running, ready: g.availability.ready },
            );
        return button(
          [
            node("span", { className: `a-topo-dot is-${g.overall.statusBadge.tone || "idle"}`, "aria-hidden": "true" }),
            node("span", { className: "a-topo-pill-name" }, g.groupName),
            node("span", { className: "a-topo-pill-metric" }, metricText),
          ],
          () => onSelect(g.groupName),
          {
            className: `a-topo-group-pill${active ? " is-active" : ""}`,
            role: "tab",
            "aria-selected": active ? "true" : "false",
            "data-group-name": g.groupName,
            "data-refresh-key": `${keyPrefix}-pill-${g.id}`,
            title: `${g.groupName} (${g.routeText})`,
          },
        );
      }),
    );
  };

  const renderVizModeSwitch = (currentViz, onChange, { attrName, keyPrefix, labels = ["链路图", "地理图"] } = {}) => node("div", {
    className: "a-dash-topo-mode-switch",
    role: "group",
    "aria-label": "可视化呈现方式",
    "data-refresh-key": `${keyPrefix}-viz-switch`,
  },
    button(labels[0], () => onChange("flow"), {
      className: `a-dash-topo-mode-btn${currentViz === "flow" ? " is-active" : ""}`,
      "aria-pressed": currentViz === "flow" ? "true" : "false",
      [attrName]: "flow",
      "data-refresh-key": `${keyPrefix}-viz-flow`,
    }),
    button(labels[1], () => onChange("map"), {
      className: `a-dash-topo-mode-btn${currentViz === "map" ? " is-active" : ""}`,
      "aria-pressed": currentViz === "map" ? "true" : "false",
      [attrName]: "map",
      "data-refresh-key": `${keyPrefix}-viz-map`,
    }),
  );

  const renderGroupHeaderBar = (group, {
    includeFocusBtn = false,
    includeDelete = false,
    vizSwitchEl = null,
  } = {}) => {
    const scheduleLabel = group.autoEnabled ? `自动 ${group.autoIntervalSeconds}s` : "已暂停";
    const metaTags = [
      badge(group.overall.statusBadge.label, group.overall.statusBadge.tone),
      badge(groupKindBadgeText(group), ""),
      badge(group.bidirectional ? "去/回双向" : "单向", ""),
      badge(scheduleLabel, group.autoEnabled ? "" : "warn"),
    ];
    return node("div", { className: "a-topo-single-head", "data-refresh-key": `head-${group.id}` },
      node("div", { className: "a-topo-single-title" },
        node("div", { className: "a-topo-single-name-row" },
          node("h3", {}, group.groupName),
          ...metaTags,
        ),
        group.remark ? node("p", { className: "a-muted a-topo-remark" }, group.remark) : null,
      ),
      node("div", { className: "a-actions a-topo-single-ops" },
        vizSwitchEl,
        ...renderGroupActionButtons(group, { includeFocusBtn, includeDelete }),
      ),
    );
  };

  const renderSingleGroupWorkspace = (group, {
    metricDir = "both",
    vizMode = "flow",
    vizSwitchEl = null,
    showHistory = false,
    includeDelete = false,
    gradPrefix = "dash",
    renderWidth = 880,
    isMobile = false,
  } = {}) => {
    const availNotice = !group.availability.ready && group.availability.reason
      ? node("p", { className: "a-notice warn", role: "status", "data-refresh-key": `avail-notice-${group.id}` }, group.availability.reason)
      : null;

    let stageBody;
    if (vizMode === "map") {
      const singleModel = buildSingleGroupTopologyModel(group, { metricDir });
      lastSingleMapModel = singleModel;
      stageBody = buildMapStageElement({
        model: singleModel,
        singleGroup: group,
        scopeKey: `${gradPrefix}-single`,
        isMobile,
        width: renderWidth,
      });
    } else {
      lastSingleMapModel = null;
      stageBody = renderSingleChainVisual(group, metricDir);
    }

    return node("div", {
      className: `a-topo-single-stage is-tone-${group.overall.statusBadge.tone || "idle"}`,
      "data-refresh-key": `single-stage-${group.id}`,
      "data-group-name": group.groupName,
    },
      renderGroupHeaderBar(group, { includeFocusBtn: false, includeDelete, vizSwitchEl }),
      availNotice,
      stageBody,
      renderInspectStrip(group, metricDir),
      showHistory ? renderHistorySection(group, metricDir, gradPrefix) : null,
    );
  };

  const renderOverviewCard = (group) => {
    const tone = group.overall.statusBadge.tone || "idle";
    const metricLine = summarizeGroupMetricLine(group, topoMetricDir);
    const sampledText = group.overall.sampledAt ? date(group.overall.sampledAt) : "待采样";

    const miniChainItems = [];
    group.nodes.forEach((ep, idx) => {
      miniChainItems.push(node("span", {
        className: `a-topo-mini-node${ep.issue ? " is-issue" : ""}`,
        title: `${ep.name}${ep.address ? ` (${ep.address})` : ""}${ep.issue ? ` · ${ep.issue}` : ""}`,
      },
        node("span", { className: `a-topo-dot is-${ep.issue ? "bad" : ep.online ? "ok" : "bad"}`, "aria-hidden": "true" }),
        ep.name,
      ));
      if (idx < group.hops.length) {
        const hop = group.hops[idx];
        const hopMetric = topoMetricDir === "reverse"
          ? (hop.bidirectional ? formatDirBadgeMetric(hop.reverse, { running: group.running, ready: hop.ready }) : "单向")
          : topoMetricDir === "forward" || !hop.bidirectional
            ? formatDirBadgeMetric(hop.forward, { running: group.running, ready: hop.ready })
            : `${formatMsCompact(hop.forward.avgMS)}${hop.reverse?.avgMS != null ? `/${formatMsCompact(hop.reverse.avgMS)}` : ""}${hop.worstLossPercent > 0 ? ` · ${formatLoss(hop.worstLossPercent)}` : ""}`;
        miniChainItems.push(button(
          hopMetric || hop.statusLabel,
          () => setTopoViewMode("focus", { focusGroupName: group.groupName, focusItem: `hop:${hop.hopIndex}` }),
          {
            className: "a-topo-mini-hop",
            "data-tone": hop.tone || "idle",
            "data-refresh-key": `mini-hop-${hop.id}`,
            title: `${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name} · 点击聚焦分析`,
          },
        ));
      }
    });

    return node("article", {
      className: "a-card a-topo-overview-card",
      "data-refresh-key": `overview-card-${group.id}`,
      "data-group-name": group.groupName,
      "data-tone": tone,
    },
      node("div", { className: "a-topo-overview-head" },
        node("div", { className: "a-topo-overview-title" },
          node("div", { className: "a-topo-single-name-row" },
            node("h3", {}, group.groupName),
            badge(group.overall.statusBadge.label, group.overall.statusBadge.tone),
            badge(groupKindBadgeText(group), ""),
          ),
          group.remark ? node("p", { className: "a-muted a-topo-remark" }, group.remark) : null,
        ),
        node("div", { className: "a-topo-overview-kpi" },
          node("strong", { className: `a-topo-overview-metric is-${tone}` }, metricLine),
          muted(`${group.autoEnabled ? `自动 ${group.autoIntervalSeconds}s` : "已暂停"} · ${sampledText}`),
        ),
      ),
      node("div", { className: "a-topo-mini-chain" }, ...miniChainItems),
      (!group.availability.ready && group.availability.reason) || group.overall.error
        ? node("p", { className: "a-topo-overview-alert" }, group.availability.reason || group.overall.error)
        : null,
      node("div", { className: "a-topo-overview-foot a-actions" },
        ...renderGroupActionButtons(group, { includeFocusBtn: true, includeDelete: true }),
      ),
    );
  };

  const renderFlowComparisonCard = (group) => {
    const tone = group.overall.statusBadge.tone || "idle";
    const prefix = `${group.id}::`;
    const activeItemKey = flowInspectKey.startsWith(prefix) ? flowInspectKey.slice(prefix.length) : "";
    const selectItem = (itemKey) => {
      const fullKey = `${prefix}${itemKey}`;
      flowInspectKey = flowInspectKey === fullKey ? "" : fullKey;
      paint(true);
    };
    const clearItem = () => {
      flowInspectKey = "";
      paint(true);
    };

    return node("article", {
      className: "a-card a-topo-flow-group-card",
      "data-refresh-key": `flow-group-card-${group.id}`,
      "data-group-name": group.groupName,
      "data-tone": tone,
    },
      renderGroupHeaderBar(group, { includeFocusBtn: true, includeDelete: true }),
      (!group.availability.ready && group.availability.reason) || group.overall.error
        ? node("p", { className: "a-topo-overview-alert" }, group.availability.reason || group.overall.error)
        : null,
      renderSingleChainVisual(group, topoMetricDir, {
        activeItemKey,
        onSelect: selectItem,
        keyScope: "flowlist",
      }),
      activeItemKey
        ? renderInspectStrip(group, topoMetricDir, {
            activeItemKey,
            onClear: clearItem,
            extraActions: [
              button("进入聚焦分析", () => setTopoViewMode("focus", { focusGroupName: group.groupName, focusItem: activeItemKey }), {
                className: "a-button a-topo-mini-btn",
                "data-refresh-key": `flow-deep-${group.id}`,
              }),
            ],
          })
        : null,
    );
  };

  const renderFocusDetails = (group) => {
    const dirCard = (title, dir, unavailableMsg = "") => {
      if (unavailableMsg) {
        return node("div", { className: "a-topo-dir-card", "data-refresh-key": `dir-${title}` },
          node("div", { className: "a-topo-dir-head" }, node("strong", {}, title), badge("未启用", "")),
          muted(unavailableMsg),
        );
      }
      return node("div", { className: "a-topo-dir-card", "data-refresh-key": `dir-${title}` },
        node("div", { className: "a-topo-dir-head" },
          node("strong", {}, title),
          badge(dir.status.label, dir.status.tone),
        ),
        node("dl", { className: "a-dash-probe-stats" },
          node("div", {}, node("dt", {}, "平均延迟"), node("dd", { className: "is-primary" }, formatMs(dir.avgMS))),
          node("div", {}, node("dt", {}, "最小 / 最大"), node("dd", {}, dir.minMS != null && dir.maxMS != null ? `${dir.minMS.toFixed(1)} / ${dir.maxMS.toFixed(1)} ms` : "—")),
          node("div", {}, node("dt", {}, "抖动"), node("dd", {}, formatMs(dir.stdevMS))),
          node("div", {}, node("dt", {}, "丢包率"), node("dd", { className: dir.lossPercent > 0 ? (dir.lossPercent >= 20 || !dir.reached ? "is-bad" : "is-warn") : "" }, formatLoss(dir.lossPercent))),
        ),
        dir.error ? node("p", { className: "a-topo-err-inline" }, dir.error) : null,
      );
    };

    const dirCards = [];
    if (topoMetricDir !== "reverse") {
      dirCards.push(dirCard("去程综合指标", group.overall.forward));
    }
    if (topoMetricDir !== "forward") {
      dirCards.push(
        group.bidirectional
          ? dirCard("回程综合指标", group.overall.reverse)
          : dirCard("回程综合指标", null, group.isExternal ? "自定义目标 IP 模式仅支持单向探测" : "该链路未启用双向测量"),
      );
    }

    const hopRows = group.hops.map((hop) => [
      `第 ${hop.hopIndex + 1} 跳`,
      `${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name}`,
      `${hop.from.address || "—"} → ${hop.to.address || "—"}`,
      formatDirBadgeMetric(hop.forward, { running: group.running, ready: hop.ready }),
      hop.bidirectional ? formatDirBadgeMetric(hop.reverse, { running: group.running, ready: hop.ready }) : "—",
      badge(hop.unavailableReason || hop.statusLabel, hop.tone || ""),
    ]);

    const historyEntries = [...group.history]
      .sort((a, b) => Date.parse(b.sampled_at) - Date.parse(a.sampled_at))
      .map((sample, idx) => {
        const fwd = sample.forward
          ? (sample.forward.reached ? `${formatMsCompact(sample.forward.avg_ms)} (${formatLoss(sample.forward.loss_percent)})` : (sample.forward.error || "不可达"))
          : "—";
        const rev = group.bidirectional
          ? (sample.reverse ? (sample.reverse.reached ? `${formatMsCompact(sample.reverse.avg_ms)} (${formatLoss(sample.reverse.loss_percent)})` : (sample.reverse.error || "不可达")) : "—")
          : "—";
        const tone = sample.state === "succeeded" && sample.forward?.reached && (!group.bidirectional || sample.reverse?.reached) ? "ok" : "bad";
        return {
          key: String(sample.id || sample.session_id || sample.sampled_at || idx),
          cells: [
            date(sample.sampled_at),
            badge(sample.state === "succeeded" ? "完成" : sample.state === "canceled" ? "已取消" : "失败", tone),
            fwd,
            rev,
            sample.error || "—",
          ],
        };
      });

    const hopsSection = section("逐跳分段明细",
      table(["跳段", "起止节点", "探测地址", "去程", "回程", "状态"], hopRows, {
        label: "逐跳分段明细",
        paginate: 20,
        motion: false,
        emptyText: "暂无跳段明细",
        rowKey: (_, idx) => `${group.id}-hop-${idx}`,
      }),
    );
    hopsSection.setAttribute("data-refresh-key", `focus-hops-sec-${group.id}`);

    const historySection = section("历史延迟趋势",
      node("div", { className: "a-card-body a-topo-focus-history" },
        renderHistorySection(group, topoMetricDir, "focus"),
        table(["采样时间", "状态", "去程", "回程", "摘要"], historyEntries.map((e) => e.cells), {
          label: "最近采样记录",
          paginate: 8,
          motion: false,
          emptyText: "暂无历史采样记录",
          rowKey: (_, idx) => `${group.id}-${historyEntries[idx]?.key || idx}`,
        }),
      ),
    );
    historySection.setAttribute("data-refresh-key", `focus-hist-sec-${group.id}`);

    return node("div", { className: "a-topo-focus-details", "data-refresh-key": `focus-details-${group.id}` },
      node("div", {
        className: `a-topo-dir-grid${dirCards.length === 1 ? " is-single" : ""}`,
        "data-refresh-key": `focus-dirs-${group.id}`,
      }, ...dirCards),
      hopsSection,
      historySection,
    );
  };

  const buildStandaloneControls = (groups, stats, domainBuckets, effectiveDomain, domainStats) => {
    const filterDefs = [
      { id: "all", label: `全部 (${domainStats.total})` },
      { id: "abnormal", label: `异常 (${domainStats.bad + domainStats.warn})` },
      { id: "running", label: `探测中 (${domainStats.running})` },
      { id: "multihop", label: `多跳 (${domainStats.multiHop})` },
      { id: "paused", label: `已暂停 (${domainStats.total - domainStats.autoEnabled})` },
    ];

    const viewSwitch = node("div", {
      className: "a-dash-topo-mode-switch",
      role: "group",
      "aria-label": "拓扑视图模式",
      "data-refresh-key": "topo-view-switch",
    },
      button("总览", () => setTopoViewMode("overview"), {
        className: `a-dash-topo-mode-btn${topoViewMode === "overview" ? " is-active" : ""}`,
        "aria-pressed": topoViewMode === "overview" ? "true" : "false",
        "data-topo-view-btn": "overview",
        "data-refresh-key": "view-overview",
      }),
      button("链图", () => setTopoViewMode("flow"), {
        className: `a-dash-topo-mode-btn${topoViewMode === "flow" ? " is-active" : ""}`,
        "aria-pressed": topoViewMode === "flow" ? "true" : "false",
        "data-topo-view-btn": "flow",
        "data-refresh-key": "view-flow",
      }),
      button("地图", () => setTopoViewMode("map"), {
        className: `a-dash-topo-mode-btn${topoViewMode === "map" ? " is-active" : ""}`,
        "aria-pressed": topoViewMode === "map" ? "true" : "false",
        "data-topo-view-btn": "map",
        "data-refresh-key": "view-map",
      }),
      button("聚焦分析", () => setTopoViewMode("focus", { focusGroupName: lastActiveGroup?.groupName || groups[0]?.groupName || "" }), {
        className: `a-dash-topo-mode-btn${topoViewMode === "focus" ? " is-active" : ""}`,
        "aria-pressed": topoViewMode === "focus" ? "true" : "false",
        "data-topo-view-btn": "focus",
        "data-refresh-key": "view-focus",
      }),
    );

    const dirSwitch = node("div", {
      className: "a-dash-topo-mode-switch",
      role: "group",
      "aria-label": "指标显示方向",
      "data-refresh-key": "topo-dir-switch",
    },
      ...["both", "forward", "reverse"].map((dir) => button(directionLabel(dir), () => setTopoMetricDir(dir), {
        className: `a-dash-topo-mode-btn${topoMetricDir === dir ? " is-active" : ""}`,
        "aria-pressed": topoMetricDir === dir ? "true" : "false",
        "data-topo-dir-btn": dir,
        "data-refresh-key": `dir-${dir}`,
      })),
    );

    const sortOptions = [
      ["default", "默认顺序"],
      ["abnormal", "异常优先"],
      ["latency_desc", "延迟从高到低"],
      ["loss_desc", "丢包从高到低"],
      ["name_asc", "分组名称 (A–Z)"],
    ];
    const sortSelect = node("select", {
      name: "topo_sort_by",
      "aria-label": "分组排序方式",
      "data-refresh-key": "topo-sort-select",
      onchange: (e) => setTopoSort(e.currentTarget.value),
    },
      node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")),
      ...sortOptions.map(([val, label]) => node("option", { value: val }, label)),
    );
    sortSelect.value = topoSortBy;

    const filterGroup = node("div", {
      className: "a-dash-topo-filter-group",
      role: "group",
      "aria-label": "监测分组状态筛选",
      "data-refresh-key": "topo-filter-group",
    },
      ...filterDefs.map((def) => button(def.label, () => setTopoFilter(def.id), {
        className: "a-dash-topo-filter-btn",
        "data-filter": def.id,
        "aria-pressed": topoStatusFilter === def.id ? "true" : "false",
        "data-refresh-key": `filter-${def.id}`,
      })),
    );

    const domainBar = domainBuckets.length > 1
      ? node("div", {
          className: "a-topo-domain-bar",
          role: "group",
          "aria-label": "按网络域筛选",
          "data-refresh-key": "topo-domain-bar",
        },
          button(`全部分域 (${groups.length})`, () => setTopoDomainGroup("all"), {
            className: "a-topo-domain-pill",
            "aria-pressed": effectiveDomain === "all" ? "true" : "false",
            "data-topo-domain-btn": "all",
            "data-refresh-key": "domain-all",
          }),
          ...domainBuckets.map((d) => button(`${d.title} (${d.chains.length})`, () => setTopoDomainGroup(d.id), {
            className: "a-topo-domain-pill",
            "aria-pressed": effectiveDomain === d.id ? "true" : "false",
            "data-topo-domain-btn": d.id,
            "data-refresh-key": `domain-${d.id}`,
          })),
        )
      : null;

    const kpis = node("div", { className: "a-topo-kpi-strip", "data-refresh-key": "topo-kpis" },
      keyedBadge(`监测组 ${stats.total}`, "", "kpi-total"),
      keyedBadge(`正常 ${stats.healthy}`, stats.healthy ? "ok" : "", "kpi-ok"),
      stats.bad + stats.warn > 0 ? keyedBadge(`告警 ${stats.bad + stats.warn}`, "bad", "kpi-bad") : null,
      stats.running > 0 ? keyedBadge(`探测中 ${stats.running}`, "warn", "kpi-run") : null,
      stats.multiHop > 0 ? keyedBadge(`多跳 ${stats.multiHop}`, "", "kpi-multi") : null,
      keyedBadge(`自动 ${stats.autoEnabled}/${stats.total}`, "", "kpi-auto"),
    );

    return node("div", { className: "a-topo-controls-wrap", "data-refresh-key": "topo-controls" },
      kpis,
      domainBar,
      node("div", { className: "a-topo-controls-bar" },
        filterGroup,
        node("div", { className: "a-actions a-topo-right-controls" },
          viewSwitch,
          dirSwitch,
          node("div", { className: "a-field a-topo-sort-field" }, sortSelect),
        ),
      ),
    );
  };

  const paint = (force = false) => {
    paintScheduled = false;
    paintForced = false;
    if (!canViewSurface() || disposed) return;

    const states = getStates();
    const probesState = states.get("probes") || "";
    const probesData = getProbes();
    const rawPairs = Array.isArray(probesData?.pairs) ? probesData.pairs : [];
    const agents = getAgents();
    const orderIDs = getOrderIDs();

    const groups = canReadProbes()
      ? buildProbeGroupModels({ pairs: rawPairs, agents, orderIDs })
      : [];
    lastGroups = groups;
    const stats = summarizeProbeGroups(groups);

    const measuredWidth = observedWidth >= 220 ? observedWidth : (bodyHost.clientWidth >= 220 ? bodyHost.clientWidth : 0);
    const fallbackWidth = Math.max(300, Math.min(1120, Math.round((window.innerWidth || 1080) - ((window.innerWidth || 1080) > 896 ? 300 : 48))));
    const renderWidth = Math.max(280, Math.round(measuredWidth > 0 ? measuredWidth : fallbackWidth));
    const isMobile = renderWidth < 580;

    // Standalone domain grouping
    const domainMetaModel = isStandalonePage ? buildProbeTopologyModel({ groups, metricDir: topoMetricDir }) : null;
    const domainBuckets = domainMetaModel?.domainGroups || [];
    const effectiveDomain = (isStandalonePage && topoDomainGroup !== "all" && domainBuckets.some((d) => d.id === topoDomainGroup))
      ? topoDomainGroup
      : "all";
    const domainScopedGroups = effectiveDomain === "all"
      ? groups
      : groups.filter((g) => domainGroupForProbeGroup(g).id === effectiveDomain);
    const domainStats = summarizeProbeGroups(domainScopedGroups);

    const filteredGroups = isStandalonePage
      ? filterAndSortProbeGroups(domainScopedGroups, {
          statusFilter: topoStatusFilter,
          sortBy: topoSortBy,
          metricDir: topoMetricDir,
        })
      : groups;

    const activeResolution = isStandalonePage
      ? resolveSelectedProbeGroup(filteredGroups, topoSelectedGroup)
      : resolveSelectedProbeGroup(groups, dashGroupName);
    const activeGroup = activeResolution.group;
    lastActiveGroup = activeGroup;

    const showMapCurrently = isStandalonePage
      ? (topoViewMode === "map" || (topoViewMode === "focus" && topoFocusViz === "map"))
      : dashViewMode === "map";

    // Reconcile local ephemeral focus key if active group changed or element disappeared
    if (activeGroup && focusedItemKey.startsWith("node:")) {
      const idx = Number(focusedItemKey.slice(5));
      if (!Number.isInteger(idx) || idx < 0 || idx >= activeGroup.nodes.length) focusedItemKey = "";
    } else if (activeGroup && focusedItemKey.startsWith("hop:")) {
      const idx = Number(focusedItemKey.slice(4));
      if (!Number.isInteger(idx) || idx < 0 || idx >= activeGroup.hops.length) focusedItemKey = "";
    } else if (!activeGroup) {
      focusedItemKey = "";
    }

    const fleetMapModel = (isStandalonePage && topoViewMode === "map")
      ? buildProbeTopologyModel({ groups: filteredGroups, metricDir: topoMetricDir })
      : null;
    lastFleetModel = fleetMapModel;
    if (fleetMapModel) {
      const fleetClusters = clusterTopologyNodes(fleetMapModel.nodes);
      mapPinnedKey = reconcileSelectionKey(mapPinnedKey, fleetMapModel, { clusters: fleetClusters, allowCluster: true });
      mapHoveredKey = reconcileSelectionKey(mapHoveredKey, fleetMapModel, { clusters: fleetClusters, allowCluster: true });
    } else {
      mapPinnedKey = "";
    }

    const waiting = canReadProbes() && (!states.has("probes") || probesState === "loading") && !rawPairs.length;
    const denied = !canReadProbes() || probesState === "denied";
    const failed = probesState === "failed";

    let nextStatus = "";
    if (denied) nextStatus = "无诊断读取权限";
    else if (waiting) nextStatus = "读取中…";
    else if (failed) nextStatus = rawPairs.length ? "刷新失败 · 保留上次记录" : "探测链路读取失败";

    const sig = JSON.stringify([
      isStandalonePage,
      chartWidth,
      showMapCurrently ? (isMobile ? renderWidth : "desk") : "nomap",
      isMobile,
      showMapCurrently ? mapZoom : null,
      nextStatus,
      probesState,
      dashGroupName,
      dashViewMode,
      dashShowHistory,
      topoViewMode,
      effectiveDomain,
      topoSelectedGroup,
      topoFocusViz,
      topoStatusFilter,
      topoSortBy,
      topoMetricDir,
      focusedItemKey,
      flowInspectKey,
      mapPinnedKey,
      canExecuteProbes(),
      groups.map((g) => ({
        id: g.id,
        groupName: g.groupName,
        remark: g.remark,
        running: g.running,
        autoEnabled: g.autoEnabled,
        autoIntervalSeconds: g.autoIntervalSeconds,
        availability: g.availability,
        nodes: g.nodes,
        hops: g.hops,
        overall: g.overall,
        historyLen: g.history.length,
        latestAt: g.overall.sampledAt,
      })),
    ]);
    if (!force && sig === lastPaintSignature) return;
    lastPaintSignature = sig;

    if (statusNode.textContent !== nextStatus) statusNode.textContent = nextStatus;

    // Header badges & top actions (strict single entry per action)
    const topBadges = [];
    if (!isStandalonePage && groups.length) {
      topBadges.push(keyedBadge(`${groups.length} 组`, "", "head-group-count"));
      if (stats.bad + stats.warn > 0) topBadges.push(keyedBadge(`告警 ${stats.bad + stats.warn}`, "bad", "head-alert"));
      else if (stats.healthy > 0) topBadges.push(keyedBadge("正常", "ok", "head-ok"));
    }
    patchChildren(headerBadges, topBadges);

    const topActionNodes = [];
    if (!isStandalonePage && activeGroup) {
      topActionNodes.push(renderVizModeSwitch(dashViewMode, setDashboardViewMode, {
        attrName: "data-topo-dash-view",
        keyPrefix: "dash",
        labels: ["链路图", "地理图"],
      }));
      topActionNodes.push(button("历史趋势", toggleDashboardHistory, {
        className: "a-button a-topo-mini-btn",
        "aria-pressed": dashShowHistory ? "true" : "false",
        "data-refresh-key": "dash-toggle-history",
      }));
    }
    if (!isStandalonePage && canViewSurface()) {
      topActionNodes.push(node("a", {
        className: "a-button a-topo-mini-btn",
        href: "#topology",
        "data-refresh-key": "open-topology-page",
      }, "拓扑详情"));
    }
    if (canExecuteProbes()) {
      topActionNodes.push(button("+ 新建监测组", () => probeController.openCreate(), {
        className: "a-button a-topo-mini-btn",
        tone: "primary",
        "data-refresh-key": "add-probe-group",
      }));
    }
    topActionNodes.push(keyed("head-status", statusNode));
    patchChildren(headerActions, topActionNodes);

    // Body content
    const bodyChildren = [];
    if (denied) {
      bodyChildren.push(keyed("denied-notice", notice("当前账号缺少监测链路读取权限（需节点读取与任务读取权限）。", "warn")));
      patchChildren(bodyHost, bodyChildren);
      return;
    }

    if (!groups.length) {
      const msg = waiting
        ? "正在读取监测分组…"
        : failed
          ? "监测分组读取失败，请点击右上角刷新重试。"
          : canExecuteProbes()
            ? "暂无监测分组，点击右上角「+ 新建监测组」创建一条监测链路。"
            : "暂无已配置的监测分组。";
      bodyChildren.push(keyed("empty-groups", empty(msg)));
      patchChildren(bodyHost, bodyChildren);
      return;
    }

    if (!isStandalonePage) {
      // Homepage: strictly ONE selected group + ONE link at a time
      bodyChildren.push(renderGroupSwitcher(groups, activeGroup, (name) => selectDashboardGroup(name), "dash"));
      if (activeGroup) {
        bodyChildren.push(renderSingleGroupWorkspace(activeGroup, {
          metricDir: "both",
          vizMode: dashViewMode,
          showHistory: dashShowHistory,
          includeDelete: false,
          gradPrefix: "dash",
          renderWidth,
          isMobile,
        }));
      }
      patchChildren(bodyHost, bodyChildren);
      if (dashViewMode === "map") syncLiveMapSelection();
      return;
    }

    // Standalone #topology page
    bodyChildren.push(buildStandaloneControls(groups, stats, domainBuckets, effectiveDomain, domainStats));

    if (!filteredGroups.length) {
      bodyChildren.push(keyed("filter-empty", node("div", { className: "a-topo-empty-box" },
        empty("当前筛选条件下无匹配的监测分组。"),
        node("div", { className: "a-actions" },
          topoStatusFilter !== "all"
            ? button("显示全部状态", () => setTopoFilter("all"), {
                className: "a-button",
                "data-refresh-key": "reset-topo-filter",
              })
            : null,
          effectiveDomain !== "all"
            ? button("显示全部分域", () => setTopoDomainGroup("all"), {
                className: "a-button",
                "data-refresh-key": "reset-topo-domain",
              })
            : null,
        ),
      )));
      patchChildren(bodyHost, bodyChildren);
      return;
    }

    if (topoViewMode === "focus") {
      bodyChildren.push(renderGroupSwitcher(filteredGroups, activeGroup, (name) => setTopoSelectedGroup(name), "focus", topoMetricDir));
      if (activeGroup) {
        const vizSwitchEl = renderVizModeSwitch(topoFocusViz, setTopoFocusViz, {
          attrName: "data-topo-focus-viz",
          keyPrefix: "focus",
          labels: ["链路流图", "地理轨迹"],
        });
        bodyChildren.push(renderSingleGroupWorkspace(activeGroup, {
          metricDir: topoMetricDir,
          vizMode: topoFocusViz,
          vizSwitchEl,
          showHistory: false,
          includeDelete: true,
          gradPrefix: "focus",
          renderWidth,
          isMobile,
        }));
        bodyChildren.push(renderFocusDetails(activeGroup));
      }
    } else if (topoViewMode === "map") {
      bodyChildren.push(
        node("div", { className: "a-topo-fleet-map-panel", "data-refresh-key": "fleet-map-panel" },
          buildMapStageElement({
            model: fleetMapModel,
            singleGroup: null,
            scopeKey: "fleet",
            isMobile,
            width: renderWidth,
          }),
          renderFleetMapInspect(fleetMapModel),
        ),
      );
    } else if (topoViewMode === "flow") {
      bodyChildren.push(node("div", {
        className: "a-topo-flow-list",
        "data-refresh-key": "flow-group-list",
      }, ...filteredGroups.map((g) => renderFlowComparisonCard(g))));
    } else {
      bodyChildren.push(node("div", {
        className: "a-topo-overview-grid",
        "data-refresh-key": "overview-grid",
      }, ...filteredGroups.map((g) => renderOverviewCard(g))));
    }

    patchChildren(bodyHost, bodyChildren);
    if (showMapCurrently) syncLiveMapSelection();
    const sortEl = bodyHost.querySelector('select[name="topo_sort_by"]');
    if (sortEl && document.activeElement !== sortEl && sortEl.value !== topoSortBy) {
      sortEl.value = topoSortBy;
    }
  };

  const schedulePaint = (force = false) => {
    if (force) paintForced = true;
    if (paintScheduled || disposed || !canViewSurface()) return;
    paintScheduled = true;
    queueMicrotask(() => {
      if (!paintScheduled || disposed) return;
      const useForce = paintForced;
      paintScheduled = false;
      paintForced = false;
      if (ctx.current()) paint(useForce);
    });
  };

  const onProbeSaved = (saved, { isEdit = false, previousGroupName = "" } = {}) => {
    const nextName = String(saved?.group_name || "").trim();
    if (!nextName) return;
    if (!isStandalonePage) {
      if (!isEdit || dashGroupName === previousGroupName || !dashGroupName) {
        dashGroupTouched = true;
        dashGroupName = nextName;
        ctx.preferences?.patch?.("dashboard", { topo_group: nextName });
      }
    } else {
      const patchFields = {};
      if (!isEdit || topoSelectedGroup === previousGroupName || topoViewMode === "focus") {
        topoGroupTouched = true;
        topoSelectedGroup = nextName;
        patchFields.selected_group = nextName;
      }
      if (!isEdit && topoStatusFilter !== "all") {
        topoFilterTouched = true;
        topoStatusFilter = "all";
        patchFields.status_filter = "all";
      }
      if (!isEdit && topoDomainGroup !== "all") {
        topoDomainTouched = true;
        topoDomainGroup = "all";
        patchFields.domain_group = "all";
      }
      if (Object.keys(patchFields).length) {
        ctx.preferences?.patch?.("topology", patchFields);
      }
    }
  };

  const onProbeDeleted = (pair) => {
    const deletedName = String(pair?.group_name || "").trim();
    if (!deletedName) return;
    if (!isStandalonePage && dashGroupName === deletedName) {
      dashGroupName = "";
      focusedItemKey = "";
    }
    if (isStandalonePage && topoSelectedGroup === deletedName) {
      topoSelectedGroup = "";
      focusedItemKey = "";
    }
  };

  const card = canViewSurface() && section(
    "网络延迟监测拓扑",
    bodyHost,
    node("div", { className: "a-actions a-dash-topo-tools" }, headerBadges, headerActions),
  );
  card?.classList?.add("a-dash-topo-card");
  if (isStandalonePage && card) card.classList.add("is-standalone");

  return {
    visible: Boolean(card),
    element: card,
    paint,
    schedulePaint,
    onProbeSaved,
    onProbeDeleted,
  };
}
