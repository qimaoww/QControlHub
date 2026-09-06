// Shared, interruptible motion. Business actions never wait for page animations.
// Only disclosures animate layout; all other effects use opacity/transform.
const ease = "cubic-bezier(.2,.8,.2,1)";
const effects = new Map();
const dialogs = new WeakMap();
const boundDialogs = new WeakSet();
const disclosures = new WeakMap();
const busyControls = new WeakMap();
const panelChanges = new WeakMap();
const inertOwners = new WeakMap();
const pending = new Set();
let frame = null;
let installed = false;
let actionControl = null;

export function motionEnabled() {
  return typeof document !== "undefined" && !document.hidden &&
    !globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

export function motionOwnsInert(element) {
  return inertOwners.has(element);
}

function lockInert(element, owner) {
  if (!element) return;
  const previous = inertOwners.get(element);
  inertOwners.set(element, { owner, original: previous?.original ?? element.inert });
  element.inert = true;
}

function unlockInert(element, owner) {
  const state = inertOwners.get(element);
  if (!state || state.owner !== owner) return;
  element.inert = state.original;
  inertOwners.delete(element);
}

// Cancellation settles the old promise but must never run its final action.
export function animateSurface(element, frames, { duration = 220, delay = 0 } = {}) {
  effects.get(element)?.cancel();
  if (!element?.isConnected || !element.animate || !motionEnabled())
    return Promise.resolve(true);
  let resolve;
  const result = new Promise((done) => { resolve = done; });
  let animation;
  try {
    animation = element.animate(frames, { duration, delay, easing: ease, fill: "both" });
  } catch {
    // An unsupported visual effect must not prevent closing/saving the UI.
    return Promise.resolve(true);
  }
  let settled = false;
  let timer;
  const settle = (completed) => {
    if (settled) return;
    settled = true;
    clearTimeout(timer);
    if (effects.get(element)?.animation === animation) effects.delete(element);
    animation.cancel();
    resolve(completed);
  };
  effects.set(element, { animation, cancel: () => settle(false), finish: () => settle(true) });
  animation.finished.then(() => settle(true), () => settle(false));
  // Background tabs, detached nodes and interrupted browsers cannot strand UI.
  timer = setTimeout(() => settle(true), duration + delay + 100);
  return result;
}

export function cancelSurfaceMotion(element) {
  effects.get(element)?.cancel();
}

function currentFrame(element) {
  if (!element) return { opacity: 1, transform: "none" };
  const style = getComputedStyle(element);
  return { opacity: style.opacity, transform: style.transform };
}

const entranceSelector = [
  ".workspace-main", ".context-sidebar", ".login-card", ".qch-swap-panel",
  ".node-card", ".service-card", ".traffic-policy-card", ".task-event",
  ".core-log-row", ".client-access-node-card", ".client-profile-row",
  ".access-control-card", ".substore-agent-card", ".template-card",
  ".settings-version-card", ".node-batch-bar", ".batch-results",
  ".alert", ".task-feedback", ".core-log-source-notice", ".empty",
  ".metric-trend-panel", ".batch-result-row", ".enrollment-history-empty",
].join(",");

function outermost(elements) {
  const set = new Set(elements);
  return [...set].filter((item) => {
    for (let parent = item.parentElement; parent; parent = parent.parentElement)
      if (set.has(parent)) return false;
    return true;
  });
}

export function queueEntrance(element) {
  if (!element?.matches || !motionEnabled()) return;
  pending.add(element);
  if (frame != null) return;
  frame = requestAnimationFrame(() => {
    frame = null;
    const candidates = [...pending].filter((item) => item.isConnected);
    pending.clear();
    // Choose one visual level, never animate a card and its parent together.
    const roots = outermost(candidates);
    const visible = roots.slice(0, 48).filter((item) => {
      if (item.closest('[hidden],dialog:not([open]),[data-motion-closing]')) return false;
      // A lifecycle animation owns its final action; discovery must not cancel
      // a disclosure close or a tab exit just because a child was inserted.
      if (disclosures.has(item) || motionOwnsInert(item)) return false;
      for (let parent = item.parentElement; parent; parent = parent.parentElement)
        if (effects.has(parent)) return false;
      const rect = item.getBoundingClientRect();
      return rect.width > 0 && rect.height > 0 && rect.bottom > 0 && rect.right > 0 &&
        rect.top < innerHeight && rect.left < innerWidth;
    }).slice(0, 12);
    visible.forEach((item) => {
      const shell = item.matches(".workspace-main,.context-sidebar");
      const start = effects.has(item) ? currentFrame(item) :
        { opacity: shell ? .45 : 0, transform: shell ? "none" : "translateY(5px)" };
      animateSurface(item, [
        start,
        { opacity: 1, transform: "none" },
      ], { duration: shell ? 240 : 200 });
    });
  });
}

export async function removeWithMotion(element) {
  if (!element || element.dataset.motionClosing) return;
  element.dataset.motionClosing = "true";
  const owner = {};
  lockInert(element, owner);
  const completed = await animateSurface(element, [currentFrame(element),
    { opacity: 0, transform: "translateY(-4px)" }], { duration: 140 });
  if (completed) element.remove();
  unlockInert(element, owner);
}

function dialogSurface(dialog) {
  return dialog.matches(".modal-backdrop")
    ? dialog.querySelector(".deploy-command-modal") : dialog;
}

export function openDialog(dialog) {
  if (!dialog?.isConnected) return;
  const previous = dialogs.get(dialog);
  if (dialog.open && !previous?.closing) return;
  const surface = dialogSurface(dialog);
  const start = dialog.open ? currentFrame(surface) :
    { opacity: 0, transform: "translateY(10px) scale(.985)" };
  const state = { closing: false };
  dialogs.set(dialog, state);
  delete dialog.dataset.motionClosing;
  if (previous) unlockInert(dialog, previous);
  dialog.setAttribute("closedby", "closerequest");
  if (!dialog.open) dialog.showModal();
  if (!boundDialogs.has(dialog)) {
    boundDialogs.add(dialog);
    dialog.addEventListener("cancel", (event) => {
      // Confirmation dialogs own their resolver and cancel handler.
      if (dialog.matches("[data-confirm-dialog]")) return;
      event.preventDefault();
      closeDialog(dialog);
    });
  }
  animateSurface(surface, [start, { opacity: 1, transform: "none" }], { duration: 240 });
}

export function closeDialog(dialog, returnValue) {
  if (!dialog?.open) return Promise.resolve(true);
  const previous = dialogs.get(dialog);
  if (previous?.closing) return previous.promise;
  const state = { closing: true };
  dialogs.set(dialog, state);
  dialog.dataset.motionClosing = "true";
  // Retain the native top layer/focus trap through the exit. No click-through.
  lockInert(dialog, state);
  const surface = dialogSurface(dialog);
  state.promise = animateSurface(surface, [currentFrame(surface),
    { opacity: 0, transform: "translateY(7px) scale(.99)" }], { duration: 160 })
    .then((completed) => {
      if (!completed || dialogs.get(dialog) !== state) return false;
      dialog.close(returnValue);
      unlockInert(dialog, state);
      delete dialog.dataset.motionClosing;
      dialogs.delete(dialog);
      return true;
    });
  return state.promise;
}

export function dismissDialogsImmediately() {
  // Session expiration is a security boundary: never retain command secrets in
  // an exit snapshot or leave a body-level modal above the login screen.
  document.querySelectorAll("dialog").forEach((dialog) => {
    dialog.dataset.motionDiscarded = "true";
    cancelSurfaceMotion(dialogSurface(dialog));
    unlockInert(dialog, dialogs.get(dialog));
    dialogs.delete(dialog);
    if (dialog.open) dialog.close();
    if (dialog.matches(".modal-backdrop")) dialog.remove();
  });
}

export function setDisclosureOpen(details, open) {
  if (!details) return Promise.resolve(false);
  const previous = disclosures.get(details);
  if ((previous?.open ?? details.open) === open) return previous?.promise || Promise.resolve(true);
  const summary = details.querySelector(":scope > summary");
  const children = [...details.children].filter((item) => item !== summary);
  const floating = isFloatingDisclosure(details);
  const surface = floating ? children[0] : details;
  if (!summary || !details.isConnected || !motionEnabled()) {
    cancelSurfaceMotion(surface);
    previous?.children.forEach((item) => unlockInert(item, previous));
    disclosures.delete(details);
    details.open = open;
    return Promise.resolve(true);
  }
  const start = details.getBoundingClientRect().height;
  const visualStart = previous ? currentFrame(surface) : null;
  cancelSurfaceMotion(surface);
  details.open = true;
  const style = getComputedStyle(details);
  const padding = ["paddingTop", "paddingBottom", "borderTopWidth", "borderBottomWidth"]
    .reduce((total, key) => total + (parseFloat(style[key]) || 0), 0);
  const expanded = details.getBoundingClientRect().height;
  const collapsed = summary.getBoundingClientRect().height + padding;
  const state = { open, children };
  children.forEach((item) => {
    if (open) unlockInert(item, previous);
    else lockInert(item, state);
  });
  if (!open && children.some((item) => item.contains(document.activeElement)))
    summary.focus({ preventScroll: true });
  disclosures.set(details, state);
  // Popover menus are out of flow: animate the sheet, not their tiny summary.
  state.promise = animateSurface(surface, floating
    ? [visualStart || (open ? { opacity: 0, transform: "translateY(6px)" } : currentFrame(surface)),
      { opacity: open ? 1 : 0, transform: open ? "none" : "translateY(6px)" }]
    : [{ height: `${start}px`, overflow: "clip" },
      { height: `${open ? expanded : collapsed}px`, overflow: "clip" }],
  { duration: open ? 220 : 170 }).then((completed) => {
    if (!completed || disclosures.get(details) !== state) return false;
    details.open = open;
    children.forEach((item) => unlockInert(item, state));
    disclosures.delete(details);
    return true;
  });
  return state.promise;
}

function isFloatingDisclosure(details) {
  if (details.matches(".mobile-account-menu,.dashboard-month-picker,.client-access-search-menu")) return true;
  const content = [...details.children].find((item) => item.tagName !== "SUMMARY");
  return content && ["absolute", "fixed"].includes(getComputedStyle(content).position);
}

export function switchPanels(panels, selected) {
  const group = panels[0]?.parentElement;
  if (!group || !selected) return;
  const previous = panelChanges.get(group);
  if (!previous && !selected.hidden && panels.every((item) => item === selected || item.hidden)) {
    panels.forEach((item) => item.setAttribute("aria-hidden", String(item !== selected)));
    return;
  }
  const outgoing = panels.find((item) => !item.hidden && item !== selected);
  const visualStart = outgoing ? currentFrame(outgoing) : null;
  previous?.panels.forEach(cancelSurfaceMotion);
  previous?.panels.forEach((item) => unlockInert(item, previous));
  const state = { panels };
  panelChanges.set(group, state);
  const current = panels.filter((item) => !item.hidden && item !== selected);
  const commit = () => {
    if (panelChanges.get(group) !== state) return;
    panels.forEach((item) => {
      item.hidden = item !== selected;
      item.setAttribute("aria-hidden", String(item !== selected));
    });
    panels.forEach((item) => unlockInert(item, state));
    panelChanges.delete(group);
    queueEntrance(selected);
  };
  // Initial binding must not flash all panels or delay setting form visibility.
  if (!selected.hidden || current.length !== 1 || !motionEnabled()) return commit();
  lockInert(current[0], state);
  animateSurface(current[0], [visualStart || currentFrame(current[0]),
    { opacity: 0, transform: "translateY(-3px)" }], { duration: 90 })
    .then((completed) => { if (completed) commit(); });
}

let themeTimer;
export function transitionTheme(change) {
  clearTimeout(themeTimer);
  if (motionEnabled()) document.documentElement.dataset.themeTransition = "true";
  change();
  themeTimer = setTimeout(() => {
    delete document.documentElement.dataset.themeTransition;
  }, 240);
}

export function beginBusy(button) {
  button = actionControl?.isConnected ? actionControl : button;
  if (!button?.matches?.("button,input[type=submit]")) return () => {};
  let state = busyControls.get(button);
  if (!state) {
    state = { count: 0, previous: button.getAttribute("aria-busy") };
    busyControls.set(button, state);
  }
  state.count += 1;
  button.setAttribute("aria-busy", "true");
  let released = false;
  return () => {
    if (released) return;
    released = true;
    if (--state.count > 0) return;
    if (state.previous == null) button.removeAttribute("aria-busy");
    else button.setAttribute("aria-busy", state.previous);
    busyControls.delete(button);
  };
}

// Coalesce high-frequency pointer events without retaining stale drag state.
export function frameLatest(callback) {
  let id = null;
  let latest;
  const flush = () => {
    if (id == null) return;
    cancelAnimationFrame(id);
    id = null;
    callback(latest);
  };
  const schedule = (value) => {
    latest = value;
    if (id == null) id = requestAnimationFrame(flush);
  };
  schedule.flush = flush;
  schedule.cancel = () => {
    if (id != null) cancelAnimationFrame(id);
    id = null;
    latest = null;
  };
  return schedule;
}

export function installMotion() {
  if (installed || typeof MutationObserver === "undefined") return;
  installed = true;
  const rememberAction = (event) => {
    const control = event.submitter || event.target.closest?.("button");
    actionControl = control || null;
    queueMicrotask(() => { if (actionControl === control) actionControl = null; });
  };
  document.addEventListener("click", rememberAction, true);
  document.addEventListener("submit", rememberAction, true);
  const seen = new WeakSet();
  const discover = (root) => {
    if (root.nodeType !== 1 || root.closest(".node-card-ghost,.traffic-card-ghost")) return;
    const items = [root, ...root.querySelectorAll(entranceSelector)];
    items.forEach((item) => {
      if (seen.has(item)) return;
      seen.add(item);
      if (item.matches(entranceSelector)) queueEntrance(item);
    });
  };
  new MutationObserver((records) => {
    const added = new Set();
    for (const record of records) {
      if (record.type === "childList") {
        record.addedNodes.forEach((node) => { if (node.nodeType === 1) added.add(node); });
      } else if (record.attributeName === "hidden" && !record.target.hidden) {
        queueEntrance(record.target);
      } else if (record.attributeName === "data-live-config-phase") {
        queueEntrance(record.target);
      }
    }
    outermost(added).forEach(discover);
    // Clear detached effects immediately, not at the end of an old timer.
    effects.forEach((effect, element) => { if (!element.isConnected) effect.cancel(); });
  }).observe(document.body, {
    subtree: true, childList: true, attributes: true,
    attributeFilter: ["hidden", "data-live-config-phase"],
  });
  document.addEventListener("click", (event) => {
    const menuLink = event.target.closest?.(".mobile-account-menu a[href]");
    if (menuLink && !event.defaultPrevented) setDisclosureOpen(menuLink.closest("details"), false);
    const summary = event.target.closest?.("summary");
    if (!summary || event.defaultPrevented || event.button !== 0 ||
      event.target.closest("a,button,input,select,textarea")) return;
    const details = summary.parentElement;
    if (details?.tagName !== "DETAILS") return;
    event.preventDefault();
    setDisclosureOpen(details, !(disclosures.get(details)?.open ?? details.open));
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || event.defaultPrevented || document.querySelector("dialog[open]")) return;
    const menus = [...document.querySelectorAll("details[open]")].filter(isFloatingDisclosure);
    const menu = menus.find((item) => item.contains(document.activeElement)) || menus.at(-1);
    if (menu) {
      event.preventDefault();
      setDisclosureOpen(menu, false);
    }
  });
  const finish = () => {
    effects.forEach((effect) => effect.finish());
    pending.clear();
    if (frame != null) cancelAnimationFrame(frame);
    frame = null;
  };
  document.addEventListener("visibilitychange", () => { if (document.hidden) finish(); });
  globalThis.matchMedia?.("(prefers-reduced-motion: reduce)")
    ?.addEventListener?.("change", (event) => { if (event.matches) finish(); });
}
