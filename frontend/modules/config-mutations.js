import { accountStorage, storageAccount } from "./account-storage.js";

// Store only task addresses, never configuration or credentials. The task ID
// is allocated before sending so an interrupted response can be read back
// without guessing which concurrent revision belongs to this submission.
export function createConfigMutations({ api, state }, { onTask, waitForTerminal, recoverPortConflict }) {
  const storageKey = "qch.config-submissions";
  const pending = new WeakMap();
  const entries = () => {
    let records = pending.get(state.data);
    if (!records) {
      records = {};
      if (storageAccount().stable) {
        try { records = JSON.parse(accountStorage.getItem(storageKey) || "{}"); } catch { /* Storage may be unavailable. */ }
      }
      if (!records || typeof records !== "object" || Array.isArray(records)) records = {};
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
  function finishRequest(result, record, owner, records, id) {
    if (state.data !== owner) throw new DOMException("Aborted", "AbortError");
    track(result, record, owner, records, id);
    if (result.config?.version > result.task?.config_version) {
      throw Object.assign(new Error("配置已保存且任务已提交，之后又有新的配置保存，请重新读取后继续。"), {
        status:409, code:"config_submission_superseded",
      });
    }
    return result;
  }
  async function readReceipt(id, record) {
    const task = await api(`/tasks/${encodeURIComponent(id)}?view=status`, {method:"GET"});
    if ((task.id !== id && task.submission_id !== id) || task.agent_id !== record.agentId || task.engine !== record.engine ||
        task.action !== record.intent || (task.config_version !== record.version && !(record.unchangedBinding && task.reused && task.config_version === record.version - 1)) || !task.config_id)
      throw new Error("保存任务与当前目标不一致，请查看执行记录。");
    if (record.instanceId !== undefined && ((task.shared_traffic_id || "") !== record.instanceId ||
        Boolean(task.shared_instance) !== Boolean(record.instanceId)))
      throw new Error("保存任务与当前实例不一致，请查看执行记录。");
    const config = await api(`/agents/${encodeURIComponent(record.agentId)}/configs/${encodeURIComponent(record.engine)}`, {method:"GET"});
    if (config.id !== task.config_id || config.version < task.config_version)
      throw new Error("保存配置尚未确认，请查看执行记录。");
    return {config, task};
  }
  async function recover(agentId, engine, instanceId) {
    const owner = state.data, records = entries();
    for (const [id, record] of Object.entries(records)) {
      if (!record || record.agentId !== agentId || record.engine !== engine) continue;
      if (instanceId !== undefined && record.instanceId !== undefined && record.instanceId !== instanceId) continue;
      try {
        const result = await readReceipt(id, {...record, instanceId:instanceId ?? record.instanceId});
        if (state.data !== owner) return;
        track(result, record, owner, records, id);
      } catch { /* Unknown is not failure. Keep the address; never repeat a write. */ }
    }
  }
  async function request(path, options = {}) {
    const match = path.match(/^\/agents\/([^/]+)\/configs\/([^/]+)\/(?:source|server-inbounds|outbounds|management|fields\/[^?]+)(?:\?.*)?$/);
    if (!["POST", "PUT"].includes(options.method) || !match) return api(path, options);
    const owner = state.data, records = entries();
    const input = JSON.parse(options.body);
    const record = {agentId:decodeURIComponent(match[1]), engine:decodeURIComponent(match[2]),
      intent:input.intent, unchangedBinding:input.operation === "bind", version:Number(input.expected_version ?? input.version) + 1, instanceId:input.expected_instance_id};
    const operation = {...input};
    delete operation.version; delete operation.expected_version; delete operation.install_if_missing;
    if (globalThis.crypto.subtle) {
      const digest = await globalThis.crypto.subtle.digest("SHA-256", new TextEncoder().encode(JSON.stringify(operation)));
      record.fingerprint = [...new Uint8Array(digest)].map(value=>value.toString(16).padStart(2,"0")).join("");
    }
    if (state.data !== owner) throw new DOMException("目标已变化", "AbortError");
    // A refresh or an interrupted response must not allocate another operation
    // for the same instance while the durable receipt is still unresolved.
    let reusableID;
    for (const [existingID, existing] of Object.entries(records)) {
      if (!existing || existing.agentId !== record.agentId || existing.engine !== record.engine ||
          (existing.instanceId || "") !== (record.instanceId || "") ||
          existing.fingerprint && existing.fingerprint !== record.fingerprint) continue;
      let receipt;
      try { receipt = await readReceipt(existingID, existing); }
      catch (error) {
        // A request that never reached the server can reuse its receipt and
        // original CAS. A late first commit then rejects/rolls back this retry.
        if (error.status === 404 && existing.fingerprint && existing.fingerprint === record.fingerprint && existing.version === record.version) {
          reusableID = existingID;
          break;
        }
        throw Object.assign(new Error("上次提交结果尚未确认，草稿已保留，请在任务页核对后重试。"), {deployPreflight:true});
      }
      if (state.data !== owner) throw new DOMException("目标已变化", "AbortError");
      if (!["succeeded", "failed", "canceled"].includes(receipt.task.status))
        throw Object.assign(new Error("当前实例已有配置任务正在执行，草稿已保留，请等待结果后重试。"), {deployPreflight:true});
      delete records[existingID]; persist(records);
    }
    const bytes = globalThis.crypto.getRandomValues(new Uint8Array(8));
    const id = reusableID || `tsk_${[...bytes].map(value => value.toString(16).padStart(2,"0")).join("")}`;
    records[id] = record;
    persist(records);
    let result;
    try {
      const submit = () => api(path, {...options, headers:{...options.headers, "X-Config-Task-ID":id}});
      try { result = await submit(); }
      catch (error) {
        // A structured 409 guarantees that the atomic mutation was rejected.
        // Refresh only the authorized instance, then retry the same bytes and
        // receipt once. Lost/ambiguous responses always take the readback path.
        if (error.status !== 409 || error.code !== "port_conflict" || !error.recoveryEngine || !recoverPortConflict)
          throw error;
        let recovered;
        try { recovered = await recoverPortConflict(error); }
        catch (recoveryError) {
          if (recoveryError.name === "AbortError") throw recoveryError;
          error.message = recoveryError.message;
          throw error;
        }
        if (!recovered) throw error;
        if (state.data !== owner) throw new DOMException("Aborted", "AbortError");
        result = await submit();
      }
    } catch (error) {
      if (state.data !== owner || error.name === "AbortError") throw error;
      if (error.status === 409 && reusableID) {
        try { return finishRequest(await readReceipt(id, record), record, owner, records, id); }
        catch (receiptError) { if (receiptError.name === "AbortError") throw receiptError; }
      }
      if (error.status >= 400 && error.status < 500 && error.status !== 408) {
        delete records[id];
        persist(records);
        error.configSubmissionRejected = true;
        throw error;
      }
      // A bounded readback handles a lost response, including a transaction
      // that commits shortly after the original connection closes.
      for (let attempt = 0; attempt < 3; attempt++) {
        if (state.data !== owner) throw error;
        try { result = await readReceipt(id, record); break; }
        catch { if (attempt < 2) await new Promise(resolve => setTimeout(resolve, 250)); }
      }
      if (!result) {
        error.recoverConfigSubmission = async () => {
          const recovered = await readReceipt(id, record);
          if (state.data !== owner) throw new DOMException("Aborted", "AbortError");
          return finishRequest(recovered, record, owner, records, id);
        };
        throw error;
      }
    }
    return finishRequest(result, record, owner, records, id);
  }
  return { request, recover };
}
