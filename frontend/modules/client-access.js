import { createCardMasonry } from "./card-masonry.js";
import { createClientAccessView } from "./client-access-view.js";
import { createClientAccessBindings } from "./client-access-bindings.js";
export { normalizeClientAccessFilters, filterClientAccessEntries, groupClientAccessEntries, clientAccessAddressChoices, clientAccessEntryForAddress } from "./client-access-model.js";
export { copyClientValue } from "./client-clipboard.js";
import { createRefreshChannel } from "./refresh.js";

export function installClientAccess(ctx) {
  const { api, state, can } = ctx;
  const masonry = createCardMasonry(".client-access-node-grid:not(.empty)", ".client-access-node-card");
  const view = createClientAccessView(ctx, { masonry });
  const bind = createClientAccessBindings(ctx, { clientAccess, renderClientAccess, masonry });
  function renderClientAccess() {
    const scroller = document.querySelector("[data-client-access-page]")?.closest(".workspace-main");
    const top = scroller?.scrollTop;
    view();
    bind();
    // Masonry spans are reapplied after keyed reconciliation. Restore the
    // reading position after that layout, when the full scroll range exists.
    if (scroller?.isConnected) scroller.scrollTop = top;
  }
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "client-access",
    getScope: () => state.navigationEpoch,
  });

  async function clientAccess() {
    if (state.route !== "client-access") return false;
    state.data.clientAccessLoading = true;
    state.data.clientAccessError = "";
    if (!document.querySelector("[data-client-access-page]")) renderClientAccess();
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
      state.data.clientAccessLoading = false;
      state.data.clientAccessError = error.message;
      renderClientAccess();
      return false;
    }
    if (!applied) return false;
    state.data.clientAccessLoading = false;
    state.data.clientAccessError = "";
    const [entries, agents] = payload;
    state.data.agents = agents;
    state.data.clientAccessEntries = entries;
    renderClientAccess();
    return true;
  }

  clientAccess.dispose = () => { refresh.invalidate(); masonry.disconnect(); };
  return clientAccess;
}
