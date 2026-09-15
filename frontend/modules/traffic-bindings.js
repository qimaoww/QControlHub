import { trafficCardIdentity } from "./traffic-model.js";
export function createTrafficBindings({ state }, { bindTrafficForms, enableTrafficCardDrag }) {
  return ({ selectableAgents, filteredItems, orderedItems }, endpoints, resetCreate) => {
    bindTrafficForms(selectableAgents, endpoints);
    document.querySelectorAll("[data-traffic-status-open]").forEach(button => {
      button.onclick = () => document.querySelector(`[data-traffic-status-dialog="${CSS.escape(button.dataset.trafficStatusOpen)}"]`)?.showModal();
    });
    document.querySelectorAll("[data-traffic-status-dialog]").forEach(dialog => {
      dialog.querySelector("[data-traffic-status-close]").onclick = () => dialog.close();
      dialog.onclick = event => { if (event.target === dialog) dialog.close(); };
    });
    const cardGrid = document.querySelector(".traffic-policy-grid");
    if (cardGrid && filteredItems.length > 1) {
      enableTrafficCardDrag(cardGrid, orderedItems.map(trafficCardIdentity));
    }
    if (resetCreate) document.querySelector("#traffic-policy-form")?.reset();
    if (state.anchor === "traffic-new") {
      const create = document.querySelector("#traffic-new");
      create?.showModal();
      state.anchor = "traffic";
    }
  };

}
