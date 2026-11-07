import assert from "node:assert/strict";

import { filterSubStoreProfiles, groupSubStoreProfiles, subStoreAddressChoices, subStoreProfileNodeCount, subStoreSelectionPayload } from "../modules/substore-model.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const subStoreProfiles = [
  {
    agent_id: "alpha",
    agent_name: "Alpha node",
    engine: "sing-box",
    profile_tag: "vless-in",
    protocol: "VLESS",
    port: 443,
    default_name: "Alpha node · vless-in",
    custom_name: "Tokyo Premium",
    selected: true,
    available: true,
  },
  {
    agent_id: "beta",
    agent_name: "Beta node",
    engine: "xray",
    profile_tag: "ss-in",
    protocol: "Shadowsocks 2022",
    port: 8443,
    default_name: "Beta node · ss-in",
    selected: false,
    available: true,
  },
];
assert.deepEqual(
  filterSubStoreProfiles(subStoreProfiles, "alpha", "premium"),
  [subStoreProfiles[0]],
  "Sub-Store node and keyword filters compose locally",
);
assert.deepEqual(
  filterSubStoreProfiles(subStoreProfiles, "", "8443"),
  [subStoreProfiles[1]],
  "Sub-Store profiles can be searched by listening port",
);
const subStoreGroups = groupSubStoreProfiles(
  [
    subStoreProfiles[1],
    subStoreProfiles[0],
    { ...subStoreProfiles[0], engine: "mihomo", profile_tag: "second-alpha" },
  ],
  [{ id: "alpha" }, { id: "beta" }, { id: "empty" }],
  ["beta", "alpha"],
);
assert.deepEqual(
  subStoreGroups.map((group) => group.agent_id),
  ["beta", "alpha"],
  "Sub-Store cards follow the saved node order",
);
assert.deepEqual(
  subStoreGroups[1].profiles.map((profile) => profile.profile_tag),
  ["vless-in", "second-alpha"],
  "Sub-Store node ordering retains profile order within each node",
);
assert.deepEqual(subStoreSelectionPayload(subStoreProfiles), [
  {
    agent_id: "alpha",
    engine: "sing-box",
    profile_tag: "vless-in",
    custom_name: "Tokyo Premium",
    address_mode: "auto",
  },
]);
assert.deepEqual(
  subStoreAddressChoices({
    addresses: [
      { family: "ipv4", address: "198.51.100.10" },
      { family: "ipv6", address: "2001:db8::10" },
    ],
  }).map((choice) => choice.value),
  ["auto", "ipv4", "ipv6", "both"],
  "dual-stack Sub-Store profiles expose automatic, family, and both-address modes",
);
assert.equal(
  subStoreProfileNodeCount({ selected: true, available: true, address_mode: "both" }),
  2,
  "dual-stack Sub-Store selections count both generated nodes",
);
// Native substore-scope/substore-layout browser modes cover the actual page,
// connection settings, source identity, target forms, sync modes and unlink
// confirmation. Keep model contracts here without retaining a classic renderer.

}
