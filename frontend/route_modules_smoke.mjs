import assert from "node:assert/strict";
import * as dashboard from "./modules/dashboard.js";
import * as settings from "./modules/settings.js";
import * as access from "./modules/access-control.js";
import * as tcp from "./modules/system-bbr.js";
import * as client from "./modules/client-access.js";
import * as substore from "./modules/substore-sync.js";
import * as tasks from "./modules/tasks.js";
import * as logs from "./modules/core-logs.js";
import * as traffic from "./modules/traffic.js";
import * as users from "./modules/users.js";
import * as quality from "./modules/ip-quality.js";
import { createTaskView } from "./modules/task-view.js";

const boundaries = [
  [quality, "installIPQuality"],
  [dashboard, "installDashboard", "dashboard-model", ["dashboardTrafficMonthDays", "aggregateDashboardTrafficDays"]],
  [settings, "installSettings"],
  [access, "installAccessControl"],
  [tcp, "installSystemBBR", "system-bbr-model", ["systemBBRFeature", "systemBBRActions", "validateTCPSelection", "systemBBRState"]],
  [client, "installClientAccess", "client-access-model", ["normalizeClientAccessFilters", "filterClientAccessEntries", "groupClientAccessEntries", "clientAccessAddressChoices", "clientAccessEntryForAddress"]],
  [substore, "installSubStoreSync", "substore-model", ["filterSubStoreProfiles", "groupSubStoreProfiles", "subStoreSelectionPayload", "subStoreAddressChoices", "subStoreProfileNodeCount", "subStoreAddressModeLabel"]],
  [tasks, "installTasks", "task-model", ["coreSourceName", "coreSourceLabel"]],
  [logs, "installCoreLogs", "core-log-model", ["filterCoreLogEntries", "coreLogFilterCounts"]],
  [traffic, "installTraffic", "traffic-model", ["trafficCardIdentity", "orderTrafficItems", "mergeVisibleTrafficCardOrder", "trafficRateForDisplay", "mergeTrafficPorts"]],
  [users, "installUsers", "user-model", ["userPermissions", "parseSharedPorts", "formatSharedPorts", "sharedPortsLabel", "selectedSharedEngines", "sharedLimitBytes", "sharedLimitGiB", "agentShareStatus", "mergeUserAllocation"]],
];

for (const [module, installer, owner, helpers] of boundaries) {
  const state = { route: "", data: {}, session: { role: "admin" } };
  const unexpected = () => assert.fail(`${installer} must not perform work before its collaborators are wired`);
  const ctx = new Proxy({
    state, engines: [], actions: [],
    api: unexpected, shell: unexpected, notify: unexpected, can: unexpected,
    setTimer: unexpected, clearTimer: unexpected,
  }, { get: (target, key) => target[key] ?? unexpected });
  const page = module[installer](ctx);
  if (installer === "installUsers") {
    assert.deepEqual(Object.keys(page).sort(), ["captureDraft", "closeInvitation", "hasUnsavedChanges", "myQuota", "users"]);
    assert.equal(page.hasUnsavedChanges(), false);
    page.captureDraft();
    page.closeInvitation();
  } else {
    assert.equal(typeof page, "function", `${installer} preserves its callable API`);
    if (installer === "installAccessControl") assert.equal(typeof page.open, "function");
  }
  if (owner) {
    const focused = await import(`./modules/${owner}.js`);
    for (const name of helpers) assert.equal(module[name], focused[name], `${installer}.${name} must re-export the focused owner`);
  }
}
assert.equal(client.copyClientValue, (await import("./modules/client-clipboard.js")).copyClientValue);
assert.equal(traffic.resetTrafficCreateForm, (await import("./modules/traffic-form-model.js")).resetTrafficCreateForm);
assert.equal(traffic.renderTrafficAccounting, (await import("./modules/traffic-accounting-view.js")).renderTrafficAccounting);
console.log("Remaining route factories are inert and preserve their public APIs");

// Historical task JSON omits config_version when the database value is NULL.
// It must not offer an ordinary retry which the server cannot authorize.
const taskFixture = { id: "historical-task", agent_id: "node", engine: "mihomo", action: "deploy", status: "canceled", config_id: "archive" };
const taskCards = (task, permissions = ["tasks.execute", "configs.write"]) => createTaskView({
  state: { data: {} }, actions: [], can: permission => permissions.includes(permission),
  esc: value => String(value ?? ""), statusName: String, engineName: String, short: String,
  date: () => "fixture date", ago: () => "fixture age", actionName: String, statusTone: () => "neutral",
}).renderTaskCards([task], [], new Set());
for (const config_version of [undefined, null, 0]) {
  const html = taskCards({ ...taskFixture, config_version });
  assert.match(html, /版本未知/);
  assert.match(html, /缺少配置版本/);
  assert.doesNotMatch(html, /data-retry=|vundefined|vnull|· v0/);
  assert.match(html, /data-edit-retry="historical-task"/);
}
const versioned = taskCards({ ...taskFixture, config_version: 3 });
assert.match(versioned, /重试配置 v3/);
assert.match(versioned, /data-retry-version="3"/);
assert.doesNotMatch(versioned, /缺少配置版本|版本未知/);
const statusTask = taskCards({ ...taskFixture, action: "status", config_id: "" });
assert.match(statusTask, /data-retry="historical-task"/);
assert.match(statusTask, /重试任务/);
assert.doesNotMatch(statusTask, /缺少配置版本|版本未知/);
assert.doesNotMatch(taskCards(taskFixture, []), /data-retry=|data-edit-retry=/);
console.log("Task retry rendering handles omitted historical revisions and operation permissions");
