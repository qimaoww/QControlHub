import { bindEvent } from "./refresh.js";

import { createUserQuotaView } from "./user-quota-view.js";
export function createUserQuota(ctx, { lifecycle, myQuota }) {
  const { api, state, notify, confirmAction } = ctx;
  const { renderQuotaView, renderInvitationDialog } = createUserQuotaView(ctx, lifecycle);
  let activeInvitation = null;
  function closeInvitation() {
    if (!activeInvitation) return;
    const { dialog, trigger } = activeInvitation;
    activeInvitation = null;
    dialog.close();
    dialog.remove();
    if (trigger?.isConnected) trigger.focus();
  }

  const lockResponse = (saving) => {
    document.querySelectorAll("[data-share-decision], [data-share-open], [data-quota-refresh], [data-invitation-reload]").forEach((control) => {
      if (!saving.controls.has(control)) saving.controls.set(control, control.disabled);
      control.disabled = true;
    });
  };

  function openInvitation(share, trigger) {
    if (!share?.enabled || share.status !== "pending" || state.route !== "my-quota") return;
    closeInvitation();
    const data = state.data;
    const dialog = document.createElement("dialog");
    dialog.className = "traffic-edit-dialog agent-invitation-dialog";
    dialog.setAttribute("aria-labelledby", "agent-invitation-title");
    dialog.setAttribute("aria-describedby", "agent-invitation-origin");
    renderInvitationDialog(dialog, share);
    activeInvitation = { dialog, data, share, trigger };
    document.body.append(dialog);
    dialog.showModal();
    bindEvent(dialog.querySelector("[data-invitation-close]"), "click", closeInvitation);
    bindEvent(dialog, "cancel", (event) => { event.preventDefault(); closeInvitation(); });
    dialog.querySelectorAll("[data-share-decision]").forEach((button) => bindEvent(button, "click", () => {
      if (activeInvitation?.dialog !== dialog || data !== state.data) return;
      void respondToShare(share, button.dataset.shareDecision, data);
    }));
    bindEvent(dialog.querySelector("[data-invitation-reload]"), "click", async (event) => {
      if (activeInvitation?.dialog !== dialog || data !== state.data || data.agentShareResponse || activeInvitation.loading) return;
      const button = event.currentTarget, previous = activeInvitation;
      previous.loading = true;
      button.disabled = true;
      const decisions = [...dialog.querySelectorAll("[data-share-decision]")].map(control => [control, control.disabled]);
      decisions.forEach(([control]) => { control.disabled = true; });
      try {
        const latest = await api("/agent-access");
        if (activeInvitation !== previous || data !== state.data || state.route !== "my-quota") return;
        ++lifecycle.serial;
        renderQuota(latest, data);
      } catch (error) {
        if (activeInvitation === previous && data === state.data && error.name !== "AbortError")
          dialog.querySelector("[role=alert]").textContent = error.message;
      } finally {
        previous.loading = false;
        button.disabled = false;
        decisions.forEach(([control, disabled]) => { control.disabled = disabled; });
      }
    });
    if (data.agentShareResponse) lockResponse(data.agentShareResponse);
  }

  async function respondToShare(share, decision, data) {
    if (data !== state.data || data.agentShareResponse || state.route !== "my-quota" || activeInvitation?.loading) return;
    const saving = { controls: new Map() };
    data.agentShareResponse = saving;
    lockResponse(saving);
    try {
      const saved = await api(`/agent-access/${encodeURIComponent(share.id)}/response`, {
        method: "POST", body: JSON.stringify({ revision: share.invitation_revision, decision }),
      });
      if (data !== state.data) return;
      delete data.agentShareResponse;
      if (activeInvitation?.data === data && activeInvitation.share.id === share.id) closeInvitation();
      if (state.route === "my-quota") {
        ++lifecycle.serial; // Discard a read begun before the response committed.
        renderQuota(saved, data);
      }
      notify(decision === "accept" ? "已接受共享" : share.status === "accepted" ? "已退出共享" : "已拒绝共享");
    } catch (error) {
      if (data !== state.data || error.name === "AbortError") return;
      if (activeInvitation?.data === data && activeInvitation.share.id === share.id) {
        const output = activeInvitation.dialog.querySelector("[data-invitation-error]");
        output.hidden = false;
        output.querySelector("[role=alert]").textContent = error.message;
      } else {
        const output = state.route === "my-quota" && document.querySelector("[data-quota-error]");
        if (output) { output.textContent = error.message; output.hidden = false; }
        else notify(error.message, "error");
      }
    } finally {
      if (data.agentShareResponse === saving) delete data.agentShareResponse;
      saving.controls.forEach((disabled, control) => { control.disabled = disabled; });
    }
  }

  function renderQuota(access, data) {
    const openedID = activeInvitation?.data === data ? activeInvitation.share.id : null;
    closeInvitation();
    data.agentAccess = access;
    renderQuotaView(access);
    const selectedShare = button => access.shares.find(share => share.id === button.closest("[data-quota-share]").dataset.quotaShare);
    document.querySelectorAll("[data-share-open]").forEach((button) => bindEvent(button, "click", () => {
      if (data !== state.data || data.agentShareResponse || state.route !== "my-quota") return;
      openInvitation(selectedShare(button), button);
    }));
    if (openedID) {
      const share = access.shares.find(share => share.id === openedID);
      const trigger = [...document.querySelectorAll("[data-share-open]")].find(button => selectedShare(button)?.id === openedID);
      if (share) openInvitation(share, trigger);
    }
    if (data.agentShareResponse) lockResponse(data.agentShareResponse);
    document.querySelectorAll(".user-quota-card [data-share-decision]").forEach((button) => bindEvent(button, "click", async () => {
      if (data !== state.data || data.agentShareResponse || state.route !== "my-quota") return;
      const share = selectedShare(button);
      if (!share) return;
      if (!await confirmAction("退出后将失去此节点的访问权限。", "退出共享")) return;
      if (data !== state.data || data.agentShareResponse || state.route !== "my-quota" || !button.isConnected) return;
      await respondToShare(share, "reject", data);
    }));
    bindEvent(document.querySelector("[data-quota-refresh]"), "click", async (event) => {
      if (data !== state.data || data.agentShareResponse) return;
      const button = event.currentTarget;
      button.disabled = true;
      try { await myQuota(); } catch (error) { if (error.name !== "AbortError") notify(error.message, "error"); }
      finally { button.disabled = false; }
    });
  }

  return { renderQuota, closeInvitation };
}
