import assert from "node:assert/strict";
import { parseSharedPorts, sharedLimitBytes, sharedLimitGiB } from "./modules/users.js";

assert.deepEqual(parseSharedPorts(" 31002，31001\n31003 "), [31001, 31002, 31003]);
assert.deepEqual(parseSharedPorts(""), []);
for (const input of ["1,01", "0", "65536", "-1", "1.1", "1e3", "10085", "10086", "22,abc"]) {
  assert.throws(() => parseSharedPorts(input), /端口/);
}
assert.throws(() => parseSharedPorts(Array.from({ length: 257 }, (_, index) => index + 1).join(",")));
assert.equal(sharedLimitBytes("0"), 0);
assert.equal(sharedLimitBytes("0.5"), 536870912);
assert.equal(sharedLimitBytes("0.01"), 10737418);
assert.equal(sharedLimitGiB(10737418), "0.01");
for (const bytes of [0, 1, 128, 10737418, 15250032, 1024 ** 3, 9_000_000_000_000_000]) {
  assert.equal(sharedLimitBytes(sharedLimitGiB(bytes)), bytes, "display formatting changed the stored quota");
}
for (const value of ["", "-1", "Infinity", "NaN", "0.00000000001", "8388608"]) {
  assert.throws(() => sharedLimitBytes(value), /额度/);
}
console.log("User allocation input smoke passed");
