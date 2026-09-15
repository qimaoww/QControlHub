import assert from "node:assert/strict";

import { agentStructureSignature } from "../modules/agents.js";

import {
  migrateLegacyNodeOrder,
  nodeCardOrderKey,
  orderNodesBySavedOrder,
  orderedNodeList,
  savedNodeOrder,
  saveNodeOrder,
} from "../modules/node-order.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const state = { data: {}, session: { role: "admin" } };
const noop = () => {};

const nodeOrderStorage = {
  value: null,
  getItem(key) {
    assert.equal(key, nodeCardOrderKey);
    return this.value;
  },
  setItem(key, value) {
    assert.equal(key, nodeCardOrderKey);
    this.value = value;
  },
};
saveNodeOrder(["node-c", "node-a", "node-c", ""], nodeOrderStorage);
assert.deepEqual(
  savedNodeOrder(nodeOrderStorage),
  ["node-c", "node-a"],
  "saved node order ignores duplicate and empty identifiers",
);
const unorderedNodes = [
  { id: "node-a" },
  { id: "node-b" },
  { id: "node-c" },
  { id: "node-d" },
];
assert.deepEqual(
  orderNodesBySavedOrder(unorderedNodes, savedNodeOrder(nodeOrderStorage)).map(
    (node) => node.id,
  ),
  ["node-c", "node-a", "node-b", "node-d"],
  "sidebars keep the dragged node order and append unknown nodes stably",
);

// The browser-wide order written by older builds migrates into the
// account-scoped key on first use, keeping only nodes this account can see.
const legacyOrderStorage = {
  value: JSON.stringify(["node-d", "node-b", "foreign-node"]),
  getItem(key) {
    assert.equal(key, nodeCardOrderKey);
    return this.value;
  },
  setItem(key, value) {
    assert.equal(key, nodeCardOrderKey);
    this.value = value;
  },
};
const scopedOrderStorage = {
  value: null,
  getItem(key) {
    assert.equal(key, nodeCardOrderKey);
    return this.value;
  },
  setItem(key, value) {
    assert.equal(key, nodeCardOrderKey);
    this.value = value;
  },
};
assert.deepEqual(
  orderedNodeList(unorderedNodes, scopedOrderStorage, legacyOrderStorage).map(
    (node) => node.id,
  ),
  ["node-d", "node-b", "node-a", "node-c"],
  "legacy browser-wide node order migrates into the account-scoped key",
);
assert.deepEqual(
  JSON.parse(scopedOrderStorage.value),
  ["node-d", "node-b"],
  "migration keeps only nodes the current account can see",
);
migrateLegacyNodeOrder(unorderedNodes, scopedOrderStorage, legacyOrderStorage);
assert.deepEqual(
  JSON.parse(scopedOrderStorage.value),
  ["node-d", "node-b"],
  "an existing scoped order is never overwritten by the legacy value",
);
assert.deepEqual(
  unorderedNodes.map((node) => node.id),
  ["node-a", "node-b", "node-c", "node-d"],
  "node ordering does not mutate the API response",
);

assert.equal(
  agentStructureSignature([
    { id: "beta" },
    { id: "alpha" },
  ]),
  agentStructureSignature([
    { id: "alpha" },
    { id: "beta" },
  ]),
  "Agent structure signatures ignore response ordering",
);
assert.notEqual(
  agentStructureSignature([{ id: "alpha", capabilities: ["mihomo"] }]),
  agentStructureSignature([
    { id: "alpha", capabilities: ["mihomo"] },
    { id: "new-node", capabilities: [] },
  ]),
  "Agent structure signatures detect newly enrolled nodes",
);

assert.notEqual(
  agentStructureSignature([{ id: "alpha", capabilities: ["mihomo"] }]),
  agentStructureSignature([{ id: "alpha", capabilities: [] }]),
  "Capability changes from another session trigger a structural refresh",
);
  return { state, noop };
}
