import assert from "node:assert/strict";
import {
  coreLogFilterLimits,
  coreLogPreferenceKey,
  saveCoreLogPreferences,
  savedCoreLogPreferences,
} from "./modules/core-log-preferences.js";
import { installCoreLogs } from "./modules/core-logs.js";

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

const fakeElement = ({ value = "", dataset = {} } = {}) => {
  const listeners = new Map();
  const element = {
    dataset,
    value,
    attributes: {},
    addEventListener(type, handler) {
      listeners.set(type, handler);
    },
    setAttribute(name, next) {
      element.attributes[name] = String(next);
    },
    click() {
      return listeners.get("click")?.({ currentTarget: element, preventDefault() {} });
    },
    input(next) {
      element.value = next;
      return listeners.get("input")?.({ currentTarget: element });
    },
    change(next) {
      element.value = next;
      return listeners.get("change")?.({ currentTarget: element });
    },
  };
  return element;
};

const stubDom = () => {
  const engineButtons = ["", ...engines].map((value) => fakeElement({ value }));
  const levelButtons = ["", "info", "warning", "error"].map((value) =>
    fakeElement({ value }),
  );
  const searchInput = fakeElement({ value: "" });
  const limitSelect = fakeElement({ value: "1000" });
  const resetButton = fakeElement();
  const refreshToggle = fakeElement();
  const agentLinks = [
    fakeElement({ dataset: { coreLogAgent: "" } }),
    fakeElement({ dataset: { coreLogAgent: agentID } }),
  ];
  return {
    document: {
      querySelector: (selector) =>
        ({
          '#core-log-filters input[name="q"]': searchInput,
          '#core-log-filters select[name="limit"]': limitSelect,
          "[data-reset-core-logs]": resetButton,
          "[data-toggle-core-log-refresh]": refreshToggle,
          "[data-core-log-refresh-label]": null,
        })[selector] ?? null,
      querySelectorAll: (selector) =>
        ({
          "[data-core-log-page-index]": [],
          "[data-core-log-engine]": engineButtons,
          "[data-core-log-level]": levelButtons,
          "[data-core-log-agent]": agentLinks,
        })[selector] ?? [],
    },
    engineButtons,
    levelButtons,
    searchInput,
    limitSelect,
    resetButton,
    refreshToggle,
    agentLinks,
  };
};

// Preceding smoke suites share this process and may still have queued
// microtasks that replace the document stub. Drain them before taking over.
await new Promise((resolve) => setImmediate(resolve));
const previousDocument = globalThis.document;
try {
  const agents = [
    {
      id: agentID,
      name: "Alpha",
      status: "online",
      features: ["core-logs-v1", "core-log-status-v1"],
      runtime: { xray: { installed: true, core_log_status: "active" } },
    },
  ];
  const entries = (agent, limit) =>
    Array.from({ length: limit }, (_, index) => ({
      id: limit * 10 + index,
      agent_id: agent || agentID,
      engine: "xray",
      level: "warning",
      message: `${keyword} ${index}`,
      logged_at: "now",
    }));
  const storage = memoryStorage({
    [coreLogPreferenceKey]: JSON.stringify({
      ...filters,
      auto_refresh: false,
    }),
  });
  const dom = stubDom();
  globalThis.document = dom.document;
  const calls = [];
  const state = { route: "core-logs", navigationEpoch: 1, data: {} };
  let markup = "";
  let timers = 0;
  const render = installCoreLogs({
    state,
    storage,
    engines,
    can: () => true,
    now: () => 1,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    shell: (value) => {
      markup = value;
    },
    setTimer: () => (timers += 1),
    clearTimer: () => {},
    api: async (path) => {
      calls.push(path);
      if (path === "/agents") return agents;
      const params = new URLSearchParams(path.split("?")[1]);
      return entries(params.get("agent_id") || "", Number(params.get("limit")));
    },
  });
  await render();
  assert.equal(calls[0], "/agents");
  assert.equal(
    calls[1],
    `/core-logs?agent_id=${agentID}&limit=200`,
    "a restored window above the preview limit still previews the selected node first",
  );
  assert.equal(calls[2], `/core-logs?agent_id=${agentID}&limit=2000`);
  assert.equal(
    state.data.coreLogEntries.length,
    2000,
    "the full restored window replaces the preview",
  );
  assert.match(markup, /显示 <strong>2000<\/strong> 条结果/);
  assert.equal(
    state.data.coreLogAutoRefresh,
    false,
    "the stored live-update switch seeds the session",
  );
  assert.equal(timers, 0, "a restored paused stream must not schedule a poll");
  assert.match(
    markup,
    /name="engine" value="xray" data-core-log-engine aria-pressed="true"/,
    "the stored engine filter renders as pressed",
  );
  assert.match(
    markup,
    /name="level" value="warning" data-core-log-level aria-pressed="true"/,
    "the stored level filter renders as pressed",
  );
  assert.match(markup, new RegExp(`value="${keyword}"`));
  assert.match(markup, /<option value="2000" selected>/);
  assert.match(markup, /aria-checked="false" data-toggle-core-log-refresh/);
  assert.match(markup, /当前范围：<strong>Alpha<\/strong>/);

  dom.engineButtons.find((button) => button.value === "sing-box").click();
  assert.equal(
    storage.read(coreLogPreferenceKey).engine,
    "sing-box",
    "an engine change is remembered",
  );
  dom.searchInput.input("disk");
  assert.equal(
    storage.read(coreLogPreferenceKey).q,
    "disk",
    "keyword edits are remembered as typed",
  );
  dom.resetButton.click();
  assert.deepEqual(
    storage.read(coreLogPreferenceKey),
    { agent_id: agentID, limit: 2000, auto_refresh: false },
    "clearing local filters keeps the node scope and the per-engine window",
  );
  await dom.agentLinks[0].click();
  assert.deepEqual(
    storage.read(coreLogPreferenceKey),
    { limit: 2000, auto_refresh: false },
    "switching back to all nodes is remembered",
  );
  dom.refreshToggle.click();
  assert.equal(
    storage.read(coreLogPreferenceKey).auto_refresh,
    true,
    "resuming live updates is remembered",
  );
  assert.equal(state.data.coreLogAutoRefresh, true);
  assert.equal(timers, 1, "resuming live updates restarts the poller");
  await dom.limitSelect.change("500");
  assert.equal(
    storage.read(coreLogPreferenceKey).limit,
    500,
    "a new per-engine window is remembered",
  );

  // Leave a different selection in the store so precedence is observable.
  saveCoreLogPreferences(
    { filters: { engine: "sing-box", limit: 500 }, autoRefresh: false },
    storage,
    engines,
  );
  const explicitDom = stubDom();
  globalThis.document = explicitDom.document;
  const explicitCalls = [];
  const explicitState = {
    route: "core-logs",
    navigationEpoch: 1,
    data: { coreLogFilters: { limit: 200 } },
  };
  let explicitMarkup = "";
  const explicitRender = installCoreLogs({
    state: explicitState,
    storage,
    engines,
    can: () => true,
    now: () => 1,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    shell: (value) => {
      explicitMarkup = value;
    },
    setTimer: () => 1,
    clearTimer: () => {},
    api: async (path) => {
      explicitCalls.push(path);
      return path === "/agents" ? [] : [];
    },
  });
  await explicitRender();
  assert.equal(
    explicitState.data.coreLogFilters.engine,
    undefined,
    "browser storage must not overwrite a selection the session already owns",
  );
  assert.equal(
    explicitState.data.coreLogFilters.limit,
    200,
    "the session's own window must win over the stored one",
  );
  assert.equal(
    explicitState.data.coreLogAutoRefresh,
    undefined,
    "the session keeps control of the live-update switch",
  );
  assert.deepEqual(explicitCalls, ["/agents", "/core-logs?limit=200"]);
  assert.match(explicitMarkup, /<option value="200" selected>/);

  const deniedDom = stubDom();
  globalThis.document = deniedDom.document;
  const deniedCalls = [];
  const deniedState = { route: "core-logs", navigationEpoch: 1, data: {} };
  const deniedRender = installCoreLogs({
    state: deniedState,
    storage: denied,
    engines,
    can: () => true,
    now: () => 1,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    shell: () => {},
    setTimer: () => 1,
    clearTimer: () => {},
    api: async (path) => {
      deniedCalls.push(path);
      return [];
    },
  });
  await deniedRender();
  assert.equal(
    deniedCalls.at(-1),
    "/core-logs?limit=1000",
    "a browser that denies reads falls back to the default window",
  );
  deniedDom.searchInput.input("disk");
  assert.equal(
    deniedState.data.coreLogFilters.q,
    "disk",
    "a browser that denies writes still applies local filtering",
  );
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}

console.log("Core log selection persistence smoke passed");
