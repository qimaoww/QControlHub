import assert from "node:assert/strict";
import { installCoreLogs } from "./modules/core-logs.js";
import { createCoreLogCache } from "./modules/core-log-cache.js";
import { createCoreLogView } from "./modules/core-log-view.js";
import { readCoreLogWindow } from "./modules/core-log-read.js";
import { reuseCoreLogEntries } from "./modules/core-log-projection.js";

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
  const policyState = { data: {} };
  let policyMarkup = "", canReadSettings = true;
  const policyView = createCoreLogView({
    state: policyState, engines: ["xray"],
    can: permission => permission !== "settings.read" || canReadSettings,
    esc: value => String(value ?? ""), engineName: String, date: String,
    shell: markup => { policyMarkup = markup; },
  });
  for (const days of [1, 3, 7, 14, 30]) {
    policyState.data.settings = { core_log_minimum_level: "warning", core_log_retention_days: days };
    policyView([], [], {});
    assert.match(policyMarkup, new RegExp(`保存警告及以上 · 保留 ${days} 天`),
      "the log page must use the account's actual retention period");
    assert.equal((policyMarkup.match(/保留 \d+ 天/g) || []).length, 1);
  }
  for (const days of [undefined, null, 0, -1, 1.5, "unknown"]) {
    policyState.data.settings.core_log_retention_days = days;
    policyView([], [], {});
    assert.doesNotMatch(policyMarkup, /保留 \d+ 天/, "unknown retention must not be fabricated");
  }
  policyState.data.settings = { core_log_minimum_level: "warning", core_log_retention_days: 30 };
  canReadSettings = false;
  policyView([], [], {});
  assert.match(policyMarkup, /保存策略不可见/);
  assert.doesNotMatch(policyMarkup, /保存警告及以上|保留 \d+ 天/,
    "cached settings must not leak a policy after read permission is revoked");

  const state = { route: "core-logs", navigationEpoch: 1, data: { coreLogFilters: { agent_id: "alpha", limit: 2000 } } };
  const sources = ["alpha", "beta", "gamma"].map(agent => ({agent_id: agent, name: agent.toUpperCase(), instance_id: "main", engine: "xray", owner_id: "self", username: "self", status: "active", features: ["core-logs-v1", "core-log-status-v1"], grant_revision: "1"}));
  const calls = [];
  let markup = "", paints = 0;
  let directory = deferred(), preview = deferred(), full = deferred();
  const render = installCoreLogs({
    state, engines: ["xray"], can: () => true, now: () => clock,
    esc: value => String(value ?? ""), engineName: String, date: String,
    shell: value => { markup = value; paints++; }, setTimer: () => 1, clearTimer: () => {},
    api: (path, {signal}) => {
      calls.push({path, signal});
      if (path === "/core-log-sources") return directory.promise;
      return new URLSearchParams(path.split("?")[1]).get("limit") === "200" ? preview.promise : full.promise;
    },
  });
  const first = render();
  assert.equal(render(), first);
  assert.equal(calls.length, 1, "authorization directory gates cached and fresh reads");
  assert.match(markup, /正在加载日志/);
  directory.resolve(sources); await flush();
  assert.equal(calls.length, 3, "preview and full windows start concurrently after grant validation");
  preview.resolve(entries("alpha", 200)); await flush();
  assert.equal(state.data.coreLogEntries.length, 200);
  full.resolve(entries("alpha", 2000)); await first;
  assert.equal(state.data.coreLogEntries.length, 2000);
  assert.equal((markup.match(/class="core-log-row /g) || []).length, 200);
  assert.match(markup, /日志节点/);
  assert.match(markup, /使用用户 \/ 实例/);
  assert.equal(state.data.coreLogAgents.length, 3);

  directory = deferred(); preview = deferred(); full = deferred();
  state.data.coreLogFilters = {agent_id: "beta", limit: 2000};
  const second = render({scopeChange: true});
  assert.doesNotMatch(markup, /alpha log/, "scope switches remove previous rows immediately");
  const staleSignal = calls.at(-1).signal;
  const staleDirectory = directory;
  directory = deferred();
  state.data.coreLogFilters = {agent_id: "alpha", limit: 2000};
  const returned = render({scopeChange: true});
  assert.ok(staleSignal.aborted);
  assert.doesNotMatch(markup, /alpha log/, "returning to a cached scope still requires current grants");
  directory.resolve(sources); await flush();
  full.resolve(entries("alpha", 1999)); await returned;
  staleDirectory.resolve(sources); await second;
  assert.equal(state.data.coreLogEntries.length, 1999);
  assert.doesNotMatch(markup, /beta log/);

  // A grant change discards every retained window, including same-node
  // recipient windows that a previous directory still authorized.
  directory = deferred(); preview = deferred(); full = deferred();
  const revocation = render({background: true});
  directory.resolve(sources.filter(source => source.agent_id !== "beta")); await flush();
  assert.equal(state.data.coreLogEntries.length, 0);
  full.resolve(entries("alpha", 1)); await revocation;
  assert.equal(state.data.coreLogCache.get(JSON.stringify(["beta",2000])), null);

  directory = deferred();
  const denied = render({background:true});
  directory.reject(Object.assign(new Error("revoked"), {status:403})); await denied; await flush();
  assert.equal(state.data.coreLogEntries.length, 0);
  assert.equal(state.data.coreLogCache, undefined);
  assert.match(markup, /无权查看内核日志/);

  state.data = {coreLogFilters: {agent_id:"alpha",limit:2000}};
  directory = deferred();
  const loggingOut = render();
  state.data = {};
  directory.resolve(sources); await loggingOut;
  assert.deepEqual(state.data, {}, "late authorization cannot populate another account");

} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}

{
  const preview = deferred(), full = deferred();
  const signals = [];
  let previews = 0;
  const read = readCoreLogWindow((path, { signal }) => {
    signals.push(signal);
    return path.endsWith("limit=200") ? preview.promise : full.promise;
  }, new URLSearchParams("agent_id=alpha&limit=2000"), {
    signal: new AbortController().signal, preview: true, engines: ["xray"],
    onPreview: () => previews++,
  });
  const snapshot = entries("alpha", 2000);
  full.resolve(snapshot);
  assert.equal(await read, snapshot, "a fast full read never waits for a slow preview");
  assert.equal(signals[0].aborted, true);
  preview.resolve(entries("alpha", 200));
  await flush();
  assert.equal(previews, 0, "late previews cannot replace a completed full window");
  assert.equal(reuseCoreLogEntries(snapshot, structuredClone(snapshot)), snapshot, "equal payloads reuse the window");
  const changed = structuredClone(snapshot);
  changed[0].message = "updated sanitizer output";
  const reused = reuseCoreLogEntries(snapshot, changed);
  assert.equal(reused[0].message, changed[0].message, "matching IDs cannot hide changed content");
  assert.equal(reused[1], snapshot[1], "other records retain their search/render memoization");
  const windows = createCoreLogCache();
  windows.set(JSON.stringify(["alpha", 2000]), snapshot);
  const smaller = windows.preview(JSON.stringify(["alpha", 1000]));
  assert.equal(smaller.complete, true);
  assert.equal(smaller.entries.length, 1000, "related windows respect the new per-engine cap");
  assert.equal(smaller.entries[0], snapshot[0], "cache slicing keeps newest-first order");
  assert.equal(windows.preview(JSON.stringify(["beta", 1000])), null, "related caches never cross nodes");

  const delayedPreview = deferred();
  let partial = 0;
  const failure = new Error("fast full failure");
  const failedRead = readCoreLogWindow(path => path.endsWith("limit=200")
    ? delayedPreview.promise : Promise.reject(failure), new URLSearchParams("limit=2000"), {
    signal: new AbortController().signal, preview: true, engines: ["xray"],
    onPreview: entries => { partial = entries.length; },
  });
  const rejected = assert.rejects(failedRead, failure);
  delayedPreview.resolve(entries("alpha", 200));
  await rejected;
  assert.equal(partial, 200, "a failed full read still permits a successful preview before reporting incompleteness");
}

console.log("Core log staged loading/cache smoke passed");
