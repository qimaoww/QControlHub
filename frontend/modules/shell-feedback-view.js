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

export function renderConfirmationDetails(host, details = [], targets = []) {
  if (!host) return;
  host.replaceChildren();
  host.hidden = !details.length && !targets.length;
  if (details.length) {
    const list = document.createElement("dl");
    list.className = "confirm-facts";
    details.forEach(([label, value]) => {
      const row = document.createElement("div");
      const term = document.createElement("dt");
      const description = document.createElement("dd");
      term.textContent = label;
      description.textContent = value;
      row.append(term, description);
      list.append(row);
    });
    host.append(list);
  }
  if (targets.length) {
    const title = document.createElement("p");
    title.className = "confirm-targets-title";
    title.textContent = `目标节点 · ${targets.length}`;
    const list = document.createElement("ul");
    list.className = "confirm-targets";
    list.setAttribute("aria-label", "目标节点");
    targets.forEach((name) => {
      const item = document.createElement("li");
      item.textContent = name;
      list.append(item);
    });
    host.append(title, list);
  }
}
