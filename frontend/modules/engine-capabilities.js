const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const names = { mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "Shadowsocks Rust" };

export function sharedEngineNames(selected = []) {
  return engines.filter(engine => selected.includes(engine)).map(engine => names[engine]).join(" / ") || "未分配";
}

export function sharedEngineChoices(selected = [], supported = []) {
  return `<fieldset class="shared-engine-options"><legend>内核</legend><div>${engines
    .filter(engine => supported.includes(engine) || selected.includes(engine))
    .map(engine => `<label><input type="checkbox" name="engines" value="${engine}" ${selected.includes(engine) ? "checked" : ""}><span>${names[engine]}${supported.includes(engine) ? "" : " · 不可用"}</span></label>`)
    .join("") || '<span class="settings-hint">Agent 尚未声明内核</span>'}</div></fieldset>`;
}

export function engineCapabilityToggles(selected, { supported = engines, writable = true, node = false, transitions = {} } = {}) {
  return `<div class="core-capability-list" role="group" aria-label="${node ? "节点内核能力" : "新 Agent 默认内核能力"}">${engines.map((engine) => {
    const available = supported.includes(engine);
    const checked = selected.includes(engine);
    const transition = transitions[engine];
    const pending = ["pending", "running"].includes(transition?.status);
    const failed = ["failed", "canceled"].includes(transition?.status);
    const hint = pending
      ? `${transition.enabled ? "等待启动服务并开启能力" : "等待停止服务并关闭能力"}；离线节点上线后执行`
      : failed
        ? "上次启停失败或取消，能力未变更；可重试开关，详情见任务页"
        : !available ? "Agent 未声明支持，请调整 Agent 安装配置后重新注册"
        : "";
    const hintID = `${node ? "node" : "default"}-capability-${engine}-hint`;
    const status = pending ? (transition.enabled ? "等待启动" : "等待停止") : failed ? "未变更" : !available ? "不支持" : "";
    return `<label class="core-capability-row ${pending ? "is-pending" : failed ? "is-failed" : ""}"><span class="core-capability-copy"><b>${names[engine]}</b>${hint ? `<small id="${hintID}">${hint}</small>` : ""}</span><span class="core-capability-state" aria-hidden="true">${status || `<span class="when-enabled">${node ? "已开启" : "默认开启"}</span><span class="when-disabled">${node ? "已关闭" : "默认关闭"}</span>`}</span><input class="core-capability-switch" type="checkbox" role="switch" aria-label="${names[engine]}" ${hint ? `aria-describedby="${hintID}"` : ""} name="default_agent_engines" value="${engine}" ${node ? `data-engine-capability="${engine}"` : ""} ${checked ? "checked" : ""} ${pending || !writable || (!available && !checked) ? "disabled" : ""}></label>`;
  }).join("")}</div>`;
}

export function selectedDefaultEngines(data) {
  return data.getAll("default_agent_engines");
}
