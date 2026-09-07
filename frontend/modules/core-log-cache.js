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
    set(key, entries) {
      prune();
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
