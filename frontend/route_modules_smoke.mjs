import assert from "node:assert/strict";
import { taskRetryState, taskTiming, coreSourceName, coreSourceLabel } from "./modules/task-model.js";

// Native pages perform work when called. Importing their modules must remain
// inert so route loading cannot start requests, read the DOM or mount a page.
const descriptors = new Map(["document", "window", "fetch", "localStorage"].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
const unexpected = () => assert.fail("native route import performed browser work");
try {
  for (const key of descriptors.keys()) Object.defineProperty(globalThis, key, { configurable: true, value: key === "fetch" ? unexpected : new Proxy({}, { get: unexpected }) });
  for (const [path, exports] of [
    ["operations", ["dashboardPage", "tasksPage", "quotaPage"]], ["settings", ["settingsPage"]],
    ["client-access", ["clientAccessPage"]], ["substore", ["substorePage"]],
    ["traffic", ["trafficPage"]], ["users", ["usersPage"]],
    ["nodes", ["nodesPage"]], ["live-config", ["liveConfigPage"]],
  ]) {
    const module = await import(`./analytics/${path}.js`);
    for (const name of exports) assert.equal(typeof module[name], "function", `${path}.${name} is the shipped page entry`);
  }
} finally {
  for (const [key, descriptor] of descriptors) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else delete globalThis[key];
  }
}
for (const [owner, helpers] of [
  ["dashboard-model", ["dashboardTrafficMonthDays", "aggregateDashboardTrafficDays"]],
  ["client-access-model", ["normalizeClientAccessFilters", "filterClientAccessEntries", "groupClientAccessEntries", "clientAccessAddressChoices", "clientAccessEntryForAddress"]],
  ["substore-model", ["filterSubStoreProfiles", "groupSubStoreProfiles", "subStoreSelectionPayload", "subStoreAddressChoices", "subStoreProfileNodeCount", "subStoreAddressModeLabel"]],
  ["traffic-model", ["trafficCardIdentity", "orderTrafficItems", "mergeVisibleTrafficCardOrder", "trafficRateForDisplay", "mergeTrafficPorts"]],
  ["user-model", ["userPermissions", "parseSharedPorts", "formatSharedPorts", "sharedPortsLabel", "selectedSharedEngines", "sharedLimitBytes", "sharedLimitGiB", "agentShareStatus", "mergeUserAllocation"]],
]) {
  const model = await import(`./modules/${owner}.js`);
  for (const name of helpers) assert.notEqual(model[name], undefined, `${owner}.${name} remains available to native pages`);
}
assert.equal(typeof coreSourceName, "function"); assert.equal(typeof coreSourceLabel, "function");
console.log("Native route imports remain inert and focused business APIs remain available");

// Keep the original historical-task cases while checking the shared state
// consumed by the native UI. Browser task-retry covers the actual controls,
// permission combinations, target/version payload and recovery guidance.
const taskFixture = { id: "historical-task", agent_id: "node", engine: "mihomo", action: "deploy", status: "canceled", config_id: "archive" };
for (const config_version of [undefined, null, 0]) {
  const state = taskRetryState({ ...taskFixture, config_version });
  assert.equal(state.missingConfigVersion, true);
  assert.equal(state.canRetry, false);
  assert.equal(state.canEditRetry, true);
  assert.match(state.diagnostic.advice, /缺少配置历史关联/);
}
const versioned = taskRetryState({ ...taskFixture, config_version: 3 });
assert.equal(versioned.canRetry, true); assert.equal(versioned.missingConfigVersion, false);
assert.equal(versioned.diagnostic, null);
const statusTask = taskRetryState({ ...taskFixture, action: "status", config_id: "" });
assert.equal(statusTask.canRetry, true); assert.equal(statusTask.canEditRetry, false);
assert.equal(statusTask.diagnostic, null);
const failedLegacy = taskRetryState({ ...taskFixture, status: "failed", error: "Caddy rollback failed" });
assert.match(failedLegacy.diagnostic.advice, /缺少配置历史关联/);
assert.match(failedLegacy.diagnostic.rollback, /回滚失败：需检查节点最终服务与路由状态/);
console.log("Native task retry state preserves omitted historical revisions and rollback diagnostics");

const started = '2026-11-07T01:00:00Z', finished = '2026-11-07T01:01:05Z';
assert.equal(taskTiming({status:'pending'}), '准备执行');
assert.equal(taskTiming({status:'running'}), '正在启动执行');
assert.equal(taskTiming({status:'running',started_at:'invalid'}), '正在启动执行');
assert.equal(taskTiming({status:'running',started_at:started},Date.parse(finished)), '已运行 1\u00a0分 5\u00a0秒');
assert.equal(taskTiming({status:'running',started_at:finished},Date.parse(started)), '已运行 0\u00a0秒');
assert.equal(taskTiming({status:'succeeded',started_at:started,finished_at:finished}), '执行 1\u00a0分 5\u00a0秒');
assert.equal(taskTiming({status:'failed',started_at:started,finished_at:'2026-11-07T01:00:05Z'}), '执行 5\u00a0秒');
assert.equal(taskTiming({status:'canceled',finished_at:finished}), '未开始执行');
for (const task of [{status:'failed'},{status:'succeeded',started_at:started},{status:'failed',started_at:'invalid',finished_at:finished}]) assert.equal(taskTiming(task),'耗时未知');
