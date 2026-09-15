import assert from "node:assert/strict";
import { installAgents } from "./modules/agents.js";
import { installConfigPages } from "./modules/configs.js";
import { createConfigDeployment } from "./modules/config-deployment.js";
import { createLiveConfigReader } from "./modules/live-config-reader.js";

const flush = () => new Promise(setImmediate);
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};

// Composition must remain inert until a route or user action invokes a
// controller. In particular, callback wiring must not need a mounted DOM.
const context = {
  state: { data: {}, session: null },
  can: () => false, esc: String, bytes: String,
  api: () => assert.fail("constructing a route installer must not make requests"),
};
for (const [pages, names] of [
  [installAgents(context), [
    "agents", "nodeSettings", "submitTask", "bindCodeEditors", "showCommand",
    "pollAgentMetrics", "updateAgentMetrics", "cancelAgentInteractions",
    "sharingHasUnsavedChanges", "compactPresetPage",
  ]],
  [installConfigPages(context), [
    "agentConfig", "liveConfig", "archiveConfigs", "capturePresetDrafts",
    "presetHasUnsavedChanges", "configHasUnsavedChanges",
  ]],
]) {
  assert.deepEqual(Object.keys(pages).sort(), names.sort());
  for (const method of Object.values(pages)) assert.equal(typeof method, "function");
}

{
  const state = { data: {} };
  const requests = [], invalidated = [], refreshed = [], notices = [];
  const deployment = createConfigDeployment({
    state, can: () => true, notify: message => notices.push(message),
    api: (path, options = {}) => {
      const request = { path, options, ...deferred() };
      requests.push(request);
      return request.promise;
    },
  }, {
    invalidateLiveSnapshot: (...selection) => invalidated.push(selection),
    maybeRerenderLiveConfig: (...selection) => refreshed.push(selection),
  });
  const track = id => {
    deployment.recordPendingDeploy(id, "node", "xray");
    const promise = deployment.monitorDeployTask(id, "node", "xray");
    return { promise, request: requests.at(-1) };
  };
  const old = track("old");
  assert.equal(deployment.monitorDeployTask("old", "node", "xray"), old.promise);
  const statusWaiter = deployment.waitForDeployTerminal("old");
  assert.equal(requests.length, 1, "status feedback and deployment recovery must share the task poller");
  assert.equal(old.request.path, "/tasks/old?view=status");
  assert.equal(old.request.options.method, "GET", "task monitoring must bypass render read caching");
  const next = track("next");
  old.request.resolve({ id: "old", status: "failed", error: "old failure" });
  await Promise.all([old.promise, statusWaiter]);
  assert.equal(state.data.pendingDeployTasks["node|xray"].taskId, "next");
  assert.deepEqual(notices, [], "a superseded failure must not overwrite newer feedback");
  next.request.resolve({ id: "next", status: "succeeded" });
  await next.promise;
  assert.equal(state.data.pendingDeployTasks["node|xray"], undefined);
  assert.deepEqual(invalidated, [["node", "xray"]]);
  assert.deepEqual(refreshed, [["node", "xray"]]);

  const earlierSuccess = track("earlier-success");
  const laterFailure = track("later-failure");
  earlierSuccess.request.resolve({ id: "earlier-success", status: "succeeded" });
  await earlierSuccess.promise;
  assert.equal(invalidated.length, 2, "every successful deploy changes the live baseline");
  assert.equal(state.data.pendingDeployTasks["node|xray"].taskId, "later-failure");
  laterFailure.request.resolve({ id: "later-failure", status: "failed", error: "failure" });
  await laterFailure.promise;
  assert.equal(state.data.pendingDeployTasks["node|xray"], undefined);
  assert.equal(invalidated.length, 2, "failed deployments must not discard the baseline");
  assert.equal(notices.length, 1);

  const previousAccount = track("same-task");
  state.data = {};
  const currentAccount = track("same-task");
  assert.notEqual(previousAccount.promise, currentAccount.promise, "task IDs must not share pollers across accounts");
  previousAccount.request.resolve({ id: "same-task", status: "succeeded" });
  await previousAccount.promise;
  assert.equal(invalidated.length, 2, "old-account completion must have no effects in the new account");
  assert.equal(state.data.pendingDeployTasks["node|xray"].taskId, "same-task");
  currentAccount.request.resolve({ id: "same-task", status: "succeeded" });
  await currentAccount.promise;
  assert.equal(invalidated.length, 3);
  assert.equal(state.data.pendingDeployTasks["node|xray"], undefined);

  deployment.recordPendingDeploy("restored", "node", "xray");
  const reconciliation = deployment.reconcilePendingDeploy("node", "xray");
  assert.equal(requests.at(-1).path, "/tasks/restored");
  requests.at(-1).resolve({ id: "restored", status: "succeeded" });
  await reconciliation;
  assert.equal(invalidated.length, 4);
  assert.equal(state.data.pendingDeployTasks["node|xray"], undefined);
}

{
  const state = { data: {} };
  const denied = () => assert.fail("missing task-read permission must prevent monitoring");
  const deployment = createConfigDeployment({
    state, can: () => false, api: denied, notify: denied,
  }, { invalidateLiveSnapshot: denied, maybeRerenderLiveConfig: denied });
  deployment.recordPendingDeploy("pending", "node", "xray");
  assert.equal(deployment.monitorDeployTask("pending", "node", "xray"), undefined);
  await deployment.reconcilePendingDeploy("node", "xray");
  assert.equal(state.data.pendingDeployTasks["node|xray"].taskId, "pending");
}

{
  const calls = [];
  let reads = 0;
  const finishedAt = "2026-09-15T04:00:00Z";
  const reader = createLiveConfigReader({
    state: { data: {} },
    api: async (path, options = {}) => {
      const body = options.body && JSON.parse(options.body);
      calls.push({ path, body });
      if (path === "/tasks")
        return { id: ++reads === 1 ? "cached" : "fresh", status: "succeeded", reused: reads === 1, finished_at: finishedAt };
      if (path === "/tasks/cached/config-snapshot")
        throw Object.assign(new Error("expired"), { status: 404 });
      assert.equal(path, "/tasks/fresh/config-snapshot");
      return { content: "{}\n" };
    },
  }, { onRead: () => assert.fail("preflight reads must not rerender the page") });
  const snapshot = await reader.requestCurrentConfigSnapshot({ id: "node" }, "xray", "read-managed-config", { preferCached: true });
  assert.equal(calls[0].body.prefer_cached, true);
  assert.equal(Object.hasOwn(calls[2].body, "prefer_cached"), false, "an expired snapshot must retry without the cache");
  assert.deepEqual(snapshot, { content: "{}\n", taskId: "fresh", cached: false, readAt: Date.parse(finishedAt) });

  let current = true, created = 0;
  const abandoned = createLiveConfigReader({
    state: { data: {} },
    api: async path => {
      if (path === "/tasks") return { id: String(++created), status: "succeeded" };
      current = false;
      throw Object.assign(new Error("expired after navigation"), { status: 404 });
    },
  }, { onRead: () => {} });
  assert.equal(await abandoned.requestCurrentConfigSnapshot({ id: "node" }, "xray", "read-managed-config", { isCurrent: () => current }), null);
  assert.equal(created, 1, "navigation must prevent a fresh retry task from being created");
}

{
  const state = {
    route: "live-config", navigationEpoch: 1,
    data: { liveAgent: "node", liveEngine: "xray", liveConfigSource: "managed" },
  };
  const pending = deferred();
  let reads = 0, renders = 0;
  const reader = createLiveConfigReader({
    state,
    api: async path => {
      if (path === "/tasks")
        return ++reads === 1 ? { id: "old-read", status: "running" } : { id: "new-read", status: "succeeded" };
      if (path === "/tasks/old-read") return pending.promise;
      assert.equal(path, "/tasks/new-read/config-snapshot");
      return { content: "{\"log\":{}}\n" };
    },
  }, { onRead: () => { renders++; } });
  const reading = reader.readCurrentConfig({ id: "node" }, "xray", "node|xray", "read-managed-config");
  await flush();
  assert.equal(state.data.liveSources["node|xray"].pendingTaskId, "old-read");
  reader.invalidateLiveSnapshot("node", "xray");
  assert.equal(state.data.staleReadTasks["node|xray"], "old-read");
  pending.resolve({ id: "old-read", status: "succeeded" });
  await reading;
  assert.equal(state.data.liveSources["node|xray"], undefined);
  assert.equal(renders, 0, "an invalidated read must not restore a stale editor");
  await reader.readCurrentConfig({ id: "node" }, "xray", "node|xray", "read-managed-config");
  assert.equal(state.data.staleReadTasks["node|xray"], undefined);
  assert.equal(state.data.liveSources["node|xray"].agentContent, "{\"log\":{}}\n");
  assert.equal(state.data.liveSources["node|xray"].content, state.data.liveSources["node|xray"].agentContent);
  assert.equal(renders, 1);
}

console.log("Extracted controller composition, deployment lifecycle and snapshot isolation smoke passed");
