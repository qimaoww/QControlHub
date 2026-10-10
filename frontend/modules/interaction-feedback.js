import { reducedMotion } from "./motion.js";
import { updateFeedback, removePresented } from "./presence-motion.js";

let installed = false, tooltip, owner, showTimer, closeTimer, watch;
let hovered, focused, errorID = 0;
const errors = new WeakMap();
const target = node => node?.closest?.("[title],[data-motion-title]");
function hideTip(immediate = false) {
  clearTimeout(showTimer);
  if (!tooltip) return;
  tooltip.removeAttribute("data-open");
  clearTimeout(closeTimer);
  const remove = () => {
    if (owner?.hasAttribute("data-motion-title")) {
      owner.title = owner.dataset.motionTitle;
      delete owner.dataset.motionTitle;
    }
    if (owner?.hasAttribute("data-motion-description")) {
      owner.removeAttribute("aria-description");
      delete owner.dataset.motionDescription;
    }
    tooltip?.remove(); tooltip = owner = null;
    watch?.disconnect(); watch = null;
  };
  if (immediate || reducedMotion()) remove();
  else closeTimer = setTimeout(remove, 240);
}
function showTip(element) {
  if (!element?.isConnected || element.closest("[hidden],[inert]")) return;
  const modal = document.querySelector("dialog:modal");
  if (modal && !modal.contains(element)) return;
  const text = element.dataset.motionTitle ?? element.getAttribute("title");
  if (!text?.trim()) return;
  clearTimeout(closeTimer);
  if (owner !== element) {
    hideTip(true);
    owner = element;
    element.dataset.motionTitle = text;
    element.removeAttribute("title");
    if (!element.hasAttribute("aria-description")) {
      element.setAttribute("aria-description", text);
      element.dataset.motionDescription = "";
    }
    tooltip = document.createElement("div");
    tooltip.className = "interaction-tooltip";
    tooltip.setAttribute("role", "tooltip");
    tooltip.setAttribute("popover", "manual");
    tooltip.textContent = text;
    document.body.append(tooltip);
    tooltip.showPopover?.();
    watch = new MutationObserver(records => {
      if (!owner?.isConnected) return hideTip(true);
      if (!records.some(record => record.target === owner && record.type === "attributes")) return;
      if (owner.hasAttribute("title")) {
        owner.dataset.motionTitle = owner.title;
        owner.removeAttribute("title");
      }
      const message = owner.dataset.motionTitle;
      if (!message) return hideTip(true);
      if (owner.hasAttribute("data-motion-description")) owner.setAttribute("aria-description", message);
      updateFeedback(tooltip, message);
    });
    watch.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["title", "data-motion-title"] });
  }
  const bounds = element.getBoundingClientRect(), tip = tooltip.getBoundingClientRect();
  const top = bounds.top >= tip.height + 16 ? bounds.top - tip.height - 9 : bounds.bottom + 9;
  tooltip.style.left = `${Math.max(8, Math.min(innerWidth - tip.width - 8, bounds.left + (bounds.width - tip.width) / 2))}px`;
  tooltip.style.top = `${Math.max(8, Math.min(innerHeight - tip.height - 8, top))}px`;
  getComputedStyle(tooltip).opacity;
  tooltip.dataset.open = "";
}
function scheduleTip(element, delay = 220) {
  clearTimeout(showTimer);
  if (element === owner && tooltip) showTip(element);
  else { hideTip(); showTimer = setTimeout(() => showTip(element), delay); }
}
function clearError(control) {
  const error = errors.get(control);
  if (!error) return;
  removePresented(error.label);
  for (const [attribute, value] of error.attributes) {
    if (value == null) control.removeAttribute(attribute);
    else control.setAttribute(attribute, value);
  }
  delete control.dataset.motionInvalid;
  errors.delete(control);
}
function invalidFeedback(control) {
  let error = errors.get(control);
  if (!error?.label.isConnected) {
    const label = document.createElement("small");
    label.className = "interaction-validation";
    label.hidden = true;
    label.id = `motion-validation-${++errorID}`;
    label.setAttribute("role", "alert");
    label.dataset.motionValidation = "";
    const group = control.closest(".secret-value-control,.generated-input-control") || control;
    group.after(label);
    error = { label, attributes: ["aria-invalid", "aria-errormessage"].map(name => [name, control.getAttribute(name)]) };
    errors.set(control, error);
    control.dataset.motionInvalid = "";
    control.setAttribute("aria-invalid", "true");
    control.setAttribute("aria-errormessage", label.id);
  }
  updateFeedback(error.label, control.validationMessage);
}

// One delegated binding covers current and subsequently mounted pages/forms.
// Native validity and submission still decide whether an action may proceed.
export function installInteractionFeedback() {
  if (installed) return;
  installed = true;
  document.addEventListener("pointerover", event => {
    if (event.pointerType === "touch") return;
    const element = target(event.target);
    if (element === hovered) return;
    hovered = element;
    if (element) scheduleTip(element); else if (!focused) hideTip();
  });
  document.addEventListener("pointerout", event => {
    if (!hovered?.contains(event.relatedTarget)) { hovered = null; if (!focused) hideTip(); }
  });
  document.addEventListener("focusin", event => {
    focused = target(event.target);
    if (focused) scheduleTip(focused, 80); else hideTip();
  });
  document.addEventListener("focusout", event => {
    if (!focused?.contains(event.relatedTarget)) { focused = null; if (!hovered) hideTip(); }
  });
  document.addEventListener("pointerdown", () => hideTip(), true);
  document.addEventListener("beforetoggle", event => {
    if (event.target.tagName === "DIALOG" && event.newState === "open") hideTip();
  }, true);
  document.addEventListener("keydown", event => { if (event.key === "Escape") hideTip(); }, true);
  document.addEventListener("invalid", event => {
    const control = event.target;
    if (!control.validationMessage || !control.matches("input,select,textarea")) return;
    event.preventDefault();
    hideTip(true);
    const first = [...(control.form?.elements || [control])].find(element => element.willValidate && !element.validity.valid);
    if (first === control) control.focus();
    invalidFeedback(control);
  }, true);
  const validate = event => {
    const control = event.target;
    if (!errors.has(control)) return;
    if (control.validity.valid) clearError(control); else invalidFeedback(control);
  };
  document.addEventListener("input", validate);
  document.addEventListener("change", validate);
  document.addEventListener("visibilitychange", () => { if (document.hidden) hideTip(true); });
  window.addEventListener("hashchange", () => hideTip(true));
  window.addEventListener("resize", () => hideTip(true));
  document.addEventListener("scroll", () => { if (tooltip?.hasAttribute("data-open")) hideTip(true); }, true);
}
