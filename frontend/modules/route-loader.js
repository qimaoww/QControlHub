export function createRouteModuleLoader(factories) {
  const pending = new Map();
  const loaded = new Map();

  const load = (name) => {
    if (loaded.has(name)) return Promise.resolve(loaded.get(name));
    if (pending.has(name)) return pending.get(name);
    const factory = factories[name];
    if (typeof factory !== "function")
      return Promise.reject(new Error(`unknown route module: ${name}`));

    const request = Promise.resolve()
      .then(factory)
      .then((value) => {
        loaded.set(name, value);
        pending.delete(name);
        return value;
      })
      .catch((error) => {
        if (pending.get(name) === request) pending.delete(name);
        throw error;
      });
    pending.set(name, request);
    return request;
  };

  return {
    load,
    preload(name) {
      return load(name).catch(() => undefined);
    },
    peek(name) {
      return loaded.get(name);
    },
  };
}
