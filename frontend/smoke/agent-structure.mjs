import assert from "node:assert/strict";

import { coreSourceForInstall, developmentSourceVisible, installAgents } from "../modules/agents.js";
import { coreSourceLabel, coreSourceName } from "../modules/tasks.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
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
      // Keep capability membership stable: changing it now deliberately
      // requests one structural refresh for node-level switches.
      capabilities: ["sing-box", "xray"],
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
      capabilities: ["sing-box", "xray"],
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
    "linux / amd64 · 1/2 内核已安装",
  );

  structurePayload = () => [
    {
      id: "alpha",
      os: "linux",
      arch: "amd64",
      status: "online",
      metrics: {},
      capabilities: ["sing-box", "xray"],
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

}
