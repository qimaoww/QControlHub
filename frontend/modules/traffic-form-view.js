import { dateInputValue, quotaInputValue } from "./traffic-model.js";
export function createTrafficFormView({ esc, engineName }) {
  const engineOptions = (agent, selected = "") =>
    (agent?.capabilities || [])
      .map((engine) => `<option value="${esc(engine)}" ${engine === selected ? "selected" : ""}>${esc(engineName(engine))}</option>`)
      .join("");
  const agentOption = (agent, selected) =>
    `<option value="${esc(agent.id)}" ${agent.id === selected ? "selected" : ""}>${esc(agent.name)} · ${agent.status === "online" ? "在线" : "离线"}</option>`;
  const policyFields = (policy, agents, prefix) => {
    const selectedAgent = agents.find((agent) => agent.id === policy.agent_id) || agents[0];
    const editing = Boolean(policy.id);
    const agentField = editing
      ? `<label>节点<select disabled>${selectedAgent ? agentOption(selectedAgent, selectedAgent.id) : `<option>${esc(policy.agent_id)}</option>`}</select><input type="hidden" name="agent_id" value="${esc(policy.agent_id)}"></label>`
      : `<label>节点<select name="agent_id" data-traffic-agent-select="${esc(prefix)}" required>${agents.map((agent) => agentOption(agent, selectedAgent?.id)).join("")}</select></label>`;
    return `<div class="traffic-form-grid">
      ${agentField}
      <label>内核<select name="engine" data-traffic-engine-select="${esc(prefix)}" required>${engineOptions(selectedAgent, policy.engine)}</select></label>
      <label>名称<input name="name" value="${esc(policy.name || "")}" required maxlength="100" placeholder="例如 Reality 入口"></label>
      <label>端口<input name="port" type="number" value="${esc(policy.port || "")}" min="1" max="65535" required placeholder="443"></label>
      <label>协议<select name="protocol"><option value="both" ${policy.protocol === "both" || !policy.protocol ? "selected" : ""}>TCP + UDP</option><option value="tcp" ${policy.protocol === "tcp" ? "selected" : ""}>TCP</option><option value="udp" ${policy.protocol === "udp" ? "selected" : ""}>UDP</option></select></label>
      <label>统计周期<select name="cycle"><option value="monthly" ${policy.cycle === "monthly" || !policy.cycle ? "selected" : ""}>每月</option><option value="yearly" ${policy.cycle === "yearly" ? "selected" : ""}>每年</option></select></label>
      <label>周期起始日期<input name="cycle_anchor" type="date" value="${dateInputValue(policy.cycle_anchor)}" max="${dateInputValue()}" required></label>
      <label>周期额度（GiB）<input name="limit_gb" type="number" value="${quotaInputValue(policy.quota_enabled === false ? 0 : policy.limit_bytes)}" min="0" max="8388607" step="0.000001" placeholder="留空或填 0：仅监控"></label>
    </div><label class="traffic-auto-block"><input type="checkbox" name="auto_block" value="1" ${policy.auto_block !== false ? "checked" : ""}><span><b>超额自动封禁</b><small>关闭后仍统计流量，但不会阻断端口</small></span></label>`;
  };
  const selectOptions = (items, value, label) =>
    `<option value="">${label}</option>${items.map((item) => `<option value="${esc(item.value)}" ${item.value === value ? "selected" : ""}>${esc(item.label)}</option>`).join("")}`;

  return { engineOptions, policyFields, selectOptions };
}
