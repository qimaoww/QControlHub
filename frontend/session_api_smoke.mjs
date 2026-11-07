import assert from "node:assert/strict";
import { accountRequest } from "./modules/account-request.js";

const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const flush = () => new Promise(setImmediate);
const originalFetch = globalThis.fetch;
const session = id => ({ user_id: id, csrf_token: `csrf-${id}` });
const calls = [], logins = [];
let respond = () => Response.json({});

try {
  globalThis.fetch = (url, options) => {
    calls.push({ url, options });
    return respond(url, options);
  };
  assert.equal(calls.length, 0, "importing the shipped transport must remain inert");
  const first = session("first");
  for (const method of ["GET", "HEAD", "OPTIONS"]) {
    const controller = new AbortController();
    await accountRequest("/settings", { session: first, method, signal: controller.signal });
    const { url, options } = calls.at(-1);
    assert.equal(url, "/api/v1/settings");
    assert.equal(options.method, method);
    assert.equal(options.credentials, "same-origin");
    assert.equal(options.cache, "no-store");
    assert.equal(options.headers.Accept, "application/json");
    assert.equal(options.headers["X-QControlHub-CSRF"], first.csrf_token, "reads bind to their initiating account even if another tab replaces the cookie");
    assert.equal(options.headers["Content-Type"], undefined);
    assert.equal(options.signal, controller.signal, "the native route owns request cancellation");
  }
  for (const taskID of ["tsk_0123456789abcdef", "tsk_invalid", "tsk_0123456789abcdef\r\nX-Injected: yes", undefined]) {
    await accountRequest("/tasks", {
      session: first, method: "POST", body: { action: "deploy" },
      headers: { "X-Config-Task-ID": taskID, "X-QControlHub-CSRF": "another-account", Authorization: "untrusted" },
    });
    const { options } = calls.at(-1);
    assert.equal(options.headers["X-QControlHub-CSRF"], first.csrf_token);
    assert.equal(options.headers["Content-Type"], "application/json");
    assert.equal(options.headers.Authorization, undefined, "only the existing config-task header is forwarded");
    assert.equal(options.headers["X-Config-Task-ID"], taskID === "tsk_0123456789abcdef" ? taskID : undefined);
    assert.deepEqual(JSON.parse(options.body), { action: "deploy" });
    assert.equal(options.signal, undefined, "the transport must not invent a route signal for a committed write");
  }
  for (const method of ["GET", "POST"]) {
    const controller = new AbortController(), response = deferred();
    respond = (_url, options) => {
      options.signal.addEventListener("abort", () => response.reject(options.signal.reason), { once: true });
      return response.promise;
    };
    const read = accountRequest("/tasks", { session: first, method, signal: controller.signal });
    const rejected = assert.rejects(read, { name: "AbortError" });
    controller.abort();
    await rejected;
    assert.equal(calls.at(-1).options.signal, controller.signal, "caller cancellation remains available for reads and writes");
  }
  for (const phase of ["success", "unauthorized", "body", "error-body", "network"]) {
    const response = deferred(), parsed = deferred();
    let current = first;
    respond = () => phase.endsWith("body")
      ? { ok: phase === "body", status: phase === "body" ? 200 : 409, json: () => parsed.promise }
      : response.promise;
    const read = accountRequest("/agents", {
      session: first, isCurrent: () => current === first, onUnauthorized: message => logins.push(message),
    });
    const rejected = assert.rejects(read, { name: "AbortError" });
    await flush();
    current = session(`next-${phase}`);
    if (phase.endsWith("body")) parsed.resolve({ private: "previous account" });
    else if (phase === "network") response.reject(new Error("old private network diagnostic"));
    else response.resolve(Response.json({ private: "previous account" }, { status: phase === "unauthorized" ? 401 : 200 }));
    await rejected;
    assert.equal(calls.at(-1).options.headers["X-QControlHub-CSRF"], first.csrf_token);
    assert.deepEqual(logins, [], "late responses must not expire the replacement account");
  }
  respond = () => Response.json({ error: "invalid admin token" }, { status: 401 });
  await assert.rejects(accountRequest("/auth/login", {
    method: "POST", body: { username: "fixture" }, onUnauthorized: message => logins.push(message),
  }), { status: 401 });
  assert.deepEqual(logins, [], "failed credentials remain in the existing login flow");
  await assert.rejects(accountRequest("/settings", { session: first, onUnauthorized: message => logins.push(message) }), { status: 401 });
  assert.equal(logins.length, 1);
  assert.match(logins[0], /登录已失效/);
  for (const status of [403, 404, 503]) {
    respond = () => Response.json({ error: "fixture failure" }, { status });
    await assert.rejects(accountRequest("/settings", { session: first }), { status });
  }
  respond = () => Response.json({ account: "fresh", items: [] });
  for (const path of ["/auth/session", "/settings", "/agents"]) {
    const count = calls.length;
    const [a, b] = await Promise.all([accountRequest(path, { session: first }), accountRequest(path, { session: first })]);
    assert.equal(calls.length, count + 2, "native requests have no render-prefetch cache");
    a.items.push("changed");
    assert.deepEqual(b.items, [], "independent callers cannot mutate another response");
  }
  // Native requests always read fresh state. A read made during a write
  // cannot become a reusable pre-commit baseline, even if its reply is lost.
  for (const fails of [false, true]) {
    let version = 1;
    const committed = deferred();
    respond = (_url, options) => options.method === 'PUT' ? committed.promise : Response.json({ version });
    const write = accountRequest('/settings', { session: first, method: 'PUT', body: { name: 'new' } });
    assert.equal((await accountRequest('/settings', { session: first })).version, 1);
    version = 2;
    if (fails) { const rejected = assert.rejects(write, error => /无法连接服务器/.test(error.message) && /response lost/.test(error.cause?.message)); committed.reject(new Error('response lost after save')); await rejected; }
    else { committed.resolve(Response.json({ version })); await write; }
    assert.equal((await accountRequest('/settings', { session: first })).version, 2, 'write completion reused an in-flight pre-commit snapshot');
  }
  let failures = true;
  respond = () => failures ? Response.json({ error: 'temporary read failure' }, { status: 503 }) : Response.json({ recovered: true });
  await assert.rejects(accountRequest('/settings', { session: first }), { status: 503 });
  failures = false;
  assert.equal((await accountRequest('/settings', { session: first })).recovered, true, 'failed settings read pinned a cached error');
  for (const identity of [first, session('replacement')]) {
    respond = (_url, options) => Response.json({ account: options.headers['X-QControlHub-CSRF'] });
    assert.equal((await accountRequest('/settings', { session: identity })).account, identity.csrf_token, 'replacement account inherited shell settings');
  }
  // Account order/storage and actual navigation/pending-write guards retain
  // their existing model and motion-browser suites.

} finally {
  globalThis.fetch = originalFetch;
}

console.log("Native account transport, cancellation, CSRF and stale-response smoke passed");
