import { bindEvent } from "./refresh.js";

import { parseSharedPorts, selectedSharedEngines, sharedLimitBytes, mergeUserAllocation } from "./user-model.js";
import { createUserAllocationView, formValues } from "./user-allocation-view.js";
export function createUserAllocations(ctx, { lifecycle, renderUsers, report }) {
  const { api, state, notify, confirmAction } = ctx;
  const { allocationFields, renderAllocationDialog } = createUserAllocationView(ctx);
  function lockAllocationControls(data, userID, saving) {
    if (data !== state.data || state.route !== "users" || data.userID !== userID) return;
    const dialog = document.querySelector("[data-allocation-dialog]");
    const controls = [...document.querySelectorAll("[data-allocation-add], [data-allocation-edit], [data-allocation-invite], [data-allocation-revoke], [data-user-reload], [data-user-delete], [data-user-edit]"), ...(dialog?.querySelector("form")?.elements || []), ...(dialog?.querySelectorAll("[data-allocation-close]") || [])];
    for (const control of controls) {
      if (!saving.controls.has(control)) saving.controls.set(control, control.disabled);
      control.disabled = true;
    }
  }

  function closeAllocation() {
    if (!lifecycle.activeAllocation) return;
    const previous = lifecycle.activeAllocation;
    if (previous.data.userAccessSaves.has(previous.userID)) return;
    previous.data.userDrafts.delete(previous.userID);
    lifecycle.activeAllocation = null;
    lifecycle.captureActive = () => {};
    previous.dialog.close();
    previous.dialog.innerHTML = "";
    if (previous.trigger?.isConnected) previous.trigger.focus();
  }

  function openAllocation(user, agents, latest, options) {
    const data = state.data, draft = options.draft;
    const access = draft?.access || latest;
    let mode = options.mode || "edit";
    const value = draft?.values.rows[0];
    let agentID = value?.agent_id || options.agentID || "";
    const shares = access.shares || [];
    const available = agents.filter(item => !(access.owned_agent_ids || []).includes(item.id) && !shares.some(row => row.agent_id === item.id));
    if (mode === "add" && !available.some(item => item.id === agentID)) agentID = "";
    const original = shares.find(share => share.agent_id === agentID);
    if (mode !== "add" && !original) return;
    if (mode === "invite" && original.enabled && original.status !== "rejected") mode = "edit";
    const share = { enabled: true, ...original, ...value };
    if (mode === "invite") { share.enabled = true; share.reinvite = original.status === "rejected"; }
    const agent = agents.find(item => item.id === agentID);
    const dialog = document.querySelector("[data-allocation-dialog]");
    const title = mode === "add" ? "分配节点" : mode === "invite" ? "重新邀请" : "编辑分配";
    renderAllocationDialog(dialog, { title, user, agentID, mode, available, share, agent, original });
    const form = dialog.querySelector("form");
    const initial = formValues(form);
    if (mode === "invite") {
      // Restoring a revoked share changes enabled even without a reinvite flag.
      initial.rows[0].enabled = original.enabled;
      initial.rows[0].reinvite = false;
    }
    const baseline = draft?.baseline || JSON.stringify(initial);
    const capture = () => {
      if (!form.isConnected || data !== state.data) return;
      const values = formValues(form), serialized = JSON.stringify(values);
      const previous = data.userDrafts.get(user.id);
      if (serialized === baseline) data.userDrafts.delete(user.id);
      else if (previous?.access.revision !== access.revision || previous.baseline !== baseline || JSON.stringify(previous.values) !== serialized)
        data.userDrafts.set(user.id, { access, values, baseline, mode });
    };
    const editor = { dialog, userID: user.id, data, trigger: options.trigger, reloading: false };
    const isCurrent = () => lifecycle.activeAllocation === editor && data === state.data && state.route === "users" && data.userID === user.id;
    const updateConsent = () => {
      const message = form.querySelector("[data-allocation-consent]");
      const addedEngines = [...form.querySelectorAll('[name="engines"]:checked')].some(input => !original?.engines?.includes(input.value));
      message.textContent = mode !== "edit" ? "用户接受邀请后才能使用节点。"
        : !original.enabled ? "保存修改不会恢复共享。"
        : original.status === "rejected" ? "保存修改不会重新发送邀请。"
        : original.status === "accepted" && addedEngines ? "增加内核后，用户需重新接受。"
        : "";
      message.hidden = !message.textContent || !form.querySelector("[data-share-row]").dataset.shareRow;
    };
    lifecycle.activeAllocation = editor;
    lifecycle.captureActive = capture;
    if (!dialog.open) dialog.showModal();
    updateConsent();
    capture();
    bindEvent(form, "input", capture);
    bindEvent(form, "change", (event) => {
      if (event.target.matches("[data-share-agent]")) {
        const id = event.target.value;
        form.querySelector("[data-share-row]").dataset.shareRow = id;
        const fields = form.querySelector("[data-allocation-fields]");
        fields.innerHTML = allocationFields({}, agents.find(item => item.id === id));
        fields.disabled = !id;
        fields.hidden = !id;
        form.querySelector('[type="submit"]').disabled = !id;
      }
      if (event.target.name === "quota_mode") {
        const unlimited = event.target.value === "unlimited";
        form.querySelector("[data-quota-amount]").hidden = unlimited;
        form.elements.limit_gib.disabled = unlimited;
        if (!unlimited && Number(form.elements.limit_gib.value) === 0) form.elements.limit_gib.value = "";
      }
      updateConsent();
      capture();
    });
    dialog.querySelectorAll("[data-allocation-close]").forEach(button => bindEvent(button, "click", () => closeAllocation()));
    bindEvent(dialog, "cancel", event => { event.preventDefault(); closeAllocation(); });
    bindEvent(form.querySelector("[data-allocation-reload]"), "click", async () => {
      if (!isCurrent() || editor.reloading || data.userAccessSaves.has(user.id) || data.userDeletions.has(user.id)) return;
      editor.reloading = true;
      const controls = [...form.elements].filter(control => !control.hasAttribute("data-allocation-close")).map(control => [control, control.disabled]);
      controls.forEach(([control]) => { control.disabled = true; });
      try {
        if (!await confirmAction("重新读取会丢弃未保存的分配。", "重新读取") || !isCurrent()) return;
        const selectedID = formValues(form).rows[0].agent_id;
        const refreshed = await api(`/users/${encodeURIComponent(user.id)}/agent-access`);
        if (!isCurrent()) return;
        data.userDrafts.delete(user.id);
        ++lifecycle.serial;
        renderUsers(data.users, agents, user, refreshed);
        openAllocation(user, agents, refreshed, { mode, agentID: selectedID, trigger: document.querySelector(mode === "add" ? "[data-allocation-add]" : `[data-allocation-edit="${CSS.escape(selectedID)}"]`) });
      } catch (error) { if (isCurrent()) reportAllocationError(error, data, user.id); }
      finally {
        editor.reloading = false;
        controls.forEach(([control, disabled]) => { control.disabled = disabled; });
      }
    });
    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      if (!isCurrent() || editor.reloading || form.inert || data.userAccessSaves.has(user.id) || data.userDeletions.has(user.id)) return;
      try {
        const row = form.querySelector("[data-share-row]"), values = formValues(form).rows[0];
        if (!values.agent_id) throw new Error("请选择节点。");
        const limit = values.unlimited ? 0 : sharedLimitBytes(values.limit_gib);
        if (!values.unlimited && limit === 0) throw new Error("请输入大于 0 的总额度，或选择不限量。");
        const allocation = { agent_id: values.agent_id, enabled: values.enabled, engines: selectedSharedEngines(row), ports: parseSharedPorts(values.ports_text), limit_bytes: limit, reinvite: values.reinvite };
        capture();
        await saveAllocation(user, agents, access, allocation, { data, trigger: form, notice: mode === "edit" ? "分配已保存" : "共享邀请已发送" });
      } catch (error) { reportAllocationError(error, data, user.id); }
    });
    const pending = data.userAccessSaves.get(user.id);
    if (pending) lockAllocationControls(data, user.id, pending);
  }

  function reportAllocationError(error, data, userID) {
    if (error.name === "AbortError" || data !== state.data) return;
    if (lifecycle.activeAllocation?.data === data && lifecycle.activeAllocation.userID === userID && lifecycle.activeAllocation.dialog.isConnected) {
      lifecycle.activeAllocation.dialog.querySelector(".user-allocation-failure").hidden = false;
      report(error, lifecycle.activeAllocation.dialog);
    } else if (state.route === "users" && data.userID === userID) report(error, document.querySelector("[data-user-allocations]"));
    else notify(error.message, "error");
  }

  async function saveAllocation(user, agents, access, allocation, { data, trigger, notice, confirmation }) {
    const currentUser = () => data === state.data && state.route === "users" && data.userID === user.id && trigger.isConnected;
    if (!currentUser() || data.userAccessSaves.has(user.id) || data.userDeletions.has(user.id)) return;
    const submitted = data.userDrafts.get(user.id), saving = { controls: new Map() };
    data.userAccessSaves.set(user.id, saving);
    lockAllocationControls(data, user.id, saving);
    try {
      if (confirmation && (!await confirmAction(confirmation, "撤销共享") || !currentUser())) return;
      const saved = await api(`/users/${encodeURIComponent(user.id)}/agent-access`, {
        method: "PUT", body: JSON.stringify({ isolated: true, shares: mergeUserAllocation(access.shares, allocation), revision: access.revision }),
      });
      if (data !== state.data) return;
      if (data.userDrafts.get(user.id) === submitted) data.userDrafts.delete(user.id);
      data.userAccessSaves.delete(user.id);
      if (state.route === "users" && data.userID === user.id) {
        ++lifecycle.serial;
        lifecycle.captureActive = () => {};
        renderUsers(data.users, agents, data.users.find(item => item.id === user.id) || user, saved);
      }
      notify(notice);
    } catch (error) { reportAllocationError(error, data, user.id); }
    finally {
      if (data.userAccessSaves.get(user.id) === saving) data.userAccessSaves.delete(user.id);
      saving.controls.forEach((disabled, control) => { control.disabled = disabled; });
    }
  }

  return { openAllocation, saveAllocation, lockAllocationControls };
}
