export const taskTerminal = task => ["succeeded", "failed", "canceled"].includes(task?.status);

// One poller per task and login session, shared by preset feedback and live
// configuration recovery. Temporary navigation/network failures are retried;
// permissions, deleted tasks and logout stop polling.
export function createTaskMonitor({read, session, sleep = ms => new Promise(resolve => setTimeout(resolve, ms)),
  hidden = () => globalThis.document?.hidden, attempts = 240}) {
  const sessions = new WeakMap();
  return (id, onProgress) => {
    const owner = session();
    let tasks = sessions.get(owner);
    if (!tasks) sessions.set(owner, tasks = new Map());
    let entry = tasks.get(id);
    if (entry) {
      if (onProgress) entry.listeners.add(onProgress);
      return entry.promise;
    }
    entry = {listeners:new Set(onProgress ? [onProgress] : [])};
    tasks.set(id, entry);
    const current = () => session() === owner;
    const progress = (task, error) => {
      for (const listener of entry.listeners) {
        try { listener(task, error); } catch { /* UI callbacks cannot stop polling. */ }
      }
    };
    entry.promise = (async () => {
      let failures = 0;
      for (let attempt = 0; attempt < attempts; attempt++) {
        if (!current()) throw new DOMException("Aborted", "AbortError");
        try {
          const task = await read(id);
          if (!current()) throw new DOMException("Aborted", "AbortError");
          failures = 0;
          if (taskTerminal(task)) return task;
          progress(task);
        } catch (error) {
          if (!current() || [401, 403, 404].includes(error.status)) throw error;
          progress(null, error);
          failures++;
        }
        const interval = hidden() ? 5000 : failures ? Math.min(5000, 500 * 2 ** Math.min(failures, 4)) : attempt < 8 ? 500 : attempt < 30 ? 1000 : 2000;
        await sleep(interval);
      }
      throw new Error("等待任务结果超时，请查看执行记录");
    })().finally(() => {
      tasks.delete(id);
      entry.listeners.clear();
    });
    return entry.promise;
  };
}
