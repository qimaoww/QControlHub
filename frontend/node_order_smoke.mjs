import assert from "node:assert/strict";
import { accountStorage, setStorageAccount } from "./modules/account-storage.js";
import {
  migrateLegacyNodeOrder, nodeCardOrderKey, orderedNodeList, orderNodesBySavedOrder,
} from "./modules/node-order.js";

const previous = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
const values = new Map([[nodeCardOrderKey, JSON.stringify(["d", "b", "foreign", "d", "", null])]]);
const nodes = ["a", "b", "c", "d"].map(id => ({ id }));
Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
  getItem: key => values.get(key) ?? null,
  setItem: (key, value) => values.set(key, String(value)),
} });
try {
  setStorageAccount({ user_id: "order-alice" });
  assert.deepEqual(orderNodesBySavedOrder([nodes[0], nodes[1]]).map(node => node.id), ["b", "a"],
    "a directly opened partial sidebar must use legacy order");
  assert.equal(accountStorage.getItem(nodeCardOrderKey), null,
    "a partial sidebar must not finalize migration and discard other visible nodes");
  assert.deepEqual(orderNodesBySavedOrder(nodes).map(node => node.id), ["d", "b", "a", "c"]);
  migrateLegacyNodeOrder(nodes);
  assert.deepEqual(JSON.parse(accountStorage.getItem(nodeCardOrderKey)), ["d", "b"],
    "full-fleet migration must filter foreign and duplicate identifiers");
  accountStorage.setItem(nodeCardOrderKey, "[]");
  assert.deepEqual(orderNodesBySavedOrder(nodes), nodes, "an explicitly empty scoped order wins");
  migrateLegacyNodeOrder(nodes);
  assert.equal(accountStorage.getItem(nodeCardOrderKey), "[]");
  accountStorage.setItem(nodeCardOrderKey, "");
  migrateLegacyNodeOrder(nodes);
  assert.equal(accountStorage.getItem(nodeCardOrderKey), "", "an existing scoped key must never be overwritten");

  setStorageAccount({ user_id: "order-bob" });
  migrateLegacyNodeOrder([]);
  assert.equal(accountStorage.getItem(nodeCardOrderKey), null, "empty initial data must not finalize migration");
  migrateLegacyNodeOrder([nodes[3]]);
  assert.deepEqual(JSON.parse(accountStorage.getItem(nodeCardOrderKey)), ["d"],
    "another account must migrate only its own visible subset");

  Object.defineProperty(globalThis, "localStorage", { configurable: true, get() {
    throw new DOMException("Storage access denied", "SecurityError");
  } });
  assert.deepEqual(orderedNodeList(nodes), nodes, "blocked browser storage must not break node rendering");
  assert.deepEqual(orderNodesBySavedOrder(nodes), nodes, "blocked browser storage must not break sidebars");
} finally {
  setStorageAccount(null);
  if (previous) Object.defineProperty(globalThis, "localStorage", previous);
  else delete globalThis.localStorage;
}
console.log("Node order migration and direct-sidebar smoke passed");
