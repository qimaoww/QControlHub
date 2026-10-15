import { createConfigArchive } from "../modules/config-archive.js";
import { bindCodeEditors } from "../modules/code-editor.js";
import { reconcileView } from "../modules/refresh.js";
import { configDefaults } from "../modules/config-defaults.js";
import { createDisplayHelpers } from "../modules/formatting.js";
import { assert, waitFor } from "./assertions.mjs";

const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const change = (form, engine) => {
  const select = form.querySelector('[name="engine"]');
  select.value = engine;
  select.dispatchEvent(new Event("change", { bubbles: true }));
};
const fill = (form, name, value) => {
  const input = form.querySelector(`[name="${name}"]`);
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
};
const field = (form, name) => form.querySelector(`[name="${name}"]`);
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};

export async function testArchiveEnginesRuntime(preview = false) {
  const state = { route: "archive-config", data: { newConfig: true } };
  const display = createDisplayHelpers(state);
  const records = { writes: [], templates: [], restores: [], errors: [] };
  const configs = [], templates = [], revisions = new Map();
  let writable = true, readGate = null, saveGate = null, renderCount = 0;
  const api = async (path, options = {}) => {
    const method = options.method || "GET";
    if (method === "GET") {
      if (path === "/configs") {
        const snapshot = configs.map(item => ({ ...item }));
        if (readGate) await readGate.promise;
        return snapshot;
      }
      if (path === "/templates") return templates;
      if (path === "/agents") return [];
      const match = path.match(/^\/configs\/([^/]+)\/revisions(?:\/(\d+))?/);
      if (match) {
        const history = revisions.get(match[1]) || [];
        return match[2] ? history.find(item => item.version === Number(match[2])) : history;
      }
    }
    const payload = JSON.parse(options.body || "{}");
    if (method === "POST" && path === "/templates") {
      records.templates.push(payload);
      templates.push({ ...payload, id: `tpl-${templates.length + 1}` });
      return templates.at(-1);
    }
    if (method === "DELETE" && path.startsWith("/templates/")) {
      templates.splice(templates.findIndex(item => item.id === path.split("/")[2]), 1);
      return null;
    }
    if (path.endsWith("/restore")) {
      records.restores.push(payload);
      const id = path.split("/")[2], config = configs.find(item => item.id === id);
      const revision = revisions.get(id).find(item => item.version === Number(path.split("/")[4]));
      Object.assign(config, { ...revision, version: config.version + 1 });
      revisions.get(id).push({ ...config });
      return { ...config };
    }
    if ((method === "POST" && path === "/configs") || method === "PUT") {
      records.writes.push({ method, payload });
      if (saveGate) await saveGate.promise;
      const id = method === "POST" ? `cfg-${configs.length + 1}` : path.split("/")[2];
      const config = { ...payload, id, version: payload.version + 1 };
      if (method === "POST") configs.push(config);
      else Object.assign(configs.find(item => item.id === id), config);
      revisions.set(id, [...(revisions.get(id) || []), { ...config }]);
      return { ...config };
    }
    throw new Error(`Unexpected archive API ${method} ${path}`);
  };
  const root = document.createElement("main");
  document.body.replaceChildren(root);
  const render = createConfigArchive({ api, optionalAPI: api, state, engines, ...display,
    can: permission => writable || permission.endsWith(".read"),
    confirmAction: async () => true, notify: message => records.errors.push(message), submitTask: async () => {}, bindCodeEditors,
    shell: (markup, title, { viewKey } = {}) => {
      const fresh = document.createElement("main");
      fresh.innerHTML = `<button id="new-config" type="button">New archive</button><div data-refresh-key="${viewKey}">${markup}</div>`;
      reconcileView(root, fresh);
      renderCount++;
    },
  });
  const archive = () => document.querySelector("#archive-form");
  const template = () => document.querySelector("#template-form");
  const editor = () => archive().querySelector("[data-code-editor]");
  const check = (engine, content = configDefaults[engine], dirty = false, valid = true) => {
    const form = archive(), code = editor();
    const badge = form.closest(".config-workspace").querySelector(".engine-badge");
    assert.equal(field(form, "engine").value, engine, "select engine");
    assert.equal(form.dataset.engine, engine, "form engine");
    assert.equal(badge.textContent, display.engineName(engine), "top engine name");
    assert.equal(badge.className, `engine-badge ${engine}`, "top badge style");
    assert.equal(code.dataset.codeLanguage, engine === "mihomo" ? "YAML" : "JSON", "language");
    assert.equal(code.querySelector(".code-language").textContent, code.dataset.codeLanguage, "language label");
    assert.equal(code.querySelector(".code-file-meta b").textContent, engine === "mihomo" ? "config.yaml" : "config.json", "filename");
    assert.equal(field(form, "content").getAttribute("aria-label"), `${display.engineName(engine)} 配置档案源码`, "accessible name");
    assert.equal(field(form, "content").value, content, `${engine} source`);
    assert.equal(code.dataset.dirty, dirty ? "1" : "0", "dirty state");
    assert.equal(code.dataset.codeValid, valid ? "1" : "0", "validation state");
    assert.equal(code.querySelector("[data-code-reset]").disabled, !dirty || !writable, "reset state");
    assert.equal(form.querySelector('[type="submit"]')?.disabled || false, !valid, "submit validation");
  };
  await render();
  if (preview) return;
  fill(archive(), "name", "My archive");
  fill(archive(), "description", "Keep these notes");
  fill(template(), "name", "My template");
  for (const engine of [...engines, ...engines.toReversed(), "xray", "ss-rust", "sing-box", "xray"]) {
    change(archive(), engine);
    check(engine);
    assert.equal(field(archive(), "name").value, "My archive");
    assert.equal(field(archive(), "description").value, "Keep these notes");
    assert.equal(template().dataset.engine, "mihomo", "archive switch changed template");
  }
  for (const engine of engines) {
    change(archive(), engine);
    const draft = engine === "mihomo" ? `${configDefaults[engine]}# my YAML draft\n` : '{"custom":"draft-' + engine + '"}';
    fill(archive(), "content", draft);
    change(archive(), engines[(engines.indexOf(engine) + 1) % engines.length]);
    change(archive(), engine);
    check(engine, draft, true);
    archive().querySelector("[data-code-format]").click();
    const formatted = field(archive(), "content").value;
    change(archive(), engines[(engines.indexOf(engine) + 1) % engines.length]);
    change(archive(), engine);
    check(engine, formatted, true);
    archive().querySelector("[data-code-reset]").click();
    check(engine);
  }
  change(archive(), "xray");
  fill(archive(), "content", '{"broken":');
  check("xray", '{"broken":', true, false);
  change(archive(), "sing-box"); check("sing-box");
  change(archive(), "xray"); check("xray", '{"broken":', true, false);
  assert.ok(editor().querySelector("[data-code-validation]").textContent.includes("JSON"), "invalid JSON feedback missing");
  await render(); check("xray", '{"broken":', true, false);
  archive().querySelector("[data-code-reset]").click(); check("xray");

  // A slow refresh cannot apply the engine/content captured before a switch.
  readGate = deferred();
  const refreshing = render();
  change(archive(), "sing-box");
  change(archive(), "ss-rust");
  fill(archive(), "content", configDefaults["ss-rust"] + "\n");
  const archiveDraft = field(archive(), "content").value;
  readGate.resolve(); readGate = null;
  await refreshing;
  check("ss-rust", archiveDraft, true);
  for (const engine of [...engines, "xray", "sing-box", "ss-rust"]) {
    change(template(), engine);
    assert.equal(template().dataset.engine, engine, "template engine");
    assert.equal(field(template(), "content").value, configDefaults[engine], "template default");
    check("ss-rust", archiveDraft, true);
    const content = configDefaults[engine] + "\n";
    fill(template(), "content", content);
    change(template(), engine === "mihomo" ? "xray" : "mihomo");
    change(template(), engine);
    assert.equal(field(template(), "content").value, content, "template draft lost");
    // Restore to the default to repeat an engine later in the same loop.
    fill(template(), "content", configDefaults[engine]);
  }
  fill(template(), "content", configDefaults["ss-rust"] + "\n");
  const beforeTemplateSave = renderCount;
  template().requestSubmit();
  await waitFor(() => renderCount > beforeTemplateSave, "template save did not refresh");
  check("ss-rust", archiveDraft, true);
  assert.equal(records.templates.at(-1).engine, "ss-rust", "template payload engine");
  assert.equal(records.templates.at(-1).content, configDefaults["ss-rust"] + "\n", "template payload content");
  assert.equal(records.templates.at(-1).name, "My template", "template payload name");
  fill(template(), "name", "Keep template draft");
  change(template(), "xray");
  fill(template(), "content", '{"template":"unsaved"}');
  const beforeDelete = renderCount;
  document.querySelector("[data-delete-template]").click();
  await waitFor(() => renderCount > beforeDelete, "template deletion did not refresh");
  check("ss-rust", archiveDraft, true);
  assert.equal(field(template(), "name").value, "Keep template draft");
  assert.equal(field(template(), "content").value, '{"template":"unsaved"}');

  // Save/reopen all four engines and verify version/history and locked engine.
  for (const engine of engines) {
    state.data.newConfig = true; state.data.archiveConfigId = "";
    await render();
    change(archive(), engine);
    fill(archive(), "name", `Archive ${engine}`);
    fill(archive(), "description", `Notes ${engine}`);
    archive().querySelector("[data-code-reset]").click();
    const content = field(archive(), "content").value;
    const beforeSave = renderCount;
    archive().requestSubmit();
    await waitFor(() => renderCount > beforeSave, `${engine} save did not finish`);
    const payload = records.writes.at(-1).payload;
    assert.equal(payload.engine, engine, "saved engine");
    assert.equal(payload.content, content, "saved source");
    assert.equal(payload.name, `Archive ${engine}`);
    assert.equal(payload.description, `Notes ${engine}`);
    assert.equal(payload.version, 0);
    const savedId = state.data.archiveConfigId;
    // Simulate reopening with a fresh page state, forcing reads from the API.
    state.data = { archiveConfigId: savedId };
    await render();
    check(engine, content);
    assert.equal(field(archive(), "engine").disabled, true, "existing archive engine must remain locked");
    assert.equal(archive().closest(".config-workspace").querySelector(".editor-toolbar-state b").textContent, "v1");
    fill(archive(), "content", content + "\n");
    await render();
    check(engine, content + "\n", true);
    const nextRender = renderCount;
    archive().requestSubmit();
    await waitFor(() => renderCount > nextRender, "new version save did not finish");
    assert.equal(records.writes.at(-1).payload.version, 1);
    assert.equal(records.writes.at(-1).payload.engine, engine);
    check(engine, content + "\n");
    document.querySelector('[data-revision="1"]').click();
    await waitFor(() => document.querySelector(".timeline-preview textarea"), "revision preview missing");
    assert.equal(document.querySelector(".timeline-preview textarea").value, content);
    assert.equal(document.querySelector(".timeline-preview textarea").readOnly, true);
    document.querySelector("[data-restore-revision]").click();
    await waitFor(() => records.restores.length === engines.indexOf(engine) + 1 &&
      archive().closest(".config-workspace").querySelector(".editor-toolbar-state b").textContent === "v3", "restore failed");
    check(engine, content);
    assert.equal(records.restores.at(-1).expected_version, 2);
    writable = false;
    await render();
    assert.equal(field(archive(), "engine").disabled, true);
    assert.equal(field(archive(), "content").readOnly, true);
    assert.equal(field(archive(), "name").readOnly, true);
    assert.equal(archive().querySelector('[type="submit"]'), null);
    assert.equal(archive().querySelector("[data-code-format]"), null);
    assert.equal(template(), null);
    writable = true;
  }
  state.data = { newConfig: true };
  await render();
  change(archive(), "xray");
  saveGate = deferred();
  archive().requestSubmit();
  fill(archive(), "content", '{"edited":"during save"}');
  fill(archive(), "description", "Unsaved during save");
  const beforePendingSave = renderCount;
  saveGate.resolve(); saveGate = null;
  await waitFor(() => renderCount > beforePendingSave, "pending save never finished");
  check("xray", '{"edited":"during save"}', true);
  assert.equal(field(archive(), "description").value, "Unsaved during save");
  archive().querySelector("[data-code-reset]").click();
  check("xray");
  // A history preview replaces the workspace while a clean save is pending.
  // Later edits belong to the new mounted draft, not the submit handler's form.
  fill(archive(), "description", "");
  saveGate = deferred();
  archive().requestSubmit();
  state.data.revisionVersion = 1;
  await render();
  fill(archive(), "name", "Edited during history preview");
  fill(archive(), "content", '{"edited":"after history preview"}');
  fill(archive(), "description", "Keep history preview draft");
  const beforeHistorySave = renderCount;
  saveGate.resolve(); saveGate = null;
  await waitFor(() => renderCount > beforeHistorySave, "history save never finished");
  check("xray", '{"edited":"after history preview"}', true);
  assert.equal(field(archive(), "name").value, "Edited during history preview");
  assert.equal(field(archive(), "description").value, "Keep history preview draft");
  archive().querySelector("[data-code-reset]").click();
  check("xray");
  // Latest refresh wins and another account cannot inherit a page draft.
  readGate = deferred();
  const stale = render();
  state.data = { newConfig: true };
  const latest = render();
  readGate.resolve(); readGate = null;
  await Promise.all([stale, latest]);
  check("mihomo");
  assert.equal(field(archive(), "name").value, "新配置", "new account inherited a draft");
  assert.equal(records.errors.length, 0, records.errors.join("; "));
}
