// Owned by state.data: no IP history persists across accounts or browser sessions.
export function createConnectionCache({ now = Date.now, ttl = 30_000, retention = 300_000, maxEntries = 24, maxBytes = 4 * 1024 * 1024 } = {}) {
  const entries = new Map();
  let bytes = 0;
  const remove = key => {
    const entry = entries.get(key);
    if (entry) bytes -= entry.bytes;
    entries.delete(key);
  };
  const prune = () => {
    for (const [key, entry] of entries) if (now() - entry.at >= retention) remove(key);
  };
  return {
    get(key) {
      prune();
      const entry = entries.get(key);
      if (!entry) return null;
      entries.delete(key);
      entries.set(key, entry);
      return { ...entry, fresh: now() - entry.at < ttl };
    },
    set(key, result, at = now(), locationsAttempted = false) {
      remove(key);
      const size = JSON.stringify(result).length * 2 + key.length * 2;
      if (size > maxBytes) return;
      entries.set(key, { result, at, bytes: size, locationsAttempted });
      bytes += size;
      prune();
      while (entries.size > maxEntries || bytes > maxBytes) remove(entries.keys().next().value);
    },
    preview(query) {
      prune();
      if (query.has("cursor")) return null;
      for (const [key, entry] of [...entries].reverse()) {
        const scope = new URLSearchParams(key);
        if (scope.has("cursor") || ["since", "until", "include_non_public", "group_by"].some(name => scope.get(name) !== query.get(name)) || JSON.stringify(scope.getAll("node_order")) !== JSON.stringify(query.getAll("node_order"))) continue;
        if (["agent_id", "engine", "client_ip"].some(name => scope.get(name) && scope.get(name) !== query.get(name))) continue;
        const records = entry.result.records.filter(row => (!query.get("agent_id") || row.agent_id === query.get("agent_id")) && (!query.get("engine") || row.engine === query.get("engine")) && (!query.get("client_ip") || row.client_ip === query.get("client_ip")));
        if (!records.length) continue;
        // This may be only one page of the broader scope. Never invent totals
        // or pagination from that partial slice; the server supplies both.
        return { records, sources: entry.result.sources.filter(source => !query.get("agent_id") || source.agent_id === query.get("agent_id")), timeline: [], preview: true };
      }
      return null;
    },
    clear() { entries.clear(); bytes = 0; },
  };
}
