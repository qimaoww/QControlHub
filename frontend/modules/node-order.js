import { accountStorage } from "./account-storage.js";

export const nodeCardOrderKey = "qcontrolhub:node-card-order";

export function savedNodeOrder(storage) {
  try {
    const source = storage ?? accountStorage;
    const parsed = JSON.parse(source?.getItem(nodeCardOrderKey));
    if (!Array.isArray(parsed)) return [];
    const seen = new Set();
    return parsed.filter((id) => {
      if (typeof id !== "string" || id === "" || seen.has(id)) return false;
      seen.add(id);
      return true;
    });
  } catch {
    return [];
  }
}

export function saveNodeOrder(ids, storage) {
  try {
    const target = storage ?? accountStorage;
    target?.setItem(nodeCardOrderKey, JSON.stringify(ids));
  } catch {}
}

// Older builds kept one browser-wide order under the bare key. Migrate it into
// the account-scoped key the first time an account is opened, keeping only the
// nodes that account can currently see, so a shared browser never inherits
// another account's node identifiers. The legacy value is left in place so the
// next account can migrate its own visible subset.
export function migrateLegacyNodeOrder(nodes = [], storage, legacyStorage) {
  try {
    const target = storage ?? accountStorage;
    if (target?.getItem(nodeCardOrderKey) != null) return;
    const migrated = visibleLegacyOrder(nodes, legacyStorage);
    if (!migrated.length) return;
    target.setItem(nodeCardOrderKey, JSON.stringify(migrated));
  } catch {}
}

function visibleLegacyOrder(nodes, legacyStorage) {
  const legacy = legacyStorage ?? globalThis.localStorage;
  const visible = new Set((nodes || []).map((node) => node?.id).filter(Boolean));
  return savedNodeOrder(legacy).filter((id) => visible.has(id));
}

export function orderNodesBySavedOrder(nodes = [], saved) {
  if (saved === undefined) {
    saved = savedNodeOrder();
    try {
      // Some sidebars only have nodes with a deployment or profile. Apply
      // legacy order without persisting a partial list; /agents performs
      // the one-time migration using the complete account-visible fleet.
      if (accountStorage.getItem(nodeCardOrderKey) == null)
        saved = visibleLegacyOrder(nodes);
    } catch {}
  }
  if (!saved.length) return nodes;
  const position = new Map(saved.map((id, index) => [id, index]));
  return [...nodes].sort(
    (left, right) =>
      (position.get(left.id) ?? saved.length) -
      (position.get(right.id) ?? saved.length),
  );
}

// Full-fleet entry point: migrate legacy order, then apply it.
export function orderedNodeList(nodes = [], storage, legacyStorage) {
  migrateLegacyNodeOrder(nodes, storage, legacyStorage);
  return orderNodesBySavedOrder(nodes, savedNodeOrder(storage));
}
