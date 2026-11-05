import { accountStorage, storageAccount } from "./account-storage.js";

// Store only task addresses, never configuration or credentials. The task ID
// is allocated before sending so an interrupted response can be read back
// without guessing which concurrent revision belongs to this submission.
export function createConfigMutations({ api, state }, { onTask, waitForTerminal }) {
  const storageKey = "qch.config-submissions";
  const pending = new WeakMap();
  const entries = () => {
    let records = pending.get(state.data);
    if (!records) {
      records = {};
      if (storageAccount().stable) {
        try { records = JSON.parse(accountStorage.getItem(storageKey) || "{}"); } catch { /* Storage may be unavailable. */ }
      }
      pending.set(state.data, records);
    }
    return records;
  };
  const persist = records => {
    if (storageAccount().stable) {
      try { accountStorage.setItem(storageKey, JSON.stringify(records)); } catch { /* Tracking in memory still works. */ }
    }
  };
  function track(result, record, owner, records, id) {
    if (state.data !== owner || !result.task?.id) return;
    if (["succeeded", "failed", "canceled"].includes(result.task.status)) {
      delete records[id];
      persist(records);
    }
    onTask(result, record);
    void waitForTerminal(result.task.id).then(() => {
      if (state.data !== owner || records[id] !== record) return;
      delete records[id];
      persist(records);
    }).catch(() => { /* Retain the address for the next page visit. */ });
  }
  async function readReceipt(id, record) {
    const task = await api(`/tasks/${encodeURIComponent(id)}?view=status`, {method:"GET"});
    if (task.id !== id || task.agent_id !== record.agentId || task.engine !== record.engine ||
        task.action !== record.intent || task.config_version !== record.version || !task.config_id)
      throw new Error("保存任务与当前目标不一致，请查看执行记录。");
    const config = await api(`/agents/${encodeURIComponent(record.agentId)}/configs/${encodeURIComponent(record.engine)}`, {method:"GET"});
    if (config.id !== task.config_id || config.version < task.config_version)
      throw new Error("保存配置尚未确认，请查看执行记录。");
    return {config, task};
  }
  async function recover(agentId, engine) {
    const owner = state.data, records = entries();
    for (const [id, record] of Object.entries(records)) {
      if (!record || record.agentId !== agentId || record.engine !== engine) continue;
      try {
        const result = await readReceipt(id, record);
        if (state.data !== owner) return;
        track(result, record, owner, records, id);
      } catch { /* Unknown is not failure. Keep the address; never repeat a write. */ }
    }
  }
  async function request(path, options = {}) {
    const match = path.match(/^\/agents\/([^/]+)\/configs\/([^/]+)\/(?:source|server-inbounds|fields\/[^?]+)(?:\?.*)?$/);
    if (options.method !== "POST" || !match) return api(path, options);
    const owner = state.data, records = entries();
    const input = JSON.parse(options.body);
    const record = {agentId:decodeURIComponent(match[1]), engine:decodeURIComponent(match[2]),
      intent:input.intent, version:Number(input.expected_version ?? input.version) + 1};
    const bytes = globalThis.crypto.getRandomValues(new Uint8Array(8));
    const id = `tsk_${[...bytes].map(value => value.toString(16).padStart(2,"0")).join("")}`;
    records[id] = record;
    persist(records);
    let result;
    try {
      result = await api(path, {...options, headers:{...options.headers, "X-Config-Task-ID":id}});
    } catch (error) {
      if (state.data !== owner) throw error;
      if (error.status && error.status < 500 && error.status !== 408) {
        delete records[id];
        persist(records);
        throw error;
      }
      // A bounded readback handles a lost response, including a transaction
      // that commits shortly after the original connection closes.
      for (let attempt = 0; attempt < 3; attempt++) {
        if (state.data !== owner) throw error;
        try { result = await readReceipt(id, record); break; }
        catch { if (attempt < 2) await new Promise(resolve => setTimeout(resolve, 250)); }
      }
      if (!result) throw error;
    }
    track(result, record, owner, records, id);
    return result;
  }
  return { request, recover };
}
