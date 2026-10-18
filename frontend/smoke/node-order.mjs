import assert from "node:assert/strict";

import { agentStructureSignature } from "../modules/agents.js";

import {
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
