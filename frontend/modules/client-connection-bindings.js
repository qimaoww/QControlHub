import { bindEvent } from "./refresh.js";

export function bindClientConnections({ current, refresh, search, selectAgent, recent, month, next, previous, filterToggle }) {
  document.querySelectorAll("[data-connection-agent]").forEach(link => bindEvent(link, "click", event => {
    event.preventDefault();
    if (current()) selectAgent(link.dataset.connectionAgent);
  }));
  const form = document.querySelector("[data-connection-filters]");
  bindEvent(document.querySelector(".connection-filter-panel"), "toggle", event => { if (current()) filterToggle(event.target.open); });
  bindEvent(form, "submit", event => {
    event.preventDefault();
    if (current()) search(Object.fromEntries(new FormData(form)));
  });
  for (const [selector, action] of [["month", () => month(Object.fromEntries(new FormData(form)))], ["refresh", refresh], ["recent", recent], ["next", next], ["previous", previous]]) {
    bindEvent(document.querySelector(`[data-connection-${selector}]`), "click", () => { if (current()) action(); });
  }
}
