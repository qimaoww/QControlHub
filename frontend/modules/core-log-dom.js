import { reconcileView } from "./refresh.js";
import { createShellContext } from "./shell-context.js";
import { animateMotion, syncSelectionMotion } from "./motion.js";

// Keep the shell and controls mounted; a log poll only patches changed regions.
// Log rows contain no controls or nested scrollers, so they need no global
// focus/scroll capture (which otherwise reads layout for every descendant).
export function createCoreLogDOM(ctx) {
  const context = createShellContext(ctx);
  let owner, mounted, sidebar = "";
  const markupByRegion = new Map();
  return (markup, regions) => {
    const workspace = document.querySelector("[data-core-log-page]");
    if (!workspace || mounted !== workspace || owner !== ctx.state.data) {
      ctx.shell(markup, "内核日志");
      owner = ctx.state.data;
      mounted = document.querySelector("[data-core-log-page]");
      markupByRegion.clear();
      for (const [selector, html] of regions) markupByRegion.set(selector, html);
      sidebar = "";
      return;
    }
    for (const [selector, html] of regions) {
      if (markupByRegion.get(selector) === html) continue;
      markupByRegion.set(selector, html);
      const current = workspace.querySelector(selector);
      const template = document.createElement("template");
      template.innerHTML = html;
      const liveRows = selector === ".core-log-stream" && current.dataset.motionKey === template.content.firstElementChild.dataset.motionKey;
      const oldKeys = liveRows ? new Set([...current.querySelectorAll(".core-log-row")].map(row => row.dataset.refreshKey)) : null;
      const main = workspace.closest(".workspace-main");
      const scroller = liveRows && main.scrollHeight <= main.clientHeight ? document.scrollingElement : main;
      // Preserve the row being read when new entries prepend to the same page.
      // Read only until the first row intersects the viewport, before writing.
      const top = scroller?.scrollTop && scroller !== document.scrollingElement ? Math.max(0, scroller.getBoundingClientRect().top) : 0;
      let anchor = null;
      if (liveRows && scroller?.scrollTop) {
        for (const row of current.querySelectorAll(".core-log-row")) {
          const rect = row.getBoundingClientRect();
          if (rect.bottom > top) { anchor = { row, rect }; break; }
        }
      }
      reconcileView(current, template.content.firstElementChild, { preserveViewState: selector !== ".core-log-stream" });
      if (selector === ".core-log-filters") {
        // The options and saved selection belong to the verified scope. A
        // native select auto-selects its first option while sources load;
        // generic dirty-control preservation must not retain that placeholder.
        for (const fresh of template.content.querySelectorAll("select")) {
          const select = current.querySelector(`select[name="${fresh.name}"]`);
          if (select && select.value !== fresh.value) select.value = fresh.value;
        }
      }
      if (anchor?.row.isConnected) scroller.scrollTo({ top: scroller.scrollTop + anchor.row.getBoundingClientRect().top - anchor.rect.top, behavior: "instant" });
      if (oldKeys && !scroller?.scrollTop) {
        [...current.querySelectorAll(".core-log-row")].filter(row => !oldKeys.has(row.dataset.refreshKey)).slice(0, 3)
          .forEach(row => animateMotion(row, [{ opacity: .65 }, { opacity: 1 }], { id: "qch-log-new", token: "--motion-fast" }));
      }
    }
    const freshContext = context();
    if (sidebar !== freshContext) {
      sidebar = freshContext;
      const current = document.querySelector(".context-sidebar");
      if (current) {
        const template = document.createElement("template");
        template.innerHTML = `<aside class="context-sidebar" data-refresh-key="context-core-logs">${current.querySelector(".context-brand").outerHTML}${freshContext}</aside>`;
        reconcileView(current, template.content.firstElementChild);
      }
    }
    const online = (ctx.state.data.coreLogAgents || []).filter(agent => agent.status === "online").length;
    const sync = document.querySelector("[data-sync-state]");
    sync?.classList.toggle("inactive", !online);
    const label = document.querySelector("[data-sync-label]");
    if (label) label.textContent = online ? `${online} 个节点在线` : "等待节点连接";
    syncSelectionMotion(workspace);
  };
}
