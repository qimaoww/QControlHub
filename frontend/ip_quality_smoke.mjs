import assert from "node:assert/strict";
import { createIPQualityController } from "./modules/ip-quality-controller.js";
import { ipQualityToday, ipQualityLatest, validIPQualityDate, nextIPQualityDay, nextIPQualityMonth, ipQualityAvailableDates, ipQualityHistoryNeighbor, ipQualityValue, ipQualitySummary, ipQualityReportRecord, ipQualityTiming, validateIPQualityHistory, ipQualityNodeList } from "./modules/ip-quality-model.js";

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
const history = (day, records = []) => ({ date: day, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC", dates: fixtureDates, records, schedules: [] });
const pendingHistory = history("", [{ task_id: "pending-check", agent_id: "alpha", status: "pending" }]);
assert.equal(validateIPQualityHistory(pendingHistory, "", pendingHistory.timezone), pendingHistory);
assert.throws(() => validateIPQualityHistory({ ...pendingHistory, records: [null] }, "", pendingHistory.timezone));
assert.throws(() => validateIPQualityHistory(pendingHistory, "2026-09-01", pendingHistory.timezone));
assert.throws(() => validateIPQualityHistory({ ...pendingHistory, records: [{ ...pendingHistory.records[0],
  last_successful: { task_id: "foreign-check", agent_id: "beta", status: "succeeded", result: { reports: [report] } },
}] }, "", pendingHistory.timezone), "another node's previous report must never replace the selected node's image");
const timingRecord = {status: "running", created_at: "2026-10-17T05:58:00Z", started_at: "2026-10-17T05:58:37Z"};
const timingNow = Date.parse("2026-10-17T06:00:00Z");
assert.equal(ipQualityTiming(timingRecord, timingNow), "已执行 1 分 23 秒");
assert.equal(ipQualityTiming({...timingRecord,status:"pending"},timingNow), "已排队 2 分 0 秒");
for (const status of ["succeeded","failed","canceled"]) {
  assert.equal(ipQualityTiming({...timingRecord,status,finished_at:"2026-10-17T06:00:00Z"},timingNow+60000), "执行耗时 1 分 23 秒");
}
assert.equal(ipQualityTiming({...timingRecord,started_at:"invalid"},timingNow), "");
assert.equal(ipQualityTiming({...timingRecord,started_at:"2026-10-18T00:00:00Z"},timingNow), "");
// Native DOM/report behavior is covered by browser/ip-quality*.mjs. Keep
// selection and fallback contracts against the shared production model here.
const todayRecord = { task_id: "task-1", agent_id: "alpha", status: "succeeded", result: { reports: [report] } };
const oldReport = { ...todayRecord, finished_at: "2026-09-20T06:05:00Z",
  archives: [{ family: 4, sha256: "abc", rendered_at: "2026-09-20T06:05:00Z" }] };
const historicalNodes = ipQualityNodeList([agent, legacyAgent], [todayRecord], false, "beta");
assert.equal(historicalNodes.selected.id, "alpha", "history retained an unlisted selection");
assert.deepEqual(historicalNodes.items, [{ id: "alpha", name: "Node A", note: "已完成", dot: "ok" }]);
const todayNodes = ipQualityNodeList([agent, legacyAgent], [], true, "beta");
assert.equal(todayNodes.selected.id, "beta");
assert.deepEqual(todayNodes.items.map(item => item.note), ["当天未检测", "需升级 Agent"]);
assert.equal(ipQualityNodeList([agent], [], true, "", "", true).items[0].note, "尚未检测");
assert.equal(ipQualityNodeList([agent], [], true, "", "正在读取记录").items[0].note, "正在读取记录");
assert.equal(ipQualityNodeList([agent], [], true, "", "读取失败").items[0].note, "读取失败");
assert.deepEqual(ipQualityNodeList([agent], [], false).items, []);
assert.equal(ipQualityNodeList([agent], [], false).selected, null);
for (const status of ["pending", "running", "failed", "canceled"]) {
  assert.equal(ipQualityReportRecord({ ...todayRecord, status, last_successful: oldReport }, ipQualityLatest, "UTC"), oldReport);
}
const sameDayReport = { ...oldReport, created_at: "2025-09-15T23:30:00Z" };
const repeatedCheck = { task_id: "repeated", agent_id: "alpha", status: "running", last_successful: sameDayReport };
assert.equal(ipQualityReportRecord(repeatedCheck, "2025-09-16", "Asia/Shanghai"), sameDayReport);
assert.equal(ipQualityReportRecord(repeatedCheck, "2025-09-16", "UTC"), null, "fallback crossed the requested local day");
assert.equal(ipQualityReportRecord({ ...repeatedCheck, agent_id: "beta" }, ipQualityLatest, "UTC"), null, "fallback crossed nodes");
assert.deepEqual(ipQualityAvailableDates(["2024-02-29", "2024-02-29", "2024-02-30", "2024-01-01", "2999-01-01"]), ["2024-02-29", "2024-01-01"]);
assert.equal(nextIPQualityMonth("2024-01", -1), "2023-12");
assert.equal(nextIPQualityMonth("2024-12", 1), "2025-01");
assert.equal(nextIPQualityMonth("invalid", 1), "");
const sparseDates = ["2024-03-02", "2024-02-29", "2024-01-01"];
assert.equal(ipQualityHistoryNeighbor(sparseDates, ipQualityLatest, -1), "2024-03-02");
assert.equal(ipQualityHistoryNeighbor(sparseDates, "2024-03-02", -1), "2024-02-29");
assert.equal(ipQualityHistoryNeighbor(sparseDates, "2024-02-29", 1), "2024-03-02");
assert.equal(ipQualityHistoryNeighbor(sparseDates, "2024-01-01", -1), "");
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
  assert.equal(calls.length, count + 2, "polling should refresh history and current node permissions");
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
const savedDocument = globalThis.document;
globalThis.document = { querySelector: () => null, querySelectorAll: () => [] };
try {
  const state = { data: {}, route: "ip-quality", navigationEpoch: 0 };
  const views = [];
  let releaseCreate, creates = 0, failAcceptedRead = false;
  const controller = createIPQualityController({state, can:()=>true, notify:()=>{}, confirmAction:async()=>true,
    setTimer:()=>0, clearTimer:()=>{}, api:async(path,options={})=> {
      if (options.method) { creates++; return new Promise((resolve,reject)=> {releaseCreate={resolve,reject};}); }
      if (failAcceptedRead && path !== "/agents") throw new Error("history temporarily unavailable");
      return path==="/agents" ? [agent] : history(null,[oldReport]);
    }},value=>views.push(value));
  await controller.load();
  const pending = controller.runCheck("alpha");
  await Promise.resolve(); await Promise.resolve();
  assert.equal(views.at(-1).creating.has("alpha"),true);
  await controller.runCheck("alpha");
  assert.equal(creates,1,"in-flight creation was submitted twice");
  releaseCreate.reject(new Error("任务创建失败（请求 ID：req_test）"));
  await pending;
  assert.equal(views.at(-1).submitting.has("alpha"),false);
  assert.equal(views.at(-1).creating.has("alpha"),false);
  assert.match(views.at(-1).creationErrors.get("alpha"),/req_test/);
  assert.equal(views.at(-1).history.records[0].task_id,oldReport.task_id);
  const retry = controller.runCheck("alpha");
  await Promise.resolve(); await Promise.resolve();
  assert.equal(views.at(-1).creationErrors.has("alpha"),false);
  failAcceptedRead = true;
  releaseCreate.resolve({id:"accepted",agent_id:"alpha",action:"ip-quality",status:"pending",created_at:new Date().toISOString()}); await retry;
  assert.equal(views.at(-1).creating.has("alpha"),false,"history read extended the creation phase");
  assert.equal(views.at(-1).history.records[0].task_id,"accepted","failed history read hid the accepted task");
  assert.equal(views.at(-1).history.records[0].last_successful.task_id,oldReport.task_id);
  failAcceptedRead = false;
  state.data={}; await controller.load();
  assert.equal(views.at(-1).creationErrors.size,0,"another account inherited creation errors");
} finally { globalThis.document=savedDocument; }
console.log("IP quality model, selection, request races and mutation guards passed");
