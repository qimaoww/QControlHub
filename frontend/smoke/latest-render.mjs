import assert from "node:assert/strict";

import { createLatestRenderScheduler } from "../modules/refresh.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
let requestedRoute = "dashboard";
let releaseFirstRender;
const firstRenderGate = new Promise((resolve) => {
  releaseFirstRender = resolve;
});
const renderedRoutes = [];
let activeRenders = 0;
let maximumActiveRenders = 0;
let canceledRenders = 0;
const scheduleRender = createLatestRenderScheduler(
  async () => {
    const route = requestedRoute;
    renderedRoutes.push(route);
    activeRenders += 1;
    maximumActiveRenders = Math.max(maximumActiveRenders, activeRenders);
    if (renderedRoutes.length === 1) await firstRenderGate;
    activeRenders -= 1;
  },
  { cancelActive: () => (canceledRenders += 1) },
);
const firstRender = scheduleRender();
requestedRoute = "node-settings";
scheduleRender();
requestedRoute = "tasks";
scheduleRender();
releaseFirstRender();
await firstRender;
assert.deepEqual(
  renderedRoutes,
  ["dashboard", "tasks"],
  "in-flight navigation coalesces to one render of the latest route",
);
assert.equal(maximumActiveRenders, 1, "route renders never overlap");
assert.equal(canceledRenders, 2, "new navigation cancels the stale route work");

}
