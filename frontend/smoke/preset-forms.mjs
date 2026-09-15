import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
// The preset version form binds the real reveal and source-carrying payload path.
{
  const presetDomDocument = globalThis.document;
  const presetDomFormData = globalThis.FormData;
  const presetDomDetails = globalThis.HTMLDetailsElement;
  const presetDomCSS = globalThis.CSS;

  const radio = (name, value, checked = false) => {
    const listeners = [];
    return {
      name,
      value,
      checked,
      addEventListener(type, listener) {
        if (type === "change") listeners.push(listener);
      },
      fireChange() {
        listeners.forEach((listener) => listener({ target: this }));
      },
    };
  };
  const checkedOf = (radios) => radios.find((item) => item.checked);

  const makeVersionForm = ({ agent, canMirror }) => {
    const channels = ["stable", "development", "custom"].map((value, index) =>
      radio("release_channel", value, index === 0),
    );
    const sources = ["official", "mirror"].map((value) =>
      radio("core_source", value, value === "official"),
    );
    const fieldset = { hidden: true };
    const form = {
      dataset: { versionEngine: "mihomo", versionAgent: agent.id },
      elements: {
        namedItem(name) {
          if (name === "release_channel") {
            return { value: checkedOf(channels).value };
          }
          if (name === "core_source") {
            return { value: checkedOf(sources).value };
          }
          if (name === "custom_version") return { value: "" };
          return null;
        },
      },
      querySelector(selector) {
        if (selector === ".custom-version-field") return null;
        if (selector === "[data-development-source]") return fieldset;
        if (selector === 'input[name="release_channel"]:checked') {
          return checkedOf(channels);
        }
        return null;
      },
      querySelectorAll(selector) {
        if (selector === 'input[name="release_channel"]') return channels;
        return [];
      },
      setChannel(value) {
        const current = channels.find((item) => item.value === value);
        channels.forEach((item) => {
          item.checked = item === current;
        });
        current.fireChange();
      },
      setSource(value) {
        sources.forEach((item) => {
          item.checked = item.value === value;
        });
      },
      fieldset,
    };
    return form;
  };

  const agent = {
    id: "gamma",
    name: "Gamma",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["mihomo"],
    features: ["mihomo-development-source-v1"],
    runtime: {
      mihomo: { installed: true, service_status: "active", version: "1.0.0" },
    },
  };
  const versionForm = makeVersionForm({ agent, canMirror: true });
  const taskBodies = [];

  globalThis.HTMLDetailsElement = class {};
  globalThis.CSS = { escape: (value) => String(value) };
  globalThis.FormData = class {
    constructor(form) {
      this.form = form;
    }
    get(name) {
      return this.form.elements.namedItem(name)?.value ?? null;
    }
  };
  globalThis.document = {
    querySelector: () => null,
    querySelectorAll(selector) {
      if (selector === ".core-version-form") return [versionForm];
      if (selector === ".preset-node-workspace") {
        return [
          {
            dataset: { agentNode: "gamma" },
            querySelector: () => null,
            querySelectorAll: () => [],
          },
        ];
      }
      if (
        selector ===
        ".preset-node-workspace, .machine-workspace, .node-operations-workspace"
      ) {
        return [
          {
            dataset: { agentNode: "gamma" },
            querySelector: () => null,
            querySelectorAll: () => [],
          },
        ];
      }
      return [];
    },
  };

  try {
    const state = {
      route: "agents",
      anchor: "agents",
      data: { selectedAgent: "gamma" },
    };
    const ctx = new Proxy(
      {
        state,
        engines: ["mihomo"],
        api: async (path, options) => {
          if (path === "/agents") return [agent];
          if (path === "/deployments") return [];
          if (path === "/client-access") return [];
          if (path === "/overview") return { agents: 1, agents_online: 1 };
          if (path === "/agents/gamma/configs") return [];
          if (path === "/tasks") {
            taskBodies.push(options?.body);
            return { id: "task-1" };
          }
          assert.fail(`unexpected preset interaction API path ${path}`);
        },
        optionalAPI: async () => null,
        can: () => true,
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        serviceStatusName: (value) => value,
        statusTone: (value) => value,
        conciseVersion: (_engine, value) => value,
        confirmAction: async () => true,
        shell: noop,
      },
      { get: (target, key) => target[key] ?? noop },
    );
    const { agents: renderPresetInteraction } = installAgents(ctx);
    await renderPresetInteraction({ overview: { agents: 1, agents_online: 1 } });

    assert.equal(
      versionForm.fieldset.hidden,
      true,
      "stable channel keeps the development source fieldset hidden",
    );
    versionForm.setChannel("development");
    assert.equal(
      versionForm.fieldset.hidden,
      false,
      "dev channel reveals the development source fieldset",
    );
    versionForm.setChannel("stable");
    assert.equal(
      versionForm.fieldset.hidden,
      true,
      "returning to stable hides the development source fieldset",
    );

    versionForm.setChannel("development");
    versionForm.setSource("official");
    await versionForm.onsubmit({ preventDefault: noop });
    let payload = JSON.parse(taskBodies.at(-1));
    assert.equal(payload.engine, "mihomo", "payload targets mihomo");
    assert.equal(payload.core_version, "development", "payload carries dev channel");
    assert.equal(
      payload.core_source,
      "official",
      "explicit official carries through the payload",
    );

    versionForm.setSource("mirror");
    await versionForm.onsubmit({ preventDefault: noop });
    payload = JSON.parse(taskBodies.at(-1));
    assert.equal(
      payload.core_source,
      "mirror",
      "feature-capable mirror carries through the payload",
    );
    clearTimeout(state.agentPollTimer);
  } finally {
    if (presetDomDocument === undefined) delete globalThis.document;
    else globalThis.document = presetDomDocument;
    if (presetDomFormData === undefined) delete globalThis.FormData;
    else globalThis.FormData = presetDomFormData;
    if (presetDomDetails === undefined) delete globalThis.HTMLDetailsElement;
    else globalThis.HTMLDetailsElement = presetDomDetails;
    if (presetDomCSS === undefined) delete globalThis.CSS;
    else globalThis.CSS = presetDomCSS;
  }
}

}
