import assert from "node:assert/strict";
import { installCoreLogs } from "./modules/core-logs.js";
import { createCoreLogCache } from "./modules/core-log-cache.js";

const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
const entries = (agent, count) => Array.from({ length: count }, (_, i) => ({
  id: count - i, agent_id: agent, engine: "xray", level: "info",
  message: `${agent} log ${i}`, logged_at: "2026-09-07T00:00:00Z",
}));
let clock = 1;
const cache = createCoreLogCache({ now: () => clock, maxScopes: 2, maxBytes: 3000, ttl: 100 });
cache.set("a", entries("a", 1));
cache.set("b", entries("b", 1));
assert.equal(cache.get("a")[0].agent_id, "a");
cache.set("c", entries("c", 1));
assert.equal(cache.get("b"), null, "LRU limits retained node scopes");
cache.set("huge", entries("huge", 100));
assert.equal(cache.get("huge"), null, "large snapshots cannot bypass the memory budget");
assert.ok(cache.get("a"));
clock += 100;
assert.equal(cache.get("a"), null, "reading a snapshot does not extend its freshness TTL");
const byteCache = createCoreLogCache({ maxScopes: 10, maxBytes: 1200 });
for (const key of ["a", "b", "c"]) byteCache.set(key, entries(key, 1));
assert.equal(byteCache.get("a"), null, "byte budget also evicts snapshots below the scope cap");

const previousDocument = globalThis.document;
globalThis.document = { querySelector: () => null, querySelectorAll: () => [] };
try {
  const state = { route: "core-logs", navigationEpoch: 1, data: { coreLogFilters: { agent_id: "alpha", limit: 2000 } } };
  const calls = [];
  let markup = "";
  let metadata = deferred();
  let preview = deferred();
  let full = deferred();
  const render = installCoreLogs({
    state, engines: ["xray"], can: () => true, now: () => clock,
    esc: (v) => String(v ?? ""), engineName: (v) => v, date: (v) => v,
    shell: (value) => { markup = value; }, setTimer: () => 1, clearTimer: () => {},
    api: (path, { signal }) => {
      calls.push({ path, signal });
      if (path === "/agents") return metadata.promise;
      const params = new URLSearchParams(path.split("?")[1]);
      return Number(params.get("limit")) === 200 ? preview.promise : full.promise;
    },
  });
  const first = render();
  assert.match(markup, /正在加载日志/, "cold selection is acknowledged synchronously");
  assert.equal(calls[1].path, "/core-logs?agent_id=alpha&limit=200");
  preview.resolve(entries("alpha", 200));
  await flush();
  assert.equal(state.data.coreLogEntries.length, 200, "preview paints without awaiting slow node metadata or the full window");
  assert.match(markup, /正在补齐所选窗口/);
  assert.equal(calls[2].path, "/core-logs?agent_id=alpha&limit=2000");
  full.resolve(entries("alpha", 2000));
  await first;
  assert.equal(state.data.coreLogEntries.length, 2000, "full requested window replaces the preview");
  assert.equal((markup.match(/class="core-log-row /g) || []).length, 200);
  assert.equal((markup.match(/aria-label="日志分页"/g) || []).length, 2);
  const streamStart = markup.indexOf('<section class="core-log-stream');
  const streamEnd = markup.indexOf('</section>', streamStart);
  assert.doesNotMatch(markup.slice(streamStart, streamEnd), /core-log-pagination/, "pager never separates column headings from rows");
  metadata.resolve([{ id: "alpha", name: "Alpha", status: "online", features: ["core-logs-v1", "core-log-status-v1"], runtime: {} }]);
  await flush();
  assert.match(markup, /<strong>Alpha<\/strong>/, "late metadata updates the node name independently");

  preview = deferred(); full = deferred();
  state.data.coreLogFilters = { agent_id: "beta", limit: 2000 };
  const second = render({ scopeChange: true });
  assert.doesNotMatch(markup, /alpha log/, "uncached node switch immediately removes the previous node's rows");
  assert.equal(calls.filter((call) => call.path === "/agents").length, 1, "quick sidebar switch reuses recent metadata");
  const staleSignal = calls.at(-1).signal;
  const stalePreview = preview;
  state.data.coreLogFilters = { agent_id: "alpha", limit: 2000 };
  const returned = render({ scopeChange: true });
  assert.equal(staleSignal.aborted, true, "superseded log requests are cancelled");
  assert.match(markup, /alpha log/, "cached node paints synchronously without waiting on the network");
  assert.match(markup, /已显示缓存，正在更新/);
  assert.equal(state.data.coreLogEntries.length, 2000);
  full.resolve(entries("alpha", 1999));
  await returned;
  stalePreview.resolve(entries("beta", 200));
  await second;
  assert.equal(state.data.coreLogEntries.length, 1999);
  assert.doesNotMatch(markup, /beta log/, "late node response never contaminates the selected node");

  preview = deferred(); full = deferred();
  state.data.coreLogFilters = { agent_id: "gamma", limit: 1000 };
  const partial = render({ scopeChange: true });
  preview.resolve(entries("gamma", 200));
  await flush();
  full.reject(new Error("full window failed"));
  await partial;
  assert.equal(state.data.coreLogEntries.length, 200);
  assert.match(markup, /当前仅显示部分日志/, "a preview is never falsely labeled as the complete window");
  assert.equal(state.data.coreLogCache.get(JSON.stringify(["gamma", 1000])), null, "partial windows are not cached as complete");

  preview = deferred();
  state.data.coreLogFilters = { agent_id: "quiet", limit: 2000 };
  const quiet = render({ scopeChange: true });
  const quietCalls = calls.length;
  preview.resolve(entries("quiet", 10));
  await quiet;
  assert.equal(calls.length, quietCalls, "quiet engines do not issue an unnecessary full-window query");

  full = deferred();
  state.data.coreLogFilters = { agent_id: "alpha", limit: 2000 };
  const denied = render({ scopeChange: true });
  full.reject(Object.assign(new Error("forbidden"), { status: 403 }));
  await denied;
  assert.match(markup, /无权查看内核日志/);
  assert.equal(state.data.coreLogEntries.length, 0, "revoked permissions clear displayed logs");
  assert.equal(state.data.coreLogCache, undefined, "revoked permissions discard all retained snapshots");

  preview = deferred(); metadata = deferred();
  state.data.coreLogFilters = { agent_id: "late", limit: 2000 };
  const left = render();
  state.navigationEpoch++;
  state.route = "dashboard";
  const beforeLeave = markup;
  preview.resolve(entries("late", 200));
  metadata.resolve([{ id: "late" }]);
  await left;
  await flush();
  assert.equal(markup, beforeLeave, "navigation suppresses both staged log renders and late metadata");

  state.route = "core-logs";
  preview = deferred(); full = deferred(); metadata = deferred();
  state.data.coreLogFilters = { agent_id: "logout", limit: 2000 };
  const loggingOut = render();
  preview.resolve(entries("logout", 200));
  await flush();
  const beforeLogout = markup;
  state.data = {};
  full.resolve(entries("logout", 2000));
  metadata.resolve([{ id: "logout" }]);
  await loggingOut;
  await flush();
  assert.equal(markup, beforeLogout, "logout discards pending full-window and metadata responses even without navigation");
  assert.deepEqual(state.data, {}, "a late response cannot restore another session's log cache");

  state.data = { coreLogFilters: { agent_id: "metadata-failed", limit: 2000 }, agents: [
    { id: "metadata-failed", name: "Metadata Failed", status: "online", features: ["core-logs-v1", "core-log-status-v1"], runtime: {} },
  ] };
  preview = deferred(); metadata = deferred();
  const missingMetadata = render();
  metadata.reject(new Error("node metadata unavailable"));
  preview.resolve(entries("metadata-failed", 10));
  await missingMetadata;
  assert.equal(state.data.coreLogEntries.length, 10, "failed node metadata never drops successful log results");
  assert.match(markup, /无法确认当前节点是否仍在采集日志/, "failed node status is not reported as healthy");

  // Expired/oversized snapshots must not turn periodic refresh into a new
  // cold preview and shrink the active window while a reader is paging.
  delete state.data.coreLogCache;
  full = deferred();
  const activeBefore = state.data.coreLogEntries;
  const refreshActive = render({ background: true });
  assert.match(calls.at(-1).path, /limit=2000$/);
  assert.equal(state.data.coreLogEntries, activeBefore);
  full.resolve(entries("metadata-failed", 2000));
  await refreshActive;
  assert.equal(state.data.coreLogEntries.length, 2000);
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}
console.log("Core log staged loading/cache smoke passed");
