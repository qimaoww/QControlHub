import assert from "node:assert/strict";

import { installConfigPages } from "../modules/configs.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const runtimeRefreshDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const runtimeState = {
    route: "live-config",
    navigationEpoch: 10,
    data: {
      liveAgent: "upgraded-node",
      liveEngine: "sing-box",
      agents: [],
      liveSources: {},
    },
  };
  const runtimeSnapshots = [
    "executable is not in a supported standard path",
    "standard executable did not pass protected native binary validation",
    "",
  ];
  let runtimeAgentRequests = 0;
  let runtimeWorkspaceRequests = 0;
  let runtimeMarkup = "";
  const runtimeAgent = (unsupportedReason = "") => ({
    id: "upgraded-node",
    name: "Upgraded node",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["sing-box"],
    runtime: {
      "sing-box": {
        service_status: "active",
        installed: !unsupportedReason,
        existing_config_available: !unsupportedReason,
        existing_config_unsupported_reason: unsupportedReason,
      },
    },
  });
  const pages = installConfigPages({
    api: async (path) => {
      if (path === "/agents") {
        const reason = runtimeSnapshots[runtimeAgentRequests++];
        return [runtimeAgent(reason)];
      }
      if (
        path ===
        "/agents/upgraded-node/configs/sing-box/workspace"
      ) {
        runtimeWorkspaceRequests += 1;
        return { config: null };
      }
      assert.fail(`unexpected live runtime refresh path ${path}`);
    },
    optionalAPI: async () => null,
    state: runtimeState,
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
      runtimeMarkup = markup;
    },
    submitTask: noop,
    bindCodeEditors: noop,
  });

  await pages.liveConfig();
  assert.equal(
    runtimeMarkup.includes(runtimeSnapshots[0]),
    true,
    "the first live-config visit renders the then-current unsupported reason",
  );

  runtimeState.route = "tasks";
  runtimeState.navigationEpoch += 1;
  runtimeState.route = "live-config";
  runtimeState.navigationEpoch += 1;
  await pages.liveConfig();
  assert.equal(
    runtimeMarkup.includes(runtimeSnapshots[1]),
    true,
    "upgrade to tasks to live-config refreshes the Agent runtime without a hard reload",
  );
  assert.equal(runtimeMarkup.includes(runtimeSnapshots[0]), false);

  const migrationSnapshot =
    '{"inbounds":[{"tag":"complete-existing-snapshot"}]}';
  runtimeState.data.liveConfigSource = "import";
  runtimeState.data.liveSources["upgraded-node|sing-box|import"] = {
    content: migrationSnapshot,
    taskId: "read-after-upgrade",
  };
  runtimeState.route = "tasks";
  runtimeState.navigationEpoch += 1;
  runtimeState.route = "live-config";
  runtimeState.navigationEpoch += 1;
  await pages.liveConfig();
  assert.equal(runtimeMarkup.includes(migrationSnapshot), true);
  assert.equal(
    runtimeMarkup.includes("readonly"),
    true,
    "a newly available migration snapshot is rendered read-only",
  );
  assert.equal(
    (runtimeMarkup.match(/data-live-intent="import"/g) || []).length,
    1,
    "a newly available migration exposes exactly one import action",
  );
  assert.equal(
    runtimeAgentRequests,
    3,
    "each cross-route visit refreshes runtime once",
  );
  await pages.liveConfig();
  assert.equal(
    runtimeAgentRequests,
    3,
    "same-route live-config renders reuse the current scoped runtime",
  );
  assert.equal(runtimeWorkspaceRequests, 4);
} finally {
  if (runtimeRefreshDocument === undefined) delete globalThis.document;
  else globalThis.document = runtimeRefreshDocument;
}

}
