// Real Chromium contexts against isolated PostgreSQL and password/CSRF sessions.
// Follow the repository's dependency-free native CDP browser convention.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const [base, mode, baseline] = process.argv.slice(2);
const fixture = JSON.parse(process.env.QCH_MULTI_USER_FIXTURE);
const artifacts = process.env.QCH_MULTI_USER_ARTIFACT_DIR;
const profile = await mkdtemp(join(tmpdir(), "qch-multi-user-"));
const child = spawn(process.env.QCH_CHROME_BIN || "chromium", ["--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
const report = [];
const record = message => { report.push(message); console.log(message); };
let socket;
const timer = setTimeout(() => child.kill("SIGKILL"), 60000);
try {
  let stderr = "";
  const endpoint = await new Promise((resolve, reject) => {
    child.stderr.on("data", chunk => {
      stderr = (stderr + chunk).slice(-8192);
      const match = stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      if (match) resolve(match[1]);
    });
    child.once("error", reject);
    child.once("exit", code => reject(new Error(`Chromium exited ${code}: ${stderr}`)));
  });
  socket = new WebSocket(endpoint);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", reject, { once: true }); });
  let sequence = 0;
  const pending = new Map();
  socket.addEventListener("message", ({ data }) => {
    const message = JSON.parse(String(data));
    const entry = pending.get(message.id);
    if (!entry) return;
    pending.delete(message.id);
    clearTimeout(entry.timer);
    if (message.error) entry.reject(new Error(`${entry.method}: ${JSON.stringify(message.error)}`));
    else entry.resolve(message.result);
  });
  const call = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 10000);
    pending.set(id, { resolve, reject, timer, method });
    socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  });
  const evaluate = async (page, expression) => {
    const result = await call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }, page);
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  const wait = async (page, expression) => {
    for (let attempt = 0; attempt < 160; attempt++) {
      try {
        const result = await evaluate(page, expression);
        if (result) return result;
      } catch (error) {
        // Only read-only DOM polling may resume across document navigation.
        if (!/Execution context was destroyed|Cannot find context|Inspected target navigated/.test(String(error))) throw error;
      }
      await delay(50);
    }
    throw new Error(`Browser condition timed out: ${expression}`);
  };
  const reload = async page => {
    let timeout;
    let onMessage;
    const loaded = new Promise((resolve, reject) => {
      timeout = setTimeout(() => reject(new Error("Page reload did not finish")), 10000);
      onMessage = ({ data }) => {
        const message = JSON.parse(String(data));
        if (message.sessionId === page && message.method === "Page.loadEventFired") resolve();
      };
      socket.addEventListener("message", onMessage);
    });
    try {
      // Register before reloading: command acknowledgement precedes navigation.
      await Promise.all([call("Page.reload", {}, page), loaded]);
    } finally {
      clearTimeout(timeout);
      socket.removeEventListener("message", onMessage);
    }
  };
  const createPage = async (url = base) => {
    const { browserContextId } = await call("Target.createBrowserContext");
    const { targetId } = await call("Target.createTarget", { url: "about:blank", browserContextId });
    const { sessionId } = await call("Target.attachToTarget", { targetId, flatten: true });
    await call("Page.enable", {}, sessionId);
    await call("Emulation.setDeviceMetricsOverride", { width: mode === "mobile" ? 390 : 1440, height: mode === "mobile" ? 844 : 1000, deviceScaleFactor: 1, mobile: mode === "mobile" }, sessionId);
    if (mode === "mobile") await call("Emulation.setTouchEmulationEnabled", { enabled: true }, sessionId);
    await call("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }, { name: "prefers-color-scheme", value: "light" }] }, sessionId);
    await call("Page.navigate", { url }, sessionId);
    await wait(sessionId, "!!document.querySelector('#login-form')");
    return sessionId;
  };
  const login = async (page, username) => {
    await evaluate(page, `(() => { const form = document.querySelector('#login-form'); form.elements.username.value=${JSON.stringify(username)}; form.elements.token.value='config scope password fixture'; form.requestSubmit(); })()`);
    await wait(page, "!!document.querySelector('#logout')");
    return evaluate(page, "fetch('/api/v1/auth/session').then(r => r.json())");
  };
  const api = async (page, session, method, path, body, status = 200) => {
    const result = await evaluate(page, `(async () => { const r = await fetch('/api/v1' + ${JSON.stringify(path)}, {method:${JSON.stringify(method)}, credentials:'same-origin', headers:{'Content-Type':'application/json','X-QControlHub-CSRF':${JSON.stringify(session.csrf_token)}}, ${body === undefined ? "" : `body:JSON.stringify(${JSON.stringify(body)})`} }); return {status:r.status, body:await r.text()}; })()`);
    assert.equal(result.status, status, `${method} ${path}: ${result.body}`);
    return result.body ? JSON.parse(result.body) : null;
  };
  const screenshot = async (page, label) => {
    if (!artifacts) return;
    await mkdir(resolve(artifacts), { recursive: true });
    const { data } = await call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }, page);
    await writeFile(join(resolve(artifacts), `${mode}-${label}.png`), Buffer.from(data, "base64"));
  };

  const [a, b, manager] = await Promise.all([createPage(), createPage(), createPage()]);
  const [alice, bob, admin] = await Promise.all([login(a, "alice"), login(b, "bob"), login(manager, "browser-admin")]);
  assert.equal(alice.role, "user"); assert.equal(bob.role, "user"); assert.equal(admin.role, "admin");
  assert.notEqual(alice.user_id, bob.user_id);
  record(`${mode}: three independent browser contexts, real password/cookie/CSRF identities`);
  for (const [user, route, selector] of [["task-reader", "tasks", "[data-task-page]"], ["traffic-reader", "traffic", ".traffic-workspace"]]) {
    const page = await createPage();
    await login(page, user);
    await evaluate(page, `location.hash=${JSON.stringify("#" + route)}`);
    await wait(page, `!!document.querySelector(${JSON.stringify(selector)})`);
    assert.ok(await evaluate(page, "!document.querySelector('.workspace-main .alert.error')"));
    assert.ok(await evaluate(page, "document.documentElement.scrollWidth <= innerWidth + 1"));
    assert.equal(await evaluate(page, "document.querySelector('[data-sync-label]').textContent"), user === "traffic-reader" ? "1 个节点在线" : "节点状态未知");
    await screenshot(page, user);
    await evaluate(page, "location.hash='#certificates'");
    await wait(page, "!!document.querySelector('[data-cert-accounts]')");
    await evaluate(page, "document.querySelector('[data-cert-accounts]').click()");
    await wait(page, "!!document.querySelector('dialog[open] [data-cert-http-grant]')");
    const nodes = await evaluate(page, "[...document.querySelectorAll('dialog[open] select[name=agent_id] option')].map(option=>option.value).filter(Boolean)");
    assert.deepEqual(nodes, [fixture.owned[user]], "certificate HTTP authorization must use the owned-node scope without agents.read");
    await evaluate(page, "document.querySelector('[data-cert-close]').click()");
    // Delay the real response rather than replacing its authorization/data.
    await evaluate(page, `(() => { const original=window.fetch; window.__certificateRelease=null; window.__certificateWaiting=false; window.fetch=async(...args)=>{const response=await original(...args);if(String(args[0]).endsWith('/certificate-http-nodes')){window.__certificateWaiting=true;await new Promise(done=>window.__certificateRelease=done);}return response;};document.querySelector('[data-cert-accounts]').click();})()`);
    await wait(page, "window.__certificateWaiting");
    await evaluate(page, `location.hash=${JSON.stringify("#" + route)}`);
    await wait(page, `!!document.querySelector(${JSON.stringify(selector)})`);
    await evaluate(page, "window.__certificateRelease()");
    await delay(100);
    assert.equal(await evaluate(page, "document.querySelectorAll('dialog[open]').length"), 0, "late certificate responses cannot reopen a dialog on another route");
  }
  record(`${mode}: task-only and traffic-only pages and owned certificate HTTP nodes work without agents.read/settings.read; delayed certificate dialogs stay closed after navigation`);
  const noPermissions = await createPage();
  await login(noPermissions, "no-permissions");
  assert.equal(await evaluate(noPermissions, "document.querySelectorAll('.dock-nav a[href=\"#live-config\"]').length"), 0);
  await evaluate(noPermissions, "location.hash='#live-config'");
  await wait(noPermissions, "document.querySelector('.workspace-main .alert.error')?.textContent.includes('无权查看配置')");
  assert.ok(await evaluate(noPermissions, "!document.querySelector('.live-config-empty')"), "permission denial must not look like a resource-free account");
  const stalePage = await createPage();
  const staleSession = await login(stalePage, "alice");
  // The cookie belongs to B after another tab logs in, while this page still
  // holds A's session and preferences. Its next navigation must clear the UI.
  await api(stalePage, staleSession, "POST", "/auth/login", { username: "bob", token: "config scope password fixture" });
  await evaluate(stalePage, "location.hash='#client-access'");
  await wait(stalePage, "!!document.querySelector('#login-form')");
  assert.equal((await evaluate(stalePage, "fetch('/api/v1/auth/session').then(r=>r.json())")).user_id, bob.user_id);
  record(`${mode}: replacement cookie cannot populate the old account's page; B's session remains valid`);
  for (const [page, session, user, other] of [[a, alice, "alice", "bob"], [b, bob, "bob", "alice"]]) {
    let agents = await api(page, session, "GET", "/agents");
    assert.deepEqual(agents.map(agent => agent.id), [fixture.owned[user]]);
    await api(page, session, "GET", `/agents/${fixture.owned[other]}/configs`, undefined, 404);
    await api(page, session, "GET", `/agents/${fixture.shared}/configs`, undefined, 404);
    const access = await api(page, session, "GET", "/agent-access");
    const share = access.shares[0]; assert.equal(share.status, "pending");
    await api(page, session, "POST", `/agent-access/${share.id}/response`, { revision: share.invitation_revision, decision: "accept" });
    agents = await api(page, session, "GET", "/agents");
    assert.ok(agents.some(agent => agent.id === fixture.shared && !agent.can_manage));
    await evaluate(page, "location.hash='#node-settings'");
    await wait(page, "document.querySelectorAll('[data-agent-node]').length===2");
    assert.equal(await evaluate(page, "document.querySelectorAll('[data-node-batch-toggle]').length"), 0,
      "one owned node plus a shared instance must not expose a host batch entry");
  }
  record(`${mode}: owned nodes isolated; pending shares denied, accepted shares visible without host management`);
  const archiveInput = { name: "private archive", engine: "mihomo", content: "mixed-port: 22001\n" };
  const ca = await api(a, alice, "POST", "/configs", archiveInput, 201);
  const cb = await api(b, bob, "POST", "/configs", archiveInput, 201);
  assert.notEqual(ca.id, cb.id);
  await api(b, bob, "GET", `/configs/${ca.id}/revisions`, undefined, 404);
  await api(b, bob, "PUT", `/configs/${ca.id}`, { ...ca, name: "foreign mutation" }, 404);
  await api(a, alice, "DELETE", `/configs/${cb.id}`, undefined, 404);
  assert.deepEqual((await api(a, alice, "GET", "/configs")).map(config => config.id), [ca.id]);
  assert.deepEqual((await api(b, bob, "GET", "/configs")).map(config => config.id), [cb.id]);
  record(`${mode}: symmetric A/B archives, foreign revisions/update/delete rejected with no side effects`);
  const configA = await api(a, alice, "PUT", `/agents/${fixture.shared}/configs/mihomo`, { name: "A workspace", content: fixture.contentA, version: 0 });
  const configB = await api(b, bob, "PUT", `/agents/${fixture.shared}/configs/mihomo`, { name: "B workspace", content: fixture.contentB, version: 0 });
  assert.notEqual(configA.id, configB.id);
  const request = config => ({ agent_id: fixture.shared, engine: "mihomo", config_id: config.id, action: "deploy", expected_config_version: config.version });
  const taskA = await api(a, alice, "POST", "/tasks", request(configA), 201);
  const taskB = await api(b, bob, "POST", "/tasks", request(configB), 201);
  assert.equal((await api(b, bob, "POST", "/tasks", request(configB))).id, taskB.id);
  await api(a, alice, "GET", `/tasks/${taskB.id}`, undefined, 404);
  await api(b, bob, "DELETE", `/tasks/${taskA.id}`, undefined, 404);
  await api(a, alice, "DELETE", `/tasks/${taskA.id}`, undefined, 204);
  const nextA = await api(a, alice, "PUT", `/agents/${fixture.shared}/configs/mihomo`, { ...configA, content: fixture.contentA + "\n# edited by A\n" });
  record(`${mode}: private workspaces and tasks; duplicate submit reused B task; foreign task access/cancel denied`);

  if (baseline) {
    const before = await createPage(baseline);
    await login(before, "alice");
    await evaluate(before, "location.hash='#tasks'");
    await wait(before, `!!document.querySelector('[data-retry="${taskA.id}"]')`);
    await evaluate(before, `document.querySelector('[data-retry="${taskA.id}"]').click()`);
    await wait(before, "!!document.querySelector('[data-confirm-dialog][open]')");
    assert.ok(await evaluate(before, "document.querySelector('[data-confirm-message]').textContent.includes('当前配置')"));
    await screenshot(before, "before");
  }
  await evaluate(a, "location.hash='#tasks'");
  await wait(a, `!!document.querySelector('[data-retry="${taskA.id}"]')`);
  assert.equal(await evaluate(a, `document.querySelector('[data-retry="${taskA.id}"]').textContent`), "重试配置 v1");
  await evaluate(a, `document.querySelector('[data-retry="${taskA.id}"]').click()`);
  await wait(a, "!!document.querySelector('[data-confirm-dialog][open]')");
  assert.ok(await evaluate(a, "document.querySelector('[data-confirm-message]').textContent.includes('配置已有新版本')"));
  assert.ok(await evaluate(a, "!document.querySelector('[data-confirm-message]').textContent.includes('安装稳定版')"));
  await screenshot(a, "after");
  await evaluate(a, "document.querySelector('[data-confirm-accept]').click()");
  await wait(a, "document.body.textContent.includes('保存后配置已发生变化')");
  assert.ok(await evaluate(a, "document.documentElement.scrollWidth <= innerWidth + 1"));
  await api(a, alice, "POST", `/tasks/${taskA.id}/retry`, {}, 409);
  const pendingA = await api(a, alice, "POST", "/tasks", request(nextA), 201);
  record(`${mode}: version-labelled UI confirmation; stale retry returned 409; explicit new-version task accepted`);

  let sharing = await api(manager, admin, "GET", `/agents/${fixture.shared}/sharing`);
  await api(manager, admin, "PUT", `/agents/${fixture.shared}/sharing`, { revision: sharing.revision, shares: sharing.shares.map(share => ({ username: share.username, engines: share.engines, ports: share.ports, limit_bytes: share.limit_bytes, enabled: share.user_id !== alice.user_id })) });
  await api(a, alice, "GET", `/agents/${fixture.shared}/configs`, undefined, 404);
  assert.ok(!(await api(a, alice, "GET", "/agents")).some(agent => agent.id === fixture.shared));
  assert.equal((await api(manager, admin, "GET", `/tasks/${pendingA.id}`)).status, "canceled");
  assert.equal((await api(b, bob, "GET", `/tasks/${taskB.id}`)).status, "pending");
  await api(manager, admin, "DELETE", `/users/${alice.user_id}`, undefined, 204);
  await api(a, alice, "GET", "/auth/session", undefined, 401);
  await api(b, bob, "GET", "/auth/session");
  await reload(a);
  await wait(a, "!!document.querySelector('#login-form')");
  const switched = await login(a, "bob");
  assert.equal(switched.user_id, bob.user_id);
  assert.deepEqual((await api(a, switched, "GET", "/configs")).map(config => config.id), [cb.id]);
  record(`${mode}: A share revocation canceled queued work; B session/task stayed valid; disabled A session denied; same-context switch to B isolated`);
  if (artifacts) await writeFile(join(resolve(artifacts), `${mode}-record.txt`), report.join("\n") + "\n");
} finally {
  clearTimeout(timer);
  socket?.close();
  child.kill("SIGKILL");
  await new Promise(resolve => child.exitCode !== null || child.signalCode !== null ? resolve() : child.once("exit", resolve));
  await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}
