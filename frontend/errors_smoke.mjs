import assert from "node:assert/strict";
import { diagnosticError, errorMessage, requestJSON } from "./modules/errors.js";

for (const status of [400, 401, 403, 404, 405, 408, 409, 413, 422, 429, 500, 502, 503, 504, 599]) {
  assert.match(errorMessage("unknown English error", status), /\p{Script=Han}/u);
}
for (const value of [null, {}, [], 42, "", "<html>Bad Gateway</html>"]) {
  assert.match(errorMessage(value, 502), /后端服务/);
}
assert.equal(errorMessage("已有中文提示"), "已有中文提示");
assert.match(errorMessage("cross-site login request rejected"), /HTTPS.*QCH_CORS_ORIGINS/);
assert.match(diagnosticError("exit status 1"), /原始错误（供排查）：exit status 1/);
assert.equal(diagnosticError("中文诊断"), "中文诊断");
assert.equal(diagnosticError(null), "");
assert.match(diagnosticError("dial tcp: connection refused"), /目标服务拒绝连接/);
assert.match(diagnosticError("permission denied"), /节点执行权限不足/);
assert.match(diagnosticError("context deadline exceeded"), /节点操作超时/);
assert.match(diagnosticError("configuration rejected; rolled back"), /已自动回滚/);

let unauthorized = 0;
const send = (response, extra = {}) => requestJSON("/api/v1/test", {}, {
  fetchImpl: async () => response,
  onUnauthorized: () => unauthorized++,
  ...extra,
});
for (const isLogin of [true, false]) {
  await assert.rejects(send(new Response('{"error":"invalid admin token"}', { status: 401 }), { isLogin }), (error) => {
    assert.equal(error.status, 401);
    assert.match(error.message, isLogin ? /用户名、密码或管理员令牌不正确/ : /登录已失效/);
    return true;
  });
}
assert.equal(unauthorized, 1, "invalid login must not trigger session-expired handling");
await assert.rejects(send(new Response("<html>Bad Gateway</html>", { status: 502 })), (error) => {
  assert.equal(error.status, 502);
  assert.match(error.message, /后端服务/);
  assert.doesNotMatch(error.message, /html/);
  return true;
});
await assert.rejects(send(new Response('{"error":{"bad":"shape"}}', { status: 403 })), /请求被拒绝/);
await assert.rejects(send(new Response('{"error":"请先保存配置"}', { status: 409 })), /请先保存配置/);
await assert.rejects(send(new Response("not JSON")), /响应不完整或格式无效/);
assert.equal(await send(new Response(null, { status: 204 })), null);
assert.deepEqual(await send(new Response('{"ok":true}')), { ok: true });
for (const name of ["TypeError", "TimeoutError"]) {
  const cause = new Error("native error");
  cause.name = name;
  await assert.rejects(send(null, { fetchImpl: async () => { throw cause; } }), (error) => {
    assert.equal(error.cause, cause);
    assert.match(error.message, name === "TimeoutError" ? /请求超时/ : /无法连接服务器/);
    assert.match(error.message, /避免重复提交/);
    return true;
  });
}
const abort = new DOMException("The operation was aborted", "AbortError");
const timeout = new DOMException("The operation timed out", "TimeoutError");
await assert.rejects(requestJSON("/test", { signal: AbortSignal.abort(timeout) }, {
  fetchImpl: async () => { throw timeout; },
}), /请求超时/);
await assert.rejects(send(null, { fetchImpl: async () => { throw abort; } }), (error) => error === abort);
await assert.rejects(send({ ok: true, status: 200, json: async () => { throw abort; } }), (error) => error === abort);
await assert.rejects(send({ ok: false, status: 503, json: async () => { throw abort; } }), (error) => error === abort);
console.log("Chinese error messages smoke tests passed");
