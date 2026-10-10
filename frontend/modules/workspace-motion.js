import { animateMotion, cancelMotion, reducedMotion, retireSurface } from "./motion.js";
import { isolateMotionSurface } from "./motion-isolation.js";

const entrances = new WeakMap();
const layerSelector = ".node-card,.traffic-policy-card,.workspace-panel,.service-card,.ops-stat,.dashboard-stat,.task-event-card,.client-access-node-card,.access-control-card,.bbr-card,.substore-agent-card,.settings-version-card,.template-card,.user-account-card,.section-head,.config-command-bar";

// A fast response must not cut off outgoing paint. Snapshot only a changing
// workspace, before the body skin changes, and retire it outside live queries.
export function captureWorkspaceExit(main) {
  if (!main?.isConnected || reducedMotion() || document.hidden) return null;
  const rect = main.getBoundingClientRect(), style = getComputedStyle(main);
  const clone = main.cloneNode(true);
  const originals = [main, ...main.querySelectorAll("*")];
  const copies = [clone, ...clone.querySelectorAll("*")];
  // Preserve interrupted entrances/FLIP in the outgoing paint, without copying
  // native animation owners or any component's listeners/business lifecycle.
  main.getAnimations({ subtree: true }).forEach(animation => {
    const target = animation.effect?.target;
    const index = originals.indexOf(target);
    if (index < 1) return;
    const painted = getComputedStyle(target), copy = copies[index];
    for (const property of ["opacity", "translate", "scale", "transform"]) copy.style[property] = painted[property];
    copy.style.animation = copy.style.transition = "none";
  });
  clone.classList.remove("is-route-pending", "is-route-departing");
  clone.querySelectorAll("dialog,iframe,script,video,audio,object,embed").forEach(element => element.remove());
  clone.style.cssText = `position:fixed;left:${rect.left}px;top:${rect.top}px;width:${rect.width}px;height:${rect.height}px;margin:0;opacity:${style.opacity};transition:none;pointer-events:none`;
  const frame = document.createElement("div");
  frame.className = document.body.className;
  frame.append(clone);
  return { frame, clone, scrollTop: main.scrollTop, scrollLeft: main.scrollLeft };
}

export function finishWorkspaceExit(snapshot) {
  if (!snapshot) return;
  const { frame, clone, scrollTop, scrollLeft } = snapshot;
  const host = isolateMotionSurface(frame);
  if (!host) return;
  clone.scrollTop = scrollTop;
  clone.scrollLeft = scrollLeft;
  retireSurface(host, { surface: clone, from: { opacity: clone.style.opacity, translate: "0 0", scale: "1" } });
}

// The canvas fades once. Visible sibling surfaces move in a bounded sequence;
// a nested panel never translates on top of its translating parent.
export function enterWorkspace(main, context) {
  if (!main || reducedMotion()) return;
  cancelMotion(main);
  const entrance = {};
  entrances.set(main, entrance);
  animateMotion(main, [{ opacity: 0 }, { opacity: 1 }], {
    id: "qch-route", token: "--motion-slow", defer: true,
    onSettled: () => { if (entrances.get(main) === entrance) entrances.delete(main); },
  });
  if (context?.getClientRects().length) animateMotion(context, [
    { opacity: 0, translate: "-14px 0" }, { opacity: 1, translate: "0 0" },
  ], { id: "qch-context", token: "--motion-base", defer: true });
  requestAnimationFrame(() => {
    if (!main.isConnected || entrances.get(main) !== entrance || main.classList.contains("is-route-departing")) return;
    const bounds = main.getBoundingClientRect();
    let layers = [...main.querySelectorAll(layerSelector)].filter(element => {
      if (element.parentElement.closest(layerSelector)) return false;
      if (element.closest("[hidden],details:not([open]),dialog:not([open])")) return false;
      const rect = element.getBoundingClientRect();
      return rect.width && rect.height && rect.bottom > Math.max(0, bounds.top) && rect.top < innerHeight;
    });
    if (!layers.length) layers = [...main.children].filter(element => !element.hidden && element.getClientRects().length);
    const stagger = Number.parseFloat(getComputedStyle(main).getPropertyValue("--motion-stagger")) || 45;
    layers.slice(0, 12).forEach((element, index) => animateMotion(element, [
      { translate: "0 28px", scale: ".975" }, { translate: "0 0", scale: "1" },
    ], { id: "qch-route-layer", token: "--motion-slow", delay: Math.min(index, 5) * stagger }));
  });
}

// Mounted navigation keeps one highlight owner. Native CSS width transitions
// can reverse while the compositor carries the highlight to the next route.
export function syncDockMotion(dock) {
  const marker = dock?.querySelector(".dock-active-indicator");
  const selected = dock?.querySelector('.dock-nav [aria-current="page"],.dock-tools [aria-current="page"]');
  if (!marker) return;
  if (!selected?.getClientRects().length) { marker.hidden = true; return; }
  const bounds = selected.getBoundingClientRect(), root = dock.getBoundingClientRect();
  const next = `0 ${bounds.top - root.top}px`;
  const previous = marker.style.translate;
  const painted = previous ? getComputedStyle(marker).translate : next;
  marker.hidden = false;
  marker.style.translate = next;
  marker.style.height = `${bounds.height}px`;
  if (previous && previous !== next) animateMotion(marker, [
    { translate: painted }, { translate: next },
  ], { id: "qch-navigation", token: "--motion-base" });
}

export function departWorkspace(main) {
  const painted = getComputedStyle(main);
  const from = { opacity: painted.opacity, translate: painted.translate };
  main.classList.add("is-route-departing");
  animateMotion(main, [from, { opacity: .32, translate: "0 -10px" }], {
    id: "qch-route-exit", token: "--motion-exit",
  });
}

// Reversing navigation before its request completes keeps the original page.
// Restore from its current paint instead of letting the old exit finish over
// a new CSS entrance, then abruptly uncover a fully opaque workspace.
export function restoreWorkspace(main) {
  if (!main?.classList.contains("is-route-departing")) return;
  const painted = getComputedStyle(main);
  const from = { opacity: painted.opacity, translate: painted.translate };
  main.classList.remove("is-route-departing");
  animateMotion(main, [from, { opacity: 1, translate: "0 0" }], {
    id: "qch-route-resume", token: "--motion-base",
  });
}

const panelOwnership = new WeakMap();
export function setPanelVisible(panel, visible, launcher) {
  if (panel.hidden === !visible) return;
  panel.dataset.motionPanel = "";
  if (visible) {
    const previous = panelOwnership.get(panel);
    panel.inert = previous?.inert || false;
    if (previous?.aria == null) panel.removeAttribute("aria-hidden");
    else panel.setAttribute("aria-hidden", previous.aria);
    panelOwnership.delete(panel);
  } else {
    panelOwnership.set(panel, { inert: panel.inert, aria: panel.getAttribute("aria-hidden") });
    if (panel.contains(document.activeElement)) launcher?.focus({ preventScroll: true });
    panel.inert = true;
    panel.setAttribute("aria-hidden", "true");
  }
  panel.hidden = !visible;
}
