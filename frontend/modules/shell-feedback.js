import { bindEvent } from "./refresh.js";
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
  notice.className = `alert ${tone}`;
  notice.dataset.spaNotice = "";
  notice.dataset.refreshKey = "spa-notice";
  notice.setAttribute("role", tone === "error" ? "alert" : "status");
  notice.textContent = message;
  if (!notice.isConnected) main.prepend(notice);
  if (tone !== "error") noticeTimer = setTimeout(() => notice.remove(), 5000);
}

function confirmAction(message, label = "确认继续") {
  const dialog = document.querySelector("[data-confirm-dialog]");
  if (!dialog?.showModal) return Promise.resolve(window.confirm(message));
  dialog.querySelector("[data-confirm-message]").textContent = message;
  dialog.querySelector("[data-confirm-accept]").textContent = label;
  state.confirmOpen = true;
  dialog.showModal();
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
    confirmDialog.close();
    resolve(accepted);
  };
  confirmDialog.querySelector("[data-confirm-cancel]").onclick = () =>
    finishConfirm(false);
  confirmDialog.querySelector("[data-confirm-accept]").onclick = () =>
    finishConfirm(true);
  bindEvent(confirmDialog, "cancel", (event) => {
    event.preventDefault();
    finishConfirm(false);
  });
}

  return { notify, confirmAction, bindConfirmationDialog };
}
