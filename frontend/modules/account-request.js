import { requestJSON } from "./errors.js";

// Both runtimes use the same cookie and bind every authenticated request to
// the CSRF token from the session that initiated it.
export function accountRequest(path, { session, method = "GET", body, signal, isCurrent, onUnauthorized, conditional, onResponse, headers: requestHeaders } = {}) {
  const headers = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (session?.csrf_token) headers["X-QControlHub-CSRF"] = session.csrf_token;
  const taskID = requestHeaders?.["X-Config-Task-ID"];
  if (/^tsk_[0-9a-f]{16}$/.test(taskID || "")) headers["X-Config-Task-ID"] = taskID;
  return requestJSON(`/api/v1${path}`, {
    method, headers, signal, conditional, onResponse, credentials: "same-origin", cache: "no-store",
    body: body === undefined ? undefined : JSON.stringify(body),
  }, { isLogin: path === "/auth/login", isCurrent, onUnauthorized, conditional, onResponse });
}
