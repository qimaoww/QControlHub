import { bindEvent } from "./refresh.js";
import { animateNodeCardDrop, clearNodeCardDragState, nodeCardDropIndex } from "./agent-card-drag.js";
import { saveNodeOrder } from "./node-order.js";
import { openRegionPicker } from "./regions.js";

export function createAgentCardInteractions({ api, state, esc, notify }, { can, cardInteractions, loadRegionDisplay, loadKomariDisplay }) {
  let cancelCardDrag = () => {};
// Drag reordering of the overview cards. A cloned ghost follows the cursor and
// the target card is highlighted, while the grid itself does not reflow
// mid-drag; on release the cards FLIP-animate to their new layout and the
// order is committed to localStorage.
function enableCardDrag(grid) {
  let drag = null;
  let cancelLanding = null;
  const dropIndex = (pointerX, pointerY) => {
    const rects = [...grid.querySelectorAll(".node-card")].map((card) =>
      card.getBoundingClientRect(),
    );
    return nodeCardDropIndex(
      rects,
      { x: pointerX, y: pointerY },
      drag.grabOffset,
    );
  };
  const highlight = (index) => {
    grid
      .querySelectorAll(".node-card")
      .forEach((card) => card.classList.remove("drop-target"));
    if (index == null) return;
    const rest = [...grid.querySelectorAll(".node-card")].filter(
      (card) => card !== drag.card,
    );
    rest[index]?.classList.add("drop-target");
  };
  const clearDragState = (clearAnimationStyles) => {
    if (!drag) return;
    clearNodeCardDragState(grid, drag, {
      clearAnimationStyles,
    });
    drag = null;
  };
  const reset = () => {
    const landing = cancelLanding;
    cancelLanding = null;
    const releaseInteraction = drag?.releaseInteraction;
    landing?.();
    clearDragState(true);
    if (!landing) releaseInteraction?.();
  };
  cancelCardDrag = reset;
  const finish = (event) => {
    if (!drag || event.pointerId !== drag.pointerId) return;
    const { card, moved, drop, releaseInteraction } = drag;
    if (!moved || !grid.contains(card)) return reset();
    const rest = [...grid.querySelectorAll(".node-card")].filter(
      (item) => item !== card,
    );
    const ghostRect = drag.ghost
      ? drag.ghost.getBoundingClientRect()
      : card.getBoundingClientRect();
    const oldRects = new Map(
      [card, ...rest].map((item) => [
        item,
        item === card ? ghostRect : item.getBoundingClientRect(),
      ]),
    );
    const target = drop == null || drop >= rest.length ? null : rest[drop];
    if (target) target.before(card);
    else grid.append(card);
    const next = [...grid.querySelectorAll(".node-card")];
    const ids = next.map((item) => item.dataset.agentNode);
    clearDragState(false);
    let settled = false;
    const cancelAnimation = animateNodeCardDrop(next, oldRects, {
      onSettled: () => {
        settled = true;
        cancelLanding = null;
        releaseInteraction();
      },
    });
    if (!settled) cancelLanding = cancelAnimation;
    saveNodeOrder(ids);
  };
  const cancel = (event) => {
    if (!drag || event.pointerId !== drag.pointerId) return;
    reset();
  };
  grid.querySelectorAll(".node-card-grip").forEach((grip) => {
    bindEvent(grip, "pointerdown", (event) => {
      if (event.button !== 0 || drag) return;
      const releaseInteraction = cardInteractions.begin();
      cancelLanding?.();
      cancelLanding = null;
      const card = grip.closest(".node-card");
      if (!card) {
        releaseInteraction();
        return;
      }
      event.preventDefault();
      const rect = card.getBoundingClientRect();
      drag = {
        card,
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        grabOffset: {
          x: event.clientX - (rect.left + rect.width / 2),
          y: event.clientY - (rect.top + rect.height / 2),
        },
        rect,
        started: false,
        moved: false,
        drop: null,
        ghost: null,
        releaseInteraction,
      };
      grip.setPointerCapture(event.pointerId);
    });
    bindEvent(grip, "pointermove", (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      if (!drag.card.isConnected) {
        reset();
        return;
      }
      if (!drag.started) {
        if (
          Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 4
        )
          return;
        drag.started = true;
        drag.card.classList.add("dragging");
        document.body.classList.add("node-card-dragging");
        const ghost = drag.card.cloneNode(true);
        ghost.classList.remove("dragging");
        ghost.classList.add("node-card-ghost");
        ghost.removeAttribute("href");
        ghost.removeAttribute("data-agent-node");
        ghost.removeAttribute("data-agent-metrics");
        ghost.removeAttribute("data-metric-poll");
        ghost.style.position = "fixed";
        ghost.style.left = `${drag.rect.left}px`;
        ghost.style.top = `${drag.rect.top}px`;
        ghost.style.width = `${drag.rect.width}px`;
        drag.ghost = ghost;
        document.body.appendChild(ghost);
      }
      drag.moved = true;
      const dx = event.clientX - drag.startX;
      const dy = event.clientY - drag.startY;
      drag.ghost.style.transform = `translate(${dx}px, ${dy}px) scale(.99) rotate(.3deg)`;
      const index = dropIndex(event.clientX, event.clientY);
      drag.drop = index;
      highlight(index);
    });
    bindEvent(grip, "pointerup", finish);
    bindEvent(grip, "pointercancel", cancel);
    bindEvent(grip, "lostpointercapture", cancel);
    bindEvent(grip, "click", (event) => {
      event.preventDefault();
      event.stopPropagation();
    });
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
      const originalTitle = button.title;
      button.classList.add("copied");
      button.title = "已复制";
      window.setTimeout(() => {
        button.classList.remove("copied");
        if (button.isConnected) button.title = originalTitle;
      }, 1600);
    });
  });

  }
  return {
    bindCards,
    cancel() { cancelCardDrag(); cancelCardDrag = () => {}; },
  };
}
