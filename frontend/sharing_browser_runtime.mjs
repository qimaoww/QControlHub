import { createAgentSharing } from "./modules/agent-sharing.js";

const assert = (value, message) => { if (!value) throw new Error(message); };
const esc = value => String(value ?? "").replace(/[&<>"']/g, char => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
})[char]);
const waitFor = async (check, message) => {
  const deadline = performance.now() + 3000;
  while (!check()) {
    if (performance.now() > deadline) throw new Error(message);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
};
const input = (element, value) => {
  element.value = value;
  element.dispatchEvent(new Event("input", { bubbles: true }));
};

export async function testAgentSharingRuntime() {
  const state = { data: {}, session: { user_id: "alice", role: "user" }, navigationEpoch: 1 };
  const agent = { id: "own", name: "Alice own Agent", can_manage: true, features: ["shared-traffic-v1"] };
  let sharing = { revision: 2, shares: [{ id: "shr_bob", username: "bob", user_id: "bob-id", status: "accepted", invitation_revision: 2, ports: [21003], limit_bytes: 1024 ** 3, used_bytes: 64, enabled: true }] };
  const writes = [], notifications = [];
  let hold = false, release, gates = 0, confirm = true;
  const editor = createAgentSharing({
    state, esc, can: () => true, notify: message => notifications.push(message),
    confirmAction: async () => confirm,
    api: async (path, options = {}) => {
      assert(path === "/agents/own/sharing", "borrowed host queried owner sharing");
      if (options.method) {
        const body = JSON.parse(options.body);
        writes.push(body);
        if (hold) await new Promise(resolve => { release = resolve; });
        if (body.revision !== sharing.revision) throw new Error("共享分配已变更，请重新读取");
        sharing = { revision: sharing.revision + 1, shares: body.shares.map(row => ({
          ...row, id: `shr_${row.username}`, user_id: `${row.username}-id`, used_bytes: 64,
          status: row.reinvite ? "pending" : sharing.shares.find(share => share.username === row.username)?.status || "pending", reinvite: false,
        })) };
      }
      return structuredClone(sharing);
    },
  }, { begin: () => { gates++; return () => { gates--; }; } });
  const dialog = () => document.querySelector(".agent-sharing-dialog");
  const form = () => dialog().querySelector("form");
  const field = name => form().querySelector(`[name="${name}"]`);
  await editor.open({ ...agent, can_manage: false });
  assert(!dialog(), "borrowed host opened sharing controls");
  await editor.open(agent);
  assert(gates === 1 && field("username").readOnly, "existing recipients or refresh gate not protected");
  dialog().querySelector("[data-recipient-add]").click();
  assert(form().querySelectorAll("[data-recipient]").length === 2 && editor.hasUnsavedChanges(), "new recipient was not captured");
  dialog().querySelector("[data-recipient-remove]").click();
  assert(form().querySelectorAll("[data-recipient]").length === 1 && !editor.hasUnsavedChanges(), "removing an accidental blank recipient lost the original allocation");
  input(field("ports"), "0");
  form().requestSubmit();
  await waitFor(() => !dialog().querySelector("[data-sharing-error]").hidden, "invalid port has no inline error");
  assert(writes.length === 0, "invalid port reached API");
  input(field("ports"), "21003, 21004");
  editor.close();
  assert(editor.hasUnsavedChanges() && gates === 0, "closing lost the account-scoped draft or leaked the refresh gate");
  await editor.open(agent);
  assert(field("ports").value === "21003, 21004", "reopening did not restore draft");
  sharing.revision++;
  form().requestSubmit();
  await waitFor(() => dialog().querySelector("[data-sharing-error]").textContent.includes("已变更"), "stale revision did not surface conflict");
  assert(editor.hasUnsavedChanges() && field("ports").value === "21003, 21004", "conflict lost unsaved input");
  confirm = false;
  dialog().querySelector("[data-sharing-reload]").click();
  await Promise.resolve();
  assert(field("ports").value === "21003, 21004", "canceling reload discarded the draft");
  confirm = true;
  dialog().querySelector("[data-sharing-reload]").click();
  await waitFor(() => field("ports").value === "21003", "confirmed reload did not recover current revision");
  assert(!editor.hasUnsavedChanges(), "reload retained stale draft");

  input(field("limit_gib"), "2.5");
  hold = true;
  form().requestSubmit();
  form().requestSubmit();
  await waitFor(() => Boolean(release), "save never started");
  assert(writes.length === 2 && field("ports").disabled, "save was duplicated or editable while pending");
  editor.close();
  await editor.open(agent);
  assert(field("ports").disabled, "reopen bypassed in-flight lock");
  const finish = release; release = null; hold = false; finish();
  await waitFor(() => !field("ports").disabled && !editor.hasUnsavedChanges(), "save did not settle reopened dialog");
  assert(sharing.shares[0].limit_bytes === 2.5 * 1024 ** 3, "GiB conversion changed the allowance");
  assert(sharing.shares[0].ports.length === 1 && sharing.shares[0].username === "bob", "recipient scope changed");
  assert(dialog().scrollWidth <= dialog().clientWidth + 1, "sharing dialog overflows mobile viewport");
  assert(dialog().querySelector("header>div").getBoundingClientRect().width > 200,
    "header text was squeezed into the inherited traffic icon column");

  editor.close();
  sharing.shares[0].status = "rejected";
  sharing.revision++;
  await editor.open(agent);
  assert(dialog().querySelector("[data-share-status]").textContent === "已拒绝", "owner cannot see recipient rejection");
  dialog().querySelector("[data-recipient-reinvite]").click();
  assert(editor.hasUnsavedChanges() && dialog().querySelector("[data-share-status]").textContent === "待发送", "reinvite was not staged explicitly");
  editor.close();
  await editor.open(agent);
  assert(dialog().querySelector("[data-recipient-reinvite]").disabled, "reopening lost staged reinvitation");
  form().requestSubmit();
  await waitFor(() => dialog().querySelector("[data-share-status]").textContent === "待接受", "reinvite did not become pending");
  assert(writes.at(-1).shares[0].reinvite && !dialog().querySelector("[data-recipient-reinvite]"), "reinvite was not explicit or could be immediately duplicated");

  input(field("ports"), "21003, 21005");
  hold = true;
  form().requestSubmit();
  await waitFor(() => Boolean(release), "logout race save never started");
  const notificationsBefore = notifications.length;
  state.data = {};
  state.session = { user_id: "carol", role: "user" };
  state.navigationEpoch++;
  editor.close();
  release();
  await waitFor(() => gates === 0, "logout retained an interaction gate");
  await new Promise(resolve => setTimeout(resolve, 30));
  assert(!dialog() && !editor.hasUnsavedChanges(), "previous account restored sharing data after logout");
  assert(notifications.length === notificationsBefore, "old account save notified the new account");
  assert(Object.keys(state.data).length === 0, "old account populated the new account cache");
}
