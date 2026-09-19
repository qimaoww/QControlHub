import assert from "node:assert/strict";
import { createIPQualityController } from "./modules/ip-quality-controller.js";
import { createIPQualityView } from "./modules/ip-quality-view.js";
import { createIPQualityArchiveView } from "./modules/ip-quality-archive-view.js";
import { createIPQualityReportView } from "./modules/ip-quality-report-view.js";
import { ipQualityToday, validIPQualityDate, nextIPQualityDay, ipQualityValue, ipQualitySummary } from "./modules/ip-quality-model.js";

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
const agent = { id: "alpha", name: "Node A", can_manage: true, status: "online", features: ["ip-quality-v1"] };
const history = (day, records = []) => ({ date: day, timezone: "UTC", records, schedules: [] });
let markup = "";
const render = createIPQualityView({ shell: (html) => { markup = html; }, esc, date: (value) => value || "—" });
render({ date: "2025-09-16", timezone: "UTC", history: history("2025-09-16", [{
  task_id: "task-1", agent_id: "alpha", status: "succeeded", result: { reports: [report] },
}]), agents: [agent], submitting: new Set(), editable: () => true });
assert.ok(markup.includes("&lt;img"));
assert.ok(!markup.includes("<img"));
assert.ok(markup.includes("<dd>未知</dd>"));
assert.ok(markup.includes("<dd>否</dd>"));
assert.ok(markup.includes("0.47%"));
assert.ok(!markup.includes("演示"));
assert.ok(!markup.includes("/ 100"));
assert.ok(!markup.includes('style="'));
assert.ok(markup.includes("DNS / WebRTC 泄漏未检测"));
render({ date: "2025-09-16", timezone: "UTC", error: "无法读取", agents: [], submitting: new Set(), editable: () => false });
assert.ok(!markup.includes("203.0.113.1"), "failed reads must never create example nodes");
assert.ok(!markup.includes("data-ip-quality-run"));

const renderReport = createIPQualityReportView({ esc });
const blacklistReport = {
  ...report,
  Mail: { ...report.Mail, DNSBlacklist: { Total: 439, Clean: 411, Marked: 28, Blacklisted: 0 } },
};
assert.ok(renderReport(blacklistReport).includes("<dt>干净</dt><dd>411</dd>"));
const ipv6Report = { ...blacklistReport, Head: { ...report.Head, IP: "2001:db8::1" } };
const originalIPv6Report = JSON.stringify(ipv6Report);
const ipv6Markup = renderReport(ipv6Report);
assert.ok(ipv6Markup.includes("未检测：当前上游仅查询 IPv4 DNS 黑名单"));
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
  assert.equal(state.data.ipQualityDate, ipQualityToday(), "route options became the date");
  assert.deepEqual(views.at(-1).agents.map((item) => item.id), ["alpha"]);
  assert.ok(calls.some((call) => call.path.includes("timezone=")));
  const count = calls.length;
  const scheduledTimer = timerID;
  await controller.load("");
  await controller.load("2026-02-30");
  await controller.load(nextIPQualityDay(ipQualityToday(), 1));
  assert.equal(calls.length, count, "invalid/future dates requested the API");
  assert.equal(state.data.ipQualityDate, ipQualityToday(), "invalid date replaced the active date");
  assert.equal(timers.size, 1, "invalid date stopped automatic refresh");
  assert.ok(timers.has(scheduledTimer), "invalid date replaced the pending poll");
  await timers.get(scheduledTimer)();
  assert.equal(calls.length, count + 2, "polling did not continue after an invalid date");
  assert.equal(timers.size, 1, "polling duplicated its timer");

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
  assert.equal(calls.find((call) => call.options.method === "POST").options.body, '{"agent_id":"alpha"}');
  confirmation = true;
  await controller.setSchedule("alpha", true);
  assert.equal(mutations, 2);
  assert.ok(calls.some((call) => call.path === "/ip-quality/schedules/alpha" && call.options.body === '{"enabled":true}'));
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
} finally {
  if (originalDocument === undefined) delete globalThis.document;
  else globalThis.document = originalDocument;
}
console.log("IP quality model, rendering, request races and mutation guards passed");

const archiveMarkup = createIPQualityArchiveView({ esc, date: String })({ task_id: "task-1", archives: [
  { family: 4, downloaded_at: "2026-09-19T00:00:00Z", sha256: "abc", source_url: "https://Report.Check.Place/IP/fixture.svg" },
] });
assert.ok(archiveMarkup.includes('<img src="/api/v1/ip-quality/task-1/archives/4"'));
assert.ok(archiveMarkup.includes('?download=1'));
assert.ok(!archiveMarkup.includes("Report.Check.Place"), "browser must never contact the upstream report host");
assert.ok(!archiveMarkup.includes("iframe") && !archiveMarkup.includes("<svg"), "SVG must remain an inert image");
