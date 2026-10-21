import { bindEvent } from "./refresh.js";
import { createConfigDocument } from "./config-document.js";

export { configJSONMembers, canonicalConfigJSON, splitConfigFiles, mergeConfigFiles, configFileDisplayName } from "./config-json.js";

// A DOM adapter only: document content, selection and dirty state belong to
// the workspace model, including on native YAML/ssserver configurations.
export function bindConfigFiles(form, engine, notify, options = {}) {
  if (!form) return null;
  const editor = form.querySelector("[data-code-editor]"), input = form.querySelector("[data-code-input]");
  if (!editor || !input) return null;
  const previous = editor.configFileController;
  previous?.dirty();
  let model = options.document || previous?.document || createConfigDocument(engine, input.value, {inbounds:options.inbounds});
  const list = form.querySelector("[data-config-objects]");
  const readOnly = input.readOnly;
  let updating = false;
  const esc = value => String(value ?? "").replace(/[&<>"']/g, char => ({"&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;"}[char]));
  const flush = () => { if (!updating && !input.readOnly && input.value !== model.text()) model.edit(input.value); };
  const revealSelectedObject = () => {
    const selected = list?.querySelector('[aria-current="true"]');
    if (!selected || !list.clientWidth) return;
    const bounds = list.getBoundingClientRect(), item = selected.getBoundingClientRect();
    // Keep the selected button visible without moving the page or editor.
    if (item.left < bounds.left) list.scrollLeft += item.left - bounds.left - 2;
    else if (item.right > bounds.right) list.scrollLeft += item.right - bounds.right + 2;
  };
  const render = () => {
    const objects = model.objects(), chosen = model.selected();
    const item = (id, attributes, title, detail, dirty = false) => `<button type="button" class="config-file-button${chosen === id ? " active" : ""}${dirty ? " is-dirty" : ""}" data-config-object="${esc(id)}" ${attributes} aria-current="${chosen === id}" aria-pressed="${chosen === id}" aria-label="${esc(`${title} · ${detail}${dirty ? " · 未保存" : ""}`)}"><b>${esc(title)}</b><small>${esc(detail)}</small></button>`;
    const buttons = objects.map((object, index) => item(object.id, `data-config-file="${index}"`, object.title,
      `${object.kind === "common" ? "公共" : object.kind === "outbound" ? object.system ? "统计" : "出站" : "入站"}${object.detail ? ` · ${object.detail}` : ""}`, object.dirty));
    if (!model.segmented) for (const inbound of model.inbounds()) {
      const id = `native:${inbound.tag}:${inbound.port}`;
      buttons.push(item(id, `data-access-inbound="${esc(inbound.tag)}"`, inbound.tag, `入站 · ${inbound.protocol || engine} · ${Number(inbound.port)}`));
    }
    if (model.segmented) buttons.push(item("source", "data-config-source", "完整源码", readOnly ? "查看完整配置" : "编辑完整配置", model.dirty()));
    buttons.push(item("preview", "data-config-preview", "完整配置预览", "只读 · 包含全部编辑内容"));
    if (list) {
      // Keep focused navigation elements stable while typing or switching.
      const template = document.createElement("template"); template.innerHTML = buttons.join("");
      for (const [index, fresh] of [...template.content.children].entries()) {
        const old = [...list.children].find(element => element.dataset.configObject === fresh.dataset.configObject);
        if (old) {
          old.className = fresh.className;
          if (fresh.hasAttribute("data-config-file")) old.dataset.configFile = fresh.dataset.configFile;
          for (const name of ["aria-current", "aria-pressed", "aria-label"]) old.setAttribute(name, fresh.getAttribute(name));
          if (old.innerHTML !== fresh.innerHTML) old.innerHTML = fresh.innerHTML;
        }
        const element = old || fresh;
        if (list.children[index] !== element) list.insertBefore(element, list.children[index] || null);
      }
      const ids = new Set([...(template.content.children)].map(element => element.dataset.configObject));
      // Appending removes nodes from the fragment, so include model IDs too.
      objects.forEach(object => ids.add(object.id)); ids.add("preview"); if (model.segmented) ids.add("source");
      model.inbounds().forEach(inbound => ids.add(`native:${inbound.tag}:${inbound.port}`));
      [...list.children].filter(element => !ids.has(element.dataset.configObject)).forEach(element => element.remove());
    }
    const count = form.querySelector("[data-config-object-count]");
    if (count) count.textContent = model.segmented
      ? `${objects.filter(item => item.kind === "inbound").length} 入站 · ${objects.filter(item => item.kind === "outbound" && !item.system).length} 出站`
      : `${model.inbounds().length} 入站 · 完整配置`;
    const label = editor.querySelector(".code-file-meta b");
    if (label) label.textContent = chosen === "preview" ? "完整配置预览" : chosen === "source" ? "完整源码" : (objects.find(item => item.id === chosen)?.title || model.selectedInbound()?.tag || "完整源码");
    const summary = form.querySelector(".config-draft-summary");
    if (summary) {
      summary.dataset.dirty = model.dirty() ? "1" : "0";
      summary.textContent = readOnly ? "只读" : model.dirty() ? "有未保存修改" : "";
    }
    options.onChange?.(model);
  };
  const display = () => {
    updating = true;
    input.value = model.text();
    input.readOnly = readOnly || model.selected() === "preview";
    editor.codeBaseline = {content:model.original(), status:model.selected() === "preview" ? "完整配置 · 只读" : readOnly ? "只读" : "已保存", validation:""};
    render();
    input.dispatchEvent(new Event("input", {bubbles:true}));
    input.dispatchEvent(new Event("config-selection", {bubbles:true}));
    updating = false;
    revealSelectedObject();
  };
  bindEvent(list, "click", event => {
    const button = event.target.closest("[data-config-object]");
    if (!button || !list.contains(button)) return;
    event.preventDefault();
    try { flush(); if (model.select(button.dataset.configObject)) display(); }
    catch (error) { notify(error.message, "error"); }
  });
  bindEvent(input, "input", () => {
    if (updating || input.readOnly) return;
    model.edit(input.value); render();
  }, {bindingKey:"config-document"});
  const controller = {
    get document() { return model; },
    selectedCommon:() => model.selectedCommon(),
    selectedInbound:() => model.selectedInbound(),
    selectedOutbound:() => model.objects().find(item => item.id === model.selected() && item.kind === "outbound"),
    selectInbound(tag, port) { flush(); if (model.selectInbound(tag, port)) display(); },
    content() { flush(); return model.content(); },
    paths:() => model.objects().map(file => file.path),
    original:() => model.original(),
    dirty() { flush(); return model.dirty(); },
    currentDirty:() => model.currentDirty(),
    rebase(document) { model = document; display(); },
    reset() { model.reset(); display(); },
    replace(content) { flush(); model.replace(content); display(); },
    display,
    refreshObjects() { render(); input.dispatchEvent(new Event("config-selection", {bubbles:true})); },
  };
  editor.configFileController = controller;
  bindEvent(globalThis.window, "resize", () => { if (form.isConnected) revealSelectedObject(); }, {bindingKey:"config-objects"});
  display();
  return controller;
}
