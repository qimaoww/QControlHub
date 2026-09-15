import assert from "node:assert/strict";

import { installConfigPages } from "../modules/configs.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const staleRuntimeDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const staleState = {
    route: "live-config",
    navigationEpoch: 20,
    data: {
      liveAgent: "race-node",
      liveEngine: "sing-box",
      liveConfigSource: "import",
      agents: [],
      liveSources: {
        "race-node|sing-box|import": { content: '{"log":{"level":"info"}}' },
      },
    },
  };
  let resolveOldAgents;
  const oldAgents = new Promise((resolve) => {
    resolveOldAgents = resolve;
  });
  let staleAgentRequests = 0;
  let staleWorkspaceRequests = 0;
  let staleMarkup = "";
  const raceAgent = (runtime) => ({
    id: "race-node",
    name: "Race node",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["sing-box"],
    runtime: { "sing-box": runtime },
  });
  const pages = installConfigPages({
    api: async (path) => {
      if (path === "/agents") {
        staleAgentRequests += 1;
        if (staleAgentRequests === 1) return oldAgents;
        return [
          raceAgent({
            installed: true,
            existing_config_available: true,
          }),
        ];
      }
      if (path === "/agents/race-node/configs/sing-box/workspace") {
        staleWorkspaceRequests += 1;
        return { config: null };
      }
      assert.fail(`unexpected stale runtime path ${path}`);
    },
    optionalAPI: async () => null,
    state: staleState,
    engines: ["sing-box"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    conciseVersion: (engine, version) => version || engine,
    date: (value) => value,
    ago: (value) => value,
    bytes: (value) => value,
    confirmAction: async () => true,
    notify: noop,
    shell: (markup) => {
      staleMarkup = markup;
    },
    submitTask: noop,
    bindCodeEditors: noop,
  });

  const staleRender = pages.liveConfig();
  staleState.navigationEpoch += 1;
  const currentRender = pages.liveConfig();
  await currentRender;
  resolveOldAgents([
    raceAgent({
      installed: false,
      existing_config_unsupported_reason: "stale unsupported reason",
    }),
  ]);
  await staleRender;
  assert.equal(
    staleState.data.agents[0].runtime["sing-box"].existing_config_available,
    true,
  );
  assert.equal(staleMarkup.includes("stale unsupported reason"), false);
  assert.equal(staleMarkup.includes('data-live-intent="import"'), true);
  assert.equal(
    staleWorkspaceRequests,
    2,
    "parallel workspace reads must not cause an extra read after runtime resolves",
  );
} finally {
  if (staleRuntimeDocument === undefined) delete globalThis.document;
  else globalThis.document = staleRuntimeDocument;
}

}
