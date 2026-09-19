import assert from "node:assert/strict";

import { batchAgentEligibility, batchSelectAllState } from "../modules/agents.js";

import { coreLogFilterCounts, filterCoreLogEntries } from "../modules/core-logs.js";

import { trafficRateForDisplay } from "../modules/traffic.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const trafficRateNow = Date.parse("2026-08-28T00:00:30Z");
for (const invalidRate of [-1, NaN, Infinity, "invalid"]) {
  assert.equal(trafficRateForDisplay(invalidRate, "2026-08-28T00:00:15Z", "online", trafficRateNow), 0);
}
assert.equal(
  trafficRateForDisplay(4096, "2026-08-28T00:00:15Z", "online", trafficRateNow),
  4096,
  "a fresh online traffic rate remains visible",
);
assert.equal(
  trafficRateForDisplay(4096, "2026-08-28T00:00:15Z", "offline", trafficRateNow),
  0,
  "an offline Agent never keeps a stale traffic rate visible",
);
assert.equal(
  trafficRateForDisplay(4096, "2026-08-27T23:59:00Z", "online", trafficRateNow),
  0,
  "an old traffic sample decays to zero",
);

const coreLogFilterFixture = [
  { engine: "mihomo", level: "debug", message: "bootstrap complete" },
  { engine: "xray", level: "info", message: "Accepted GitHub connection" },
  { engine: "sing-box", level: "warning", message: "slow handshake" },
  { engine: "ss-rust", level: "critical", message: "upstream timeout" },
];
assert.deepEqual(
  filterCoreLogEntries(coreLogFilterFixture, {
    engine: "xray",
    level: "info",
    q: "github",
  }),
  [coreLogFilterFixture[1]],
  "core log engine, level, and keyword filters compose locally",
);
assert.deepEqual(
  coreLogFilterCounts(coreLogFilterFixture, [
    "mihomo",
    "xray",
    "sing-box",
    "ss-rust",
  ]),
  {
    total: 4,
    engine: { mihomo: 1, xray: 1, "sing-box": 1, "ss-rust": 1 },
    level: { info: 2, warning: 1, error: 1 },
  },
  "core log buttons keep names separate from accurate result counts",
);

assert.deepEqual(
  batchAgentEligibility(
    { status: "online", features: ["agent-self-upgrade-v1"] },
    "upgrade-agent",
    "",
  ),
  { eligible: true, reason: "在线 · 支持远程升级" },
);
assert.match(
  batchAgentEligibility({ status: "offline" }, "upgrade-agent", "").reason,
  /离线/,
);
assert.match(
  batchAgentEligibility({ status: "online", features: [] }, "upgrade-agent", "")
    .reason,
  /旧版 Agent/,
);
assert.equal(
  batchAgentEligibility(
    { status: "online", runtime: { mihomo: { installed: true } } },
    "restart",
    "mihomo",
  ).eligible,
  true,
);
assert.equal(
  batchAgentEligibility(
    { status: "online", runtime: { mihomo: { installed: false } } },
    "restart",
    "mihomo",
  ).eligible,
  false,
);
for (const agent of [
  { status: "offline", runtime: { mihomo: { installed: true } } },
  { status: "online", can_manage: false, runtime: { mihomo: { installed: true } } },
  { status: "online", runtime: { mihomo: { installed: false } } },
  { status: "online", runtime: { mihomo: { installed: true, existing_config_unsupported_reason: "不可自动迁移" } } },
]) {
  assert.equal(batchAgentEligibility(agent, "install", "mihomo").eligible, false);
}
assert.equal(batchAgentEligibility({ status: "online", runtime: { mihomo: { installed: true } } }, "install", "mihomo").eligible, true);
assert.deepEqual(
  batchSelectAllState([
    { disabled: false, checked: true },
    { disabled: false, checked: false },
    { disabled: true, checked: true },
  ]),
  { eligible: 2, selected: 1, checked: false, indeterminate: true },
);
assert.deepEqual(
  batchSelectAllState([
    { disabled: false, checked: true },
    { disabled: false, checked: true },
  ]),
  { eligible: 2, selected: 2, checked: true, indeterminate: false },
);
assert.deepEqual(
  batchSelectAllState([
    { disabled: true, checked: true, dataset: { batchEligible: "1" } },
    { disabled: true, checked: true, dataset: { batchEligible: "1" } },
    { disabled: true, checked: false, dataset: { batchEligible: "0" } },
  ]),
  { eligible: 2, selected: 2, checked: true, indeterminate: false },
  "busy interaction locks do not erase the qualified selection state",
);
assert.deepEqual(batchSelectAllState([]), {
  eligible: 0,
  selected: 0,
  checked: false,
  indeterminate: false,
});

}
