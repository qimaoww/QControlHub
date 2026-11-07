import { listen, patch } from "./live-dom.js";
import { currentTheme } from "../modules/theme-lifecycle.js";
let fieldID = 0;
export function bindSelectionSurface(root, { selector, active = () => true, preserveControls = false, signal }) {
  const toggle = event => {
    if (!active()) return;
    const surface = event.target.closest(selector);
    if (!surface || !root.contains(surface)) return;
    const input = surface.querySelector('input[type=checkbox]');
    if (!input || input.matches(':disabled')) return;
    if (event.type === 'keydown') {
      if (event.target !== surface || ![' ', 'Enter'].includes(event.key) || event.repeat) return;
    } else {
      if (event.target === input || event.target.closest('label')?.contains(input)) return;
      if (preserveControls && event.target.closest('input,select,textarea,button,a,label,summary')) return;
    }
    event.preventDefault(); event.stopPropagation();
    input.click();
  };
  root.addEventListener('click', toggle, { capture: true, signal });
  root.addEventListener('keydown', toggle, { capture: true, signal });
}
const icons = {
  brand: '<path d="M3 12h4l3-7 4 14 3-7h4"/>',
  grid: '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
  server: '<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/>',
  file: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zm0 0v6h6M8 13h8m-8 4h8"/>',
  link: '<path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-2 2m3 6a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l2-2"/>',
  shield: '<path d="M12 22s8-4 8-11V5l-8-3-8 3v6c0 7 8 11 8 11zM9 12l2 2 4-4"/>',
  activity: '<path d="M3 17h3l3-10 4 13 3-8h5"/>',
  list: '<path d="M9 6h12M9 12h12M9 18h12M3 6h.01M3 12h.01M3 18h.01"/>',
  grip: '<path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01"/>',
  users: '<circle cx="9" cy="8" r="3"/><path d="M2 20v-2a4 4 0 0 1 4-4h6a4 4 0 0 1 4 4v2m4 0v-2a4 4 0 0 0-3-4m-2-9a3 3 0 0 1 0 6"/>',
  settings: '<path d="M4 6h16M4 12h16M4 18h16"/><path d="M9 4v4m6 2v4m-6 2v4"/>',
  palette: '<path d="M12 3a9 9 0 1 0 0 18h1.5a1.5 1.5 0 0 0 1.1-2.5 1.5 1.5 0 0 1 1.1-2.5H18a3 3 0 0 0 3-3 10 10 0 0 0-9-10zM7.5 12h.01M10 7.5h.01M15 7.5h.01M17.5 12h.01"/>',
  menu: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>',
  close: '<path d="m6 6 12 12M18 6 6 18"/>',
  copy: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"/>',
  refresh: '<path d="M20 7v5h-5M4 17v-5h5m11 0a8 8 0 0 0-14-5m-2 5a8 8 0 0 0 14 5"/>',
  logout: '<path d="M10 4H5v16h5m-1-8h12m-4-4 4 4-4 4"/>',
};

export function node(tag, attrs = {}, ...children) {
  const element = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === false || value == null) continue;
    if (key.startsWith("on")) listen(element, key.slice(2), value);
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
export function dragHandle(id, label, disabled = false) {
  const handle = node("button", { type: "button", className: "a-icon", "data-node-drag": id,
    "aria-label": `拖动调整 ${label} 的顺序`, "aria-pressed": "false", title: "拖动调整顺序；方向键移动，Esc 取消", disabled }, icon("grip"));
  handle.querySelector("svg").setAttribute("stroke-width", "2.6");
  return handle;
}
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
  return node("div", { className: "a-filterbar" }, content,
    content.tagName !== "FORM" && content.querySelectorAll("input,select").length > 1 ? actions(filterReset(content), tools) : tools);
}
export function filterReset(content) {
  const fields = [...content.querySelectorAll("input,select,textarea")].filter(field => field.name !== "format");
  const defaults = fields.map(field => field.type === "checkbox" ? field.checked : field.value);
  return button("重置", () => {
    fields.forEach((field, index) => { if (field.type === "checkbox") field.checked = defaults[index]; else field.value = defaults[index]; });
    fields.forEach(field => field.dispatchEvent(new Event(field.tagName === "SELECT" || field.type === "checkbox" ? "change" : "input", { bubbles: true })));
  });
}
// Read-only queries stay editable while loading; only the newest query may paint.
export function autoFilter(ctx, fields, load, resetValues = {}) {
  const feedback = notice("");
  const form = node("form", { className: "a-stack a-filter-form a-auto-filter" },
    node("fieldset", { className: "a-form-fields a-stack" }, fields), feedback);
  const controls = [...form.querySelectorAll("input,select,textarea")];
  const defaults = controls.map(field => resetValues[field.name] ?? (field.type === "checkbox" ? field.checked : field.value));
  form.append(actions(button("重置", () => form.reset())));
  let timer, controller, revision = 0;
  const cancel = () => { clearTimeout(timer); controller?.abort(); revision++; form.removeAttribute("aria-busy"); };
  ctx.signal.addEventListener("abort", cancel, { once: true });
  async function run(expected) {
    if (ctx.signal.aborted || expected !== revision || !form.checkValidity()) return;
    controller = new AbortController();
    const signal = controller.signal;
    const current = () => !signal.aborted && expected === revision && ctx.current();
    form.setAttribute("aria-busy", "true");
    feedback.textContent = "";
    try {
      await load(formData(form), form, {
        current, api: (path, options = {}) => ctx.api(path, { ...options, signal }),
      });
    } catch (error) {
      if (current() && error.name !== "AbortError") feedback.textContent = error.message;
    } finally { if (current()) form.removeAttribute("aria-busy"); }
  }
  function schedule(delay = 0) {
    cancel();
    const expected = revision;
    if (delay) timer = setTimeout(() => void run(expected), delay);
    else void run(expected);
  }
  form.addEventListener("submit", event => { event.preventDefault(); schedule(); });
  form.addEventListener("reset", event => {
    event.preventDefault();
    controls.forEach((field, index) => { if (field.type === "checkbox") field.checked = defaults[index]; else field.value = defaults[index]; });
    schedule();
  });
  form.addEventListener("keydown", event => {
    if (event.key === "Enter" && !event.isComposing && event.target.matches("input")) {
      event.preventDefault(); schedule();
    }
  });
  form.addEventListener("input", event => {
    if (event.isComposing) return;
    if (event.target.matches('input:not([type=checkbox]),textarea')) schedule(250);
  });
  form.addEventListener("compositionend", () => schedule(250));
  form.addEventListener("change", () => schedule());
  return form;
}
export const notice = (message, tone = "error") => node("p", { className: `a-notice ${tone}`, role: tone === "error" ? "alert" : "status" }, message);

export function table(columns, rows, { emptyText, paginate = 12, label = "记录", rowKey, motion = true, columnWidths } = {}) {
  const body = node("tbody");
  const scroll = node("div", { className: "a-table-scroll", tabindex: "0", role: "region", "aria-label": label },
    node("table", {}, columnWidths && node("colgroup", {}, columnWidths.map(width => node("col", { width }))),
      node("thead", {}, node("tr", {}, columns.map(title => node("th", { scope: "col", "data-column": title }, title)))), body));

  if (columns.length === 2) scroll.classList.add("a-table-pairs");
  const previous = button("上一页", () => show(page - 1));
  const next = button("下一页", () => show(page + 1));
  const count = node("span", { className: "a-muted", "aria-live": "polite" });
  let size = paginate || Math.max(1, rows.length);
  let last = Math.max(1, Math.ceil(rows.length / size));
  let page = 1;
  function show(value, animate = motion) {
    page = Math.max(1, Math.min(last, value));
    patch(body, node("tbody", {}, rows.slice((page - 1) * size, page * size).map((row, index) =>
      node("tr", rowKey ? { "data-refresh-key": rowKey(row, (page - 1) * size + index) } : {}, (typeof row === "function" ? row() : row).map((cell, index) => node("td", { "data-column": columns[index] }, cell ?? "—"))))));
    const summary = `共 ${rows.length} 条 · 第 ${page} / ${last} 页`;
    if (count.textContent !== summary) count.textContent = summary;
    previous.disabled = page === 1;
    next.disabled = page === last;
    if (animate) currentTheme("analytics")?.motion?.list(body);
  }
  show(1);
  const noRows = empty(emptyText);
  noRows.hidden = rows.length > 0;
  scroll.hidden = !rows.length;
  const pagination = node("div", { className: "a-pagination" }, count, actions(previous, next));
  pagination.hidden = last <= 1;
  const result = node("div", {}, noRows, scroll, pagination);
  result.tableState = { signature: JSON.stringify([columns, paginate, label, columnWidths, emptyText]), rows };
  result.updateRows = value => { rows = value; result.tableState.rows = value; if (!paginate) size = Math.max(1, rows.length); last = Math.max(1, Math.ceil(rows.length / size)); show(page, false); pagination.hidden = last <= 1; noRows.hidden = rows.length > 0; scroll.hidden = !rows.length; };
  result.reconcileTable = fresh => {
    if (result.tableState.signature !== fresh.tableState?.signature) return false;
    result.updateRows(fresh.tableState.rows); return true;
  };
  return result;
}

export function field(label, name, value = "", attrs = {}) {
  const { options, multiline, hint, suggestions, ...attributes } = attrs;
  let input;
  if (options) {
    input = node("select", { name, ...attributes }, options.map(item => {
      const [id, text] = Array.isArray(item) ? item : [item, item];
      return node("option", { value: id }, text);
    }));
    input.prepend(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")));
    input.value = value;
    input.addEventListener('keydown', event => {
      const option = document.activeElement;
      if (event.key !== 'Tab' || !input.matches(':open') || option?.tagName !== 'OPTION' || !input.contains(option) || option.matches(':disabled')) return;
      if (input.value !== option.value) {
        input.value = option.value;
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new Event('change', { bubbles: true }));
      }
    });
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
  const choices = suggestions?.length ? node('datalist', { id: `${input.id}-choices` },
    suggestions.map(value => node('option', { value }, value))) : null;
  if (choices) input.setAttribute('list', choices.id);
  const helper = hint && node("span", { className: "a-field-hint", id: `${input.id}-hint` }, hint);
  if (helper) input.setAttribute("aria-describedby", helper.id);
  return node("div", { className: "a-field" }, node("label", { className: "a-field-label", for: input.id }, label), input, helper, choices);
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
  const focusScope = new AbortController();
  const outside = event => {
    const rect = box.getBoundingClientRect();
    return event.target === box && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom);
  };
  let backdropPress = false;
  box.addEventListener('pointerdown', event => { backdropPress = outside(event); }, { signal: focusScope.signal });
  box.addEventListener('click', event => {
    const dismiss = backdropPress && outside(event); backdropPress = false;
    if (dismiss && box.dispatchEvent(new Event('cancel', { cancelable: true }))) box.close();
  }, { signal: focusScope.signal });
  box.addEventListener('keydown', event => {
    if (event.key !== 'Tab') return;
    const controls = [...box.querySelectorAll('a[href],button,input,select,textarea,summary,[tabindex]')]
      .filter(control => control.tabIndex >= 0 && !control.matches(':disabled,[inert]') && control.getClientRects().length && getComputedStyle(control).visibility !== 'hidden');
    const first = controls[0], last = controls.at(-1);
    if (first && event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (last && !event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }, { signal: focusScope.signal });
  box.addEventListener('close', () => focusScope.abort(), { once: true });
  box.showModal();
}
