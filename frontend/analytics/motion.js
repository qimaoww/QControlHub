// Analytics has its own small, quiet motion vocabulary. Layout, visibility,
// native modal state, focus and business commits always happen synchronously.
export function createAnalyticsMotion(scope) {
  const active = new Map(), tails = new Set(), messages = new WeakMap(), seenMessages = new WeakMap();
  const invalid = new Map(); let validationID = 0;
  const ease = "cubic-bezier(.2,.8,.2,1)", exitEase = "cubic-bezier(.4,0,1,1)";
  let tooltip, tooltipOwner, tipTimer;
  function stop(element) { active.get(element)?.(); }
  function play(element, frames, { duration = 160, name = "component", easing = ease, settled = () => {} } = {}) {
    if (!element?.isConnected || !scope.enabled() || !element.animate) { settled(); return () => {}; }
    // Reverse from the currently painted values rather than jumping to frame 0.
    if (active.has(element)) {
      const painted = getComputedStyle(element);
      frames = frames.map((frame, index) => index ? frame : Object.fromEntries(Object.entries(frame).map(([key, value]) => [key, painted[key] || value])));
    }
    stop(element);
    let animation;
    try { animation = element.animate(frames, { duration, easing }); }
    catch { settled(); return () => {}; }
    animation.id = `analytics-${name}`;
    let done = false, timer;
    const finish = () => {
      if (done) return;
      done = true; scope.clearTimer(timer);
      animation.onfinish = animation.oncancel = null;
      try { animation.cancel(); } catch { /* Native target may already be gone. */ }
      active.delete(element); settled();
    };
    active.set(element, finish);
    animation.onfinish = animation.oncancel = finish;
    timer = scope.timer(finish, duration + 120);
    return finish;
  }
  function cancel() {
    [...active.values()].forEach(finish => finish());
    tails.forEach(tail => tail.remove()); tails.clear();
    hideTip();
  }
  const movingAncestor = element => {
    for (let parent = element.parentElement; parent; parent = parent.parentElement)
      if (active.has(parent)) return true;
    return false;
  };
  const reveal = (element, name = "component") => {
    if (!element?.isConnected || element.closest('[hidden],dialog:not([open])') || movingAncestor(element)) return;
    play(element, [{ opacity: .25, translate: "0 4px" }, { opacity: 1, translate: "0 0" }], { name, duration: name === "page" ? 180 : 140 });
  };
  function clearTails() { tails.forEach(tail => { stop(tail); tail.remove(); }); tails.clear(); }
  function retire(element, { modal = false } = {}) {
    if (!scope.enabled() || !element.isConnected) return;
    const rect = element.getBoundingClientRect();
    if (!rect.width || !rect.height || rect.bottom < 0 || rect.top > innerHeight) return;
    let clone = element.cloneNode(true);
    if (element.tagName === "DIALOG") {
      const surface = document.createElement("div"); surface.className = clone.className;
      surface.append(...clone.childNodes); clone = surface;
    }
    clone.querySelectorAll('script,iframe,object,embed,[autofocus]').forEach(item => item.remove());
    clone.removeAttribute("id"); clone.querySelectorAll("[id]").forEach(item => item.removeAttribute("id"));
    clone.inert = true; clone.setAttribute("aria-hidden", "true");
    clone.removeAttribute("popover");
    Object.assign(clone.style, { display: "block", position: "fixed", inset: "auto", left: `${rect.left}px`, top: `${rect.top}px`, width: `${rect.width}px`, height: `${rect.height}px`, margin: "0", maxHeight: "none", pointerEvents: "none", transform: "none" });
    const host = document.createElement("div");
    host.className = `a-motion-tail${modal ? " a-modal-tail" : ""}`;
    host.setAttribute("popover", "manual"); host.setAttribute("aria-hidden", "true"); host.inert = true;
    host.append(clone); document.body.append(host); tails.add(host);
    try { host.showPopover(); } catch { host.remove(); tails.delete(host); return; }
    play(host, [{ opacity: 1 }, { opacity: 0 }], { duration: 110, name: "exit", easing: exitEase,
      settled() { host.remove(); tails.delete(host); } });
  }
  function visible(element, show, launcher = document.activeElement) {
    if (!element || element.hidden === !show) return;
    const siblings = scope.enabled() && element.isConnected ? [...(element.parentElement?.children || [])]
      .filter(child => child !== element && !child.hidden).slice(0, 8).map(child => [child, child.getBoundingClientRect()]) : [];
    clearTails();
    if (!show) {
      retire(element);
      if (element.contains(document.activeElement)) {
        if (!launcher || element.contains(launcher)) launcher = [...(element.parentElement?.querySelectorAll('button,input,select,textarea,a[href]') || [])]
          .find(control => !element.contains(control) && !control.disabled && !control.closest('[hidden],[inert]'));
        launcher?.focus?.({ preventScroll: true });
      }
    }
    stop(element); element.hidden = !show;
    if (show) reveal(element, "field");
    for (const [child, before] of siblings) {
      const after = child.getBoundingClientRect();
      const y = before.top - after.top;
      if (Math.abs(y) > .5 && !movingAncestor(child))
        play(child, [{ translate: `0 ${y}px` }, { translate: "0 0" }], { name: "field-layout", duration: 150 });
    }
  }
  function feedback(element) {
    const text = element.textContent.trim(), previous = messages.get(element);
    if (previous === text) return;
    messages.set(element, text);
    const region = element.closest('dialog,.a-main') || document.body;
    let seen = seenMessages.get(region);
    if (!seen) { seen = new Set(); seenMessages.set(region, seen); }
    const key = `${element.className}|${text}`;
    if (!text) { seen.delete(`${element.className}|${previous}`); stop(element); return; }
    if (previous === undefined && seen.has(key)) return;
    seen.add(key);
    if (!movingAncestor(element)) play(element, [{ opacity: .55 }, { opacity: 1 }], { name: "feedback", duration: 130 });
  }
  function hideTip() {
    scope.clearTimer(tipTimer);
    if (tooltipOwner) {
      tooltipOwner.title = tooltipOwner.dataset.aMotionTitle || "";
      delete tooltipOwner.dataset.aMotionTitle;
      if (tooltipOwner.dataset.aMotionDescription === "true") tooltipOwner.removeAttribute("aria-description");
      delete tooltipOwner.dataset.aMotionDescription;
    }
    if (tooltip) { stop(tooltip); tooltip.remove(); }
    tooltip = tooltipOwner = null;
  }
  function showTip(owner) {
    hideTip();
    const text = owner?.getAttribute("title");
    if (!text || !owner.isConnected || owner.closest('[hidden],[inert]')) return;
    const modal = document.querySelector('dialog:modal');
    if (modal && !modal.contains(owner)) return;
    tooltipOwner = owner; owner.dataset.aMotionTitle = text; owner.removeAttribute("title");
    if (!owner.hasAttribute("aria-description")) { owner.setAttribute("aria-description", text); owner.dataset.aMotionDescription = "true"; }
    tooltip = document.createElement("div"); tooltip.className = "a-tooltip";
    tooltip.setAttribute("role", "tooltip"); tooltip.setAttribute("popover", "manual"); tooltip.textContent = text;
    document.body.append(tooltip);
    try { tooltip.showPopover(); } catch { hideTip(); return; }
    const rect = owner.getBoundingClientRect(), box = tooltip.getBoundingClientRect();
    tooltip.style.left = `${Math.max(8, Math.min(innerWidth - box.width - 8, rect.left + (rect.width - box.width) / 2))}px`;
    tooltip.style.top = `${Math.max(8, rect.top > box.height + 12 ? rect.top - box.height - 6 : rect.bottom + 6)}px`;
    reveal(tooltip, "tooltip");
  }
  scope.listen(document, "beforetoggle", event => {
    const element = event.target;
    if (!element.matches?.('dialog.a-dialog')) return;
    hideTip();
    if (event.newState === "open") {
      clearTails();
      seenMessages.delete(element);
      scope.frame(() => {
        if (!element.open) return;
        const drawer = element.classList.contains("a-drawer");
        play(element, drawer ? [{ opacity: .4, translate: "-12px 0" }, { opacity: 1, translate: "0 0" }] :
          [{ opacity: .2, translate: "0 6px", scale: ".995" }, { opacity: 1, translate: "0 0", scale: "1" }],
        { name: drawer ? "menu" : "dialog", duration: drawer ? 170 : 180 });
      });
    } else { retire(element, { modal: true }); stop(element); }
  }, { capture: true });
  scope.listen(document, "pointerover", event => {
    if (event.pointerType === "touch") return;
    const owner = event.target.closest?.('[title]');
    if (owner) { scope.clearTimer(tipTimer); tipTimer = scope.timer(() => showTip(owner), 240); }
  });
  scope.listen(document, "focusin", event => {
    const owner = event.target.closest?.('[title]');
    if (owner) { scope.clearTimer(tipTimer); tipTimer = scope.timer(() => showTip(owner), 100); }
  });
  for (const event of ["pointerout", "focusout", "pointerdown", "keydown", "scroll"])
    scope.listen(document, event, hideTip, { capture: true });
  scope.listen(window, "hashchange", () => { clearTails(); hideTip(); });
  scope.listen(window, "resize", () => { clearTails(); hideTip(); });
  function clearValidation(control) {
    const error = invalid.get(control);
    if (!error) return;
    error.label.remove();
    for (const [name, value] of error.attributes) {
      if (value == null) control.removeAttribute(name); else control.setAttribute(name, value);
    }
    invalid.delete(control);
  }
  scope.listen(document, "invalid", event => {
    const control = event.target;
    if (!control.matches?.('input,select,textarea') || !control.validationMessage) return;
    event.preventDefault();
    let error = invalid.get(control);
    if (!error) {
      const label = document.createElement('p');
      label.className = 'a-notice error a-validation'; label.setAttribute('role','alert'); label.id = `a-validation-${++validationID}`;
      error = { label, attributes: ['aria-invalid','aria-errormessage'].map(name => [name,control.getAttribute(name)]) };
      const field = control.closest('.a-field');
      if (field) field.append(label); else (control.closest('.a-check') || control).after(label);
      invalid.set(control,error);
      control.setAttribute('aria-invalid','true'); control.setAttribute('aria-errormessage',label.id);
    }
    error.label.textContent = control.validationMessage;
    const first = [...(control.form?.elements || [control])].find(item => item.willValidate && !item.validity.valid);
    if (first === control) control.focus();
  }, { capture: true });
  for (const event of ['input','change']) scope.listen(document,event, event => {
    const control = event.target, error = invalid.get(control);
    if (!error) return;
    if (control.validity.valid) clearValidation(control); else error.label.textContent = control.validationMessage;
  });
  const observer = new MutationObserver(records => {
    const candidates = new Set();
    for (const record of records) {
      const target = record.target.nodeType === 1 ? record.target : record.target.parentElement;
      const message = target?.closest('.a-notice,.a-empty');
      if (message) candidates.add(message);
      for (const node of record.addedNodes || []) if (node.nodeType === 1 && !node.closest('.a-motion-tail')) {
        if (node.matches('.a-notice,.a-empty')) candidates.add(node);
        node.querySelectorAll('.a-notice,.a-empty').forEach(item => candidates.add(item));
      }
    }
    for (const element of [...active.keys()]) if (!element.isConnected) stop(element);
    for (const control of invalid.keys()) if (!control.isConnected) clearValidation(control);
    candidates.forEach(element => { if (element.isConnected && !element.closest('.a-motion-tail,[hidden],dialog:not([open])')) feedback(element); });
  });
  observer.observe(document.body, { subtree: true, childList: true, characterData: true });
  scope.onSuspend(cancel);
  scope.own(() => { observer.disconnect(); cancel(); [...invalid.keys()].forEach(clearValidation); });
  return {
    page: element => { clearTails(); seenMessages.delete(element.closest(".a-main") || element); reveal(element, "page"); }, visible,
    list: element => { if (element.isConnected && !movingAncestor(element)) play(element, [{ opacity: .65 }, { opacity: 1 }], { name: "list", duration: 130 }); },
    cancel,
  };
}
