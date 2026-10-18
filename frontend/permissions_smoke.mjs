import assert from "node:assert/strict";
import { createPermissionChecker } from "./modules/permissions.js";

const state = { session: null };
const can = createPermissionChecker(state);
assert.equal(can("agents.read"), false);
assert.equal(can("agent-access.read"), false);
assert.equal(can("users.manage"), false);

state.session = { role: "user", permissions: ["agents.read", "tasks.execute", "users.manage", "agents.manage"] };
assert.equal(can("agent-access.read"), true);
assert.equal(can("agents.read"), true);
assert.equal(can("system-bbr.read"), true);
assert.equal(can("users.manage"), false, "a supplied permission must never grant administrator-only user management");
assert.equal(can("settings.manage"), false);
assert.equal(can("host.manage"), true);
assert.equal(can("operator"), true);
assert.equal(can("host.manage", {}), false, "user host operations require explicit node ownership");
assert.equal(can("host.manage", { can_manage: false }), false);
assert.equal(can("host.manage", { can_manage: true }), true);
assert.equal(can("operator", { can_manage: false }), false);
assert.equal(can("agents.manage", { can_manage: false }), false);

state.session.permissions = ["agents.read"];
assert.equal(can("host.manage", { can_manage: true }), false, "node ownership must not replace the task permission");

for (const role of ["operator", "auditor", "readonly"]) {
  state.session = { role };
  assert.equal(can("agents.read"), false, "historical roles must not invent grants absent from the API session");
  assert.equal(can("users.manage"), false);
  assert.equal(can("settings.manage"), false);
  assert.equal(can("host.manage"), false);
}

state.session = { role: "admin" };
assert.equal(can("users.manage"), true);
assert.equal(can("host.manage", { can_manage: false }), false, "node scope applies before role-wide capabilities");
state.session = { role: "user", permissions: ["tasks.execute"] };
const shared = { can_manage: false, features: ["shared-core-lifecycle-v1"], shared_allocation: { enabled: true, status: "accepted" } };
assert.equal(can("instance.manage", shared), true, "instance operations do not require host administration");
assert.equal(can("host.manage", shared), false);
for (const status of ["pending", "rejected", "revoked"]) {
  assert.equal(can("instance.manage", { ...shared, shared_allocation: { enabled: true, status } }), false);
}
assert.equal(can("instance.manage", { ...shared, shared_allocation: { enabled: false, status: "accepted" } }), false);
assert.equal(can("instance.manage", { ...shared, can_manage: true }), false, "an owned node is never a recipient instance");
assert.equal(can("host.manage", { can_manage: true }), true);
state.session = null;
assert.equal(can("users.manage"), false, "a checker must not retain the previous account's privileges");
assert.equal(can("agents.read"), false);

console.log("Extracted permission checker smoke passed");
