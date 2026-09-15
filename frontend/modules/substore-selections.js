import { subStoreSelectionPayload } from "./substore-model.js";
export function createSubStoreSelections({ api, state, notify }, { lifecycle, subStoreSync }) {
  async function saveSelections(targetID, selections) {
    await api("/substore-sync/selections", {
      method: "PUT",
      body: JSON.stringify({ target_id: targetID, selections }),
    });
    notify("同步清单已更新");
    await subStoreSync();
  }

  function trackSelectionSave(profiles) {
    const targetID = lifecycle.activeTargetID;
    const selections = subStoreSelectionPayload(profiles);
    const selectionSession = state.data;
    // Keep rapid checkbox/name edits in browser order. Replacing the complete
    // selection set concurrently could otherwise let an older response win.
    const previous = lifecycle.pendingSelectionSave?.catch(() => {}) || Promise.resolve();
    const operation = previous.then(() => {
      if (state.data !== selectionSession) return;
      return saveSelections(targetID, selections);
    });
    lifecycle.pendingSelectionSave = operation;
    operation.then(
      () => {
        if (lifecycle.pendingSelectionSave === operation) lifecycle.pendingSelectionSave = null;
      },
      () => {
        if (lifecycle.pendingSelectionSave === operation) lifecycle.pendingSelectionSave = null;
      },
    );
    return operation;
  }

  function profileForRow(row) {
    const resource = state.data.subStoreSync || {};
    const profiles = resource.profiles || [];
    const [agentID, engine, tag, configID = ""] = decodeURIComponent(
      String(row.dataset.substoreKey || ""),
    ).split("\u0000");
    return profiles.find(
      (profile) => profile.agent_id === agentID && profile.engine === engine && profile.profile_tag === tag && (profile.config_id || "") === configID,
    );
  }

  return (profiles) => {
    document.querySelectorAll("[data-substore-add], [data-substore-select]").forEach((control) => {
      control.onclick = async () => {
        const profile = profileForRow(control.closest("[data-substore-key]"));
        if (!profile) return;
        profile.selected = control.matches("[data-substore-select]") ? control.checked : true;
        if (profile.selected && !profile.custom_name) profile.custom_name = profile.default_name;
        try {
          await trackSelectionSave(profiles);
        } catch (error) {
          notify(error.message, "error");
          await subStoreSync();
        }
      };
    });
    document.querySelectorAll("[data-substore-remove]").forEach((button) => {
      button.onclick = async () => {
        const profile = profileForRow(button.closest("[data-substore-key]"));
        if (!profile) return;
        profile.selected = false;
        try {
          await trackSelectionSave(profiles);
        } catch (error) {
          notify(error.message, "error");
          await subStoreSync();
        }
      };
    });
    document.querySelectorAll("[data-substore-parameters-form]").forEach((form) => {
      form.onsubmit = async (event) => {
        event.preventDefault();
        const profile = profileForRow(form.closest("[data-substore-key]"));
        if (!profile) return;
        const previousName = profile.custom_name;
        const previousMode = profile.address_mode || "auto";
        const data = new FormData(form);
        profile.custom_name = String(data.get("custom_name") || "").trim();
        profile.address_mode = String(data.get("address_mode") || "auto");
        const submit = form.querySelector("button[type=submit]");
        if (submit) submit.disabled = true;
        try {
          await trackSelectionSave(profiles);
        } catch (error) {
          profile.custom_name = previousName;
          profile.address_mode = previousMode;
          notify(error.message, "error");
          await subStoreSync();
        }
      };
    });

  };
}
