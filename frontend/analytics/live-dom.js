// Stable, keyed updates retain controls, native selection and scroll containers.
// Handlers are rebound to the latest resource without replacing live elements.
const events = new WeakMap();
export function listen(element, type, handler) {
  let bindings = events.get(element);
  if (!bindings) { bindings = new Map(); events.set(element, bindings); }
  if (bindings.has(type)) { bindings.get(type).handler = handler; return; }
  const binding = { handler };
  bindings.set(type, binding);
  element.addEventListener(type, event => binding.handler(event));
}
const key = element => element?.nodeType === 1 ? element.getAttribute('data-refresh-key') || (element.tagName === 'IMG' ? element.getAttribute('src') : '') || element.getAttribute('data-agent-id') || element.id || '' : '';
export function patch(current, fresh) {
  if (current.nodeType !== fresh.nodeType || current.nodeName !== fresh.nodeName) { current.replaceWith(fresh); return fresh; }
  if (current.nodeType === 3) { if (current.data !== fresh.data) current.data = fresh.data; return current; }
  if (current.nodeType !== 1) return current;
  if (current.reconcileTable?.(fresh)) return current;
  const bindings = events.get(fresh);
  bindings?.forEach((binding, type) => listen(current, type, binding.handler));
  // Edits, focus and disclosure state belong to the reader.
  const control = current.matches('input,textarea,select');
  for (const attr of [...current.attributes]) {
    if (control && ['value', 'checked', 'selected'].includes(attr.name) || current.tagName === 'DETAILS' && attr.name === 'open') continue;
    if (!fresh.hasAttribute(attr.name)) current.removeAttribute(attr.name);
  }
  for (const attr of fresh.attributes) {
    if (control && ['value', 'checked', 'selected'].includes(attr.name) || current.tagName === 'DETAILS' && attr.name === 'open') continue;
    if (current.getAttribute(attr.name) !== attr.value) current.setAttribute(attr.name, attr.value);
  }
  if (control) return current;
  const previous = [...current.childNodes], used = new Set();
  const keyed = new Map(previous.filter(child => key(child)).map(child => [key(child), child]));
  const children = [...fresh.childNodes].map((child, index) => {
    let match = key(child) ? keyed.get(key(child)) : previous[index];
    if (used.has(match) || key(match) !== key(child) || match?.nodeName !== child.nodeName) match = null;
    if (!match) return child;
    used.add(match); return patch(match, child);
  });
  const retained = new Set(children);
  previous.forEach(child => { if (!retained.has(child)) child.remove(); });
  children.forEach((child, index) => {
    if (current.childNodes[index] !== child) {
      if (current.moveBefore && child.parentNode === current && current.isConnected) current.moveBefore(child, current.childNodes[index] || null);
      else current.insertBefore(child, current.childNodes[index] || null);
    }
  });
  return current;
}

export function poll(ctx, run, delay = 2000) {
  let timer, running = false;
  const schedule = () => { timer = setTimeout(tick, typeof delay === 'function' ? delay() : delay); };
  const stop = () => clearTimeout(timer);
  async function tick() {
    stop();
    if (!ctx.current() || running) return;
    running = true;
    try { if (!document.hidden) await run(); }
    catch (error) { if (ctx.current() && error.name !== 'AbortError') ctx.onRefreshError?.(error); }
    finally { running = false; if (ctx.current()) schedule(); }
  }
  document.addEventListener('visibilitychange', tick, { signal: ctx.signal });
  ctx.signal.addEventListener('abort', stop, { once: true });
  schedule();
  return tick;
}
