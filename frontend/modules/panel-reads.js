// Shell-level reads — the panel overview counters and the panel settings —
// change slowly, but the shell renders on every navigation. Re-reading both per
// route costs a round trip each on a link where a single read takes most of a
// second, so an account keeps a short cache and a write invalidates what it
// changed. Routes that display a value live read through the cache.
const ttlByKey = {
  overview: 30_000,
  settings: 300_000,
};

export function createPanelReads(read, { now = () => Date.now() } = {}) {
  const cache = new Map();
  const cacheKey = (scope, key) => `${scope}|${key}`;

  return {
    get(key, { scope = "", live = false } = {}) {
      const ttl = ttlByKey[key];
      if (ttl === undefined) throw new Error(`unknown panel read: ${key}`);
      if (typeof read !== "function") throw new Error("panel reads need a reader");
      const scoped = cacheKey(scope, key);
      const cached = cache.get(scoped);
      const at = now();
      if (cached && !live && at - cached.at < ttl) return cached.promise;
      const promise = read(key).catch((error) => {
        // A failed read must not pin the failure for the whole TTL.
        if (cache.get(scoped)?.promise === promise) cache.delete(scoped);
        throw error;
      });
      cache.set(scoped, { at, promise });
      return promise;
    },
    invalidate(...keys) {
      if (!keys.length) {
        cache.clear();
        return;
      }
      for (const key of keys) {
        const suffix = `|${key}`;
        for (const scoped of [...cache.keys()]) {
          if (scoped.endsWith(suffix)) cache.delete(scoped);
        }
      }
    },
    size: () => cache.size,
  };
}
