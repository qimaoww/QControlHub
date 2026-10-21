import { reconcileView } from "./refresh.js";
import { syncSelectionMotion } from "./motion.js";
import { preparePresetFields } from "./preset-runtime.js";
import { protocolNavigationNames } from "./server-plan-form.js";

// Compose only the requested editor from explicit component outputs. No page
// template is parsed to extract or relocate another module's internal DOM.
export function renderEmbeddedPresetView(host, {viewKey, protocols, selectedProtocolKey, commonFields, selectedField,
  executionCallout, protocolForm, advancedStudio, revisionTimeline, esc}) {
  const groups = [...new Set(protocols.map(protocol => protocol.group || ""))];
  const options = group => protocols.filter(protocol => (protocol.group || "") === group).map(protocol =>
    `<option value="${esc(protocol.key)}"${protocol.key === selectedProtocolKey ? " selected" : ""}>${esc(protocolNavigationNames[protocol.key] || protocol.name)}</option>`).join("");
  const selection = host.kind === "add"
    ? `<label>协议<select data-preset-protocol>${groups.map(group => group ? `<optgroup label="${esc(group)}">${options(group)}</optgroup>` : options(group)).join("")}</select></label>`
    : host.kind.startsWith("common-") && commonFields.length
      ? `<label>公共配置项<select data-common-field>${commonFields.map(field => `<option value="${esc(field.key)}"${field.key === selectedField?.key ? " selected" : ""}>${esc(field.label)} · ${esc(field.key)}</option>`).join("")}</select></label>` : "";
  const fresh = host.root.cloneNode(false);
  fresh.innerHTML = `<div data-refresh-key="${esc(viewKey)}" data-motion-region="preset-selection" data-motion-key="${esc(viewKey)}"><div class="config-command-bar inbound-editor-toolbar">${selection}<button class="button small" type="button" data-refresh-preset>刷新状态</button></div>${host.kind === "history" ? revisionTimeline : executionCallout + (protocolForm ? `<article class="settings-form">${protocolForm}</article>` : advancedStudio)}</div>`;
  fresh.querySelectorAll("details#advanced, details#inbound-options, details#revisions").forEach(element => { element.open = true; });
  preparePresetFields(fresh);
  reconcileView(host.root, fresh);
  syncSelectionMotion(host.root);
}
