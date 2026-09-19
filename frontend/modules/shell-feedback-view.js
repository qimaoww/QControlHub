// Build feedback with text nodes so server messages and node names stay inert.
export function renderNotice(notice, message, tone) {
  const icon = document.createElement("span");
  icon.className = "notice-icon";
  icon.setAttribute("aria-hidden", "true");
  icon.textContent = tone === "error" ? "!" : "✓";
  const text = document.createElement("span");
  text.className = "notice-message";
  text.textContent = message;
  const close = document.createElement("button");
  close.type = "button";
  close.className = "notice-close";
  close.setAttribute("aria-label", "关闭提示");
  close.textContent = "×";
  close.onclick = () => notice.remove();
  notice.replaceChildren(icon, text, close);
}
