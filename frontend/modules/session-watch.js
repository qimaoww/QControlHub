import { createPoller } from "./refresh.js";

// Reuse the page polling lifecycle to revalidate idle views as well as actions.
// Sharing usage counters are deliberately excluded from the grant identity.
const accessKey = (access) => JSON.stringify([
  access?.revision,
  (access?.shares || []).map(share => [share.id, share.agent_id, share.enabled,
    share.status, share.invitation_revision, share.engines, share.ports, share.limit_bytes]),
]);

export function createSessionWatch({ api, state, onAccessChanged, setTimer, clearTimer }) {
  const poller = createPoller({
    isActive: () => Boolean(state.session) && !document.hidden,
    delay: () => 10_000,
    setTimer, clearTimer,
    async run() {
      const session = state.session, data = state.data;
      try {
        await api("/auth/session");
        if (state.session !== session || state.data !== data || session.role === "admin") return;
        const access = await api("/agent-access");
        if (state.session !== session || state.data !== data) return;
        const changed = data.agentAccess && accessKey(access) !== accessKey(data.agentAccess);
        data.agentAccess = access;
        if (changed) await onAccessChanged();
      } catch {
        // The transport clears expired sessions. Transient failures retry on
        // the next tick; they must not replace a working editor with an error.
      }
    },
  });
  return poller;
}
