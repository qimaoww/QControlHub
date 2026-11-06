import assert from "node:assert/strict";
import {
  coreLogFilterLimits,
  coreLogPreferenceKey,
  saveCoreLogPreferences,
  savedCoreLogPreferences,
} from "./modules/core-log-preferences.js";
import { createCoreLogSelection } from "./modules/core-log-selection.js";

const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const agentID = "agt_0123456789abcdef";
const keyword = "pressure entry 7";

const memoryStorage = (entries = {}) => {
  const items = new Map(Object.entries(entries));
  return {
    getItem: (key) => (items.has(key) ? items.get(key) : null),
    setItem: (key, value) => items.set(key, String(value)),
    read: (key) => (items.has(key) ? JSON.parse(items.get(key)) : null),
  };
};

assert.deepEqual(
  coreLogFilterLimits,
  [100, 200, 500, 1000, 2000],
  "the stored window schema must match the selectable per-engine limits",
);
assert.deepEqual(
  savedCoreLogPreferences(memoryStorage(), engines),
  { filters: {}, autoRefresh: true },
  "a browser without a stored record keeps the default view",
);
assert.deepEqual(
  savedCoreLogPreferences(null, engines),
  { filters: {}, autoRefresh: true },
  "a missing browser store must not break the log page",
);

const filters = {
  agent_id: agentID,
  engine: "xray",
  level: "warning",
  q: keyword,
  limit: 2000,
};
const roundTrip = memoryStorage();
saveCoreLogPreferences({ filters, autoRefresh: false }, roundTrip, engines);
assert.deepEqual(
  roundTrip.read(coreLogPreferenceKey),
  { ...filters, auto_refresh: false },
  "the stored record keeps the selected node, filters, window, and live-update switch",
);
assert.deepEqual(
  savedCoreLogPreferences(roundTrip, engines),
  { filters: { ...filters }, autoRefresh: false },
  "a stored record round-trips without losing a field",
);

assert.deepEqual(
  savedCoreLogPreferences(
    memoryStorage({
      [coreLogPreferenceKey]: JSON.stringify({
        agent_id: "not a valid id!",
        engine: "nginx",
        level: "debug",
        q: 7,
        limit: 250,
        auto_refresh: "no",
      }),
    }),
    engines,
  ),
  { filters: {}, autoRefresh: true },
  "unknown filters and non-boolean switches fall back to the default view",
);
assert.deepEqual(
  savedCoreLogPreferences(
    memoryStorage({
      [coreLogPreferenceKey]: JSON.stringify({
        engine: "sing-box",
        level: "error",
        limit: "500",
        auto_refresh: true,
      }),
    }),
    engines,
  ),
  { filters: { engine: "sing-box", level: "error", limit: 500 }, autoRefresh: true },
  "a partially valid record keeps the fields it can still serve",
);

const bounded = memoryStorage();
saveCoreLogPreferences(
  { filters: { q: "x".repeat(300) }, autoRefresh: true },
  bounded,
  engines,
);
assert.equal(
  bounded.read(coreLogPreferenceKey).q.length,
  120,
  "a restored keyword stays within the search input limit",
);
saveCoreLogPreferences(
  { filters: { q: "   " }, autoRefresh: true },
  bounded,
  engines,
);
assert.deepEqual(
  bounded.read(coreLogPreferenceKey),
  { auto_refresh: true },
  "a whitespace-only keyword is not a selection",
);

assert.deepEqual(
  savedCoreLogPreferences(
    memoryStorage({ [coreLogPreferenceKey]: "{oops" }),
    engines,
  ),
  { filters: {}, autoRefresh: true },
  "a corrupted record cannot pin the log page to an invalid query",
);
const denied = {
  getItem() {
    throw new Error("denied");
  },
  setItem() {
    throw new Error("denied");
  },
};
assert.deepEqual(
  savedCoreLogPreferences(denied, engines),
  { filters: {}, autoRefresh: true },
  "a browser that denies reads still shows the default view",
);
assert.doesNotThrow(
  () =>
    saveCoreLogPreferences(
      { filters: { engine: "xray" }, autoRefresh: true },
      denied,
      engines,
    ),
  "a browser that denies writes still filters logs",
);

// The independent log page uses this same state owner. Real control changes,
// restored rendering and live requests run in browser/core-logs.mjs.
const storage = memoryStorage({ [coreLogPreferenceKey]: JSON.stringify({ ...filters, auto_refresh: false }) });
const state = { data: {} };
const selection = createCoreLogSelection({ state, engines, storage });
selection.restoreSelection();
assert.deepEqual(state.data.coreLogFilters, filters, 'a fresh account restores all validated filter fields');
assert.equal(state.data.coreLogAutoRefresh, false, 'a stored pause seeds the new session');
assert.equal(selection.query().params.toString(), `agent_id=${agentID}&limit=2000`, 'engine/level/keyword are local filters, not remote query limits');
state.data.coreLogFilters.engine = 'sing-box'; selection.rememberSelection();
assert.equal(storage.read(coreLogPreferenceKey).engine, 'sing-box', 'engine edits are remembered');
state.data.coreLogFilters.q = 'disk'; selection.rememberSelection();
assert.equal(storage.read(coreLogPreferenceKey).q, 'disk', 'typed keywords are remembered');
state.data.coreLogFilters = { agent_id: agentID, limit: 2000 }; selection.rememberSelection();
assert.deepEqual(storage.read(coreLogPreferenceKey), { agent_id: agentID, limit: 2000, auto_refresh: false }, 'clearing local filters retains scope, window and pause');
state.data.coreLogFilters = { limit: 2000 }; selection.rememberSelection();
assert.deepEqual(storage.read(coreLogPreferenceKey), { limit: 2000, auto_refresh: false }, 'all-node selection is remembered');
state.data.coreLogAutoRefresh = true; selection.rememberSelection();
assert.equal(storage.read(coreLogPreferenceKey).auto_refresh, true, 'resuming updates is remembered');
state.data.coreLogFilters.limit = 500; selection.rememberSelection();
assert.equal(storage.read(coreLogPreferenceKey).limit, 500);
const explicitState = { data: { coreLogFilters: { limit: 200 } } };
const explicit = createCoreLogSelection({ state: explicitState, engines, storage });
explicit.restoreSelection();
assert.deepEqual(explicitState.data.coreLogFilters, { limit: 200 }, 'browser storage cannot overwrite the current session selection');
assert.equal(explicitState.data.coreLogAutoRefresh, undefined, 'an existing session owns its live-update switch');
assert.equal(explicit.query().params.toString(), 'limit=200');
const deniedState = { data: {} };
const inaccessible = createCoreLogSelection({ state: deniedState, engines, storage: denied });
inaccessible.restoreSelection();
assert.equal(inaccessible.query().params.toString(), 'limit=1000', 'denied reads use the default window');
deniedState.data.coreLogFilters.q = 'disk'; inaccessible.rememberSelection();
assert.equal(deniedState.data.coreLogFilters.q, 'disk', 'denied storage writes cannot discard in-memory filtering');
selection.restoreSelection();
assert.equal(state.data.coreLogFilters.limit, 500, 'returning to the page retains the in-memory window');
state.data = {}; selection.restoreSelection();
assert.equal(state.data.coreLogFilters.limit, 500, 'a new account data owner validates the stored record again');
console.log('Native log selection persistence, current-session precedence and storage failures passed');
