import { listenerAdvancedOptions } from "./server-plan-form.js";

export function mieruProtocolOptions(plan) {
  const transport = plan.mieru_transport || "TCP";
  const trafficPattern = plan.mieru_traffic_pattern || "off";
  const selected = (preset) => trafficPattern === preset ? "selected" : "";
  return `<div class="preset-protocol-options" data-mieru-options>
    <details class="preset-option-panel" open><summary><b>Mieru 传输</b></summary><div class="plan-fields one"><label>传输协议<select name="mieru_transport"><option value="TCP" ${transport === "TCP" ? "selected" : ""}>TCP</option><option value="UDP" ${transport === "UDP" ? "selected" : ""}>UDP</option></select><small>客户端须使用相同传输协议；两种方式均可代理 TCP 与 UDP 流量。</small></label></div></details>
    <details class="preset-option-panel" open><summary><b>流量整形</b></summary><div class="plan-fields one"><label>预设<select name="mieru_traffic_pattern"><option value="off" ${selected("off")}>关闭</option><option value="light" ${selected("light")}>轻度 · 优先性能</option><option value="balanced" ${selected("balanced")}>均衡 · 建议先试</option><option value="aggressive" ${selected("aggressive")}>激进 · 更高流量开销</option></select><small>关闭时不写入 traffic-pattern，使用 Mieru 默认行为。轻度优先性能；均衡采用低熵整形；激进有较高流量开销。TCP 分片仅在 TCP 传输下生效。导出的客户端配置使用同一预设。</small></label></div></details>
    ${listenerAdvancedOptions(plan)}</div>`;
}
