import {
  buildProbeGroupModels,
  buildProbeTopologyModel,
  domainGroupForProbeGroup,
  filterAndSortProbeGroups,
  resolveSelectedProbeGroup,
  summarizeProbeGroups,
} from "../modules/dashboard-model.js";
import { readout } from "./dashboard-console.js";
import {
  blendLinkCamera,
  clusterFocusCamera3D,
  compareColorClass,
  extractPublicTargets,
  fleetLinkCamera,
  linkMapCamera,
  linkMapSize,
  openingLinkCamera,
  renderFleetLinkMap,
  renderLinkMap,
  renderLinkTrace,
  rotateLinkCamera,
  sameLinkCamera,
  zoomLinkCamera,
} from "./dashboard-linkmap.js";
import {
  formatDirBadgeMetric,
  formatLoss,
  formatMs,
  formatMsCompact,
  renderProbeHistoryChart,
} from "./dashboard-topo-shared.js";

import { listen, patch } from "./live-dom.js";
import { badge, button, empty, muted, node, notice, section, table } from "./ui.js";

const VALID_TOPO_DOMAINS = new Set([
  "all",
  "domain:multihop",
  "domain:apac",
  "domain:emea",
  "domain:amer",
  "domain:cross",
  "domain:external",
]);
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

function groupKindLabel(group) {
  if (group.isExternal) return "自定义 IP";
  if (group.isMultiHop) return `${group.nodes.length} 站多跳`;
  return "双节点";
}

const NBSP = String.fromCharCode(160);
const BLANK_FIGURE = Object.freeze(["—", ""]);

function latencyParts(ms) {
  if (typeof ms !== "number" || !Number.isFinite(ms)) return BLANK_FIGURE;
  return [ms.toFixed(ms >= 100 ? 0 : 1), `${NBSP}ms`];
}

function directionReading(dir, running, ready, { detailed = false } = {}) {
  if (!dir || !dir.available) {
    if (running) return { parts: BLANK_FIGURE, note: "探测中", tone: "" };
    return { parts: BLANK_FIGURE, note: ready ? "待采样" : "暂不可用", tone: "" };
  }
  if (dir.state === "running" || dir.state === "preparing") return { parts: BLANK_FIGURE, note: "探测中", tone: "" };
  if (dir.state === "failed" || dir.error) return { parts: BLANK_FIGURE, note: dir.error || "探测失败", tone: "bad" };
  if (!dir.reached || (dir.lossPercent ?? 0) >= 100) return { parts: BLANK_FIGURE, note: "不可达 · 100% 丢包", tone: "bad" };
  const tone = dir.status?.tone;
  const notes = [];
  if (detailed && dir.minMS != null && dir.maxMS != null) {
    notes.push(`${dir.minMS.toFixed(0)}–${dir.maxMS.toFixed(0)} ms`);
  }
  if (detailed && dir.stdevMS != null) {
    notes.push(`抖动 ${dir.stdevMS.toFixed(1)} ms`);
  }
  notes.push(`丢包 ${formatLoss(dir.lossPercent)}`);
  return { parts: latencyParts(dir.avgMS), note: notes.join(" · "), tone: tone === "bad" || tone === "warn" ? tone : "" };
}

function railReadout(key, label, dir, running, ready, rail = "", opts = undefined) {
  const reading = directionReading(dir, running, ready, opts);
  return readout(key, label, reading.parts, reading.note, `${reading.tone} ${rail}`.trim());
}

const railText = (key, className, text) => node("span", { className, "data-refresh-key": key }, text);

function nodeSubject(group, index, opts) {
  const endpoint = group.nodes[index];
  if (!endpoint) return null;
  const inbound = index > 0 ? group.hops[index - 1] : null;
  const outbound = group.hops[index] || null;
  const head = [
    keyedBadge(endpoint.role, endpoint.issue || !endpoint.online ? "bad" : "ok", "role"),
    railText("name", "a-con-link-name", endpoint.name),
    endpoint.address ? node("code", { className: "a-dash-topo-code", "data-refresh-key": "addr" }, endpoint.address) : null,
  ];
  const alert = endpoint.issue
    ? node("div", { className: "a-topo-inline-alert is-bad", "data-refresh-key": "issue" }, endpoint.issue)
    : null;
  const rows = [
    inbound ? railReadout("in", `入站 (${inbound.from.name})`, inbound.forward, group.running, inbound.ready, "is-fwd", opts) : null,
    outbound ? railReadout("out", `出站 (${outbound.to.name})`, outbound.forward, group.running, outbound.ready, "is-fwd", opts) : null,
  ];
  return { head, meta: null, alert, rows };
}

function hopSubject(group, index, metricDir = "both", opts = undefined) {
  const hop = group.hops[index];
  if (!hop) return null;
  const route = `${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name}`;
  const head = [
    keyedBadge(`第 ${index + 1}/${group.hopCount} 跳`, hop.tone || "", "hop"),
    railText("name", "a-con-link-name", route),
  ];
  const alert = hop.unavailableReason
    ? node("div", { className: "a-topo-inline-alert is-warn", "data-refresh-key": "reason" }, hop.unavailableReason)
    : null;
  const rows = [
    metricDir !== "reverse" ? railReadout("fwd", "去程", hop.forward, group.running, hop.ready, "is-fwd", opts) : null,
    hop.bidirectional && metricDir !== "forward" ? railReadout("rev", "回程", hop.reverse, group.running, hop.ready, "is-rev", opts) : null,
  ];
  return { head, meta: null, alert, rows };
}

function renderStatusDot(group, {
  active = false,
  pinnedSlot = null,
  refreshKey = undefined,
} = {}) {
  const tone = group?.overall?.statusBadge?.tone || "";
  const extra = refreshKey ? { "data-refresh-key": refreshKey } : {};
  if (pinnedSlot != null) {
    return node("span", {
      className: `a-con-link-gdot ${compareColorClass(pinnedSlot)}${tone === "bad" ? " is-bad" : ""}`,
      "aria-hidden": "true",
      ...extra,
    });
  }
  const showAccent = active && tone !== "bad" && tone !== "warn";
  return node("span", {
    className: `a-topo-dot is-${tone || "idle"}${showAccent ? " is-accent" : ""}`,
    "aria-hidden": "true",
    ...extra,
  });
}

function groupSubject(group, metricDir = "both", {
  showName = false,
  detailed = false,
  showColorDot = false,
  pinnedSlot = null,
} = {}) {
  const status = group.overall.statusBadge;
  const ready = group.availability.ready;
  const head = [
    showName && showColorDot ? renderStatusDot(group, {
      active: true,
      pinnedSlot,
      refreshKey: "gdot",
    }) : null,
    showName ? railText("gname", "a-con-link-name", group.groupName) : null,
    keyedBadge(status.label, status.tone, "status"),
    !showName && !group.autoEnabled ? railText("paused", "a-con-link-alert", "已暂停") : null,
    !showName && !ready && group.availability.reason ? railText("reason", "a-con-link-alert", group.availability.reason) : null,
    !showName && group.overall.error ? railText("error", "a-topo-err-inline", group.overall.error) : null,
  ];
  const meta = showName
    ? node("div", { className: "a-topo-inspector-meta", "data-refresh-key": "meta" },
        node("span", {}, groupKindLabel(group)),
        node("span", { className: "a-topo-meta-sep", "aria-hidden": "true" }, "·"),
        node("span", { className: group.autoEnabled ? "" : "is-warn" }, group.autoEnabled ? `自动 ${group.autoIntervalSeconds}s` : "自动已暂停"),
      )
    : null;
  const alertMsg = group.overall.error || (!ready ? group.availability.reason : "");
  const alert = showName && alertMsg
    ? node("div", {
        className: `a-topo-inline-alert${group.overall.error ? " is-bad" : " is-warn"}`,
        "data-refresh-key": "alert",
      }, alertMsg)
    : null;
  const rows = [
    metricDir !== "reverse" ? railReadout("fwd", "去程", group.overall.forward, group.running, ready, "is-fwd", { detailed }) : null,
    group.bidirectional && metricDir !== "forward" ? railReadout("rev", "回程", group.overall.reverse, group.running, ready, "is-rev", { detailed }) : null,
  ];
  return { head, meta, alert, rows };
}

function railTrace(group, sampledText = "") {
  const trace = renderLinkTrace(group);
  if (!trace) return null;
  return node("div", { className: "a-con-link-trace", "data-refresh-key": "trace" },
    trace.svg,
    node("div", { className: "a-con-link-trace-meta" },
      railText("range", "a-con-note", `近 ${trace.count} 次`),
      sampledText ? railText("sampled", "a-con-note a-con-link-time", sampledText) : null,
    ));
}

function railSubject(group, focusKey, metricDir = "both", opts = undefined) {
  if (focusKey.startsWith("node:")) return nodeSubject(group, Number(focusKey.slice(5)), opts) || groupSubject(group, metricDir, opts);
  if (focusKey.startsWith("hop:")) return hopSubject(group, Number(focusKey.slice(4)), metricDir, opts) || groupSubject(group, metricDir, opts);
  return groupSubject(group, metricDir, opts);
}

function makeNativeSelect({ name, ariaLabel, options, value, onChange, refreshKey, className = "" }) {
  const select = node("select", {
    name,
    "aria-label": ariaLabel,
    "data-refresh-key": refreshKey,
    onchange: (event) => onChange(event.currentTarget.value),
  },
    node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")),
    ...options.map(([val, label]) => node("option", { value: val }, label)),
  );
  select.value = value;
  return node("div", { className: `a-field ${className}`.trim(), "data-refresh-key": `${refreshKey}-wrap` }, select);
}

function groupMetricText(group, metricDir = "both") {
  const flags = { running: group.running, ready: group.availability.ready };
  if (metricDir === "reverse") {
    return group.bidirectional ? formatDirBadgeMetric(group.overall.reverse, flags) : "单向";
  }
  return formatDirBadgeMetric(group.overall.forward, flags);
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
  let dashGroupTouched = false;

  // Standalone #topology workspace preferences (page = "topology")
  let topoDomainGroup = "all";
  let topoSelectedGroup = "";
  let topoStatusFilter = "all";
  let topoSortBy = "default";
  let topoMetricDir = "both";
  let topoDomainTouched = false;
  let topoGroupTouched = false;
  let topoFilterTouched = false;
  let topoSortTouched = false;
  let topoDirTouched = false;

  // Ephemeral inspection & 3D globe state (never persisted)
  let focusedItemKey = "";
  let topoMapShowAll = true;
  let fleetHoverGroup = "";
  let fleetHoverKeepsInspector = false;
  let fleetPinnedNodeID = "";
  let lastScrolledGroupKey = "";

  // 3D sphere camera, inertia coasting & animated zoom state
  let linkCamera = null;
  let linkGoal = null;
  let linkBaseCamera = null;
  let linkWidth = 0;
  let linkSizeOpts = { minHeight: 220, maxHeight: 320, ratio: 0.36 };
  let linkGlide = 0;
  let inertiaFrame = 0;
  let globeCustom = false;
  let globeScopeSig = "";
  let wheelCommitTimer = null;
  let fleetHoverClearTimer = null;
  let fleetHoverEnterTimer = null;
  let fleetHoverPending = "";
  let lastGlobePointer = { x: 0, y: 0, at: 0 };
  let suppressNextGlobeClick = false;
  let globeDrag = {
    active: false,
    dragging: false,
    movedDist: 0,
    pointerId: null,
    lastClient: { x: 0, y: 0 },
    vx: 0,
    vy: 0,
    lastMoveAt: 0,
    svgEl: null,
    raf: 0,
  };

  let lastActiveGroup = null;
  let lastFilteredGroups = [];
  let lastFleetModel = null;
  let lastFleetActive = false;
  let lastEffectiveDomainTitle = "全球链路全景";

  const clearWheelTimer = () => {
    if (wheelCommitTimer) {
      clearTimeout(wheelCommitTimer);
      wheelCommitTimer = null;
    }
  };

  const clearFleetHoverTimer = () => {
    if (fleetHoverClearTimer) {
      clearTimeout(fleetHoverClearTimer);
      fleetHoverClearTimer = null;
    }
    if (fleetHoverEnterTimer) {
      clearTimeout(fleetHoverEnterTimer);
      fleetHoverEnterTimer = null;
    }
    fleetHoverPending = "";
  };

  const stopCameraMotion = () => {
    cancelAnimationFrame(linkGlide);
    cancelAnimationFrame(inertiaFrame);
    linkGlide = 0;
    inertiaFrame = 0;
  };

  ctx.signal.addEventListener("abort", () => {
    disposed = true;
    paintScheduled = false;
    stopCameraMotion();
    cancelAnimationFrame(globeDrag.raf);
    clearWheelTimer();
    clearFleetHoverTimer();
    lastActiveGroup = null;
    lastFilteredGroups = [];
    lastFleetModel = null;
  }, { once: true });

  const syncSavedPreferences = ({ repaint = false } = {}) => {
    if (disposed || !ctx.preferences?.get) return;
    let changed = false;

    if (!isStandalonePage) {
      const saved = ctx.preferences.get("dashboard") || {};
      if (!dashGroupTouched && typeof saved.topo_group === "string" && saved.topo_group.length <= 40 && saved.topo_group !== dashGroupName) {
        dashGroupName = saved.topo_group;
        focusedItemKey = "";
        globeCustom = false;
        changed = true;
      }
    } else {
      const saved = ctx.preferences.get("topology") || {};
      if (!topoDomainTouched && saved.domain_group !== undefined) {
        const nextDomain = normalizeDomainPref(saved.domain_group);
        if (nextDomain !== topoDomainGroup) {
          topoDomainGroup = nextDomain;
          fleetHoverGroup = "";
          fleetHoverKeepsInspector = false;
          fleetPinnedNodeID = "";
          globeCustom = false;
          changed = true;
        }
      }
      if (!topoGroupTouched && typeof saved.selected_group === "string" && saved.selected_group.length <= 40 && saved.selected_group !== topoSelectedGroup) {
        topoSelectedGroup = saved.selected_group;
        focusedItemKey = "";
        globeCustom = false;
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
    globeCustom = false;
    clearWheelTimer();
    if (changed) paint(true);
  };

  const setTopoDomainGroup = (domainID) => {
    const normalized = normalizeDomainPref(domainID);
    topoDomainTouched = true;
    const changed = topoDomainGroup !== normalized;
    topoDomainGroup = normalized;
    if (changed) {
      fleetHoverGroup = "";
      fleetHoverKeepsInspector = false;
      fleetPinnedNodeID = "";
      focusedItemKey = "";
      globeCustom = false;
      clearWheelTimer();
      ctx.preferences?.patch?.("topology", { domain_group: normalized });
      paint(true);
    }
  };

  const setTopoSelectedGroup = (groupName, { persist = true, showOverview = false } = {}) => {
    const next = String(groupName || "").trim().slice(0, 40);
    topoGroupTouched = true;
    const changed = topoSelectedGroup !== next || topoMapShowAll !== showOverview;
    topoMapShowAll = showOverview;
    if (persist && next && topoSelectedGroup !== next) {
      ctx.preferences?.patch?.("topology", { selected_group: next });
    }
    if (next) topoSelectedGroup = next;
    focusedItemKey = "";
    fleetHoverGroup = "";
    fleetHoverKeepsInspector = false;
    fleetPinnedNodeID = "";
    globeCustom = false;
    clearWheelTimer();
    if (changed) paint(true);
  };

  const setTopoFilter = (filter) => {
    if (!VALID_TOPO_FILTERS.has(filter)) return;
    topoFilterTouched = true;
    const changed = topoStatusFilter !== filter;
    topoStatusFilter = filter;
    if (changed) {
      fleetHoverGroup = "";
      fleetHoverKeepsInspector = false;
      fleetPinnedNodeID = "";
      globeCustom = false;
      clearWheelTimer();
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
  let cardEl = null;

  // ==================== 3D Globe Drawing, Glide, Inertia & Cross-Panel Sync ====================

  const drawCurrentGlobe = (draft = false) => {
    if (lastFleetActive) {
      return renderFleetLinkMap(lastFilteredGroups, {
        width: linkWidth,
        sizeOpts: linkSizeOpts,
        focusGroupName: "",
        hoverGroupName: fleetHoverGroup,
        pinnedNodeID: fleetPinnedNodeID,
        camera: linkCamera,
        draft,
        metricDir: topoMetricDir,
        idPrefix: "fleet",
      });
    }
    if (!lastActiveGroup) return null;
    return renderLinkMap(lastActiveGroup, {
      width: linkWidth,
      sizeOpts: linkSizeOpts,
      focusKey: focusedItemKey,
      camera: linkCamera,
      draft,
      metricDir: isStandalonePage ? topoMetricDir : "both",
      idPrefix: isStandalonePage ? "topo" : "dash",
    });
  };

  const updateZoomHUD = () => {
    const resetBtn = bodyHost.querySelector('[data-refresh-key="lm-zoom-reset"]');
    if (!resetBtn || !linkCamera || !linkBaseCamera) return;
    const pct = Math.max(25, Math.min(800, Math.round((linkCamera.radius / Math.max(1, linkBaseCamera.radius)) * 100)));
    const modified = globeCustom || !sameLinkCamera(linkCamera, linkBaseCamera);
    resetBtn.textContent = modified ? `重置 ${pct}%` : `${pct}%`;
    resetBtn.disabled = !modified && !focusedItemKey && !fleetPinnedNodeID;
  };

  const patchLiveGlobe = (draft = false) => {
    const live = bodyHost.querySelector("svg.a-lm");
    if (!live) return;
    const next = drawCurrentGlobe(draft);
    if (!next) return;
    next.classList.toggle("is-dragging", globeDrag.dragging);
    patch(live, next);
    updateZoomHUD();
  };

  const syncRailPeerHover = (groupName) => {
    const rail = bodyHost.querySelector(".a-con-link-list");
    if (!rail) return;
    rail.querySelectorAll(".a-con-link-item[data-group-name]").forEach((row) => {
      row.classList.toggle("is-peer-hover", Boolean(groupName) && row.getAttribute("data-group-name") === groupName);
    });
  };

  const applyFleetHover = (nextGroup, nextKeepInspector = false) => {
    const prevKeep = fleetHoverKeepsInspector;
    fleetHoverGroup = nextGroup;
    fleetHoverKeepsInspector = Boolean(nextGroup && nextKeepInspector);
    patchLiveGlobe(false);
    if (!fleetHoverKeepsInspector && !(nextGroup === "" && prevKeep)) {
      patchStandaloneInspectorOnly();
    }
    syncRailPeerHover(nextGroup);
  };

  const setFleetHover = (groupName, { immediate = false, delay = 145, resetTimer = false, keepInspector = false } = {}) => {
    if (!lastFleetActive || globeDrag.active || globeDrag.dragging) return;
    if (!groupName) {
      if (fleetHoverEnterTimer) {
        clearTimeout(fleetHoverEnterTimer);
        fleetHoverEnterTimer = null;
      }
      fleetHoverPending = "";
      if (!fleetHoverGroup) return;
      if (immediate) {
        if (fleetHoverClearTimer) {
          clearTimeout(fleetHoverClearTimer);
          fleetHoverClearTimer = null;
        }
        applyFleetHover("", false);
        return;
      }
      if (!fleetHoverClearTimer) {
        fleetHoverClearTimer = setTimeout(() => {
          fleetHoverClearTimer = null;
          if (!fleetHoverGroup || disposed || globeDrag.active || globeDrag.dragging) return;
          applyFleetHover("", false);
        }, 175);
      }
      return;
    }
    if (fleetHoverClearTimer) {
      clearTimeout(fleetHoverClearTimer);
      fleetHoverClearTimer = null;
    }
    if (fleetHoverGroup === groupName && fleetHoverKeepsInspector === Boolean(keepInspector)) {
      if (fleetHoverEnterTimer) {
        clearTimeout(fleetHoverEnterTimer);
        fleetHoverEnterTimer = null;
      }
      fleetHoverPending = "";
      return;
    }
    if (immediate) {
      clearFleetHoverTimer();
      applyFleetHover(groupName, keepInspector);
      return;
    }
    if (fleetHoverPending === groupName && fleetHoverEnterTimer && !resetTimer) return;
    if (fleetHoverEnterTimer) {
      clearTimeout(fleetHoverEnterTimer);
      fleetHoverEnterTimer = null;
    }
    fleetHoverPending = groupName;
    fleetHoverEnterTimer = setTimeout(() => {
      fleetHoverEnterTimer = null;
      if (disposed || !lastFleetActive || globeDrag.active || globeDrag.dragging) return;
      if (fleetHoverPending !== groupName) return;
      fleetHoverPending = "";
      applyFleetHover(groupName, keepInspector);
    }, delay);
  };

  const glideLinkMap = (from, duration, { easeOutOnly = false } = {}) => {
    stopCameraMotion();
    const goal = linkGoal;
    let started = 0;
    const step = (now) => {
      if (disposed || goal !== linkGoal) return;
      if (!started) started = now;
      const t = Math.min(1, (now - started) / duration);
      const eased = easeOutOnly
        ? 1 - Math.pow(1 - t, 3)
        : (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(2 - 2 * t, 3) / 2);
      linkCamera = t < 1 ? blendLinkCamera(from, goal, eased) : goal;
      linkGlide = t < 1 ? requestAnimationFrame(step) : 0;
      patchLiveGlobe(linkGlide !== 0);
    };
    linkGlide = requestAnimationFrame(step);
  };

  const zoomToCluster = (spec) => {
    if (!spec) return;
    clearWheelTimer();
    clearFleetHoverTimer();
    fleetHoverGroup = "";
    fleetHoverKeepsInspector = false;
    syncRailPeerHover("");
    const goal = clusterFocusCamera3D(spec, linkWidth, linkSizeOpts);
    if (!goal) return;
    globeCustom = true;
    linkGoal = goal;
    if (!allowMotion()) {
      stopCameraMotion();
      linkCamera = goal;
      patchLiveGlobe(false);
      return;
    }
    glideLinkMap(linkCamera || linkBaseCamera || goal, 620, { easeOutOnly: true });
  };

  const coastGlobeInertia = (vx0, vy0) => {
    stopCameraMotion();
    let vx = vx0, vy = vy0;
    const step = () => {
      if (disposed || globeDrag.active || !linkCamera) {
        inertiaFrame = 0;
        return;
      }
      vx *= 0.89;
      vy *= 0.89;
      if (Math.hypot(vx, vy) < 0.28) {
        inertiaFrame = 0;
        patchLiveGlobe(false);
        return;
      }
      linkCamera = rotateLinkCamera(linkCamera, vx, vy, linkWidth);
      linkGoal = linkCamera;
      patchLiveGlobe(true);
      inertiaFrame = requestAnimationFrame(step);
    };
    inertiaFrame = requestAnimationFrame(step);
  };

  const aimGlobe = ({ fleet, groups, activeGroup, width, sizeOpts, scopeSig }) => {
    const resized = width !== linkWidth || sizeOpts.maxHeight !== linkSizeOpts.maxHeight;
    linkWidth = width;
    linkSizeOpts = sizeOpts;

    const goal = fleet
      ? fleetLinkCamera(groups, width, sizeOpts)
      : linkMapCamera(activeGroup, width, sizeOpts);
    const scopeChanged = scopeSig !== globeScopeSig;
    linkBaseCamera = goal;
    globeScopeSig = scopeSig;

    if (globeCustom && !scopeChanged && !resized && linkCamera) {
      return;
    }
    if (scopeChanged) globeCustom = false;
    if (!resized && sameLinkCamera(goal, linkGoal) && linkCamera) return;

    const from = linkCamera;
    linkGoal = goal;
    if (!allowMotion() || (from && resized && !scopeChanged)) {
      stopCameraMotion();
      linkCamera = goal;
      return;
    }
    linkCamera = from || openingLinkCamera(goal);
    glideLinkMap(linkCamera, from ? 740 : 1040);
  };

  const resetGlobeView = () => {
    clearWheelTimer();
    clearFleetHoverTimer();
    globeCustom = false;
    focusedItemKey = "";
    fleetHoverGroup = "";
    fleetHoverKeepsInspector = false;
    fleetPinnedNodeID = "";
    syncRailPeerHover("");
    if (linkBaseCamera && linkCamera && allowMotion() && !sameLinkCamera(linkCamera, linkBaseCamera)) {
      linkGoal = linkBaseCamera;
      glideLinkMap(linkCamera, 520, { easeOutOnly: true });
    } else if (linkBaseCamera) {
      stopCameraMotion();
      linkCamera = linkBaseCamera;
      linkGoal = linkBaseCamera;
    }
    paint(true);
  };

  const zoomGlobeBy = (factor, { animate = false } = {}) => {
    if (!linkCamera) return;
    stopCameraMotion();
    const size = linkMapSize(linkWidth, linkSizeOpts);
    const target = zoomLinkCamera(linkCamera, factor, size);
    globeCustom = true;
    if (animate && allowMotion() && !sameLinkCamera(linkCamera, target)) {
      clearWheelTimer();
      linkGoal = target;
      glideLinkMap(linkCamera, 220, { easeOutOnly: true });
      return;
    }
    linkCamera = target;
    linkGoal = target;
    patchLiveGlobe(true);
    clearWheelTimer();
    wheelCommitTimer = setTimeout(() => {
      wheelCommitTimer = null;
      if (!disposed && ctx.current() && !globeDrag.dragging) {
        patchLiveGlobe(false);
      }
    }, 150);
  };

  const handleGlobalGlobeMove = (event) => {
    if (!globeDrag.active) return;
    if (globeDrag.pointerId != null && event.pointerId !== globeDrag.pointerId) return;
    const liveSvg = globeDrag.svgEl?.isConnected ? globeDrag.svgEl : bodyHost.querySelector("svg.a-lm");
    if (!liveSvg || !linkCamera) return;
    const stepDx = event.clientX - globeDrag.lastClient.x;
    const stepDy = event.clientY - globeDrag.lastClient.y;
    globeDrag.lastClient = { x: event.clientX, y: event.clientY };
    globeDrag.movedDist += Math.hypot(stepDx, stepDy);
    globeDrag.vx = globeDrag.vx * 0.55 + stepDx * 0.45;
    globeDrag.vy = globeDrag.vy * 0.55 + stepDy * 0.45;
    globeDrag.lastMoveAt = performance.now();
    if (!globeDrag.dragging) {
      if (globeDrag.movedDist < 4) return;
      globeDrag.dragging = true;
      stopCameraMotion();
      clearWheelTimer();
      liveSvg.classList.add("is-dragging");
      try { liveSvg.setPointerCapture?.(event.pointerId); } catch {}
    }
    linkCamera = rotateLinkCamera(linkCamera, stepDx, stepDy, linkWidth);
    linkGoal = linkCamera;
    globeCustom = true;
    if (!globeDrag.raf) {
      globeDrag.raf = requestAnimationFrame(() => {
        globeDrag.raf = 0;
        if (!disposed && globeDrag.dragging) patchLiveGlobe(true);
      });
    }
  };

  const handleGlobalGlobeEnd = (event) => {
    if (!globeDrag.active && !globeDrag.dragging) return;
    if (globeDrag.pointerId != null && event?.pointerId != null && event.pointerId !== globeDrag.pointerId) return;
    const liveSvg = globeDrag.svgEl?.isConnected ? globeDrag.svgEl : bodyHost.querySelector("svg.a-lm");
    const wasDragged = globeDrag.dragging && globeDrag.movedDist >= 4;
    const pointerId = globeDrag.pointerId;
    const vx = globeDrag.vx, vy = globeDrag.vy;
    const recentMove = performance.now() - globeDrag.lastMoveAt < 80;
    globeDrag.active = false;
    globeDrag.dragging = false;
    globeDrag.pointerId = null;
    globeDrag.svgEl = null;
    cancelAnimationFrame(globeDrag.raf);
    globeDrag.raf = 0;
    liveSvg?.classList?.remove("is-dragging");
    if (liveSvg && pointerId != null) {
      try { liveSvg.releasePointerCapture?.(pointerId); } catch {}
    }
    if (wasDragged) {
      suppressNextGlobeClick = true;
      setTimeout(() => { suppressNextGlobeClick = false; }, 65);
      if (allowMotion() && recentMove && Math.hypot(vx, vy) > 1.35) {
        coastGlobeInertia(Math.max(-24, Math.min(24, vx)), Math.max(-18, Math.min(18, vy)));
      } else {
        patchLiveGlobe(false);
      }
    }
  };

  window.addEventListener("pointermove", handleGlobalGlobeMove, { signal: ctx.signal });
  window.addEventListener("pointerup", handleGlobalGlobeEnd, { signal: ctx.signal });
  window.addEventListener("pointercancel", handleGlobalGlobeEnd, { signal: ctx.signal });

  listen(document, "keydown", (event) => {
    if (event.key !== "Escape") return;
    if (document.querySelector("dialog[open], .a-tooltip")) return;
    const scopeEl = cardEl || bodyHost;
    if (!scopeEl.contains(event.target) && !scopeEl.contains(document.activeElement)) return;
    if (focusedItemKey || fleetPinnedNodeID || fleetHoverGroup) {
      clearFleetHoverTimer();
      focusedItemKey = "";
      fleetPinnedNodeID = "";
      fleetHoverGroup = "";
      fleetHoverKeepsInspector = false;
      syncRailPeerHover("");
      paint(true);
    } else if (globeCustom) {
      resetGlobeView();
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

  const patchStandaloneInspectorOnly = () => {
    if (!isStandalonePage) return;
    const host = bodyHost.querySelector('[data-refresh-key="topo-inspector"]');
    if (!host) return;
    const fresh = renderStandaloneInspector(lastFilteredGroups, lastActiveGroup, lastFleetActive, lastFleetModel, lastEffectiveDomainTitle);
    patch(host, fresh);
  };

  const patchStandaloneRailOnly = () => {
    if (!isStandalonePage) return;
    const host = bodyHost.querySelector('[data-refresh-key="topo-rail-shell"]');
    if (!host || !lastFilteredGroups.length) return;
    const fresh = renderVerticalGroupRail(lastFilteredGroups, {
      activeGroup: lastActiveGroup,
      onSelect: (gName) => setTopoSelectedGroup(gName, { showOverview: false }),
      keyPrefix: "topo",
      metricDir: topoMetricDir,
      includeFleetOverview: lastFilteredGroups.length > 1,
      fleetActive: lastFleetActive,
      onSelectFleet: () => setTopoSelectedGroup(topoSelectedGroup, { persist: false, showOverview: true }),
      showGroupColors: true,
      onHoverGroup: setFleetHover,
    });
    patch(host, fresh);
  };

  const bindGlobeInteractions = (svg) => {
    listen(svg, "pointerdown", (event) => {
      if (event.pointerType === "mouse" && event.button !== 0) return;
      if (event.target.closest?.(".a-lm-overlay-tools")) return;
      clearFleetHoverTimer();
      stopCameraMotion();
      globeDrag = {
        active: true,
        dragging: false,
        movedDist: 0,
        pointerId: event.pointerId,
        lastClient: { x: event.clientX, y: event.clientY },
        vx: 0,
        vy: 0,
        lastMoveAt: performance.now(),
        svgEl: event.currentTarget || svg,
        raf: 0,
      };
    });

    listen(svg, "wheel", (event) => {
      if (!event.ctrlKey && !event.metaKey && !event.shiftKey) return;
      event.preventDefault();
      zoomGlobeBy(event.deltaY < 0 ? 1.18 : 1 / 1.18, { animate: false });
    }, { passive: false });

    listen(svg, "dblclick", (event) => {
      if (event.target.closest?.("[data-lm-key], [data-lm-group], [data-lm-node], [data-lm-cluster]")) return;
      if (!linkCamera || !linkBaseCamera) return;
      if (linkCamera.radius >= linkBaseCamera.radius * 2.05) resetGlobeView();
      else zoomGlobeBy(1.38, { animate: true });
    });

    if (lastFleetActive) {
      // Velocity-aware hover-intent via pointermove: ignores brisk sweeps across
      // the globe and node/cluster targets, spotlighting only when cursor settles.
      listen(svg, "pointermove", (event) => {
        if (globeDrag.active || globeDrag.dragging) return;
        const now = performance.now();
        const dt = Math.max(1, now - lastGlobePointer.at);
        const speed = Math.hypot(event.clientX - lastGlobePointer.x, event.clientY - lastGlobePointer.y) / dt;
        lastGlobePointer = { x: event.clientX, y: event.clientY, at: now };
        if (event.target.closest?.("[data-lm-node], [data-lm-cluster]")) {
          setFleetHover("");
          return;
        }
        const arc = event.target.closest?.("[data-lm-group]");
        const nextHover = arc ? (arc.getAttribute("data-lm-group") || "") : "";
        if (!nextHover) {
          setFleetHover("");
          return;
        }
        const fastSweep = speed > 0.45 && nextHover !== fleetHoverGroup;
        setFleetHover(nextHover, { delay: 165, resetTimer: fastSweep });
      });
      listen(svg, "pointerleave", () => {
        clearFleetHoverTimer();
        if (fleetHoverGroup && !globeDrag.dragging) setFleetHover("", { immediate: true });
      });
      listen(svg, "click", (event) => {
        if (suppressNextGlobeClick) { suppressNextGlobeClick = false; return; }
        const chipEl = event.target.closest?.(".a-lm-cluster-chip[data-lm-cluster]");
        if (chipEl) {
          zoomToCluster(chipEl.getAttribute("data-lm-cluster") || "");
          return;
        }
        const nodeEl = event.target.closest?.("[data-lm-node]");
        if (nodeEl) {
          clearFleetHoverTimer();
          const id = nodeEl.getAttribute("data-lm-node") || "";
          fleetPinnedNodeID = fleetPinnedNodeID === id ? "" : id;
          fleetHoverGroup = "";
          fleetHoverKeepsInspector = false;
          syncRailPeerHover("");
          patchLiveGlobe(false);
          patchStandaloneInspectorOnly();
          patchStandaloneRailOnly();
          return;
        }
        const arc = event.target.closest?.("[data-lm-group]");
        if (arc) {
          const gName = arc.getAttribute("data-lm-group") || "";
          if (gName) setTopoSelectedGroup(gName, { showOverview: false });
          return;
        }
        const clusterEl = event.target.closest?.("[data-lm-cluster]");
        if (clusterEl) {
          zoomToCluster(clusterEl.getAttribute("data-lm-cluster") || "");
          return;
        }
        if (fleetPinnedNodeID) {
          fleetPinnedNodeID = "";
          patchLiveGlobe(false);
          patchStandaloneInspectorOnly();
          patchStandaloneRailOnly();
        }
      });
      listen(svg, "keydown", (event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        const clusterEl = event.target.closest?.("[data-lm-cluster]");
        if (clusterEl) {
          event.preventDefault();
          zoomToCluster(clusterEl.getAttribute("data-lm-cluster") || "");
          return;
        }
        const nodeEl = event.target.closest?.("[data-lm-node]");
        if (nodeEl) {
          event.preventDefault();
          const id = nodeEl.getAttribute("data-lm-node") || "";
          fleetPinnedNodeID = fleetPinnedNodeID === id ? "" : id;
          patchLiveGlobe(false);
          patchStandaloneInspectorOnly();
          patchStandaloneRailOnly();
          return;
        }
        const arc = event.target.closest?.("[data-lm-group]");
        if (arc) {
          event.preventDefault();
          const gName = arc.getAttribute("data-lm-group") || "";
          if (gName) setTopoSelectedGroup(gName, { showOverview: false });
        }
      });
    } else {
      listen(svg, "click", (event) => {
        if (suppressNextGlobeClick) { suppressNextGlobeClick = false; return; }
        const item = event.target.closest?.("[data-lm-key]");
        if (item) toggleFocus(item.getAttribute("data-lm-key"));
        else if (focusedItemKey) toggleFocus(focusedItemKey);
      });
      listen(svg, "keydown", (event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        const item = event.target.closest?.("[data-lm-key]");
        if (!item) return;
        event.preventDefault();
        toggleFocus(item.getAttribute("data-lm-key"));
      });
    }
  };

  // ==================== Shared Group Action & Rail Renderers ====================

  const railProbeButton = (group) => {
    if (!canExecuteProbes()) return null;
    const act = (kind) => (event) => void probeController.mutatePair(group.raw, event.currentTarget, kind);
    if (group.running) return button("停止", act("stop"), { className: "a-topo-mini-btn", "data-refresh-key": `stop-${group.id}` });
    return button("立即探测", act("run"), { className: "a-topo-mini-btn", disabled: !group.availability.ready, "data-refresh-key": `run-${group.id}` });
  };

  const renderGroupActionButtons = (group, { includeDelete = true } = {}) => {
    if (!canExecuteProbes()) return [];
    const items = [
      group.running
        ? button("停止", (e) => void probeController.mutatePair(group.raw, e.currentTarget, "stop"), {
            className: "a-button a-topo-op-btn",
            title: "停止当前探测",
            "data-refresh-key": `stop-${group.id}`,
          })
        : button("探测", (e) => void probeController.mutatePair(group.raw, e.currentTarget, "run"), {
            className: "a-button a-topo-op-btn",
            tone: group.availability.ready ? "primary" : "",
            disabled: !group.availability.ready,
            title: group.availability.reason || "立即发起一次手动探测",
            "data-refresh-key": `run-${group.id}`,
          }),
      button(group.autoEnabled ? "暂停" : "开启", (e) => void probeController.mutatePair(group.raw, e.currentTarget, "toggle"), {
        className: "a-button a-topo-op-btn",
        title: group.autoEnabled ? "暂停自动探测" : "开启自动探测",
        "data-refresh-key": `toggle-${group.id}`,
      }),
      button("编辑", () => probeController.openEdit(group.raw), {
        className: "a-button a-topo-op-btn",
        title: "编辑监测组配置",
        "data-refresh-key": `edit-${group.id}`,
      }),
    ];
    if (includeDelete) {
      items.push(button("删除", (e) => void probeController.mutatePair(group.raw, e.currentTarget, "delete"), {
        className: "a-button a-topo-op-btn",
        tone: "danger",
        title: "删除该监测组",
        "data-refresh-key": `delete-${group.id}`,
      }));
    }
    return items;
  };

  const renderDashboardRightRail = (group) => {
    const subject = railSubject(group, focusedItemKey, "both");
    const sampledAt = group.overall.sampledAt;
    const foot = [
      sampledAt ? railText("sampled", "a-con-note a-con-link-time", date(sampledAt)) : null,
      focusedItemKey ? button("取消聚焦", () => toggleFocus(focusedItemKey), { className: "a-topo-mini-btn", "data-refresh-key": "topo-unfocus" }) : null,
      focusedItemKey ? null : railProbeButton(group),
    ];
    return node("div", { className: "a-con-link-rail", "data-refresh-key": "topo-inspect" },
      node("div", { className: "a-con-link-head", "data-refresh-key": "head" }, subject.head),
      subject.rows,
      focusedItemKey ? null : railTrace(group),
      node("div", { className: "a-con-link-foot", "data-refresh-key": "foot" }, foot));
  };

  const renderVerticalGroupRail = (groups, {
    activeGroup,
    onSelect,
    keyPrefix,
    metricDir = "both",
    includeFleetOverview = false,
    fleetActive = false,
    onSelectFleet = null,
    showGroupColors = false,
    onHoverGroup = null,
  }) => {
    const rows = [];
    if (includeFleetOverview) {
      rows.push(node("button", {
        type: "button",
        className: `a-con-link-item is-overview${fleetActive ? " is-active" : ""}`,
        role: "tab",
        "aria-selected": String(fleetActive),
        "data-refresh-key": `${keyPrefix}-overview`,
        onclick: () => onSelectFleet?.(),
      },
      node("span", { className: "a-con-link-item-main" },
        node("span", { className: `a-topo-dot ${fleetActive ? "is-accent" : "is-ok"}`, "aria-hidden": "true" }),
        node("span", { className: "a-con-link-item-name" }, `同图全景 (${groups.length})`),
      ),
      node("span", { className: "a-con-link-item-metric" }, "全景")));
    }
    const pinnedSlotMap = new Map();
    if (lastFleetActive && fleetPinnedNodeID) {
      let slot = 0;
      for (const g of groups) {
        if ((g.nodes || []).some(n => n.agentID === fleetPinnedNodeID)) {
          pinnedSlotMap.set(g.groupName, slot++);
        }
      }
    }
    groups.forEach((group) => {
      const active = !fleetActive && Boolean(activeGroup && group.id === activeGroup.id);
      const status = group.overall.statusBadge;
      const metric = groupMetricText(group, metricDir);
      const pinnedSlot = pinnedSlotMap.has(group.groupName) ? pinnedSlotMap.get(group.groupName) : null;
      const rowBtn = node("button", {
        type: "button",
        className: `a-con-link-item${active ? " is-active" : ""}${fleetHoverGroup === group.groupName ? " is-peer-hover" : ""}`,
        role: "tab",
        "aria-selected": String(active),
        "data-group-name": group.groupName,
        "data-refresh-key": `${keyPrefix}-item-${group.id}`,
        onclick: () => onSelect(group.groupName),
      },
      node("span", { className: "a-con-link-item-main" },
        showGroupColors
          ? renderStatusDot(group, {
              active: active || fleetHoverGroup === group.groupName,
              pinnedSlot,
            })
          : node("span", { className: `a-topo-dot is-${status.tone || "idle"}`, "aria-hidden": "true" }),
        node("span", { className: "a-con-link-item-name" }, group.groupName),
      ),
      node("span", { className: `a-con-link-item-metric${status.tone ? ` is-${status.tone}` : ""}` }, metric));
      if (onHoverGroup) {
        listen(rowBtn, "pointerenter", () => onHoverGroup(group.groupName));
        listen(rowBtn, "pointerleave", () => onHoverGroup(""));
      }
      rows.push(rowBtn);
    });

    const listEl = node("div", {
      className: "a-con-link-list",
      role: "tablist",
      "aria-orientation": "vertical",
      "aria-label": "监测分组列表",
      "data-refresh-key": `${keyPrefix}-rail-list`,
    }, rows);

    if (onHoverGroup) {
      listen(listEl, "pointerleave", () => onHoverGroup("", { immediate: true }));
    }

    listen(listEl, "keydown", (event) => {
      if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
      const items = [...listEl.querySelectorAll('[role="tab"]')];
      if (!items.length) return;
      const cur = items.indexOf(event.target.closest?.('[role="tab"]'));
      if (cur < 0) return;
      event.preventDefault();
      let nextIdx = cur;
      if (event.key === "ArrowDown") nextIdx = Math.min(items.length - 1, cur + 1);
      else if (event.key === "ArrowUp") nextIdx = Math.max(0, cur - 1);
      else if (event.key === "Home") nextIdx = 0;
      else if (event.key === "End") nextIdx = items.length - 1;
      const target = items[nextIdx];
      target?.focus({ preventScroll: true });
      target?.click();
    });

    return node("div", { className: "a-topo-rail-shell", "data-refresh-key": `${keyPrefix}-rail-shell` }, listEl);
  };

  const renderCompactGroupSelect = (groups, {
    activeGroup,
    onSelect,
    keyPrefix,
    metricDir = "both",
    includeFleetOverview = false,
    fleetActive = false,
    onSelectFleet = null,
  }) => {
    const options = [];
    if (includeFleetOverview) options.push(["__fleet__", `同图全景 (${groups.length} 组)`]);
    for (const g of groups) {
      options.push([g.groupName, `${g.groupName} · ${groupMetricText(g, metricDir)}`]);
    }
    const currentVal = fleetActive ? "__fleet__" : (activeGroup?.groupName || groups[0]?.groupName || "");
    return node("div", { className: "a-con-link-picker", "data-refresh-key": `${keyPrefix}-picker` },
      makeNativeSelect({
        name: `${keyPrefix}_group_picker`,
        ariaLabel: "选择监测分组",
        options,
        value: currentVal,
        refreshKey: `${keyPrefix}-picker-select`,
        className: "a-con-link-select-field",
        onChange: (val) => {
          if (val === "__fleet__") onSelectFleet?.();
          else onSelect(val);
        },
      }),
    );
  };

  const renderGlobeOverlayTools = ({ compact = false } = {}) => {
    const pct = (linkCamera && linkBaseCamera)
      ? Math.max(25, Math.min(800, Math.round((linkCamera.radius / Math.max(1, linkBaseCamera.radius)) * 100)))
      : 100;
    const modified = globeCustom || (linkCamera && linkBaseCamera && !sameLinkCamera(linkCamera, linkBaseCamera));
    return node("div", {
      className: `a-lm-overlay-tools${compact ? " is-compact" : ""}`,
      role: "group",
      "aria-label": "3D 地球视角控制",
      "data-refresh-key": "lm-zoom-tools",
    },
      button("−", () => zoomGlobeBy(1 / 1.28, { animate: true }), { className: "a-button", title: "缩小视角", "data-refresh-key": "lm-zoom-out" }),
      button(modified ? `重置 ${pct}%` : `${pct}%`, () => resetGlobeView(), {
        className: "a-button",
        title: "恢复默认视角",
        disabled: !modified && !focusedItemKey && !fleetPinnedNodeID,
        "data-refresh-key": "lm-zoom-reset",
      }),
      button("+", () => zoomGlobeBy(1.28, { animate: true }), { className: "a-button", title: "放大视角", "data-refresh-key": "lm-zoom-in" }),
    );
  };

  // ==================== Dashboard 3-Rail Console Link Deck ====================

  const renderDashboardLinkDeck = (groups, activeGroup, renderWidth, isCompact) => {
    const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
    const wide = (window.innerWidth || 1440) >= 1800;
    const leftRailPx = wide ? 17.5 * rem : 16 * rem;
    const rightRailPx = wide ? 15 * rem : 14 * rem;
    const mapWidth = !isCompact ? Math.max(300, Math.round(renderWidth - leftRailPx - rightRailPx)) : renderWidth;
    const sizeOpts = isCompact
      ? { minHeight: 220, maxHeight: 260, ratio: 0.62 }
      : { minHeight: 260, maxHeight: 320, ratio: 0.42 };

    lastFleetActive = false;
    lastActiveGroup = activeGroup;
    aimGlobe({
      fleet: false,
      groups,
      activeGroup,
      width: mapWidth,
      sizeOpts,
      scopeSig: `dash:${activeGroup.id}`,
    });

    const map = drawCurrentGlobe(linkGlide !== 0);
    bindGlobeInteractions(map);

    const stage = node("div", { className: "a-lm-stage", "data-refresh-key": "lm-stage" },
      node("div", { className: "a-lm-stage-head", "data-refresh-key": "lm-dash-head" },
        node("span", { className: "a-lm-stage-title", "data-refresh-key": "lm-dash-badge" }, activeGroup.routeText),
        renderGlobeOverlayTools({ compact: true }),
      ),
      map,
      node("span", { className: "a-lm-drag-hint", "aria-hidden": "true" }, "拖拽旋转 · Ctrl+滚轮缩放"),
    );

    const leftNav = isCompact
      ? renderCompactGroupSelect(groups, { activeGroup, onSelect: selectDashboardGroup, keyPrefix: "dash" })
      : renderVerticalGroupRail(groups, { activeGroup, onSelect: selectDashboardGroup, keyPrefix: "dash", showGroupColors: true });

    return node("div", {
      className: `a-con-link${isCompact ? " is-compact" : ""}`,
      "data-refresh-key": `single-stage-${activeGroup.id}`,
      "data-group-name": activeGroup.groupName,
    }, leftNav, stage, renderDashboardRightRail(activeGroup));
  };

  // ==================== Standalone #topology 3D Globe Console ====================

  const renderHistorySection = (group, metricDir, gradPrefix, widthOverride = 0) => {
    const chart = renderProbeHistoryChart(group.history, {
      bidirectional: group.bidirectional,
      metricDir,
      width: Math.max(240, Math.min(540, widthOverride || chartWidth)),
      height: 92,
      gradKey: `${gradPrefix}-${group.id}`,
    });
    if (!chart) {
      return node("div", { className: "a-topo-history-empty", "data-refresh-key": `history-empty-${group.id}` },
        muted(group.history.length ? "已有 1 次采样记录，完成第 2 次采样后显示延迟走势" : "暂无历史采样趋势"),
      );
    }
    const legend = group.bidirectional && metricDir === "both"
      ? "实线 去程 · 虚线 回程 · 红点 异常"
      : `近 ${chart.sampleCount} 次 · 峰值 ${formatMs(chart.peakMS)}`;
    return node("div", { className: "a-topo-history-box", "data-refresh-key": `history-box-${group.id}` },
      keyed("svg", chart.svg),
      node("div", { className: "a-chart-meta" },
        muted(legend),
        muted(group.overall.sampledAt ? date(group.overall.sampledAt) : ""),
      ),
    );
  };

  const makeInspectorJumpRow = (g, keyPrefix, { pinnedSlot = null } = {}) => {
    const btn = node("button", {
      type: "button",
      className: "a-topo-inspector-jump",
      "data-refresh-key": `${keyPrefix}-${g.id}`,
      onclick: () => setTopoSelectedGroup(g.groupName, { showOverview: false }),
    },
      renderStatusDot(g, {
        active: fleetHoverGroup === g.groupName,
        pinnedSlot,
      }),
      node("span", { className: "a-topo-inspector-jump-name" }, g.groupName),
      node("span", { className: `a-topo-inspector-jump-val${g.overall.statusBadge.tone ? ` is-${g.overall.statusBadge.tone}` : ""}` }, groupMetricText(g, topoMetricDir)),
    );
    listen(btn, "pointerenter", () => setFleetHover(g.groupName, { delay: 180, keepInspector: true }));
    listen(btn, "pointerleave", () => setFleetHover(""));
    return btn;
  };

  const renderStandaloneInspector = (groups, activeGroup, fleetActive, fleetModel, domainTitle) => {
    // 1) Fleet panoramic mode: hovered group (from globe/rail) -> pinned node -> panoramic summary
    if (fleetActive) {
      if (fleetHoverGroup && !fleetHoverKeepsInspector) {
        const hovered = groups.find(g => g.groupName === fleetHoverGroup);
        if (hovered) {
          const subject = railSubject(hovered, "", topoMetricDir, { showName: true, detailed: true, showColorDot: true });
          const sampledStr = hovered.overall.sampledAt ? date(hovered.overall.sampledAt) : "";
          return node("div", { className: "a-topo-inspector", "data-refresh-key": "topo-inspector" },
            node("div", { className: "a-con-link-head", "data-refresh-key": "head" }, subject.head),
            subject.meta,
            subject.alert,
            node("p", { className: "a-con-note a-topo-inspector-route", "data-refresh-key": "route" }, hovered.routeText),
            node("div", { className: "a-topo-inspector-metrics", "data-refresh-key": "metrics" }, subject.rows),
            railTrace(hovered, sampledStr),
            node("div", { className: "a-con-link-foot", "data-refresh-key": "foot" },
              railText("hint", "a-con-note", "点击进入单链分析"),
            ),
          );
        }
      }

      if (fleetPinnedNodeID && fleetModel) {
        const pinned = (fleetModel.nodes || []).find(n => n.id === fleetPinnedNodeID);
        if (pinned) {
          const related = groups.filter(g => g.nodes.some(n => n.agentID === pinned.id));
          const relList = node("div", { className: "a-topo-inspector-list", "data-refresh-key": "nrels" },
            node("span", { className: "a-con-label" }, `关联链路 (${related.length})`),
            ...related.slice(0, 6).map((g, idx) => makeInspectorJumpRow(g, "njump", { pinnedSlot: idx })),
          );
          listen(relList, "pointerleave", () => {
            if (fleetHoverKeepsInspector) setFleetHover("", { immediate: true });
          });
          return node("div", { className: "a-topo-inspector", "data-refresh-key": "topo-inspector" },
            node("div", { className: "a-con-link-head", "data-refresh-key": "head" },
              keyedBadge(pinned.isOnline ? "在线" : "离线", pinned.isOnline ? "ok" : "bad", "nstate"),
              railText("nname", "a-con-link-name", pinned.name),
              pinned.ip ? node("code", { className: "a-dash-topo-code", "data-refresh-key": "nip" }, pinned.ip) : null,
            ),
            relList,
            node("div", { className: "a-con-link-foot", "data-refresh-key": "foot" },
              button("取消聚焦", () => {
                fleetPinnedNodeID = "";
                patchLiveGlobe(false);
                patchStandaloneInspectorOnly();
                patchStandaloneRailOnly();
              }, { className: "a-topo-mini-btn", "data-refresh-key": "node-unpin" }),
            ),
          );
        }
      }

      const fStats = fleetModel?.stats || { total: groups.length, hopCount: 0, alertCount: 0, avgFwdMS: null, avgRevMS: null };
      const alertGroups = groups.filter(g => g.overall.statusBadge.tone === "bad" || g.overall.statusBadge.tone === "warn").slice(0, 5);
      let alertList = null;
      if (alertGroups.length) {
        alertList = node("div", { className: "a-topo-inspector-list", "data-refresh-key": "falerts" },
          node("span", { className: "a-con-label" }, "需关注链路"),
          ...alertGroups.map(g => makeInspectorJumpRow(g, "falert")),
        );
        listen(alertList, "pointerleave", () => {
          if (fleetHoverKeepsInspector) setFleetHover("", { immediate: true });
        });
      }
      return node("div", { className: "a-topo-inspector", "data-refresh-key": "topo-inspector" },
        node("div", { className: "a-con-link-head", "data-refresh-key": "head" },
          railText("ftitle", "a-con-link-name", domainTitle),
          keyedBadge(fStats.alertCount > 0 ? `告警 ${fStats.alertCount}` : "正常", fStats.alertCount > 0 ? "bad" : "ok", "fstat"),
        ),
        readout("fnodes", "在线节点", [String(fleetModel?.onlineNodes || 0), `/${fleetModel?.totalNodes || 0}`], `${fStats.total} 组 · ${fStats.hopCount} 跳`),
        node("div", { className: "a-topo-inspector-metrics", "data-refresh-key": "fmetrics" },
          topoMetricDir !== "reverse" ? readout("ffwd", "去程均值", latencyParts(fStats.avgFwdMS), fStats.avgFwdMS != null ? "可达均值" : "待采样", "is-fwd") : null,
          topoMetricDir !== "forward" ? readout("frev", "回程均值", latencyParts(fStats.avgRevMS), fStats.avgRevMS != null ? "可达均值" : "待采样", "is-rev") : null,
        ),
        alertList || railText("fhint", "a-con-note", "所有监测链路运行平稳，悬停左侧或弧线查看实时详情"),
      );
    }

    // 2) Single-group inspection / focus mode
    if (!activeGroup) {
      return node("div", { className: "a-topo-inspector", "data-refresh-key": "topo-inspector" }, muted("请选择监测分组"));
    }

    const subject = railSubject(activeGroup, focusedItemKey, topoMetricDir, { showName: true, detailed: true, showColorDot: true });
    const sampledStr = activeGroup.overall.sampledAt ? date(activeGroup.overall.sampledAt) : "";
    const traceEl = focusedItemKey ? null : railTrace(activeGroup, sampledStr);
    const ops = renderGroupActionButtons(activeGroup, { includeDelete: true });

    return node("div", { className: "a-topo-inspector", "data-refresh-key": "topo-inspector" },
      node("div", { className: "a-con-link-head", "data-refresh-key": "head" },
        ...subject.head,
        focusedItemKey
          ? button("返回全链", () => toggleFocus(focusedItemKey), { className: "a-button a-topo-unfocus-pill", "data-refresh-key": "topo-unfocus" })
          : null,
      ),
      subject.meta,
      subject.alert,
      !focusedItemKey
        ? node("p", { className: "a-con-note a-topo-inspector-route", "data-refresh-key": "route" }, activeGroup.routeText)
        : null,
      !focusedItemKey && activeGroup.remark
        ? node("p", { className: "a-muted a-topo-remark", "data-refresh-key": "remark" }, activeGroup.remark)
        : null,
      node("div", { className: "a-topo-inspector-metrics", "data-refresh-key": "metrics" }, subject.rows),
      traceEl,
      ops.length || (!traceEl && sampledStr)
        ? node("div", { className: "a-topo-inspector-foot", "data-refresh-key": "foot" },
            !traceEl && sampledStr ? railText("sampled", "a-con-note a-con-link-time", sampledStr) : null,
            ops.length ? node("div", { className: "a-topo-inspector-ops", "data-refresh-key": "ops" }, ...ops) : null,
          )
        : null,
    );
  };

  // Lower analytical deck (single-group hops + history) with full-row hop focus.
  const renderStandaloneLowerDeck = (group, renderWidth, rem) => {
    const historyChartW = renderWidth >= 72 * rem
      ? Math.max(240, Math.round(renderWidth * 0.44 - 64))
      : Math.max(240, Math.round(renderWidth - 72));

    const hopRows = group.hops.map((hop, idx) => {
      const key = `hop:${idx}`;
      const selected = focusedItemKey === key;
      return [
        node("button", {
          type: "button",
          className: `a-con-jump a-topo-hop-pick${selected ? " is-active" : ""}`,
          "aria-pressed": String(selected),
          "data-hop-idx": String(idx),
          onclick: (e) => { e.stopPropagation(); toggleFocus(key); },
        }, `第 ${hop.hopIndex + 1} 跳`),
        `${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name}`,
        node("span", { className: "a-topo-addr-cell" }, `${hop.from.address || "—"} → ${hop.to.address || "—"}`),
        node("span", { className: "a-topo-metric-cell" }, formatDirBadgeMetric(hop.forward, { running: group.running, ready: hop.ready })),
        node("span", { className: "a-topo-metric-cell" }, hop.bidirectional ? formatDirBadgeMetric(hop.reverse, { running: group.running, ready: hop.ready }) : "—"),
        badge(hop.unavailableReason || hop.statusLabel, hop.tone || ""),
      ];
    });

    const hopsTable = table(["跳段", "起止节点", "探测地址", "去程", "回程", "状态"], hopRows, {
      label: "逐跳分段明细",
      paginate: 20,
      motion: false,
      emptyText: "暂无跳段明细",
      rowKey: (_, idx) => `${group.id}-hop-${idx}`,
    });

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
        const errText = String(sample.error || "").trim();
        return {
          key: String(sample.id || sample.session_id || sample.sampled_at || idx),
          hasError: Boolean(errText),
          cells: [
            node("span", { className: "a-topo-time-cell" }, date(sample.sampled_at)),
            badge(sample.state === "succeeded" ? "完成" : sample.state === "canceled" ? "已取消" : "失败", tone),
            node("span", { className: "a-topo-metric-cell" }, fwd),
            node("span", { className: "a-topo-metric-cell" }, rev),
            errText || "—",
          ],
        };
      });
    const showHistorySummary = historyEntries.some(e => e.hasError);
    const historyCols = showHistorySummary ? ["采样时间", "状态", "去程", "回程", "摘要"] : ["采样时间", "状态", "去程", "回程"];
    const historyRows = historyEntries.map(e => (showHistorySummary ? e.cells : e.cells.slice(0, 4)));

    const hopsPanel = node("section", { className: "a-topo-deck-pane a-topo-hops-pane", "data-refresh-key": `deck-hops-${group.id}` },
      node("div", { className: "a-topo-deck-pane-head" },
        node("h3", {}, "逐跳明细"),
        muted("点击行高亮弧线"),
      ),
      hopsTable,
    );
    listen(hopsPanel, "click", (event) => {
      if (event.target.closest?.(".a-topo-hop-pick")) return;
      const tr = event.target.closest?.("tbody tr");
      const pick = tr?.querySelector?.(".a-topo-hop-pick[data-hop-idx]");
      const idx = pick?.getAttribute("data-hop-idx");
      if (idx != null) toggleFocus(`hop:${idx}`);
    });

    const historyPanel = node("section", { className: "a-topo-deck-pane", "data-refresh-key": `deck-hist-${group.id}` },
      node("div", { className: "a-topo-deck-pane-head" },
        node("h3", {}, "历史延迟"),
        muted(group.bidirectional ? "去/回双向" : "单向"),
      ),
      node("div", { className: "a-topo-focus-history" },
        renderHistorySection(group, topoMetricDir, "focus", historyChartW),
        table(historyCols, historyRows, {
          label: "最近采样记录",
          paginate: 6,
          motion: false,
          emptyText: "暂无历史采样记录",
          rowKey: (_, idx) => `${group.id}-${historyEntries[idx]?.key || idx}`,
        }),
      ),
    );

    return node("div", { className: "a-topo-lower-deck", "data-refresh-key": `lower-deck-${group.id}` },
      hopsPanel,
      historyPanel,
    );
  };

  // Single ultra-clean toolbar strip: hides zero-count filter pills and drops duplicate view switches.
  const buildStandaloneControlStrip = (groups, domainBuckets, effectiveDomain, domainStats) => {
    const domainOptions = [
      ["all", `全部域 (${groups.length})`],
      ...domainBuckets.map(d => [d.id, `${d.title} (${d.chains.length})`]),
    ];
    const domainSelect = makeNativeSelect({
      name: "topo_domain_select",
      ariaLabel: "按网络域筛选",
      options: domainOptions,
      value: effectiveDomain,
      refreshKey: "topo-domain-select",
      className: "a-topo-strip-select",
      onChange: (val) => setTopoDomainGroup(val),
    });

    const abnormalCount = domainStats.bad + domainStats.warn;
    const pausedCount = Math.max(0, domainStats.total - domainStats.autoEnabled);
    const filterDefs = [
      { id: "all", label: `全部 ${domainStats.total}`, show: true },
      { id: "abnormal", label: `异常 ${abnormalCount}`, show: abnormalCount > 0 || topoStatusFilter === "abnormal" },
      { id: "multihop", label: `多跳 ${domainStats.multiHop}`, show: domainStats.multiHop > 0 || topoStatusFilter === "multihop" },
      { id: "running", label: `探测中 ${domainStats.running}`, show: domainStats.running > 0 || topoStatusFilter === "running" },
      { id: "paused", label: `暂停 ${pausedCount}`, show: pausedCount > 0 || topoStatusFilter === "paused" },
    ].filter(def => def.show);

    const filterGroup = node("div", {
      className: "a-dash-topo-filter-group",
      role: "group",
      "aria-label": "监测分组状态筛选",
      "data-refresh-key": "topo-filter-group",
    }, ...filterDefs.map(def => button(def.label, () => setTopoFilter(def.id), {
      className: "a-dash-topo-filter-btn",
      "data-filter": def.id,
      "aria-pressed": topoStatusFilter === def.id ? "true" : "false",
      "data-refresh-key": `filter-${def.id}`,
    })));

    const dirSwitch = node("div", {
      className: "a-dash-topo-mode-switch",
      role: "group",
      "aria-label": "指标显示方向",
      "data-refresh-key": "topo-dir-switch",
    }, ...["both", "forward", "reverse"].map(dir => button(directionLabel(dir), () => setTopoMetricDir(dir), {
      className: `a-dash-topo-mode-btn${topoMetricDir === dir ? " is-active" : ""}`,
      "aria-pressed": topoMetricDir === dir ? "true" : "false",
      "data-topo-dir-btn": dir,
      "data-refresh-key": `dir-${dir}`,
    })));

    const sortOptions = [
      ["default", "默认顺序"],
      ["abnormal", "异常优先"],
      ["latency_desc", "延迟降序"],
      ["loss_desc", "丢包降序"],
      ["name_asc", "名称 A–Z"],
    ];
    const sortSelect = makeNativeSelect({
      name: "topo_sort_by",
      ariaLabel: "分组排序方式",
      options: sortOptions,
      value: topoSortBy,
      refreshKey: "topo-sort-select",
      className: "a-topo-strip-select",
      onChange: (val) => setTopoSort(val),
    });

    return node("div", { className: "a-topo-strip-bar", "data-refresh-key": "topo-controls" },
      node("div", { className: "a-topo-strip-left" },
        domainBuckets.length > 1 ? domainSelect : null,
        filterGroup,
      ),
      node("div", { className: "a-topo-strip-right" },
        dirSwitch,
        sortSelect,
      ),
    );
  };

  // ==================== Main Paint ====================

  const paint = (force = false) => {
    paintScheduled = false;
    paintForced = false;
    if (!canViewSurface() || disposed) return;
    if (!force && (globeDrag.dragging || wheelCommitTimer)) return;

    const states = getStates();
    const probesState = states.get("probes") || "";
    const probesData = getProbes();
    const rawPairs = Array.isArray(probesData?.pairs) ? probesData.pairs : [];
    const agents = getAgents();
    const orderIDs = getOrderIDs();

    const groups = canReadProbes()
      ? buildProbeGroupModels({ pairs: rawPairs, agents, orderIDs }).map((g, idx) => ({ ...g, colorSlot: idx }))
      : [];
    const stats = summarizeProbeGroups(groups);

    const measuredWidth = observedWidth >= 220 ? observedWidth : (bodyHost.clientWidth >= 220 ? bodyHost.clientWidth : 0);
    const fallbackWidth = Math.max(300, Math.min(1440, Math.round((window.innerWidth || 1280) - ((window.innerWidth || 1280) > 896 ? 260 : 32))));
    const renderWidth = Math.max(280, Math.round(measuredWidth > 0 ? measuredWidth : fallbackWidth));
    const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
    const isCompact = renderWidth < 60 * rem;

    const domainMetaModel = isStandalonePage ? buildProbeTopologyModel({ groups, metricDir: topoMetricDir }) : null;
    const domainBuckets = domainMetaModel?.domainGroups || [];
    const effectiveDomain = (isStandalonePage && topoDomainGroup !== "all" && domainBuckets.some(d => d.id === topoDomainGroup))
      ? topoDomainGroup
      : "all";
    const domainBucketObj = domainBuckets.find(d => d.id === effectiveDomain) || null;
    const domainTitle = domainBucketObj ? `${domainBucketObj.title}全景` : "全球链路全景";
    lastEffectiveDomainTitle = domainTitle;

    const domainScopedGroups = effectiveDomain === "all"
      ? groups
      : groups.filter(g => domainGroupForProbeGroup(g).id === effectiveDomain);
    const domainStats = summarizeProbeGroups(domainScopedGroups);

    const filteredGroups = isStandalonePage
      ? filterAndSortProbeGroups(domainScopedGroups, {
          statusFilter: topoStatusFilter,
          sortBy: topoSortBy,
          metricDir: topoMetricDir,
        })
      : groups;
    lastFilteredGroups = filteredGroups;

    const activeResolution = isStandalonePage
      ? resolveSelectedProbeGroup(filteredGroups, topoSelectedGroup)
      : resolveSelectedProbeGroup(groups, dashGroupName);
    const activeGroup = activeResolution.group;
    lastActiveGroup = activeGroup;

    if (activeGroup && focusedItemKey.startsWith("node:")) {
      const idx = Number(focusedItemKey.slice(5));
      if (!Number.isInteger(idx) || idx < 0 || idx >= activeGroup.nodes.length) focusedItemKey = "";
    } else if (activeGroup && focusedItemKey.startsWith("hop:")) {
      const idx = Number(focusedItemKey.slice(4));
      if (!Number.isInteger(idx) || idx < 0 || idx >= activeGroup.hops.length) focusedItemKey = "";
    } else if (!activeGroup) {
      focusedItemKey = "";
    }

    const fleetActive = isStandalonePage && topoMapShowAll && filteredGroups.length > 1;
    lastFleetActive = fleetActive;
    const fleetModel = fleetActive ? buildProbeTopologyModel({ groups: filteredGroups, metricDir: topoMetricDir }) : null;
    lastFleetModel = fleetModel;
    if (!fleetActive) {
      clearFleetHoverTimer();
      fleetHoverGroup = "";
      fleetHoverKeepsInspector = false;
      fleetPinnedNodeID = "";
    } else if (fleetPinnedNodeID && !(fleetModel?.nodes || []).some(n => n.id === fleetPinnedNodeID)) {
      fleetPinnedNodeID = "";
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
      renderWidth,
      isCompact,
      nextStatus,
      probesState,
      dashGroupName,
      topoMapShowAll,
      effectiveDomain,
      topoSelectedGroup,
      topoStatusFilter,
      topoSortBy,
      topoMetricDir,
      focusedItemKey,
      fleetPinnedNodeID,
      globeCustom,
      canExecuteProbes(),
      groups.map(g => ({
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

    const alertCount = stats.bad + stats.warn;
    const topBadges = [];
    if (isStandalonePage) {
      topBadges.push(
        keyedBadge(`${stats.total} 组`, "", "kpi-total"),
        alertCount > 0
          ? keyedBadge(`告警 ${alertCount}`, "bad", "kpi-bad")
          : keyedBadge("运行正常", "ok", "kpi-ok"),
        stats.running > 0 ? keyedBadge(`探测中 ${stats.running}`, "warn", "kpi-run") : null,
      );
    } else if (alertCount > 0) {
      topBadges.push(keyedBadge(`告警 ${alertCount}`, "bad", "head-alert"));
    }
    patchChildren(headerBadges, topBadges);

    const topActionNodes = [];
    if (!isStandalonePage && canViewSurface()) {
      topActionNodes.push(node("a", { className: "a-con-jump", href: "#topology", "data-refresh-key": "open-topology-page" }, "拓扑详情"));
    }
    const blank = !groups.length && !waiting && !failed && !denied;
    if (canExecuteProbes() && (isStandalonePage || blank)) {
      topActionNodes.push(button("+ 新建监测组", () => probeController.openCreate(), { className: "a-button a-topo-mini-btn", tone: "primary", "data-refresh-key": "add-probe-group" }));
    }
    topActionNodes.push(keyed("head-status", statusNode));
    patchChildren(headerActions, topActionNodes);

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

    // ==================== #dashboard Mode ====================
    if (!isStandalonePage) {
      if (activeGroup) bodyChildren.push(renderDashboardLinkDeck(groups, activeGroup, renderWidth, isCompact));
      patchChildren(bodyHost, bodyChildren);
      const pickerEl = bodyHost.querySelector('select[name="dash_group_picker"]');
      if (pickerEl && document.activeElement !== pickerEl && activeGroup) pickerEl.value = activeGroup.groupName;
      const scrollKey = `dash:${activeGroup?.id || ""}`;
      if (activeGroup && scrollKey !== lastScrolledGroupKey) {
        lastScrolledGroupKey = scrollKey;
        bodyHost.querySelector('.a-con-link-item.is-active')?.scrollIntoView?.({ block: "nearest" });
      }
      return;
    }

    // ==================== #topology Standalone Console Mode ====================
    bodyChildren.push(buildStandaloneControlStrip(groups, domainBuckets, effectiveDomain, domainStats));

    if (!filteredGroups.length) {
      bodyChildren.push(keyed("filter-empty", node("div", { className: "a-topo-empty-box" },
        empty("当前筛选条件下无匹配的监测分组。"),
        node("div", { className: "a-actions" },
          topoStatusFilter !== "all"
            ? button("显示全部状态", () => setTopoFilter("all"), { className: "a-button", "data-refresh-key": "reset-topo-filter" })
            : null,
          effectiveDomain !== "all"
            ? button("显示全部域", () => setTopoDomainGroup("all"), { className: "a-button", "data-refresh-key": "reset-topo-domain" })
            : null,
        ),
      )));
      patchChildren(bodyHost, bodyChildren);
      return;
    }

    const wide = (window.innerWidth || 1440) >= 1800;
    const leftRailPx = wide ? 17.5 * rem : 15.75 * rem;
    const rightRailPx = wide ? 18.5 * rem : 17 * rem;
    const mapWidth = !isCompact ? Math.max(340, Math.round(renderWidth - leftRailPx - rightRailPx)) : renderWidth;
    const sizeOpts = isCompact
      ? { minHeight: 250, maxHeight: 310, ratio: 0.74 }
      : fleetActive
        ? (wide
            ? { minHeight: 520, maxHeight: 620, ratio: 0.58 }
            : { minHeight: 470, maxHeight: 550, ratio: 0.80 })
        : (wide
            ? { minHeight: 380, maxHeight: 450, ratio: 0.46 }
            : { minHeight: 350, maxHeight: 410, ratio: 0.62 });

    const scopeSig = fleetActive
      ? `fleet:${effectiveDomain}:${topoStatusFilter}:${filteredGroups.length}`
      : `single:${activeGroup?.id || "none"}`;

    aimGlobe({
      fleet: fleetActive,
      groups: filteredGroups,
      activeGroup: activeGroup || filteredGroups[0],
      width: mapWidth,
      sizeOpts,
      scopeSig,
    });

    const globeSvg = drawCurrentGlobe(linkGlide !== 0);
    if (globeSvg) bindGlobeInteractions(globeSvg);

    const extTargets = fleetActive ? extractPublicTargets(filteredGroups) : [];
    const extDock = extTargets.length
      ? node("div", { className: "a-topo-ext-dock", "data-refresh-key": "topo-ext-dock" },
          node("span", { className: "a-con-label a-topo-ext-dock-label" }, "公网出口"),
          ...extTargets.map((target) => {
            const chip = node("button", {
              type: "button",
              className: `a-topo-ext-chip${target.tone ? ` is-${target.tone}` : ""}`,
              "aria-label": `点击聚焦 ${target.groupName}`,
              "data-refresh-key": `ext-chip-${target.address}`,
              onclick: () => setTopoSelectedGroup(target.groupName, { showOverview: false }),
            },
              node("span", { className: "a-topo-ext-diamond", "aria-hidden": "true" }),
              node("span", { className: "a-topo-ext-addr" }, target.name),
              node("span", { className: "a-topo-ext-metric" }, target.metricText),
            );
            listen(chip, "pointerenter", () => setFleetHover(target.groupName));
            listen(chip, "pointerleave", () => setFleetHover(""));
            return chip;
          }),
        )
      : null;

    const onSelectOverview = () => {
      setTopoSelectedGroup(topoSelectedGroup, { persist: false, showOverview: true });
    };
    const onSelectGroup = (gName) => {
      setTopoSelectedGroup(gName, { showOverview: false });
    };

    const stageTitle = fleetActive
      ? domainTitle
      : (activeGroup ? `${activeGroup.groupName} · ${activeGroup.hopCount} 跳` : domainTitle);

    const backToPanoramaBtn = (!fleetActive && filteredGroups.length > 1)
      ? button("← 全景", onSelectOverview, {
          className: "a-button a-lm-back-btn",
          title: "返回同图全景",
          "data-refresh-key": "lm-back-panorama",
        })
      : null;

    const dragHintText = fleetActive
      ? "拖拽旋转 · Ctrl+滚轮缩放 · 点击 +N 展开近距集群"
      : "拖拽旋转 · Ctrl+滚轮缩放 · 点击弧线或节点聚焦";

    const globeStage = node("div", { className: "a-topo-globe-stage", "data-refresh-key": "topo-globe-stage" },
      node("div", { className: "a-lm-stage", "data-refresh-key": "lm-stage" },
        node("div", { className: "a-lm-stage-head", "data-refresh-key": "lm-topo-head" },
          backToPanoramaBtn,
          node("span", { className: "a-lm-stage-title", "data-refresh-key": "lm-topo-badge" }, stageTitle),
          renderGlobeOverlayTools({ compact: isCompact }),
        ),
        globeSvg,
        node("span", { className: "a-lm-drag-hint", "aria-hidden": "true" }, dragHintText),
      ),
      extDock,
    );

    const leftRail = isCompact
      ? renderCompactGroupSelect(filteredGroups, {
          activeGroup,
          onSelect: onSelectGroup,
          keyPrefix: "topo",
          metricDir: topoMetricDir,
          includeFleetOverview: filteredGroups.length > 1,
          fleetActive,
          onSelectFleet: onSelectOverview,
        })
      : renderVerticalGroupRail(filteredGroups, {
          activeGroup,
          onSelect: onSelectGroup,
          keyPrefix: "topo",
          metricDir: topoMetricDir,
          includeFleetOverview: filteredGroups.length > 1,
          fleetActive,
          onSelectFleet: onSelectOverview,
          showGroupColors: true,
          onHoverGroup: setFleetHover,
        });

    const rightInspector = renderStandaloneInspector(filteredGroups, activeGroup, fleetActive, fleetModel, domainTitle);

    bodyChildren.push(
      node("div", {
        className: `a-topo-console-grid${isCompact ? " is-compact" : ""}${fleetActive ? " is-panorama" : ""}`,
        "data-refresh-key": "topo-console-grid",
      }, leftRail, globeStage, rightInspector),
    );

    if (!fleetActive && activeGroup) {
      bodyChildren.push(renderStandaloneLowerDeck(activeGroup, renderWidth, rem));
    }

    patchChildren(bodyHost, bodyChildren);

    const syncVal = (selector, val) => {
      const el = bodyHost.querySelector(selector);
      if (el && document.activeElement !== el && el.value !== val) el.value = val;
    };
    syncVal('select[name="topo_sort_by"]', topoSortBy);
    syncVal('select[name="topo_domain_select"]', effectiveDomain);
    syncVal('select[name="topo_group_picker"]', fleetActive ? "__fleet__" : (activeGroup?.groupName || ""));

    const scrollKey = fleetActive ? "topo:fleet" : `topo:${activeGroup?.id || ""}`;
    if (scrollKey !== lastScrolledGroupKey) {
      lastScrolledGroupKey = scrollKey;
      bodyHost.querySelector('.a-con-link-item.is-active')?.scrollIntoView?.({ block: "nearest" });
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
      if (!isEdit || topoSelectedGroup === previousGroupName || !topoMapShowAll) {
        topoGroupTouched = true;
        topoSelectedGroup = nextName;
        patchFields.selected_group = nextName;
        if (!isEdit) topoMapShowAll = false;
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
    isStandalonePage ? "网络延迟监测拓扑" : "链路延迟",
    bodyHost,
    node("div", { className: "a-actions a-dash-topo-tools" }, headerBadges, headerActions),
  );
  cardEl = card || null;
  card?.classList?.add("a-dash-topo-card");
  if (card) card.classList.add(isStandalonePage ? "is-standalone" : "a-con-link-card");

  return {
    visible: Boolean(card),
    element: card,
    paint,
    schedulePaint,
    onProbeSaved,
    onProbeDeleted,
  };
}
