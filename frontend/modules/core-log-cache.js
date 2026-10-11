// Session-memory only: state.data owns this cache and is discarded on logout.
// Limits apply to retained snapshots, not to the active searchable log window.
export function createCoreLogCache({ now = Date.now, maxScopes = 3, maxBytes = 16 * 1024 * 1024, ttl = 30_000 } = {}) {
  const snapshots = new Map();
  let bytes = 0;
  const remove = (key) => {
    const snapshot = snapshots.get(key);
    if (!snapshot) return;
    bytes -= snapshot.bytes;
    snapshots.delete(key);
  };
  const prune = () => {
    for (const [key, snapshot] of snapshots)
      if (now() - snapshot.at >= ttl) remove(key);
  };
  return {
    get(key) {
      prune();
      const snapshot = snapshots.get(key);
      if (!snapshot) return null;
      snapshots.delete(key);
      snapshots.set(key, snapshot);
      return snapshot.entries;
    },
    preview(key) {
      prune();
      const [agent, limit] = JSON.parse(key);
      const scopes = [...snapshots].reverse();
      const related = scopes.find(([scope]) => JSON.parse(scope)[0] === agent);
      if (!related) return null;
      const [scope, snapshot] = related;
      const counts = new Map();
      const entries = snapshot.entries.filter(entry => {
        const count = counts.get(entry.engine) || 0;
        counts.set(entry.engine, count + 1);
        return count < limit;
      });
      return { entries, complete: JSON.parse(scope)[1] >= limit };
    },
    set(key, entries) {
      prune();
      const existing = snapshots.get(key);
      if (existing?.entries === entries) {
        existing.at = now();
        snapshots.delete(key);
        snapshots.set(key, existing);
        return;
      }
      remove(key);
      // Conservative UTF-16 text estimate plus per-entry object overhead.
      const size = entries.reduce((total, entry) => total + 512 + 2 * String(entry.message || "").length, 0);
      if (size > maxBytes) return;
      snapshots.set(key, { entries, bytes: size, at: now() });
      bytes += size;
      while (snapshots.size > maxScopes || bytes > maxBytes)
        remove(snapshots.keys().next().value);
    },
  };
}
