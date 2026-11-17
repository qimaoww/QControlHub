import { createSVGEl } from "./dashboard-panel.js";
import { isProbeRunning as isModelProbeRunning, probeHistorySeries } from "../modules/dashboard-model.js";
import { geoRegionDetails } from "../modules/region-model.js";
import { PACIFIC_MAP_BOUNDS, projectPacificGeo } from "./pacific-map-data.js";

export const INK_COLOR = "#18181b";
export const SOFT_INK_COLOR = "#62626b";
export const BORDER_COLOR = "#e4e4e7";
export const BAD_COLOR = "#a41a2b";
export const IDLE_FLOW_COLOR = "#a1a1aa";
export const OK_COLOR = "#155731";
export const WARN_COLOR = "#b45309";
export const RUNNING_COLOR = "#0284c7";
export const CO_LOCATED_RADIUS_PX = 5.5;

export function tonePalette(tone) {
  if (tone === "ok") {
    return {
      stroke: "#155731",
      fill: "#eef9f0",
      border: "#c9e8d2",
      ink: "#155731",
      pulse: "#22c55e",
      pulseCore: "#dcfce7",
      activeDot: "#4ade80",
    };
  }
  if (tone === "warn") {
    return {
      stroke: "#b45309",
      fill: "#fff8e9",
      border: "#efddbb",
      ink: "#754511",
      pulse: "#f59e0b",
      pulseCore: "#fef3c7",
      activeDot: "#fbbf24",
    };
  }
  if (tone === "bad") {
    return {
      stroke: "#a41a2b",
      fill: "#fff2f3",
      border: "#f2cdd2",
      ink: "#a41a2b",
      pulse: "#ef4444",
      pulseCore: "#fee2e2",
      activeDot: "#f87171",
    };
  }
  if (tone === "running") {
    return {
      stroke: "#0284c7",
      fill: "#f0f9ff",
      border: "#bae6fd",
      ink: "#0369a1",
      pulse: "#38bdf8",
      pulseCore: "#e0f2fe",
      activeDot: "#38bdf8",
    };
  }
  return {
    stroke: "#71717a",
    fill: "#f4f4f5",
    border: "#e4e4e7",
    ink: "#52525b",
    pulse: "#a1a1aa",
    pulseCore: "#f4f4f5",
    activeDot: "#a1a1aa",
  };
}

export function isProbeRunning(probe) {
  if (!probe) return false;
  if (probe.running) return true;
  return isModelProbeRunning(probe);
}

export function resolveChainTone(probe) {
  if (!probe) return "";
  if (isProbeRunning(probe)) return "running";
  const rawTone = probe.tone ?? probe.overall?.statusBadge?.tone ?? probe.overall?.tone ?? probe.status?.tone ?? "";
  if (rawTone === "ok" || rawTone === "warn" || rawTone === "bad" || rawTone === "running") {
    return rawTone;
  }
  return "";
}

export function resolveArrowMarkerID(prefix = "a-topo", tone = "", small = false) {
  const norm = tone === "ok" || tone === "warn" || tone === "bad" || tone === "running" ? tone : "idle";
  return `${prefix}-arrow-${small ? "sm-" : ""}${norm}`;
}

export function visualUnits(text) {
  let units = 0;
  for (const ch of String(text || "")) {
    units += ch.charCodeAt(0) > 255 ? 1.85 : 1;
  }
  return units;
}

export function clipLabel(text, maxUnits) {
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

/** Discrete CSS animation speed class for live probe pulses (strict CSP, zero inline styles). */
export function flowSpeedClass(latencyMS) {
  const ms = Number(latencyMS);
  if (!Number.isFinite(ms) || ms <= 0) return "is-speed-med";
  if (ms <= 60) return "is-speed-fast";
  if (ms <= 160) return "is-speed-med";
  return "is-speed-slow";
}

export function formatMs(ms, digits = null) {
  if (typeof ms !== "number" || !Number.isFinite(ms)) return "—";
  const d = digits != null ? digits : (ms >= 100 ? 0 : 1);
  return `${ms.toFixed(d)} ms`;
}

export function formatMsCompact(ms) {
  if (typeof ms !== "number" || !Number.isFinite(ms)) return "—";
  return `${ms.toFixed(ms >= 100 ? 0 : 1)}ms`;
}

export function formatLoss(loss) {
  if (typeof loss !== "number" || !Number.isFinite(loss)) return "—";
  return `${loss.toFixed(loss % 1 === 0 ? 0 : 1)}%`;
}

export function formatDirBadgeMetric(dir, { prefix = "", running = false, ready = true } = {}) {
  if (running && (!dir || !dir.available)) return `${prefix}探测中`;
  if (!ready && (!dir || !dir.available)) return `${prefix}暂不可用`;
  if (!dir || !dir.available) return `${prefix}待采样`;
  if (dir.state === "running" || dir.state === "preparing") return `${prefix}探测中`;
  if (dir.state === "failed" || dir.error) return `${prefix}失败`;
  if (!dir.reached || (dir.lossPercent ?? 0) >= 100) return `${prefix}不可达`;
  const rtt = formatMsCompact(dir.avgMS);
  const loss = dir.lossPercent > 0 ? ` · ${formatLoss(dir.lossPercent)}` : "";
  return `${prefix}${rtt}${loss}`;
}

export function summarizeGroupMetricLine(group, metricDir = "both") {
  if (!group) return "—";
  const { overall, bidirectional, running, availability } = group;
  if (running && !overall?.forward?.available) return "探测中…";
  if (!availability?.ready && !overall?.forward?.available) return availability?.reason || "暂不可用";
  if (metricDir === "reverse") {
    if (!bidirectional) return "单向链路";
    return formatDirBadgeMetric(overall.reverse, { prefix: "回 ", running, ready: availability?.ready });
  }
  if (metricDir === "forward" || !bidirectional) {
    return formatDirBadgeMetric(overall.forward, { prefix: bidirectional ? "去 " : "", running, ready: availability?.ready });
  }
  const f = formatDirBadgeMetric(overall.forward, { prefix: "去 ", running, ready: availability?.ready });
  const r = formatDirBadgeMetric(overall.reverse, { prefix: "回 ", running, ready: availability?.ready });
  return `${f} / ${r}`;
}

export function isProbeUnreachable(probe) {
  if (!probe) return true;
  if (probe.unavailableReverse) return false;
  if (probe.tone === "bad" && probe.latencyMS == null && (!probe.bidirectional || probe.revLatencyMS == null)) return true;
  if (probe.lossPercent != null && probe.lossPercent >= 100 && probe.latencyMS == null && probe.revLatencyMS == null) return true;
  return false;
}

export function buildProbePillLabel(probe, compact = false, dense = false, metricDir = null) {
  if (!probe) return "—";
  const dirMode = metricDir || probe.metricDir || "both";
  const prefix = "";
  if (isProbeRunning(probe)) return `${prefix}探测中…`;
  if (probe.unavailableReverse || (dirMode === "reverse" && !probe.bidirectional)) return `${prefix}单向`;
  if (isProbeUnreachable(probe)) {
    if (compact) return `${prefix}不可达`;
    return probe.lossPercent != null && probe.lossPercent >= 100
      ? `${prefix}不可达 · 100%`
      : `${prefix}${probe.metricLabel || "不可达"}`;
  }
  const fmtPct = (v) => Number(v).toFixed(Number(v) % 1 ? 1 : 0);
  const slash = compact || dense ? "/" : " / ";
  const hasPrimary = probe.latencyMS != null && Number.isFinite(Number(probe.latencyMS))
    && !(probe.fwdLossPercent != null && Number(probe.fwdLossPercent) >= 100 && Number(probe.latencyMS) <= 0);
  const hasRev = dirMode === "both" && Boolean(probe.bidirectional) && probe.revLatencyMS != null && Number.isFinite(Number(probe.revLatencyMS))
    && !(probe.revLossPercent != null && Number(probe.revLossPercent) >= 100 && Number(probe.revLatencyMS) <= 0);

  if (dirMode === "both" && probe.bidirectional && (hasPrimary || hasRev)) {
    const fLoss = probe.fwdLossPercent != null && Number.isFinite(Number(probe.fwdLossPercent)) ? Number(probe.fwdLossPercent) : null;
    const rLoss = probe.revLossPercent != null && Number.isFinite(Number(probe.revLossPercent)) ? Number(probe.revLossPercent) : null;
    const fUnreach = !hasPrimary && fLoss != null;
    const rUnreach = !hasRev && rLoss != null;
    const isAsymLoss = (fLoss != null && rLoss != null && Math.round(fLoss * 10) !== Math.round(rLoss * 10) && (fLoss > 0 || rLoss > 0))
      || (hasPrimary !== hasRev);
    if (isAsymLoss && !dense) {
      const dirSpace = compact ? "" : " ";
      const dirSep = " / ";
      const fPart = hasPrimary
        ? `去${dirSpace}${formatMsCompact(Number(probe.latencyMS))}${fLoss != null && fLoss > 0 ? `·${fmtPct(fLoss)}%` : ""}`
        : `去${dirSpace}${fUnreach ? "×" : "—"}`;
      const rPart = hasRev
        ? `回${dirSpace}${formatMsCompact(Number(probe.revLatencyMS))}${rLoss != null && rLoss > 0 ? `·${fmtPct(rLoss)}%` : ""}`
        : `回${dirSpace}${rUnreach ? "×" : "—"}`;
      return `${prefix}${fPart}${dirSep}${rPart}`;
    }
  }

  let rtt = "";
  let dirLoss = probe.lossPercent;
  let dirJitterHigh = Boolean(probe.jitterHigh);
  let dirLatencyHigh = Boolean(probe.latencyHigh);

  if (dirMode === "reverse") {
    const revVal = probe.metricDir !== "reverse" && probe.revLatencyMS != null ? probe.revLatencyMS : probe.latencyMS;
    const revLossVal = probe.metricDir !== "reverse" && probe.revLossPercent != null ? probe.revLossPercent : probe.lossPercent;
    const revOk = revVal != null && Number.isFinite(Number(revVal))
      && !(revLossVal != null && Number(revLossVal) >= 100 && Number(revVal) <= 0);
    if (!revOk) return `${prefix}${probe.metricLabel || "待采样"}`;
    rtt = compact ? `回${formatMsCompact(Number(revVal))}` : `回 ${formatMsCompact(Number(revVal))}`;
    if (probe.metricDir !== "reverse" && probe.revLossPercent != null) dirLoss = probe.revLossPercent;
    if (probe.metricDir !== "reverse" && probe.revJitterHigh !== undefined) dirJitterHigh = Boolean(probe.revJitterHigh);
  } else if (dirMode === "forward") {
    if (!hasPrimary) return `${prefix}${probe.metricLabel || "待采样"}`;
    rtt = compact
      ? (probe.bidirectional ? `去${formatMsCompact(Number(probe.latencyMS))}` : formatMsCompact(Number(probe.latencyMS)))
      : (!probe.bidirectional ? formatMsCompact(Number(probe.latencyMS)) : `去 ${formatMsCompact(Number(probe.latencyMS))}`);
    if (probe.metricDir !== "forward" && probe.fwdLossPercent != null) dirLoss = probe.fwdLossPercent;
    if (probe.metricDir !== "forward" && probe.fwdJitterHigh !== undefined) dirJitterHigh = Boolean(probe.fwdJitterHigh);
  } else if (probe.bidirectional && (hasPrimary || hasRev)) {
    const fKnown = hasPrimary || probe.fwdLossPercent != null;
    const rKnown = hasRev || probe.revLossPercent != null;
    const f = hasPrimary ? formatMsCompact(Number(probe.latencyMS)) : (fKnown ? "×" : "—");
    const r = hasRev ? formatMsCompact(Number(probe.revLatencyMS)) : (rKnown ? "×" : "");
    rtt = dense || !r ? f : `${f}${slash}${r}`;
  } else if (hasPrimary) {
    rtt = formatMsCompact(Number(probe.latencyMS));
  } else {
    return `${prefix}${probe.metricLabel || "待采样"}`;
  }

  if (dirLoss == null) return `${prefix}${rtt}`;
  const lossVal = Number(dirLoss);
  const lossNum = fmtPct(lossVal);
  if (lossVal > 0) {
    return compact ? `${prefix}${rtt} · ${lossNum}%` : `${prefix}${rtt} · 丢包${lossNum}%`;
  }
  const base = compact ? `${prefix}${rtt}` : `${prefix}${rtt} · 0%`;
  if (dirJitterHigh) return `${base}${compact ? " · 抖" : " · 高抖动"}`;
  if (dirLatencyHigh) return `${base}${compact ? " · 慢" : " · 高延迟"}`;
  return base;
}

export function buildProbeShortAlertLabel(probe) {
  if (!probe) return "异常";
  const prefix = "";
  if (isProbeRunning(probe)) return `${prefix}探测中…`;
  if (isProbeUnreachable(probe)) return `${prefix}不可达`;
  if (probe.lossPercent != null && probe.lossPercent > 0) {
    const lossNum = probe.lossPercent.toFixed(probe.lossPercent % 1 ? 1 : 0);
    return `${prefix}丢包${lossNum}%`;
  }
  if (probe.jitterHigh) return `${prefix}高抖动`;
  if (probe.latencyHigh) return `${prefix}高延迟`;
  return `${prefix}${probe.metricLabel || "异常"}`;
}

export function selectTopAmbientBadProbeIDs(probeLinks, { includeExternal = true, maxCount = 6 } = {}) {
  const candidates = (probeLinks || []).filter((p) => (p.tone === "bad" || p.tone === "warn") && (includeExternal || !p.isExternal));
  candidates.sort((a, b) => {
    const toneWeight = { bad: 2, warn: 1 };
    const tDiff = (toneWeight[b.tone] || 0) - (toneWeight[a.tone] || 0);
    if (tDiff !== 0) return tDiff;
    const aUnreach = isProbeUnreachable(a) ? 1 : 0;
    const bUnreach = isProbeUnreachable(b) ? 1 : 0;
    if (bUnreach !== aUnreach) return bUnreach - aUnreach;
    const lossDiff = (Number(b.lossPercent) || 0) - (Number(a.lossPercent) || 0);
    if (lossDiff !== 0) return lossDiff;
    const latDiff = (Number(b.latencyMS) || 0) - (Number(a.latencyMS) || 0);
    if (latDiff !== 0) return latDiff;
    return String(a.id || "").localeCompare(String(b.id || ""));
  });
  return new Set(candidates.slice(0, Math.max(0, maxCount)).map((p) => p.id));
}

export function buildBidirectionalPillLines(hop, metricDir = "both") {
  const prefix = "";
  const fmtPct = (v) => Number(v).toFixed(Number(v) % 1 ? 1 : 0);

  if (hop?.running) {
    const line1 = `${prefix}探测中…`;
    const line2 = hop?.bidirectional && metricDir === "both" ? "去/回检测中" : "链路检测中";
    return { line1, line2, fullLabel: `${line1} · ${line2}` };
  }

  if (hop?.unavailableReverse || (metricDir === "reverse" && !hop?.bidirectional)) {
    const line1 = `${prefix}单向链路`;
    const line2 = "无回程测量";
    return { line1, line2, fullLabel: `${line1} · ${line2}` };
  }

  const showFwd = metricDir !== "reverse";
  const showRev = Boolean(hop?.bidirectional) && metricDir !== "forward";

  const hasFwd = showFwd && hop?.latencyMS != null && Number.isFinite(Number(hop.latencyMS))
    && !(hop?.fwdLossPercent != null && Number(hop.fwdLossPercent) >= 100 && Number(hop.latencyMS) === 0);
  const hasRev = showRev && hop?.revLatencyMS != null && Number.isFinite(Number(hop.revLatencyMS))
    && !(hop?.revLossPercent != null && Number(hop.revLossPercent) >= 100 && Number(hop.revLatencyMS) === 0);
  const singleReverseVal = metricDir === "reverse" && hop?.latencyMS != null && Number.isFinite(Number(hop.latencyMS));

  const lossVal = hop?.lossPercent != null
    ? Number(hop.lossPercent)
    : (hop?.fwdLossPercent != null ? Number(hop.fwdLossPercent) : (hop?.revLossPercent != null ? Number(hop.revLossPercent) : null));

  if (!hasFwd && !hasRev && !singleReverseVal) {
    const rawLabel = String(hop?.statusLabel || "");
    if (rawLabel && rawLabel !== "不可达" && rawLabel !== "异常" && lossVal == null) {
      const line1 = `${prefix}${rawLabel}`;
      const line2 = hop?.unavailableReason || (showRev && showFwd ? "去/回待测" : "待采样");
      return { line1, line2, fullLabel: `${line1} · ${line2}` };
    }
    const line1 = `${prefix}${showFwd && showRev ? "去/回 不可达" : "不可达"}`;
    const line2 = lossVal != null ? `丢包 ${fmtPct(lossVal)}%` : (hop?.unavailableReason || "100% 丢包");
    return { line1, line2, fullLabel: `${line1} · ${line2}` };
  }

  let line1 = "";
  if (metricDir === "reverse") {
    line1 = `${prefix}回 ${formatMsCompact(hop.latencyMS)}`;
  } else if (showFwd && showRev) {
    const fwdText = hasFwd ? `去 ${formatMsCompact(hop.latencyMS)}` : "去 ×";
    const revText = hasRev ? `回 ${formatMsCompact(hop.revLatencyMS)}` : "回 ×";
    line1 = `${prefix}${fwdText} · ${revText}`;
  } else {
    line1 = `${prefix}去 ${formatMsCompact(hop.latencyMS)}`;
  }

  let line2 = "";
  if (
    showFwd && showRev
    && hop?.fwdLossPercent != null
    && hop?.revLossPercent != null
    && (Number(hop.fwdLossPercent) > 0 || Number(hop.revLossPercent) > 0)
    && Math.round(Number(hop.fwdLossPercent) * 10) !== Math.round(Number(hop.revLossPercent) * 10)
  ) {
    line2 = `去${fmtPct(hop.fwdLossPercent)}%/回${fmtPct(hop.revLossPercent)}%`;
  } else if (lossVal != null && lossVal > 0) {
    line2 = `丢包 ${fmtPct(lossVal)}%`;
  } else if (lossVal === 0) {
    line2 = "0% 丢包";
  } else {
    line2 = hop?.isExternal ? "公网出口" : (hop?.bidirectional ? "双程探测" : "单向探测");
  }

  return { line1, line2, fullLabel: `${line1} · ${line2}` };
}

const sanitizeSVGKey = (val) => String(val || "default").replace(/[^a-zA-Z0-9_-]/g, "_");

/**
 * Renders latency & packet-loss history SVG sparkline for a monitoring group.
 * Keeps linearGradient IDs deterministic via `gradKey` so keyed patch() updates
 * never flicker or regenerate SVG gradients across background polls.
 */
export function renderProbeHistoryChart(
  history = [],
  {
    bidirectional = false,
    metricDir = "both",
    width = 520,
    height = 76,
    gradKey = "default",
  } = {},
) {
  const showFwd = metricDir !== "reverse";
  const showRev = bidirectional && metricDir !== "forward";
  const fwdSeries = showFwd ? probeHistorySeries(history, "forward") : [];
  const revSeries = showRev ? probeHistorySeries(history, "reverse") : [];
  if (fwdSeries.length < 2 && revSeries.length < 2) {
    return null;
  }

  const w = Math.max(200, Math.round(width));
  const h = Math.max(54, Math.round(height));
  const padX = 6;
  const padY = 8;
  const usableW = Math.max(24, w - padX * 2);
  const usableH = h - padY * 2;

  const finite = [
    ...fwdSeries.map((pt) => pt.avgMS),
    ...revSeries.map((pt) => pt.avgMS),
  ].filter((v) => typeof v === "number" && Number.isFinite(v) && v >= 0);

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
  const primary = fwd.pts.length ? fwd : rev;

  const svg = createSVGEl("svg", {
    class: "a-dash-spark is-probe a-topo-history-svg",
    viewBox: `0 0 ${w} ${h}`,
    "aria-hidden": "true",
  });

  svg.append(
    createSVGEl("line", { x1: padX, y1: (padY + usableH * 0.5).toFixed(1), x2: w - padX, y2: (padY + usableH * 0.5).toFixed(1), class: "a-dash-spark-baseline" }),
    createSVGEl("line", { x1: padX, y1: (padY + usableH).toFixed(1), x2: w - padX, y2: (padY + usableH).toFixed(1), class: "a-dash-osc-axis" }),
  );

  if (primary.pts.length >= 2) {
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
      d: `${primary.d} L${primary.pts.at(-1)[0].toFixed(1)} ${(h - padY).toFixed(1)} L${primary.pts[0][0].toFixed(1)} ${(h - padY).toFixed(1)} Z`,
      fill: `url(#${gradID})`,
    }));
  }
  if (rev.d) svg.append(createSVGEl("path", { d: rev.d, class: "a-dash-spark-secondary" }));
  if (fwd.d) svg.append(createSVGEl("path", { d: fwd.d, class: "a-dash-spark-line" }));
  if (primary.pts.length) {
    const [lx, ly] = primary.pts.at(-1);
    svg.append(createSVGEl("circle", { cx: lx.toFixed(1), cy: ly.toFixed(1), r: "2.5", class: "a-dash-spark-dot" }));
  }

  const lossPoints = fwdSeries.length ? fwdSeries : revSeries;
  lossPoints.forEach((pt, idx) => {
    if (!pt) return;
    if (!pt.reached || pt.lossPercent > 0) {
      const x = xFor(idx).toFixed(1);
      const y = pt.avgMS != null ? yFor(pt.avgMS).toFixed(1) : (padY + usableH - 2).toFixed(1);
      svg.append(createSVGEl("circle", { cx: x, cy: y, r: "3.1", class: "a-dash-probe-loss-dot" }));
    }
  });

  return {
    svg,
    sampleCount: Math.max(fwdSeries.length, revSeries.length),
    peakMS: finite.length ? Math.max(...finite) : null,
  };
}

export function bridgeArchPath(x0, yA, yB, bow, railOffset = 0) {
  const sx = x0 + Math.max(0, railOffset);
  const apexX = sx + bow;
  const yMid = (yA + yB) / 2;
  const dir = yB >= yA ? 1 : -1;
  const c1 = Math.max(20, bow * 0.64);
  const c2 = Math.max(12, Math.abs(yB - yA) * 0.24);
  const lead = railOffset > 0 ? [`M ${x0.toFixed(1)} ${yA.toFixed(1)}`, `L ${sx.toFixed(1)} ${yA.toFixed(1)}`] : [`M ${sx.toFixed(1)} ${yA.toFixed(1)}`];
  const tail = railOffset > 0 ? [`L ${x0.toFixed(1)} ${yB.toFixed(1)}`] : [`L ${sx.toFixed(1)} ${yB.toFixed(1)}`];
  const d = [
    ...lead,
    `C ${(sx + c1).toFixed(1)} ${yA.toFixed(1)}, ${apexX.toFixed(1)} ${(yMid - dir * c2).toFixed(1)}, ${apexX.toFixed(1)} ${yMid.toFixed(1)}`,
    `C ${apexX.toFixed(1)} ${(yMid + dir * c2).toFixed(1)}, ${(sx + c1).toFixed(1)} ${yB.toFixed(1)}, ${sx.toFixed(1)} ${yB.toFixed(1)}`,
    ...(railOffset > 0 ? tail : []),
  ].join(" ");
  return { d, apexX, apexY: yMid };
}

export function bridgePointAt(x0, yA, yB, bow, t = 0.5, railOffset = 0) {
  const sx = x0 + Math.max(0, railOffset);
  const apexX = sx + bow;
  const yMid = (yA + yB) / 2;
  const dir = yB >= yA ? 1 : -1;
  const c1 = Math.max(20, bow * 0.64);
  const c2 = Math.max(12, Math.abs(yB - yA) * 0.24);
  const evalSeg = (p0x, p0y, p1x, p1y, p2x, p2y, p3x, p3y, u) => {
    const mu = 1 - u;
    return {
      x: mu * mu * mu * p0x + 3 * mu * mu * u * p1x + 3 * mu * u * u * p2x + u * u * u * p3x,
      y: mu * mu * mu * p0y + 3 * mu * mu * u * p1y + 3 * mu * u * u * p2y + u * u * u * p3y,
    };
  };
  if (t <= 0.5) {
    return evalSeg(sx, yA, sx + c1, yA, apexX, yMid - dir * c2, apexX, yMid, Math.max(0, t * 2));
  }
  return evalSeg(apexX, yMid, apexX, yMid + dir * c2, sx + c1, yB, sx, yB, Math.min(1, (t - 0.5) * 2));
}

export function quadPointAt(x1, y1, cx, cy, x2, y2, t) {
  const mt = 1 - t;
  return {
    x: mt * mt * x1 + 2 * mt * t * cx + t * t * x2,
    y: mt * mt * y1 + 2 * mt * t * cy + t * t * y2,
  };
}

export function rectsOverlap(a, b, padX = 4, padY = padX) {
  return a.x - padX < b.x + b.w + padX
    && a.x + a.w + padX > b.x - padX
    && a.y - padY < b.y + b.h + padY
    && a.y + a.h + padY > b.y - padY;
}

function pointsClose(p, q, eps = 0.5) {
  return Math.abs(p.x - q.x) <= eps && Math.abs(p.y - q.y) <= eps;
}

function cross2d(o, a, b) {
  return (a.x - o.x) * (b.y - o.y) - (a.y - o.y) * (b.x - o.x);
}

function onSegment2d(p, q, r) {
  return q.x <= Math.max(p.x, r.x) + 1e-6 && q.x >= Math.min(p.x, r.x) - 1e-6
    && q.y <= Math.max(p.y, r.y) + 1e-6 && q.y >= Math.min(p.y, r.y) - 1e-6;
}

export function segmentsIntersect(a1, a2, b1, b2) {
  if (!a1 || !a2 || !b1 || !b2) return false;
  if (pointsClose(a1, b1) || pointsClose(a1, b2) || pointsClose(a2, b1) || pointsClose(a2, b2)) {
    return false;
  }
  const d1 = cross2d(b1, b2, a1);
  const d2 = cross2d(b1, b2, a2);
  const d3 = cross2d(a1, a2, b1);
  const d4 = cross2d(a1, a2, b2);

  if (((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) && ((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0))) {
    return true;
  }
  if (Math.abs(d1) < 1e-6 && onSegment2d(b1, a1, b2)) return true;
  if (Math.abs(d2) < 1e-6 && onSegment2d(b1, a2, b2)) return true;
  if (Math.abs(d3) < 1e-6 && onSegment2d(a1, b1, a2)) return true;
  if (Math.abs(d4) < 1e-6 && onSegment2d(a1, b2, a2)) return true;
  return false;
}

function nearestAnchorFor(cx, cy, anchorList) {
  let best = anchorList[0];
  let bestD = Infinity;
  for (const a of anchorList) {
    const d = Math.hypot(cx - a.x, cy - a.y);
    if (d < bestD) {
      bestD = d;
      best = a;
    }
  }
  return { anchor: best, dist: bestD };
}

export function placeCollisionFreePill(targetX, targetY, w, h, occupied, bounds, anchors = null, opts = {}) {
  const padX = opts.padX ?? 3;
  const padY = opts.padY ?? 2.5;
  const reserveW = Math.max(w, opts.reserveW ?? w);
  const reserveH = Math.max(h, opts.reserveH ?? h);
  const minCX = bounds.minX + reserveW / 2;
  const maxCX = Math.max(minCX, bounds.maxX - reserveW / 2);
  const minCY = bounds.minY + reserveH / 2;
  const maxCY = Math.max(minCY, bounds.maxY - reserveH / 2);
  const clampPoint = (px, py) => ({
    cx: Math.max(minCX, Math.min(maxCX, px)),
    cy: Math.max(minCY, Math.min(maxCY, py)),
  });
  const anchorList = Array.isArray(anchors) && anchors.length ? anchors : [{ x: targetX, y: targetY }];
  const leaderPoints = Array.isArray(opts.leaderPoints) && opts.leaderPoints.length ? opts.leaderPoints : anchorList;
  const leaderSegments = Array.isArray(opts.leaderSegments) ? opts.leaderSegments : null;
  const singleLeaderOrigin = leaderSegments && leaderPoints.length === 1 ? leaderPoints[0] : null;
  const localDx = [0, 10, -10, 22, -22, 34, -34];
  const localDy = [0, -8, 8, -16, 16];

  const boxFits = (box) => !occupied.some((o) => rectsOverlap(box, o, padX, padY));
  const leaderClear = (cx, cy) => !singleLeaderOrigin
    || !leaderSegments.some((seg) => segmentsIntersect(singleLeaderOrigin, { x: cx, y: cy }, seg.a, seg.b));

  const finalize = (chosen, fallback) => {
    const picked = chosen || fallback;
    occupied.push(picked.box);
    const { anchor: nearA, dist } = nearestAnchorFor(picked.cx, picked.cy, leaderPoints);
    if (singleLeaderOrigin && Math.hypot(picked.cx - singleLeaderOrigin.x, picked.cy - singleLeaderOrigin.y) > 4) {
      leaderSegments.push({ a: singleLeaderOrigin, b: { x: picked.cx, y: picked.cy } });
    }
    return { cx: picked.cx, cy: picked.cy, anchorX: nearA.x, anchorY: nearA.y, anchorDist: dist, shifted: dist > 18 };
  };

  let fallback = null;
  let boxOnlyMatch = null;
  for (const anchor of anchorList) {
    for (const dy of localDy) {
      for (const dx of localDx) {
        const { cx, cy } = clampPoint(anchor.x + dx, anchor.y + dy);
        const box = { x: cx - reserveW / 2, y: cy - reserveH / 2, w: reserveW, h: reserveH };
        if (!fallback) fallback = { cx, cy, box };
        if (boxFits(box)) {
          if (!boxOnlyMatch) boxOnlyMatch = { cx, cy, box };
          if (leaderClear(cx, cy)) return finalize({ cx, cy, box }, fallback);
        }
      }
    }
  }

  const spanY = Math.max(240, Math.round(maxCY - minCY));
  const stepY = Math.max(10, Math.round(reserveH * 0.55));
  const stepX = Math.max(14, Math.round(reserveW * 0.22));
  let best = null;
  let bestScore = Infinity;

  for (let absDy = 0; absDy <= spanY; absDy += stepY) {
    const dySigns = absDy === 0 ? [0] : [-absDy, absDy];
    if (best && absDy * absDy >= bestScore) break;
    for (const dy of dySigns) {
      for (const base of anchorList) {
        const py = Math.max(minCY, Math.min(maxCY, base.y + dy));
        for (let px = minCX; px <= maxCX + 0.5; px += stepX) {
          const cx = Math.min(maxCX, px);
          const cy = py;
          const box = { x: cx - reserveW / 2, y: cy - reserveH / 2, w: reserveW, h: reserveH };
          if (!boxFits(box)) continue;
          const score = Math.pow((cx - base.x) * 0.62, 2) + Math.pow(cy - base.y, 2);
          if (!boxOnlyMatch) boxOnlyMatch = { cx, cy, box };
          if (leaderClear(cx, cy) && score < bestScore) {
            bestScore = score;
            best = { cx, cy, box };
          }
        }
      }
    }
  }

  if (!best) {
    for (let cy = minCY; cy <= maxCY + 0.5; cy += 10) {
      for (let cx = minCX; cx <= maxCX + 0.5; cx += 12) {
        const clampedX = Math.min(maxCX, cx);
        const clampedY = Math.min(maxCY, cy);
        const box = { x: clampedX - reserveW / 2, y: clampedY - reserveH / 2, w: reserveW, h: reserveH };
        if (!boxFits(box)) continue;
        if (!boxOnlyMatch) boxOnlyMatch = { cx: clampedX, cy: clampedY, box };
        if (!leaderClear(clampedX, clampedY)) continue;
        const { dist } = nearestAnchorFor(clampedX, clampedY, leaderPoints);
        if (dist < bestScore) {
          bestScore = dist;
          best = { cx: clampedX, cy: clampedY, box };
        }
      }
    }
  }

  return finalize(best || boxOnlyMatch, fallback);
}

export function createTopologyDefs(prefix = "a-topo") {
  const defs = createSVGEl("defs", { "data-refresh-key": `${prefix}-defs` });
  const addArrow = (id, color, size = 5.2) => {
    const marker = createSVGEl("marker", {
      id,
      "data-refresh-key": id,
      viewBox: "0 0 10 10",
      refX: "8.2",
      refY: "5",
      markerWidth: String(size),
      markerHeight: String(size),
      orient: "auto-start-reverse",
    });
    marker.append(createSVGEl("path", { d: "M 0 1.8 L 8.5 5 L 0 8.2 z", fill: color }));
    defs.append(marker);
  };

  const tones = [
    ["ok", OK_COLOR],
    ["warn", WARN_COLOR],
    ["bad", BAD_COLOR],
    ["idle", IDLE_FLOW_COLOR],
    ["running", RUNNING_COLOR],
  ];
  for (const [name, color] of tones) {
    addArrow(`${prefix}-arrow-${name}`, color, 5.2);
    addArrow(`${prefix}-arrow-sm-${name}`, color, 4.2);
  }

  return defs;
}

/** Pure network-monitoring entity key constructors. */
export const entityKey = Object.freeze({
  node: (id) => `node:${String(id || "")}`,
  external: (id) => {
    const s = String(id || "");
    return s.startsWith("ext:") ? s : `ext:${s}`;
  },
  probe: (id) => `probe:${String(id || "")}`,
  cluster: (memberIDs) => {
    const uniqueSorted = [...new Set((memberIDs || []).map((id) => String(id || "")).filter(Boolean))].sort();
    return `cluster:${uniqueSorted.join(",")}`;
  },
});

export function buildPeerProbeTags(model) {
  const map = new Map();
  for (const n of (model?.nodes || [])) map.set(n.id, new Set());
  for (const ext of (model?.externalTargets || [])) map.set(ext.id, new Set());
  for (const p of (model?.probeLinks || [])) {
    const tag = entityKey.probe(p.id);
    const parentTag = p.probeID && p.probeID !== p.id ? entityKey.probe(p.probeID) : "";
    const fromKey = entityKey.node(p.from);
    const toKey = p.isExternal ? entityKey.external(p.to) : entityKey.node(p.to);
    map.get(p.from)?.add(tag);
    if (parentTag) map.get(p.from)?.add(parentTag);
    map.get(p.from)?.add(toKey);
    map.get(p.to)?.add(tag);
    if (parentTag) map.get(p.to)?.add(parentTag);
    map.get(p.to)?.add(fromKey);
  }
  const out = new Map();
  map.forEach((set, k) => out.set(k, [...set]));
  return out;
}

export function buildTopologySelectionIndex(model, clusters = []) {
  const peerTags = buildPeerProbeTags(model);
  const nodeMap = new Map();
  const extMap = new Map();
  const probeMap = new Map();
  const chainMap = new Map();
  const clusterMap = new Map();

  for (const n of (model?.nodes || [])) {
    const nKey = entityKey.node(n.id);
    nodeMap.set(n.id, [nKey, ...(peerTags.get(n.id) || [])]);
  }
  for (const ext of (model?.externalTargets || [])) {
    const extKey = entityKey.external(ext.id);
    extMap.set(ext.id, [extKey, ...(peerTags.get(ext.id) || [])]);
  }
  for (const ch of (model?.probeChains || [])) {
    const cKey = entityKey.probe(ch.id);
    const set = new Set([cKey]);
    for (const h of (ch.hops || [])) set.add(entityKey.probe(h.id));
    for (const ep of (ch.endpoints || [])) {
      set.add(ep.kind === "ext" ? entityKey.external(ep.id) : entityKey.node(ep.id));
    }
    chainMap.set(ch.id, [...set]);
  }
  for (const p of (model?.probeLinks || [])) {
    const pKey = entityKey.probe(p.id);
    const parentKey = p.probeID && p.probeID !== p.id ? entityKey.probe(p.probeID) : "";
    const fromKey = entityKey.node(p.from);
    const toKey = p.isExternal ? entityKey.external(p.to) : entityKey.node(p.to);
    const set = new Set([pKey, ...(parentKey ? [parentKey] : []), fromKey, toKey]);
    probeMap.set(p.id, [...set]);
  }
  for (const cl of (clusters || [])) {
    if (!cl || (cl.members || []).length <= 1) continue;
    const cKey = cl.key || entityKey.cluster(cl.members.map((m) => m.node.id));
    const cPeers = new Set([cKey]);
    for (const m of cl.members) {
      const nKey = entityKey.node(m.node.id);
      cPeers.add(nKey);
      for (const tag of (nodeMap.get(m.node.id) || [])) cPeers.add(tag);
    }
    clusterMap.set(cKey, [...cPeers]);
  }

  return {
    nodePeers: (id) => nodeMap.get(id) || [entityKey.node(id)],
    extPeers: (id) => extMap.get(id) || [entityKey.external(id)],
    probePeers: (probe) => probeMap.get(probe.id) || chainMap.get(probe.id) || [
      entityKey.probe(probe.id),
      ...(probe.from ? [entityKey.node(probe.from)] : []),
      ...(probe.to ? [probe.isExternal ? entityKey.external(probe.to) : entityKey.node(probe.to)] : []),
    ],
    chainPeers: (chainID) => chainMap.get(chainID) || probeMap.get(chainID) || [entityKey.probe(chainID)],
    clusterPeers: (clusterKey) => clusterMap.get(clusterKey) || [clusterKey],
  };
}

export function matchesActiveSelection(peers, sel) {
  if (!sel) return null;
  if (peers.includes(sel)) return true;
  if (sel.startsWith("cluster:")) {
    const memberIDs = sel.slice("cluster:".length).split(",").filter(Boolean);
    if (memberIDs.some((id) => peers.includes(`node:${id}`))) return true;
  }
  return false;
}

export function reconcileSelectionKey(key, model, { clusters = null, allowCluster = true } = {}) {
  const s = String(key || "");
  if (!s || !model || !Array.isArray(model.nodes) || !model.nodes.length) return "";
  const nodeSet = new Set(model.nodes.map((n) => n.id));
  const extSet = new Set((model.externalTargets || []).map((e) => e.id));
  const probeSet = new Set([
    ...(model.probeLinks || []).map((p) => p.id),
    ...(model.probeChains || []).map((c) => c.id),
  ]);

  if (s.startsWith("node:")) return nodeSet.has(s.slice(5)) ? s : "";
  if (s.startsWith("ext:")) return extSet.has(s) ? s : "";
  if (s.startsWith("probe:")) return probeSet.has(s.slice(6)) ? s : "";
  if (s.startsWith("cluster:")) {
    const alive = s.slice(8).split(",").filter((id) => nodeSet.has(id));
    if (!alive.length) return "";
    if (!allowCluster || alive.length === 1) return entityKey.node(alive[0]);
    const targetClusters = clusters || clusterTopologyNodes(model.nodes);
    const aliveSet = new Set(alive);
    const hitClusters = targetClusters.filter((cl) => cl.members.some((m) => aliveSet.has(m.node.id)));
    if (hitClusters.length === 1) {
      const target = hitClusters[0];
      return target.members.length > 1 ? target.key : entityKey.node(target.members[0].node.id);
    }
    if (hitClusters.length > 1) return entityKey.node(alive[0]);
    return "";
  }
  return "";
}

const HUB_GEO_KEYWORDS = Object.freeze([
  { countries: ["CN"], keys: ["上海", "shanghai", "sha", "pvg"], lat: 31.23, lon: 121.47 },
  { countries: ["CN"], keys: ["北京", "beijing", "pek", "bjs"], lat: 39.90, lon: 116.40 },
  { countries: ["CN"], keys: ["广州", "guangzhou", "can"], lat: 23.13, lon: 113.26 },
  { countries: ["CN"], keys: ["深圳", "shenzhen", "szx"], lat: 22.54, lon: 114.06 },
  { countries: ["CN"], keys: ["杭州", "hangzhou", "hgh"], lat: 30.27, lon: 120.15 },
  { countries: ["CN"], keys: ["成都", "chengdu", "ctu"], lat: 30.57, lon: 104.07 },
  { countries: ["HK"], keys: ["香港", "hong kong", "hongkong", "hkg", "hk-"], lat: 22.32, lon: 114.17 },
  { countries: ["TW"], keys: ["台北", "臺北", "taipei", "tpe", "tw-"], lat: 25.03, lon: 121.57 },
  { countries: ["TW"], keys: ["高雄", "kaohsiung"], lat: 22.63, lon: 120.30 },
  { countries: ["JP"], keys: ["东京", "東京", "tokyo", "nrt", "hnd", "tyo", "jp-"], lat: 35.68, lon: 139.69 },
  { countries: ["JP"], keys: ["大阪", "osaka", "kix"], lat: 34.69, lon: 135.50 },
  { countries: ["KR"], keys: ["首尔", "seoul", "icn", "sel", "kr-"], lat: 37.57, lon: 126.98 },
  { countries: ["KR"], keys: ["釜山", "busan"], lat: 35.18, lon: 129.08 },
  { countries: ["SG"], keys: ["新加坡", "singapore", "sin", "sg-"], lat: 1.35, lon: 103.82 },
  { countries: ["MY"], keys: ["吉隆坡", "kuala lumpur", "kul", "my-"], lat: 3.14, lon: 101.69 },
  { countries: ["TH"], keys: ["曼谷", "bangkok", "bkk"], lat: 13.76, lon: 100.50 },
  { countries: ["VN"], keys: ["胡志明", "ho chi minh", "sgn"], lat: 10.82, lon: 106.63 },
  { countries: ["VN"], keys: ["河内", "hanoi", "han"], lat: 21.03, lon: 105.85 },
  { countries: ["ID"], keys: ["雅加达", "jakarta", "cgk"], lat: -6.21, lon: 106.85 },
  { countries: ["PH"], keys: ["马尼拉", "manila", "mnl"], lat: 14.60, lon: 120.98 },
  { countries: ["IN"], keys: ["孟买", "mumbai", "bom", "in-"], lat: 19.08, lon: 72.88 },
  { countries: ["IN"], keys: ["新德里", "delhi", "del"], lat: 28.61, lon: 77.21 },
  { countries: ["IN"], keys: ["班加罗尔", "bangalore", "blr"], lat: 12.97, lon: 77.59 },
  { countries: ["AE"], keys: ["迪拜", "dubai", "dxb", "ae-"], lat: 25.20, lon: 55.27 },
  { countries: ["SA"], keys: ["利雅得", "riyadh"], lat: 24.71, lon: 46.68 },
  { countries: ["IL"], keys: ["特拉维夫", "tel aviv", "tlv"], lat: 32.09, lon: 34.78 },
  { countries: ["TR"], keys: ["伊斯坦布尔", "istanbul", "ist"], lat: 41.01, lon: 28.98 },
  { countries: ["AU"], keys: ["悉尼", "sydney", "syd", "au-"], lat: -33.87, lon: 151.21 },
  { countries: ["AU"], keys: ["墨尔本", "melbourne", "mel"], lat: -37.81, lon: 144.96 },
  { countries: ["NZ"], keys: ["奥克兰", "auckland", "akl"], lat: -36.85, lon: 174.76 },
  { countries: ["DE"], keys: ["法兰克福", "frankfurt", "fra", "de-"], lat: 50.11, lon: 8.68 },
  { countries: ["DE"], keys: ["慕尼黑", "munich", "muc"], lat: 48.14, lon: 11.58 },
  { countries: ["DE"], keys: ["柏林", "berlin", "ber"], lat: 52.52, lon: 13.40 },
  { countries: ["GB", "UK"], keys: ["伦敦", "london", "lhr", "lon", "uk-", "gb-"], lat: 51.51, lon: -0.13 },
  { countries: ["NL"], keys: ["阿姆斯特丹", "amsterdam", "ams", "nl-"], lat: 52.37, lon: 4.90 },
  { countries: ["FR"], keys: ["巴黎", "paris", "cdg", "par", "fr-"], lat: 48.86, lon: 2.35 },
  { countries: ["IE"], keys: ["都柏林", "dublin", "dub"], lat: 53.35, lon: -6.26 },
  { countries: ["CH"], keys: ["苏黎世", "zurich", "zrh"], lat: 47.37, lon: 8.54 },
  { countries: ["SE"], keys: ["斯德哥尔摩", "stockholm", "arn"], lat: 59.33, lon: 18.07 },
  { countries: ["FI"], keys: ["赫尔辛基", "helsinki", "hel"], lat: 60.17, lon: 24.94 },
  { countries: ["ES"], keys: ["马德里", "madrid", "mad"], lat: 40.42, lon: -3.70 },
  { countries: ["IT"], keys: ["米兰", "milan", "mxp"], lat: 45.46, lon: 9.19 },
  { countries: ["RU"], keys: ["莫斯科", "moscow", "mow"], lat: 55.76, lon: 37.62 },
  { countries: ["US"], keys: ["圣何塞", "san jose", "sjc", "硅谷", "us-10"], lat: 37.34, lon: -121.89 },
  { countries: ["US"], keys: ["旧金山", "san francisco", "sfo"], lat: 37.77, lon: -122.42 },
  { countries: ["US"], keys: ["洛杉矶", "los angeles", "lax", "us-04"], lat: 34.05, lon: -118.24 },
  { countries: ["US"], keys: ["西雅图", "seattle", "sea"], lat: 47.61, lon: -122.33 },
  { countries: ["US"], keys: ["达拉斯", "dallas", "dfw"], lat: 32.78, lon: -96.80 },
  { countries: ["US"], keys: ["芝加哥", "chicago", "ord"], lat: 41.88, lon: -87.63 },
  { countries: ["US"], keys: ["纽约", "new york", "nyc", "jfk", "ewr"], lat: 40.71, lon: -74.01 },
  { countries: ["US"], keys: ["阿什本", "ashburn", "iad", "弗吉尼亚"], lat: 39.04, lon: -77.49 },
  { countries: ["US"], keys: ["迈阿密", "miami", "mia"], lat: 25.76, lon: -80.19 },
  { countries: ["CA"], keys: ["多伦多", "toronto", "yyz", "ca-"], lat: 43.65, lon: -79.38 },
  { countries: ["CA"], keys: ["温哥华", "vancouver", "yvr"], lat: 49.28, lon: -123.12 },
  { countries: ["BR"], keys: ["圣保罗", "sao paulo", "gru", "br-"], lat: -23.55, lon: -46.63 },
  { countries: ["AR"], keys: ["布宜诺斯艾利斯", "buenos aires", "eze"], lat: -34.60, lon: -58.38 },
  { countries: ["ZA"], keys: ["约翰内斯堡", "johannesburg", "jnb", "za-"], lat: -26.20, lon: 28.05 },
  { countries: ["ZA"], keys: ["开普敦", "cape town", "cpt"], lat: -33.92, lon: 18.42 },
]);

const REGION_CENTROIDS = Object.freeze({
  CN: [31.23, 121.47], HK: [22.32, 114.17], MO: [22.20, 113.54], TW: [25.03, 121.57],
  JP: [35.68, 139.69], KR: [37.57, 126.98], SG: [1.35, 103.82], MY: [3.14, 101.69],
  TH: [13.76, 100.50], VN: [10.82, 106.63], ID: [-6.21, 106.85], PH: [14.60, 120.98],
  IN: [19.08, 72.88], AE: [25.20, 55.27], SA: [24.71, 46.68], IL: [32.09, 34.78],
  TR: [41.01, 28.98], RU: [55.76, 37.62], GB: [51.51, -0.13], UK: [51.51, -0.13],
  DE: [50.11, 8.68], FR: [48.86, 2.35], NL: [52.37, 4.90], IE: [53.35, -6.26],
  CH: [47.37, 8.54], SE: [59.33, 18.07], FI: [60.17, 24.94], ES: [40.42, -3.70],
  IT: [45.46, 9.19], PL: [52.23, 21.01], US: [37.34, -121.89], CA: [43.65, -79.38],
  MX: [19.43, -99.13], BR: [-23.55, -46.63], AR: [-34.60, -58.38], CL: [-33.45, -70.67],
  AU: [-33.87, 151.21], NZ: [-36.85, 174.76], ZA: [-26.20, 28.05], EG: [30.04, 31.24],
  NG: [6.52, 3.38], KE: [-1.29, 36.82],
});

const GEO_REGEX_CACHE = new Map();
const escapeRegExp = (str) => str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

export function matchesGeoKeyword(haystack, kw) {
  const source = String(haystack || "");
  const needle = String(kw || "");
  if (!needle) return false;
  if (/[^\x00-\x7f]/.test(needle)) {
    return source.toLowerCase().includes(needle.toLowerCase());
  }
  let rx = GEO_REGEX_CACHE.get(needle);
  if (!rx) {
    const escaped = escapeRegExp(needle);
    rx = needle.endsWith("-")
      ? new RegExp(`(?:^|[^a-z0-9])${escaped}[a-z0-9]`, "i")
      : new RegExp(`(?:^|[^a-z0-9])${escaped}(?:$|[^a-z0-9])`, "i");
    GEO_REGEX_CACHE.set(needle, rx);
  }
  return rx.test(source);
}

const normCountryCode = (code) => {
  const c = String(code || "").trim().toUpperCase();
  return c === "UK" ? "GB" : c;
};

export function resolveNodeGeoPoint(node, fallbackIndex = 0) {
  const haystack = `${node?.name || ""} ${node?.id || ""}`;
  const rawRC = String(node?.regionCode || node?.region_code || node?.region || "").trim().toUpperCase();
  const normRC = normCountryCode(rawRC);

  if (normRC) {
    for (const hub of HUB_GEO_KEYWORDS) {
      if (!hub.countries.some((c) => normCountryCode(c) === normRC)) continue;
      if (hub.keys.some((kw) => matchesGeoKeyword(haystack, kw))) {
        const pt = projectPacificGeo(hub.lat, hub.lon);
        return { x: pt.x, y: pt.y, lat: hub.lat, lon: hub.lon, matched: true, source: "city", label: hub.keys[0], country: hub.countries[0] };
      }
    }
    const centroid = REGION_CENTROIDS[rawRC] || REGION_CENTROIDS[normRC];
    if (centroid) {
      const region = geoRegionDetails(rawRC);
      const pt = projectPacificGeo(centroid[0], centroid[1]);
      return { x: pt.x, y: pt.y, lat: centroid[0], lon: centroid[1], matched: true, source: "country", label: region?.name || rawRC, country: normRC };
    }
  }

  for (const hub of HUB_GEO_KEYWORDS) {
    if (hub.keys.some((kw) => matchesGeoKeyword(haystack, kw))) {
      const pt = projectPacificGeo(hub.lat, hub.lon);
      return { x: pt.x, y: pt.y, lat: hub.lat, lon: hub.lon, matched: true, source: "city", label: hub.keys[0], country: hub.countries[0] };
    }
  }

  const region = rawRC ? geoRegionDetails(rawRC) : null;
  const idx = Math.max(0, Number(fallbackIndex) || 0);
  const cols = 6;
  const col = idx % cols;
  const row = Math.floor(idx / cols);
  const fallbackLon = 152 + col * 14;
  const fallbackLat = -14 - row * 11;
  const pt = projectPacificGeo(fallbackLat, fallbackLon);
  return {
    x: Math.max(PACIFIC_MAP_BOUNDS.padX + 40, Math.min(PACIFIC_MAP_BOUNDS.width - PACIFIC_MAP_BOUNDS.padX - 40, pt.x)),
    y: Math.max(120, Math.min(PACIFIC_MAP_BOUNDS.height - 46, pt.y)),
    lat: fallbackLat,
    lon: fallbackLon,
    matched: false,
    source: "fallback",
    label: region?.name || "待定位",
    country: normRC,
  };
}

export function formatMapCityLabel(name, node = null) {
  const raw = String(name || "").trim();
  if (!raw && !node) return "-";
  const baseName = raw || String(node?.name || node?.id || "");
  const parts = baseName.split(/\s*[·|]\s*/);
  if (parts.length > 1) return parts[0].trim();

  if (node) {
    const geo = resolveNodeGeoPoint(node, 0);
    if (geo.source === "city" && /[\u4e00-\u9fff]/.test(geo.label)) {
      return geo.label;
    }
    const rc = String(node.regionCode || node.region_code || node.region || "").trim().toUpperCase();
    const region = rc ? geoRegionDetails(rc) : null;
    if (region?.name) return region.name;
  }
  return baseName;
}

export function extractCityClusterLabel(members) {
  if (!Array.isArray(members) || !members.length) return "同城节点";
  const primaryNode = members[0]?.node || members[0];
  const geo = members[0]?.geo || resolveNodeGeoPoint(primaryNode, 0);
  if (geo?.source === "city" && /[\u4e00-\u9fff]/.test(geo.label || "")) {
    return geo.label;
  }
  const region = geoRegionDetails(primaryNode?.regionCode || primaryNode?.region_code || primaryNode?.region);
  const rawCity = formatMapCityLabel(primaryNode?.name, primaryNode);
  if (/[·|]/.test(String(primaryNode?.name || ""))) return rawCity;
  return region?.name || rawCity;
}

export function clusterTopologyNodes(nodes, { radiusPx = CO_LOCATED_RADIUS_PX, scaleX = 1, scaleY = 1, offsetX = 0, offsetY = 0, worldWrapSpan = 0, viewportWidth = PACIFIC_MAP_BOUNDS.width } = {}) {
  const wrapSpan = worldWrapSpan > 0 ? Number(worldWrapSpan) : 0;
  const wrapMod = (v) => (wrapSpan > 0 ? ((v % wrapSpan) + wrapSpan) % wrapSpan : v);
  const items = [];
  let unmatchedIdx = 0;
  for (const node of (nodes || [])) {
    const precheck = resolveNodeGeoPoint(node, 0);
    const geo = precheck.matched ? precheck : resolveNodeGeoPoint(node, unmatchedIdx++);
    const rawWx = geo.x * scaleX;
    const wx = wrapMod(rawWx);
    const wy = geo.y * scaleY;
    const cityKey = geo.matched
      ? (geo.source === "city" ? `city:${geo.label}` : `country:${geo.country}:${Math.round(geo.x)}:${Math.round(geo.y)}`)
      : `unmatched:${node.id}`;
    items.push({ node, geo, worldX: wx, worldY: wy, cityKey });
  }

  const n = items.length;
  const visited = new Array(n).fill(false);
  const components = [];
  const sameCityEdge = (a, b) => {
    if (!a.geo.matched || !b.geo.matched) return false;
    if (a.cityKey === b.cityKey) return true;
    const rawDx = Math.abs(a.worldX - b.worldX);
    const dx = wrapSpan > 0 ? Math.min(rawDx, Math.abs(wrapSpan - rawDx)) : rawDx;
    return Math.hypot(dx, a.worldY - b.worldY) < radiusPx;
  };

  for (let i = 0; i < n; i++) {
    if (visited[i]) continue;
    const queue = [i];
    visited[i] = true;
    const comp = [];
    while (queue.length) {
      const cur = queue.shift();
      comp.push(items[cur]);
      for (let j = 0; j < n; j++) {
        if (!visited[j] && sameCityEdge(items[cur], items[j])) {
          visited[j] = true;
          queue.push(j);
        }
      }
    }
    components.push(comp);
  }

  return components.map((comp) => {
    const members = [...comp].sort((a, b) => {
      const aOn = a.node.isOnline ? 0 : 1;
      const bOn = b.node.isOnline ? 0 : 1;
      if (aOn !== bOn) return aOn - bOn;
      return String(a.node.id).localeCompare(String(b.node.id));
    });
    const refWx = members[0].worldX;
    const unwrappedSumX = members.reduce((s, m) => {
      if (wrapSpan <= 0) return s + m.worldX;
      const d = ((((m.worldX - refWx) % wrapSpan) + wrapSpan * 1.5) % wrapSpan) - wrapSpan * 0.5;
      return s + refWx + d;
    }, 0);
    const avgWorldX = wrapMod(unwrappedSumX / members.length);
    const avgWorldY = members.reduce((s, m) => s + m.worldY, 0) / members.length;
    let sx = offsetX + avgWorldX;
    if (wrapSpan > 0) {
      const k = Math.round((viewportWidth * 0.5 - sx) / wrapSpan);
      sx += k * wrapSpan;
    }
    const sy = offsetY + avgWorldY;
    const key = members.length > 1
      ? entityKey.cluster(members.map((m) => m.node.id))
      : entityKey.node(members[0].node.id);
    const label = extractCityClusterLabel(members);
    return {
      key,
      isCluster: members.length > 1,
      primary: members[0],
      members,
      label,
      worldX: avgWorldX,
      worldY: avgWorldY,
      geoX: sx,
      geoY: sy,
      x: sx,
      y: sy,
    };
  });
}

export function findSameCityMembers(nodes, targetNodeID) {
  if (!targetNodeID || !Array.isArray(nodes) || !nodes.length) return [];
  const clusters = clusterTopologyNodes(nodes);
  const hit = clusters.find((cl) => cl.isCluster && cl.members.some((m) => m.node.id === targetNodeID));
  return hit ? hit.members.map((m) => m.node) : [];
}

/**
 * Computes deduplicated topological grid coordinates (`col`, `row`) for a monitoring
 * domain group or single-group chain using pure network-monitoring graph structure.
 */
export function planGroupTopologyGraph(group, { maxCols = 4, stacked = false } = {}) {
  const safeMaxCols = Math.max(2, Math.min(4, Number(maxCols) || 4));
  const vertexMap = new Map();
  const adj = new Map();
  const addVertex = (v) => {
    if (!vertexMap.has(v.key)) {
      vertexMap.set(v.key, v);
      adj.set(v.key, new Set());
    }
    return vertexMap.get(v.key);
  };

  for (const n of group?.nodes || []) {
    addVertex({
      key: entityKey.node(n.id),
      kind: "node",
      id: n.id,
      label: n.name || n.id,
      isOnline: Boolean(n.isOnline),
      node: n,
      ext: null,
    });
  }
  for (const e of group?.externalTargets || []) {
    const key = entityKey.external(e.id);
    addVertex({
      key,
      kind: "ext",
      id: e.id,
      label: e.name || e.address || e.id,
      isOnline: true,
      node: null,
      ext: e,
    });
  }

  const chains = group?.chains || [];
  const edges = [];
  const nodeHopStats = new Map();
  const bumpStats = (key, hop) => {
    const st = nodeHopStats.get(key) || { links: 0, bad: 0, latSum: 0, latCount: 0 };
    st.links += 1;
    if (hop.tone === "bad" || hop.tone === "warn") st.bad += 1;
    if (hop.latencyMS != null) {
      st.latSum += Number(hop.latencyMS);
      st.latCount += 1;
    }
    nodeHopStats.set(key, st);
  };

  for (const chain of chains) {
    for (const ep of chain.endpoints || []) {
      if (ep.kind === "ext") {
        addVertex({
          key: entityKey.external(ep.id),
          kind: "ext",
          id: ep.id,
          label: ep.name || ep.address || ep.id,
          isOnline: true,
          node: null,
          ext: { id: ep.id, name: ep.name || ep.address, address: ep.address || "", linkCount: 1 },
        });
      } else {
        addVertex({
          key: entityKey.node(ep.id),
          kind: "node",
          id: ep.id,
          label: ep.name || ep.id,
          isOnline: Boolean(ep.online),
          node: { id: ep.id, name: ep.name || ep.id, regionCode: ep.regionCode || "", isOnline: Boolean(ep.online), ip: ep.address || "", issue: ep.issue || "", shared: Boolean(ep.shared), role: ep.role || "" },
          ext: null,
        });
      }
    }
    for (const hop of chain.hops || []) {
      const u = entityKey.node(hop.from);
      const v = hop.isExternal ? entityKey.external(hop.to) : entityKey.node(hop.to);
      if (adj.has(u) && adj.has(v)) {
        adj.get(u).add(v);
        adj.get(v).add(u);
      }
      bumpStats(u, hop);
      bumpStats(v, hop);
      edges.push([u, v, hop]);
    }
  }

  const allVerts = [...vertexMap.values()];
  for (const v of allVerts) {
    const st = nodeHopStats.get(v.key) || { links: 0, bad: 0, latSum: 0, latCount: 0 };
    v.degree = adj.get(v.key)?.size || 0;
    v.degreeInGroup = st.links;
    v.badCount = st.bad;
    v.avgLatencyMS = st.latCount ? Math.round((st.latSum / st.latCount) * 10) / 10 : null;
  }

  const sortVertsDefault = (arr) => [...arr].sort((a, b) => (
    (b.degree - a.degree)
    || (b.degreeInGroup - a.degreeInGroup)
    || (Number(b.isOnline) - Number(a.isOnline))
    || String(a.label).localeCompare(String(b.label))
  ));

  const placements = new Map();
  if (!allVerts.length) {
    return { cols: 1, rows: 1, layout: "empty", vertices: [], placements, edgeCount: 0 };
  }

  let layout = "grid";
  let cols = 1;

  const hasMultiHop = chains.some((c) => c.isMultiHop || (c.endpoints?.length || 0) >= 3);
  const nodeVerts = allVerts.filter((v) => v.kind === "node");
  const extVerts = allVerts.filter((v) => v.kind === "ext");

  const sortColByBarycenter = (colKeys, refRowMap, fallbackAll) => {
    colKeys.sort((ka, kb) => {
      const avgOf = (k) => {
        const neighbors = [...(adj.get(k) || [])].filter((nb) => refRowMap.has(nb));
        if (!neighbors.length) return Number.MAX_SAFE_INTEGER;
        return neighbors.reduce((s, nb) => s + refRowMap.get(nb), 0) / neighbors.length;
      };
      const ba = avgOf(ka);
      const bb = avgOf(kb);
      if (ba !== bb) return ba - bb;
      return fallbackAll(vertexMap.get(ka), vertexMap.get(kb));
    });
  };

  if (stacked) {
    layout = "stack";
    cols = 1;
    const ordered = [];
    if (hasMultiHop || chains.length === 1) {
      const seen = new Set();
      for (const chain of chains) {
        for (const ep of chain.endpoints || []) {
          const k = ep.kind === "ext" ? entityKey.external(ep.id) : entityKey.node(ep.id);
          if (!seen.has(k) && vertexMap.has(k)) {
            seen.add(k);
            ordered.push(vertexMap.get(k));
          }
        }
      }
      for (const v of sortVertsDefault(allVerts)) {
        if (!seen.has(v.key)) ordered.push(v);
      }
    } else {
      ordered.push(...sortVertsDefault(nodeVerts), ...sortVertsDefault(extVerts));
    }
    ordered.forEach((v, idx) => placements.set(v.key, { col: 0, row: idx }));
  } else if (edges.length === 0) {
    layout = "grid";
    cols = Math.min(safeMaxCols, Math.max(1, Math.ceil(Math.sqrt(allVerts.length))));
    sortVertsDefault(allVerts).forEach((v, i) => {
      placements.set(v.key, { col: i % cols, row: Math.floor(i / cols) });
    });
  } else if (extVerts.length > 0 && !hasMultiHop && edges.every(([, vKey]) => vertexMap.get(vKey)?.kind === "ext" || vKey.startsWith("ext:"))) {
    layout = "bipartite";
    cols = 2;
    const left = sortVertsDefault(nodeVerts);
    const leftRows = new Map(left.map((v, idx) => [v.key, idx]));
    const rightKeys = extVerts.map((v) => v.key);
    sortColByBarycenter(rightKeys, leftRows, (a, b) => (b.degreeInGroup - a.degreeInGroup) || a.label.localeCompare(b.label));
    left.forEach((v, r) => placements.set(v.key, { col: 0, row: r }));
    rightKeys.forEach((k, r) => placements.set(k, { col: 1, row: r }));
  } else if (hasMultiHop || chains.length === 1) {
    layout = "chain";
    const rank = new Map();
    const visitOrder = [];
    const seen = new Set();
    for (const chain of chains) {
      const eps = chain.endpoints || [];
      eps.forEach((ep, idx) => {
        const k = ep.kind === "ext" ? entityKey.external(ep.id) : entityKey.node(ep.id);
        if (!vertexMap.has(k)) return;
        const prev = rank.has(k) ? rank.get(k) : -1;
        if (idx > prev) rank.set(k, idx);
        if (!seen.has(k)) {
          seen.add(k);
          visitOrder.push(k);
        }
      });
    }
    for (const v of sortVertsDefault(allVerts)) {
      if (!seen.has(v.key)) {
        rank.set(v.key, 0);
        visitOrder.push(v.key);
      }
    }
    const maxRank = Math.max(0, ...visitOrder.map((k) => rank.get(k) || 0));
    cols = Math.max(2, Math.min(safeMaxCols, maxRank + 1));

    const occupied = new Set();
    const rankBucketCount = new Map();
    for (const k of visitOrder) {
      const r = rank.get(k) || 0;
      const band = Math.floor(r / cols);
      const stepInBand = r % cols;
      const col = band % 2 === 1 ? (cols - 1 - stepInBand) : stepInBand;
      let row = band + (rankBucketCount.get(`${band}:${col}`) || 0);
      while (occupied.has(`${col}:${row}`)) row += 1;
      occupied.add(`${col}:${row}`);
      rankBucketCount.set(`${band}:${col}`, (rankBucketCount.get(`${band}:${col}`) || 0) + 1);
      placements.set(k, { col, row });
    }
  } else if (allVerts.length <= 5) {
    layout = "maxcut-2col";
    cols = 2;
    const sorted = sortVertsDefault(allVerts);
    const side = new Map();
    let count0 = 0;
    let count1 = 0;
    for (const v of sorted) {
      if (v.kind === "ext") {
        side.set(v.key, 1);
        count1 += 1;
        continue;
      }
      const nbs = [...(adj.get(v.key) || [])];
      const links0 = nbs.filter((nb) => side.get(nb) === 0).length;
      const links1 = nbs.filter((nb) => side.get(nb) === 1).length;
      const target = links0 !== links1 ? (links0 < links1 ? 0 : 1) : (count0 <= count1 ? 0 : 1);
      side.set(v.key, target);
      if (target === 0) count0 += 1;
      else count1 += 1;
    }
    const limit = Math.ceil(sorted.length / 2);
    while (count0 > limit) {
      const move = sorted.find((v) => side.get(v.key) === 0);
      if (!move) break;
      side.set(move.key, 1);
      count0 -= 1;
      count1 += 1;
    }
    while (count1 > limit) {
      const move = sorted.find((v) => side.get(v.key) === 1 && v.kind !== "ext");
      if (!move) break;
      side.set(move.key, 0);
      count1 -= 1;
      count0 += 1;
    }

    const leftKeys = sorted.filter((v) => side.get(v.key) === 0).map((v) => v.key);
    const rightKeys = sorted.filter((v) => side.get(v.key) === 1).map((v) => v.key);
    const leftMap = new Map(leftKeys.map((k, idx) => [k, idx]));
    sortColByBarycenter(rightKeys, leftMap, (a, b) => (b.degree - a.degree) || a.label.localeCompare(b.label));
    const rightMap = new Map(rightKeys.map((k, idx) => [k, idx]));
    sortColByBarycenter(leftKeys, rightMap, (a, b) => (b.degree - a.degree) || a.label.localeCompare(b.label));

    leftKeys.forEach((k, r) => placements.set(k, { col: 0, row: r }));
    rightKeys.forEach((k, r) => placements.set(k, { col: 1, row: r }));
  } else {
    layout = "hub-center-3col";
    cols = Math.min(safeMaxCols, 3);
    const sorted = sortVertsDefault(allVerts);
    const hubCount = Math.max(1, Math.min(2, Math.round(sorted.length / 4)));
    const hubs = sorted.filter((v) => v.kind !== "ext").slice(0, hubCount);
    const hubSet = new Set(hubs.map((v) => v.key));
    const satellites = sorted.filter((v) => !hubSet.has(v.key));

    const leftKeys = [];
    const rightKeys = [];
    const side = new Map();
    for (const v of satellites) {
      if (v.kind === "ext") {
        rightKeys.push(v.key);
        side.set(v.key, 2);
        continue;
      }
      const nbs = [...(adj.get(v.key) || [])];
      const satLeftLinks = nbs.filter((nb) => side.get(nb) === 0).length;
      const satRightLinks = nbs.filter((nb) => side.get(nb) === 2).length;
      const goLeft = satLeftLinks !== satRightLinks
        ? satLeftLinks < satRightLinks
        : leftKeys.length <= rightKeys.length;
      if (goLeft) {
        leftKeys.push(v.key);
        side.set(v.key, 0);
      } else {
        rightKeys.push(v.key);
        side.set(v.key, 2);
      }
    }
    while (leftKeys.length - rightKeys.length > 1) {
      rightKeys.push(leftKeys.pop());
    }
    while (rightKeys.length - leftKeys.length > 1) {
      const idx = rightKeys.findIndex((k) => vertexMap.get(k)?.kind !== "ext");
      if (idx < 0) break;
      const [k] = rightKeys.splice(idx, 1);
      leftKeys.push(k);
    }

    const centerKeys = hubs.map((h) => h.key);
    const centerMap = new Map(centerKeys.map((k, idx) => [k, idx]));
    const cmpFallback = (a, b) => (b.degree - a.degree) || a.label.localeCompare(b.label);
    sortColByBarycenter(leftKeys, centerMap, cmpFallback);
    sortColByBarycenter(rightKeys, centerMap, cmpFallback);

    const maxWingRows = Math.max(leftKeys.length, rightKeys.length);
    const centerStart = maxWingRows > centerKeys.length ? Math.floor((maxWingRows - centerKeys.length) / 2) : 0;
    leftKeys.forEach((k, r) => placements.set(k, { col: 0, row: r }));
    centerKeys.forEach((k, r) => placements.set(k, { col: 1, row: centerStart + r }));
    rightKeys.forEach((k, r) => placements.set(k, { col: 2, row: r }));
  }

  let maxRow = 0;
  for (const v of allVerts) {
    const pos = placements.get(v.key) || { col: 0, row: 0 };
    v.col = pos.col;
    v.row = pos.row;
    v.isHub = layout === "hub-center-3col" && pos.col === 1;
    if (pos.row > maxRow) maxRow = pos.row;
  }

  return {
    cols,
    rows: maxRow + 1,
    layout,
    vertices: allVerts,
    placements,
    edgeCount: edges.length,
  };
}
