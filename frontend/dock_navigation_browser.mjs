// Dependency-free CDP regression against the production shell and deterministic
// API fixture. Real wheel/touch input supplements frame-by-frame DOM geometry.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

const root = dirname(fileURLToPath(import.meta.url));
const profile = await mkdtemp(join(tmpdir(), "qch-dock-browser-"));
const report = [];
let measurements = [];
const children = [];
let socket;
let pending = new Map();
const start = (command, args, options) => {
  const child = spawn(command, args, { stdio: ["ignore", "pipe", "pipe"], ...options });
  children.push(child);
  return child;
};
const endpoint = (child, pattern) => new Promise((resolve, reject) => {
  let output = "";
  for (const stream of [child.stdout, child.stderr]) stream.on("data", chunk => {
    output = (output + chunk).slice(-8192);
    const match = output.match(pattern);
    if (match) resolve(match[1]);
  });
  child.once("error", reject);
  child.once("exit", code => reject(new Error(`fixture/browser exited ${code}: ${output}`)));
});
const deadline = setTimeout(() => { for (const child of children) child.kill("SIGKILL"); }, 150000);
try {
  const server = start(process.execPath, [join(root, "agents_browser_smoke.mjs")], {
    env: { ...process.env, QCH_BROWSER_SMOKE_SERVE_ONLY: "1" },
  });
  const base = await endpoint(server, /(http:\/\/127\.0\.0\.1:\d+)\//);
  const chrome = start(process.env.QCH_CHROME_BIN || "chromium", ["--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"]);
  socket = new WebSocket(await endpoint(chrome, /DevTools listening on (ws:\/\/[^\s]+)/));
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", reject, { once: true }); });
  let sequence = 0;
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
    pending.set(id, { method, timer, resolve, reject });
    socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  });
  const evaluate = async (page, expression) => {
    const result = await call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }, page);
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  const wait = async (page, expression) => {
    for (let i = 0; i < 120; i++) {
      const result = await evaluate(page, expression);
      if (result) return result;
      await delay(50);
    }
    throw new Error(`condition timed out: ${expression}`);
  };
  const metrics = async (page, width, height, touch) => {
    await call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: touch ? 2 : 1, mobile: touch }, page);
    await call("Emulation.setTouchEmulationEnabled", { enabled: touch, maxTouchPoints: 5 }, page);
  };
  const point = (page, selector) => evaluate(page, `(() => { const r=document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return {x:(r.left+r.right)/2,y:(r.top+r.bottom)/2}; })()`);
  const tap = async (page, selector, touch) => {
    await evaluate(page, `document.querySelector(${JSON.stringify(selector)}).scrollIntoView({block:'nearest',inline:'nearest'})`);
    await delay(180);
    const p = await point(page, selector);
    const hit = await evaluate(page, `document.elementFromPoint(${p.x},${p.y})?.closest(${JSON.stringify(selector)})===document.querySelector(${JSON.stringify(selector)})`);
    assert.ok(hit, `tap must hit ${selector} at ${JSON.stringify(p)}: ${await evaluate(page, `document.elementFromPoint(${p.x},${p.y})?.outerHTML`)}`);
    if (touch) {
      await call("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [p] }, page);
      await call("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] }, page);
    } else {
      await call("Input.dispatchMouseEvent", { type: "mousePressed", ...p, button: "left", clickCount: 1 }, page);
      await call("Input.dispatchMouseEvent", { type: "mouseReleased", ...p, button: "left", clickCount: 1 }, page);
    }
  };
  const swipe = async (page, from, to) => {
    await call("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: 30, y: from }] }, page);
    for (let i = 1; i <= 8; i++) {
      await call("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: 30, y: from + (to - from) * i / 8 }] }, page);
      await delay(12);
    }
    await call("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] }, page);
    await delay(200);
  };
  const selection = `(() => {
    const rail=document.querySelector('dialog.a-drawer[open] .a-rail') || document.querySelector('.a-layout>.a-rail');
    const nav=rail?.querySelector('nav'), active=nav?.querySelector('[aria-current="page"]');
    const rect=active?.getBoundingClientRect(), clip=nav?.getBoundingClientRect();
    return {hash:location.hash, active:active?.getAttribute('href'),
      selected:nav?.querySelectorAll('[aria-current="page"]').length,
      scroll:nav?.scrollTop,max:nav?nav.scrollHeight-nav.clientHeight:0,
      visibleHeight:rect&&clip?Math.max(0,Math.min(rect.bottom,clip.bottom)-Math.max(rect.top,clip.top)):0,
      background:active&&getComputedStyle(active).backgroundColor,
      width:innerWidth,pageWidth:document.documentElement.scrollWidth};
  })()`;
  const check = async (page, route, label) => {
    // Route DOM commits before the selection color transition paints. Wait for
    // that visible state instead of sampling its initial transparent frame.
    await wait(page, `(() => { const rail=document.querySelector('dialog.a-drawer[open] .a-rail') || document.querySelector('.a-layout>.a-rail'); const active=rail?.querySelector('nav [aria-current="page"]'); return active && getComputedStyle(active).backgroundColor!=='rgba(0, 0, 0, 0)'; })()`);
    const result=await evaluate(page,selection);
    assert.equal(result.hash,`#${route}`,`${label}: scroll changed route`);
    assert.equal(result.active,`#${route}`,`${label}: selected route diverged`);
    assert.equal(result.selected,1,`${label}: duplicate/missing selected destination`);
    assert.notEqual(result.background,'rgba(0, 0, 0, 0)',`${label}: selection lost its background`);
    assert.ok(result.pageWidth<=result.width+1,`${label}: horizontal overflow`);
    measurements.push({label,...result});return result;
  };
  for (const scenario of [
    {name:'desktop-admin',width:1920,height:1080,touch:false,role:'admin'},
    {name:'desktop-4k-admin',width:3840,height:2160,touch:false,role:'admin'},
    {name:'tablet-admin',width:1120,height:680,touch:true,role:'admin'},
    {name:'tablet-user-reduced',width:1024,height:768,touch:true,role:'user',reduced:true},
    {name:'phone-admin',width:390,height:844,touch:true,role:'admin'},
    {name:'phone-user',width:375,height:667,touch:true,role:'user'},
  ]) {
    measurements=[];
    const {browserContextId}=await call('Target.createBrowserContext');
    const {targetId}=await call('Target.createTarget',{url:'about:blank',browserContextId});
    const {sessionId:page}=await call('Target.attachToTarget',{targetId,flatten:true});
    await call('Page.enable',{},page);
    await call('Emulation.setFocusEmulationEnabled',{enabled:true},page);
    await metrics(page,scenario.width,scenario.height,scenario.touch);
    await call('Emulation.setEmulatedMedia',{features:[{name:'prefers-reduced-motion',value:scenario.reduced?'reduce':'no-preference'}]},page);
    await call('Page.navigate',{url:`${base}/?mode=navigation-preview&preview=1&role=${scenario.role}#tasks`},page);
    await wait(page,'document.querySelector("[data-task-id]")');
    const drawer=async()=>{
      if(await evaluate(page,"matchMedia('(max-width:56rem)').matches && !document.querySelector('dialog.a-drawer[open]')")) {
        await tap(page,'button[aria-label="切换导航"]',scenario.touch);
        await wait(page,"document.querySelector('dialog.a-drawer[open]')");
      }
    };
    await drawer();
    await check(page,'tasks','initial selection');
    // The native shell retains both the rail and nav across route changes.
    await evaluate(page,"window.originalRail=document.querySelector('.a-layout>.a-rail');window.originalNav=originalRail.querySelector('nav')");
    if(scenario.width>896) {
      await metrics(page,scenario.width,450,scenario.touch);
      await evaluate(page,"document.querySelector('.a-layout>.a-rail nav').scrollTop=0");
      if(scenario.touch) await swipe(page,350,140);
      else await call('Input.dispatchMouseEvent',{type:'mouseWheel',x:30,y:300,deltaX:0,deltaY:180},page);
      await wait(page,"document.querySelector('.a-layout>.a-rail nav').scrollTop>0");
      await check(page,'tasks','real navigation scroll');
      await tap(page,'button[aria-label="切换导航"]',scenario.touch);
      await wait(page,"document.querySelector('.a-layout').classList.contains('a-collapsed')");
      assert.equal(await evaluate(page,"document.querySelector('.a-layout').classList.contains('a-collapsed')"),true);
      await tap(page,'button[aria-label="切换导航"]',scenario.touch);
      await wait(page,"!document.querySelector('.a-layout').classList.contains('a-collapsed')");
      assert.equal(await evaluate(page,"document.querySelector('.a-layout').classList.contains('a-collapsed')"),false);
      await check(page,'tasks','collapse and restore');
    } else {
      assert.equal(await evaluate(page,"document.activeElement.closest('dialog.a-drawer')!==null"),true,'drawer must contain keyboard focus');
      await call('Input.dispatchKeyEvent',{type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27},page);
      await call('Input.dispatchKeyEvent',{type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27},page);
      await wait(page,"!document.querySelector('dialog.a-drawer')");
      assert.equal(await evaluate(page,"document.activeElement.getAttribute('aria-label')"),'切换导航','Escape must restore toggle focus');
    }
    for(const route of ['node-settings','core-logs','tasks']) {
      await drawer();
      const prefix=await evaluate(page,"document.querySelector('dialog.a-drawer[open]')?'dialog.a-drawer[open] ':'.a-layout>.a-rail '");
      await tap(page,`${prefix}.a-nav-link[href="#${route}"]`,scenario.touch);
      await wait(page,`location.hash==='#${route}' && document.querySelector('.a-top-title')?.textContent===${JSON.stringify({'node-settings':'节点','core-logs':'日志',tasks:'任务'}[route])}`);
      if(scenario.width<=896) await wait(page,"!document.querySelector('dialog.a-drawer')");
      assert.equal(await evaluate(page,"window.originalRail===document.querySelector('.a-layout>.a-rail')&&window.originalNav===originalRail.querySelector('nav')"),true,'route switch replaced stable shell');
      await drawer();await check(page,route,'tap after scrolling');
      if(scenario.width<=896) {
        await tap(page,'dialog.a-drawer[open] button[title="关闭导航"]',scenario.touch);
        await wait(page,"!document.querySelector('dialog.a-drawer')");
      }
    }
    const before=await evaluate(page,"window.__agentsBrowserTestAPI.calls.filter(c=>c.path==='/tasks').length");
    for(let i=0;i<3;i++) {
      await evaluate(page,"originalNav.scrollTop=40;[...document.querySelectorAll('.a-top button')].find(b=>b.textContent==='刷新').click()");
      await wait(page,"![...document.querySelectorAll('.a-top button')].find(b=>b.textContent==='刷新').disabled");
      assert.equal(await evaluate(page,"originalNav.scrollTop"),await evaluate(page,"Math.min(40,originalNav.scrollHeight-originalNav.clientHeight)"),'refresh reset navigation scroll');
    }
    const manual=await evaluate(page,"window.__agentsBrowserTestAPI.calls.filter(c=>c.path==='/tasks').length");
    assert.ok(manual>=before+3,'manual task refresh did not reach API');
    await delay(2300);
    assert.ok(await evaluate(page,"window.__agentsBrowserTestAPI.calls.filter(c=>c.path==='/tasks').length")>manual,'automatic task refresh did not reach API');
    await evaluate(page,"location.hash='#core-logs';setTimeout(()=>location.hash='#tasks',25);setTimeout(()=>location.hash='#dashboard',50);setTimeout(()=>location.hash='#tasks',75)");
    await wait(page,"location.hash==='#tasks'&&document.querySelector('.a-top-title')?.textContent==='任务'&&document.querySelector('[data-task-id]')");
    await drawer();await check(page,'tasks','rapid route changes');
    if(scenario.width<=896) {
      await tap(page,'dialog.a-drawer[open] button[title="关闭导航"]',scenario.touch);
      await wait(page,"!document.querySelector('dialog.a-drawer')");
    }
    for(const [width,height] of [[844,390],[1100,600],[390,844]]) {
      await metrics(page,width,height,scenario.touch);await delay(150);await drawer();
      await check(page,'tasks','orientation and width change');
      if(width<=896) {
        await tap(page,'dialog.a-drawer[open] button[title="关闭导航"]',scenario.touch);
        await wait(page,"!document.querySelector('dialog.a-drawer')");
      }
    }
    report.push({...scenario,result:'passed',measurements});console.log(`PASS ${scenario.name}`);
    await call('Target.disposeBrowserContext',{browserContextId});
  }
  if (process.env.QCH_DOCK_REPORT) {
    await mkdir(dirname(process.env.QCH_DOCK_REPORT), { recursive: true });
    await writeFile(process.env.QCH_DOCK_REPORT, JSON.stringify(report, null, 2)+"\n");
  }
} finally {
  clearTimeout(deadline);
  for (const entry of pending.values()) clearTimeout(entry.timer);
  pending.clear();
  socket?.close();
  for (const child of children) {
    if (child.exitCode != null || child.signalCode != null) continue;
    const stopped = new Promise(resolve => child.once("exit", resolve));
    child.kill("SIGTERM");
    await Promise.race([stopped, delay(2000)]);
    if (child.exitCode == null && child.signalCode == null) { child.kill("SIGKILL"); await stopped; }
  }
  await rm(profile, { recursive: true, force: true });
}
