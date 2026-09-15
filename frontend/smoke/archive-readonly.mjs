import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

import { installConfigPages } from "../modules/configs.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
// A non-operator archive new-config never renders the format action and does
// not bind an engine-sync handler, so the readonly snapshot is untouched.
{
  const readonlyDocument = globalThis.document;
  const readonlyEvent = globalThis.Event;
  globalThis.Event = class {
    constructor(type, options) {
      this.type = type;
      this.bubbles = options?.bubbles;
    }
  };
  const makeTarget = () => {
    const listeners = new Map();
    return {
      textContent: "",
      style: {},
      disabled: false,
      list: listeners,
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
    };
  };
  const engine = makeTarget();
  engine.value = "mihomo";
  engine.disabled = true;
  const form = {
    querySelector(selector) {
      if (selector === 'select[name="engine"]') return engine;
      return null;
    },
    addEventListener() {},
  };
  const editor = {
    dataset: {
      codeLanguage: "YAML",
      codeMaxBytes: "2097152",
      dirty: "0",
      codeValid: "1",
    },
    querySelector(selector) {
      if (selector === "[data-code-format]") return null;
      if (selector === "[data-code-input]")
        return {
          value: "",
          readOnly: true,
          disabled: false,
          selectionStart: 0,
          selectionEnd: 0,
          scrollTop: 0,
          scrollLeft: 0,
          classList: { toggle() {} },
          setAttribute() {},
          focus() {},
          closest: () => ({ querySelectorAll: () => [], addEventListener: () => {} }),
          setSelectionRange() {},
          addEventListener() {},
          dispatchEvent() {},
        };
      if (selector === "[data-line-numbers]")
        return { textContent: "", scrollTop: 0 };
      return null;
    },
  };
  globalThis.document = {
    querySelector(selector) {
      if (selector === "#archive-form") return form;
      if (selector === "[data-code-editor]") return editor;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") return [editor];
      return [];
    },
  };
  const { bindCodeEditors } = installAgents(
    new Proxy(
      { state: { route: "archive-config", data: {} } },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const { archiveConfigs } = installConfigPages(
    new Proxy(
      {
        state: { route: "archive-config", data: { newConfig: true } },
        engines: ["mihomo", "xray"],
        api: async () => [],
        optionalAPI: async () => null,
        can: () => false,
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        ago: () => "now",
        date: () => "now",
        conciseVersion: () => "1.0.0",
        bytes: () => "0 B",
        confirmAction: async () => true,
        notify: () => {},
        shell: () => {},
        submitTask: async () => {},
        bindCodeEditors,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  try {
    await archiveConfigs();
    assert.equal(
      engine.disabled,
      true,
      "readonly archive keeps the engine selector disabled",
    );
    assert.equal(
      editor.querySelector("[data-code-format]"),
      null,
      "readonly archive has no format action",
    );
    assert.equal(
      (engine.list.get("change") || []).length,
      0,
      "readonly archive does not bind an engine-sync change handler",
    );
  } finally {
    if (readonlyDocument === undefined) delete globalThis.document;
    else globalThis.document = readonlyDocument;
    if (readonlyEvent === undefined) delete globalThis.Event;
    else globalThis.Event = readonlyEvent;
  }
}

}
