import assert from "node:assert/strict";
import { diagnosticError, errorMessage, redactDiagnostic, requestJSON } from "./modules/errors.js";

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
for (const [raw, explanation] of [
  ["kernel mihomo: certificate file access at listeners[1].certificate (/etc/qcontrolhub/tls/cert.pem): certificate/key file not found", /文件不存在/],
  ["kernel mihomo: certificate file access at listeners[1].private-key (/etc/qcontrolhub/tls/key.pem): certificate/key file unreadable as qcontrolhub-core: exit status 1", /文件无法读取/],
  ["mihomo rejected the configuration: parse certificate failed: failed to find any PEM data in key input", /格式错误/],
  ["x509: certificate signed by unknown authority", /信任或域名验证失败/],
  ["x509: certificate has expired or is not yet valid", /信任或域名验证失败/],
  ["Reality target www.getty.edu: TLS 1.3 handshake failed: x509: certificate signed by unknown authority", /Reality 目标握手失败/],
  ["invalid Reality privateKey: invalid-key-value", /Reality 配置解析失败/],
  ["WireGuard: invalid private-key", /私钥配置解析失败/],
  ["tls: invalid private key", /TLS 私钥格式错误/],
  ["kernel mihomo: configuration fields: listeners[0].reality-config.private-key, listeners[1].private-key; failed to parse certificate: malformed PEM", /TLS 私钥格式错误/],
  ["parse certificate failed, maybe format error:tls: failed to find any PEM data in certificate input, or path error: open /var/lib/qcontrolhub-mihomo/-----BEGIN CERTIFICATE-----\\ninvalid-cert\\n-----END CERTIFICATE-----: no such file or directory", /TLS 私钥格式错误/],
  ["parse certificate failed, maybe format error:tls: private key does not match public key, or path error: open /var/lib/qcontrolhub-mihomo/[redacted]: no such file or directory", /TLS 私钥格式错误/],
  ["parse certificate failed, maybe format error:tls: failed to find any PEM data in certificate input, or path error: open /etc/qcontrolhub/tls/missing.pem: no such file or directory", /文件不存在/],
]) {
  const diagnostic = diagnosticError(raw);
  assert.match(diagnostic, explanation);
  if (raw.includes("listeners[1]")) assert.ok(diagnostic.includes("listeners[1]"), "field location must survive diagnostic translation");
}
assert.doesNotMatch(diagnosticError("WireGuard: invalid private-key"), /Reality 配置解析失败/);
assert.doesNotMatch(diagnosticError("kernel mihomo: configuration fields: listeners[0].reality-config.private-key; invalid private key"), /Reality 配置解析失败/);
const oldFailure = `kernel mihomo service qagent-mihomo.service: read certificate/key /var/lib/qcontrolhub-mihomo/${"A".repeat(43)} as qcontrolhub-core: exit status 1`;
assert.match(diagnosticError(oldFailure), /文件无法读取/);
assert.ok(!diagnosticError(oldFailure).includes("A".repeat(43)), "historical task errors must mask the inline Reality key");
assert.ok(!redactDiagnostic(oldFailure).includes("A".repeat(43)), "task output and journal display must mask historical paths");
const realTLSPath = `certificate file access at listeners[1].private-key (/var/lib/qcontrolhub-mihomo/${"A".repeat(43)}): permission denied`;
assert.ok(diagnosticError(realTLSPath).includes("A".repeat(43)), "a real TLS filename that resembles a key must remain locatable");
assert.equal(redactDiagnostic('invalid "privateKey": inline-secret'), 'invalid "privateKey": [redacted]');
assert.ok(!diagnosticError("中文诊断 private-key: inline-secret").includes("inline-secret"));
assert.equal(redactDiagnostic("-----BEGIN PRIVATE KEY-----\ninline-secret\n-----END PRIVATE KEY-----"), "[redacted private PEM]");
assert.match(diagnosticError("dial tcp: connection refused"), /目标服务拒绝连接/);
assert.match(diagnosticError("permission denied"), /节点执行权限不足/);
assert.match(diagnosticError("context deadline exceeded"), /节点操作超时/);
assert.match(diagnosticError("configuration rejected; rolled back"), /已自动回滚/);
const recoveryFailure = "configuration restart failed (exit status 1); previous file was restored but service recovery failed: exit status 1";
assert.match(errorMessage(diagnosticError(recoveryFailure)), /回滚或服务恢复失败/);
assert.ok(errorMessage(diagnosticError(recoveryFailure)).includes(recoveryFailure), "notification must preserve deployment recovery diagnostics");

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
const expiredRoute = new AbortController();
await assert.rejects(requestJSON("/api/v1/agents", { signal: expiredRoute.signal }, {
  fetchImpl: async () => ({
    ok: false, status: 401,
    json: async () => { assert.fail("401 must not read a body after login cancels the route"); },
  }),
  onUnauthorized: (message) => {
    assert.match(message, /登录已失效/);
    expiredRoute.abort();
  },
}), (error) => error.status === 401 && error.name === "Error" && /登录已失效/.test(error.message));
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
for (const status of [200, 401, 403, 500]) {
  let current = true;
  await assert.rejects(send(null, {
    isCurrent: () => current,
    fetchImpl: async () => {
      current = false;
      return new Response('{"private":"previous account"}', { status });
    },
  }), error => error.name === "AbortError");
}
assert.equal(unauthorized, 1, "a previous account's 401 must not expire the new session");
for (const ok of [true, false]) {
  let current = true;
  await assert.rejects(send({
    ok, status: ok ? 200 : 409,
    json: async () => { current = false; return { private: "previous account" }; },
  }, { isCurrent: () => current }), error => error.name === "AbortError",
  "a slow JSON response must not cross an account switch");
}
await assert.rejects(send(null, {
  isCurrent: () => false,
  fetchImpl: async () => { throw new Error("old private network diagnostic"); },
}), error => error.name === "AbortError");
console.log("Chinese error messages smoke tests passed");
