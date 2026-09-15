import { createCardMasonry } from "./card-masonry.js";
import { createSubStoreView } from "./substore-view.js";
import { createSubStoreBindings } from "./substore-bindings.js";
export { filterSubStoreProfiles, groupSubStoreProfiles, subStoreSelectionPayload, subStoreAddressChoices, subStoreProfileNodeCount, subStoreAddressModeLabel } from "./substore-model.js";
import { createRefreshChannel } from "./refresh.js";

export function installSubStoreSync(ctx) {
  const { api, state } = ctx;
  const lifecycle = {
    agentFilter: "", query: "", activeTargetID: "",
    pendingSelectionSave: null, sessionData: state.data,
  };
  const masonry = createCardMasonry(".substore-agent-grid:not(.empty)", ".substore-agent-card");
  const view = createSubStoreView(ctx, { lifecycle, masonry });
  const bind = createSubStoreBindings(ctx, { lifecycle, subStoreSync, render, masonry });
  function render() { view(); bind(); }
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "substore-sync",
    getScope: () => state.navigationEpoch,
  });

  async function subStoreSync() {
    if (lifecycle.sessionData !== state.data) {
      lifecycle.sessionData = state.data;
      lifecycle.activeTargetID = "";
      lifecycle.agentFilter = "";
      lifecycle.query = "";
      lifecycle.pendingSelectionSave = null;
    }
    let resource;
    const applied = await refresh.run(
      (signal) =>
        api(
          `/substore-sync${lifecycle.activeTargetID ? `?target_id=${encodeURIComponent(lifecycle.activeTargetID)}` : ""}`,
          { signal },
        ),
      (value) => {
        resource = value;
      },
    );
    if (!applied) return false;
    lifecycle.activeTargetID = resource.target_id || "";
    state.data.subStoreSync = resource;
    render();
    return true;
  }

  return subStoreSync;
}
