import { createCardMoveScroll } from "../modules/card-move-scroll.js";

// Only the explicit handle owns ordering. Content retains native text selection
// and touch scrolling. Native motion lifts the record while live slots make
// room; only release commits the preview to the account preference.
export function bindNodeOrdering(ctx, root, { enabled, order, save, redraw, saved = '节点顺序已保存' }) {
  let source = '', target = '', saving = false, touch = null, pointer = null, startPoint, keyboard = false;
  let lastPoint, preview = [], draining = false, origin, lifted, vertical;
  const moves = [];
  let writing = Promise.resolve();
  const scroll = createCardMoveScroll(root, () => { if (lastPoint) point(lastPoint.x, lastPoint.y); });
  const handle = event => event.target.closest?.('[data-node-drag]');
  const active = () => Boolean(source || saving || draining);
  const mark = () => root.querySelectorAll('[data-node-drag]').forEach(control => control.setAttribute('aria-pressed', String(!!source && control.dataset.nodeDrag === target)));
  const clear = () => {
    scroll.stop(); lifted?.close(); lifted = null; origin = null; lastPoint = startPoint = null;
    source = target = ''; touch = pointer = null; keyboard = false; preview = [];
    mark();
  };
  const record = grip => grip.closest('article[data-agent-id],article[data-traffic-key],tr');
  const records = () => [...root.querySelectorAll('[data-node-drag]')].map(record).filter(item => item && item.getBoundingClientRect().width);
  const showPreview = () => {
    const items = records(), parents = new Set(items.map(item => item.parentElement));
    ctx.motion.reorder(items.filter(item => !item.querySelector(`[data-node-drag="${CSS.escape(source)}"]`)), () => {
      for (const parent of parents) {
        const children = items.filter(item => item.parentElement === parent);
        const keys = new Map(children.map(item => [item.querySelector('[data-node-drag]').dataset.nodeDrag, item]));
        preview.filter(id => keys.has(id)).forEach((id, index) => {
          const item = keys.get(id), before = parent.children[index] || null;
          if (item !== before) {
            if (parent.moveBefore) parent.moveBefore(item, before);
            else parent.insertBefore(item, before);
          }
        });
      }
    });
  };
  const point = (x, y) => {
    lastPoint = { x, y };
    if (!lifted && origin) {
      const item = records().find(item => item.querySelector('[data-node-drag]').dataset.nodeDrag === source);
      if (item) { vertical = item.tagName === 'TR'; lifted = ctx.motion.lift(item); }
    }
    lifted?.move(vertical ? 0 : x - origin.x, y - origin.y);
    // Hit the actual layout slots, ignoring the short arrival animation of a
    // displaced sibling. The floating surface never participates in hit tests.
    const found = records().find(item => {
      const rect = item.getBoundingClientRect();
      const [tx = 0, ty = 0] = getComputedStyle(item).translate.split(' ').map(value => parseFloat(value) || 0);
      return x >= rect.left - tx && x <= rect.right - tx && y >= rect.top - ty && y <= rect.bottom - ty;
    });
    const id = found?.querySelector('[data-node-drag]').dataset.nodeDrag;
    if (!id || id === source) return;
    const start = preview.indexOf(source), end = preview.indexOf(id);
    if (start < 0 || end < 0) return;
    target = id; preview.splice(start, 1); preview.splice(end, 0, source);
    showPreview(); mark();
  };
  const focus = id => [...root.querySelectorAll('[data-node-drag]')].find(control => control.dataset.nodeDrag === id)?.focus({ preventScroll: true });
  function commit(ids, id) {
    saving = true;
    writing = (async () => {
    try { await save(ids); if (ctx.current()) ctx.announce(saved); }
    catch (error) { if (ctx.current() && error.name !== 'AbortError') ctx.announce(error.orderUnconfirmed ? '顺序尚未确认，请重试。' : error.message, 'error'); }
    finally { saving = false; if (ctx.current()) { redraw(); focus(id); } }
    })();
    return writing;
  }
  async function finish(accept) {
    const from = source, ids = [...preview], original = order();
    clear();
    if (!accept || !ctx.current() || !ids.includes(from) || JSON.stringify(ids) === JSON.stringify(original)) {
      if (ctx.current()) { redraw(); focus(from); } return;
    }
    await commit(ids, from);
  }
  // Queue relative keyboard moves, then resolve each against the confirmed
  // order. A slow write must not silently discard the user's later keypresses.
  async function drain() {
    if (draining) return;
    draining = true;
    let focused;
    try {
      await writing;
      while (moves.length && ctx.current()) {
        const { id, direction } = moves.shift(), ids = order(), start = ids.indexOf(id), end = start + direction;
        focused = id;
        if (start < 0 || end < 0 || end >= ids.length) continue;
        ids.splice(start, 1); ids.splice(end, 0, id); await commit(ids, id);
      }
    } finally { moves.length = 0; draining = false; if (ctx.current()) { redraw(); focus(focused); } }
  }
  const begin = grip => { source = target = grip.dataset.nodeDrag; preview = [...order()]; grip.focus({ preventScroll: true }); mark(); };
  const on = (name, callback, options = {}) => root.addEventListener(name, callback, { ...options, signal: ctx.signal });
  on('dragstart', event => event.preventDefault());
  on('pointerdown', event => {
    const grip = handle(event);
    if (event.pointerType === 'touch' || event.button !== 0 || !grip || !enabled() || active()) return;
    event.preventDefault(); begin(grip); pointer = event.pointerId; startPoint = origin = { x: event.clientX, y: event.clientY };
    root.setPointerCapture(event.pointerId);
  });
  on('pointermove', event => {
    if (pointer !== event.pointerId || !source) return;
    if (startPoint && Math.hypot(event.clientX - startPoint.x, event.clientY - startPoint.y) < 5) return;
    startPoint = null; point(event.clientX, event.clientY); scroll.move(lastPoint);
  });
  on('pointerup', event => { if (pointer === event.pointerId && source) { const accept = !startPoint; void finish(accept); } });
  on('pointercancel', event => { if (pointer === event.pointerId && source) void finish(false); });
  on('lostpointercapture', event => { if (pointer === event.pointerId && source) void finish(false); });
  on('touchstart', event => {
    const grip = handle(event);
    if (!grip || !enabled() || active() || event.touches.length !== 1) return;
    event.preventDefault(); begin(grip); touch = event.changedTouches[0].identifier;
    const finger = event.changedTouches[0]; startPoint = origin = { x: finger.clientX, y: finger.clientY };
  }, { passive: false });
  on('touchmove', event => {
    const finger = [...event.changedTouches].find(item => item.identifier === touch);
    if (!source || !finger) return;
    event.preventDefault();
    if (startPoint && Math.hypot(finger.clientX - startPoint.x, finger.clientY - startPoint.y) < 5) return;
    startPoint = null; point(finger.clientX, finger.clientY); scroll.move(lastPoint);
  }, { passive: false });
  on('touchend', event => { const finger = [...event.changedTouches].find(item => item.identifier === touch); if (source && finger) { event.preventDefault(); void finish(!startPoint); } }, { passive: false });
  on('touchcancel', () => { if (source) void finish(false); });
  on('keydown', event => {
    if (event.key === 'Escape' && source) { event.preventDefault(); void finish(false); return; }
    const grip = handle(event);
    if (!grip || !enabled()) return;
    if ([' ', 'Enter'].includes(event.key) && (!active() || keyboard)) {
      event.preventDefault();
      if (keyboard) void finish(true);
      else { begin(grip); keyboard = true; preview = order(); }
      return;
    }
    const direction = ['ArrowUp', 'ArrowLeft'].includes(event.key) ? -1 : ['ArrowDown', 'ArrowRight'].includes(event.key) ? 1 : 0;
    if (!direction || source && !keyboard) return;
    event.preventDefault();
    if (keyboard) {
      const index = preview.indexOf(source), end = index + direction;
      if (end < 0 || end >= preview.length) return;
      target = preview[end]; preview.splice(index, 1); preview.splice(end, 0, source); showPreview(); mark();
    } else { moves.push({ id: grip.dataset.nodeDrag, direction }); void drain(); }
  });
  ctx.signal.addEventListener('abort', () => { moves.length = 0; clear(); }, { once: true });
  return { active };
}
