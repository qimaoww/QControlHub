import { configDefaults } from "./config-defaults.js";
import { readableConfigSource } from './code-format.js';

export function createArchiveDraft(config = {}) {
  const engine = config.engine || "mihomo";
  const content = readableConfigSource(config.content ?? configDefaults[engine], engine);
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
