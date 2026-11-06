import { patch } from "./live-dom.js";
import { node, empty } from "./ui.js";

// Retain the scroll region, rows and unchanged cells across observations.
export function observationTable(columns, label) {
  const body = node("tbody"), placeholder = empty();
  const scroll = node("div", { className: "a-table-scroll", tabindex: "0", role: "region", "aria-label": label },
    node("table", {}, node("thead", {}, node("tr", {}, columns.map(title => node("th", { scope: "col" }, title)))), body));
  const root = node("div", {}, scroll, placeholder);
  const rows = new Map();
  return {
    root,
    update(records, project, emptyText = "暂无记录") {
      const ids = new Set(records.map(record => String(record.id)));
      for (const [id, row] of rows) if (!ids.has(id)) { row.remove(); rows.delete(id); }
      let previous = null;
      for (const record of records) {
        const id = String(record.id);
        let row = rows.get(id);
        if (!row) { row = node("tr", {}, columns.map(title => node("td", { "data-column": title }))); rows.set(id, row); }
        const values = project(record);
        values.forEach((value, index) => {
          const cell = row.children[index], signature = typeof value === "string" ? value : value?.textContent || "—";
          if (cell.dataset.value === signature) return;
          // A text selection belongs to the reader until they release it.
          const selection = globalThis.getSelection?.();
          if (selection && !selection.isCollapsed && (cell.contains(selection.anchorNode) || cell.contains(selection.focusNode))) return;
          cell.replaceChildren(value instanceof Node ? value : String(value ?? "—"));
          cell.dataset.value = signature;
        });
        const following = previous ? previous.nextSibling : body.firstChild;
        if (following !== row) body.insertBefore(row, following);
        previous = row;
      }
      scroll.hidden = !records.length;
      placeholder.hidden = Boolean(records.length);
      placeholder.textContent = emptyText;
    },
  };
}

// Stable entry nodes preserve reading, selection and scroll through polling.
export function observationStream(label) {
  const body = node("div", { role: "region", "aria-label": label });
  const placeholder = empty();
  const root = node("div", {}, body, placeholder);
  const rows = new Map();
  return {
    root,
    update(records, project, emptyText = "暂无记录") {
      const ids = new Set(records.map(record => String(record.id)));
      for (const [id, row] of rows) if (!ids.has(id)) { row.remove(); rows.delete(id); }
      let previous = null;
      for (const record of records) {
        const id = String(record.id);
        let row = rows.get(id);
        if (!row) {
          row = node("article", { "data-log-entry": id, className: "a-card-body a-stack" }, node("div"), node("div"));
          rows.set(id, row);
        }
        const values = project(record);
        const cells = [...row.children];
        values.forEach((value, index) => {
          const cell = cells[index], signature = value?.outerHTML || String(value ?? "—");
          if (cell.dataset.value === signature) return;
          const selection = globalThis.getSelection?.();
          if (selection && !selection.isCollapsed && (cell.contains(selection.anchorNode) || cell.contains(selection.focusNode))) return;
          if (cell.firstChild && value instanceof Node) patch(cell.firstChild, value);
          else cell.replaceChildren(value instanceof Node ? value : String(value ?? "—"));
          cell.dataset.value = signature;
        });
        const following = previous ? previous.nextSibling : body.firstChild;
        if (following !== row) body.insertBefore(row, following);
        previous = row;
      }
      body.hidden = !records.length; placeholder.hidden = Boolean(records.length); placeholder.textContent = emptyText;
    },
  };
}
