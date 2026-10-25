import { createTrafficBatch } from "./traffic-batch.js";
import { updateFeedback } from "./presence-motion.js";
import { createTrafficFormView } from "./traffic-form-view.js";
import { createTrafficOrder } from "./traffic-order.js";
import { createTrafficCardInteractions } from "./traffic-card-interactions.js";
import { createTrafficView } from "./traffic-view.js";
import { createTrafficSyncDialog } from "./traffic-sync-dialog.js";
import { createTrafficForms } from "./traffic-forms.js";
import { createTrafficBindings } from "./traffic-bindings.js";
export { renderTrafficAccounting } from "./traffic-accounting-view.js";
export { trafficCardIdentity, orderTrafficItems, mergeVisibleTrafficCardOrder, trafficRateForDisplay, mergeTrafficPorts } from "./traffic-model.js";
export { resetTrafficCreateForm } from "./traffic-form-model.js";
import { createInteractionGate, createPoller, createRefreshChannel } from "./refresh.js";
import { reusableTrafficSnapshot } from "./traffic-model.js";

import { accountStorage } from "./account-storage.js";

// Quota editing dialogs hold unsaved drafts. A background refresh must not
// rebuild them, so it waits for the last one to close. Status dialogs render
// live accounting and opt in through data-refresh-live.
const draftDialogSelector = ".traffic-workspace dialog[open]:not([data-refresh-live])";

export function installTraffic(ctx) {
  const {
    api, state,
    storage = accountStorage,
    setTimer = (callback, delay) => {
      state.trafficPollTimer = setTimeout(callback, delay);
      return state.trafficPollTimer;
    },
    clearTimer = (timer) => clearTimeout(timer),
  } = ctx;
  // A response is only applied to the account it was read for.
  let accountData = state.data;
  let accountSession = state.session;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "traffic" &&
      state.data === accountData && state.session === accountSession,
    getScope: () => state.navigationEpoch,
  });
  const cardInteractions = createInteractionGate();
  let pendingTrafficRender = null;
  // Last complete read of the current account's traffic scope.
  let snapshot = null;
  let deferredTraffic = null;
  const draftWatchers = new Set();
  const filters = () => {
    state.data.trafficFilters ||= {};
    return state.data.trafficFilters;
  };

  const fields = createTrafficFormView(ctx);
  const order = createTrafficOrder(storage);
  const cards = createTrafficCardInteractions({ cardInteractions, ...order, notify: ctx.notify });
  const view = createTrafficView(ctx, { filters, storage, ...order, ...fields });
  const sync = createTrafficSyncDialog(ctx, { traffic });
  const bindTrafficForms = createTrafficForms(ctx, { filters, renderTraffic, traffic, sync, ...fields });
  const batch = createTrafficBatch(ctx, { renderTraffic, traffic, cancelCardDrag: cards.cancel });
  const bind = createTrafficBindings(ctx, { bindTrafficForms, bindTrafficBatch: batch.bind, ...cards });

  function releaseDraftWatchers() {
    draftWatchers.forEach((dialog) => dialog.removeEventListener("close", drainDeferredTraffic));
    draftWatchers.clear();
  }

  function drainDeferredTraffic() {
    if (document.querySelectorAll(draftDialogSelector).length) return;
    releaseDraftWatchers();
    const pending = deferredTraffic;
    deferredTraffic = null;
    if (!pending || state.route !== "traffic") return;
    renderTraffic(...pending);
  }

  function deferDraftTraffic(values) {
    const dialogs = document.querySelectorAll(draftDialogSelector);
    if (!dialogs.length) return false;
    deferredTraffic = values;
    dialogs.forEach((dialog) => {
      if (draftWatchers.has(dialog)) return;
      draftWatchers.add(dialog);
      dialog.addEventListener("close", drainDeferredTraffic);
    });
    return true;
  }

  function renderTraffic(agents, policies, endpoints, resetCreate = false, background = false) {
    if (background && deferDraftTraffic([agents, policies, endpoints])) return;
    if (deferredTraffic) {
      releaseDraftWatchers();
      deferredTraffic = null;
    }
    if (cardInteractions.activeCount()) {
      pendingTrafficRender = [agents, policies, endpoints, resetCreate];
      cardInteractions.defer(() => {
        const pending = pendingTrafficRender;
        pendingTrafficRender = null;
        if (pending && state.route === "traffic") renderTraffic(...pending);
      }, "traffic-card-refresh");
      return;
    }
    cards.cancel();

    const rendered = view(agents, policies, endpoints);
    bind(rendered, endpoints, resetCreate);
  }
  const poller = createPoller({
    run: () => traffic({ background: true }),
    isActive: () => state.route === "traffic",
    delay: () => 2000,
    setTimer,
    clearTimer,
  });

  // Scope-only navigations (sidebar node scope, user scope, engine, port and
  // status filters, returning to the page) render the account's fresh snapshot
  // instead of re-reading the same three collections. A call inside one
  // navigation is a live refresh (poll, save, batch, sync) and reads the server.
  function reusableSnapshot(epoch, data, session, canRead) {
    if (snapshot && (snapshot.data !== data || snapshot.session !== session))
      snapshot = null;
    const reusable = reusableTrafficSnapshot(snapshot, { data, session, canRead, epoch });
    if (snapshot) snapshot.epoch = epoch;
    return reusable;
  }

  async function traffic({ background = false, resetCreate = false } = {}) {
    const epoch = state.navigationEpoch;
    const data = state.data;
    const session = state.session;
    accountData = data;
    accountSession = session;
    const canRead = ctx.can("agents.read");
    const cached = background ? null : reusableSnapshot(epoch, data, session, canRead);
    if (cached) {
      // Keep exactly one poll timer: a navigation cleared the pending id even
      // though this render did not start a request.
      poller.start();
      renderTraffic(cached.agents, cached.policies, cached.endpoints, resetCreate);
      return true;
    }
    poller.stop();
    try {
      const applied = await refresh.run(
        async (signal) => {
          if (!canRead) {
            const [page, endpoints] = await Promise.all([
              api("/traffic-policies?view=page", { signal }),
              api("/traffic-endpoints", { signal }),
            ]);
            return [page.agents, page.policies, endpoints];
          }
          return Promise.all([
            api("/agents", { signal }),
            api("/traffic-policies", { signal }),
            api("/traffic-endpoints", { signal }),
          ]);
        },
        ([agents, policies, endpoints]) => {
          snapshot = { agents, policies, endpoints, at: Date.now(), epoch, data, session, canRead };
          renderTraffic(agents, policies, endpoints, resetCreate, background);
        },
      );
      if (applied) poller.start();
      return applied;
    } catch (error) {
      const status = document.querySelector(".traffic-total");
      if (!status && !background) throw error;
      if (status) {
        status.dataset.refreshError = "1";
        status.title = error.message;
        const label = status.querySelector("[data-traffic-refresh-label]");
        if (label) updateFeedback(label, "刷新失败，保留上次数据");
      }
      poller.start();
      return false;
    }
  }

  // Quota writes change the exact collections this snapshot holds.
  traffic.invalidate = () => { snapshot = null; };

  traffic.cancelInteractions = () => {
    poller.stop();
    refresh.invalidate();
    cardInteractions.cancel();
    pendingTrafficRender = null;
    releaseDraftWatchers();
    deferredTraffic = null;
    if (snapshot && (snapshot.data !== state.data || snapshot.session !== state.session))
      snapshot = null;
    cards.cancel();
    batch.cancel();
  };
  return traffic;
}
