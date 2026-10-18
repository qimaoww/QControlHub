import assert from "node:assert/strict";
import { createSessionWatch } from "./modules/session-watch.js";

const previousDocument = globalThis.document;
globalThis.document = { hidden: false };
try {
  const access = { revision: 1, shares: [{ id: "share", agent_id: "node", status: "accepted", enabled: true, engines: ["xray"], ports: [443], used_bytes: 1 }] };
  const state = { session: { role: "user" }, data: { agentAccess: structuredClone(access) } };
  const timers = new Map(), calls = [];
  let changes = 0, response = access, wait;
  const watcher = createSessionWatch({
    state, onAccessChanged: () => { changes++; },
    setTimer: callback => { const id = {}; timers.set(id, callback); return id; },
    clearTimer: id => timers.delete(id),
    api: async path => { calls.push(path); if (path === "/auth/session") return {}; return wait ? wait : structuredClone(response); },
  });
  watcher.start(); watcher.start();
  assert.equal(timers.size, 1);
  await watcher.trigger();
  assert.deepEqual(calls, ["/auth/session", "/agent-access"]);
  assert.equal(changes, 0);
  response.shares[0].used_bytes++;
  await watcher.trigger();
  assert.equal(changes, 0, "usage counters must not invalidate an editor's authorization");
  response.shares[0].enabled = false;
  await watcher.trigger();
  assert.equal(changes, 1, "revocation must update an already-open page");
  let resolve;
  wait = new Promise(done => { resolve = done; });
  const pending = watcher.trigger();
  await new Promise(setImmediate);
  const nextData = {};
  state.session = { role: "user" }; state.data = nextData;
  resolve({ revision: 2, shares: [] });
  await pending;
  assert.equal(changes, 1);
  assert.deepEqual(nextData, {}, "a delayed access response cannot update another account");
  watcher.stop();
  assert.equal(timers.size, 0);
  state.session = null;
  await watcher.trigger();
  assert.equal(timers.size, 0);
} finally {
  globalThis.document = previousDocument;
}
console.log("Idle session revalidation, revocation, single timer and delayed account switching passed");
