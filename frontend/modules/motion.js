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
