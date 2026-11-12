import assert from "node:assert/strict";
import { webcrypto } from "node:crypto";
import { requestID } from "./analytics/request-id.js";

const descriptor = Object.getOwnPropertyDescriptor(globalThis, "crypto");
try {
  Object.defineProperty(globalThis, "crypto", { configurable: true, value: {
    getRandomValues: webcrypto.getRandomValues.bind(webcrypto),
  } });
  assert.equal(globalThis.crypto.randomUUID, undefined);
  const ids = Array.from({ length: 1000 }, () => requestID());
  ids.forEach(id => assert.match(id, /^[0-9a-f]{32}$/));
  assert.equal(new Set(ids).size, ids.length);
} finally {
  if (descriptor) Object.defineProperty(globalThis, "crypto", descriptor);
  else delete globalThis.crypto;
}
console.log("Request IDs work without the secure-context randomUUID API");
