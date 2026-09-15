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
  items,
  oldRects,
  {
    requestFrame = (callback) => requestAnimationFrame(callback),
    cancelFrame = (frame) => cancelAnimationFrame(frame),
    setTimer = (callback, delay) => setTimeout(callback, delay),
    clearTimer = (timer) => clearTimeout(timer),
    fallbackDelay = 240,
    onSettled = () => {},
  } = {},
) {
  let active = true;
  let settled = false;
  let frame = null;
  let timer = null;
  const animated = new Set();
  const listeners = new Map();
  const clear = (snap = false) => {
    if (!active) return;
    active = false;
    if (frame != null) cancelFrame(frame);
    if (timer != null) clearTimer(timer);
    listeners.forEach((listener, item) =>
      item.removeEventListener("transitionend", listener),
    );
    items.forEach((item) => {
      if (snap) {
        item.style.transition = "none";
        item.style.transform = "";
        void item.offsetWidth;
      }
      item.style.transition = "";
      item.style.transform = "";
    });
    if (!settled) {
      settled = true;
      onSettled();
    }
  };
  items.forEach((item) => {
    const prev = oldRects.get(item);
    if (!prev) return;
    const rect = item.getBoundingClientRect();
    const dx = prev.left - rect.left;
    const dy = prev.top - rect.top;
    if (Math.abs(dx) < 1 && Math.abs(dy) < 1) return;
    item.style.transition = "none";
    item.style.transform = `translate(${dx}px, ${dy}px)`;
    void item.offsetWidth;
    animated.add(item);
  });
  if (!animated.size) {
    clear();
    return () => {};
  }
  animated.forEach((item) => {
    const listener = (event) => {
      if (event.target !== item || event.propertyName !== "transform") return;
      animated.delete(item);
      if (!animated.size) clear();
    };
    listeners.set(item, listener);
    item.addEventListener("transitionend", listener);
  });
  frame = requestFrame(() => {
    frame = null;
    if (!active) return;
    animated.forEach((item) => {
      item.style.transition = "";
      item.style.transform = "";
    });
    timer = setTimer(() => clear(), fallbackDelay);
  });
  return () => clear(true);
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
