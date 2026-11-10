// Real Chromium contexts against isolated PostgreSQL and password/CSRF sessions.
// Follow the repository's dependency-free native CDP browser convention.
import assert from "node:assert/strict";
import { runStructureLoadingBrowser } from './structure_loading_browser.mjs';
import { runAnalyticsBrowser } from "./analytics_browser_session.mjs";
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
const timer = setTimeout(() => child.kill("SIGKILL"), 120000);
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
  const reload = async (page, trigger = () => call("Page.reload", {}, page)) => {
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
      await Promise.all([trigger(), loaded]);
    } finally {
      clearTimeout(timeout);
      socket.removeEventListener("message", onMessage);
    }
  };
  const createPage = async (url = base, checkStartup = false) => {
    const { browserContextId } = await call("Target.createBrowserContext");
    const { targetId } = await call("Target.createTarget", { url: "about:blank", browserContextId });
    const { sessionId } = await call("Target.attachToTarget", { targetId, flatten: true });
    await call("Page.enable", {}, sessionId);
    await call("Emulation.setDeviceMetricsOverride", { width: mode === "mobile" ? 390 : 1440, height: mode === "mobile" ? 844 : 1000, deviceScaleFactor: 1, mobile: mode === "mobile" }, sessionId);
    if (mode === "mobile") await call("Emulation.setTouchEmulationEnabled", { enabled: true }, sessionId);
    await call("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }, { name: "prefers-color-scheme", value: "light" }] }, sessionId);
    let startupScript;
    if (checkStartup) {
      // Hold the real session request to inspect the entry before either theme
      // mounts. Releasing it continues the normal authenticated bootstrap.
      startupScript = await call("Page.addScriptToEvaluateOnNewDocument", {source: `
        const originalFetch = window.fetch;
        const startupGate = new Promise(resolve => window.__releaseStartup = resolve);
        window.fetch = (...args) => String(args[0]).endsWith('/auth/session')
          ? startupGate.then(() => originalFetch(...args)) : originalFetch(...args);
      `}, sessionId);
    }
    await call("Page.navigate", { url }, sessionId);
    if (checkStartup) {
      await wait(sessionId, "!!window.__releaseStartup && !!document.querySelector('#qch-startup-style')?.sheet");
      await wait(sessionId, `(() => {
        const screen = document.querySelector('.qch-startup'), box = screen?.getBoundingClientRect();
        return box && getComputedStyle(screen).position === 'fixed' && box.width === innerWidth && box.height === innerHeight && !document.getAnimations().length;
      })()`);
      assert.ok(await evaluate(sessionId, `(() => {
        const screen = document.querySelector('.qch-startup'), box = screen.getBoundingClientRect();
        return getComputedStyle(screen).position === 'fixed' && box.width === innerWidth && box.height === innerHeight
          && !document.getAnimations().length && !!document.querySelector('[role=status]');
      })()`), "entry fills the viewport and remains clear with reduced motion");
      await screenshot(sessionId, "startup-loading");
      await call("Emulation.setEmulatedMedia", {features:[{name:"prefers-reduced-motion",value:"no-preference"}]}, sessionId);
      await wait(sessionId, "document.getAnimations().length===2 && document.getAnimations().every(a=>a.playState==='running')");
      await call("Page.removeScriptToEvaluateOnNewDocument", {identifier:startupScript.identifier}, sessionId);
      await evaluate(sessionId, "window.__releaseStartup()");
    }
    await wait(sessionId, "!!document.querySelector('#login-form,.a-login form')");
    if (checkStartup) {
      await wait(sessionId, "!document.querySelector('.qch-startup,#qch-startup-style')");
      assert.ok(await evaluate(sessionId, "!document.getAnimations().some(a=>a.animationName?.startsWith('qch-startup'))"), "startup animations and stylesheet are released after theme mount");
      await call("Emulation.setEmulatedMedia", {features:[{name:"prefers-reduced-motion",value:"reduce"},{name:"prefers-color-scheme",value:"light"}]}, sessionId);
    }
    return sessionId;
  };
  const login = async (page, username) => {
    await evaluate(page, `(() => { const form = document.querySelector('#login-form,.a-login form'); form.elements.username.value=${JSON.stringify(username)}; form.elements.token.value='config scope password fixture'; form.requestSubmit(); })()`);
    await wait(page, "!!document.querySelector('#logout,.a-user')");
    const identity = await evaluate(page, "fetch('/api/v1/auth/session').then(r => r.json())");
    // The shell mounts before the login mutation finishes its initial read.
    // Wait for that read before navigation can pass the submission guard.
    await wait(page, "!!document.querySelector('.a-user') && !document.querySelector('[aria-busy=true]')");
    return identity;
  };
  const api = async (page, session, method, path, body, status = 200) => {
    const result = await evaluate(page, `(async () => { const r = await fetch('/api/v1' + ${JSON.stringify(path)}, {method:${JSON.stringify(method)}, credentials:'same-origin', headers:{'Content-Type':'application/json','X-QControlHub-CSRF':${JSON.stringify(session.csrf_token)}}, ${body === undefined ? "" : `body:JSON.stringify(${JSON.stringify(body)})`} }); return {status:r.status, body:await r.text()}; })()`);
    assert.equal(result.status, status, `${method} ${path}: ${result.body}`);
    return result.body ? JSON.parse(result.body) : null;
  };
  const screenshot = async (page, label) => {
    if (!artifacts) return;
    await mkdir(resolve(artifacts), { recursive: true });
    // Redact generated fixture credentials only for evidence, without input
    // events or changes to the draft/baseline used by the behavioral checks.
    const redact = () => {
      window.__evidenceFields = [];
      for (const field of document.querySelectorAll('textarea,input')) {
        const value = field.value;
        const clean = value.replace(/((?:["']?(?:password|private_key|token|credential|secret)["']?)\s*[:=]\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^\r\n,}]+)/gi, '$1[测试凭据已隐藏]');
        if (clean !== value) { window.__evidenceFields.push([field,value]); field.value = clean; }
      }
    };
    await evaluate(page, `(${redact.toString()})()`);
    try {
      const { data } = await call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }, page);
      await writeFile(join(resolve(artifacts), `${mode}-${label}.png`), Buffer.from(data, "base64"));
    } finally {
      await evaluate(page, `window.__evidenceFields.forEach(([field,value])=>{field.value=value});delete window.__evidenceFields`);
    }
  };

  // No preference row: use the production entry without a preview hint.
  const defaultPage = await createPage(`${base}/?theme=classic#themes`, true);
  assert.ok(await evaluate(defaultPage, "!!document.querySelector('.a-login form') && !new URL(location.href).searchParams.has('theme')"));
  await evaluate(defaultPage, "window.__defaultDocument=true");
  const defaultLogin = async () => {
    await evaluate(defaultPage, `(() => {const form=document.querySelector('.a-login form');form.elements.username.value='default-theme-user';form.elements.token.value='config scope password fixture';form.requestSubmit()})()`);
    await wait(defaultPage, "document.querySelector('.a-top h1')?.textContent==='主题' && !document.querySelector('[aria-busy=true]')");
  };
  await defaultLogin();
  let defaultIdentity = await evaluate(defaultPage, "fetch('/api/v1/auth/session').then(r=>r.json())");
  assert.deepEqual(await api(defaultPage, defaultIdentity, "GET", "/preferences/theme"), {theme_id:"analytics",revision:0});
  assert.equal(await evaluate(defaultPage, "window.__defaultDocument"), true, "matching default login keeps the document and avoids a reload prompt");
  await screenshot(defaultPage, "default-theme-account");
  await reload(defaultPage);
  await wait(defaultPage, "!!document.querySelector('.a-top h1') && !document.querySelector('[aria-busy=true]')");
  await evaluate(defaultPage, "document.querySelector('.a-user [title=退出登录]').click()");
  await wait(defaultPage, "!!document.querySelector('.a-login form')");
  await defaultLogin();
  defaultIdentity = await evaluate(defaultPage, "fetch('/api/v1/auth/session').then(r=>r.json())");
  assert.deepEqual(await api(defaultPage, defaultIdentity, "GET", "/preferences/theme"), {theme_id:"analytics",revision:0});
  // A failed stylesheet must recover without changing the stored preference.
  await call("Network.enable", {}, defaultPage);
  await call("Network.setBlockedURLs", {urls:["*analytics/theme.css*"]}, defaultPage);
  await reload(defaultPage);
  await wait(defaultPage, "!!document.querySelector('.qch-startup[data-failed] button')");
  assert.ok(await evaluate(defaultPage, "document.querySelector('.qch-startup [role=alert]').textContent.includes('加载失败')"));
  assert.equal((await api(defaultPage, defaultIdentity, "GET", "/preferences/theme")).theme_id, "analytics");
  assert.equal(await evaluate(defaultPage, "document.querySelectorAll('link[href*=\"analytics/theme.css\"]').length"), 0, "failed theme styles are removed");
  await call("Network.setBlockedURLs", {urls:[]}, defaultPage);
  await reload(defaultPage);
  await wait(defaultPage, "!!document.querySelector('.a-top h1') && !document.querySelector('[aria-busy=true]')");
  // If neither runtime loads, keep an accessible entry and a working retry.
  await call("Network.setBlockedURLs", {urls:["*/analytics/app.js*", "*/assets/app.js*"]}, defaultPage);
  await reload(defaultPage);
  await wait(defaultPage, "!!document.querySelector('.qch-startup[data-failed] button')");
  assert.ok(await evaluate(defaultPage, "!!document.querySelector('.qch-startup [role=alert]') && !document.querySelector('.qch-startup[aria-busy]') && !document.getAnimations().length"));
  await screenshot(defaultPage, "startup-failure");
  await call("Input.dispatchKeyEvent", {type:"keyDown",key:"Tab",code:"Tab",windowsVirtualKeyCode:9}, defaultPage);
  await call("Input.dispatchKeyEvent", {type:"keyUp",key:"Tab",code:"Tab",windowsVirtualKeyCode:9}, defaultPage);
  assert.ok(await evaluate(defaultPage, "document.activeElement.matches('.qch-startup-retry')"));
  await call("Network.setBlockedURLs", {urls:[]}, defaultPage);
  await reload(defaultPage, async () => {
    await call("Input.dispatchKeyEvent", {type:"keyDown",key:"Enter",code:"Enter",windowsVirtualKeyCode:13,text:"\r"}, defaultPage);
    await call("Input.dispatchKeyEvent", {type:"keyUp",key:"Enter",code:"Enter",windowsVirtualKeyCode:13}, defaultPage);
  });
  await wait(defaultPage, "!!document.querySelector('.a-top h1') && !document.querySelector('[aria-busy=true],#qch-startup-style')");
  record(`${mode}: styled startup, reduced motion, animation cleanup, resource failure and keyboard retry`);
  record(`${mode}: default analytics login, URL independence, account revision zero, refresh/relogin and failed-resource recovery through the production entry`);

  await runStructureLoadingBrowser({ base, mode, createPage, login, call, evaluate, wait, screenshot, record });

  await runAnalyticsBrowser({ base, mode, fixture, createPage, login, call, evaluate, wait, screenshot, api, record });

  const [a, b, manager] = await Promise.all([createPage(), createPage(), createPage()]);
  const [alice, bob, admin] = await Promise.all([login(a, "alice"), login(b, "bob"), login(manager, "browser-admin")]);
  assert.equal(alice.role, "user"); assert.equal(bob.role, "user"); assert.equal(admin.role, "admin");
  assert.notEqual(alice.user_id, bob.user_id);
  record(`${mode}: three independent browser contexts, real password/cookie/CSRF identities`);
  // Every account has the sole runtime; saved choices remain account scoped.
  for (const [page, identity] of [[a, alice], [manager, admin]]) {
    await evaluate(page, "location.hash='#themes'");
    await wait(page, "!!document.querySelector('.a-top h1') && !document.querySelector('[aria-busy=true]')");
    const preference = await api(page, identity, "GET", "/preferences/theme");
    assert.equal(preference.theme_id, "analytics");
    await api(page, identity, "PUT", "/preferences/theme", {theme_id:"analytics",revision:preference.revision});
    await api(page, identity, "PUT", "/preferences/theme", {theme_id:"analytics",revision:preference.revision}, 409);
    assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"));
  }
  assert.equal((await api(b, bob, "GET", "/preferences/theme")).revision, 0, "another account preference is unchanged");
  record(`${mode}: sole theme, optimistic conflict and account isolation`);
  for (const [user, route, selector] of [["task-reader", "tasks", ".a-main table"], ["traffic-reader", "traffic", ".a-traffic-page"]]) {
    const page = await createPage();
    await login(page, user);
    await evaluate(page, `location.hash=${JSON.stringify("#" + route)}`);
    await wait(page, `document.querySelector('.a-top h1')?.textContent===${JSON.stringify(route === "tasks" ? "任务" : "流量")} && !document.querySelector('[aria-busy=true]')`);
    assert.ok(await evaluate(page, "![...document.querySelectorAll('.a-main [role=alert]')].some(el=>el.textContent.trim())"));
    assert.ok(await evaluate(page, "document.documentElement.scrollWidth <= innerWidth + 1"));
    if (user === "traffic-reader") {
      const scoped = await api(page, await evaluate(page, "fetch('/api/v1/auth/session').then(r=>r.json())"), "GET", "/traffic-policies?view=page");
      assert.equal(scoped.agents.length, 1, "traffic permission includes authorized node metadata");
    }
    await screenshot(page, user);
    await evaluate(page, "location.hash='#certificates'");
    await wait(page, "[...document.querySelectorAll('.a-main button')].some(el=>el.textContent==='账号与认证授权') && !document.querySelector('[aria-busy=true]')");
    await evaluate(page, "[...document.querySelectorAll('.a-main button')].find(el=>el.textContent==='账号与认证授权').click()");
    await wait(page, "!!document.querySelector('dialog[open] select[name=agent_id]') && !document.querySelector('dialog[open] [aria-busy=true]')");
    const nodes = await evaluate(page, "[...document.querySelectorAll('dialog[open] select[name=agent_id] option')].map(option=>option.value).filter(Boolean)");
    assert.deepEqual(nodes, [fixture.owned[user]], "certificate HTTP authorization must use the owned-node scope without agents.read");
    await evaluate(page, "document.querySelector('dialog[open] [aria-label=关闭]').click()");
    // Delay the real response rather than replacing its authorization/data.
    await evaluate(page, `(() => { const original=window.fetch; window.__certificateRelease=null; window.__certificateWaiting=false; window.fetch=async(...args)=>{const response=await original(...args);if(String(args[0]).endsWith('/certificate-http-nodes')){window.__certificateWaiting=true;await new Promise(done=>window.__certificateRelease=done);}return response;};[...document.querySelectorAll('.a-main button')].find(el=>el.textContent==='账号与认证授权').click();})()`);
    await wait(page, "window.__certificateWaiting");
    await evaluate(page, `location.hash=${JSON.stringify("#" + route)}`);
    await wait(page, `document.querySelector('.a-top h1')?.textContent===${JSON.stringify(route === "tasks" ? "任务" : "流量")} && !document.querySelector('[aria-busy=true]')`);
    await evaluate(page, "window.__certificateRelease()");
    await delay(100);
    assert.equal(await evaluate(page, "document.querySelectorAll('dialog[open]').length"), 0, "late certificate responses cannot reopen a dialog on another route");
    if (user === "traffic-reader") {
      await call("Page.navigate", { url: `${base}/assets/testdata/analytics-runtime.html#traffic` }, page);
      await wait(page, "!!document.querySelector('.a-traffic-page [data-traffic-key]') && !document.querySelector('[aria-busy=true]')");
      assert.deepEqual(await evaluate(page, "[...document.querySelectorAll('.a-traffic-filters [name=agent] option')].map(option=>option.value).filter(Boolean)"), [fixture.owned[user]], "traffic-only account receives authorized node metadata without agents.read");
      assert.equal(await evaluate(page, "[...document.querySelectorAll('.a-main button')].filter(button=>['添加配额','编辑配额','设置配额','清零','删除','同步配置端口','批量操作'].includes(button.textContent)).length"), 0, "read-only traffic identity has no mutation controls");
      await screenshot(page, "analytics-traffic-reader");
    }
  }
  record(`${mode}: task-only and traffic-only pages and owned certificate HTTP nodes work without agents.read/settings.read; delayed certificate dialogs stay closed after navigation`);
  const noPermissions = await createPage();
  await login(noPermissions, "no-permissions");
  await evaluate(noPermissions, "location.hash='#themes'");
  await wait(noPermissions, "!!document.querySelector('.a-top h1') && !document.querySelector('[aria-busy=true]')");
  assert.ok(await evaluate(noPermissions, "![...document.querySelectorAll('.a-main [role=alert]')].some(el=>el.textContent.trim())"));
  assert.equal(await evaluate(noPermissions, "document.querySelectorAll('.a-rail a[href=\"#live-config\"]').length"), 0);
  await evaluate(noPermissions, "location.hash='#live-config'");
  await wait(noPermissions, "document.querySelector('.a-main [role=alert]')?.textContent.includes('没有访问此页面的权限')");
  assert.ok(await evaluate(noPermissions, "!document.querySelector('.a-main form')"), "permission denial must not look like a resource-free account");
  const stalePage = await createPage();
  const staleSession = await login(stalePage, "alice");
  // The cookie belongs to B after another tab logs in, while this page still
  // holds A's session and preferences. Its next navigation must clear the UI.
  await api(stalePage, staleSession, "POST", "/auth/login", { username: "bob", token: "config scope password fixture" });
  await evaluate(stalePage, "location.hash='#client-access'");
  await wait(stalePage, "!!document.querySelector('#login-form,.a-login form')");
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
    await wait(page, "document.querySelectorAll('[data-agent-id]').length===2");
    assert.equal(await evaluate(page, "[...document.querySelectorAll('[data-agent-id]')].find(el=>el.textContent.includes('本人共享实例'))?.querySelectorAll('[title=共享]').length || 0"), 0,
      "a shared instance never exposes host sharing management");
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
    await wait(before, "!!document.querySelector('dialog[open]')");
    assert.ok(await evaluate(before, "document.querySelector('dialog[open] .a-dialog-body').textContent.includes('当前配置')"));
    await screenshot(before, "before");
  }
  await evaluate(a, "location.hash='#tasks'");
  await wait(a, `!!document.querySelector('[data-retry="${taskA.id}"]')`);
  assert.equal(await evaluate(a, `document.querySelector('[data-retry="${taskA.id}"]').textContent`), "重试配置");
  await evaluate(a, `document.querySelector('[data-retry="${taskA.id}"]').click()`);
  await wait(a, "!!document.querySelector('dialog[open]')");
  assert.ok(await evaluate(a, "document.querySelector('dialog[open] .a-dialog-body').textContent.includes('配置已有更新')"));
  assert.ok(await evaluate(a, "!document.querySelector('dialog[open] .a-dialog-body').textContent.includes('安装稳定版')"));
  await screenshot(a, "after");
  await evaluate(a, "[...document.querySelectorAll('dialog[open] button')].find(el=>el.textContent==='重试任务').click()");
  await wait(a, "document.body.textContent.includes('保存后配置已发生变化')");
  assert.ok(await evaluate(a, "document.documentElement.scrollWidth <= innerWidth + 1"));
  await api(a, alice, "POST", `/tasks/${taskA.id}/retry`, {}, 409);
  const pendingA = await api(a, alice, "POST", "/tasks", request(nextA), 201);
  record(`${mode}: revision-free UI confirmation; stale retry returned 409; explicit updated-config task accepted`);

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
  await wait(a, "!!document.querySelector('#login-form,.a-login form')");
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
