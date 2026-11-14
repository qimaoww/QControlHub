import {
  isValidProbeIPAddress,
  probeDirectionStatus,
  probeHistorySeries,
  summarizeDashboardProbes,
} from "../modules/dashboard-model.js";
import { orderNodesBySavedOrder } from "../modules/node-order.js";
import { createSVGEl } from "./dashboard-panel.js";
import { patch } from "./live-dom.js";
import { nodeRegionFlag } from "./node-region.js";
import { actions, badge, button, empty, field, list, muted, node, pathID } from "./ui.js";

const nodeKey = (endpoint) => `${endpoint?.agent_id || ""}/${endpoint?.instance_id || ""}`;
const isIPv6 = (addr) => String(addr || "").includes(":");
const fmtMS = (val) => (typeof val === "number" && Number.isFinite(val) ? `${val.toFixed(1)} ms` : "—");
const fmtLoss = (val) => (typeof val === "number" && Number.isFinite(val) ? `${val.toFixed(val % 1 === 0 ? 0 : 1)}%` : "—");
const sanitizeSVGKey = (val) => String(val || "default").replace(/[^a-zA-Z0-9_-]/g, "_");

function renderConduitSVG(dir, availability, activeState) {
  const info = probeDirectionStatus(dir, availability, activeState);
  const toneClass = info.tone === "ok"
    ? "is-ok"
    : info.tone === "warn"
      ? "is-warn"
      : info.tone === "bad"
        ? "is-bad"
        : "is-muted";
  const label = dir
    ? (dir.reached ? fmtMS(dir.avg_ms) : dir.loss_percent === 100 ? "100% 丢包" : (info.available === false ? "离线" : info.label))
    : (info.available === false ? "离线" : info.label);

  const svg = createSVGEl("svg", {
    class: `a-dash-conduit-svg ${toneClass}`,
    viewBox: "0 0 80 28",
    "aria-hidden": "true",
  });
  svg.append(
    createSVGEl("line", { x1: "5", y1: "19", x2: "70", y2: "19", class: "a-dash-conduit-line" }),
    createSVGEl("circle", { cx: "5", cy: "19", r: "2.2", class: "a-dash-conduit-node" }),
    createSVGEl("circle", { cx: "27", cy: "19", r: "1.2", class: "a-dash-conduit-pulse" }),
    createSVGEl("circle", { cx: "49", cy: "19", r: "1.2", class: "a-dash-conduit-pulse" }),
    createSVGEl("path", { d: "M67 15.5 L75 19 L67 22.5 Z", class: "a-dash-conduit-head" }),
    createSVGEl("text", { x: "40", y: "10", "text-anchor": "middle", class: "a-dash-conduit-label" }, label),
  );
  return { svg, info };
}

function renderProbeHistoryChart(fwdSeries = [], revSeries = [], width = 360, gradKey = "default") {
  const w = Math.max(160, Math.round(width));
  const h = 46;
  const padX = 4;
  const padY = 6;
  const usableW = Math.max(20, w - padX * 2);
  const usableH = h - padY * 2;
  const fwdVals = fwdSeries.map((pt) => pt.avgMS);
  const revVals = revSeries.map((pt) => pt.avgMS);
  const finite = [...fwdVals, ...revVals].filter((v) => typeof v === "number" && Number.isFinite(v) && v >= 0);
  const rawPeak = finite.length ? Math.max(...finite) : 1;
  const peak = rawPeak > 0 ? rawPeak * 1.2 : 1;
  const count = Math.max(fwdSeries.length, revSeries.length, 2);
  const xFor = (i) => (count <= 1 ? w - padX : padX + (i / (count - 1)) * usableW);
  const yFor = (val) => padY + usableH - (Math.max(0, Math.min(peak, val)) / peak) * usableH;

  const buildPath = (series) => {
    const pts = [];
    series.forEach((pt, idx) => {
      if (pt?.avgMS == null || !Number.isFinite(pt.avgMS)) return;
      pts.push([xFor(idx), yFor(pt.avgMS)]);
    });
    const d = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
    return { d, pts };
  };

  const fwd = buildPath(fwdSeries);
  const rev = buildPath(revSeries);
  const svg = createSVGEl("svg", {
    class: "a-dash-spark is-probe",
    viewBox: `0 0 ${w} ${h}`,
    "aria-hidden": "true",
  });

  svg.append(
    createSVGEl("line", { x1: padX, y1: (padY + usableH * 0.5).toFixed(1), x2: w - padX, y2: (padY + usableH * 0.5).toFixed(1), class: "a-dash-spark-baseline" }),
    createSVGEl("line", { x1: padX, y1: (padY + usableH).toFixed(1), x2: w - padX, y2: (padY + usableH).toFixed(1), class: "a-dash-osc-axis" }),
  );

  if (fwd.pts.length >= 2) {
    const gradID = `a-probe-grad-${sanitizeSVGKey(gradKey)}`;
    const defs = createSVGEl("defs");
    const grad = createSVGEl("linearGradient", { id: gradID, x1: "0", y1: "0", x2: "0", y2: "1" });
    grad.append(
      createSVGEl("stop", { offset: "0%", "stop-color": "#18181b", "stop-opacity": "0.14" }),
      createSVGEl("stop", { offset: "100%", "stop-color": "#18181b", "stop-opacity": "0.01" }),
    );
    defs.append(grad);
    svg.append(defs);
    svg.append(createSVGEl("path", {
      d: `${fwd.d} L${fwd.pts.at(-1)[0].toFixed(1)} ${(h - padY).toFixed(1)} L${fwd.pts[0][0].toFixed(1)} ${(h - padY).toFixed(1)} Z`,
      fill: `url(#${gradID})`,
    }));
  }
  if (rev.d) svg.append(createSVGEl("path", { d: rev.d, class: "a-dash-spark-secondary" }));
  if (fwd.d) svg.append(createSVGEl("path", { d: fwd.d, class: "a-dash-spark-line" }));
  if (fwd.pts.length) {
    const [lx, ly] = fwd.pts.at(-1);
    svg.append(createSVGEl("circle", { cx: lx.toFixed(1), cy: ly.toFixed(1), r: "2.3", class: "a-dash-spark-dot" }));
  }

  fwdSeries.forEach((pt, idx) => {
    if (!pt) return;
    if (!pt.reached || pt.lossPercent > 0) {
      const x = xFor(idx).toFixed(1);
      const y = pt.avgMS != null ? yFor(pt.avgMS).toFixed(1) : (padY + usableH - 2).toFixed(1);
      svg.append(createSVGEl("circle", { cx: x, cy: y, r: "3", class: "a-dash-probe-loss-dot" }));
    }
  });

  return {
    svg,
    peakMS: finite.length ? Math.max(...finite) : null,
  };
}

function probeEndpointCell(flagNode, name, address, alignRight = false) {
  return node("div", { className: `a-dash-flow-ep${alignRight ? " is-end" : ""}` },
    node("div", { className: "a-dash-flow-name" },
      !alignRight && flagNode,
      node("span", { className: "a-dash-ep" }, name || "—"),
      alignRight && flagNode,
    ),
    address ? node("span", { className: "a-dash-ep-addr" }, address) : null,
  );
}

function directionBlock({ key = "fwd", title, fromName, fromAddress, toName, toAddress, fromFlag, toFlag, dir, pair }) {
  const { svg: conduitSVG, info: statusInfo } = renderConduitSVG(dir, pair.availability, pair.active_state);
  const lossVal = dir ? dir.loss_percent : null;
  const lossClass = dir && lossVal > 0 ? (lossVal >= 20 || !dir.reached ? "is-bad" : "is-warn") : "";

  return node("div", { className: "a-dash-probe-lane", "data-refresh-key": key },
    node("div", { className: "a-dash-probe-lane-head" },
      node("span", { className: "a-dash-probe-dir-tag", "data-refresh-key": "dir" }, title),
      (() => { const b = badge(statusInfo.label, statusInfo.tone); b.setAttribute("data-refresh-key", "status"); return b; })(),
    ),
    node("div", { className: "a-dash-flow-row" },
      probeEndpointCell(fromFlag, fromName, fromAddress, false),
      node("div", { className: "a-dash-conduit" }, conduitSVG),
      probeEndpointCell(toFlag, toName, toAddress, true),
    ),
    node("dl", { className: "a-dash-probe-stats" },
      node("div", { "data-refresh-key": "avg" }, node("dt", {}, "平均延迟"), node("dd", { className: "is-primary" }, dir ? fmtMS(dir.avg_ms) : "—")),
      node("div", { "data-refresh-key": "minmax" }, node("dt", {}, "最小 / 最大"), node("dd", {}, dir && typeof dir.min_ms === "number" && typeof dir.max_ms === "number" ? `${dir.min_ms.toFixed(1)} / ${dir.max_ms.toFixed(1)} ms` : "—")),
      node("div", { "data-refresh-key": "jitter" }, node("dt", {}, "抖动"), node("dd", {}, dir ? fmtMS(dir.stdev_ms) : "—")),
      node("div", { "data-refresh-key": "loss" }, node("dt", {}, "丢包率"), node("dd", { className: lossClass }, dir ? fmtLoss(lossVal) : "—")),
    ),
  );
}

function resolvedPairEndpoints(pair) {
  if (Array.isArray(pair?.chain) && pair.chain.length >= 2) {
    return pair.chain.map((hop, idx) => ({
      agent_id: hop.agent_id || "",
      instance_id: hop.instance_id || "",
      name: hop.name || (idx === 0 ? pair.node1_name : idx === pair.chain.length - 1 ? pair.node2_name : "") || hop.address || hop.agent_id || `节点 ${idx + 1}`,
      region: hop.region || (idx === 0 ? pair.node1_region : idx === pair.chain.length - 1 ? pair.node2_region : "") || "",
      address: hop.address || "",
    }));
  }
  if (Array.isArray(pair?.nodes) && pair.nodes.length >= 2) {
    return pair.nodes.map((hop, idx) => ({
      agent_id: hop.agent_id || "",
      instance_id: hop.instance_id || "",
      name: hop.name || (idx === 0 ? pair.node1_name : idx === pair.nodes.length - 1 ? pair.node2_name : "") || hop.address || hop.agent_id || `节点 ${idx + 1}`,
      region: idx === 0 ? (pair.node1_region || "") : idx === pair.nodes.length - 1 ? (pair.node2_region || "") : "",
      address: hop.address || "",
    }));
  }
  const n1 = pair?.node1 || {};
  const n2 = pair?.node2 || {};
  return [
    {
      agent_id: n1.agent_id || "",
      instance_id: n1.instance_id || "",
      name: pair?.node1_name || n1.name || n1.agent_id || n1.address || "节点 A",
      region: pair?.node1_region || "",
      address: n1.address || "",
    },
    {
      agent_id: n2.agent_id || "",
      instance_id: n2.instance_id || "",
      name: pair?.target_mode === "ip" ? (n2.address || "自定义 IP") : (pair?.node2_name || n2.name || n2.agent_id || n2.address || "节点 B"),
      region: pair?.target_mode === "ip" ? "" : (pair?.node2_region || ""),
      address: n2.address || "",
    },
  ];
}

function summarizeHopMetricText(dir, availability, activeState) {
  const info = probeDirectionStatus(dir, availability, activeState);
  if (!dir) return { text: info.available === false ? "离线" : info.label, tone: info.tone || "" };
  if (dir.state === "running" || dir.state === "preparing") return { text: "探测中", tone: "warn" };
  if (dir.state === "failed" || dir.error) return { text: dir.error || "失败", tone: "bad" };
  if (!dir.reached || dir.loss_percent >= 100) return { text: "不可达 · 100%丢包", tone: "bad" };
  const ms = typeof dir.avg_ms === "number" && Number.isFinite(dir.avg_ms) ? `${dir.avg_ms.toFixed(1)} ms` : "—";
  const loss = dir.loss_percent > 0 ? ` · ${fmtLoss(dir.loss_percent)}丢包` : "";
  return { text: `${ms}${loss}`, tone: info.tone || "ok" };
}

export function createDashboardProbesSection(ctx, { date, getOrderIDs, getProbes, getProbesState, reloadProbes }) {
  const canExecute = () => ctx.can("tasks.execute");
  const canView = () => ctx.can("agents.read") && ctx.can("tasks.read");
  const summaryNode = node("div", { className: "a-actions", role: "status" });
  const addProbeButton = canExecute()
    ? button("添加链路", () => openProbeDialog(), { tone: "primary", "data-refresh-key": "add-probe" })
    : null;
  const modalToolbar = node("div", { className: "a-dash-probe-modal-bar" }, summaryNode, addProbeButton);
  const gridHost = node("div", { className: "a-dash-probe-grid" });
  const modalContent = node("div", { className: "a-dash-probe-modal" }, modalToolbar, gridHost);

  const VALID_PROBE_MODAL_FILTERS = new Set(["all", "abnormal"]);
  let modalBox = null;
  let focusProbeID = "";
  let filterMode = "all";
  let filterModeTouched = false;
  const syncSavedProbeFilter = ({ repaint = false } = {}) => {
    if (!ctx.preferences?.get || filterModeTouched) return;
    const saved = ctx.preferences.get("dashboard");
    if (VALID_PROBE_MODAL_FILTERS.has(saved?.probe_filter) && saved.probe_filter !== filterMode) {
      filterMode = saved.probe_filter;
      if (repaint && modalBox?.open && ctx.current()) paint(true);
    }
  };
  syncSavedProbeFilter({ repaint: false });
  if (ctx.preferences) {
    const unsubPrefs = ctx.preferences.subscribe?.(() => syncSavedProbeFilter({ repaint: true }));
    if (typeof unsubPrefs === "function" && ctx.signal) {
      ctx.signal.addEventListener("abort", unsubPrefs, { once: true });
    }
    void ctx.preferences.ready?.().then(() => syncSavedProbeFilter({ repaint: true }));
  }
  let diagnosticNodes = [];
  const inFlightOps = new Set();
  let sparkWidth = 360;
  let lastPaintSignature = "";
  const keyed = (key, el) => {
    if (el?.setAttribute) el.setAttribute("data-refresh-key", key);
    return el;
  };

  if (typeof ResizeObserver === "function") {
    let measureFrame = 0;
    const ro = new ResizeObserver(() => {
      if (!canView() || !ctx.current() || !modalBox?.open || !gridHost.isConnected) return;
      const sampleSpark = gridHost.querySelector(".a-dash-probe-spark");
      const raw = sampleSpark ? sampleSpark.clientWidth - 32 : 0;
      const fallback = gridHost.clientWidth > 0 ? Math.max(220, gridHost.clientWidth - 36) : 0;
      const next = raw > 40 ? raw : fallback;
      if (next > 0 && Math.abs(next - sparkWidth) >= 4) {
        sparkWidth = next;
        cancelAnimationFrame(measureFrame);
        measureFrame = requestAnimationFrame(() => {
          measureFrame = 0;
          if (canView() && ctx.current() && modalBox?.open) paint();
        });
      }
    });
    ro.observe(gridHost);
    ctx.signal.addEventListener("abort", () => {
      cancelAnimationFrame(measureFrame);
      ro.disconnect();
    }, { once: true });
  }
  ctx.signal.addEventListener("abort", () => modalBox?.close(), { once: true });

  function openProbeDialog(pair = null, opts = {}) {
    if (!canExecute()) return;
    const MAX_HOPS = 10;
    const isEdit = Boolean(pair?.id);
    const params = pair?.parameters || {};
    const initialMode = pair?.target_mode === "ip" ? "ip" : "node";
    const existingGroups = [...new Set(
      (getProbes()?.pairs || [])
        .map((item) => String(item?.group_name || "").trim())
        .filter(Boolean),
    )].sort((a, b) => a.localeCompare(b, "zh-CN"));

    const existingNodes = Array.isArray(pair?.nodes) && pair.nodes.length >= 2
      ? pair.nodes
      : pair
        ? [pair.node1, pair.node2]
        : [];
    const hopsState = existingNodes.length >= 2
      ? existingNodes.map((ep) => ({
          key: ep?.agent_id ? nodeKey(ep) : "",
          address: ep?.address || "",
        }))
      : [
          { key: "", address: "" },
          { key: "", address: "" },
        ];
    if (initialMode === "ip" && hopsState.length < 2) {
      hopsState.push({ key: "", address: "" });
    }

    const nameInput = field("探测名称", "name", pair?.name || "", {
      maxlength: 80,
      placeholder: "可选，如 亚太全程链路",
    });
    const groupInput = field("监测分组", "group_name", pair?.group_name || opts?.groupName || "", {
      maxlength: 40,
      placeholder: "可选，同组链路将在拓扑中归组展示",
      suggestions: existingGroups,
    });
    const modeField = field("监测模式", "target_mode", initialMode, {
      options: [
        ["node", "托管节点全链路 (支持 A→B→C→D 多跳)"],
        ["ip", "自定义目标 IP (单跳)"],
      ],
    });

    const ipSrcNodeField = field("起点节点", "ip_src_key", hopsState[0]?.key || "", {
      options: [["", "读取中…"]],
    });
    const ipSrcAddrField = field("起点地址", "ip_src_addr", hopsState[0]?.address || "", {
      options: hopsState[0]?.address ? [[hopsState[0].address, hopsState[0].address]] : [["", "请选择"]],
    });
    const customIPField = field("对端 IP", "node2_ip", pair && initialMode === "ip" ? pair?.node2?.address || "" : "", {
      placeholder: "IPv4 / IPv6",
    });

    const bidirField = field("去程与回程双向测量", "bidirectional", pair ? Boolean(pair.bidirectional) : true, {
      type: "checkbox",
      hint: "启用后按序双向实测每跳去程与回程延迟、丢包率",
    });

    const countField = field("探测次数", "count", params.count || 5, { type: "number", min: 1, max: 100, required: true });
    const intervalField = field("间隔 (ms)", "interval_ms", params.interval_ms || 200, { type: "number", min: 200, max: 5000, required: true });
    const timeoutField = field("超时 (ms)", "timeout_ms", params.timeout_ms || 1000, { type: "number", min: 100, max: 5000, required: true });
    const sizeField = field("负载 (B)", "size", params.size ?? 56, { type: "number", min: 0, max: 1400, required: true });
    const autoEnabledField = field("自动周期探测", "auto_enabled", pair ? Boolean(pair.auto_enabled) : true, { type: "checkbox" });
    const autoSecField = field("周期 (s)", "auto_interval_seconds", pair?.auto_interval_seconds || 60, { type: "number", min: 15, max: 3600, required: true });
    const readHint = muted("读取节点中…");

    const modeSelect = modeField.querySelector("select");
    const ipSrcNodeSelect = ipSrcNodeField.querySelector("select");
    const ipSrcAddrSelect = ipSrcAddrField.querySelector("select");
    const ipInput = customIPField.querySelector("input");
    const bidirInput = bidirField.querySelector("input");

    const chainCountBadge = badge(`${hopsState.length} / ${MAX_HOPS} 节点`);
    const hopListHost = node("div", { className: "a-dash-probe-hop-list" });
    const chainPreview = node("p", { className: "a-muted a-dash-probe-chain-preview" });
    let addHopBtn;
    const chainBuilder = node("div", { className: "a-dash-probe-chain-builder" },
      node("div", { className: "a-dash-probe-chain-head" },
        node("div", { className: "a-dash-probe-chain-title" },
          node("strong", {}, "全程监测节点顺序"),
          chainCountBadge,
        ),
        addHopBtn = button("+ 追加途经节点", () => {
          if (hopsState.length >= MAX_HOPS) return;
          const usedKeys = new Set(hopsState.map((h) => h.key).filter(Boolean));
          const candidate = orderedNodes.find((item) => !usedKeys.has(nodeKey(item))) || null;
          hopsState.push({ key: candidate ? nodeKey(candidate) : "", address: "" });
          renderChainRows();
        }, { className: "a-button", "data-refresh-key": "add-hop" }),
      ),
      hopListHost,
      chainPreview,
    );
    const customIPGrid = node("div", { className: "a-form-grid" },
      ipSrcNodeField,
      ipSrcAddrField,
      customIPField,
    );

    let orderedNodes = [];
    const nodeByKey = new Map();

    const syncOptions = (selectEl, items, preferred) => {
      const btn = selectEl.querySelector("button");
      const opts = items.map(([val, label, disabled = false]) => node("option", { value: val, disabled }, label));
      selectEl.replaceChildren(...(btn ? [btn] : []), ...opts);
      if (items.some(([val, , dis]) => val === preferred && !dis)) selectEl.value = preferred;
      else selectEl.value = items.find(([, , dis]) => !dis)?.[0] || "";
    };

    const nodeOptionItems = (excludeKeys) => orderedNodes
      .filter((item) => !excludeKeys.has(nodeKey(item)))
      .map((item) => {
        const key = nodeKey(item);
        const suffix = !item.online
          ? " · 离线"
          : item.instance_id && item.instance_id !== "main"
            ? ` · ${item.context || item.instance_id}`
            : "";
        return [key, `${item.name}${suffix}`, false];
      });

    const compatibleAddrsForHop = (agentItem, baseAddr) => {
      const allAddrs = agentItem?.capabilities?.addresses || [];
      if (!baseAddr) return allAddrs;
      return allAddrs.filter((addr) => isIPv6(addr) === isIPv6(baseAddr));
    };

    const updatePreviewText = () => {
      const names = hopsState.map((hop, idx) => {
        const item = nodeByKey.get(hop.key);
        return item?.name || existingNodes[idx]?.name || `第 ${idx + 1} 站`;
      });
      const bidir = !bidirInput.disabled && bidirInput.checked;
      chainPreview.textContent = bidir
        ? `去程: ${names.join(" → ")} ｜ 回程: ${[...names].reverse().join(" → ")}`
        : `去程: ${names.join(" → ")}`;
      chainCountBadge.textContent = `${hopsState.length} / ${MAX_HOPS} 节点`;
      if (addHopBtn) addHopBtn.disabled = hopsState.length >= MAX_HOPS || (orderedNodes.length > 0 && hopsState.length >= orderedNodes.length);
    };

    const renderChainRows = () => {
      const rows = hopsState.map((hop, idx) => {
        const otherKeys = new Set(hopsState.filter((_, i) => i !== idx).map((h) => h.key).filter(Boolean));
        const availableItems = nodeOptionItems(otherKeys);
        const nodeSelect = node("select", { name: `hop_node_${idx}`, "data-hop-index": String(idx), required: modeSelect.value !== "ip" });
        nodeSelect.prepend(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")));
        syncOptions(
          nodeSelect,
          availableItems.length ? availableItems : [["", orderedNodes.length ? "无更多可选节点" : "读取节点中…", true]],
          hop.key,
        );
        if (nodeSelect.value && nodeByKey.has(nodeSelect.value)) hop.key = nodeSelect.value;

        const addrSelect = node("select", { name: `hop_addr_${idx}`, "data-hop-addr-index": String(idx), required: modeSelect.value !== "ip" });
        addrSelect.prepend(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")));

        const syncRowAddrs = (baseAddr) => {
          const item = nodeByKey.get(hop.key);
          const allAddrs = item?.capabilities?.addresses || [];
          const compat = idx === 0 ? allAddrs : compatibleAddrsForHop(item, baseAddr);
          const addrItems = compat.length
            ? compat.map((addr) => [addr, addr, false])
            : [["", allAddrs.length ? "无匹配 IP 协议族地址" : (orderedNodes.length ? "节点无可用单播地址" : (hop.address || "请选择")), !hop.address || allAddrs.length > 0]];
          syncOptions(addrSelect, addrItems, hop.address);
          if (addrSelect.value) hop.address = addrSelect.value;
          else if (orderedNodes.length) hop.address = "";
        };

        const baseAddr = idx === 0 ? "" : hopsState[0]?.address || "";
        syncRowAddrs(baseAddr);

        nodeSelect.addEventListener("change", () => {
          hop.key = nodeSelect.value;
          hop.address = "";
          renderChainRows();
        });
        addrSelect.addEventListener("change", () => {
          hop.address = addrSelect.value;
          if (idx === 0) renderChainRows();
          else updatePreviewText();
        });

        const roleLabel = idx === 0 ? "起点" : idx === hopsState.length - 1 ? "终点" : `第 ${idx + 1} 站`;
        return node("div", { className: "a-dash-probe-hop-row", "data-hop-row": String(idx) },
          node("span", { className: "a-badge a-dash-probe-hop-idx" }, roleLabel),
          node("div", { className: "a-dash-probe-hop-fields" },
            node("div", { className: "a-field" },
              node("label", { className: "a-field-label" }, idx === 0 ? "节点" : "下一跳节点"),
              nodeSelect,
            ),
            node("div", { className: "a-field" },
              node("label", { className: "a-field-label" }, "探测地址"),
              addrSelect,
            ),
          ),
          node("div", { className: "a-actions a-dash-probe-hop-ops" },
            button("↑", () => {
              if (idx <= 0) return;
              [hopsState[idx - 1], hopsState[idx]] = [hopsState[idx], hopsState[idx - 1]];
              renderChainRows();
            }, { className: "a-button", disabled: idx === 0, title: "上移节点" }),
            button("↓", () => {
              if (idx >= hopsState.length - 1) return;
              [hopsState[idx], hopsState[idx + 1]] = [hopsState[idx + 1], hopsState[idx]];
              renderChainRows();
            }, { className: "a-button", disabled: idx === hopsState.length - 1, title: "下移节点" }),
            button("移除", () => {
              if (hopsState.length <= 2) return;
              hopsState.splice(idx, 1);
              renderChainRows();
            }, { className: "a-button", tone: "danger", disabled: hopsState.length <= 2, title: "移除此跳节点" }),
          ),
        );
      });
      hopListHost.replaceChildren(...rows);
      updatePreviewText();
    };

    const syncCustomIPSrcAddrs = () => {
      const n1 = nodeByKey.get(ipSrcNodeSelect.value);
      const addrs = n1?.capabilities?.addresses || [];
      syncOptions(
        ipSrcAddrSelect,
        addrs.length ? addrs.map((addr) => [addr, addr]) : [["", orderedNodes.length ? "节点无可用单播地址" : "请选择", true]],
        ipSrcAddrSelect.value || hopsState[0]?.address,
      );
    };

    ipSrcNodeSelect.addEventListener("change", syncCustomIPSrcAddrs);

    const syncMode = () => {
      const custom = modeSelect.value === "ip";
      chainBuilder.hidden = custom;
      customIPGrid.hidden = !custom;
      ipSrcNodeSelect.required = custom;
      ipSrcAddrSelect.required = custom;
      ipInput.required = custom;
      bidirField.hidden = custom;
      if (custom) {
        bidirInput.checked = false;
        bidirInput.disabled = true;
      } else {
        bidirInput.disabled = false;
      }
      renderChainRows();
    };

    bidirInput.addEventListener("change", updatePreviewText);
    modeSelect.addEventListener("change", syncMode);
    syncMode();

    let box;
    const form = ctx.form([
      readHint,
      node("div", { className: "a-form-grid" }, nameInput, groupInput, modeField),
      chainBuilder,
      customIPGrid,
      bidirField,
      node("div", { className: "a-form-grid" },
        countField,
        intervalField,
        timeoutField,
        sizeField,
        autoSecField,
      ),
      autoEnabledField,
    ], isEdit ? "保存修改" : "添加链路", async (values, formEl) => {
      const custom = values.target_mode === "ip";
      let payload;
      if (custom) {
        const n1 = nodeByKey.get(values.ip_src_key);
        if (!n1) throw new Error("请选择有效的起点节点。");
        if (!values.ip_src_addr) throw new Error("请选择起点节点的探测地址。");
        const ip = String(values.node2_ip || "").trim();
        if (!isValidProbeIPAddress(ip)) throw new Error("请输入有效的 IPv4 或 IPv6 单播目标地址。");
        if (isIPv6(ip) !== isIPv6(values.ip_src_addr)) throw new Error("对端 IP 须与起点地址使用相同 IP 地址族。");
        payload = {
          name: String(values.name || "").trim(),
          group_name: String(values.group_name || "").trim(),
          target_mode: "ip",
          node1: {
            agent_id: n1.agent_id,
            instance_id: n1.instance_id || "",
            name: n1.name,
            address: values.ip_src_addr,
          },
          node2: { address: ip, name: ip },
          bidirectional: false,
        };
      } else {
        if (hopsState.length < 2) throw new Error("全程监测至少需要 2 个节点。");
        if (hopsState.length > MAX_HOPS) throw new Error(`单条全程监测链路最多支持 ${MAX_HOPS} 个节点。`);
        const seenAgents = new Set();
        let baseIs6 = null;
        const endpoints = hopsState.map((hop, idx) => {
          const item = nodeByKey.get(hop.key);
          if (!item) throw new Error(`请选择有效的第 ${idx + 1} 跳节点。`);
          if (seenAgents.has(item.agent_id)) throw new Error("同一条全程监测链路中不能包含重复节点。");
          seenAgents.add(item.agent_id);
          if (!hop.address) throw new Error(`第 ${idx + 1} 跳节点缺少匹配协议族的可用地址。`);
          const v6 = isIPv6(hop.address);
          if (baseIs6 === null) baseIs6 = v6;
          else if (v6 !== baseIs6) throw new Error("全链路节点必须使用相同 IP 地址族。");
          return {
            agent_id: item.agent_id,
            instance_id: item.instance_id || "",
            name: item.name,
            address: hop.address,
          };
        });
        payload = {
          name: String(values.name || "").trim(),
          group_name: String(values.group_name || "").trim(),
          target_mode: "",
          node1: endpoints[0],
          node2: endpoints[endpoints.length - 1],
          nodes: endpoints,
          bidirectional: Boolean(formEl.elements.bidirectional.checked),
        };
      }

      payload.parameters = {
        tool: "ping",
        protocol: "icmp",
        count: Number(values.count),
        interval_ms: Number(values.interval_ms),
        timeout_ms: Number(values.timeout_ms),
        size: Number(values.size),
      };
      payload.auto_enabled = Boolean(formEl.elements.auto_enabled.checked);
      payload.auto_interval_seconds = Number(values.auto_interval_seconds);
      if (isEdit) payload.revision = Number(pair.revision || 0);

      await ctx.api(isEdit ? `/dashboard/probes/${pathID(pair.id)}` : "/dashboard/probes", {
        method: isEdit ? "PUT" : "POST",
        body: payload,
      });
      if (!ctx.current() || !box?.open) return;
      box.close();
      await reloadProbes();
      ctx.announce(isEdit ? "探测链路已更新" : "探测链路已添加");
    });

    box = ctx.dialog(isEdit ? "编辑探测链路" : "添加探测链路", form, { wide: true });

    const populateNodes = (nodes) => {
      if (!ctx.current() || !box.open) return;
      diagnosticNodes = list(nodes);
      const orderIDs = getOrderIDs();
      orderedNodes = orderNodesBySavedOrder(
        diagnosticNodes.map((item) => ({ ...item, id: item.agent_id })),
        orderIDs,
      );
      nodeByKey.clear();
      orderedNodes.forEach((item) => nodeByKey.set(nodeKey(item), item));
      const srcItems = nodeOptionItems(new Set());
      syncOptions(ipSrcNodeSelect, srcItems.length ? srcItems : [["", "暂无已接入诊断节点", true]], hopsState[0]?.key || "");
      syncCustomIPSrcAddrs();
      renderChainRows();
      readHint.hidden = true;
    };

    void ctx.api("/network-diagnostics/nodes", { signal: ctx.signal })
      .then((nodes) => populateNodes(nodes))
      .catch((error) => {
        if (!ctx.current() || !box.open || error.name === "AbortError") return;
        readHint.textContent = `诊断节点读取失败：${error.message}`;
        if (diagnosticNodes.length) populateNodes(diagnosticNodes);
      });
  }

  async function mutatePair(pair, btn, action) {
    if (!canExecute() || inFlightOps.has(pair.id)) return;
    if (action === "delete") {
      if (!await ctx.confirm(`删除探测链路“${pair.name || pair.node1?.name}”及其历史采样？`, "删除链路")) return;
    }
    inFlightOps.add(pair.id);
    try {
      await ctx.mutate(btn, async () => {
        if (action === "run") {
          await ctx.api(`/dashboard/probes/${pathID(pair.id)}/run`, { method: "POST" });
          ctx.announce("已发起探测");
        } else if (action === "stop") {
          await ctx.api(`/dashboard/probes/${pathID(pair.id)}/stop`, { method: "POST" });
          ctx.announce("已发送停止指令");
        } else if (action === "toggle") {
          await ctx.api(`/dashboard/probes/${pathID(pair.id)}`, {
            method: "PUT",
            body: {
              name: pair.name || "",
              group_name: pair.group_name || "",
              target_mode: pair.target_mode || "",
              node1: pair.node1,
              node2: pair.node2,
              ...(pair.target_mode !== "ip" && Array.isArray(pair.nodes) && pair.nodes.length >= 2 ? { nodes: pair.nodes } : {}),
              bidirectional: Boolean(pair.bidirectional),
              parameters: pair.parameters,
              auto_enabled: !pair.auto_enabled,
              auto_interval_seconds: pair.auto_interval_seconds || 60,
              revision: Number(pair.revision || 0),
            },
          });
          ctx.announce(!pair.auto_enabled ? "已启用自动探测" : "已暂停自动探测");
        } else if (action === "delete") {
          await ctx.api(`/dashboard/probes/${pathID(pair.id)}`, { method: "DELETE" });
          ctx.announce("探测链路已删除");
        }
        await reloadProbes();
      });
    } finally {
      inFlightOps.delete(pair.id);
    }
  }

  const isPairAbnormal = (pair) => {
    if (pair?.availability?.ready === false) return true;
    const dirs = [pair?.latest?.forward, pair?.bidirectional ? pair?.latest?.reverse : null].filter(Boolean);
    if (dirs.some((d) => ["warn", "bad"].includes(probeDirectionStatus(d, pair.availability, pair.active_state).tone))) return true;
    const hops = Array.isArray(pair?.latest?.hops) ? pair.latest.hops : [];
    return hops.some((hop) => ["warn", "bad"].includes(probeDirectionStatus(hop?.forward, pair.availability, pair.active_state).tone)
      || (pair?.bidirectional && ["warn", "bad"].includes(probeDirectionStatus(hop?.reverse, pair.availability, pair.active_state).tone)));
  };

  const focusProbeCard = (probeID) => {
    if (!probeID || !modalBox?.open) return;
    const card = [...gridHost.querySelectorAll(".a-dash-probe-card")].find(
      (el) => el.getAttribute("data-refresh-key") === probeID,
    );
    card?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  };

  function paint(force = false) {
    if (!canView()) return;
    const raw = getProbes();
    const pairs = Array.isArray(raw?.pairs) ? raw.pairs : [];
    const probeState = getProbesState();
    const summary = summarizeDashboardProbes(pairs);
    const exec = canExecute();
    const abnormalPairs = pairs.filter(isPairAbnormal);
    // Preserve saved "abnormal" preference when focusing a probe or before probes finish loading.
    const effectiveFilterMode = (focusProbeID || !abnormalPairs.length) ? "all" : filterMode;
    const visiblePairs = effectiveFilterMode === "abnormal" ? abnormalPairs : pairs;
    const sampledLabels = visiblePairs.map((p) => (p.latest?.sampled_at ? date(p.latest.sampled_at) : "未采样"));
    const sig = JSON.stringify([probeState, exec, sparkWidth, effectiveFilterMode, focusProbeID, summary, visiblePairs, sampledLabels]);
    if (!force && sig === lastPaintSignature) return;
    lastPaintSignature = sig;

    const showFilter = pairs.length > 0 && (abnormalPairs.length > 0 || filterMode === "abnormal");
    patch(summaryNode, node("div", { className: "a-actions", role: "status" },
      pairs.length ? keyed("count", badge(`自动 ${summary.enabled}/${summary.total}`)) : null,
      summary.running > 0 ? keyed("running", badge(`进行中 ${summary.running}`, "warn")) : null,
      summary.bad > 0 ? keyed("abnormal", badge(`异常 ${summary.bad}`, "bad")) : null,
      showFilter ? button(`全部 ${pairs.length}`, () => {
        filterModeTouched = true;
        if (filterMode === "all" && !focusProbeID) return;
        const changed = filterMode !== "all";
        filterMode = "all";
        focusProbeID = "";
        if (changed) ctx.preferences?.patch?.("dashboard", { probe_filter: "all" });
        paint(true);
      }, {
        className: "a-button a-dash-probe-filter",
        "aria-pressed": effectiveFilterMode === "all" ? "true" : "false",
        "data-refresh-key": "filter-all",
      }) : null,
      showFilter ? button(`仅告警 ${abnormalPairs.length}`, () => {
        filterModeTouched = true;
        if (filterMode === "abnormal" && !focusProbeID) return;
        const changed = filterMode !== "abnormal";
        filterMode = "abnormal";
        focusProbeID = "";
        if (changed) ctx.preferences?.patch?.("dashboard", { probe_filter: "abnormal" });
        paint(true);
      }, {
        className: "a-button a-dash-probe-filter",
        "aria-pressed": effectiveFilterMode === "abnormal" ? "true" : "false",
        "data-refresh-key": "filter-abnormal",
      }) : null,
      probeState === "failed" ? keyed("failed", badge("读取失败 · 保留上次数据", "warn")) : null,
      probeState === "denied" ? keyed("denied", badge("无诊断读取权限", "warn")) : null,
    ));

    if (!visiblePairs.length) {
      const message = probeState === "loading"
        ? "读取中…"
        : probeState === "failed"
          ? "读取失败"
          : "暂无探测链路";
      patch(gridHost, node("div", { className: "a-dash-probe-grid" }, empty(message)));
      return;
    }

    const colMode = visiblePairs.length <= 1 ? "1" : "2";
    const cards = visiblePairs.map((pair, pairIdx) => {
      const endpoints = resolvedPairEndpoints(pair);
      const isChain = endpoints.length > 2;
      const startEp = endpoints[0] || {};
      const endEp = endpoints[endpoints.length - 1] || {};
      const aName = startEp.name || "节点 A";
      const bName = endEp.name || "节点 B";
      const aFlag = nodeRegionFlag(ctx, { id: startEp.agent_id, region_code: startEp.region });
      const bFlag = pair.target_mode === "ip" ? null : nodeRegionFlag(ctx, { id: endEp.agent_id, region_code: endEp.region });
      const arrow = pair.bidirectional ? " ⇄ " : " → ";
      const routeTitle = endpoints.map((ep) => ep.name).join(arrow);
      const title = pair.name || routeTitle;
      const isRunning = Boolean(pair.availability?.running || ["preparing", "running", "stopping", "cleanup_pending"].includes(pair.active_state));
      const isFocused = Boolean(focusProbeID && pair.id === focusProbeID);

      const fwdSeries = probeHistorySeries(pair.history, "forward");
      const revSeries = pair.bidirectional ? probeHistorySeries(pair.history, "reverse") : [];
      const hasSpark = fwdSeries.length >= 2 || revSeries.length >= 2;
      const trend = hasSpark ? renderProbeHistoryChart(fwdSeries, pair.bidirectional ? revSeries : [], sparkWidth, pair.id) : null;

      const laneReasons = [pair.latest?.forward, pair.bidirectional ? pair.latest?.reverse : null]
        .filter(Boolean)
        .map((d) => probeDirectionStatus(d, pair.availability, pair.active_state).label);
      const showOuterReason = Boolean(!pair.availability?.ready && pair.availability?.reason && !laneReasons.includes(pair.availability.reason));
      const hopSpec = isChain ? `${endpoints.length} 站 ${endpoints.length - 1} 跳` : "";
      const specText = [
        hopSpec,
        pair.bidirectional ? "去/回双程" : "单向",
        `ICMP ×${pair.parameters?.count || 5}`,
        pair.auto_enabled ? `${pair.auto_interval_seconds}s` : "暂停",
      ].filter(Boolean).join(" · ");
      const trendLabel = trend
        ? (pair.bidirectional
            ? "实线 去程 · 虚线 回程"
            : trend.peakMS != null
              ? `近 ${fwdSeries.length} 次 · 峰值 ${fmtMS(trend.peakMS)}`
              : `近 ${fwdSeries.length} 次 · 丢包`)
        : (showOuterReason ? pair.availability.reason : "—");

      const hopItems = isChain
        ? endpoints.slice(0, -1).map((fromEp, hopIdx) => {
            const toEp = endpoints[hopIdx + 1];
            const hopData = Array.isArray(pair.latest?.hops)
              ? (pair.latest.hops.find((h) => h.hop_index === hopIdx) || pair.latest.hops[hopIdx])
              : null;
            const fwdSummary = summarizeHopMetricText(hopData?.forward, pair.availability, pair.active_state);
            const revSummary = pair.bidirectional
              ? summarizeHopMetricText(hopData?.reverse, pair.availability, pair.active_state)
              : null;
            const hopTone = fwdSummary.tone === "bad" || revSummary?.tone === "bad"
              ? "bad"
              : fwdSummary.tone === "warn" || revSummary?.tone === "warn"
                ? "warn"
                : fwdSummary.tone === "ok" && (!revSummary || revSummary.tone === "ok")
                  ? "ok"
                  : "";
            return node("div", { className: `a-dash-probe-hop-item${hopTone ? ` is-${hopTone}` : ""}` },
              node("span", { className: "a-dash-probe-hop-seg" }, `${hopIdx + 1}. ${fromEp.name}${pair.bidirectional ? " ⇄ " : " → "}${toEp.name}`),
              node("span", { className: "a-dash-probe-hop-metrics" },
                `去 ${fwdSummary.text}${revSummary ? ` · 回 ${revSummary.text}` : ""}`,
              ),
            );
          })
        : null;

      return node("article", {
        className: `a-card a-dash-probe-card${isFocused ? " is-focused" : ""}`,
        "data-refresh-key": pair.id,
      },
        node("div", { className: "a-dash-probe-head" },
          node("div", { className: "a-dash-probe-title" },
            node("div", { className: "a-dash-probe-tags" },
              pair.group_name ? badge(pair.group_name, "") : null,
              isChain ? badge(`全程 ${endpoints.length} 站`, "") : null,
            ),
            node("h3", {}, title),
            pair.name ? node("p", { className: "a-muted a-dash-probe-route" }, routeTitle) : null,
            muted(specText),
          ),
          keyed("status", isRunning ? badge("探测中", "warn") : pair.availability?.ready === false ? badge("不可用", "warn") : badge(pair.auto_enabled ? "监控中" : "已暂停", pair.auto_enabled ? "ok" : "")),
        ),
        node("div", { className: "a-dash-probe-lanes" },
          directionBlock({
            key: "fwd",
            title: isChain ? "去程全程" : "去程",
            fromName: aName,
            fromAddress: startEp.address || pair.node1?.address || "—",
            toName: bName,
            toAddress: pair.target_mode === "ip" ? "" : (endEp.address || pair.node2?.address || "—"),
            fromFlag: aFlag,
            toFlag: bFlag,
            dir: pair.latest?.forward,
            pair,
          }),
          pair.bidirectional ? directionBlock({
            key: "rev",
            title: isChain ? "回程全程" : "回程",
            fromName: bName,
            fromAddress: endEp.address || pair.node2?.address || "—",
            toName: aName,
            toAddress: startEp.address || pair.node1?.address || "—",
            fromFlag: bFlag,
            toFlag: aFlag,
            dir: pair.latest?.reverse,
            pair,
          }) : null,
        ),
        hopItems ? node("div", { className: "a-dash-probe-hops" }, hopItems) : null,
        node("div", { className: "a-dash-probe-spark" },
          trend?.svg ? keyed("spark-svg", trend.svg) : null,
          node("div", { className: "a-chart-meta", "data-refresh-key": "spark-meta" },
            muted(trendLabel),
            muted(sampledLabels[pairIdx]),
          ),
          trend && showOuterReason ? keyed("spark-reason", muted(pair.availability.reason)) : null,
        ),
        exec ? node("div", { className: "a-dash-probe-actions a-actions" },
          isRunning
            ? button("停止", (e) => void mutatePair(pair, e.currentTarget, "stop"), { "data-refresh-key": "stop" })
            : button("立即探测", (e) => void mutatePair(pair, e.currentTarget, "run"), { disabled: pair.availability?.ready === false, title: pair.availability?.reason || "", "data-refresh-key": "run" }),
          button(pair.auto_enabled ? "暂停自动" : "开启自动", (e) => void mutatePair(pair, e.currentTarget, "toggle"), { "data-refresh-key": "toggle" }),
          button("编辑", () => openProbeDialog(pair), { "data-refresh-key": "edit" }),
          button("删除", (e) => void mutatePair(pair, e.currentTarget, "delete"), { tone: "danger", "data-refresh-key": "delete" }),
        ) : null,
      );
    });

    patch(gridHost, node("div", { className: "a-dash-probe-grid", "data-cols": colMode }, cards));
  }

  function openModal(targetProbeID = "", options = {}) {
    if (!canView()) return;
    if (options?.create) {
      openProbeDialog(null, { groupName: options.groupName || "" });
      return;
    }
    if (options?.edit && targetProbeID) {
      const target = (getProbes()?.pairs || []).find((item) => item.id === targetProbeID);
      if (target) {
        openProbeDialog(target);
        return;
      }
    }
    focusProbeID = String(targetProbeID || "");
    paint(true);
    if (!modalBox || !modalBox.open) {
      modalBox = ctx.dialog("节点链路监控", modalContent, { wide: true });
      modalBox.classList.add("a-dash-probe-dialog");
      modalBox.addEventListener("close", () => {
        modalBox = null;
        focusProbeID = "";
      }, { once: true });
    }
    requestAnimationFrame(() => {
      if (!modalBox?.open || !ctx.current()) return;
      const sampleSpark = gridHost.querySelector(".a-dash-probe-spark");
      const next = sampleSpark ? sampleSpark.clientWidth - 32 : 0;
      if (next > 40 && Math.abs(next - sparkWidth) >= 4) {
        sparkWidth = next;
        paint(true);
      }
      focusProbeCard(focusProbeID);
    });
  }

  return {
    canView: canView(),
    open: openModal,
    paint,
  };
}
