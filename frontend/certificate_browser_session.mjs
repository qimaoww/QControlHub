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
    await wait(sessionId, "!!document.querySelector('#login-form,.a-login form')");
    return sessionId;
  };
  const login = async (page, username) => {
    await evaluate(page, `(() => { const form = document.querySelector('#login-form,.a-login form'); form.elements.username.value=${JSON.stringify(username)}; form.elements.token.value='config scope password fixture'; form.requestSubmit(); })()`);
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
    const { data } = await call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }, page);
    await writeFile(join(resolve(artifacts), `${mode}-${label}.png`), Buffer.from(data, "base64"));
  };


  const a=await createPage(), b=await createPage();
  const alice=await login(a,"alice"), bob=await login(b,"bob");
  const click=async(page,selector)=>evaluate(page,`document.querySelector(${JSON.stringify(selector)}).click()`);
  const set=async(page,selector,value)=>evaluate(page,`(() => {const field=document.querySelector(${JSON.stringify(selector)});field.value=${JSON.stringify(value)};field.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  const open=async(page)=>{await evaluate(page,"location.hash='#certificates'");await wait(page,"!!document.querySelector('[data-certificate-create=self-signed]')")};
  await open(a);
  assert.equal(await evaluate(a,"document.querySelectorAll('.cert-summary').length"),0);
  assert.equal(await evaluate(a,"document.querySelectorAll('.cert-card,.context-steps').length"),0);
  await click(a,"[data-certificate-accounts-open]");await wait(a,"!!document.querySelector('[data-certificate-new-dns]')");await click(a,"[data-certificate-new-dns]");await wait(a,"!!document.querySelector('[data-certificate-account]')");
  await set(a,'[name=name]','Alice DNS');await set(a,'[name=zone]','example.test');await set(a,'[name=token]','local-browser-fixture-token');
  await evaluate(a,"document.querySelector('[data-certificate-account]').requestSubmit()");
  await wait(a,"!!document.querySelector('[data-certificate-new-dns]') && !document.querySelector('[data-certificate-account],[aria-busy=true]')");
  assert.equal((await api(a,alice,'GET','/certificate-accounts')).length,1);
  assert.deepEqual(await api(b,bob,'GET','/certificate-accounts'),[]);
  await click(a,'dialog[open]:last-of-type [aria-label=关闭]');await click(a,'[data-certificate-create=self-signed]');await wait(a,"!!document.querySelector('[name=days]')");
  await set(a,'[name=name]','边缘入口证书');await set(a,'[name=domains]','edge.example.test');await evaluate(a,"document.querySelector('[data-certificate-form]').requestSubmit()");
  await wait(a,"!!document.querySelector('[data-certificate-grant-open]')");
  const cert=(await api(a,alice,'GET','/certificates')).items[0];assert.equal(cert.version,1);assert.equal(cert.owner_id,alice.user_id);
  await api(b,bob,'GET',`/certificates/${cert.id}`,undefined,404);
  // Clearly isolated presentation fixtures. Authorized reads still hit the real
  // API; only this page's returned order view is substituted. No database order,
  // CA request or credential is fabricated or sent to a public service.
  await evaluate(a,`(() => {
    const original=window.fetch.bind(window);
    window.certificateOrderFixture={original,requests:0,order:{id:'synthetic-order',status:'queued',stage:'queued',attempt:0,created_at:'2026-11-02T04:05:06Z',challenges:[]}};
    window.fetch=async(input,options={})=>{
      const fixture=window.certificateOrderFixture;
      const path=String(input);
      if(path.endsWith('/orders/synthetic-order/continue')){
        fixture.requests++;
        return new Promise(resolve=>fixture.release=()=>resolve(new Response(JSON.stringify({error:'fixture validation interrupted'}),{status:409,headers:{'Content-Type':'application/json'}})));
      }
      const response=await original(input,options);
      if(path.endsWith('/certificates/${cert.id}')&&(!options.method||options.method==='GET')&&response.ok){
        const body=await response.json();
        body.certificate.source='acme';body.certificate.method=fixture.order.method||'dns-manual';body.orders=[structuredClone(fixture.order)];
        return new Response(JSON.stringify(body),{status:200,headers:{'Content-Type':'application/json'}});
      }
      return response;
    };
  })()`);
  const detailText=()=>evaluate(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent");
  const refreshOrder=async patch=>{await evaluate(a,`Object.assign(window.certificateOrderFixture.order,${JSON.stringify(patch)})`);await click(a,'[data-certificate-refresh]');};
  await refreshOrder({});await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('尚未执行')");
  assert.doesNotMatch(await detailText(),/第 0 次|v1/);
  await evaluate(a,"(() => {document.querySelector('[data-certificate-refresh]').focus();const content=document.querySelector('[data-certificate-id] .a-dialog-body');content.scrollTop=100;window.certificateScroll=content.scrollTop;window.certificateOrderFixture.order.status='running';window.certificateOrderFixture.order.stage='preparing';window.certificateOrderFixture.order.attempt=1;})()");
  await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('首次执行中')");
  assert.equal(await evaluate(a,"document.activeElement.hasAttribute('data-certificate-refresh')"),true,'poll lost keyboard focus');
  assert.equal(await evaluate(a,"document.querySelector('[data-certificate-id] .a-dialog-body').scrollTop"),await evaluate(a,"window.certificateScroll"),'poll lost scroll');
  await refreshOrder({status:'waiting_dns',stage:'waiting_dns',challenges:[{type:'dns-01',domain:'example.test',name:'_acme-challenge.example.test',value:'synthetic-txt-value',status:'pending'}]});
  await wait(a,"!!document.querySelector('[data-certificate-action$=continue]')");
  assert.match(await detailText(),/等待人工 DNS 操作/);
  assert.match(await detailText(),/_acme-challenge.example.test/);
  await evaluate(a,"document.querySelector('[data-certificate-action$=continue]').click();document.querySelector('[data-certificate-action$=continue]').click()");
  assert.equal(await evaluate(a,"window.certificateOrderFixture.requests"),1);
  assert.equal(await evaluate(a,"document.querySelector('[data-certificate-action$=synthetic-order]').disabled"),true);
  await evaluate(a,"window.certificateOrderFixture.release()");
  await wait(a,"!document.querySelector('[data-certificate-action$=continue]').disabled");
  assert.match(await detailText(),/synthetic-txt-value/);
  await refreshOrder({status:'queued',stage:'retry',attempt:2,error:'not found',next_attempt:'2026-11-02T04:06:06Z'});
  await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('等待自动重试')");
  assert.match(await detailText(),/已执行 2 次.*后台将自动继续申请/s);
  assert.match(await detailText(),/暂无法确认具体资源或原因/);
  await refreshOrder({status:'failed',stage:'cleanup',method:'dns-api',attempt:8,created_at:'2026-11-02T06:18:04Z',next_attempt:'2026-11-02T06:23:04Z',error:'验证资源清理失败，将重试',failure_stage:'validating',challenges:[]});
  await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('原始申请错误未保留')");
  assert.match(await detailText(),/已执行 8 次/);
  await call('Emulation.setDeviceMetricsOverride',{width:mode==='mobile'?390:1920,height:mode==='mobile'?844:1080,deviceScaleFactor:1,mobile:mode==='mobile'},a);
  await evaluate(a,"Array.from(document.querySelectorAll('dialog.a-dialog h2')).find(e=>e.textContent==='申请与续期记录').scrollIntoView({block:'start'})");
  await screenshot(a,'order-legacy-failure');
  await refreshOrder({error:'读取 ACME 申请账号失败：申请账号解密失败，请联系管理员检查当前及旧加密密钥',failure_stage:'preparing',cleanup_error:''});
  await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('旧加密密钥')");
  assert.doesNotMatch(await detailText(),/本人申请账号是否仍可用/);
  await screenshot(a,'order-account-decryption');

  await refreshOrder({error:'DNS TXT 尚未传播：_acme-challenge.example.test',cleanup_error:'DNS API 返回 HTTP 503，请检查凭据与区域权限'});
  await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('清理失败：DNS API 返回 HTTP 503')");
  assert.match(await detailText(),/DNS TXT 尚未传播/);
  assert.match(await detailText(),/下次清理/);
  await evaluate(a,"Array.from(document.querySelectorAll('dialog.a-dialog h2')).find(e=>e.textContent==='申请与续期记录').scrollIntoView({block:'start'})");
  await screenshot(a,'order-failed-cleanup');
  for(const [width,height,dpr] of [[1920,1080,1],[3840,2160,1],[1920,1080,2],[1280,800,1],[1024,768,1],[768,1024,1],[390,844,1],[844,390,1],[320,568,1]]){
    await call('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:dpr,mobile:width<768},a);
    await call('Emulation.setTouchEmulationEnabled',{enabled:width<768},a);
    await delay(50);
    assert.equal(await evaluate(a,"document.documentElement.scrollWidth<=innerWidth+1 && document.querySelector('dialog.a-dialog[open]:last-of-type').getBoundingClientRect().right<=innerWidth+1"),true,'order detail overflow at '+width+'x'+height+' DPR '+dpr);
  }
  for(const status of ['succeeded','canceled']){
    await refreshOrder({status,stage:'complete'});
    await wait(a,`Array.from(document.querySelectorAll('dialog.a-dialog .a-badge')).some(e=>e.textContent===${JSON.stringify(status==='succeeded'?'签发成功':'已取消')})`);
    assert.doesNotMatch(await detailText(),/后台将自动继续申请|not found/);
  }
  await refreshOrder({status:'succeeded',stage:'cleanup',error:'',cleanup_error:'DNS API 返回 HTTP 503'});
  await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('验证资源待清理') && document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('签发成功')");
  assert.match(await detailText(),/签发成功/);
  await call('Emulation.setDeviceMetricsOverride',{width:mode==='mobile'?390:1920,height:mode==='mobile'?844:1080,deviceScaleFactor:1,mobile:mode==='mobile'},a);
  await evaluate(a,"Array.from(document.querySelectorAll('dialog.a-dialog h2')).find(e=>e.textContent==='申请与续期记录').scrollIntoView({block:'start'})");
  await screenshot(a,'order-success-cleanup');
  await refreshOrder({status:'succeeded',stage:'complete',cleanup_error:'',next_attempt:null});
  await wait(a,"!document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('清理失败')");
  assert.doesNotMatch(await detailText(),/下次清理|验证资源待清理/);
  await screenshot(a,'order-success-complete');
  await evaluate(a,"window.fetch=window.certificateOrderFixture.original;delete window.certificateOrderFixture");
  await click(a,'[data-certificate-refresh]');await wait(a,"!document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('验证资源待清理')");
  await call('Emulation.setDeviceMetricsOverride',{width:mode==='mobile'?390:1440,height:mode==='mobile'?844:1000,deviceScaleFactor:1,mobile:mode==='mobile'},a);
  record(mode+': synthetic order presentation: live polling/focus/scroll, DNS continuation single submit and recovery, retries, terminal cleanup, dates and 9 CSS viewport/DPR combinations passed');

  await click(a,'[data-certificate-grant-open]');await wait(a,"!!document.querySelector('[data-certificate-grant]')");await set(a,'[name=username]','bob');await evaluate(a,"document.querySelector('[data-certificate-grant]').requestSubmit()");await wait(a,"!!document.querySelector('[data-certificate-action$=grants]')");
  await open(b);await wait(b,`!!document.querySelector('[data-certificate-detail="${cert.id}"]')`);
  assert.equal((await api(b,bob,'GET','/certificates')).items[0].owned,false);
  await evaluate(b,`location.hash='#live-config?agent=${fixture.shared}&engine=mihomo'`);await wait(b,"Array.from(document.querySelectorAll('button')).some(b=>b.textContent==='添加入站')");
  await evaluate(b,"Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='添加入站').click()");await wait(b,"!!document.querySelector('dialog.a-dialog select')");
  await evaluate(b,"(() => {const s=document.querySelector('dialog.a-dialog [name=protocol]');s.value='trojan';s.dispatchEvent(new Event('change',{bubbles:true}));})()");
  await wait(b,`!!document.querySelector('[name=panel_certificate_id] option[value="${cert.id}"]')`);
  assert.equal(await evaluate(b,`document.querySelector('[name=panel_certificate_id] option[value="${cert.id}"]').parentElement.label`),'授权给我的证书');
  const localPath=await evaluate(b,"document.querySelector('[name=certificate_path]').value");
  await set(b,'[name=panel_certificate_id]',cert.id);
  assert.equal(await evaluate(b,"document.querySelector('[name=certificate_path]').closest('.a-field').hidden"),true);
  await set(b,'[name=panel_certificate_id]','');
  assert.equal(await evaluate(b,"document.querySelector('[name=certificate_path]').value"),localPath);
  assert.equal(await evaluate(b,"document.querySelector('[name=certificate_path]').closest('.a-field').hidden"),false);
  await set(b,'[name=panel_certificate_id]',cert.id);
  assert.equal(await evaluate(b,"document.querySelector('[name=certificate_path]').value"),`/etc/qcontrolhub/panel-certificates/${cert.id}/cert.pem`);
  await evaluate(b,"document.querySelector('[name=panel_certificate_id]').scrollIntoView({block:'center'})");
  assert.ok(await evaluate(b,"document.documentElement.scrollWidth <= innerWidth+1"));
  record(`${mode}: real UI DNS-account save, self-signed issuance, owner isolation, UI share, authorized protocol selector and protected path markers passed`);
  await screenshot(b,'certificate-selector');
  for (const [width,height] of mode==='mobile'?[[390,844],[844,390],[768,1024]]:[[1920,1080],[3840,2160],[1280,800],[540,720]]) {
    await call('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:mode==='mobile'},b);
    for (const theme of ['analytics']) {
      await evaluate(b,`document.documentElement.dataset.theme=${JSON.stringify(theme)}`);
      await evaluate(b,'new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
      const point=await evaluate(b,"(() => {const select=document.querySelector('[name=panel_certificate_id]');select.scrollIntoView({block:'center'});const r=select.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()");
      await call('Input.dispatchMouseEvent',{type:'mousePressed',button:'left',clickCount:1,...point},b);
      await call('Input.dispatchMouseEvent',{type:'mouseReleased',button:'left',clickCount:1,...point},b);
      await wait(b,"document.querySelector('[name=panel_certificate_id]').matches(':open')");
      const bounds=await evaluate(b,"(() => {const options=[...document.querySelector('[name=panel_certificate_id]').options].map(option=>option.getBoundingClientRect()).filter(rect=>rect.width&&rect.height);return {left:Math.min(...options.map(r=>r.left)),right:Math.max(...options.map(r=>r.right)),top:Math.min(...options.map(r=>r.top)),bottom:Math.max(...options.map(r=>r.bottom)),overflow:document.documentElement.scrollWidth>innerWidth+1,width:innerWidth,height:innerHeight}})()");
      assert.ok(bounds.left>=-1&&bounds.right<=bounds.width+1&&bounds.top>=-1&&bounds.bottom<=bounds.height+1&&!bounds.overflow,JSON.stringify(bounds));
      const occlusion=await evaluate(b,"(() => {const s=document.querySelector('[name=panel_certificate_id]').getBoundingClientRect(),body=document.querySelector('.a-dialog-body').getBoundingClientRect(),footer=document.querySelector('dialog.a-dialog button[type=submit]').getBoundingClientRect();return {selectTop:s.top,selectBottom:s.bottom,bodyTop:body.top,footerTop:footer.top}})()");
      assert.ok(occlusion.selectTop>=occlusion.bodyTop-1&&occlusion.selectBottom<=occlusion.footerTop+1,JSON.stringify(occlusion));
      await screenshot(b,`certificate-menu-${theme}-${width}x${height}`);
      for(const type of ['keyDown','keyUp']) await call('Input.dispatchKeyEvent',{type,key:'Escape',code:'Escape',windowsVirtualKeyCode:27},b);
      await wait(b,"!document.querySelector('[name=panel_certificate_id]').matches(':open') && !!document.querySelector('dialog.a-dialog[open]')");
    }
  }
  await call('Emulation.setDeviceMetricsOverride',{width:mode==='mobile'?390:1440,height:mode==='mobile'?844:1000,deviceScaleFactor:1,mobile:mode==='mobile'},b);


  // An already-open recipient detail must lose its stale content when the
  // owner revokes access; the read poll must distinguish 404 from outages.
  const c=await createPage(), recipient=await login(c,'bob');
  await open(c);await click(c,`[data-certificate-detail="${cert.id}"]`);
  await wait(c,`!!document.querySelector('dialog.a-dialog[data-certificate-id="${cert.id}"]')`);
  await api(a,alice,'DELETE',`/certificates/${cert.id}/grants`,{username:'bob'},204);
  await wait(c,`!document.querySelector('dialog.a-dialog[open]:last-of-type') && !document.querySelector('[data-certificate-detail="${cert.id}"]')`);
  await api(c,recipient,'GET',`/certificates/${cert.id}`,undefined,404);
  await api(a,alice,'POST',`/certificates/${cert.id}/grants`,{username:'bob'},204);
  record(`${mode}: revoked certificate detail and list disappear without navigation or manual refresh`);

  await evaluate(c,`(() => {
    const original=window.fetch;window.__certRestoreFetch=()=>{window.fetch=original};
    const gate=new Promise(resolve=>{window.__certRelease=resolve});
    window.fetch=async(...args)=>{const response=await original(...args);if(String(args[0]).endsWith('/certificate-http-nodes')){window.__certBlocked=true;await gate}return response};
  })()`);
  await click(c,'[data-certificate-create=acme]');await wait(c,'window.__certBlocked && !!document.querySelector("dialog.a-dialog[open]")');
  for(const type of ['keyDown','keyUp'])await call('Input.dispatchKeyEvent',{type,key:'Escape',code:'Escape',windowsVirtualKeyCode:27},c);
  await wait(c,"!document.querySelector('dialog.a-dialog[open]:last-of-type')");
  await evaluate(c,'window.__certRestoreFetch();window.__certRelease()');await delay(500);
  assert.equal(await evaluate(c,"document.querySelectorAll('dialog.a-dialog').length"),0);
  record(`${mode}: Escape closes a certificate form and invalidates its delayed real resource response`);

  // Node enrollment uses the existing source dialog and never edits a config.
  const nativeBefore=await api(a,alice,'GET','/agents/'+fixture.native+'/configs/mihomo');
  await click(a,'dialog[open]:last-of-type [aria-label=关闭]');await click(a,'[data-certificate-sources-open]');
  await wait(a,"!!document.querySelector("+JSON.stringify('[data-certificate-source="'+fixture.owner_source+'"][data-certificate-source-action="enroll"]')+")");
  const enrollSelector='[data-certificate-source="'+fixture.owner_source+'"][data-certificate-source-action="enroll"]';
  await click(a,enrollSelector);await wait(a,"!!document.querySelector('dialog.a-dialog[open]:last-of-type')");
  await click(a,'dialog[open]:last-of-type .a-dialog-body button:not(.primary)');
  assert.equal((await api(a,alice,'GET','/certificates')).items.some(item=>item.source_id===fixture.owner_source),false);
  await click(a,enrollSelector);await wait(a,"!!document.querySelector('dialog.a-dialog[open]:last-of-type')");await click(a,'dialog[open]:last-of-type .a-dialog-body button.primary');
  await wait(a,"fetch('/api/v1/certificates').then(r=>r.json()).then(d=>d.items.some(i=>i.source_id==="+JSON.stringify(fixture.owner_source)+"))");
  const nativeCert=(await api(a,alice,'GET','/certificates')).items.find(item=>item.source_id===fixture.owner_source);
  assert.equal(nativeCert.source,'node');assert.equal(nativeCert.renewal_manager,'external');assert.equal(nativeCert.auto_renew,false);
  await api(b,bob,'GET','/certificates/'+nativeCert.id,undefined,404);
  await api(b,bob,'PUT','/certificate-managed-sources/'+fixture.owner_source,{action:'enroll'},404);
  assert.deepEqual(await api(a,alice,'GET','/agents/'+fixture.native+'/configs/mihomo'),nativeBefore);
  await click(a,'[data-certificate-sources-open]');await wait(a,"!!document.querySelector("+JSON.stringify('[data-certificate-detail="'+nativeCert.id+'"]')+")");
  await click(a,'[data-certificate-detail="'+nativeCert.id+'"]');await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type')?.textContent.includes('原有方式续期')");
  assert.equal(await evaluate(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('Caddy 自动续期')"),false);
  await screenshot(a,'node-certificate-detail');
  await click(a,'[data-certificate-sources-open]');await wait(a,"document.querySelector('dialog.a-dialog[open]:last-of-type h2')?.textContent==='节点证书来源'");
  for(const [width,height] of [[1920,1080],[3840,2160],[1280,800],[1024,768],[768,1024],[390,844],[844,390],[320,568]]) {
    await call('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:width<768},a);
    await call('Emulation.setTouchEmulationEnabled',{enabled:width<768},a);
    await delay(100);
    assert.equal(await evaluate(a,"document.documentElement.scrollWidth<=innerWidth+1 && (()=>{const r=document.querySelector('dialog.a-dialog[open]:last-of-type').getBoundingClientRect();return r.left>=-1&&r.right<=innerWidth+1&&r.top>=-1&&r.bottom<=innerHeight+1})()"),true,'source layout overflow at '+width+'x'+height);
    await screenshot(a,'node-sources-'+width+'x'+height);
  }
  for(const type of ['keyDown','keyUp'])await call('Input.dispatchKeyEvent',{type,key:'Tab',code:'Tab',windowsVirtualKeyCode:9},a);
  assert.equal(await evaluate(a,"!!document.activeElement.closest('dialog.a-dialog')"),true);
  await call('Emulation.setDeviceMetricsOverride',{width:mode==='mobile'?390:1440,height:mode==='mobile'?844:1000,deviceScaleFactor:1,mobile:mode==='mobile'},a);
  for(const type of ['keyDown','keyUp'])await call('Input.dispatchKeyEvent',{type,key:'Escape',code:'Escape',windowsVirtualKeyCode:27},b);
  await wait(b,"document.querySelector('dialog[open]:last-of-type h2')?.textContent==='放弃更改'");
  await click(b,'dialog[open]:last-of-type .a-dialog-body button.primary');
  await wait(b,"!document.querySelector('[name=panel_certificate_id]')");
  await open(b);await click(b,'[data-certificate-sources-open]');await wait(b,"!!document.querySelector("+JSON.stringify('[data-certificate-source="'+fixture.shared_source+'"][data-certificate-source-action="enroll"]')+")");
  assert.equal(await evaluate(b,"!!document.querySelector("+JSON.stringify('[data-certificate-source="'+fixture.owner_source+'"]')+")"),false);
  await click(b,'[data-certificate-source="'+fixture.shared_source+'"][data-certificate-source-action="enroll"]');await wait(b,"!!document.querySelector('dialog.a-dialog[open]:last-of-type')");await click(b,'dialog[open]:last-of-type .a-dialog-body button.primary');
  await wait(b,"fetch('/api/v1/certificates').then(r=>r.json()).then(d=>d.items.some(i=>i.source_id==="+JSON.stringify(fixture.shared_source)+"))");
  const sharedNative=(await api(b,bob,'GET','/certificates')).items.find(item=>item.source_id===fixture.shared_source);
  assert.equal(sharedNative.owned,true);await api(a,alice,'GET','/certificates/'+sharedNative.id,undefined,404);
  record(mode+': explicit native enrollment, canceled consent, unchanged TLS config, shared source isolation, 8 responsive viewports and keyboard focus passed');

  // Reuse the same document and installed route module across real logins.
  await click(a,'dialog[open]:last-of-type [aria-label=关闭]');
  await set(a,'[name=search]','alice-private.example.test');
  await wait(a,"document.querySelector('[name=search]')?.value==='alice-private.example.test'");
  await click(a,'[title=退出登录]');await wait(a,"!!document.querySelector('#login-form,.a-login form')");
  assert.equal(await evaluate(a,"document.querySelectorAll('dialog.a-dialog').length"),0);
  const switched=await login(a,'bob');await open(a);
  assert.equal(await evaluate(a,"document.querySelector('[name=search]').value"),'');
  await wait(a,`!!document.querySelector('[data-certificate-detail="${cert.id}"]')`);
  await click(a,'[data-certificate-accounts-open]');await wait(a,"!!document.querySelector('[data-certificate-new-dns]')");
  assert.equal(await evaluate(a,"document.querySelector('dialog.a-dialog[open]:last-of-type').textContent.includes('Alice DNS')"),false);
  assert.deepEqual(await api(a,switched,'GET','/certificate-accounts'),[]);
  record(`${mode}: same-browser logout/login clears private filters, dialogs and credential-account state`);

} finally {
  clearTimeout(timer);
  socket?.close();
  child.kill("SIGKILL");
  await new Promise(resolve => child.exitCode !== null || child.signalCode !== null ? resolve() : child.once("exit", resolve));
  await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}
