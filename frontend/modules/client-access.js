import { createCardMasonry } from "./card-masonry.js";
import { createClientAccessView } from "./client-access-view.js";
import { createClientAccessBindings } from "./client-access-bindings.js";
export { normalizeClientAccessFilters, filterClientAccessEntries, groupClientAccessEntries, clientAccessAddressChoices, clientAccessEntryForAddress } from "./client-access-model.js";
export { copyClientValue } from "./client-clipboard.js";
import { createRefreshChannel } from "./refresh.js";

export function installClientAccess(ctx) {
  const { api, state, can, notify } = ctx;
  const masonry = createCardMasonry(".client-access-node-grid:not(.empty)", ".client-access-node-card");
  const view = createClientAccessView(ctx, { masonry });
  const bind = createClientAccessBindings(ctx, { clientAccess, renderClientAccess, masonry });
  function renderClientAccess() { view(); bind(); }
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "client-access",
    getScope: () => state.navigationEpoch,
  });

  async function clientAccess() {
    let payload;
    let applied;
    try {
      applied = await refresh.run(
        (signal) =>
          Promise.all([
            api("/client-access", { signal }),
            can("agents.read")
              ? api("/agents", { signal })
              : Promise.resolve([]),
          ]),
        (value) => {
          payload = value;
        },
      );
    } catch (error) {
      const current = document.querySelector("[data-client-access-page]");
      if (!current) throw error;
      current.dataset.refreshError = "1";
      notify(error.message, "error");
      return false;
    }
    if (!applied) return false;
    const [entries, agents] = payload;
    state.data.agents = agents;
    state.data.clientAccessEntries = entries;
    renderClientAccess();
    return true;
  }

  return clientAccess;
}
