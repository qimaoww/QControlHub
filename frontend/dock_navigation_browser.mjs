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
    const dock=document.querySelector('.app-dock'), nav=dock.querySelector('.dock-nav');
    const active=dock.querySelector('.dock-nav [aria-current="page"],.dock-tools [aria-current="page"]');
    const marker=active?.querySelector('.dock-active-indicator');
    const visible=[...dock.querySelectorAll('.dock-active-indicator')].filter(e=>e.getClientRects().length);
    const r=active?.getBoundingClientRect(), m=marker?.getBoundingClientRect(), clip=active?.parentElement.getBoundingClientRect();
    return {hash:location.hash, active:active?.getAttribute('href'), classActive:active?.classList.contains('active'),
      delta:r&&m?Math.max(...['top','left','width','height'].map(k=>Math.abs(r[k]-m[k]))):null,
      decorations:visible.length, scroll:nav.scrollTop, max:nav.scrollHeight-nav.clientHeight,
      partial:r&&clip?r.top<clip.top&&r.bottom>clip.top||r.top<clip.bottom&&r.bottom>clip.bottom:false,
      visibleHeight:r&&clip?Math.max(0,Math.min(r.bottom,clip.bottom)-Math.max(r.top,clip.top)):0,
      overflow:active&&getComputedStyle(active.parentElement).overflowY, clipped:marker?.closest('.dock-nav')===nav, pointer:marker&&getComputedStyle(marker).pointerEvents,
      width:innerWidth, pageWidth:document.documentElement.scrollWidth};
  })()`;
  const check = async (page, route, label) => {
    const r = await evaluate(page, selection);
    assert.equal(r.hash, `#${route}`, `${label}: scroll changed route`);
    assert.equal(r.active, `#${route}`, `${label}: route and aria-current diverged`);
    assert.equal(r.classActive, true);
    assert.equal(r.decorations, 1, `${label}: duplicate/missing visible decoration`);
    assert.ok(r.delta <= 1, `${label}: highlight offset ${r.delta}`);
    assert.equal(r.pointer, "none");
    assert.ok(r.pageWidth <= r.width + 1, `${label}: page overflows`);
    measurements.push({label, ...r});
    return r;
  };
  for (const scenario of [
    { name: "desktop-light-admin", width: 1280, height: 720, touch: false, role: "admin", theme: "light" },
    { name: "tablet-dark-admin", width: 1180, height: 820, touch: true, role: "admin", theme: "dark" },
    { name: "tablet-light-user-reduced", width: 1024, height: 768, touch: true, role: "user", theme: "light", reduced: true },
    { name: "phone-dark-admin", width: 390, height: 844, touch: true, role: "admin", theme: "dark" },
    { name: "phone-light-user", width: 375, height: 667, touch: true, role: "user", theme: "light" },
  ]) {
    measurements = [];
    const { browserContextId } = await call("Target.createBrowserContext");
    const { targetId } = await call("Target.createTarget", { url: "about:blank", browserContextId });
    const { sessionId: page } = await call("Target.attachToTarget", { targetId, flatten: true });
    await call("Page.enable", {}, page);
    await call("Emulation.setFocusEmulationEnabled", { enabled: true }, page);
    await metrics(page, scenario.width, scenario.height, scenario.touch);
    await call("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: scenario.reduced ? "reduce" : "no-preference" }] }, page);
    await call("Page.addScriptToEvaluateOnNewDocument", { source: `localStorage.setItem('qcontrolhub-color-theme',${JSON.stringify(scenario.theme)});` }, page);
    await call("Page.navigate", { url: `${base}/?mode=tasks-preview&preview=1&role=${scenario.role}#tasks` }, page);
    await wait(page, "document.querySelector('.page-tasks .task-event-card')");
    await delay(500);
    if (scenario.width <= 820) {
      await tap(page, ".mobile-account-menu>summary", true);
      await wait(page, "document.querySelector('.mobile-account-menu[open]')");
      assert.equal(await evaluate(page, "[...document.querySelectorAll('.dock-nav>a')].filter(e=>e.getClientRects().length).length"), scenario.role === "admin" ? 4 : 3, "phone primary destinations must fit beside More");
      assert.equal(await evaluate(page, "document.querySelector('.mobile-account-menu [aria-current=page]').getAttribute('href')"), "#tasks");
      for (const [width, height] of [[844,390], [390,600], [375,667]]) {
        await metrics(page, width, height, true);
        await delay(450);
        assert.equal(await evaluate(page, "location.hash"), "#tasks");
        assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"));
      }
      await evaluate(page, "document.querySelector('.mobile-account-menu').open=true");
      const menu = await evaluate(page, "(() => { const e=document.querySelector('.mobile-account-menu>div'),r=e.getBoundingClientRect(); return {top:r.top,bottom:r.bottom,width:r.width,w:innerWidth,h:innerHeight}; })()");
      assert.ok(menu.top >= 0 && menu.bottom < menu.h, "phone menu must fit the visible viewport");
      assert.ok(menu.width >= menu.w-22, "animated More sheet must span the viewport, not the launcher");
      measurements.push({label:"phone sheet",...menu});
      await tap(page, '.mobile-account-menu [href="#core-logs"]', true);
      await wait(page, "document.body.classList.contains('page-core-logs')");
      assert.equal(await evaluate(page, "location.hash"), "#core-logs");
      assert.equal(await evaluate(page, "document.querySelector('.mobile-account-menu').open"), false);
    } else {
      let r = await check(page, "tasks", scenario.name);
      const count = await evaluate(page, "document.querySelectorAll('.dock-nav>a').length");
      assert.equal(count, scenario.role === "admin" ? 13 : 10);
      if (scenario.touch) await tap(page, ".dock-toggle", true);
      else await call("Input.dispatchMouseEvent", {type:"mouseMoved",x:30,y:250}, page);
      await delay(450);
      assert.ok(await evaluate(page, "document.querySelector('.app-dock').getBoundingClientRect().width>=211"), "scroll regression must cover the expanded rail shown in the report");
      // Frame sampler catches transient drift during real input and refresh.
      await evaluate(page, `window.dockSamples=[];window.sampleDock=true;(function sample(){if(!window.sampleDock)return;window.dockSamples.push(${selection});requestAnimationFrame(sample)})()`);
      if (scenario.touch) {
        await swipe(page, 440, 190);
        await swipe(page, 200, 460);
        await swipe(page, 450, 200);
      } else {
        for (const deltaY of [38,38,-30,-40,50,-20]) {
          await call("Input.dispatchMouseEvent", { type: "mouseWheel", x: 30, y: 350, deltaX: 0, deltaY }, page);
          await delay(100);
        }
      }
      await evaluate(page, "window.sampleDock=false");
      const samples = await evaluate(page, "window.dockSamples");
      assert.ok(samples.length > 10);
      assert.ok(samples.every(s => s.hash === "#tasks" && s.delta <= 1), "scroll frames must preserve route and geometry");
      if (scenario.role === "admin") assert.ok(samples.some(s=>s.scroll>0), "real input must actually scroll the navigation");
      measurements.push({label:"expanded scrolling",frames:samples.length,maxDrift:Math.max(...samples.map(s=>s.delta)),maxScroll:Math.max(...samples.map(s=>s.scroll))});
      if (scenario.touch) await tap(page, ".dock-toggle", true);
      else await call("Input.dispatchMouseEvent", {type:"mouseMoved",x:scenario.width-30,y:200}, page);
      await delay(450);
      await metrics(page, scenario.width, 600, scenario.touch);
      await delay(400);
      await tap(page, '.dock-nav [href="#core-logs"]', scenario.touch);
      await wait(page, "document.body.classList.contains('page-core-logs')");
      await delay(300);
      await check(page, "core-logs", "tap after scrolling");
      await tap(page, '.dock-nav [href="#tasks"]', scenario.touch);
      await wait(page, "document.body.classList.contains('page-tasks')");
      await delay(300);
      await check(page, "tasks", "second tap after scrolling");
      // Offscreen/partial selection, then re-entry: test actual clipped geometry.
      await evaluate(page, "location.hash='#dashboard'");
      await wait(page, "document.body.classList.contains('page-dashboard')");
      await delay(400);
      await evaluate(page, "document.querySelector('.dock-nav').scrollTop=20");
      r = await check(page, "dashboard", "partial selection");
      assert.ok(r.partial && r.clipped && r.visibleHeight > 0);
      assert.ok(["auto","scroll","hidden","clip"].includes(r.overflow), "actual clipping ancestor required");
      await evaluate(page, "document.querySelector('.dock-nav').scrollTop=10000");
      r = await check(page, "dashboard", "offscreen selection");
      // A shorter user menu may not need scrolling at this height.
      if (r.max >= 38) assert.equal(r.visibleHeight, 0);
      await evaluate(page, "document.querySelector('.dock-nav').scrollTop=0");
      await check(page, "dashboard", "selection re-entry");
      for (const route of [scenario.role === "admin" ? "users" : "my-quota", "tasks", ...(scenario.role === "admin" ? ["settings"] : [])]) {
        await evaluate(page, `document.querySelector('.dock-nav').scrollTop=10000;location.hash='#${route}'`);
        await wait(page, `document.body.classList.contains('page-${route}')`);
        await delay(400);
        await check(page, route, "nonzero-scroll route change");
      }
      await evaluate(page, "document.querySelector('.dock-nav').scrollTop=10000;location.hash='#core-logs'");
      await wait(page, "document.body.classList.contains('page-core-logs')");
      await delay(300);
      await evaluate(page, "location.hash='#tasks'");
      await wait(page, "document.body.classList.contains('page-tasks')");
      assert.equal(await evaluate(page, "document.getAnimations().some(a=>a.id==='qch-navigation')"), !scenario.reduced, "route switching retains navigation motion and respects reduced motion");
      await metrics(page, scenario.width-10, 590, scenario.touch);
      await delay(400);
      await check(page, "tasks", "resize during animation");
      // Rapid route changes retain only the latest route/animation owner.
      await evaluate(page, "location.hash='#core-logs';setTimeout(()=>location.hash='#tasks',25);setTimeout(()=>location.hash='#dashboard',50);setTimeout(()=>location.hash='#tasks',75)");
      await wait(page, "location.hash==='#tasks'&&document.body.classList.contains('page-tasks')");
      await delay(500);
      await check(page, "tasks", "rapid switching");
      if (scenario.touch) {
        await tap(page, ".dock-toggle", true);
        await delay(450);
        assert.ok(await evaluate(page, "document.querySelector('.app-dock').getBoundingClientRect().width>=211"));
        await tap(page, ".dock-toggle", true);
        await delay(450);
        assert.ok(await evaluate(page, "document.querySelector('.app-dock').getBoundingClientRect().width<=69"));
        assert.ok(await evaluate(page, "[...document.querySelectorAll('.dock-nav a,.dock-tools a,.dock-tools button')].every(e=>e.getBoundingClientRect().height>=44)"));
      }
      // Force shell reconciliation while scrolling as well as allowing the
      // actual task auto-refresh timer to run (API call count proves it fired).
      const before = await evaluate(page, "window.__agentsBrowserTestAPI.calls.filter(c=>c.path==='/tasks').length");
      for (let i=0;i<22;i++) {
        await evaluate(page, `document.querySelector('.dock-nav').scrollTop=${i%2 ? 20 : 40};document.querySelector('#refresh').click()`);
        await delay(90);
        const refreshed = await check(page, "tasks", "refresh during scroll");
        assert.equal(refreshed.scroll, Math.min(i%2 ? 20 : 40, refreshed.max), "refresh must preserve user scroll");
      }
      const manual = await evaluate(page, "window.__agentsBrowserTestAPI.calls.filter(c=>c.path==='/tasks').length");
      await evaluate(page, `window.sampleDock=true;window.dockSamples=[];window.dockScroller=setInterval(()=>{const n=document.querySelector('.dock-nav');n.scrollTop=n.scrollTop>20?10:40},60);(function sample(){if(!window.sampleDock)return;window.dockSamples.push(${selection});requestAnimationFrame(sample)})()`);
      await delay(2200);
      await evaluate(page, "clearInterval(window.dockScroller);window.sampleDock=false");
      assert.ok(await evaluate(page, "window.dockSamples.length>20&&window.dockSamples.every(s=>s.hash==='#tasks'&&s.delta<=1)"), "auto-refresh while continuously scrolling must preserve geometry");
      const after = await evaluate(page, "window.__agentsBrowserTestAPI.calls.filter(c=>c.path==='/tasks').length");
      assert.ok(manual > before, "manual task refresh must run");
      assert.ok(after > manual, "automatic task refresh must run");
      await check(page, "tasks", "automatic refresh");
      await evaluate(page, "document.documentElement.style.setProperty('--ui-font-scale','1.5');document.querySelector('.dock-nav a .dock-label').textContent='很长的导航菜单名称用于字体放大验证'");
      await metrics(page, scenario.touch ? 834 : 1100, scenario.touch ? 1194 : 600, scenario.touch);
      await delay(450);
      await check(page, "tasks", "font scaling/orientation");
      await call("Emulation.setPageScaleFactor", { pageScaleFactor: 1.5 }, page);
      await check(page, "tasks", "pinch zoom");
      await call("Emulation.setPageScaleFactor", { pageScaleFactor: 1 }, page);
    }
    report.push({ ...scenario, result: "passed", measurements });
    console.log(`PASS ${scenario.name}`);
    await call("Target.disposeBrowserContext", { browserContextId });
  }
  // Chromium settings require a regular profile tab; incognito settings pages
  // cannot navigate. Verify real browser zoom in a separate owned target.
  const {targetId: zoomTarget} = await call("Target.createTarget", {url:"chrome://settings/appearance"});
  const {sessionId: zoomPage} = await call("Target.attachToTarget", {targetId:zoomTarget,flatten:true});
  await call("Page.enable", {}, zoomPage);
  await wait(zoomPage, "!!globalThis.chrome?.settingsPrivate?.setDefaultZoom");
  await evaluate(zoomPage, "new Promise(resolve=>chrome.settingsPrivate.setDefaultZoom(1.25,()=>chrome.settingsPrivate.getDefaultZoom(resolve)))");
  await metrics(zoomPage,1280,720,false);
  await call("Page.navigate", {url:`${base}/?mode=tasks-preview&preview=1#tasks`}, zoomPage);
  await wait(zoomPage, "document.querySelector('.page-tasks .task-event-card')");
  await delay(450);
  measurements=[];
  await check(zoomPage, "tasks", "browser zoom 125 percent");
  assert.equal(await evaluate(zoomPage, "devicePixelRatio"),1.25,"actual browser zoom must be applied");
  report.push({name:"desktop-browser-zoom-125",result:"passed",measurements});
  console.log("PASS desktop-browser-zoom-125");
  await call("Target.closeTarget", {targetId:zoomTarget});
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
