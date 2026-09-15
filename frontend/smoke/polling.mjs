import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

import { installCoreLogs } from "../modules/core-logs.js";

import { installTraffic, mergeVisibleTrafficCardOrder, mergeTrafficPorts, orderTrafficItems, resetTrafficCreateForm } from "../modules/traffic.js";

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
  const coreTimers = new Map();
  let nextCoreTimer = 1;
  let coreRequests = 0;
  let coreRenders = 0;
  let coreMarkup = "";
  let coreFailure = false;
  let coreLogQuery = "";
  let coreEntries = [{ id: 1, agent_id: "alpha", engine: "mihomo", level: "info", message: "ready", logged_at: "now" }];
  let coreAgents = [{ id: "alpha", name: "Alpha" }];
  const coreState = {
    route: "core-logs",
    navigationEpoch: 1,
    data: {},
  };
  const renderCoreLogs = installCoreLogs({
    state: coreState,
    engines: ["mihomo", "xray", "sing-box", "ss-rust"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    api: async (path) => {
      coreRequests += 1;
      if (path.startsWith("/core-logs?") && coreFailure)
        throw new Error("temporary log failure");
      if (path.startsWith("/core-logs?")) {
        coreLogQuery = path;
        return coreEntries;
      }
      if (path === "/agents") return coreAgents;
      assert.fail(`unexpected core-log polling path ${path}`);
    },
    shell: (markup) => {
      coreMarkup = markup;
      coreRenders += 1;
    },
    setTimer: (callback) => {
      const id = nextCoreTimer++;
      coreTimers.set(id, callback);
      return id;
    },
    clearTimer: (id) => coreTimers.delete(id),
  });
  await renderCoreLogs();
  assert.equal(coreRequests, 2, "log refresh uses two parallel data requests");
  assert.equal(coreRenders, 1);
  assert.equal(coreTimers.size, 1, "log polling owns one timer");
  assert.equal(coreMarkup.includes('data-refresh-key="core-log-1"'), true);
  assert.equal(coreMarkup.includes('name="agent_id"'), false, "the sidebar is the only node filter");
  assert.equal(coreMarkup.includes("data-core-log-engine"), true, "engines render as immediate filter buttons");
  assert.equal(coreMarkup.includes("data-core-log-level"), true, "levels render as immediate filter buttons");
  assert.equal(coreMarkup.includes('type="submit"'), false, "core log filters do not require an apply action");
  assert.equal(coreMarkup.includes("core-log-columns"), true, "the light log table has explicit column headings");
  assert.equal(coreMarkup.includes('每内核上限<select name="limit">'), true, "the log count setting explicitly applies to each engine");
  const corePoll = [...coreTimers.values()][0];
  coreTimers.clear();
  await corePoll();
  assert.equal(coreRequests, 4, "one log poll issues one request per data source");
  assert.equal(coreRenders, 2);
  assert.equal(coreTimers.size, 1, "log polling reschedules exactly one timer");
  coreFailure = true;
  const failedCorePoll = [...coreTimers.values()][0];
  coreTimers.clear();
  await failedCorePoll();
  assert.equal(coreRenders, 3, "a log error preserves data and renders the failure status");
  assert.equal(coreTimers.size, 1, "a log error keeps recovery polling alive");
  coreFailure = false;
  const recoveredCorePoll = [...coreTimers.values()][0];
  coreTimers.clear();
  await recoveredCorePoll();
  assert.equal(coreRenders, 4, "log polling recovers without clearing state");
  assert.equal(coreTimers.size, 1);
  coreEntries = ["mihomo", "xray", "sing-box", "ss-rust"].flatMap((engine, engineIndex) =>
    Array.from({ length: 100 }, (_, index) => ({
      id: 400 - engineIndex * 100 - index, agent_id: "alpha", engine,
      level: "info", message: `${engine} log ${index}`, logged_at: "now",
    })),
  );
  coreState.data.coreLogFilters = { limit: 100 };
  await renderCoreLogs();
  assert.equal(coreState.data.coreLogs.length, 400, "all engines keep their own 100-entry allowance without a global frontend slice");
  assert.equal(coreLogQuery, "/core-logs?limit=100", "the selected value is passed unchanged as the API's per-engine cap");
  assert.equal(coreMarkup.includes('value="sing-box" data-core-log-engine aria-pressed="false"><span>sing-box</span><b>100</b>'), true, "engine badges report their independent loaded counts");
  assert.equal(coreMarkup.includes('显示 <strong>400</strong> 条结果'), true);
  coreState.data.coreLogFilters = { limit: 100, engine: "sing-box" };
  await renderCoreLogs();
  assert.equal(coreState.data.coreLogs.length, 100, "selecting a quiet engine still exposes its full allowance");
  assert.equal(coreState.data.coreLogs.every((entry) => entry.engine === "sing-box"), true);
  for (const limit of [1000, 2000]) {
    coreEntries = ["mihomo", "xray", "sing-box", "ss-rust"].flatMap((engine, engineIndex) =>
      Array.from({ length: limit }, (_, index) => ({
        id: engineIndex * limit + index + 1, agent_id: "alpha", engine,
        level: "info", message: `${engine} pressure ${index}`, logged_at: "now",
      })));
    coreState.data.coreLogFilters = { limit };
    await renderCoreLogs();
    assert.equal(coreLogQuery, `/core-logs?limit=${limit}`);
    assert.equal(coreState.data.coreLogs.length, 4 * limit);
    assert.equal((coreMarkup.match(/class="core-log-row /g) || []).length, 200,
      "large windows keep DOM work bounded without truncating the searchable dataset");
    assert.ok(coreMarkup.includes(`已加载 ${4 * limit} 条`));
    assert.ok(coreMarkup.includes('value="1000"') && coreMarkup.includes('value="2000"'));
  }
  coreEntries = [];
  coreAgents = [{ id: "alpha", name: "Alpha", status: "online", features: ["core-logs-v1", "core-log-status-v1"], runtime: { "sing-box": { core_log_status: "failed", core_log_error: "permission-denied" } } }];
  coreState.data.coreLogFilters = { agent_id: "alpha", engine: "sing-box" };
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("日志采集失败"), true, "collector failures are distinct from a genuinely empty stream");
  coreAgents = [{ id: "alpha", name: "Alpha", status: "online", features: [], runtime: {} }];
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("此节点不支持集中日志"), true, "legacy unsupported Agents have a distinct empty state");
  coreAgents = [{ id: "alpha", name: "Alpha", status: "online", features: ["core-logs-v1"], runtime: {} }];
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("日志状态能力不可用"), true, "legacy log-capable Agents do not claim an unverifiable healthy source");
  coreEntries = [{ id: "historical", agent_id: "alpha", engine: "sing-box", level: "info", message: "historical entry", logged_at: "2026-08-24T00:00:00Z" }];
  coreAgents = [{ id: "alpha", name: "Alpha", status: "online", features: ["core-logs-v1", "core-log-status-v1"], runtime: { "sing-box": { core_log_status: "failed", core_log_error: "collector-failed" } } }];
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("historical entry"), true, "historical results remain visible during a current failure");
  assert.equal(coreMarkup.includes("日志采集失败"), true, "current failure remains visible beside historical results");
  coreAgents = [{ id: "alpha", name: "Alpha", status: "online", features: ["core-logs-v1", "core-log-status-v1"], runtime: { xray: { core_log_status: "failed" }, "sing-box": { core_log_status: "active" } } }];
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("日志采集失败"), false, "another engine failure does not contaminate the selected engine status");
  coreEntries = [];
  coreAgents = [{ id: "alpha", name: "Alpha", status: "online", features: ["core-logs-v1", "core-log-status-v1"], runtime: { "sing-box": { installed: false } } }];
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("内核尚未安装"), true, "an uninstalled engine is distinct from collection failure and an empty active source");

  coreEntries = [];
  coreAgents = [{ id: "alpha", name: "Alpha", status: "offline", features: ["core-logs-v1", "core-log-status-v1"], runtime: { "sing-box": { installed: true, core_log_status: "active" } } }];
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("节点离线"), true, "persisted runtime state is not trusted after an Agent goes offline");
  assert.equal(coreMarkup.includes("当前来源工作正常"), false, "offline runtime state is not presented as current health");
  coreEntries = [{ id: "offline-history", agent_id: "alpha", engine: "sing-box", level: "info", message: "offline historical entry", logged_at: "2026-08-24T00:00:00Z" }];
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("offline historical entry"), true, "offline Agents retain historical log rows");
  assert.equal(coreMarkup.includes("节点离线"), true, "offline notice remains visible beside historical rows");

  coreEntries = [];
  coreAgents = [{ id: "alpha", name: "Alpha", status: "online", features: ["core-logs-v1", "core-log-status-v1"], runtime: { "sing-box": { installed: true, core_log_status: "active" } } }];
  coreState.data.coreLogFilters = {};
  await renderCoreLogs({ syncFilters: true });
  assert.equal(coreMarkup.includes("尚未收到符合当前筛选条件的运行记录"), true, "aggregate empty state remains neutral");
  assert.equal(coreMarkup.includes("当前来源工作正常"), false, "aggregate filters do not claim every source is healthy");

  let releaseStaleCoreLogs;
  const staleCoreLogs = new Promise((resolve) => { releaseStaleCoreLogs = resolve; });
  let staleCoreMarkup = "";
  const staleCoreState = {
    route: "core-logs",
    navigationEpoch: 7,
    data: { coreLogFilters: { agent_id: "alpha", engine: "sing-box" } },
  };
  const renderStaleCoreLogs = installCoreLogs({
    state: staleCoreState,
    engines: ["sing-box"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    api: async (path) => {
      if (path.startsWith("/core-logs?") && path.includes("agent_id=alpha")) return staleCoreLogs;
      if (path.startsWith("/core-logs?") && path.includes("agent_id=beta"))
        return [{ id: "newest", agent_id: "beta", engine: "sing-box", level: "info", message: "newest selected node", logged_at: "now" }];
      if (path === "/agents") return [
        { id: "alpha", name: "Alpha", status: "offline", features: ["core-logs-v1", "core-log-status-v1"], runtime: {} },
        { id: "beta", name: "Beta", status: "online", features: ["core-logs-v1", "core-log-status-v1"], runtime: { "sing-box": { core_log_status: "active" } } },
      ];
      assert.fail(`unexpected stale core-log path ${path}`);
    },
    shell: (markup) => { staleCoreMarkup = markup; },
    setTimer: () => 1,
    clearTimer: () => {},
  });
  const oldCoreRender = renderStaleCoreLogs({ syncFilters: true });
  staleCoreState.data.coreLogFilters = { agent_id: "beta", engine: "sing-box" };
  await renderStaleCoreLogs({ syncFilters: true });
  assert.equal(staleCoreMarkup.includes("newest selected node"), true, "new node selection renders immediately");
  releaseStaleCoreLogs([{ id: "stale", agent_id: "alpha", engine: "sing-box", level: "info", message: "stale offline node", logged_at: "old" }]);
  await oldCoreRender;
  assert.equal(staleCoreMarkup.includes("newest selected node"), true, "older node response cannot overwrite the new selection");
  assert.equal(staleCoreMarkup.includes("stale offline node"), false, "stale offline response remains discarded");

  coreEntries = [{ id: "historical", agent_id: "alpha", engine: "sing-box", level: "info", message: "historical entry", logged_at: "2026-08-24T00:00:00Z" }];
  let noAgentDataMarkup = "";
  const renderWithoutAgentData = installCoreLogs({
    state: { route: "core-logs", navigationEpoch: 1, data: { coreLogFilters: { agent_id: "alpha", engine: "sing-box" } } },
    engines: ["sing-box"],
    can: () => false,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    api: async (path) => path.startsWith("/core-logs?") ? coreEntries : assert.fail(`unexpected no-agent-data path ${path}`),
    shell: (markup) => { noAgentDataMarkup = markup; },
    setTimer: () => 1,
    clearTimer: () => {},
  });
  await renderWithoutAgentData();
  assert.equal(noAgentDataMarkup.includes("historical entry"), true, "log permission can retain historical results without agents.read");
  assert.equal(noAgentDataMarkup.includes("无法核验采集状态"), true, "missing agents.read data is not presented as a healthy source");

  let deniedMarkup = "";
  const renderDeniedCoreLogs = installCoreLogs({
    state: { route: "core-logs", navigationEpoch: 1, data: {} },
    engines: ["sing-box"],
    can: () => false,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    api: async () => {
      const error = new Error("forbidden");
      error.status = 403;
      throw error;
    },
    shell: (markup) => { deniedMarkup = markup; },
    setTimer: () => 1,
    clearTimer: () => {},
  });
  await renderDeniedCoreLogs();
  assert.equal(deniedMarkup.includes("无权查看内核日志"), true, "permission failures have a distinct initial state");

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
    can: () => false,
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
          { id: "policy-a", agent_id: "alpha", engine: "mihomo", name: "Primary", port: 443, protocol: "tcp", cycle: "monthly", cycle_anchor: "2026-01-01T00:00:00Z", used_bytes: 10, limit_bytes: 100, received_bytes: 6, sent_bytes: 4, receive_bps: 1, send_bps: 1, enforcement_available: true, last_reported_at: "now", auto_block: true, quota_enabled: true },
          { id: "policy-b", agent_id: "alpha", engine: "mihomo", name: "Existing UDP", port: 8443, protocol: "udp", cycle: "monthly", cycle_anchor: "2026-08-01T00:00:00Z", used_bytes: 20, limit_bytes: Number.MAX_SAFE_INTEGER, received_bytes: 12, sent_bytes: 8, receive_bps: 2, send_bps: 1, enforcement_available: true, last_reported_at: "now", auto_block: false, quota_enabled: false, discovered: true },
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
  assert.equal(trafficRequests, 5, "background traffic polling reuses configured ports instead of reparsing every saved configuration");
  assert.equal(trafficRenders, 2);
  assert.equal(trafficTimers.size, 1, "traffic polling reschedules one timer");

  assert.deepEqual(
    mergeTrafficPorts(
      [{ id: "policy-a", agent_id: "alpha", port: 443 }],
      [{ agent_id: "alpha", engine: "xray", port: 443, protocol: "tcp" }, { agent_id: "alpha", engine: "xray", port: 8443, protocol: "udp" }, { agent_id: "alpha", engine: "sing-box", port: 8443, protocol: "tcp" }],
    ).map((item) => item.kind),
    ["policy", "endpoint"],
    "configured ports merge with policies by the Agent-wide port identity",
  );
  assert.deepEqual(
    mergeTrafficPorts(
      [{ id: "policy-hidden", agent_id: "alpha", port: 443, monitoring_enabled: false }],
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
