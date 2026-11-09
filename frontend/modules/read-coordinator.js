import { requestCoreLogJSON } from "./core-log-read.js";

// Owned by one authenticated session. Completed values stay with the existing
// domain caches: sharing an in-flight read never extends a snapshot's lifetime.
export function createReadCoordinator(transport, { capacity = 128, timeout = 15000 } = {}) {
  const pending = new Map(), active = new Set();
  const canceled = () => new DOMException("Read canceled", "AbortError");
  const remove = entry => {
    if (pending.get(entry.key) === entry) pending.delete(entry.key);
    active.delete(entry);
  };
  function invalidate(matches = () => true) {
    for (const entry of [...active]) if (matches(entry.key)) {
      remove(entry);
      entry.controller.abort();
    }
  }
  function read(path, options = {}) {
    const method = String(options.method || 'GET').toUpperCase();
    // Never share writes, conditional entities, response callbacks or domain
    // readers with independent preview, pagination and incremental contracts.
    const share = method === 'GET' && !options.conditional && !options.onResponse && !options.headers
      && options.body === undefined && !/^\/(core-logs|client-connections)(?:[/?]|$)/.test(path)
      && Object.keys(options).every(key => ['signal', 'method', 'fresh'].includes(key));
    if (method !== 'GET') return transport(path, options);
    const signal = options.signal;
    if (signal?.aborted) return Promise.reject(signal.reason || canceled());
    let entry = share && !options.fresh ? pending.get(path) : null;
    if (!entry) {
      if (active.size >= capacity) return Promise.reject(new Error("读取请求过多，请稍后重试。"));
      const controller = new AbortController();
      entry = { key: path, controller, readers: new Set() };
      active.add(entry);
      if (share) pending.set(path, entry);
      const owned = entry;
      const { fresh, ...request } = options;
      void requestCoreLogJSON(transport, path, { ...request, signal: controller.signal, timeout })
        .then(value => {
          remove(owned);
          if (controller.signal.aborted) throw canceled();
          // Make isolated values only on a real overlap, before resolving any
          // reader. A page's mutable node models must never affect another.
          const readers = [...owned.readers];
          const values = readers.length > 1 ? readers.map(() => structuredClone(value)) : [value];
          readers.forEach((reader, index) => reader.resolve(values[index]));
        })
        .catch(error => { remove(owned); owned.readers.forEach(reader => reader.reject(error)); })
        .finally(() => {
          remove(owned);
          owned.readers.forEach(reader => reader.dispose());
          owned.readers.clear();
        });
    }
    if (entry.readers.size >= capacity) return Promise.reject(new Error("读取请求过多，请稍后重试。"));
    return new Promise((resolve, reject) => {
      const reader = { resolve, reject, dispose: () => signal?.removeEventListener('abort', abort) };
      const abort = () => {
        reader.dispose(); entry.readers.delete(reader);
        reject(signal.reason || canceled());
        if (!entry.readers.size) { remove(entry); entry.controller.abort(); }
      };
      entry.readers.add(reader);
      signal?.addEventListener('abort', abort, { once: true });
    });
  }
  return { read, invalidate, clear: () => invalidate(), size: () => active.size };
}

// Preference migration must not cancel unrelated first-page reads. Task
// creation likewise cannot invalidate the status of a different running task.
export function readsAffectedByWrite(path) {
  const resource = path.split('?')[0];
  if (resource.startsWith('/preferences/')) return key => key.split('?')[0] === resource;
  if (resource.startsWith('/tasks')) {
    const task = resource.match(/^\/tasks\/([^/]+)/)?.[1];
    return key => /^\/(agents|configs|deployments|client-access|traffic-policies|traffic-endpoints|overview)(?:[/?]|$)/.test(key)
      || /^\/tasks(?:\?|$)/.test(key) || Boolean(task && key.split('?')[0] === `/tasks/${task}`);
  }
  if (resource.startsWith('/settings')) return key => /^\/settings(?:[/?]|$)/.test(key);
  return () => true;
}
