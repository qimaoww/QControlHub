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

  function openProbeDialog(pair = null) {
    if (!canExecute()) return;
    const isEdit = Boolean(pair?.id);
    const params = pair?.parameters || {};
    const initialMode = pair?.target_mode === "ip" ? "ip" : "node";

    const nameInput = field("探测名称", "name", pair?.name || "", { maxlength: 80, placeholder: "可选" });
    const modeField = field("目标模式", "target_mode", initialMode, {
      options: [["node", "托管节点"], ["ip", "自定义 IP"]],
    });
    const node1Field = field("起点节点", "node1_key", pair ? nodeKey(pair.node1) : "", {
      options: [["", "读取中…"]],
      required: true,
    });
    const addr1Field = field("起点地址", "node1_addr", pair?.node1?.address || "", {
      options: pair?.node1?.address ? [[pair.node1.address, pair.node1.address]] : [["", "请选择"]],
      required: true,
    });
    const node2Field = field("对端节点", "node2_key", pair && initialMode === "node" ? nodeKey(pair.node2) : "", {
      options: [["", "读取中…"]],
    });
    const addr2Field = field("对端地址", "node2_addr", pair && initialMode === "node" ? pair?.node2?.address || "" : "", {
      options: pair?.node2?.address ? [[pair.node2.address, pair.node2.address]] : [["", "请选择"]],
    });
    const customIPField = field("对端 IP", "node2_ip", pair && initialMode === "ip" ? pair?.node2?.address || "" : "", {
      placeholder: "IPv4 / IPv6",
    });
    const bidirField = field("双向链路测量", "bidirectional", Boolean(pair?.bidirectional), {
      type: "checkbox",
    });

    const countField = field("探测次数", "count", params.count || 5, { type: "number", min: 1, max: 100, required: true });
    const intervalField = field("间隔 (ms)", "interval_ms", params.interval_ms || 200, { type: "number", min: 200, max: 5000, required: true });
    const timeoutField = field("超时 (ms)", "timeout_ms", params.timeout_ms || 1000, { type: "number", min: 100, max: 5000, required: true });
    const sizeField = field("负载 (B)", "size", params.size ?? 56, { type: "number", min: 0, max: 1400, required: true });
    const autoEnabledField = field("自动周期探测", "auto_enabled", pair ? Boolean(pair.auto_enabled) : true, { type: "checkbox" });
    const autoSecField = field("周期 (s)", "auto_interval_seconds", pair?.auto_interval_seconds || 60, { type: "number", min: 15, max: 3600, required: true });
    const readHint = muted("读取节点中…");

    const modeSelect = modeField.querySelector("select");
    const n1Select = node1Field.querySelector("select");
    const a1Select = addr1Field.querySelector("select");
    const n2Select = node2Field.querySelector("select");
    const a2Select = addr2Field.querySelector("select");
    const ipInput = customIPField.querySelector("input");
    const bidirInput = bidirField.querySelector("input");

    let orderedNodes = [];
    const nodeByKey = new Map();

    const syncOptions = (selectEl, items, preferred) => {
      const btn = selectEl.querySelector("button");
      const opts = items.map(([val, label, disabled = false]) => node("option", { value: val, disabled }, label));
      selectEl.replaceChildren(...(btn ? [btn] : []), ...opts);
      if (items.some(([val, , dis]) => val === preferred && !dis)) selectEl.value = preferred;
      else selectEl.value = items.find(([, , dis]) => !dis)?.[0] || "";
    };

    const syncNode2Addrs = () => {
      const a1 = a1Select.value;
      const n2 = nodeByKey.get(n2Select.value);
      const allAddrs = n2?.capabilities?.addresses || [];
      const compat = a1 ? allAddrs.filter((addr) => isIPv6(addr) === isIPv6(a1)) : allAddrs;
      const shown = compat.length ? compat : [];
      syncOptions(
        a2Select,
        shown.length ? shown.map((addr) => [addr, addr]) : [["", allAddrs.length ? "无匹配 IP 协议族地址" : "节点无可用单播地址", true]],
        pair?.node2?.address,
      );
    };

    const syncNode2 = () => {
      const n1 = nodeByKey.get(n1Select.value);
      const targets = orderedNodes.filter((item) => item.agent_id !== n1?.agent_id);
      const items = targets.map((item) => {
        const key = nodeKey(item);
        const suffix = !item.online ? " · 离线" : item.instance_id && item.instance_id !== "main" ? ` · ${item.context || item.instance_id}` : "";
        return [key, `${item.name}${suffix}`, false];
      });
      syncOptions(n2Select, items.length ? items : [["", "暂无其他托管节点", true]], n2Select.value || (pair ? nodeKey(pair.node2) : ""));
      syncNode2Addrs();
    };

    const syncNode1Addrs = () => {
      const n1 = nodeByKey.get(n1Select.value);
      const addrs = n1?.capabilities?.addresses || [];
      syncOptions(
        a1Select,
        addrs.length ? addrs.map((addr) => [addr, addr]) : [["", "节点无可用单播地址", true]],
        a1Select.value || pair?.node1?.address,
      );
      syncNode2();
    };

    const syncMode = () => {
      const custom = modeSelect.value === "ip";
      node2Field.hidden = custom;
      addr2Field.hidden = custom;
      customIPField.hidden = !custom;
      n2Select.required = !custom;
      a2Select.required = !custom;
      ipInput.required = custom;
      bidirField.hidden = custom;
      if (custom) {
        bidirInput.checked = false;
        bidirInput.disabled = true;
      } else {
        bidirInput.disabled = false;
      }
    };

    n1Select.addEventListener("change", syncNode1Addrs);
    a1Select.addEventListener("change", syncNode2Addrs);
    n2Select.addEventListener("change", syncNode2Addrs);
    modeSelect.addEventListener("change", syncMode);
    syncMode();

    let box;
    const form = ctx.form([
      readHint,
      nameInput,
      node("div", { className: "a-form-grid" },
        node1Field,
        addr1Field,
        modeField,
        node2Field,
        addr2Field,
        customIPField,
      ),
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
      const n1 = nodeByKey.get(values.node1_key);
      if (!n1) throw new Error("请选择有效的起点节点。");
      if (!values.node1_addr) throw new Error("请选择起点节点的探测地址。");
      const custom = values.target_mode === "ip";
      let n2Endpoint;
      if (custom) {
        const ip = String(values.node2_ip || "").trim();
        if (!isValidProbeIPAddress(ip)) throw new Error("请输入有效的 IPv4 或 IPv6 单播目标地址。");
        if (isIPv6(ip) !== isIPv6(values.node1_addr)) throw new Error("对端 IP 须与起点地址使用相同 IP 地址族。");
        n2Endpoint = { address: ip, name: ip };
      } else {
        const n2 = nodeByKey.get(values.node2_key);
        if (!n2) throw new Error("请选择有效的对端节点。");
        if (!values.node2_addr) throw new Error("对端节点缺少匹配协议族的可用地址。");
        n2Endpoint = {
          agent_id: n2.agent_id,
          instance_id: n2.instance_id || "",
          name: n2.name,
          address: values.node2_addr,
        };
      }
      const payload = {
        name: String(values.name || "").trim(),
        target_mode: custom ? "ip" : "",
        node1: {
          agent_id: n1.agent_id,
          instance_id: n1.instance_id || "",
          name: n1.name,
          address: values.node1_addr,
        },
        node2: n2Endpoint,
        bidirectional: !custom && Boolean(formEl.elements.bidirectional.checked),
        parameters: {
          tool: "ping",
          protocol: "icmp",
          count: Number(values.count),
          interval_ms: Number(values.interval_ms),
          timeout_ms: Number(values.timeout_ms),
          size: Number(values.size),
        },
        auto_enabled: Boolean(formEl.elements.auto_enabled.checked),
        auto_interval_seconds: Number(values.auto_interval_seconds),
        ...(isEdit ? { revision: Number(pair.revision || 0) } : {}),
      };
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
      const n1Items = orderedNodes.map((item) => [
        nodeKey(item),
        `${item.name}${!item.online ? " · 离线" : item.instance_id && item.instance_id !== "main" ? ` · ${item.context || item.instance_id}` : ""}`,
        false,
      ]);
      syncOptions(n1Select, n1Items.length ? n1Items : [["", "暂无已接入诊断节点", true]], pair ? nodeKey(pair.node1) : "");
      syncNode1Addrs();
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
              target_mode: pair.target_mode || "",
              node1: pair.node1,
              node2: pair.node2,
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
    return dirs.some((d) => ["warn", "bad"].includes(probeDirectionStatus(d, pair.availability, pair.active_state).tone));
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

    const showFilter = abnormalPairs.length > 0 && abnormalPairs.length < pairs.length;
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
      const aName = pair.node1_name || pair.node1?.name || pair.node1?.agent_id || "节点 A";
      const bName = pair.target_mode === "ip"
        ? pair.node2?.address || "自定义 IP"
        : pair.node2_name || pair.node2?.name || pair.node2?.agent_id || "节点 B";
      const aFlag = nodeRegionFlag(ctx, { id: pair.node1?.agent_id, region_code: pair.node1_region });
      const bFlag = pair.target_mode === "ip" ? null : nodeRegionFlag(ctx, { id: pair.node2?.agent_id, region_code: pair.node2_region });
      const title = pair.name || `${aName} ${pair.bidirectional ? "↔" : "→"} ${bName}`;
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
      const specText = `${pair.bidirectional ? "双向" : "单向"} · ICMP ×${pair.parameters?.count || 5} · ${pair.auto_enabled ? `${pair.auto_interval_seconds}s` : "暂停"}`;
      const trendLabel = trend
        ? (pair.bidirectional
            ? "实线 去程 · 虚线 回程"
            : trend.peakMS != null
              ? `近 ${fwdSeries.length} 次 · 峰值 ${fmtMS(trend.peakMS)}`
              : `近 ${fwdSeries.length} 次 · 丢包`)
        : (showOuterReason ? pair.availability.reason : "—");

      return node("article", {
        className: `a-card a-dash-probe-card${isFocused ? " is-focused" : ""}`,
        "data-refresh-key": pair.id,
      },
        node("div", { className: "a-dash-probe-head" },
          node("div", { className: "a-dash-probe-title" },
            node("h3", {}, title),
            muted(specText),
          ),
          keyed("status", isRunning ? badge("探测中", "warn") : pair.availability?.ready === false ? badge("不可用", "warn") : badge(pair.auto_enabled ? "监控中" : "已暂停", pair.auto_enabled ? "ok" : "")),
        ),
        node("div", { className: "a-dash-probe-lanes" },
          directionBlock({
            key: "fwd",
            title: "去程",
            fromName: aName,
            fromAddress: pair.node1?.address || "—",
            toName: bName,
            toAddress: pair.target_mode === "ip" ? "" : (pair.node2?.address || "—"),
            fromFlag: aFlag,
            toFlag: bFlag,
            dir: pair.latest?.forward,
            pair,
          }),
          pair.bidirectional ? directionBlock({
            key: "rev",
            title: "回程",
            fromName: bName,
            fromAddress: pair.node2?.address || "—",
            toName: aName,
            toAddress: pair.node1?.address || "—",
            fromFlag: bFlag,
            toFlag: aFlag,
            dir: pair.latest?.reverse,
            pair,
          }) : null,
        ),
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

  function openModal(targetProbeID = "") {
    if (!canView()) return;
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
