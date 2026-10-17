import { describeEngineCapability, describeShareEngineRuntime } from "./status-controls.js";

const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const names = { mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "Shadowsocks Rust" };
const defaultEsc = (value) => String(value ?? "").replace(/[&<>"']/g, (char) => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
})[char]);

export function sharedEngineNames(selected = []) {
  return engines.filter(engine => selected.includes(engine)).map(engine => names[engine]).join(" / ") || "未分配";
}

export function sharedEngineRuntimeBadges(share = {}, agent = null, esc = defaultEsc) {
  const runtimeMap = share?.engine_runtime;
  if (!runtimeMap || typeof runtimeMap !== "object") return "";
  const selected = engines.filter((engine) => (share?.engines || []).includes(engine) && runtimeMap[engine]);
  if (!selected.length) return "";
  return `<div class="share-engine-runtime-strip" data-share-engine-runtime aria-label="分配内核实例状态">${selected.map((engine) => {
    const info = describeShareEngineRuntime(engine, runtimeMap[engine], { agent });
    if (!info) return "";
    return `<span class="share-engine-runtime-chip ${info.tone}" data-share-engine="${esc(engine)}" data-share-engine-state="${esc(info.state)}" title="${esc(`${names[engine]} · ${info.statusLabel}${info.configVersion ? ` · 配置 v${info.configVersion}` : ""}`)}"><i aria-hidden="true"></i><b>${esc(names[engine])}</b><span>${esc(info.statusLabel)}</span>${info.configVersion ? `<code>v${info.configVersion}</code>` : ""}</span>`;
  }).join("")}</div>`;
}

export function sharedEngineChoices(selected = [], supported = []) {
  return `<fieldset class="shared-engine-options"><legend>内核</legend><div>${engines
    .filter(engine => supported.includes(engine) || selected.includes(engine))
    .map(engine => `<label><input type="checkbox" name="engines" value="${engine}" ${selected.includes(engine) ? "checked" : ""}><span>${names[engine]}${supported.includes(engine) ? "" : " · 不可用"}</span></label>`)
    .join("") || '<span class="settings-hint">Agent 尚未声明内核</span>'}</div></fieldset>`;
}

export function engineCapabilityToggles(selected, { supported = engines, writable = true, node = false, transitions = {} } = {}) {
  return `<div class="core-capability-list" role="group" aria-label="${node ? "节点内核开关" : "新 Agent 默认内核"}">${engines.map((engine) => {
    const info = describeEngineCapability(engine, {
      selected,
      supported,
      writable,
      node,
      transitions,
      engineLabel: names[engine],
    });
    const hintID = `${node ? "node" : "default"}-capability-${engine}-hint`;
    const titleAttr = info.disabledReason ? ` title="${info.disabledReason}" data-disabled-reason="${info.disabledReason}"` : "";
    return `<label class="core-capability-row ${info.pending ? "is-pending" : info.failed ? "is-failed" : ""}" data-capability-engine="${engine}" data-capability-state="${info.state}"${titleAttr}><span class="core-capability-copy"><b>${names[engine]}</b>${info.hint ? `<small id="${hintID}">${info.hint}</small>` : ""}</span><span class="core-capability-state" aria-hidden="true">${info.statusText || `<span class="when-enabled">${info.enabledLabel}</span><span class="when-disabled">${info.disabledLabel}</span>`}</span><input class="core-capability-switch" type="checkbox" role="switch" aria-label="${names[engine]}" ${info.hint ? `aria-describedby="${hintID}"` : ""}${titleAttr} name="default_agent_engines" value="${engine}" ${node ? `data-engine-capability="${engine}"` : ""} ${info.checked ? "checked" : ""} ${info.disabled ? "disabled" : ""}></label>`;
  }).join("")}</div>`;
}

export function selectedDefaultEngines(data) {
  return data.getAll("default_agent_engines");
}
