import { animateNodeCardDrop } from "./agent-card-drag.js";
import { cancelOwnMotion } from "./motion.js";
import { createCardMoveGhost, moveCardGhost } from "./card-move-preview.js";
import { cardMoveLayout, cardMoveTarget, cardMoveOrder } from "./card-move-layout.js";
import { createCardMoveScroll } from "./card-move-scroll.js";

const directions = { ArrowLeft: -1, ArrowUp: -1, ArrowRight: 1, ArrowDown: 1 };
const threshold = 6;

// Order persistence is synchronous, like the existing account-scoped browser
// preferences. Each gesture commits once before releasing the refresh gate.
export function bindCardMove({ grid, selector, gripSelector, cardInteractions, save, notify }) {
  const listeners = new AbortController();
  const cards = () => [...grid.querySelectorAll(selector)];
  const grips = () => [...grid.querySelectorAll(gripSelector)];
  const status = grid.parentElement.querySelector("[data-card-order-status]");
  let drag = null, landing = null, disposed = false, pointerClick = null;
  const say = (message, error = false) => {
    if (disposed) return;
    if (status) {
      status.setAttribute("aria-live", error && notify ? "off" : "polite");
      status.textContent = message;
      status.dataset.phase = error ? "error" : /移动中|已拾起|已离开/.test(message) ? "moving" : message.includes("已保存") ? "saved" : "idle";
    }
    if (error) notify?.(message, "error");
  };
  const paint = () => new Map(cards().map(card => [card, card.getBoundingClientRect()]));
  const stopMotion = () => cards().forEach(cancelOwnMotion);
  const scroll = createCardMoveScroll(grid, () => updatePointer());
  const restoreFocus = grip => { if (grip?.isConnected) grip.focus({ preventScroll: true }); };
  const clearPreview = previous => {
    previous?.card.classList.remove("dragging");
    previous?.card.classList.remove("card-move-outside");
    previous?.grip.setAttribute("aria-pressed", "false");
    cards().forEach(card => { card.style.order = ""; card.classList.remove("drop-target"); });
    grid.classList.remove("card-move-active");
    delete grid.dataset.cardMoveInput;
    document.body.classList.remove("card-moving", "node-card-dragging", "traffic-card-dragging");
    if (previous?.ghost) {
      cancelOwnMotion(previous.ghost);
      previous.ghost.closest(".card-move-layer")?.remove();
      previous.ghost.remove();
    }
  };
  const stopLanding = () => { const previous = landing; landing = null; previous?.(); };
  const begin = () => {
    // Take the new gate before releasing an interrupted landing. A queued
    // roster refresh cannot replace the grip between consecutive gestures.
    const release = cardInteractions.begin();
    stopLanding();
    return release;
  };
  const detachPointer = previous => {
    drag = null;
    scroll.stop();
    cards().forEach(card => card.classList.remove("drop-target"));
    if (previous.pointerId != null && pointerClick?.id === previous.pointerId) pointerClick.until = performance.now() + 800;
    if (previous.pointerId != null && previous.capture.hasPointerCapture(previous.pointerId)) previous.capture.releasePointerCapture(previous.pointerId);
  };
  const preview = (index, announce = true) => {
    const previous = drag;
    if (previous.drop === index && previous.started) return;
    cards().forEach(card => card.classList.remove("drop-target"));
    if (index == null) {
      previous.drop = null;
      previous.card.classList.add("card-move-outside");
      if (announce) say("已离开可移动区域 · 松开取消");
      return;
    }
    const from = paint();
    stopMotion();
    const next = cardMoveOrder(previous.original, previous.card, index);
    next.forEach((card, position) => { card.style.order = String(position); });
    previous.card.classList.add("drop-target");
    previous.drop = index;
    previous.card.classList.remove("card-move-outside");
    animateNodeCardDrop(next.filter(card => card !== previous.card), from, { resize: true });
    previous.layout = cardMoveLayout(grid, cards()).signature;
    if (announce) say(`移动中 · 第 ${index + 1} 位 / ${previous.original.length}`);
  };
  const activate = previous => {
    previous.started = true;
    restoreFocus(previous.grip);
    if (previous.pointerId != null) previous.ghost = createCardMoveGhost(previous.card, previous.rect);
    previous.card.classList.add("dragging");
    previous.grip.setAttribute("aria-pressed", "true");
    grid.classList.add("card-move-active");
    grid.dataset.cardMoveInput = previous.pointerId == null ? "keyboard" : "pointer";
    document.body.classList.add("card-moving", previous.card.matches(".node-card") ? "node-card-dragging" : "traffic-card-dragging");
    previous.drop = null;
    preview(previous.original.indexOf(previous.card));
    say(`已拾起第 ${previous.drop + 1} 项，共 ${previous.original.length} 项`);
  };
  function updatePointer() {
    if (!drag?.ghost) return;
    if (!grid.isConnected || !grid.contains(drag.card)) return cancel(false);
    moveCardGhost(drag);
    const layout = cardMoveLayout(grid, cards());
    const moved = !drag.lastPoint || drag.lastPoint.x !== drag.point.x || drag.lastPoint.y !== drag.point.y;
    if (!moved && layout.signature === drag.layout) return;
    drag.lastPoint = { ...drag.point };
    drag.layout = layout.signature;
    const changedLayout = drag.hitLayout && layout.signature !== drag.hitLayout;
    drag.hitLayout = layout.signature;
    preview(cardMoveTarget(layout, drag.point, changedLayout ? null : drag.drop));
    if (drag) drag.hitLayout = drag.layout;
  }
  const settle = (previous, from) => {
    // Hand the preview's painted bounds to the real card, as the original
    // traffic drop did. Only one card is painted during landing or return.
    if (previous.ghost) from.set(previous.card, previous.ghost.getBoundingClientRect());
    clearPreview(previous);
    let done = false;
    const cancelAnimation = animateNodeCardDrop(cards(), from, {
      resize: true,
      onSettled: () => { done = true; landing = null; previous.release(); },
    });
    if (!done) landing = cancelAnimation;
  };
  const commit = previous => {
    const next = cardMoveOrder(previous.original, previous.card, previous.drop);
    if (next.every((card, index) => card === previous.original[index])) {
      say("位置未改变");
      return;
    }
    try {
      if (save(next) !== true) throw new Error("order was not saved");
      // Move just this card, preserving DOM identities, drafts and disclosures.
      const target = next[next.indexOf(previous.card) + 1];
      if (target) target.before(previous.card); else grid.append(previous.card);
      say(`已保存 · 第 ${previous.drop + 1} 位 / ${next.length}`);
    } catch {
      say("顺序保存失败，已恢复原位。请检查浏览器存储后重试。", true);
    }
  };
  const finish = () => {
    const previous = drag;
    if (!previous) return;
    if (!previous.started) { detachPointer(previous); clearPreview(previous); previous.release(); return; }
    if (previous.drop == null) return cancel();
    const from = paint();
    detachPointer(previous);
    stopMotion();
    commit(previous);
    cards().forEach(card => { card.style.order = ""; });
    restoreFocus(previous.grip);
    settle(previous, from);
  };
  function cancel(animate = true) {
    const previous = drag;
    if (!previous) { stopLanding(); return; }
    const from = paint();
    detachPointer(previous);
    stopMotion();
    cards().forEach(card => { card.style.order = ""; });
    if (previous.started) say("已取消 · 顺序未改变");
    if (animate) restoreFocus(previous.grip);
    if (animate && previous.started && grid.isConnected) settle(previous, from);
    else { clearPreview(previous); previous.release(); }
  }
  const on = (target, type, handler, options = {}) => target.addEventListener(type, handler, { ...options, signal: listeners.signal });
  grips().forEach(grip => {
    const disabled = cards().length < 2;
    grip.setAttribute("aria-disabled", String(disabled));
    grip.tabIndex = disabled ? -1 : 0;
    on(grip, "click", event => { event.preventDefault(); event.stopPropagation(); });
    on(grip, "dragstart", event => event.preventDefault());
    on(grip, "keydown", event => {
      const direction = directions[event.key], pickup = ["Enter", " "].includes(event.key);
      if (!direction && !pickup) return;
      event.preventDefault(); event.stopPropagation();
      if (drag?.pointerId != null) return;
      if (drag && drag.grip !== grip) cancel(false);
      if (pickup && drag) { finish(); return; }
      if (cards().length < 2) return;
      if (!drag) {
        const original = cards(), card = grip.closest(selector), from = paint(), release = begin();
        stopMotion();
        drag = { card, grip, release, original, rect: from.get(card), started: false, drop: original.indexOf(card) };
        if (pickup) { activate(drag); return; }
      }
      const index = Math.max(0, Math.min(drag.original.length - 1, drag.drop + direction));
      const pickedUp = drag.started;
      preview(index);
      if (!pickedUp) { drag.started = true; finish(); }
    });
  });
  on(grid, "pointerdown", event => {
    if (event.button !== 0 || !event.isPrimary || drag || cards().length < 2) return;
    const card = event.target.closest(selector), handle = event.target.closest(gripSelector);
    if (!card || !grid.contains(card)) return;
    // Touch content remains scrollable. Mouse/pen can also grab non-control
    // surfaces; a click below threshold still follows the card's own link.
    const control = event.target.closest("button,input,select,textarea,summary,a,[role=button],[contenteditable=true],label");
    if (!handle && (event.pointerType === "touch" || control && control !== card)) return;
    const grip = card.querySelector(gripSelector), rect = card.getBoundingClientRect();
    if (!grip) return;
    event.preventDefault(); event.stopPropagation();
    const release = begin();
    stopMotion();
    const start = { x: event.clientX, y: event.clientY }, capture = handle || card;
    drag = { card, grip, capture, rect, release, start, point: start, pointerId: event.pointerId,
      original: cards(), grab: { x: start.x - rect.left, y: start.y - rect.top },
      started: false, drop: null, ghost: null };
    capture.setPointerCapture(event.pointerId);
  });
  on(grid, "pointermove", event => {
    if (!drag || drag.pointerId !== event.pointerId) return;
    drag.point = { x: event.clientX, y: event.clientY };
    if (!drag.started && Math.hypot(drag.point.x - drag.start.x, drag.point.y - drag.start.y) < threshold) return;
    event.preventDefault();
    if (!drag.started) {
      pointerClick = { id: event.pointerId, until: Infinity };
      activate(drag);
    }
    updatePointer();
    if (drag) scroll.move(drag.point);
  });
  on(grid, "pointerup", event => {
    if (drag?.pointerId !== event.pointerId) return;
    drag.point = { x: event.clientX, y: event.clientY };
    if (drag.started) updatePointer();
    finish();
  });
  on(grid, "dragstart", event => { if (drag) event.preventDefault(); });
  for (const type of ["pointercancel", "lostpointercapture"]) on(grid, type, event => {
    if (drag?.pointerId === event.pointerId) cancel();
  });
  // Releasing/lost capture can retarget the synthesized click to the card link
  // after it moves. Consume that click, and reset on the next genuine press.
  on(document, "pointerdown", () => { if (!drag) pointerClick = null; }, { capture: true });
  on(document, "click", event => {
    if (!pointerClick || !event.detail || performance.now() > pointerClick.until ||
        event.pointerId != null && event.pointerId !== pointerClick.id) return;
    pointerClick = null;
    event.preventDefault(); event.stopImmediatePropagation();
  }, { capture: true });
  on(document, "keydown", event => {
    if (event.key !== "Escape" || !drag) return;
    event.preventDefault(); event.stopPropagation(); cancel();
  }, { capture: true });
  on(window, "blur", () => cancel(false));
  on(document, "visibilitychange", () => { if (document.hidden) cancel(false); });
  const removed = new MutationObserver(() => { if (!grid.isConnected) cancel(false); });
  removed.observe(grid.ownerDocument.body, { childList: true, subtree: true });
  return () => {
    disposed = true;
    listeners.abort(); removed.disconnect(); scroll.stop();
    cancel(false); stopMotion();
  };
}
