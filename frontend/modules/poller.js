export function createPoller({
  run,
  isActive,
  delay,
  setTimer = (callback, timeout) => setTimeout(callback, timeout),
  clearTimer = (timer) => clearTimeout(timer),
}) {
  let timer = null;
  let running = false;
  let queued = false;
  const schedule = () => {
    clearTimer(timer);
    timer = null;
    if (!isActive()) return;
    timer = setTimer(trigger, delay());
  };
  const trigger = async () => {
    clearTimer(timer);
    timer = null;
    if (!isActive()) return;
    if (running) {
      queued = true;
      return;
    }
    running = true;
    try {
      do {
        queued = false;
        await run();
      } while (queued && isActive());
    } finally {
      running = false;
      schedule();
    }
  };
  return {
    start: schedule,
    trigger,
    stop() {
      clearTimer(timer);
      timer = null;
      queued = false;
    },
    timerCount: () => (timer == null ? 0 : 1),
  };
}

