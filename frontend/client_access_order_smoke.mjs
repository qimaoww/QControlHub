import assert from "node:assert/strict";
import { accountStorage, setStorageAccount } from "./modules/account-storage.js";
import {
  filterClientAccessEntries, groupClientAccessEntries, installClientAccess,
} from "./modules/client-access.js";
import { nodeCardOrderKey } from "./modules/node-order.js";

const previousDocument = globalThis.document;
const previousStorage = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
const values = new Map();
Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
  getItem: key => values.get(key) ?? null,
  setItem: (key, value) => values.set(key, String(value)),
} });

const agents = Object.freeze(["alpha", "beta", "gamma", "empty"].map(id => ({
  id, name: id, status: "online",
})));
const entries = Object.freeze([
  ["gamma", "mihomo"], ["alpha", "xray"], ["missing", "xray"],
  ["beta", "mihomo"], ["alpha", "mihomo"], ["gamma", "xray"],
].map(([agent_id, engine]) => Object.freeze({
  agent_id, agent_name: agent_id, engine, address: `${agent_id}.example.test`,
  profiles: Object.freeze([20002, 20001].map(port => ({
    tag: `${agent_id}-${port}`, port, protocol: "test",
    profile: { format: "URI", uri: `test-${agent_id}-${port}`, fields: [] },
  }))),
})));
const groupIDs = (items = entries, nodes = agents) =>
  groupClientAccessEntries(items, nodes).map(group => group.agent_id);

try {
  setStorageAccount({ user_id: "client-order-alice" });
  assert.deepEqual(groupIDs(), ["alpha", "beta", "gamma", "missing"],
    "default client cards must follow the node list, not the export response");
  assert.deepEqual(groupClientAccessEntries([], agents), []);

  const saved = JSON.stringify(["gamma", "beta", "retired"]);
  accountStorage.setItem(nodeCardOrderKey, saved);
  assert.deepEqual(groupIDs(), ["gamma", "beta", "alpha", "missing"],
    "saved node order wins, with new nodes and missing metadata appended stably");
  assert.deepEqual(groupIDs(entries, []), ["gamma", "beta", "alpha", "missing"],
    "accounts without agent metadata must still use their saved node order");
  const groups = groupClientAccessEntries(entries, agents);
  assert.deepEqual(groups.find(group => group.agent_id === "alpha").entries, [entries[1], entries[4]],
    "sorting node groups must retain each node's engine and profile order");
  assert.deepEqual(groupIDs(filterClientAccessEntries(entries, { engine: "xray" })),
    ["gamma", "alpha", "missing"], "engine filtering must retain node order");
  assert.deepEqual(groupIDs(filterClientAccessEntries(entries, { query: "20001" })),
    ["gamma", "beta", "alpha", "missing"], "profile search must retain node order");
  assert.equal(accountStorage.getItem(nodeCardOrderKey), saved,
    "filtering must not persist a partial node order");

  let markup = "";
  let canReadAgents = true;
  const calls = [];
  const state = { route: "client-access", navigationEpoch: 1, data: {} };
  const sidebarLinks = ["", "alpha"].map(accessAgent => ({ dataset: { accessAgent } }));
  globalThis.document = {
    querySelector: () => null,
    querySelectorAll: selector => selector === "[data-access-agent]" ? sidebarLinks : [],
  };
  const render = installClientAccess({
    state, engines: ["mihomo", "xray"], esc: value => String(value ?? ""), engineName: String,
    can: capability => capability === "agents.read" && canReadAgents,
    notify: message => assert.fail(message),
    shell: html => { markup = html; },
    api: async path => {
      calls.push(path);
      if (path === "/agents") return agents;
      if (path === "/client-access") return entries;
      assert.fail(`unexpected client ordering API: ${path}`);
    },
  });
  const cardIDs = () => [...markup.matchAll(/data-refresh-key="client-access-node-([^"]+)"/g)]
    .map(match => match[1]);
  await render();
  assert.deepEqual(cardIDs(), ["gamma", "beta", "alpha", "missing"],
    "opening the client page must apply the saved node order");
  sidebarLinks[1].onclick({ preventDefault() {} });
  assert.deepEqual(cardIDs(), ["alpha"], "node filtering must keep the selected card");
  sidebarLinks[0].onclick({ preventDefault() {} });
  assert.deepEqual(cardIDs(), ["gamma", "beta", "alpha", "missing"],
    "clearing a node filter must restore the complete node order");
  assert.equal(calls.length, 2, "local filtering must not refetch data");

  accountStorage.setItem(nodeCardOrderKey, JSON.stringify(["beta", "alpha"]));
  await render();
  assert.deepEqual(cardIDs(), ["beta", "alpha", "gamma", "missing"],
    "refresh must apply later changes to the node order");
  state.data.accessEngine = "xray";
  await render();
  assert.deepEqual(cardIDs(), ["alpha", "gamma", "missing"],
    "rendering an engine filter must retain the updated node order");
  state.data.accessEngine = "";
  state.data.accessQuery = "20001";
  await render();
  assert.deepEqual(cardIDs(), ["beta", "alpha", "gamma", "missing"],
    "rendering profile search results must retain the updated node order");
  assert.equal((markup.match(/class="client-profile-row"/g) || []).length, entries.length,
    "profile search must still narrow each entry to matching profiles");
  state.data.accessQuery = "";
  canReadAgents = false;
  const beforeReadonly = calls.length;
  await render();
  assert.deepEqual(cardIDs(), ["beta", "alpha", "gamma", "missing"]);
  assert.deepEqual(calls.slice(beforeReadonly), ["/client-access"],
    "sorting must not require additional agent permissions or requests");

  setStorageAccount({ user_id: "client-order-bob" });
  assert.deepEqual(groupIDs(), ["alpha", "beta", "gamma", "missing"],
    "one account must not inherit another account's node order");
  values.set(nodeCardOrderKey, JSON.stringify(["beta", "gamma"]));
  assert.deepEqual(groupIDs(), ["beta", "gamma", "alpha", "missing"],
    "direct navigation must respect legacy node ordering");
  assert.equal(accountStorage.getItem(nodeCardOrderKey), null,
    "grouping filtered exports must not finalize legacy order migration");
  for (const invalid of ["[]", "invalid JSON"]) {
    accountStorage.setItem(nodeCardOrderKey, invalid);
    assert.deepEqual(groupIDs(), ["alpha", "beta", "gamma", "missing"],
      "empty or invalid scoped preferences must fall back to node list order");
  }

  Object.defineProperty(globalThis, "localStorage", { configurable: true, get() {
    throw new DOMException("Storage access denied", "SecurityError");
  } });
  assert.deepEqual(groupIDs(), ["alpha", "beta", "gamma", "missing"],
    "blocked browser storage must not break client ordering");
  assert.deepEqual(groupIDs(entries, []), ["gamma", "alpha", "missing", "beta"],
    "without metadata or saved order, exports must retain their source order");
  assert.deepEqual(entries.map(entry => entry.agent_id),
    ["gamma", "alpha", "missing", "beta", "alpha", "gamma"],
    "sorting must not mutate the API response");
} finally {
  setStorageAccount(null);
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
  if (previousStorage) Object.defineProperty(globalThis, "localStorage", previousStorage);
  else delete globalThis.localStorage;
}
console.log("Client access node ordering smoke passed");
