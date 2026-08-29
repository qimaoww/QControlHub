import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import "./refresh_smoke.mjs";

import {
  agentStructureSignature,
  animateNodeCardDrop,
  batchAgentEligibility,
  batchSelectAllState,
  clearNodeCardDragState,
  coreSourceForInstall,
  developmentSourceVisible,
  formatHostPort,
  installAgents,
  manualConnectionAddressNote,
  nodeCardDropIndex,
  publicAddressRows,
  updatePublicIPDisplays,
} from "./modules/agents.js";
import { coreSourceLabel, coreSourceName } from "./modules/tasks.js";
import {
  copyClientValue,
  clientAccessAddressChoices,
  clientAccessEntryForAddress,
  filterClientAccessEntries,
  groupClientAccessEntries,
  installClientAccess,
  normalizeClientAccessFilters,
} from "./modules/client-access.js";
import { ConfigFormatError, formatConfigContent } from "./modules/code-format.js";
import {
  bindServerPlanRegeneration,
  installConfigPages,
  liveConfigEngineEligible,
  liveConfigEditorState,
  liveConfigReadAction,
  readServerPlanInput,
  submitLiveConfigChange,
} from "./modules/configs.js";
import {
  coreLogFilterCounts,
  filterCoreLogEntries,
  installCoreLogs,
} from "./modules/core-logs.js";
import {
  aggregateDashboardTrafficDays,
  dashboardTrafficMonthDays,
  installDashboard,
} from "./modules/dashboard.js";
import { installSettings } from "./modules/settings.js";
import {
  filterSubStoreProfiles,
  installSubStoreSync,
  subStoreAddressChoices,
  subStoreProfileNodeCount,
  subStoreSelectionPayload,
} from "./modules/substore-sync.js";
import { installTasks } from "./modules/tasks.js";
import {
  installTraffic,
  mergeVisibleTrafficCardOrder,
  mergeTrafficPorts,
  orderTrafficItems,
  resetTrafficCreateForm,
  trafficRateForDisplay,
} from "./modules/traffic.js";
import { createLatestRenderScheduler } from "./modules/refresh.js";

const state = { data: {}, session: { role: "admin" } };
const noop = () => {};

assert.equal(
  agentStructureSignature([
    { id: "beta" },
    { id: "alpha" },
  ]),
  agentStructureSignature([
    { id: "alpha" },
    { id: "beta" },
  ]),
  "Agent structure signatures ignore response ordering",
);
assert.notEqual(
  agentStructureSignature([{ id: "alpha", capabilities: ["mihomo"] }]),
  agentStructureSignature([
    { id: "alpha", capabilities: ["mihomo"] },
    { id: "new-node", capabilities: [] },
  ]),
  "Agent structure signatures detect newly enrolled nodes",
);

const trafficRateNow = Date.parse("2026-08-28T00:00:30Z");
assert.equal(
  trafficRateForDisplay(4096, "2026-08-28T00:00:15Z", "online", trafficRateNow),
  4096,
  "a fresh online traffic rate remains visible",
);
assert.equal(
  trafficRateForDisplay(4096, "2026-08-28T00:00:15Z", "offline", trafficRateNow),
  0,
  "an offline Agent never keeps a stale traffic rate visible",
);
assert.equal(
  trafficRateForDisplay(4096, "2026-08-27T23:59:00Z", "online", trafficRateNow),
  0,
  "an old traffic sample decays to zero",
);

const coreLogFilterFixture = [
  { engine: "mihomo", level: "debug", message: "bootstrap complete" },
  { engine: "xray", level: "info", message: "Accepted GitHub connection" },
  { engine: "sing-box", level: "warning", message: "slow handshake" },
  { engine: "ss-rust", level: "critical", message: "upstream timeout" },
];
assert.deepEqual(
  filterCoreLogEntries(coreLogFilterFixture, {
    engine: "xray",
    level: "info",
    q: "github",
  }),
  [coreLogFilterFixture[1]],
  "core log engine, level, and keyword filters compose locally",
);
assert.deepEqual(
  coreLogFilterCounts(coreLogFilterFixture, [
    "mihomo",
    "xray",
    "sing-box",
    "ss-rust",
  ]),
  {
    total: 4,
    engine: { mihomo: 1, xray: 1, "sing-box": 1, "ss-rust": 1 },
    level: { info: 2, warning: 1, error: 1 },
  },
  "core log buttons keep names separate from accurate result counts",
);

assert.deepEqual(
  batchAgentEligibility(
    { status: "online", features: ["agent-self-upgrade-v1"] },
    "upgrade-agent",
    "",
  ),
  { eligible: true, reason: "在线 · 支持远程升级" },
);
assert.match(
  batchAgentEligibility({ status: "offline" }, "upgrade-agent", "").reason,
  /离线/,
);
assert.match(
  batchAgentEligibility({ status: "online", features: [] }, "upgrade-agent", "")
    .reason,
  /旧版 Agent/,
);
assert.equal(
  batchAgentEligibility(
    { status: "online", runtime: { mihomo: { installed: true } } },
    "restart",
    "mihomo",
  ).eligible,
  true,
);
assert.equal(
  batchAgentEligibility(
    { status: "online", runtime: { mihomo: { installed: false } } },
    "restart",
    "mihomo",
  ).eligible,
  false,
);
assert.deepEqual(
  batchSelectAllState([
    { disabled: false, checked: true },
    { disabled: false, checked: false },
    { disabled: true, checked: true },
  ]),
  { eligible: 2, selected: 1, checked: false, indeterminate: true },
);
assert.deepEqual(
  batchSelectAllState([
    { disabled: false, checked: true },
    { disabled: false, checked: true },
  ]),
  { eligible: 2, selected: 2, checked: true, indeterminate: false },
);
assert.deepEqual(
  batchSelectAllState([
    { disabled: true, checked: true, dataset: { batchEligible: "1" } },
    { disabled: true, checked: true, dataset: { batchEligible: "1" } },
    { disabled: true, checked: false, dataset: { batchEligible: "0" } },
  ]),
  { eligible: 2, selected: 2, checked: true, indeterminate: false },
  "busy interaction locks do not erase the qualified selection state",
);
assert.deepEqual(batchSelectAllState([]), {
  eligible: 0,
  selected: 0,
  checked: false,
  indeterminate: false,
});

const pendingMigrationSource = {
  content: '{"inbounds":[{"tag":"original"}]}',
  taskId: "tsk_snapshot",
};
const migrationEditor = liveConfigEditorState({
  existingAvailable: true,
  canOperate: true,
  sourceContent: pendingMigrationSource.content,
  formContent: '{"inbounds":[{"tag":"edited"}]}',
});
assert.equal(migrationEditor.readOnly, true, "pending migration snapshots are read-only");
assert.equal(
  migrationEditor.content,
  pendingMigrationSource.content,
  "migration submission keeps the exact node snapshot bytes",
);
const migrationForm = new Map([
  ["name", "existing snapshot"],
  ["description", "pending migration"],
  ["content", '{"inbounds":[{"tag":"edited"}]}'],
  ["version", "0"],
]);
let savedMigrationConfig;
let migrationTaskAttempts = 0;
await assert.rejects(
  submitLiveConfigChange({
    api: async (path, options) => {
      if (path === "/tasks") {
        migrationTaskAttempts += 1;
        throw new Error("temporary task failure");
      }
      const input = JSON.parse(options.body);
      savedMigrationConfig = { id: "cfg_snapshot", ...input };
      return savedMigrationConfig;
    },
    submitTask: noop,
    agent: { id: "agt_snapshot" },
    engine: "sing-box",
    intent: "import",
    form: migrationForm,
    source: pendingMigrationSource,
    existingAvailable: true,
    savedConfig: null,
  }),
  /temporary task failure/,
);
assert.equal(savedMigrationConfig.content, pendingMigrationSource.content);
assert.deepEqual(
  pendingMigrationSource,
  { content: '{"inbounds":[{"tag":"original"}]}', taskId: "tsk_snapshot" },
  "failed import does not replace the pending migration source",
);
await submitLiveConfigChange({
  api: async (path) => {
    if (path !== "/tasks") throw new Error("retry unexpectedly rewrote snapshot");
    migrationTaskAttempts += 1;
    return { id: "tsk_retry" };
  },
  submitTask: noop,
  agent: { id: "agt_snapshot" },
  engine: "sing-box",
  intent: "import",
  form: migrationForm,
  source: pendingMigrationSource,
  existingAvailable: true,
  savedConfig: savedMigrationConfig,
});
assert.equal(migrationTaskAttempts, 2, "failed import remains retryable without another revision");

assert.equal(liveConfigEngineEligible({ installed: true }), true);
assert.equal(
  liveConfigEngineEligible({
    installed: false,
    existing_config_available: true,
  }),
  true,
  "migratable existing services remain selectable in manual configuration",
);
assert.equal(
  liveConfigEngineEligible({
    existing_config_unsupported_reason: "unsupported wrapper",
  }),
  true,
  "unsupported existing services remain eligible for the reason page and sidebar",
);
assert.equal(liveConfigEngineEligible({ installed: false }), false);
assert.equal(
  liveConfigReadAction({
    sourceMode: "managed",
    managedReadSupported: false,
    existingAvailable: false,
  }),
  "read-config",
  "old Agents keep the legacy managed read when no external service exists",
);
assert.equal(
  liveConfigReadAction({
    sourceMode: "managed",
    managedReadSupported: false,
    existingAvailable: true,
  }),
  "",
  "old Agents cannot confuse two coexisting configuration sources",
);
assert.equal(
  liveConfigReadAction({
    sourceMode: "managed",
    managedReadSupported: true,
    existingAvailable: true,
  }),
  "read-managed-config",
);
assert.equal(
  liveConfigReadAction({
    sourceMode: "import",
    managedReadSupported: true,
    existingAvailable: true,
  }),
  "read-config",
);

const dualSourceDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const dualState = {
    route: "live-config",
    navigationEpoch: 1,
    data: {
      liveAgent: "dual-node",
      liveEngine: "xray",
      liveConfigSource: "managed",
      liveSources: {
        "dual-node|xray": { content: '{"tag":"managed"}' },
        "dual-node|xray|import": { content: '{"tag":"external"}' },
      },
    },
  };
  const dualAgent = {
    id: "dual-node",
    name: "Dual source node",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["xray"],
    runtime: {
      xray: {
        installed: true,
        version: "25.8",
        existing_config_available: true,
      },
    },
  };
  let dualMarkup = "";
  const pages = installConfigPages({
    api: async (path) => {
      if (path === "/agents") return [dualAgent];
      if (path === "/agents/dual-node/configs/xray/workspace")
        return { config: { id: "cfg-managed", version: 3 } };
      assert.fail(`unexpected dual-source path ${path}`);
    },
    optionalAPI: async () => null,
    state: dualState,
    engines: ["xray"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    conciseVersion: (_engine, version) => version,
    date: (value) => value,
    ago: (value) => value,
    bytes: (value) => value,
    confirmAction: async () => true,
    notify: noop,
    shell: (markup) => {
      dualMarkup = markup;
    },
    submitTask: noop,
    bindCodeEditors: noop,
  });
  await pages.liveConfig();
  assert.equal(dualMarkup.includes("QAgent 配置"), true);
  assert.equal(dualMarkup.includes("系统服务配置"), true);
  assert.equal(dualMarkup.includes('{"tag":"managed"}'), true);
  assert.equal(dualMarkup.includes("readonly"), false);
  assert.equal(dualMarkup.includes('data-live-intent="deploy"'), true);

  dualState.data.liveConfigSource = "import";
  await pages.liveConfig();
  assert.equal(dualMarkup.includes('{"tag":"external"}'), true);
  assert.equal(dualMarkup.includes("readonly"), true);
  assert.equal(dualMarkup.includes('data-live-intent="import"'), true);
} finally {
  if (dualSourceDocument === undefined) delete globalThis.document;
  else globalThis.document = dualSourceDocument;
}

const unsupportedDocument = globalThis.document;
const unsupportedState = {
  route: "live-config",
  navigationEpoch: 1,
  data: {
    liveAgent: "unsupported-node",
    liveEngine: "xray",
    agents: [
      {
        id: "unsupported-node",
        name: "Unsupported node",
        os: "linux",
        arch: "amd64",
        status: "online",
        capabilities: ["xray"],
        runtime: {
          xray: {
            installed: false,
            existing_config_unsupported_reason: "complex wrapper is unsupported",
          },
        },
      },
    ],
  },
};
let unsupportedMarkup = "";
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const pages = installConfigPages({
    api: async (path) => {
      if (path === "/agents") return unsupportedState.data.agents;
      assert.equal(
        path,
        "/agents/unsupported-node/configs/xray/workspace",
      );
      return { config: null };
    },
    optionalAPI: async () => null,
    state: unsupportedState,
    engines: ["xray"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    conciseVersion: (engine, version) => version || engine,
    date: (value) => value,
    ago: (value) => value,
    bytes: (value) => value,
    confirmAction: async () => true,
    notify: noop,
    shell: (markup) => {
      unsupportedMarkup = markup;
    },
    submitTask: noop,
    bindCodeEditors: noop,
  });
  await pages.liveConfig();
  assert.deepEqual(unsupportedState.data.liveEngines, ["xray"]);
  assert.equal(unsupportedState.data.liveEngine, "xray");
  assert.equal(
    unsupportedMarkup.includes("检测到现有服务，但不可自动迁移"),
    true,
  );
  assert.equal(
    unsupportedMarkup.includes("complex wrapper is unsupported"),
    true,
  );
  assert.equal(
    unsupportedMarkup.includes("data-live-intent"),
    false,
    "unsupported reason pages expose no executable action",
  );
} finally {
  if (unsupportedDocument === undefined) delete globalThis.document;
  else globalThis.document = unsupportedDocument;
}

const runtimeRefreshDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const runtimeState = {
    route: "live-config",
    navigationEpoch: 10,
    data: {
      liveAgent: "upgraded-node",
      liveEngine: "sing-box",
      agents: [],
      liveSources: {},
    },
  };
  const runtimeSnapshots = [
    "executable is not in a supported standard path",
    "standard executable did not pass protected native binary validation",
    "",
  ];
  let runtimeAgentRequests = 0;
  let runtimeWorkspaceRequests = 0;
  let runtimeMarkup = "";
  const runtimeAgent = (unsupportedReason = "") => ({
    id: "upgraded-node",
    name: "Upgraded node",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["sing-box"],
    runtime: {
      "sing-box": {
        service_status: "active",
        installed: !unsupportedReason,
        existing_config_available: !unsupportedReason,
        existing_config_unsupported_reason: unsupportedReason,
      },
    },
  });
  const pages = installConfigPages({
    api: async (path) => {
      if (path === "/agents") {
        const reason = runtimeSnapshots[runtimeAgentRequests++];
        return [runtimeAgent(reason)];
      }
      if (
        path ===
        "/agents/upgraded-node/configs/sing-box/workspace"
      ) {
        runtimeWorkspaceRequests += 1;
        return { config: null };
      }
      assert.fail(`unexpected live runtime refresh path ${path}`);
    },
    optionalAPI: async () => null,
    state: runtimeState,
    engines: ["sing-box"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    conciseVersion: (engine, version) => version || engine,
    date: (value) => value,
    ago: (value) => value,
    bytes: (value) => value,
    confirmAction: async () => true,
    notify: noop,
    shell: (markup) => {
      runtimeMarkup = markup;
    },
    submitTask: noop,
    bindCodeEditors: noop,
  });

  await pages.liveConfig();
  assert.equal(
    runtimeMarkup.includes(runtimeSnapshots[0]),
    true,
    "the first live-config visit renders the then-current unsupported reason",
  );

  runtimeState.route = "tasks";
  runtimeState.navigationEpoch += 1;
  runtimeState.route = "live-config";
  runtimeState.navigationEpoch += 1;
  await pages.liveConfig();
  assert.equal(
    runtimeMarkup.includes(runtimeSnapshots[1]),
    true,
    "upgrade to tasks to live-config refreshes the Agent runtime without a hard reload",
  );
  assert.equal(runtimeMarkup.includes(runtimeSnapshots[0]), false);

  const migrationSnapshot =
    '{"inbounds":[{"tag":"complete-existing-snapshot"}]}';
  runtimeState.data.liveConfigSource = "import";
  runtimeState.data.liveSources["upgraded-node|sing-box|import"] = {
    content: migrationSnapshot,
    taskId: "read-after-upgrade",
  };
  runtimeState.route = "tasks";
  runtimeState.navigationEpoch += 1;
  runtimeState.route = "live-config";
  runtimeState.navigationEpoch += 1;
  await pages.liveConfig();
  assert.equal(runtimeMarkup.includes(migrationSnapshot), true);
  assert.equal(
    runtimeMarkup.includes("readonly"),
    true,
    "a newly available migration snapshot is rendered read-only",
  );
  assert.equal(
    (runtimeMarkup.match(/data-live-intent="import"/g) || []).length,
    1,
    "a newly available migration exposes exactly one import action",
  );
  assert.equal(
    runtimeAgentRequests,
    3,
    "each cross-route visit refreshes runtime once",
  );
  await pages.liveConfig();
  assert.equal(
    runtimeAgentRequests,
    3,
    "same-route live-config renders reuse the current scoped runtime",
  );
  assert.equal(runtimeWorkspaceRequests, 4);
} finally {
  if (runtimeRefreshDocument === undefined) delete globalThis.document;
  else globalThis.document = runtimeRefreshDocument;
}

const staleRuntimeDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const staleState = {
    route: "live-config",
    navigationEpoch: 20,
    data: {
      liveAgent: "race-node",
      liveEngine: "sing-box",
      liveConfigSource: "import",
      agents: [],
      liveSources: {
        "race-node|sing-box|import": { content: '{"log":{"level":"info"}}' },
      },
    },
  };
  let resolveOldAgents;
  const oldAgents = new Promise((resolve) => {
    resolveOldAgents = resolve;
  });
  let staleAgentRequests = 0;
  let staleWorkspaceRequests = 0;
  let staleMarkup = "";
  const raceAgent = (runtime) => ({
    id: "race-node",
    name: "Race node",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["sing-box"],
    runtime: { "sing-box": runtime },
  });
  const pages = installConfigPages({
    api: async (path) => {
      if (path === "/agents") {
        staleAgentRequests += 1;
        if (staleAgentRequests === 1) return oldAgents;
        return [
          raceAgent({
            installed: true,
            existing_config_available: true,
          }),
        ];
      }
      if (path === "/agents/race-node/configs/sing-box/workspace") {
        staleWorkspaceRequests += 1;
        return { config: null };
      }
      assert.fail(`unexpected stale runtime path ${path}`);
    },
    optionalAPI: async () => null,
    state: staleState,
    engines: ["sing-box"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    conciseVersion: (engine, version) => version || engine,
    date: (value) => value,
    ago: (value) => value,
    bytes: (value) => value,
    confirmAction: async () => true,
    notify: noop,
    shell: (markup) => {
      staleMarkup = markup;
    },
    submitTask: noop,
    bindCodeEditors: noop,
  });

  const staleRender = pages.liveConfig();
  staleState.navigationEpoch += 1;
  const currentRender = pages.liveConfig();
  await currentRender;
  resolveOldAgents([
    raceAgent({
      installed: false,
      existing_config_unsupported_reason: "stale unsupported reason",
    }),
  ]);
  await staleRender;
  assert.equal(
    staleState.data.agents[0].runtime["sing-box"].existing_config_available,
    true,
  );
  assert.equal(staleMarkup.includes("stale unsupported reason"), false);
  assert.equal(staleMarkup.includes('data-live-intent="import"'), true);
  assert.equal(
    staleWorkspaceRequests,
    1,
    "the superseded runtime never loads a workspace",
  );
} finally {
  if (staleRuntimeDocument === undefined) delete globalThis.document;
  else globalThis.document = staleRuntimeDocument;
}

const ctx = new Proxy(
  { state, engines: [], actions: [] },
  { get: (target, key) => target[key] ?? noop },
);

for (const install of [
  installAgents,
  installClientAccess,
  installConfigPages,
  installCoreLogs,
  installDashboard,
  installSettings,
  installTasks,
  installTraffic,
]) {
  const page = install(ctx);
  if (typeof page !== "function" && (typeof page !== "object" || !page)) {
    throw new TypeError(`${install.name} returned an invalid page module`);
  }
}

// Manual-config deploy-cache regression tests (round 5).
// Tests drive actual production form-submit handlers with controlled
// async timing to verify:
// A. Route abort on monitor -> reconcile sees running -> recovery poller
//    detects succeeded -> cache invalidated -> fresh read fires.
// B. Field deploy failure does NOT invalidate (real handler).
// C. Validate-only preserves cache and records no pending.
// D. Different agent|engine keys do not cross-contaminate.
// E. ABA: old deploy A succeeds while B is pending -> cache IS
//    invalidated (node changed!), B's pending NOT cleared by A.
// F. Live-config editor deploy while on page -> auto-converges,
//    editorReadCount >= 1 proves fresh read fired.
// G. Source-config deploy success path uses same terminal mechanism.
{
  const previousDocument = globalThis.document;
  const previousFormData = globalThis.FormData;
  const previousSetTimeout = globalThis.setTimeout;
  globalThis.location ??= { hash: "" };

  class FakeForm {
    constructor(elements = {}) {
      this.isConnected = true;
      this.querySelector = () => null;
      this.querySelectorAll = () => [];
      this._el = new Map(
        Object.entries(elements).map(([k, v]) => [
          k,
          { name: k, value: v, type: "text", hidden: false,
            addEventListener() {}, replaceWith() {},
            parentElement: { classList: { contains: () => false }, append() {} } },
        ]),
      );
      this._l = new Map();
    }
    get elements() { return { namedItem: (n) => this._el.get(n) ?? null }; }
    addEventListener(type, listener) {
      const list = this._l.get(type) || [];
      list.push(listener);
      this._l.set(type, list);
    }
    async dispatchSubmit(ds = {}) {
      await Promise.all(
        (this._l.get("submit") || []).map((fn) =>
          fn({ currentTarget: this, preventDefault() {},
               submitter: { dataset: ds } }),
        ),
      );
    }
  }

  const deferred = () => {
    let resolve, reject;
    return { promise: new Promise((res, rej) => { resolve = res; reject = rej; }),
             resolve, reject };
  };

  globalThis.setTimeout = (cb) => { cb(); return 1; };
  globalThis.FormData = class {
    constructor(f) { this.f = f; }
    get(n) { return this.f.elements.namedItem(n)?.value ?? null; }
  };

  const AGENT_ID = "test-agent";
  const ENGINE = "xray";
  const KEY = `${AGENT_ID}|${ENGINE}`;
  const OLD_CONTENT = '{"inbounds":[{"tag":"old-inbound"}]}';
  const NEW_CONTENT = '{"inbounds":[]}';

  const makeAgent = (id = AGENT_ID) => ({
    id, name: `Agent-${id}`, os: "linux", arch: "amd64",
    status: "online", capabilities: [ENGINE],
    features: ["managed-config-read-v1"],
    runtime: { [ENGINE]: { installed: true } },
  });

  const workspaceWithConfig = (overrides = {}) => ({
    config: { id: "cfg-1", version: 1, name: "cfg", description: "", ...overrides },
    inbounds: [{ tag: "old-inbound", listen: "0.0.0.0", port: 443 }],
    protocols: [{ key: "ss2022", badge: "SS", name: "SS 2022", docs: "",
      methods: [], transports: ["raw"], default_port: 443 }],
    catalog: { fields: [{ key: "log", label: "Log", kind: "object", docs: "" }],
      name: "", format: "JSON", topic_count: 0, topic_groups: [] },
    present_fields: {},
  });

  const emptyWorkspace = {
    config: null, inbounds: [],
    protocols: [{ key: "ss2022", badge: "SS", name: "SS 2022", docs: "",
      methods: [], transports: ["raw"], default_port: 443 }],
    catalog: { fields: [], name: "", format: "JSON", topic_count: 0,
      topic_groups: [] }, present_fields: {},
  };

  const buildContext = (testState, handlers) => {
    const apiLog = [];
    let markup = "";
    const noopFn = () => {};
    const match = (method, path) => {
      for (const [key, value] of Object.entries(handlers)) {
        if (key.startsWith(method + " ")) {
          try {
            if (new RegExp(`^${key.slice(method.length + 1)}$`).test(path))
              return value;
          } catch {}
        }
      }
      return undefined;
    };
    const ctx = new Proxy({
      state: testState, engines: [ENGINE],
      api: async (path, options = {}) => {
        const method = options.method || "GET";
        apiLog.push({ method, path });
        const h = match(method, path);
        if (h === undefined) throw new Error(`unmocked ${method} ${path}`);
        return typeof h === "function" ? h(options, path) : h;
      },
      optionalAPI: async () => null,
      can: () => true, esc: (v) => String(v ?? ""),
      engineName: (v) => v, conciseVersion: (_e, v) => v,
      notify: noopFn, confirmAction: async () => true,
      shell: (m) => { markup = m; },
      submitTask: async (payload) => {
        const h = match("POST", "/tasks");
        if (h === undefined) return null;
        return typeof h === "function"
          ? h({ body: JSON.stringify(payload) }, "/tasks") : h;
      },
      bindCodeEditors: noopFn,
    }, { get: (t, k) => t[k] ?? noopFn });
    return { ctx, apiLog, markup: () => markup };
  };

  const installForms = (ctx, forms = {}) => {
    globalThis.document = {
      querySelector: (sel) => forms[sel] ?? null,
      querySelectorAll: () => [],
      createElement: () => ({
        className: "", type: "", dataset: {}, textContent: "",
        setAttribute() {}, removeAttribute() {}, append() {}, addEventListener() {},
        parentElement: { classList: { contains: () => false }, append() {} },
      }),
    };
    return installConfigPages(ctx);
  };

  const drain = async () => {
    for (let i = 0; i < 300; i++) await new Promise((r) => setImmediate(r));
  };

  // --- Test A: Route abort on monitor -> reconcile sees running ->
  // recovery poller detects succeeded -> cache invalidated -> fresh read.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let freshReadCount = 0;

    const planForm = new FakeForm({
      operation: "delete", tag: "old-inbound", listen: "0.0.0.0",
      port: "443", username: "", credential: "", secondary_credential: "",
      method: "", transport: "raw", transport_path: "",
      tls_enabled: "0", certificate_path: "", private_key_path: "",
      reality_enabled: "0", reality_private_key: "", reality_public_key: "",
      reality_short_id: "", reality_server_name: "",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/.*": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/server-inbounds": () => ({
        config: { id: "cfg-new", version: 2 },
        task: { id: "deploy-t1", status: "pending" },
      }),
      "GET /tasks/deploy-t1": (() => {
        let calls = 0;
        return () => {
          calls += 1;
          if (calls <= 1) throw new DOMException("route abort", "AbortError");
          return { status: "succeeded", id: "deploy-t1" };
        };
      })(),
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "read-managed-config") {
          freshReadCount += 1;
          return { id: `read-${freshReadCount}` };
        }
        return { id: "other" };
      },
      "GET /tasks/read-\\d+": (_o, p) => ({ status: "succeeded", id: p.split("/").pop() }),
      "GET /tasks/read-\\d+/config-snapshot": { content: NEW_CONTENT },
    });

    const pages = installForms(ctx, { "#server-plan-form": planForm });
    state.data.inboundTag = "old-inbound";
    await pages.agentConfig();
    await planForm.dispatchSubmit({ planIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY]?.taskId, "deploy-t1",
      "[A] pending recorded despite monitor abort");

    state.route = "live-config";
    await pages.liveConfig();
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[A] pending cleared after recovery poller saw succeeded");
    assert.ok(freshReadCount >= 1,
      "[A] fresh read fired after convergence");
    if (state.data.liveSources?.[KEY]?.content)
      assert.notEqual(state.data.liveSources[KEY].content, OLD_CONTENT,
        "[A] old content not persisted after convergence");
  }

  // --- Test B: Field deploy failure does NOT invalidate (real handler).
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let failPollCount = 0;
    let fieldPostCalled = false;

    const fieldForm = new FakeForm({ mutation: "modify", fragment: '{"level":"info"}' });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig({ version: 3, name: "existing-cfg" }),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/log": { present: true, fragment: "{}" },
      "POST /agents/test-agent/configs/xray/fields/log": () => {
        fieldPostCalled = true;
        return { config: { version: 4 }, task: { id: "field-deploy-fail", status: "pending" } };
      },
      "GET /tasks/field-deploy-fail": () => {
        failPollCount += 1;
        return failPollCount <= 2
          ? { status: "running", id: "field-deploy-fail" }
          : { status: "failed", id: "field-deploy-fail", error: "node down" };
      },
    });

    const pages = installForms(ctx, { "#field-form": fieldForm });
    await pages.agentConfig();
    await fieldForm.dispatchSubmit({ fieldIntent: "deploy" });
    await drain();

    assert.ok(fieldPostCalled, "[B] field POST was called");
    assert.ok(failPollCount > 0, "[B] deploy terminal was polled");
    assert.equal(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[B] cache NOT invalidated after deploy failed");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[B] pending entry cleared after failed terminal");
  }

  // --- Test C: Validate-only preserves cache and records no pending.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };

    const planForm = new FakeForm({
      operation: "modify", tag: "t", listen: "127.0.0.1", port: "8443",
      username: "u", credential: "c", secondary_credential: "",
      method: "", transport: "raw", transport_path: "",
      tls_enabled: "0", certificate_path: "", private_key_path: "",
      reality_enabled: "0", reality_private_key: "", reality_public_key: "",
      reality_short_id: "", reality_server_name: "",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/.*": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/server-inbounds": {
        config: { version: 2 }, task: { id: "v-only-task" } },
    });

    installForms(ctx, { "#server-plan-form": planForm });
    const p = installConfigPages(ctx);
    await p.agentConfig();
    await planForm.dispatchSubmit({ planIntent: "validate" });
    await drain();

    assert.equal(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[C] validate-only preserves cached content");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[C] no pending deploy recorded for validate-only");
  }

  // --- Test D: Different agent|engine keys do not cross-contaminate.
  {
    const OTHER_AGENT = "other-agent";
    const OTHER_KEY = `${OTHER_AGENT}|${ENGINE}`;
    const state = {
      route: "live-config",
      data: {
        agents: [makeAgent(), makeAgent(OTHER_AGENT)],
        agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: {
          [KEY]: { content: OLD_CONTENT, reading: false },
          [OTHER_KEY]: { content: '{"other":true}', reading: false },
        },
        pendingDeployTasks: { [KEY]: { taskId: "dep-a" } },
      },
      session: { role: "admin" },
    };

    const { ctx } = buildContext(state, {
      "GET /agents": () => state.data.agents,
      "GET /agents/test-agent/configs/xray/workspace": emptyWorkspace,
      "GET /tasks/dep-a": { status: "succeeded", id: "dep-a" },
      "POST /tasks": { id: "r-x" },
      "GET /tasks/r-x": { status: "succeeded", id: "r-x" },
      "GET /tasks/r-x/config-snapshot": { content: NEW_CONTENT },
    });

    installForms(ctx, {});
    const pages = installConfigPages(ctx);
    await pages.liveConfig();
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[D] pending deploy cleared for target agent");
    assert.notEqual(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[D] target agent's old content replaced after reconciliation");
    assert.equal(state.data.liveSources?.[OTHER_KEY]?.content, '{"other":true}',
      "[D] other agent's cache NOT affected");
    assert.equal(state.data.pendingDeployTasks?.[OTHER_KEY], undefined,
      "[D] other agent has no pending to clear (was never set)");
  }

  // --- Test E (ABA): Old deploy A succeeds -> cache invalidated.
  // Then B overwrites pending; B fails later -> only B's pending cleared.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };

    const gateA = deferred();
    const gateB = deferred();
    let editorReadCountE = 0;


    const planForm = new FakeForm({
      operation: "modify", tag: "t", listen: "0.0.0.0", port: "443",
      username: "", credential: "", secondary_credential: "",
      method: "", transport: "raw", transport_path: "",
      tls_enabled: "0", certificate_path: "", private_key_path: "",
      reality_enabled: "0", reality_private_key: "", reality_public_key: "",
      reality_short_id: "", reality_server_name: "",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/.*": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/server-inbounds": () => ({
        config: { id: "cfg-a", version: 2 },
        task: { id: "dep-A", status: "pending" },
      }),
      "GET /tasks/dep-A": () => gateA.promise,
      "GET /tasks/dep-B": (() => {
        let bCalls = 0;
        return async () => {
          bCalls += 1;
          if (bCalls <= 1) return { status: "running", id: "dep-B" };
          return await gateB.promise;
        };
      })(),
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "read-managed-config") {
          editorReadCountE += 1;
          return { id: "read-e1" };
        }
        return { id: "other-e" };
      },
      "GET /tasks/read-e1": { status: "succeeded", id: "read-e1" },
      "GET /tasks/read-e1/config-snapshot": { content: NEW_CONTENT },
    });

    const pages = installForms(ctx, { "#server-plan-form": planForm });
    await pages.agentConfig();

    // Submit deploy A via form handler (starts monitor A).
    await planForm.dispatchSubmit({ planIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY]?.taskId, "dep-A",
      "[E] dep-A recorded as pending");

    // Simulate deploy B overwriting A in the pending map
    // (happens when a second mutation fires before A completes).
    state.data.pendingDeployTasks[KEY] = { taskId: "dep-B" };

    // Release gate A -> old dep-A succeeds.
    gateA.resolve({ status: "succeeded", id: "dep-A" });
    await drain();

    // Cache IS invalidated because A succeeded and changed the node file.
    assert.equal(state.data.liveSources?.[KEY], undefined,
      "[E] cache invalidated by old deploy A's success");

    // Pending still holds dep-B (A did NOT clear it via CAS).
    assert.equal(state.data.pendingDeployTasks?.[KEY]?.taskId, "dep-B",
      "[E] newer dep-B pending NOT cleared by old dep-A completion");

    // Step 4: Start B's recovery poller by entering live-config.
    // reconcilePendingDeploy sees dep-B running -> starts recovery poller.
    state.route = "live-config";
    await pages.liveConfig();
    await drain();

    // Step 5: Release gate B -> dep-B fails.
    gateB.resolve({ status: "failed", id: "dep-B", error: "conflict" });
    await drain();

    // Assert: B's failure cleared its own pending record.
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[E] dep-B pending cleared after B's own terminal failure");

    // Strict: post-A fresh read completed and cached NEW_CONTENT.
    assert.ok(editorReadCountE >= 1,
      "[E] post-A fresh managed-config read fired");
    assert.equal(state.data.liveSources?.[KEY]?.content, NEW_CONTENT,
      "[E] cache contains post-deploy content (not OLD_CONTENT) after B failed");
  }


  // --- Test F: Live-config editor deploy -> terminal success ->
  // pending cleanup + cache invalidation + guarded re-render.
  {
    const state = {
      route: "live-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let editorReadCount = 0;

    const liveForm = new FakeForm({
      content: NEW_CONTENT, name: "e", description: "d", version: "1",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": emptyWorkspace,
      "PUT /agents/test-agent/configs/xray": { config: { id: "lc", version: 2 } },
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "deploy") return { id: "lc-deploy-task" };
        editorReadCount += 1;
        return { id: `editor-read-${editorReadCount}` };
      },
      "GET /tasks/lc-deploy-task": () => ({
        status: "succeeded", id: "lc-deploy-task",
      }),
      "GET /tasks/editor-read-\\d+": (_o, p) => ({
        status: "succeeded", id: p.split("/").pop(),
      }),
      "GET /tasks/editor-read-\\d+/config-snapshot": { content: NEW_CONTENT },
    });

    const pages = installForms(ctx, { "#live-config-form": liveForm });
    await pages.liveConfig();
    await liveForm.dispatchSubmit({ liveIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[F] pending cleared after editor deploy succeeded");
    assert.ok(editorReadCount >= 1,
      "[F] fresh managed-config read fired after editor deploy succeeded");
    assert.equal(state.data.liveSources?.[KEY]?.content, NEW_CONTENT,
      "[F] cache contains post-deploy content after convergence");
  }

  // --- Test G: Source-config deploy success uses same terminal mechanism.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let srcPollCount = 0;

    const srcForm = new FakeForm({ name: "s", description: "d", content: NEW_CONTENT });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/log": { present: false, fragment: "" },
      "PUT /agents/test-agent/configs/xray": { config: { id: "sc", version: 2 } },
      "POST /tasks": { id: "src-deploy-task" },
      "GET /tasks/src-deploy-task": () => {
        srcPollCount += 1;
        return { status: "succeeded", id: "src-deploy-task" };
      },
    });

    const pages = installForms(ctx, { "#source-config-form": srcForm });
    await pages.agentConfig();
    await srcForm.dispatchSubmit({ sourceIntent: "deploy" });
    await drain();

    assert.ok(srcPollCount > 0,
      "[G] source-config deploy terminal was checked");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[G] source-config pending cleared after deploy succeeded");
    assert.equal(state.data.liveSources?.[KEY], undefined,
      "[G] source-config cache invalidated after deploy succeeded");
  }

  if (previousSetTimeout === undefined) delete globalThis.setTimeout;
  else globalThis.setTimeout = previousSetTimeout;
  if (previousFormData === undefined) delete globalThis.FormData;
  else globalThis.FormData = previousFormData;
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}
const structureDocument = globalThis.document;
const structureCSS = globalThis.CSS;
const flushMicrotasks = () => new Promise((resolve) => setImmediate(resolve));
class StructureElement {
  constructor() {
    this.dataset = {};
    this.textContent = "";
    this.className = "";
    this.value = "";
    this.disabled = false;
    this.hasAttribute = () => false;
    this.setAttribute = () => {};
    this.removeAttribute = () => {};
    this.closest = () => null;
    this.querySelector = () => null;
    this.querySelectorAll = () => [];
  }
}
const structureCards = {
  "sing-box": new StructureElement(),
  xray: new StructureElement(),
};
const structureStates = {
  "sing-box": new StructureElement(),
  xray: new StructureElement(),
};
const structureServices = {
  "sing-box": new StructureElement(),
  xray: new StructureElement(),
};
for (const [engine, service] of Object.entries(structureServices))
  service.closest = () => structureStates[engine];
const structureInstalledSummary = new StructureElement();
for (const card of Object.values(structureCards)) {
  card.dataset.runtimeStructure = "full";
  card.dataset.coreInstalled = "0";
  card.dataset.existingPending = "0";
  card.dataset.existingUnsupported = "";
}
const structureRoot = new StructureElement();
structureRoot.querySelector = (selector) => {
  const match = selector.match(/^\.service-(.+)$/);
  if (match) return structureCards[match[1]] || null;
  const serviceMatch = selector.match(/^\[data-core-service="(.+)"\]$/);
  if (serviceMatch) return structureServices[serviceMatch[1]] || null;
  if (selector === "[data-core-installed-summary]")
    return structureInstalledSummary;
  return null;
};
globalThis.CSS = { escape: (value) => String(value) };
globalThis.document = {
  hidden: false,
  activeElement: null,
  querySelector: (selector) =>
    selector === '[data-agent-metrics="alpha"]' ? structureRoot : null,
  querySelectorAll: () => [],
};
try {
  let structureRequests = 0;
  let structureRenders = 0;
  let structureMarkup = "";
  const structureNotifications = [];
  let controlledRender = null;
  let compactPolling = false;
  const structureState = {
    route: "node-settings",
    anchor: "settings-node-alpha",
    navigationEpoch: 1,
    data: { nodeView: "detail", selectedAgent: "alpha" },
  };
  const singleEngine = (unsupported = false) => [
    {
      id: "alpha",
      os: "linux",
      arch: "amd64",
      status: "online",
      metrics: {},
      capabilities: ["sing-box"],
      runtime: {
        "sing-box": {
          installed: !unsupported,
          existing_config_available: true,
          ...(unsupported ? { existing_config_unsupported_reason: "unsupported" } : {}),
        },
      },
    },
  ];
  const twoEngine = [
    {
      id: "alpha",
      status: "online",
      metrics: {},
      capabilities: ["sing-box", "xray"],
      runtime: {
        "sing-box": { installed: false, existing_config_available: true },
        xray: { installed: false, existing_config_available: true },
      },
    },
  ];
  let structurePayload = singleEngine;
  const { pollAgentMetrics } = installAgents(
    new Proxy(
      {
        state: structureState,
        api: async (path) => {
          assert.equal(path, "/agents");
          structureRequests += 1;
          if (compactPolling) return structurePayload();
          if (structureRequests % 2 === 1) return structurePayload();
          structureRenders += 1;
          if (controlledRender && !controlledRender.used) {
            controlledRender.used = true;
            return controlledRender.promise;
          }
          if (structureRenders === 1)
            throw new Error("temporary structure render failure");
          return singleEngine(true);
        },
        can: (capability) => capability === "metrics.read",
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        serviceStatusName: (value) => value,
        statusTone: (value) => value,
        conciseVersion: (_engine, value) => value,
        notify: (message) => structureNotifications.push(message),
        shell: (markup) => {
          structureMarkup = markup;
        },
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );

  // The first structure render rejects: the marker is not committed, so the
  // next poll retries and applies the new state instead of going permanently stale.
  structurePayload = singleEngine;
  await pollAgentMetrics();
  clearTimeout(structureState.agentPollTimer);
  await flushMicrotasks();
  assert.equal(structureRenders, 1, "first structure render is attempted");
  assert.equal(
    structureCards["sing-box"].dataset.existingPending,
    "0",
    "a rejected structure render must not precommit the pending marker",
  );
  assert.deepEqual(
    structureNotifications,
    ["temporary structure render failure"],
    "the rejected structure render is handled (no unhandled rejection)",
  );

  await pollAgentMetrics();
  clearTimeout(structureState.agentPollTimer);
  await flushMicrotasks();
  assert.equal(structureRenders, 2, "render is retried after the transient failure");
  assert.equal(
    structureState.data.agents[0].runtime["sing-box"].existing_config_unsupported_reason,
    "unsupported",
    "the retried render applies the new unsupported runtime",
  );
  assert.equal(
    structureMarkup.includes('data-existing-pending="1"'),
    true,
    "the retried render applies the pending marker",
  );
  assert.equal(
    structureMarkup.includes('data-existing-unsupported="unsupported"'),
    true,
    "the retried render applies the unsupported marker",
  );
  assert.equal(
    structureNotifications.length,
    1,
    "the successful retry does not raise another error",
  );

  // Multiple engine transitions in one poll are coalesced into one in-flight
  // structure render rather than launching concurrent duplicate renders.
  structureRequests = 0;
  structureRenders = 0;
  structureNotifications.length = 0;
  structurePayload = () => twoEngine;
  let finishControlledRender;
  controlledRender = {
    used: false,
    promise: new Promise((resolve) => {
      finishControlledRender = resolve;
    }),
  };
  await pollAgentMetrics();
  clearTimeout(structureState.agentPollTimer);
  await flushMicrotasks();
  assert.equal(structureRenders, 1, "one poll coalesces several engine transitions into one render");
  assert.equal(structureRequests, 2, "coalescing keeps one metrics poll and one structure render request");
  finishControlledRender(twoEngine);
  await flushMicrotasks();
  clearTimeout(structureState.agentPollTimer);

  // Aggregate cards render compact core chips, not full service-card
  // structure. Missing full-view markers on those chips must not turn every
  // metrics poll into another page render.
  structureRequests = 0;
  structureRenders = 0;
  controlledRender = null;
  compactPolling = true;
  structurePayload = () => [
    {
      id: "alpha",
      os: "linux",
      arch: "amd64",
      status: "online",
      metrics: {},
      capabilities: ["sing-box"],
      runtime: {
        "sing-box": {
          installed: false,
          service_status: "unknown",
          existing_config_available: true,
        },
      },
    },
  ];
  delete structureCards["sing-box"].dataset.runtimeStructure;
  delete structureCards["sing-box"].dataset.existingPending;
  delete structureCards["sing-box"].dataset.existingUnsupported;
  await pollAgentMetrics();
  clearTimeout(structureState.agentPollTimer);
  await flushMicrotasks();
  assert.equal(
    structureRequests,
    1,
    "a compact aggregate core chip keeps one bounded metrics request",
  );
  assert.equal(
    structureRenders,
    0,
    "a compact aggregate core chip does not request a structural page render",
  );
  assert.equal(structureCards["sing-box"].dataset.coreInstalled, "0");
  assert.equal(structureServices["sing-box"].textContent, "未安装");
  assert.equal(structureStates["sing-box"].className, "engine-state muted");
  assert.equal(
    structureInstalledSummary.textContent,
    "linux / amd64 · 尚未安装内核",
  );

  structurePayload = () => [
    {
      id: "alpha",
      os: "linux",
      arch: "amd64",
      status: "online",
      metrics: {},
      capabilities: ["sing-box"],
      runtime: {
        "sing-box": { installed: true, service_status: "running" },
      },
    },
  ];
  await pollAgentMetrics();
  clearTimeout(structureState.agentPollTimer);
  await flushMicrotasks();
  assert.equal(structureRequests, 2, "compact install transition stays in place");
  assert.equal(structureRenders, 0, "compact install transition does not render");
  assert.equal(structureCards["sing-box"].dataset.coreInstalled, "1");
  assert.equal(structureServices["sing-box"].textContent, "running");
  assert.equal(structureStates["sing-box"].className, "engine-state running");
  assert.equal(
    structureInstalledSummary.textContent,
    "linux / amd64 · 1/1 内核已安装",
  );

  structurePayload = () => [
    {
      id: "alpha",
      os: "linux",
      arch: "amd64",
      status: "online",
      metrics: {},
      capabilities: ["sing-box"],
      runtime: {
        "sing-box": { installed: false, service_status: "unknown" },
      },
    },
  ];
  await pollAgentMetrics();
  clearTimeout(structureState.agentPollTimer);
  await flushMicrotasks();
  assert.equal(structureRequests, 3, "compact uninstall transition stays bounded");
  assert.equal(structureRenders, 0, "compact uninstall transition does not render");
  assert.equal(structureCards["sing-box"].dataset.coreInstalled, "0");
  assert.equal(structureServices["sing-box"].textContent, "未安装");
  assert.equal(structureStates["sing-box"].className, "engine-state muted");
  assert.equal(
    structureInstalledSummary.textContent,
    "linux / amd64 · 尚未安装内核",
  );

  // A newly enrolled Agent has no existing card to patch. The fleet poll must
  // request one structural render so it appears without a browser refresh.
  const newlyEnrolled = {
    id: "new-node",
    name: "New node",
    os: "linux",
    arch: "amd64",
    status: "online",
    metrics: {},
    capabilities: [],
    runtime: {},
    labels: {},
    features: [],
  };
  const expandedFleet = () => [...singleEngine(), newlyEnrolled];
  structureRequests = 0;
  structureRenders = 0;
  structureMarkup = "";
  compactPolling = false;
  structureState.anchor = "node-settings";
  structureState.data.nodeView = "overview";
  structureState.data.agents = singleEngine();
  structurePayload = expandedFleet;
  controlledRender = {
    used: false,
    promise: Promise.resolve(expandedFleet()),
  };
  await pollAgentMetrics();
  clearTimeout(structureState.agentPollTimer);
  await flushMicrotasks();
  assert.equal(structureRequests, 2, "new Agent detection uses one poll and one render request");
  assert.equal(structureRenders, 1, "a new Agent triggers one structural render");
  assert.equal(
    structureMarkup.includes('data-agent-node="new-node"'),
    true,
    "the structural render includes the newly enrolled Agent card",
  );
  clearTimeout(structureState.agentPollTimer);
} finally {
  if (structureDocument === undefined) delete globalThis.document;
  else globalThis.document = structureDocument;
  if (structureCSS === undefined) delete globalThis.CSS;
  else globalThis.CSS = structureCSS;
}

assert.equal(developmentSourceVisible("mihomo", "development"), true, "mihomo development shows source choice");
assert.equal(developmentSourceVisible("mihomo", "stable"), false, "stable hides source choice");
assert.equal(developmentSourceVisible("xray", "development"), false, "non-mihomo hides source choice");
assert.equal(coreSourceForInstall("mihomo", "development", "mirror"), "mirror", "mirror carries through");
assert.equal(coreSourceForInstall("mihomo", "development", ""), "official", "omitted source defaults to official");
assert.equal(coreSourceForInstall("mihomo", "stable", "mirror"), undefined, "stable omits source");
assert.equal(coreSourceForInstall("xray", "development", "mirror"), undefined, "non-mihomo omits source");
assert.equal(coreSourceName("mirror"), "vernesong/mihomo 镜像（第三方）", "mirror audited label");
assert.equal(coreSourceName("official"), "MetaCubeX/mihomo 官方", "official audited label");
assert.equal(coreSourceName(""), "", "no source has no label");
assert.equal(coreSourceLabel("mihomo", "development", ""), "MetaCubeX/mihomo 官方", "omitted mihomo development audits as official");
assert.equal(coreSourceLabel("mihomo", "development", "official"), "MetaCubeX/mihomo 官方", "explicit official audits as official");
assert.equal(coreSourceLabel("mihomo", "development", "mirror"), "vernesong/mihomo 镜像（第三方）", "mirror audits as third party");
assert.equal(coreSourceLabel("mihomo", "stable", ""), "", "stable has no source label");
assert.equal(coreSourceLabel("xray", "development", ""), "", "non-mihomo has no source label");

const formattedJson = formatConfigContent(
  '{"proxies":[],"mode":"rule","port":7890,"enabled":true,"empty":null}',
  "JSON",
);
assert.equal(
  formattedJson,
  '{\n  "proxies": [],\n  "mode": "rule",\n  "port": 7890,\n  "enabled": true,\n  "empty": null\n}\n',
  "JSON format applies two-space indentation, preserves order and types, and ends with one newline",
);
assert.equal(
  formatConfigContent('[1,{"nested":[2,3]}]', "JSON"),
  '[\n  1,\n  {\n    "nested": [\n      2,\n      3\n    ]\n  }\n]\n',
  "JSON format keeps array order and nesting",
);
assert.ok(
  formatConfigContent('{"a":1,\n\n\n "b":2}', "JSON").startsWith('{\n  "a": 1'),
  "JSON format is syntax-aware and idempotent across whitespace",
);
assert.throws(
  () => formatConfigContent('{"a":1,}', "JSON"),
  ConfigFormatError,
  "JSON with a trailing comma fails closed",
);
assert.throws(
  () => formatConfigContent('{"a":1} // sing-box comment', "JSON"),
  ConfigFormatError,
  "sing-box extended JSON comments fail closed",
);
assert.throws(
  () =>
    formatConfigContent(
      '{"a":1,"a":2}',
      "JSON",
    ),
  ConfigFormatError,
  "duplicate JSON keys fail closed",
);
assert.equal(
  formatConfigContent('{"big":9007199254740993}', "JSON"),
  '{\n  "big": 9007199254740993\n}\n',
  "unsafe integers are preserved verbatim, never rounded",
);
assert.equal(
  formatConfigContent('{"x":1e400}', "JSON"),
  '{\n  "x": 1e400\n}\n',
  "overflowing exponents are preserved verbatim",
);
assert.equal(
  formatConfigContent('{"x":9.007199254740993e15}', "JSON"),
  '{\n  "x": 9.007199254740993e15\n}\n',
  "high-precision decimals keep their full token",
);
assert.equal(
  formatConfigContent('{"x":0.100000000000000005}', "JSON"),
  '{\n  "x": 0.100000000000000005\n}\n',
  "high-precision decimal literals are not collapsed",
);
assert.equal(
  formatConfigContent('{"x":-0}', "JSON"),
  '{\n  "x": -0\n}\n',
  "negative zero keeps its sign",
);
assert.equal(
  formatConfigContent('{"10":"ten","2":"two","a":1}', "JSON"),
  '{\n  "10": "ten",\n  "2": "two",\n  "a": 1\n}\n',
  "object member order is preserved, including integer-like keys",
);
assert.equal(
  formatConfigContent('{"a\\u0041":1,"b\\n":2}', "JSON"),
  '{\n  "a\\u0041": 1,\n  "b\\n": 2\n}\n',
  "escaped object keys and string escapes are preserved",
);
assert.throws(
  () =>
    formatConfigContent(
      "mixed-port: 7890\n# keep me\nproxies: []\n",
      "YAML",
    ),
  ConfigFormatError,
  "Mihomo YAML with comments fails closed without a comment-preserving parser",
);
assert.throws(
  () => formatConfigContent("   ", "JSON"),
  ConfigFormatError,
  "empty content fails closed",
);

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

const previousFormData = globalThis.FormData;
const planControls = Object.fromEntries(
  Object.entries({
    operation: "modify",
    tag: "unsaved-tag",
    listen: "127.0.0.1",
    port: "24443",
    username: "unsaved-user",
    credential: "unsaved-credential",
    secondary_credential: "unsaved-secondary",
    method: "2022-blake3-aes-128-gcm",
    transport: "grpc",
    transport_path: "unsaved-service",
    tls_enabled: "1",
    certificate_path: "/unsaved/certificate.pem",
    private_key_path: "/unsaved/private-key.pem",
    reality_enabled: "0",
    reality_private_key: "",
    reality_public_key: "",
    reality_short_id: "",
    reality_server_name: "unsaved.example.test",
    reality_min_client_ver: "0.0.0",
    reality_mldsa65_seed: "",
    reality_mldsa65_verify: "",
    vless_decryption: "",
    vless_encryption: "",
    listener_routing_mark: "51820",
    listener_rule: "private-egress",
    listener_proxy: "upstream",
    snell_version: "5",
    snell_udp: "1",
    snell_reuse: "1",
    snell_obfs_mode: "shadow-tls",
    snell_obfs_host: "cover.example.test",
    snell_client_fingerprint: "chrome",
    sudoku_client_key: "client-private-key",
    sudoku_padding_min: "2",
    sudoku_padding_max: "18",
    sudoku_table_type: "prefer_entropy",
    sudoku_handshake_timeout: "15",
    sudoku_httpmask_enabled: "1",
    sudoku_httpmask_mode: "stream",
    sudoku_httpmask_tls: "1",
    sudoku_httpmask_host: "cdn.example.test:443",
    sudoku_httpmask_path_root: "qch",
    sudoku_multiplex: "on",
    target_address: "backend.example.test",
    target_port: "9443",
    network: "udp",
  }).map(([name, value]) => [name, { name, value }]),
);
const planFormListeners = new Map();
const planForm = {
  isConnected: true,
  elements: {
    namedItem(name) {
      return planControls[name] || null;
    },
  },
  addEventListener(type, listener) {
    const listeners = planFormListeners.get(type) || [];
    listeners.push(listener);
    planFormListeners.set(type, listeners);
  },
  async dispatch(type) {
    await Promise.all(
      (planFormListeners.get(type) || []).map((listener) =>
        listener({ currentTarget: this }),
      ),
    );
  },
};
const fakePlanButton = (textContent, dataset) => {
  const listeners = [];
  const attributes = new Map();
  return {
    attributes,
    dataset,
    disabled: false,
    textContent,
    addEventListener(type, listener) {
      if (type === "click") listeners.push(listener);
    },
    setAttribute(name, value) {
      attributes.set(name, value);
    },
    removeAttribute(name) {
      attributes.delete(name);
    },
    dispatchClick() {
      return Promise.all(
        listeners.map((listener) =>
          listener({ currentTarget: this, preventDefault: noop }),
        ),
      );
    },
  };
};
const planButton = fakePlanButton("重新生成参数", { regenerate: "all" });
const credentialPlanButton = fakePlanButton("生成", {
  regenerate: "credential",
  regenerateSuccess: "凭据已生成",
});
const realityKeyPlanButton = fakePlanButton("生成密钥对", {
  regenerate: "reality_private_key,reality_public_key",
  regenerateSuccess: "Reality 密钥对已生成",
});
const mldsaPlanButton = fakePlanButton("生成密钥对", {
  regenerate: "reality_mldsa65_seed",
  regenerateSuccess: "ML-DSA-65 密钥对已生成",
});
const deferredPlan = () => {
  let resolve;
  let reject;
  const promise = new Promise((accept, fail) => {
    resolve = accept;
    reject = fail;
  });
  return { promise, resolve, reject };
};
const planRequests = [];
const planNotifications = [];
const appliedPlans = [];
const pageState = {
  route: "#agent-config",
  node: "node-current",
  engine: "ss-rust",
  tab: "transport",
  expanded: true,
  scrollY: 720,
};
globalThis.FormData = class {
  constructor(form) {
    this.form = form;
  }

  get(name) {
    return this.form.elements.namedItem(name)?.value ?? null;
  }
};

try {
  const completePresetInput = readServerPlanInput(planForm, {
    key: "sudoku",
    requires_tls: false,
  });
  assert.equal(completePresetInput.listener_routing_mark, 51820);
  assert.equal(completePresetInput.snell_obfs_mode, "shadow-tls");
  assert.equal(completePresetInput.snell_reuse, true);
  assert.equal(completePresetInput.sudoku_client_key, "client-private-key");
  assert.equal(completePresetInput.sudoku_httpmask_mode, "stream");

  bindServerPlanRegeneration({
    form: planForm,
    buttons: [planButton, credentialPlanButton, realityKeyPlanButton, mldsaPlanButton],
    api: async (path, options) => {
      const pending = deferredPlan();
      planRequests.push({ path, options, pending });
      return pending.promise;
    },
    base: "/agents/node-current/configs/ss-rust",
    protocol: {
      key: "ss2022",
      requires_tls: false,
      uses_reality: false,
    },
    report: (...message) => planNotifications.push(message),
    onApplied: (plan) => appliedPlans.push(plan),
  });

  const firstClick = planButton.dispatchClick();
  assert.equal(planButton.disabled, true, "regeneration disables its button");
  assert.equal(credentialPlanButton.disabled, true);
  assert.equal(planButton.textContent, "生成中…");
  assert.equal(planButton.attributes.get("aria-busy"), "true");
  assert.equal(planRequests.length, 1);
  assert.equal(
    planRequests[0].path,
    "/agents/node-current/configs/ss-rust/plans",
    "regeneration stays scoped to the selected node and engine",
  );
  const firstPayload = JSON.parse(planRequests[0].options.body);
  assert.equal(
    firstPayload.input.method,
    "2022-blake3-aes-128-gcm",
    "regeneration uses the current unsaved method",
  );
  assert.equal(firstPayload.input.transport, "grpc");
  assert.equal(firstPayload.input.listen, "127.0.0.1");
  assert.equal(firstPayload.input.certificate_path, "/unsaved/certificate.pem");
  assert.equal(
    firstPayload.input.reality_min_client_ver,
    "0.0.0",
    "the editable minClientVer preserves the legacy preset default",
  );
  assert.equal(firstPayload.input.target_address, "backend.example.test");
  assert.equal(firstPayload.input.target_port, 9443);
  assert.equal(firstPayload.input.network, "udp");
  planRequests[0].pending.resolve({
    tag: "regenerated-tag",
    port: 35555,
    username: "regenerated-user",
    credential: "regenerated-credential",
    secondary_credential: "regenerated-secondary",
    transport_path: "regenerated-service",
    method: "server-default-method",
    transport: "websocket",
    listen: "0.0.0.0",
    certificate_path: "/server/default.pem",
    target_address: "127.0.0.1",
    target_port: 80,
    network: "tcp",
  });
  await firstClick;
  assert.equal(planControls.tag.value, "regenerated-tag");
  assert.equal(planControls.port.value, "35555");
  assert.equal(planControls.credential.value, "regenerated-credential");
  assert.equal(
    planControls.method.value,
    "2022-blake3-aes-128-gcm",
    "local updates preserve current selections",
  );
  assert.equal(planControls.transport.value, "grpc");
  assert.equal(planControls.listen.value, "127.0.0.1");
  assert.equal(planControls.operation.value, "modify");
  assert.equal(
    planControls.target_address.value,
    "backend.example.test",
    "regeneration preserves the selected forwarding target",
  );
  assert.equal(planControls.target_port.value, "9443");
  assert.equal(planControls.network.value, "udp");
  assert.deepEqual(pageState, {
    route: "#agent-config",
    node: "node-current",
    engine: "ss-rust",
    tab: "transport",
    expanded: true,
    scrollY: 720,
  });
  assert.equal(appliedPlans.length, 1);
  assert.equal(planButton.disabled, false);
  assert.equal(planButton.textContent, "重新生成参数");
  assert.equal(planButton.attributes.has("aria-busy"), false);

  const fullTag = planControls.tag.value;
  const fullPort = planControls.port.value;
  const credentialClick = credentialPlanButton.dispatchClick();
  assert.equal(planButton.disabled, true);
  assert.equal(credentialPlanButton.textContent, "生成中…");
  planRequests[1].pending.resolve({
    tag: "must-not-apply",
    port: 38888,
    credential: "credential-only-result",
  });
  await credentialClick;
  assert.equal(
    planControls.credential.value,
    "credential-only-result",
    "a field action updates its requested result",
  );
  assert.equal(planControls.tag.value, fullTag);
  assert.equal(planControls.port.value, fullPort);
  assert.deepEqual(planNotifications.at(-1), ["凭据已生成"]);
  assert.equal(credentialPlanButton.textContent, "生成");

  const previousShortID = planControls.reality_short_id.value;
  const realityKeyClick = realityKeyPlanButton.dispatchClick();
  planRequests[2].pending.resolve({
    reality_private_key: "regenerated-private-key",
    reality_public_key: "regenerated-public-key",
    reality_short_id: "must-not-apply",
  });
  await realityKeyClick;
  assert.equal(
    planControls.reality_private_key.value,
    "regenerated-private-key",
  );
  assert.equal(planControls.reality_public_key.value, "regenerated-public-key");
  assert.equal(
    planControls.reality_short_id.value,
    previousShortID,
    "Reality key generation updates the pair without changing Short ID",
  );

  const failedCredential = planControls.credential.value;
  const failedClick = planButton.dispatchClick();
  planRequests[3].pending.reject(new Error("generation unavailable"));
  await failedClick;
  assert.equal(
    planControls.credential.value,
    failedCredential,
    "a failed request preserves the current form",
  );
  assert.deepEqual(planNotifications.at(-1), [
    "生成参数失败：generation unavailable",
    "error",
  ]);
  assert.equal(planButton.disabled, false);

  const olderClick = planButton.dispatchClick();
  planControls.method.value = "2022-blake3-aes-256-gcm";
  await planForm.dispatch("change");
  const newerClick = planButton.dispatchClick();
  assert.equal(planRequests.length, 6, "rapid clicks can be ordered safely");
  assert.equal(
    JSON.parse(planRequests[5].options.body).input.method,
    "2022-blake3-aes-256-gcm",
    "the newer request captures the newer unsaved selection",
  );
  planRequests[5].pending.resolve({
    tag: "newer-tag",
    port: 36666,
    credential: "newer-credential",
  });
  await newerClick;
  assert.equal(planControls.credential.value, "newer-credential");
  assert.equal(
    planButton.disabled,
    true,
    "the button stays disabled until every pending request settles",
  );
  planRequests[4].pending.resolve({
    tag: "older-tag",
    port: 37777,
    credential: "older-credential",
  });
  await olderClick;
  assert.equal(
    planControls.credential.value,
    "newer-credential",
    "an out-of-order response cannot overwrite the newer result",
  );
  assert.equal(planControls.method.value, "2022-blake3-aes-256-gcm");
  assert.equal(planButton.disabled, false);

  const mldsaClick = mldsaPlanButton.dispatchClick();
  planRequests[6].pending.resolve({
    reality_mldsa65_seed: "generated-mldsa-seed",
    reality_mldsa65_verify: "server-derived-verify",
  });
  await mldsaClick;
  assert.equal(
    planControls.reality_mldsa65_seed.value,
    "generated-mldsa-seed",
    "ML-DSA generation applies only the persisted server seed",
  );
  assert.equal(
    planControls.reality_mldsa65_verify.value,
    "",
    "the UI does not retain a stale client verify value",
  );
  assert.deepEqual(planNotifications.at(-1), ["ML-DSA-65 密钥对已生成"]);
} finally {
  if (previousFormData === undefined) delete globalThis.FormData;
  else globalThis.FormData = previousFormData;
}

const cardSlots = [
  { left: 0, right: 100, top: 0, bottom: 160 },
  { left: 120, right: 220, top: 0, bottom: 160 },
  { left: 240, right: 340, top: 0, bottom: 160 },
  { left: 0, right: 100, top: 180, bottom: 340 },
  { left: 120, right: 220, top: 180, bottom: 340 },
];
const upperRightGripOffset = { x: 40, y: -60 };
const dropAt = (x, y) =>
  nodeCardDropIndex(cardSlots, { x, y }, upperRightGripOffset);

assert.equal(dropAt(90, 20), 0, "natural upper-half drop selects first slot");
assert.equal(dropAt(210, 20), 1, "natural upper-half drop selects middle slot");
assert.equal(dropAt(330, 20), 2, "natural upper-half drop selects last slot");
assert.equal(dropAt(90, 200), 3, "drop selection follows the actual grid row");
assert.equal(dropAt(210, 200), 4, "drop selection follows row direction");

const fakeCard = (left) => {
  const listeners = new Map();
  const classes = new Set();
  return {
    left,
    style: { transition: "", transform: "" },
    classList: {
      add: (...names) => names.forEach((name) => classes.add(name)),
      remove: (...names) => names.forEach((name) => classes.delete(name)),
      contains: (name) => classes.has(name),
    },
    forcedLayouts: 0,
    get offsetWidth() {
      this.forcedLayouts += 1;
      return 100;
    },
    getBoundingClientRect() {
      return { left: this.left, top: 0 };
    },
    addEventListener(type, listener) {
      listeners.set(type, listener);
    },
    removeEventListener(type, listener) {
      if (listeners.get(type) === listener) listeners.delete(type);
    },
    dispatchTransitionEnd() {
      listeners.get("transitionend")?.({
        target: this,
        propertyName: "transform",
      });
    },
    hasTransitionListener() {
      return listeners.has("transitionend");
    },
  };
};
const animationRuntime = () => {
  const frames = new Map();
  const timers = new Map();
  let nextID = 1;
  return {
    frames,
    timers,
    requestFrame(callback) {
      const id = nextID++;
      frames.set(id, callback);
      return id;
    },
    cancelFrame(id) {
      frames.delete(id);
    },
    setTimer(callback) {
      const id = nextID++;
      timers.set(id, callback);
      return id;
    },
    clearTimer(id) {
      timers.delete(id);
    },
    runFrame() {
      const pending = [...frames.values()];
      frames.clear();
      pending.forEach((callback) => callback());
    },
    runTimer() {
      const pending = [...timers.values()];
      timers.clear();
      pending.forEach((callback) => callback());
    },
  };
};

const landingCard = fakeCard(120);
const landingRuntime = animationRuntime();
const cancelLanding = animateNodeCardDrop(
  [landingCard],
  new Map([[landingCard, { left: 20, top: 0 }]]),
  landingRuntime,
);
assert.equal(landingCard.style.transition, "none");
assert.equal(landingCard.style.transform, "translate(-100px, 0px)");
assert.equal(landingCard.forcedLayouts, 1);
assert.equal(landingRuntime.frames.size, 1);
landingRuntime.runFrame();
assert.equal(landingCard.style.transition, "");
assert.equal(landingCard.style.transform, "");
assert.equal(landingRuntime.timers.size, 1);
assert.equal(landingCard.hasTransitionListener(), true);
landingCard.dispatchTransitionEnd();
assert.equal(landingRuntime.timers.size, 0);
assert.equal(landingCard.hasTransitionListener(), false);
assert.equal(landingCard.style.transition, "");
assert.equal(landingCard.style.transform, "");

const interruptedCard = fakeCard(120);
const interruptedRuntime = animationRuntime();
const interruptLanding = animateNodeCardDrop(
  [interruptedCard],
  new Map([[interruptedCard, { left: 20, top: 0 }]]),
  interruptedRuntime,
);
interruptLanding();
assert.equal(interruptedRuntime.frames.size, 0);
assert.equal(interruptedCard.hasTransitionListener(), false);
assert.equal(interruptedCard.style.transition, "");
assert.equal(interruptedCard.style.transform, "");
assert.equal(interruptedCard.forcedLayouts, 2);

const timedOutCard = fakeCard(120);
const timedOutRuntime = animationRuntime();
animateNodeCardDrop(
  [timedOutCard],
  new Map([[timedOutCard, { left: 20, top: 0 }]]),
  timedOutRuntime,
);
timedOutRuntime.runFrame();
timedOutRuntime.runTimer();
assert.equal(timedOutCard.hasTransitionListener(), false);
assert.equal(timedOutCard.style.transition, "");
assert.equal(timedOutCard.style.transform, "");
cancelLanding();

const releaseCard = fakeCard(0);
const releaseTarget = fakeCard(120);
releaseCard.classList.add("dragging");
releaseTarget.classList.add("drop-target");
releaseCard.style.order = "1";
releaseCard.style.transition = "none";
releaseCard.style.transform = "translate(-100px, 0px)";
let ghostRemoved = false;
const dragBody = fakeCard(0);
dragBody.classList.add("node-card-dragging");
const dragGrid = {
  querySelectorAll: () => [releaseCard, releaseTarget],
};
clearNodeCardDragState(
  dragGrid,
  { card: releaseCard, ghost: { remove: () => (ghostRemoved = true) } },
  { clearAnimationStyles: false, body: dragBody },
);
assert.equal(releaseCard.classList.contains("dragging"), false);
assert.equal(releaseTarget.classList.contains("drop-target"), false);
assert.equal(dragBody.classList.contains("node-card-dragging"), false);
assert.equal(ghostRemoved, true);
assert.equal(releaseCard.style.order, "");
assert.equal(releaseCard.style.transition, "none");
assert.equal(releaseCard.style.transform, "translate(-100px, 0px)");
clearNodeCardDragState(
  dragGrid,
  { card: releaseCard },
  { clearAnimationStyles: true, body: dragBody },
);
assert.equal(releaseCard.style.transition, "");
assert.equal(releaseCard.style.transform, "");

const previousDocument = globalThis.document;
const previousDetailsElement = globalThis.HTMLDetailsElement;
const previousCSS = globalThis.CSS;
class PresetWorkspace {
  constructor(agentID) {
    this.dataset = { agentNode: agentID };
    this.id = `preset-node-${agentID}`;
  }
  querySelector() {
    return null;
  }
  querySelectorAll() {
    return [];
  }
}

const presetEngines = ["mihomo", "xray", "sing-box", "ss-rust"];
const presetAgents = [
  {
    id: "alpha",
    name: "Alpha",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: presetEngines,
    runtime: Object.fromEntries(
      presetEngines.map((engine) => [
        engine,
        { installed: true, service_status: "active", version: "1.0.0" },
      ]),
    ),
  },
  {
    id: "beta",
    name: "Beta",
    os: "linux",
    arch: "arm64",
    status: "online",
    capabilities: presetEngines,
    runtime: Object.fromEntries(
      presetEngines.map((engine) =>
        engine === "sing-box"
          ? [
              engine,
              {
                installed: false,
                existing_config_available: false,
                existing_config_unsupported_reason: "unsupported wrapper",
              },
            ]
          : [
              engine,
              { installed: true, service_status: "active", version: "1.0.0" },
            ],
      ),
    ),
  },
  {
    id: "gamma",
    name: "Gamma",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: presetEngines,
    features: ["mihomo-development-source-v1"],
    runtime: Object.fromEntries(
      presetEngines.map((engine) => [
        engine,
        {
          installed: true,
          service_status: "active",
          version: "1.0.0",
          ...(engine === "xray" ? { existing_config_available: true } : {}),
        },
      ]),
    ),
  },
];
const presetState = {
  route: "agents",
  anchor: "agents",
  data: { selectedAgent: "" },
};
const presetLinks = presetAgents.map((agent) => ({
  dataset: { contextAgent: agent.id },
  href: `#node-${agent.id}`,
}));
let presetWorkspaces = [];
let presetMarkup = "";

globalThis.HTMLDetailsElement = class {};
globalThis.CSS = { escape: (value) => String(value) };
globalThis.document = {
  querySelector: () => null,
  querySelectorAll(selector) {
    if (selector === ".preset-node-workspace") return presetWorkspaces;
    if (selector === "[data-context-agent]") return presetLinks;
    if (
      selector ===
      ".preset-node-workspace, .machine-workspace, .node-operations-workspace"
    )
      return presetWorkspaces;
    return [];
  },
};

try {
  const presetShell = (markup) => {
    presetMarkup = markup;
    presetWorkspaces = [
      ...markup.matchAll(
        /<section class="preset-node-workspace[^>]*data-agent-node="([^"]+)"/g,
      ),
    ].map((match) => new PresetWorkspace(match[1]));
  };
  const presetCtx = new Proxy(
    {
      state: presetState,
      engines: presetEngines,
      api: async (path) => {
        if (path === "/agents") return presetAgents;
        if (path.endsWith("/configs")) return [];
        assert.fail(`unexpected preset smoke API path ${path}`);
      },
      optionalAPI: async () => null,
      can: (capability) =>
        capability === "agent-config.read" || capability === "overview.read",
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      serviceStatusName: (value) => value,
      statusTone: (value) => value,
      conciseVersion: (_engine, value) => value,
      shell: presetShell,
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const { agents: renderPresetAgents } = installAgents(presetCtx);
  await renderPresetAgents({ overview: { agents: 2, agents_online: 2 } });

  const assertFocusedPreset = (selected, excluded) => {
    assert.equal(presetState.route, "agents", "preset selection stays on agents");
    assert.deepEqual(
      presetWorkspaces.map((workspace) => workspace.id),
      [`preset-node-${selected}`],
      "only the selected preset workspace enters the main DOM",
    );
    assert.equal(
      presetMarkup.includes(`data-agent-node="${excluded}"`),
      false,
      "unselected node content stays out of the main DOM",
    );
    assert.equal(
      presetMarkup.includes('class="machine-workspace"'),
      false,
      "focused preset content has no node accordion",
    );
    assert.equal(
      presetMarkup.includes('class="machine-header"'),
      false,
      "focused preset content has no node summary header",
    );
    assert.equal(
      presetMarkup.includes('class="node-page-intro"'),
      false,
      "focused preset content has no redundant page introduction",
    );
    assert.equal(
      presetMarkup.includes("data-development-source"),
      true,
      "preset version drawer exposes the Mihomo development source fieldset",
    );
    assert.equal(
      presetMarkup.includes('value="mirror" disabled'),
      true,
      "legacy Agent mirror stays disabled",
    );
    assert.equal(
      presetMarkup.includes("source-upgrade-note"),
      true,
      "legacy Agent shows the upgrade-source explanation",
    );
    assert.equal(
      (presetMarkup.match(/<article class="service-card service-/g) || [])
        .length,
      presetEngines.length,
      "focused preset content keeps all engine cards",
    );
    assert.equal(
      (presetMarkup.match(new RegExp(`data-config="${selected}"`, "g")) || [])
        .length,
      presetEngines.length,
      "focused preset content keeps each engine configuration action",
    );
    assert.equal(presetMarkup.includes("节点内核"), true);
  };

  assert.equal(
    presetState.data.selectedAgent,
    "alpha",
    "first preset visit selects the first node",
  );
  assertFocusedPreset("alpha", "beta");
  assert.equal(presetState.route, "agents", "preset selection stays on agents");
  assert.deepEqual(
    presetLinks.map((link) => link.href),
    ["#preset-node-alpha", "#preset-node-beta", "#preset-node-gamma"],
    "preset sidebar links target per-node preset anchors",
  );
  assert.equal(
    presetLinks.some((link) => link.href.startsWith("#settings-node-")),
    false,
    "preset sidebar never enters node settings",
  );

  presetState.anchor = "preset-node-beta";
  presetState.data.selectedAgent = "beta";
  await renderPresetAgents({ overview: { agents: 2, agents_online: 2 } });
  assertFocusedPreset("beta", "alpha");
  assert.equal(
    presetMarkup.includes('data-config="beta" data-engine="sing-box"'),
    true,
    "unsupported preset engine keeps a config entry",
  );
  assert.equal(
    presetMarkup.includes("查看现有服务不可导入原因"),
    true,
    "unsupported preset engine still explains why its optional import is unavailable",
  );

  presetState.anchor = "preset-node-gamma";
  presetState.data.selectedAgent = "gamma";
  await renderPresetAgents({ overview: { agents: 3, agents_online: 3 } });
  assert.equal(
    presetWorkspaces.map((workspace) => workspace.id)[0],
    "preset-node-gamma",
    "feature-capable preset node is selected",
  );
  assert.equal(
    presetMarkup.includes('value="mirror" disabled'),
    false,
    "source-capable Agent mirror is not disabled",
  );
  assert.equal(
    presetMarkup.includes("source-upgrade-note"),
    false,
    "source-capable Agent hides the upgrade-source explanation",
  );
  assert.equal(
    presetMarkup.includes('data-config="gamma" data-engine="xray"'),
    true,
    "an optional import does not replace the managed configuration action",
  );
  assert.equal(
    presetMarkup.includes(
      'data-manual-agent="gamma" data-manual-engine="xray" aria-label="导入现有服务"',
    ),
    true,
    "an installed managed core keeps a separate optional import action",
  );
  assert.equal(
    presetMarkup.includes("请先手动导入"),
    false,
    "optional imports never become an installation prerequisite",
  );
  assert.equal(
    presetMarkup.includes('value="mirror"'),
    true,
    "source-capable Agent exposes the mirror option",
  );
  assert.equal(
    presetMarkup.includes("data-development-source"),
    true,
    "source-capable Agent still exposes the source fieldset",
  );
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
  if (previousDetailsElement === undefined) delete globalThis.HTMLDetailsElement;
  else globalThis.HTMLDetailsElement = previousDetailsElement;
  if (previousCSS === undefined) delete globalThis.CSS;
  else globalThis.CSS = previousCSS;
}

const routeDataDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const dashboardCalls = [];
  let dashboardMarkup = "";
  const dashboardState = {
    route: "dashboard",
    data: { dashboardTrafficMonth: "2026-08" },
  };
  const renderDashboard = installDashboard(
    new Proxy(
      {
        state: dashboardState,
        api: async (path) => {
          dashboardCalls.push(path);
          if (path === "/agents" || path.startsWith("/tasks?")) return [];
          if (path === "/traffic-usage?month=2026-08")
            return { days: [{ day: "2026-08-27", received_bytes: 6, sent_bytes: 4, used_bytes: 10, peak_receive_bps: 2, peak_send_bps: 1 }] };
          assert.fail(`unexpected dashboard preload API path ${path}`);
        },
        can: (capability) => capability === "traffic.read",
        esc: (value) => String(value ?? ""),
        bytes: (value) => `${value || 0} B`,
        rate: (value) => `${value || 0} B/s`,
        shell: (markup) => { dashboardMarkup = markup; },
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  await renderDashboard({
    overview: { agents: 3, agents_online: 3, tasks_pending: 0 },
  });
  assert.deepEqual(
    dashboardCalls,
    ["/agents", "/tasks?limit=7", "/traffic-usage?month=2026-08"],
    "dashboard reuses the route bootstrap overview",
  );
  assert.equal(dashboardMarkup.includes('id="traffic-usage"'), true, "dashboard owns the monthly traffic chart");
  assert.equal(dashboardMarkup.includes('data-dashboard-traffic-month'), true, "dashboard traffic history can change month");
  assert.equal(dashboardMarkup.includes('data-dashboard-traffic-details'), true, "dashboard daily traffic opens from a dedicated action");
  assert.equal(dashboardMarkup.includes('data-dashboard-traffic-dialog'), true, "dashboard daily traffic is rendered in a modal dialog");
  assert.equal(dashboardMarkup.includes('class="dashboard-traffic-axis"'), true, "dashboard chart keeps dates on a stable external axis");
  assert.equal(dashboardMarkup.includes("31日"), true, "dashboard chart labels natural days explicitly");
  assert.equal(dashboardMarkup.includes('class="dashboard-month-picker"'), true, "dashboard uses a theme-native month picker");
  assert.equal(dashboardMarkup.includes('type="month"'), false, "dashboard does not open the browser-native month panel");
  assert.equal(dashboardMarkup.includes("<details class=\"traffic-daily\""), false, "dashboard no longer expands daily history inline");
  assert.equal(dashboardMarkup.includes("2026-08-27"), true, "dashboard renders persisted daily traffic details");
  assert.equal(dashboardTrafficMonthDays("2024-02").length, 29, "dashboard traffic month helper observes leap years");
  assert.deepEqual(
    aggregateDashboardTrafficDays([
      { day: "2026-08-01", received_bytes: 4, sent_bytes: 3, used_bytes: 7, peak_receive_bps: 2 },
      { day: "2026-08-01", received_bytes: 5, sent_bytes: 2, used_bytes: 7, peak_receive_bps: 8 },
    ], "2026-08")[0],
    { day: "2026-08-01", received_bytes: 9, sent_bytes: 5, used_bytes: 14, peak_receive_bps: 8, peak_send_bps: 0 },
    "same-day policy rows are aggregated for the dashboard chart",
  );

  const taskCalls = [];
  const taskTimers = new Map();
  let nextTaskTimer = 1;
  let taskNow = 1_000;
  const taskState = { route: "tasks", data: {} };
  const renderTasks = installTasks(
    new Proxy(
      {
        state: taskState,
        actions: [],
        api: async (path) => {
          taskCalls.push(path);
          if (path === "/agents" || path.startsWith("/tasks?")) return [];
          if (path === "/settings") return { task_poll_interval_ms: 600 };
          assert.fail(`unexpected task polling API path ${path}`);
        },
        shell: noop,
        setTimer: (callback) => {
          const id = nextTaskTimer++;
          taskTimers.set(id, callback);
          return id;
        },
        clearTimer: (id) => taskTimers.delete(id),
        now: () => taskNow,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  await renderTasks({ settings: { task_poll_interval_ms: 600 } });
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    0,
    "initial task render reuses bootstrap settings",
  );
  assert.equal(taskTimers.size, 1, "task page schedules one polling timer");
  const poll = [...taskTimers.values()][0];
  taskTimers.clear();
  await poll();
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    0,
    "background task polling does not refetch unchanged settings",
  );
  assert.equal(
    taskCalls.filter((path) => path.startsWith("/tasks?")).length,
    2,
    "each task refresh issues one task request",
  );
  assert.equal(
    taskCalls.filter((path) => path === "/agents").length,
    2,
    "each task refresh issues one agent request",
  );
  assert.equal(taskTimers.size, 1, "task polling keeps a single timer");
  const expiredPoll = [...taskTimers.values()][0];
  taskTimers.clear();
  taskNow += 30_001;
  await expiredPoll();
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    1,
    "task polling refreshes cached settings after the bounded interval",
  );
  assert.equal(taskTimers.size, 1, "expired settings refresh keeps one timer");
  taskTimers.clear();
} finally {
  if (routeDataDocument === undefined) delete globalThis.document;
  else globalThis.document = routeDataDocument;
}

const accessEntries = [
  {
    agent_id: "alpha",
    agent_name: "Alpha node",
    engine: "mihomo",
    address: "alpha.example.test",
    source: "test",
    profiles: [
      {
        tag: "alpha-in",
        protocol: "test",
        profile: { format: "URI", uri: "test-alpha", fields: [] },
      },
    ],
    address_options: [
      { family: "ipv4", address: "198.51.100.10", source: "IPv4", profiles: [] },
      { family: "ipv6", address: "2001:db8::10", source: "IPv6", profiles: [] },
    ],
  },
  {
    agent_id: "beta",
    agent_name: "Beta node",
    engine: "xray",
    address: "beta.example.test",
    source: "test",
    profiles: [
      {
        tag: "beta-in",
        protocol: "test",
        profile: { format: "URI", uri: "test-beta", fields: [] },
      },
    ],
  },
];
assert.deepEqual(
  clientAccessAddressChoices(accessEntries[0]).map((choice) => choice.value),
  ["auto", "ipv4", "ipv6"],
  "client configuration exposes both address families",
);
assert.equal(
  clientAccessEntryForAddress(accessEntries[0], "ipv6").address,
  "2001:db8::10",
  "client configuration switches to the IPv6 profile variant",
);
const accessAgents = [
  { id: "alpha", name: "Alpha node", labels: {}, status: "online" },
  { id: "beta", name: "Beta node", labels: {}, status: "online" },
];
assert.deepEqual(
  normalizeClientAccessFilters(accessEntries, accessAgents, {
    agent: "removed-node",
    engine: "xray",
    query: "  BETA  ",
  }),
  { agent: "", engine: "xray", query: "BETA" },
  "deleted node selections are discarded while valid global filters remain",
);
assert.deepEqual(
  normalizeClientAccessFilters(accessEntries, accessAgents, {
    agent: "alpha",
    engine: "xray",
  }),
  { agent: "alpha", engine: "", query: "" },
  "an engine unavailable on the selected node is discarded",
);
assert.deepEqual(
  filterClientAccessEntries(accessEntries, { query: "BETA-IN" }).map(
    (entry) => entry.agent_id,
  ),
  ["beta"],
  "client search covers profile tags case-insensitively",
);
assert.deepEqual(
  groupClientAccessEntries([
    accessEntries[0],
    { ...accessEntries[0], engine: "sing-box" },
    accessEntries[1],
  ]).map((group) => [group.agent_id, group.entries.length]),
  [
    ["alpha", 2],
    ["beta", 1],
  ],
  "multiple engine exports for one node share one node card",
);
let copiedClientValue = "";
assert.equal(
  await copyClientValue(
    { value: "ss://demo" },
    {
      navigatorObject: {
        clipboard: { writeText: async (value) => (copiedClientValue = value) },
      },
    },
  ),
  "clipboard",
);
assert.equal(copiedClientValue, "ss://demo");
let legacyCopySelected = false;
let legacyCopyRestored = false;
const legacyCopyInput = {
  value: "ss://legacy",
  selectionStart: 1,
  selectionEnd: 3,
  selectionDirection: "forward",
  focus: noop,
  select: () => (legacyCopySelected = true),
  setSelectionRange: (start, end, direction) => {
    legacyCopyRestored = start === 1 && end === 3 && direction === "forward";
  },
};
assert.equal(
  await copyClientValue(legacyCopyInput, {
    navigatorObject: {},
    documentObject: {
      activeElement: null,
      execCommand: (command) => command === "copy" && legacyCopySelected,
    },
  }),
  "legacy",
);
assert.equal(legacyCopyRestored, true);
const accessState = {
  route: "client-access",
  data: { accessAgent: "beta", accessEngine: "", accessQuery: "" },
};
const accessSidebarLinks = [
  { dataset: { accessAgent: "" }, onclick: null },
  { dataset: { accessAgent: "alpha" }, onclick: null },
  { dataset: { accessAgent: "beta" }, onclick: null },
];
let accessMarkup = "";
let accessAPICalls = 0;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll(selector) {
    if (selector === "[data-access-agent]") return accessSidebarLinks;
    return [];
  },
};

try {
  const accessCtx = new Proxy(
    {
      state: accessState,
      engines: ["mihomo", "xray"],
      api: async (path) => {
        accessAPICalls += 1;
        if (path === "/client-access") return accessEntries;
        if (path === "/agents") return accessAgents;
        assert.fail(`unexpected client access smoke API path ${path}`);
      },
      can: (capability) => capability === "agents.read" || capability === "agents.manage",
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      short: (value) => value,
      shell: (markup) => {
        accessMarkup = markup;
      },
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const renderClientAccess = installClientAccess(accessCtx);
  await renderClientAccess();
  assert.equal(accessAPICalls, 2);

  assert.equal(accessMarkup.includes("Alpha node"), false);
  assert.equal(accessMarkup.includes("Beta node"), true);
  assert.equal(accessMarkup.includes("data-filter-agent"), false);
  assert.equal(accessMarkup.includes("按节点筛选"), false);
  assert.equal(accessMarkup.includes('data-filter-engine=""'), true);
  assert.equal(accessMarkup.includes("按内核筛选"), true);
  assert.equal(accessMarkup.includes("client-access-toolbar"), true);
  assert.equal(accessMarkup.includes("client-access-node-card"), true);
  assert.equal(accessMarkup.includes("修改显示参数"), true);
  assert.equal(accessMarkup.includes("client-display-dialog"), true);
  assert.equal(accessMarkup.includes("客户端地址协议栈"), false);
  assert.equal(accessMarkup.includes("client-address-editor"), false);
  assert.equal(accessMarkup.includes("data-client-parameter-open"), true);
  assert.equal(accessMarkup.includes("client-parameter-dialog"), true);
  assert.equal(accessMarkup.includes("traffic-edit-dialog"), true);
  assert.equal(accessMarkup.includes("client-parameter-menu"), false);
  assert.equal(accessMarkup.includes("client-access-hero"), false);
  assert.equal(accessMarkup.includes("client-access-filter-panel"), false);
  assert.equal(
    accessSidebarLinks.every((link) => typeof link.onclick === "function"),
    true,
    "context sidebar remains the executable node filter",
  );

  await accessSidebarLinks[0].onclick({ preventDefault: noop });
  assert.equal(accessState.data.accessAgent, "");
  assert.equal(accessMarkup.includes("Alpha node"), true);
  assert.equal(accessMarkup.includes("Beta node"), true);
  assert.equal(accessMarkup.includes("客户端地址协议栈"), true);
  assert.equal(
    accessAPICalls,
    2,
    "switching the local node filter does not refetch client access data",
  );

  accessState.data.accessEngine = "xray";
  await renderClientAccess();
  assert.equal(accessMarkup.includes("Alpha node"), false);
  assert.equal(accessMarkup.includes("Beta node"), true);
  assert.equal(accessAPICalls, 4, "an explicit page refresh still reloads both APIs");

  await accessSidebarLinks[1].onclick({ preventDefault: noop });
  assert.equal(accessState.data.accessAgent, "alpha");
  assert.equal(accessState.data.accessEngine, "");
  assert.equal(accessMarkup.includes("Alpha node"), true);
  assert.equal(accessMarkup.includes("Beta node"), false);
  assert.equal(
    accessAPICalls,
    4,
    "node changes normalize incompatible engine filters locally",
  );
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}

const subStoreProfiles = [
  {
    agent_id: "alpha",
    agent_name: "Alpha node",
    engine: "sing-box",
    profile_tag: "vless-in",
    protocol: "VLESS",
    port: 443,
    default_name: "Alpha node · vless-in",
    custom_name: "Tokyo Premium",
    selected: true,
    available: true,
  },
  {
    agent_id: "beta",
    agent_name: "Beta node",
    engine: "xray",
    profile_tag: "ss-in",
    protocol: "Shadowsocks 2022",
    port: 8443,
    default_name: "Beta node · ss-in",
    selected: false,
    available: true,
  },
];
assert.deepEqual(
  filterSubStoreProfiles(subStoreProfiles, "alpha", "premium"),
  [subStoreProfiles[0]],
  "Sub-Store node and keyword filters compose locally",
);
assert.deepEqual(
  filterSubStoreProfiles(subStoreProfiles, "", "8443"),
  [subStoreProfiles[1]],
  "Sub-Store profiles can be searched by listening port",
);
assert.deepEqual(subStoreSelectionPayload(subStoreProfiles), [
  {
    agent_id: "alpha",
    engine: "sing-box",
    profile_tag: "vless-in",
    custom_name: "Tokyo Premium",
    address_mode: "auto",
  },
]);
assert.deepEqual(
  subStoreAddressChoices({
    addresses: [
      { family: "ipv4", address: "198.51.100.10" },
      { family: "ipv6", address: "2001:db8::10" },
    ],
  }).map((choice) => choice.value),
  ["auto", "ipv4", "ipv6", "both"],
  "dual-stack Sub-Store profiles expose automatic, family, and both-address modes",
);
assert.equal(
  subStoreProfileNodeCount({ selected: true, available: true, address_mode: "both" }),
  2,
  "dual-stack Sub-Store selections count both generated nodes",
);
const previousSubStoreDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  let subStoreMarkup = "";
  const subStoreState = { route: "substore-sync", navigationEpoch: 1, data: {} };
  const renderSubStore = installSubStoreSync({
    state: subStoreState,
    api: async (path) => {
      assert.equal(path, "/substore-sync");
      return {
        settings: {
          configured: true,
          endpoint_hint: "https://substore.example/••••••",
        },
        targets: [{
          id: "sst_primary",
          display_name: "Primary review",
          subscription_name: "QControlHub",
          sync_mode: "incremental",
          selection_count: 1,
          last_sync_status: "success",
          last_synced_at: "2026-08-28T00:00:00Z",
        }],
        target_id: "sst_primary",
        profiles: subStoreProfiles.map((profile) => ({ ...profile })),
        selections: subStoreSelectionPayload(subStoreProfiles),
      };
    },
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    notify: noop,
    shell: (markup) => {
      subStoreMarkup = markup;
    },
  });
  await renderSubStore();
  assert.equal(subStoreMarkup.includes("substore-status-bar"), true);
  assert.equal(subStoreMarkup.includes("substore-agent-card"), true);
  assert.equal(subStoreMarkup.includes("Tokyo Premium"), true);
  assert.equal(subStoreMarkup.includes("VLESS · :443"), true);
  assert.equal(subStoreMarkup.includes("data-substore-target-add"), true);
  assert.equal(subStoreMarkup.includes("data-substore-settings-dialog"), true);
  assert.equal(subStoreMarkup.includes("仅修改面板名称"), true);
  assert.equal(subStoreMarkup.includes("同时修改远端组名"), true);
  assert.equal(subStoreMarkup.includes("增量模式"), true);
  assert.equal(subStoreMarkup.includes("完全托管模式"), true);
  assert.equal(subStoreMarkup.includes("substore-node-settings-row"), true);
  assert.equal(subStoreMarkup.includes("Sub-Store 已有组"), true);
  assert.equal(subStoreMarkup.includes("仅移除面板中的同步关系"), true);
  assert.equal(subStoreMarkup.includes("substore-hero"), false);
  assert.equal(subStoreMarkup.includes("<h1"), false, "sync page does not repeat a large title hero");
} finally {
  if (previousSubStoreDocument === undefined) delete globalThis.document;
  else globalThis.document = previousSubStoreDocument;
}

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
  let coreEntries = [{ id: 1, agent_id: "alpha", engine: "mihomo", level: "info", message: "ready", logged_at: "now" }];
  let coreAgents = [{ id: "alpha", name: "Alpha" }];
  const coreState = {
    route: "core-logs",
    navigationEpoch: 1,
    data: {},
  };
  const renderCoreLogs = installCoreLogs({
    state: coreState,
    engines: ["mihomo"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    api: async (path) => {
      coreRequests += 1;
      if (path.startsWith("/core-logs?") && coreFailure)
        throw new Error("temporary log failure");
      if (path.startsWith("/core-logs?")) return coreEntries;
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
  assert.equal(coreRenders, 2, "a log error preserves the current view");
  assert.equal(coreTimers.size, 1, "a log error keeps recovery polling alive");
  coreFailure = false;
  const recoveredCorePoll = [...coreTimers.values()][0];
  coreTimers.clear();
  await recoveredCorePoll();
  assert.equal(coreRenders, 3, "log polling recovers without clearing state");
  assert.equal(coreTimers.size, 1);
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

// The preset page compacts physical DOM only, never the install/version drawer.
{
  const compactDocument = globalThis.document;
  const { compactPresetPage: runCompactPresetPage } = installAgents(
    new Proxy(
      { state: { route: "agents", data: {} } },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const counts = {
    enrollment: 0,
    batch: 0,
    summary: 0,
    state: 0,
    inspector: 0,
    footer: 0,
    unavailable: 0,
    upgrade: 0,
    batchLabel: 0,
    drawer: 0,
    toggle: 0,
  };
  const tracked = (name) => ({
    remove: () => {
      counts[name] += 1;
    },
  });
  const workspace = {
    dataset: { agentNode: "gamma" },
    querySelector(selector) {
      if (selector === ".machine-resource-summary") return tracked("summary");
      if (selector === ".machine-state") return tracked("state");
      if (selector === ".node-inspector") return tracked("inspector");
      if (selector === ".machine-footer") return tracked("footer");
      if (selector === ".runtime-drawer") return tracked("drawer");
      if (selector === ".service-version-toggle") return tracked("toggle");
      return null;
    },
    querySelectorAll(selector) {
      if (selector === ".service-management-unavailable, [data-upgrade-agent]") {
        return [tracked("unavailable"), tracked("upgrade")];
      }
      if (selector === "[data-batch-checkbox]") {
        const label = tracked("batchLabel");
        return [{ closest: () => label }];
      }
      return [];
    },
  };
  globalThis.document = {
    querySelector(selector) {
      if (selector === "#enrollment") return tracked("enrollment");
      if (selector === "#batch-form") return tracked("batch");
      return null;
    },
    querySelectorAll(selector) {
      if (selector === ".preset-node-workspace") return [workspace];
      return [];
    },
  };
  try {
    runCompactPresetPage();
    assert.equal(counts.drawer, 0, "compact keeps the preset version drawer");
    assert.equal(counts.toggle, 0, "compact keeps the preset version toggle");
    assert.equal(counts.enrollment, 1, "compact removes the enrollment sheet");
    assert.equal(counts.batch, 1, "compact removes the batch form");
    assert.equal(counts.summary, 1, "compact removes the resource summary");
    assert.equal(counts.state, 1, "compact removes the machine state");
    assert.equal(counts.inspector, 1, "compact removes the node inspector");
    assert.equal(counts.footer, 1, "compact removes the machine footer");
    assert.equal(counts.unavailable, 1, "compact removes unavailable-service block");
    assert.equal(counts.upgrade, 1, "compact removes the upgrade-agent block");
    assert.equal(counts.batchLabel, 1, "compact removes the batch checkbox label");
  } finally {
    if (compactDocument === undefined) delete globalThis.document;
    else globalThis.document = compactDocument;
  }
}

// The preset version form binds the real reveal and source-carrying payload path.
{
  const presetDomDocument = globalThis.document;
  const presetDomFormData = globalThis.FormData;
  const presetDomDetails = globalThis.HTMLDetailsElement;
  const presetDomCSS = globalThis.CSS;

  const radio = (name, value, checked = false) => {
    const listeners = [];
    return {
      name,
      value,
      checked,
      addEventListener(type, listener) {
        if (type === "change") listeners.push(listener);
      },
      fireChange() {
        listeners.forEach((listener) => listener({ target: this }));
      },
    };
  };
  const checkedOf = (radios) => radios.find((item) => item.checked);

  const makeVersionForm = ({ agent, canMirror }) => {
    const channels = ["stable", "development", "custom"].map((value, index) =>
      radio("release_channel", value, index === 0),
    );
    const sources = ["official", "mirror"].map((value) =>
      radio("core_source", value, value === "official"),
    );
    const fieldset = { hidden: true };
    const form = {
      dataset: { versionEngine: "mihomo", versionAgent: agent.id },
      elements: {
        namedItem(name) {
          if (name === "release_channel") {
            return { value: checkedOf(channels).value };
          }
          if (name === "core_source") {
            return { value: checkedOf(sources).value };
          }
          if (name === "custom_version") return { value: "" };
          return null;
        },
      },
      querySelector(selector) {
        if (selector === ".custom-version-field") return null;
        if (selector === "[data-development-source]") return fieldset;
        if (selector === 'input[name="release_channel"]:checked') {
          return checkedOf(channels);
        }
        return null;
      },
      querySelectorAll(selector) {
        if (selector === 'input[name="release_channel"]') return channels;
        return [];
      },
      setChannel(value) {
        const current = channels.find((item) => item.value === value);
        channels.forEach((item) => {
          item.checked = item === current;
        });
        current.fireChange();
      },
      setSource(value) {
        sources.forEach((item) => {
          item.checked = item.value === value;
        });
      },
      fieldset,
    };
    return form;
  };

  const agent = {
    id: "gamma",
    name: "Gamma",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["mihomo"],
    features: ["mihomo-development-source-v1"],
    runtime: {
      mihomo: { installed: true, service_status: "active", version: "1.0.0" },
    },
  };
  const versionForm = makeVersionForm({ agent, canMirror: true });
  const taskBodies = [];

  globalThis.HTMLDetailsElement = class {};
  globalThis.CSS = { escape: (value) => String(value) };
  globalThis.FormData = class {
    constructor(form) {
      this.form = form;
    }
    get(name) {
      return this.form.elements.namedItem(name)?.value ?? null;
    }
  };
  globalThis.document = {
    querySelector: () => null,
    querySelectorAll(selector) {
      if (selector === ".core-version-form") return [versionForm];
      if (selector === ".preset-node-workspace") {
        return [
          {
            dataset: { agentNode: "gamma" },
            querySelector: () => null,
            querySelectorAll: () => [],
          },
        ];
      }
      if (
        selector ===
        ".preset-node-workspace, .machine-workspace, .node-operations-workspace"
      ) {
        return [
          {
            dataset: { agentNode: "gamma" },
            querySelector: () => null,
            querySelectorAll: () => [],
          },
        ];
      }
      return [];
    },
  };

  try {
    const state = {
      route: "agents",
      anchor: "agents",
      data: { selectedAgent: "gamma" },
    };
    const ctx = new Proxy(
      {
        state,
        engines: ["mihomo"],
        api: async (path, options) => {
          if (path === "/agents") return [agent];
          if (path === "/deployments") return [];
          if (path === "/client-access") return [];
          if (path === "/overview") return { agents: 1, agents_online: 1 };
          if (path === "/agents/gamma/configs") return [];
          if (path === "/tasks") {
            taskBodies.push(options?.body);
            return { id: "task-1" };
          }
          assert.fail(`unexpected preset interaction API path ${path}`);
        },
        optionalAPI: async () => null,
        can: () => true,
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        serviceStatusName: (value) => value,
        statusTone: (value) => value,
        conciseVersion: (_engine, value) => value,
        confirmAction: async () => true,
        shell: noop,
      },
      { get: (target, key) => target[key] ?? noop },
    );
    const { agents: renderPresetInteraction } = installAgents(ctx);
    await renderPresetInteraction({ overview: { agents: 1, agents_online: 1 } });

    assert.equal(
      versionForm.fieldset.hidden,
      true,
      "stable channel keeps the development source fieldset hidden",
    );
    versionForm.setChannel("development");
    assert.equal(
      versionForm.fieldset.hidden,
      false,
      "dev channel reveals the development source fieldset",
    );
    versionForm.setChannel("stable");
    assert.equal(
      versionForm.fieldset.hidden,
      true,
      "returning to stable hides the development source fieldset",
    );

    versionForm.setChannel("development");
    versionForm.setSource("official");
    await versionForm.onsubmit({ preventDefault: noop });
    let payload = JSON.parse(taskBodies.at(-1));
    assert.equal(payload.engine, "mihomo", "payload targets mihomo");
    assert.equal(payload.core_version, "development", "payload carries dev channel");
    assert.equal(
      payload.core_source,
      "official",
      "explicit official carries through the payload",
    );

    versionForm.setSource("mirror");
    await versionForm.onsubmit({ preventDefault: noop });
    payload = JSON.parse(taskBodies.at(-1));
    assert.equal(
      payload.core_source,
      "mirror",
      "feature-capable mirror carries through the payload",
    );
  } finally {
    if (presetDomDocument === undefined) delete globalThis.document;
    else globalThis.document = presetDomDocument;
    if (presetDomFormData === undefined) delete globalThis.FormData;
    else globalThis.FormData = presetDomFormData;
    if (presetDomDetails === undefined) delete globalThis.HTMLDetailsElement;
    else globalThis.HTMLDetailsElement = presetDomDetails;
    if (presetDomCSS === undefined) delete globalThis.CSS;
    else globalThis.CSS = presetDomCSS;
  }
}

// The manual config code editor formats JSON in place and fails closed on
// YAML/comments/lossy or readonly content, without re-rendering the page.
{
  const formatDocument = globalThis.document;
  const makeTextNode = () => {
    const listeners = new Map();
    return {
      textContent: "",
      style: {},
      disabled: false,
      hidden: false,
      value: "",
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      _listeners: listeners,
    };
  };
  const makeCodeInput = (initialValue, { readOnly = false } = {}) => {
    const listeners = new Map();
    const input = {
      value: initialValue,
      readOnly,
      disabled: false,
      selectionStart: initialValue.length,
      selectionEnd: initialValue.length,
      scrollTop: 0,
      scrollLeft: 0,
      classList: { toggle() {} },
      setAttribute() {},
      dispatchEvent() {},
      focus() {},
      closest: () => ({
        querySelectorAll: () => [],
        addEventListener: () => {},
      }),
      setSelectionRange(start, end) {
        input.selectionStart = start;
        input.selectionEnd = end;
      },
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      _listeners: listeners,
    };
    return input;
  };
  const buildEditor = (initialValue, language, readOnly = false) => {
    const input = makeCodeInput(initialValue, { readOnly });
    const gutter = makeTextNode();
    const byteLabel = makeTextNode();
    const position = makeTextNode();
    const status = makeTextNode();
    const statusDot = makeTextNode();
    const validation = makeTextNode();
    const reset = makeTextNode();
    const format = makeTextNode();
    const editor = {
      dataset: {
        codeLanguage: language,
        codeMaxBytes: "2097152",
        dirty: "0",
        codeValid: "1",
      },
      querySelector(selector) {
        if (selector === "[data-code-input]") return input;
        if (selector === "[data-line-numbers]") return gutter;
        if (selector === "[data-code-bytes]") return byteLabel;
        if (selector === "[data-code-position]") return position;
        if (selector === "[data-code-status]") return status;
        if (selector === "[data-code-status-dot]") return statusDot;
        if (selector === "[data-code-validation]") return validation;
        if (selector === "[data-code-reset]") return reset;
        if (selector === "[data-code-format]") return format;
        return null;
      },
      input,
      gutter,
      byteLabel,
      position,
      status,
      statusDot,
      validation,
      reset,
      format,
    };
    return editor;
  };

  const { bindCodeEditors } = installAgents(
    new Proxy(
      {
        state: { route: "live-config", data: {} },
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );

  const unformatted = '{"tag":"demo","port":443,"tls":{"enabled":true}}';
  const editor = buildEditor(unformatted, "JSON");
  const readonlyEditor = buildEditor(unformatted, "JSON", true);
  const brokenEditor = buildEditor('{"tag":"demo",}', "JSON");
  const largeEditor = buildEditor(
    JSON.stringify(Array(840001).fill(0)),
    "JSON",
  );
  const deepEditor = buildEditor("[".repeat(600000), "JSON");
  let editorQueryCalls = 0;

  globalThis.document = {
    querySelector: () => null,
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") {
        editorQueryCalls += 1;
        return [editor, readonlyEditor, brokenEditor, largeEditor, deepEditor];
      }
      return [];
    },
  };

  try {
    bindCodeEditors();
    assert.equal(
      editorQueryCalls,
      1,
      "binding queries the editor list exactly once",
    );
    assert.equal(
      editor.dataset.dirty,
      "0",
      "unmodified editor starts clean",
    );

    editor.format.dispatch("click", {});
    assert.equal(
      editorQueryCalls,
      1,
      "formatting does not re-query or re-render the editor page",
    );
    assert.equal(
      editor.input.value,
      '{\n  "tag": "demo",\n  "port": 443,\n  "tls": {\n    "enabled": true\n  }\n}\n',
      "JSON is formatted in place with two-space indentation and a final newline",
    );
    assert.equal(editor.dataset.dirty, "1", "formatting marks the editor dirty");
    assert.equal(editor.reset.disabled, false, "reset is enabled after formatting");
    assert.equal(
      editor.input.value.includes("  \"tag\": \"demo\""),
      true,
      "two-space indentation applied",
    );

    const firstSnapshot = editor.input.value;
    editor.format.dispatch("click", {});
    assert.equal(
      editor.input.value,
      firstSnapshot,
      "repeated formatting is idempotent",
    );

    readonlyEditor.format.dispatch("click", {});
    assert.equal(
      readonlyEditor.input.value,
      unformatted,
      "readonly editor is never rewritten",
    );
    assert.equal(
      readonlyEditor.dataset.dirty,
      "0",
      "readonly editor stays clean",
    );

    brokenEditor.format.dispatch("click", {});
    assert.equal(
      brokenEditor.input.value,
      '{"tag":"demo",}',
      "syntax-error content is preserved on failure",
    );
    assert.equal(
      brokenEditor.dataset.dirty,
      "0",
      "failure keeps the dirty baseline unchanged",
    );
    assert.equal(
      /无法安全格式化|JSON 语法错误/.test(brokenEditor.validation.textContent),
      true,
      "failure surfaces a local, explicit message",
    );
    assert.equal(
      brokenEditor.statusDot.style.background,
      "var(--red)",
      "failure marks the status dot red",
    );

    const deepSnapshot = deepEditor.input.value;
    deepEditor.format.dispatch("click", {});
    assert.equal(
      deepEditor.input.value,
      deepSnapshot,
      "over-deep content is preserved on failure",
    );
    assert.equal(
      deepEditor.dataset.dirty,
      "0",
      "over-deep failure keeps the dirty baseline unchanged",
    );
    assert.equal(
      deepEditor.validation.textContent,
      "当前内容无法安全格式化。",
      "internal formatter errors fall back to a generic local message",
    );
    assert.equal(
      deepEditor.statusDot.style.background,
      "var(--red)",
      "over-deep failure marks the status dot red",
    );

    const largeSnapshot = largeEditor.input.value;
    largeEditor.format.dispatch("click", {});
    assert.equal(
      largeEditor.input.value,
      largeSnapshot,
      "over-limit formatted output keeps the original text",
    );
    assert.equal(
      largeEditor.dataset.dirty,
      "0",
      "over-limit result keeps the dirty baseline unchanged",
    );
    assert.equal(
      /超过 2 MiB 上限/.test(largeEditor.validation.textContent),
      true,
      "over-limit result shows a local error without submitting",
    );
    assert.equal(
      largeEditor.statusDot.style.background,
      "var(--red)",
      "over-limit result marks the status dot red",
    );
  } finally {
    if (formatDocument === undefined) delete globalThis.document;
    else globalThis.document = formatDocument;
  }
}

// The archive new-config engine selector syncs the source editor language,
// file/language labels, and formatter on change without rebuilding the DOM.
{
  const archiveDocument = globalThis.document;
  const archiveEvent = globalThis.Event;
  globalThis.Event = class {
    constructor(type, options) {
      this.type = type;
      this.bubbles = options?.bubbles;
    }
  };

  const makeTarget = () => {
    const listeners = new Map();
    return {
      textContent: "",
      style: {},
      disabled: false,
      hidden: false,
      value: "",
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      _listeners: listeners,
    };
  };
  const makeInput = (initialValue) => {
    const listeners = new Map();
    const input = {
      value: initialValue,
      readOnly: false,
      disabled: false,
      selectionStart: initialValue.length,
      selectionEnd: initialValue.length,
      scrollTop: 0,
      scrollLeft: 0,
      classList: { toggle() {} },
      setAttribute() {},
      focus() {},
      closest: () => ({
        querySelectorAll: () => [],
        addEventListener: () => {},
      }),
      setSelectionRange(start, end) {
        input.selectionStart = start;
        input.selectionEnd = end;
      },
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      dispatchEvent(event) {
        input.dispatch(event?.type || "input", event);
      },
    };
    return input;
  };
  const makeArchiveEditor = (initialValue, engine) => {
    const input = makeInput(initialValue);
    const gutter = makeTarget();
    const byteLabel = makeTarget();
    const position = makeTarget();
    const status = makeTarget();
    const statusDot = makeTarget();
    const validation = makeTarget();
    const reset = makeTarget();
    const format = makeTarget();
    const languageLabel = makeTarget();
    const fileLabel = makeTarget();
    const editor = {
      dataset: {
        codeLanguage: engine === "mihomo" ? "YAML" : "JSON",
        codeMaxBytes: "2097152",
        dirty: "0",
        codeValid: "1",
      },
      querySelector(selector) {
        if (selector === "[data-code-input]") return input;
        if (selector === "[data-line-numbers]") return gutter;
        if (selector === "[data-code-bytes]") return byteLabel;
        if (selector === "[data-code-position]") return position;
        if (selector === "[data-code-status]") return status;
        if (selector === "[data-code-status-dot]") return statusDot;
        if (selector === "[data-code-validation]") return validation;
        if (selector === "[data-code-reset]") return reset;
        if (selector === "[data-code-format]") return format;
        if (selector === ".code-language") return languageLabel;
        if (selector === ".code-file-meta b") return fileLabel;
        return null;
      },
      input,
      gutter,
      byteLabel,
      position,
      status,
      statusDot,
      validation,
      reset,
      format,
      languageLabel,
      fileLabel,
    };
    return editor;
  };

  const engineSelect = makeTarget();
  engineSelect.value = "mihomo";
  const archiveForm = {
    querySelector(selector) {
      if (selector === 'select[name="engine"]') return engineSelect;
      if (selector === "[data-code-editor]") return archiveEditor;
      return null;
    },
    addEventListener() {},
  };
  const archiveEditor = makeArchiveEditor(
    "mixed-port: 7890\nproxies: []\n",
    "mihomo",
  );

  globalThis.document = {
    querySelector(selector) {
      if (selector === "#archive-form") return archiveForm;
      if (selector === "[data-code-editor]") return archiveEditor;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") return [archiveEditor];
      return [];
    },
  };

  const archiveState = { route: "archive-config", data: { newConfig: true } };
  const { bindCodeEditors } = installAgents(
    new Proxy(
      {
        state: { route: "archive-config", data: {} },
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const { archiveConfigs } = installConfigPages(
    new Proxy(
      {
        state: archiveState,
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
        api: async () => [],
        optionalAPI: async () => null,
        can: () => true,
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        ago: () => "now",
        date: () => "now",
        conciseVersion: () => "1.0.0",
        bytes: () => "0 B",
        confirmAction: async () => true,
        notify: () => {},
        shell: () => {},
        submitTask: async () => {},
        bindCodeEditors,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );

  try {
    await archiveConfigs();
    assert.equal(
      archiveEditor.dataset.codeLanguage,
      "YAML",
      "new archive config starts with YAML editor language",
    );

    engineSelect.value = "xray";
    engineSelect.dispatch("change", {});
    assert.equal(
      archiveEditor.dataset.codeLanguage,
      "JSON",
      "engine switch updates the editor language to JSON",
    );
    assert.equal(
      archiveEditor.languageLabel.textContent,
      "JSON",
      "engine switch updates the language badge",
    );
    assert.equal(
      archiveEditor.fileLabel.textContent,
      "config.json",
      "engine switch updates the file name label",
    );

    archiveEditor.input.value = '{"a":1,"b":[2,3]}';
    archiveEditor.format.dispatch("click", {});
    assert.equal(
      archiveEditor.input.value,
      '{\n  "a": 1,\n  "b": [\n    2,\n    3\n  ]\n}\n',
      "formatter uses the switched JSON language without rebuilding the editor",
    );
    assert.equal(
      archiveEditor.dataset.dirty,
      "1",
      "engine switch preserves dirty state after formatting",
    );

    engineSelect.value = "mihomo";
    engineSelect.dispatch("change", {});
    assert.equal(
      archiveEditor.dataset.codeLanguage,
      "YAML",
      "engine switch back updates the editor language to YAML",
    );
    const beforeYaml = archiveEditor.input.value;
    archiveEditor.format.dispatch("click", {});
    assert.equal(
      archiveEditor.input.value,
      beforeYaml,
      "YAML fail-closed keeps the original editor text",
    );
    assert.equal(
      /无法安全格式化|保留原文/.test(archiveEditor.validation.textContent),
      true,
      "YAML fail-closed shows a local message",
    );
  } finally {
    if (archiveDocument === undefined) delete globalThis.document;
    else globalThis.document = archiveDocument;
    if (archiveEvent === undefined) delete globalThis.Event;
    else globalThis.Event = archiveEvent;
  }
}

// A non-operator archive new-config never renders the format action and does
// not bind an engine-sync handler, so the readonly snapshot is untouched.
{
  const readonlyDocument = globalThis.document;
  const readonlyEvent = globalThis.Event;
  globalThis.Event = class {
    constructor(type, options) {
      this.type = type;
      this.bubbles = options?.bubbles;
    }
  };
  const makeTarget = () => {
    const listeners = new Map();
    return {
      textContent: "",
      style: {},
      disabled: false,
      list: listeners,
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
    };
  };
  const engine = makeTarget();
  engine.value = "mihomo";
  engine.disabled = true;
  const form = {
    querySelector(selector) {
      if (selector === 'select[name="engine"]') return engine;
      return null;
    },
    addEventListener() {},
  };
  const editor = {
    dataset: {
      codeLanguage: "YAML",
      codeMaxBytes: "2097152",
      dirty: "0",
      codeValid: "1",
    },
    querySelector(selector) {
      if (selector === "[data-code-format]") return null;
      if (selector === "[data-code-input]")
        return {
          value: "",
          readOnly: true,
          disabled: false,
          selectionStart: 0,
          selectionEnd: 0,
          scrollTop: 0,
          scrollLeft: 0,
          classList: { toggle() {} },
          setAttribute() {},
          focus() {},
          closest: () => ({ querySelectorAll: () => [], addEventListener: () => {} }),
          setSelectionRange() {},
          addEventListener() {},
          dispatchEvent() {},
        };
      if (selector === "[data-line-numbers]")
        return { textContent: "", scrollTop: 0 };
      return null;
    },
  };
  globalThis.document = {
    querySelector(selector) {
      if (selector === "#archive-form") return form;
      if (selector === "[data-code-editor]") return editor;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") return [editor];
      return [];
    },
  };
  const { bindCodeEditors } = installAgents(
    new Proxy(
      { state: { route: "archive-config", data: {} } },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const { archiveConfigs } = installConfigPages(
    new Proxy(
      {
        state: { route: "archive-config", data: { newConfig: true } },
        engines: ["mihomo", "xray"],
        api: async () => [],
        optionalAPI: async () => null,
        can: () => false,
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        ago: () => "now",
        date: () => "now",
        conciseVersion: () => "1.0.0",
        bytes: () => "0 B",
        confirmAction: async () => true,
        notify: () => {},
        shell: () => {},
        submitTask: async () => {},
        bindCodeEditors,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  try {
    await archiveConfigs();
    assert.equal(
      engine.disabled,
      true,
      "readonly archive keeps the engine selector disabled",
    );
    assert.equal(
      editor.querySelector("[data-code-format]"),
      null,
      "readonly archive has no format action",
    );
    assert.equal(
      (engine.list.get("change") || []).length,
      0,
      "readonly archive does not bind an engine-sync change handler",
    );
  } finally {
    if (readonlyDocument === undefined) delete globalThis.document;
    else globalThis.document = readonlyDocument;
    if (readonlyEvent === undefined) delete globalThis.Event;
    else globalThis.Event = readonlyEvent;
  }
}

// Dual-stack public address display regressions.
const ipRows = publicAddressRows({
  observed_public_ip: "93.184.216.34",
  public_ipv4: "198.35.26.96",
  public_ipv6: "2001:4860:4860::8888",
});
assert.equal(ipRows.length, 2);
assert.equal(ipRows[0].value, "198.35.26.96");
assert.equal(ipRows[0].source, "Agent 本地直连探测");
assert.equal(ipRows[0].ok, true);
assert.equal(ipRows[1].value, "2001:4860:4860::8888");

const managedProbeRows = publicAddressRows({
  public_ipv4: "198.35.26.96",
  public_ipv4_source: "control-plane-config",
});
assert.equal(managedProbeRows[0].source, "控制面配置的 Agent 直连探测");
assert.equal(publicAddressRows({
  public_ipv4: "198.35.26.96",
  public_ipv4_source: "untrusted-source",
})[0].value, "", "unknown probe provenance must fail closed");

const fallbackRows = publicAddressRows({ observed_public_ip: "93.184.216.34" });
assert.equal(fallbackRows[0].value, "93.184.216.34");
assert.equal(fallbackRows[0].source, "已验证连接来源");
assert.equal(fallbackRows[1].value, "");
assert.equal(fallbackRows[1].ok, false);

const manualRows = publicAddressRows(
  {
    public_ipv4: "198.35.26.96",
    public_ipv6: "2001:4860:4860::8888",
  },
  {
    client_address: "198.35.26.10",
    public_ip: "93.184.216.34",
  },
  ["public-ip-probe-v1"],
);
assert.equal(manualRows[0].value, "198.35.26.10");
assert.equal(manualRows[0].source, "手动设置");
assert.equal(manualRows[1].value, "2001:4860:4860::8888");
assert.equal(manualRows[1].source, "Agent 本地直连探测");

// A hostname remains the highest-priority client connection setting, while a
// supported public_ip literal may still fill its own family without DNS
// inference or pretending that the hostname is an IP address.
const manualDomainRows = publicAddressRows(
  { public_ipv4: "198.35.26.96" },
  { client_address: "node.example.com", public_ip: "93.184.216.34" },
);
assert.equal(manualDomainRows[0].value, "93.184.216.34");
assert.equal(manualDomainRows[0].source, "节点公网 IP");
assert.equal(manualDomainRows[1].value, "");
assert.equal(manualDomainRows[1].source, "公网探测未启用 · 可手动设置");

assert.equal(
  publicAddressRows({}, {}, [])[0].source,
  "公网探测未启用 · 可手动设置",
);
assert.equal(
  publicAddressRows({}, {}, ["public-ip-probe-v1"])[0].source,
  "公网探测已启用 · 等待结果",
);
assert.equal(
  publicAddressRows({}, {}, ["managed-public-ip-probe-v1"])[0].source,
  "公网探测未启用 · 可手动设置",
);
assert.equal(
  publicAddressRows({ collected_at: "now" }, {}, ["public-ip-probe-v1"])[0].source,
  "无可验证公网地址 · 可手动设置",
);

const manualIPv6Rows = publicAddressRows(
  { public_ipv6: "2620:4f:8001::1" },
  { client_address: "[2001:4860:4860::8888]" },
);
assert.equal(manualIPv6Rows[1].value, "2001:4860:4860::8888");
assert.equal(manualIPv6Rows[1].source, "手动设置");

const invalidManualRows = publicAddressRows(
  { public_ipv4: "198.35.26.96" },
  {
    client_address: "100.64.0.8",
    public_ip: "198.19.0.1",
    public_host: "2606:4700:4700::1111%eth0",
  },
  ["public-ip-probe-v1"],
);
assert.equal(invalidManualRows[0].value, "198.35.26.96");
assert.equal(invalidManualRows[0].source, "Agent 本地直连探测");
assert.equal(invalidManualRows[1].value, "");
assert.equal(invalidManualRows[1].ok, false);

const leadingZeroRows = publicAddressRows(
  {},
  { client_address: "001.001.001.001", public_ip: "01.2.3.4" },
);
assert.equal(leadingZeroRows[0].value, "");
assert.equal(leadingZeroRows[0].ok, false);
assert.equal(leadingZeroRows[1].value, "");
assert.equal(
  manualConnectionAddressNote({ client_address: "001.001.001.001" }),
  "手动连接地址：001.001.001.001",
);
assert.equal(
  manualConnectionAddressNote({ public_ip: "01.2.3.4" }),
  "手动连接地址：01.2.3.4",
);

const mappedLeadingZeroRows = publicAddressRows(
  {},
  { client_address: "::ffff:001.001.001.001", public_ip: "::ffff:1.2.3.004" },
);
assert.equal(mappedLeadingZeroRows[0].value, "");
assert.equal(mappedLeadingZeroRows[0].ok, false);
assert.equal(
  manualConnectionAddressNote({ client_address: "::ffff:001.001.001.001" }),
  "手动连接地址：::ffff:001.001.001.001",
);

// IPv4-mapped IPv6 has the same family and IANA policy as its unmapped IPv4.
// Hexadecimal and dotted spellings must therefore converge before display or
// copy eligibility is decided.
const mappedHexRows = publicAddressRows(
  {},
  { client_address: "::ffff:c000:201", public_ip: "::ffff:0a00:1" },
);
assert.equal(mappedHexRows[0].value, "");
assert.equal(mappedHexRows[0].ok, false);
assert.equal(mappedHexRows[1].value, "");
assert.equal(mappedHexRows[1].ok, false);

const mappedGlobalRows = publicAddressRows(
  {},
  { client_address: "::ffff:0101:0101" },
);
assert.equal(mappedGlobalRows[0].value, "1.1.1.1");
assert.equal(mappedGlobalRows[0].source, "手动设置");
assert.equal(mappedGlobalRows[1].value, "");

const embeddedIPv4Rows = publicAddressRows(
  {},
  { client_address: "2001:4860::192.0.2.1" },
);
assert.equal(embeddedIPv4Rows[0].value, "");
assert.equal(embeddedIPv4Rows[1].value, "2001:4860::192.0.2.1");
assert.equal(embeddedIPv4Rows[1].source, "手动设置");
const embeddedLeadingZeroRows = publicAddressRows(
  {},
  { client_address: "2001:4860::192.0.2.001" },
);
assert.equal(embeddedLeadingZeroRows[0].value, "");
assert.equal(embeddedLeadingZeroRows[1].value, "");

const normalizedProbeRows = publicAddressRows(
  {
    public_ipv4: "::ffff:0101:0101",
    public_ipv6: "2001:4860::192.0.2.1",
  },
  {},
  ["public-ip-probe-v1"],
);
assert.equal(normalizedProbeRows[0].value, "1.1.1.1");
assert.equal(normalizedProbeRows[0].source, "Agent 本地直连探测");
assert.equal(normalizedProbeRows[1].value, "2001:4860::192.0.2.1");
assert.equal(normalizedProbeRows[1].source, "Agent 本地直连探测");

const normalizedObservedRows = publicAddressRows({
  observed_public_ip: "::ffff:0101:0101",
});
assert.equal(normalizedObservedRows[0].value, "1.1.1.1");
assert.equal(normalizedObservedRows[0].source, "已验证连接来源");
assert.equal(normalizedObservedRows[1].value, "");

const mappedInterfaceRows = publicAddressRows({
  network_interfaces: [
    { name: "eth0", addresses: ["::ffff:0101:0101", "2001:4860::192.0.2.1"] },
  ],
});
assert.equal(mappedInterfaceRows[0].value, "1.1.1.1");
assert.equal(mappedInterfaceRows[0].source, "默认路由接口 eth0");
assert.equal(mappedInterfaceRows[1].value, "2001:4860::192.0.2.1");
assert.equal(mappedInterfaceRows[1].source, "默认路由接口 eth0");

const canonicalManualRows = publicAddressRows(
  {},
  { client_address: "1.1.1.1" },
);
assert.equal(canonicalManualRows[0].value, "1.1.1.1");
assert.equal(canonicalManualRows[0].source, "手动设置");
assert.equal(manualConnectionAddressNote({ client_address: "1.1.1.1" }), "");

// An observed relay (e.g. a Cloudflare edge) must never beat a genuine
// default-route interface address that reports the same family.
const relayRows = publicAddressRows({
  observed_public_ip: "2400:cb00::1",
  network_interfaces: [{ name: "eth0", addresses: ["2001:4860:4860::8888"] }],
});
assert.equal(relayRows[0].value, "");
assert.equal(relayRows[0].source, "公网探测未启用 · 可手动设置");
assert.equal(relayRows[0].ok, false);
assert.equal(relayRows[1].value, "2001:4860:4860::8888");
assert.equal(relayRows[1].source, "默认路由接口 eth0");

for (const relayAddress of [
  "104.22.17.83",
  "172.69.135.152",
  "162.158.193.59",
  "172.64.217.32",
  "172.71.124.82",
  "172.68.225.178",
]) {
  const rows = publicAddressRows({ observed_public_ip: relayAddress });
  assert.equal(rows[0].value, "", `Cloudflare relay ${relayAddress} must not be displayed`);
  assert.equal(rows[0].ok, false, `Cloudflare relay ${relayAddress} must not be copyable`);
}
const mappedRelayRows = publicAddressRows({ observed_public_ip: "::ffff:172.69.135.152" });
assert.equal(mappedRelayRows[0].value, "", "mapped Cloudflare relay must be filtered");
const cloudflareIPv6Rows = publicAddressRows({ observed_public_ip: "2606:4700::1111" });
assert.equal(cloudflareIPv6Rows[1].value, "", "Cloudflare IPv6 relay must be filtered");
const cloudflareInterfaceRows = publicAddressRows({
  network_interfaces: [{ name: "eth0", addresses: ["104.22.17.83", "2606:4700::1111"] }],
});
assert.equal(cloudflareInterfaceRows[0].value, "", "Cloudflare IPv4 interface must be filtered");
assert.equal(cloudflareInterfaceRows[1].value, "", "Cloudflare IPv6 interface must be filtered");
const relayProbeFallbackRows = publicAddressRows({
  collected_at: "now",
  public_ipv4: "172.69.135.152",
  public_ipv6: "::ffff:172.69.135.152",
  network_interfaces: [
    { name: "eth0", addresses: ["198.35.26.96", "2001:4860:4860::8888"] },
  ],
});
assert.equal(relayProbeFallbackRows[0].value, "198.35.26.96");
assert.equal(relayProbeFallbackRows[0].source, "默认路由接口 eth0");
assert.equal(relayProbeFallbackRows[1].value, "2001:4860:4860::8888");
assert.equal(relayProbeFallbackRows[1].source, "默认路由接口 eth0");
const relayProbeVerifiedFallbackRows = publicAddressRows({
  collected_at: "now",
  public_ipv4: "104.22.17.83",
  observed_public_ip: "93.184.216.34",
});
assert.equal(relayProbeVerifiedFallbackRows[0].value, "93.184.216.34");
assert.equal(relayProbeVerifiedFallbackRows[0].source, "已验证连接来源");
const realObservedRows = publicAddressRows({ observed_public_ip: "93.184.216.34" });
assert.equal(realObservedRows[0].value, "93.184.216.34");
assert.equal(realObservedRows[0].source, "已验证连接来源");
assert.equal(relayRows[1].ok, true);

// Interface fallback is strictly filtered: private, CGNAT, documentation and
// link-local addresses are dropped while a truly routable address surfaces.
const filteredRows = publicAddressRows({
  network_interfaces: [
    { name: "tailscale0", addresses: ["100.64.0.8", "fd00::8"] },
    { name: "docker0", addresses: ["172.17.0.1"] },
    { name: "eth0", addresses: ["192.0.2.9", "198.35.26.96", "2001:db8::8", "2001:4860:4860::8888", "fe80::1%eth0"] },
  ],
});
assert.equal(filteredRows[0].value, "198.35.26.96");
assert.equal(filteredRows[0].source, "默认路由接口 eth0");
assert.equal(filteredRows[1].value, "2001:4860:4860::8888");
assert.equal(filteredRows[1].source, "默认路由接口 eth0");

// The frontend IANA special-purpose policy must stay equivalent to Go
// internal/netpolicy instead of the loose IsGlobalUnicast. These are exact
// boundary cases: the benchmark /15 covers both 198.18 and 198.19, the /48
// special range covers 2620:4f:8000, and a zoned global address is scoped.
const boundaryRows = publicAddressRows({
  network_interfaces: [
    { name: "eth0", addresses: ["198.19.0.1", "198.18.0.1", "2620:4f:8000::1", "2606:4700:4700::1111%eth0"] },
  ],
});
assert.equal(boundaryRows[0].value, "");
assert.equal(boundaryRows[0].source, "公网探测未启用 · 可手动设置");
assert.equal(boundaryRows[0].ok, false);
assert.equal(boundaryRows[1].value, "");
assert.equal(boundaryRows[1].source, "公网探测未启用 · 可手动设置");
assert.equal(boundaryRows[1].ok, false);

const outsideBoundaryRows = publicAddressRows({
  network_interfaces: [
    { name: "eth0", addresses: ["198.20.0.1", "2620:4f:8001::1"] },
  ],
});
assert.equal(outsideBoundaryRows[0].value, "198.20.0.1");
assert.equal(outsideBoundaryRows[0].source, "默认路由接口 eth0");
assert.equal(outsideBoundaryRows[0].ok, true);
assert.equal(outsideBoundaryRows[1].value, "2620:4f:8001::1");
assert.equal(outsideBoundaryRows[1].source, "默认路由接口 eth0");
assert.equal(outsideBoundaryRows[1].ok, true);

// The same denylist also guards probed and verified-WSS fallbacks, so a
// special-purpose or zoned value can never be shown or copied even if a stale
// value reaches the metrics payload.
const staleSourceRows = publicAddressRows({
  observed_public_ip: "2620:4f:8000::1",
  public_ipv4: "198.19.0.1",
  public_ipv6: "2606:4700:4700::1111%eth0",
});
assert.equal(staleSourceRows[0].value, "");
assert.equal(staleSourceRows[0].source, "公网探测未启用 · 可手动设置");
assert.equal(staleSourceRows[0].ok, false);
assert.equal(staleSourceRows[1].value, "");
assert.equal(staleSourceRows[1].source, "公网探测未启用 · 可手动设置");
assert.equal(staleSourceRows[1].ok, false);

// CSS contract: the IPv4/IPv6 badge must override the <i> default italic and
// must not regress to the old tiny sizes; card and detail address text must
// keep the readability floor while staying on a single ellipsizing line.
{
  const css = readFileSync(new URL("app.css", import.meta.url), "utf8");
  const badge = /\.ip-family\{[^}]*\}/.exec(css)?.[0] || "";
  assert.equal(
    badge.includes("font-style:normal"),
    true,
    ".ip-family must override the <i> default italic",
  );
  assert.equal(
    /\.ip-family\{[^}]*font-size:8px/.test(css),
    false,
    ".ip-family badge size must not regress to 8px",
  );
  const cardCode = /\.card-ip-row code\{[^}]*\}/.exec(css)?.[0] || "";
  const publicCode = /\.public-ip-row code\{[^}]*\}/.exec(css)?.[0] || "";
  assert.equal(
    /\bfont-size:11px/.test(cardCode),
    true,
    "card address text must be at least 11px",
  );
  assert.equal(
    /\bfont-size:12px/.test(publicCode),
    true,
    "detail address text must be at least 12px",
  );
  assert.equal(
    /\.card-ip-row code\{[^}]*font-size:9px/.test(css),
    false,
    "card address size must not regress to 9px",
  );
  assert.equal(
    css.includes(".card-ip-row code{min-width:0;max-width:calc(100% - 72px);flex:0 1 auto"),
    true,
    "card copy control must stay next to the address",
  );
}

assert.equal(
  formatHostPort("2606:4700:4700::1111", "443"),
  "[2606:4700:4700::1111]:443",
);
assert.equal(
  formatHostPort("[2606:4700:4700::1111]", "443"),
  "[2606:4700:4700::1111]:443",
);
assert.equal(formatHostPort("[::1]", "443"), "[::1]:443");
assert.equal(formatHostPort("93.184.216.34", "443"), "93.184.216.34:443");
assert.equal(formatHostPort("node.example.com", "8443"), "node.example.com:8443");
assert.equal(formatHostPort("", "443"), "");

{
  const codeV4 = { textContent: "" };
  const copyV4 = {
    dataset: { copyIp: "" },
    hidden: true,
    title: "",
    setAttribute() {},
  };
  const lineV4 = {
    hidden: true,
    querySelector(sel) {
      if (sel === "code") return codeV4;
      if (sel === "[data-copy-ip]") return copyV4;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-card-ips"; } },
    querySelector(sel) {
      return sel === '.card-ip-row[data-ip-family="v4"]' ? lineV4 : null;
    },
  };
  const root = {
    dataset: {},
    querySelector() { return null; },
    querySelectorAll(sel) {
      return sel === ".node-card-ips, .node-public-ips" ? [container] : [];
    },
  };
  updatePublicIPDisplays(root, { public_ipv4: "198.35.26.10", public_ipv6: "" });
  assert.equal(lineV4.hidden, false);
  assert.equal(codeV4.textContent, "198.35.26.10");
  assert.equal(copyV4.dataset.copyIp, "198.35.26.10");
  assert.equal(copyV4.hidden, false);
}

{
  const code = { textContent: "" };
  const small = { textContent: "" };
  const line = {
    hidden: true,
    querySelector(sel) {
      if (sel === "code") return code;
      if (sel === "small") return small;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-public-ips"; } },
    querySelector(sel) {
      return sel === '.public-ip-row[data-ip-family="v6"]' ? line : null;
    },
  };
  const root = {
    dataset: {},
    querySelector() { return null; },
    querySelectorAll(sel) {
      return sel === ".node-card-ips, .node-public-ips" ? [container] : [];
    },
  };
  updatePublicIPDisplays(root, { public_ipv6: "2001:4860:4860::8888" });
  assert.equal(line.hidden, false);
  assert.equal(code.textContent, "2001:4860:4860::8888");
  assert.equal(small.textContent, "Agent 本地直连探测");
  updatePublicIPDisplays(root, { public_ipv6: "" });
  assert.equal(line.hidden, true, "undetected IPv6 row is hidden in place");
}

{
  const code = { textContent: "", title: "" };
  const copy = {
    dataset: { copyIp: "" },
    hidden: true,
    title: "",
    attrs: {},
    setAttribute(name, value) {
      this.attrs[name] = value;
    },
  };
  const line = {
    hidden: false,
    dataset: {},
    querySelector(sel) {
      if (sel === "code") return code;
      if (sel === "[data-copy-ip]") return copy;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-card-ips"; } },
    querySelector(sel) {
      return sel === '.card-ip-row[data-ip-family="v4"]' ? line : null;
    },
  };
  const root = {
    querySelectorAll(sel) {
      return sel === ".node-card-ips, .node-public-ips" ? [container] : [];
    },
  };
  updatePublicIPDisplays(root, {
    collected_at: "now",
    public_ipv4: "172.69.135.152",
    observed_public_ip: "93.184.216.34",
  });
  assert.equal(line.hidden, false, "relay probe falls back to verified WSS in place");
  assert.equal(code.textContent, "93.184.216.34");
  assert.equal(copy.dataset.copyIp, "93.184.216.34");
  assert.equal(copy.hidden, false);
  updatePublicIPDisplays(root, { collected_at: "now", public_ipv4: "104.22.17.83" });
  assert.equal(line.hidden, true, "relay-only probe hides the IPv4 row");
  assert.equal(copy.hidden, true, "relay-only probe removes the copy target");
  updatePublicIPDisplays(root, { public_ipv4: "198.35.26.96" });
  assert.equal(line.hidden, false, "a later genuine probe restores the same row");
  assert.equal(code.textContent, "198.35.26.96");
  assert.equal(copy.dataset.copyIp, "198.35.26.96");
  assert.equal(copy.hidden, false);
}

// Runtime smoke: a normal node-settings overview render must not throw
// ReferenceError for esc inside cardIPRow, and must emit the probe rows.
{
  const previousDocument = globalThis.document;
  const previousSetTimeout = globalThis.setTimeout;
  const previousClearTimeout = globalThis.clearTimeout;
  globalThis.setTimeout = () => 0;
  globalThis.clearTimeout = () => {};
  globalThis.document = {
    querySelector: () => null,
    querySelectorAll: () => [],
  };
  try {
  const overviewState = {
    route: "node-settings",
    navigationEpoch: 1,
    anchor: "node-settings",
    data: {},
  };
  let overviewMarkup = "";
  const overviewAgents = [
    {
      id: "alpha",
      name: "Alpha",
      os: "linux",
      arch: "amd64",
      status: "online",
      version: "1.2.3",
      capabilities: ["mihomo"],
      features: [],
      labels: { client_address: "node.example.com" },
      metrics: {
        public_ipv4: "198.35.26.96",
        public_ipv6: "",
        collected_at: "now",
      },
      runtime: {
        mihomo: {
          installed: true,
          version: "1.19.0",
          service_status: "running",
        },
      },
      last_seen: "now",
      enrolled_at: "now",
    },
  ];
  const overviewCtx = new Proxy(
    {
      state: overviewState,
      engines: ["mihomo"],
      api: async (path) => {
        if (path === "/agents") return overviewAgents;
        if (path === "/overview") return { agents: 1, agents_online: 1 };
        if (path === "/enrollment-tokens") return [];
        if (path.endsWith("/configs")) return [];
        if (path === "/deployments" || path === "/client-access") return [];
        return [];
      },
      optionalAPI: async () => null,
      can: () => true,
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      serviceStatusName: (value) => value,
      statusTone: (value) => value,
      conciseVersion: (_engine, value) => value,
      ago: () => "刚刚",
      heartbeat: () => "刚刚",
      bytes: (value) => `${value || 0} B`,
      percent: (used, limit) => (limit ? Number(used || 0) / limit : 0),
      rate: (value) => `${value || 0} B/s`,
      actionName: (value) => value,
      serviceActionDisabled: () => false,
      trafficChart: () => "",
      renderConfigDiff: () => "",
      notify: () => {},
      confirmAction: () => {},
      short: (value) => value,
      date: (value) => value,
      shell: (markup) => {
        overviewMarkup = markup;
      },
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const { nodeSettings: renderOverview } = installAgents(overviewCtx);
  await renderOverview(false, { overview: { agents: 1, agents_online: 1 } });
  assert.equal(
    overviewMarkup.includes('class="node-card-ips"'),
    true,
    "node-settings overview card renders the public-address strip",
  );
  assert.equal(
    overviewMarkup.includes("198.35.26.96"),
    true,
    "node-settings card prints the probed IPv4 address",
  );
  assert.equal(
    /class="card-ip-row empty"[^>]*hidden/.test(overviewMarkup),
    true,
    "node-settings card hides an unavailable address family",
  );
  assert.equal(
    overviewMarkup.includes("data-copy-ip"),
    true,
    "node-settings card keeps a copy button",
  );
  assert.equal(
    overviewMarkup.includes("手动连接地址：node.example.com"),
    true,
    "node-settings card keeps a non-IP manual connection address separate",
  );

  overviewState.nodeView = "detail";
  overviewState.anchor = "settings-node-alpha";
  overviewAgents[0].labels = {
    client_address: "198.35.26.10",
    public_ip: "2001:4860:4860::8888",
  };
  await renderOverview(false, { overview: { agents: 1, agents_online: 1 } });
  assert.equal(
    overviewMarkup.includes('class="node-public-ips"'),
    true,
    "node-settings detail renders the public-address section",
  );
  assert.equal(
    overviewMarkup.includes("198.35.26.10"),
    true,
    "node-settings detail renders a manually configured IPv4",
  );
  assert.equal(
    overviewMarkup.includes("2001:4860:4860::8888"),
    true,
    "node-settings detail renders a manually configured IPv6",
  );
  assert.equal(
    overviewMarkup.includes('data-ip-source="手动设置"'),
    true,
    "node-settings detail marks manual literal addresses explicitly",
  );
  } finally {
    if (previousSetTimeout === undefined) delete globalThis.setTimeout;
    else globalThis.setTimeout = previousSetTimeout;
    if (previousClearTimeout === undefined) delete globalThis.clearTimeout;
    else globalThis.clearTimeout = previousClearTimeout;
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}

// Runtime smoke: the real metrics patch path (pollAgentMetrics ->
// updateAgentMetrics -> updatePublicIPDisplays) updates the card rows and
// copy-button state instead of only the direct helper.
{
  const previousDocument = globalThis.document;
  const previousCSS = globalThis.CSS;
  const previousSetTimeout = globalThis.setTimeout;
  const previousClearTimeout = globalThis.clearTimeout;
  globalThis.setTimeout = () => 0;
  globalThis.clearTimeout = () => {};
  globalThis.CSS = { escape: (value) => String(value) };
  const metricState = {
    route: "node-settings",
    navigationEpoch: 1,
    data: {},
  };
  // Pre-populate with a stale address so the refresh must update every field
  // (text, title, copy dataset, copy aria, and the detail source) in place.
  const cardCodeV4 = { textContent: "2400:cb00::1", title: "2400:cb00::1" };
  const copyV4 = {
    dataset: { copyIp: "2400:cb00::1" },
    hidden: false,
    title: "复制 IPv4 地址",
    attrs: {},
    setAttribute(name, value) {
      this.attrs[name] = value;
    },
  };
  const cardLineV4 = {
    hidden: false,
    dataset: {},
    querySelector(sel) {
      if (sel === "code") return cardCodeV4;
      if (sel === "[data-copy-ip]") return copyV4;
      return null;
    },
    classList: { toggle() {} },
  };
  const cardContainer = {
    classList: { contains(cls) { return cls === "node-card-ips"; } },
    querySelector(sel) {
      return sel === '.card-ip-row[data-ip-family="v4"]' ? cardLineV4 : null;
    },
  };
  const publicCodeV4 = { textContent: "", title: "" };
  const publicSmallV4 = { textContent: "" };
  const publicLineV4 = {
    hidden: false,
    dataset: {},
    querySelector(sel) {
      if (sel === "code") return publicCodeV4;
      if (sel === "small") return publicSmallV4;
      return null;
    },
    classList: { toggle() {} },
  };
  const publicContainer = {
    classList: { contains(cls) { return cls === "node-public-ips"; } },
    querySelector(sel) {
      return sel === '.public-ip-row[data-ip-family="v4"]' ? publicLineV4 : null;
    },
  };
  const connectionNote = { textContent: "旧手动地址", hidden: false };
  const root = {
    dataset: { available: "0" },
    querySelector() { return null; },
    querySelectorAll(sel) {
      if (sel === ".node-card-ips, .node-public-ips") {
        return [cardContainer, publicContainer];
      }
      if (sel === "[data-node-connection-address]") return [connectionNote];
      return [];
    },
  };
  globalThis.document = {
    hidden: false,
    querySelector(sel) {
      return sel.includes("data-agent-metrics") ? root : null;
    },
    querySelectorAll() { return []; },
  };
  try {
    const { updateAgentMetrics } = installAgents(
      new Proxy(
        {
          state: metricState,
          engines: [],
          api: async () => [],
          optionalAPI: async () => null,
          can: () => true,
          esc: (value) => String(value ?? ""),
          engineName: (value) => value,
          serviceStatusName: (value) => value,
          statusTone: (value) => value,
          conciseVersion: (_engine, value) => value,
          ago: () => "刚刚",
          heartbeat: () => "刚刚",
          bytes: (value) => `${value || 0} B`,
          percent: (used, limit) => (limit ? Number(used || 0) / limit : 0),
          rate: (value) => `${value || 0} B/s`,
          actionName: (value) => value,
          serviceActionDisabled: () => false,
          trafficChart: () => "",
          renderConfigDiff: () => "",
          notify: () => {},
          confirmAction: () => {},
          short: (value) => value,
          date: (value) => value,
        },
        { get: (target, key) => target[key] ?? noop },
      ),
    );
    updateAgentMetrics({
      id: "alpha",
      status: "online",
      metrics: { public_ipv4: "198.35.26.96", public_ipv6: "" },
      labels: { client_address: "93.184.216.34" },
      features: ["public-ip-probe-v1"],
      runtime: {},
      version: "1.2.3",
      last_seen: "now",
    });
    // The one in-place refresh must update the card text and hover title, the
    // copy target/title/aria, and the detail source, without replacing the DOM.
    assert.equal(cardCodeV4.textContent, "93.184.216.34");
    assert.equal(cardLineV4.hidden, false);
    assert.equal(cardCodeV4.title, "93.184.216.34");
    assert.equal(cardLineV4.dataset.ipSource, "手动设置");
    assert.equal(copyV4.dataset.copyIp, "93.184.216.34");
    assert.equal(copyV4.title, "复制 IPv4 地址");
    assert.equal(copyV4.attrs["aria-label"], "复制 IPv4 公网地址 93.184.216.34");
    assert.equal(copyV4.hidden, false);
    assert.equal(publicCodeV4.textContent, "93.184.216.34");
    assert.equal(publicLineV4.hidden, false);
    assert.equal(publicCodeV4.title, "93.184.216.34");
    assert.equal(publicLineV4.dataset.ipSource, "手动设置");
    assert.equal(publicSmallV4.textContent, "手动设置");
    assert.equal(connectionNote.textContent, "");
    assert.equal(connectionNote.hidden, true);
    assert.equal(cardContainer.querySelector('.card-ip-row[data-ip-family="v4"]'), cardLineV4);
    assert.equal(publicContainer.querySelector('.public-ip-row[data-ip-family="v4"]'), publicLineV4);

    updateAgentMetrics({
      id: "alpha",
      status: "online",
      metrics: { public_ipv4: "198.35.26.96", public_ipv6: "" },
      labels: {},
      features: ["public-ip-probe-v1"],
      runtime: {},
      version: "1.2.3",
      last_seen: "now",
    });
    assert.equal(cardCodeV4.textContent, "198.35.26.96");
    assert.equal(cardLineV4.dataset.ipSource, "Agent 本地直连探测");
    assert.equal(publicSmallV4.textContent, "Agent 本地直连探测");
    assert.equal(connectionNote.textContent, "");
    assert.equal(connectionNote.hidden, true);

    updateAgentMetrics({
      id: "alpha",
      status: "online",
      metrics: { public_ipv4: "::ffff:0101:0101", public_ipv6: "" },
      labels: {},
      features: ["public-ip-probe-v1"],
      runtime: {},
      version: "1.2.3",
      last_seen: "now",
    });
    assert.equal(cardCodeV4.textContent, "1.1.1.1");
    assert.equal(cardLineV4.dataset.ipSource, "Agent 本地直连探测");
    assert.equal(copyV4.dataset.copyIp, "1.1.1.1");
    assert.equal(publicCodeV4.textContent, "1.1.1.1");
    assert.equal(publicSmallV4.textContent, "Agent 本地直连探测");
    assert.equal(cardContainer.querySelector('.card-ip-row[data-ip-family="v4"]'), cardLineV4);
    assert.equal(publicContainer.querySelector('.public-ip-row[data-ip-family="v4"]'), publicLineV4);
  } finally {
    if (previousSetTimeout === undefined) delete globalThis.setTimeout;
    else globalThis.setTimeout = previousSetTimeout;
    if (previousClearTimeout === undefined) delete globalThis.clearTimeout;
    else globalThis.clearTimeout = previousClearTimeout;
    if (previousCSS === undefined) delete globalThis.CSS;
    else globalThis.CSS = previousCSS;
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}
