import assert from 'node:assert/strict';
import { createAnalyticsMotion } from '../analytics/motion.js';

// Exercise the shipped motion owner. Native modal paint, disclosure, focus,
// reduced-motion preference changes and pointer geometry also run in Chrome.
export async function run() {
  const names = ['document', 'window', 'MutationObserver', 'getComputedStyle'];
  const originals = names.map(name => [name, Object.getOwnPropertyDescriptor(globalThis, name)]);
  const timers = new Map(), disposers = [], suspensions = [];
  let observer, observers = 0, next = 0, enabled = true;
  Object.assign(globalThis, {
    document: { body: {} }, window: {},
    getComputedStyle: () => ({ opacity: '.42', translate: '0 0' }),
    MutationObserver: class {
      constructor(callback) { observer = callback; }
      observe() { observers++; }
      disconnect() { observers--; }
    },
  });
  const scope = {
    enabled: () => enabled, current: () => true, listen() {},
    timer(callback, delay) { const id = ++next; timers.set(id, { callback, delay }); return id; },
    clearTimer: id => timers.delete(id),
    onSuspend: callback => suspensions.push(callback), own: callback => disposers.push(callback),
  };
  const element = () => ({
    isConnected: true, parentElement: null, calls: [], frames: [],
    animate(frames, options) {
      assert.equal(options.duration, 240);
      const animation = { ready: Promise.resolve(), cancel() { this.oncancel?.(); } };
      this.calls.push(animation); this.frames.push(frames); return animation;
    },
  });
  try {
    const motion = createAnalyticsMotion(scope), a = element(), b = element();
    motion.list(a); const cancelFirst = a.calls[0].oncancel;
    motion.list(a);
    assert.equal(a.frames[1][0].opacity, '.42', 'interruption continues from current paint');
    cancelFirst();
    assert.equal(timers.size, 1, 'stale completion must not cancel the newer owner');
    motion.list(b); await Promise.resolve();
    assert.equal(observers, 1, 'animations share the native lifecycle observer');
    a.isConnected = false; observer([]);
    assert.equal(timers.size, 1, 'removed elements release their deadline');
    suspensions.forEach(callback => callback());
    assert.equal(timers.size, 0, 'suspending the theme releases every active animation');
    enabled = false; motion.list(b);
    assert.equal(b.calls.length, 1, 'disabled/reduced motion leaves the committed DOM immediate');
    enabled = true; motion.list(b); await Promise.resolve();
    const fallback = [...timers.values()][0];
    assert.equal(fallback.delay, 360);
    fallback.callback(); fallback.callback();
    assert.equal(timers.size, 0, 'lost native finish events have idempotent bounded cleanup');
    motion.list(b); b.calls.at(-1).onfinish();
    assert.equal(timers.size, 0, 'native completion cleans the deadline');
    motion.list(b); b.calls.at(-1).oncancel();
    assert.equal(timers.size, 0, 'external cancellation releases the owner');
    let ready;
    const pending = element(), animate = pending.animate;
    pending.animate = function (...args) {
      const animation = animate.apply(this, args);
      animation.ready = new Promise(resolve => { ready = resolve; }); return animation;
    };
    motion.list(pending);
    assert.equal([...timers.values()][0].delay, 1240, 'unready playback still has a bounded safety deadline');
    ready(); await Promise.resolve();
    assert.equal([...timers.values()][0].delay, 360, 'normal deadline begins at playback readiness');
    motion.cancel(); assert.equal(timers.size, 0);
    const failed = element(); failed.animate = () => { throw new Error('presentation unavailable'); };
    assert.doesNotThrow(() => motion.list(failed));
    assert.equal(timers.size, 0, 'presentation failure must not block business state');
    motion.list(b); disposers.forEach(callback => callback());
    assert.equal(timers.size, 0); assert.equal(observers, 0);
  } finally {
    for (const [name, descriptor] of originals) {
      if (descriptor) Object.defineProperty(globalThis, name, descriptor); else delete globalThis[name];
    }
  }
}
