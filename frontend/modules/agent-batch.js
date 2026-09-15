// Eligibility and selection state shared by batch-operation controls.

export function batchAgentEligibility(agent, action, engine) {
  if (agent?.can_manage === false)
    return { eligible: false, reason: "共享节点的主机操作仅限所有者" };
  if (!agent || agent.status !== "online")
    return { eligible: false, reason: "节点离线，不能执行当前动作" };
  if (action === "upgrade-agent") {
    if (!(agent.features || []).includes("agent-self-upgrade-v1"))
      return {
        eligible: false,
        reason: "旧版 Agent 缺少远程升级能力，请先重新安装或单独升级",
      };
    return { eligible: true, reason: "在线 · 支持远程升级" };
  }
  if (!agent.runtime?.[engine]?.installed)
    return {
      eligible: false,
      reason: `未安装 ${engine || "所选内核"}，不能执行当前动作`,
    };
  return { eligible: true, reason: "在线 · 已安装所选内核" };
}

export function batchSelectAllState(inputs) {
  const eligible = [...inputs].filter((input) =>
    input.dataset?.batchEligible === undefined
      ? !input.disabled
      : input.dataset.batchEligible === "1",
  );
  const selected = eligible.filter((input) => input.checked);
  return {
    eligible: eligible.length,
    selected: selected.length,
    checked: eligible.length > 0 && selected.length === eligible.length,
    indeterminate: selected.length > 0 && selected.length < eligible.length,
  };
}
