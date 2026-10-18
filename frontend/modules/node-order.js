import { accountStorage, storageAccount, onStorageAccountChange } from "./account-storage.js";

export const nodeCardOrderKey = "qcontrolhub:node-card-order";
const clean = ids => Array.isArray(ids) ? [...new Set(ids.filter(id => typeof id === "string" && id !== ""))] : [];
const cached = storage => {
  try { return clean(JSON.parse(storage.getItem(nodeCardOrderKey))); } catch { return []; }
};

// Each instance belongs to one authenticated account lifetime. Reads and writes
// share a queue so a slow poll cannot overwrite a later gesture.
export function createNodeOrderSync(api, storage, isCurrent = () => true, canMigrate = true) {
  let order = null, queue = Promise.resolve(), reading = null;
  const current = () => { if (!isCurrent()) throw new DOMException("Account changed", "AbortError"); };
  const apply = value => {
    current();
    if (!value || !Array.isArray(value.ids) || !Number.isSafeInteger(value.revision) || value.revision < 0)
      throw new Error("Invalid node order response");
    order = { ids: clean(value.ids), revision: value.revision };
    try {
      storage.setItem(nodeCardOrderKey, JSON.stringify(order.ids));
      storage.setItem(`${nodeCardOrderKey}:server`, "true");
    } catch {}
    return order;
  };
  const enqueue = run => {
    const result = queue.catch(() => {}).then(() => { current(); return run(); });
    queue = result;
    return result;
  };
  async function read() {
    current();
    // Capture the old scoped preference before caching the server response.
    let legacy = [];
    try { if (canMigrate && !storage.getItem(`${nodeCardOrderKey}:server`)) legacy = cached(storage); } catch {}
    const value = await api("/preferences/node-order");
    current();
    if (value.revision === 0 && legacy.length) {
      const visible = new Set(value.ids);
      const ids = legacy.filter(id => visible.has(id));
      if (ids.length) {
        return apply(await api("/preferences/node-order", { method: "PUT", body: JSON.stringify({ ids, revision: 0, initialize: true }) }));
      }
    }
    return apply(value);
  }
  return {
    ids: () => order?.ids ?? cached(storage),
    load() {
      if (!reading) {
        const result = enqueue(read);
        reading = result;
        result.finally(() => { if (reading === result) reading = null; }).catch(() => {});
      }
      return reading;
    },
    save(ids) {
      const subset = [...ids];
      return enqueue(async () => {
        if (!order) await read();
        // A conflict rebases this subset on the current complete server order.
        for (let attempt = 0; attempt < 3; attempt++) {
          current();
          try {
            apply(await api("/preferences/node-order", { method: "PUT", body: JSON.stringify({ ids: subset, revision: order.revision }) }));
            return true;
          } catch (error) {
            current();
            // Also reconcile an ambiguous failure after the database committed.
            try {
              const actual = apply(await api("/preferences/node-order"));
              const selected = new Set(subset);
              if (JSON.stringify(actual.ids.filter(id => selected.has(id))) === JSON.stringify(subset)) return true;
            } catch {
              // The write may have committed but its response and readback were
              // lost. Keep the attempted order explicitly unconfirmed in the UI.
              error.orderUnconfirmed = true;
              current();
              throw error;
            }
            current();
            if (error.status !== 409 || attempt === 2) throw error;
          }
        }
      });
    },
  };
}

let transport, sync, epoch = 0;
onStorageAccountChange(() => { epoch++; sync = null; });
export function configureNodeOrder(api) { transport = api; epoch++; sync = null; }
function accountSync() {
  if (!transport || storageAccount().id === "signed-out") return null;
  if (!sync) {
    const generation = epoch;
    sync = createNodeOrderSync(transport, accountStorage, () => generation === epoch, storageAccount().stable);
  }
  return sync;
}
export const refreshNodeOrder = () => accountSync()?.load();
export function savedNodeOrder(storage) { return storage && storage !== accountStorage ? cached(storage) : accountSync()?.ids() ?? cached(accountStorage); }

export function saveNodeOrder(ids, storage) {
  if (!storage && accountSync()) return accountSync().save(ids);
  try {
    const target = storage ?? accountStorage, value = JSON.stringify(clean(ids));
    target.setItem(nodeCardOrderKey, value);
    return target.getItem(nodeCardOrderKey) === value;
  } catch { return false; }
}

export function orderNodesBySavedOrder(nodes = [], saved = savedNodeOrder()) {
  if (!saved.length) return nodes;
  const position = new Map(saved.map((id, index) => [id, index]));
  return [...nodes].sort((a, b) => (position.get(a.id) ?? saved.length) - (position.get(b.id) ?? saved.length));
}
export function orderedNodeList(nodes = [], storage) {
  return orderNodesBySavedOrder(nodes, savedNodeOrder(storage));
}
