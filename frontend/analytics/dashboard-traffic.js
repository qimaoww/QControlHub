import {
  buildDashboardTrafficGrid, dashboardTrafficAxis, dashboardTrafficPeriodMonths, dashboardTrafficRange,
  niceTrafficScaleMax, rankDashboardTrafficView, summarizeDashboardTrafficPeriods, summarizeDashboardTrafficRange, utcMonth,
} from "../modules/dashboard-model.js";
import { figure, quantityParts, readout, segmentMeter } from "./dashboard-console.js";
import { createSVGEl } from "./dashboard-panel.js";
import { patch } from "./live-dom.js";
import { nodeRegionFlag } from "./node-region.js";
import { bindPreferenceFields } from "./preferences.js";
import { actions, button, field, link, list, node, pathID, section, stack, table } from "./ui.js";

// The traffic stage shows the persisted per-port daily traffic of the
// monitored ports: the period channels, the daily bars on a UTC day ruler
// and, below, the nodes ranked by the selected period on the same tracks.
const PERIODS = [["today", "今日", "今日流量"], ["week", "本周", "本周流量"], ["month", "本月", "本月流量"]];
const PERIOD_STATES = { loading: "读取中", failed: "读取失败", denied: "无权限" };
const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const BLANK = ["—", ""];
const NO_ITEMS = Object.freeze([]);
const PLOT_TOP = 16;
const PLOT_BASE = 208;
const RULER_HEIGHT = 36;
const LANE_BAR = 8;
const LANE_FOLD = 5;
const LANE_CAP = 60;

const el = (tag, className, ...children) => node(tag, { className }, children);
const span = (className, ...children) => el("span", className, children);
const keyed = (key, element) => { element.setAttribute("data-refresh-key", key); return element; };
const weekdayOf = (day) => new Date(`${day}T00:00:00Z`).getUTCDay();
const shortDay = (day) => day.slice(5);
const fixed = (value) => Number(value).toFixed(1);
const periodLabel = (value) => PERIODS.find(entry => entry[0] === value)[1];
const inRange = (day, range) => day >= range.start && day <= range.end;

const duplex = (received, sent, bytes, suffix = "") => [
  node("span", { className: "a-con-rx", "data-refresh-key": "rx" }, `↓ ${bytes(received)}${suffix}`),
  node("span", { className: "a-con-tx", "data-refresh-key": "tx" }, `↑ ${bytes(sent)}${suffix}`),
];

function channelBody(cell) {
  const notes = cell.notes.map((item, index) =>
    node("span", { className: item.tone ? `a-con-sub-item ${item.tone}` : "a-con-sub-item", "data-refresh-key": `note-${index}` }, item.text));
  return span("a-flux-channel-body",
    node("span", { className: "a-flux-channel-head", "data-refresh-key": "head" },
      node("span", { className: "a-con-label", "data-refresh-key": "label" }, cell.label),
      node("span", { className: "a-con-token", "data-refresh-key": "token" }, cell.token)),
    figure(cell.figure, "a-con-fig a-flux-channel-fig"),
    node("span", { className: "a-con-note", "data-refresh-key": "note" }, notes));
}

// One period channel: a native radio, so the group works with the keyboard
// without extra handling. Only the text beside the input is repainted.
function createChannel([value, label, key], onSelect) {
  const input = node("input", { type: "radio", name: "flux_period", value, className: "a-flux-radio" });
  const body = channelBody({ label, token: "", figure: BLANK, notes: NO_ITEMS });
  const element = node("label", { className: "a-flux-channel", "data-refresh-key": key, "data-state": "loading" }, input, body);
  input.addEventListener("change", () => { if (input.checked) onSelect(value); });
  const paint = (cell) => { element.dataset.state = cell.state; patch(body, channelBody(cell)); };
  return { value, input, element, paint };
}

const legendKey = (mark, label) => span("a-flux-key", span(`a-flux-mark ${mark}`), label);

// Receive solid and send hollow, as in the daily bars. The length is the
// node's amount against the largest node of the ranking.
function laneBar(item, scale, width) {
  const svg = createSVGEl("svg", { class: "a-flux-lane-bar", viewBox: `0 0 ${width} ${LANE_BAR}`, height: LANE_BAR, preserveAspectRatio: "none", "aria-hidden": "true" });
  if (item.used <= 0) return svg;
  const length = Math.max(2, item.used / Math.max(scale, item.used) * width);
  const first = item.received / item.used * length;
  if (first > 0) svg.append(createSVGEl("rect", { x: 0, y: 0, width: fixed(first), height: LANE_BAR, class: "a-flux-bar is-rx" }));
  if (length - first < 1) return svg;
  svg.append(createSVGEl("rect", { x: fixed(first + .5), y: .5, width: fixed(Math.max(.5, length - first - 1)), height: LANE_BAR - 1, class: "a-flux-bar is-tx" }));
  return svg;
}

// Day columns for a pixel width: the bars, the ruler and the cursor share
// this geometry.
function dayColumns(count, width) {
  const step = count > 0 ? width / count : 0;
  return { step, left: (index) => index * step, center: (index) => (index + .5) * step };
}

function dayLabel(entry, bytes) {
  const head = `${entry.day} ${WEEKDAYS[weekdayOf(entry.day)]}`;
  if (!entry.rows) return `${head}，未采集`;
  return `${head}，合计 ${bytes(entry.total)}，接收 ${bytes(entry.received)}，发送 ${bytes(entry.sent)}`;
}

const barWidth = (step) => Math.max(2, Math.min(18, Math.round(step * .58)));

// Solid receive below hollow send, stacked into one bar. A day without rows
// gets a short dashed mark; a measured zero keeps a solid one.
function dayMarks(entry, x, width, scale, tone) {
  const left = fixed(x), size = fixed(width);
  if (!entry.rows) return [createSVGEl("line", { x1: left, y1: PLOT_BASE - 3.5, x2: fixed(x + width), y2: PLOT_BASE - 3.5, class: "a-flux-void" })];
  if (entry.total <= 0) return [createSVGEl("rect", { x: left, y: PLOT_BASE - 2, width: size, height: 2, class: `a-flux-bar is-rx ${tone}` })];
  const full = Math.max(2, entry.total / scale * (PLOT_BASE - PLOT_TOP));
  const lower = entry.received / entry.total * full;
  const marks = [];
  if (lower > 0) marks.push(createSVGEl("rect", { x: left, y: fixed(PLOT_BASE - lower), width: size, height: fixed(lower), class: `a-flux-bar is-rx ${tone}` }));
  if (full - lower < 1) return marks;
  marks.push(createSVGEl("rect", {
    x: fixed(x + .5), y: fixed(PLOT_BASE - full + .5), width: fixed(Math.max(1, width - 1)), height: fixed(Math.max(.5, full - lower - 1)),
    class: `a-flux-bar is-tx ${tone}`,
  }));
  return marks;
}

function gridMarks(scale, width, bytes) {
  const marks = [];
  for (let part = 1; part <= 3; part++) {
    const y = PLOT_BASE - part / 3 * (PLOT_BASE - PLOT_TOP);
    marks.push(createSVGEl("line", { x1: 0, y1: fixed(y), x2: width, y2: fixed(y), class: "a-flux-grid" }));
    marks.push(createSVGEl("text", { x: -8, y: fixed(y), class: "a-flux-grid-label" }, bytes(scale * part / 3)));
  }
  return marks;
}

// One tick per day. A Monday tick is longer, carries its date and continues
// as a week line through the plot; the first day of a month that follows the
// lead days of the week is longer still. The axis is labelled as UTC.
function rulerMarks(days, columns) {
  const marks = [createSVGEl("text", { x: -8, y: PLOT_BASE + 21, class: "a-flux-zone" }, "UTC")];
  days.forEach((entry, index) => {
    const x = fixed(columns.left(index));
    const monday = weekdayOf(entry.day) === 1;
    let size = monday ? 8 : 4;
    if (index > 0 && entry.day.endsWith("-01")) size = 12;
    if (monday && index > 0) marks.push(createSVGEl("line", { x1: x, y1: 0, x2: x, y2: PLOT_BASE, class: "a-flux-week" }));
    marks.push(createSVGEl("line", { x1: x, y1: PLOT_BASE, x2: x, y2: PLOT_BASE + size, class: monday ? "a-flux-tick is-week" : "a-flux-tick" }));
    if (monday) marks.push(createSVGEl("text", { x: fixed(columns.left(index) + 3), y: PLOT_BASE + 21, class: "a-flux-tick-label" }, shortDay(entry.day)));
  });
  return marks;
}

function nowMark(columns, index) {
  const x = columns.center(index);
  return createSVGEl("path", { d: `M${fixed(x - 4)} ${PLOT_BASE + 10}L${fixed(x)} ${PLOT_BASE + 4}L${fixed(x + 4)} ${PLOT_BASE + 10}Z`, class: "a-flux-now" });
}

// Bracket under the ruler that spans the days of the selected period.
function bracketMark(columns, first, last) {
  const y = PLOT_BASE + 31;
  const from = fixed(columns.left(first) + 1), to = fixed(columns.left(last) + columns.step - 1);
  return createSVGEl("path", { d: `M${from} ${y - 5}V${y}H${to}V${y - 5}`, class: "a-flux-bracket" });
}

function hitMark(entry, index, columns, height, bytes) {
  return createSVGEl("rect", {
    x: fixed(columns.left(index) + 1), y: 1, width: fixed(Math.max(1, columns.step - 2)), height: height - 2,
    class: "a-flux-hit", role: "button", tabindex: -1, "data-day": index, "aria-pressed": "false", "aria-label": dayLabel(entry, bytes),
  });
}

// Daily totals of the monitored ports with the day ruler below. Every
// elapsed day gets one focusable area, so no value depends on a pointer.
function drawPlot(view, width, bytes) {
  const days = view.grid.days, height = PLOT_BASE + RULER_HEIGHT;
  const columns = dayColumns(days.length, width);
  const scale = niceTrafficScaleMax(view.grid.dayPeak);
  const bar = barWidth(columns.step);
  const svg = createSVGEl("svg", {
    class: "a-flux-svg", viewBox: `0 0 ${width} ${height}`, height, preserveAspectRatio: "none",
    role: "group", "aria-label": "每日流量，实心为接收，空心为发送",
  });
  if (view.grid.dayPeak > 0) svg.append(...gridMarks(scale, width, bytes));
  svg.append(createSVGEl("line", { x1: 0, y1: PLOT_BASE, x2: width, y2: PLOT_BASE, class: "a-flux-base" }), ...rulerMarks(days, columns));
  const inside = [];
  days.forEach((entry, index) => {
    if (entry.future) return;
    const within = inRange(entry.day, view.range);
    if (within) inside.push(index);
    svg.append(...dayMarks(entry, columns.center(index) - bar / 2, bar, scale, within ? "is-in" : "is-out"));
  });
  const today = days.findIndex(entry => entry.day === view.today);
  if (today >= 0) svg.append(nowMark(columns, today));
  if (inside.length) svg.append(bracketMark(columns, inside[0], inside.at(-1)));
  svg.append(createSVGEl("rect", { x: 0, y: 0, width: fixed(columns.step), height, class: "a-flux-cursor", hidden: true }));
  days.forEach((entry, index) => { if (!entry.future) svg.append(hitMark(entry, index, columns, height, bytes)); });
  return svg;
}

// A node carries at most one tag: blocked, or a quota that is nearly used.
function laneTag(item) {
  const quota = item.quotaPercent;
  if (item.blocked) return keyed("tag", span("a-con-token bad", "已封禁"));
  if (quota == null || quota < 80) return null;
  return keyed("tag", span(quota >= 100 ? "a-con-token bad" : "a-con-token warn", `配额 ${Math.round(quota)}%`));
}

function quotaReadout(item, bytes) {
  if (item.quotaPercent == null) return readout("配额", "配额", BLANK, "未设置");
  const tone = item.quotaPercent >= 100 ? "bad" : item.quotaPercent >= 80 ? "warn" : "";
  const note = [segmentMeter(Math.min(100, item.quotaPercent), 24), `${bytes(item.limitedUsedBytes)} / ${bytes(item.limitBytes)}`];
  return readout("配额", "配额", [item.quotaPercent.toFixed(1), "%"], note, tone);
}

// Detail strip of an opened lane: quota, period amounts, the live port
// rate and where to go next.
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

function laneNode(ctx, item, context) {
  const meta = item.agent || { id: item.agentID, name: item.agentID, region_code: "" };
  const name = meta.name || item.agentID || "关联节点";
  const open = context.open === item.agentID;
  const toggle = node("button", { type: "button", className: "a-flux-lane-name", "aria-expanded": String(open), "data-lane": item.agentID, title: name },
    nodeRegionFlag(ctx, meta), span("a-flux-lane-text", name));
  const bar = keyed(`bar-${context.width}-${item.received}-${item.sent}-${context.scale}`, laneBar(item, context.scale, context.width));
  const row = el("div", "a-flux-lane-row",
    node("div", { className: "a-flux-lane-id", "data-refresh-key": "id" }, toggle, laneTag(item)),
    bar,
    node("strong", { className: "a-flux-lane-val", "data-refresh-key": "val" }, context.bytes(item.used)));
  return node("li", { className: "a-flux-lane", "data-refresh-key": item.agentID, "data-open": open ? "true" : null },
    row, open ? laneInspect(item, context.bytes, context.canNodes) : null);
}

// Fields that decide whether the lanes are rebuilt. Live rates belong to
// the opened lane only, so a quiet list is not rebuilt on every sample.
function laneSignature(item, open) {
  const quota = item.quotaPercent == null ? null : Math.round(item.quotaPercent);
  const live = open === item.agentID ? [item.receiveBPS, item.sendBPS, item.limitedUsedBytes, item.limitBytes] : null;
  return [
    item.agentID, item.used, item.received, item.sent, quota, item.blocked, item.portCount, item.hasCurrentUser, item.hasOtherUser,
    item.owners, item.agent?.name, item.agent?.region_code, item.agent?.labels?.region_code, live,
  ];
}

const readoutHead = (title, token) => node("div", { className: "a-flux-readout-head", "data-refresh-key": "head" },
  node("span", { className: "a-con-label", "data-refresh-key": "title" }, title),
  token ? node("span", { className: "a-con-token", "data-refresh-key": "token" }, token) : null);

// Figures of the day under the cursor.
function dayReadout(entry, pinned, bytes) {
  const head = readoutHead(`${shortDay(entry.day)} ${WEEKDAYS[weekdayOf(entry.day)]}`, pinned ? "已固定" : "");
  if (!entry.rows) return el("div", "a-flux-readout", head, readout("合计", "合计", BLANK, "未采集"));
  return el("div", "a-flux-readout", head,
    readout("合计", "合计", quantityParts(bytes(entry.total)), duplex(entry.received, entry.sent, bytes)));
}

// Figures of the selected period. A period without persisted rows shows no
// figures, so a missing collection never reads as a measured zero.
function rangeReadout(view, period, bytes) {
  const summary = summarizeDashboardTrafficRange(view.grid.days, view.range);
  const single = view.range.start === view.range.end;
  const token = single ? shortDay(view.range.start) : `${shortDay(view.range.start)} 至 ${shortDay(view.range.end)}`;
  const head = readoutHead(view.live ? periodLabel(period) : view.month, token);
  if (!summary.rows) return el("div", "a-flux-readout", head, readout("合计", "合计", BLANK, "未采集"));
  const rows = [];
  if (!view.live) rows.push(readout("合计", "合计", quantityParts(bytes(summary.total)), duplex(summary.received, summary.sent, bytes)));
  if (single) {
    rows.push(readout("接收", "接收", quantityParts(bytes(summary.received))), readout("发送", "发送", quantityParts(bytes(summary.sent))));
  } else {
    const dayCount = summary.collected < summary.elapsed ? `${summary.collected}/${summary.elapsed} 天` : `${summary.elapsed} 天`;
    rows.push(readout("日均", "日均", quantityParts(bytes(summary.total / summary.collected)), dayCount));
    rows.push(readout("峰值日", "峰值日", quantityParts(bytes(summary.peakBytes)), shortDay(summary.peakDay)));
  }
  return el("div", "a-flux-readout", head, rows);
}

const sameItems = (left, right) => left.length === right.length && left.every((item, index) => item === right[index]);
const rowsOf = (values) => values.flatMap(value => list(value?.days));

// The natural week can start in the previous month. Its figure then needs
// both months and is marked incomplete while the older one is unreadable.
function weekSource(sources) {
  const current = sources.at(-1), previous = sources[0];
  if (sources.length < 2 || !current.value) return current;
  if (previous.value) return { value: current.value, state: previous.state === "failed" ? "failed" : current.state };
  if (previous.state === "loading") return { value: null, state: "loading" };
  return { value: current.value, state: current.state, partial: true };
}

// A period without persisted rows was not collected: it shows no figure
// rather than a zero. A measured zero keeps its figure.
function periodCell(label, token, period, source, bytes) {
  const cell = { label, token, figure: BLANK, notes: NO_ITEMS, state: "ready" };
  if (!source.value) {
    cell.state = PERIOD_STATES[source.state] ? source.state : "loading";
    cell.notes = [{ text: PERIOD_STATES[cell.state], tone: cell.state === "loading" ? "" : "bad" }];
    return cell;
  }
  const notes = [];
  if (period.rows) {
    cell.figure = quantityParts(bytes(period.total));
    notes.push({ text: `↓ ${bytes(period.received)}`, tone: "" }, { text: `↑ ${bytes(period.sent)}`, tone: "" });
  } else if (!source.partial) {
    cell.state = "empty";
    notes.push({ text: "未采集", tone: "" });
  }
  if (source.partial) {
    cell.state = "partial";
    notes.push({ text: "不完整", tone: "warn" });
  } else if (source.state === "failed") {
    cell.state = "stale";
    notes.push({ text: "刷新失败", tone: "warn" });
  }
  cell.notes = notes;
  return cell;
}

// Period sums and the day grid are recomputed only when a reading is
// replaced or the UTC day rolls over, not on every live refresh.
function periodsOf(stage, sources, now) {
  const today = now.toISOString().slice(0, 10);
  const values = sources.map(item => item.value);
  const cache = stage.periodCache;
  if (cache && cache.today === today && sameItems(cache.values, values)) return cache.periods;
  stage.periodCache = { today, values, periods: summarizeDashboardTrafficPeriods(rowsOf(values), now) };
  return stage.periodCache.periods;
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

// A day without a persisted row was not collected and shows no amounts.
function detailRow(day, bytes) {
  if (!day.rows) return [day.day, "未采集", "—", "—", "—", "—"];
  return [day.day, bytes(day.received), bytes(day.sent), bytes(day.total), `${bytes(day.peakReceiveBPS)}/s`, `${bytes(day.peakSendBPS)}/s`];
}

function paintChannels(stage, sources, now) {
  const periods = periodsOf(stage, sources, now);
  const current = sources.at(-1), bytes = stage.bytes;
  const cells = [
    periodCell("今日", shortDay(periods.today.end), periods.today, current, bytes),
    periodCell("本周", `${shortDay(periods.week.start)} 起`, periods.week, weekSource(sources), bytes),
    periodCell("本月", periods.month.start.slice(0, 7), periods.month, current, bytes),
  ];
  stage.channels.forEach((channel, index) => {
    channel.paint(cells[index]);
    channel.input.checked = stage.view.live && channel.value === stage.period;
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

// One day of the plot stays in the tab order: the last focused day, else
// the pinned one, else today.
function syncRoving(stage) {
  const days = stage.view.grid.days;
  let anchor = stage.rove >= 0 ? stage.rove : stage.pinned;
  if (anchor < 0) anchor = days.findIndex(day => day.day === stage.view.today);
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
  stage.plotHost.replaceChildren(drawPlot(view, stage.width, stage.bytes));
  syncRoving(stage);
  if (focused >= 0) hitOf(stage, focused)?.focus({ preventScroll: true });
}

// Ranks the nodes of the selected period and draws one row per node. The
// list shows the first few nodes and unfolds on request.
function paintLanes(stage) {
  const view = stage.view;
  if (!view) return;
  const raw = stage.getPolicies();
  const policies = Array.isArray(raw) ? raw : list(raw?.policies);
  const agents = stage.getAgents();
  const ranked = rankDashboardTrafficView({
    rows: rowsOf(view.sources.map(item => item.value)), month: view.month, today: view.today, range: view.range,
    agents, policies, pageAgents: Array.isArray(raw) ? NO_ITEMS : list(raw?.agents),
    scope: stage.scopeSelect.value || "all", limit: stage.unfolded ? "all" : String(LANE_FOLD), orderIDs: stage.getOrderIDs(),
  });
  const known = policies.length > 0 || agents.length > 0;
  if (known && !ranked.hasCrossOwner && stage.scopeSelect.value !== "all") {
    stage.scopeSelect.value = "all";
    paintLanes(stage);
    return;
  }
  stage.scopeField.hidden = !ranked.hasCrossOwner;
  const ready = Boolean(view.sources.at(-1).value) && stage.width > 0;
  const items = ready ? ranked.items.slice(0, LANE_CAP) : NO_ITEMS;
  const stamp = JSON.stringify([stage.width, view.range, stage.unfolded, ranked.totalCount, ranked.maxUsed, items.map(item => laneSignature(item, stage.open))]);
  if (stamp === stage.laneStamp) return;
  stage.laneStamp = stamp;
  const context = { width: stage.width, open: stage.open, bytes: stage.bytes, canNodes: stage.canNodes, scale: ranked.maxUsed };
  patch(stage.laneList, node("ol", { className: "a-flux-lane-list", "aria-label": "节点流量排行" }, items.map(item => laneNode(stage.ctx, item, context))));
  stage.lanes.hidden = !items.length;
  const foldable = ranked.totalCount > LANE_FOLD;
  stage.fold.hidden = !foldable;
  stage.fold.textContent = stage.unfolded ? "收起" : `全部 ${ranked.totalCount} 个节点`;
  stage.fold.setAttribute("aria-expanded", String(stage.unfolded));
  const rest = ranked.items.length - items.length;
  stage.more.hidden = rest <= 0;
  stage.more.textContent = rest > 0 ? `另有 ${rest} 个节点` : "";
  stage.foot.hidden = !foldable && rest <= 0;
}

// Moves the day cursor of the plot and shows the figures of the day under
// it, or of the selected period while no day is active.
function applyCursor(stage) {
  const view = stage.view;
  if (!view || !stage.width) return;
  const active = stage.hover >= 0 ? stage.hover : stage.pinned;
  const mark = stage.plotHost.querySelector(".a-flux-cursor");
  if (mark && active < 0) mark.setAttribute("hidden", "true");
  if (mark && active >= 0) {
    mark.removeAttribute("hidden");
    mark.setAttribute("x", fixed(dayColumns(view.grid.days.length, stage.width).left(active)));
    mark.setAttribute("class", active === stage.pinned ? "a-flux-cursor is-pinned" : "a-flux-cursor");
  }
  const fresh = active >= 0 ? dayReadout(view.grid.days[active], active === stage.pinned, stage.bytes) : rangeReadout(view, stage.period, stage.bytes);
  patch(stage.readoutHost, fresh);
}

function paintStage(stage) {
  const now = new Date(), today = now.toISOString().slice(0, 10);
  const month = stage.getMonth(), live = month === today.slice(0, 7);
  const periodSources = dashboardTrafficPeriodMonths(now).map(stage.source);
  const sources = live ? periodSources : [stage.source(month)];
  const cached = gridOf(stage, sources.map(item => item.value), dashboardTrafficAxis(month, now), now);
  stage.view = { grid: cached.grid, revision: cached.revision, range: dashboardTrafficRange(stage.period, month, now), today, month, live, sources };
  paintChannels(stage, periodSources, now);
  paintHead(stage);
  paintPlot(stage);
  paintLanes(stage);
  applyCursor(stage);
}

// Day under a pointer position on the plot. The day columns span the
// drawing only, so the axis gutter around it selects nothing; days still to
// come are not selectable either.
function indexAt(stage, host, clientX) {
  const days = stage.view?.grid.days || NO_ITEMS;
  const frame = host.matches("svg") ? host : host.querySelector("svg");
  const box = frame?.getBoundingClientRect();
  if (!days.length || !box || box.width <= 0 || clientX < box.left || clientX >= box.right) return -1;
  const index = Math.min(days.length - 1, Math.floor((clientX - box.left) / box.width * days.length));
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

// A click anywhere on a row opens or closes its detail strip; the name is
// the control that carries the state for the keyboard.
function laneClick(stage, event) {
  const toggle = event.target.closest?.(".a-flux-lane-row")?.querySelector("[data-lane]");
  if (!toggle) return;
  stage.open = stage.open === toggle.dataset.lane ? "" : toggle.dataset.lane;
  paintLanes(stage);
}

function toggleFold(stage) {
  stage.unfolded = !stage.unfolded;
  stage.foldTouched = true;
  stage.ctx.preferences?.patch?.("dashboard", { traffic_rank_limit: stage.unfolded ? "all" : String(LANE_FOLD) });
  paintLanes(stage);
}

// The saved list size applies once, unless the list was toggled before the
// preferences arrived.
function restoreFold(stage) {
  const preferences = stage.ctx.preferences;
  if (!preferences?.ready) return;
  preferences.ready().then(() => {
    if (stage.foldTouched || !stage.ctx.current()) return;
    stage.unfolded = preferences.get("dashboard")?.traffic_rank_limit === "all";
    paintLanes(stage);
  }).catch(() => null);
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

function observeWidth(stage) {
  const apply = (value) => {
    const next = Math.round(value);
    if (next <= 0 || next === stage.width) return;
    stage.width = next;
    if (stage.view) paintStage(stage);
  };
  if (typeof ResizeObserver !== "function") { apply(stage.plotHost.clientWidth || 640); return; }
  const observer = new ResizeObserver(entries => apply(entries[0]?.contentRect?.width || 0));
  observer.observe(stage.plotHost);
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
  stage.fold.addEventListener("click", () => toggleFold(stage), { signal });
  stage.monthInput.addEventListener("change", () => changeMonth(stage), { signal });
  bindPreferenceFields(ctx, "dashboard", [stage.monthField], { restore: () => changeMonth(stage) });
  stage.scopeSelect.addEventListener("change", () => paintLanes(stage), { signal });
  bindPreferenceFields(ctx, "dashboard", [stage.scopeField], { restore: () => paintLanes(stage) });
  restoreFold(stage);
  signal.addEventListener("abort", () => stage.dialog?.close(), { once: true });
}

const DETAIL_COLUMNS = ["日期", "接收", "发送", "合计", "接收峰值", "发送峰值"];
const RANK_SCOPES = [["all", "全部用户"], ["current", "当前用户"]];

function buildStage(ctx, options) {
  const saved = ctx.state.data.dashboardPeriod;
  const stage = {
    ctx, ...options,
    period: PERIODS.some(entry => entry[0] === saved) ? saved : "month",
    canNodes: ctx.can("agents.read"),
    width: 0, hover: -1, pinned: -1, rove: -1, open: "", view: null, dialog: null, unfolded: false, foldTouched: false,
    plotStamp: "", laneStamp: "", detailStamp: "", periodCache: null, gridCache: null,
  };
  stage.channels = PERIODS.map(entry => createChannel(entry, value => selectPeriod(stage, value)));
  stage.plotHost = el("div", "a-flux-plot");
  stage.readoutHost = el("div", "a-flux-readout");
  stage.laneList = node("ol", { className: "a-flux-lane-list", "aria-label": "节点流量排行" });
  stage.note = node("span", { className: "a-con-token", role: "status", hidden: true });
  stage.voidKey = legendKey("is-void", "未采集");
  stage.voidKey.hidden = true;
  stage.fold = node("button", { type: "button", className: "a-con-jump", "aria-expanded": "false", hidden: true });
  stage.more = node("a", { className: "a-con-jump", href: "#traffic", hidden: true });
  stage.foot = node("div", { className: "a-flux-lanes-foot", hidden: true }, stage.fold, stage.more);
  stage.lanes = node("div", { className: "a-flux-lanes", hidden: true }, stage.laneList, stage.foot);
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
  const channels = node("div", { className: "a-flux-channels", role: "radiogroup", "aria-label": "统计周期" }, stage.channels.map(channel => channel.element));
  const band = el("div", "a-flux-band", channels, stage.plotHost, stage.readoutHost);
  stage.element = section("监控端口流量", el("div", "a-flux-body", band, stage.lanes), tools);
  stage.element.classList.add("a-flux");
  return stage;
}

// Stage of the persisted traffic of the monitored ports: period channels,
// daily bars on a UTC day ruler and the node ranking of the period.
export function createTrafficStage(ctx, options) {
  const stage = buildStage(ctx, options);
  bindStage(stage);
  observeWidth(stage);
  return { element: stage.element, paint: () => paintStage(stage) };
}
