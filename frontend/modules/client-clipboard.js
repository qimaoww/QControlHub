export async function copyClientValue(
  input,
  {
    navigatorObject = globalThis.navigator,
    documentObject = globalThis.document,
  } = {},
) {
  if (!input) throw new Error("没有可复制的内容");
  if (navigatorObject?.clipboard?.writeText) {
    await navigatorObject.clipboard.writeText(input.value || "");
    return "clipboard";
  }
  if (typeof documentObject?.execCommand !== "function")
    throw new Error("当前浏览器不支持剪贴板操作");

  const active = documentObject.activeElement;
  // Closed disclosures and password fields cannot reliably participate in
  // execCommand selection. Copy the complete value through a temporary control.
  const temporary = documentObject.createElement?.("textarea");
  const target = temporary || input;
  const selection = { start: input.selectionStart, end: input.selectionEnd, direction: input.selectionDirection };
  if (temporary) {
    temporary.value = input.value || "";
    temporary.setAttribute("readonly", "");
    temporary.style.cssText = "position:fixed;left:-9999px;top:0;opacity:0";
    // Keep the fallback within the active modal; the rest of the page is inert.
    (input.closest?.("dialog[open]") || documentObject.body).append(temporary);
  }
  try {
    target.focus({ preventScroll: true });
    target.select();
    if (!documentObject.execCommand("copy")) throw new Error("浏览器拒绝了剪贴板操作");
  } finally {
    temporary?.remove();
    if (!temporary && selection.start != null && typeof input.setSelectionRange === "function")
      input.setSelectionRange(selection.start, selection.end, selection.direction || "none");
    active?.focus?.({ preventScroll: true });
  }
  return "legacy";
}
