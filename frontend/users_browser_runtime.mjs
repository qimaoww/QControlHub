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
  const agents = [{ id: "shared", name: "共享 Agent", capabilities: ["mihomo", "xray"], features: ["shared-traffic-v1", "shared-engines-v1", "independent-egress-v1"] }];
  const access = new Map(items.map((user, index) => [user.id, {
    isolated: user.role !== "admin", revision: 4, shares: user.role === "admin" ? [] : [{
      id: `shr_${user.id}`, status: "accepted", invitation_revision: 2,
      agent_id: "shared", agent_name: "共享 Agent", enabled: true, engines: ["mihomo"], ports: [31001 + index],
      used_bytes: 128, limit_bytes: 1024 ** 3,
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
        access.set(userID, { ...body, revision: body.revision + 1, shares: body.shares.map(share => ({
          ...access.get(userID).shares.find(prior => prior.agent_id === share.agent_id), ...share,
          status: share.reinvite ? "pending" : access.get(userID).shares.find(prior => prior.agent_id === share.agent_id)?.status || "pending",
          reinvite: false,
        })) });
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
  const select = async id => {
    document.querySelector(`[data-user-select="${id}"]`).click();
    await waitFor(() => document.querySelector(".users-toolbar h2")?.textContent === (id === "admin" ? "admin" : id === "alice" ? "Alice" : "Bob"), "user switch did not complete");
  };
  assert(!form().elements.isolated && form().textContent.includes("账号资源始终独立"), "regular accounts must always be private");
  if (innerWidth <= 820) {
    assert(document.querySelector("[data-user-mobile-select]").getBoundingClientRect().height > 0, "mobile user selector is unavailable");
  }
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
  document.querySelector("[data-user-reload]").click();
  await new Promise(resolve => setTimeout(resolve, 20));
  assert(pages.hasUnsavedChanges(), "canceling reload discarded the draft");
  confirm = true;
  document.querySelector("[data-user-reload]").click();
  await waitFor(() => row().querySelector('[name="ports"]').value === "31001", "confirmed reload retained stale fields");
  assert(!pages.hasUnsavedChanges(), "confirmed reload retained dirty state");
  assert(!row().querySelector('[name="engines"][value="xray"]').checked, "reload retained a stale engine grant");
  conflict = false;
  input(row().querySelector('[name="limit_gib"]'), "2.5");
  holdSave = true;
  form().requestSubmit();
  await waitFor(() => form().querySelector('[type="submit"]').disabled, "save did not prevent mid-request edits");
  await select("bob");
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
  assert(!state.data.userDrafts.has("alice") && !state.data.userAccessSaves.size, "saving across navigation left a stale draft or request");
  assert(!form().querySelector('[type="submit"]').disabled, "completed save left the current form locked");
  await select("bob");
  assert(row().querySelector('[name="ports"]').value === "31002, pending-draft", "another user's pending draft was lost after saving");
  document.querySelector("[data-user-reload]").click();
  await waitFor(() => row().querySelector('[name="ports"]').value === "31002", "second user's draft did not reload");
  await select("alice");
  assert(!pages.hasUnsavedChanges(), "successful save left stale draft state");
  assert(access.get("alice").isolated, "saving allocations disabled resource isolation");
  access.get("bob").shares[0].status = "rejected";
  await select("bob");
  assert(row().querySelector("[data-share-status]").textContent === "已拒绝", "admin cannot see rejection");
  row().querySelector("[data-share-reinvite]").click();
  await select("alice");
  await select("bob");
  assert(row().dataset.reinvite === "true" && row().querySelector("[data-share-reinvite]").disabled, "user switch lost staged reinvite");
  form().requestSubmit();
  await waitFor(() => row().querySelector("[data-share-status]").textContent === "待接受", "admin reinvite did not stay pending");
  assert(writes.at(-1).body.shares[0].reinvite === true, "admin reinvite was not explicit");
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
  await testInvitationRuntime();
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
