import { updateFeedback } from "./presence-motion.js";
import { connectionStateLabel, normalizeConnectionState } from "./status-controls.js";

function presenceState(status) {
  return normalizeConnectionState(status);
}

function presenceLabel(state, nodeLabel) {
  return connectionStateLabel(state, { nodeLabel });
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
    if (label.textContent !== text) updateFeedback(label, text);
  });
}
