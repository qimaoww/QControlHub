import assert from "node:assert/strict";
import { clientAccessExport } from "../modules/client-access-model.js";

import { clientAccessAddressChoices, clientAccessBatchExport, clientAccessRecordKey, filterClientAccessEntries, groupClientAccessEntries, normalizeClientAccessFilters } from "../modules/client-access-model.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const exportSample = { profile: { format: "VLESS URI", uri: "vless://example" }, mihomo_yaml: '{name: "Example", type: vless, server: "example.test", port: 443}' };
assert.equal(clientAccessExport(exportSample, "url").value, exportSample.profile.uri);
assert.equal(clientAccessExport(exportSample, "mihomo").value, exportSample.mihomo_yaml);
assert.equal(clientAccessExport({ ...exportSample, mihomo_error: "unsupported" }, "mihomo").value, exportSample.profile.uri);
assert.equal(clientAccessExport({ profile: exportSample.profile }, "mihomo").format, "URL");
const yaml = { profile: { format: "Mihomo Mieru YAML", uri: "{type: mieru, server: edge.test, port: 1234}" } };
assert.equal(clientAccessExport(yaml, "url").value, yaml.profile.uri);
assert.equal(clientAccessExport(yaml, "url").outputs[0].extension, "yaml");
assert.equal(clientAccessExport({ profile: { format: "URL", uri: 'name: broken' } }, "url").value, "");
assert.equal(clientAccessExport({ profile: { format: "VMess v2 JSON URI", uri: "vmess://eyJ2IjoiMiJ9" } }, "url").error, "");
const native = { profile: { format: 'sing-box client JSON', uri_error: 'no lossless URL', uri: '{"outbounds":[{"type":"vless","server":"edge.test","tls":{"enabled":true}}]}' } };
assert.deepEqual(JSON.parse(clientAccessExport(native, 'url').value), JSON.parse(native.profile.uri));
assert.equal(clientAccessExport(native, 'url').outputs[0].mime, 'application/json');
for (const [format, uri] of [['Surge Snell', 'edge = snell, edge.test, 1234, psk=fixture'], ['WireGuard native config', '[Interface]\nPrivateKey = fixture\n[Peer]\nEndpoint = edge.test:1234']]) {
  const exported = clientAccessExport({profile:{format,uri}}, 'url');
  assert.equal(exported.value, uri); assert.equal(exported.outputs[0].extension, 'conf');
}
const variants = { profile: { ...native.profile, variants: [
  {name:'direct', profile:exportSample.profile, mihomo_yaml:exportSample.mihomo_yaml},
  {name:'split', profile:{uri_error:'no URL', xray_json:native.profile.uri}},
] } };
for (const format of ['url','mihomo']) {
  const exported = clientAccessExport(variants, format);
  assert.equal(exported.outputs.length, 2);assert.equal(exported.errors.length, 0);
  assert.equal(exported.outputs[1].value, native.profile.uri);
  assert.equal(exported.outputs[0].value, format === 'url' ? exportSample.profile.uri : exportSample.mihomo_yaml);
}
assert.ok(clientAccessExport({error:'missing credential',profile:exportSample.profile},'url').error);
assert.equal(clientAccessExport({profile:{uri:'vless://uuid@host:443',uri_error:'use JSON'}},'url').value, '');
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
assert.equal(accessEntries[0].profiles[0].address_overridden, true);
const batchEntries = [
  {...accessEntries[0], config_id:'one', profiles:[{...accessEntries[0].profiles[0], ...native}]},
  {...accessEntries[0], engine:'xray', config_id:'two', profiles:[{...accessEntries[0].profiles[0], ...variants}]},
  {...accessEntries[0], agent_id:'different', agent_name:accessEntries[0].agent_name, profiles:[{...accessEntries[0].profiles[0], ...yaml}]},
];
assert.notEqual(clientAccessRecordKey(batchEntries[0],batchEntries[0].profiles[0]),clientAccessRecordKey(batchEntries[1],batchEntries[1].profiles[0]));
const batch = clientAccessBatchExport(groupClientAccessEntries([...batchEntries, batchEntries[0]]),'url');
assert.equal(batch.outputs.length,4,'same tag/port and same name must retain distinct records and schemes');
assert.equal(batch.errors.length,0);assert.ok(batch.value.includes(native.profile.uri));assert.ok(batch.value.includes('[different]'));
const partial = clientAccessBatchExport(groupClientAccessEntries([...batchEntries,{agent_id:'missing',engine:'xray',address_required:true,profiles:[]}]),'url');
assert.equal(partial.outputs.length,4);assert.equal(partial.errors.length,1);
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
