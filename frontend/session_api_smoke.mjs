import assert from "node:assert/strict";
import { getEventListeners } from "node:events";
import { createSessionAPI } from "./modules/session-api.js";
import { accountStorage, setStorageAccount } from "./modules/account-storage.js";
import { nodeCardOrderKey, savedNodeOrder } from "./modules/node-order.js";

const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const flush = () => new Promise(setImmediate);
const originals = new Map(["fetch", "localStorage"].map(key => [
  key, Object.getOwnPropertyDescriptor(globalThis, key),
]));
const storage = new Map();
const session = id => ({ user_id: id, role: "admin", csrf_token: `csrf-${id}` });
const state = { session: session("first"), data: {}, routeSignal: new AbortController().signal };
const logins = [], calls = [];
let respond = () => Response.json({});

try {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) },
  });
  Object.defineProperty(globalThis, "fetch", {
    configurable: true,
    value: (url, options) => {
      calls.push({ url, options });
      return respond(url, options);
    },
  });
  const transport = createSessionAPI({ state, renderLogin: message => logins.push(message) });
  const { api, optionalAPI, ensureSession, scopedAPI, readPanelData, invalidatePanelReads } = transport;
  assert.equal(calls.length, 0, "constructing transport must not read the session or render login");

  for (const method of ["GET", "HEAD", "OPTIONS"]) {
    await api("/settings", { method });
    const { url, options } = calls.at(-1);
    assert.equal(url, "/api/v1/settings");
    assert.equal(options.credentials, "same-origin");
    assert.equal(options.headers.Accept, "application/json");
    assert.equal(options.headers["X-QControlHub-CSRF"], undefined);
    assert.equal(options.headers["Content-Type"], undefined);
    assert.equal(options.signal, state.routeSignal);
  }
  await api("/tasks", {
    method: "post", body: "{}",
    headers: { "X-Test": "preserved", "X-QControlHub-CSRF": "not-the-session" },
  });
  assert.equal(calls.at(-1).options.headers["X-QControlHub-CSRF"], state.session.csrf_token);
  assert.equal(calls.at(-1).options.headers["Content-Type"], "application/json");
  assert.equal(calls.at(-1).options.headers["X-Test"], "preserved");
  assert.equal(calls.at(-1).options.signal, undefined, "navigation must not cancel a committed write");

  for (const cancelSource of ["route", "caller", "none"]) {
    const route = new AbortController(), caller = new AbortController(), response = deferred();
    state.routeSignal = route.signal;
    respond = (_url, options) => {
      options.signal.addEventListener("abort", () => response.reject(options.signal.reason), { once: true });
      return response.promise;
    };
    const read = api("/agents", { signal: caller.signal });
    const combined = calls.at(-1).options.signal;
    assert.notEqual(combined, route.signal);
    assert.notEqual(combined, caller.signal);
    assert.equal(getEventListeners(route.signal, "abort").length, 1);
    assert.equal(getEventListeners(caller.signal, "abort").length, 1);
    if (cancelSource === "none") {
      response.resolve(Response.json([]));
      await read;
    } else {
      const rejected = assert.rejects(read, { name: "AbortError" });
      (cancelSource === "route" ? route : caller).abort();
      await rejected;
      assert.equal(combined.aborted, true);
    }
    assert.equal(getEventListeners(route.signal, "abort").length, 0);
    assert.equal(getEventListeners(caller.signal, "abort").length, 0, "settled requests must release combined signal listeners");
  }

  {
    const route = new AbortController(), caller = new AbortController(), response = deferred();
    state.routeSignal = route.signal;
    respond = () => response.promise;
    const write = api("/tasks", { method: "POST", body: "{}", signal: caller.signal });
    assert.equal(calls.at(-1).options.signal, caller.signal, "caller cancellation remains available for writes");
    route.abort();
    assert.equal(calls.at(-1).options.signal.aborted, false);
    response.resolve(Response.json({ id: "submitted" }));
    assert.deepEqual(await write, { id: "submitted" });
  }
  state.routeSignal = new AbortController().signal;

  respond = () => Response.json({ error: "missing" }, { status: 404 });
  assert.equal(await optionalAPI("/optional"), null);
  respond = () => Response.json({ error: "unavailable" }, { status: 503 });
  await assert.rejects(optionalAPI("/optional"), { status: 503 });

  respond = () => Response.json(session("signed-in"));
  state.session = null;
  assert.equal(await ensureSession(), true);
  assert.deepEqual(state.session, session("signed-in"));
  accountStorage.setItem("probe", "signed-in");
  assert.equal(storage.get("probe:account:signed-in"), "signed-in");
  storage.set(nodeCardOrderKey, JSON.stringify(["hidden", "node-b", "node-a"]));
  respond = () => Response.json([{ id: "node-a" }, { id: "node-b" }]);
  await api("/agents");
  assert.deepEqual(savedNodeOrder(), ["node-b", "node-a"], "transport migrates only the current account-visible fleet");

  // A superseded response must neither log out the new account nor migrate
  // node IDs into its storage, including a switch while JSON is being decoded.
  for (const phase of ["success", "unauthorized", "body"]) {
    const response = deferred(), parsed = deferred();
    state.session = session("previous");
    setStorageAccount(state.session);
    respond = () => phase === "body"
      ? { ok: true, status: 200, json: () => parsed.promise }
      : response.promise;
    const read = api("/agents");
    const rejected = assert.rejects(read, { name: "AbortError" });
    await flush();
    const nextData = { draft: "keep" };
    state.session = session(`next-${phase}`);
    state.data = nextData;
    setStorageAccount(state.session);
    if (phase === "body") parsed.resolve([{ id: "node-a" }]);
    else response.resolve(Response.json([{ id: "node-a" }], { status: phase === "unauthorized" ? 401 : 200 }));
    await rejected;
    assert.equal(state.data, nextData);
    assert.equal(state.session.user_id, `next-${phase}`);
    assert.equal(accountStorage.getItem(nodeCardOrderKey), null);
    assert.deepEqual(logins, [], "stale unauthorized responses must not render login");
  }

  respond = () => Response.json({ error: "invalid admin token" }, { status: 401 });
  const beforeLogin = state.session;
  await assert.rejects(api("/auth/login", { method: "POST", body: "{}" }), { status: 401 });
  assert.equal(state.session, beforeLogin, "failed credentials must stay on the login flow");
  assert.deepEqual(logins, []);
  await assert.rejects(api("/settings"), { status: 401 });
  assert.equal(state.session, null);
  assert.deepEqual(state.data, {});
  assert.equal(logins.length, 1);
  accountStorage.setItem("probe", "signed-out");
  assert.equal(storage.get("probe:account:signed-out"), "signed-out");

  state.session = session("failed-session");
  state.data = { stale: true };
  respond = () => Response.json({ error: "forbidden" }, { status: 403 });
  assert.equal(await ensureSession(), false);
  assert.equal(state.session, null);
  assert.deepEqual(state.data, {});

  state.session = session("cache-a");
  respond = () => Response.json({ account: state.session.user_id, sequence: calls.length });
  scopedAPI.begin();
  const startReads = calls.length;
  const [first, second] = await Promise.all([api("/shared"), api("/shared")]);
  assert.equal(calls.length, startReads + 1);
  assert.notEqual(first, second);
  scopedAPI.end();
  await api("/shared");
  assert.equal(calls.length, startReads + 2);
  const overview = await readPanelData("overview");
  assert.equal(await readPanelData("overview"), overview);
  assert.notEqual(await readPanelData("overview", { live: true }), overview);
  const settings = await readPanelData("settings");
  assert.equal(await readPanelData("settings"), settings);
  invalidatePanelReads("settings");
  assert.notEqual(await readPanelData("settings"), settings);
  state.session = session("cache-b");
  assert.equal((await readPanelData("overview")).account, "cache-b");
  assert.equal((await readPanelData("settings")).account, "cache-b", "panel caches must follow the current session scope");
} finally {
  setStorageAccount(null);
  for (const [key, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else delete globalThis[key];
  }
}

console.log("Session API transport, cancellation, CSRF and account-isolation smoke passed");
