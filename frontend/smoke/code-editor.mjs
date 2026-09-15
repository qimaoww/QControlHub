import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
// The manual config code editor formats JSON in place and fails closed on
// YAML/comments/lossy or readonly content, without re-rendering the page.
{
  const formatDocument = globalThis.document;
  const makeTextNode = () => {
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
  const makeCodeInput = (initialValue, { readOnly = false } = {}) => {
    const listeners = new Map();
    const input = {
      value: initialValue,
      readOnly,
      disabled: false,
      selectionStart: initialValue.length,
      selectionEnd: initialValue.length,
      scrollTop: 0,
      scrollLeft: 0,
      classList: { toggle() {} },
      setAttribute() {},
      dispatchEvent() {},
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
      _listeners: listeners,
    };
    return input;
  };
  const buildEditor = (initialValue, language, readOnly = false) => {
    const input = makeCodeInput(initialValue, { readOnly });
    const gutter = makeTextNode();
    const byteLabel = makeTextNode();
    const position = makeTextNode();
    const status = makeTextNode();
    const statusDot = makeTextNode();
    const validation = makeTextNode();
    const reset = makeTextNode();
    const format = makeTextNode();
    const editor = {
      dataset: {
        codeLanguage: language,
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
    };
    return editor;
  };

  const { bindCodeEditors } = installAgents(
    new Proxy(
      {
        state: { route: "live-config", data: {} },
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );

  const unformatted = '{"tag":"demo","port":443,"tls":{"enabled":true}}';
  const editor = buildEditor(unformatted, "JSON");
  const readonlyEditor = buildEditor(unformatted, "JSON", true);
  const brokenEditor = buildEditor('{"tag":"demo",}', "JSON");
  const largeEditor = buildEditor(
    JSON.stringify(Array(840001).fill(0)),
    "JSON",
  );
  const deepEditor = buildEditor("[".repeat(600000), "JSON");
  let editorQueryCalls = 0;

  globalThis.document = {
    querySelector: () => null,
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") {
        editorQueryCalls += 1;
        return [editor, readonlyEditor, brokenEditor, largeEditor, deepEditor];
      }
      return [];
    },
  };

  try {
    bindCodeEditors();
    assert.equal(
      editorQueryCalls,
      1,
      "binding queries the editor list exactly once",
    );
    assert.equal(
      editor.dataset.dirty,
      "0",
      "unmodified editor starts clean",
    );

    editor.format.dispatch("click", {});
    assert.equal(
      editorQueryCalls,
      1,
      "formatting does not re-query or re-render the editor page",
    );
    assert.equal(
      editor.input.value,
      '{\n  "tag": "demo",\n  "port": 443,\n  "tls": {\n    "enabled": true\n  }\n}\n',
      "JSON is formatted in place with two-space indentation and a final newline",
    );
    assert.equal(editor.dataset.dirty, "1", "formatting marks the editor dirty");
    assert.equal(editor.reset.disabled, false, "reset is enabled after formatting");
    assert.equal(
      editor.input.value.includes("  \"tag\": \"demo\""),
      true,
      "two-space indentation applied",
    );

    const firstSnapshot = editor.input.value;
    editor.format.dispatch("click", {});
    assert.equal(
      editor.input.value,
      firstSnapshot,
      "repeated formatting is idempotent",
    );

    readonlyEditor.format.dispatch("click", {});
    assert.equal(
      readonlyEditor.input.value,
      unformatted,
      "readonly editor is never rewritten",
    );
    assert.equal(
      readonlyEditor.dataset.dirty,
      "0",
      "readonly editor stays clean",
    );

    brokenEditor.format.dispatch("click", {});
    assert.equal(
      brokenEditor.input.value,
      '{"tag":"demo",}',
      "syntax-error content is preserved on failure",
    );
    assert.equal(
      brokenEditor.dataset.dirty,
      "0",
      "failure keeps the dirty baseline unchanged",
    );
    assert.equal(
      /无法安全格式化|JSON 语法错误/.test(brokenEditor.validation.textContent),
      true,
      "failure surfaces a local, explicit message",
    );
    assert.equal(
      brokenEditor.statusDot.style.background,
      "var(--red)",
      "failure marks the status dot red",
    );

    const deepSnapshot = deepEditor.input.value;
    deepEditor.format.dispatch("click", {});
    assert.equal(
      deepEditor.input.value,
      deepSnapshot,
      "over-deep content is preserved on failure",
    );
    assert.equal(
      deepEditor.dataset.dirty,
      "0",
      "over-deep failure keeps the dirty baseline unchanged",
    );
    assert.equal(
      deepEditor.validation.textContent,
      "当前内容无法安全格式化。",
      "internal formatter errors fall back to a generic local message",
    );
    assert.equal(
      deepEditor.statusDot.style.background,
      "var(--red)",
      "over-deep failure marks the status dot red",
    );

    const largeSnapshot = largeEditor.input.value;
    largeEditor.format.dispatch("click", {});
    assert.equal(
      largeEditor.input.value,
      largeSnapshot,
      "over-limit formatted output keeps the original text",
    );
    assert.equal(
      largeEditor.dataset.dirty,
      "0",
      "over-limit result keeps the dirty baseline unchanged",
    );
    assert.equal(
      /超过 2 MiB 上限/.test(largeEditor.validation.textContent),
      true,
      "over-limit result shows a local error without submitting",
    );
    assert.equal(
      largeEditor.statusDot.style.background,
      "var(--red)",
      "over-limit result marks the status dot red",
    );
  } finally {
    if (formatDocument === undefined) delete globalThis.document;
    else globalThis.document = formatDocument;
  }
}

}
