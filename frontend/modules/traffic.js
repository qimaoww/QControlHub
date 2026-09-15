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

import { accountStorage } from "./account-storage.js";

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
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "traffic",
    getScope: () => state.navigationEpoch,
  });
  const cardInteractions = createInteractionGate();
  let pendingTrafficRender = null;
  const filters = () => {
    state.data.trafficFilters ||= {};
    return state.data.trafficFilters;
  };

  const fields = createTrafficFormView(ctx);
  const order = createTrafficOrder(storage);
  const cards = createTrafficCardInteractions({ cardInteractions, ...order });
  const view = createTrafficView(ctx, { filters, storage, ...order, ...fields });
  const sync = createTrafficSyncDialog(ctx, { traffic });
  const bindTrafficForms = createTrafficForms(ctx, { filters, renderTraffic, traffic, sync, ...fields });
  const bind = createTrafficBindings(ctx, { bindTrafficForms, ...cards });
  function renderTraffic(agents, policies, endpoints, resetCreate = false) {
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

  async function traffic({ background = false, resetCreate = false } = {}) {
    poller.stop();
    try {
      const applied = await refresh.run(
        (signal) => Promise.all([
          api("/agents", { signal }),
          api("/traffic-policies", { signal }),
          background && Array.isArray(state.data.trafficEndpoints)
            ? Promise.resolve(state.data.trafficEndpoints)
            : api("/traffic-endpoints", { signal }),
        ]),
        ([agents, policies, endpoints]) => renderTraffic(agents, policies, endpoints, resetCreate),
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
        if (label) label.textContent = "刷新失败，保留上次数据";
      }
      poller.start();
      return false;
    }
  }

  return traffic;
}
