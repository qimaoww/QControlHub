import { bindEvent } from "./refresh.js";
import {
  animateNodeCardDrop,
  nodeCardDropIndex,
} from "./agent-card-drag.js";

import { mergeVisibleTrafficCardOrder } from "./traffic-model.js";
export function createTrafficCardInteractions({ cardInteractions, saveTrafficCardOrder }) {
  let cancelTrafficCardDrag = () => {};
  function enableTrafficCardDrag(grid, allKeys) {
    let drag = null;
    let cancelLanding = null;
    const cards = () => [...grid.querySelectorAll("[data-traffic-card-key]")];
    const clear = (clearAnimationStyles = true) => {
      if (!drag) return;
      drag.card.classList.remove("dragging");
      document.body.classList.remove("traffic-card-dragging");
      cards().forEach((card) => {
        card.classList.remove("drop-target");
        if (clearAnimationStyles) {
          card.style.transform = "";
          card.style.transition = "";
        }
      });
      drag.ghost?.remove();
      drag = null;
    };
    const reset = () => {
      const landing = cancelLanding;
      cancelLanding = null;
      const releaseInteraction = drag?.releaseInteraction;
      landing?.();
      clear(true);
      if (!landing) releaseInteraction?.();
    };
    cancelTrafficCardDrag = reset;
    const highlight = (index) => {
      cards().forEach((card) => card.classList.remove("drop-target"));
      if (index == null) return;
      cards().filter((card) => card !== drag.card)[index]?.classList.add("drop-target");
    };
    const finish = (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      const { card, moved, drop, releaseInteraction } = drag;
      if (!moved || !grid.contains(card)) return reset();
      const rest = cards().filter((item) => item !== card);
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
      const next = cards();
      const visibleKeys = next.map((item) => item.dataset.trafficCardKey);
      saveTrafficCardOrder(mergeVisibleTrafficCardOrder(allKeys, visibleKeys));
      clear(false);
      let settled = false;
      const cancelAnimation = animateNodeCardDrop(next, oldRects, {
        onSettled: () => {
          settled = true;
          cancelLanding = null;
          releaseInteraction();
        },
      });
      if (!settled) cancelLanding = cancelAnimation;
    };
    const cancel = (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      reset();
    };
    grid.querySelectorAll(".traffic-card-grip").forEach((grip) => {
      bindEvent(grip, "pointerdown", (event) => {
        if (event.button !== 0 || drag) return;
        const releaseInteraction = cardInteractions.begin();
        cancelLanding?.();
        cancelLanding = null;
        const card = grip.closest("[data-traffic-card-key]");
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
          moved: false,
          drop: null,
          ghost: null,
          releaseInteraction,
        };
        grip.setPointerCapture(event.pointerId);
      });
      bindEvent(grip, "pointermove", (event) => {
        if (!drag || event.pointerId !== drag.pointerId) return;
        if (!drag.card.isConnected) return reset();
        if (!drag.ghost) {
          if (Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 4) return;
          drag.card.classList.add("dragging");
          document.body.classList.add("traffic-card-dragging");
          const ghost = drag.card.cloneNode(true);
          ghost.classList.remove("dragging");
          ghost.classList.add("traffic-card-ghost");
          ghost.removeAttribute("id");
          ghost.removeAttribute("data-traffic-card-key");
          ghost.querySelectorAll("dialog,[id]").forEach((element) => {
            if (element.matches("dialog")) element.remove();
            else element.removeAttribute("id");
          });
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
        const rects = cards().map((card) => card.getBoundingClientRect());
        drag.drop = nodeCardDropIndex(
          rects,
          { x: event.clientX, y: event.clientY },
          drag.grabOffset,
        );
        highlight(drag.drop);
      });
      bindEvent(grip, "pointerup", finish);
      bindEvent(grip, "pointercancel", cancel);
      bindEvent(grip, "lostpointercapture", cancel);
    });
  }

  return {
    enableTrafficCardDrag,
    cancel() { cancelTrafficCardDrag(); cancelTrafficCardDrag = () => {}; },
  };
}
