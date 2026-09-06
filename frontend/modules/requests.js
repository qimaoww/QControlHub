// Reuse reads only within one page load. Nothing survives a navigation, a
// mutation, or the end of rendering; polling still fetches fresh server state.
export function createScopedAPI(send) {
  let reads = null;
  return {
    begin() { reads = new Map(); },
    end() { reads = null; },
    request(path, options = {}) {
      const method = String(options.method || "GET").toUpperCase();
      if (!["GET", "HEAD", "OPTIONS"].includes(method)) reads?.clear();
      // Independently cancellable requests and custom headers must not share
      // a promise with another caller's request options.
      if (!reads || Object.keys(options).length || path.startsWith("/auth/"))
        return send(path, options);
      if (!reads.has(path)) reads.set(path, send(path, options));
      return reads.get(path).then((value) => structuredClone(value));
    },
  };
}
