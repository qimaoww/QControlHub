import assert from "node:assert/strict";

import { installConfigPages } from "../modules/configs.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const dualSourceDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const dualState = {
    route: "live-config",
    navigationEpoch: 1,
    data: {
      liveAgent: "dual-node",
      liveEngine: "xray",
      liveConfigSource: "managed",
      liveSources: {
        "dual-node|xray": { content: '{"tag":"managed"}' },
        "dual-node|xray|import": { content: '{"tag":"external"}' },
      },
    },
  };
  const dualAgent = {
    id: "dual-node",
    name: "Dual source node",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["xray"],
    runtime: {
      xray: {
        installed: true,
        version: "25.8",
        existing_config_available: true,
      },
    },
  };
  let dualMarkup = "";
  const pages = installConfigPages({
    api: async (path) => {
      if (path === "/agents") return [dualAgent];
      if (path === "/agents/dual-node/configs/xray/workspace")
        return { config: { id: "cfg-managed", version: 3 } };
      assert.fail(`unexpected dual-source path ${path}`);
    },
    optionalAPI: async () => null,
    state: dualState,
    engines: ["xray"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    conciseVersion: (_engine, version) => version,
    date: (value) => value,
    ago: (value) => value,
    bytes: (value) => value,
    confirmAction: async () => true,
    notify: noop,
    shell: (markup) => {
      dualMarkup = markup;
    },
    submitTask: noop,
    bindCodeEditors: noop,
  });
  await pages.liveConfig();
  assert.equal(dualMarkup.includes("QAgent 配置"), true);
  assert.equal(dualMarkup.includes("系统服务配置"), true);
  assert.equal(dualMarkup.includes('{"tag":"managed"}'), true);
  assert.equal(dualMarkup.includes("readonly"), false);
  assert.equal(dualMarkup.includes('data-live-intent="deploy"'), true);

  dualState.data.liveConfigSource = "import";
  await pages.liveConfig();
  assert.equal(dualMarkup.includes('{"tag":"external"}'), true);
  assert.equal(dualMarkup.includes("readonly"), true);
  assert.equal(dualMarkup.includes('data-live-intent="import"'), true);
} finally {
  if (dualSourceDocument === undefined) delete globalThis.document;
  else globalThis.document = dualSourceDocument;
}

}
