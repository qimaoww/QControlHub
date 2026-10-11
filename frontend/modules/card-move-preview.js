import { animateMotion } from "./motion.js";
import { freezePaint } from "./paint-snapshot.js";
import { nodeCardDropIndex } from "./agent-card-drag.js";

export function createCardMoveGhost(card, rect) {
  const ghost = card.cloneNode(true);
  freezePaint(card, ghost);
  ghost.classList.remove("dragging", "drop-target");
  ghost.classList.add("card-move-ghost", card.matches(".node-card") ? "node-card-ghost" : "traffic-card-ghost");
  // Previews cannot participate in metrics refresh, selectors, dialogs or ARIA.
  [ghost, ...ghost.querySelectorAll("*")].forEach(element => {
    [...element.attributes].forEach(attribute => {
      if (attribute.name === "id" || ["data-agent-node", "data-agent-metrics", "data-metric-poll", "data-refresh-key", "data-traffic-card-key", "data-motion-region", "data-motion-key"].includes(attribute.name)) element.removeAttribute(attribute.name);
    });
  });
  ghost.querySelectorAll("dialog").forEach(dialog => dialog.remove());
  ghost.inert = true;
  ghost.setAttribute("aria-hidden", "true");
  ghost.removeAttribute("href");
  Object.assign(ghost.style, {
    left: `${rect.left}px`, top: `${rect.top}px`, width: `${rect.width}px`, height: `${rect.height}px`,
    order: "", gridRowEnd: "", transform: "none", transition: "none",
    transformOrigin: "top left",
  });
  // Traffic card layout is scoped to its workspace. Preserve that context in
  // the viewport layer so pickup cannot change the header, type or spacing.
  const layer = document.createElement("div");
  layer.className = `card-move-layer${card.matches(".traffic-policy-card") ? " traffic-workspace" : ""}`;
  layer.inert = true;
  layer.setAttribute("aria-hidden", "true");
  layer.append(ghost);
  document.body.append(layer);
  animateMotion(ghost, [{ boxShadow: "var(--shadow)" }, { boxShadow: "var(--shadow-lift)" }], {
    id: "qch-card-pickup", token: "--motion-card-pickup", retarget: false,
  });
  return ghost;
}

export function moveCardGhost(drag) {
  drag.ghost.style.transform = `translate(${drag.point.x - drag.start.x}px, ${drag.point.y - drag.start.y}px)`;
}

// Slots stay fixed during a gesture, as do the original traffic cards.
// Scrolling offsets these same slots without changing their identities.
export function cardMoveDropIndex(drag, grid) {
  const current = grid.getBoundingClientRect();
  const dx = current.left - drag.gridRect.left, dy = current.top - drag.gridRect.top;
  const center = { x: drag.point.x - drag.offset.x, y: drag.point.y - drag.offset.y };
  if (center.x < current.left - 32 || center.x > current.right + 32 ||
      center.y < current.top - 48 || center.y > current.bottom + 48) return null;
  return nodeCardDropIndex(drag.slots.map(rect => ({
    left: rect.left + dx, right: rect.right + dx, top: rect.top + dy, bottom: rect.bottom + dy,
  })), drag.point, drag.offset);
}

export function createCardMoveScroll(grid, update) {
  let scroller = grid.parentElement;
  while (scroller && !/(auto|scroll)/.test(getComputedStyle(scroller).overflowY)) scroller = scroller.parentElement;
  scroller ||= document.scrollingElement;
  let frame = null, point = null, previous = null;
  const tick = time => {
    frame = null;
    const viewport = scroller === document.scrollingElement ? { top: 0, bottom: innerHeight } : scroller.getBoundingClientRect();
    const dock = document.querySelector(".app-dock");
    const bottom = Math.min(innerHeight, viewport.bottom, dock && getComputedStyle(dock).position === "fixed" ? dock.getBoundingClientRect().top : innerHeight);
    const bar = document.querySelector(".workspace-topbar");
    const barBottom = bar && /sticky|fixed/.test(getComputedStyle(bar).position) ? bar.getBoundingClientRect().bottom : 0;
    const top = Math.max(0, viewport.top, barBottom);
    const velocity = point.y < top + 56 ? -Math.min(1, (top + 56 - point.y) / 56)
      : point.y > bottom - 56 ? Math.min(1, (point.y - bottom + 56) / 56) : 0;
    const before = scroller.scrollTop;
    scroller.scrollTop += velocity * Math.min(32, previous == null ? 16 : time - previous) * .75;
    previous = time;
    if (scroller.scrollTop !== before) update();
    frame = requestAnimationFrame(tick);
  };
  return {
    move(value) { point = value; if (frame == null) frame = requestAnimationFrame(tick); },
    stop() { if (frame != null) cancelAnimationFrame(frame); frame = null; point = previous = null; },
  };
}

