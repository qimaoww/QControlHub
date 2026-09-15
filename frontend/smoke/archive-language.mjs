import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

import { installConfigPages } from "../modules/configs.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
// The archive new-config engine selector syncs the source editor language,
// file/language labels, and formatter on change without rebuilding the DOM.
{
  const archiveDocument = globalThis.document;
  const archiveEvent = globalThis.Event;
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
      hidden: false,
      value: "",
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      _listeners: listeners,
    };
  };
  const makeInput = (initialValue) => {
    const listeners = new Map();
    const input = {
      value: initialValue,
      readOnly: false,
      disabled: false,
      selectionStart: initialValue.length,
      selectionEnd: initialValue.length,
      scrollTop: 0,
      scrollLeft: 0,
      classList: { toggle() {} },
      setAttribute() {},
      focus() {},
      closest: () => ({
        querySelectorAll: () => [],
        addEventListener: () => {},
      }),
      setSelectionRange(start, end) {
        input.selectionStart = start;
        input.selectionEnd = end;
      },
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      dispatchEvent(event) {
        input.dispatch(event?.type || "input", event);
      },
    };
    return input;
  };
  const makeArchiveEditor = (initialValue, engine) => {
    const input = makeInput(initialValue);
    const gutter = makeTarget();
    const byteLabel = makeTarget();
    const position = makeTarget();
    const status = makeTarget();
    const statusDot = makeTarget();
    const validation = makeTarget();
    const reset = makeTarget();
    const format = makeTarget();
    const languageLabel = makeTarget();
    const fileLabel = makeTarget();
    const editor = {
      dataset: {
        codeLanguage: engine === "mihomo" ? "YAML" : "JSON",
        codeMaxBytes: "2097152",
        dirty: "0",
        codeValid: "1",
      },
      querySelector(selector) {
        if (selector === "[data-code-input]") return input;
        if (selector === "[data-line-numbers]") return gutter;
        if (selector === "[data-code-bytes]") return byteLabel;
        if (selector === "[data-code-position]") return position;
        if (selector === "[data-code-status]") return status;
        if (selector === "[data-code-status-dot]") return statusDot;
        if (selector === "[data-code-validation]") return validation;
        if (selector === "[data-code-reset]") return reset;
        if (selector === "[data-code-format]") return format;
        if (selector === ".code-language") return languageLabel;
        if (selector === ".code-file-meta b") return fileLabel;
        return null;
      },
      input,
      gutter,
      byteLabel,
      position,
      status,
      statusDot,
      validation,
      reset,
      format,
      languageLabel,
      fileLabel,
    };
    return editor;
  };

  const engineSelect = makeTarget();
  engineSelect.value = "mihomo";
  const archiveForm = {
    querySelector(selector) {
      if (selector === 'select[name="engine"]') return engineSelect;
      if (selector === "[data-code-editor]") return archiveEditor;
      return null;
    },
    addEventListener() {},
  };
  const archiveEditor = makeArchiveEditor(
    "mixed-port: 7890\nproxies: []\n",
    "mihomo",
  );

  globalThis.document = {
    querySelector(selector) {
      if (selector === "#archive-form") return archiveForm;
      if (selector === "[data-code-editor]") return archiveEditor;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") return [archiveEditor];
      return [];
    },
  };

  const archiveState = { route: "archive-config", data: { newConfig: true } };
  const { bindCodeEditors } = installAgents(
    new Proxy(
      {
        state: { route: "archive-config", data: {} },
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const { archiveConfigs } = installConfigPages(
    new Proxy(
      {
        state: archiveState,
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
        api: async () => [],
        optionalAPI: async () => null,
        can: () => true,
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
      archiveEditor.dataset.codeLanguage,
      "YAML",
      "new archive config starts with YAML editor language",
    );

    engineSelect.value = "xray";
    engineSelect.dispatch("change", {});
    assert.equal(
      archiveEditor.dataset.codeLanguage,
      "JSON",
      "engine switch updates the editor language to JSON",
    );
    assert.equal(
      archiveEditor.languageLabel.textContent,
      "JSON",
      "engine switch updates the language badge",
    );
    assert.equal(
      archiveEditor.fileLabel.textContent,
      "config.json",
      "engine switch updates the file name label",
    );

    archiveEditor.input.value = '{"a":1,"b":[2,3]}';
    archiveEditor.format.dispatch("click", {});
    assert.equal(
      archiveEditor.input.value,
      '{\n  "a": 1,\n  "b": [\n    2,\n    3\n  ]\n}\n',
      "formatter uses the switched JSON language without rebuilding the editor",
    );
    assert.equal(
      archiveEditor.dataset.dirty,
      "1",
      "engine switch preserves dirty state after formatting",
    );

    engineSelect.value = "mihomo";
    engineSelect.dispatch("change", {});
    assert.equal(
      archiveEditor.dataset.codeLanguage,
      "YAML",
      "engine switch back updates the editor language to YAML",
    );
    const beforeYaml = archiveEditor.input.value;
    archiveEditor.format.dispatch("click", {});
    assert.equal(
      archiveEditor.input.value,
      beforeYaml,
      "YAML fail-closed keeps the original editor text",
    );
    assert.equal(
      /无法安全格式化|保留原文/.test(archiveEditor.validation.textContent),
      true,
      "YAML fail-closed shows a local message",
    );
  } finally {
    if (archiveDocument === undefined) delete globalThis.document;
    else globalThis.document = archiveDocument;
    if (archiveEvent === undefined) delete globalThis.Event;
    else globalThis.Event = archiveEvent;
  }
}

}
