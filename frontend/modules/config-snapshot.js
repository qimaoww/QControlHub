export async function requestConfigSnapshot(
  { api, waitForTask }, agent,
  engine,
  readAction,
  { preferCached = false, isCurrent = () => true, onTask = () => {} } = {},
) {
  if (!isCurrent()) return null;
  const createReadTask = (allowCached) => api("/tasks", {
    method: "POST",
    body: JSON.stringify({
      agent_id: agent.id,
      engine,
      action: readAction,
      ...(allowCached ? { prefer_cached: true } : {}),
    }),
  });
  const finishReadTask = async (task) => {
    if (!isCurrent()) return null;
    onTask(task.id);
    const finished = ["succeeded", "failed", "canceled"].includes(task.status)
      ? task
      : await waitForTask(task.id, isCurrent);
    if (!finished || !isCurrent()) return null;
    if (finished.status !== "succeeded")
      throw new Error(finished.error || "节点未能读取当前配置");
    return finished;
  };
  let task = await createReadTask(preferCached);
  let cacheHit = Boolean(preferCached && task.reused && task.status === "succeeded");
  let finished = await finishReadTask(task);
  if (!finished || !isCurrent()) return null;
  let snapshot;
  try {
    snapshot = await api(`/tasks/${encodeURIComponent(finished.id)}/config-snapshot`);
  } catch (error) {
    // Another tab or a mutation can retire any read snapshot before its GET,
    // including a fresh preflight result. Retry once without the cache, but
    // never create work for an abandoned page or a different account.
    if (!isCurrent()) return null;
    if (error?.status !== 404) throw error;
    task = await createReadTask(false);
    cacheHit = false;
    finished = await finishReadTask(task);
    if (!finished || !isCurrent()) return null;
    snapshot = await api(`/tasks/${encodeURIComponent(finished.id)}/config-snapshot`);
  }
  if (!isCurrent()) return null;
  if (!snapshot.content)
    throw new Error("节点返回的配置快照已失效，请重新读取");
  return {
    content: snapshot.content,
    taskId: finished.id,
    cached: cacheHit,
    readAt: Number.isFinite(Date.parse(finished.finished_at || ""))
      ? Date.parse(finished.finished_at)
      : Date.now(),
  };
}
