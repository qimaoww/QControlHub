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
  const agents = [{ id: "shared", name: "共享 Agent", features: ["shared-traffic-v1"] }];
  const access = new Map(items.map((user, index) => [user.id, {
    isolated: user.role !== "admin", revision: 4, shares: user.role === "admin" ? [] : [{
      agent_id: "shared", agent_name: "共享 Agent", enabled: true, ports: [31001 + index],
      used_bytes: 128, limit_bytes: 1024 ** 3,
    }],
  }]));
  const writes = [], notifications = [];
  let conflict = false, confirm = true, holdSave, releaseSave, failedUser = "";
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
    if (path.endsWith("/agent-access")) {
      if (!body && userID === failedUser) throw new Error("用户读取失败");
      if (body) {
        if (holdSave) await new Promise(resolve => { releaseSave = resolve; });
        if (conflict || body.revision !== access.get(userID).revision) throw new Error("分配已变更，请重新读取后保存。");
        access.set(userID, { ...body, revision: body.revision + 1 });
      }
      return structuredClone(access.get(userID));
    }
    throw new Error(`Unexpected users API: ${path}`);
  };
  const pages = installUsers({
    api, state, esc, notify: message => notifications.push(message), confirmAction: async () => confirm,
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
  assert(form().elements.isolated.checked, "isolated account rendered unrestricted");
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
  assert(pages.hasUnsavedChanges(), "allocation edits were not captured");
  await select("bob");
  assert(row().querySelector('[name="ports"]').value === "31002", "another user's port draft leaked");
  await select("alice");
  assert(row().querySelector('[name="ports"]').value === "31001, invalid", "switching users lost an invalid draft");
  assert(row().querySelector('[name="limit_gib"]').value === "2.5", "switching users lost the allowance draft");
  form().requestSubmit();
  await waitFor(() => !form().querySelector("[data-user-error]").hidden, "invalid port did not produce a local error");
  assert(writes.length === 0, "invalid port reached the API");
  input(row().querySelector('[name="ports"]'), "31001, 31003");
  conflict = true;
  form().requestSubmit();
  await waitFor(() => form().querySelector("[data-user-error]").textContent.includes("分配已变更") && !form().elements.isolated.disabled, "stale allocation conflict did not recover");
  assert(row().querySelector('[name="ports"]').value === "31001, 31003", "conflict discarded entered ports");
  confirm = false;
  document.querySelector("[data-user-reload]").click();
  await new Promise(resolve => setTimeout(resolve, 20));
  assert(pages.hasUnsavedChanges(), "canceling reload discarded the draft");
  confirm = true;
  document.querySelector("[data-user-reload]").click();
  await waitFor(() => row().querySelector('[name="ports"]').value === "31001", "confirmed reload retained stale fields");
  assert(!pages.hasUnsavedChanges(), "confirmed reload retained dirty state");
  conflict = false;
  input(row().querySelector('[name="limit_gib"]'), "2.5");
  holdSave = true;
  form().requestSubmit();
  await waitFor(() => form().elements.isolated.disabled, "save did not prevent mid-request edits");
  await select("bob");
  input(row().querySelector('[name="ports"]'), "31002, pending-draft");
  pages.captureDraft();
  state.route = "my-quota";
  await pages.myQuota();
  state.route = "users";
  state.data.userID = "alice";
  await pages.users();
  assert(form().elements.isolated.disabled, "returning to a pending save unlocked the allocation");
  const pendingWrites = writes.length;
  form().requestSubmit();
  assert(writes.length === pendingWrites, "navigation allowed a duplicate in-flight save");
  releaseSave();
  await waitFor(() => notifications.includes("分配已保存"), "allocation did not save");
  holdSave = false;
  assert(access.get("alice").shares[0].limit_bytes === 2.5 * 1024 ** 3, "GiB allocation was rounded incorrectly");
  assert(!state.data.userDrafts.has("alice") && !state.data.userAccessSaves.size, "saving across navigation left a stale draft or request");
  assert(!form().elements.isolated.disabled, "completed save left the current form locked");
  await select("bob");
  assert(row().querySelector('[name="ports"]').value === "31002, pending-draft", "another user's pending draft was lost after saving");
  document.querySelector("[data-user-reload]").click();
  await waitFor(() => row().querySelector('[name="ports"]').value === "31002", "second user's draft did not reload");
  await select("alice");
  assert(!pages.hasUnsavedChanges(), "successful save left stale draft state");
  form().elements.isolated.click();
  form().requestSubmit();
  await waitFor(() => !access.get("alice").isolated, "turning off isolation failed");
  assert(access.get("alice").shares[0].limit_bytes === 2.5 * 1024 ** 3, "turning off isolation erased its dormant allowance");
  await select("admin");
  assert(!document.querySelector('[name="isolated"], [data-share-row]'), "administrator can accidentally be isolated");
  document.querySelector("[data-user-create]").click();
  const dialog = document.querySelector("[data-user-dialog]");
  let account = dialog.querySelector("form");
  input(account.elements.username, "new-user");
  input(account.elements.password, "test-password-only");
  account.querySelector("details").open = true;
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
  assert(account.querySelector("footer").getBoundingClientRect().bottom <= innerHeight + 1, "account save buttons are clipped");
  account.requestSubmit();
  await waitFor(() => writes.some(write => write.path === "/users"), "create user did not submit");
  assert(writes.find(write => write.path === "/users").body.agent_isolation === true, "new regular user was created with unrestricted fleet access");
  await waitFor(() => state.data.userID === "new-user" && !document.querySelector("[data-user-dialog]").open, "created user did not load");
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
}
