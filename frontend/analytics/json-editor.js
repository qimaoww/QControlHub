// Keep the native textarea for selection, undo, IME, forms and accessibility.
// The visual layer never parses numbers or changes the submitted source.
import { readableConfigSource } from '../modules/code-format.js';
const editors = new WeakMap();
const jsonToken = /"(?:\\[\s\S]|[^"\\])*"\s*(?=:)|"(?:\\[\s\S]|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|\b(?:true|false|null)\b|[{}\[\]:,]/g;

function highlight(text) {
  const result = document.createDocumentFragment();
  let offset = 0;
  for (const match of text.matchAll(jsonToken)) {
    result.append(text.slice(offset, match.index));
    const token = document.createElement('span'), raw = match[0];
    token.className = `a-json-${raw.startsWith('"') ? text[match.index + raw.length] === ':' ? 'key' : 'string' : /^-?\d/.test(raw) ? 'number' : /^(true|false|null)$/.test(raw) ? 'literal' : 'punctuation'}`;
    token.textContent = raw;
    result.append(token);
    offset = match.index + raw.length;
  }
  result.append(text.slice(offset));
  return result;
}

export function bindJSONEditor(input, language = 'JSON', signal) {
  if (editors.has(input)) {
    const editor = editors.get(input); editor.update(language); return editor;
  }
  const host = document.createElement('div'); host.className = 'a-source-editor';
  const paint = document.createElement('pre'); paint.className = 'a-source-highlight';
  const gutter = document.createElement('pre'); gutter.className = 'a-source-lines';
  paint.setAttribute('aria-hidden', 'true'); gutter.setAttribute('aria-hidden', 'true');
  input.replaceWith(host); host.append(paint, gutter, input);
  let active = language === 'JSON', cached, lines = [], offsets = [], previousRange = '', dialog, disposed = false;
  const originalWrap = input.getAttribute('wrap');
  function render() {
    if (disposed || !active) return;
    const currentDialog = host.closest('dialog');
    if (currentDialog && dialog !== currentDialog) {
      dialog = currentDialog; dialog.addEventListener('close', dispose, { once: true });
    }
    const text = input.value;
    if (cached !== text) {
      cached = text; lines = text.split('\n'); offsets = [0];
      for (const line of lines) offsets.push(offsets.at(-1) + line.length + 1);
      previousRange = '';
    }
    const height = parseFloat(getComputedStyle(input).lineHeight) || 24;
    const first = Math.max(0, Math.floor(input.scrollTop / height) - 2);
    const last = Math.min(lines.length, first + Math.ceil((input.clientHeight || 480) / height) + 5);
    const range = `${first}:${last}`;
    if (range !== previousRange) {
      previousRange = range;
      // Bound highlighted DOM even for a long single line or a 2 MiB draft.
      const start = offsets[first] || 0, end = Math.min(offsets[last] || text.length, start + 50000);
      paint.replaceChildren(text.slice(0, start), highlight(text.slice(start, end)), text.slice(end) + '\n');
      gutter.textContent = '\n'.repeat(first) + lines.slice(first, last).map((_, i) => first + i + 1).join('\n') + '\n'.repeat(Math.max(1, lines.length - last + 1));
    }
    paint.scrollTop = gutter.scrollTop = input.scrollTop;
    paint.scrollLeft = input.scrollLeft;
  }
  const editor = { update(next = language) {
    language = next; active = language === 'JSON'; host.classList.toggle('a-source-json', active);
    paint.hidden = gutter.hidden = !active;
    if (active) input.setAttribute('wrap', 'off');
    else if (originalWrap === null) input.removeAttribute('wrap'); else input.setAttribute('wrap', originalWrap);
    previousRange = ''; render();
  } };
  // API adoption and reset paths assign value directly; repaint in the same
  // operation without dispatching input, touching baselines or moving focus.
  const value = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value');
  Object.defineProperty(input, 'value', { configurable: true,
    get() { return value.get.call(this); },
    set(text) { value.set.call(this, text); render(); },
  });
  input.addEventListener('input', render);
  input.addEventListener('scroll', render, { passive: true });
  input.form?.addEventListener('reset', () => queueMicrotask(render));
  const observer = new ResizeObserver(render); observer.observe(input);
  function dispose() {
    disposed = true; observer.disconnect(); signal?.removeEventListener('abort', dispose);
  }
  signal?.addEventListener('abort', dispose, { once: true });
  editors.set(input, editor); editor.update(language);
  return editor;
}

export function sourcePreview(text, engine) {
  const pre = document.createElement('pre'); pre.className = 'a-code'; pre.tabIndex = 0;
  text = readableConfigSource(text, engine);
  if (engine === 'mihomo') pre.textContent = text;
  else {
    pre.classList.add('a-json-preview');
    // Preview text stays selectable and contains no copied line numbers.
    const lines = text.split('\n');
    for (let i = 0; i < Math.min(lines.length, 2000); i++) {
      const line = document.createElement('span'); line.className = 'a-json-preview-line';
      line.append(highlight(lines[i]), i < lines.length - 1 ? '\n' : ''); pre.append(line);
    }
    if (lines.length > 2000) pre.append(lines.slice(2000).join('\n'));
  }
  return pre;
}
