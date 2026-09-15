import { createTaskMonitor } from "./task-monitor.js";
import { diagnosticError } from "./errors.js";
import { liveSourceKey } from "./live-config-state.js";

export function createConfigDeployment({ api, state, can, notify }, { invalidateLiveSnapshot, maybeRerenderLiveConfig }) {
const waitForDeployTerminal = createTaskMonitor({
  // Explicit GET bypasses render-scoped read caching for live task state.
  read: id => api(`/tasks/${encodeURIComponent(id)}?view=status`, {method:"GET"}),
  session: () => state.data,
});


function recordPendingDeploy(taskID, agentId, engine) {
  const key = liveSourceKey(agentId, engine);
  state.data.pendingDeployTasks ||= {};
  state.data.pendingDeployTasks[key] = { taskId: taskID };
}

// CAS clear: only remove if the tracked taskId still matches.
function clearPendingDeploy(agentId, engine, expectedTaskID) {
  const key = liveSourceKey(agentId, engine);
  const entry = state.data.pendingDeployTasks?.[key];
  if (entry && (!expectedTaskID || entry.taskId === expectedTaskID))
    delete state.data.pendingDeployTasks[key];
  return entry?.taskId === expectedTaskID;
}

function handleDeployTerminal(result, taskID, agentId, engine) {
  if (result.status === "succeeded") {
    // Every successful deploy changes the node file — always invalidate,
    // even if this task is no longer the latest pending record. This is
    // idempotent (deleting a non-existent key is a no-op).
    invalidateLiveSnapshot(agentId, engine);
    maybeRerenderLiveConfig(agentId, engine);
    // Clean up pending only if this is still the tracked task.
    clearPendingDeploy(agentId, engine, taskID);
  } else {
    // Failed/canceled: notify and clear only if still current pending.
    if (clearPendingDeploy(agentId, engine, taskID)) {
      notify(
        diagnosticError(result.error) ||
          `部署${result.status === "canceled" ? "已取消" : "失败"}`,
        "error",
      );
      maybeRerenderLiveConfig(agentId, engine);
    }
    // Old failed/canceled tasks do NOT clear newer pending records.
  }
}

// Track active reconcilers: Map<key, Set<taskID>> so multiple tasks
// for the same key can each have their own poller.
const activeReconcilers = new WeakMap();

// Fire-and-forget monitor for a newly submitted deploy task.
function monitorDeployTask(taskID, agentId, engine) {
  if (!can("tasks.read")) return;
  const sessionData = state.data;
  let watchers = activeReconcilers.get(sessionData);
  if (!watchers) activeReconcilers.set(sessionData, watchers = new Map());
  if (watchers.has(taskID)) return watchers.get(taskID);
  const monitoring = (async () => {
    try {
      const result = await waitForDeployTerminal(taskID);
      if (state.data !== sessionData) return null;
      handleDeployTerminal(result, taskID, agentId, engine);
      return result;
    } catch {
      return null;
    } finally { watchers.delete(taskID); }
  })();
  watchers.set(taskID, monitoring);
  return monitoring;
}

// Single-instance recovery poller per key. Survives while the page is open;
// cleans up on abort so a future visit can restart it.
function startRecoveryPoller(taskID, agentId, engine) {
  void monitorDeployTask(taskID, agentId, engine);
}

// Called when entering live-config to resolve any pending deploy that
// outlived navigation. Uses the current (route-scoped) api context.
async function reconcilePendingDeploy(agentId, engine) {
  if (!can("tasks.read")) return;
  const key = liveSourceKey(agentId, engine);
  const pending = state.data.pendingDeployTasks?.[key];
  if (!pending?.taskId) return;
  const sessionData = state.data;
  if (activeReconcilers.get(sessionData)?.has(pending.taskId)) return;
  try {
    const task = await api(`/tasks/${encodeURIComponent(pending.taskId)}`);
    if (state.data !== sessionData) return;
    if (["succeeded", "failed", "canceled"].includes(task.status))
      handleDeployTerminal(task, pending.taskId, agentId, engine);
    else startRecoveryPoller(pending.taskId, agentId, engine);
  } catch {
    // Network error or abort — retry on next visit; keep pending entry.
  }
}


  return { waitForDeployTerminal, recordPendingDeploy, monitorDeployTask, reconcilePendingDeploy };
}
