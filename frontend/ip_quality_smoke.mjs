import assert from "node:assert/strict";
import { createIPQualityController } from "./modules/ip-quality-controller.js";
import { createIPQualityView } from "./modules/ip-quality-view.js";
import { createIPQualityArchiveView } from "./modules/ip-quality-archive-view.js";
import { createIPQualityReportView } from "./modules/ip-quality-report-view.js";
import { ipQualityToday, ipQualityLatest, validIPQualityDate, nextIPQualityDay, nextIPQualityMonth, ipQualityAvailableDates, ipQualityHistoryNeighbor, ipQualityValue, ipQualitySummary } from "./modules/ip-quality-model.js";
import { createIPQualityCalendarView } from "./modules/ip-quality-calendar-view.js";

assert.equal(validIPQualityDate("2026-02-30"), false);
assert.equal(validIPQualityDate("2026-2-03"), false);
assert.equal(nextIPQualityDay("2024-03-01", -1), "2024-02-29");
assert.equal(nextIPQualityDay("invalid", 1), "");
for (const item of [null, undefined, "", "null", "N/A", {}]) assert.equal(ipQualityValue(item), "未知");
assert.equal(ipQualityValue(0), "0");
assert.equal(ipQualityValue(false), "否");
assert.equal(ipQualityValue("0.47%"), "0.47%");
assert.deepEqual(ipQualitySummary([{ status: "succeeded" }, { status: "running" }, { status: "failed" }]),
  { total: 3, succeeded: 1, running: 1, failed: 1 });

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
const report = {
  Head: { IP: "203.0.113.1", Version: "fixture" },
  Info: { Organization: '<img src=x onerror="alert(1)">', ASN: "64500" }, Type: {},
  Score: { IPQS: "null", SCAMALYTICS: "0", ipapi: "0.47%" },
  Factor: { Proxy: { IPQS: false } }, Media: { Netflix: { Status: "Yes", Region: "JP", Type: "Native" } },
  Mail: { Port25: false, DNSBlacklist: { Total: null, Blacklisted: null } },
};
const agent = { id: "alpha", name: "Node A", can_manage: true, status: "online", features: ["ip-quality-v2"] };
const legacyAgent = { id: "beta", name: "Node B", can_manage: true, status: "offline", features: [] };
const fixtureDates = [ipQualityToday(), nextIPQualityDay(ipQualityToday(), -1), "2025-09-11", "2025-09-12", "2025-09-13", "2025-09-14", "2025-09-15", "2025-09-16"];
const history = (day, records = []) => ({ date: day, timezone: "UTC", dates: fixtureDates, records, schedules: [] });
let markup = "";
const viewState = { data: {} };
const render = createIPQualityView({ shell: (html) => { markup = html; }, state: viewState, esc, date: (value) => value || "—" });
const renderDay = (date, records = [], agents = [agent, legacyAgent], options = {}) =>
  render({ date, timezone: "UTC", history: history(date, records), agents, submitting: new Set(), editable: () => true, ...options });
const todayRecord = { task_id: "task-1", agent_id: "alpha", status: "succeeded", result: { reports: [report] } };
renderDay("2025-09-16", [todayRecord]);
assert.ok(markup.includes("&lt;img"));
assert.ok(!markup.includes("<img"));
assert.ok(markup.includes("<dd>未知</dd>"));
assert.ok(markup.includes("<dd>否</dd>"));
assert.ok(markup.includes("0.47%"));
assert.ok(!markup.includes("演示"));
assert.ok(!markup.includes("/ 100"));
assert.ok(!markup.includes('style="'));
assert.ok(markup.includes("xykt/IPQuality"));
assert.ok(!markup.includes("data-ip-quality-run"), "historical report has a run button");
assert.ok(!markup.includes("data-ip-quality-schedule"), "historical report has schedule controls");
assert.ok(!markup.includes("节点在线"), "historical report presents the current online state");
assert.ok(markup.includes("data-ip-quality-latest"), "history has no return-to-latest control");
// The page shows one node, and publishes the same list the sidebar renders.
assert.equal(markup.match(/data-ip-quality-panel=/g).length, 1, "the page rendered more than one node");
assert.ok(markup.includes('data-ip-quality-panel="alpha"'), "the recorded node is not the selected panel");
assert.equal(viewState.data.ipQualityAgent, "alpha", "selection was not published for the sidebar");
assert.deepEqual(viewState.data.ipQualityNodes.map((node) => node.id), ["alpha"], "history offered nodes without records");
assert.deepEqual(viewState.data.ipQualityNodes[0], { id: "alpha", name: "Node A", note: "已完成", dot: "ok" },
  "the sidebar projection lost its per-node status");
renderDay(ipQualityToday());
assert.ok(markup.includes("data-ip-quality-run"), "today lost its run button");
assert.ok(markup.includes("data-ip-quality-schedule"), "today lost its schedule controls");
assert.deepEqual(viewState.data.ipQualityNodes.map((node) => node.id), ["alpha", "beta"], "today hid a managed node");
assert.deepEqual(viewState.data.ipQualityNodes.map((node) => node.note), ["当天未检测", "需升级 Agent"],
  "the sidebar lost its per-node status");
// A remembered node that the day does not list falls back to the first visible
// one, so the sidebar highlight and the panel can never disagree.
viewState.data.ipQualityAgent = "beta";
renderDay("2025-09-16", [todayRecord]);
assert.equal(viewState.data.ipQualityAgent, "alpha", "history kept a node it does not list");
// A foreground read with no data yet must not drop the selection or blink the
// sidebar away: only a completed read may publish an empty node list.
viewState.data.ipQualityAgent = "beta";
render({ date: ipQualityToday(), timezone: "UTC", agents: [], submitting: new Set(), editable: () => true, loading: true });
assert.equal(viewState.data.ipQualityAgent, "beta", "a loading read dropped the selected node");
assert.deepEqual(viewState.data.ipQualityNodes.map((node) => node.id), ["alpha"], "a loading read cleared the sidebar");
assert.ok(markup.includes("正在读取检测记录…"));
render({ date: ipQualityToday(), timezone: "UTC", agents: [agent], submitting: new Set(), editable: () => true, loading: true });
assert.ok(markup.includes('data-ip-quality-panel="alpha"'), "nodes should appear before history finishes");
assert.ok(markup.includes("正在读取这台节点的检测记录…"));
assert.ok(markup.includes("读取中"));
assert.ok(markup.includes("<strong>—</strong>"), "missing history must not look like zero detections");
assert.ok(markup.includes('data-ip-quality-run="alpha" disabled'), "partial reads must not enable mutations");
assert.equal(viewState.data.ipQualityNodes[0].note, "正在读取记录");
render({ date: ipQualityToday(), timezone: "UTC", agents: [agent], submitting: new Set(), editable: () => true,
  readFailed: true, error: "无法读取" });
assert.ok(markup.includes("读取失败"));
assert.ok(!markup.includes("请稍候"));
assert.ok(!markup.includes('ip-quality-badge unknown"><i></i>未检测'), "failed read must not look like an empty result");
renderDay("2025-09-16", []);
assert.ok(markup.includes("当天没有检测记录"));
assert.ok(!markup.includes("data-ip-quality-panel"), "history rendered nodes without records");
assert.deepEqual(viewState.data.ipQualityNodes, [], "history listed nodes without records");
render({ date: "2025-09-16", timezone: "UTC", error: "无法读取", agents: [], submitting: new Set(), editable: () => false });
assert.ok(!markup.includes("203.0.113.1"), "failed reads must never create example nodes");
assert.ok(!markup.includes("data-ip-quality-run"));

const oldReport = { ...todayRecord, finished_at: "2026-09-20T06:05:00Z",
  archives: [{ family: 4, sha256: "abc", rendered_at: "2026-09-20T06:05:00Z" }] };
renderDay(ipQualityLatest, [oldReport]);
assert.ok(markup.includes('data-ip-quality-latest aria-pressed="true"'));
assert.ok(markup.includes('data-ip-quality-date data-selected-date=""'), "latest was presented as a specific day");
assert.ok(markup.includes("2026-09-20T06:05:00Z"), "latest did not show the actual report time");
assert.ok(markup.includes('data-ip-quality-run="alpha">立即检测'), "old report prevented a fresh check");
assert.deepEqual(viewState.data.ipQualityNodes.map((node) => node.id), ["alpha", "beta"]);
for (const status of ["pending", "running", "failed", "canceled"]) {
  renderDay(ipQualityLatest, [{ task_id: "new-task", agent_id: "alpha", status, last_successful: oldReport }]);
  assert.ok(markup.includes("上次成功检测"), `${status}: retained report was not labeled`);
  assert.ok(markup.includes("203.0.113.1"), `${status}: lost the successful report`);
  assert.ok(markup.includes('/ip-quality/task-1/archives/4'), `${status}: wrong archive task`);
  assert.ok(!markup.includes('/ip-quality/new-task/archives/4'), `${status}: report attributed to latest task`);
}
renderDay("2025-09-16", [{ task_id: "new-task", agent_id: "alpha", status: "failed", last_successful: oldReport }]);
assert.ok(!markup.includes("203.0.113.1"), "history showed a successful report from a different day");
renderDay(ipQualityLatest, []);
assert.ok(markup.includes("首次报告"));
assert.equal(viewState.data.ipQualityNodes[0].note, "尚未检测");

assert.deepEqual(ipQualityAvailableDates(["2024-02-29", "2024-02-29", "2024-02-30", "2024-01-01", "2999-01-01"]), ["2024-02-29", "2024-01-01"]);
assert.equal(nextIPQualityMonth("2024-01", -1), "2023-12");
assert.equal(nextIPQualityMonth("2024-12", 1), "2025-01");
assert.equal(nextIPQualityMonth("invalid", 1), "");
const sparseDates = ["2024-03-02", "2024-02-29", "2024-01-01"];
assert.equal(ipQualityHistoryNeighbor(sparseDates, ipQualityLatest, -1), "2024-03-02");
assert.equal(ipQualityHistoryNeighbor(sparseDates, "2024-03-02", -1), "2024-02-29");
assert.equal(ipQualityHistoryNeighbor(sparseDates, "2024-02-29", 1), "2024-03-02");
assert.equal(ipQualityHistoryNeighbor(sparseDates, "2024-01-01", -1), "");
const calendarMarkup = createIPQualityCalendarView({ esc })({ date: "2024-02-29", dates: sparseDates, month: "2024-02", timezone: "UTC" });
assert.match(calendarMarkup, /data-ip-quality-date-option="2024-02-29"[^>]*aria-pressed="true"[^>]*title="查看检测记录"/);
assert.match(calendarMarkup, /data-ip-quality-date-option="2024-02-28"[^>]* disabled/);
assert.ok(!calendarMarkup.includes("2024-02-30"), "calendar invented a day in a leap month");
assert.match(createIPQualityCalendarView({ esc })({ date: ipQualityLatest, dates: [], timezone: "UTC" }), /暂无检测记录/);

const renderReport = createIPQualityReportView({ esc });
const blacklistReport = {
  ...report,
  Mail: { ...report.Mail, DNSBlacklist: { Total: 439, Clean: 411, Marked: 28, Blacklisted: 0 } },
};
assert.ok(renderReport(blacklistReport).includes("<dt>干净</dt><dd>411</dd>"));
// Risk factors are evidence, not a menu: every provider value renders inline.
const factorMarkup = renderReport(report);
assert.ok(factorMarkup.includes('<section class="ip-quality-factor"><strong>代理</strong>'), "a risk factor is not rendered inline");
assert.ok(!factorMarkup.includes('class="ip-quality-factor"><summary'), "a risk factor is folded behind a disclosure");
assert.ok(factorMarkup.includes("<dt>IPQS</dt><dd>否</dd>"), "a risk factor lost its provider value");
const ipv6Report = { ...blacklistReport, Head: { ...report.Head, IP: "2001:db8::1" } };
const originalIPv6Report = JSON.stringify(ipv6Report);
const ipv6Markup = renderReport(ipv6Report);
assert.ok(ipv6Markup.includes("未检测（仅支持 IPv4）"));
assert.ok(!ipv6Markup.includes("<dt>干净</dt>"), "IPv4 blacklist counts were attributed to IPv6");
assert.ok(ipv6Markup.includes("&quot;Total&quot;: 439"), "source JSON lost the upstream values");
assert.equal(JSON.stringify(ipv6Report), originalIPv6Report, "rendering rewrote the source report");

const originalDocument = globalThis.document;
globalThis.document = { querySelector: () => null, querySelectorAll: () => [] };
try {
  const state = { data: {}, route: "ip-quality", navigationEpoch: 0 };
  const views = [], calls = [], notices = [], gates = new Map();
  const timers = new Map();
  let timerID = 0;
  let confirmation = true, failReads = false, mutations = 0, permitWrites = true;
  const ctx = {
    state, can: () => permitWrites, notify: (...args) => notices.push(args),
    confirmAction: async () => confirmation,
    setTimer: (callback) => { timers.set(++timerID, callback); return timerID; },
    clearTimer: (id) => { timers.delete(id); },
    api: async (path, options = {}) => {
      calls.push({ path, options });
      if (options.method) { mutations += 1; return { id: "queued" }; }
      if (path === "/agents") return [agent, { ...agent, id: "shared", can_manage: false }];
      if (failReads) throw new Error("record service unavailable");
      const day = new URL(path, "http://fixture.test").searchParams.get("date");
      if (gates.has(day)) return gates.get(day).promise;
      return history(day);
    },
  };
  const controller = createIPQualityController(ctx, (value) => views.push(value));
  assert.equal(calls.length, 0, "controller construction must remain inert");
  await controller.load({ overview: {} });
  assert.equal(state.data.ipQualityDate, ipQualityLatest, "page entry did not default to latest");
  assert.deepEqual(views.at(-1).agents.map((item) => item.id), ["alpha"]);
  assert.ok(calls.some((call) => call.path.includes("timezone=")));
  assert.equal(new URL(calls.find((call) => call.path.startsWith("/ip-quality?")).path, "http://fixture.test").searchParams.has("date"), false,
    "latest query was still limited to today");
  const count = calls.length;
  const scheduledTimer = timerID;
  await controller.load("");
  await controller.load("2026-02-30");
  await controller.load(nextIPQualityDay(ipQualityToday(), 1));
  await controller.load("2025-09-10");
  assert.equal(calls.length, count, "invalid/future/unavailable dates requested the API");
  assert.equal(state.data.ipQualityDate, ipQualityLatest, "invalid date replaced the active view");
  assert.equal(timers.size, 1, "invalid date stopped automatic refresh");
  assert.ok(timers.has(scheduledTimer), "invalid date replaced the pending poll");
  await timers.get(scheduledTimer)();
  assert.equal(calls.length, count + 1, "polling should refresh history without rereading agents");
  assert.equal(timers.size, 1, "polling duplicated its timer");

  await controller.load("2025-09-12");
  const latestDone = gate(null);
  const latestReturn = controller.load(ipQualityLatest);
  assert.equal(views.at(-1).date, ipQualityLatest, "latest cache was not restored");
  assert.ok(views.at(-1).history, "latest cache disappeared while reading");
  latestDone(history(""));
  await latestReturn;
  gates.delete(null);
  await controller.load(ipQualityToday());
  await controller.load("2025-09-12");
  const todayDone = gate(ipQualityToday());
  const cachedReturn = controller.load(ipQualityToday());
  assert.equal(views.at(-1).history?.date, ipQualityToday(), "returning to a seen day should show cached records while reading");
  assert.equal(views.at(-1).loading, true);
  todayDone(history(ipQualityToday()));
  await cachedReturn;
  gates.delete(ipQualityToday());

  const partialDone = gate("2025-09-11");
  const partialLoad = controller.load("2025-09-11");
  await Promise.resolve();
  assert.equal(views.at(-1).loading, true);
  assert.equal(views.at(-1).history, undefined);
  assert.deepEqual(views.at(-1).agents.map((item) => item.id), ["alpha"],
    "node list should appear while the database read is pending");
  partialDone(history("2025-09-11"));
  await partialLoad;
  gates.delete("2025-09-11");

  // Switching the selected node repaints from the loaded snapshot: the sidebar
  // only offers nodes the day already returned, so no read is issued.
  const readsBeforeSelect = calls.length, paintsBeforeSelect = views.length;
  assert.equal(controller.select("beta"), true, "selecting a node did not repaint");
  assert.equal(state.data.ipQualityAgent, "beta");
  assert.equal(calls.length, readsBeforeSelect, "selecting a node re-read the API");
  assert.equal(views.length, paintsBeforeSelect + 1, "selecting a node did not repaint exactly once");
  assert.equal(controller.select("beta"), false, "re-selecting the same node repainted again");
  assert.equal(views.length, paintsBeforeSelect + 1, "re-selecting the same node repainted again");

  function gate(day) {
    let resolve;
    const promise = new Promise((done) => { resolve = done; });
    gates.set(day, { promise, resolve });
    return resolve;
  }
  const firstDone = gate("2025-09-14"), secondDone = gate("2025-09-15");
  const first = controller.load("2025-09-14", { background: true });
  const second = controller.load("2025-09-15", { background: true });
  secondDone(history("2025-09-15"));
  await second;
  const beforeStale = views.length;
  firstDone(history("2025-09-14"));
  await first;
  assert.equal(views.length, beforeStale, "old date response repainted the new date");
  assert.equal(views.at(-1).date, "2025-09-15");

  const accountDone = gate("2025-09-13");
  const oldAccount = controller.load("2025-09-13", { background: true });
  state.data = {};
  accountDone(history("2025-09-13"));
  await oldAccount;
  assert.equal(views.length, beforeStale, "old account response leaked into a new session");
  await controller.load();
  confirmation = false;
  await controller.runCheck("alpha");
  assert.equal(mutations, 0, "canceled confirmation submitted a task");
  confirmation = true;
  await controller.runCheck("shared");
  assert.equal(mutations, 0, "share recipient submitted a host check");
  permitWrites = false;
  await controller.runCheck("alpha");
  assert.equal(mutations, 0, "read-only session submitted a task");
  permitWrites = true;
  let accept;
  confirmation = new Promise((resolve) => { accept = resolve; });
  const submitting = controller.runCheck("alpha");
  await controller.runCheck("alpha");
  accept(true);
  await submitting;
  assert.equal(mutations, 1, "double click submitted multiple checks");
  assert.equal(state.data.ipQualityDate, ipQualityLatest, "fresh check switched latest view to today");
  assert.equal(calls.find((call) => call.options.method === "POST").options.body, '{"agent_id":"alpha"}');
  confirmation = true;
  await controller.setSchedule("alpha", true);
  assert.equal(mutations, 2);
  assert.ok(calls.some((call) => call.path === "/ip-quality/schedules/alpha" && call.options.body === '{"enabled":true}'));
  const yesterday = nextIPQualityDay(ipQualityToday(), -1);
  await controller.load(yesterday);
  await controller.runCheck("alpha");
  await controller.setSchedule("alpha", true);
  await controller.setSchedule("alpha", false);
  assert.equal(mutations, 2, "historical date allowed writes through the controller");
  await controller.load(ipQualityToday());
  confirmation = new Promise((resolve) => { accept = resolve; });
  const changedDateConfirmation = controller.runCheck("alpha");
  await controller.load(yesterday);
  accept(true);
  await changedDateConfirmation;
  assert.equal(mutations, 2, "confirmation submitted after switching to history");
  await controller.load(ipQualityToday());
  confirmation = new Promise((resolve) => { accept = resolve; });
  const midnightConfirmation = controller.setSchedule("alpha", false);
  const OriginalDate = globalThis.Date;
  const tomorrow = nextIPQualityDay(ipQualityToday(), 1);
  try {
    globalThis.Date = class extends OriginalDate {
      constructor(...args) { super(...(args.length ? args : [tomorrow + "T12:00:00"])); }
    };
    accept(true);
    await midnightConfirmation;
    assert.equal(mutations, 2, "confirmation submitted after the selected day became history");
  } finally { globalThis.Date = OriginalDate; }
  confirmation = true;
  failReads = true;
  await controller.load();
  assert.equal(views.at(-1).readFailed, true);
  assert.equal(views.at(-1).error, "record service unavailable");
  await controller.runCheck("alpha");
  assert.equal(mutations, 2, "failed authorization refresh still allowed writes");
  failReads = false;
  await controller.load();
  confirmation = new Promise((resolve) => { accept = resolve; });
  const oldConfirmation = controller.runCheck("alpha");
  state.data = {};
  const noticeCount = notices.length, viewCount = views.length;
  accept(true);
  await oldConfirmation;
  assert.equal(mutations, 2, "old account confirmation submitted under the new account");
  assert.equal(notices.length, noticeCount);
  assert.equal(views.length, viewCount);
  await controller.load(ipQualityLatest);
  confirmation = new Promise((resolve) => { accept = resolve; });
  const latestMidnightConfirmation = controller.runCheck("alpha");
  try {
    globalThis.Date = class extends OriginalDate {
      constructor(...args) { super(...(args.length ? args : [tomorrow + "T12:00:00"])); }
    };
    accept(true);
    await latestMidnightConfirmation;
    assert.equal(mutations, 3, "latest view stopped accepting checks at midnight");
    assert.equal(state.data.ipQualityDate, ipQualityLatest, "midnight check replaced latest with a date");
  } finally { globalThis.Date = OriginalDate; }
} finally {
  if (originalDocument === undefined) delete globalThis.document;
  else globalThis.document = originalDocument;
}
console.log("IP quality model, rendering, request races and mutation guards passed");

const archiveMarkup = createIPQualityArchiveView({ esc, date: String })({ task_id: "task-1", archives: [
  { family: 4, rendered_at: "2026-09-19T00:00:00Z", sha256: "abc" },
] });
assert.ok(archiveMarkup.includes('<img src="/api/v1/ip-quality/task-1/archives/4"'));
assert.ok(archiveMarkup.includes('?download=1'));
assert.ok(archiveMarkup.includes("存档信息"));
assert.ok(!archiveMarkup.includes("Report.Check.Place"), "browser must never contact the upstream report host");
assert.ok(!archiveMarkup.includes("iframe") && !archiveMarkup.includes("<svg"), "SVG must remain an inert image");
