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
  const touch = (key, snapshot) => {
    snapshots.delete(key);
    snapshots.set(key, snapshot);
  };
  return {
    get(key) {
      prune();
      const snapshot = snapshots.get(key);
      if (!snapshot) return null;
      touch(key, snapshot);
      return snapshot.entries;
    },
    preview(key) {
      prune();
      const [agent, limit, instance = "", user = ""] = JSON.parse(key);
      const related = snapshots.has(key) ? [key, snapshots.get(key)] : [...snapshots].reverse().find(([scope]) => {
        const [otherAgent, , otherInstance = "", otherUser = ""] = JSON.parse(scope);
        return agent === otherAgent && instance === otherInstance && user === otherUser;
      });
      if (!related) return null;
      const [scope, snapshot] = related;
      touch(scope, snapshot);
      if (scope === key) return { entries: snapshot.entries, complete: snapshot.complete };
      const counts = new Map();
      const entries = snapshot.entries.filter(entry => {
        const count = counts.get(entry.engine) || 0;
        counts.set(entry.engine, count + 1);
        return count < limit;
      });
      return { entries: entries.length === snapshot.entries.length ? snapshot.entries : entries,
        complete: snapshot.complete && JSON.parse(scope)[1] >= limit };
    },
    set(key, entries, { complete = true } = {}) {
      prune();
      const existing = snapshots.get(key);
      if (existing?.entries === entries) {
        existing.at = now();
        existing.complete = complete;
        touch(key, existing);
        return;
      }
      remove(key);
      // Conservative UTF-16 text estimate plus per-entry object overhead.
      const size = entries.reduce((total, entry) => total + 512 + 2 * String(entry.message || "").length, 0);
      if (size > maxBytes) return;
      snapshots.set(key, { entries, complete, bytes: size, at: now() });
      bytes += size;
      while (snapshots.size > maxScopes || bytes > maxBytes)
        remove(snapshots.keys().next().value);
    },
  };
}
