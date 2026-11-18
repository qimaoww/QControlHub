const databaseName = "qcontrolhub:connections";
const storeName = "pages";
const scopeRange = scope => IDBKeyRange.bound([scope], [scope, []]);

// Old tab entries are per login and cannot be used as persistent baselines.
export function clearConnectionStorage() {
  try {
    for (const key of Object.keys(globalThis.sessionStorage)) {
      if (key.startsWith(databaseName + ":")) globalThis.sessionStorage.removeItem(key);
    }
  } catch { /* Storage can be disabled. */ }
}

export function sameConnectionValue(a, b) {
  if (a === b) return true;
  if (!a || !b || typeof a !== "object" || typeof b !== "object" || Array.isArray(a) !== Array.isArray(b)) return false;
  const keys = Object.keys(a);
  return keys.length === Object.keys(b).length && keys.every(key => Object.hasOwn(b, key) && sameConnectionValue(a[key], b[key]));
}

function openConnectionDatabase() {
  return new Promise((resolve, reject) => {
    const request = globalThis.indexedDB?.open(databaseName, 1);
    if (!request) { resolve(null); return; }
    request.onupgradeneeded = () => {
      const store = request.result.createObjectStore(storeName, { keyPath: ["scope", "kind", "key"] });
      store.createIndex("at", "at");
      store.createIndex("position", ["scope", "kind", "key", "observedAt"]);
    };
    request.onsuccess = () => {
      request.result.onversionchange = () => request.result.close();
      resolve(request.result);
    };
    request.onerror = () => reject(request.error);
    request.onblocked = () => resolve(null);
  }).catch(() => null);
}

function transaction(db, mode, work) {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(storeName, mode);
    tx.onabort = () => reject(tx.error);
    const request = work(tx.objectStore(storeName));
    tx.oncomplete = () => resolve(request?.result);
  });
}

function trimPersistent(store, capacity, expired) {
  const count = store.count(), index = store.index("at");
  count.onsuccess = () => {
    let excess = count.result - capacity;
    index.openKeyCursor().onsuccess = event => {
      const cursor = event.target.result;
      if (!cursor || (excess <= 0 && cursor.key >= expired)) return;
      excess--;
      store.delete(cursor.primaryKey);
      cursor.continue();
    };
  };
}

// Avoid serializing result payloads just to estimate their size. Each page
// counts toward both the memory limit and a fixed share of the disk budget.
function estimatedSize(value, limit) {
  let remaining = limit;
  // Connection pages contain shallow JSON. Stop at the budget even for an
  // exceptionally wide array or an unexpected nested/cyclic stored entry.
  function visit(item, depth) {
    if (typeof item === "string") remaining -= item.length * 2 + 16;
    else if (!item || typeof item !== "object") remaining -= 16;
    else if (depth === 0) remaining = -1;
    else if (Array.isArray(item)) {
      remaining -= 32 + item.length * 16;
      for (let i = 0; i < item.length && remaining >= 0; i++) visit(item[i], depth - 1);
    } else {
      remaining -= 32;
      for (const key in item) {
        if (remaining < 0) break;
        if (!Object.hasOwn(item, key)) continue;
        remaining -= 16 + key.length * 2;
        visit(item[key], depth - 1);
      }
    }
  }
  visit(value, 12);
  return remaining < 0 ? Infinity : limit - remaining;
}

const validResult = result => result && Array.isArray(result.records) && Array.isArray(result.sources)
  && Array.isArray(result.timeline) && !result.preview && Number.isSafeInteger(result.ips) && result.ips >= 0
  && Number.isSafeInteger(result.flows) && result.flows >= 0 && result.records.every(row => row &&
    typeof row.agent_id === "string" && typeof row.client_ip === "string" && typeof row.engine === "string"
    && typeof row.first_seen === "string" && typeof row.last_seen === "string"
    && (row.endpoints === undefined || Array.isArray(row.endpoints) && row.endpoints.every(port => port && Number.isInteger(port.local_port))))
  && result.sources.every(source => source && typeof source.agent_id === "string")
  && ["next_cursor", "page_cursor"].every(key => result[key] === undefined || typeof result[key] === "string");

const validSelection = saved => saved?.filters && typeof saved.filters.date === "string"
  && ["day", "month"].includes(saved.filters.period) && typeof saved.cursor === "string"
  && Array.isArray(saved.cursors) && saved.cursors.length <= 100 && saved.cursors.every(value => typeof value === "string")
  && Array.isArray(saved.nodeOrder) && saved.nodeOrder.length <= 500 && saved.nodeOrder.every(value => typeof value === "string")
  && Array.isArray(saved.sources) && saved.sources.every(source => source && typeof source.agent_id === "string")
  && ["agent_id", "engine", "client_ip", "country_code", "include_non_public"].every(key => saved.filters[key] === undefined || typeof saved.filters[key] === "string");

export function createConnectionCache({ now = Date.now, ttl = 30_000, historyTtl = 24 * 60 * 60_000, maxEntries = 24, maxBytes = 4 * 1024 * 1024, database = openConnectionDatabase() } = {}) {
  const entries = new Map(), pending = new Set();
  let scope = "", selection = null, selectionTime = 0, generation = 0, bytes = 0, queuedBytes = 0;
  const diskPageBudget = Math.floor(maxBytes / maxEntries);
  const withinLifetime = at => Number.isFinite(at) && now() >= at && now() - at < historyTtl;
  const remove = key => {
    bytes -= entries.get(key)?.bytes || 0;
    entries.delete(key);
  };
  const retain = entry => {
    remove(entry.key);
    entries.set(entry.key, entry);
    bytes += entry.bytes;
  };
  const prune = () => {
    for (const [key, entry] of entries) if (!withinLifetime(entry.at)) remove(key);
    while (entries.size > maxEntries || bytes > maxBytes) remove(entries.keys().next().value);
  };
  const run = operation => {
    const task = Promise.resolve(database).then(db => db && operation(db)).catch(() => {});
    pending.add(task); void task.finally(() => pending.delete(task));
    return task;
  };
  const write = entry => {
    if (!scope || entry.bytes > diskPageBudget || queuedBytes + entry.bytes > maxBytes || pending.size >= maxEntries) return;
    const version = generation;
    queuedBytes += entry.bytes;
    void run(db => {
      if (version !== generation) return;
      return transaction(db, "readwrite", store => {
        const prefix = [entry.scope, entry.kind, entry.key];
        const versions = IDBKeyRange.bound(prefix, [...prefix, []]);
        const read = store.index("position").openKeyCursor(versions);
        read.onsuccess = () => {
          // A request started in another tab can finish later. Never publish
          // its older view over a newer read of the same authorized page.
          if (read.result && read.result.key[3] >= entry.observedAt) return;
          store.put(entry);
          trimPersistent(store, maxEntries, now() - historyTtl);
        };
      });
    }).finally(() => { queuedBytes -= entry.bytes; });
  };
  const reset = verifiedScope => {
    ++generation; scope = verifiedScope; entries.clear(); selection = null; selectionTime = 0; bytes = 0;
  };
  return {
    get scope() { return scope; },
    get selection() { return selection; },
    async restore(verifiedScope, current = () => true) {
      if (!current()) return;
      if (!verifiedScope) { reset(""); return; }
      if (scope !== verifiedScope) reset(verifiedScope);
      const version = generation;
      // IndexedDB clones each value before JavaScript can validate it. Walk
      // one page at a time instead of cloning the whole account's cache.
      await run(db => transaction(db, "readonly", store => {
        const request = store.openCursor(scopeRange(verifiedScope));
        let visited = 0;
        request.onsuccess = () => {
          const cursor = request.result;
          if (!cursor || generation !== version || !current()) return;
          const entry = cursor.value;
          if (entry.scope === scope && withinLifetime(entry.at) && Number.isFinite(entry.observedAt)
            && Number.isFinite(entry.bytes) && entry.bytes > 0 && entry.bytes <= diskPageBudget) {
            // Recount values rather than trusting persisted size metadata.
            const size = estimatedSize(entry, diskPageBudget);
            if (Number.isFinite(size)) {
              if (entry.kind === "page" && validResult(entry.result) && entry.result.cache_scope === scope
                && (entries.get(entry.key)?.observedAt ?? -Infinity) < entry.observedAt) {
                retain({ ...entry, bytes: size }); prune();
              }
              if (entry.kind === "position" && entry.observedAt > selectionTime && validSelection(entry.value)) {
                selection = entry.value; selectionTime = entry.observedAt;
              }
            }
          }
          if (++visited < maxEntries) cursor.continue();
        };
      }));
    },
    bind(verifiedScope) {
      if (verifiedScope && scope !== verifiedScope) reset(verifiedScope);
    },
    select(value, observedAt = now()) {
      if (observedAt < selectionTime) return;
      const entry = { scope, kind: "position", key: "", at: now(), observedAt, value, bytes: 0 };
      entry.bytes = estimatedSize(entry, diskPageBudget);
      if (!Number.isFinite(entry.bytes) || sameConnectionValue(selection, value)) return;
      selection = value; selectionTime = observedAt;
      write(entry);
    },
    get(key) {
      prune();
      const entry = entries.get(key);
      if (!entry) return null;
      entries.delete(key); entries.set(key, entry);
      const historical = Date.parse(new URLSearchParams(key).get("until")) <= entry.at;
      return { ...entry, historical, fresh: now() - entry.at < (historical ? historyTtl : ttl) };
    },
    set(key, result, at = now(), { observedAt = at, etag = "", unchanged = false } = {}) {
      const previous = entries.get(key);
      if (previous && observedAt < previous.observedAt) return;
      // In-memory confirmation may advance the token/time; only a changed
      // data page earns a structured clone and a durable write.
      const entry = { scope, kind: "page", key, result, at, observedAt, etag, bytes: 0 };
      entry.bytes = unchanged && previous ? previous.bytes : estimatedSize(entry, maxBytes);
      if (entry.bytes > maxBytes) { remove(key); return; }
      retain(entry);
      prune();
      if (!unchanged) write(entry);
    },
    preview(query) {
      prune();
      if (query.has("cursor")) return null;
      for (const [key, entry] of [...entries].reverse()) {
        const candidate = new URLSearchParams(key);
        if (candidate.has("cursor") || ["since", "until", "include_non_public", "group_by", "timeline", "bucket", "limit"].some(name => candidate.get(name) !== query.get(name)) || !sameConnectionValue(candidate.getAll("node_order"), query.getAll("node_order"))) continue;
        if (["agent_id", "engine", "client_ip", "country_code"].some(name => candidate.get(name) && candidate.get(name) !== query.get(name))) continue;
        const records = entry.result.records.filter(row => (!query.get("agent_id") || row.agent_id === query.get("agent_id")) && (!query.get("engine") || row.engine === query.get("engine")) && (!query.get("client_ip") || row.client_ip === query.get("client_ip")) && (!query.get("country_code") || !row.location?.non_public && row.location?.country_code === query.get("country_code")));
        if (!records.length) continue;
        return { records, sources: entry.result.sources.filter(source => !query.get("agent_id") || source.agent_id === query.get("agent_id")), timeline: [], preview: true, snapshotAt: entry.at };
      }
      return null;
    },
    clear() {
      const revoked = scope;
      reset("");
      if (revoked) void run(db => transaction(db, "readwrite", store => store.delete(scopeRange(revoked))));
    },
    flush() { return Promise.all([...pending]); },
  };
}
