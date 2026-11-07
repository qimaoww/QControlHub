import assert from "node:assert/strict";
import * as agents from "./modules/agents.js";
import * as addresses from "./modules/agent-addresses.js";
import * as batch from "./modules/agent-batch.js";
import * as drag from "./modules/agent-card-drag.js";
import * as coreActions from "./modules/agent-core-actions.js";
import * as agentRefresh from "./modules/agent-refresh.js";
import { bindCodeEditors } from "./modules/code-editor.js";
import { createAgentEnrollment } from "./modules/agent-enrollment.js";

// Existing consumers keep using the route modules; new consumers can depend on
// the focused helpers without importing the route's rendering implementation.
for (const [facade, owner, names] of [
  [agents, addresses, ["formatHostPort", "manualConnectionAddressNote", "publicAddressRows", "updatePublicIPDisplays"]],
  [agents, batch, ["batchAgentEligibility", "batchSelectAllState"]],
  [agents, drag, ["animateNodeCardDrop", "clearNodeCardDragState", "nodeCardDropIndex"]],
  [agents, coreActions, ["developmentSourceVisible", "coreSourceForInstall"]],
  [agents, agentRefresh, ["agentStructureSignature"]],
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

console.log("Focused feature module API and account isolation smoke passed");
