import assert from "node:assert/strict";
import { clientAccessExport } from "../modules/client-access-model.js";

import { clientAccessAddressChoices, clientAccessEntryForAddress, filterClientAccessEntries, groupClientAccessEntries, normalizeClientAccessFilters } from "../modules/client-access-model.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const exportSample = { profile: { format: "VLESS URI", uri: "vless://example" }, mihomo_yaml: '{name: "Example", type: vless, server: "example.test", port: 443}' };
assert.equal(clientAccessExport(exportSample, "url").value, exportSample.profile.uri);
assert.equal(clientAccessExport(exportSample, "mihomo").value, exportSample.mihomo_yaml);
assert.equal(clientAccessExport({ ...exportSample, mihomo_error: "unsupported" }, "mihomo").value, "");
assert.ok(clientAccessExport({ profile: exportSample.profile }, "mihomo").error);
assert.ok(clientAccessExport({ profile: { format: "Mihomo Mieru YAML", uri: "{type: mieru}" } }, "url").error);
// Content determines URL availability; labels such as "JSON URI" do not.
for (const value of ['{"outbounds":[]}', 'socks5 = socks5, host, 1080', 'name: example\ntype: ss']) {
  assert.equal(clientAccessExport({ profile: { format: "URL", uri: value } }, "url").value, "");
}
assert.equal(clientAccessExport({ profile: { format: "VMess v2 JSON URI", uri: "vmess://eyJ2IjoiMiJ9" } }, "url").error, "");
assert.equal(clientAccessExport({ profile: { uri: "vless://uuid@host:443#name", uri_error: "使用 JSON" } }, "url").value, "");
const accessEntries = [
  {
    agent_id: "alpha",
    agent_name: "Alpha node",
    engine: "mihomo",
    address: "alpha.example.test",
    source: "test",
    profiles: [
      {
        tag: "alpha-in",
        port: 20001,
        protocol: "test",
        address: "198.51.100.10",
        address_mode: "ipv6",
        address_overridden: true,
        profile: { format: "URI", uri: "test-alpha", fields: [] },
      },
    ],
    address_options: [
      { family: "ipv4", address: "198.51.100.10", source: "IPv4", profiles: [{tag: "alpha-in", port: 20001, protocol: "test", profile: {format: "URI", uri: "test-alpha", fields: []}}] },
      { family: "ipv6", address: "2001:db8::10", source: "IPv6", profiles: [{tag: "alpha-in", port: 20001, protocol: "test", profile: {format: "URI", uri: "test-alpha-v6", fields: []}}] },
    ],
  },
  {
    agent_id: "beta",
    agent_name: "Beta node",
    engine: "xray",
    address: "beta.example.test",
    source: "test",
    profiles: [
      {
        tag: "beta-in",
        protocol: "test",
        profile: { format: "URI", uri: "test-beta", fields: [] },
      },
    ],
  },
];
assert.deepEqual(
  clientAccessAddressChoices(accessEntries[0]).map((choice) => choice.value),
  ["auto", "ipv4", "ipv6"],
  "client configuration exposes both address families",
);
assert.equal(
  clientAccessEntryForAddress(accessEntries[0], "ipv6").address,
  "2001:db8::10",
  "client configuration switches to the IPv6 profile variant",
);
const accessAgents = [
  { id: "alpha", name: "Alpha node", labels: {}, status: "online" },
  { id: "beta", name: "Beta node", labels: {}, status: "online" },
];
assert.deepEqual(
  normalizeClientAccessFilters(accessEntries, accessAgents, {
    agent: "removed-node",
    engine: "xray",
    query: "  BETA  ",
  }),
  { agent: "", engine: "xray", query: "BETA" },
  "deleted node selections are discarded while valid global filters remain",
);
assert.deepEqual(
  normalizeClientAccessFilters(accessEntries, accessAgents, {
    agent: "alpha",
    engine: "xray",
  }),
  { agent: "alpha", engine: "", query: "" },
  "an engine unavailable on the selected node is discarded",
);
assert.deepEqual(
  filterClientAccessEntries(accessEntries, { query: "BETA-IN" }).map(
    (entry) => entry.agent_id,
  ),
  ["beta"],
  "client search covers profile tags case-insensitively",
);
assert.deepEqual(
  groupClientAccessEntries([
    accessEntries[0],
    { ...accessEntries[0], engine: "sing-box" },
    accessEntries[1],
  ]).map((group) => [group.agent_id, group.entries.length]),
  [
    ["alpha", 2],
    ["beta", 1],
  ],
  "multiple engine exports for one node share one node card",
);
// Actual native client-layout desktop/mobile modes now cover modern clipboard,
// rejected/missing clipboard fallback, masked values, modal containment,
// original focus/selection, denial and duplicate locks. Native client-order
// modes retain sorting/filter/reset/refresh/readonly behavior; ports modes
// retain per-port address families, manual overrides and display writes.
}
