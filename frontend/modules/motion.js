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
  const contains = (root, element) => {
    for (let node = element; node; node = node.getRootNode?.().host)
      if (root === node || root.contains(node)) return true;
    return false;
  };
  const cancel = (root) => {
    for (const [element, settle] of [...active])
      if (!root || contains(root, element)) settle();
  };
  const preferenceChanged = () => { if (preference.matches) cancel(); };
  const visibilityChanged = () => { if (globalThis.document?.hidden) cancel(); };
  const watch = () => {
    if (active.size !== 1) return;
    preference = media();
    preference?.addEventListener?.("change", preferenceChanged);
    globalThis.document?.addEventListener("visibilitychange", visibilityChanged);
    stopObserving = observe(() => {
      for (const [element, settle] of [...active])
        if (!element.isConnected) settle();
    });
  };
  const unwatch = () => {
    if (active.size) return;
    preference?.removeEventListener?.("change", preferenceChanged);
    globalThis.document?.removeEventListener("visibilitychange", visibilityChanged);
    preference = null;
    stopObserving?.();
    stopObserving = null;
  };
  const animate = (element, keyframes, {
    token = "--motion-base", fallback = 200, id = "qch-surface", easingToken = "--motion-ease-out", fill = "none",
    delay = 0, duration: requestedDuration, defer = false, retarget = true,
    onSettled = () => {},
  } = {}) => {
    // Capture the current painted values before releasing the old owner.
    // Rapid changes continue from that state instead of restarting at frame 0.
    if (retarget && active.has(element) && Array.isArray(keyframes)) {
      const painted = style(element);
      keyframes = keyframes.map((frame, index) => index ? frame : Object.fromEntries(
        Object.entries(frame).map(([key, value]) => [key, painted[key] || value]),
      ));
    }
    active.get(element)?.();
    if (!element?.isConnected || !element.animate || media()?.matches || globalThis.document?.hidden) {
      onSettled();
      return () => {};
    }
    const computed = style(element);
    const value = computed.getPropertyValue(token).trim();
    const parsed = Number.parseFloat(value);
    const duration = requestedDuration ?? (Number.isFinite(parsed)
      ? Math.max(0, parsed * (value.endsWith("ms") ? 1 : 1000)) : fallback);
    const animation = element.animate(keyframes, {
      duration, easing: computed.getPropertyValue(easingToken).trim() || "ease-out",
      delay, fill: delay && fill === "none" ? "backwards" : fill,
    });
    animation.id = id;
    let settled = false;
    let timer;
    let frame;
    const settle = () => {
      if (settled) return;
      settled = true;
      clearTimer(timer);
      if (frame != null) cancelAnimationFrame(frame);
      animation.onfinish = animation.oncancel = null;
      animation.cancel();
      active.delete(element);
      unwatch();
      onSettled();
    };
    active.set(element, settle);
    animation.onfinish = animation.oncancel = settle;
    // The safety deadline starts at playback readiness. A long render/bind
    // must not spend the animation's entire budget before its first paint.
    const deadline = () => { if (!settled) timer = setTimer(settle, duration + delay + 80); };
    const ready = () => animation.ready ? animation.ready.then(deadline, settle) : deadline();
    if (defer && animation.pause) {
      animation.pause();
      frame = requestAnimationFrame(() => { frame = null; animation.play(); ready(); });
    } else ready();
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
  if (element?.closest?.("[hidden],details:not([open]) > :not(summary)")) return () => {};
  return animateMotion(element, [
    { opacity: 0, translate: "0 14px", scale: ".985" },
    { opacity: 1, translate: "0 0", scale: "1" },
  ], { defer: true, ...options });
}
export function updateFeedback(element, message) {
  if (!element) return;
  const text = String(message || "");
  const changed = element.hidden || element.textContent !== text;
  element.textContent = text;
  element.hidden = !text;
  if (!text) cancelMotion(element);
  else if (changed) enterSurface(element, { token: "--motion-feedback", id: "qch-feedback" });
}
export function reducedMotion() {
  return Boolean(globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches);
}

// Callers commit close/dismissal first. This inert, non-announcing surface owns
// only its short visual tail; cancellation, navigation and lost events remove it.
export function retireSurface(element, { surface = element, from, onSettled = () => {} } = {}) {
  const computed = getComputedStyle(surface);
  const start = from || { opacity: computed.opacity, translate: computed.translate, scale: computed.scale };
  element.inert = true;
  element.setAttribute("aria-hidden", "true");
  element.dataset.motionExiting = "";
  cancelMotion(element);
  surface.getAnimations().forEach(animation => animation.cancel());
  const navigate = () => cancelMotion(surface);
  window.addEventListener("hashchange", navigate);
  const distance = computed.getPropertyValue("--motion-exit-distance").trim() || "4px";
  return animateMotion(surface, [start, { opacity: 0, translate: `0 ${distance}`, scale: ".97" }], {
    token: "--motion-exit", fallback: 220, id: "qch-exit", easingToken: "--motion-ease-in", fill: "forwards",
    onSettled: () => {
      window.removeEventListener("hashchange", navigate);
      element.remove();
      onSettled();
    },
  });
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
    // An initial skeleton must not consume its key: a slow first response
    // still deserves a reveal after the route entrance has completed.
    if (element.dataset.motionReady === "false") {
      if (!previous.has(name)) previous.set(name, null);
      cancelMotion(element);
      return;
    }
    if (!previous.has(name) || !animate) {
      previous.set(name, key);
      return;
    }
    if (previous.get(name) === key) return;
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
      animateMotion(element, [
        { opacity: .25, translate: "0 12px" }, { opacity: 1, translate: "0 0" },
      ], {
        id: "qch-selection", token: "--motion-feedback",
      });
    });
}
