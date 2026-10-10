const scrollPaint = new WeakMap();

// A retired surface retains the currently painted descendants, including
// interrupted native transitions. It owns no listeners or live form state.
export function freezePaint(source, copy) {
  const originals = [source, ...source.querySelectorAll("*")];
  const copies = [copy, ...copy.querySelectorAll("*")];
  originals.forEach((element, index) => {
    const clone = copies[index];
    // Native cloning retains input/textarea drafts, but not option selection
    // or scroll offsets. Those must match the last live frame during exit.
    if (element.tagName === "SELECT") {
      [...element.options].forEach((option, optionIndex) => { clone.options[optionIndex].selected = option.selected; });
      if (!element.multiple) clone.selectedIndex = element.selectedIndex;
    }
    const top = element.scrollTop, left = element.scrollLeft;
    if (top || left) scrollPaint.set(clone, { top, left });
  });
  const targets = new Set(source.querySelectorAll("h2,footer"));
  source.getAnimations({ subtree: true }).forEach(animation => {
    if (animation.effect?.target) targets.add(animation.effect.target);
  });
  targets.delete(source);
  targets.forEach(target => {
    const index = originals.indexOf(target);
    if (index < 1) return;
    const painted = getComputedStyle(target), clone = copies[index];
    for (const property of ["opacity", "translate", "scale", "transform"]) clone.style[property] = painted[property];
    clone.style.animation = clone.style.transition = "none";
  });
}

// Detached copies have no scrollable layout. Restore offsets after the
// isolated host is mounted, before the browser paints its first exit frame.
export function restorePaintScroll(surface) {
  [surface, ...surface.querySelectorAll("*")].forEach(element => {
    const scroll = scrollPaint.get(element);
    if (!scroll) return;
    element.scrollTop = scroll.top;
    element.scrollLeft = scroll.left;
    scrollPaint.delete(element);
  });
}
