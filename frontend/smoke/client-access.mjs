import assert from "node:assert/strict";

import {
  copyClientValue,
  clientAccessAddressChoices,
  clientAccessEntryForAddress,
  filterClientAccessEntries,
  groupClientAccessEntries,
  installClientAccess,
  normalizeClientAccessFilters,
} from "../modules/client-access.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop, previousDocument }) {
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
let copiedClientValue = "";
assert.equal(
  await copyClientValue(
    { value: "ss://demo" },
    {
      navigatorObject: {
        clipboard: { writeText: async (value) => (copiedClientValue = value) },
      },
    },
  ),
  "clipboard",
);
assert.equal(copiedClientValue, "ss://demo");
let legacyCopySelected = false;
let legacyCopyRestored = false;
const legacyCopyInput = {
  value: "ss://legacy",
  selectionStart: 1,
  selectionEnd: 3,
  selectionDirection: "forward",
  focus: noop,
  select: () => (legacyCopySelected = true),
  setSelectionRange: (start, end, direction) => {
    legacyCopyRestored = start === 1 && end === 3 && direction === "forward";
  },
};
assert.equal(
  await copyClientValue(legacyCopyInput, {
    navigatorObject: {},
    documentObject: {
      activeElement: null,
      execCommand: (command) => command === "copy" && legacyCopySelected,
    },
  }),
  "legacy",
);
assert.equal(legacyCopyRestored, true);
const accessState = {
  route: "client-access",
  data: { accessAgent: "beta", accessEngine: "", accessQuery: "" },
};
const accessSidebarLinks = [
  { dataset: { accessAgent: "" }, onclick: null },
  { dataset: { accessAgent: "alpha" }, onclick: null },
  { dataset: { accessAgent: "beta" }, onclick: null },
];
let accessMarkup = "";
let accessAPICalls = 0;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll(selector) {
    if (selector === "[data-access-agent]") return accessSidebarLinks;
    return [];
  },
};

try {
  const accessCtx = new Proxy(
    {
      state: accessState,
      engines: ["mihomo", "xray"],
      api: async (path) => {
        accessAPICalls += 1;
        if (path === "/client-access") return accessEntries;
        if (path === "/agents") return accessAgents;
        assert.fail(`unexpected client access smoke API path ${path}`);
      },
      can: (capability) => capability === "agents.read" || capability === "agents.manage",
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      short: (value) => value,
      shell: (markup) => {
        accessMarkup = markup;
      },
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const renderClientAccess = installClientAccess(accessCtx);
  await renderClientAccess();
  assert.equal(accessAPICalls, 2);

  assert.equal(accessMarkup.includes("Alpha node"), false);
  assert.equal(accessMarkup.includes("Beta node"), true);
  assert.equal(accessMarkup.includes("data-filter-agent"), false);
  assert.equal(accessMarkup.includes("按节点筛选"), false);
  assert.equal(accessMarkup.includes('data-filter-engine=""'), true);
  assert.equal(accessMarkup.includes("按内核筛选"), true);
  assert.equal(accessMarkup.includes("client-access-toolbar"), true);
  assert.equal(accessMarkup.includes("client-access-node-card"), true);
  assert.equal(accessMarkup.includes("修改显示参数"), true);
  assert.equal(accessMarkup.includes("client-display-dialog"), true);
  assert.equal(accessMarkup.includes("客户端地址协议栈"), false);
  assert.equal(accessMarkup.includes("client-address-editor"), false);
  assert.equal(accessMarkup.includes("data-client-parameter-open"), true);
  assert.equal(accessMarkup.includes("client-parameter-dialog"), true);
  assert.equal(accessMarkup.includes("traffic-edit-dialog"), true);
  assert.equal(accessMarkup.includes("client-parameter-menu"), false);
  assert.equal(accessMarkup.includes("client-access-hero"), false);
  assert.equal(accessMarkup.includes("client-access-filter-panel"), false);
  assert.equal(
    accessSidebarLinks.every((link) => typeof link.onclick === "function"),
    true,
    "context sidebar remains the executable node filter",
  );

  await accessSidebarLinks[0].onclick({ preventDefault: noop });
  assert.equal(accessState.data.accessAgent, "");
  assert.equal(accessMarkup.includes("Alpha node"), true);
  assert.equal(accessMarkup.includes("Beta node"), true);
  assert.equal(accessMarkup.includes("客户端地址协议栈"), true);
  assert.equal(
    accessMarkup.includes('data-saved-mode="ipv6"'),
    true,
    "client profile address family is not scoped to its own port",
  );
  assert.equal(
    accessMarkup.includes("当前使用手动连接地址"),
    true,
    "a manual address does not lock the client profile family selector",
  );
  assert.equal(
    accessMarkup.includes('placeholder="留空使用自动识别地址"'),
    true,
    "client profile address field no longer defaults to the automatic address",
  );
  assert.equal(
    accessAPICalls,
    2,
    "switching the local node filter does not refetch client access data",
  );

  accessState.data.accessEngine = "xray";
  await renderClientAccess();
  assert.equal(accessMarkup.includes("Alpha node"), false);
  assert.equal(accessMarkup.includes("Beta node"), true);
  assert.equal(accessAPICalls, 4, "an explicit page refresh still reloads both APIs");

  await accessSidebarLinks[1].onclick({ preventDefault: noop });
  assert.equal(accessState.data.accessAgent, "alpha");
  assert.equal(accessState.data.accessEngine, "");
  assert.equal(accessMarkup.includes("Alpha node"), true);
  assert.equal(accessMarkup.includes("Beta node"), false);
  assert.equal(
    accessAPICalls,
    4,
    "node changes normalize incompatible engine filters locally",
  );
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}

}
