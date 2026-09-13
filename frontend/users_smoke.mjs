import assert from "node:assert/strict";
import { mergeUserAllocation, parseSharedPorts, sharedLimitBytes, sharedLimitGiB } from "./modules/users.js";

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
const shares = [
  { id: "first", agent_id: "alpha", enabled: true, status: "accepted", engines: ["mihomo"], ports: [21001], limit_bytes: 10737418, used_bytes: 128 },
  { id: "second", agent_id: "bravo", enabled: false, status: "rejected", engines: ["xray"], ports: [22001], limit_bytes: 15250032, used_bytes: 256, reinvite: false },
];
const original = structuredClone(shares);
const edited = mergeUserAllocation(shares, { agent_id: "alpha", ports: [21002] });
assert.deepEqual(edited, [
  { agent_id: "alpha", enabled: true, engines: ["mihomo"], ports: [21002], limit_bytes: 10737418 },
  { agent_id: "bravo", enabled: false, engines: ["xray"], ports: [22001], limit_bytes: 15250032 },
], "editing one share must retain all other reservations and exact quota bytes");
const revoked = mergeUserAllocation(shares, { agent_id: "alpha", enabled: false });
assert.deepEqual(revoked[0], { ...edited[0], enabled: false, ports: [21001] }, "revocation must not clear ports or quota");
const invited = mergeUserAllocation(shares, { agent_id: "bravo", enabled: true, reinvite: true });
assert.equal(invited[1].reinvite, true);
assert.equal("reinvite" in invited[0], false, "editing a different node must not replay invitation actions");
const added = { agent_id: "charlie", enabled: true, engines: ["sing-box"], ports: [], limit_bytes: 0 };
assert.deepEqual(mergeUserAllocation(shares, added), [{ ...edited[0], ports: [21001] }, edited[1], added]);
assert.deepEqual(mergeUserAllocation([], added), [added]);
edited[1].engines.push("mihomo");
edited[1].ports.push(22002);
assert.deepEqual(shares, original, "merging an allocation mutated the saved access snapshot");
console.log("User allocation input and merge smoke passed");
