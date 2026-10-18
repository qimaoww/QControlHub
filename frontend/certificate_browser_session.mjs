// Real Chromium contexts against isolated PostgreSQL and password/CSRF sessions.
// Follow the repository's dependency-free native CDP browser convention.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const [base, mode, baseline] = process.argv.slice(2);
const fixture = JSON.parse(process.env.QCH_CERTIFICATE_FIXTURE);
const artifacts = process.env.QCH_CERTIFICATE_ARTIFACT_DIR;
const profile = await mkdtemp(join(tmpdir(), "qch-certificates-"));
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


  const a=await createPage(), b=await createPage();
  const alice=await login(a,"alice"), bob=await login(b,"bob");
  const click=async(page,selector)=>evaluate(page,`document.querySelector(${JSON.stringify(selector)}).click()`);
  const set=async(page,selector,value)=>evaluate(page,`(() => {const field=document.querySelector(${JSON.stringify(selector)});field.value=${JSON.stringify(value)};field.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  const open=async(page)=>{await evaluate(page,"location.hash='#certificates'");await wait(page,"!!document.querySelector('[data-cert-create]')")};
  await open(a);
  assert.equal(await evaluate(a,"document.querySelectorAll('.cert-summary').length"),0);
  assert.equal(await evaluate(a,"document.querySelectorAll('.cert-card,.context-steps').length"),0);
  await click(a,"[data-cert-accounts]");await wait(a,"!!document.querySelector('[data-cert-new-dns]')");await click(a,"[data-cert-new-dns]");await wait(a,"!!document.querySelector('[data-cert-account-form]')");
  await set(a,'[name=name]','Alice DNS');await set(a,'[name=zone]','example.test');await set(a,'[name=token]','local-browser-fixture-token');
  await evaluate(a,"document.querySelector('[data-cert-account-form]').requestSubmit()");
  await wait(a,"!!document.querySelector('[data-cert-new-dns]')");
  assert.equal((await api(a,alice,'GET','/certificate-accounts')).length,1);
  assert.deepEqual(await api(b,bob,'GET','/certificate-accounts'),[]);
  await click(a,'[data-cert-close]');await click(a,'[data-cert-create]');await wait(a,"!!document.querySelector('[name=source]')");await set(a,'[name=source]','self-signed');await wait(a,"!!document.querySelector('[name=days]')");
  await set(a,'[name=name]','Alice UI TLS');await set(a,'[name=domains]','edge.example.test');await evaluate(a,"document.querySelector('[data-cert-form]').requestSubmit()");
  await wait(a,"!!document.querySelector('[data-cert-share]')");
  const cert=(await api(a,alice,'GET','/certificates')).items[0];assert.equal(cert.version,1);assert.equal(cert.owner_id,alice.user_id);
  await api(b,bob,'GET',`/certificates/${cert.id}`,undefined,404);
  await click(a,'[data-cert-share]');await wait(a,"!!document.querySelector('[data-cert-grant-form]')");await set(a,'[name=username]','bob');await evaluate(a,"document.querySelector('[data-cert-grant-form]').requestSubmit()");await wait(a,"!!document.querySelector('[data-cert-revoke]')");
  await open(b);await wait(b,`!!document.querySelector('[data-cert-detail="${cert.id}"]')`);
  assert.equal((await api(b,bob,'GET','/certificates')).items[0].owned,false);
  await evaluate(b,`location.hash='#live-config?agent=${fixture.shared}&engine=mihomo'`);await wait(b,"Array.from(document.querySelectorAll('button')).some(b=>b.textContent==='＋ 增加入站')");
  await evaluate(b,"Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='＋ 增加入站').click()");await wait(b,"!!document.querySelector('.config-inbound-dialog select')");
  await evaluate(b,"(() => {const s=document.querySelector('.config-inbound-dialog select');s.value='trojan';s.dispatchEvent(new Event('change',{bubbles:true}));})()");
  await wait(b,"!!document.querySelector('[name=panel_certificate_id]')");
  assert.equal(await evaluate(b,`document.querySelector('[name=panel_certificate_id] option[value="${cert.id}"]').parentElement.label`),'授权给我的证书');
  const localPath=await evaluate(b,"document.querySelector('[name=certificate_path]').value");
  await set(b,'[name=panel_certificate_id]',cert.id);
  assert.equal(await evaluate(b,"document.querySelector('[name=certificate_path]').closest('label').hidden"),true);
  await set(b,'[name=panel_certificate_id]','');
  assert.equal(await evaluate(b,"document.querySelector('[name=certificate_path]').value"),localPath);
  assert.equal(await evaluate(b,"document.querySelector('[name=certificate_path]').closest('label').hidden"),false);
  await set(b,'[name=panel_certificate_id]',cert.id);
  assert.equal(await evaluate(b,"document.querySelector('[name=certificate_path]').value"),`/etc/qcontrolhub/panel-certificates/${cert.id}/cert.pem`);
  await evaluate(b,"document.querySelector('[data-builder-step=security]')?.click()");
  assert.ok(await evaluate(b,"document.documentElement.scrollWidth <= innerWidth+1"));
  record(`${mode}: real UI DNS-account save, self-signed issuance, owner isolation, UI share, authorized protocol selector and protected path markers passed`);
  await screenshot(b,'certificate-selector');

  // An already-open recipient detail must lose its stale content when the
  // owner revokes access; the read poll must distinguish 404 from outages.
  const c=await createPage(), recipient=await login(c,'bob');
  await open(c);await click(c,`[data-cert-detail="${cert.id}"]`);
  await wait(c,`!!document.querySelector('.cert-dialog[data-cert-detail-id="${cert.id}"]')`);
  await api(a,alice,'DELETE',`/certificates/${cert.id}/grants`,{username:'bob'},204);
  await wait(c,`!document.querySelector('.cert-dialog') && !document.querySelector('[data-cert-detail="${cert.id}"]')`);
  await api(c,recipient,'GET',`/certificates/${cert.id}`,undefined,404);
  await api(a,alice,'POST',`/certificates/${cert.id}/grants`,{username:'bob'},204);
  record(`${mode}: revoked certificate detail and list disappear without navigation or manual refresh`);

  await click(c,'[data-cert-create]');await wait(c,"!!document.querySelector('[data-cert-form]')");
  await evaluate(c,`(() => {
    const original=window.fetch;window.__certRestoreFetch=()=>{window.fetch=original};
    const gate=new Promise(resolve=>{window.__certRelease=resolve});
    window.fetch=async(...args)=>{const response=await original(...args);if(String(args[0]).endsWith('/certificate-http-nodes')){window.__certBlocked=true;await gate}return response};
  })()`);
  await set(c,'[name=source]','self-signed');await wait(c,'window.__certBlocked');
  for(const type of ['keyDown','keyUp'])await call('Input.dispatchKeyEvent',{type,key:'Escape',code:'Escape',windowsVirtualKeyCode:27},c);
  await wait(c,"!document.querySelector('.cert-dialog')");
  await evaluate(c,'window.__certRestoreFetch();window.__certRelease()');await delay(500);
  assert.equal(await evaluate(c,"document.querySelectorAll('.cert-dialog').length"),0);
  record(`${mode}: Escape closes a certificate form and invalidates its delayed real resource response`);

  // Reuse the same document and installed route module across real logins.
  await click(a,'[data-cert-close]');
  await set(a,'[data-cert-search]','alice-private.example.test');
  await wait(a,"document.querySelector('[data-cert-search]')?.value==='alice-private.example.test'");
  await click(a,'#logout');await wait(a,"!!document.querySelector('#login-form')");
  assert.equal(await evaluate(a,"document.querySelectorAll('.cert-dialog').length"),0);
  const switched=await login(a,'bob');await open(a);
  assert.equal(await evaluate(a,"document.querySelector('[data-cert-search]').value"),'');
  await wait(a,`!!document.querySelector('[data-cert-detail="${cert.id}"]')`);
  await click(a,'[data-cert-accounts]');await wait(a,"!!document.querySelector('[data-cert-new-dns]')");
  assert.equal(await evaluate(a,"document.querySelector('.cert-dialog').textContent.includes('Alice DNS')"),false);
  assert.deepEqual(await api(a,switched,'GET','/certificate-accounts'),[]);
  record(`${mode}: same-browser logout/login clears private filters, dialogs and credential-account state`);

} finally {
  clearTimeout(timer);
  socket?.close();
  child.kill("SIGKILL");
  await new Promise(resolve => child.exitCode !== null || child.signalCode !== null ? resolve() : child.once("exit", resolve));
  await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}
