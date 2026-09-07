import assert from "node:assert/strict";
import { systemBBRState, validateTCPSelection } from "./modules/system-bbr.js";

const now = Date.now();
const agent = { features: ["system-bbr-v1"], status: "online", metrics: { bbr: {
  collected_at: new Date(now).toISOString(), available: true, congestion_control: "bbr",
  default_qdisc: "fq_codel", persistence: "unmanaged",
} } };
assert.match(systemBBRState(agent, now).text, /BBR 已启用/);
assert.equal(systemBBRState(agent, now).controllable, true, "external BBR can be inspected and managed");
assert.equal(systemBBRState({ ...agent, status: "offline" }, now).controllable, false);
assert.match(systemBBRState({ ...agent, features: [] }, now).text, /升级/);
assert.match(systemBBRState(agent, now + 91_000).text, /等待/);
assert.match(systemBBRState({ ...agent, metrics: {} }, now).text, /等待/);
const changed = structuredClone(agent);
changed.metrics.bbr.congestion_control = "bbr3";
assert.match(systemBBRState(changed, now).text, /bbr3/);
changed.metrics.bbr.persistence = "error";
assert.equal(systemBBRState(changed, now).controllable, false);
const rules = [
  { key: "net.ipv4.tcp_rmem", label: "缓冲区", tuple: true, min: 1, max: 1073741824 },
  { key: "net.ipv4.tcp_ecn", label: "ECN", max: 2 },
  { key: "net.ipv4.tcp_congestion_control", label: "算法", choices: ["bbr", "cubic"] },
];
assert.deepEqual(validateTCPSelection({ "net.ipv4.tcp_rmem": "4096\t87380 16777216", "net.ipv4.tcp_ecn": "02" }, rules), {
  "net.ipv4.tcp_rmem": "4096 87380 16777216", "net.ipv4.tcp_ecn": "2",
});
for (const settings of [{}, { "kernel.sysrq": "1" }, { "net.ipv4.tcp_ecn": "3" }, { "net.ipv4.tcp_rmem": "8192 4096 16384" }, { "net.ipv4.tcp_rmem": "4096 8192" }, { "net.ipv4.tcp_congestion_control": "bbr\nnet.ipv4.ip_forward=1" }])
  assert.throws(() => validateTCPSelection(settings, rules));
for (const value of ["+1", "0".repeat(101), "\u3000".repeat(34) + "1"])
  assert.throws(() => validateTCPSelection({ "net.ipv4.tcp_ecn": value }, rules));
console.log("system BBR/TCP module smoke passed");
