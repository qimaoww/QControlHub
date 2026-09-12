import assert from "node:assert/strict";
import { accountStorage, setStorageAccount } from "./modules/account-storage.js";

const previous = globalThis.localStorage;
const values = new Map([["logs", "legacy private query"]]);
globalThis.localStorage = {
  getItem: key => values.get(key) ?? null,
  setItem: (key, value) => values.set(key, String(value)),
};
try {
  setStorageAccount({ user_id: "alice", role: "user" });
  assert.equal(accountStorage.getItem("logs"), null, "unscoped legacy data is never inherited");
  accountStorage.setItem("logs", "alice private query");
  setStorageAccount({ user_id: "bob", role: "user" });
  assert.equal(accountStorage.getItem("logs"), null);
  accountStorage.setItem("logs", "bob private query");
  setStorageAccount(null);
  assert.equal(accountStorage.getItem("logs"), null);
  setStorageAccount({ user_id: "alice", role: "user" });
  assert.equal(accountStorage.getItem("logs"), "alice private query");
  setStorageAccount({ user_id: "", role: "user", workspace_id: "token_alice" });
  accountStorage.setItem("logs", "legacy token Alice");
  setStorageAccount({ user_id: "", role: "user", workspace_id: "token_bob" });
  assert.equal(accountStorage.getItem("logs"), null, "separate legacy tokens must not share a browser namespace");
  setStorageAccount({ user_id: "", role: "user", workspace_id: "token_alice" });
  assert.equal(accountStorage.getItem("logs"), "legacy token Alice");
} finally {
  setStorageAccount(null);
  if (previous === undefined) delete globalThis.localStorage;
  else globalThis.localStorage = previous;
}
console.log("Account-scoped browser storage smoke passed");
