import assert from "node:assert/strict";

import { assertAgentConfigBaseline, liveConfigEngineEligible, liveConfigEditorState, liveConfigReadAction, liveConfigSnapshotReusable, submitLiveConfigChange } from "../modules/configs.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
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
  api: async (path, options) => {
    if (path !== "/tasks") throw new Error("retry unexpectedly rewrote snapshot");
    assert.equal(JSON.parse(options.body).expected_config_version, savedMigrationConfig.version, "import retry must pin the saved snapshot version");
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
const snapshotNow = Date.now();
assert.equal(liveConfigSnapshotReusable({ content:"node", readAt:snapshotNow - 599_999 }, snapshotNow), true);
assert.equal(liveConfigSnapshotReusable({ content:"node", readAt:snapshotNow - 600_000 }, snapshotNow), false,
  "volatile Agent snapshots must not outlive the PostgreSQL 600-second cache window");
assert.equal(liveConfigSnapshotReusable({ content:"saved draft", saved:true, readAt:0 }, snapshotNow), true,
  "saved drafts are not Agent snapshot cache entries and must remain available");

assert.doesNotThrow(() => assertAgentConfigBaseline("agent bytes\n", "agent bytes\n"));
let baselineConflict;
try { assertAgentConfigBaseline("cached bytes\n", "changed on agent\n"); }
catch (error) { baselineConflict = error; }
assert.match(baselineConflict?.message || "", /部署前核验发现 Agent 当前配置已在页面读取后发生变化/);
assert.equal(baselineConflict.deployPreflight, true);

const deployOrder = [];
const deployForm = new Map([
  ["name", "verified deployment"],
  ["description", "preflight order"],
  ["content", "edited target\n"],
  ["version", "0"],
]);
await submitLiveConfigChange({
  api: async (_path, options) => {
    deployOrder.push("save");
    return { id: "cfg_verified", version: 1, ...JSON.parse(options.body) };
  },
  submitTask: async () => {
    deployOrder.push("deploy");
    return { id: "tsk_verified" };
  },
  agent: { id: "agt_verified" },
  engine: "xray",
  intent: "deploy",
  form: deployForm,
  source: { content: "cached bytes\n", agentContent: "cached bytes\n" },
  existingAvailable: false,
  savedConfig: null,
  beforeDeploy: async () => { deployOrder.push("verify"); },
});
assert.deepEqual(deployOrder, ["verify", "save", "deploy"],
  "deployment must force Agent verification before persisting the draft or creating its task");

let preflightMutationAttempted = false;
await assert.rejects(submitLiveConfigChange({
  api: async () => { preflightMutationAttempted = true; },
  submitTask: async () => { preflightMutationAttempted = true; },
  agent: { id: "agt_conflict" },
  engine: "xray",
  intent: "deploy",
  form: deployForm,
  source: { content: "cached bytes\n", agentContent: "cached bytes\n" },
  existingAvailable: false,
  savedConfig: null,
  beforeDeploy: async () => assertAgentConfigBaseline("cached bytes\n", "changed on agent\n"),
}), /已停止保存和部署/);
assert.equal(preflightMutationAttempted, false,
  "a changed Agent baseline must block both the config save and deploy task");

}
