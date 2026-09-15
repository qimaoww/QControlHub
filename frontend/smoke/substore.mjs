import assert from "node:assert/strict";

import {
  filterSubStoreProfiles,
  groupSubStoreProfiles,
  installSubStoreSync,
  subStoreAddressChoices,
  subStoreProfileNodeCount,
  subStoreSelectionPayload,
} from "../modules/substore-sync.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
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
const previousSubStoreDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  let subStoreMarkup = "";
  const subStoreState = { route: "substore-sync", navigationEpoch: 1, data: {} };
  const renderSubStore = installSubStoreSync({
    state: subStoreState,
    api: async (path) => {
      assert.equal(path, "/substore-sync");
      return {
        settings: {
          configured: true,
          endpoint_hint: "https://substore.example/••••••",
        },
        targets: [{
          id: "sst_primary",
          display_name: "Primary review",
          subscription_name: "QControlHub",
          sync_mode: "incremental",
          selection_count: 1,
          last_sync_status: "success",
          last_synced_at: "2026-08-28T00:00:00Z",
        }],
        target_id: "sst_primary",
        profiles: subStoreProfiles.map((profile) => ({ ...profile })),
        selections: subStoreSelectionPayload(subStoreProfiles),
      };
    },
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    notify: noop,
    shell: (markup) => {
      subStoreMarkup = markup;
    },
  });
  await renderSubStore();
  assert.equal(subStoreMarkup.includes("substore-status-bar"), true);
  assert.equal(subStoreMarkup.includes("substore-agent-card"), true);
  assert.equal(subStoreMarkup.includes("Tokyo Premium"), true);
  assert.equal(subStoreMarkup.includes("VLESS · :443"), true);
  assert.equal(subStoreMarkup.includes("data-substore-target-add"), true);
  assert.equal(subStoreMarkup.includes("data-substore-settings-dialog"), true);
  assert.equal(subStoreMarkup.includes("仅修改面板名称"), true);
  assert.equal(subStoreMarkup.includes("同时修改远端组名"), true);
  assert.equal(subStoreMarkup.includes("增量模式"), true);
  assert.equal(subStoreMarkup.includes("完全托管模式"), true);
  assert.equal(subStoreMarkup.includes("substore-node-settings-row"), true);
  assert.equal(subStoreMarkup.includes("Sub-Store 已有组"), true);
  assert.equal(subStoreMarkup.includes("仅移除面板中的同步关系"), true);
  assert.equal(subStoreMarkup.includes("substore-hero"), false);
  assert.equal(subStoreMarkup.includes("<h1"), false, "sync page does not repeat a large title hero");
} finally {
  if (previousSubStoreDocument === undefined) delete globalThis.document;
  else globalThis.document = previousSubStoreDocument;
}

}
