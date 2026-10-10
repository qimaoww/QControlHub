// Native animations own no inline styles or business state. A bounded fallback
// also settles them when the document is hidden or finish events are lost.
export function createMotionController({
  media = () => globalThis.matchMedia?.("(prefers-reduced-motion: reduce)"),
  style = (element) => getComputedStyle(element),
  setTimer = (callback, delay) => setTimeout(callback, delay),
  clearTimer = (timer) => clearTimeout(timer),
  observe = (callback) => {
    const observer = new MutationObserver(callback);
    observer.observe(document.documentElement, { childList: true, subtree: true });
    return () => observer.disconnect();
  },
} = {}) {
  const active = new Map();
  let preference;
  let stopObserving;
  const cancel = (root) => {
    for (const [element, settle] of [...active])
      if (!root || root === element || root.contains(element)) settle();
  };
  const preferenceChanged = () => { if (preference.matches) cancel(); };
  const watch = () => {
    if (active.size !== 1) return;
    preference = media();
    preference?.addEventListener?.("change", preferenceChanged);
    stopObserving = observe(() => {
      for (const [element, settle] of [...active])
        if (!element.isConnected) settle();
    });
  };
  const unwatch = () => {
    if (active.size) return;
    preference?.removeEventListener?.("change", preferenceChanged);
    preference = null;
    stopObserving?.();
    stopObserving = null;
  };
  const animate = (element, keyframes, {
    token = "--motion-base", fallback = 200, id = "qch-surface",
    onSettled = () => {},
  } = {}) => {
    active.get(element)?.();
    if (!element?.isConnected || !element.animate || media()?.matches) {
      onSettled();
      return () => {};
    }
    const computed = style(element);
    const value = computed.getPropertyValue(token).trim();
    const parsed = Number.parseFloat(value);
    const duration = Number.isFinite(parsed)
      ? Math.max(0, parsed * (value.endsWith("ms") ? 1 : 1000)) : fallback;
    const animation = element.animate(keyframes, {
      duration, easing: computed.getPropertyValue("--motion-ease-out").trim() || "ease-out",
      fill: "none",
    });
    animation.id = id;
    let settled = false;
    let timer;
    const settle = () => {
      if (settled) return;
      settled = true;
      clearTimer(timer);
      animation.onfinish = animation.oncancel = null;
      animation.cancel();
      active.delete(element);
      unwatch();
      onSettled();
    };
    active.set(element, settle);
    animation.onfinish = animation.oncancel = settle;
    timer = setTimer(settle, duration + 80);
    watch();
    return settle;
  };
  return { animate, cancel, activeCount: () => active.size };
}

let controller;
const motion = () => (controller ||= createMotionController());
export function cancelMotion(root) { controller?.cancel(root); }
export function animateMotion(element, keyframes, options) {
  return motion().animate(element, keyframes, options);
}
export function enterSurface(element, options) {
  return animateMotion(element, [{ opacity: .55 }, { opacity: 1 }], options);
}
export function reducedMotion() {
  return Boolean(globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches);
}

const selectionScopes = new WeakMap();
// Semantic selection keys exclude live data. A loading/preview paint does not
// consume a selection: its completed result owns the single short feedback.
// The workspace owns these keys even when a result changes to an empty state.
export function syncSelectionMotion(root, { animate = true } = {}) {
  if (!root) return;
  if (root.getAnimations?.().some(animation => animation.id === "qch-route")) animate = false;
  const previous = selectionScopes.get(root) || new Map();
  const present = new Set();
  const changed = [];
  root.querySelectorAll("[data-motion-region]").forEach(element => {
    const name = element.dataset.motionRegion;
    const key = element.dataset.motionKey || "";
    present.add(name);
    if (!previous.has(name) || !animate) {
      previous.set(name, key);
      return;
    }
    if (previous.get(name) === key) {
      if (element.dataset.motionReady === "false") cancelMotion(element);
      return;
    }
    cancelMotion(element);
    if (element.dataset.motionReady === "false") return;
    previous.set(name, key);
    if (element.closest("[hidden],details:not([open])")) return;
    // Incremental search remains readable while typing or composing. Its
    // count and selected controls provide feedback without pulsing the rows.
    if (element.hasAttribute("data-motion-live") &&
        root.contains(document.activeElement) &&
        document.activeElement.matches('input[type="search"]')) return;
    changed.push(element);
  });
  for (const name of previous.keys()) if (!present.has(name)) previous.delete(name);
  selectionScopes.set(root, previous);
  changed.filter(element => !changed.some(parent => parent !== element && parent.contains(element)))
    .forEach(element => {
      cancelMotion(element);
      animateMotion(element, [{ opacity: .8 }, { opacity: 1 }], {
        id: "qch-selection", token: "--motion-feedback",
      });
    });
}
