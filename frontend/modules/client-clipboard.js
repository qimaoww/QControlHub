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
  const selection = {
    start: input.selectionStart,
    end: input.selectionEnd,
    direction: input.selectionDirection,
  };
  input.focus({ preventScroll: true });
  input.select();
  try {
    if (!documentObject.execCommand("copy"))
      throw new Error("浏览器拒绝了剪贴板操作");
  } finally {
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
