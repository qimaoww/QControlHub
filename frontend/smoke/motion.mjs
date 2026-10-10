import assert from "node:assert/strict";
import { createMotionController } from "../modules/motion.js";

export async function run() {
  const timers = new Map(), listeners = new Set();
  let removed, observers = 0, next = 0;
  const preference = {
    matches: false,
    addEventListener: (_type, fn) => listeners.add(fn),
    removeEventListener: (_type, fn) => listeners.delete(fn),
  };
  const controller = createMotionController({
    media: () => preference,
    style: () => ({ opacity: ".42", getPropertyValue: name => name === "--motion-base" ? ".2s" : "ease-out" }),
    setTimer: (fn, delay) => { assert.equal(delay, 280); timers.set(++next, fn); return next; },
    clearTimer: id => timers.delete(id),
    observe: fn => { removed = fn; observers++; return () => observers--; },
  });
  const element = () => ({
    isConnected: true, calls: [], frames: [],
    animate(frames, options) {
      assert.equal(options.duration, 200, "CSS tokens are shared with native animations");
      assert.equal(options.fill, "none", "finished animation never masks hover/drag");
      const animation = { cancel() { this.oncancel?.(); } };
      this.calls.push(animation);
      this.frames.push(frames);
      return animation;
    },
  });
  const a = element(), b = element();
  let settled = 0;
  const animate = node => controller.animate(node, [{ opacity: 0 }, { opacity: 1 }], { onSettled: () => settled++ });
  const cancelFirst = animate(a);
  animate(a);
  assert.equal(a.frames.at(-1)[0].opacity, ".42", "interrupted motion resumes from current paint");
  assert.equal(settled, 1, "superseded motion settles the previous owner");
  cancelFirst();
  assert.equal(controller.activeCount(), 1, "stale cancellation cannot stop the newer animation");
  animate(b);
  assert.equal(observers, 1, "all animations share one removal observer");
  assert.equal(listeners.size, 1, "preference listener is shared and bounded");
  a.isConnected = false;
  removed();
  assert.equal(controller.activeCount(), 1, "removed element is released");
  preference.matches = true;
  for (const fn of [...listeners]) fn();
  assert.equal(controller.activeCount(), 0, "live preference change snaps to the final DOM state");
  assert.equal(timers.size, 0);
  assert.equal(listeners.size, 0);
  assert.equal(observers, 0);
  animate(b);
  assert.equal(b.calls.length, 1, "reduced motion does not call Element.animate");
  preference.matches = false;
  animate(b);
  const fallback = [...timers.values()][0];
  fallback(); fallback();
  assert.equal(controller.activeCount(), 0, "lost finish event has a bounded cleanup");
  assert.equal(timers.size, 0);
  animate(b);
  b.calls.at(-1).onfinish();
  assert.equal(timers.size, 0, "native finish cleans the fallback");
  animate(b);
  b.calls.at(-1).oncancel();
  assert.equal(controller.activeCount(), 0, "external cancellation releases ownership");
  assert.equal(observers, 0);
  const shadowHost = {};
  b.getRootNode = () => ({ host: shadowHost });
  animate(b);
  controller.cancel({ contains: node => node === shadowHost });
  assert.equal(controller.activeCount(), 0, "container cancellation crosses an isolated exit shadow boundary");
  assert.equal(timers.size, 0);

  let releaseReady;
  const pending = element();
  const native = pending.animate;
  pending.animate = function (...args) {
    const animation = native.apply(this, args);
    animation.ready = new Promise(resolve => { releaseReady = resolve; });
    return animation;
  };
  animate(pending);
  assert.equal(timers.size, 0, "render work before playback readiness cannot spend the fallback budget");
  releaseReady(); await Promise.resolve();
  assert.equal(timers.size, 1, "playback readiness starts the bounded deadline");
  controller.cancel(pending);
  assert.equal(timers.size, 0);
}
