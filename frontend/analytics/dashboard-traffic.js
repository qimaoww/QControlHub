import {
  buildDashboardTrafficGrid, dashboardTrafficAxis, dashboardTrafficPeriodMonths, dashboardTrafficRange,
  niceTrafficScaleMax, rankDashboardTrafficView, summarizeDashboardTrafficPeriods, summarizeDashboardTrafficRange, utcMonth,
} from "../modules/dashboard-model.js";
import { figure, progressBar, quantityParts, readout } from "./dashboard-console.js";
import { createSVGEl } from "./dashboard-panel.js";
import { patch } from "./live-dom.js";
import { nodeRegionFlag } from "./node-region.js";
import { bindPreferenceFields } from "./preferences.js";
import { actions, button, field, link, list, node, pathID, stack, table } from "./ui.js";

// Redesigned traffic card: balanced 65% / 35% split.
// Left: clean inline summary + smooth emerald/slate dual area-line trend on a UTC axis.
// Right: compact Top 5 node leaderboard with click-to-inspect details and zero page bloat.
const PERIODS = [["today", "今日"], ["week", "本周"], ["month", "本月"]];
const PERIOD_STATES = { loading: "读取中…", failed: "读取失败", denied: "无权限" };
const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const BLANK = ["—", ""];
const NO_ITEMS = Object.freeze([]);
const PAD_LEFT = 52;
const PAD_RIGHT = 10;
const PLOT_TOP = 14;
const PLOT_BASE = 160;
const RULER_HEIGHT = 26;
const LANE_BAR = 4;
const LANE_FOLD = 5;
const LANE_CAP = 60;

const el = (tag, className, ...children) => node(tag, { className }, children);
const span = (className, ...children) => el("span", className, children);
const keyed = (key, element) => { element.setAttribute("data-refresh-key", key); return element; };
const weekdayOf = (day) => new Date(`${day}T00:00:00Z`).getUTCDay();
const shortDay = (day) => day.slice(5);
const fixed = (value) => Number(value).toFixed(1);
const periodLabel = (value) => PERIODS.find(entry => entry[0] === value)?.[1] || "本月";
const inRange = (day, range) => day >= range.start && day <= range.end;

const duplex = (received, sent, bytes, suffix = "") => [
  node("span", { className: "a-flux-rx", "data-refresh-key": "rx" }, `↓ ${bytes(received)}${suffix}`),
  node("span", { className: "a-flux-tx", "data-refresh-key": "tx" }, `↑ ${bytes(sent)}${suffix}`),
];

function createPeriodTab([value, label], onSelect) {
  const input = node("input", { type: "radio", name: "flux_period", value, className: "a-flux-radio" });
  const element = node("label", { className: "a-flux-tab", "data-refresh-key": `tab-${value}` },
    input,
    span("a-flux-tab-text", label));
  input.addEventListener("change", () => { if (input.checked) onSelect(value); });
  return { value, input, element };
}

const legendKey = (mark, label) => span("a-flux-key", span(`a-flux-mark ${mark}`), label);

// Fritsch-Carlson monotone cubic Bezier path: silky smooth curves that never
// overshoot below zero or above local peaks.
function monotonePath(points) {
  const n = points.length;
  if (!n) return "";
  if (n === 1) return `M${fixed(points[0][0])} ${fixed(points[0][1])}`;
  if (n === 2) return `M${fixed(points[0][0])} ${fixed(points[0][1])} L${fixed(points[1][0])} ${fixed(points[1][1])}`;
  const dx = new Array(n - 1);
  const dy = new Array(n - 1);
  const slope = new Array(n - 1);
  for (let i = 0; i < n - 1; i++) {
    dx[i] = points[i + 1][0] - points[i][0];
    dy[i] = points[i + 1][1] - points[i][1];
    slope[i] = dx[i] ? dy[i] / dx[i] : 0;
  }
  const tangents = new Array(n);
  tangents[0] = slope[0];
  tangents[n - 1] = slope[n - 2];
  for (let i = 1; i < n - 1; i++) {
    if (slope[i - 1] * slope[i] <= 0) {
      tangents[i] = 0;
    } else {
      const w1 = 2 * dx[i] + dx[i - 1];
      const w2 = dx[i] + 2 * dx[i - 1];
      tangents[i] = (w1 + w2) / (w1 / slope[i - 1] + w2 / slope[i]);
    }
  }
  const parts = [`M${fixed(points[0][0])} ${fixed(points[0][1])}`];
  for (let i = 0; i < n - 1; i++) {
    const [x0, y0] = points[i];
    const [x1, y1] = points[i + 1];
    const h = dx[i] / 3;
    const c1y = Math.max(PLOT_TOP, Math.min(PLOT_BASE, y0 + tangents[i] * h));
    const c2y = Math.max(PLOT_TOP, Math.min(PLOT_BASE, y1 - tangents[i + 1] * h));
    parts.push(`C${fixed(x0 + h)} ${fixed(c1y)}, ${fixed(x1 - h)} ${fixed(c2y)}, ${fixed(x1)} ${fixed(y1)}`);
  }
  return parts.join(" ");
}

// Slim 4px rounded pill track with emerald receive + cool slate send segments.
function laneBar(item, scale, width) {
  const safeW = Math.max(80, Math.round(width));
  const r = (LANE_BAR / 2).toFixed(1);
  const svg = createSVGEl("svg", {
    class: "a-flux-lane-bar",
    viewBox: `0 0 ${safeW} ${LANE_BAR}`,
    height: LANE_BAR,
    "aria-hidden": "true",
  });
  svg.append(createSVGEl("rect", { x: 0, y: 0, width: safeW, height: LANE_BAR, rx: r, class: "a-flux-lane-track" }));
  if (item.used <= 0) return svg;
  const totalLen = Math.max(4, (item.used / Math.max(scale, item.used)) * safeW);
  const rxLen = (item.received / item.used) * totalLen;
  const txLen = totalLen - rxLen;
  if (txLen >= 1.5 && rxLen >= 1.5) {
    const gap = 1.5;
    const rxW = Math.max(2, rxLen - gap / 2);
    const txX = rxW + gap;
    const txW = Math.max(2, totalLen - txX);
    svg.append(
      createSVGEl("rect", { x: 0, y: 0, width: fixed(rxW), height: LANE_BAR, rx: r, class: "a-flux-lane-rx" }),
      createSVGEl("rect", { x: fixed(txX), y: 0, width: fixed(txW), height: LANE_BAR, rx: r, class: "a-flux-lane-tx" }),
    );
  } else if (rxLen >= txLen) {
    svg.append(createSVGEl("rect", { x: 0, y: 0, width: fixed(totalLen), height: LANE_BAR, rx: r, class: "a-flux-lane-rx" }));
  } else {
    svg.append(createSVGEl("rect", { x: 0, y: 0, width: fixed(totalLen), height: LANE_BAR, rx: r, class: "a-flux-lane-tx" }));
  }
  return svg;
}

// Geometry inside the padded plot area [PAD_LEFT, width - PAD_RIGHT].
function plotGeometry(count, width) {
  const inner = Math.max(100, width - PAD_LEFT - PAD_RIGHT);
  const step = count > 0 ? inner / count : 0;
  return {
    inner,
    step,
    left: (index) => PAD_LEFT + index * step,
    center: (index) => PAD_LEFT + (index + 0.5) * step,
  };
}

function dayLabel(entry, bytes) {
  const head = `${entry.day} ${WEEKDAYS[weekdayOf(entry.day)]}`;
  if (!entry.rows) return `${head}，未采集`;
  return `${head}，合计 ${bytes(entry.total)}，接收 ${bytes(entry.received)}，发送 ${bytes(entry.sent)}`;
}

function gridMarks(scale, width, bytes) {
  const marks = [];
  const right = width - PAD_RIGHT;
  for (let part = 1; part <= 3; part++) {
    const y = PLOT_BASE - (part / 3) * (PLOT_BASE - PLOT_TOP);
    marks.push(createSVGEl("line", { x1: PAD_LEFT, y1: fixed(y), x2: right, y2: fixed(y), class: "a-flux-grid" }));
    marks.push(createSVGEl("text", { x: PAD_LEFT - 10, y: fixed(y), class: "a-flux-grid-label" }, bytes((scale * part) / 3)));
  }
  marks.push(createSVGEl("text", { x: PAD_LEFT - 10, y: PLOT_BASE, class: "a-flux-grid-label" }, "0"));
  return marks;
}

function visibleDays(view) {
  const days = view?.grid?.days || NO_ITEMS;
  const elapsed = days.filter(entry => !entry.future);
  return elapsed.length ? elapsed : days;
}

function rulerMarks(days, geom, width) {
  const marks = [createSVGEl("text", { x: PAD_LEFT - 10, y: PLOT_BASE + 18, class: "a-flux-zone" }, "UTC")];
  const right = width - PAD_RIGHT;
  const labelIndices = [];
  days.forEach((entry, index) => {
    const cx = geom.center(index);
    const x = fixed(cx);
    const monday = weekdayOf(entry.day) === 1;
    let size = monday ? 5 : 2.5;
    if (index > 0 && entry.day.endsWith("-01")) size = 8;
    marks.push(createSVGEl("line", { x1: x, y1: PLOT_BASE, x2: x, y2: PLOT_BASE + size, class: monday ? "a-flux-tick is-week" : "a-flux-tick" }));
    if (monday) labelIndices.push(index);
  });
  if (days.length > 0) {
    if (!labelIndices.length || geom.center(labelIndices[0]) - geom.center(0) >= 52) {
      labelIndices.unshift(0);
    }
    const last = days.length - 1;
    if (last > 0 && !labelIndices.includes(last)) {
      const prev = labelIndices.at(-1);
      if (prev == null || geom.center(last) - geom.center(prev) >= 44) {
        labelIndices.push(last);
      } else if (prev > 0) {
        labelIndices[labelIndices.length - 1] = last;
      }
    }
  }
  for (const index of labelIndices) {
    const cx = geom.center(index);
    const atLeft = index === 0 || cx - 20 < PAD_LEFT;
    const atRight = index === days.length - 1 || cx + 20 > right;
    const anchor = atLeft ? "start" : (atRight ? "end" : "middle");
    const lx = atLeft ? Math.max(PAD_LEFT, cx - 2) : (atRight ? Math.min(right, cx + 2) : cx);
    marks.push(createSVGEl("text", {
      x: fixed(lx),
      y: PLOT_BASE + 18,
      "text-anchor": anchor,
      class: "a-flux-tick-label",
    }, shortDay(days[index].day)));
  }
  return marks;
}

function splitCollectedSegments(coords, filterFn = null) {
  const segments = [];
  let cur = [];
  for (const item of coords) {
    const ok = item.hasRows && (!filterFn || filterFn(item));
    if (ok) {
      cur.push(item);
    } else if (cur.length) {
      segments.push(cur);
      cur = [];
    }
  }
  if (cur.length) segments.push(cur);
  return segments;
}

function appendCurves(svg, segments, { gradId, faded = false }) {
  const clsSuffix = faded ? " is-faded" : "";
  for (const seg of segments) {
    if (seg.length === 1) {
      const pt = seg[0];
      svg.append(
        createSVGEl("line", { x1: fixed(pt.x), y1: fixed(Math.min(pt.rxY, pt.txY)), x2: fixed(pt.x), y2: PLOT_BASE, class: `a-flux-stem${clsSuffix}` }),
        createSVGEl("circle", { cx: fixed(pt.x), cy: fixed(pt.txY), r: "3", class: `a-flux-solo-tx${clsSuffix}` }),
        createSVGEl("circle", { cx: fixed(pt.x), cy: fixed(pt.rxY), r: "3.5", class: `a-flux-solo-rx${clsSuffix}` }),
      );
      continue;
    }
    const rxPts = seg.map(pt => [pt.x, pt.rxY]);
    const txPts = seg.map(pt => [pt.x, pt.txY]);
    const rxLine = monotonePath(rxPts);
    const txLine = monotonePath(txPts);
    if (!faded) {
      const firstX = fixed(rxPts[0][0]);
      const lastX = fixed(rxPts.at(-1)[0]);
      svg.append(createSVGEl("path", {
        d: `${rxLine} L${lastX} ${PLOT_BASE} L${firstX} ${PLOT_BASE} Z`,
        fill: `url(#${gradId})`,
        class: "a-flux-area-rx",
      }));
    }
    svg.append(
      createSVGEl("path", { d: txLine, class: `a-flux-line-tx${clsSuffix}` }),
      createSVGEl("path", { d: rxLine, class: `a-flux-line-rx${clsSuffix}` }),
    );
  }
}

function hitMark(entry, index, geom, height, bytes) {
  return createSVGEl("rect", {
    x: fixed(geom.left(index)),
    y: PLOT_TOP - 4,
    width: fixed(Math.max(2, geom.step)),
    height: height - PLOT_TOP + 2,
    class: "a-flux-hit",
    role: "button",
    tabindex: -1,
    "data-day": index,
    "aria-pressed": "false",
    "aria-label": dayLabel(entry, bytes),
  });
}

// Smooth monochrome dual area-line chart spanning elapsed days edge-to-edge.
function drawPlot(view, width, bytes) {
  const days = visibleDays(view);
  const height = PLOT_BASE + RULER_HEIGHT;
  const geom = plotGeometry(days.length, width);
  const seriesPeak = days.reduce((max, day) => (day.future || !day.rows) ? max : Math.max(max, day.received || 0, day.sent || 0), 0);
  const scale = niceTrafficScaleMax(seriesPeak);
  const usableH = PLOT_BASE - PLOT_TOP;
  const gradId = "a-flux-rx-grad";

  const svg = createSVGEl("svg", {
    class: "a-flux-svg",
    viewBox: `0 0 ${width} ${height}`,
    height,
    role: "group",
    "aria-label": "每日流量趋势，深色实线为接收，浅灰虚线为发送",
  });

  const defs = createSVGEl("defs");
  const grad = createSVGEl("linearGradient", { id: gradId, x1: "0", y1: "0", x2: "0", y2: "1" });
  grad.append(
    createSVGEl("stop", { offset: "0%", "stop-color": "#18181b", "stop-opacity": "0.08" }),
    createSVGEl("stop", { offset: "100%", "stop-color": "#18181b", "stop-opacity": "0.0" }),
  );
  defs.append(grad);
  svg.append(defs);

  const coords = days.map((entry, index) => {
    const x = geom.center(index);
    const within = !entry.future && inRange(entry.day, view.range);
    const hasRows = !entry.future && Boolean(entry.rows);
    const rxY = hasRows && scale > 0 ? PLOT_BASE - (Math.max(0, entry.received || 0) / scale) * usableH : PLOT_BASE;
    const txY = hasRows && scale > 0 ? PLOT_BASE - (Math.max(0, entry.sent || 0) / scale) * usableH : PLOT_BASE;
    return { index, x, rxY, txY, within, hasRows, future: Boolean(entry.future) };
  });

  const inside = coords.filter(c => c.within);
  const partialWindow = inside.length > 0 && inside.length < coords.filter(c => !c.future).length;

  // Soft monochrome highlight band + baseline accent only when inspecting 今日 or 本周 inside the month
  if (partialWindow) {
    const x0 = geom.left(inside[0].index);
    const x1 = geom.left(inside.at(-1).index) + geom.step;
    svg.append(createSVGEl("rect", {
      x: fixed(x0), y: PLOT_TOP - 2, width: fixed(Math.max(2, x1 - x0)), height: PLOT_BASE - PLOT_TOP + 2,
      rx: 4, class: "a-flux-window-band",
    }));
  }

  if (seriesPeak > 0) svg.append(...gridMarks(scale, width, bytes));
  svg.append(
    createSVGEl("line", { x1: PAD_LEFT, y1: PLOT_BASE, x2: width - PAD_RIGHT, y2: PLOT_BASE, class: "a-flux-base" }),
    ...rulerMarks(days, geom, width),
  );

  // Subtle muted baseline dots on elapsed days without persisted rows
  for (const pt of coords) {
    if (!pt.future && !pt.hasRows) {
      svg.append(createSVGEl("circle", { cx: fixed(pt.x), cy: PLOT_BASE - 3.5, r: "1.4", class: "a-flux-void-dot" }));
    }
  }

  // Draw curves (context faded if period is a sub-window, then crisp active segment)
  if (partialWindow) {
    appendCurves(svg, splitCollectedSegments(coords), { gradId, faded: true });
    appendCurves(svg, splitCollectedSegments(coords, pt => pt.within), { gradId, faded: false });
    const from = fixed(geom.left(inside[0].index) + 1);
    const to = fixed(geom.left(inside.at(-1).index) + geom.step - 1);
    svg.append(createSVGEl("line", { x1: from, y1: PLOT_BASE + 0.5, x2: to, y2: PLOT_BASE + 0.5, class: "a-flux-range-line" }));
  } else {
    const segments = splitCollectedSegments(coords);
    appendCurves(svg, segments, { gradId, faded: false });
    const lastSeg = segments.at(-1);
    if (lastSeg && lastSeg.length > 1) {
      const endPt = lastSeg.at(-1);
      svg.append(createSVGEl("circle", { cx: fixed(endPt.x), cy: fixed(endPt.rxY), r: "2.6", class: "a-flux-end-rx" }));
    }
  }

  // O(1) hover / pin focus guide & monochrome hollow dots
  svg.append(
    createSVGEl("line", { x1: PAD_LEFT, y1: PLOT_TOP - 2, x2: PAD_LEFT, y2: PLOT_BASE, class: "a-flux-guide", hidden: true }),
    createSVGEl("circle", { cx: PAD_LEFT, cy: PLOT_BASE, r: "3.4", class: "a-flux-dot-tx", hidden: true }),
    createSVGEl("circle", { cx: PAD_LEFT, cy: PLOT_BASE, r: "4.2", class: "a-flux-dot-rx", hidden: true }),
  );

  days.forEach((entry, index) => { if (!entry.future) svg.append(hitMark(entry, index, geom, height, bytes)); });
  return { svg, coords, days };
}

function laneTag(item) {
  const quota = item.quotaPercent;
  if (item.blocked) return keyed("tag", span("a-flux-pill bad", "已封禁"));
  if (quota == null || quota < 80) return null;
  return keyed("tag", span(quota >= 100 ? "a-flux-pill bad" : "a-flux-pill warn", `配额 ${Math.round(quota)}%`));
}

function quotaReadout(item, bytes) {
  if (item.quotaPercent == null) return readout("配额", "配额", BLANK, "未设置");
  const tone = item.quotaPercent >= 100 ? "bad" : item.quotaPercent >= 80 ? "warn" : "";
  const note = [progressBar(Math.min(100, item.quotaPercent), tone, "is-slim"), `${bytes(item.limitedUsedBytes)} / ${bytes(item.limitBytes)}`];
  return readout("配额", "配额", [item.quotaPercent.toFixed(1), "%"], note, tone);
}

function laneInspect(item, bytes, canNodes) {
  const id = pathID(item.agentID);
  const live = item.receiveBPS > 0 || item.sendBPS > 0;
  const rate = live ? quantityParts(bytes(item.receiveBPS + item.sendBPS), "/s") : BLANK;
  const ports = item.portCount ? [String(item.portCount), ""] : BLANK;
  return node("div", { className: "a-flux-inspect", "data-refresh-key": "inspect" },
    quotaReadout(item, bytes),
    readout("收发", "收发", quantityParts(bytes(item.used)), duplex(item.received, item.sent, bytes)),
    readout("端口速率", "端口速率", rate, live ? duplex(item.receiveBPS, item.sendBPS, bytes, "/s") : ""),
    readout("端口", "端口", ports, item.owners.join("、")),
    node("div", { className: "a-flux-inspect-links", "data-refresh-key": "links" },
      link("流量", `#traffic?agent=${id}`),
      canNodes ? link("节点", `#node-settings?agent=${id}`) : null));
}

// Compact 2-line leaderboard row (name + total on line 1, thin 4px share bar on line 2).
function laneNode(ctx, item, context, rank = 0) {
  const meta = item.agent || { id: item.agentID, name: item.agentID, region_code: "" };
  const name = meta.name || item.agentID || "关联节点";
  const inModal = Boolean(context.inModal);
  const open = inModal && context.open === item.agentID;
  const rankBadge = inModal && rank > 0
    ? span(`a-flux-rank-num${rank <= 3 ? " is-top" : ""}`, String(rank))
    : null;
  const toggle = node("button", {
    type: "button",
    className: "a-flux-lane-name",
    "aria-expanded": inModal ? String(open) : null,
    "aria-haspopup": inModal ? null : "dialog",
    "data-lane": item.agentID,
    title: name,
  }, rankBadge, nodeRegionFlag(ctx, meta), span("a-flux-lane-text", name));
  const bar = keyed(`bar-${context.barWidth}-${item.received}-${item.sent}-${context.scale}`, laneBar(item, context.scale, context.barWidth));
  const valBlock = inModal
    ? el("div", "a-flux-lane-metrics",
        el("span", "a-flux-lane-duplex", ...duplex(item.received, item.sent, context.bytes)),
        node("strong", { className: "a-flux-lane-val", "data-refresh-key": "val" }, context.bytes(item.used)))
    : node("strong", { className: "a-flux-lane-val", "data-refresh-key": "val" }, context.bytes(item.used));
  const row = el("div", "a-flux-lane-row",
    el("div", "a-flux-lane-top",
      node("div", { className: "a-flux-lane-id", "data-refresh-key": "id" }, toggle, laneTag(item)),
      valBlock),
    node("div", { className: "a-flux-lane-track-wrap", "data-refresh-key": "track" }, bar));
  return node("li", { className: "a-flux-lane", "data-refresh-key": item.agentID, "data-open": open ? "true" : null },
    row, open ? laneInspect(item, context.bytes, context.canNodes) : null);
}

function laneSignature(item, open) {
  const quota = item.quotaPercent == null ? null : Math.round(item.quotaPercent);
  const live = open === item.agentID ? [item.receiveBPS, item.sendBPS, item.limitedUsedBytes, item.limitBytes] : null;
  return [
    item.agentID, item.used, item.received, item.sent, quota, item.blocked, item.portCount, item.hasCurrentUser, item.hasOtherUser,
    item.owners, item.agent?.name, item.agent?.region_code, item.agent?.labels?.region_code, live,
  ];
}

function inlineStat(key, label, valueText, noteText = "") {
  return node("div", { className: "a-flux-stat", "data-refresh-key": key },
    node("span", { className: "a-con-label", "data-refresh-key": "label" }, label),
    node("div", { className: "a-flux-stat-val", "data-refresh-key": "val" },
      node("strong", { "data-refresh-key": "num" }, valueText),
      noteText ? node("span", { className: "a-flux-stat-note", "data-refresh-key": "note" }, noteText) : null));
}

function daySummary(entry, pinned, bytes) {
  const title = `${shortDay(entry.day)} ${WEEKDAYS[weekdayOf(entry.day)]}（UTC）`;
  const tag = pinned ? keyed("pin", span("a-flux-pill ok", "已固定")) : null;
  if (!entry.rows) {
    return el("div", "a-flux-summary",
      el("div", "a-flux-summary-main",
        el("div", "a-flux-summary-ctx", span("a-con-label", title), tag),
        el("div", "a-flux-summary-hero", figure(BLANK, "a-con-fig a-flux-hero-fig"), span("a-con-note", "未采集"))));
  }
  return el("div", "a-flux-summary",
    el("div", "a-flux-summary-main",
      el("div", "a-flux-summary-ctx", span("a-con-label", title), tag),
      el("div", "a-flux-summary-hero",
        figure(quantityParts(bytes(entry.total)), "a-con-fig a-flux-hero-fig"),
        el("div", "a-flux-summary-duplex", ...duplex(entry.received, entry.sent, bytes)))),
    el("div", "a-flux-summary-stats",
      inlineStat("rx", "当日接收", bytes(entry.received)),
      inlineStat("tx", "当日发送", bytes(entry.sent))));
}

function rangeSummary(view, period, activeSource, bytes) {
  const summary = summarizeDashboardTrafficRange(view.grid.days, view.range);
  const single = view.range.start === view.range.end;
  const spanText = single ? shortDay(view.range.start) : `${shortDay(view.range.start)} 至 ${shortDay(view.range.end)}`;
  const prefix = view.live ? `${periodLabel(period)}合计` : `${view.month} 合计`;
  const ctxLabel = `${prefix} · ${spanText}（UTC）`;

  let statusPill = null;
  if (activeSource?.partial) statusPill = keyed("st", span("a-flux-pill warn", "不完整"));
  else if (activeSource?.state === "failed" && activeSource?.value) statusPill = keyed("st", span("a-flux-pill warn", "刷新失败"));

  if (!activeSource?.value) {
    const waitState = PERIOD_STATES[activeSource?.state] || "读取中…";
    const tone = activeSource?.state && activeSource.state !== "loading" ? "bad" : "";
    return el("div", "a-flux-summary",
      el("div", "a-flux-summary-main",
        el("div", "a-flux-summary-ctx", span("a-con-label", ctxLabel)),
        el("div", "a-flux-summary-hero",
          figure(BLANK, "a-con-fig a-flux-hero-fig"),
          span(tone ? `a-con-note ${tone}` : "a-con-note", waitState))));
  }

  if (!summary.rows) {
    return el("div", "a-flux-summary",
      el("div", "a-flux-summary-main",
        el("div", "a-flux-summary-ctx", span("a-con-label", ctxLabel), statusPill),
        el("div", "a-flux-summary-hero",
          figure(BLANK, "a-con-fig a-flux-hero-fig"),
          span("a-con-note", "未采集"))));
  }

  const stats = single
    ? [
        inlineStat("rx", "接收", bytes(summary.received)),
        inlineStat("tx", "发送", bytes(summary.sent)),
      ]
    : [
        inlineStat("avg", "日均流量", bytes(summary.total / summary.collected),
          summary.collected < summary.elapsed ? `${summary.collected}/${summary.elapsed} 天` : `${summary.elapsed} 天`),
        inlineStat("peak", "峰值日", bytes(summary.peakBytes),
          `${shortDay(summary.peakDay)} ${WEEKDAYS[weekdayOf(summary.peakDay)]}`),
      ];

  return el("div", "a-flux-summary",
    el("div", "a-flux-summary-main",
      el("div", "a-flux-summary-ctx", span("a-con-label", ctxLabel), statusPill),
      el("div", "a-flux-summary-hero",
        figure(quantityParts(bytes(summary.total)), "a-con-fig a-flux-hero-fig"),
        el("div", "a-flux-summary-duplex", ...duplex(summary.received, summary.sent, bytes)))),
    el("div", "a-flux-summary-stats", ...stats));
}

const sameItems = (left, right) => left.length === right.length && left.every((item, index) => item === right[index]);
const rowsOf = (values) => values.flatMap(value => list(value?.days));

function weekSource(sources) {
  const current = sources.at(-1), previous = sources[0];
  if (sources.length < 2 || !current.value) return current;
  if (previous.value) return { value: current.value, state: previous.state === "failed" ? "failed" : current.state };
  if (previous.state === "loading") return { value: null, state: "loading" };
  return { value: current.value, state: current.state, partial: true };
}

function activePeriodSource(view, period, periodSources) {
  if (!view.live) return view.sources.at(-1);
  if (period === "week") return weekSource(periodSources);
  return periodSources.at(-1);
}

function gridOf(stage, values, axis, now) {
  const stamp = `${axis[0]}|${axis.length}|${now.toISOString().slice(0, 10)}`;
  const cache = stage.gridCache;
  if (cache && cache.stamp === stamp && sameItems(cache.values, values)) return cache;
  if (cache?.stamp !== stamp) { stage.hover = -1; stage.pinned = -1; stage.rove = -1; }
  stage.gridCache = { stamp, values, revision: (cache?.revision || 0) + 1, grid: buildDashboardTrafficGrid(rowsOf(values), axis, now) };
  return stage.gridCache;
}

function stageNote(view) {
  const main = view.sources.at(-1);
  if (main.state === "denied") return "无权限";
  if (!main.value) return main.state === "failed" ? "读取失败" : "读取中…";
  if (view.sources.some(item => item.state === "failed")) return "刷新失败";
  if (view.sources.some(item => !item.value && item.state === "loading")) return "读取中…";
  if (view.sources.some(item => !item.value)) return "不完整";
  return view.grid.days.some(day => day.rows) ? "" : "暂无记录";
}

function detailRow(day, bytes) {
  if (!day.rows) return [day.day, "未采集", "—", "—", "—", "—"];
  return [day.day, bytes(day.received), bytes(day.sent), bytes(day.total), `${bytes(day.peakReceiveBPS)}/s`, `${bytes(day.peakSendBPS)}/s`];
}

function paintTabs(stage) {
  stage.tabs.forEach((tab) => {
    const active = stage.view.live && tab.value === stage.period;
    tab.input.checked = active;
    tab.element.classList.toggle("is-active", active);
  });
}

function paintHead(stage) {
  const view = stage.view, note = stageNote(view);
  if (stage.note.textContent !== note) stage.note.textContent = note;
  stage.note.hidden = !note;
  const ready = Boolean(view.sources.at(-1).value);
  stage.detailsButton.disabled = !ready;
  stage.voidKey.hidden = !ready || !view.grid.days.some(day => !day.future && !day.rows);
  const stamp = `${view.revision}|${ready}`;
  if (stage.detailStamp === stamp) return;
  stage.detailStamp = stamp;
  const days = ready ? view.grid.days.filter(day => !day.future) : NO_ITEMS;
  stage.detailPeriod.textContent = days.length ? `${days[0].day} 至 ${days.at(-1).day}（UTC）` : "";
  stage.detailTable.updateRows(days.map(day => detailRow(day, stage.bytes)));
}

const hitOf = (stage, index) => stage.plotHost.querySelector(`.a-flux-hit[data-day="${index}"]`);

function focusedDay(stage) {
  const element = document.activeElement;
  return element && stage.plotHost.contains(element) && element.matches(".a-flux-hit") ? Number(element.dataset.day) : -1;
}

function syncRoving(stage) {
  const days = stage.plottedDays.length ? stage.plottedDays : visibleDays(stage.view);
  let anchor = stage.rove >= 0 && stage.rove < days.length ? stage.rove : stage.pinned;
  if (anchor < 0 || anchor >= days.length) anchor = days.findIndex(day => day.day === stage.view.today);
  if (anchor < 0) anchor = days.length - 1;
  for (const hit of stage.plotHost.querySelectorAll(".a-flux-hit")) {
    const index = Number(hit.dataset.day);
    hit.setAttribute("tabindex", index === anchor ? "0" : "-1");
    hit.setAttribute("aria-pressed", String(index === stage.pinned));
  }
}

function paintPlot(stage) {
  const view = stage.view;
  if (!stage.width) return;
  const stamp = `${view.revision}|${stage.width}|${view.range.start}|${view.range.end}`;
  if (stamp === stage.plotStamp) return;
  stage.plotStamp = stamp;
  const focused = focusedDay(stage);
  const { svg, coords, days } = drawPlot(view, stage.width, stage.bytes);
  stage.dayCoords = coords;
  stage.plottedDays = days;
  if (stage.hover >= days.length) stage.hover = -1;
  if (stage.pinned >= days.length) stage.pinned = -1;
  if (stage.rove >= days.length) stage.rove = -1;
  stage.plotHost.replaceChildren(svg);
  syncRoving(stage);
  if (focused >= 0 && focused < days.length) hitOf(stage, focused)?.focus({ preventScroll: true });
}

function rangeContextText(view, period) {
  const single = view.range.start === view.range.end;
  const spanText = single ? shortDay(view.range.start) : `${shortDay(view.range.start)} 至 ${shortDay(view.range.end)}`;
  const prefix = view.live ? periodLabel(period) : view.month;
  return `${prefix} · ${spanText}（UTC）`;
}

function rankTraffic(stage, limit) {
  const view = stage.view;
  const raw = stage.getPolicies();
  const policies = Array.isArray(raw) ? raw : list(raw?.policies);
  const agents = stage.getAgents();
  const ranked = rankDashboardTrafficView({
    rows: rowsOf(view.sources.map(item => item.value)), month: view.month, today: view.today, range: view.range,
    agents, policies, pageAgents: Array.isArray(raw) ? NO_ITEMS : list(raw?.agents),
    scope: stage.scopeSelect.value || "all", limit, orderIDs: stage.getOrderIDs(),
  });
  return { ranked, known: policies.length > 0 || agents.length > 0 };
}

function paintRankDialog(stage) {
  if (!stage.rankDialog?.open || !stage.view) return;
  const ready = Boolean(stage.view.sources.at(-1).value);
  const { ranked } = rankTraffic(stage, "all");
  const items = ready ? ranked.items.slice(0, LANE_CAP) : NO_ITEMS;
  const rest = ranked.items.length - items.length;
  stage.rankMeta.textContent = `${rangeContextText(stage.view, stage.period)} · 共 ${ranked.totalCount} 个节点`;
  const context = { barWidth: 560, open: stage.modalOpen, bytes: stage.bytes, canNodes: stage.canNodes, scale: ranked.maxUsed, inModal: true };
  patch(stage.rankModalList, node("ol", { className: "a-flux-lane-list a-flux-rank-modal-list", "aria-label": "全部节点流量排行" },
    items.map((item, idx) => laneNode(stage.ctx, item, context, idx + 1))));
  stage.rankModalList.hidden = !items.length;
  stage.rankModalEmpty.hidden = items.length > 0;
  stage.rankModalMore.hidden = rest <= 0;
  stage.rankModalMore.textContent = rest > 0 ? `另有 ${rest} 个节点` : "";
}

function paintNodeDialog(stage) {
  if (!stage.nodeDialog?.open || !stage.view || !stage.nodeDialogID) return;
  const { ranked } = rankTraffic(stage, "all");
  const item = ranked.items.find(entry => entry.agentID === stage.nodeDialogID);
  if (!item) return;
  const meta = item.agent || { id: item.agentID, name: item.agentID, region_code: "" };
  const bar = laneBar(item, ranked.maxUsed, 440);
  patch(stage.nodeDialogBody, el("div", "a-flux-node-modal-body",
    el("div", "a-flux-node-modal-head",
      el("div", "a-flux-node-modal-id",
        nodeRegionFlag(stage.ctx, meta),
        span("a-con-label", rangeContextText(stage.view, stage.period)),
        laneTag(item)),
      node("strong", { className: "a-flux-node-modal-total" }, stage.bytes(item.used))),
    el("div", "a-flux-lane-track-wrap", bar),
    laneInspect(item, stage.bytes, stage.canNodes)));
}

function paintLanes(stage) {
  const view = stage.view;
  if (!view) return;
  const { ranked, known } = rankTraffic(stage, String(LANE_FOLD));
  if (known && !ranked.hasCrossOwner && stage.scopeSelect.value !== "all") {
    stage.scopeSelect.value = "all";
    paintLanes(stage);
    return;
  }
  stage.scopeField.hidden = !ranked.hasCrossOwner;
  const ready = Boolean(view.sources.at(-1).value);
  const items = ready ? ranked.items.slice(0, LANE_FOLD) : NO_ITEMS;
  const barW = Math.max(180, stage.sideWidth ? stage.sideWidth - 36 : 320);
  const stamp = JSON.stringify([ready, barW, view.range, ranked.totalCount, ranked.maxUsed, items.map(item => laneSignature(item, ""))]);
  if (stamp !== stage.laneStamp) {
    stage.laneStamp = stamp;
    const context = { barWidth: barW, open: "", bytes: stage.bytes, canNodes: stage.canNodes, scale: ranked.maxUsed, inModal: false };
    patch(stage.laneList, node("ol", { className: "a-flux-lane-list", "aria-label": "节点流量排行" }, items.map(item => laneNode(stage.ctx, item, context))));
    stage.laneList.hidden = !items.length;
    stage.lanesEmpty.hidden = items.length > 0;
    if (!items.length) {
      const mainState = view.sources.at(-1)?.state;
      stage.lanesEmpty.textContent = !ready
        ? (PERIOD_STATES[mainState] || "读取中…")
        : "当前周期暂无节点流量记录";
    }

    const foldable = ranked.totalCount > LANE_FOLD;
    stage.lanesTitle.textContent = foldable ? `节点排行 Top ${LANE_FOLD}` : "节点排行";
    stage.fold.hidden = !foldable;
    stage.fold.textContent = `全部 ${ranked.totalCount} 个`;
  }
  paintRankDialog(stage);
  paintNodeDialog(stage);
}

function applyCursor(stage) {
  const view = stage.view;
  if (!view || !stage.width) return;
  const days = stage.plottedDays.length ? stage.plottedDays : visibleDays(view);
  const active = stage.hover >= 0 && stage.hover < days.length
    ? stage.hover
    : (stage.pinned >= 0 && stage.pinned < days.length ? stage.pinned : -1);
  const guide = stage.plotHost.querySelector(".a-flux-guide");
  const dotRx = stage.plotHost.querySelector(".a-flux-dot-rx");
  const dotTx = stage.plotHost.querySelector(".a-flux-dot-tx");
  const coord = active >= 0 ? stage.dayCoords?.[active] : null;

  if (guide && dotRx && dotTx) {
    if (!coord) {
      guide.setAttribute("hidden", "true");
      dotRx.setAttribute("hidden", "true");
      dotTx.setAttribute("hidden", "true");
    } else {
      const x = fixed(coord.x);
      guide.removeAttribute("hidden");
      guide.setAttribute("x1", x);
      guide.setAttribute("x2", x);
      guide.setAttribute("class", active === stage.pinned ? "a-flux-guide is-pinned" : "a-flux-guide");
      if (coord.hasRows) {
        dotRx.removeAttribute("hidden");
        dotRx.setAttribute("cx", x);
        dotRx.setAttribute("cy", fixed(coord.rxY));
        dotTx.removeAttribute("hidden");
        dotTx.setAttribute("cx", x);
        dotTx.setAttribute("cy", fixed(coord.txY));
      } else {
        dotRx.setAttribute("hidden", "true");
        dotTx.setAttribute("hidden", "true");
      }
    }
  }

  const fresh = active >= 0
    ? daySummary(days[active], active === stage.pinned, stage.bytes)
    : rangeSummary(view, stage.period, stage.activeSource, stage.bytes);
  patch(stage.summaryHost, fresh);
}

function paintStage(stage) {
  const now = new Date(), today = now.toISOString().slice(0, 10);
  const month = stage.getMonth(), live = month === today.slice(0, 7);
  const periodSources = dashboardTrafficPeriodMonths(now).map(stage.source);
  const sources = live ? periodSources : [stage.source(month)];
  const cached = gridOf(stage, sources.map(item => item.value), dashboardTrafficAxis(month, now), now);
  stage.view = { grid: cached.grid, revision: cached.revision, range: dashboardTrafficRange(stage.period, month, now), today, month, live, sources };
  stage.activeSource = activePeriodSource(stage.view, stage.period, periodSources);
  summarizeDashboardTrafficPeriods(rowsOf(periodSources.map(item => item.value)), now);
  paintTabs(stage);
  paintHead(stage);
  paintPlot(stage);
  paintLanes(stage);
  applyCursor(stage);
}

function indexAt(stage, host, clientX) {
  const days = stage.plottedDays.length ? stage.plottedDays : visibleDays(stage.view);
  const frame = host.matches("svg") ? host : host.querySelector("svg");
  const box = frame?.getBoundingClientRect();
  if (!days.length || !box || box.width <= 0 || !stage.width) return -1;
  const scaleX = box.width / stage.width;
  const plotLeft = box.left + PAD_LEFT * scaleX;
  const plotRight = box.right - PAD_RIGHT * scaleX;
  if (clientX < plotLeft || clientX >= plotRight) return -1;
  const index = Math.min(days.length - 1, Math.floor(((clientX - plotLeft) / (plotRight - plotLeft)) * days.length));
  return days[index].future ? -1 : index;
}

function setHover(stage, index) {
  if (stage.hover === index) return;
  stage.hover = index;
  applyCursor(stage);
}

function togglePin(stage, index) {
  if (index < 0) return;
  stage.pinned = stage.pinned === index ? -1 : index;
  syncRoving(stage);
  applyCursor(stage);
}

function focusDay(stage, target) {
  if (!target.matches?.(".a-flux-hit")) return;
  stage.rove = Number(target.dataset.day);
  syncRoving(stage);
  setHover(stage, stage.rove);
}

function plotKey(stage, event) {
  const current = focusedDay(stage);
  if (current < 0) return;
  if (event.key === "Escape" && stage.pinned >= 0) { togglePin(stage, stage.pinned); return; }
  if (event.key === "Enter" || event.key === " ") { event.preventDefault(); togglePin(stage, current); return; }
  const hits = [...stage.plotHost.querySelectorAll(".a-flux-hit")];
  const at = hits.findIndex(hit => Number(hit.dataset.day) === current);
  const moves = { ArrowLeft: at - 1, ArrowRight: at + 1, Home: 0, End: hits.length - 1 };
  if (!Object.hasOwn(moves, event.key)) return;
  event.preventDefault();
  hits[Math.max(0, Math.min(hits.length - 1, moves[event.key]))].focus({ preventScroll: true });
}

function plotClick(stage, event) {
  const hit = event.target.closest?.(".a-flux-hit");
  togglePin(stage, hit ? Number(hit.dataset.day) : indexAt(stage, stage.plotHost, event.clientX));
}

function openNodeDialog(stage, agentID) {
  if (!stage.view || !agentID) return;
  const { ranked } = rankTraffic(stage, "all");
  const item = ranked.items.find(entry => entry.agentID === agentID);
  if (!item) return;
  const name = item.agent?.name || item.agentID || "关联节点";
  stage.nodeDialogID = agentID;
  stage.nodeDialog?.close();
  stage.nodeDialogBody = el("div", "a-flux-node-modal-body");
  stage.nodeDialog = stage.ctx.dialog(`${name} · 流量详情`, stage.nodeDialogBody);
  paintNodeDialog(stage);
}

function openRankDialog(stage) {
  if (stage.rankDialog?.open || !stage.view) return;
  stage.modalOpen = "";
  const legend = el("div", "a-flux-legend", legendKey("is-rx", "接收"), legendKey("is-tx", "发送"));
  const head = el("div", "a-flux-rank-modal-head", stage.rankMeta, legend);
  const body = stack(head, stage.rankModalList, stage.rankModalEmpty, actions(stage.rankModalMore, link("流量管理", "#traffic")));
  stage.rankDialog = stage.ctx.dialog("节点流量排行", body, { wide: true });
  paintRankDialog(stage);
}

function laneClick(stage, event) {
  const toggle = event.target.closest?.(".a-flux-lane-row")?.querySelector("[data-lane]");
  if (!toggle) return;
  openNodeDialog(stage, toggle.dataset.lane);
}

function modalLaneClick(stage, event) {
  const toggle = event.target.closest?.(".a-flux-lane-row")?.querySelector("[data-lane]");
  if (!toggle) return;
  stage.modalOpen = stage.modalOpen === toggle.dataset.lane ? "" : toggle.dataset.lane;
  paintRankDialog(stage);
}

function selectPeriod(stage, value) {
  stage.period = value;
  stage.ctx.state.data.dashboardPeriod = value;
  stage.pinned = -1;
  stage.hover = -1;
  const current = utcMonth();
  if (stage.getMonth() === current) { paintStage(stage); return; }
  stage.monthInput.value = current;
  stage.monthInput.dispatchEvent(new Event("change", { bubbles: true }));
}

function changeMonth(stage) {
  if (!stage.monthInput.checkValidity()) return;
  stage.pinned = -1;
  stage.hover = -1;
  stage.onMonth(stage.monthInput.value);
  paintStage(stage);
}

function openDetails(stage) {
  if (stage.dialog?.open) return;
  const body = stack(stage.detailPeriod, stage.detailTable, actions(link("流量管理", "#traffic")));
  stage.dialog = stage.ctx.dialog("流量明细", body, { wide: true });
}

function observeWidths(stage) {
  const applyPlot = (value) => {
    const next = Math.round(value);
    if (next <= 0 || next === stage.width) return;
    stage.width = next;
    if (stage.view) paintStage(stage);
  };
  const applySide = (value) => {
    const next = Math.round(value);
    if (next <= 0 || Math.abs(next - stage.sideWidth) < 2) return;
    stage.sideWidth = next;
    if (stage.view) paintLanes(stage);
  };
  if (typeof ResizeObserver !== "function") {
    applyPlot(Math.min(680, stage.plotHost.clientWidth || 640));
    applySide(stage.sidePane.clientWidth || 340);
    return;
  }
  const observer = new ResizeObserver((entries) => {
    for (const entry of entries) {
      if (entry.target === stage.plotHost) applyPlot(entry.contentRect?.width || 0);
      else if (entry.target === stage.sidePane) applySide(entry.contentRect?.width || 0);
    }
  });
  observer.observe(stage.plotHost);
  observer.observe(stage.sidePane);
  stage.ctx.signal.addEventListener("abort", () => observer.disconnect(), { once: true });
}

function bindStage(stage) {
  const { ctx, plotHost } = stage;
  const signal = ctx.signal;
  plotHost.addEventListener("mousedown", (event) => event.preventDefault(), { signal });
  plotHost.addEventListener("pointermove", (event) => { if (event.pointerType === "mouse") setHover(stage, indexAt(stage, plotHost, event.clientX)); }, { signal });
  plotHost.addEventListener("pointerleave", () => setHover(stage, focusedDay(stage)), { signal });
  plotHost.addEventListener("click", (event) => plotClick(stage, event), { signal });
  plotHost.addEventListener("focusin", (event) => focusDay(stage, event.target), { signal });
  plotHost.addEventListener("focusout", () => setHover(stage, -1), { signal });
  plotHost.addEventListener("keydown", (event) => plotKey(stage, event), { signal });
  stage.laneList.addEventListener("click", (event) => laneClick(stage, event), { signal });
  stage.rankModalList.addEventListener("click", (event) => modalLaneClick(stage, event), { signal });
  stage.fold.addEventListener("click", () => openRankDialog(stage), { signal });
  stage.monthInput.addEventListener("change", () => changeMonth(stage), { signal });
  bindPreferenceFields(ctx, "dashboard", [stage.monthField], { restore: () => changeMonth(stage) });
  stage.scopeSelect.addEventListener("change", () => paintLanes(stage), { signal });
  bindPreferenceFields(ctx, "dashboard", [stage.scopeField], { restore: () => paintLanes(stage) });
  signal.addEventListener("abort", () => {
    stage.dialog?.close();
    stage.rankDialog?.close();
    stage.nodeDialog?.close();
  }, { once: true });
}

const DETAIL_COLUMNS = ["日期", "接收", "发送", "合计", "接收峰值", "发送峰值"];
const RANK_SCOPES = [["all", "全部用户"], ["current", "当前用户"]];

function buildStage(ctx, options) {
  const saved = ctx.state.data.dashboardPeriod;
  const stage = {
    ctx, ...options,
    period: PERIODS.some(entry => entry[0] === saved) ? saved : "month",
    canNodes: ctx.can("agents.read"),
    width: 0, sideWidth: 0, dayCoords: [], plottedDays: [], hover: -1, pinned: -1, rove: -1,
    modalOpen: "", nodeDialogID: "",
    view: null, activeSource: null, dialog: null, rankDialog: null, nodeDialog: null, nodeDialogBody: null,
    plotStamp: "", laneStamp: "", detailStamp: "", gridCache: null,
  };
  stage.tabs = PERIODS.map(entry => createPeriodTab(entry, value => selectPeriod(stage, value)));
  stage.summaryHost = el("div", "a-flux-summary");
  stage.plotHost = el("div", "a-flux-plot");
  stage.mainPane = el("div", "a-flux-main", stage.summaryHost, stage.plotHost);

  stage.lanesTitle = span("a-flux-side-title", `节点排行 Top ${LANE_FOLD}`);
  stage.fold = node("button", { type: "button", className: "a-con-jump", "aria-haspopup": "dialog", hidden: true });
  stage.lanesHead = el("div", "a-flux-lanes-head", stage.lanesTitle, el("div", "a-flux-lanes-actions", stage.fold));
  stage.laneList = node("ol", { className: "a-flux-lane-list", "aria-label": "节点流量排行" });
  stage.lanesEmpty = el("p", "a-flux-lanes-empty a-muted", "读取中…");
  stage.lanesEmpty.hidden = true;
  stage.sidePane = el("div", "a-flux-side", stage.lanesHead, stage.laneList, stage.lanesEmpty);

  stage.rankMeta = el("span", "a-muted");
  stage.rankModalList = node("ol", { className: "a-flux-lane-list a-flux-rank-modal-list", "aria-label": "全部节点流量排行" });
  stage.rankModalEmpty = el("p", "a-flux-lanes-empty a-muted", "当前周期暂无节点流量记录");
  stage.rankModalEmpty.hidden = true;
  stage.rankModalMore = el("span", "a-con-note");
  stage.rankModalMore.hidden = true;

  stage.note = node("span", { className: "a-con-token", role: "status", hidden: true });
  stage.voidKey = legendKey("is-void", "未采集");
  stage.voidKey.hidden = true;
  stage.monthField = field("月份", "traffic_month", options.getMonth(), { type: "month", max: utcMonth(), required: true });
  stage.monthField.classList.add("a-dash-inline-field");
  stage.monthInput = stage.monthField.querySelector("input");
  stage.scopeField = field("范围", "traffic_rank_scope", "all", { options: RANK_SCOPES, "aria-label": "排行统计范围" });
  stage.scopeField.classList.add("a-dash-inline-field");
  stage.scopeField.hidden = true;
  stage.scopeSelect = stage.scopeField.querySelector("select");
  stage.detailTable = table(DETAIL_COLUMNS, NO_ITEMS, { label: "每日流量明细", paginate: 10, rowKey: row => row[0] });
  stage.detailPeriod = el("span", "a-muted");
  stage.detailsButton = button("明细", () => openDetails(stage));

  const legend = el("div", "a-flux-legend", legendKey("is-rx", "接收"), legendKey("is-tx", "发送"), stage.voidKey);
  const tools = el("div", "a-actions a-con-tools", stage.note, legend, stage.scopeField, stage.monthField, stage.detailsButton);
  const tabGroup = node("div", { className: "a-flux-tabs", role: "radiogroup", "aria-label": "统计周期" }, stage.tabs.map(t => t.element));
  const head = el("div", "a-card-head a-flux-head",
    el("div", "a-flux-head-left", node("h2", {}, "监控端口流量"), tabGroup),
    tools);
  stage.element = node("section", { className: "a-card a-flux" },
    head,
    el("div", "a-flux-split", stage.mainPane, stage.sidePane));
  return stage;
}

export function createTrafficStage(ctx, options) {
  const stage = buildStage(ctx, options);
  bindStage(stage);
  observeWidths(stage);
  return { element: stage.element, paint: () => paintStage(stage) };
}
