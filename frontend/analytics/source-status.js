import { patch } from "./live-dom.js";
import { node, button, actions, stack, section, table, badge, muted, empty } from "./ui.js";

// Both observation pages use the same scoped, stable status dialog.
export function sourceStatus(ctx, { marker, lastSeenLabel = "最近更新", lastSeenEmpty = "尚无记录" }) {
  const content = stack(), groups = node("div", { className: "a-stack" }), scope = muted("");
  const totals = actions(), footer = muted("");
  content.append(scope, totals, groups, footer);
  let dialog, signature = "";
  const expanded = new Map();
  const trigger = button("来源采集状态", () => {
    if (dialog?.open) return;
    dialog = ctx.dialog("来源采集状态", content);
    dialog.setAttribute(marker, "");
  }, { "aria-haspopup": "dialog", "data-source-status": "" });
  ctx.signal.addEventListener("abort", () => dialog?.close(), { once: true });
  return {
    trigger,
    update(items, { scope: scopeText = "", policy = "" } = {}) {
      const next = JSON.stringify([items, scopeText, policy]);
      if (next === signature) return;
      signature = next;
      const attention = items.filter(item => item.attention).length;
      trigger.textContent = attention ? `来源采集状态 · ${attention} 项需关注` : "来源采集状态";
      scope.textContent = scopeText; footer.textContent = policy; footer.hidden = !policy;
      patch(totals, actions(badge(`来源 ${items.length}`), attention > 0 && badge(`需关注 ${attention}`, "warn")));
      const grouped = new Map();
      for (const item of items) {
        const key = JSON.stringify([item.agentID, item.instanceID || ""]);
        if (!grouped.has(key)) grouped.set(key, []);
        grouped.get(key).push(item);
      }
      const fresh = node("div", { className: "a-stack" }, [...grouped].map(([key, rows], index) => {
        const first = rows[0], failed = rows.filter(row => row.attention).length;
        if (!expanded.has(key)) expanded.set(key, Boolean(failed || !attention && index === 0));
        const body = node("div", { "data-source-body": "", hidden: !expanded.get(key) }, table(["采集范围", "采集状态"], rows.map(row => [
            row.engine,
            stack(actions(badge(row.state, row.tone), row.detail && button("详情", () => {
              const detail = ctx.dialog(`${first.agentName} · ${row.engine}`, stack(first.instance && muted(first.instance), muted(row.state), row.transport && muted(row.transport), muted(row.detail)));
              dialog.addEventListener("close", () => detail.close(), { once: true });
            })), row.transport && muted(row.transport), muted(`${lastSeenLabel} · ${row.lastSeen || lastSeenEmpty}`)),
          ]), { paginate: 0, motion: false, rowKey: row => row[0], label: `${first.agentName}采集状态` }));
        const toggle = button(expanded.get(key) ? "收起" : "展开", event => {
          const open = !expanded.get(key); expanded.set(key, open);
          event.currentTarget.closest('[data-source-group]').querySelector('[data-source-body]').hidden = !open;
          event.currentTarget.textContent = open ? "收起" : "展开";
          event.currentTarget.setAttribute("aria-expanded", String(open));
        }, { "aria-expanded": String(expanded.get(key)), "data-source-expand": "" });
        const group = section(first.agentName, body, actions(
          failed ? badge(`${failed} 项需关注`, "warn") : muted(`${rows.length} 个来源`), toggle), first.instance);
        group.dataset.refreshKey = key; group.dataset.sourceGroup = "";
        return group;
      }));
      if (!items.length) fresh.append(empty("当前范围没有获授权的采集来源"));
      patch(groups, fresh);
    },
  };
}
