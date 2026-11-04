import { cancelMotion, enterSurface } from "./motion.js";

import { configJSONMembers, canonicalConfigJSON, splitConfigFiles, mergeConfigFiles, configFileDisplayName } from "./config-file-model.js";
export { configJSONMembers, canonicalConfigJSON, splitConfigFiles, mergeConfigFiles, configFileDisplayName } from "./config-file-model.js";

export function bindConfigFiles(form, engine, notify) {
  if (!form || !["xray","sing-box"].includes(engine)) return null;
  const editor = form.querySelector("[data-code-editor]"), input = form.querySelector("[data-code-input]");
  if (!editor || !input) return null;
  // Reconciliation preserves the visible fragment's value, which is not the
  // complete configuration. Rebinding from that fragment would silently lose
  // every other inbound tab. Keep real edits, otherwise use the fresh full
  // server baseline supplied by the rendered textarea.
  const previous = editor.configFileController;
  const selectedInbound = previous?.selectedInbound();
  if (previous) {
    input.value = previous.dirty() ? previous.content() : input.defaultValue;
    editor.querySelector(".config-file-buttons")?.remove();
    editor.querySelector(".config-file-navigation")?.remove();
  }
  let files;
  try { files = splitConfigFiles(engine,input.value); } catch { return null; }
  const originals = files.map(file => file.content), readOnly = input.readOnly;
  let selected = 0;
  const tabs = document.createElement("nav");
  tabs.className = "config-file-buttons";
  tabs.setAttribute("aria-label", "选择入站或公共配置");
  const fileList = document.createElement("div");
  fileList.className = "config-file-list";
  tabs.append(fileList);
  const buttons = files.map((file, i) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "config-file-button";
    button.dataset.configFile = String(i);
    button.append(document.createElement("b"), document.createElement("small"));
    button.addEventListener("click", () => switchFile(i));
    fileList.append(button);
    return button;
  });
  const fileLabel = editor.querySelector(".code-file-meta b");
  const renderButtons = () => {
    files.forEach((file, i) => {
      const button = buttons[i];
      let name = file.path.split("/").at(-1).replace(/\.json$/, "");
      let detail = "路由 · DNS · 共享出口";
      if (i === 0) name = "公共配置";
      else {
        detail = "入站配置";
        try {
          const entry = JSON.parse(file.content)[file.path.split("/")[0]]?.[0];
          name = typeof entry?.tag === "string" && entry.tag ? entry.tag : name;
          detail = [entry?.type || entry?.protocol, entry?.listen_port ?? entry?.port].filter(value => value !== undefined && value !== "").join(" · ") || detail;
        } catch { /* Invalid drafts keep their navigation identity. */ }
      }
      button.querySelector("b").textContent = name;
      button.querySelector("small").textContent = detail;
      button.title = configFileDisplayName(file.path, file.content);
      button.classList.toggle("active", selected === i);
      button.setAttribute("aria-pressed", String(selected === i));
    });
    if (fileLabel) fileLabel.textContent = selected === "preview" ? "合并预览 · config.json" : files[selected].path;
  };
  editor.querySelector(".code-editor-toolbar").before(tabs);
  const navigation = document.createElement("div"); navigation.className = "config-file-navigation";
  const summary = document.createElement("span");
  summary.className = "config-draft-summary";
  summary.setAttribute("role", "status");
  const previewButton = document.createElement("button"); previewButton.type = "button"; previewButton.className = "button"; previewButton.textContent = "合并预览";
  previewButton.dataset.configPreview = "";
  previewButton.setAttribute("aria-pressed", "false");
  const actions = document.createElement("div"); actions.className = "config-file-actions";
  actions.append(previewButton);
  navigation.append(summary, actions);
  editor.querySelector(".code-editor-toolbar").after(navigation);
  let lastFile = 0;
  previewButton.addEventListener("click", () => {
    switchFile(selected === "preview" ? lastFile : "preview");
  });
  const save = () => { if (selected !== "preview" && !readOnly) files[selected].content = input.value; };
  const updateDrafts = () => {
    let count = 0;
    files.forEach((file, i) => {
      const dirty = file.content !== originals[i];
      if (dirty) count++;
      buttons[i].classList.toggle("is-dirty", dirty);
      buttons[i].setAttribute("aria-label", `${buttons[i].querySelector("b").textContent} · ${buttons[i].querySelector("small").textContent}${dirty ? " · 未保存" : ""}`);
    });
    summary.dataset.dirty = count ? "1" : "0";
    const message = `${selected === "preview" ? "合并预览只读 · " : ""}${count ? `${count} 个未保存` : `${files.length} 个源码文件`} · 保存与部署包含全部文件`;
    if (summary.textContent !== message) summary.textContent = message;
    return count;
  };
  // Reflect renamed preset tags on navigation, preserving invalid drafts until
  // the user fixes them. Only the returned merged content is saved/deployed.
  const refreshNames = () => {
    try {
      const renamed = splitConfigFiles(engine, mergeConfigFiles(files));
      if (renamed.length !== files.length) return;
      files.forEach((file,i) => {
        file.path = renamed[i].path;
      });
    } catch { /* A syntax error must not discard a draft or block switching. */ }
  };
  const controller = {
    selectedCommon() { return selected === 0; },
    selectedInbound() {
      if (selected === "preview" || selected === 0) return null;
      try {
        const entry = JSON.parse(input.value)[files[selected].path.split("/")[0]]?.[0];
        return entry && { tag: entry.tag, port: Number(entry.port ?? entry.listen_port) };
      } catch { return null; }
    },
    selectInbound(tag, port) {
      const index = files.findIndex((file, i) => {
        if (!i) return false;
        try { const entry = JSON.parse(file.content)[file.path.split("/")[0]]?.[0]; return entry?.tag === tag && Number(entry.port ?? entry.listen_port) === port; } catch { return false; }
      });
      if (index > 0) switchFile(index);
    },
    content() { save(); return mergeConfigFiles(files); },
    paths() { save(); refreshNames(); return files.map(file => file.path); },
    original() { return selected === "preview" ? input.value : originals[selected]; },
    dirty() { save(); return updateDrafts() > 0; },
    currentDirty() { return selected !== "preview" && input.value !== originals[selected]; },
    reset() {
      if (selected === "preview" || readOnly) return;
      input.value = files[selected].content = originals[selected];
      refreshNames(); renderButtons(); updateDrafts();
    },
  };
  function switchFile(next) {
    if (next === selected) return;
    save(); refreshNames();
    try {
      const content = next === "preview" ? mergeConfigFiles(files) : files[next].content;
      if (next !== "preview") lastFile = next;
      selected = next; input.value = content; input.readOnly = readOnly || next === "preview";
      previewButton.textContent = next === "preview" ? "返回文件" : "合并预览";
      previewButton.setAttribute("aria-pressed", String(next === "preview"));
      input.dispatchEvent(new Event("input", {bubbles:true}));
      const frame = editor.querySelector(".code-editor-frame");
      if (frame) {
        cancelMotion(frame);
        enterSurface(frame, { token: "--motion-feedback", id: "qch-selection" });
      }
    } catch (error) { notify(error.message,"error"); }
    renderButtons();
    updateDrafts();
  }
  renderButtons();
  updateDrafts();
  input.value = files[0].content;
  const reset = editor.querySelector("[data-code-reset]");
  if (reset) reset.textContent = "恢复当前文件";
  editor.configFileController = controller;
  if (selectedInbound) controller.selectInbound(selectedInbound.tag, selectedInbound.port);
  return controller;
}
