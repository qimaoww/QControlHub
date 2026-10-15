import { bindEvent } from "./refresh.js";
import { configDefaults } from "./config-defaults.js";

export function createArchiveDraft(config = {}) {
  const engine = config.engine || "mihomo";
  const content = config.content ?? configDefaults[engine];
  return {
    ...config, engine, name: config.name ?? "", description: config.description ?? "",
    originalName: config.name ?? "", originalDescription: config.description ?? "",
    sources: new Map([[engine, { content, original: content }]]),
  };
}

export function archiveDraftDirty(draft) {
  return draft.name !== draft.originalName || draft.description !== draft.originalDescription ||
    [...draft.sources.values()].some(source => source.content !== source.original);
}

// Archive and template forms own separate sessions; switching never fetches or
// re-renders, including switches between engines that both use JSON.
export function bindArchiveDraft(form, draft, { engineName, writable, template = false }) {
  if (!form) return () => {};
  const field = name => form.querySelector(`[name="${name}"]`);
  const select = field("engine");
  const input = field("content");
  if (!select || !input) return () => {};
  const editor = form.querySelector("[data-code-editor]");
  const currentSource = () => {
    if (!draft.sources.has(draft.engine)) {
      const content = configDefaults[draft.engine];
      draft.sources.set(draft.engine, { content, original: content });
    }
    return draft.sources.get(draft.engine);
  };
  const capture = () => {
    if (!writable || form.isConnected === false) return;
    if (field("name")) draft.name = field("name").value;
    if (field("description")) draft.description = field("description").value;
    if (input) currentSource().content = input.value;
  };
  const sync = () => {
    const engine = select.value;
    draft.engine = engine;
    const source = currentSource();
    const language = engine === "mihomo" ? "YAML" : "JSON";
    form.dataset.engine = engine;
    input.value = source.content;
    input.setAttribute("aria-label", `${engineName(engine)} ${template ? "模板正文" : "配置档案源码"}`);
    if (editor) {
      editor.codeBaseline = { content: source.original, status: draft.id ? "已保存" : "草稿", validation: "" };
      editor.dataset.codeLanguage = language;
      editor.querySelector(".code-language").textContent = language;
      editor.querySelector(".code-file-meta b").textContent = engine === "mihomo" ? "config.yaml" : "config.json";
      const badge = form.closest(".config-workspace").querySelector(".editor-toolbar-state .engine-badge");
      badge.className = `engine-badge ${engine}`;
      badge.textContent = engineName(engine);
    }
    input.dispatchEvent(new Event("input", { bubbles: true }));
  };
  if (field("name")) field("name").value = draft.name;
  if (field("description")) field("description").value = draft.description;
  if (writable) {
    bindEvent(form, "input", capture);
    if (!select.disabled) bindEvent(select, "change", () => {
      capture();
      sync();
    });
  }
  select.value = draft.engine;
  sync();
  return capture;
}
