import assert from "node:assert/strict";
import {
  installTraffic, mergeTrafficPorts, orderTrafficItems, trafficCardIdentity,
} from "./modules/traffic.js";
import { nodeCardOrderKey } from "./modules/node-order.js";

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
  const agents = ["alpha", "beta", "gamma", "empty"].map(id => ({
    id, name: id, status: "online", features: ["port-traffic-v1"], capabilities: ["mihomo"],
  }));
  let policies = [policy];
  let endpoints = [];
  const state = { route: "traffic", navigationEpoch: 1, data: {} };
  const orders = new Map([[nodeCardOrderKey, "[]"]]);
  const cardOrderKey = "qcontrolhub:traffic-card-order";
  const cardKeys = () => [...markup.matchAll(/data-traffic-card-key="([^"]+)"/g)].map(match => match[1]);
  const render = installTraffic({
    state,
    can: () => true, esc: value => String(value ?? ""), engineName: String,
    bytes: String, rate: String, percent: () => 0, ago: () => "now",
    storage: {
      getItem: key => orders.get(key) ?? null,
      setItem: (key, value) => orders.set(key, value),
    },
    setTimer: () => 1, clearTimer() {}, notify() {}, confirmAction() { assert.fail("sync must use the selection dialog"); },
    shell: html => { markup = html; button = {}; },
    api: async path => {
      if (path === "/agents") return agents;
      if (path === "/traffic-policies") return policies;
      if (path === "/traffic-endpoints") return endpoints;
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

  policies = [
    { ...policy, id: "policy-b", agent_id: "beta" },
    policy,
    { ...policy, id: "policy-c", agent_id: "gamma" },
  ];
  endpoints = [
    { agent_id: "alpha", engine: "mihomo", port: 8443, protocol: "tcp" },
    { agent_id: "beta", engine: "mihomo", port: 8443, protocol: "tcp" },
    { agent_id: "missing", engine: "mihomo", port: 9443, protocol: "tcp" },
  ];
  await render();
  assert.deepEqual(cardKeys(), ["alpha:443", "alpha:8443", "beta:443", "beta:8443", "gamma:443", "missing:9443"],
    "without a saved node order, cards follow the API node list rather than the policy list");

  orders.set(nodeCardOrderKey, JSON.stringify(["gamma", "beta"]));
  await render();
  assert.deepEqual(cardKeys(), ["gamma:443", "beta:443", "beta:8443", "alpha:443", "alpha:8443", "missing:9443"],
    "traffic rendering uses the account's saved node order and appends new nodes");
  assert.equal(orders.has(cardOrderKey), false, "default rendering must not persist a custom card order");

  orders.set(nodeCardOrderKey, JSON.stringify(nodeOrder));
  await render({ background: true });
  assert.deepEqual(cardKeys(), ["beta:443", "beta:8443", "alpha:443", "alpha:8443", "gamma:443", "missing:9443"],
    "background refresh follows later changes to the node order");

  orders.set(cardOrderKey, JSON.stringify(["alpha:8443", "beta:443"]));
  await render();
  assert.deepEqual(cardKeys(), ["alpha:8443", "beta:443", "beta:8443", "alpha:443", "gamma:443", "missing:9443"],
    "saved card positions override the node order without changing the fallback for new cards");
  state.data.trafficFilters = { agent_id: "beta" };
  await render();
  assert.deepEqual(cardKeys(), ["beta:443", "beta:8443"], "filtering preserves card order within the selected node");
  assert.deepEqual(JSON.parse(orders.get(cardOrderKey)), ["alpha:8443", "beta:443"],
    "filtering must not overwrite the complete saved card order");

  state.data.trafficFilters = {};
  orders.set(cardOrderKey, "invalid JSON");
  await render();
  assert.deepEqual(cardKeys(), ["beta:443", "beta:8443", "alpha:443", "alpha:8443", "gamma:443", "missing:9443"],
    "invalid saved card data falls back to node order");
  assert.deepEqual(policies.map(item => item.agent_id), ["beta", "alpha", "gamma"], "sorting must not mutate API data");
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}
console.log("traffic selection and ordering module smoke passed");
