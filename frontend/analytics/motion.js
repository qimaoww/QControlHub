// Analytics has its own small, quiet motion vocabulary. Layout, visibility,
// native modal state, focus and business commits always happen synchronously.
export function createAnalyticsMotion(scope) {
  const active = new Map(), tails = new Set(), messages = new WeakMap(), seenMessages = new WeakMap();
  const invalid = new Map(); let validationID = 0;
  // A quick departure and a long, damped arrival preserve spatial continuity.
  // These are analytics' own timings, independent of classic motion.
  const ease = "cubic-bezier(.16,1,.3,1)", exitEase = "cubic-bezier(.4,0,.7,.2)";
  let tooltip, tooltipOwner, tooltipAnchor, tipTimer;
  function stop(element) { active.get(element)?.(); }
  function play(element, frames, { duration = 220, delay = 0, name = "component", easing = ease, settled = () => {} } = {}) {
    if (!element?.isConnected || !scope.enabled() || !element.animate) { settled(); return () => {}; }
    // Reverse from the currently painted values rather than jumping to frame 0.
    if (active.has(element)) {
      const painted = getComputedStyle(element);
      frames = frames.map((frame, index) => index ? frame : Object.fromEntries(Object.entries(frame).map(([key, value]) => [key, painted[key] || value])));
    }
    stop(element);
    let animation;
    try { animation = element.animate(frames, { duration, delay, easing, fill: "both" }); }
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
    // Rendering a large form may delay the animation's start. Start the normal
    // cleanup deadline once the browser is ready, with a bounded fallback.
    timer = scope.timer(finish, duration + delay + 1000);
    animation.ready.then(() => {
      if (done || !scope.current()) return;
      scope.clearTimer(timer);
      timer = scope.timer(finish, duration + delay + 120);
    }).catch(finish);
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
    play(element, [{ opacity: 0, translate: "0 6px" }, { opacity: 1, translate: "0 0" }], { name, duration: name === "page" ? 300 : 220 });
  };
  function stopTail(tail) { [...active.keys()].filter(target => tail.contains(target)).forEach(stop); }
  function clearTails() { tails.forEach(tail => { stopTail(tail); tail.remove(); }); tails.clear(); }
  function floatingCopy(element, modal = false) {
    if (!element.isConnected) return;
    if (element.matches('[data-sensitive]') || element.querySelector('[data-sensitive]')) return;
    const rect = element.getBoundingClientRect();
    if (!rect.width || !rect.height || rect.bottom < 0 || rect.top > innerHeight) return;
    let clone = element.cloneNode(true);
    if (element.tagName === "DIALOG") {
      const surface = document.createElement("div"); surface.className = clone.className;
      surface.append(...clone.childNodes); clone = surface;
    }
    clone.querySelectorAll('script,iframe,object,embed').forEach(item => item.remove());
    clone.querySelectorAll('[autofocus]').forEach(item => item.removeAttribute('autofocus'));
    clone.removeAttribute("id"); clone.querySelectorAll("[id]").forEach(item => item.removeAttribute("id"));
    // A retired dialog is a DIV, so it must keep its painted surface rather
    // than relying on the browser's default DIALOG background.
    const paint = getComputedStyle(element);
    clone.style.backgroundColor = paint.backgroundColor;
    clone.style.color = paint.color;
    const originalParts = [element, ...element.querySelectorAll('*')];
    const cloneParts = [clone, ...clone.querySelectorAll('*')];
    originalParts.forEach((part, index) => {
      const copy = cloneParts[index];
      if (!copy) return;
      if ('value' in part && 'value' in copy) copy.value = part.value;
      if ('checked' in part && 'checked' in copy) copy.checked = part.checked;
      if (part.tagName === 'SELECT' && copy.tagName === 'SELECT') [...part.options].forEach((option, index) => { if (copy.options[index]) copy.options[index].selected = option.selected; });
    });
    clone.querySelectorAll('.a-toast-region').forEach(region=>region.remove());
    if (element.tagName === 'TR') {
      // Keep native table/column styling around a lifted row. A bare TR in
      // the top layer loses cell layout and inherits a transparent surface.
      const row = clone;
      const table = element.closest('table').cloneNode(false);
      table.removeAttribute('id');
      const columns = element.closest('table').querySelector(':scope > colgroup');
      if (columns) table.append(columns.cloneNode(true));
      const body = document.createElement('tbody'); body.append(row); table.append(body);
      clone = document.createElement('div');
      clone.className = element.closest('.a-table-scroll')?.className || 'a-table-scroll';
      clone.append(table);
      clone.style.overflow = 'hidden';
      clone.style.backgroundColor = getComputedStyle(element.closest('.a-card') || document.body).backgroundColor;
      clone.style.color = paint.color;
    }
    clone.inert = true; clone.setAttribute("aria-hidden", "true");
    clone.removeAttribute("popover");
    Object.assign(clone.style, { display: "block", position: "fixed", inset: "auto", left: `${rect.left}px`, top: `${rect.top}px`, width: `${rect.width}px`, height: `${rect.height}px`, margin: "0", maxHeight: "none", pointerEvents: "none", transform: "none" });
    const host = document.createElement("div");
    host.className = `a-motion-tail${modal ? " a-modal-tail" : ""}`;
    host.setAttribute("popover", "manual"); host.setAttribute("aria-hidden", "true"); host.inert = true;
    host.append(clone); document.body.append(host); tails.add(host);
    try { host.showPopover(); } catch { host.remove(); tails.delete(host); return; }
    originalParts.forEach((part, index) => { const copy = cloneParts[index]; if (copy) { copy.scrollTop = part.scrollTop; copy.scrollLeft = part.scrollLeft; } });
    return { host, clone, rect, paint };
  }
  function lift(element) {
    const floating = floatingCopy(element);
    if (!floating) return null;
    const { host, clone, rect } = floating;
    host.dataset.dragPreview = "";
    let tracking;
    const placeholder = element.animate([{ opacity: .25 }, { opacity: .25 }], { duration: 1, fill: 'both' });
    placeholder.id = 'analytics-drag-placeholder';
    return {
      move(x, y) {
        tracking?.cancel();
        tracking = clone.animate([{ translate: `${x}px ${y}px` }, { translate: `${x}px ${y}px` }], { duration: 1, fill: 'both' });
        tracking.id = 'analytics-drag-follow';
      },
      close() { tracking?.cancel(); placeholder.cancel(); host.remove(); tails.delete(host); },
      rect,
    };
  }
  function reorder(elements, change) {
    const previous = new Map(elements.map(element => [element, element.getBoundingClientRect()]));
    const scrollers = new Map();
    for (const element of elements) for (let parent = element.parentElement; parent; parent = parent.parentElement)
      if (!scrollers.has(parent)) scrollers.set(parent, { left: parent.scrollLeft, top: parent.scrollTop });
    elements.forEach(stop);
    change();
    // Reading geometry flushes native scroll anchoring before restoring the
    // reader's scroll position. Moving a focused record must not scroll to it.
    elements.forEach(element => element.getBoundingClientRect());
    scrollers.forEach((position, element) => {
      if (element.scrollLeft !== position.left || element.scrollTop !== position.top) element.scrollTo({ ...position, behavior: 'instant' });
    });
    if (!scope.enabled()) return;
    for (const element of elements) {
      const before = previous.get(element), after = element.getBoundingClientRect();
      if (before.left !== after.left || before.top !== after.top)
        play(element, [{ translate: `${before.left - after.left}px ${before.top - after.top}px` }, { translate: '0 0' }], { name: 'reorder', duration: 220 });
    }
  }
  function retire(element, { modal = false } = {}) {
    if (!scope.enabled()) return;
    const floating = floatingCopy(element, modal);
    if (!floating) return;
    const { host, clone, rect, paint } = floating;
    if (modal) {
      // The surface leaves against a held scrim; the scrim clears last. Never
      // fade the full host, which would double-paint a form over the live page.
      const drawer = element.classList.contains("a-drawer");
      // Preserve the current paint when closing during the entrance.
      const translate = paint.translate, scale = paint.scale;
      // Clear text before fading the white surface. Two readable forms must
      // never blend together, even halfway through a large editor's exit.
      if (!drawer) for (const content of clone.children)
        play(content, [{ opacity: 1 }, { opacity: 0 }], { duration: 100, name: "dialog-content-exit", easing: "ease-out", settled() { content.style.visibility = "hidden"; } });
      play(clone, [{ opacity: 1, translate, scale }, { opacity: 1, offset: .6 }, { opacity: drawer ? 1 : 0, translate: drawer ? "-100% 0" : "0 8px", scale: drawer ? "1" : ".98" }],
        { duration: drawer ? 220 : 180, name: "dialog-exit", easing: exitEase, settled() { clone.style.visibility = "hidden"; } });
      play(host, [{ backgroundColor: getComputedStyle(host).backgroundColor }, { backgroundColor: "rgba(24,24,27,0)" }],
        { delay: drawer ? 100 : 120, duration: 140, name: "exit", easing: "ease-out", settled() { [...clone.children].forEach(stop); stop(clone); host.remove(); tails.delete(host); } });
    } else play(host, [{ opacity: 1 }, { opacity: 0 }], { duration: 160, name: "exit", easing: exitEase,
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
        play(child, [{ translate: `0 ${y}px` }, { translate: "0 0" }], { name: "field-layout", duration: 240 });
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
    if (previous === undefined && seen.has(key) && !element.classList.contains("a-toast")) return;
    seen.add(key);
    if (!movingAncestor(element)) play(element, [{ opacity: 0, translate: element.classList.contains("a-toast") ? "10px 0" : "0 3px" }, { opacity: 1, translate: "0 0" }], { name: "feedback", duration: 240 });
  }
  function hideTip() {
    scope.clearTimer(tipTimer);
    if (tooltipOwner) {
      tooltipOwner.setAttribute('title', tooltipOwner.dataset.aMotionTitle || "");
      delete tooltipOwner.dataset.aMotionTitle;
      if (tooltipOwner.dataset.aMotionDescription === "true") tooltipOwner.removeAttribute("aria-description");
      delete tooltipOwner.dataset.aMotionDescription;
    }
    if (tooltip) { stop(tooltip); tooltip.remove(); }
    tooltip = tooltipOwner = tooltipAnchor = null;
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
    tooltipAnchor = rect;
    tooltip.style.left = `${Math.max(8, Math.min(innerWidth - box.width - 8, rect.left + (rect.width - box.width) / 2))}px`;
    tooltip.style.top = `${Math.max(8, rect.top > box.height + 12 ? rect.top - box.height - 6 : rect.bottom + 6)}px`;
    reveal(tooltip, "tooltip");
  }
  scope.listen(document, "toggle", event => {
    const element = event.target;
    if (!element.matches?.('details.a-disclosure')) return;
    const body = element.querySelector('.a-disclosure-body');
    if (element.open) reveal(body, "field"); else if (body) { stop(body); if (body.contains(document.activeElement)) element.querySelector('summary')?.focus({ preventScroll: true }); }
  }, { capture: true });
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
        play(element, drawer ? [{ translate: "-100% 0" }, { translate: "0 0" }] :
          [{ opacity: 0, translate: "0 12px", scale: ".975" }, { opacity: 1, translate: "0 0", scale: "1" }],
        { name: drawer ? "menu" : "dialog", duration: drawer ? 360 : 300 });
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
  for (const event of ["pointerout", "focusout", "pointerdown", "keydown"])
    scope.listen(document, event, event => {
      if (event.type === 'pointerout' && event.pointerType === 'touch' && tooltipOwner === document.activeElement) return;
      hideTip();
    }, { capture: true });
  scope.listen(document, 'scroll', () => {
    if (!tooltipOwner || !tooltipAnchor) return;
    const rect = tooltipOwner.getBoundingClientRect();
    if (!tooltipOwner.isConnected || ['left','top','width','height'].some(key => Math.abs(rect[key] - tooltipAnchor[key]) > .5)) hideTip();
  }, { capture: true });
  scope.listen(window, "hashchange", () => { clearTails(); hideTip(); });
  scope.listen(window, "resize", () => { clearTails(); hideTip(); });
  const validationMessage = control => control.validity.customError ? control.validationMessage : control.validity.valueMissing ? "请填写此项。" : "请检查此项的格式或取值范围。";
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
    error.label.textContent = validationMessage(control);
    const first = [...(control.form?.elements || [control])].find(item => item.willValidate && !item.validity.valid);
    if (first === control) control.focus();
  }, { capture: true });
  for (const event of ['input','change']) scope.listen(document,event, event => {
    const control = event.target, error = invalid.get(control);
    if (!error) return;
    if (control.validity.valid) clearValidation(control); else error.label.textContent = validationMessage(control);
  });
  const observer = new MutationObserver(records => {
    if (tooltipOwner && !tooltipOwner.isConnected) hideTip();
    if (tooltipOwner?.hasAttribute('title')) {
      const text = tooltipOwner.getAttribute('title');
      tooltipOwner.dataset.aMotionTitle = text; tooltipOwner.removeAttribute('title');
      if (tooltipOwner.dataset.aMotionDescription === 'true') tooltipOwner.setAttribute('aria-description', text);
      tooltip.textContent = text;
    }
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
  observer.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['title'] });
  scope.onSuspend(cancel);
  scope.own(() => { observer.disconnect(); cancel(); [...invalid.keys()].forEach(clearValidation); });
  return {
    page: element => {
      clearTails(); seenMessages.delete(element.closest(".a-main") || element);
      // Animate two visual groups at most, never every row or polling update.
      const content = element.children.length === 1 && element.firstElementChild.classList.contains("a-stack") ? element.firstElementChild : element;
      const groups = [...content.children].filter(child => !child.hidden && child.getBoundingClientRect().top < innerHeight);
      if (groups.length < 2) { reveal(element, "page"); return; }
      groups.slice(0, 2).forEach((group, index) => play(group,
        [{ opacity: 0, translate: "0 10px" }, { opacity: 1, translate: "0 0" }],
        { name: index ? "page-follow" : "page", duration: 320, delay: index * 40 }));
    }, visible, tooltip: showTip, lift, reorder,
    notificationExit: element => retire(element),
    navigation: layout => {
      clearTails();
      const rail = layout.querySelector(':scope > .a-rail');
      if (!layout.classList.contains('a-collapsed')) reveal(rail, "navigation");
      const work = layout.querySelector('.a-work');
      if (work) play(work, [{ opacity: .5, translate: "6px 0" }, { opacity: 1, translate: "0 0" }], { name: "workspace", duration: 260 });
    },
    list: element => { if (element.isConnected && !movingAncestor(element)) play(element, [{ opacity: .3, translate: "0 3px" }, { opacity: 1, translate: "0 0" }], { name: "list", duration: 240 }); },
    cancel,
  };
}
