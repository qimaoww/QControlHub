const prefix = "qcontrolhub:connections:v1:", indexKey = `${prefix}index`;
export const connectionRetention = 300_000;
export function connectionSessionStorage() {
  try { return globalThis.sessionStorage; } catch { return null; }
}
export function clearConnectionStorage(storage = connectionSessionStorage()) {
  try {
    for (const key of Object.keys(storage || {})) if (key.startsWith(prefix)) storage.removeItem(key);
  } catch { /* Disabled storage must not prevent login or API reads. */ }
}
const validResult = result => result && Array.isArray(result.records) && Array.isArray(result.sources)
  && Array.isArray(result.timeline) && !result.preview
  && Number.isSafeInteger(result.ips) && result.ips >= 0 && Number.isSafeInteger(result.flows) && result.flows >= 0
  && ["next_cursor", "page_cursor"].every(key => result[key] === undefined || typeof result[key] === "string")
  && result.records.every(row => row && typeof row.agent_id === "string" && typeof row.client_ip === "string" && typeof row.engine === "string"
    && typeof row.first_seen === "string" && typeof row.last_seen === "string"
    && (row.endpoints === undefined || Array.isArray(row.endpoints) && row.endpoints.every(endpoint => endpoint && Number.isInteger(endpoint.local_port))))
  && result.sources.every(source => source && typeof source.agent_id === "string" && (source.agent_name === undefined || typeof source.agent_name === "string"));

// Persistence is tab/session scoped and can only be restored after a current
// server scope check. Preference IDs and usernames never authorize IP history.
export function createConnectionCache({ now = Date.now, ttl = 30_000, retention = connectionRetention, maxEntries = 24, maxBytes = 4 * 1024 * 1024, storage = null } = {}) {
  const entries = new Map(), dirty = new Set(), deleted = new Set();
  let bytes = 0, scope = "", selection = null, timer, restoring = false;
  const remove = key => {
    const entry = entries.get(key);
    if (entry) bytes -= entry.bytes;
    entries.delete(key); dirty.delete(key); deleted.add(key);
  };
  const prune = () => {
    for (const [key, entry] of entries) if (now() < entry.at || now() - entry.at >= retention) remove(key);
  };
  function flush() {
    clearTimeout(timer); timer = null;
    if (!storage || !scope) return;
    prune();
    try {
      // Write changed pages only. Location completion and filter changes never
      // serialize every retained result or change its original read time.
      for (const key of deleted) storage.removeItem(prefix + key);
      for (const key of dirty) {
        const entry = entries.get(key);
        if (entry) storage.setItem(prefix + key, JSON.stringify({ scope, at: entry.at, result: entry.result, locationsAttempted: entry.locationsAttempted }));
      }
      storage.setItem(indexKey, JSON.stringify({ version: 1, scope, selection, keys: [...entries.keys()] }));
    } catch {
      clearConnectionStorage(storage); storage = null;
    }
    dirty.clear(); deleted.clear();
  }
  const schedule = () => { if (!timer && storage && scope) timer = setTimeout(flush, 100); };
  const clear = () => {
    clearTimeout(timer); timer = null; entries.clear(); dirty.clear(); deleted.clear(); bytes = 0; selection = null;
    clearConnectionStorage(storage);
  };
  const api = {
    get scope() { return scope; },
    get selection() { return selection; },
    hasStored() { try { return Boolean(storage?.getItem(indexKey)); } catch { storage = null; return false; } },
    restore(verifiedScope) {
      if (!verifiedScope || typeof verifiedScope !== "string") { clear(); scope = ""; return; }
      if (scope && scope !== verifiedScope) clear();
      scope = verifiedScope;
      try {
        const raw = storage?.getItem(indexKey);
        if (!raw) return;
        if (raw.length > 128 * 1024) throw new Error("cache index size");
        const index = JSON.parse(raw);
        if (index.version !== 1 || index.scope !== scope || !Array.isArray(index.keys) || index.keys.length > maxEntries) throw new Error("cache index");
        let restoredBytes = 0;
        restoring = true;
        for (const key of index.keys) {
          if (typeof key !== "string" || key.length > 16384) throw new Error("cache key");
          const value = storage.getItem(prefix + key);
          if (!value) continue;
          restoredBytes += value.length * 2 + key.length * 2;
          if (restoredBytes > maxBytes + maxEntries * 1024) throw new Error("cache size");
          const entry = JSON.parse(value);
          if (entry.scope !== scope || !Number.isFinite(entry.at) || entry.at > now() || typeof entry.locationsAttempted !== "boolean" || !validResult(entry.result)) throw new Error("cache entry");
          if (now() - entry.at < retention) api.set(key, entry.result, entry.at, entry.locationsAttempted);
          else deleted.add(key);
        }
        const saved = index.selection;
        if (saved && typeof saved === "object" && saved.filters && typeof saved.filters.date === "string"
          && ["day", "month"].includes(saved.filters.period) && typeof saved.cursor === "string"
          && Array.isArray(saved.cursors) && saved.cursors.length <= 100 && saved.cursors.every(value => typeof value === "string")
          && Array.isArray(saved.nodeOrder) && saved.nodeOrder.length <= 500 && saved.nodeOrder.every(value => typeof value === "string")
          && Array.isArray(saved.sources) && saved.sources.every(source => source && typeof source.agent_id === "string" && (source.agent_name === undefined || typeof source.agent_name === "string"))
          && ["agent_id", "engine", "client_ip", "country_code", "include_non_public"].every(key => saved.filters[key] === undefined || typeof saved.filters[key] === "string")) selection = saved;
      } catch { clear(); }
      finally { restoring = false; }
    },
    bind(verifiedScope) {
      if (typeof verifiedScope !== "string" || !verifiedScope) return;
      if (scope !== verifiedScope) { clear(); scope = verifiedScope; }
    },
    select(value) { selection = value; schedule(); },
    get(key) {
      prune();
      const entry = entries.get(key);
      if (!entry) { schedule(); return null; }
      entries.delete(key); entries.set(key, entry);
      return { ...entry, fresh: now() - entry.at < ttl };
    },
    set(key, result, at = now(), locationsAttempted = false) {
      remove(key);
      let size;
      try { size = JSON.stringify(result).length * 2 + key.length * 2; } catch { return; }
      if (size > maxBytes || !Number.isFinite(at) || at > now() || now() - at >= retention) { schedule(); return; }
      entries.set(key, { result, at, bytes: size, locationsAttempted });
      deleted.delete(key); if (!restoring) dirty.add(key); bytes += size;
      prune();
      while (entries.size > maxEntries || bytes > maxBytes) remove(entries.keys().next().value);
      if (!restoring) schedule();
    },
    preview(query) {
      prune();
      if (query.has("cursor")) return null;
      for (const [key, entry] of [...entries].reverse()) {
        const candidate = new URLSearchParams(key);
        if (candidate.has("cursor") || ["since", "until", "include_non_public", "group_by", "timeline", "bucket", "limit"].some(name => candidate.get(name) !== query.get(name)) || JSON.stringify(candidate.getAll("node_order")) !== JSON.stringify(query.getAll("node_order"))) continue;
        if (["agent_id", "engine", "client_ip", "country_code"].some(name => candidate.get(name) && candidate.get(name) !== query.get(name))) continue;
        const records = entry.result.records.filter(row => (!query.get("agent_id") || row.agent_id === query.get("agent_id")) && (!query.get("engine") || row.engine === query.get("engine")) && (!query.get("client_ip") || row.client_ip === query.get("client_ip")) && (!query.get("country_code") || !row.location?.non_public && row.location?.country_code === query.get("country_code")));
        if (!records.length) continue;
        return { records, sources: entry.result.sources.filter(source => !query.get("agent_id") || source.agent_id === query.get("agent_id")), timeline: [], preview: true, snapshotAt: entry.at };
      }
      return null;
    },
    clear, flush,
  };
  return api;
}
