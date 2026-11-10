import assert from "node:assert/strict";
import {submitConfigImport} from "../modules/config-import.js";

import { liveConfigEngineEligible, liveConfigEditorState, liveConfigReadAction, liveConfigSnapshotReusable, submitLiveConfigChange } from "../modules/live-config-state.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const pendingMigrationSource = {
  content: '{"inbounds":[{"tag":"original"}]}',
  taskId: "tsk_snapshot",
};
const migrationEditor = liveConfigEditorState({ sourceMode:"import",
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
let migrationTaskAttempts=0;
const original = JSON.stringify(pendingMigrationSource);
await assert.rejects(submitConfigImport({api:async(path,options)=>{
 assert.equal(path,"/agents/agt_snapshot/configs/sing-box/import");
 const body=JSON.parse(options.body);
 assert.equal(body.snapshot_task_id,"tsk_snapshot");
 assert.equal(body.content,undefined,"import only accepts the authorized snapshot identity");
 migrationTaskAttempts++;throw new Error("temporary task failure");
},agent:{id:"agt_snapshot"},engine:"sing-box",source:pendingMigrationSource,form:migrationForm}),/temporary task failure/);
assert.equal(JSON.stringify(pendingMigrationSource),original,"failure preserves the source snapshot");
const result=await submitConfigImport({api:async()=>({id:"tsk_retry"}),agent:{id:"agt_snapshot"},engine:"sing-box",source:pendingMigrationSource,form:migrationForm});
assert.equal(result.task.id,"tsk_retry");
assert.equal(result.saved,undefined,"preparing migration must not save the imported configuration");
await assert.rejects(submitLiveConfigChange({intent:"import"}),/不支持导入/);

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

const requests = [];
for (const intent of ["validate", "deploy"]) {
  const form = new Map([["content", '{"inbounds":[]}'], ["name", "config"], ["version", "1"]]);
  const result = await submitLiveConfigChange({
    api:async (path, options) => {
      requests.push({path, body:JSON.parse(options.body)});
      return {config:{id:"cfg", version:2, content:'{"inbounds":[]}'}, task:{id:"task", action:intent}};
    },
    submitTask:() => assert.fail("save and task must be atomic"),
    agent:{id:"node"}, engine:"xray", intent, form, existingAvailable:false,
  });
  assert.equal(result.task.action, intent);
  assert.equal(requests.at(-1).path, "/agents/node/configs/xray/source");
  assert.equal(requests.at(-1).body.version, 1);
  assert.equal(requests.at(-1).body.expected_instance_id, "");
}
assert.equal(requests.length, 2);
}

assert.equal(liveConfigEditorState({existingAvailable:true,canOperate:true,formContent:"manual",sourceContent:"external"}).content,"manual");
assert.equal(liveConfigEditorState({existingAvailable:true,canOperate:true,formContent:"manual"}).readOnly,false);
