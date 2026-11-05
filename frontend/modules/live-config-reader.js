import { requestConfigSnapshot } from "./config-snapshot.js";
import { liveSourceKey } from "./live-config-state.js";

// Snapshot reads and invalidation share one monotonic request counter. A
// superseded read cannot replace the current account's editor baseline.
export function createLiveConfigReader({ api, state }, { onRead }) {
  let liveReadRequest = 0;
function markStaleReadTask(agentId, engine) {
  const key = `${agentId}|${engine}`;
  const entry = state.data.liveSources?.[key];
  const staleId = entry?.pendingTaskId || entry?.taskId;
  if (staleId) {
    state.data.staleReadTasks ||= {};
    state.data.staleReadTasks[key] = staleId;
  }
}

function invalidateLiveSnapshot(agentId, engine) {
  const key = liveSourceKey(agentId, engine);
  markStaleReadTask(agentId, engine);
  liveReadRequest += 1;
  if (state.data.liveSources?.[key]) delete state.data.liveSources[key];
}


const requestCurrentConfigSnapshot = (agent, engine, readAction, options) =>
  requestConfigSnapshot({ api, waitForTask }, agent, engine, readAction, options);

async function readCurrentConfig(agent, engine, sourceKey, readAction, preferCached = false) {
  if (state.data.liveSources?.[sourceKey]?.reading) return;
  const request = ++liveReadRequest;
  const data = state.data;
  const epoch = state.navigationEpoch, sourceMode = data.liveConfigSource;
  const reading = { reading: true };
  const isCurrent = () =>
    data === state.data &&
    epoch === state.navigationEpoch &&
    request === liveReadRequest &&
    state.route === "live-config" &&
    state.data.liveAgent === agent.id &&
    state.data.liveEngine === engine &&
    state.data.liveConfigSource === sourceMode;
  const discardReading = () => {
    if (data.liveSources?.[sourceKey] === reading)
      delete data.liveSources[sourceKey];
  };
  state.data.staleReadTasks ||= {};
  const staleTaskId = state.data.staleReadTasks[sourceKey];
  if (staleTaskId) {
    delete state.data.staleReadTasks[sourceKey];
    try {
      for (let attempt = 0; attempt < 600; attempt += 1) {
        if (!isCurrent()) return;
        const staleTask = await api(`/tasks/${encodeURIComponent(staleTaskId)}`);
        if (!isCurrent()) return;
        if (["succeeded", "failed", "canceled"].includes(staleTask.status)) break;
        await new Promise((resolve) => setTimeout(resolve, 600));
      }
    } catch { /* best effort drain */ }
    if (!isCurrent()) return;
  }
  state.data.liveSources ||= {};
  state.data.liveSources[sourceKey] = reading;
  try {
    const snapshot = await requestCurrentConfigSnapshot(agent, engine, readAction, {
      preferCached,
      isCurrent,
      onTask: taskId => {
        if (isCurrent() && data.liveSources?.[sourceKey] === reading)
          reading.pendingTaskId = taskId;
      },
    });
    if (!snapshot || !isCurrent()) return discardReading();
    state.data.liveSources[sourceKey] = {
      content: snapshot.content,
      // Keep the Agent bytes separate from later saved-but-not-deployed edits.
      // Display provenance survives later saved-but-not-deployed edits.
      agentContent: snapshot.content,
      taskId: snapshot.taskId,
      readAt: snapshot.readAt,
      reading: false,
      cached: snapshot.cached,
    };
  } catch (error) {
    if (error?.name === "AbortError" || !isCurrent())
      return discardReading();
    state.data.liveSources[sourceKey] = {
      error: error.message,
      reading: false,
    };
  }
  if (
    state.route === "live-config" &&
    state.data.liveAgent === agent.id &&
    state.data.liveEngine === engine
  )
    await onRead();
}

async function waitForTask(taskID, isCurrent = () => true) {
  for (let attempt = 0; attempt < 200; attempt += 1) {
    if (!isCurrent()) return null;
    const task = await api(`/tasks/${encodeURIComponent(taskID)}`);
    if (!isCurrent()) return null;
    if (["succeeded", "failed", "canceled"].includes(task.status)) return task;
    await new Promise((resolve) => setTimeout(resolve, attempt < 5 ? 200 : 600));
  }
  throw new Error("等待节点返回配置超时");
}


  return { requestCurrentConfigSnapshot, readCurrentConfig, invalidateLiveSnapshot };
}
