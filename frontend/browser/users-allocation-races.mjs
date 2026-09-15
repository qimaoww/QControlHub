import { installUsers } from "../modules/users.js";
import { assert, esc, waitFor, input } from "./users-helpers.mjs";

export async function testAllocationRaceRuntime() {
  const state = { route: "users", data: {}, session: { role: "admin", user_id: "admin" } };
  const users = ["alice", "bob"].map(id => ({ id, username: id, role: "user" }));
  const agents = [{ id: "shared", name: "Shared", capabilities: ["mihomo"], features: ["shared-traffic-v1", "shared-engines-v1", "independent-egress-v1"] }];
  const access = new Map(users.map(user => [user.id, { isolated: true, revision: 4, shares: [{
    id: `share_${user.id}`, agent_id: "shared", enabled: true, status: "accepted",
    engines: ["mihomo"], ports: [31001], limit_bytes: 1024 ** 3, used_bytes: 128,
  }] }]));
  const writes = [], notices = [];
  let delayRead = false, releaseRead, failRead = false, delayWrite = false, releaseWrite;
  let delayConfirm = false, releaseConfirm, confirmations = 0;
  const pages = installUsers({
    state, esc, notify: message => notices.push(message),
    shell: markup => { document.body.innerHTML = `<main>${markup}</main>`; },
    confirmAction: async () => {
      confirmations++;
      if (delayConfirm) await new Promise(resolve => { releaseConfirm = resolve; });
      return true;
    },
    api: async (path, options = {}) => {
      if (path === "/users") return structuredClone(users);
      if (path === "/agents") return structuredClone(agents);
      const id = path.split("/")[2];
      assert(path.endsWith("/agent-access") && access.has(id), "allocation race used an unexpected API");
      if (!options.method) {
        if (failRead) throw new Error("读取分配失败");
        const result = structuredClone(access.get(id));
        if (delayRead) {
          delayRead = false;
          await new Promise(resolve => { releaseRead = resolve; });
        }
        return result;
      }
      const body = JSON.parse(options.body);
      writes.push(body);
      if (delayWrite) await new Promise(resolve => { releaseWrite = resolve; });
      const previous = access.get(id);
      if (body.revision !== previous.revision) throw new Error("分配已变更，请重新读取后保存。");
      access.set(id, { ...body, revision: body.revision + 1, shares: body.shares.map(share => ({ ...previous.shares.find(item => item.agent_id === share.agent_id), ...share })) });
      return structuredClone(access.get(id));
    },
  });
  const form = () => document.querySelector("[data-user-access-form]");
  const open = () => document.querySelector('[data-allocation-edit="shared"]').click();
  const cancel = () => form().querySelector("[data-allocation-close]").click();
  const reload = () => form().querySelector("[data-allocation-reload]");
  const error = () => form().querySelector("[data-user-error]").textContent;
  await pages.users();
  open();
  input(form().elements.ports, "31001, invalid");
  form().requestSubmit();
  delayRead = true;
  reload().click();
  reload().click();
  await waitFor(() => releaseRead, "allocation reload did not start");
  assert(confirmations === 1 && form().querySelector('[type="submit"]').disabled, "reload allowed duplicate confirmations or a concurrent save");
  cancel();
  open();
  const reopened = form();
  input(reopened.elements.ports, "33001, invalid");
  releaseRead();
  await new Promise(resolve => setTimeout(resolve, 20));
  assert(form() === reopened && form().elements.ports.value === "33001, invalid", "stale reload overwrote a newly opened editor in the same dialog");
  form().requestSubmit();
  failRead = true;
  reload().click();
  await waitFor(() => error().includes("读取分配失败"), "reload failure was not reported");
  assert(form().elements.ports.value === "33001, invalid" && !form().querySelector('[type="submit"]').disabled && pages.hasUnsavedChanges(),
    "failed reload discarded or locked the current draft");
  failRead = false;
  cancel();
  open();
  input(form().elements.ports, "31007");
  access.get("alice").revision++;
  access.get("alice").shares[0].ports = [31008];
  await pages.users();
  assert(form().elements.ports.value === "31007" && document.querySelector("[data-allocation-list]").textContent.includes("31008"),
    "refresh did not distinguish an unsaved draft from the latest saved result");
  form().requestSubmit();
  await waitFor(() => error().includes("分配已变更"), "restored draft silently adopted a newer revision");
  assert(writes.at(-1).revision === 4 && access.get("alice").shares[0].ports[0] === 31008, "draft overwrote a concurrent allocation edit");
  reload().click();
  await waitFor(() => form().elements.ports.value === "31008", "confirmed reload did not load the current revision");
  input(form().elements.ports, "31009");
  form().requestSubmit();
  await waitFor(() => !form(), "reloaded allocation could not save");
  assert(writes.at(-1).revision === 5 && access.get("alice").shares[0].used_bytes === 128, "reload lost revision protection or usage");

  delayConfirm = true;
  const beforeRevoke = writes.length;
  document.querySelector("[data-allocation-revoke]").click();
  await waitFor(() => releaseConfirm, "revocation confirmation did not start");
  state.data.userID = "bob";
  await pages.users();
  delayConfirm = false;
  releaseConfirm();
  await waitFor(() => !state.data.userAccessSaves.size, "stale revocation confirmation did not settle");
  assert(writes.length === beforeRevoke && access.get("alice").shares[0].enabled, "navigating away during confirmation revoked the former selection");

  state.data.userID = "alice";
  await pages.users();
  open();
  input(form().elements.limit_gib, "2");
  delayWrite = true;
  form().requestSubmit();
  await waitFor(() => releaseWrite, "delayed allocation save did not start");
  const previousData = state.data, beforeNotices = notices.length;
  state.data = {};
  state.session = { role: "admin", user_id: "another-admin" };
  await pages.users();
  open();
  const newSessionForm = form();
  input(newSessionForm.elements.ports, "31111");
  releaseWrite();
  await waitFor(() => !previousData.userAccessSaves.size, "old-session save did not settle");
  assert(form() === newSessionForm && form().elements.ports.value === "31111" && state.data.userDrafts.has("alice"),
    "old-session save replaced the new session's editor or draft");
  assert(notices.length === beforeNotices, "old-session save leaked a notification into the new session");
  cancel();
}
