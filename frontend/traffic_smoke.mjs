import assert from "node:assert/strict";
import { installTraffic, mergeTrafficPorts } from "./modules/traffic.js";

// Selection, cancellation, retry and navigation use the real DOM in the
// traffic-layout browser suite. Keep rendering/optional quota coverage here.
const previousDocument = globalThis.document;
try {
  let button = {};
  let markup = "";
  globalThis.document = {
    querySelector: selector => selector === "[data-traffic-sync]" ? button : null,
    querySelectorAll: () => [],
  };
  const policy = { id: "policy-a", agent_id: "alpha", name: "Monitor", engine: "mihomo", port: 443, protocol: "tcp", quota_enabled: false };
  const render = installTraffic({
    state: { route: "traffic", navigationEpoch: 1, data: {} },
    can: () => true, esc: value => String(value ?? ""), engineName: String,
    bytes: String, rate: String, percent: () => 0, ago: () => "now", storage: null,
    setTimer: () => 1, clearTimer() {}, notify() {}, confirmAction() { assert.fail("sync must use the selection dialog"); },
    shell: html => { markup = html; button = {}; },
    api: async path => {
      if (path === "/agents") return [{ id: "alpha", name: "Alpha", status: "online", features: ["port-traffic-v1"], capabilities: ["mihomo"] }];
      if (path === "/traffic-policies") return [policy];
      if (path === "/traffic-endpoints") return [];
      assert.fail(`render must be read-only: ${path}`);
    },
  });
  await render();
  const input = markup.match(/<input name="limit_gb"[^>]*>/)[0];
  assert.equal(input.includes("required"), false);
  assert.match(input, /min="0"/);
  assert.match(input, /value=""/);
  assert.equal(typeof button.onclick, "function");
  assert.equal(button.disabled, false);
  assert.deepEqual(mergeTrafficPorts([{...policy, monitoring_enabled: false}], [policy]), [], "deleted ports remain hidden until explicitly restored");
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}
console.log("traffic selection module smoke passed");
