import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

import { installTraffic, resetTrafficCreateForm } from "../modules/traffic.js";
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
  const trafficTimers = new Map();
  let nextTrafficTimer = 1;
  let trafficRequests = 0;
  let trafficRenders = 0;
  let trafficMarkup = "";
  const trafficStorage = new Map([
    ["qcontrolhub:traffic-card-order", JSON.stringify(["alpha:8443", "alpha:443"])],
  ]);
  const trafficState = {
    route: "traffic",
    navigationEpoch: 1,
    anchor: "traffic",
    data: {},
  };
  const renderTraffic = installTraffic({
    state: trafficState,
    can: permission => permission === "agents.read",
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    bytes: (value) => `${value || 0} B`,
    rate: (value) => `${value || 0} B/s`,
    percent: (used, limit) => (limit ? Number(used || 0) * 100 / Number(limit) : 0),
    ago: () => "刚刚",
    storage: {
      getItem: (key) => trafficStorage.get(key) || null,
      setItem: (key, value) => trafficStorage.set(key, value),
    },
    api: async (path) => {
      trafficRequests += 1;
      if (path === "/agents")
        return [{ id: "alpha", name: "Alpha", status: "online", features: ["port-traffic-v1"], capabilities: ["mihomo"] }];
      if (path === "/traffic-policies")
        return [
          { deployment_confirmed: true, is_current_user: true, id: "policy-a", agent_id: "alpha", engine: "mihomo", name: "Primary", port: 443, protocol: "tcp", cycle: "monthly", cycle_anchor: "2026-01-01T00:00:00Z", used_bytes: 10, limit_bytes: 100, received_bytes: 6, sent_bytes: 4, receive_bps: 1, send_bps: 1, enforcement_available: true, last_reported_at: "now", auto_block: true, quota_enabled: true },
          { deployment_confirmed: true, is_current_user: true, id: "policy-b", agent_id: "alpha", engine: "mihomo", name: "Existing UDP", port: 8443, protocol: "udp", cycle: "monthly", cycle_anchor: "2026-08-01T00:00:00Z", used_bytes: 20, limit_bytes: Number.MAX_SAFE_INTEGER, received_bytes: 12, sent_bytes: 8, receive_bps: 2, send_bps: 1, enforcement_available: true, last_reported_at: "now", auto_block: false, quota_enabled: false, discovered: true },
        ];
      if (path === "/traffic-endpoints")
        return [
          { agent_id: "alpha", engine: "mihomo", name: "Primary from config", port: 443, protocol: "tcp", config_version: 2 },
          { agent_id: "alpha", engine: "mihomo", name: "Existing UDP", port: 8443, protocol: "udp", config_version: 2 },
        ];
      assert.fail(`unexpected traffic polling path ${path}`);
    },
    shell: (markup) => {
      trafficMarkup = markup;
      trafficRenders += 1;
    },
    setTimer: (callback) => {
      const id = nextTrafficTimer++;
      trafficTimers.set(id, callback);
      return id;
    },
    clearTimer: (id) => trafficTimers.delete(id),
  });
  await renderTraffic();
  assert.equal(trafficRequests, 3, "traffic refresh loads agents, live policies, and configured ports");
  assert.equal(trafficRenders, 1);
  assert.equal(trafficTimers.size, 1, "traffic polling owns one timer");
  assert.equal(
    trafficMarkup.includes('data-refresh-key="traffic-policy-policy-a"'),
    true,
  );
  assert.equal(trafficMarkup.includes('data-traffic-filter="agent_id"'), false, "traffic workspace does not duplicate the sidebar node selector");
  assert.equal(trafficMarkup.includes("traffic-history"), false, "monthly history is rendered only on the dashboard");
  assert.equal(trafficMarkup.includes("traffic-hero"), false, "traffic page starts directly with useful controls instead of a repeated title hero");
  assert.equal(trafficMarkup.includes("traffic-policy-grid"), true, "traffic policies render as compact cards");
  assert.equal(trafficMarkup.includes("traffic-card-grip"), true, "traffic cards expose the same drag grip as node cards");
  assert.equal(trafficMarkup.includes('data-traffic-card-key="alpha:8443"'), true, "traffic cards have stable node and port identities");
  assert.equal(trafficMarkup.indexOf("Existing UDP") < trafficMarkup.indexOf("Primary"), true, "persisted traffic card order is applied before rendering");
  assert.equal(trafficMarkup.includes("Existing UDP"), true, "discovered ports render as automatic traffic monitors");
  assert.equal(trafficMarkup.includes("Primary from config"), false, "a configured endpoint already covered by a policy is not duplicated");
  assert.equal(trafficMarkup.includes("持续统计"), true, "monitoring remains active without a quota");
  assert.equal(trafficMarkup.includes("traffic-edit-dialog"), false, "read-only roles do not receive quota mutation dialogs");
  const trafficPoll = [...trafficTimers.values()][0];
  trafficTimers.clear();
  await trafficPoll();
  assert.equal(trafficRequests, 6, "background traffic polling refreshes the applied port set after deployment");
  assert.equal(trafficRenders, 2);
  assert.equal(trafficTimers.size, 1, "traffic polling reschedules one timer");

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

  const createAgent = { value: "beta" };
  const createEngine = { innerHTML: "stale beta engines" };
  const createForm = {
    reset() { createAgent.value = "alpha"; },
    querySelector(selector) {
      if (selector === "[name=agent_id]") return createAgent;
      if (selector === "[name=engine]") return createEngine;
      return null;
    },
  };
  resetTrafficCreateForm(
    createForm,
    [{ id: "alpha", capabilities: ["mihomo"] }, { id: "beta", capabilities: ["xray"] }],
    (agent) => `${agent.id} engines`,
  );
  assert.equal(createAgent.value, "alpha", "general quota creation resets the prefilled Agent");
  assert.equal(createEngine.innerHTML, "alpha engines", "general quota creation also rebuilds the matching engine choices");

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
