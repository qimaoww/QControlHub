// Own presentation resources, never requests, business commits or focus policy.
let active;
const sheets = { analytics: "/analytics/theme.css" };
export const currentTheme = id => !id || active?.id === id ? active : null;
export function disposeTheme() { active?.dispose(); }
export function mountTheme(id, createMotion) {
  if (!Object.hasOwn(sheets, id)) throw new Error("未知主题");
  disposeTheme();
  const abort = new AbortController(), cleanups = new Set(), suspensions = new Set();
  const frames = new Set(), timers = new Set();
  const media = globalThis.matchMedia?.("(prefers-reduced-motion: reduce)");
  let alive = true, allowed = true;
  const root = document.documentElement;
  const links = [...document.querySelectorAll('link[rel="stylesheet"]')].filter(link =>
    new URL(link.href, location.href).pathname.endsWith(sheets[id]));
  links.forEach(link => { link.dataset.themeOwner = id; link.disabled = false; });
  root.dataset.uiTheme = id;
  const cancelScheduled = () => {
    frames.forEach(frame => cancelAnimationFrame(frame)); frames.clear();
    timers.forEach(timer => clearTimeout(timer)); timers.clear();
  };
  const scope = {
    id, signal: abort.signal,
    current: () => alive && active === scope,
    enabled: () => scope.current() && allowed && !media?.matches && !document.hidden,
    own(cleanup) { cleanups.add(cleanup); return () => cleanups.delete(cleanup); },
    onSuspend(cleanup) { suspensions.add(cleanup); return () => suspensions.delete(cleanup); },
    frame(callback) {
      if (!scope.enabled()) return null;
      const frame = requestAnimationFrame(() => { frames.delete(frame); if (scope.enabled()) callback(); });
      frames.add(frame); return frame;
    },
    timer(callback, delay) {
      if (!scope.current()) return null;
      const timer = setTimeout(() => { timers.delete(timer); if (scope.current()) callback(); }, delay);
      timers.add(timer); return timer;
    },
    clearTimer(timer) { clearTimeout(timer); timers.delete(timer); },
    listen(target, event, callback, options = {}) {
      target?.addEventListener(event, callback, { ...options, signal: abort.signal });
    },
    cancel() {
      if (active !== scope) return;
      cancelScheduled();
      suspensions.forEach(cleanup => { try { cleanup(); } catch { /* Presentation is optional. */ } });
      document.getAnimations?.().forEach(animation => { try { animation.cancel(); } catch { /* Already detached. */ } });
    },
    setEnabled(value) { allowed = Boolean(value); synchronize(); },
    dispose() {
      if (!alive) return;
      alive = false;
      abort.abort();
      scope.cancel();
      cleanups.forEach(cleanup => { try { cleanup(); } catch { /* Continue releasing the remaining resources. */ } });
      cleanups.clear(); suspensions.clear();
      links.forEach(link => { link.disabled = true; });
      if (active === scope) { active = null; delete root.dataset.uiTheme; delete root.dataset.themeMotion; }
    },
  };
  function synchronize() {
    if (!scope.current()) return;
    root.dataset.themeMotion = scope.enabled() ? "on" : "off";
    if (!scope.enabled()) scope.cancel();
  }
  active = scope;
  scope.listen(media, "change", synchronize);
  scope.listen(document, "visibilitychange", synchronize);
  scope.listen(window, "pagehide", () => scope.dispose(), { once: true });
  synchronize();
  try { scope.motion = createMotion(scope); }
  catch { scope.motion = null; /* The theme remains usable without animation. */ }
  return scope;
}
// A cached document has already released its presentation lifetime.
globalThis.addEventListener?.("pageshow", event => { if (event.persisted) location.reload(); });
