import { installUsers } from "./modules/users.js";

const assert = (value, message) => { if (!value) throw new Error(message); };
const esc = value => String(value ?? "").replace(/[&<>"']/g, char => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
})[char]);
const waitFor = async (condition, message) => {
  const deadline = performance.now() + 3000;
  while (!condition()) {
    if (performance.now() > deadline) throw new Error(message);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
};
const input = (element, value) => {
  element.value = value;
  element.dispatchEvent(new Event("input", { bubbles: true }));
};

export async function testUsersRuntime(preview = false) {
  const state = { route: "users", data: {}, session: { role: "admin", user_id: "admin" } };
  const items = [
    { id: "alice", username: "alice", display_name: "Alice", role: "user", permissions: ["agents.read"] },
    { id: "bob", username: "bob", display_name: "Bob", role: "user", permissions: ["agents.read"] },
    { id: "admin", username: "admin", role: "admin", permissions: [] },
  ];
  const agents = ["shared", "reserved", "available", "owned"].map(id => ({
    id, name: id === "shared" ? "共享 Agent" : id, capabilities: ["mihomo", "xray"],
    features: ["shared-traffic-v1", "shared-engines-v1", "independent-egress-v1"],
  }));
  const access = new Map(items.map((user, index) => [user.id, {
    isolated: user.role !== "admin", revision: 4, owned_agent_ids: ["owned"], shares: user.role === "admin" ? [] : [{
      id: `shr_${user.id}`, status: "accepted", invitation_revision: 2,
      agent_id: "shared", agent_name: "共享 Agent", enabled: true, engines: ["mihomo"], ports: [31001 + index],
      used_bytes: 128, limit_bytes: 1024 ** 3,
    }, {
      id: `shr_reserved_${user.id}`, status: "rejected", invitation_revision: 3,
      agent_id: "reserved", enabled: false, engines: ["xray"], ports: [32001 + index],
      used_bytes: 256, limit_bytes: 15250032,
    }],
  }]));
  const writes = [], notifications = [];
  let conflict = false, confirm = true, confirmCount = 0, holdSave, releaseSave, failedUser = "";
  let confirmGate, releaseConfirm, purgeGate, releasePurge, rejectConfirmation = 0;
  const api = async (path, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : undefined;
    if (options.method) writes.push({ path, body, method: options.method });
    if (path === "/agents") return structuredClone(agents);
    if (path === "/users" && !body) return structuredClone(items);
    if (path === "/agent-access") return structuredClone(access.get(state.session.user_id));
    if (path === "/users" && body) {
      const user = { ...body, id: "new-user", disabled: false };
      items.push(user);
      access.set(user.id, { isolated: body.agent_isolation, revision: 1, shares: [] });
      return structuredClone(user);
    }
    const userID = path.split("/")[2];
    if (path.endsWith("/purge") && options.method === "POST") {
      if (purgeGate) await new Promise(resolve => { releasePurge = resolve; });
      const index = items.findIndex(user => user.id === userID);
      if (index < 0) throw new Error("账号不存在");
      items.splice(index, 1);
      access.delete(userID);
      return null;
    }
    if (path.endsWith("/agent-access")) {
      if (!body && userID === failedUser) throw new Error("用户读取失败");
      if (body) {
        if (holdSave) await new Promise(resolve => { releaseSave = resolve; });
        if (conflict || body.revision !== access.get(userID).revision) throw new Error("分配已变更，请重新读取后保存。");
        const previous = access.get(userID);
        for (const share of body.shares) {
          const prior = previous.shares.find(item => item.agent_id === share.agent_id);
          if (share.reinvite && (!share.enabled || prior?.status !== "rejected"))
            throw new Error("only a rejected share can be reinvited");
        }
        access.set(userID, { ...previous, ...body, revision: body.revision + 1, shares: body.shares.map(share => {
          const prior = previous.shares.find(item => item.agent_id === share.agent_id);
          const invite = share.enabled && (share.reinvite || !prior?.enabled || (prior.status === "accepted" && share.engines.some(engine => !prior.engines.includes(engine))));
          return { id: `shr_${userID}_${share.agent_id}`, ...prior, ...share, status: invite ? "pending" : prior?.status || "pending", reinvite: false };
        }) });
      }
      return structuredClone(access.get(userID));
    }
    throw new Error(`Unexpected users API: ${path}`);
  };
  const pages = installUsers({
    api, state, esc, notify: message => notifications.push(message),
    confirmAction: async () => {
      confirmCount++;
      if (confirmGate) await new Promise(resolve => { releaseConfirm = resolve; });
      return confirm && confirmCount !== rejectConfirmation;
    },
    shell: markup => {
      document.body.innerHTML = `<nav>${items.map(user => `<a href="#users" data-user-select="${user.id}">${user.username}</a>`).join("")}</nav><main>${markup}</main>`;
    },
  });
  await pages.users();
  if (preview) return;
  const form = () => document.querySelector("[data-user-access-form]");
  const row = () => form().querySelector("[data-share-row]");
  const savedRow = () => document.querySelector('[data-allocation-agent="shared"]');
  const open = () => document.querySelector('[data-allocation-edit="shared"]').click();
  const cancel = () => form().querySelector("[data-allocation-close]").click();
  const select = async id => {
    document.querySelector(`[data-user-select="${id}"]`).click();
    const user = items.find(item => item.id === id);
    await waitFor(() => document.querySelector(".users-toolbar h2")?.textContent === (user.display_name || user.username), "user switch did not complete");
  };
  assert(!form() && !document.querySelector("[data-allocation-list] input"), "saved allocations are still a bulk edit form");
  assert(!document.querySelector('[name="isolated"], .user-isolation, .settings-section-number'), "allocation page repeats account setup or exposes isolation");
  assert(document.querySelector(".user-owned-nodes li")?.textContent === "owned", "owned nodes are not separate from shared allocations");
  if (innerWidth <= 820) {
    assert(document.querySelector("[data-user-mobile-select]").getBoundingClientRect().height > 0, "mobile user selector is unavailable");
  }
  open();
  input(row().querySelector('[name="ports"]'), "31009");
  assert(!savedRow().textContent.includes("31009"), "unsaved fields changed the saved allocation list");
  cancel();
  assert(!form() && !pages.hasUnsavedChanges() && writes.length === 0, "canceling edit saved or retained a draft");
  open();
  input(row().querySelector('[name="ports"]'), "31008");
  form().closest("dialog").dispatchEvent(new Event("cancel", { cancelable: true }));
  assert(!form() && !pages.hasUnsavedChanges() && writes.length === 0, "Escape changed an allocation");
  open();
  failedUser = "bob";
  document.querySelector('[data-user-select="bob"]').click();
  assert(form().inert, "switching users left the previous allocation editable");
  form().requestSubmit();
  assert(writes.length === 0, "a stale visible form saved during a user switch");
  await waitFor(() => notifications.includes("用户读取失败"), "failed user selection was not reported");
  assert(state.data.userID === "alice" && document.querySelector("[data-user-mobile-select]").value === "alice" && !form().inert,
    "failed selection left the visible form bound to a different user");
  failedUser = "";
  input(row().querySelector('[name="ports"]'), "31001, invalid");
  input(row().querySelector('[name="limit_gib"]'), "2.5");
  row().querySelector('[name="engines"][value="xray"]').click();
  assert(pages.hasUnsavedChanges(), "allocation edits were not captured");
  await select("bob");
  assert(!form(), "an unedited user inherited another user's editor");
  open();
  assert(row().querySelector('[name="ports"]').value === "31002", "another user's port draft leaked");
  assert(!row().querySelector('[name="engines"][value="xray"]').checked, "another user's engine draft leaked");
  await select("alice");
  assert(row().querySelector('[name="ports"]').value === "31001, invalid", "switching users lost an invalid draft");
  assert(row().querySelector('[name="limit_gib"]').value === "2.5", "switching users lost the allowance draft");
  assert(row().querySelector('[name="engines"][value="xray"]').checked, "switching users lost the engine draft");
  form().requestSubmit();
  await waitFor(() => !form().querySelector("[data-user-error]").hidden, "invalid port did not produce a local error");
  assert(writes.length === 0, "invalid port reached the API");
  input(row().querySelector('[name="ports"]'), "31001, 31003");
  conflict = true;
  form().requestSubmit();
  await waitFor(() => form().querySelector("[data-user-error]").textContent.includes("分配已变更") && !form().querySelector('[type="submit"]').disabled, "stale allocation conflict did not recover");
  assert(row().querySelector('[name="ports"]').value === "31001, 31003", "conflict discarded entered ports");
  confirm = false;
  form().querySelector("[data-allocation-reload]").click();
  await new Promise(resolve => setTimeout(resolve, 20));
  assert(pages.hasUnsavedChanges() && !form().querySelector('[type="submit"]').disabled, "canceling reload discarded or locked the draft");
  confirm = true;
  form().querySelector("[data-allocation-reload]").click();
  await waitFor(() => row().querySelector('[name="ports"]').value === "31001", "confirmed reload retained stale fields");
  assert(!pages.hasUnsavedChanges(), "confirmed reload retained dirty state");
  assert(!row().querySelector('[name="engines"][value="xray"]').checked, "reload retained a stale engine grant");
  conflict = false;
  input(row().querySelector('[name="limit_gib"]'), "2.5");
  holdSave = true;
  form().requestSubmit();
  await waitFor(() => form().querySelector('[type="submit"]').disabled, "save did not prevent mid-request edits");
  assert([...form().querySelectorAll("input, button")].every(control => control.disabled), "save left allocation controls editable");
  form().closest("dialog").dispatchEvent(new Event("cancel", { cancelable: true }));
  assert(form(), "Escape closed an in-flight save");
  await select("bob");
  open();
  input(row().querySelector('[name="ports"]'), "31002, pending-draft");
  pages.captureDraft();
  state.route = "my-quota";
  await pages.myQuota();
  state.route = "users";
  state.data.userID = "alice";
  await pages.users();
  assert(form().querySelector('[type="submit"]').disabled, "returning to a pending save unlocked the allocation");
  const pendingWrites = writes.length;
  form().requestSubmit();
  assert(writes.length === pendingWrites, "navigation allowed a duplicate in-flight save");
  releaseSave();
  await waitFor(() => notifications.includes("分配已保存"), "allocation did not save");
  holdSave = false;
  assert(access.get("alice").shares[0].limit_bytes === 2.5 * 1024 ** 3, "GiB allocation was rounded incorrectly");
  assert(access.get("alice").shares[0].engines.join(",") === "mihomo", "allocation widened engine scope");
  assert(access.get("alice").shares[1].limit_bytes === 15250032 && access.get("alice").shares[1].ports[0] === 32001 && !access.get("alice").shares[1].enabled,
    "editing one allocation lost another node's quota or revoked port reservation");
  assert(!("reinvite" in writes.at(-1).body.shares[1]), "saving one node replayed another invitation");
  assert(!state.data.userDrafts.has("alice") && !state.data.userAccessSaves.size, "saving across navigation left a stale draft or request");
  assert(!form() && !document.querySelector('[data-allocation-edit="shared"]').disabled, "completed save did not return to the unlocked result list");
  await select("bob");
  assert(row().querySelector('[name="ports"]').value === "31002, pending-draft", "another user's pending draft was lost after saving");
  cancel();
  await select("alice");
  assert(!pages.hasUnsavedChanges(), "successful save left stale draft state");
  assert(access.get("alice").isolated, "saving allocations disabled resource isolation");
  access.get("bob").shares[0].status = "rejected";
  await select("bob");
  assert(savedRow().querySelector("[data-share-status]").textContent === "已拒绝", "admin cannot see rejection");
  open();
  input(row().querySelector('[name="limit_gib"]'), "3");
  form().requestSubmit();
  await waitFor(() => !form(), "editing rejected terms did not finish");
  assert(savedRow().querySelector("[data-share-status]").textContent === "已拒绝" && !writes.at(-1).body.shares[0].reinvite,
    "ordinary edit silently resent a rejected invitation");
  savedRow().querySelector("[data-allocation-invite]").click();
  assert(pages.hasUnsavedChanges() && savedRow().textContent.includes("已拒绝"), "opening reinvite changed saved status or lost its intent");
  await select("alice");
  await select("bob");
  assert(row().dataset.reinvite === "true" && document.querySelector("#user-allocation-dialog-title").textContent === "重新邀请", "user switch lost staged reinvite");
  form().requestSubmit();
  await waitFor(() => !form() && savedRow().querySelector("[data-share-status]").textContent === "待接受", "admin reinvite did not stay pending");
  assert(writes.at(-1).body.shares[0].reinvite === true, "admin reinvite was not explicit");
  const beforeRevoke = writes.length, beforeRevokeConfirms = confirmCount;
  confirm = false;
  savedRow().querySelector("[data-allocation-revoke]").click();
  await waitFor(() => !state.data.userAccessSaves.size, "canceled revocation stayed locked");
  assert(writes.length === beforeRevoke && confirmCount === beforeRevokeConfirms + 1, "canceling revocation changed sharing");
  confirm = true;
  savedRow().querySelector("[data-allocation-revoke]").click();
  await waitFor(() => savedRow().textContent.includes("已撤销"), "explicit revocation did not update the list");
  assert(access.get("bob").shares[0].ports[0] === 31002 && access.get("bob").shares[0].used_bytes === 128, "revocation cleared ports or usage");
  open();
  input(row().querySelector('[name="ports"]'), "");
  form().requestSubmit();
  await waitFor(() => !form(), "revoked allocation could not release its ports");
  assert(!access.get("bob").shares[0].enabled && access.get("bob").shares[0].ports.length === 0, "editing revoked sharing implicitly restored it");
  for (const [userID, previousStatus, otherUser] of [["bob", "pending", "alice"], ["alice", "accepted", "bob"]]) {
    await select(userID);
    const before = structuredClone(access.get(userID).shares[0]);
    assert(before.status === previousStatus, `restore fixture is not ${previousStatus}`);
    if (before.enabled) {
      savedRow().querySelector("[data-allocation-revoke]").click();
      await waitFor(() => savedRow().textContent.includes("已撤销"), `could not revoke ${previousStatus} sharing`);
    }
    const beforeRestore = writes.length, restoreRevision = access.get(userID).revision;
    savedRow().querySelector("[data-allocation-invite]").click();
    assert(pages.hasUnsavedChanges() && !access.get(userID).shares[0].enabled && writes.length === beforeRestore,
      "opening restore lost its intent or changed the saved allocation");
    await select(otherUser);
    await select(userID);
    assert(form() && row().dataset.reinvite === "false" && row().querySelector('[name="enabled"]').checked
      && document.querySelector("#user-allocation-dialog-title").textContent === "重新邀请",
      `switching users lost the revoked ${previousStatus} restoration or staged an invalid reinvite`);
    form().requestSubmit();
    await waitFor(() => !form() && savedRow().querySelector("[data-share-status]").textContent === "待接受",
      `restoring revoked ${previousStatus} sharing did not stay pending`);
    const write = writes.at(-1).body, restored = access.get(userID).shares[0];
    assert(write.revision === restoreRevision && write.shares[0].enabled && write.shares[0].reinvite === false,
      `restoring revoked ${previousStatus} sharing sent an invalid reinvite or revision`);
    assert(restored.enabled && restored.status === "pending" && restored.limit_bytes === before.limit_bytes
      && restored.used_bytes === before.used_bytes && restored.ports.join(",") === before.ports.join(",")
      && restored.engines.join(",") === before.engines.join(","),
      `restoring revoked ${previousStatus} sharing changed its terms or skipped consent`);
    assert(!pages.hasUnsavedChanges(), "restoring sharing retained a stale draft");
  }
  await select("admin");
  assert(!document.querySelector('[name="isolated"], [data-share-row]'), "administrator can accidentally be isolated");
  document.querySelector("[data-user-create]").click();
  const dialog = document.querySelector("[data-user-dialog]");
  let account = dialog.querySelector("form");
  input(account.elements.username, "new-user");
  input(account.elements.password, "test-password-only");
  account.querySelector("details").open = true;
  await Promise.all(dialog.getAnimations().map(animation => animation.finished));
  const body = dialog.querySelector(".traffic-edit-body").getBoundingClientRect();
  assert(dialog.scrollWidth <= dialog.clientWidth + 1 && body.width > 0, "user form overflows its dialog");
  let previousBottom = 0;
  for (const name of ["username", "display_name", "password", "role"]) {
    const field = account.elements[name];
    const bounds = field.getBoundingClientRect();
    assert(getComputedStyle(field.parentElement).display === "grid", "account labels are not stacked above their inputs");
    assert(bounds.width >= body.width - 45 && bounds.top >= previousBottom, "account fields are inline, overlapping or too narrow");
    previousBottom = bounds.bottom;
  }
  const footerBottom = account.querySelector("footer").getBoundingClientRect().bottom;
  assert(footerBottom <= innerHeight + 1, `account save buttons are clipped: ${footerBottom} > ${innerHeight}`);
  account.requestSubmit();
  await waitFor(() => writes.some(write => write.path === "/users"), "create user did not submit");
  assert(writes.find(write => write.path === "/users").body.agent_isolation === true, "new regular user was created with unrestricted fleet access");
  assert(["enrollment.manage", "agents.manage", "settings.manage"].every(permission => writes.find(write => write.path === "/users").body.permissions.includes(permission)), "new users cannot manage their own nodes and integrations");
  await waitFor(() => state.data.userID === "new-user" && !document.querySelector("[data-user-dialog]").open, "created user did not load");
  // Deleting an account is a two-step administrator action; the account row
  // and its grants disappear, while the fleet itself is untouched.
  await select("alice");
  assert(document.querySelector("[data-user-delete]"), "administrator cannot delete a regular account");
  await select("admin");
  assert(!document.querySelector("[data-user-delete]"), "administrator account offered deletion");
  await select("alice");
  confirm = false;
  document.querySelector("[data-user-delete]").click();
  await Promise.resolve();
  assert(!writes.some(write => write.path === "/users/alice/purge"), "canceling confirmation deleted the account");
  confirm = true;
  rejectConfirmation = confirmCount + 2;
  document.querySelector("[data-user-delete]").click();
  await waitFor(() => !document.querySelector("[data-user-delete]").disabled, "canceling the second confirmation left deletion locked");
  assert(!writes.some(write => write.path === "/users/alice/purge"), "canceling the second confirmation deleted the account");
  rejectConfirmation = 0;

  confirmGate = true;
  const confirmButton = document.querySelector("[data-user-delete]");
  const beforeDuplicate = confirmCount;
  confirmButton.click();
  confirmButton.click();
  assert(confirmCount === beforeDuplicate + 1 && confirmButton.disabled, "confirmation allowed overlapping delete flows");
  await select("bob");
  confirmGate = false;
  releaseConfirm();
  await waitFor(() => !state.data.userDeletions.size, "stale confirmation did not settle");
  assert(!writes.some(write => write.path === "/users/alice/purge"), "switching accounts during confirmation deleted the previous selection");
  await select("alice");
  open();
  input(row().querySelector('[name="ports"]'), "31001, unsaved-before-delete");
  assert(state.data.userDrafts.has("alice"), "delete test did not capture the unsaved draft");

  const confirmsBefore = confirmCount;
  purgeGate = true;
  document.querySelector("[data-user-delete]").click();
  await waitFor(() => writes.some(write => write.path === "/users/alice/purge"), "account deletion did not submit");
  assert(confirmCount === confirmsBefore + 2, "account deletion was not double-confirmed");
  assert(writes.find(write => write.path === "/users/alice/purge").method === "POST", "account deletion used the wrong method");
  await select("bob");
  await select("alice");
  assert(document.querySelector("[data-user-delete]").disabled, "returning to an in-flight deletion allowed duplicate submission");
  document.querySelector("[data-user-delete]").click();
  assert(writes.filter(write => write.path === "/users/alice/purge").length === 1, "deletion submitted more than once");
  purgeGate = false;
  releasePurge();
  await waitFor(() => !items.some(user => user.id === "alice") && state.data.userID === "bob", "deleted account stayed selected");
  assert(notifications.includes("账号“Alice”已删除"), "account deletion was not reported");
  assert(!state.data.userDrafts.has("alice") && !state.data.userDeletions.size && !pages.hasUnsavedChanges(),
    "refresh resurrected the deleted account's draft or pending state");
  state.route = "my-quota";
  state.session = { role: "user", user_id: "bob" };
  access.get("bob").shares[0].enabled = false;
  access.get("bob").shares[0].agent_name = "<img src=x onerror=alert(1)>";
  await pages.myQuota();
  assert(document.body.textContent.includes("已撤销"), "revoked allocation was shown as usable");
  assert(!document.querySelector(".user-quota-card img"), "Agent name was not escaped");
  const count = writes.length;
  state.route = "users";
  await pages.users();
  assert(writes.length === count && !document.querySelector("[data-user-create]"), "regular user opened account administration");
  await testAllocationRaceRuntime();
  await testInvitationRuntime();
}

async function testAllocationRaceRuntime() {
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

async function testInvitationRuntime() {
  const state = { route: "my-quota", data: {}, session: { role: "user", user_id: "recipient" } };
  let access = { isolated: true, revision: 4, shares: [
    { id: "shr_pending", agent_id: "shared", agent_name: "共享 Agent", owner_username: "<img src=x onerror=alert(1)>", enabled: true, status: "pending", invitation_revision: 2, engines: ["mihomo", "xray"], ports: [21001], limit_bytes: 1024 ** 3, used_bytes: 64 },
    { id: "shr_revoked", agent_id: "revoked", agent_name: "已撤销 Agent", enabled: false, status: "pending", invitation_revision: 8, engines: [], ports: [], limit_bytes: 0, used_bytes: 0 },
  ] };
  const writes = [], notices = [];
  let hold = false, release, conflict = false, confirm = true, holdRead = false, releaseRead, failRead = false;
  const pages = installUsers({
    state, esc, notify: message => notices.push(message), confirmAction: async () => confirm,
    shell: markup => { document.body.innerHTML = `<main>${markup}</main>`; },
    api: async (path, options = {}) => {
      if (!options.method) {
        assert(path === "/agent-access", "quota page loaded host/user administration");
        if (failRead) throw new Error("邀请读取失败");
        const result = structuredClone(access);
        if (holdRead) await new Promise(resolve => { releaseRead = resolve; });
        return result;
      }
      assert(path === "/agent-access/shr_pending/response" && options.method === "POST", "response addressed an unintended share");
      const input = JSON.parse(options.body), target = access;
      writes.push(input);
      if (hold) await new Promise(resolve => { release = resolve; });
      if (conflict || input.revision !== target.shares[0].invitation_revision) throw new Error("共享邀请已变更，请刷新后重试。");
      target.shares[0].status = input.decision === "accept" ? "accepted" : "rejected";
      target.shares[0].invitation_revision++;
      target.revision++;
      return structuredClone(target);
    },
  });
  const card = () => document.querySelector('[data-quota-share="shr_pending"]');
  const dialog = () => document.querySelector(".agent-invitation-dialog");
  const open = () => card().querySelector("[data-share-open]").click();
  const accept = () => dialog()?.querySelector('[data-share-decision="accept"]');
  const reject = () => dialog()?.querySelector('[data-share-decision="reject"]') || card()?.querySelector('[data-share-decision="reject"]');
  await pages.myQuota();
  assert(card().textContent.includes("待接受") && card().querySelector("[data-share-open]"), "pending invitation lacks a dedicated entry");
  assert(!card().querySelector("[data-share-decision]"), "accept/reject actions still appear inline on the quota page");
  assert(!document.querySelector(".user-quota-card img"), "inviter identity was not escaped");
  assert(!document.querySelector('[data-quota-share="shr_revoked"] :is([data-share-decision],[data-share-open])'), "withdrawn invitation can be accepted");
  assert(card().scrollWidth <= card().clientWidth + 1, "invitation card overflows mobile viewport");
  open();
  assert(dialog()?.open && accept() && reject(), "dedicated dialog lacks consent actions");
  assert(dialog().textContent.includes("共享 Agent") && dialog().textContent.includes("21001") && !dialog().querySelector("img"), "dialog lost terms or failed to escape the owner");
  assert(dialog().textContent.includes("Mihomo / Xray") && !dialog().textContent.includes("sing-box"), "consent terms did not show the exact engine allocation");
  // Measure the final layout, not the shared dialog entrance transform.
  await Promise.all(dialog().getAnimations().map(animation => animation.finished));
  const bounds = dialog().getBoundingClientRect(), footer = dialog().querySelector("footer").getBoundingClientRect();
  assert(dialog().scrollWidth <= dialog().clientWidth + 1 && footer.bottom <= innerHeight + 1, "invitation dialog clips controls or overflows");
  if (innerWidth <= 600) assert(Math.abs(bounds.width - innerWidth) <= 1 && Math.abs(bounds.height - innerHeight) <= 1, "mobile invitation is not a full-page overlay");
  dialog().querySelector("[data-invitation-close]").click();
  assert(!dialog() && writes.length === 0 && card().textContent.includes("待接受"), "closing an invitation rejected it");
  open();
  dialog().dispatchEvent(new Event("cancel", { cancelable: true }));
  assert(!dialog() && writes.length === 0, "Escape changed the invitation decision");
  open();
  hold = true;
  accept().click();
  reject().click();
  assert(writes.length === 1 && accept().disabled && reject().disabled && pages.hasUnsavedChanges(), "duplicate response or unlocked in-flight action");
  await pages.myQuota();
  assert(accept().disabled && document.querySelector("[data-quota-refresh]").disabled, "refresh bypassed in-flight response lock");
  // A read begun before acceptance must not restore the old invitation.
  holdRead = true;
  const oldRead = pages.myQuota();
  await waitFor(() => Boolean(releaseRead), "stale read did not start");
  hold = false;
  release();
  await waitFor(() => card().textContent.includes("已接受"), "acceptance did not update the card");
  releaseRead();
  holdRead = false;
  await oldRead;
  assert(!accept() && card().textContent.includes("已接受") && !pages.hasUnsavedChanges(), "stale read reversed acceptance");
  confirm = false;
  reject().click();
  await Promise.resolve();
  assert(writes.length === 1, "canceling leave changed consent");
  confirm = true;
  reject().click();
  await waitFor(() => card().textContent.includes("已拒绝"), "recipient cannot leave an accepted share");
  assert(!accept() && !reject(), "rejected invitation is still actionable");

  access.shares[0].status = "pending";
  access.shares[0].invitation_revision++;
  await pages.myQuota();
  open();
  conflict = true;
  accept().click();
  await waitFor(() => !dialog().querySelector("[data-invitation-error]").hidden, "stale invitation did not report conflict inside its dialog");
  assert(!accept().disabled && card().textContent.includes("待接受"), "failed response granted access or stranded controls");
  conflict = false;
  access.shares[0].ports = [21002];
  access.shares[0].invitation_revision++;
  holdRead = true;
  releaseRead = null;
  const beforeReload = writes.length;
  dialog().querySelector("[data-invitation-reload]").click();
  await waitFor(() => Boolean(releaseRead), "invitation refresh did not start");
  accept().click();
  reject().click();
  assert(accept().disabled && reject().disabled && writes.length === beforeReload, "refresh allowed a decision on stale invitation terms");
  holdRead = false;
  releaseRead();
  await waitFor(() => dialog()?.textContent.includes("21002"), "invitation refresh did not load changed terms");
  reject().click();
  await waitFor(() => card().textContent.includes("已拒绝"), "pending invitation cannot be rejected");
  assert(!dialog(), "successful response left the invitation dialog open");

  access.shares[0].status = "pending";
  access.shares[0].engines = [];
  access.shares[0].invitation_revision++;
  await pages.myQuota();
  open();
  assert(accept().disabled && !reject().disabled, "未分配内核的旧邀请可以被接受");
  failRead = true;
  dialog().querySelector("[data-invitation-reload]").click();
  await waitFor(() => dialog()?.querySelector("[data-invitation-error]").textContent.includes("读取失败"), "刷新失败没有保留错误");
  assert(accept().disabled && !reject().disabled, "刷新失败解除了接受按钮的安全限制");
  failRead = false;
  pages.closeInvitation();
  access.shares[0].engines = ["mihomo"];
  access.shares[0].status = "pending";
  access.shares[0].invitation_revision++;
  await pages.myQuota();
  open();
  pages.closeInvitation();
  state.route = "tasks";
  assert(!dialog(), "navigation left a stale invitation overlay");
  state.route = "my-quota";
  await pages.myQuota();
  open();
  hold = true;
  release = null;
  accept().click();
  await waitFor(() => Boolean(release), "logout-race response did not start");
  const noticeCount = notices.length;
  state.data = {};
  state.session = { role: "user", user_id: "another-account" };
  access = { isolated: true, revision: 1, shares: [] };
  await pages.myQuota();
  release();
  await new Promise(resolve => setTimeout(resolve, 30));
  assert(!dialog() && !card() && state.data.agentAccess.shares.length === 0 && notices.length === noticeCount, "old response contaminated a different account");
}
