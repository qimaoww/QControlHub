import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const previousDocument = globalThis.document;
const previousDetailsElement = globalThis.HTMLDetailsElement;
const previousCSS = globalThis.CSS;
class PresetWorkspace {
  constructor(agentID) {
    this.dataset = { agentNode: agentID };
    this.id = `preset-node-${agentID}`;
  }
  querySelector() {
    return null;
  }
  querySelectorAll() {
    return [];
  }
}

const presetEngines = ["mihomo", "xray", "sing-box", "ss-rust"];
const presetAgents = [
  {
    id: "alpha",
    name: "Alpha",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: presetEngines,
    runtime: Object.fromEntries(
      presetEngines.map((engine) => [
        engine,
        { installed: true, service_status: "active", version: "1.0.0" },
      ]),
    ),
  },
  {
    id: "beta",
    name: "Beta",
    os: "linux",
    arch: "arm64",
    status: "online",
    capabilities: presetEngines,
    runtime: Object.fromEntries(
      presetEngines.map((engine) =>
        engine === "sing-box"
          ? [
              engine,
              {
                installed: false,
                existing_config_available: false,
                existing_config_unsupported_reason: "unsupported wrapper",
              },
            ]
          : [
              engine,
              { installed: true, service_status: "active", version: "1.0.0" },
            ],
      ),
    ),
  },
  {
    id: "gamma",
    name: "Gamma",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: presetEngines,
    features: ["mihomo-development-source-v1"],
    runtime: Object.fromEntries(
      presetEngines.map((engine) => [
        engine,
        {
          installed: true,
          service_status: "active",
          version: "1.0.0",
          ...(engine === "xray" ? { existing_config_available: true } : {}),
        },
      ]),
    ),
  },
];
const presetState = {
  route: "agents",
  anchor: "agents",
  data: { selectedAgent: "" },
};
const presetLinks = presetAgents.map((agent) => ({
  dataset: { contextAgent: agent.id },
  href: `#node-${agent.id}`,
}));
let presetWorkspaces = [];
let presetMarkup = "";
const presetConfigReads = [];

globalThis.HTMLDetailsElement = class {};
globalThis.CSS = { escape: (value) => String(value) };
globalThis.document = {
  querySelector: () => null,
  querySelectorAll(selector) {
    if (selector === ".preset-node-workspace") return presetWorkspaces;
    if (selector === "[data-context-agent]") return presetLinks;
    if (
      selector ===
      ".preset-node-workspace, .machine-workspace, .node-operations-workspace"
    )
      return presetWorkspaces;
    return [];
  },
};

try {
  const presetShell = (markup) => {
    presetMarkup = markup;
    presetWorkspaces = [
      ...markup.matchAll(
        /<section class="preset-node-workspace[^>]*data-agent-node="([^"]+)"/g,
      ),
    ].map((match) => new PresetWorkspace(match[1]));
  };
  const presetCtx = new Proxy(
    {
      state: presetState,
      engines: presetEngines,
      api: async (path) => {
        if (path === "/agents") return presetAgents;
        if (path.endsWith("/configs")) {
          presetConfigReads.push(path);
          return [];
        }
        assert.fail(`unexpected preset smoke API path ${path}`);
      },
      optionalAPI: async () => null,
      can: (capability) =>
        capability === "agent-config.read" || capability === "overview.read",
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      serviceStatusName: (value) => value,
      statusTone: (value) => value,
      conciseVersion: (_engine, value) => value,
      shell: presetShell,
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const { agents: renderPresetAgents } = installAgents(presetCtx);
  await renderPresetAgents({ overview: { agents: 2, agents_online: 2 } });
  assert.deepEqual(presetConfigReads, [`/agents/${presetState.data.selectedAgent}/configs`],
    "preset loading only fetches the visible node's configuration, not the entire fleet");

  const assertFocusedPreset = (selected, excluded) => {
    assert.equal(presetState.route, "agents", "preset selection stays on agents");
    assert.deepEqual(
      presetWorkspaces.map((workspace) => workspace.id),
      [`preset-node-${selected}`],
      "only the selected preset workspace enters the main DOM",
    );
    assert.equal(
      presetMarkup.includes(`data-agent-node="${excluded}"`),
      false,
      "unselected node content stays out of the main DOM",
    );
    assert.equal(
      presetMarkup.includes('class="machine-workspace"'),
      false,
      "focused preset content has no node accordion",
    );
    assert.equal(
      presetMarkup.includes('class="machine-header"'),
      false,
      "focused preset content has no node summary header",
    );
    assert.equal(
      presetMarkup.includes('class="node-page-intro"'),
      false,
      "focused preset content has no redundant page introduction",
    );
    assert.equal(
      presetMarkup.includes("data-development-source"),
      true,
      "preset version drawer exposes the Mihomo development source fieldset",
    );
    assert.equal(
      presetMarkup.includes('value="mirror" disabled'),
      true,
      "legacy Agent mirror stays disabled",
    );
    assert.equal(
      presetMarkup.includes("source-upgrade-note"),
      true,
      "legacy Agent shows the upgrade-source explanation",
    );
    assert.equal(
      (presetMarkup.match(/<article class="service-card service-/g) || [])
        .length,
      presetEngines.length,
      "focused preset content keeps all engine cards",
    );
    assert.equal(
      (presetMarkup.match(new RegExp(`data-config="${selected}"`, "g")) || [])
        .length,
      presetEngines.length,
      "focused preset content keeps each engine configuration action",
    );
    assert.equal(presetMarkup.includes("节点内核"), true);
  };

  assert.equal(
    presetState.data.selectedAgent,
    "alpha",
    "first preset visit selects the first node",
  );
  assertFocusedPreset("alpha", "beta");
  assert.equal(presetState.route, "agents", "preset selection stays on agents");
  assert.deepEqual(
    presetLinks.map((link) => link.href),
    ["#preset-node-alpha", "#preset-node-beta", "#preset-node-gamma"],
    "preset sidebar links target per-node preset anchors",
  );
  assert.equal(
    presetLinks.some((link) => link.href.startsWith("#settings-node-")),
    false,
    "preset sidebar never enters node settings",
  );

  presetState.anchor = "preset-node-beta";
  presetState.data.selectedAgent = "beta";
  await renderPresetAgents({ overview: { agents: 2, agents_online: 2 } });
  assertFocusedPreset("beta", "alpha");
  assert.equal(
    presetMarkup.includes('data-config="beta" data-engine="sing-box"'),
    true,
    "unsupported preset engine keeps a config entry",
  );
  assert.equal(
    presetMarkup.includes("查看现有服务不可导入原因"),
    true,
    "unsupported preset engine still explains why its optional import is unavailable",
  );

  presetState.anchor = "preset-node-gamma";
  presetState.data.selectedAgent = "gamma";
  await renderPresetAgents({ overview: { agents: 3, agents_online: 3 } });
  assert.equal(
    presetWorkspaces.map((workspace) => workspace.id)[0],
    "preset-node-gamma",
    "feature-capable preset node is selected",
  );
  assert.equal(
    presetMarkup.includes('value="mirror" disabled'),
    false,
    "source-capable Agent mirror is not disabled",
  );
  assert.equal(
    presetMarkup.includes("source-upgrade-note"),
    false,
    "source-capable Agent hides the upgrade-source explanation",
  );
  assert.equal(
    presetMarkup.includes('data-config="gamma" data-engine="xray"'),
    true,
    "an optional import does not replace the managed configuration action",
  );
  assert.equal(
    presetMarkup.includes(
      'data-manual-agent="gamma" data-manual-engine="xray" aria-label="导入现有服务"',
    ),
    true,
    "an installed managed core keeps a separate optional import action",
  );
  assert.equal(
    presetMarkup.includes("请先手动导入"),
    false,
    "optional imports never become an installation prerequisite",
  );
  assert.equal(
    presetMarkup.includes('value="mirror"'),
    true,
    "source-capable Agent exposes the mirror option",
  );
  assert.equal(
    presetMarkup.includes("data-development-source"),
    true,
    "source-capable Agent still exposes the source fieldset",
  );
} finally {
  clearTimeout(presetState.agentPollTimer);
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
  if (previousDetailsElement === undefined) delete globalThis.HTMLDetailsElement;
  else globalThis.HTMLDetailsElement = previousDetailsElement;
  if (previousCSS === undefined) delete globalThis.CSS;
  else globalThis.CSS = previousCSS;
}
  return { previousDocument };
}
