import assert from "node:assert/strict";

import { installConfigPages } from "../modules/configs.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const unsupportedDocument = globalThis.document;
const unsupportedState = {
  route: "live-config",
  navigationEpoch: 1,
  data: {
    liveAgent: "unsupported-node",
    liveEngine: "xray",
    agents: [
      {
        id: "unsupported-node",
        name: "Unsupported node",
        os: "linux",
        arch: "amd64",
        status: "online",
        capabilities: ["xray"],
        runtime: {
          xray: {
            installed: false,
            existing_config_unsupported_reason: "complex wrapper is unsupported",
          },
        },
      },
    ],
  },
};
let unsupportedMarkup = "";
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const pages = installConfigPages({
    api: async (path) => {
      if (path === "/agents") return unsupportedState.data.agents;
      assert.equal(
        path,
        "/agents/unsupported-node/configs/xray/workspace",
      );
      return { config: null };
    },
    optionalAPI: async () => null,
    state: unsupportedState,
    engines: ["xray"],
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
      unsupportedMarkup = markup;
    },
    submitTask: noop,
    bindCodeEditors: noop,
  });
  await pages.liveConfig();
  assert.deepEqual(unsupportedState.data.liveEngines, ["xray"]);
  assert.equal(unsupportedState.data.liveEngine, "xray");
  assert.equal(
    unsupportedMarkup.includes("检测到现有服务，但不可自动迁移"),
    true,
  );
  assert.equal(
    unsupportedMarkup.includes("complex wrapper is unsupported"),
    true,
  );
  assert.equal(
    unsupportedMarkup.includes("data-live-intent"),
    false,
    "unsupported reason pages expose no executable action",
  );
} finally {
  if (unsupportedDocument === undefined) delete globalThis.document;
  else globalThis.document = unsupportedDocument;
}

}
