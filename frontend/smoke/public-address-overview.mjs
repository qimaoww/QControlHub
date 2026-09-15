import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";
export async function run({ noop }) {
// Runtime smoke: a normal node-settings overview render must not throw
// ReferenceError for esc inside cardIPRow, and must emit the probe rows.
{
  const previousDocument = globalThis.document;
  const previousSetTimeout = globalThis.setTimeout;
  const previousClearTimeout = globalThis.clearTimeout;
  globalThis.setTimeout = () => 0;
  globalThis.clearTimeout = () => {};
  globalThis.document = {
    querySelector: () => null,
    querySelectorAll: () => [],
  };
  try {
  const overviewState = {
    route: "node-settings",
    navigationEpoch: 1,
    anchor: "node-settings",
    data: {},
  };
  let overviewMarkup = "";
  const overviewAgents = [
    {
      id: "alpha",
      name: "Alpha",
      os: "linux",
      arch: "amd64",
      status: "online",
      version: "1.2.3",
      capabilities: ["mihomo"],
      features: [],
      labels: { client_address: "node.example.com" },
      metrics: {
        public_ipv4: "198.35.26.96",
        public_ipv6: "",
        collected_at: "now",
      },
      runtime: {
        mihomo: {
          installed: true,
          version: "1.19.0",
          service_status: "running",
        },
      },
      last_seen: "now",
      enrolled_at: "now",
    },
  ];
  const overviewCtx = new Proxy(
    {
      state: overviewState,
      engines: ["mihomo"],
      api: async (path) => {
        if (path === "/agents") return overviewAgents;
        if (path === "/overview") return { agents: 1, agents_online: 1 };
        if (path === "/enrollment-tokens") return [];
        if (path.endsWith("/configs")) return [];
        if (path === "/deployments" || path === "/client-access") return [];
        return [];
      },
      optionalAPI: async () => null,
      can: () => true,
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      serviceStatusName: (value) => value,
      statusTone: (value) => value,
      conciseVersion: (_engine, value) => value,
      ago: () => "刚刚",
      heartbeat: () => "刚刚",
      bytes: (value) => `${value || 0} B`,
      percent: (used, limit) => (limit ? Number(used || 0) / limit : 0),
      rate: (value) => `${value || 0} B/s`,
      actionName: (value) => value,
      serviceActionDisabled: () => false,
      trafficChart: () => "",
      renderConfigDiff: () => "",
      notify: () => {},
      confirmAction: () => {},
      short: (value) => value,
      date: (value) => value,
      shell: (markup) => {
        overviewMarkup = markup;
      },
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const { nodeSettings: renderOverview } = installAgents(overviewCtx);
  await renderOverview(false, { overview: { agents: 1, agents_online: 1 } });
  assert.equal(
    overviewMarkup.includes('class="node-card-ips"'),
    true,
    "node-settings overview card renders the public-address strip",
  );
  assert.equal(
    overviewMarkup.includes("198.35.26.96"),
    true,
    "node-settings card prints the probed IPv4 address",
  );
  assert.equal(
    /class="card-ip-row empty"[^>]*hidden/.test(overviewMarkup),
    true,
    "node-settings card hides an unavailable address family",
  );
  assert.equal(
    overviewMarkup.includes("data-copy-ip"),
    true,
    "node-settings card keeps a copy button",
  );
  assert.equal(
    overviewMarkup.includes("手动连接地址：node.example.com"),
    true,
    "node-settings card keeps a non-IP manual connection address separate",
  );

  overviewState.nodeView = "detail";
  overviewState.anchor = "settings-node-alpha";
  overviewAgents[0].labels = {
    client_address: "198.35.26.10",
    public_ip: "2001:4860:4860::8888",
  };
  await renderOverview(false, { overview: { agents: 1, agents_online: 1 } });
  assert.equal(
    overviewMarkup.includes('class="node-public-ips"'),
    true,
    "node-settings detail renders the public-address section",
  );
  assert.equal(
    overviewMarkup.includes("198.35.26.10"),
    true,
    "node-settings detail renders a manually configured IPv4",
  );
  assert.equal(
    overviewMarkup.includes("2001:4860:4860::8888"),
    true,
    "node-settings detail renders a manually configured IPv6",
  );
  assert.equal(
    overviewMarkup.includes('data-ip-source="手动设置"'),
    true,
    "node-settings detail marks manual literal addresses explicitly",
  );
  } finally {
    if (previousSetTimeout === undefined) delete globalThis.setTimeout;
    else globalThis.setTimeout = previousSetTimeout;
    if (previousClearTimeout === undefined) delete globalThis.clearTimeout;
    else globalThis.clearTimeout = previousClearTimeout;
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}

}
