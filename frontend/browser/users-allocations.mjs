import { installUsers } from "../modules/users.js";
import { assert, esc, waitFor, input } from "./users-helpers.mjs";
import { testAllocationRaceRuntime } from "./users-allocation-races.mjs";
import { testInvitationRuntime } from "./users-invitations.mjs";

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
  input(row().querySelector('[name="ports"]'), "31001-31003");
  await select("bob");
  await select("alice");
  assert(row().querySelector('[name="ports"]').value === "31001-31003", "switching users discarded the range draft");
  conflict = true;
  form().requestSubmit();
  await waitFor(() => form().querySelector("[data-user-error]").textContent.includes("分配已变更") && !form().querySelector('[type="submit"]').disabled, "stale allocation conflict did not recover");
  assert(row().querySelector('[name="ports"]').value === "31001-31003", "conflict discarded the entered range");
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
