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
  items, oldRects, { animate = animateMotion, onSettled = () => {}, token = "--motion-reorder", resize = false } = {},
) {
  // Read all geometry before starting compositor animations. No forced layout
  // per card, temporary transform styles, or transition listeners are needed.
  const moves = items.map((item) => {
    cancelMotion(item);
    const previous = oldRects.get(item);
    if (!previous) return null;
    const rect = item.getBoundingClientRect();
    const scaleX = rect.width ? rect.width / item.offsetWidth : 1;
    const scaleY = rect.height ? rect.height / item.offsetHeight : 1;
    const dx = (previous.left - rect.left) / (scaleX || 1), dy = (previous.top - rect.top) / (scaleY || 1);
    const sx = resize && previous.width && rect.width ? previous.width / rect.width : 1;
    const sy = resize && previous.height && rect.height ? previous.height / rect.height : 1;
    const resized = resize && (Math.abs((previous.width || rect.width) - rect.width) > 1 || Math.abs((previous.height || rect.height) - rect.height) > 1);
    return Math.abs(dx) < 1 && Math.abs(dy) < 1 && !resized ? null : { item, dx, dy, sx, sy };
  }).filter(Boolean);
  let remaining = moves.length;
  let settled = false;
  const settle = () => {
    if (settled) return;
    settled = true;
    onSettled();
  };
  if (!remaining) { settle(); return () => {}; }
  const cancels = moves.map(({ item, dx, dy, sx, sy }) => animate(item, [
    { transform: `translate(${dx}px, ${dy}px)${resize ? ` scale(${sx}, ${sy})` : ""}`, ...(resize ? { transformOrigin: "top left" } : {}) },
    { transform: "none", ...(resize ? { transformOrigin: "top left" } : {}) },
  ], { id: "qch-drop", token, retarget: false, onSettled: () => { if (!--remaining) settle(); } }));
  return () => { cancels.forEach(cancel => cancel()); settle(); };
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
