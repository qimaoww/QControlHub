import { startAnalytics } from "../analytics/app.js";
import { configDefaults } from "../modules/config-defaults.js";
import { assert, delay, waitFor } from "./assertions.mjs";

const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const change = (input, value) => { input.value = value; input.dispatchEvent(new Event("change", { bubbles: true })); };
const fill = (form, name, value) => { form.elements[name].value = value; form.elements[name].dispatchEvent(new Event("input", { bubbles: true })); };
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };

export async function testArchiveEnginesRuntime(preview = false) {
  const records = { writes: [], templates: [], restores: [] }, configs = [], templates = [], revisions = new Map();
  let writable = true, saveGate, readGate;
  const nativeFetch = globalThis.fetch;
  let session;
  const api = async (path, options) => {
    const method = options.method || "GET", payload = JSON.parse(options.body || "{}");
    if (method !== "GET") assert.equal(new Headers(options.headers).get("X-QControlHub-CSRF"), "archive-fixture", "archive mutation missing CSRF");
    if (path === "/auth/session") return session;
    if (method === "GET") {
      if (path === "/configs") { const snapshot = configs.map(item => ({ ...item })); if (readGate) await readGate.promise; return snapshot; }
      if (path === "/templates") return templates;
      if (path === "/agents") return [];
      const match = path.match(/^\/configs\/([^/]+)\/revisions(?:\/(\d+))?/);
      if (match) { const history = revisions.get(match[1]) || []; return match[2] ? history.find(item => item.version === Number(match[2])) : history; }
    }
    if (path === "/templates" && method === "POST") {
      records.templates.push(payload); templates.push({ ...payload, id: `tpl-${templates.length + 1}`, updated_at: new Date().toISOString() }); return templates.at(-1);
    }
    if (path.startsWith("/templates/") && method === "DELETE") { templates.splice(templates.findIndex(item => item.id === path.split("/")[2]), 1); return null; }
    if (path.endsWith("/restore")) {
      records.restores.push(payload);
      const id = path.split("/")[2], current = configs.find(item => item.id === id), revision = revisions.get(id).find(item => item.version === Number(path.split("/")[4]));
      assert.equal(payload.expected_version, current.version, "restore missing current concurrency version");
      Object.assign(current, { ...revision, version: current.version + 1, updated_at: new Date().toISOString() }); revisions.get(id).push({ ...current }); return { ...current };
    }
    if (path === "/configs" && method === "POST" || method === "PUT") {
      records.writes.push({ method, payload }); if (saveGate) await saveGate.promise;
      const id = method === "POST" ? `cfg-${configs.length + 1}` : path.split("/")[2];
      if (method === "PUT") assert.equal(payload.version, configs.find(item => item.id === id).version, "save missing current concurrency version");
      const saved = { ...payload, id, version: payload.version + 1, updated_at: new Date().toISOString() };
      if (method === "POST") configs.push(saved); else Object.assign(configs.find(item => item.id === id), saved);
      revisions.set(id, [...(revisions.get(id) || []), { ...saved }]); return { ...saved };
    }
    throw new Error(`Unexpected archive API ${method} ${path}`);
  };
  globalThis.fetch = async (url, options = {}) => {
    if (!String(url).startsWith("/api/v1")) return nativeFetch(url, options);
    return new Response(JSON.stringify(await api(String(url).slice(7), options)), { headers: { "Content-Type": "application/json" } });
  };
  const styles = Object.assign(document.createElement("link"), { rel: "stylesheet", href: "/assets/analytics/theme.css" });
  const loaded = new Promise((resolve, reject) => { styles.onload = resolve; styles.onerror = reject; }); document.head.append(styles); await loaded;
  const button = (label, scope = document) => [...scope.querySelectorAll("button")].find(control => control.textContent === label);
  const settled = async () => { await waitFor(() => !document.querySelector('#archive-test[aria-busy="true"]'), "archive operation remained pending"); await delay(15); };
  const openDialog = title => [...document.querySelectorAll("dialog[open]")].find(box => box.querySelector("h2")?.textContent === title);
  const formOf = box => box.querySelector("form");
  const retainedClose = box => { button("保留草稿并关闭", box).click(); assert.ok(!box.open, "retained draft did not close"); };
  const mount = async (account = "alice") => {
    window.dispatchEvent(new Event("pagehide"));
    document.querySelectorAll("dialog").forEach(box => box.close());
    session = { user_id: account, username: account, role: "user", csrf_token: "archive-fixture", permissions: ["configs.read", "templates.read", "agents.read", ...(writable ? ["configs.write", "configs.restore", "configs.delete", "templates.write", "templates.delete"] : [])] };
    document.body.replaceChildren(Object.assign(document.createElement("div"), { id: "archive-test" })); history.replaceState(null, "", "#archive-config");
    startAnalytics({ app: document.querySelector("#archive-test"), session });
    await waitFor(() => document.querySelector('[aria-label="配置档案"]'), "actual analytics archive did not load"); await settled();
  };
  const create = () => { button("新建配置").click(); return openDialog("新建配置"); };
  const rowFor = name => [...document.querySelectorAll('[aria-label="配置档案"] tbody tr')].find(row => row.cells[0].textContent === name);
  const reopen = name => { button("查看与编辑", rowFor(name)).click(); return openDialog(name); };
  const check = (form, engine, content = configDefaults[engine], dirty = false, valid = true) => {
    assert.equal(form.elements.engine.value, engine, "selected engine"); assert.equal(form.elements.content.value, content, `${engine} source`);
    assert.equal(form.elements.content.getAttribute("aria-label"), `${engine} 配置源码`, "source accessible engine identity");
    assert.ok(form.querySelector(".a-editor .a-badge").textContent.includes(engine === "mihomo" ? "YAML" : "JSON"), "source language missing");
    assert.equal(form.elements.content.validity.valid, valid, "source validation");
    if (writable) { assert.equal(button("重置源码", form).disabled, !dirty, "source reset state"); assert.equal(form.querySelector('[type="submit"]').disabled, !valid, "save validity"); }
  };
  await mount();
  if (preview) { create(); return; }
  try {
    let box = create(), form = formOf(box);
    fill(form, "name", "My archive"); fill(form, "description", "Keep these notes");
    for (const engine of [...engines, ...engines.toReversed(), "xray", "ss-rust", "sing-box", "xray"]) {
      change(form.elements.engine, engine); check(form, engine); assert.equal(form.elements.name.value, "My archive"); assert.equal(form.elements.description.value, "Keep these notes");
    }
    const editor = form.elements.content, editorHost = editor.closest('.a-editor');
    const feedback = document.getElementById(editor.getAttribute('aria-describedby'));
    assert.equal(feedback?.getAttribute('role'), 'status', "source feedback is not associated with the input or announced");
    const format = () => button("格式化源码", form).click();
    const unformatted = '{"tag":"demo","port":443,"tls":{"enabled":true}}';
    fill(form, "content", unformatted); format();
    const formatted = '{\n  "tag": "demo",\n  "port": 443,\n  "tls": {\n    "enabled": true\n  }\n}\n';
    assert.equal(editor.value, formatted, "JSON formatting must use two spaces and a final newline");
    format(); assert.equal(editor.value, formatted, "formatting is not idempotent");
    assert.equal(form.elements.content, editor, "formatting replaced the source control");
    assert.equal(editor.closest('.a-editor'), editorHost, "formatting rebuilt the editor");
    for (const [content, message] of [
      ['{"tag":"demo",}', /JSON 语法错误/],
      ['['.repeat(600000), /当前内容无法安全格式化/],
      [JSON.stringify(Array(840001).fill(0)), /超过 2 MiB 上限/],
    ]) {
      fill(form, "content", content);
      const validBefore = editor.validity.valid, resetBefore = button("重置源码", form).disabled;
      format();
      assert.equal(editor.value, content, "failed formatting changed the source");
      assert.equal(editor.validity.valid, validBefore, "failed formatting changed source validation");
      assert.equal(button("重置源码", form).disabled, resetBefore, "failed formatting changed the draft baseline");
      assert.match(editorHost.textContent, message, "formatter failure lacks safe local feedback");
      assert.equal(form.elements.content, editor, "formatter failure replaced the input");
    }
    button("重置源码", form).click(); check(form, "xray");
    for (const engine of engines) {
      change(form.elements.engine, engine);
      const content = engine === "mihomo" ? `${configDefaults[engine]}# my YAML draft\n` : `{"custom":"draft-${engine}"}`;
      fill(form, "content", content); change(form.elements.engine, engines[(engines.indexOf(engine) + 1) % engines.length]); change(form.elements.engine, engine); check(form, engine, content, true);
      button("格式化源码", form).click(); const formatted = form.elements.content.value;
      change(form.elements.engine, engines[(engines.indexOf(engine) + 1) % engines.length]); change(form.elements.engine, engine); check(form, engine, formatted, true);
      button("重置源码", form).click(); check(form, engine);
    }
    change(form.elements.engine, "xray"); fill(form, "content", '{"broken":'); check(form, "xray", '{"broken":', true, false);
    change(form.elements.engine, "sing-box"); check(form, "sing-box"); change(form.elements.engine, "xray"); check(form, "xray", '{"broken":', true, false);
    assert.ok(form.querySelector(".a-editor").textContent.includes("JSON 语法错误"), "invalid JSON lacks feedback");
    retainedClose(box); box = create(); form = formOf(box); check(form, "xray", '{"broken":', true, false); button("重置源码", form).click();
    change(form.elements.engine, "ss-rust"); const archiveDraft = configDefaults["ss-rust"] + "\n"; fill(form, "content", archiveDraft);
    button("新建模板").click(); const templateBox = openDialog("新建模板"), templateForm = formOf(templateBox); fill(templateForm, "name", "My template");
    for (const engine of [...engines, "xray", "sing-box", "ss-rust"]) {
      change(templateForm.elements.engine, engine); check(templateForm, engine); check(form, "ss-rust", archiveDraft, true);
      fill(templateForm, "content", configDefaults[engine] + "\n"); change(templateForm.elements.engine, engine === "mihomo" ? "xray" : "mihomo"); change(templateForm.elements.engine, engine);
      check(templateForm, engine, configDefaults[engine] + "\n", true); fill(templateForm, "content", configDefaults[engine]);
    }
    fill(templateForm, "content", configDefaults["ss-rust"] + "\n"); templateForm.requestSubmit(); await waitFor(() => !templateBox.open, "template save did not complete"); await settled(); check(form, "ss-rust", archiveDraft, true);
    assert.equal(records.templates.at(-1).engine, "ss-rust"); assert.equal(records.templates.at(-1).content, configDefaults["ss-rust"] + "\n"); assert.equal(records.templates.at(-1).name, "My template");
    button("新建模板").click(); let pendingTemplate = openDialog("新建模板"), pendingForm = formOf(pendingTemplate);
    fill(pendingForm, "name", "Keep template draft"); change(pendingForm.elements.engine, "xray"); fill(pendingForm, "content", '{"template":"unsaved"}'); retainedClose(pendingTemplate);
    retainedClose(box);
    const templateRow = document.querySelector('[aria-label="配置模板"] tbody tr'); button("删除", templateRow).click();
    const confirmDelete = await waitFor(() => openDialog("删除模板"), "template delete confirmation missing"); button("删除模板", confirmDelete).click(); await waitFor(() => !templates.length, "template delete not applied"); await settled();
    box = create(); form = formOf(box); check(form, "ss-rust", archiveDraft, true); button("新建模板").click(); pendingTemplate = openDialog("新建模板"); pendingForm = formOf(pendingTemplate);
    assert.equal(pendingForm.elements.name.value, "Keep template draft"); check(pendingForm, "xray", '{"template":"unsaved"}', true); retainedClose(pendingTemplate);
    for (const engine of engines) {
      change(form.elements.engine, engine); fill(form, "name", `Archive ${engine}`); fill(form, "description", `Notes ${engine}`); button("重置源码", form).click();
      const content = form.elements.content.value; form.requestSubmit(); await waitFor(() => configs.some(item => item.name === `Archive ${engine}`), "new archive save not applied"); await settled();
      const payload = records.writes.at(-1).payload; assert.equal(payload.engine, engine); assert.equal(payload.content, content); assert.equal(payload.version, 0); assert.equal(payload.description, `Notes ${engine}`);
      // Reopen from the saved API record with its engine locked.
      retainedClose(box); box = reopen(`Archive ${engine}`); form = formOf(box); check(form, engine, content); assert.ok(form.elements.engine.disabled, "existing engine is mutable");
      fill(form, "content", content + "\n"); retainedClose(box); box = reopen(`Archive ${engine}`); form = formOf(box); check(form, engine, content + "\n", true);
      form.requestSubmit(); await waitFor(() => configs.find(item => item.name === `Archive ${engine}`).version === 2, "second save did not advance internal version"); await settled(); check(form, engine, content + "\n"); retainedClose(box);
      const current = configs.find(item => item.name === `Archive ${engine}`), historyBefore = revisions.get(current.id).length;
      button("历史", rowFor(current.name)).click(); const historyBox = await waitFor(() => openDialog(`${current.name} · 保存历史`), "archive history missing"); await settled();
      const rows = [...historyBox.querySelectorAll("tbody tr")]; button("预览", rows[0]).click(); await waitFor(() => historyBox.querySelector("pre"), "history preview missing"); await settled(); assert.equal(historyBox.querySelector("pre").textContent, content);
      button("恢复此档案", historyBox).click(); const restore = await waitFor(() => openDialog("恢复配置"), "restore confirmation missing"); button("恢复配置", restore).click(); await waitFor(() => !historyBox.open, "restore did not close history"); await settled();
      assert.equal(records.restores.at(-1).expected_version, 2); assert.equal(current.version, 3); assert.equal(revisions.get(current.id).length, historyBefore + 1, "restore overwrote original history");
      box = reopen(current.name); form = formOf(box); check(form, engine, content); assert.ok(form.textContent.includes("已保存"), "saved timestamp missing"); retainedClose(box);
      if (engine !== engines.at(-1)) { box = create(); form = formOf(box); }
    }
    box = create(); form = formOf(box); change(form.elements.engine, "xray"); fill(form, "name", "Queued save");
    saveGate = deferred(); form.requestSubmit(); await waitFor(() => form.getAttribute("aria-busy") === "true", "save not pending");
    const submitted = form.elements.content.value; button("格式化源码", form).click(); assert.equal(form.elements.content.value, submitted, "pending format mutated submitted source");
    fill(form, "content", '{"edited":"during save"}'); fill(form, "description", "Unsaved during save"); saveGate.resolve(); saveGate = null; await settled();
    check(form, "xray", '{"edited":"during save"}', true); assert.ok(form.textContent.includes("有未保存修改"), "queued draft is mislabeled as saved"); assert.equal(form.elements.description.value, "Unsaved during save"); button("重置源码", form).click(); check(form, "xray"); retainedClose(box);
    writable = false; await mount();
    for (const engine of engines) {
      box = reopen(`Archive ${engine}`); form = formOf(box); check(form, engine);
      assert.ok(form.elements.content.readOnly && form.elements.name.readOnly && form.elements.engine.disabled, "read-only archive permits edits");
      assert.equal(form.querySelector('[type="submit"]'), null); assert.equal(button("格式化源码", form), undefined); assert.equal(button("重置源码", form), undefined); assert.equal(button("新建模板"), undefined); retainedClose(box);
    }
    writable = true;
    // A pending old account response cannot write into the new account mount.
    readGate = deferred(); const staleMount = mount("alice"); await delay(20); const latestMount = mount("bob"); readGate.resolve(); readGate = null; await Promise.all([staleMount, latestMount]);
    box = create(); form = formOf(box); check(form, "mihomo"); assert.equal(form.elements.name.value, "", "new account inherited draft name"); assert.equal(form.elements.description.value, "", "new account inherited draft notes");
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "archive dialog overflows viewport");
  } finally {
    window.dispatchEvent(new Event("pagehide")); document.querySelectorAll("dialog").forEach(box => box.close()); globalThis.fetch = nativeFetch; styles.remove();
  }
}
