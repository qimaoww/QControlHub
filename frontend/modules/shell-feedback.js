import { cancelMotion, enterSurface } from "./motion.js";
import { bindEvent } from "./refresh.js";
import { renderNotice } from "./shell-feedback-view.js";
import { errorMessage } from "./errors.js";

export function createShellFeedback(state) {
let noticeTimer;
function notify(message, tone = "success") {
  clearTimeout(noticeTimer);
  if (tone === "error") message = errorMessage(message);
  const main = document.querySelector(".workspace-main");
  if (!main) return;
  const notice =
    main.querySelector(":scope > [data-spa-notice]") ||
    document.createElement("div");
  const changed = notice.dataset.noticeMessage !== String(message) || notice.dataset.noticeTone !== tone;
  notice.dataset.noticeMessage = String(message);
  notice.dataset.noticeTone = tone;
  notice.className = `alert ${tone}`;
  notice.dataset.spaNotice = "";
  notice.dataset.refreshKey = "spa-notice";
  notice.setAttribute("role", tone === "error" ? "alert" : "status");
  renderNotice(notice, message, tone);
  if (!notice.isConnected) main.prepend(notice);
  notice.querySelector(".notice-close").onclick = () => {
    clearTimeout(noticeTimer);
    noticeTimer = null;
    cancelMotion(notice);
    notice.remove();
  };
  if (changed) enterSurface(notice, { token: "--motion-feedback" });
  if (tone !== "error") noticeTimer = setTimeout(() => notice.remove(), 5000);
}

function confirmAction(message, label = "确认继续", options = {}) {
  const dialog = document.querySelector("[data-confirm-dialog]");
  if (!dialog?.showModal) {
    return Promise.resolve(window.confirm([options.title, message].filter(Boolean).join("\n")));
  }
  dialog.dataset.tone = options.tone || "danger";
  const title = dialog.querySelector("[data-confirm-title]");
  if (title) title.textContent = options.title || "确认继续？";
  dialog.querySelector("[data-confirm-message]").textContent = message;
  dialog.querySelector("[data-confirm-message]").hidden = !message;
  dialog.querySelector("[data-confirm-accept]").textContent = label;
  // Superseding a confirmation must settle the earlier caller as canceled.
  state.confirmResolver?.(false);
  state.confirmResolver = null;
  if (dialog.open) dialog.close();
  state.confirmOpen = true;
  dialog.showModal();
  dialog.querySelector("[data-confirm-cancel]")?.focus?.();
  return new Promise((resolve) => {
    state.confirmResolver = resolve;
  });
}

function bindConfirmationDialog() {
  const confirmDialog = document.querySelector("[data-confirm-dialog]");
  const finishConfirm = (accepted) => {
    if (!state.confirmResolver) return;
    const resolve = state.confirmResolver;
    state.confirmResolver = null;
    state.confirmOpen = false;
    if (confirmDialog.open) confirmDialog.close();
    resolve(accepted);
  };
  confirmDialog.querySelector("[data-confirm-cancel]").onclick = () =>
    finishConfirm(false);
  confirmDialog.querySelector("[data-confirm-accept]").onclick = () =>
    finishConfirm(true);
  bindEvent(confirmDialog, "close", () => { if (!confirmDialog.open) finishConfirm(false); });
  bindEvent(confirmDialog, "cancel", (event) => {
    event.preventDefault();
    finishConfirm(false);
  });
}

  function resetFeedback() {
    clearTimeout(noticeTimer);
    noticeTimer = null;
    const resolve = state.confirmResolver;
    state.confirmResolver = null;
    state.confirmOpen = false;
    document.querySelector("[data-confirm-dialog][open]")?.close();
    resolve?.(false);
  }
  return { notify, confirmAction, bindConfirmationDialog, resetFeedback };
}
