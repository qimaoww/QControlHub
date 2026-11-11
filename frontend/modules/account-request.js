import { createFailureRecorder } from "./failure-records.js";
const recorders = new WeakMap();
export function accountFailureRecorder(session, isCurrent = () => true) {
 if (!session || typeof session !== "object") return createFailureRecorder({session,isCurrent});
 if (!recorders.has(session)) recorders.set(session,createFailureRecorder({session,isCurrent}));
 return recorders.get(session);
}

import { requestJSON } from "./errors.js";

// Both runtimes use the same cookie and bind every authenticated request to
// the CSRF token from the session that initiated it.
export async function accountRequest(path, { session, method = "GET", body, signal, isCurrent, onUnauthorized, conditional, onResponse, keepalive, headers: requestHeaders } = {}) {
  const headers = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (session?.csrf_token) headers["X-QControlHub-CSRF"] = session.csrf_token;
  const taskID = requestHeaders?.["X-Config-Task-ID"];
  if (/^tsk_[0-9a-f]{16}$/.test(taskID || "")) headers["X-Config-Task-ID"] = taskID;
  try { return await requestJSON(`/api/v1${path}`, {
    method, headers, signal, conditional, onResponse, keepalive, credentials: "same-origin", cache: "no-store",
    body: body === undefined ? undefined : JSON.stringify(body),
  }, { isLogin: path === "/auth/login", isCurrent, onUnauthorized, conditional, onResponse });
  } catch(error) {
    if (path !== "/tasks/failures" && !path.startsWith("/auth/")) error = await accountFailureRecorder(session,isCurrent)(error,`${method} ${path.split("?")[0]}`,"api_request");
    throw error;
  }
}
