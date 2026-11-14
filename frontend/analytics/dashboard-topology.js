import { createSVGEl } from "./dashboard-panel.js";
import { renderPacificMapSVG } from "./dashboard-map.js";
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
  buildPeerProbeTags,
  buildProbePillLabel,
  buildProbeShortAlertLabel,
  clipLabel,
  createTopologyDefs,
  cubicPath,
  extendedCubicPath,
  flowDur,
  formatMapCityLabel,
  formatMs,
  isProbeUnreachable,
  matchesActiveSelection,
  placeCollisionFreePill,
  resolveNodeGeoPoint,
  selectTopAmbientBadProbeIDs,
  tonePalette,
  visualUnits,
} from "./dashboard-topo-shared.js";

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
  let lastModel = null;
  let paintScheduled = false;
  let paintForced = false;
  let disposed = false;

  ctx.signal.addEventListener("abort", () => {
    disposed = true;
    paintScheduled = false;
  }, { once: true });

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
    if (persist) {
      viewModeTouched = true;
      ctx.preferences?.patch?.("dashboard", { topo_view_mode: normalized });
    }
    if (viewMode === normalized) return;
    viewMode = normalized;
    applyViewModeButtons();
    if (repaint) paint(true);
  };
  const syncSavedTopologyPreferences = ({ repaint = false } = {}) => {
    if (disposed || !ctx.preferences?.get) return;
    const saved = ctx.preferences.get("dashboard");
    let changed = false;
    if (!viewModeTouched && VALID_VIEW_MODES.has(saved?.topo_view_mode) && saved.topo_view_mode !== viewMode) {
      viewMode = saved.topo_view_mode;
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
  const detailPageLink = !isStandalonePage && ctx.can("agents.read")
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

  const findSameCityMembers = (targetNode, model) => {
    if (!targetNode) return [];
    const targetGeo = resolveNodeGeoPoint(targetNode, 0);
    if (!targetGeo.matched) return [];
    return (model?.nodes || []).filter((candidate) => {
      const geo = resolveNodeGeoPoint(candidate, 0);
      return geo.matched && Math.hypot(geo.x - targetGeo.x, geo.y - targetGeo.y) < 4.5;
    });
  };

  const selectPinnedEntity = (entityKey) => {
    pinnedKey = entityKey;
    hoveredKey = "";
    syncHighlight();
    syncInspect();
  };

  /** Appends consistent same-city cluster + member switcher pills with active-state highlight. */
  const appendSameCitySwitchers = (chips, members, currentKey) => {
    if (!Array.isArray(members) || members.length <= 1) return;
    const clusterKey = `cluster:${members.map((m) => m.id).join(",")}`;
    chips.push(button(
      `同城聚合 (${members.length})`,
      () => selectPinnedEntity(clusterKey),
      {
        className: `a-dash-topo-cluster-btn ${currentKey === clusterKey ? "is-active" : ""}`,
        "data-cluster-key": clusterKey,
      },
    ));
    members.forEach((member) => {
      const memberKey = `node:${member.id}`;
      chips.push(button(
        member.name || member.id,
        () => selectPinnedEntity(memberKey),
        {
          className: `a-dash-topo-cluster-btn ${currentKey === memberKey ? "is-active" : ""}`,
          "data-cluster-member": member.id,
          "data-cluster-key": memberKey,
          title: `切换聚焦到 ${member.name || member.id}`,
        },
      ));
    });
  };

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
        chips.push(muted(`24h ${model.ingress.clientIPs} IP · ${model.ingress.clientFlows} 流`));
      }
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, "入站流量")],
        detail: chips,
      };
    }

    if (key === "hub:egress") {
      return {
        primary: [node("span", { className: "a-dash-topo-inspect-title" }, "出站流量")],
        detail: [
          muted(`实时出站 ↑ ${bytes(model.egress.rateBPS)}/s`),
          muted(`本月发送 ${bytes(model.egress.monthlyBytes)}`),
          muted(`${model.onlineNodes}/${model.totalNodes} 节点在线`),
        ],
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
        muted(`${related.length} 条探测链路`),
      ];
      const priority = pickPriorityProbe(related);
      if (canOpenProbes && onOpenProbes && priority) {
        chips.push(button("链路监控", () => onOpenProbes(priority.id), { className: "a-button" }));
      }
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
        const rawCity = formatMapCityLabel(primaryNode.name, primaryNode);
        const cityName = /[·|]/.test(String(primaryNode.name || "")) ? rawCity : (region?.name || rawCity.replace(/-[a-z0-9]{1,3}$/i, ""));
        const onlineCount = members.filter((m) => m.isOnline).length;
        const totalRx = members.reduce((s, m) => s + (m.rxBPS || 0), 0);
        const totalTx = members.reduce((s, m) => s + (m.txBPS || 0), 0);
        const memberSet = new Set(memberIDs);
        const relatedProbes = model.probeLinks.filter((p) => memberSet.has(p.from) || (!p.isExternal && memberSet.has(p.to)));
        const priorityProbe = pickPriorityProbe(relatedProbes);

        const chips = [
          badge(`${onlineCount}/${members.length} 在线`, onlineCount === members.length ? "ok" : onlineCount > 0 ? "warn" : "bad"),
          muted(`合计 ↓ ${bytes(totalRx)}/s · ↑ ${bytes(totalTx)}/s`),
        ];
        appendSameCitySwitchers(chips, members, key);
        if (canOpenProbes && onOpenProbes && relatedProbes.length) {
          chips.push(button(`关联链路 (${relatedProbes.length})`, () => onOpenProbes(priorityProbe?.id || "")));
        }
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
          muted(`↓ ${bytes(item.rxBPS)}/s · ↑ ${bytes(item.txBPS)}/s`),
          muted(`本月 ${bytes(item.monthlyUsedBytes)}`),
        ];
        if (item.activeEngines.length) chips.push(muted(`内核 ${item.activeEngines.join(" / ")}`));
        if (item.blocked) chips.push(badge("已超限阻断", "bad"));
        if (viewMode === "map") {
          appendSameCitySwitchers(chips, findSameCityMembers(item, model), key);
        }
        if (canOpenProbes && onOpenProbes && relatedProbes.length) {
          chips.push(button(`关联链路 (${relatedProbes.length})`, () => onOpenProbes(priorityProbe?.id || "")));
        }
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
      muted(`↓ ${bytes(model.ingress.rateBPS)}/s · ↑ ${bytes(model.egress.rateBPS)}/s`),
      model.hiddenNodeCount > 0 ? muted(`显示前 ${model.nodes.length}/${model.totalNodes} 节点`) : null,
      model.totalProbeLinks > 0 ? muted(`探测链路 ${model.totalProbeLinks}`) : null,
    ].filter(Boolean);
    return {
      primary: [node("span", { className: "a-dash-topo-inspect-title" }, "实时流量汇总")],
      detail: parts,
    };
  };

  const syncInspect = () => {
    const focusedClusterKey = inspectDetail.contains(document.activeElement)
      ? document.activeElement?.getAttribute?.("data-cluster-key") || ""
      : "";
    const { primary, detail } = resolveKeyDescription(activeKey(), lastModel);
    inspectPrimary.replaceChildren(...primary);
    inspectDetail.replaceChildren(...detail);
    if (focusedClusterKey) {
      const target = [...inspectDetail.querySelectorAll("[data-cluster-key]")].find((el) => el.getAttribute("data-cluster-key") === focusedClusterKey);
      target?.focus?.();
    }
  };

  const syncAmbientPillGeometry = (el, expanded) => {
    const want = expanded ? "true" : "false";
    if (el.getAttribute("data-expanded") === want) return;
    el.setAttribute("data-expanded", want);
    const cx = Number(el.getAttribute("data-cx") || 0);
    const cy = Number(el.getAttribute("data-cy") || 0);
    const compact = el.getAttribute("data-compact") === "1";
    const w = Number(el.getAttribute(expanded ? "data-full-w" : "data-short-w") || 64);
    const nextLabel = el.getAttribute(expanded ? "data-full-label" : "data-short-label") || "";
    const rectEl = el.querySelector("rect.a-dash-topo-probe-pill");
    if (rectEl) {
      rectEl.setAttribute("x", (cx - w / 2).toFixed(1));
      rectEl.setAttribute("width", w.toFixed(1));
    }
    const dotX = cx - w / 2 + (compact ? 8 : 10);
    const dotEl = el.querySelector(".a-dash-topo-pill-dot");
    if (dotEl) {
      if (dotEl.tagName.toLowerCase() === "circle") {
        dotEl.setAttribute("cx", dotX.toFixed(1));
      } else {
        dotEl.setAttribute("d", `M ${(dotX - 2.3).toFixed(1)} ${(cy - 2.3).toFixed(1)} L ${(dotX + 2.3).toFixed(1)} ${(cy + 2.3).toFixed(1)} M ${(dotX + 2.3).toFixed(1)} ${(cy - 2.3).toFixed(1)} L ${(dotX - 2.3).toFixed(1)} ${(cy + 2.3).toFixed(1)}`);
      }
    }
    const textEl = el.querySelector("text.a-dash-topo-pill-text");
    if (textEl) {
      textEl.setAttribute("x", (dotX + (compact ? 6.5 : 7.5)).toFixed(1));
      if (textEl.textContent !== nextLabel) textEl.textContent = nextLabel;
    }
  };

  const syncHighlight = () => {
    const sel = activeKey();
    const svg = canvasHost.querySelector("svg.a-dash-topo-svg");
    if (svg) {
      svg.querySelectorAll("[data-topo-peers]").forEach((el) => {
        const peers = (el.getAttribute("data-topo-peers") || "").split(" ").filter(Boolean);
        const base = el.getAttribute("data-base-opacity") || "1";
        const match = matchesActiveSelection(peers, sel);
        const dimOpacity = el.getAttribute("data-dim-opacity") ?? (el.classList.contains("a-dash-topo-card-node") ? "0.35" : "0.14");
        const next = match === true ? "1" : match === false ? dimOpacity : base;
        if (el.getAttribute("opacity") !== next) el.setAttribute("opacity", next);
        el.classList.toggle("is-highlighted", match === true);
        if (el.getAttribute("data-ambient-alert") === "true") {
          syncAmbientPillGeometry(el, match === true);
        }
      });
      svg.querySelectorAll("[data-topo-entity]").forEach((el) => {
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
    const links = model?.probeLinks || [];
    if (!links.length) {
      probeMatrixHost.hidden = true;
      if (probeMatrixHost.childElementCount) probeMatrixHost.replaceChildren();
      return;
    }
    probeMatrixHost.hidden = false;

    const nodeNameByID = new Map((model.nodes || []).map((n) => [n.id, n.name || n.id]));
    const extNameByID = new Map((model.externalTargets || []).map((e) => [e.id, e.name || e.address || e.id]));
    const fullNameOf = (id) => {
      const s = String(id || "");
      if (s.startsWith("ext:")) return extNameByID.get(s) || s.slice(4);
      return nodeNameByID.get(s) || s;
    };
    const shortCardName = (id) => fullNameOf(id).replace(/^qch-tools-installed-/, "tools-");

    const badCount = links.filter((p) => p.tone === "bad" || p.tone === "warn").length;
    const meshCount = links.filter((p) => !p.isExternal).length;
    const extCount = links.length - meshCount;

    // Preserve saved "bad" preference while probes are empty/healthy so async loading never overwrites account state.
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
          probeFilterTouched = true;
          ctx.preferences?.patch?.("dashboard", { topo_probe_filter: f.id });
          if (probeFilter === f.id && effectiveFilter === f.id) return;
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
            const entityKey = `probe:${probe.id}`;
            const peers = [entityKey, `node:${probe.from}`, probe.isExternal ? probe.to : `node:${probe.to}`];
            const arrow = probe.bidirectional ? "↔" : "→";
            const routeTitle = `${shortCardName(probe.from)} ${arrow} ${shortCardName(probe.to)}`;
            const fullRouteTip = `${fullNameOf(probe.from)} ${arrow} ${fullNameOf(probe.to)}`;
            const cardEl = node(
              "button",
              {
                type: "button",
                className: "a-dash-topo-link-card",
                "data-refresh-key": `ledger-card-${probe.id}`,
                "data-tone": probe.tone || "idle",
                "data-topo-peers": peers.join(" "),
                title: `${fullRouteTip} · ${formatRttText(probe)} · ${formatLossBadge(probe)}`,
              },
              node(
                "div",
                { className: "a-dash-topo-link-top", "data-refresh-key": "top" },
                node("span", { className: "a-dash-topo-link-route", "data-refresh-key": "route" }, routeTitle),
                node("span", { className: "a-dash-topo-link-loss", "data-refresh-key": "loss" }, formatLossBadge(probe)),
              ),
              node(
                "div",
                { className: "a-dash-topo-link-metrics", "data-refresh-key": "metrics" },
                node("span", { className: "a-dash-topo-link-rtt", "data-refresh-key": "rtt" }, formatRttText(probe)),
                node("span", { className: "a-dash-topo-link-kind", "data-refresh-key": "kind" }, probe.isExternal ? "公网目标" : (probe.bidirectional ? "双向探测" : "单向探测")),
              ),
            );
            bindInteractive(cardEl, entityKey, { openProbeID: probe.id });
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

  const renderFlowCurve = ({ d, peers, baseOpacity, glowColor, glowWidth, stroke, strokeWidth, dash, markerEnd, pulseColor, pulseOpacity, pulseDur, photonColor, rateLabel, rateColor, labelPos, labelAnchor = "middle", labelLayer = null, labelOffsetY = -5, refreshKey }) => {
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
        style: pulseDur ? `--topo-dur: ${pulseDur}` : null,
        class: "a-dash-topo-flow-pulse",
      }));
    }
    if (photonColor && allowMotion()) {
      const photon = createSVGEl("circle", {
        "data-refresh-key": `${refreshKey}-photon`,
        r: "2.3",
        fill: "#ffffff",
        stroke: photonColor,
        "stroke-width": "1.35",
        opacity: "0.95",
      });
      photon.append(createSVGEl("animateMotion", {
        "data-refresh-key": `${refreshKey}-motion`,
        path: d,
        dur: pulseDur || "1.9s",
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

  const createProbePillGroup = ({ probe, cx, cy, pillW, fullW = pillW, pillH, label, shortLabel = label, pal, compact = false, peers, focusOnly = false, ambientAlert = false }) => {
    const entityKey = `probe:${probe.id}`;
    const hl = isHighlightedEdge(peers);
    const selected = activeKey() === entityKey;
    const expanded = !ambientAlert || hl === true || selected;
    const curW = expanded ? fullW : pillW;
    const curLabel = expanded ? label : shortLabel;
    const baseStr = focusOnly ? "0" : "1";
    const dimStr = focusOnly ? "0" : ambientAlert ? "0.18" : "0.22";
    const op = focusOnly ? (hl === true ? "1" : "0") : (hl === false ? dimStr : "1");
    const g = createSVGEl("g", {
      class: `a-dash-topo-probe-item ${focusOnly ? "a-dash-topo-focus-only" : ""} ${selected ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
      "data-refresh-key": `probe-pill-${probe.id}`,
      "data-probe-id": probe.id,
      "data-topo-peers": peers.join(" "),
      "data-base-opacity": baseStr,
      "data-dim-opacity": dimStr,
      ...(ambientAlert ? {
        "data-ambient-alert": "true",
        "data-expanded": expanded ? "true" : "false",
        "data-cx": cx.toFixed(1),
        "data-cy": cy.toFixed(1),
        "data-compact": compact ? "1" : "0",
        "data-short-w": pillW.toFixed(1),
        "data-full-w": fullW.toFixed(1),
        "data-short-label": shortLabel,
        "data-full-label": label,
      } : {}),
      opacity: op,
    });
    g.append(createSVGEl("title", {}, `${probe.name}: ${probe.metricLabel} (${probe.statusLabel}) — 点击打开链路监控`));
    g.append(createSVGEl("rect", {
      x: (cx - curW / 2).toFixed(1),
      y: (cy - pillH / 2).toFixed(1),
      width: curW.toFixed(1),
      height: pillH,
      rx: (pillH / 2).toFixed(1),
      fill: pal.fill,
      stroke: pal.border,
      "stroke-width": selected || hl === true ? "1.8" : "1.1",
      class: "a-dash-topo-probe-pill",
    }));

    const dotX = cx - curW / 2 + (compact ? 8 : 10);
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
    }, curLabel));

    bindInteractive(g, entityKey, { openProbeID: probe.id });
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

  const renderDesktopSVG = (width, model) => {
    const nodesCount = model.nodes.length;
    const isDense = nodesCount > 8 || model.probeLinks.length >= 10 || model.externalTargets.length >= 4;
    const vw = Math.max(isDense ? 1280 : 1080, Math.round(width));
    const padX = 14;
    const padTop = 32;
    const padBottom = 16;
    const nodeH = nodesCount > 12 ? 34 : nodesCount > 8 ? 44 : 52;
    const nodeGap = nodesCount > 12 ? 6 : nodesCount > 8 ? 11 : 18;
    const sideW = Math.round(Math.max(162, Math.min(184, vw * 0.142)));
    const nodeW = Math.round(Math.max(236, Math.min(278, vw * 0.215)));
    const leftX = padX;
    const rightX = vw - padX - sideW;
    const interior = Math.max(nodeW + 320, rightX - (leftX + sideW));
    const remaining = interior - nodeW;
    const leftGap = isDense
      ? Math.max(84, Math.min(114, Math.round(remaining * 0.165)))
      : Math.max(88, Math.min(116, Math.round(remaining * 0.175)));
    const nodeX = leftX + sideW + leftGap;
    const rightGap = Math.max(240, rightX - (nodeX + nodeW));

    const nodesStackH = nodesCount ? nodesCount * nodeH + (nodesCount - 1) * nodeGap : nodeH;

    const ingressCardH = model.ingress.clientIPs != null ? 108 : 88;
    const egressCardH = 82;
    const extItems = [...model.externalTargets];
    const extCardH = extItems.length >= 4 ? 44 : 52;
    const extGap = extItems.length >= 4 ? 10 : 12;
    const rightGapV = extItems.length >= 4 ? 22 : 20;
    const rightStackH = egressCardH + (extItems.length ? rightGapV + extItems.length * extCardH + (extItems.length - 1) * extGap : 0);

    const bodyH = Math.max(210, nodesStackH, rightStackH, ingressCardH);
    const totalH = padTop + bodyH + padBottom;
    const ingressY = Math.round(padTop + (bodyH - ingressCardH) / 2);
    const nodesStartY = Math.round(padTop + (bodyH - nodesStackH) / 2);

    const svg = createSVGEl("svg", {
      class: "a-dash-topo-svg",
      "data-refresh-key": "topo-svg",
      viewBox: `0 0 ${vw} ${totalH}`,
      role: "img",
      "aria-label": "全网节点数据流与链路延迟拓扑图",
    });
    svg.append(createTopologyDefs());

    const probeTagsByNode = buildPeerProbeTags(model);
    const nodeGeom = new Map();
    const rowByID = new Map();
    model.nodes.forEach((item, index) => {
      const y = nodesStartY + index * (nodeH + nodeGap);
      nodeGeom.set(item.id, { x: nodeX, y, w: nodeW, h: nodeH, cy: y + nodeH / 2, index, item });
      rowByID.set(item.id, index);
    });

    extItems.sort((a, b) => {
      const avgRow = (ext) => {
        const linked = model.probeLinks.filter((p) => p.isExternal && p.to === ext.id && rowByID.has(p.from));
        return linked.length ? linked.reduce((s, p) => s + rowByID.get(p.from), 0) / linked.length : 999;
      };
      return avgRow(a) - avgRow(b);
    });

    const ingressBox = { x: leftX, y: ingressY, w: sideW, h: ingressCardH, cy: ingressY + ingressCardH / 2 };

    const egressTop = extItems.length
      ? Math.max(padTop + 6, Math.round(padTop + Math.max(0, bodyH - rightStackH) * 0.36))
      : Math.round(padTop + (bodyH - egressCardH) / 2);
    const egressBox = { x: rightX, y: egressTop, w: sideW, h: egressCardH, cy: egressTop + egressCardH / 2 };

    const extGeom = new Map();
    const extAreaTop = egressBox.y + egressBox.h + rightGapV;
    const extAreaBottom = totalH - padBottom - 8;
    const extAvailH = Math.min(extItems.length * (extCardH + 24), Math.max(extItems.length * (extCardH + extGap), extAreaBottom - extAreaTop));
    let extCursorY = extAreaTop;
    extItems.forEach((ext, idx) => {
      const linked = model.probeLinks.filter((p) => p.isExternal && p.to === ext.id && nodeGeom.has(p.from));
      const targetAvgCY = linked.length
        ? linked.reduce((s, p) => s + nodeGeom.get(p.from).cy, 0) / linked.length
        : extCursorY + extCardH / 2;
      const evenCY = extItems.length > 1
        ? extAreaTop + extCardH / 2 + (idx / (extItems.length - 1)) * Math.max(0, extAvailH - extCardH)
        : targetAvgCY;
      const desiredCY = extItems.length >= 4 ? evenCY * 0.68 + targetAvgCY * 0.32 : targetAvgCY;
      const remain = extItems.length - idx;
      const maxAllowedY = totalH - padBottom - remain * extCardH - (remain - 1) * extGap;
      const y = Math.round(Math.max(extCursorY, Math.min(maxAllowedY, desiredCY - extCardH / 2)));
      extGeom.set(ext.id, { x: rightX, y, w: sideW, h: extCardH, cy: y + extCardH / 2, idx, item: ext });
      extCursorY = y + extCardH + extGap;
    });

    const occupiedRects = [
      { x: ingressBox.x, y: ingressBox.y, w: ingressBox.w, h: ingressBox.h },
      { x: egressBox.x, y: egressBox.y, w: egressBox.w, h: egressBox.h },
      ...[...nodeGeom.values()].map((b) => ({ x: b.x, y: b.y, w: b.w, h: b.h })),
      ...[...extGeom.values()].map((b) => ({ x: b.x, y: b.y, w: b.w, h: b.h })),
    ];

    const backdropLayer = createSVGEl("g", { "data-refresh-key": "backdrop-layer" });
    const flowLayer = createSVGEl("g", { "data-refresh-key": "flow-layer" });
    const probeLayer = createSVGEl("g", { "data-refresh-key": "probe-layer" });
    const entityLayer = createSVGEl("g", { "data-refresh-key": "entity-layer" });
    const rateLabelLayer = createSVGEl("g", { "data-refresh-key": "rate-label-layer" });
    const probeLabelLayer = createSVGEl("g", { "data-refresh-key": "probe-label-layer" });

    const inRailX = Math.round((ingressBox.x + ingressBox.w + nodeX) / 2);
    const outRailX = Math.round(nodeX + nodeW + rightGap * 0.16);
    const addStageHeader = (cx, label) => createSVGEl("text", {
      x: cx.toFixed(1),
      y: 17,
      "text-anchor": "middle",
      class: "a-dash-topo-stage-label",
    }, label);
    backdropLayer.append(
      createSVGEl("line", { x1: inRailX, y1: padTop - 4, x2: inRailX, y2: totalH - padBottom, class: "a-dash-topo-guide-rail" }),
      createSVGEl("line", { x1: outRailX, y1: padTop - 4, x2: outRailX, y2: totalH - padBottom, class: "a-dash-topo-guide-rail" }),
      addStageHeader(leftX + sideW / 2, "入站汇总"),
      addStageHeader(nodeX + nodeW / 2, "转发节点"),
      addStageHeader(rightX + sideW / 2, "出站与公网目标"),
    );

    // 1. Ingress -> Nodes smooth optical streams
    model.ingressLinks.forEach((edge, idx) => {
      const target = nodeGeom.get(edge.to);
      if (!target) return;
      const spreadY = model.ingressLinks.length > 1
        ? ingressBox.y + 20 + (idx / (model.ingressLinks.length - 1)) * (ingressBox.h - 40)
        : ingressBox.cy;
      const x1 = ingressBox.x + ingressBox.w;
      const x2 = target.x - 2;
      const landX = isDense && leftGap >= 80 ? x2 - 28 : x2;
      const d = landX < x2
        ? `${cubicPath(x1, spreadY, landX, target.cy)} L ${x2.toFixed(1)} ${target.cy.toFixed(1)}`
        : cubicPath(x1, spreadY, x2, target.cy);
      const peers = ["hub:ingress", `node:${edge.to}`, `edge:${edge.id}`, ...(probeTagsByNode.get(edge.to) || [])];
      const strokeW = edge.active ? 1.5 + edge.weight * 2.2 : 1.05;
      const baseOpacity = edge.active ? 0.62 : 0.28;
      flowLayer.append(renderFlowCurve({
        d,
        peers,
        baseOpacity,
        glowColor: "",
        glowWidth: 0,
        stroke: edge.active ? "url(#a-topo-grad-rx)" : IDLE_FLOW_COLOR,
        strokeWidth: edge.active ? Math.min(2.6, strokeW) : Math.min(1.6, strokeW),
        dash: edge.active ? null : "3 4",
        markerEnd: edge.active ? "url(#a-topo-arrow-rx)" : "url(#a-topo-arrow-idle)",
        pulseColor: edge.active ? INGRESS_COLOR : "",
        pulseOpacity: 0.72,
        pulseDur: flowDur(edge.weight),
        photonColor: edge.active ? INGRESS_COLOR : "",
        refreshKey: `edge-${edge.id}`,
      }));
    });

    // 2. Nodes -> Outbound Hub optical streams
    const activeEgressList = model.egressLinks.filter((e) => !e.blocked);
    const activeEgressIdxByFrom = new Map(activeEgressList.map((e, i) => [e.from, i]));
    const useCollectorBus = nodesCount > 7;
    const txBusX = Math.round(nodeX + nodeW + 34);
    const probeRailX = useCollectorBus ? txBusX + 12 : nodeX + nodeW;
    const busPoints = [];

    model.egressLinks.forEach((edge) => {
      const src = nodeGeom.get(edge.from);
      if (!src) return;
      const peers = [`node:${edge.from}`, "hub:egress", `edge:${edge.id}`, ...(probeTagsByNode.get(edge.from) || [])];
      const startX = src.x + src.w + 2;
      const startY = src.cy + (nodeH < 42 ? 7 : nodeH < 48 ? 8 : 10);

      if (edge.blocked) {
        const hl = isHighlightedEdge(peers);
        const endX = startX + 20;
        const opacity = hl === true ? 1 : hl === false ? 0.14 : 0.78;
        const g = createSVGEl("g", {
          "data-refresh-key": `edge-${edge.id}`,
          "data-topo-peers": peers.join(" "),
          "data-base-opacity": "0.78",
          opacity: opacity.toFixed(2),
        });
        g.append(
          createSVGEl("path", {
            d: `M ${startX.toFixed(1)} ${startY.toFixed(1)} L ${endX.toFixed(1)} ${startY.toFixed(1)}`,
            fill: "none",
            stroke: BLOCKED_COLOR,
            "stroke-width": "1.6",
            "stroke-dasharray": "3 3",
          }),
          createSVGEl("circle", { cx: (endX + 4).toFixed(1), cy: startY.toFixed(1), r: 5.0, fill: "#fff2f3", stroke: "#f2cdd2", "stroke-width": "1" }),
          createSVGEl("path", {
            d: `M ${(endX + 2.2).toFixed(1)} ${(startY - 1.8).toFixed(1)} L ${(endX + 5.8).toFixed(1)} ${(startY + 1.8).toFixed(1)} M ${(endX + 5.8).toFixed(1)} ${(startY - 1.8).toFixed(1)} L ${(endX + 2.2).toFixed(1)} ${(startY + 1.8).toFixed(1)}`,
            fill: "none",
            stroke: BLOCKED_COLOR,
            "stroke-width": "1.2",
            "stroke-linecap": "round",
          }),
        );
        flowLayer.append(g);
        return;
      }

      const activeIdx = activeEgressIdxByFrom.get(edge.from) || 0;
      const spreadY = activeEgressList.length > 1
        ? egressBox.y + 18 + (activeIdx / (activeEgressList.length - 1)) * (egressBox.h - 36)
        : egressBox.cy;
      const endX = useCollectorBus ? txBusX : egressBox.x - 2;
      const endY = useCollectorBus ? startY : spreadY;
      if (useCollectorBus) busPoints.push({ y: startY, peers, active: edge.active, weight: edge.weight });
      const d = useCollectorBus
        ? `M ${startX.toFixed(1)} ${startY.toFixed(1)} L ${endX.toFixed(1)} ${startY.toFixed(1)}`
        : cubicPath(startX, startY, endX, endY);
      const strokeW = edge.active ? 1.4 + edge.weight * 2.0 : 1.0;
      const baseOpacity = edge.active ? 0.48 : 0.22;
      flowLayer.append(renderFlowCurve({
        d,
        peers,
        baseOpacity,
        glowColor: "",
        glowWidth: strokeW + 3,
        stroke: edge.active ? "url(#a-topo-grad-tx)" : IDLE_FLOW_COLOR,
        strokeWidth: strokeW,
        dash: edge.active ? "5 3" : "3 5",
        markerEnd: !useCollectorBus ? (edge.active ? "url(#a-topo-arrow-tx)" : "url(#a-topo-arrow-idle)") : null,
        pulseColor: edge.active && !useCollectorBus ? "#ffffff" : "",
        pulseOpacity: 0.72,
        pulseDur: flowDur(edge.weight),
        photonColor: edge.active ? EGRESS_COLOR : "",
        refreshKey: `edge-${edge.id}`,
      }));
    });

    if (useCollectorBus && busPoints.length) {
      const minBusY = Math.min(...busPoints.map((b) => b.y), egressBox.cy);
      const maxBusY = Math.max(...busPoints.map((b) => b.y));
      const allBusPeers = ["hub:egress", ...new Set(busPoints.flatMap((b) => b.peers))];
      flowLayer.append(createSVGEl("path", {
        d: `M ${txBusX} ${minBusY.toFixed(1)} L ${txBusX} ${maxBusY.toFixed(1)}`,
        fill: "none",
        stroke: EGRESS_COLOR,
        "stroke-width": "2.0",
        "stroke-linecap": "round",
        "data-refresh-key": "tx-collector-bus",
        "data-topo-peers": allBusPeers.join(" "),
        "data-base-opacity": "0.42",
        opacity: isHighlightedEdge(allBusPeers) === false ? "0.14" : "0.42",
      }));
      const trunkStartY = Math.max(minBusY, Math.min(maxBusY, egressBox.cy));
      const trunkD = cubicPath(txBusX, trunkStartY, egressBox.x - 2, egressBox.cy);
      flowLayer.append(renderFlowCurve({
        d: trunkD,
        peers: allBusPeers,
        baseOpacity: 0.68,
        glowColor: "",
        glowWidth: 0,
        stroke: "url(#a-topo-grad-tx)",
        strokeWidth: 2.2,
        dash: "5 3",
        markerEnd: "url(#a-topo-arrow-tx)",
        pulseColor: EGRESS_COLOR,
        pulseOpacity: 0.72,
        pulseDur: "1.45s",
        photonColor: EGRESS_COLOR,
        refreshKey: "tx-collector-trunk",
      }));
    }

    // 3. Topological Probe Links (Ambient Top-5 Bad Alert Pills + Focus-Only Route Pills)
    const laneInfo = assignProbeLanes(model.probeLinks, rowByID);
    const pillBounds = {
      minX: useCollectorBus ? probeRailX + 6 : nodeX + nodeW + 46,
      maxX: rightX - 8,
      minY: padTop + 4,
      maxY: totalH - padBottom - 4,
    };

    const nodeByID = new Map(model.nodes.map((n) => [n.id, n]));
    const flowNodeShort = (id) => {
      const s = String(id || "");
      if (s.startsWith("ext:")) return s.slice(4);
      const item = nodeByID.get(s);
      return formatMapCityLabel(item?.name || s, item);
    };

    const maxAmbient = width < 760 ? 3 : 5;
    const ambientBadIDs = selectTopAmbientBadProbeIDs(model.probeLinks, { includeExternal: true, maxCount: maxAmbient });

    const probeRenderItems = [];
    model.probeLinks.forEach((probe) => {
      const src = nodeGeom.get(probe.from);
      if (!src) return;
      const entityKey = `probe:${probe.id}`;
      const pal = tonePalette(probe.tone);
      const unreachable = isProbeUnreachable(probe);
      const marker = probe.tone === "ok"
        ? "url(#a-topo-arrow-ok)"
        : probe.tone === "warn"
          ? "url(#a-topo-arrow-warn)"
          : probe.tone === "bad"
            ? "url(#a-topo-arrow-bad)"
            : "url(#a-topo-arrow-idle)";

      const peers = [
        entityKey,
        `node:${probe.from}`,
        probe.isExternal ? probe.to : `node:${probe.to}`,
      ];
      const hl = isHighlightedEdge(peers);

      let d = "";
      let idealX = 0;
      let idealY = 0;
      let anchors = [];
      let leaderPoints = [];
      let placeRank = 1;
      let startPort = null;
      let endPort = null;
      const portOffY = nodeH < 42 ? 7 : nodeH < 48 ? 8 : 10;

      if (probe.isExternal) {
        const ext = extGeom.get(probe.to);
        if (!ext) return;
        const x1 = src.x + src.w;
        const y1 = src.cy - portOffY;
        const x2 = ext.x - 2;
        const y2 = ext.cy;
        const curve = extendedCubicPath(x1, useCollectorBus ? probeRailX : x1, y1, x2, y2);
        d = curve.d;
        anchors = [0.76, 0.64, 0.86, 0.52, 0.40].map((t) => curve.pointAt(t));
        leaderPoints = [0.40, 0.55, 0.68, 0.78, 0.86, 0.92].map((t) => curve.pointAt(t));
        idealX = anchors[0].x;
        idealY = anchors[0].y;
        placeRank = 90 + (ext.idx || 0);
        startPort = { x: x1, y: y1 };
        endPort = { x: ext.x, y: y2 };
      } else {
        const dst = nodeGeom.get(probe.to);
        if (!dst) return;
        const info = laneInfo.get(probe.id) || { lane: 0, count: 1 };
        const span = Math.abs((rowByID.get(probe.to) ?? 0) - (rowByID.get(probe.from) ?? 0));
        const anchorX = src.x + src.w;
        const railOffset = useCollectorBus ? Math.max(0, probeRailX - anchorX) : 0;
        const minBow = useCollectorBus ? 26 : Math.min(Math.round(rightGap * 0.28), 148);
        const extReserve = extItems.length ? Math.min(156, Math.max(118, Math.round((rightX - probeRailX) * 0.34))) : 48;
        const maxBow = Math.max(minBow + 64, rightX - probeRailX - extReserve);
        const laneStep = info.count > 1 ? Math.min(120, (maxBow - minBow) / (info.count - 1)) : 0;
        const bow = Math.min(maxBow, minBow + info.lane * laneStep);
        const yA = src.cy - portOffY + (src.cy < dst.cy ? 2 : -2);
        const yB = dst.cy - portOffY + (src.cy < dst.cy ? -2 : 2);
        const geom = bridgeArchPath(anchorX, yA, yB, bow, railOffset);
        d = geom.d;
        const tList = span >= 3 ? [0.50, 0.62, 0.38, 0.74, 0.26, 0.82, 0.18] : [0.50, 0.66, 0.34, 0.78, 0.22];
        anchors = tList.map((t) => bridgePointAt(anchorX, yA, yB, bow, t, railOffset));
        leaderPoints = [0.20, 0.32, 0.42, 0.50, 0.58, 0.68, 0.80].map((t) => bridgePointAt(anchorX, yA, yB, bow, t, railOffset));
        idealX = anchors[0].x;
        idealY = anchors[0].y;
        placeRank = span;
        startPort = { x: anchorX, y: yA };
        endPort = { x: dst.x + dst.w, y: yB };
      }

      const metricPart = buildProbePillLabel(probe, false, true).replace(/(\d+(?:\.\d+)?)ms\//, "$1/");
      const routeArrow = probe.bidirectional ? "↔" : "→";
      const routePrefix = `${clipLabel(flowNodeShort(probe.from), 12)}${routeArrow}${clipLabel(flowNodeShort(probe.to), 16)}`;
      const label = clipLabel(`${routePrefix} · ${metricPart}`, 38);
      const shortLabel = buildProbeShortAlertLabel(probe);
      const pillH = 19;
      const fullW = Math.max(82, Math.round(visualUnits(label) * 6.0 + 22));
      const shortW = Math.max(58, Math.round(visualUnits(shortLabel) * 6.0 + 22));
      const isAmbient = ambientBadIDs.has(probe.id);

      probeRenderItems.push({
        probe,
        entityKey,
        pal,
        unreachable,
        marker,
        peers,
        hl,
        d,
        idealX,
        idealY,
        anchors,
        leaderPoints,
        placeRank,
        pillW: isAmbient ? shortW : fullW,
        fullW,
        pillH,
        label,
        shortLabel,
        isAmbient,
        startPort,
        endPort,
      });
    });

    probeRenderItems.forEach((item) => {
      const { probe, entityKey, pal, unreachable, marker, peers, hl, d, startPort, endPort } = item;
      const defBase = probe.tone === "bad" ? (probe.isExternal ? 0.74 : 0.82)
        : probe.tone === "warn" ? (probe.isExternal ? 0.62 : 0.70)
          : (probe.isExternal ? 0.24 : 0.30);
      const g = createSVGEl("g", {
        class: "a-dash-topo-probe-item",
        "data-refresh-key": `probe-edge-${probe.id}`,
        "data-topo-peers": peers.join(" "),
        "data-base-opacity": defBase.toFixed(2),
        "data-dim-opacity": "0.08",
        opacity: hl === true ? "1" : hl === false ? "0.08" : defBase.toFixed(2),
      });
      g.append(createSVGEl("title", {}, `${probe.name}: ${probe.metricLabel} (${probe.statusLabel}) — 点击打开链路监控`));
      g.append(
        createSVGEl("path", {
          d,
          fill: "none",
          stroke: "#fafafa",
          "stroke-width": isDense ? "3.8" : "5.2",
          "stroke-linecap": "round",
          opacity: "0.88",
        }),
        createSVGEl("path", {
          d,
          fill: "none",
          stroke: pal.stroke,
          "stroke-width": activeKey() === entityKey || hl === true ? "2.4" : isDense ? "1.5" : "1.8",
          "stroke-dasharray": unreachable ? "4 3" : null,
          "marker-end": marker,
          "marker-start": probe.bidirectional && !probe.isExternal ? marker : null,
        }),
      );
      if (!unreachable && (probe.running || probe.tone === "ok" || probe.tone === "warn" || probe.tone === "bad")) {
        g.append(createSVGEl("path", {
          d,
          fill: "none",
          stroke: "#ffffff",
          "stroke-width": "1.2",
          "stroke-dasharray": "4 14",
          "stroke-linecap": "round",
          opacity: "0.82",
          class: "a-dash-topo-probe-running",
        }));
      }
      if (startPort) {
        g.append(createSVGEl("circle", { cx: startPort.x.toFixed(1), cy: startPort.y.toFixed(1), r: 2.3, fill: "#ffffff", stroke: pal.stroke, "stroke-width": "1.3" }));
      }
      if (endPort) {
        g.append(createSVGEl("circle", { cx: endPort.x.toFixed(1), cy: endPort.y.toFixed(1), r: 2.3, fill: "#ffffff", stroke: pal.stroke, "stroke-width": "1.3" }));
      }
      bindInteractive(g, entityKey, { openProbeID: probe.id });
      probeLayer.append(g);
    });

    const flowCohortBoxes = new Map();
    const occupiedForFlowCohort = (keys) => {
      const merged = [...occupiedRects];
      for (const k of keys) {
        const arr = flowCohortBoxes.get(k);
        if (arr) merged.push(...arr);
      }
      return merged;
    };
    const recordFlowCohortBox = (keys, box) => {
      for (const k of keys) {
        if (!flowCohortBoxes.has(k)) flowCohortBoxes.set(k, []);
        flowCohortBoxes.get(k).push(box);
      }
    };

    [...probeRenderItems]
      .sort((a, b) => (a.isAmbient ? 0 : 1) - (b.isAmbient ? 0 : 1) || a.placeRank - b.placeRank)
      .forEach((item) => {
        const { probe, pal, peers, hl, idealX, idealY, anchors, leaderPoints, pillW, fullW, pillH, label, shortLabel, isAmbient } = item;
        const endKeys = [`node:${probe.from}`, probe.isExternal ? probe.to : `node:${probe.to}`];
        if (isAmbient) {
          const placed = placeCollisionFreePill(idealX, idealY, pillW, pillH, occupiedRects, pillBounds, anchors, {
            padX: 3,
            padY: 3,
            leaderPoints,
            reserveW: fullW,
            reserveH: pillH,
          });
          appendProbeLeader(probeLayer, placed, pal, peers, hl, true, false);
          probeLabelLayer.append(createProbePillGroup({
            probe,
            cx: placed.cx,
            cy: placed.cy,
            pillW,
            fullW,
            pillH,
            label,
            shortLabel,
            pal,
            compact: true,
            peers,
            focusOnly: false,
            ambientAlert: true,
          }));
        } else {
          const placed = placeCollisionFreePill(idealX, idealY, pillW, pillH, occupiedForFlowCohort(endKeys), pillBounds, anchors, {
            padX: 3,
            padY: 3,
            leaderPoints,
          });
          recordFlowCohortBox(endKeys, { x: placed.cx - pillW / 2, y: placed.cy - pillH / 2, w: pillW, h: pillH });
          appendProbeLeader(probeLayer, placed, pal, peers, hl, true, true);
          probeLabelLayer.append(createProbePillGroup({
            probe,
            cx: placed.cx,
            cy: placed.cy,
            pillW,
            pillH,
            label,
            pal,
            compact: true,
            peers,
            focusOnly: true,
          }));
        }
      });

    // 4. Entity: Ingress Hub
    {
      const key = "hub:ingress";
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === key ? "is-active" : ""}`,
        "data-refresh-key": "hub-ingress",
      });
      g.append(
        createSVGEl("rect", { x: ingressBox.x, y: ingressBox.y, width: ingressBox.w, height: ingressBox.h, rx: 10, class: "a-dash-topo-hub-box" }),
        createSVGEl("rect", { x: ingressBox.x, y: ingressBox.y + 10, width: 3.5, height: ingressBox.h - 20, rx: 1.75, fill: INGRESS_COLOR }),
        createSVGEl("circle", { cx: ingressBox.x + ingressBox.w, cy: ingressBox.cy, r: 3.1, fill: "#ffffff", stroke: INGRESS_COLOR, "stroke-width": "1.6" }),
        createSVGEl("text", { x: ingressBox.x + 14, y: ingressBox.y + 23, class: "a-dash-topo-hub-kicker" }, "入站流量"),
        createSVGEl("text", { x: ingressBox.x + 14, y: ingressBox.y + 47, class: "a-dash-topo-hub-metric" }, `↓ ${bytes(model.ingress.rateBPS)}/s`),
        createSVGEl("text", { x: ingressBox.x + 14, y: ingressBox.y + 68, class: "a-dash-topo-hub-sub" }, `月接收 ${bytes(model.ingress.monthlyBytes)}`),
      );
      if (model.ingress.clientIPs != null) {
        g.append(createSVGEl("text", { x: ingressBox.x + 14, y: ingressBox.y + 88, class: "a-dash-topo-hub-sub" }, `24h ${model.ingress.clientIPs} IP · ${model.ingress.clientFlows} 流`));
      }
      bindInteractive(g, key);
      entityLayer.append(g);
    }

    // 5. Entities: Active Nodes
    const maxPeakRate = Math.max(1, ...model.nodes.map((n) => n.rxBPS + n.txBPS));
    model.nodes.forEach((item) => {
      const box = nodeGeom.get(item.id);
      if (!box) return;
      const key = `node:${item.id}`;
      const nodePeers = [key, ...(probeTagsByNode.get(item.id) || [])];
      const hl = isHighlightedEdge(nodePeers);
      const selected = activeKey() === key;
      const activeTraffic = item.isOnline && (item.rxBPS + item.txBPS > 0 || item.activeEngines.length > 0);
      const statusColor = item.blocked ? BLOCKED_COLOR : item.isOnline ? OK_COLOR : IDLE_FLOW_COLOR;
      const opacity = hl === false ? 0.35 : 1;

      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${selected || hl === true ? "is-active" : ""} ${item.isOnline ? "is-online" : "is-offline"} ${item.blocked ? "is-blocked" : ""}`,
        "data-refresh-key": `node-${item.id}`,
        "data-topo-peers": nodePeers.join(" "),
        "data-base-opacity": "1",
        opacity: opacity.toFixed(2),
      });
      g.append(createSVGEl("title", {}, `${item.name} · ↓ ${bytes(item.rxBPS)}/s · ↑ ${bytes(item.txBPS)}/s`));
      g.append(
        createSVGEl("rect", {
          x: box.x,
          y: box.y,
          width: box.w,
          height: box.h,
          rx: 9,
          class: `a-dash-topo-node-box ${selected || hl === true ? "is-active" : ""} ${item.blocked ? "is-blocked" : ""}`,
        }),
      );
      const superCompactRow = box.h < 42;
      const compactRow = box.h < 48;
      const dotY = superCompactRow ? box.y + 11.5 : compactRow ? box.y + 14.5 : box.y + 18;
      const badgeY = superCompactRow ? box.y + 4.5 : compactRow ? box.y + 6.5 : box.y + 9;
      const badgeH = superCompactRow ? 14 : 16;
      const badgeTextY = superCompactRow ? box.y + 14 : compactRow ? box.y + 17.5 : box.y + 20;
      const nameY = superCompactRow ? box.y + 14.5 : compactRow ? box.y + 18 : box.y + 21;
      const subY = superCompactRow ? box.y + 27.5 : compactRow ? box.y + 33.5 : box.y + 38.5;

      if (activeTraffic && !item.blocked) {
        g.append(createSVGEl("circle", {
          cx: box.x + 15,
          cy: dotY,
          r: 4,
          fill: "none",
          stroke: statusColor,
          "stroke-width": "1.2",
          class: "a-dash-topo-beacon-ring",
        }));
      }
      g.append(createSVGEl("circle", {
        cx: box.x + 15,
        cy: dotY,
        r: superCompactRow ? 3.0 : 3.4,
        fill: statusColor,
        class: activeTraffic && !item.blocked ? "a-dash-topo-beacon-live" : null,
      }));

      let rightBadgeW = 0;
      if (item.blocked) {
        rightBadgeW = 46;
        g.append(
          createSVGEl("rect", { x: box.x + box.w - rightBadgeW - 9, y: badgeY, width: rightBadgeW, height: badgeH, rx: 4, class: "a-dash-topo-blocked-pill" }),
          createSVGEl("text", { x: box.x + box.w - rightBadgeW / 2 - 9, y: badgeTextY, "text-anchor": "middle", class: "a-dash-topo-blocked-text" }, "已封禁"),
        );
      } else if (item.activeEngines.length && box.w >= 190) {
        const engineLabel = item.activeEngines.length > 1
          ? `${clipLabel(item.activeEngines[0], 9)} +${item.activeEngines.length - 1}`
          : clipLabel(item.activeEngines[0], 10);
        rightBadgeW = Math.max(38, Math.round(visualUnits(engineLabel) * 5.7 + 10));
        g.append(
          createSVGEl("rect", { x: box.x + box.w - rightBadgeW - 9, y: badgeY, width: rightBadgeW, height: badgeH, rx: 4, class: "a-dash-topo-engine-pill" }),
          createSVGEl("text", { x: box.x + box.w - rightBadgeW / 2 - 9, y: badgeTextY, "text-anchor": "middle", class: "a-dash-topo-engine-text" }, engineLabel),
        );
      }

      const maxNameUnits = Math.max(9, Math.floor((box.w - rightBadgeW - 36) / 6.6));
      const rxText = createSVGEl("text", { x: box.x + 14, y: subY, class: "a-dash-topo-node-sub" });
      rxText.append(
        createSVGEl("tspan", { fill: INGRESS_COLOR, "font-weight": "700" }, "↓"),
        ` ${bytes(item.rxBPS)}/s`,
      );
      const txText = createSVGEl("text", { x: box.x + Math.round(box.w * 0.50), y: subY, class: "a-dash-topo-node-sub" });
      txText.append(
        createSVGEl("tspan", { fill: item.blocked ? BLOCKED_COLOR : EGRESS_COLOR, "font-weight": "700" }, "↑"),
        ` ${bytes(item.txBPS)}/s`,
      );

      g.append(
        createSVGEl("text", { x: box.x + 26, y: nameY, class: "a-dash-topo-node-name" }, clipLabel(item.name, maxNameUnits)),
        rxText,
        txText,
      );

      const trackW = box.w - 24;
      const trackY = superCompactRow ? box.y + box.h - 3.5 : box.y + box.h - 4.5;
      const ratio = item.isOnline && (item.rxBPS + item.txBPS > 0)
        ? Math.max(0.08, Math.min(1, (item.rxBPS + item.txBPS) / maxPeakRate))
        : 0;
      g.append(createSVGEl("rect", { x: box.x + 12, y: trackY, width: trackW, height: 2, rx: 1, class: "a-dash-topo-meter-track" }));
      if (ratio > 0) {
        g.append(createSVGEl("rect", {
          x: box.x + 12,
          y: trackY,
          width: (trackW * ratio).toFixed(1),
          height: 2,
          rx: 1,
          fill: item.blocked ? BLOCKED_COLOR : "url(#a-topo-grad-bar)",
        }));
      }

      g.append(
        createSVGEl("circle", { cx: box.x, cy: box.cy, r: 2.8, fill: "#ffffff", stroke: item.isOnline ? INGRESS_COLOR : IDLE_FLOW_COLOR, "stroke-width": "1.5" }),
        createSVGEl("circle", { cx: box.x + box.w, cy: box.cy + (superCompactRow ? 7 : compactRow ? 8 : 10), r: 2.6, fill: "#ffffff", stroke: item.blocked ? BLOCKED_COLOR : item.isOnline ? EGRESS_COLOR : IDLE_FLOW_COLOR, "stroke-width": "1.4" }),
      );

      bindInteractive(g, key);
      entityLayer.append(g);
    });

    // 6. Entities: Outbound Hub & External Probe Targets
    {
      const key = "hub:egress";
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === key ? "is-active" : ""}`,
        "data-refresh-key": "hub-egress",
      });
      g.append(
        createSVGEl("rect", { x: egressBox.x, y: egressBox.y, width: egressBox.w, height: egressBox.h, rx: 10, class: "a-dash-topo-hub-box" }),
        createSVGEl("rect", { x: egressBox.x + egressBox.w - 3.5, y: egressBox.y + 10, width: 3.5, height: egressBox.h - 20, rx: 1.75, fill: EGRESS_COLOR }),
        createSVGEl("circle", { cx: egressBox.x, cy: egressBox.cy, r: 3.1, fill: "#ffffff", stroke: EGRESS_COLOR, "stroke-width": "1.6" }),
        createSVGEl("text", { x: egressBox.x + 14, y: egressBox.y + 23, class: "a-dash-topo-hub-kicker" }, "出站流量"),
        createSVGEl("text", { x: egressBox.x + 14, y: egressBox.y + 47, class: "a-dash-topo-hub-metric" }, `↑ ${bytes(model.egress.rateBPS)}/s`),
        createSVGEl("text", { x: egressBox.x + 14, y: egressBox.y + 68, class: "a-dash-topo-hub-sub" }, `月发送 ${bytes(model.egress.monthlyBytes)}`),
      );
      bindInteractive(g, key);
      entityLayer.append(g);
    }

    for (const [extID, box] of extGeom.entries()) {
      const extPeers = [extID, ...(probeTagsByNode.get(extID) || [])];
      const hl = isHighlightedEdge(extPeers);
      const selected = activeKey() === extID;
      const extCompact = box.h < 48;
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${selected ? "is-active" : ""} ${hl === true ? "is-highlighted" : ""}`,
        "data-refresh-key": `ext-${extID}`,
        "data-topo-peers": extPeers.join(" "),
        "data-base-opacity": "1",
        opacity: hl === false ? "0.35" : "1",
      });
      g.append(
        createSVGEl("rect", { x: box.x, y: box.y, width: box.w, height: box.h, rx: 9, class: `a-dash-topo-ext-box ${selected ? "is-active" : ""}` }),
        createSVGEl("circle", { cx: box.x, cy: box.cy, r: 2.8, fill: "#ffffff", stroke: EGRESS_COLOR, "stroke-width": "1.4" }),
        createSVGEl("text", { x: box.x + 13, y: box.y + (extCompact ? 16 : 21), class: "a-dash-topo-ext-kicker" }, "公网目标"),
        createSVGEl("text", { x: box.x + 13, y: box.y + (extCompact ? 33.5 : 41), class: "a-dash-topo-node-name" }, clipLabel(box.item.name, Math.max(12, Math.floor((box.w - 26) / 7)))),
      );
      bindInteractive(g, extID);
      entityLayer.append(g);
    }

    svg.append(backdropLayer, flowLayer, probeLayer, entityLayer, rateLabelLayer, probeLabelLayer);
    return svg;
  };

  const renderMobileSVG = (width, model) => {
    const padX = 8;
    const padY = 10;
    const w = Math.max(280, width);
    const inBusX = padX + 6;
    const nodeX = padX + 18;
    const hasProbes = model.probeLinks.length > 0;
    const probeWingW = hasProbes ? Math.max(58, Math.round(w * 0.22)) : 12;
    const outBusX = w - padX - probeWingW;
    const nodeW = Math.max(150, outBusX - nodeX - 10);
    const headerH = 52;
    const nodesCount = model.nodes.length;
    const nodeH = nodesCount > 12 ? 34 : nodesCount > 8 ? 40 : 48;
    const nodeGap = nodesCount > 12 ? 7 : 10;
    const nodesTop = padY + headerH + 18;
    const nodesTotalH = nodesCount ? nodesCount * nodeH + (nodesCount - 1) * nodeGap : nodeH;

    const mRowByID = new Map(model.nodes.map((n, idx) => [n.id, idx]));
    const extItems = [...model.externalTargets].sort((a, b) => {
      const avgR = (ext) => {
        const linked = model.probeLinks.filter((p) => p.isExternal && p.to === ext.id && mRowByID.has(p.from));
        return linked.length ? linked.reduce((s, p) => s + mRowByID.get(p.from), 0) / linked.length : 999;
      };
      return avgR(a) - avgR(b);
    });
    const extH = extItems.length >= 4 ? 34 : 38;
    const extGap = extItems.length >= 4 ? 6 : 8;
    const extBlockTop = nodesTop + nodesTotalH + 16;
    const extBlockH = extItems.length ? extItems.length * extH + (extItems.length - 1) * extGap + 12 : 0;
    const footerTop = extBlockTop + extBlockH + 8;
    const footerH = 52;
    const totalH = footerTop + footerH + padY;

    const svg = createSVGEl("svg", {
      class: "a-dash-topo-svg is-mobile",
      "data-refresh-key": "topo-svg",
      viewBox: `0 0 ${w} ${totalH}`,
      role: "img",
      "aria-label": "全网节点数据流与链路延迟拓扑图",
    });
    svg.append(createTopologyDefs());

    const probeTagsByNode = buildPeerProbeTags(model);
    const flowLayer = createSVGEl("g", { "data-refresh-key": "m-flow-layer" });
    const probeLayer = createSVGEl("g", { "data-refresh-key": "m-probe-layer" });
    const entityLayer = createSVGEl("g", { "data-refresh-key": "m-entity-layer" });
    const probeLabelLayer = createSVGEl("g", { "data-refresh-key": "m-probe-label-layer" });

    const nodeGeom = new Map();
    const rowByID = new Map();
    model.nodes.forEach((item, index) => {
      const y = nodesTop + index * (nodeH + nodeGap);
      nodeGeom.set(item.id, { x: nodeX, y, w: nodeW, h: nodeH, cy: y + nodeH / 2, item });
      rowByID.set(item.id, index);
    });

    const firstCY = nodesTop + nodeH / 2;
    const lastCY = nodesTop + (Math.max(1, nodesCount) - 1) * (nodeH + nodeGap) + nodeH / 2;

    flowLayer.append(
      createSVGEl("path", {
        d: `M ${inBusX} ${padY + headerH} L ${inBusX} ${lastCY.toFixed(1)}`,
        fill: "none",
        stroke: INGRESS_COLOR,
        "stroke-width": "2.1",
        opacity: "0.7",
      }),
      createSVGEl("path", {
        d: `M ${outBusX} ${firstCY.toFixed(1)} L ${outBusX} ${footerTop}`,
        fill: "none",
        stroke: EGRESS_COLOR,
        "stroke-width": "2.1",
        opacity: "0.62",
      }),
    );

    const extGeom = new Map();
    extItems.forEach((ext, i) => {
      const ey = extBlockTop + i * (extH + extGap);
      extGeom.set(ext.id, { x: nodeX, y: ey, w: nodeW, h: extH, cy: ey + extH / 2, idx: i, item: ext });
    });

    {
      const g = createSVGEl("g", { "data-refresh-key": "m-hub-ingress", class: "a-dash-topo-card-node" });
      const info = model.ingress.clientIPs != null
        ? `月收 ${bytes(model.ingress.monthlyBytes)} · ${model.ingress.clientIPs} IP`
        : `月收 ${bytes(model.ingress.monthlyBytes)}`;
      g.append(
        createSVGEl("rect", { x: padX, y: padY, width: w - padX * 2, height: headerH, rx: 9, class: "a-dash-topo-hub-box" }),
        createSVGEl("text", { x: padX + 12, y: padY + 19, class: "a-dash-topo-hub-kicker" }, "入站流量"),
        createSVGEl("text", { x: padX + 12, y: padY + 39, class: "a-dash-topo-hub-metric" }, `↓ ${bytes(model.ingress.rateBPS)}/s`),
        createSVGEl("text", { x: w - padX - 12, y: padY + 39, "text-anchor": "end", class: "a-dash-topo-hub-sub" }, info),
      );
      bindInteractive(g, "hub:ingress");
      entityLayer.append(g);
    }

    const maxChars = Math.max(8, Math.floor((nodeW - 22) / 5.65));
    model.nodes.forEach((item) => {
      const box = nodeGeom.get(item.id);
      const { y, cy } = box;
      const pTags = probeTagsByNode.get(item.id) || [];
      const tapPeers = `node:${item.id}`;
      flowLayer.append(
        createSVGEl("path", {
          d: `M ${inBusX} ${cy.toFixed(1)} L ${nodeX - 2} ${cy.toFixed(1)}`,
          fill: "none",
          stroke: item.isOnline ? INGRESS_COLOR : IDLE_FLOW_COLOR,
          "stroke-width": "1.7",
          "data-topo-peers": ["hub:ingress", tapPeers, ...pTags].join(" "),
          "data-base-opacity": item.isOnline ? "0.75" : "0.35",
          opacity: item.isOnline ? "0.75" : "0.35",
          "marker-end": item.isOnline ? "url(#a-topo-arrow-rx)" : "url(#a-topo-arrow-idle)",
        }),
      );
      if (item.blocked) {
        const stubX = nodeX + nodeW + 8;
        const capG = createSVGEl("g", {
          "data-topo-peers": [tapPeers, "hub:egress", ...pTags].join(" "),
          "data-base-opacity": "0.85",
          opacity: "0.85",
        });
        capG.append(
          createSVGEl("path", {
            d: `M ${nodeX + nodeW} ${(cy + 6).toFixed(1)} L ${stubX.toFixed(1)} ${(cy + 6).toFixed(1)}`,
            fill: "none",
            stroke: BLOCKED_COLOR,
            "stroke-width": "1.4",
            "stroke-dasharray": "2.5 2.5",
          }),
          createSVGEl("circle", { cx: (stubX + 3).toFixed(1), cy: (cy + 6).toFixed(1), r: 3.6, fill: "#fff2f3", stroke: "#f2cdd2", "stroke-width": "0.9" }),
        );
        flowLayer.append(capG);
      } else {
        flowLayer.append(
          createSVGEl("path", {
            d: `M ${nodeX + nodeW} ${(cy + 6).toFixed(1)} L ${outBusX - 2} ${(cy + 6).toFixed(1)}`,
            fill: "none",
            stroke: item.isOnline ? EGRESS_COLOR : IDLE_FLOW_COLOR,
            "stroke-width": "1.7",
            "stroke-dasharray": item.isOnline ? "5 3" : "3 5",
            "data-topo-peers": [tapPeers, "hub:egress", ...pTags].join(" "),
            "data-base-opacity": item.isOnline ? "0.68" : "0.35",
            opacity: item.isOnline ? "0.68" : "0.35",
            "marker-end": item.isOnline ? "url(#a-topo-arrow-tx)" : "url(#a-topo-arrow-idle)",
          }),
        );
      }

      const key = `node:${item.id}`;
      const activeTraffic = item.isOnline && (item.rxBPS + item.txBPS > 0 || item.activeEngines.length > 0);
      const dotColor = item.blocked ? BLOCKED_COLOR : item.isOnline ? OK_COLOR : IDLE_FLOW_COLOR;
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === key ? "is-active" : ""} ${item.blocked ? "is-blocked" : ""}`,
        "data-refresh-key": `m-node-${item.id}`,
        "data-topo-peers": [key, ...pTags].join(" "),
        "data-base-opacity": "1",
      });
      const mSuperCompact = nodeH < 39;
      const mCompact = nodeH < 46;
      const mDotY = mSuperCompact ? y + 11.5 : mCompact ? y + 14 : y + 17;
      const mNameY = mSuperCompact ? y + 14.5 : mCompact ? y + 17.5 : y + 20.5;
      const mSubY = mSuperCompact ? y + 27.5 : mCompact ? y + 32 : y + 36;
      g.append(createSVGEl("rect", { x: nodeX, y, width: nodeW, height: nodeH, rx: 8, class: `a-dash-topo-node-box ${activeKey() === key ? "is-active" : ""} ${item.blocked ? "is-blocked" : ""}` }));
      if (activeTraffic && !item.blocked) {
        g.append(createSVGEl("circle", { cx: nodeX + 12, cy: mDotY, r: 3.4, fill: "none", stroke: dotColor, "stroke-width": "1.1", class: "a-dash-topo-beacon-ring" }));
      }
      g.append(
        createSVGEl("circle", { cx: nodeX + 12, cy: mDotY, r: 2.9, fill: dotColor }),
        createSVGEl("text", { x: nodeX + 21, y: mNameY, class: "a-dash-topo-node-name" }, clipLabel(item.name, maxChars)),
        createSVGEl("text", { x: nodeX + 11, y: mSubY, class: "a-dash-topo-node-sub" }, `↓ ${bytes(item.rxBPS)}/s · ↑ ${bytes(item.txBPS)}/s`),
      );
      bindInteractive(g, key);
      entityLayer.append(g);
    });

    if (hasProbes) {
      const mDense = nodesCount > 12 || model.probeLinks.length >= 10;
      const laneInfo = assignProbeLanes(model.probeLinks, rowByID);
      model.probeLinks.forEach((probe) => {
        const src = nodeGeom.get(probe.from);
        if (!src) return;
        const entityKey = `probe:${probe.id}`;
        const pal = tonePalette(probe.tone);
        const unreachable = isProbeUnreachable(probe);
        const marker = probe.tone === "ok"
          ? "url(#a-topo-arrow-ok)"
          : probe.tone === "warn"
            ? "url(#a-topo-arrow-warn)"
            : probe.tone === "bad"
              ? "url(#a-topo-arrow-bad)"
              : "url(#a-topo-arrow-idle)";
        const peers = [entityKey, `node:${probe.from}`, probe.isExternal ? probe.to : `node:${probe.to}`];
        const hl = isHighlightedEdge(peers);

        let d = "";
        let startPort = null;
        let endPort = null;
        if (probe.isExternal) {
          const ext = extGeom.get(probe.to);
          if (!ext) return;
          const x1 = src.x + src.w;
          const y1 = src.cy - 5;
          const baseBow = outBusX - x1 + 16;
          const maxBow = w - padX - x1 - 8;
          const extSpan = extItems.length > 1 ? (ext.idx || 0) / (extItems.length - 1) : 0.5;
          const bow = Math.min(maxBow, baseBow + Math.round((maxBow - baseBow) * (0.35 + 0.6 * extSpan)));
          const geom = bridgeArchPath(x1, y1, ext.cy, bow);
          d = geom.d;
          startPort = { x: x1, y: y1 };
          endPort = { x: ext.x + ext.w, y: ext.cy };
        } else {
          const dst = nodeGeom.get(probe.to);
          if (!dst) return;
          const info = laneInfo.get(probe.id) || { lane: 0, count: 1 };
          const x1 = src.x + src.w;
          const yA = src.cy - 5;
          const yB = dst.cy - 5;
          const baseBow = outBusX - x1 + 12;
          const maxBow = w - padX - x1 - 10;
          const step = info.count > 1 ? Math.min(18, (maxBow - baseBow) / (info.count - 1)) : 0;
          const bow = Math.min(maxBow, baseBow + info.lane * step);
          const geom = bridgeArchPath(x1, yA, yB, bow);
          d = geom.d;
          startPort = { x: x1, y: yA };
          endPort = { x: dst.x + dst.w, y: yB };
        }

        const defBase = probe.tone === "bad" ? 0.82 : probe.tone === "warn" ? 0.72 : 0.36;
        const g = createSVGEl("g", {
          class: "a-dash-topo-probe-item",
          "data-refresh-key": `m-probe-edge-${probe.id}`,
          "data-topo-peers": peers.join(" "),
          "data-base-opacity": defBase.toFixed(2),
          "data-dim-opacity": "0.10",
          opacity: hl === true ? "1" : hl === false ? "0.10" : defBase.toFixed(2),
        });
        g.append(createSVGEl("title", {}, `${probe.name}: ${probe.metricLabel} (${probe.statusLabel}) — 点击打开链路监控`));
        g.append(
          createSVGEl("path", { d, fill: "none", stroke: "#fafafa", "stroke-width": mDense ? "3.2" : "4.2", "stroke-linecap": "round", opacity: "0.9" }),
          createSVGEl("path", {
            d,
            fill: "none",
            stroke: pal.stroke,
            "stroke-width": hl === true ? "2.0" : (mDense ? "1.35" : "1.6"),
            "stroke-dasharray": unreachable ? "3 2.5" : null,
            "marker-end": marker,
            "marker-start": probe.bidirectional && !probe.isExternal ? marker : null,
          }),
        );
        if (startPort) {
          g.append(createSVGEl("circle", { cx: startPort.x.toFixed(1), cy: startPort.y.toFixed(1), r: 1.9, fill: "#ffffff", stroke: pal.stroke, "stroke-width": "1.1" }));
        }
        if (endPort) {
          g.append(createSVGEl("circle", { cx: endPort.x.toFixed(1), cy: endPort.y.toFixed(1), r: 1.9, fill: "#ffffff", stroke: pal.stroke, "stroke-width": "1.1" }));
        }
        bindInteractive(g, entityKey, { openProbeID: probe.id });
        probeLayer.append(g);
      });
    }

    for (const [extID, box] of extGeom.entries()) {
      const extPeers = [extID, ...(probeTagsByNode.get(extID) || [])];
      const extCompact = box.h < 36;
      const g = createSVGEl("g", {
        class: `a-dash-topo-card-node ${activeKey() === extID ? "is-active" : ""}`,
        "data-refresh-key": `m-ext-${extID}`,
        "data-topo-peers": extPeers.join(" "),
        "data-base-opacity": "1",
      });
      g.append(
        createSVGEl("rect", { x: box.x, y: box.y, width: box.w, height: box.h, rx: 8, class: `a-dash-topo-ext-box ${activeKey() === extID ? "is-active" : ""}` }),
        createSVGEl("circle", { cx: box.x + box.w, cy: box.cy, r: 2.5, fill: "#ffffff", stroke: EGRESS_COLOR, "stroke-width": "1.3" }),
        createSVGEl("text", { x: box.x + 10, y: box.y + (extCompact ? 12.5 : 15), class: "a-dash-topo-ext-kicker" }, "公网目标"),
        createSVGEl("text", { x: box.x + 10, y: box.y + (extCompact ? 26.5 : 30), class: "a-dash-topo-node-name" }, clipLabel(box.item.name, Math.max(12, Math.floor((box.w - 20) / 6.3)))),
      );
      bindInteractive(g, extID);
      entityLayer.append(g);
    }

    {
      const g = createSVGEl("g", { "data-refresh-key": "m-hub-egress", class: "a-dash-topo-card-node" });
      g.append(
        createSVGEl("rect", { x: padX, y: footerTop, width: w - padX * 2, height: footerH, rx: 9, class: "a-dash-topo-hub-box" }),
        createSVGEl("text", { x: padX + 12, y: footerTop + 19, class: "a-dash-topo-hub-kicker" }, "出站流量"),
        createSVGEl("text", { x: padX + 12, y: footerTop + 39, class: "a-dash-topo-hub-metric" }, `↑ ${bytes(model.egress.rateBPS)}/s`),
        createSVGEl("text", { x: w - padX - 12, y: footerTop + 39, "text-anchor": "end", class: "a-dash-topo-hub-sub" }, `月发 ${bytes(model.egress.monthlyBytes)} · ${model.onlineNodes}/${model.totalNodes} 在线`),
      );
      bindInteractive(g, "hub:egress");
      entityLayer.append(g);
    }

    svg.append(flowLayer, probeLayer, entityLayer, probeLabelLayer);
    return svg;
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

    const filterUnprobed = !isStandalonePage && ctx.can("agents.read") && ctx.can("tasks.read") && states.get("probes") !== "denied";

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
      maxNodes: 24,
      onlyProbedNodes: filterUnprobed,
    });
    lastModel = model;

    const waitingKeys = [
      ...(ctx.can("agents.read") ? ["agents"] : []),
      ...(ctx.can("traffic.read") ? ["traffic", "trafficPolicies"] : []),
      ...(filterUnprobed ? ["probes"] : []),
    ];
    const waiting = waitingKeys.some((key) => !states.has(key) || states.get(key) === "loading");
    const nextStatus = waiting && !model.nodes.length
      ? "读取中…"
      : !model.nodes.length
        ? (isStandalonePage ? "暂无可用拓扑数据" : "未设置检测目标")
        : "";
    if (statusNode.textContent !== nextStatus) statusNode.textContent = nextStatus;

    const measuredWidth = observedWidth >= 200 ? observedWidth : (canvasHost.clientWidth >= 200 ? canvasHost.clientWidth : 0);
    const fallbackWidth = Math.max(300, Math.min(1120, Math.round((window.innerWidth || 1080) - (window.innerWidth > 896 ? 300 : 48))));
    const renderWidth = Math.max(280, Math.round(measuredWidth > 0 ? measuredWidth : fallbackWidth));
    const isMobile = renderWidth < 580;

    const sigModel = {
      ...model,
      egress: { ...model.egress, panelRxBPS: null, panelTxBPS: null },
    };
    const sig = JSON.stringify([renderWidth, isMobile, viewMode, sigModel, nextStatus]);
    if (!force && sig === lastPaintSignature) return;
    lastPaintSignature = sig;

    const metaBadges = [
      keyedBadge(`${model.onlineNodes}/${model.totalNodes} 在线`, model.onlineNodes ? "ok" : "bad", "topo-nodes"),
      model.probeLinks.length
        ? keyedBadge(
            model.abnormalLinks > 0 ? `链路告警 ${model.abnormalLinks}` : `探测链路 ${model.probeLinks.length}`,
            model.abnormalLinks > 0 ? "warn" : "ok",
            "topo-links",
          )
        : null,
    ].filter(Boolean);
    patchChildren(headerMeta, metaBadges);
    openProbesBtn?.classList?.toggle("is-alert", model.abnormalLinks > 0);

    if (model.nodes.length) {
      const freshSvg = viewMode === "map"
        ? renderPacificMapSVG({
            width: renderWidth,
            isMobile,
            model,
            bytes,
            helpers: {
              allowMotion,
              activeKey,
              isHighlightedEdge,
              bindInteractive,
              createProbePillGroup,
              appendProbeLeader,
            },
          })
        : (isMobile ? renderMobileSVG(renderWidth, model) : renderDesktopSVG(renderWidth, model));
      if (canvasHost.firstChild && (canvasHost.firstChild.getAttribute("data-topo-mode") || "flow") === viewMode) {
        patch(canvasHost.firstChild, freshSvg);
      } else {
        canvasHost.replaceChildren(freshSvg);
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
      // Ignore transient sub-200px collapse frames (e.g. compositor/screenshot passes) so desktop never flashes mobile SVG.
      if (next >= 200 && Math.abs(next - observedWidth) >= 2) {
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
