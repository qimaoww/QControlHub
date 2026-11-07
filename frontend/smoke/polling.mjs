import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

import { mergeVisibleTrafficCardOrder, mergeTrafficPorts, orderTrafficItems } from "../modules/traffic-model.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const pollingDocument = globalThis.document;
globalThis.document = {
  hidden: false,
  activeElement: null,
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  // Log polling now runs against the native UI in browser/core-logs.mjs:
  // per-engine windows, bounded pagination, failure/recovery, unchanged DOM,
  // visibility, reading anchors, text selection and route disposal. Source
  // states and account/grant races run in its two focused browser suites.
  // Native traffic polling, readonly scope, applied endpoint refresh, stable
  // identities and quota reset run in browser/traffic-layout.mjs.
  assert.deepEqual(
    mergeTrafficPorts(
      [{ deployment_confirmed: true, is_current_user: true, id: "policy-a", agent_id: "alpha", engine: "xray", port: 443 }],
      [{ agent_id: "alpha", engine: "xray", port: 443, protocol: "tcp" }, { agent_id: "alpha", engine: "xray", port: 8443, protocol: "udp" }, { agent_id: "alpha", engine: "sing-box", port: 8443, protocol: "tcp" }],
    ).map((item) => item.kind),
    ["policy", "endpoint"],
    "configured ports merge with policies by the Agent-wide port identity",
  );
  assert.deepEqual(
    mergeTrafficPorts(
      [{ deployment_confirmed: true, is_current_user: true, id: "policy-hidden", agent_id: "alpha", engine: "xray", port: 443, monitoring_enabled: false }],
      [{ agent_id: "alpha", engine: "xray", port: 443, protocol: "tcp" }],
    ),
    [],
    "deleted monitoring stays hidden even while the configured endpoint is still discoverable",
  );
  assert.deepEqual(
    orderTrafficItems(
      [
        { policy: { agent_id: "alpha", port: 443 } },
        { endpoint: { agent_id: "beta", port: 8443 } },
        { policy: { agent_id: "gamma", port: 80 } },
      ],
      ["beta:8443", "alpha:443"],
    ).map((item) => (item.policy || item.endpoint).agent_id),
    ["beta", "alpha", "gamma"],
    "traffic card order appends newly discovered ports after persisted cards",
  );
  assert.deepEqual(
    mergeVisibleTrafficCardOrder(
      ["alpha:443", "beta:8443", "gamma:80"],
      ["gamma:80", "alpha:443"],
    ),
    ["gamma:80", "beta:8443", "alpha:443"],
    "filtered traffic reordering preserves hidden card positions",
  );

  let metricRequests = 0;
  const metricState = {
    route: "node-settings",
    navigationEpoch: 1,
    data: {},
  };
  const { pollAgentMetrics } = installAgents(
    new Proxy(
      {
        state: metricState,
        api: async (path) => {
          assert.equal(path, "/agents");
          metricRequests += 1;
          return ["alpha", "beta", "gamma"].map((id) => ({
            id,
            status: "online",
            metrics: {},
            runtime: {},
          }));
        },
        can: (capability) => capability === "metrics.read",
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  globalThis.document.hidden = true;
  await pollAgentMetrics();
  clearTimeout(metricState.agentPollTimer);
  assert.equal(metricRequests, 0, "hidden node page defers metrics requests");
  globalThis.document.hidden = false;
  await pollAgentMetrics();
  clearTimeout(metricState.agentPollTimer);
  assert.equal(metricRequests, 1, "three-node metrics patch uses one fleet request");
  assert.deepEqual(
    metricState.data.agents.map((agent) => agent.id),
    ["alpha", "beta", "gamma"],
    "metrics polling synchronizes the shared Agent runtime snapshot",
  );

  let rosterOnlyRequests = 0;
  const rosterOnlyState = {
    route: "node-settings",
    navigationEpoch: 1,
    data: {},
  };
  const { pollAgentMetrics: pollAgentRoster } = installAgents(
    new Proxy(
      {
        state: rosterOnlyState,
        api: async (path) => {
          assert.equal(path, "/agents");
          rosterOnlyRequests += 1;
          return [];
        },
        can: (capability) => capability === "agents.read",
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  await pollAgentRoster();
  clearTimeout(rosterOnlyState.agentPollTimer);
  assert.equal(
    rosterOnlyRequests,
    1,
    "Agent roster polling does not require metrics permission",
  );
} finally {
  if (pollingDocument === undefined) delete globalThis.document;
  else globalThis.document = pollingDocument;
}

}
