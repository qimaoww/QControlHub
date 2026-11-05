// Legacy fragment helpers remain available for old imports and API compatibility.
// Both themes now edit the complete source; inbound selection is owned by
// config-restrictions and never replaces the editor with an individual fragment.
export { configJSONMembers, canonicalConfigJSON, splitConfigFiles, mergeConfigFiles, configFileDisplayName } from "./config-file-model.js";

export function bindConfigFiles() {
  return null;
}
