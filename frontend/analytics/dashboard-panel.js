import { patch } from "./live-dom.js";
import { actions, badge, muted, node, section } from "./ui.js";

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

function polarPoint(cx, cy, r, deg) {
  const rad = ((deg - 90) * Math.PI) / 180;
  return [cx + r * Math.cos(rad), cy + r * Math.sin(rad)];
}

function arcSegmentPath(cx, cy, r, startDeg, endDeg) {
  const [x1, y1] = polarPoint(cx, cy, r, startDeg);
  const [x2, y2] = polarPoint(cx, cy, r, endDeg);
  const large = endDeg - startDeg > 180 ? 1 : 0;
  return `M ${x1.toFixed(2)} ${y1.toFixed(2)} A ${r} ${r} 0 ${large} 1 ${x2.toFixed(2)} ${y2.toFixed(2)}`;
}

const DIAL_TICKS = [-120, -90, -60, -30, 0, 30, 60, 90, 120].map((deg) => {
  const [x1, y1] = polarPoint(54, 48, 39, deg);
  const [x2, y2] = polarPoint(54, 48, 43, deg);
  return { x1: x1.toFixed(2), y1: y1.toFixed(2), x2: x2.toFixed(2), y2: y2.toFixed(2) };
});

function createDialSlot(label, key) {
  const valEl = node("strong", {}, "—");
  const metaEl = node("span", { className: "a-dash-dial-meta a-muted" }, "\u00a0");
  const r = 34;
  const sweep = 240;
  const arcLen = (2 * Math.PI * r * sweep) / 360;
  const trackD = arcSegmentPath(54, 48, r, -120, 120);
  const arcEl = createSVGEl("path", {
    class: "a-dash-dial-arc",
    d: trackD,
    "stroke-dasharray": `0.001 ${arcLen.toFixed(2)}`,
    opacity: "0",
  });
  const svgEl = createSVGEl("svg", {
    class: "a-dash-dial-svg",
    viewBox: "0 0 108 80",
    "aria-hidden": "true",
  });
  for (const tick of DIAL_TICKS) {
    svgEl.append(createSVGEl("line", { class: "a-dash-dial-tick", x1: tick.x1, y1: tick.y1, x2: tick.x2, y2: tick.y2 }));
  }
  svgEl.append(createSVGEl("path", { class: "a-dash-dial-track", d: trackD }), arcEl);

  return {
    valEl,
    metaEl,
    wrap: node("div", { className: "a-dash-dial-wrap" }, svgEl, node("div", { className: "a-dash-dial-value" }, valEl)),
    setPercent(pct, detail) {
      valEl.textContent = pct == null ? "—" : `${pct.toFixed(1)}%`;
      metaEl.textContent = detail;
      const ratio = pct == null ? 0 : Math.max(0, Math.min(100, pct)) / 100;
      const filled = ratio > 0 ? Math.max(2, ratio * arcLen) : 0.001;
      arcEl.setAttribute("stroke-dasharray", `${filled.toFixed(2)} ${arcLen.toFixed(2)}`);
      arcEl.setAttribute("opacity", pct == null ? "0" : "1");
      const toneClass = pct == null ? "" : pct >= 90 ? " is-bad" : pct >= 75 ? " is-warn" : "";
      arcEl.setAttribute("class", `a-dash-dial-arc${toneClass}`);
    },
    buildCell(bottomEl) {
      return node("div", { className: "a-dash-dial-cell", "data-refresh-key": key },
        node("span", { className: "a-dash-dial-label a-muted" }, label),
        this.wrap,
        metaEl,
        bottomEl,
      );
    },
  };
}

function drawMiniSpark({ width, height }, data) {
  const values = (data?.values || []).filter((v) => v != null && Number.isFinite(v));
  const padX = 2;
  const padY = 3;
  const usableW = Math.max(10, width - padX * 2);
  const usableH = Math.max(4, height - padY * 2);
  const svg = createSVGEl("svg", {
    class: "a-dash-spark",
    viewBox: `0 0 ${width} ${height}`,
    "aria-hidden": "true",
  });
  const baseY = (padY + usableH).toFixed(1);
  svg.append(createSVGEl("line", { x1: padX, y1: baseY, x2: width - padX, y2: baseY, class: "a-dash-spark-baseline" }));

  if (values.length < 2) {
    return svg;
  }

  const gradID = data?.gradID || "a-spark-grad";
  const defs = createSVGEl("defs");
  const grad = createSVGEl("linearGradient", { id: gradID, x1: "0", y1: "0", x2: "0", y2: "1" });
  grad.append(
    createSVGEl("stop", { offset: "0%", "stop-color": "#18181b", "stop-opacity": "0.14" }),
    createSVGEl("stop", { offset: "100%", "stop-color": "#18181b", "stop-opacity": "0.01" }),
  );
  defs.append(grad);
  svg.append(defs);

  const pts = values.map((val, idx) => {
    const x = padX + (idx / (values.length - 1)) * usableW;
    const y = padY + usableH - (Math.max(0, Math.min(100, val)) / 100) * usableH;
    return [x, y];
  });
  const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
  const last = pts.at(-1);
  svg.append(
    createSVGEl("path", { d: `${line} L${last[0].toFixed(1)} ${height} L${pts[0][0].toFixed(1)} ${height} Z`, fill: `url(#${gradID})` }),
    createSVGEl("path", { d: line, class: "a-dash-spark-line" }),
    createSVGEl("circle", { cx: last[0].toFixed(1), cy: last[1].toFixed(1), r: "2.2", class: "a-dash-spark-dot" }),
  );
  return svg;
}

function drawDiskSegments({ width, height }, data) {
  const pct = clampPercent(data?.pct);
  const segCount = Math.max(12, Math.min(20, Math.floor(width / 10)));
  const gap = 2;
  const padY = 6;
  const segH = Math.max(6, height - padY * 2);
  const segW = Math.max(2, (width - gap * (segCount - 1)) / segCount);
  const active = pct == null ? 0 : Math.round((pct / 100) * segCount);
  const tone = pct == null ? "" : pct >= 90 ? " is-bad" : pct >= 75 ? " is-warn" : "";

  const svg = createSVGEl("svg", {
    class: "a-dash-disk-svg",
    viewBox: `0 0 ${width} ${height}`,
    "aria-hidden": "true",
  });
  for (let i = 0; i < segCount; i++) {
    const x = (i * (segW + gap)).toFixed(1);
    const filled = pct != null && (i < active || (pct > 0 && i === 0));
    svg.append(createSVGEl("rect", {
      x,
      y: padY,
      width: segW.toFixed(1),
      height: segH,
      rx: "1.5",
      class: filled ? `a-dash-disk-seg is-on${tone}` : "a-dash-disk-seg",
    }));
  }
  return svg;
}

function drawDualOscilloscope({ width, height }, data) {
  const rxSeries = data?.rx || [];
  const txSeries = data?.tx || [];
  const bytes = data?.bytes || ((v) => String(v));
  const padL = 4;
  const padR = 4;
  const padT = 8;
  const padB = 8;
  const usableW = Math.max(20, width - padL - padR);
  const usableH = Math.max(20, height - padT - padB);
  const rawPeak = Math.max(64, ...(rxSeries.filter(Number.isFinite)), ...(txSeries.filter(Number.isFinite)));
  const peak = rawPeak * 1.15;

  const svg = createSVGEl("svg", {
    class: "a-dash-spark is-osc",
    viewBox: `0 0 ${width} ${height}`,
    "aria-hidden": "true",
  });

  // Calibrated engineering backdrop grid.
  for (const frac of [0, 0.5, 1]) {
    const y = (padT + frac * usableH).toFixed(1);
    svg.append(createSVGEl("line", { x1: padL, y1: y, x2: width - padR, y2: y, class: frac === 1 ? "a-dash-osc-axis" : "a-dash-spark-baseline" }));
  }
  for (let i = 1; i < 6; i++) {
    const x = (padL + (i / 6) * usableW).toFixed(1);
    svg.append(createSVGEl("line", { x1: x, y1: padT, x2: x, y2: height - padB, class: "a-dash-osc-vgrid" }));
  }

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
      createSVGEl("stop", { offset: "0%", "stop-color": "#18181b", "stop-opacity": "0.14" }),
      createSVGEl("stop", { offset: "100%", "stop-color": "#18181b", "stop-opacity": "0.01" }),
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

  if (rxPts.length >= 2 && rxPts.length <= 16) {
    rxPts.slice(0, -1).forEach(([x, y]) => {
      svg.append(createSVGEl("circle", { cx: x.toFixed(1), cy: y.toFixed(1), r: "1.8", class: "a-dash-osc-node-rx" }));
    });
  }
  if (rxPts.length) {
    const [rxX, rxY] = rxPts.at(-1);
    svg.append(createSVGEl("circle", { cx: rxX.toFixed(1), cy: rxY.toFixed(1), r: "3", class: "a-dash-osc-dot-rx" }));
  }
  if (txPts.length) {
    const [txX, txY] = txPts.at(-1);
    svg.append(createSVGEl("circle", { cx: txX.toFixed(1), cy: txY.toFixed(1), r: "2.8", class: "a-dash-osc-dot-tx" }));
  }
  return svg;
}

function readSampleSeries(history, bytes) {
  const cpuSeries = [];
  const memSeries = [];
  const rxSeries = [];
  const txSeries = [];
  for (const sample of history || []) {
    cpuSeries.push(sample?.cpu_available ? clampPercent(sample.cpu_percent) : null);
    const memTotal = finiteNonNegative(sample?.memory_total_bytes);
    const memUsed = finiteNonNegative(sample?.memory_used_bytes);
    const memVal = clampPercent(sample?.memory_percent) ?? (memTotal > 0 && memUsed != null ? clampPercent((memUsed / memTotal) * 100) : null);
    memSeries.push(sample?.memory_available ? memVal : null);
    const netRateOK = sample?.network_rate_valid !== undefined
      ? Boolean(sample.network_rate_valid)
      : Boolean(sample?.network_available) && (sample?.network_rate_available === undefined || Boolean(sample.network_rate_available));
    rxSeries.push(netRateOK ? finiteNonNegative(sample?.network_rx_bps) : null);
    txSeries.push(netRateOK ? finiteNonNegative(sample?.network_tx_bps) : null);
  }
  return { cpuSeries, memSeries, rxSeries, txSeries, bytes };
}

export function createPanelHostController(ctx, { bytes, date }) {
  const headBadges = node("div", { className: "a-actions", role: "status" });
  const subTime = node("span", { className: "a-dash-subbar-text a-muted" }, "读取中…");
  const subbar = node("div", { className: "a-dash-subbar" }, subTime);
  let lastRenderSignature = "";

  const keyedBadge = (key, label, tone = "") => {
    const el = badge(label, tone);
    el.setAttribute("data-refresh-key", key);
    return el;
  };

  const cpuSpark = createResponsiveSVG("a-dash-dial-spark", ctx.signal, { height: 22, fallbackWidth: 120, draw: drawMiniSpark });
  const memSpark = createResponsiveSVG("a-dash-dial-spark", ctx.signal, { height: 22, fallbackWidth: 120, draw: drawMiniSpark });
  const diskMeter = createResponsiveSVG("a-dash-dial-spark", ctx.signal, { height: 22, fallbackWidth: 120, draw: drawDiskSegments });
  const netOsc = createResponsiveSVG("a-dash-osc-wrap", ctx.signal, { height: 112, fallbackWidth: 420, draw: drawDualOscilloscope });

  const cpuDial = createDialSlot("CPU 使用率", "CPU 使用率");
  const memDial = createDialSlot("内存使用率", "内存使用率");
  const diskDial = createDialSlot("根目录磁盘", "根目录磁盘");

  const rxValEl = node("strong", {}, "—");
  const rxCumEl = node("span", { className: "a-muted" }, "累计 —");
  const txValEl = node("strong", {}, "—");
  const txCumEl = node("span", { className: "a-muted" }, "累计 —");
  const oscSummaryEl = node("span", { className: "a-muted" }, "—");

  const body = node("div", { className: "a-dash-host-wrap" },
    subbar,
    node("div", { className: "a-dash-dial-grid" },
      cpuDial.buildCell(cpuSpark.element),
      memDial.buildCell(memSpark.element),
      diskDial.buildCell(diskMeter.element),
    ),
    node("div", { className: "a-dash-host-io" },
      node("div", { className: "a-dash-io-readouts" },
        node("div", { className: "a-dash-io-item", "data-refresh-key": "网络接收速率" },
          node("div", { className: "a-dash-io-head" }, node("span", { className: "a-dash-rx" }, "↓ 接收速率"), rxCumEl),
          rxValEl,
        ),
        node("div", { className: "a-dash-io-item", "data-refresh-key": "网络发送速率" },
          node("div", { className: "a-dash-io-head" }, node("span", { className: "a-dash-tx" }, "↑ 发送速率"), txCumEl),
          txValEl,
        ),
      ),
      node("div", { className: "a-dash-host-net-spark" },
        node("div", { className: "a-dash-io-head" },
          node("span", { className: "a-dash-osc-legend a-muted" },
            node("span", { className: "a-chart-key-rx" }),
            " 接收",
            node("span", { className: "a-chart-key-tx" }),
            " 发送",
          ),
          oscSummaryEl,
        ),
        netOsc.element,
      ),
    ),
  );

  const element = section("面板运行资源", body, headBadges);
  element.classList.add("a-dash-host-card");

  function paint({ m = {}, history = [], runtime = null, status = "" }) {
    const cpuPct = m.cpu_available ? clampPercent(m.cpu_percent) : null;
    const memTotal = finiteNonNegative(m.memory_total_bytes);
    const memUsed = finiteNonNegative(m.memory_used_bytes);
    const memOK = Boolean(m.memory_available) && memTotal > 0 && memUsed != null;
    const memPct = memOK ? clampPercent((memUsed / memTotal) * 100) : null;
    const diskTotal = finiteNonNegative(m.disk_total_bytes);
    const diskUsed = finiteNonNegative(m.disk_used_bytes);
    const diskOK = Boolean(m.disk_available) && diskTotal > 0 && diskUsed != null;
    const diskPct = diskOK ? clampPercent((diskUsed / diskTotal) * 100) : null;

    const netRateOK = Boolean(m.network_available) && (m.network_rate_available === undefined || Boolean(m.network_rate_available));
    const rxBps = netRateOK ? finiteNonNegative(m.network_rx_bps) : null;
    const txBps = netRateOK ? finiteNonNegative(m.network_tx_bps) : null;
    const rxTotal = m.network_available ? finiteNonNegative(m.network_rx_bytes) : null;
    const txTotal = m.network_available ? finiteNonNegative(m.network_tx_bytes) : null;

    const series = readSampleSeries(history, bytes);
    const time = Date.parse(m.collected_at);
    const collected = Number.isFinite(time) && time > 0;
    const stale = collected && Date.now() - time > 15000;
    let subTimeText = "";
    if (status === "denied") {
      subTimeText = "面板资源无读取权限";
    } else if (status === "failed" && !collected) {
      subTimeText = "资源读取失败";
    } else if (!collected) {
      subTimeText = "尚无有效采样";
    } else {
      const formattedTime = date(m.collected_at);
      subTimeText = status === "failed" ? `${formattedTime} · 保留上次采样` : formattedTime;
    }

    const renderSignature = JSON.stringify([
      status, collected, stale, subTimeText,
      cpuPct, memPct, memUsed, memTotal, diskPct, diskUsed, diskTotal,
      rxBps, txBps, rxTotal, txTotal,
      series.cpuSeries, series.memSeries, series.rxSeries, series.txSeries,
      Boolean(m.cpu_available), memOK, diskOK, Boolean(m.network_available), netRateOK,
    ]);
    if (renderSignature === lastRenderSignature) return;
    lastRenderSignature = renderSignature;

    cpuDial.setPercent(cpuPct, "\u00a0");
    memDial.setPercent(memPct, memOK ? `${bytes(memUsed)} / ${bytes(memTotal)}` : "\u00a0");
    diskDial.setPercent(diskPct, diskOK ? `${bytes(diskUsed)} / ${bytes(diskTotal)}` : "\u00a0");

    cpuSpark.update({ values: series.cpuSeries, gradID: "a-spark-grad-cpu" });
    memSpark.update({ values: series.memSeries, gradID: "a-spark-grad-mem" });
    diskMeter.update({ pct: diskPct });

    rxValEl.textContent = rxBps == null ? "—" : `${bytes(rxBps)}/s`;
    txValEl.textContent = txBps == null ? "—" : `${bytes(txBps)}/s`;
    rxCumEl.textContent = rxTotal == null ? "累计 —" : `累计 ${bytes(rxTotal)}`;
    txCumEl.textContent = txTotal == null ? "累计 —" : `累计 ${bytes(txTotal)}`;

    const finiteRates = [...series.rxSeries, ...series.txSeries].filter((v) => v != null && Number.isFinite(v));
    oscSummaryEl.textContent = finiteRates.length >= 2 ? `峰值 ${bytes(Math.max(...finiteRates))}/s` : "—";
    netOsc.update({ rx: series.rxSeries, tx: series.txSeries, bytes, gradID: "a-osc-grad-panel-net" });

    patch(headBadges, node("div", { className: "a-actions", role: "status" },
      collected ? keyedBadge("scope", "容器可见") : null,
      status === "denied"
        ? keyedBadge("state", "无读取权限", "bad")
        : status === "failed"
          ? keyedBadge("state", collected ? "刷新失败" : "资源读取失败", "warn")
          : collected
            ? keyedBadge("state", stale ? "采样已过期" : "实时", stale ? "warn" : "ok")
            : keyedBadge("state", "尚无有效采样", "warn"),
    ));

    if (subTime.textContent !== subTimeText) subTime.textContent = subTimeText;
  }

  return { element, paint };
}
