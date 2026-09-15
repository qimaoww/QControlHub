import { bindEvent } from "./refresh.js";

export function createUserBindings({ api, state, notify, confirmAction }, { lifecycle, users, captureDraft, current, report, editUser, openAllocation, saveAllocation, lockAllocationControls }) {
  return (items, agents, user, access, { editable, data, draft, shares }) => {
    const selectUser = async (id) => {
      if (id === state.data.userID) return;
      captureDraft();
      state.data.userID = id;
      const previousForm = document.querySelector("[data-user-access-form]") || document.querySelector("[data-user-allocations]");
      if (previousForm) { previousForm.inert = true; previousForm.setAttribute("aria-busy", "true"); }
      const loading = users(), selectionRequest = lifecycle.serial;
      try { await loading; } catch (error) {
        if (!current(selectionRequest, data, "users")) return;
        data.userID = user?.id || "";
        const select = document.querySelector("[data-user-mobile-select]");
        if (select) select.value = data.userID;
        if (error.name !== "AbortError") notify(error.message, "error");
      } finally {
        if (previousForm?.isConnected && current(selectionRequest, data, "users")) {
          previousForm.inert = false;
          previousForm.removeAttribute("aria-busy");
        }
      }
    };
    document.querySelectorAll("[data-user-select]").forEach((link) => bindEvent(link, "click", (event) => {
      event.preventDefault();
      void selectUser(link.dataset.userSelect);
    }));
    bindEvent(document.querySelector("[data-user-mobile-select]"), "change", (event) => { void selectUser(event.target.value); });
    bindEvent(document.querySelector("[data-user-create]"), "click", () => editUser(null));
    bindEvent(document.querySelector("[data-user-edit]"), "click", () => editUser(user));
    bindEvent(document.querySelector("[data-user-delete]"), "click", async (event) => {
      const button = event.currentTarget;
      const label = user.display_name || user.username;
      const currentUser = () => data === state.data && state.route === "users" && data.userID === user.id && button.isConnected;
      if (button.disabled || !currentUser() || data.userDeletions.has(user.id) || data.userAccessSaves.has(user.id)) return;
      data.userDeletions.add(user.id);
      button.disabled = true;
      try {
        if (!await confirmAction(`删除账号“${label}”后无法恢复：节点（含隐藏节点）、配置与模板将移交给管理员；旧安装凭据失效，未完成任务作废，共享与个人设置删除，流量历史保留。`, "继续删除")) return;
        if (!currentUser()) return;
        if (!await confirmAction(`再次确认删除账号“${label}”（${user.username}）？`, "永久删除")) return;
        if (!currentUser()) return;
        await api(`/users/${encodeURIComponent(user.id)}/purge`, { method: "POST" });
        if (data !== state.data) return;
        data.userDrafts?.delete(user.id);
        data.userAccessSaves?.delete(user.id);
        data.users = data.users?.filter((item) => item.id !== user.id);
        if (state.route === "users" && data.userID === user.id) {
          // users() captures the visible form before refreshing. It must
          // not resurrect an unsaved draft for the just-deleted account.
          lifecycle.captureActive = () => {};
          data.userID = "";
          await users();
        }
        notify(`账号“${label}”已删除`);
      } catch (error) {
        if (error.name !== "AbortError") notify(error.message, "error");
      } finally {
        data.userDeletions.delete(user.id);
        button.disabled = false;
        if (data === state.data && state.route === "users" && data.userID === user.id) {
          const activeButton = document.querySelector("[data-user-delete]");
          if (activeButton) activeButton.disabled = false;
        }
      }
    });
    const scope = () => data === state.data && state.route === "users" && data.userID === user?.id;
    const busy = () => !scope() || document.querySelector("[data-user-allocations]")?.inert || data.userAccessSaves.has(user.id) || data.userDeletions.has(user.id);
    bindEvent(document.querySelector("[data-user-reload]"), "click", async (event) => {
      const button = event.currentTarget;
      if (busy() || button.disabled) return;
      const panel = document.querySelector("[data-user-allocations]");
      button.disabled = true;
      panel.inert = true;
      panel.setAttribute("aria-busy", "true");
      try { await users(); } catch (error) { if (scope()) report(error, panel); }
      finally {
        button.disabled = false;
        panel.inert = false;
        panel.removeAttribute("aria-busy");
      }
    });
    if (!user || !editable) return;
    bindEvent(document.querySelector("[data-allocation-add]"), "click", (event) => {
      if (!busy()) openAllocation(user, agents, access, { mode: "add", trigger: event.currentTarget });
    });
    document.querySelectorAll("[data-allocation-edit], [data-allocation-invite]").forEach(button => bindEvent(button, "click", () => {
      if (!busy()) openAllocation(user, agents, access, {
        mode: button.hasAttribute("data-allocation-invite") ? "invite" : "edit",
        agentID: button.dataset.allocationEdit || button.dataset.allocationInvite, trigger: button,
      });
    }));
    document.querySelectorAll("[data-allocation-revoke]").forEach(button => bindEvent(button, "click", () => {
      if (busy()) return;
      const share = shares.find(item => item.agent_id === button.dataset.allocationRevoke);
      const name = agents.find(agent => agent.id === share.agent_id)?.name || share.agent_name || share.agent_id;
      void saveAllocation(user, agents, access, { agent_id: share.agent_id, enabled: false }, {
        data, trigger: button, notice: "共享已撤销",
        confirmation: `撤销“${name}”对“${user.display_name || user.username}”的共享？已用流量和端口预留会保留。`,
      });
    }));
    if (draft) openAllocation(user, agents, access, { mode: draft.mode, draft });
    const pending = data.userAccessSaves.get(user.id);
    if (pending) lockAllocationControls(data, user.id, pending);
  };

}
