export function mieruProtocolOptions(plan) {
  const transport = plan.mieru_transport || "TCP";
  return `<div class="preset-protocol-options" data-mieru-options><details class="preset-option-panel" open><summary><b>Mieru 传输</b></summary><div class="plan-fields one"><label>传输协议<select name="mieru_transport"><option value="TCP" ${transport === "TCP" ? "selected" : ""}>TCP</option><option value="UDP" ${transport === "UDP" ? "selected" : ""}>UDP</option></select><small>客户端须使用相同传输协议；两种方式均可代理 TCP 与 UDP 流量。</small></label></div></details></div>`;
}
