import assert from "node:assert/strict";
import { createCoreLogCache } from "./modules/core-log-cache.js";
import { readCoreLogWindow } from "./modules/core-log-read.js";
import { reuseCoreLogEntries } from "./modules/core-log-projection.js";
import { coreLogSourceState, coreLogSourceStatus } from './modules/core-log-source-status.js';
import { coreLogStoragePolicy } from './modules/core-log-model.js';

// Actual theme rendering, preferences, authorization-gated staged reads and
// account/target races run in browser/core-log-{source-states,read-ownership}.
// Keep the shared cache, source semantics and window races independently
// executable in the module gate without loading a retired HTML renderer.
const now = Date.parse('2026-09-07T00:05:00Z');
const source = (engine, patch = {}) => ({ agent_id: 'alpha', name: 'ALPHA', instance_id: 'main', engine, username: '主实例', status: 'active', source: 'journal', last_log_at: '2026-09-07T00:03:00Z', ...patch });
const status = (sources, options = {}) => coreLogSourceStatus(sources, { now, ...options });
for (const days of [1, 3, 7, 14, 30]) assert.equal(coreLogStoragePolicy({ core_log_minimum_level: 'warning', core_log_retention_days: days }, true), `保存警告及以上 · 保留 ${days} 天`);
for (const days of [undefined, null, 0, -1, 1.5, 'unknown']) assert.doesNotMatch(coreLogStoragePolicy({ core_log_minimum_level: 'warning', core_log_retention_days: days }, true), /保留 \d+ 天/, 'unknown retention cannot be fabricated');
assert.equal(coreLogStoragePolicy({ core_log_minimum_level: 'warning', core_log_retention_days: 30 }, false), '保存策略不可见', 'cached settings cannot leak a revoked policy');
const healthy = source('xray'), failed = source('sing-box', { status: 'failed', error: 'collector-failed' });
assert.equal(status([healthy]).noticeTitle, '');
assert.equal(status([healthy]).meta, '来源 journal · 最近日志 2 分钟前');
assert.equal(status([healthy, failed]).noticeTitle, '部分来源采集失败');
assert.match(status([healthy, failed]).noticeDetail, /sing-box · 主实例 读取失败（collector-failed）/);
assert.match(status([healthy, failed]).emptyTitle, /采集正常/);
assert.equal(status([failed]).emptyTitle, '日志采集失败');
for (const [key, title, detail] of [
  ['not-configured', '未配置', /尚无托管配置，部署后自动采集/],
  ['waiting', '等待日志', /已就绪，等待首条日志/],
  ['pending', '等待日志', /采集状态尚未上报/],
  ['offline', '节点离线', /采集已暂停/],
  ['not-installed', '内核尚未安装', /未安装/],
]) {
  const current = source('ss-rust', { status: key });
  assert.equal(status([current]).emptyTitle, title);
  assert.match(status([current]).emptyDetail, detail);
  assert.doesNotMatch(status([current]).noticeTitle, /采集正常/, 'historical rows never establish current collection health');
}
for (const last_log_at of ['', 'not-a-date']) {
  const state = coreLogSourceState(source('xray', { last_log_at }), { now });
  assert.equal(state.key, 'ready'); assert.match(state.title, /采集已就绪，尚未收到日志/);
  assert.equal(state.lastAt, 0); assert.doesNotMatch(state.title, /NaN|undefined/);
}
const legacy = coreLogSourceState(source('xray', { source: '', last_log_at: '', core_log_source: 'file', core_log_last_at: '2026-09-05T00:05:00Z' }), { now });
assert.match(legacy.title, /最近日志 2 天前/); assert.equal(legacy.transport, '日志文件');
assert.equal(status([healthy], { filtered: true }).emptyTitle, '没有匹配日志');
assert.equal(status([source('xray', { status: 'waiting' })], { filtered: true }).emptyTitle, '等待日志', 'keywords cannot turn an undelivered source into a no-match result');
assert.equal(status([healthy, failed], { filtered: true }).emptyTitle, '没有匹配日志');
assert.match(status([healthy, failed], { filtered: true }).emptyDetail, /collector-failed/);
const unconfigured = source('sing-box', { status: 'not-configured' });
assert.match(status([healthy, unconfigured]).emptyDetail, /sing-box · 主实例 尚无托管配置/);
assert.doesNotMatch(status([healthy, source('ss-rust', { status: 'not-installed' })]).noticeTitle, /内核尚未安装/, 'an uninstalled sibling cannot describe a usable scope');
for (const instance_id of ['main', 'shr_0123456789abcdef']) for (const value of ['waiting', 'pending', 'not-configured', 'active']) {
  const retained = source('ss-rust', { instance_id, username: 'alice', status: value, last_log_at: '' });
  const scoped = status([source('xray', { status: 'not-installed' }), retained]);
  assert.doesNotMatch(scoped.emptyTitle, /内核尚未安装|采集正常/);
  assert.doesNotMatch(scoped.noticeTitle, /内核尚未安装|采集正常/);
}
const failures = ['xray', 'sing-box', 'ss-rust'].map(engine => source(engine, { status: 'failed', error: 'collector-failed' }));
assert.match(status(failures).noticeDetail, /另有 1 个来源读取失败/);
const sharedFailure = source('ss-rust', { instance_id: 'shr_0123456789abcdef', username: 'alice', status: 'failed', error: 'permission-denied' });
assert.match(status([sharedFailure]).noticeDetail, /ss-rust · alice · shr_0123456789abcdef 读取失败（permission-denied）/);

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

// Revocation in either racing request must finish immediately and cancel the
// other read, including when the full failure preceded a denied preview.
for (const status of [401, 403, 404]) for (const deniedStage of ["preview", "full", "preview-after-full-failure"]) {
  const preview = deferred(), full = deferred(), signals = [];
  const failure = Object.assign(new Error("access revoked"), { status });
  let shown = 0;
  const read = readCoreLogWindow((path, { signal }) => {
    signals.push(signal);
    return path.endsWith("limit=200") ? preview.promise : full.promise;
  }, new URLSearchParams("limit=1000"), {
    signal: new AbortController().signal, preview: true, engines: ["xray"], onPreview: () => shown++,
  });
  const rejected = assert.rejects(read, error => error === failure);
  if (deniedStage === "full") full.reject(failure);
  else {
    if (deniedStage === "preview-after-full-failure") { full.reject(Object.assign(new Error("gateway timeout"), { status: 504 })); await flush(); }
    preview.reject(failure);
  }
  await rejected;
  assert.ok(signals.every(signal => signal.aborted));
  assert.equal(shown, 0, "denial cannot publish an older authorized preview");
  preview.resolve([]); full.resolve([]);
}

console.log("Core log staged loading/cache smoke passed");
