import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
// The preset page compacts physical DOM only, never the install/version drawer.
{
  const compactDocument = globalThis.document;
  const { compactPresetPage: runCompactPresetPage } = installAgents(
    new Proxy(
      { state: { route: "agents", data: {} } },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const counts = {
    enrollment: 0,
    batch: 0,
    summary: 0,
    state: 0,
    inspector: 0,
    footer: 0,
    unavailable: 0,
    upgrade: 0,
    batchLabel: 0,
    drawer: 0,
    toggle: 0,
  };
  const tracked = (name) => ({
    remove: () => {
      counts[name] += 1;
    },
  });
  const workspace = {
    dataset: { agentNode: "gamma" },
    querySelector(selector) {
      if (selector === ".machine-resource-summary") return tracked("summary");
      if (selector === ".machine-state") return tracked("state");
      if (selector === ".node-inspector") return tracked("inspector");
      if (selector === ".machine-footer") return tracked("footer");
      if (selector === ".runtime-drawer") return tracked("drawer");
      if (selector === ".service-version-toggle") return tracked("toggle");
      return null;
    },
    querySelectorAll(selector) {
      if (selector === ".service-management-unavailable, [data-upgrade-agent]") {
        return [tracked("unavailable"), tracked("upgrade")];
      }
      if (selector === "[data-batch-checkbox]") {
        const label = tracked("batchLabel");
        return [{ closest: () => label }];
      }
      return [];
    },
  };
  globalThis.document = {
    querySelector(selector) {
      if (selector === "#enrollment") return tracked("enrollment");
      if (selector === "#batch-form") return tracked("batch");
      return null;
    },
    querySelectorAll(selector) {
      if (selector === ".preset-node-workspace") return [workspace];
      return [];
    },
  };
  try {
    runCompactPresetPage();
    assert.equal(counts.drawer, 0, "compact keeps the preset version drawer");
    assert.equal(counts.toggle, 0, "compact keeps the preset version toggle");
    assert.equal(counts.enrollment, 1, "compact removes the enrollment sheet");
    assert.equal(counts.batch, 1, "compact removes the batch form");
    assert.equal(counts.summary, 1, "compact removes the resource summary");
    assert.equal(counts.state, 1, "compact removes the machine state");
    assert.equal(counts.inspector, 1, "compact removes the node inspector");
    assert.equal(counts.footer, 1, "compact removes the machine footer");
    assert.equal(counts.unavailable, 1, "compact removes unavailable-service block");
    assert.equal(counts.upgrade, 1, "compact removes the upgrade-agent block");
    assert.equal(counts.batchLabel, 1, "compact removes the batch checkbox label");
  } finally {
    if (compactDocument === undefined) delete globalThis.document;
    else globalThis.document = compactDocument;
  }
}

}
