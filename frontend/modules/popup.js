import { bindEvent } from "./refresh.js";
import { cancelMotion, enterSurface } from "./motion.js";

const popupSelector = ".mobile-account-menu,.config-inbound-menu,.config-tools-menu,.dashboard-month-picker,.ip-quality-calendar";
const retiredDialogs = new WeakMap();
const disclosureContent = new WeakMap();
function discloseInteraction(details, open) {
  const previous = disclosureContent.get(details);
  if (open) {
    previous?.forEach(({ inert, hidden }, child) => {
      child.inert = inert;
      if (hidden == null) child.removeAttribute("aria-hidden");
      else child.setAttribute("aria-hidden", hidden);
      child.removeAttribute("data-motion-retired-content");
    });
    disclosureContent.delete(details);
    return;
  }
  const states = previous || new Map();
  if (details.contains(document.activeElement) && document.activeElement !== details.querySelector("summary"))
    details.querySelector("summary")?.focus({ preventScroll: true });
  [...details.children].filter(child => child.tagName !== "SUMMARY").forEach(child => {
    if (!states.has(child)) states.set(child, { inert: child.inert, hidden: child.getAttribute("aria-hidden") });
    child.inert = true;
    child.setAttribute("aria-hidden", "true");
    child.dataset.motionRetiredContent = "";
  });
  disclosureContent.set(details, states);
}
export function prepareDisclosure(details) {
  if (details && !details.open) discloseInteraction(details, true);
  if (!details || !globalThis.CSS?.supports("selector(details::details-content)") ||
      !CSS.supports("transition-behavior", "allow-discrete")) return;
  details.dataset.motionDisclosure = "";
  if (details.matches(popupSelector)) details.dataset.motionPopup = "";
}
export const closePopup = (popup, restoreFocus = false) => {
  if (popup.hasAttribute("data-motion-disclosure")) discloseInteraction(popup, false);
  popup.open = false;
  popup.querySelector("summary")?.setAttribute("aria-expanded", "false");
  cancelMotion(popup);
  if (restoreFocus) popup.querySelector("summary")?.focus({ preventScroll: true });
};

// One shell binding serves menus/calendars and inline disclosures. Only the
// revealed content fades; the launcher and its keyboard focus remain stable.
export function bindPopups() {
  bindEvent(document, "beforetoggle", event => {
    const dialog = event.target;
    if (dialog.tagName !== "DIALOG") return;
    if (event.newState === "closed") {
      retiredDialogs.set(dialog, { inert: dialog.inert, hidden: dialog.getAttribute("aria-hidden") });
      dialog.inert = true;
      dialog.setAttribute("aria-hidden", "true");
    } else if (retiredDialogs.has(dialog)) {
      const previous = retiredDialogs.get(dialog);
      dialog.inert = previous.inert;
      if (previous.hidden == null) dialog.removeAttribute("aria-hidden");
      else dialog.setAttribute("aria-hidden", previous.hidden);
      retiredDialogs.delete(dialog);
    }
  }, true);
  const nativeDisclosure = globalThis.CSS?.supports("selector(details::details-content)") &&
    CSS.supports("transition-behavior", "allow-discrete");
  bindEvent(document, "click", event => {
    const summary = event.target.closest?.("summary");
    const details = summary?.parentElement;
    if (details?.tagName !== "DETAILS") return;
    prepareDisclosure(details);
    if (details.open && details.hasAttribute("data-motion-disclosure")) discloseInteraction(details, false);
    if (details.open && details.matches(".core-version-panel")) {
      const launcher = details.closest(".service-card")?.querySelector("[data-open-version-form]");
      launcher?.focus({ preventScroll: true });
      launcher?.setAttribute("aria-expanded", "false");
    }
  }, true);
  const openPopups = () => [...document.querySelectorAll(popupSelector)].filter(popup => popup.open);
  bindEvent(document, "pointerdown", event => {
    openPopups().forEach(popup => { if (!popup.contains(event.target)) closePopup(popup); });
  }, true);
  bindEvent(document, "focusin", event => {
    openPopups().forEach(popup => { if (!popup.contains(event.target)) closePopup(popup); });
  }, true);
  bindEvent(document, "keydown", event => {
    if (event.key !== "Escape") return;
    const popup = event.target.closest?.(popupSelector);
    if (!popup?.open) return;
    event.preventDefault();
    event.stopPropagation();
    closePopup(popup, true);
  }, true);
  bindEvent(document, "toggle", event => {
    const details = event.target;
    if (details.tagName !== "DETAILS") return;
    const popup = details.matches(popupSelector);
    if (popup) details.querySelector("summary")?.setAttribute("aria-expanded", String(details.open));
    if (details.hasAttribute("data-motion-disclosure")) discloseInteraction(details, details.open);
    if (!details.open) return cancelMotion(details);
    if (!details.contains(document.activeElement)) return;
    if (document.querySelector(".workspace-main")?.getAnimations().some(animation => animation.id === "qch-route")) return;
    if (nativeDisclosure && details.hasAttribute("data-motion-disclosure")) return;
    const content = [...details.children].find(child => child.tagName !== "SUMMARY");
    if (content) enterSurface(content, { token: "--motion-feedback", id: "qch-disclosure" });
  }, true);
  document.querySelectorAll("details[data-motion-disclosure]").forEach(details => discloseInteraction(details, details.open));
}

const backdropStarts = new WeakSet();
export function bindDialogBackdrop(dialog, close = () => dialog.close()) {
  const outside = event => {
    const rect = dialog.getBoundingClientRect();
    return event.target === dialog && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom);
  };
  bindEvent(dialog, "pointerdown", event => {
    if (outside(event)) backdropStarts.add(dialog); else backdropStarts.delete(dialog);
  });
  bindEvent(dialog, "pointercancel", () => backdropStarts.delete(dialog));
  bindEvent(dialog, "close", () => { if (!dialog.open) backdropStarts.delete(dialog); });
  dialog.onclick = event => {
    const dismiss = backdropStarts.has(dialog) && outside(event);
    backdropStarts.delete(dialog);
    if (dismiss) close();
  };
}

// Restoring a detached native dialog is reconciliation, not a fresh reveal.
export function cancelPopupEntrance(dialog) {
  dialog.getAnimations().filter(animation => ["qch-dialog-enter", "qch-fade-enter"].includes(animation.animationName) ||
      ["opacity", "translate"].includes(animation.transitionProperty))
    .forEach(animation => animation.cancel());
}
