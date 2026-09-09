const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const names = { mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "Shadowsocks Rust" };

export function engineCapabilityToggles(selected, { supported = engines, writable = true, node = false } = {}) {
  return `<div class="settings-toggle-list">${engines.map((engine) => {
    const available = supported.includes(engine);
    const checked = selected.includes(engine);
    return `<label class="settings-toggle"><span><b>${names[engine]}</b><small>${!available ? "Agent 未声明支持，请调整 Agent 安装配置后重新注册" : node ? "仅影响此节点的内核管理能力" : "新 Agent 默认能力；节点可单独覆盖"}</small></span><input type="checkbox" name="default_agent_engines" value="${engine}" ${node ? `data-engine-capability="${engine}"` : ""} ${checked ? "checked" : ""} ${!writable || (!available && !checked) ? "disabled" : ""}></label>`;
  }).join("")}</div>`;
}

export function selectedDefaultEngines(data) {
  return data.getAll("default_agent_engines");
}
