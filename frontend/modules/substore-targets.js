import { bindEvent } from "./refresh.js";

export function createSubStoreTargets({ api, esc, notify }, { lifecycle, subStoreSync }) {
  return (targets, activeTarget) => {
    const targetDialog = document.querySelector("[data-substore-target-dialog]");
    const targetForm = targetDialog?.querySelector("[data-substore-target-form]");
    const loadRemoteTargets = async (dialogTarget = null) => {
      const select = targetDialog?.querySelector("[data-substore-remote-select]");
      const button = targetDialog?.querySelector("[data-substore-remote-import-button]");
      if (!select || !button) return;
      select.disabled = true;
      button.disabled = true;
      select.dataset.substoreRemoteChosen = "";
      select.innerHTML = "<option>读取中…</option>";
      try {
        const remoteTargets = await api("/substore-sync/remote-targets");
        const available = (remoteTargets || []).filter((remoteTarget) => (
          !remoteTarget.imported || (dialogTarget && remoteTarget.subscription_name === dialogTarget.subscription_name)
        ));
        select.innerHTML = available.length
          ? `<option value="">不关联现有组</option>${available.map((target) => `<option value="${esc(target.subscription_name)}">${esc(target.subscription_name)} · ${Number(target.node_count || 0)} 个节点</option>`).join("")}`
          : "<option value=\"\">没有可加入的组</option>";
        select.disabled = !available.length;
        if (dialogTarget && available.some((remoteTarget) => remoteTarget.subscription_name === dialogTarget.subscription_name)) {
          select.value = dialogTarget.subscription_name;
        }
        button.disabled = !select.value || Boolean(dialogTarget && select.value === dialogTarget.subscription_name);
      } catch (error) {
        select.innerHTML = "<option value=\"\">读取失败</option>";
        notify(error.message, "error");
      }
    };
    const openTargetDialog = (target = null) => {
      if (!targetDialog || !targetForm) return;
      targetForm.reset();
      targetForm.elements.target_id.value = target?.id || "";
      targetForm.elements.display_name.value = target?.display_name || target?.subscription_name || "";
      const syncMode = target?.sync_mode || "incremental";
      const syncModeInput = targetForm.querySelector(`[name="sync_mode"][value="${syncMode}"]`);
      if (syncModeInput) syncModeInput.checked = true;
      const syncFormat = target?.sync_format || "url";
      const syncFormatInput = targetForm.querySelector(`[name="sync_format"][value="${syncFormat}"]`);
      if (syncFormatInput) syncFormatInput.checked = true;
      const title = targetDialog.querySelector("#substore-target-title");
      if (title) title.textContent = target ? "同步组设置" : "新建同步组";
      const remoteHelp = targetDialog.querySelector("[data-substore-remote-help]");
      if (remoteHelp) remoteHelp.textContent = target ? "当前关联的 Sub-Store 组，可切换" : "选择 Sub-Store 现有组加入同步";
      const remoteButton = targetDialog.querySelector("[data-substore-remote-import-button]");
      if (remoteButton) remoteButton.textContent = target ? "切换关联组" : "加入同步组";
      const remove = targetDialog.querySelector("[data-substore-target-delete]");
      if (remove) remove.hidden = !target;
      const renameOptions = targetDialog.querySelector("[data-substore-rename-options]");
      if (renameOptions) renameOptions.hidden = !target;
      targetDialog.showModal();
      targetForm.elements.display_name.focus();
      loadRemoteTargets(target);
    };
    bindEvent(document.querySelector("[data-substore-target-add]"), "click", () => openTargetDialog());
    bindEvent(document.querySelector("[data-substore-target-edit]"), "click", () => openTargetDialog(activeTarget));
    document.querySelectorAll("[data-substore-target-close]").forEach((button) => {
      button.onclick = () => targetDialog?.close();
    });
    bindEvent(targetForm, "submit", async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      const submit = form.querySelector("button[type=submit]");
      const targetID = String(form.elements.target_id.value || "");
      const displayName = String(form.elements.display_name.value || "").trim();
      const formData = new FormData(form);
      const renameRemote = targetID && formData.get("rename_remote") === "true";
      const syncMode = String(formData.get("sync_mode") || "incremental");
      const syncFormat = String(formData.get("sync_format") || "url");
      const remoteSelect = targetDialog?.querySelector("[data-substore-remote-select]");
      const remoteName = String(remoteSelect?.value || "");
      const remoteChosen = remoteName && remoteSelect?.dataset.substoreRemoteChosen === "true";
      submit.disabled = true;
      try {
        if (remoteChosen) {
          const route = targetID
            ? `/substore-sync/targets/${encodeURIComponent(targetID)}/remote`
            : "/substore-sync/targets/import";
          const target = await api(route, {
            method: "POST",
            body: JSON.stringify({ subscription_name: remoteName, display_name: displayName, sync_format: syncFormat }),
          });
          lifecycle.activeTargetID = target.id;
          targetDialog?.close();
          notify(targetID ? "已关联 Sub-Store 现有组" : "已加入 Sub-Store 现有组");
          await subStoreSync();
          return;
        }
        const target = await api(
          targetID ? `/substore-sync/targets/${encodeURIComponent(targetID)}` : "/substore-sync/targets",
          { method: targetID ? "PUT" : "POST", body: JSON.stringify({ display_name: displayName, rename_remote: Boolean(renameRemote), sync_mode: syncMode, sync_format: syncFormat }) },
        );
        lifecycle.activeTargetID = target.id;
        targetDialog?.close();
        notify(targetID ? "同步组已更新" : "同步组已创建");
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        submit.disabled = false;
      }
    });
    bindEvent(targetDialog?.querySelector("[data-substore-remote-select]"), "change", (event) => {
      const select = event.currentTarget;
      const button = targetDialog?.querySelector("[data-substore-remote-import-button]");
      const targetID = String(targetForm?.elements.target_id.value || "");
      const currentTarget = targets.find((target) => target.id === targetID) || null;
      const changedRemote = select.value && (!currentTarget || select.value !== currentTarget.subscription_name);
      select.dataset.substoreRemoteChosen = changedRemote ? "true" : "";
      if (button) button.disabled = !changedRemote;
      const renameOptions = targetDialog?.querySelector("[data-substore-rename-options]");
      if (renameOptions) renameOptions.hidden = !targetID || Boolean(changedRemote);
    });
    bindEvent(document.querySelector("[data-substore-remote-import-button]"), "click", async (event) => {
      const select = targetDialog?.querySelector("[data-substore-remote-select]");
      const subscriptionName = String(select?.value || "");
      if (!subscriptionName) return;
      const button = event.currentTarget;
      button.disabled = true;
      try {
        const targetID = String(targetForm?.elements.target_id.value || "");
        const route = targetID
          ? `/substore-sync/targets/${encodeURIComponent(targetID)}/remote`
          : "/substore-sync/targets/import";
        const target = await api(route, {
          method: "POST",
          body: JSON.stringify({ subscription_name: subscriptionName, display_name: targetForm?.elements.display_name.value || "", sync_format: new FormData(targetForm).get("sync_format") || "url" }),
        });
        lifecycle.activeTargetID = target.id;
        targetDialog?.close();
        notify(targetID ? `已切换到 Sub-Store 组“${target.subscription_name}”` : `已加入 Sub-Store 组“${target.subscription_name}”`);
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        button.disabled = false;
      }
    });
    const deleteDialog = document.querySelector("[data-substore-delete-dialog]");
    bindEvent(document.querySelector("[data-substore-target-delete]"), "click", () => {
      if (!activeTarget || !deleteDialog) return;
      targetDialog?.close();
      const title = deleteDialog.querySelector("#substore-delete-title");
      if (title) title.textContent = `移除“${activeTarget.display_name || activeTarget.subscription_name}”`;
      deleteDialog.querySelector("[data-substore-delete-form]")?.reset();
      deleteDialog.showModal();
    });
    document.querySelectorAll("[data-substore-delete-close]").forEach((button) => {
      button.onclick = () => deleteDialog?.close();
    });
    bindEvent(document.querySelector("[data-substore-delete-form]"), "submit", async (event) => {
      event.preventDefault();
      if (!activeTarget) return;
      const form = event.currentTarget;
      const button = form.querySelector("button[type=submit]");
      button.disabled = true;
      try {
        await api(`/substore-sync/targets/${encodeURIComponent(activeTarget.id)}`, { method: "DELETE" });
        lifecycle.activeTargetID = "";
        deleteDialog?.close();
        notify("同步组已从面板移除");
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        button.disabled = false;
      }
    });

  };
}
