// One account-bound queue, shared by all routes. Only changed fields are sent;
// a revision conflict rebases that patch onto the latest server document.
export function createUIPreferences({ request, isCurrent = () => true, report = () => {}, delay = 350 }) {
  let values = {}, revision = 0, loaded = false, disposed = false, pending, saving, timer, sequence = 0, failureShown = false;
  const dirty = new Map(), listeners = new Set(), lifetime = new AbortController();
  const active = () => !disposed && isCurrent();
  const keyOf = (page, key) => `${page}/${key}`;
  const snapshot = page => {
    const result = { ...values[page] };
    for (const item of dirty.values()) if (item.page === page) result[item.key] = item.value;
    return result;
  };
  const failed = (error, reading = false) => {
    if (active() && error.name !== 'AbortError' && !failureShown) {
      failureShown = true;
      report(reading ? '界面偏好读取失败，当前选择仍可使用；刷新后重试。' : '界面偏好未确认保存，当前选择已保留；点击页面刷新重试。');
    }
  };
  async function call(options = {}) {
    const timeout = new AbortController();
    const stop = setTimeout(() => timeout.abort(), 8000);
    try { return await request('/preferences/ui', { ...options, signal: AbortSignal.any([lifetime.signal, timeout.signal]) }); }
    catch (error) {
      if (timeout.signal.aborted && !lifetime.signal.aborted) throw Object.assign(new Error('偏好请求超时'), { status: 408 });
      throw error;
    } finally { clearTimeout(stop); }
  }
  async function load(force = false) {
    if (!active()) return;
    if (pending) return pending;
    if (loaded && !force) return;
    pending = (async () => {
      const response = await call();
      if (!active()) return;
      if (response.revision >= revision) { values = response.values; revision = response.revision; }
      loaded = true; failureShown = false;
      listeners.forEach(listener => listener());
    })().finally(() => { pending = null; });
    return pending;
  }
  function patch(page, fields) {
    if (!active()) return;
    for (const [key, value] of Object.entries(fields)) {
      if (value === undefined) continue;
      const id = keyOf(page, key), existing = dirty.get(id);
      if (existing && Object.is(existing.value, value)) continue;
      dirty.set(id, { page, key, value, sequence: ++sequence });
    }
    clearTimeout(timer);
    if (dirty.size) timer = setTimeout(() => void flush(), delay);
  }
  async function flush() {
    clearTimeout(timer);
    if (!active() || !dirty.size) return;
    if (saving) return saving;
    saving = (async () => {
      try {
        // When already loaded, start the keepalive write synchronously. A
        // pagehide handler must not yield until the request has been sent.
        if (!loaded) await load();
        for (let attempt = 0; active() && dirty.size && attempt < 4; attempt++) {
          const batch = [...dirty.values()], patch = {};
          for (const item of batch) (patch[item.page] ||= {})[item.key] = item.value;
          try {
            const response = await call({ method: 'PATCH', body: { values: patch, revision }, keepalive: true });
            if (!active()) return;
            if (response.revision >= revision) { values = response.values; revision = response.revision; }
            failureShown = false;
            for (const item of batch) if (dirty.get(keyOf(item.page, item.key))?.sequence === item.sequence) dirty.delete(keyOf(item.page, item.key));
          } catch (error) {
            if (!active() || error.status === 401 || error.status === 403) throw error;
            if (error.status === 409 || !error.status || error.status === 408 || error.status >= 500) {
              // An interrupted response may already have committed. Read back
              // before retrying; never assume a timeout means no write.
              await load(true);
              if (!active()) return;
              for (const item of batch) if (dirty.get(keyOf(item.page, item.key))?.sequence === item.sequence && Object.is(values[item.page]?.[item.key], item.value)) dirty.delete(keyOf(item.page, item.key));
              if (error.status === 409 || !dirty.size) continue;
            }
            throw error;
          }
        }
        if (active() && dirty.size) throw new Error('偏好并发更新，请重试');
      } catch (error) { failed(error); }
    })().finally(() => { saving = null; });
    return saving;
  }
  return {
    get: snapshot, patch, flush,
    ready: () => load().catch(error => failed(error, true)),
    refresh: () => load(true).catch(error => failed(error, true)),
    subscribe(callback) { listeners.add(callback); return () => listeners.delete(callback); },
    dispose() { disposed = true; clearTimeout(timer); lifetime.abort(); dirty.clear(); listeners.clear(); values = {}; },
  };
}
