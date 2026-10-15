import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { archiveDraftDirty, bindArchiveDraft, createArchiveDraft } from "../modules/archive-drafts.js";
import { configDefaults } from "../modules/config-defaults.js";
import { bindCodeEditors } from "../modules/code-editor.js";

export async function run() {
  for (const [engine, file] of Object.entries({ mihomo: "mihomo-minimal.yaml", xray: "xray-minimal.json",
    "sing-box": "sing-box-minimal.json", "ss-rust": "shadowsocks-rust-minimal.json" })) {
    assert.equal(configDefaults[engine], await readFile(new URL(`../../examples/configs/${file}`, import.meta.url), "utf8"),
      `${engine} defaults must reuse the canonical example verbatim`);
    const archive = createArchiveDraft({ name: "Archive", engine });
    const template = createArchiveDraft({ name: "Template", engine });
    assert.equal(archive.sources.get(engine).content, configDefaults[engine]);
    assert.equal(archiveDraftDirty(archive), false);
    archive.sources.get(engine).content += "\n";
    assert.equal(archiveDraftDirty(archive), true);
    assert.equal(template.sources.get(engine).content, configDefaults[engine], "forms must not share source state");
    archive.sources.get(engine).content = archive.sources.get(engine).original;
    assert.equal(archiveDraftDirty(archive), false);
    archive.name = "Renamed";
    assert.equal(archiveDraftDirty(archive), true);
    archive.name = "Archive";
    archive.description = "Notes";
    assert.equal(archiveDraftDirty(archive), true);
    if (engine !== "mihomo") assert.ok(JSON.parse(configDefaults[engine]));
  }
  assert.notEqual(configDefaults.xray, configDefaults["sing-box"]);
  assert.notEqual(configDefaults.xray, configDefaults["ss-rust"]);
  assert.notEqual(configDefaults["sing-box"], configDefaults["ss-rust"]);
  testEditorSwitches();
  console.log("Archive defaults reuse all four canonical examples; draft baselines and forms are independent");
}

function testEditorSwitches() {
  class Target extends EventTarget {
    value = ""; textContent = ""; dataset = {}; style = {}; disabled = false;
    readOnly = false; selectionStart = 0; selectionEnd = 0;
    attributes = new Map();
    classList = { toggle() {} };
    setAttribute(key, value) { this.attributes.set(key, value); }
    setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
    focus() {}
  }
  const form = new Target(), editor = new Target(), badge = new Target();
  const fields = Object.fromEntries(["name", "description", "content", "engine"].map(name => [name, new Target()]));
  const controls = Object.fromEntries(["data-line-numbers", "data-code-bytes", "data-code-position", "data-code-status",
    "data-code-status-dot", "data-code-validation", "data-code-reset", "data-code-format"].map(key => [`[${key}]`, new Target()]));
  const submit = new Target(), language = new Target(), file = new Target();
  controls["[data-code-status]"].textContent = "草稿";
  form.querySelector = selector => selector === "[data-code-editor]" ? editor : fields[selector.match(/^\[name="(.+)"\]$/)?.[1]];
  form.querySelectorAll = () => [submit];
  form.closest = () => ({ querySelector: () => badge });
  editor.querySelector = selector => ({ ...controls, "[data-code-input]": fields.content, ".code-language": language,
    ".code-file-meta b": file })[selector];
  fields.content.closest = () => form;
  fields.content.dispatchEvent = event => {
    const result = Target.prototype.dispatchEvent.call(fields.content, event);
    if (event.bubbles) form.dispatchEvent(new Event(event.type));
    return result;
  };
  const previousDocument = globalThis.document;
  globalThis.document = { querySelectorAll: () => [editor] };
  const draft = createArchiveDraft({ name: "My archive", description: "My notes" });
  const select = engine => {
    fields.engine.value = engine;
    fields.engine.dispatchEvent(new Event("change"));
  };
  const check = (engine, content = configDefaults[engine], dirty = false, valid = true) => {
    assert.equal(form.dataset.engine, engine);
    assert.equal(badge.textContent, engine);
    assert.equal(badge.className, `engine-badge ${engine}`);
    assert.equal(fields.content.attributes.get("aria-label"), `${engine} 配置档案源码`);
    assert.equal(editor.dataset.codeLanguage, engine === "mihomo" ? "YAML" : "JSON");
    assert.equal(language.textContent, editor.dataset.codeLanguage);
    assert.equal(file.textContent, engine === "mihomo" ? "config.yaml" : "config.json");
    assert.equal(fields.content.value, content);
    assert.equal(editor.dataset.dirty, dirty ? "1" : "0");
    assert.equal(editor.dataset.codeValid, valid ? "1" : "0");
    assert.equal(controls["[data-code-reset]"].disabled, !dirty);
    assert.equal(submit.disabled, !valid);
    assert.equal(fields.name.value, "My archive");
    assert.equal(fields.description.value, "My notes");
  };
  try {
    bindArchiveDraft(form, draft, { engineName: value => value, writable: true });
    bindCodeEditors();
    for (const engine of ["mihomo", "xray", "sing-box", "ss-rust", "xray", "mihomo"]) {
      select(engine); check(engine);
      const edited = engine === "mihomo" ? configDefaults[engine] + "# draft\n" : '{"draft":"' + engine + '"}';
      fields.content.value = edited;
      fields.content.dispatchEvent(new Event("input", { bubbles: true }));
      select(engine === "mihomo" ? "xray" : "mihomo");
      select(engine); check(engine, edited, true);
      bindCodeEditors(); check(engine, edited, true);
      controls["[data-code-reset]"].dispatchEvent(new Event("click"));
      check(engine);
    }
    select("xray");
    fields.content.value = '{"broken":';
    fields.content.dispatchEvent(new Event("input", { bubbles: true }));
    check("xray", '{"broken":', true, false);
    select("sing-box"); check("sing-box");
    select("xray"); check("xray", '{"broken":', true, false);
    controls["[data-code-reset]"].dispatchEvent(new Event("click"));
    check("xray");
  } finally {
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}
