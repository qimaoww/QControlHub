import assert from "node:assert/strict";
import { formatSharedPorts, mergeUserAllocation, parseSharedPorts, sharedLimitBytes, sharedLimitGiB, sharedPortsLabel } from "./modules/users.js";

assert.deepEqual(parseSharedPorts(" 31002，31001\n31003 "), [31001, 31002, 31003]);
assert.deepEqual(parseSharedPorts(""), []);
assert.deepEqual(parseSharedPorts(" \n\t "), [], "blank ports must remain unallocated");
assert.deepEqual(parseSharedPorts(" 0 "), [0], "zero alone must mean unrestricted port choice");
assert.deepEqual(parseSharedPorts(0), [0]);
assert.deepEqual(parseSharedPorts("21000-21100"), Array.from({ length: 101 }, (_, index) => 21000 + index),
  "port ranges must include both endpoints");
assert.deepEqual(parseSharedPorts("22000, 21000 - 21002，23000-23001\n65535 1-1"),
  [1, 21000, 21001, 21002, 22000, 23000, 23001, 65535], "single ports and ranges must mix across existing separators");
assert.deepEqual(parseSharedPorts("00001-00002, 65535-65535"), [1, 2, 65535]);
assert.deepEqual(parseSharedPorts("21000 \n-\t21002"), [21000, 21001, 21002]);
assert.deepEqual(parseSharedPorts(`1${" ".repeat(10000)}2`), [1, 2], "whitespace without a range marker must remain a separator");
assert.deepEqual(parseSharedPorts("21000-21255"), Array.from({ length: 256 }, (_, index) => 21000 + index),
  "the existing 256-port limit must count expanded ports");
for (const input of [
  "1,01", "65536", "-1", "1.1", "1e3", "10085", "10086", "22,abc",
  "0,21001", "21001 0", "0，0", "0,21000-21100", "0-0",
  "0-1", "65535-65536", "21100-21000", "21000-", "-21000", "21000--21100",
  "21000-21001-21002", "1.5-3", "1e3-1003", "21000/21100",
]) {
  assert.throws(() => parseSharedPorts(input), /端口/);
}
for (const input of ["21000-21002, 21002", "21000-21002, 21001-21003", "1-2,01"]) {
  assert.throws(() => parseSharedPorts(input), /重复|重叠/, "overlapping ranges must not silently change a grant");
}
for (const input of ["10084-10085", "10086-10087", "10084-10087"]) {
  assert.throws(() => parseSharedPorts(input), /保留/, "ranges must not bypass reserved ports");
}
for (const input of ["21000-21256", "21000-21255, 22000", Array.from({ length: 257 }, (_, index) => index + 1).join(",")]) {
  assert.throws(() => parseSharedPorts(input), /256/, "oversized ranges must fail before submitting");
}
assert.equal(formatSharedPorts(), "");
assert.equal(formatSharedPorts(null), "");
assert.equal(formatSharedPorts([]), "");
assert.equal(formatSharedPorts([0]), "0", "the editor must round-trip the unrestricted sentinel");
assert.equal(sharedPortsLabel([0]), "无限制");
assert.equal(sharedPortsLabel([]), "未分配");
assert.equal(sharedPortsLabel(null), "未分配");
assert.equal(sharedPortsLabel([21000, 21001, 21002]), "21000-21002");
assert.equal(formatSharedPorts(Object.freeze([21003, 22000, 21001, 21002, 65535])), "21001-21003, 22000, 65535",
  "stored ports must compact into exact ranges without mutating the API data");
assert.equal(formatSharedPorts([1, 2, 4, 65534, 65535]), "1-2, 4, 65534-65535", "formatting must not fill gaps");
for (const text of ["", "0", "1,65535", "21000-21100", "21000-21255", "21000-21002, 22000, 23000-23001"]) {
  const ports = parseSharedPorts(text);
  assert.deepEqual(parseSharedPorts(formatSharedPorts(ports)), ports, "editing formatted ranges must preserve exact port reservations");
}
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
const unrestrictedShares = mergeUserAllocation(shares, { agent_id: "bravo", ports: [0] });
assert.deepEqual(mergeUserAllocation(unrestrictedShares, { agent_id: "alpha", ports: [] })[1].ports, [0],
  "editing another allocation must preserve unrestricted authority");
assert.deepEqual(mergeUserAllocation(unrestrictedShares, { agent_id: "bravo", ports: [] })[1].ports, [],
  "explicitly clearing an unrestricted allocation must leave it unallocated");
edited[1].engines.push("mihomo");
edited[1].ports.push(22002);
assert.deepEqual(shares, original, "merging an allocation mutated the saved access snapshot");
console.log("User allocation input and merge smoke passed");
