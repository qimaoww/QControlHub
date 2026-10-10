import { bindEvent } from "./refresh.js";
import { cancelMotion, enterSurface } from "./motion.js";

const popupSelector = ".mobile-account-menu,.config-inbound-menu,.config-tools-menu,.dashboard-month-picker,.ip-quality-calendar";
const closePopup = (popup, restoreFocus = false) => {
  popup.open = false;
  popup.querySelector("summary")?.setAttribute("aria-expanded", "false");
  cancelMotion(popup);
  if (restoreFocus) popup.querySelector("summary")?.focus({ preventScroll: true });
};

// One shell binding serves menus/calendars and inline disclosures. Only the
// revealed content fades; the launcher and its keyboard focus remain stable.
export function bindPopups() {
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
    if (!details.open) return cancelMotion(details);
    if (!details.contains(document.activeElement)) return;
    if (document.querySelector(".workspace-main")?.getAnimations().some(animation => animation.id === "qch-route")) return;
    const content = [...details.children].find(child => child.tagName !== "SUMMARY");
    if (content) enterSurface(content, { token: "--motion-feedback", id: "qch-disclosure" });
  }, true);
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
  dialog.getAnimations().filter(animation => ["qch-dialog-enter", "qch-fade-enter"].includes(animation.animationName))
    .forEach(animation => animation.cancel());
}
