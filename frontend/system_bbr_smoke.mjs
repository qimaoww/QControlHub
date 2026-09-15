import assert from "node:assert/strict";
import { systemBBRState, validateTCPSelection } from "./modules/system-bbr.js";
import { systemTCPPresets, prepareTCPPreset } from "./modules/system-bbr-presets.js";

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
  { key: "net.core.default_qdisc", label: "队列", choices: ["fq", "fq_codel", "fq_pie"] },
];
assert.deepEqual(validateTCPSelection({ "net.core.default_qdisc": "fq_pie" }, rules), { "net.core.default_qdisc": "fq_pie" });
assert.deepEqual(validateTCPSelection({ "net.ipv4.tcp_rmem": "4096\t87380 16777216", "net.ipv4.tcp_ecn": "02" }, rules), {
  "net.ipv4.tcp_rmem": "4096 87380 16777216", "net.ipv4.tcp_ecn": "2",
});
for (const settings of [{}, { "kernel.sysrq": "1" }, { "net.ipv4.tcp_ecn": "3" }, { "net.ipv4.tcp_rmem": "8192 4096 16384" }, { "net.ipv4.tcp_rmem": "4096 8192" }, { "net.ipv4.tcp_congestion_control": "bbr\nnet.ipv4.ip_forward=1" }])
  assert.throws(() => validateTCPSelection(settings, rules));
for (const value of ["+1", "0".repeat(101), "\u3000".repeat(34) + "1"])
  assert.throws(() => validateTCPSelection({ "net.ipv4.tcp_ecn": value }, rules));

const expectedPreset = {
  "net.core.default_qdisc": "fq",
  "net.ipv4.tcp_congestion_control": "bbr",
  "net.core.rmem_max": "33554432",
  "net.core.wmem_max": "33554432",
  "net.ipv4.tcp_rmem": "4096 65536 33554432",
  "net.ipv4.tcp_wmem": "4096 65536 33554432",
  "net.ipv4.tcp_mtu_probing": "1",
  "net.ipv4.tcp_window_scaling": "1",
};
assert.equal(systemTCPPresets.length, 1, "only the requested BBR preset is offered");
assert.equal(systemTCPPresets[0].id, "bbr-32m");
assert.equal(systemTCPPresets[0].name, "BBR · 32 MiB 缓冲区");
assert.deepEqual(systemTCPPresets[0].settings, expectedPreset, "preset contains exactly the eight requested values");
const presetRules = [
  ...rules,
  { key: "net.ipv4.tcp_wmem", label: "发送缓冲区", tuple: true, min: 1, max: 1073741824 },
  { key: "net.core.rmem_max", label: "接收上限", min: 4096, max: 1073741824 },
  { key: "net.core.wmem_max", label: "发送上限", min: 4096, max: 1073741824 },
  { key: "net.ipv4.tcp_mtu_probing", label: "MTU 探测", max: 2 },
  { key: "net.ipv4.tcp_window_scaling", label: "窗口缩放", max: 1 },
];
const currentParameters = Object.freeze({
  ...expectedPreset,
  "net.core.default_qdisc": "fq_codel",
  "net.ipv4.tcp_congestion_control": "cubic",
  "net.ipv4.tcp_ecn": "0",
});
const originalDraft = Object.freeze({ "net.ipv4.tcp_ecn": "2", "net.core.default_qdisc": "fq_pie" });
const prepared = prepareTCPPreset("bbr-32m", presetRules, currentParameters, originalDraft);
assert.deepEqual(prepared, { ...expectedPreset, "net.ipv4.tcp_ecn": "2" }, "preset replaces its own keys and preserves unrelated draft selections");
assert.notEqual(prepared, originalDraft, "preset preparation returns a fresh draft");
assert.notEqual(prepared, systemTCPPresets[0].settings, "draft edits cannot mutate the preset catalog");
prepared["net.core.default_qdisc"] = "fq_codel";
assert.equal(originalDraft["net.core.default_qdisc"], "fq_pie");
assert.deepEqual(systemTCPPresets[0].settings, expectedPreset);
assert.equal(currentParameters["net.ipv4.tcp_congestion_control"], "cubic", "preparing a preset does not apply it to reported parameters");
const nextDraft = prepareTCPPreset("bbr-32m", presetRules, currentParameters);
assert.deepEqual(nextDraft, expectedPreset, "unselected current values are not added to the draft");
assert.notEqual(nextDraft, prepared, "each preparation has its own editable draft");
assert.throws(() => prepareTCPPreset("unknown", presetRules, currentParameters, originalDraft));
for (const key of Object.keys(expectedPreset)) {
  const missingParameter = { ...currentParameters };
  delete missingParameter[key];
  assert.throws(() => prepareTCPPreset("bbr-32m", presetRules, missingParameter, originalDraft), `missing reported ${key} rejects the whole preset`);
  assert.throws(() => prepareTCPPreset("bbr-32m", presetRules.filter((rule) => rule.key !== key), currentParameters, originalDraft), `missing rule for ${key} rejects the whole preset`);
}
for (const incompatibleRule of [
  { key: "net.ipv4.tcp_congestion_control", label: "算法", choices: ["cubic"] },
  { key: "net.core.default_qdisc", label: "队列", choices: ["fq_codel"] },
  { key: "net.core.rmem_max", label: "接收上限", min: 4096, max: 16777216 },
  { key: "net.ipv4.tcp_wmem", label: "发送缓冲区", tuple: true, min: 1, max: 16777216 },
]) {
  const incompatibleRules = presetRules.map((rule) => rule.key === incompatibleRule.key ? incompatibleRule : rule);
  assert.throws(() => prepareTCPPreset("bbr-32m", incompatibleRules, currentParameters, originalDraft), `incompatible ${incompatibleRule.key} rejects the whole preset`);
}
assert.deepEqual(originalDraft, { "net.ipv4.tcp_ecn": "2", "net.core.default_qdisc": "fq_pie" }, "failed preset preparation leaves the previous draft intact");
console.log("system BBR/TCP module smoke passed");
