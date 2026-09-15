import { createUserView } from "./user-view.js";
import { createUserAllocations } from "./user-allocations.js";
import { createUserAccountEditor } from "./user-account-editor.js";
import { createUserBindings } from "./user-bindings.js";
import { createUserQuota } from "./user-quota.js";
export { userPermissions, parseSharedPorts, formatSharedPorts, sharedPortsLabel, selectedSharedEngines, sharedLimitBytes, sharedLimitGiB, agentShareStatus, mergeUserAllocation } from "./user-model.js";

export function installUsers(ctx) {
  const { api, state, notify } = ctx;
  // Reads, mutation completions and draft capture share monotonic lifetimes.
  const lifecycle = {
    serial: 0, viewSerial: 0, captureActive: () => {}, activeAllocation: null,
  };
  const report = (error, element) => {
    if (error?.name === "AbortError" || !element?.isConnected) return;
    const output = element.querySelector("[data-user-error]");
    if (output) { output.textContent = error.message; output.hidden = false; }
    else notify(error.message, "error");
  };

  const captureDraft = () => lifecycle.captureActive();
  const hasUnsavedChanges = () => Boolean(state.data.userDrafts?.size || state.data.userAccessSaves?.size || state.data.userDeletions?.size || state.data.agentShareResponse);
  const current = (request, data, route) => request === lifecycle.serial && data === state.data && state.route === route;
  const view = createUserView(ctx, lifecycle);
  const allocations = createUserAllocations(ctx, { lifecycle, renderUsers, report });
  const editUser = createUserAccountEditor(ctx, { users, report });
  const bind = createUserBindings(ctx, { lifecycle, users, captureDraft, current, report, editUser, ...allocations });
  const { renderQuota, closeInvitation } = createUserQuota(ctx, { lifecycle, myQuota });
  function renderUsers(items, agents, user, access) {
    const rendered = view(items, agents, user, access);
    bind(items, agents, user, access, rendered);
  }

  async function users() {
    if (state.session?.role !== "admin") return;
    captureDraft();
    const request = ++lifecycle.serial, data = state.data;
    const [items, agents] = await Promise.all([api("/users"), api("/agents")]);
    if (!current(request, data, "users")) return;
    data.users = items;
    data.userID = items.some((user) => user.id === data.userID) ? data.userID : items[0]?.id || "";
    const user = items.find((item) => item.id === data.userID);
    const access = user ? await api(`/users/${encodeURIComponent(user.id)}/agent-access`) : null;
    if (!current(request, data, "users")) return;
    renderUsers(items, agents, user, access);
  }

  async function myQuota() {
    const request = ++lifecycle.serial, data = state.data;
    const access = await api("/agent-access");
    if (!current(request, data, "my-quota")) return;
    renderQuota(access, data);
  }

  return { users, myQuota, closeInvitation, captureDraft, hasUnsavedChanges };
}
