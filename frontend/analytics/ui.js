import { currentTheme } from "../modules/theme-lifecycle.js";
let fieldID = 0;
const icons = {
  brand: '<path d="M3 12h4l3-7 4 14 3-7h4"/>',
  grid: '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
  server: '<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/>',
  file: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zm0 0v6h6M8 13h8m-8 4h8"/>',
  link: '<path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-2 2m3 6a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l2-2"/>',
  shield: '<path d="M12 22s8-4 8-11V5l-8-3-8 3v6c0 7 8 11 8 11zM9 12l2 2 4-4"/>',
  activity: '<path d="M3 17h3l3-10 4 13 3-8h5"/>',
  list: '<path d="M9 6h12M9 12h12M9 18h12M3 6h.01M3 12h.01M3 18h.01"/>',
  users: '<circle cx="9" cy="8" r="3"/><path d="M2 20v-2a4 4 0 0 1 4-4h6a4 4 0 0 1 4 4v2m4 0v-2a4 4 0 0 0-3-4m-2-9a3 3 0 0 1 0 6"/>',
  settings: '<path d="M4 6h16M4 12h16M4 18h16"/><path d="M9 4v4m6 2v4m-6 2v4"/>',
  palette: '<path d="M12 3a9 9 0 1 0 0 18h1.5a1.5 1.5 0 0 0 1.1-2.5 1.5 1.5 0 0 1 1.1-2.5H18a3 3 0 0 0 3-3 10 10 0 0 0-9-10zM7.5 12h.01M10 7.5h.01M15 7.5h.01M17.5 12h.01"/>',
  menu: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>',
  close: '<path d="m6 6 12 12M18 6 6 18"/>',
  refresh: '<path d="M20 7v5h-5M4 17v-5h5m11 0a8 8 0 0 0-14-5m-2 5a8 8 0 0 0 14 5"/>',
  logout: '<path d="M10 4H5v16h5m-1-8h12m-4-4 4 4-4 4"/>',
};

export function node(tag, attrs = {}, ...children) {
  const element = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === false || value == null) continue;
    if (key.startsWith("on")) element.addEventListener(key.slice(2), value);
    else if (key === "className") element.className = value;
    else element.setAttribute(key, value === true ? "" : String(value));
  }
  for (const child of children.flat(Infinity)) {
    if (child == null || child === false) continue;
    element.append(child instanceof Node ? child : String(child));
  }
  return element;
}

export function icon(name) {
  const span = node("span", { className: "a-icon", "aria-hidden": "true" });
  span.innerHTML = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round">${icons[name] || icons.list}</svg>`;
  return span;
}

export const button = (label, onclick, { tone = "", disabled = false, glyph, title, className = "", ...attrs } = {}) =>
  node("button", { className: `a-button ${tone} ${className}`, type: "button", onclick, disabled, title, ...attrs }, glyph && icon(glyph), label);
export const link = (label, href, tone = "") => node("a", { className: `a-button ${tone}`, href }, label);
export const badge = (value, tone = "") => node("span", { className: `a-badge ${tone}` }, value);
export const actions = (...items) => node("div", { className: "a-actions" }, items);
export const stack = (...items) => node("div", { className: "a-stack" }, items);
export const grid = (...items) => node("div", { className: "a-grid" }, items);
export const empty = (message = "暂无记录") => node("p", { className: "a-empty" }, message);
export const muted = (message) => node("span", { className: "a-muted" }, message);
export const code = (message) => node("pre", { className: "a-code", tabindex: "0" }, message);
export const section = (title, content, tools, subtitle) => node("section", { className: "a-card" },
  (title || subtitle || tools) && node("div", { className: title ? "a-card-head" : "a-card-tools" },
    (title || subtitle) && node("div", {}, title && node("h2", {}, title), subtitle && node("p", {}, subtitle)), tools), content);
// Compact query controls are separate from full editing forms and card content.
export function filterbar(content, tools) {
  if (content.tagName === "FORM") content.classList.add("a-filter-form");
  return node("div", { className: "a-filterbar" }, content, tools);
}
export const notice = (message, tone = "error") => node("p", { className: `a-notice ${tone}`, role: tone === "error" ? "alert" : "status" }, message);

export function table(columns, rows, { emptyText, paginate = 12, label = "记录" } = {}) {
  const body = node("tbody");
  const scroll = node("div", { className: "a-table-scroll", tabindex: "0", role: "region", "aria-label": label },
    node("table", {}, node("thead", {}, node("tr", {}, columns.map(title => node("th", { scope: "col", "data-column": title }, title)))), body));
  if (!rows.length) return empty(emptyText);
  if (columns.length === 2) scroll.classList.add("a-table-pairs");
  const previous = button("上一页", () => show(page - 1));
  const next = button("下一页", () => show(page + 1));
  const count = node("span", { className: "a-muted", "aria-live": "polite" });
  const size = paginate || rows.length;
  const last = Math.ceil(rows.length / size);
  let page = 1;
  function show(value) {
    page = Math.max(1, Math.min(last, value));
    body.replaceChildren(...rows.slice((page - 1) * size, page * size).map(row =>
      node("tr", {}, (typeof row === "function" ? row() : row).map((cell, index) => node("td", { "data-column": columns[index] }, cell ?? "—")))));
    count.textContent = `共 ${rows.length} 条 · 第 ${page} / ${last} 页`;
    previous.disabled = page === 1;
    next.disabled = page === last;
    currentTheme("analytics")?.motion?.list(body);
  }
  show(1);
  return node("div", {}, scroll, last > 1 && node("div", { className: "a-pagination" }, count, actions(previous, next)));
}

export function field(label, name, value = "", attrs = {}) {
  const { options, multiline, hint, ...attributes } = attrs;
  let input;
  if (options) {
    input = node("select", { name, ...attributes }, options.map(item => {
      const [id, text] = Array.isArray(item) ? item : [item, item];
      return node("option", { value: id }, text);
    }));
    input.prepend(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")));
    input.value = value;
  } else if (multiline) {
    input = node("textarea", { name, rows: multiline, ...attributes });
    input.defaultValue = input.value = value ?? "";
  } else if (attributes.type === "checkbox") {
    input = node("input", { ...attributes, name, checked: Boolean(value) });
    return node("label", { className: "a-check" }, input, node("span", {}, label), hint && muted(hint));
  } else {
    input = node("input", { name, value: value ?? "", ...attributes });
  }
  if (!input.id) input.id = `a-field-${++fieldID}`;
  const helper = hint && node("span", { className: "a-field-hint", id: `${input.id}-hint` }, hint);
  if (helper) input.setAttribute("aria-describedby", helper.id);
  return node("div", { className: "a-field" }, node("label", { className: "a-field-label", for: input.id }, label), input, helper);
}

export const formData = form => Object.fromEntries(new FormData(form));
export const pathID = value => encodeURIComponent(value);
export function query(path, params) {
  const values = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value != null && value !== "") values.set(key, value);
  });
  return values.size ? `${path}?${values}` : path;
}
export const list = value => Array.isArray(value) ? value : [];

export async function copyText(value) {
  if (navigator.clipboard?.writeText) {
    try { await navigator.clipboard.writeText(String(value)); return; }
    catch { /* HTTP previews and browser restrictions use native selection. */ }
  }
  const active = document.activeElement;
  const source = node("textarea", { className: "a-clipboard", readonly: true });
  source.value = String(value);
  (document.querySelector('dialog:modal') || document.body).append(source);
  source.focus({ preventScroll: true }); source.select();
  try { if (!document.execCommand('copy')) throw new Error('浏览器拒绝了剪贴板操作'); }
  finally { source.remove(); active?.focus?.({ preventScroll: true }); }
}

// Lock the document without shifting its content when a native modal opens.
export function openDialog(box) {
  if (!document.querySelector('dialog:modal,.a-modal-tail'))
    document.documentElement.style.setProperty('--a-modal-scrollbar-gap', `${innerWidth - document.documentElement.getBoundingClientRect().width}px`);
  box.showModal();
}
