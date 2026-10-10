import { animateMotion, cancelMotion } from "./motion.js";

// Card drop geometry, animation and reversible drag cleanup.

export function nodeCardDropIndex(rects, pointer, grabOffset = { x: 0, y: 0 }) {
  if (!rects.length) return 0;
  const x = pointer.x - grabOffset.x;
  const y = pointer.y - grabOffset.y;
  const rows = [];
  rects
    .map((rect, index) => ({
      index,
      left: rect.left,
      right: rect.right,
      top: rect.top,
      bottom: rect.bottom,
      centerX: rect.left + (rect.right - rect.left) / 2,
    }))
    .sort((a, b) => a.top - b.top || a.left - b.left)
    .forEach((slot) => {
      const row = rows.find(
        (item) => slot.top < item.bottom && slot.bottom > item.top,
      );
      if (row) {
        row.top = Math.min(row.top, slot.top);
        row.bottom = Math.max(row.bottom, slot.bottom);
        row.slots.push(slot);
      } else {
        rows.push({ top: slot.top, bottom: slot.bottom, slots: [slot] });
      }
    });
  const row = rows.reduce((nearest, candidate) => {
    const distance =
      y < candidate.top
        ? candidate.top - y
        : y > candidate.bottom
          ? y - candidate.bottom
          : 0;
    return !nearest || distance < nearest.distance
      ? { row: candidate, distance }
      : nearest;
  }, null).row;
  return row.slots
    .sort((a, b) => a.centerX - b.centerX)
    .reduce((nearest, slot) => {
      const distance = Math.abs(x - slot.centerX);
      return !nearest || distance < nearest.distance
        ? { index: slot.index, distance }
        : nearest;
    }, null).index;
}

export function animateNodeCardDrop(
  items, oldRects, { animate = animateMotion, onSettled = () => {} } = {},
) {
  // Read all geometry before starting compositor animations. No forced layout
  // per card, temporary transform styles, or transition listeners are needed.
  const moves = items.map((item) => {
    cancelMotion(item);
    const previous = oldRects.get(item);
    if (!previous) return null;
    const rect = item.getBoundingClientRect();
    const dx = previous.left - rect.left, dy = previous.top - rect.top;
    return Math.abs(dx) < 1 && Math.abs(dy) < 1 ? null : { item, dx, dy };
  }).filter(Boolean);
  let remaining = moves.length;
  let settled = false;
  const settle = () => {
    if (settled) return;
    settled = true;
    onSettled();
  };
  if (!remaining) { settle(); return () => {}; }
  const cancels = moves.map(({ item, dx, dy }) => animate(item, [
    { transform: `translate(${dx}px, ${dy}px)` }, { transform: "none" },
  ], { id: "qch-drop", onSettled: () => { if (!--remaining) settle(); } }));
  return () => { cancels.forEach(cancel => cancel()); settle(); };
}

// Arrow keys provide the same order commit as dragging, without a gesture.
export function moveCardByKey(event, grid, selector) {
  const direction = { ArrowLeft: -1, ArrowUp: -1, ArrowRight: 1, ArrowDown: 1 }[event.key];
  if (!direction) return null;
  event.preventDefault();
  event.stopPropagation();
  const cards = [...grid.querySelectorAll(selector)];
  const card = event.currentTarget.closest(selector), index = cards.indexOf(card);
  const target = cards[index + direction];
  if (!target) return null;
  cards.forEach(cancelMotion);
  const oldRects = new Map(cards.map(item => [item, item.getBoundingClientRect()]));
  if (direction < 0) target.before(card); else target.after(card);
  event.currentTarget.focus({ preventScroll: true });
  let status = grid.parentElement.querySelector("[data-card-order-status]");
  if (!status) {
    status = document.createElement("span");
    status.className = "visually-hidden";
    status.dataset.cardOrderStatus = "";
    status.setAttribute("role", "status");
    grid.after(status);
  }
  status.textContent = `已移至第 ${index + direction + 1} 项，共 ${cards.length} 项`;
  return { cards: [...grid.querySelectorAll(selector)], oldRects };
}

export function clearNodeCardDragState(
  grid,
  drag,
  { clearAnimationStyles = true, body = document.body } = {},
) {
  if (!drag) return;
  drag.card.classList.remove("dragging");
  body.classList.remove("node-card-dragging");
  grid.querySelectorAll(".node-card").forEach((card) => {
    card.classList.remove("drop-target");
    card.style.order = "";
    if (clearAnimationStyles) {
      card.style.transform = "";
      card.style.transition = "";
    }
  });
  drag.ghost?.remove();
}
