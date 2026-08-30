export const nodeCardOrderKey = "qcontrolhub:node-card-order";

export function savedNodeOrder(storage) {
  try {
    const source = storage ?? globalThis.localStorage;
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
    const target = storage ?? globalThis.localStorage;
    target?.setItem(nodeCardOrderKey, JSON.stringify(ids));
  } catch {}
}

export function orderNodesBySavedOrder(nodes = [], saved = savedNodeOrder()) {
  if (!saved.length) return nodes;
  const position = new Map(saved.map((id, index) => [id, index]));
  return [...nodes].sort(
    (left, right) =>
      (position.get(left.id) ?? saved.length) -
      (position.get(right.id) ?? saved.length),
  );
}
