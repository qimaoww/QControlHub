import assert from "node:assert/strict";
import {createFailureRecorder} from "./modules/failure-records.js";
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
for (const field of ['password', 'token', 'credential', 'api_key', 'pre-shared-key']) {
  const text = `listeners[1] ${field}="fixture-secret"; connection refused`;
  const redacted = redactDiagnostic(text);
  assert.ok(!redacted.includes('fixture-secret'), `${field} leaked in historical diagnostics`);
  assert.ok(redacted.includes('listeners[1]') && redacted.includes('connection refused'), 'redaction lost diagnostic context');
  assert.equal(redactDiagnostic(redacted), redacted, 'repeated diagnostic rendering changes redacted values');
}
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
  assert.match(error.message, /<html>Bad Gateway<\/html>/, "proxy response remains available as inert diagnostic text");
  return true;
});
await assert.rejects(send(new Response('{"error":{"bad":"shape"}}', { status: 403 })), /请求被拒绝/);
await assert.rejects(send(new Response('{"error":"请先保存配置"}', { status: 409 })), /请先保存配置/);
for (const field of ["port", "tls_public_port", "untrusted-field"]) {
  await assert.rejects(send(new Response(JSON.stringify({ error: "端口 443 已由其他用户占用", code: "port_conflict", field }), { status: 409 })), error => {
    assert.equal(error.status, 409);
    assert.equal(error.code, "port_conflict");
    assert.equal(error.field, field === "untrusted-field" ? "" : field);
    return true;
  });
}
await assert.rejects(send(new Response('{"error":"revision changed"}', { status: 409 })), error => !error.code);
await assert.rejects(send(new Response("not JSON")), /响应不完整或格式无效/);
assert.equal(await send(new Response(null, { status: 204 })), null);
assert.deepEqual(await send(new Response('{"ok":true}')), { ok: true });
for (const name of ["TypeError", "TimeoutError"]) {
  const cause = new Error("native error");
  cause.name = name;
  await assert.rejects(send(null, { fetchImpl: async () => { throw cause; } }), (error) => {
    assert.equal(error.cause, cause);
    assert.match(error.message, name === "TimeoutError" ? /读取超时/ : /无法读取数据/);
    assert.doesNotMatch(error.message, /确认操作结果|重复提交/);
    return true;
  });
}
const abort = new DOMException("The operation was aborted", "AbortError");
const timeout = new DOMException("The operation timed out", "TimeoutError");
await assert.rejects(requestJSON("/test", { signal: AbortSignal.abort(timeout) }, {
  fetchImpl: async () => { throw timeout; },
}), /读取超时/);
for (const method of ["GET", "HEAD", "POST", "PUT", "DELETE"]) {
  const readOnly = ["GET", "HEAD"].includes(method);
  for (const failure of ["gateway", "network", "timeout", "body"]) {
    await assert.rejects(requestJSON("/api/v1/core-logs?limit=1000", { method }, {
      fetchImpl: async () => {
        if (failure === "gateway") return new Response("<html>Gateway Timeout</html>", { status: 504, headers: { "X-Request-ID": "read-timeout-1" } });
        if (failure === "network") throw new TypeError("Failed to fetch");
        if (failure === "timeout") throw timeout;
        return new Response("broken JSON");
      },
    }), error => {
      assert.equal(/确认操作结果|避免重复提交/.test(error.message), !readOnly, `${method} ${failure}: ${error.message}`);
      if (failure === "gateway") { assert.equal(error.status, 504); assert.equal(error.requestID, "read-timeout-1"); }
      return true;
    });
  }
}
await assert.rejects(send(null, { fetchImpl: async () => { throw abort; } }), (error) => error !== abort && error.cause === abort && error.name === "Error");
await assert.rejects(send({ ok: true, status: 200, json: async () => { throw abort; } }), (error) => error.cause === abort);
await assert.rejects(send({ ok: false, status: 503, json: async () => { throw abort; } }), (error) => error.status === 503);
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

const githubDNSTimeout = 'resolve latest stable mihomo release: Get "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest": dial tcp: lookup api.github.com: i/o timeout';
assert.match(diagnosticError(githubDNSTimeout), /GitHub API 域名解析超时/);
assert.doesNotMatch(diagnosticError(githubDNSTimeout), /节点操作超时/);
assert.match(diagnosticError("lookup example.com: i/o timeout"), /域名解析超时/);
assert.match(diagnosticError("GitHub download network failure: direct: GitHub API domain resolution timed out; acceleration: v6.gh-proxy.org domain resolution timed out"), /GitHub API 域名解析超时/);
assert.match(diagnosticError("GitHub download network failure: acceleration: connection refused"), /GitHub 下载网络故障/);

assert.match(diagnosticError("GitHub API metadata rate limit exhausted (HTTP 403); retry after reset"), /请求额度已耗尽/);
assert.match(diagnosticError("GitHub API metadata returned HTTP 404"), /资源不存在/);
assert.match(diagnosticError("downloaded release asset SHA-256 does not match GitHub metadata"), /校验失败/);
assert.match(diagnosticError("context canceled"), /操作被中断/);

const schemaMessage = '任务创建失败，尚未进入 Agent 执行。请检查控制面升级及数据库迁移状态。';
await assert.rejects(send(new Response(JSON.stringify({ error: schemaMessage, code: 'task_schema_incompatible', stage: 'task_creation', request_id: 'req_fixture123' }), { status: 500 })), error => {
  assert.equal(error.status, 500);
  assert.equal(error.code, 'task_schema_incompatible');
  assert.equal(error.stage, 'task_creation');
  assert.equal(error.requestID, 'req_fixture123');
  assert.equal(error.message, `${schemaMessage}（请求 ID：req_fixture123）`);
  return true;
});
await assert.rejects(send(new Response('{"error":"internal server error"}', {status:500,headers:{'X-Request-ID':'req_header'}})), error => error.requestID === 'req_header' && !error.code);
await assert.rejects(send(new Response(JSON.stringify({error:schemaMessage,request_id:'<script>bad</script>'}),{status:500})), error => !error.requestID && !error.message.includes('<script>'));

const originalFetch=globalThis.fetch; let retainedAborts=0;
try {
 globalThis.fetch=async()=>{retainedAborts++;return new Response('{"id":"tsk_abort_fixture"}',{status:201})};
 const recorder=createFailureRecorder({session:{csrf_token:"fixture"}});
 const unexpected=new DOMException("unexpected socket interruption","AbortError");
 const retained=await recorder(unexpected,"后台连接","transport");
 assert.equal(retained.failureTaskID,"tsk_abort_fixture");
 assert.equal(retained.cause,unexpected,"immutable native error remains in the cause chain");
 assert.equal(await recorder(unexpected,"后台连接","transport"),retained,"propagated native failure reuses its retained identity");
 const canceled=Object.assign(new DOMException("navigation","AbortError"),{intentionalCancellation:true});
 await recorder(canceled,"导航","transport");
 assert.equal(retainedAborts,1,"unexpected abort must be retained while explicit cancellation stays silent");
 globalThis.fetch=async()=>{throw new Error("fixture retention transport unavailable")};
 const failed=await recorder(new DOMException("native failure with read-only message","AbortError"),"复制","browser_action");
 assert.equal(failed.failureSaved,false);
 assert.match(failed.message,/native failure with read-only message/);
 assert.match(failed.message,/未能保存到任务/);
 assert.match(failed.message,/fixture retention transport unavailable/);
} finally {globalThis.fetch=originalFetch}

// Reads retain their real failure and diagnostic identity without acquiring a
// failed operation record, even when several application layers see them.
try {
 let reports=0;
 globalThis.fetch=async()=>{reports++;return Response.json({id:'tsk_should_not_exist'},{status:201})};
 const recorder=createFailureRecorder({session:{csrf_token:'fixture'}});
 for (const mode of ['server-policy','old-server','proxy','network','unexpected-abort','timeout','decode']) {
  let failure;
  try {await requestJSON('/api/v1/core-logs',{}, {fetchImpl:async()=>{
   if(mode==='network')throw new TypeError('Failed to fetch');
   if(mode==='unexpected-abort')throw new DOMException('unexpected interruption','AbortError');
   if(mode==='timeout')throw new DOMException('deadline','TimeoutError');
   if(mode==='decode')return new Response('broken');
   if(mode==='proxy')return new Response('Gateway Timeout',{status:504,headers:{'X-Request-ID':'req_gateway'}});
   return Response.json({error:'读取失败',request_id:'req_read',...(mode==='server-policy'?{failure_retention:'not_required'}:{})},{status:500});
  }});}catch(error){failure=error;}
  assert.ok(failure && !failure.intentionalCancellation,mode);
  await recorder(failure,'读取日志','page_loading');
  await recorder(new Error('读取区域失败',{cause:failure}),'区域','region_loading');
  assert.equal(failure.failureRetention,'not_required');
  assert.equal(failure.failureSaved,undefined);
  assert.doesNotMatch(failure.message,/未能保存到任务/);
 }
 assert.equal(reports,0,'reads were backfilled by another error layer');
 const stored=Object.assign(new Error('保存失败'),{failureSaved:true,failureTaskID:'tsk_existing'});
 await recorder(stored,'保存','panel_action'); assert.equal(reports,0,'server failure was backfilled');
 const denied=Object.assign(new Error('校验失败'),{failureRetention:'not_required'});
 await recorder(denied); assert.equal(reports,0);
 const write=await recorder(new Error('实际写入失败'),'保存','panel_action');
 assert.equal(write.failureTaskID,'tsk_should_not_exist');assert.equal(reports,1);
 // A policy response from an older caller's backfill is a successful skip.
 globalThis.fetch=async()=>Response.json({failure_retention:'not_required'});
 const skipped=await recorder(new Error('旧客户端读取失败'),'GET /core-logs');
 assert.equal(skipped.failureRetention,'not_required');assert.equal(skipped.failureSaved,undefined);
 assert.doesNotMatch(skipped.message,/未能保存到任务/);
 // Explicit cancellation after headers and even an abort-ignoring body stays silent.
 for(const status of [200,500]) {
  const controller=new AbortController();
  await assert.rejects(requestJSON('/test',{signal:controller.signal},{fetchImpl:async()=>({ok:status===200,status,json:async()=>{controller.abort();return {error:'late failure'};}})}),error=>error.intentionalCancellation===true);
 }
} finally {globalThis.fetch=originalFetch}
console.log('Read retention policy, propagation, proxy errors and lifecycle cancellation passed');
