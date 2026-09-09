const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const names = { mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "Shadowsocks Rust" };

export function engineCapabilityToggles(selected, { supported = engines, writable = true, node = false, transitions = {} } = {}) {
  return `<div class="settings-toggle-list">${engines.map((engine) => {
    const available = supported.includes(engine);
    const checked = selected.includes(engine);
    const transition = transitions[engine];
    const pending = ["pending", "running"].includes(transition?.status);
    const hint = pending
      ? `${transition.enabled ? "等待启动服务并开启能力" : "等待停止服务并关闭能力"}；离线节点上线后执行`
      : ["failed", "canceled"].includes(transition?.status)
        ? "上次启停失败或取消，能力未变更；可重试开关，详情见任务页"
        : !available ? "Agent 未声明支持，请调整 Agent 安装配置后重新注册"
        : node ? "关闭停止服务；开启恢复管理并启动已安装内核" : "新 Agent 默认能力；节点可单独覆盖";
    return `<label class="settings-toggle"><span><b>${names[engine]}</b><small>${hint}</small></span><input type="checkbox" name="default_agent_engines" value="${engine}" ${node ? `data-engine-capability="${engine}"` : ""} ${checked ? "checked" : ""} ${pending || !writable || (!available && !checked) ? "disabled" : ""}></label>`;
  }).join("")}</div>`;
}

export function selectedDefaultEngines(data) {
  return data.getAll("default_agent_engines");
}
