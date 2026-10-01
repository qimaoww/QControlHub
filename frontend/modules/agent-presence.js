function presenceState(status) {
  return status === "online" || status === "offline" ? status : "unknown";
}

function presenceLabel(state, nodeLabel) {
  const label = state === "online" ? "在线" : state === "offline" ? "离线" : "未知";
  return nodeLabel ? `节点${label}` : label;
}

// Connection state has its own presentation, independent of task/service health.
export function agentPresenceMarkup(status, { nodeLabel = false, tracked = false } = {}) {
  const state = presenceState(status);
  return `<span class="agent-presence" data-agent-presence="${state}"${nodeLabel ? ' data-presence-node-label' : ""}><i class="agent-presence-dot" aria-hidden="true"${tracked ? " data-agent-status-dot" : ""}></i><b${tracked ? " data-agent-status-label" : ""}>${presenceLabel(state, nodeLabel)}</b></span>`;
}

export function updateAgentPresence(root, status) {
  const state = presenceState(status);
  root.querySelectorAll("[data-agent-presence]").forEach((badge) => {
    if (badge.dataset.agentPresence !== state) badge.dataset.agentPresence = state;
    const label = badge.querySelector("b");
    const text = presenceLabel(state, badge.hasAttribute("data-presence-node-label"));
    if (label.textContent !== text) label.textContent = text;
  });
}
