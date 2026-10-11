// Layout coordinates never include a neighbour's FLIP animation. The grid is
// the offset parent; its viewport projection includes ancestor scroll/zoom.
export function cardMoveLayout(grid, items) {
  const box = grid.getBoundingClientRect();
  const sx = box.width / grid.offsetWidth || 1, sy = box.height / grid.offsetHeight || 1;
  const ordered = [...items].sort((a, b) => Number(a.style.order) - Number(b.style.order));
  const slots = ordered.map(card => {
    const left = box.left + (grid.clientLeft + card.offsetLeft - grid.scrollLeft) * sx;
    const top = box.top + (grid.clientTop + card.offsetTop - grid.scrollTop) * sy;
    return { card, left, top, right: left + card.offsetWidth * sx, bottom: top + card.offsetHeight * sy };
  });
  return { box, slots, signature: slots.map(r => [r.left, r.top, r.right, r.bottom].join(",")).join(";") };
}

const distance = (r, p) => Math.hypot(Math.max(r.left - p.x, 0, p.x - r.right), Math.max(r.top - p.y, 0, p.y - r.bottom));

export function cardMoveTarget({ box, slots }, point, current = null) {
  if (point.x < box.left - 24 || point.x > box.right + 24 || point.y < box.top - 32 || point.y > box.bottom + 32) return null;
  // A small boundary dead band prevents oscillation when rows change height or
  // the pointer jitters along a gap. The pointer, not the ghost centre, selects.
  if (current != null && slots[current] && distance(slots[current], point) <= 6) return current;
  let nearest = null;
  slots.forEach((slot, index) => {
    const gap = distance(slot, point);
    const center = Math.hypot(point.x - (slot.left + slot.right) / 2, point.y - (slot.top + slot.bottom) / 2);
    if (!nearest || gap < nearest.gap || gap === nearest.gap && center < nearest.center) nearest = { index, gap, center };
  });
  return nearest?.index ?? null;
}

export function cardMoveOrder(original, card, index) {
  const next = original.filter(item => item !== card);
  next.splice(index, 0, card);
  return next;
}
