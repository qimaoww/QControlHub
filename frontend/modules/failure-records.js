import { errorDiagnostic, redactDiagnostic, requestJSON } from "./errors.js";

// A recorder is bound to its initiating session. It has no recursive API path
// and no automatic retry; normal application requests own recovery.
export function createFailureRecorder({ session, isCurrent = () => true } = {}) {
  const pending = new Map(), recent = new Map();
  return async function record(error, operation = "面板操作", stage = "panel") {
    if (!error || error.name === "AbortError" || !isCurrent()) return error;
    if (error.cause?.failureTaskID) {error.failureTaskID=error.cause.failureTaskID;error.failureSaved=true;}
    const prior=recent.get(error.message);
    if(stage==="panel_feedback" && prior && Date.now()-prior.at<5000) {error.failureTaskID=prior.id;error.failureSaved=true;}
    if (error.failureTaskID || error.failureSaved === true) {
      recent.set(error.message,{id:error.failureTaskID,at:Date.now()});
      while(recent.size>64)recent.delete(recent.keys().next().value);
      return error;
    }
    if (error.failureSaved === false || !session || error.status === 401) {
      if (!error.message.includes("未能保存到任务")) error.message += `\n${error.retentionError || "未能保存到任务：当前会话未获留存权限或服务不可用。"}`;
      return error;
    }
    const detail = errorDiagnostic(error), key = `${operation}\n${error.stage || stage}\n${detail}`;
    // Coalesce propagation and concurrent refreshes. Server deduplication also
    // survives reloads, login and multiple browser sessions.
    if (!pending.has(key)) {
      while (pending.size >= 64) pending.delete(pending.keys().next().value);
      pending.set(key, (async () => {
        const headers = { Accept: "application/json", "Content-Type": "application/json" };
        if (session.csrf_token) headers["X-QControlHub-CSRF"] = session.csrf_token;
        const result = await requestJSON("/api/v1/tasks/failures", {
          method: "POST", credentials: "same-origin", headers, signal: AbortSignal.timeout(4000),
          body: JSON.stringify({ operation: redactDiagnostic(operation), stage: error.stage || stage,
            resource: redactDiagnostic(error.resource || operation), agent_id:error.agentID || "", engine:error.engine || "",
            summary: redactDiagnostic(error.message).slice(0,4096), detail, occurred_at: new Date().toISOString(),
            http_status: error.status || 0, request_id: error.requestID || "", code: error.code || "", field: error.field || "" }),
        }, { isCurrent });
        if (!result?.id) throw new Error("留存接口未返回任务标识");
        return result.id;
      })());
    }
    try {
      error.failureTaskID = await pending.get(key); error.failureSaved = true;
      recent.set(error.message,{id:error.failureTaskID,at:Date.now()});
      while(recent.size>64)recent.delete(recent.keys().next().value);
      setTimeout(()=>pending.delete(key),5000);
    }
    catch (cause) {
      pending.delete(key);
      error.failureSaved = false;
      error.message += `\n未能保存到任务：${errorDiagnostic(cause)}`;
    }
    return error;
  };
}
