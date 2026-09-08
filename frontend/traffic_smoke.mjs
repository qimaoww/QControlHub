import assert from "node:assert/strict";
import { installTraffic } from "./modules/traffic.js";

const previousDocument = globalThis.document;
try {
  let button = { disabled: false, textContent: "同步端口" };
  const form = { dataset: { trafficEditForm: "policy-a" }, addEventListener() {} };
  globalThis.document = {
    querySelector: (selector) => selector === "[data-traffic-sync]" ? button : null,
    querySelectorAll: (selector) => selector === "[data-traffic-edit-form]" ? [form] : [],
  };
  const state = { route: "traffic", navigationEpoch: 1, data: {} };
  let markup = "";
  let confirm;
  let finishSync;
  let syncCalls = 0;
  let readCalls = 0;
  const notifications = [];
  const render = installTraffic({
    state, can: () => true, esc: (value) => String(value ?? ""),
    engineName: (value) => value, bytes: String, rate: String,
    percent: () => 0, ago: () => "now", storage: null,
    setTimer: () => 1, clearTimer: () => {},
    confirmAction: () => new Promise((resolve) => { confirm = resolve; }),
    notify: (...args) => notifications.push(args),
    shell: (html) => { markup = html; button = { disabled: false }; },
    api: async (path, options) => {
      if (path === "/traffic-endpoints/sync") {
        assert.equal(options.method, "POST");
        syncCalls++;
        return new Promise((resolve, reject) => { finishSync = { resolve, reject }; });
      }
      readCalls++;
      if (path === "/agents") return [{ id: "alpha", name: "Alpha", status: "online", features: ["port-traffic-v1"], capabilities: ["mihomo"] }];
      if (path === "/traffic-policies") return [{ id: "policy-a", agent_id: "alpha", name: "Monitor", engine: "mihomo", port: 443, protocol: "tcp", quota_enabled: false }];
      if (path === "/traffic-endpoints") return [];
      assert.fail(`unexpected request ${path}`);
    },
  });
  await render();
  const quotaInput = markup.match(/<input name="limit_gb"[^>]*>/)[0];
  assert.equal(quotaInput.includes("required"), false);
  assert.match(quotaInput, /min="0"/);
  assert.match(quotaInput, /value=""/);

  // Browsers clear currentTarget after dispatch, before confirmation resolves.
  const event = { currentTarget: button };
  const pending = button.onclick(event);
  event.currentTarget = null;
  assert.equal(button.disabled, true);
  await button.onclick({ currentTarget: button });
  assert.equal(syncCalls, 0, "duplicate clicks cannot bypass confirmation");
  confirm(true);
  await Promise.resolve();
  assert.equal(syncCalls, 1, "confirmed sync survives currentTarget being cleared");
  await render();
  assert.equal(button.disabled, true, "polling preserves in-flight sync state");
  await button.onclick({ currentTarget: button });
  assert.equal(syncCalls, 1, "polling cannot enable a duplicate request");
  finishSync.resolve({});
  await pending;
  assert.equal(button.disabled, false);
  assert.equal(readCalls, 9, "successful sync reloads the traffic data");

  const cancelled = button.onclick({ currentTarget: button });
  confirm(false);
  await cancelled;
  assert.equal(button.disabled, false);
  assert.equal(syncCalls, 1, "cancel does not write");

  const failed = button.onclick({ currentTarget: button });
  confirm(true);
  await Promise.resolve();
  finishSync.reject(new Error("sync unavailable"));
  await failed;
  assert.deepEqual(notifications.at(-1), ["sync unavailable", "error"]);
  assert.equal(button.disabled, false, "failure restores the button");

  const navigated = button.onclick({ currentTarget: button });
  confirm(true);
  await Promise.resolve();
  state.route = "agents";
  state.navigationEpoch++;
  finishSync.resolve({});
  await navigated;
  assert.equal(readCalls, 9, "late completion does not reload after navigation");
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}
console.log("traffic sync module smoke passed");
