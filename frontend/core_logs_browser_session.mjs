// Real Chromium contexts against isolated PostgreSQL and password/CSRF sessions.
// Follow the repository's dependency-free native CDP browser convention.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const [base, mode] = process.argv.slice(2);
const fixture = JSON.parse(process.env.QCH_CORE_LOG_FIXTURE);
const artifacts = process.env.QCH_CORE_LOG_ARTIFACT_DIR;
const profile = await mkdtemp(join(tmpdir(), "qch-core-log-"));
const child = spawn(process.env.QCH_CHROME_BIN || "chromium", ["--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
const report = [];
const record = message => { report.push(message); console.log(message); };
let socket;
const timer = setTimeout(() => child.kill("SIGKILL"), 90000);
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
    for (let attempt = 0; attempt < 300; attempt++) {
      try {
        const result = await evaluate(page, expression);
        if (result) return result;
      } catch (error) {
        // Only read-only DOM polling may resume across document navigation.
        if (!/Execution context was destroyed|Cannot find context|Inspected target navigated/.test(String(error))) throw error;
      }
      await delay(50);
    }
    throw new Error(`Browser condition timed out: ${expression}\n${JSON.stringify(await evaluate(page, "({text:document.body.innerText.slice(0,2000),storage:{...localStorage},options:[...document.querySelectorAll(\"[data-log-filters] select\")].map(el=>[el.name,el.value])})"))}`);
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
    await wait(sessionId, "!!document.querySelector('#login-form,.a-login form')");
    return sessionId;
  };
  const login = async (page, username) => {
    await evaluate(page, `(() => { const form = document.querySelector('#login-form,.a-login form'); form.elements.username.value=${JSON.stringify(username)}; form.elements.token.value='core logs evidence password'; form.requestSubmit(); })()`);
    await wait(page, "!!document.querySelector('#logout,.a-user')");
    const identity = await evaluate(page, "fetch('/api/v1/auth/session').then(r => r.json())");
    await wait(page, "!!document.querySelector('.a-user')");
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
    const viewport = await evaluate(page,"({width:innerWidth,height:innerHeight,deviceScaleFactor:devicePixelRatio})");
    const resize = viewport.width!==1920 || viewport.height!==1080 || viewport.deviceScaleFactor!==2;
    if (resize) await call("Emulation.setDeviceMetricsOverride", {width:1920,height:1080,deviceScaleFactor:2,mobile:false},page);
    await evaluate(page,"new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))");
    const { data } = await call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }, page);
    await writeFile(join(resolve(artifacts), `${mode}-${label}.png`), Buffer.from(data, "base64"));
    if (resize) await call("Emulation.setDeviceMetricsOverride", {...viewport,mobile:mode==="mobile"},page);
  };


  const [a, b, manager] = await Promise.all([createPage(), createPage(), createPage()]);
  const [alice, bob, admin] = await Promise.all([login(a, "alice"), login(b, "bob"), login(manager, "logs-admin")]);
  const host = fixture["logs-admin"], shareA = "shr_0123456789abcdef", shareB = "shr_1123456789abcdef";
  const resources = await Promise.all([[manager,admin], [a,alice], [b,bob]].map(([page,session])=>api(page,session,"GET","/core-log-sources")));
  assert.deepEqual([...new Set(resources[0].map(source=>source.agent_id))], [host]);
  assert.ok(resources[1].every(source=>source.agent_id===fixture.alice || source.agent_id===host && source.instance_id===shareA));
  assert.ok(resources[2].every(source=>source.agent_id===fixture.bob || source.agent_id===host && source.instance_id===shareB));
  await api(a,alice,"GET","/agents",undefined,403);
  record(`${mode}: independent admin/A/B password sessions; log access without agents.read; source metadata obeys node privacy`);
  for (const [page,session,node,other,ownShare,foreignShare] of [[a,alice,fixture.alice,fixture.bob,shareA,shareB],[b,bob,fixture.bob,fixture.alice,shareB,shareA]]) {
    const logs = await api(page,session,"GET","/core-logs");
    assert.ok(logs.every(log=>log.agent_id===node || log.agent_id===host && log.instance_id===ownShare));
    for(const path of [`?agent_id=${other}`,`?agent_id=${host}&instance_id=${foreignShare}`,`?user_id=${session===alice ? bob.user_id:alice.user_id}`,`?agent_id=${host}&instance_id=main`]) await api(page,session,"GET","/core-logs"+path,undefined,404);
    const window=await api(page,session,"GET",`/core-logs?agent_id=${host}&instance_id=${ownShare}&engine=xray&level=info&q=started&limit=1`);
    assert.equal(window.length,1); assert.equal(window[0].instance_id,ownShare);
    const history=await api(page,session,"GET",`/core-logs?agent_id=${host}&before=${window[0].id}&limit=1`);
    assert.ok(history.every(log=>log.instance_id===ownShare));
    await api(manager,admin,"GET",`/core-logs?agent_id=${node}`,undefined,404);
  }
  const adminLogs = await api(manager,admin,"GET","/core-logs");
  assert.ok(adminLogs.every(log=>log.agent_id===host));
  assert.ok([shareA,shareB,"main"].every(instance=>adminLogs.some(log=>log.instance_id===instance)));
  record(`${mode}: symmetric forged node/user/instance, engine/level/search, history pagination and admin private-node requests verified`);
  for (const page of [a,b,manager]) {
    await evaluate(page,"location.hash='#core-logs'");
    await wait(page,"!!document.querySelector('[data-log-filters] select[name=instance_id]') && document.querySelector('[data-log-refresh-label]')?.textContent==='正在实时更新'");
    assert.ok(await evaluate(page,"document.documentElement.scrollWidth<=innerWidth+1"));
    assert.ok(await evaluate(page,"[...document.querySelectorAll('[data-log-filters] select,[data-log-filters] input')].every(el=>el.getBoundingClientRect().right<=innerWidth+1)"));
  }
  const choose=async(page,field,value)=>evaluate(page,`(()=>{const select=document.querySelector('[data-log-filters] select[name=${field}]');select.value=${JSON.stringify(value)};select.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  const rows=page=>evaluate(page,"[...document.querySelectorAll('.a-log-message')].map(el=>el.textContent)");
  await choose(a,"agent_id",host);
  await wait(a,`document.querySelector('[data-log-filters] [name=agent_id]').value===${JSON.stringify(host)} && [...document.querySelectorAll('[data-log-filters] select[name=instance_id] option')].length===2 && document.querySelector('[data-log-entry]')`);
  assert.deepEqual(await evaluate(a,"[...document.querySelectorAll('[data-log-filters] select[name=instance_id] option')].map(option=>option.value)"),["",shareA]);
  assert.ok((await rows(a)).every(message=>message.startsWith("alice")));
  await screenshot(a,"ordinary-authorized");
  await evaluate(a,"[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='下一页').click()");
  await wait(a,"document.querySelector('.a-main').textContent.includes('第 2 / 2 页')");
  assert.ok((await rows(a)).every(message=>message.startsWith("alice")));
  await evaluate(a, `(() => {
    window.__historyRead=0;window.__historyFetch=window.fetch;window.fetch=async(...args)=>{const result=await window.__historyFetch(...args);if(String(args[0]).includes('/core-logs?'))window.__historyRead++;return result;};
    window.__historyRow=document.querySelector('[data-log-entry]');window.__historyScroll=document.querySelector('[aria-label=内核日志]');

    document.querySelector('[data-log-filters] [name=q]').focus();
    const text=document.querySelector('.a-log-message').firstChild,range=document.createRange();range.setStart(text,0);range.setEnd(text,5);getSelection().removeAllRanges();getSelection().addRange(range);window.scrollTo(0,180);window.__historyY=window.scrollY;
  })()`);
  await wait(a,"window.__historyRead>0");
  assert.ok(await evaluate(a,"document.querySelector('.a-main').textContent.includes('第 2 / 2 页') && window.__historyRow===document.querySelector('[data-log-entry]') && window.__historyScroll===document.querySelector('[aria-label=内核日志]') && Math.abs(window.scrollY-window.__historyY)<1 && getSelection().toString()==='alice' && document.activeElement===document.querySelector('[data-log-filters] [name=q]')"),`continuous log poll preserves history page, row identity, scroll, reading selection and focus: ${JSON.stringify(await evaluate(a,"({page:document.querySelector('.a-main').textContent.includes('第 2 / 2 页'),row:window.__historyRow===document.querySelector('[data-log-entry]'),scroll:window.__historyScroll.scrollLeft,oldScroll:window.__historyX,selection:getSelection().toString(),focus:document.activeElement?.name})"))}`);
  await evaluate(a,"getSelection().removeAllRanges();window.__failedLogOnce=false;window.fetch=(...args)=>String(args[0]).includes('/core-logs?')&&!window.__failedLogOnce?(window.__failedLogOnce=true,Promise.resolve(new Response(JSON.stringify({error:'log read fixture failure'}),{status:503,headers:{'Content-Type':'application/json'}}))):window.__historyFetch(...args);[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click()");
  await wait(a,"document.querySelector('[data-log-refresh-label]').textContent.includes('刷新失败')");
  assert.ok(await evaluate(a,"window.__historyRow===document.querySelector('[data-log-entry]') && document.querySelector('.a-main').textContent.includes('第 2 / 2 页')"),"failed update retains the history window");
  await evaluate(a,"window.fetch=window.__historyFetch;[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click()");
  await wait(a,"document.querySelector('[data-log-refresh-label]').textContent==='正在实时更新'");
  record(`${mode}: continuous refresh preserves history, selection, focus and scroll; failed read retains content and retry recovers`);
  await choose(a,"instance_id",shareA);
  await wait(a,`document.querySelector('[data-log-filters] select[name=instance_id]').value===${JSON.stringify(shareA)} && document.querySelector('[data-log-entry]')`);
  await reload(a);
  await wait(a,`document.querySelector('[data-log-filters] select[name=instance_id]')?.value===${JSON.stringify(shareA)} && !!document.querySelector('[data-log-entry]')`);
  assert.ok((await rows(a)).every(message=>message.startsWith("alice")));
  record(`${mode}: UI only offers A's shared instance; pagination and reload preserve the authorized selection; no horizontal overflow`);
  // Delay a real response to exercise out-of-order scope changes.
  await evaluate(manager,`(()=>{const original=window.fetch; window.fetch=async(...args)=>{const result=await original(...args);if(String(args[0]).includes('/core-logs?')&&String(args[0]).includes(${JSON.stringify(shareA)}))await new Promise(resolve=>setTimeout(resolve,650));return result;};})()`);
  await choose(manager,"instance_id",shareA);
  await delay(70);
  await choose(manager,"instance_id",shareB);
  await wait(manager,`document.querySelector('[data-log-filters] select[name=instance_id]').value===${JSON.stringify(shareB)} && [...document.querySelectorAll('.a-log-message')].length>0 && [...document.querySelectorAll('.a-log-message')].every(el=>el.textContent.startsWith('bob'))`);
  await delay(800);
  assert.ok((await rows(manager)).every(message=>message.startsWith("bob")));
  await screenshot(manager,"admin-shared-instance");
  record(`${mode}: fast A→B selection ignores the delayed A response`);
  // Exercise the actual production layout against the real long-log/source
  // fixtures, plus a failed/delayed transport while retaining those records.
  const layoutWidths = mode === "mobile" ? [[390,844],[844,390]] : [[1920,1080],[3840,2160],[1280,900],[960,800]];
  for (const [width,height] of layoutWidths) {
    await call("Emulation.setDeviceMetricsOverride", {width,height,deviceScaleFactor:1,mobile:mode==="mobile"}, manager);
    await evaluate(manager,"window.scrollTo(0,0)");
    assert.ok(await evaluate(manager,"document.documentElement.scrollWidth<=innerWidth+1"),`${width}: long logs and source names do not overflow the viewport`);
    assert.ok(await evaluate(manager,"[...document.querySelectorAll('.a-log-message')].every(el=>el.getBoundingClientRect().right<=innerWidth+1)"),`${width}: the log message uses the visible reading width`);
    await screenshot(manager,`stream-long-${width}x${height}`);
  }
  const [layoutWidth,layoutHeight] = mode === "mobile" ? [390,844] : [1920,1080];
  await call("Emulation.setDeviceMetricsOverride", {width:layoutWidth,height:layoutHeight,deviceScaleFactor:1,mobile:mode==="mobile"}, manager);
  await evaluate(manager,"[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='暂停更新').click();window.scrollTo(0,0)");
  await screenshot(manager,"stream-paused");
  await evaluate(manager,"(()=>{const q=document.querySelector('[data-log-filters] [name=q]');q.value='unmatched layout fixture';q.dispatchEvent(new Event('input',{bubbles:true}))})()");
  await wait(manager,"!document.querySelector('[data-log-entry]')");
  await screenshot(manager,"stream-empty");
  await evaluate(manager,"(()=>{const q=document.querySelector('[data-log-filters] [name=q]');q.value='';q.dispatchEvent(new Event('input',{bubbles:true}));window.__layoutFetch=window.fetch;window.fetch=(...args)=>String(args[0]).includes('/core-logs?')?Promise.resolve(new Response(JSON.stringify({error:'日志读取失败，请重试'}),{status:503,headers:{'Content-Type':'application/json'}})):window.__layoutFetch(...args);[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='自动更新').click()})()");
  await wait(manager,"document.querySelector('[data-log-refresh-label]').textContent.includes('刷新失败')");
  assert.ok(await evaluate(manager,"document.querySelectorAll('[data-log-entry]').length>0"));
  await screenshot(manager,"stream-error-retained");
  await evaluate(manager,"window.fetch=async(...args)=>{const result=await window.__layoutFetch(...args);if(String(args[0]).includes('/core-logs?'))await new Promise(resolve=>setTimeout(resolve,1200));return result;};[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click()");
  await wait(manager,"document.querySelector('[data-log-refresh-label]').getAttribute('aria-busy')==='true'");
  assert.ok(await evaluate(manager,"document.querySelectorAll('[data-log-entry]').length>0"),"refresh keeps readable logs while the response is pending");
  await screenshot(manager,"stream-loading-retained");
  await wait(manager,"!document.querySelector('[data-log-refresh-label]').hasAttribute('aria-busy') && document.querySelector('[data-log-refresh-label]').textContent==='正在实时更新'");
  await evaluate(manager,"window.fetch=window.__layoutFetch;delete window.__layoutFetch");
  await choose(manager,"instance_id","");
  await wait(manager,"document.querySelector('[data-log-refresh-label]').textContent==='正在实时更新'");
  await evaluate(manager,"document.querySelector('[data-log-source-status]').click();document.querySelectorAll('dialog[data-log-sources] [data-source-expand][aria-expanded=false]').forEach(item=>item.click());window.scrollTo(0,0)");
  await screenshot(manager,"stream-source-states");
  await evaluate(manager,"document.querySelector('dialog[data-log-sources] [aria-label=关闭]').click()");
  assert.ok(await evaluate(manager,"document.documentElement.scrollWidth<=innerWidth+1"));
  await call("Emulation.setDeviceMetricsOverride", {width:1920,height:1080,deviceScaleFactor:2,mobile:false},manager);
  for (const name of ["agent_id","instance_id","engine","level","limit"]) {
    await call("Runtime.evaluate", {expression:`document.querySelector('[data-log-filters] [name=${name}]').showPicker()`,userGesture:true},manager);
    await screenshot(manager,`stream-options-${name}`);
    await call("Input.dispatchKeyEvent", {type:"keyDown",key:"Escape",code:"Escape",windowsVirtualKeyCode:27},manager);
    await call("Input.dispatchKeyEvent", {type:"keyUp",key:"Escape",code:"Escape",windowsVirtualKeyCode:27},manager);
  }
  record(`${mode}: long logs, long node labels, source failures, all filter pickers, empty, paused, failed/retry and delayed refresh layouts captured at required viewports`);

  let sharing=await api(manager,admin,"GET",`/agents/${host}/sharing`);
  await api(manager,admin,"PUT",`/agents/${host}/sharing`,{revision:sharing.revision,shares:sharing.shares.map(share=>({username:share.username,engines:share.engines,ports:share.ports,limit_bytes:share.limit_bytes,enabled:share.user_id!==alice.user_id}))});
  await wait(a,`!document.querySelector('[data-log-filters] select[name=instance_id] option[value="${shareA}"]') && [...document.querySelectorAll('.a-log-message')].every(el=>!el.textContent.includes('独立实例'))`);
  assert.ok((await api(b,bob,"GET",`/core-logs?agent_id=${host}`)).every(log=>log.instance_id===shareB));
  await api(manager,admin,"DELETE",`/users/${alice.user_id}`,undefined,204);
  await api(a,alice,"GET","/core-log-sources",undefined,401);
  await reload(a);
  await wait(a,"!!document.querySelector('#login-form,.a-login form')");
  const switched=await login(a,"bob");
  await evaluate(a,"location.hash='#core-logs'");
  await wait(a,"!!document.querySelector('[data-log-entry]')");
  assert.equal(switched.user_id,bob.user_id);
  assert.ok((await rows(a)).every(message=>message.startsWith("bob")));
  assert.ok(await evaluate(a,`![...document.querySelectorAll('[data-log-filters] select[name=instance_id] option')].some(option=>option.value===${JSON.stringify(shareA)})`));
  record(`${mode}: live poll removes revoked A data/options/cache; disabled session denied; same-context account switch contains only B data`);
  if(artifacts) await writeFile(join(resolve(artifacts),`${mode}-record.txt`),report.join("\n")+"\n");
} finally {
  clearTimeout(timer);
  socket?.close();
  child.kill("SIGKILL");
  await new Promise(resolve => child.exitCode !== null || child.signalCode !== null ? resolve() : child.once("exit", resolve));
  await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}
