import { patch } from "./live-dom.js";
import { badge, node, section } from "./ui.js";

const SVG_NS = "http://www.w3.org/2000/svg";

export function createSVGEl(tag, attrs = {}, text = "") {
  const element = document.createElementNS(SVG_NS, tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value != null && value !== false) element.setAttribute(key, String(value));
  }
  if (text) element.textContent = text;
  return element;
}

/**
 * Mounts an SVG whose viewBox tracks the container's real CSS pixel width so circles,
 * dash patterns and stroke weights never distort across breakpoints.
 */
export function createResponsiveSVG(hostClass, signal, { height, fallbackWidth = 300, draw }) {
  const host = node("div", { className: hostClass ? `a-dash-svg-host ${hostClass}` : "a-dash-svg-host" });
  let width = fallbackWidth;
  let observedWidth = 0;
  let currentData;
  let drawn = false;
  let lastSignature = "";

  const paintNow = () => {
    const measured = observedWidth > 0 ? observedWidth : (host.clientWidth || 0);
    if (measured > 0) width = measured;
    const renderWidth = Math.max(80, Math.round(width));
    const sig = JSON.stringify([renderWidth, currentData ?? null]);
    if (drawn && sig === lastSignature) return;
    const svg = draw({ width: renderWidth, height }, currentData);
    if (!svg) return;
    if (host.firstChild) patch(host.firstChild, svg);
    else host.append(svg);
    lastSignature = sig;
    drawn = true;
  };

  if (typeof ResizeObserver === "function") {
    const observer = new ResizeObserver((entries) => {
      const next = Math.round(entries[0]?.contentRect?.width || 0);
      if (next > 0) {
        observedWidth = next;
        if (Math.abs(next - width) >= 2 || !drawn) {
          width = next;
          paintNow();
        }
      }
    });
    observer.observe(host);
    signal?.addEventListener("abort", () => observer.disconnect(), { once: true });
  }

  return {
    element: host,
    update(data) {
      currentData = data;
      paintNow();
    },
  };
}

function finiteNumber(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function finiteNonNegative(value) {
  const num = finiteNumber(value);
  return num != null && num >= 0 ? num : null;
}

function clampPercent(value) {
  const num = finiteNonNegative(value);
  if (num == null) return null;
  return Math.max(0, Math.min(100, num));
}

const NO_SAMPLE = Object.freeze(Object.create(null));
const NO_HISTORY = Object.freeze(Array.of());
const NBSP = String.fromCharCode(160);

// Compact single-resource gauge with native smooth rounded progress bar.
function createGaugeSlot(label, key) {
  const valEl = node("strong", { "data-refresh-key": "value" }, "—");
  const metaEl = node("span", { className: "a-host-gauge-meta", "data-refresh-key": "amount" }, "");
  const barEl = node("progress", { className: "a-con-progress", max: "100", value: "0", "aria-hidden": "true" });
  const element = node("div", { className: "a-host-gauge", "data-refresh-key": key },
    node("div", { className: "a-host-gauge-head" },
      node("span", { className: "a-con-label" }, label),
      valEl),
    barEl,
    metaEl);
  return {
    element,
    setPercent(pct, detail) {
      valEl.textContent = pct == null ? "—" : `${pct.toFixed(1)}%`;
      metaEl.textContent = detail;
      barEl.value = pct == null ? 0 : pct;
      barEl.className = pct == null
        ? "a-con-progress"
        : pct >= 90
          ? "a-con-progress is-bad"
          : pct >= 75
            ? "a-con-progress is-warn"
            : "a-con-progress";
    },
  };
}

// Calm, compact dual-trace sparkline (rx solid with soft wash, tx dashed).
// Removes vertical engineering grid noise for clean scannability.
function drawDualOscilloscope({ width, height }, data) {
  const rxSeries = data?.rx || [];
  const txSeries = data?.tx || [];
  const padL = 4;
  const padR = 4;
  const padT = 7;
  const padB = 6;
  const usableW = Math.max(20, width - padL - padR);
  const usableH = Math.max(16, height - padT - padB);
  const rawPeak = Math.max(64, ...(rxSeries.filter(Number.isFinite)), ...(txSeries.filter(Number.isFinite)));
  const peak = rawPeak * 1.18;

  const svg = createSVGEl("svg", {
    class: "a-dash-spark is-osc-compact",
    viewBox: `0 0 ${width} ${height}`,
    "aria-hidden": "true",
  });

  // Subtle horizontal reference guides only (midline + baseline).
  svg.append(
    createSVGEl("line", { x1: padL, y1: (padT + usableH * 0.5).toFixed(1), x2: width - padR, y2: (padT + usableH * 0.5).toFixed(1), class: "a-dash-spark-baseline" }),
    createSVGEl("line", { x1: padL, y1: (height - padB).toFixed(1), x2: width - padR, y2: (height - padB).toFixed(1), class: "a-dash-osc-axis" }),
  );

  const count = Math.max(rxSeries.length, txSeries.length);
  const buildPath = (series) => {
    const pts = [];
    series.forEach((val, idx) => {
      if (val == null || !Number.isFinite(val)) return;
      const x = count <= 1 ? width - padR : padL + (idx / (count - 1)) * usableW;
      const y = padT + usableH - (Math.max(0, val) / peak) * usableH;
      pts.push([x, y]);
    });
    return pts;
  };

  const rxPts = buildPath(rxSeries);
  const txPts = buildPath(txSeries);

  if (rxPts.length >= 2) {
    const gradID = data?.gradID || "a-osc-grad";
    const defs = createSVGEl("defs");
    const grad = createSVGEl("linearGradient", { id: gradID, x1: "0", y1: "0", x2: "0", y2: "1" });
    grad.append(
      createSVGEl("stop", { offset: "0%", "stop-color": "#18181b", "stop-opacity": "0.10" }),
      createSVGEl("stop", { offset: "100%", "stop-color": "#18181b", "stop-opacity": "0.0" }),
    );
    defs.append(grad);
    svg.append(defs);

    const rxD = rxPts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
    const rxLast = rxPts.at(-1);
    svg.append(
      createSVGEl("path", { d: `${rxD} L${rxLast[0].toFixed(1)} ${(height - padB).toFixed(1)} L${rxPts[0][0].toFixed(1)} ${(height - padB).toFixed(1)} Z`, fill: `url(#${gradID})` }),
      createSVGEl("path", { d: rxD, class: "a-dash-spark-line" }),
    );
  }
  if (txPts.length >= 2) {
    const txD = txPts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
    svg.append(createSVGEl("path", { d: txD, class: "a-dash-spark-secondary" }));
  }

  if (rxPts.length) {
    const [rxX, rxY] = rxPts.at(-1);
    svg.append(createSVGEl("circle", { cx: rxX.toFixed(1), cy: rxY.toFixed(1), r: "2.8", class: "a-dash-osc-dot-rx" }));
  }
  if (txPts.length) {
    const [txX, txY] = txPts.at(-1);
    svg.append(createSVGEl("circle", { cx: txX.toFixed(1), cy: txY.toFixed(1), r: "2.5", class: "a-dash-osc-dot-tx" }));
  }
  return svg;
}

function readSampleSeries(history) {
  const rxSeries = [];
  const txSeries = [];
  for (const sample of history) {
    let rateOK = Boolean(sample?.network_available) && (sample?.network_rate_available === undefined || Boolean(sample.network_rate_available));
    if (sample?.network_rate_valid !== undefined) rateOK = Boolean(sample.network_rate_valid);
    rxSeries.push(rateOK ? finiteNonNegative(sample?.network_rx_bps) : null);
    txSeries.push(rateOK ? finiteNonNegative(sample?.network_tx_bps) : null);
  }
  return { rxSeries, txSeries };
}

// "3.5 / 8 GB" when both amounts share a unit, else both in full.
function usageNote(used, total) {
  const cut = used.lastIndexOf(NBSP);
  const unit = cut > 0 ? used.slice(cut) : "";
  return unit && total.endsWith(unit) ? `${used.slice(0, cut)} / ${total}` : `${used} / ${total}`;
}

export function createPanelHostController(ctx, { bytes, date }) {
  const headBadges = node("div", { className: "a-actions", role: "status" });
  const subTime = node("span", { className: "a-host-foot-time" }, "读取中…");
  let lastRenderSignature = "";

  const keyedBadge = (key, label, tone = "") => {
    const el = badge(label, tone);
    el.setAttribute("data-refresh-key", key);
    return el;
  };

  const netOsc = createResponsiveSVG("a-host-spark-wrap", ctx.signal, { height: 60, fallbackWidth: 320, draw: drawDualOscilloscope });
  const cpuGauge = createGaugeSlot("CPU", "CPU 使用率");
  const memGauge = createGaugeSlot("内存", "内存使用率");
  const diskGauge = createGaugeSlot("磁盘", "根目录磁盘");
  const rxValEl = node("strong", { "data-refresh-key": "rx" }, "—");
  const txValEl = node("strong", { "data-refresh-key": "tx" }, "—");
  const oscSummaryEl = node("span", { className: "a-host-peak" }, "");

  const netBand = node("div", { className: "a-host-net" },
    node("div", { className: "a-host-net-head" },
      node("div", { className: "a-host-rates" },
        node("span", { className: "a-host-rate-item" },
          node("span", { className: "a-chart-key-rx", "aria-hidden": "true" }),
          "接收 ",
          rxValEl),
        node("span", { className: "a-host-rate-item is-tx" },
          node("span", { className: "a-chart-key-tx", "aria-hidden": "true" }),
          "发送 ",
          txValEl)),
      oscSummaryEl),
    netOsc.element,
    node("div", { className: "a-host-foot" }, subTime));

  const body = node("div", { className: "a-host-body" },
    node("div", { className: "a-host-gauges" }, cpuGauge.element, memGauge.element, diskGauge.element),
    netBand);

  const element = section("面板主机", body, headBadges);
  element.classList.add("a-dash-host-card");

  function paint(sample) {
    const m = sample.m || NO_SAMPLE;
    const status = sample.status || "";
    const cpuPct = m.cpu_available ? clampPercent(m.cpu_percent) : null;
    const memTotal = finiteNonNegative(m.memory_total_bytes);
    const memUsed = finiteNonNegative(m.memory_used_bytes);
    const memOK = Boolean(m.memory_available) && memTotal > 0 && memUsed != null;
    const memPct = memOK ? clampPercent((memUsed / memTotal) * 100) : null;
    const diskTotal = finiteNonNegative(m.disk_total_bytes);
    const diskUsed = finiteNonNegative(m.disk_used_bytes);
    const diskOK = Boolean(m.disk_available) && diskTotal > 0 && diskUsed != null;
    const diskPct = diskOK ? clampPercent((diskUsed / diskTotal) * 100) : null;

    const netRateOK = Boolean(m.network_available) && (m.network_rate_available === undefined || Boolean(sample?.m?.network_rate_available));
    const rxBps = netRateOK ? finiteNonNegative(m.network_rx_bps) : null;
    const txBps = netRateOK ? finiteNonNegative(m.network_tx_bps) : null;

    const series = readSampleSeries(sample.history || NO_HISTORY);
    const time = Date.parse(m.collected_at);
    const collected = Number.isFinite(time) && time > 0;
    const stale = collected && Date.now() - time > 15000;
    let subTimeText = "";
    if (collected) {
      const formattedTime = date(m.collected_at);
      subTimeText = status === "failed" ? `${formattedTime} · 保留上次采样` : formattedTime;
    } else if (status === "denied") {
      subTimeText = "无面板主机指标权限";
    } else if (status === "failed") {
      subTimeText = "资源采样读取失败";
    }

    const renderSignature = JSON.stringify([
      status, collected, stale, subTimeText,
      cpuPct, memPct, memUsed, memTotal, diskPct, diskUsed, diskTotal,
      rxBps, txBps, series.rxSeries, series.txSeries,
    ]);
    if (renderSignature === lastRenderSignature) return;
    lastRenderSignature = renderSignature;

    cpuGauge.setPercent(cpuPct, collected ? "容器可见" : "");
    memGauge.setPercent(memPct, memOK ? usageNote(bytes(memUsed), bytes(memTotal)) : "");
    diskGauge.setPercent(diskPct, diskOK ? usageNote(bytes(diskUsed), bytes(diskTotal)) : "");

    rxValEl.textContent = rxBps == null ? "—" : `${bytes(rxBps)}/s`;
    txValEl.textContent = txBps == null ? "—" : `${bytes(txBps)}/s`;

    const finiteRates = [...series.rxSeries, ...series.txSeries].filter((v) => v != null && Number.isFinite(v));
    oscSummaryEl.textContent = finiteRates.length >= 2 ? `峰值 ${bytes(Math.max(...finiteRates))}/s` : "";
    netOsc.update({ rx: series.rxSeries, tx: series.txSeries, gradID: "a-osc-grad-panel-net" });

    let stateBadge = keyedBadge("state", "尚无有效采样", "warn");
    if (status === "denied") stateBadge = keyedBadge("state", "无读取权限", "bad");
    else if (status === "failed") stateBadge = keyedBadge("state", collected ? "刷新失败" : "读取失败", "warn");
    else if (collected) stateBadge = keyedBadge("state", stale ? "采样已过期" : "实时", stale ? "warn" : "ok");
    patch(headBadges, node("div", { className: "a-actions", role: "status" }, stateBadge));

    if (subTime.textContent !== subTimeText) subTime.textContent = subTimeText;
  }

  return { element, paint };
}
