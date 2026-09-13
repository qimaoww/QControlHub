import { bindEvent } from "./refresh.js";

// Both configuration menus use the same disclosure, focus and keyboard rules.
export function bindConfigMenu(menu) {
  const summary = menu.querySelector("summary");
  const items = () => [...menu.querySelectorAll('[role="menuitem"]')]
    .filter(button => !button.disabled && !button.closest("[hidden]"));
  menu.querySelectorAll('[role="menuitem"]').forEach(button => { button.tabIndex = -1; });
  const setOpen = open => {
    if (open) menu.parentElement.querySelectorAll(".config-inbound-menu").forEach(other => {
      if (other !== menu) {
        other.open = false;
        other.querySelector("summary").setAttribute("aria-expanded", "false");
      }
    });
    menu.open = open;
    summary.setAttribute("aria-expanded", String(open));
    if (open) {
      const list = menu.querySelector('[role="menu"]'), bounds = menu.getBoundingClientRect();
      list.style.right = "auto";
      list.style.left = `${Math.max(8 - bounds.left, Math.min(0, document.documentElement.clientWidth - 8 - bounds.left - list.offsetWidth))}px`;
    }
  };
  setOpen(false);
  bindEvent(summary, "click", event => {
    event.preventDefault();
    setOpen(!menu.open);
  });
  bindEvent(menu, "toggle", () => setOpen(menu.open));
  bindEvent(menu, "focusout", event => {
    if (!menu.contains(event.relatedTarget)) setOpen(false);
  });
  bindEvent(menu, "keydown", event => {
    if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false);
      summary.focus();
      return;
    }
    const enter = event.target === summary && ["Enter", " "].includes(event.key);
    if (!enter && !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    setOpen(true);
    const buttons = items(), index = buttons.indexOf(document.activeElement);
    if (!buttons.length) return;
    const next = event.key === "End" || event.key === "ArrowUp" && index < 0 ? buttons.length - 1 :
      enter || event.key === "Home" || index < 0 ? 0 :
        (index + (event.key === "ArrowUp" ? buttons.length - 1 : 1)) % buttons.length;
    buttons[next].focus();
  });
}
