// Coalesce route changes that arrive while a render is in flight. The active
// render finishes once, then the latest location/state is rendered exactly
// once instead of silently dropping navigation events.
export function createLatestRenderScheduler(renderOnce) {
  let running = false;
  let pending = false;
  let active = null;

  return function scheduleRender() {
    pending = true;
    if (running) return active;

    running = true;
    active = (async () => {
      try {
        while (pending) {
          pending = false;
          await renderOnce();
        }
      } finally {
        running = false;
        active = null;
      }
    })();
    return active;
  };
}
