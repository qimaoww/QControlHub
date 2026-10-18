export async function copyClientValue(
  input,
  {
    navigatorObject = globalThis.navigator,
    documentObject = globalThis.document,
  } = {},
) {
  if (!input) throw new Error("没有可复制的内容");
  if (navigatorObject?.clipboard?.writeText) {
    try {
      await navigatorObject.clipboard.writeText(input.value || "");
      return "clipboard";
    } catch (error) {
      if (typeof documentObject?.execCommand !== "function") throw error;
    }
  }
  if (typeof documentObject?.execCommand !== "function")
    throw new Error("当前浏览器不支持剪贴板操作");

  const active = documentObject.activeElement;
  const selection = {
    start: input.selectionStart,
    end: input.selectionEnd,
    direction: input.selectionDirection,
  };
  // Browsers prevent execCommand from copying password controls. Select an
  // unmasked temporary control while keeping the visible field concealed.
  const temporary = input.type === "password" ? documentObject.createElement("textarea") : null;
  const source = temporary || input;
  if (temporary) {
    temporary.value = input.value || "";
    temporary.readOnly = true;
    temporary.className = "visually-hidden";
    (input.closest?.("dialog[open]") || documentObject.body).append(temporary);
  }
  source.focus({ preventScroll: true });
  source.select();
  try {
    if (!documentObject.execCommand("copy"))
      throw new Error("浏览器拒绝了剪贴板操作");
  } finally {
    temporary?.remove();
    if (selection.start != null && typeof input.setSelectionRange === "function")
      input.setSelectionRange(
        selection.start,
        selection.end,
        selection.direction || "none",
      );
    active?.focus?.({ preventScroll: true });
  }
  return "legacy";
}
