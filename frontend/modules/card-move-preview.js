import { freezePaint } from "./paint-snapshot.js";

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
  return ghost;
}

export function moveCardGhost(drag) {
  // clientX/Y, painted bounds and the grab point are all viewport CSS pixels.
  // Convert once into the layer's local space (CSS/browser zoom can scale it).
  const layer = drag.ghost.parentElement, box = layer.getBoundingClientRect();
  const sx = box.width / layer.offsetWidth || 1, sy = box.height / layer.offsetHeight || 1;
  // clientLeft/Top round fractional borders under CSS zoom. The absolute
  // child's origin is the painted padding edge, so retain the used precision.
  const border = getComputedStyle(layer);
  Object.assign(drag.ghost.style, {
    left: "0px", top: "0px", width: `${drag.rect.width / sx}px`, height: `${drag.rect.height / sy}px`,
    transform: `translate(${(drag.point.x - drag.grab.x - box.left) / sx - parseFloat(border.borderLeftWidth)}px, ${(drag.point.y - drag.grab.y - box.top) / sy - parseFloat(border.borderTopWidth)}px)`,
  });
}
