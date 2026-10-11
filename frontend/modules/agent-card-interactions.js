import { bindEvent } from "./refresh.js";
import { bindCardMove } from "./card-move.js";
import { saveNodeOrder } from "./node-order.js";
import { openRegionPicker } from "./regions.js";

export function createAgentCardInteractions({ api, state, esc, notify }, { can, cardInteractions, loadRegionDisplay, loadKomariDisplay }) {
  const copyTimers = new Map();
  let cancelCardDrag = () => {};
  function enableCardDrag(grid) {
    cancelCardDrag();
    cancelCardDrag = bindCardMove({
      grid, selector: ".node-card", gripSelector: ".node-card-grip", cardInteractions, notify,
      save: cards => saveNodeOrder(cards.map(card => card.dataset.agentNode)),
    });
  }


  function bindCards(agentsByID, batchForm) {
  document.querySelectorAll("[data-agent-metrics]").forEach((root) => {
    const agent = agentsByID.get(root.dataset.agentMetrics);
    if (!agent) return;
    loadRegionDisplay(agent, root);
    if (root.querySelector("[data-komari-link]")) loadKomariDisplay(agent, root);
  });
  document.querySelectorAll("[data-region-edit]").forEach((button) => {
    button.onclick = (event) => {
      event.preventDefault();
      event.stopPropagation();
      const agent = (state.data.agents || []).find((item) => item.id === button.dataset.regionAvatar) || agentsByID.get(button.dataset.regionAvatar);
      if (!agent || !can("agents.manage", agent)) return;
      const release = cardInteractions.begin();
      openRegionPicker(agent, {
        api, esc, onClose: release,
        onSave: (code) => {
          for (const item of new Set([agent, agentsByID.get(agent.id), ...(state.data.agents || []).filter((item) => item.id === agent.id)])) {
            if (!item) continue;
            item.labels = { ...(item.labels || {}) };
            if (code) item.labels.region_code = code;
            else delete item.labels.region_code;
          }
          document.querySelectorAll("[data-agent-metrics]").forEach((root) => {
            if (root.dataset.agentMetrics === agent.id) loadRegionDisplay(agent, root);
          });
          notify(code ? "国家/地区旗帜已保存" : "已恢复自动识别旗帜");
        },
      });
    };
  });
  const cardGrid = document.querySelector(".node-card-grid");
  if (cardGrid && !batchForm) enableCardDrag(cardGrid);
  document.querySelectorAll("[data-copy-ip]").forEach((button) => {
    bindEvent(button, "click", async (event) => {
      // The card itself is a link to the node workspace; copying must not
      // navigate away from the overview.
      event.preventDefault();
      event.stopPropagation();
      const value = button.dataset.copyIp;
      if (!value) return;
      try {
        await navigator.clipboard.writeText(value);
      } catch {
        const fallback = document.createElement("textarea");
        fallback.value = value;
        fallback.style.position = "fixed";
        fallback.style.opacity = "0";
        document.body.append(fallback);
        fallback.select();
        document.execCommand("copy");
        fallback.remove();
      }
      if (!button.isConnected) return;
      const previous = copyTimers.get(button);
      clearTimeout(previous?.timer);
      const originalTitle = previous?.title || button.title;
      button.classList.add("copied");
      button.title = "已复制";
      const timer = window.setTimeout(() => {
        copyTimers.delete(button);
        button.classList.remove("copied");
        if (button.isConnected) button.title = originalTitle;
      }, 1600);
      copyTimers.set(button, { timer, title: originalTitle });
    });
  });

  }
  return {
    bindCards,
    cancel() {
      cancelCardDrag(); cancelCardDrag = () => {};
      copyTimers.forEach(({ timer, title }, button) => {
        clearTimeout(timer); button.classList.remove("copied"); button.title = title;
      });
      copyTimers.clear();
    },
  };
}
