import { bindEvent } from "./refresh.js";

import { createSubStoreSelections } from "./substore-selections.js";
import { createSubStoreTargets } from "./substore-targets.js";
export function createSubStoreBindings(ctx, { lifecycle, subStoreSync, render, masonry }) {
  const { api, state, can, notify } = ctx;
  const bindSelections = createSubStoreSelections(ctx, { lifecycle, subStoreSync });
  const bindTargets = createSubStoreTargets(ctx, { lifecycle, subStoreSync });
  function bindPage() {
    const resource = state.data.subStoreSync || {};
    const profiles = resource.profiles || [];
    const targets = resource.targets || [];
    const activeTarget = targets.find((target) => target.id === lifecycle.activeTargetID) || null;
    masonry.bind();
    document.querySelectorAll("[data-substore-target]").forEach((button) => {
      button.onclick = async () => {
        if (button.dataset.substoreTarget === lifecycle.activeTargetID) return;
        lifecycle.activeTargetID = button.dataset.substoreTarget || "";
        lifecycle.pendingSelectionSave = null;
        try {
          await subStoreSync();
        } catch (error) {
          notify(error.message, "error");
        }
      };
    });
    document.querySelectorAll("[data-substore-agent]").forEach((link) => {
      link.onclick = (event) => {
        event.preventDefault();
        lifecycle.agentFilter = link.dataset.substoreAgent || "";
        state.data.subStoreAgent = lifecycle.agentFilter;
        render();
      };
    });
    bindEvent(document.querySelector("[data-substore-query]"), "input", (event) => {
      lifecycle.query = event.currentTarget.value;
      render();
      const input = document.querySelector("[data-substore-query]");
      input?.focus();
      input?.setSelectionRange(lifecycle.query.length, lifecycle.query.length);
    });
    if (!can("settings.manage")) return;

    bindSelections(profiles);
    const dialog = document.querySelector("[data-substore-settings-dialog]");
    bindEvent(document.querySelector("[data-substore-settings]"), "click", () => dialog?.showModal());
    document.querySelectorAll("[data-substore-settings-close]").forEach((button) => {
      button.onclick = () => dialog?.close();
    });
    bindEvent(document.querySelector("[data-substore-settings-form]"), "submit", async (event) => {
      event.preventDefault();
      if (!can("settings.manage")) return;
      const form = event.currentTarget;
      const submit = form.querySelector("button[type=submit]");
      submit.disabled = true;
      try {
        const data = Object.fromEntries(new FormData(form));
        await api("/substore-sync/settings", { method: "PUT", body: JSON.stringify(data) });
        dialog?.close();
        notify("Sub-Store 连接设置已保存");
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        submit.disabled = false;
      }
    });
    bindEvent(document.querySelector("[data-substore-test]"), "click", async (event) => {
      const button = event.currentTarget;
      button.disabled = true;
      try {
        await api("/substore-sync/test", { method: "POST" });
        notify("Sub-Store 连接正常");
      } catch (error) {
        notify(error.message, "error");
      } finally {
        if (button.isConnected) button.disabled = false;
      }
    });

    bindTargets(targets, activeTarget);
    bindEvent(document.querySelector("[data-substore-run]"), "click", async (event) => {
      const button = event.currentTarget;
      button.disabled = true;
      try {
        if (lifecycle.pendingSelectionSave) await lifecycle.pendingSelectionSave;
        const result = await api("/substore-sync/run", {
          method: "POST",
          body: JSON.stringify({ target_id: lifecycle.activeTargetID }),
        });
        notify(`${result.node_count} 个节点已同步到 Sub-Store`);
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        await subStoreSync();
      }
    });
  }

  return bindPage;
}
