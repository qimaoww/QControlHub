import assert from "node:assert/strict";
import { mergeTrafficPorts, orderTrafficItems, trafficCardIdentity } from "./modules/traffic-model.js";

const mixedItems = Object.freeze([
  { policy: { agent_id: "alpha", port: 443 } },
  { endpoint: { agent_id: "beta", port: 8443 } },
  { policy: { agent_id: "alpha", port: 1080 } },
  { policy: { agent_id: "gamma", port: 80 } },
  { endpoint: { agent_id: "missing", port: 9000 } },
  { endpoint: { agent_id: "beta", port: 7443 } },
]);
const nodeOrder = ["beta", "alpha", "gamma"];
assert.deepEqual(
  orderTrafficItems(mixedItems, [], nodeOrder).map(trafficCardIdentity),
  ["beta:8443", "beta:7443", "alpha:443", "alpha:1080", "gamma:80", "missing:9000"],
  "default ordering groups policies and discovered ports by node, retaining each node's port order",
);
assert.deepEqual(
  orderTrafficItems(mixedItems, ["gamma:80", "alpha:1080", "retired:443"], nodeOrder).map(trafficCardIdentity),
  ["gamma:80", "alpha:1080", "beta:8443", "beta:7443", "alpha:443", "missing:9000"],
  "custom card positions take precedence, with new ports in node order and unknown nodes last",
);
assert.equal(orderTrafficItems(mixedItems), mixedItems, "no ordering information preserves the source order");

// Rendering, quota fields and account scope run against the native page in
// browser/traffic-layout.mjs. Keep the shared model's deployment contracts.
const policy = { id: "policy-a", agent_id: "alpha", engine: "mihomo", port: 443, deployment_confirmed: true };
assert.deepEqual(mergeTrafficPorts([{ ...policy, monitoring_enabled: false }], [policy]), [], "deleted monitoring stays hidden until explicitly restored");
assert.deepEqual(mergeTrafficPorts([{ ...policy, deployment_confirmed: false }], []), [], "a policy alone never proves deployment");
assert.deepEqual(mergeTrafficPorts([policy], []), [], "a stale policy cannot outlive its confirmed endpoint");
assert.deepEqual(mergeTrafficPorts([{ ...policy, config_id: "old", config_version: 1 }], [{ ...policy, config_id: "new", config_version: 1 }]).map(item => item.kind), ["endpoint"], "a different instance never inherits an old policy");
assert.deepEqual(mixedItems.map(item => (item.policy || item.endpoint).agent_id), ["alpha", "beta", "alpha", "gamma", "missing", "beta"], "sorting must not mutate API data");
console.log("traffic deployment and ordering model smoke passed");
