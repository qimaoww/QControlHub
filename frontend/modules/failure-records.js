import { errorDiagnostic, redactDiagnostic, requestJSON } from "./errors.js";

const mutableFailures = new WeakMap();
function mutableFailure(error) {
  if (typeof error !== "object" && typeof error !== "function") return new Error(String(error));
  if (Object.getOwnPropertyDescriptor(error, "message")?.writable) return error;
  if (!mutableFailures.has(error)) {
    const wrapped = new Error(error.message || String(error), {cause:error});
    for (const key of ["name","status","requestID","code","field","stage","agentID","engine","resource","failureTaskID","failureSaved","failureRetention","readOnly","retentionError","outcomeUnknown"])
      if (error[key] !== undefined) wrapped[key] = error[key];
    mutableFailures.set(error, wrapped);
  }
  return mutableFailures.get(error);
}

// A recorder is bound to its initiating session. It has no recursive API path
// and no automatic retry; normal application requests own recovery.
export function createFailureRecorder({ session, isCurrent = () => true } = {}) {
  const pending = new Map();
  return async function record(error, operation = "面板操作", stage = "panel") {
    if (!error || error.intentionalCancellation === true || !isCurrent()) return error;
    error = mutableFailure(error);
    // A local catch or runtime wrapper must preserve the originating request's
    // policy. Skipping retention is distinct from failing to save a record.
    let origin = error; const seen = new Set();
    while (origin && !seen.has(origin)) {
      seen.add(origin);
      if (origin.readOnly || origin.failureRetention === "not_required") {
        error.failureRetention = "not_required";
        return error;
      }
      origin = origin.cause;
    }
    if (error.cause?.failureTaskID) {error.failureTaskID=error.cause.failureTaskID;error.failureSaved=true;}
    if (error.failureTaskID || error.failureSaved === true) return error;
    if (error.failureSaved === false || !session || error.status === 401) {
      if (!error.message.includes("未能保存到任务")) error.message += `\n${error.retentionError || "未能保存到任务：当前会话未获留存权限或服务不可用。"}`;
      return error;
    }
    const detail = errorDiagnostic(error), key = JSON.stringify([operation, error.stage || stage, detail,
      error.resource, error.agentID, error.engine, error.status, error.code, error.field]);
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
            http_status: error.status || 0, request_id: error.requestID || "", code: error.code == null ? "" : String(error.code), field: error.field || "" }),
        }, { isCurrent });
        if (result?.failure_retention === "not_required") return null;
        if (!result?.id) throw new Error("留存接口未返回任务标识");
        return result.id;
      })());
    }
    try {
      const id = await pending.get(key);
      if (id) { error.failureTaskID = id; error.failureSaved = true; }
      else error.failureRetention = "not_required";
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
