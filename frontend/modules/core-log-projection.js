import { coreLogFilterCounts, filterCoreLogEntries } from "./core-log-model.js";

// Read snapshots are immutable. Reuse unchanged records, including their
// search text and rendered rows, across polls without retaining another window.
export function reuseCoreLogEntries(previous, fresh) {
  if (!previous?.length) return fresh;
  const byID = new Map(previous.map(entry => [entry.id, entry]));
  const fields = ["agent_id", "engine", "level", "message", "logged_at", "received_at"];
  let unchanged = fresh.length === previous.length;
  const entries = fresh.map((entry, index) => {
    const old = byID.get(entry.id);
    const value = old && fields.every(field => old[field] === entry[field]) ? old : entry;
    if (value !== previous[index]) unchanged = false;
    return value;
  });
  return unchanged ? previous : entries;
}

export function createCoreLogProjection(engines) {
  let source, counts, filterKey, filtered;
  const searchText = new WeakMap();
  return (entries, filters) => {
    if (source !== entries) {
      source = entries;
      counts = coreLogFilterCounts(entries, engines);
      filterKey = null;
    }
    const keyword = String(filters.q || "").trim().toLowerCase();
    const key = JSON.stringify([filters.engine || "", filters.level || "", keyword]);
    if (filterKey !== key) {
      filterKey = key;
      const candidates = filters.engine || filters.level
        ? filterCoreLogEntries(entries, { engine: filters.engine, level: filters.level }) : entries;
      filtered = keyword ? candidates.filter(entry => {
        if (!searchText.has(entry)) searchText.set(entry, String(entry.message || "").toLowerCase());
        return searchText.get(entry).includes(keyword);
      }) : candidates;
    }
    return { entries: filtered, counts };
  };
}
