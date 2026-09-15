import assert from "node:assert/strict";
import * as agents from "./modules/agents.js";
import * as addresses from "./modules/agent-addresses.js";
import * as batch from "./modules/agent-batch.js";
import * as drag from "./modules/agent-card-drag.js";
import * as komari from "./modules/agent-komari.js";
import * as coreActions from "./modules/agent-core-actions.js";
import * as agentRefresh from "./modules/agent-refresh.js";
import * as configs from "./modules/configs.js";
import * as plan from "./modules/server-plan-form.js";
import * as live from "./modules/live-config-state.js";
import { bindCodeEditors } from "./modules/code-editor.js";
import { createAgentEnrollment } from "./modules/agent-enrollment.js";

// Existing consumers keep using the route modules; new consumers can depend on
// the focused helpers without importing the route's rendering implementation.
for (const [facade, owner, names] of [
  [agents, addresses, ["formatHostPort", "manualConnectionAddressNote", "publicAddressRows", "updatePublicIPDisplays"]],
  [agents, batch, ["batchAgentEligibility", "batchSelectAllState"]],
  [agents, drag, ["animateNodeCardDrop", "clearNodeCardDragState", "nodeCardDropIndex"]],
  [agents, komari, ["komariCycleRange", "komariResetDay"]],
  [agents, coreActions, ["developmentSourceVisible", "coreSourceForInstall"]],
  [agents, agentRefresh, ["agentStructureSignature"]],
  [configs, plan, ["bindServerPlanRegeneration", "readServerPlanInput"]],
  [configs, live, ["assertAgentConfigBaseline", "liveConfigEditorState", "liveConfigEngineEligible", "liveConfigReadAction", "liveConfigSnapshotReusable", "submitLiveConfigChange"]],
]) {
  for (const name of names) assert.equal(facade[name], owner[name], `${name} must retain its public export`);
}
assert.equal(typeof bindCodeEditors, "function", "the shared editor must be importable without a document");

const previousLocation = Object.getOwnPropertyDescriptor(globalThis, "location");
try {
  Object.defineProperty(globalThis, "location", { configurable: true, value: { origin: "https://panel.example.invalid" } });
  const { enrollmentInstallCommand } = createAgentEnrollment({});
  assert.throws(() => enrollmentInstallCommand({}), /没有可恢复的部署命令/);
  const command = enrollmentInstallCommand({ token: "quoted'token", name: "edge'node" });
  assert.ok(command.includes("'quoted'\\''token' 'edge'\\''node'"));
  assert.ok(command.includes("X-QControlHub-Enrollment: quoted'\\''token"));
  assert.ok(command.includes("https://panel.example.invalid/install-agent.sh"));
} finally {
  if (previousLocation) Object.defineProperty(globalThis, "location", previousLocation);
  else delete globalThis.location;
}

const requests = [];
const state = { data: {} };
const display = komari.createAgentKomariDisplay({
  api: (path) => new Promise((resolve, reject) => requests.push({ path, resolve, reject })),
  state,
  esc: String,
  bytes: String,
});
const root = { querySelector: () => null };
const agent = { id: "alpha", labels: { komari_uuid: "binding-one" } };
display.loadKomariDisplay(agent, root);
display.loadKomariDisplay(agent, root);
assert.equal(requests.length, 1, "repeated cards must share an in-flight Komari read");
requests[0].resolve({ uuid: "binding-one", server: { uuid: "binding-one" } });
await new Promise(setImmediate);
display.loadKomariDisplay(agent, root);
assert.equal(requests.length, 1, "a rebuilt card must reuse account-scoped data");

state.data = {};
display.loadKomariDisplay(agent, root);
assert.equal(requests.length, 2, "switching accounts must discard the previous Komari cache");
requests[1].reject(new Error("temporary provider failure"));
await new Promise(setImmediate);
assert.equal(state.data.agentKomari.alpha, undefined, "a failed read must remain retryable");
display.loadKomariDisplay(agent, root);
assert.equal(requests.length, 3);
requests[2].resolve({ uuid: "binding-one", server: { uuid: "binding-one" } });
await new Promise(setImmediate);
display.loadKomariDisplay({
  ...agent,
  labels: { komari_uuid: "binding-two" },
  komari: { uuid: "binding-two", traffic_used: 10 },
}, root);
assert.equal(requests.length, 3, "a new inline binding must render without another request");
assert.equal(state.data.agentKomari.alpha.uuid, "binding-two");
assert.equal(state.data.agentKomari.alpha.link.server.traffic_used, 10);

console.log("Focused feature module API and account isolation smoke passed");
