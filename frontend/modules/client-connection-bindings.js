import { bindEvent } from "./refresh.js";

export function bindClientConnections({ current, search, recent, next, previous }) {
  const form = document.querySelector("[data-connection-filters]");
  bindEvent(form, "submit", event => {
    event.preventDefault();
    if (current()) search(Object.fromEntries(new FormData(form)));
  });
  bindEvent(document.querySelector("[data-connection-bucket]"), "change", () => { if (current()) search(Object.fromEntries(new FormData(form))); });
  for (const [selector, action] of [["recent", recent], ["next", next], ["previous", previous]]) {
    bindEvent(document.querySelector(`[data-connection-${selector}]`), "click", () => { if (current()) action(); });
  }
}
