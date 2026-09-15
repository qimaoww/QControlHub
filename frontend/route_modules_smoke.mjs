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

const boundaries = [
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
