// A retired surface retains the currently painted descendants, including
// interrupted native transitions. It owns no listeners or live form state.
export function freezePaint(source, copy) {
  const originals = [source, ...source.querySelectorAll("*")];
  const copies = [copy, ...copy.querySelectorAll("*")];
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
