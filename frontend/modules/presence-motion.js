import { animateMotion, cancelMotion, enterSurface, reducedMotion, retireSurface } from "./motion.js";
import { isolateMotionSurface } from "./motion-isolation.js";
import { freezePaint } from "./paint-snapshot.js";

const tails = new WeakMap();
const feedbackKeys = new WeakMap();
function canPresent(element) {
  if (!element?.isConnected || !element.animate || reducedMotion() || document.hidden) return false;
  if (element.closest("dialog:not([open])") || element.parentElement?.closest("[hidden],details:not([open])")) return false;
  // Parent entrance owns nested fields. Mixing its scaled coordinates with
  // layout coordinates would turn a field FLIP into horizontal overflow.
  for (let parent = element.parentElement; parent && parent !== document.body; parent = parent.parentElement) {
    if (parent.tagName === "DIALOG" && Number(getComputedStyle(parent).opacity) < .999) return false;
    if (parent.getAnimations().some(animation => animation.id.startsWith("qch-") &&
      animation.effect.getKeyframes().some(frame => frame.translate != null || frame.scale != null || frame.transform != null))) return false;
  }
  const main = element.closest(".workspace-main");
  return !main?.getAnimations().some(animation => animation.id === "qch-route" && !(animation.currentTime > 0));
}

function retirePaint(element, rect) {
  tails.get(element)?.stop();
  const painted = getComputedStyle(element);
  if (!rect.width || !rect.height || rect.bottom < 0 || rect.top > innerHeight || Number(painted.opacity) === 0) return;
  const clone = element.cloneNode(true);
  freezePaint(element, clone);
  clone.style.cssText = `position:fixed;left:${rect.left}px;top:${rect.top}px;width:${rect.width}px;height:${rect.height}px;margin:0;transform:none;translate:0 0;scale:1;opacity:${painted.opacity};animation:none;transition:none;pointer-events:none`;
  const frame = document.createElement("div");
  frame.className = document.body.className;
  const dialog = element.closest("dialog");
  if (dialog) {
    const skin = document.createElement("div");
    skin.className = dialog.className;
    skin.style.display = "contents";
    skin.append(clone); frame.append(skin);
  } else frame.append(clone);
  const host = isolateMotionSurface(frame);
  if (host) {
    const retired = { surface: clone };
    retired.stop = retireSurface(host, { surface: clone, from: { opacity: painted.opacity, translate: "0 0", scale: "1" },
      onSettled: () => { if (tails.get(element) === retired) tails.delete(element); },
    });
    tails.set(element, retired);
  }
}

// Conditional fields commit hidden/value/focus immediately. Only isolated
// outgoing paint and surviving sibling geometry participate in the transition.
export function setVisible(element, visible, { launcher = globalThis.document?.activeElement, animate = true, reflow = true } = {}) {
  if (!element || element.hidden === !visible) return false;
  const moving = animate && canPresent(element);
  const retired = visible ? tails.get(element) : null;
  const resumed = moving && retired?.surface.isConnected ? {
    rect: retired.surface.getBoundingClientRect(), opacity: getComputedStyle(retired.surface).opacity,
  } : null;
  const parent = element.parentElement;
  const siblings = moving && reflow ? [...(parent?.children || [])].filter(child => child !== element && !child.hidden)
    .slice(0, 16).map(child => [child, child.getBoundingClientRect()]) : [];
  if (!visible && moving) retirePaint(element, element.getBoundingClientRect());
  if (!visible && globalThis.document?.activeElement && element.contains?.(document.activeElement)) {
    if (!launcher || element.contains(launcher)) launcher = [...(parent?.querySelectorAll("button,input,select,textarea,a[href]") || [])]
      .find(control => !element.contains(control) && !control.disabled && !control.closest("[hidden], [inert]"));
    launcher?.focus?.({ preventScroll: true });
  }
  element.hidden = !visible;
  if (!moving) { if (visible) retired?.stop(); return true; }
  if (visible) {
    if (resumed) {
      const rect = element.getBoundingClientRect();
      animateMotion(element, [
        { opacity: resumed.opacity, transformOrigin: "0 0", translate: `${resumed.rect.left - rect.left}px ${resumed.rect.top - rect.top}px`, scale: `${resumed.rect.width / rect.width || 1} ${resumed.rect.height / rect.height || 1}` },
        { opacity: 1, transformOrigin: "0 0", translate: "0 0", scale: "1" },
      ], { id: "qch-reveal", token: "--motion-feedback" });
    } else enterSurface(element, { id: "qch-reveal", token: "--motion-feedback" });
    retired?.stop();
  }
  else cancelMotion(element);
  requestAnimationFrame(() => {
    if (!parent?.isConnected || parent.closest(".is-route-departing")) return;
    const survivors = siblings.filter(([child]) => child.isConnected && child.parentElement === parent && !child.hidden);
    survivors.forEach(([child]) => cancelMotion(child));
    const moves = survivors.map(([child, from]) => ({ child, from, to: child.getBoundingClientRect() }));
    moves.forEach(({ child, from, to }) => {
      const dx = from.left - to.left, dy = from.top - to.top;
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) return;
      animateMotion(child, [{ transform: `translate(${dx}px,${dy}px)` }, { transform: "none" }], {
        id: "qch-field-reflow", token: "--motion-feedback", retarget: false,
      });
    });
  });
  return true;
}

// A changed message receives one light transition. Identical polling/typing
// stays quiet, and clearing a message preserves its complete outgoing paint.
export function refreshFeedback(element) {
  if (!element) return;
  const key = element.textContent;
  if (feedbackKeys.get(element) === key) return;
  feedbackKeys.set(element, key);
  if (key && canPresent(element)) {
    const displaced = element.getAnimations().some(animation => animation.id.startsWith("qch-") &&
      animation.effect.getKeyframes().some(frame => frame.translate != null || frame.scale != null));
    animateMotion(element, displaced ? [
      { opacity: .55, translate: "0 0", scale: "1" }, { opacity: 1, translate: "0 0", scale: "1" },
    ] : [{ opacity: .55 }, { opacity: 1 }], { id: "qch-feedback", token: "--motion-feedback" });
  }
}

export function updateFeedback(element, message) {
  if (!element) return;
  const text = String(message || "");
  if (element.textContent === text && element.hidden === !text) { feedbackKeys.set(element, text); return; }
  if (!text) setVisible(element, false);
  const changed = element.hidden || element.textContent !== text;
  element.textContent = text;
  if (text) {
    const revealed = setVisible(element, true);
    if (revealed) feedbackKeys.set(element, text);
    else if (changed) refreshFeedback(element);
  } else { cancelMotion(element); feedbackKeys.set(element, ""); }
}

export function updateFeedbackHTML(element, markup) {
  if (!element) return;
  if (!markup) setVisible(element, false);
  if (element.innerHTML === markup && !element.hidden) return;
  element.innerHTML = markup;
  if (markup) {
    const revealed = setVisible(element, true);
    if (revealed) feedbackKeys.set(element, element.textContent);
    else refreshFeedback(element);
  } else { cancelMotion(element); feedbackKeys.set(element, ""); }
}

export function removePresented(element) {
  if (!element) return;
  setVisible(element, false);
  element.remove();
}

const feedbackSelector = 'button,[role="button"],[role="alert"],[role="status"],.alert,.empty,[data-code-status],[data-code-validation],[data-settings-state],[data-update-result],[data-preset-regenerate-status],.settings-hint,.validation-note';
const quietSelector = '[data-metric-poll],[data-metric-text],[data-code-position],[data-code-bytes],.core-log-status,.audit-live,.sync-state,.context-live';
let feedbackObserver;
// The explicit helpers own complete presence. This sparse observer covers
// remaining changed button labels and semantic messages from server markup.
// It never watches value/checked/selection, counters, geometry or clock ticks.
export function observeFeedback(root) {
  if (!root || !globalThis.MutationObserver) return;
  const seed = element => { if (!feedbackKeys.has(element)) feedbackKeys.set(element, element.textContent); };
  if (root.matches?.(feedbackSelector)) seed(root);
  root.querySelectorAll(feedbackSelector).forEach(seed);
  if (feedbackObserver) return;
  feedbackObserver = new MutationObserver(records => {
    const candidates = new Set();
    records.forEach(record => {
      const target = record.target.nodeType === 1 ? record.target : record.target.parentElement;
      const closest = target?.closest(feedbackSelector);
      if (closest) candidates.add(closest);
      record.addedNodes?.forEach(node => {
        if (node.nodeType !== 1) return;
        if (node.matches(feedbackSelector)) candidates.add(node);
        node.querySelectorAll(feedbackSelector).forEach(element => candidates.add(element));
      });
    });
    candidates.forEach(element => {
      if (!element.isConnected || element.closest(quietSelector) || element.closest('[hidden],[inert],.motion-exit-host')) return;
      for (let ancestor = element; ancestor && ancestor !== document.body; ancestor = ancestor.parentElement) {
        if (ancestor.getAnimations().some(animation => animation.id.startsWith("qch-") && animation.id !== "qch-feedback") ||
            ancestor.tagName === "DIALOG" && Number(getComputedStyle(ancestor).opacity) < 1) {
          feedbackKeys.set(element, element.textContent); return;
        }
      }
      refreshFeedback(element);
    });
  });
  feedbackObserver.observe(document.body, { childList: true, characterData: true, subtree: true });
}
