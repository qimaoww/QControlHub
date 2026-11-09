export function createPoller({
  run,
  isActive,
  delay,
  coalesce = true,
  setTimer = (callback, timeout) => setTimeout(callback, timeout),
  clearTimer = (timer) => clearTimeout(timer),
}) {
  let timer = null, work = null, queued = false, generation = 0, enabled = true;
  const cancelTimer = () => { clearTimer(timer); timer = null; };
  const schedule = () => {
    cancelTimer();
    if (!enabled || !isActive()) return;
    timer = setTimer(trigger, Math.max(100, Number(delay()) || 1000));
  };
  const trigger = () => {
    cancelTimer();
    if (!isActive()) return Promise.resolve();
    enabled = true;
    if (work) {
      if (coalesce) queued = true;
      return coalesce ? work : Promise.resolve();
    }
    const epoch = generation;
    // Publish ownership before invoking a possibly synchronous failing run.
    work = (async () => {
      await Promise.resolve();
      try {
        if (!enabled || !isActive() || epoch !== generation) return;
        queued = false;
        await run();
        // At most one immediate follow-up, even under continuous triggers.
        // Further activity waits for the normal delay instead of a hot loop.
        if (queued && isActive() && epoch === generation) { queued = false; await run(); }
      } finally {
        work = null; queued = false;
        if (enabled) schedule();
      }
    })();
    return work;
  };
  return {
    start() { enabled = true; if (!work) schedule(); },
    trigger,
    stop() { enabled = false; generation++; cancelTimer(); queued = false; },
    timerCount: () => (timer == null ? 0 : 1),
  };
}
