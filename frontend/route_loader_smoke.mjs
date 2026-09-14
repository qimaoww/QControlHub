import assert from "node:assert/strict";

import { createRouteModuleLoader } from "./modules/route-loader.js";

const deferred = () => {
  let resolve;
  const promise = new Promise((accept) => {
    resolve = accept;
  });
  return { promise, resolve };
};

const dashboardGate = deferred();
let dashboardLoads = 0;
let retryLoads = 0;
const loader = createRouteModuleLoader({
  dashboard: async () => {
    dashboardLoads += 1;
    return dashboardGate.promise;
  },
  retry: async () => {
    retryLoads += 1;
    if (retryLoads === 1) throw new Error("temporary module failure");
    return { page: "recovered" };
  },
});

const firstDashboard = loader.load("dashboard");
const secondDashboard = loader.load("dashboard");
assert.equal(firstDashboard, secondDashboard, "concurrent route loads share one request");
assert.equal(loader.peek("dashboard"), undefined, "pending modules are not exposed as loaded");
await Promise.resolve();
assert.equal(dashboardLoads, 1, "one factory starts for concurrent navigation intent");

const dashboard = { page: "dashboard" };
dashboardGate.resolve(dashboard);
assert.equal(await firstDashboard, dashboard);
assert.equal(loader.peek("dashboard"), dashboard, "resolved modules are synchronously available");
assert.equal(await loader.load("dashboard"), dashboard, "resolved modules stay cached");
assert.equal(dashboardLoads, 1, "cached navigation does not reload its module");

assert.equal(await loader.preload("retry"), undefined, "intent preload absorbs transient failures");
assert.deepEqual(await loader.load("retry"), { page: "recovered" });
assert.equal(retryLoads, 2, "a failed preload does not poison later navigation");
await assert.rejects(loader.load("missing"), /unknown route module/);
