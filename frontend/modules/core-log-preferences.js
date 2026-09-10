// The kernel log page keeps its node scope, engine/level/keyword filters,
// per-engine window, and live-update switch in one browser-stored record so a
// reload or a later visit restores the same view. Every field is re-validated
// on read: a corrupted or outdated record falls back to the default view
// instead of sending an invalid query to the API.
export const coreLogPreferenceKey = "qcontrolhub:core-log-preferences";

export const coreLogFilterLimits = [100, 200, 500, 1000, 2000];

const levelValues = new Set(["info", "warning", "error"]);
// Agent identifiers are opaque to the browser, but the API rejects any filter
// that is not an identifier-shaped string. Discarding malformed values here
// keeps one bad record from turning the page into a persistent 400.
const agentIDPattern = /^[A-Za-z0-9_-]{1,64}$/;
const keywordLength = 120;

function normalizedFilters(value, engines) {
  const source =
    value && typeof value === "object" && !Array.isArray(value) ? value : {};
  const filters = {};
  if (
    typeof source.agent_id === "string" &&
    agentIDPattern.test(source.agent_id)
  )
    filters.agent_id = source.agent_id;
  if (engines.includes(source.engine)) filters.engine = source.engine;
  if (levelValues.has(source.level)) filters.level = source.level;
  if (typeof source.q === "string" && source.q.trim())
    filters.q = source.q.slice(0, keywordLength);
  const limit = Number(source.limit);
  if (coreLogFilterLimits.includes(limit)) filters.limit = limit;
  return filters;
}

export function savedCoreLogPreferences(storage, engines = []) {
  try {
    const source = storage ?? globalThis.localStorage;
    const parsed = JSON.parse(source?.getItem(coreLogPreferenceKey));
    const record =
      parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
    return {
      filters: normalizedFilters(record, engines),
      autoRefresh: record.auto_refresh !== false,
    };
  } catch {
    return { filters: {}, autoRefresh: true };
  }
}

export function saveCoreLogPreferences(
  { filters, autoRefresh } = {},
  storage,
  engines = [],
) {
  try {
    const target = storage ?? globalThis.localStorage;
    const record = normalizedFilters(filters, engines);
    record.auto_refresh = autoRefresh !== false;
    target?.setItem(coreLogPreferenceKey, JSON.stringify(record));
  } catch {
    // Private mode or a full quota must not break log filtering.
  }
}
