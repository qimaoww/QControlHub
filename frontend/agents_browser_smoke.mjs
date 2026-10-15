import assert from "node:assert/strict";
import { runBrowserWorkers } from "./browser_scheduler.mjs";
import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { chmod, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
const isMain = Boolean(process.argv[1]) &&
  (await realpath(process.argv[1]).catch(() => null)) === fileURLToPath(import.meta.url);
const productionCSP = (await readFile(join(root, "nginx.conf"), "utf8"))
  .match(/add_header Content-Security-Policy "([^"]+)" always;/)?.[1];
assert.ok(productionCSP, "browser smoke must read the production content security policy");
const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css?v=motion-smoke"></head><body><div id="app"><div class="boot">测试载入中</div></div><script type="module" src="/assets/agents_browser_runtime.mjs"></script></body></html>`;

const mime = (path) =>
  path.endsWith(".css")
    ? "text/css; charset=utf-8"
    : path.endsWith(".js") || path.endsWith(".mjs")
      ? "text/javascript; charset=utf-8"
      : "text/html; charset=utf-8";

const previewFlags = new Map();
const archiveFailures = new Set();
let presetCatalog;
const server = createServer(async (request, response) => {
  try {
    const url = new URL(request.url, "http://127.0.0.1");
    const path = url.pathname;
    response.setHeader("Cache-Control", "no-store");
    if (process.env.QCH_BROWSER_SMOKE_DEBUG) process.stderr.write(`${path}\n`);
    if (path === "/" || path === "/agents-browser-smoke.html") {
      if (url.searchParams.get("mode")?.startsWith("dashboard") || url.searchParams.get("mode")?.startsWith("ip-quality"))
        response.setHeader("Content-Security-Policy", productionCSP);
      response.writeHead(200, { "Content-Type": mime(".html") });
      response.end(html);
      return;
    }
    if (path === "/assets/preset-plans.json") {
      if (!presetCatalog) {
        const result = spawnSync("go", ["run", join(root, "testdata/preset_plans.go")], {cwd:join(root,".."), encoding:"utf8", timeout:30000});
        if (result.status !== 0) throw new Error(`generate preset test catalog: ${result.stderr || result.error}`);
        presetCatalog = result.stdout;
      }
      response.writeHead(200, {"Content-Type":"application/json", "Cache-Control":"no-store"});
      response.end(presetCatalog);
      return;
    }
    if (/^\/api\/v1\/region-flags\/[a-z]{2}$/.test(path)) {
      // Opt-in manual previews use the production artwork; automated tests
      // remain deterministic and never depend on an external flag provider.
      if (process.env.QCH_BROWSER_SMOKE_PREVIEW_FLAGS) {
        const code = path.split("/").pop();
        if (!previewFlags.has(code)) {
          const upstream = await fetch(`https://raw.githubusercontent.com/lipis/flag-icons/main/flags/4x3/${code}.svg`, { signal: AbortSignal.timeout(5000) });
          if (!upstream.ok) throw new Error(`flag preview: ${upstream.status}`);
          previewFlags.set(code, await upstream.text());
        }
        response.writeHead(200, { "Content-Type": "image/svg+xml" });
        response.end(previewFlags.get(code));
        return;
      }
      response.writeHead(200, { "Content-Type": "image/svg+xml" });
      response.end('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 48"><rect width="64" height="48" fill="#d80027"/></svg>');
      return;
    }
    if (/^\/api\/v1\/ip-quality\/[a-z-]+\/archives\/[46]$/.test(path)) {
      if (path.includes("/quality-image-failure-") && path.endsWith("/4") && !archiveFailures.has(path)) {
        archiveFailures.add(path);
        response.writeHead(503).end("Temporary report image failure");
        return;
      }
      response.writeHead(200, { "Content-Type": "image/svg+xml", "Cache-Control": "no-store", "Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; sandbox" });
      const variants = { "quality-clean": "clean", "quality-medium": "medium", "quality-high": "high",
        "quality-missing": "missing", "quality-vsix-short": "v6-short", "quality-vsix-missing": "v6-missing" };
      const variant = variants[path.split("/")[4]];
      const fixture = variant ? `ip-quality-${variant}.svg` : path.endsWith("/6") ? "ip-quality-report-v6.svg" : "ip-quality-report.svg";
      const svg = await readFile(join(root, "testdata", fixture), "utf8");
      response.end(svg);
      return;
    }
    let file;
    if (path === "/assets/app.css") file = join(root, "app.css");
    else if (path === "/assets/app.js") file = join(root, "app.js");
    else if (path === "/assets/agents_browser_runtime.mjs")
      file = join(root, "agents_browser_runtime.mjs");
    else if (path === "/assets/config_restrictions_browser_runtime.mjs")
      file = join(root, "config_restrictions_browser_runtime.mjs");
    else if (path === "/assets/config_inbounds_browser_runtime.mjs")
      file = join(root, "config_inbounds_browser_runtime.mjs");
    else if (path === "/assets/config_migration_browser_runtime.mjs")
      file = join(root, "config_migration_browser_runtime.mjs");
    else if (path === "/assets/config_scope_browser_runtime.mjs")
      file = join(root, "config_scope_browser_runtime.mjs");
    else if (path === "/assets/users_browser_runtime.mjs")
      file = join(root, "users_browser_runtime.mjs");
    else if (path === "/assets/sharing_browser_runtime.mjs")
      file = join(root, "sharing_browser_runtime.mjs");
    else if (path === "/assets/presets_browser_runtime.mjs")
      file = join(root, "presets_browser_runtime.mjs");
    else if (path === "/assets/dashboard_browser_runtime.mjs")
      file = join(root, "dashboard_browser_runtime.mjs");
    else if (/^\/assets\/browser\/[a-z0-9-]+\.mjs$/.test(path))
      file = join(root, "browser", path.slice("/assets/browser/".length));
    else if (path.startsWith("/assets/modules/"))
      file = join(root, "modules", path.slice("/assets/modules/".length));
    if (!file) {
      response.writeHead(404).end();
      return;
    }
    response.writeHead(200, { "Content-Type": mime(file) });
    response.end(await readFile(file));
  } catch (error) {
    response.writeHead(500, { "Content-Type": "text/plain; charset=utf-8" });
    response.end(String(error?.stack || error));
  }
});

if (isMain) await new Promise((resolve, reject) => {
  server.once("error", reject);
  server.listen(0, "127.0.0.1", resolve);
});

// Reuse the same fixture for manual layout inspection, without launching the
// automated browser runner. No production API or credentials are involved.
if (isMain && process.env.QCH_BROWSER_SMOKE_SERVE_ONLY) {
  process.stdout.write(`http://127.0.0.1:${server.address().port}/agents-browser-smoke.html?mode=logs&preview=1#node-settings\n`);
  await new Promise(() => {});
}

const chrome = !isMain ? null : [
  process.env.QCH_CHROME_BIN,
  "chromium",
  "chromium-browser",
  "google-chrome",
  "google-chrome-stable",
]
  .filter(Boolean)
  .find((candidate) =>
    candidate.includes("/")
      ? spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0
      : spawnSync("which", [candidate], { stdio: "ignore" }).status === 0,
  );

const address = server.address();

const delay = (milliseconds) =>
  new Promise((resolve) => setTimeout(resolve, milliseconds).unref());

const stderrLimit = 8192;
const diagnosticText = (value, limit = 2048) => String(value ?? "").slice(0, limit);
const remember = (events, event) => {
  events.push(event);
  if (events.length > 8) events.shift();
};
const describeException = details => ({
  text: diagnosticText(details.text),
  description: diagnosticText(details.exception?.description),
  url: diagnosticText(details.url, 1024),
  line: details.lineNumber,
  column: details.columnNumber,
  frames: (details.stackTrace?.callFrames || []).slice(0, 6).map(frame => ({
    function: diagnosticText(frame.functionName, 128), url: diagnosticText(frame.url, 1024),
    line: frame.lineNumber, column: frame.columnNumber,
  })),
});

// This snapshot runs only after a failure. Its separate bound cannot extend
// a mode's passing deadline, including when the renderer stops responding.
const pageSnapshot = `({
  url: location.href.slice(0, 2048),
  readyState: document.readyState,
  visibility: document.visibilityState,
  focused: document.hasFocus(),
  marker: document.documentElement?.dataset.browserSmoke?.slice(0, 128) || null,
  routeCoverage: document.documentElement?.dataset.motionCoveragePages?.slice(0, 1024) || null,
  result: document.querySelector("#browser-smoke-result")?.textContent?.slice(0, 2048) || null,
  body: document.body?.textContent?.slice(0, 2048) || null,
  activeElement: document.activeElement?.outerHTML?.slice(0, 512) || null,
  workspace: document.querySelector(".workspace-main")?.className?.slice(0, 256) || null,
  busy: document.querySelector(".workspace-main")?.getAttribute("aria-busy")?.slice(0, 128) || null,
  gesture: window.__motionGesture?.action?.slice(0, 128) || null,
  animations: document.getAnimations().slice(0, 12).map(animation => ({
    id: animation.id.slice(0, 128), name: (animation.animationName || animation.transitionProperty)?.slice(0, 128) || null,
    state: animation.playState, time: animation.currentTime,
  })),
})`;

export function smokeFailure(mode, url, error, diagnostics, stderr = "") {
  return new Error(
    `Chrome ${mode} smoke failed (${url}): ${diagnosticText(error?.message || error)}\n` +
    `Browser diagnostics: ${JSON.stringify(diagnostics)}\n` +
    `Chrome stderr tail (last ${stderrLimit} chars):\n${stderr.slice(-stderrLimit)}`,
    { cause: error },
  );
}

async function waitForPageTarget(debugOrigin, expectedURL) {
  const deadline = Date.now() + 10000;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${debugOrigin}/json/list`);
      const targets = await response.json();
      const target = targets.find(
        (candidate) =>
          candidate.type === "page" && candidate.url.startsWith(expectedURL),
      );
      if (target?.webSocketDebuggerUrl) return target.webSocketDebuggerUrl;
    } catch (error) {
      lastError = error;
    }
    await delay(100);
  }
  throw new Error(`无法连接浏览器页面调试目标：${lastError || expectedURL}`);
}

// Real input drives drag regressions. The page fixture publishes geometry;
// desktop mouse and mobile touch use the browser's native pointer capture.
async function inputPoint(send, point) {
  // DOM client coordinates include the visual viewport's origin. CDP input
  // starts at that origin; Chromium applies browser/pinch scale itself.
  const result = await send("Runtime.evaluate", { expression: "({x:visualViewport.offsetLeft,y:visualViewport.offsetTop})", returnByValue: true });
  const origin = result.result.value;
  return { x: point.x - origin.x, y: point.y - origin.y };
}

async function driveMotionGestures(send, mobile) {
  for (const expected of ["drop", "cancel", "leave"]) {
    const result = await send("Runtime.evaluate", {
      expression: `new Promise(resolve => { const timer = setInterval(() => {
        if (window.__motionGesture || document.documentElement.dataset.browserSmoke) {
          clearInterval(timer); resolve(window.__motionGesture || null);
        }
      }, 10); })`, awaitPromise: true, returnByValue: true,
    });
    const gesture = result.result?.value;
    if (!gesture) return;
    assert.equal(gesture.action, expected);
    const start = await inputPoint(send, gesture.start);
    if (mobile) {
      await send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [start] });
      await send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [await inputPoint(send, gesture.end)] });
    } else {
      await send("Input.dispatchMouseEvent", { type: "mouseMoved", ...start });
      await send("Input.dispatchMouseEvent", { type: "mousePressed", ...start, button: "left", buttons: 1, clickCount: 1 });
      await send("Input.dispatchMouseEvent", { type: "mouseMoved", ...await inputPoint(send, gesture.end), button: "left", buttons: 1 });
    }
    if (expected === "cancel")
      await send("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    if (expected === "leave") {
      await send("Runtime.evaluate", { expression: `location.hash = "#core-logs"` });
      await delay(80);
    }
    if (mobile) await send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    else await send("Input.dispatchMouseEvent", { type: "mouseReleased", ...await inputPoint(send, gesture.end), button: "left", buttons: 0, clickCount: 1 });
    await send("Runtime.evaluate", { expression: "window.__motionGesture = null" });
  }
}

async function driveCardMoveGestures(send, mobile) {
  const evaluate = async expression => (await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true })).result?.value;
  const wait = expression => evaluate(`new Promise(resolve => { const timer = setInterval(() => {
    if (${expression} || document.documentElement.dataset.browserSmoke) { clearInterval(timer); resolve(document.documentElement.dataset.browserSmoke ? null : (${expression})); }
  }, 10); })`);
  while (true) {
    const gesture = await wait("window.__cardMoveViewport || window.__cardMoveGesture");
    if (!gesture) return;
    if (gesture.action === "viewport") {
      await send("Emulation.setPageScaleFactor", { pageScaleFactor: gesture.scale });
      if (gesture.scale > 1) {
        // Pan the zoomed visual viewport with native touch, then cancel to
        // prevent a fling or a content click before the measured grip pickup.
        await send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: 180, y: 400 }] });
        for (let step = 1; step <= 5; step++) {
          await send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: 180 - step * 14, y: 400 - step * 12 }] });
          await evaluate("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
        }
        await send("Input.dispatchTouchEvent", { type: "touchCancel", touchPoints: [] });
      }
      await evaluate("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
      await evaluate("window.__cardMoveViewport = null");
      continue;
    }
    const start = await inputPoint(send, gesture.start);
    if (mobile) await send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [start] });
    else {
      await send("Input.dispatchMouseEvent", { type: "mouseMoved", ...start });
      await send("Input.dispatchMouseEvent", { type: "mousePressed", ...start, button: "left", buttons: 1, clickCount: 1 });
    }
    // A CDP acknowledgement can precede native pointer delivery. In particular,
    // a new touch must not race the preceding touch's release during landing.
    if (await wait("window.__cardMoveInput.pressed") === null) return;
    for (let index = 0; index < gesture.points.length; index++) {
      const point = gesture.points[index];
      const input = await inputPoint(send, point);
      if (mobile) await send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [input] });
      else await send("Input.dispatchMouseEvent", { type: "mouseMoved", ...input, button: "left", buttons: 1 });
      // Chromium may coalesce native touch moves until its next input frame.
      // Inspect delivered input, rather than racing CDP acknowledgement.
      if (await wait(`window.__cardMoveInput.cancelled || (window.__cardMoveInput.point && Math.hypot(window.__cardMoveInput.point.x - ${point.x}, window.__cardMoveInput.point.y - ${point.y}) < 1)`) === null) return;
      await evaluate("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
      await evaluate(`window.__cardMoveStep = ${index}`);
      if (await wait(`window.__cardMoveAck === ${index}`) === null) return;
    }
    if (gesture.release === "escape") await send("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    if (gesture.release === "leave") { await evaluate('location.hash = "#core-logs"'); await delay(80); }
    if (mobile) await send("Input.dispatchTouchEvent", { type: gesture.release === "cancel" ? "touchCancel" : "touchEnd", touchPoints: [] });
    else {
      if (gesture.release === "cancel") await evaluate('document.activeElement.releasePointerCapture(1)');
      await send("Input.dispatchMouseEvent", { type: "mouseReleased", ...await inputPoint(send, gesture.points.at(-1)), button: "left", buttons: 0, clickCount: 1 });
    }
    if (await wait("window.__cardMoveInput.ended") === null) return;
    await evaluate("window.__cardMoveGesture = null");
  }
}


export async function observeSmokeResult(webSocketURL, navigateURL, mobile, reduced, motionMode, gestureMode, diagnostics = {}) {
  diagnostics.cdp = { state: "connecting" };
  diagnostics.runtimeExceptions = [];
  diagnostics.consoleErrors = [];
  let socket;
  try { socket = new WebSocket(webSocketURL); }
  catch (error) {
    diagnostics.cdp.error = diagnosticText(error.message || error);
    diagnostics.page = { unavailable: "CDP connection was not established" };
    throw error;
  }
  let requestID = 0;
  let cleaningUp = false;
  let connectionFailure;
  const pending = new Map();
  const rejectPending = error => {
    connectionFailure = error;
    for (const request of pending.values()) request.reject(error);
    pending.clear();
  };
  socket.addEventListener("open", () => { diagnostics.cdp.state = "open"; });
  socket.addEventListener("close", event => {
    const error = new Error(`CDP socket closed (${event.code}): ${diagnosticText(event.reason)}`);
    if (!cleaningUp) {
      diagnostics.cdp.state = "closed";
      diagnostics.cdp.close = { code: event.code, reason: diagnosticText(event.reason) };
    }
    rejectPending(error);
  });
  socket.addEventListener("error", event => {
    const error = new Error(diagnosticText(event.message || event.error?.message || "CDP socket error"));
    if (!cleaningUp) diagnostics.cdp.error = error.message;
    rejectPending(error);
  });
  socket.addEventListener("message", (event) => {
    let message;
    try { message = JSON.parse(String(event.data)); }
    catch (error) {
      diagnostics.cdp.error = diagnosticText(`invalid CDP message: ${error.message}`);
      rejectPending(error);
      return;
    }
    if (message.method === "Runtime.exceptionThrown")
      remember(diagnostics.runtimeExceptions, describeException(message.params.exceptionDetails));
    if (message.method === "Runtime.consoleAPICalled" && message.params.type === "error")
      remember(diagnostics.consoleErrors, diagnosticText(message.params.args
        .map(argument => diagnosticText(argument.value ?? argument.description, 512)).join(" ")));
    if (message.method === "Inspector.targetCrashed" || message.method === "Inspector.detached") {
      diagnostics.cdp.failure = { event: message.method, reason: diagnosticText(message.params?.reason) };
      rejectPending(new Error(message.method));
    }
    if (!message.id || !pending.has(message.id)) return;
    const { resolve, reject } = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) reject(new Error(JSON.stringify(message.error)));
    else resolve(message.result);
  });

  const send = (method, params = {}, timeout) =>
    new Promise((resolve, reject) => {
      if (connectionFailure) { reject(connectionFailure); return; }
      if (socket.readyState !== WebSocket.OPEN) {
        reject(new Error(`CDP ${method} unavailable: socket state ${socket.readyState}`));
        return;
      }
      const id = ++requestID;
      let timer;
      const settle = callback => value => { clearTimeout(timer); callback(value); };
      pending.set(id, { resolve: settle(resolve), reject: settle(reject), method });
      if (timeout) timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`${method} exceeded ${timeout}ms`));
      }, timeout);
      try { socket.send(JSON.stringify({ id, method, params })); }
      catch (error) { clearTimeout(timer); pending.delete(id); reject(error); }
    });

  const capture = async () => {
    diagnostics.cdp.pendingRequests = [...pending.values()].slice(-8).map(request => request.method);
    if (socket.readyState !== WebSocket.OPEN) {
      diagnostics.page = { unavailable: `CDP socket state ${socket.readyState}` };
      return;
    }
    try {
      const snapshot = await send("Runtime.evaluate", { expression: pageSnapshot, returnByValue: true }, 1500);
      diagnostics.page = snapshot.exceptionDetails
        ? { unavailable: describeException(snapshot.exceptionDetails) }
        : snapshot.result?.value || { unavailable: "page snapshot returned no value" };
    } catch (error) {
      diagnostics.page = { unavailable: diagnosticText(error.message || error) };
    }
  };

  try {
    await Promise.race([
      new Promise((resolve, reject) => {
        socket.addEventListener("open", resolve, { once: true });
        socket.addEventListener("error", () => reject(new Error(diagnostics.cdp.error)), { once: true });
        socket.addEventListener("close", () => reject(new Error("CDP socket closed before opening")), { once: true });
      }),
      delay(10000).then(() => { throw new Error("连接浏览器调试目标超时"); }),
    ]);
    // Diagnostic subscriptions must not delay setup or move the smoke deadline.
    // A stalled subscription is recorded without failing a working fixture.
    diagnostics.cdp.subscriptions = {};
    for (const method of ["Runtime.enable", "Inspector.enable"]) {
      diagnostics.cdp.subscriptions[method] = "pending";
      send(method, {}, 1500).then(
        () => { diagnostics.cdp.subscriptions[method] = "enabled"; },
        error => { diagnostics.cdp.subscriptions[method] = diagnosticText(error.message || error); },
      );
    }
    if (motionMode) await send("Emulation.setFocusEmulationEnabled", { enabled: true });
    if (navigateURL) {
      await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: reduced ? "reduce" : "no-preference" }] });
    }
    if (mobile) {
      // A narrow desktop window is not a touch device and Chrome may clamp its
      // width. Apply actual mobile metrics before the fixture starts running.
      await send("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
      await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 1 });
    }
    if (process.env.QCH_BROWSER_SMOKE_ZOOM) {
      const zoom = Number(process.env.QCH_BROWSER_SMOKE_ZOOM);
      assert.ok(zoom >= .5 && zoom <= 2, "browser zoom must be between 50% and 200%");
      await send("Page.navigate", { url: "chrome://settings/appearance" });
      let ready = false;
      for (let attempt = 0; attempt < 100 && !ready; attempt++) {
        const result = await send("Runtime.evaluate", { expression: "!!globalThis.chrome?.settingsPrivate?.setDefaultZoom", returnByValue: true });
        ready = result.result?.value; if (!ready) await delay(25);
      }
      assert.ok(ready, "Chromium settings zoom API was unavailable");
      const result = await send("Runtime.evaluate", { expression: `new Promise(resolve => chrome.settingsPrivate.setDefaultZoom(${zoom}, () => chrome.settingsPrivate.getDefaultZoom(resolve)))`, awaitPromise: true, returnByValue: true });
      assert.equal(result.result?.value, zoom, "browser must apply the requested zoom");
    }
    if (navigateURL) await send("Page.navigate", { url: navigateURL });
    // Popup/selection suites need focus emulation, but publish no drag input.
    // Full popup playback now samples all 36 theme/route combinations.
    // Its deadline covers this extra choreography; interaction budgets are unchanged.
    if (gestureMode) await Promise.race([
      navigateURL?.includes("mode=motion-cards") ? driveCardMoveGestures(send, mobile) : driveMotionGestures(send, mobile),
      delay(navigateURL?.includes("mode=motion-cards") ? 90000 : 30000).then(() => { throw new Error("motion input timed out"); }),
    ]);
    const deadline = Date.now() + (navigateURL?.includes("mode=motion-popup") || navigateURL?.includes("mode=ip-quality") ? 60000 : 45000);
    let evaluation;
    while (!evaluation && Date.now() < deadline) {
      try {
        evaluation = await Promise.race([
          send("Runtime.evaluate", {
            expression: `new Promise((resolve) => {
          const timer = setInterval(() => {
            const status = document.documentElement?.dataset.browserSmoke;
            if (!status) return;
            clearInterval(timer);
            resolve({
              status,
              detail: document.querySelector("#browser-smoke-result")?.textContent || "",
              measurements: window.cardCoordinateMeasurements || window.ipQualityMeasurements,
            });
          }, 10);
        })`,
            awaitPromise: true,
            returnByValue: true,
          }),
          delay(Math.max(1, deadline - Date.now())).then(() => {
            throw new Error("等待浏览器 smoke 完成标记超时");
          }),
        ]);
      } catch (error) {
        if (!String(error).includes("Execution context was destroyed")) throw error;
        await delay(100);
      }
    }
    if (!evaluation) throw new Error("等待浏览器 smoke 完成标记超时");
    if (evaluation.exceptionDetails)
      throw new Error(JSON.stringify(evaluation.exceptionDetails));
    const result = evaluation.result?.value;
    if (result?.status !== "passed") await capture();
    return result;
  } catch (error) {
    await capture();
    throw error;
  } finally {
    cleaningUp = true;
    rejectPending(new Error("CDP socket closed during cleanup"));
    try { socket.close(); }
    catch (error) { diagnostics.cdp.cleanupError = diagnosticText(error.message); }
  }
}

const activeBrowsers = new Set();
const activeProfiles = new Set();
async function stopBrowser(child) {
  if (!child.pid) return;
  const kill = signal => {
    try { process.kill(-child.pid, signal); }
    catch (error) { if (error.code !== "ESRCH") throw error; }
  };
  if (child.exitCode === null && child.signalCode === null) {
    const exited = new Promise(resolve => child.once("exit", resolve));
    kill("SIGTERM");
    await Promise.race([exited, delay(3000)]);
  }
  kill("SIGKILL");
  activeBrowsers.delete(child);
}
if (isMain) for (const [signal, code] of [["SIGINT", 130], ["SIGTERM", 143]]) {
  process.once(signal, async () => {
    process.stderr.write(`browser worker cancelled by ${signal}\n`);
    await Promise.all([...activeBrowsers].map(stopBrowser));
    await Promise.all([...activeProfiles].map(profile => rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 })));
    server.closeAllConnections();
    server.close();
    process.exit(code);
  });
}

async function runMode(mode) {
  const timing = { mode, startupMs: null, testMs: null, cleanupMs: null, passed: false };
  const started = performance.now();
  let testStarted;
  const url = `http://127.0.0.1:${address.port}/agents-browser-smoke.html?mode=${mode}#node-settings`;
  const diagnostics = {
    browser: { exitCode: null, signalCode: null },
    cdp: { state: "not-connected" },
    page: { unavailable: "CDP connection was not established" },
  };
  let profile;
  let child;
  let stderr = "";
  try {
    assert.ok(chrome, "真实浏览器 smoke 需要 Google Chrome 或 Chromium");
    profile = await mkdtemp(join(tmpdir(), `qcontrolhub-browser-${mode}-`));
    activeProfiles.add(profile);
    await chmod(profile, 0o700);
    const mobile = (mode.startsWith("ip-quality") && mode.includes("mobile")) || (mode.startsWith("motion-cards") && mode.includes("mobile")) || mode.startsWith("motion-mobile") || mode.startsWith("motion-popup-mobile") || mode.startsWith("motion-selection-mobile") || ["logs-mobile", "config-layout-mobile", "connections-mobile", "batch-layout-mobile", "config-inbounds-mobile", "substore-scope", "users-mobile", "users-layout-mobile", "sharing-mobile", "shared-node-mobile", "enrollment-mobile", "client-order-mobile", "client-layout-mobile", "dashboard-mobile", "bbr-mobile", "shell-layout-mobile", "capabilities-settings-mobile", "ports-mobile", "traffic-layout-mobile", "traffic-layout-dense-mobile", "ip-quality-mobile"].includes(mode);
    const initialURL = mobile || mode.startsWith("motion") || mode.endsWith("reduced") ? "about:blank" : url;
    child = spawn(
      chrome,
      [
        "--headless=new",
        ...(process.env.QCH_BROWSER_SMOKE_DPR ? [`--force-device-scale-factor=${Number(process.env.QCH_BROWSER_SMOKE_DPR)}`] : []),
        "--no-sandbox",
        "--disable-gpu",
        "--disable-dev-shm-usage",
        "--disable-background-networking",
        "--disable-client-side-phishing-detection",
        "--disable-component-update",
        "--disable-default-apps",
        "--disable-domain-reliability",
        "--disable-extensions",
        "--disable-sync",
        "--metrics-recording-only",
        "--no-first-run",
        "--disable-features=AutofillServerCommunication,CertificateTransparencyComponentUpdater,MediaRouter,OptimizationHints",
        "--hide-scrollbars",
        // Match a mouse-equipped desktop for TCP and shell layout checks. Headless
        // Chromium otherwise reports pointer:none and misses desktop CSS.
        ...((mode.startsWith("bbr") || mode.startsWith("shell-layout") || mode.startsWith("motion")) && !mobile ? ["--blink-settings=primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4"] : []),
        mobile ? "--window-size=390,844" : mode === "shell-layout" ? "--window-size=820,900" : mode === "traffic-layout-dense" ? "--window-size=1960,1100" : "--window-size=1280,900",
        `--user-data-dir=${profile}`,
        "--remote-debugging-port=0",
        initialURL,
      ],
      { detached: true, stdio: ["ignore", "ignore", "pipe"] },
    );
    activeBrowsers.add(child);
    child.once("error", error => { diagnostics.browser.error = diagnosticText(error.message); });
    child.once("exit", (exitCode, signalCode) => { diagnostics.browser = { exitCode, signalCode }; });
    const debugOrigin = await Promise.race([
      new Promise((resolve, reject) => {
        child.stderr.on("data", (chunk) => {
          const output = stderr + chunk;
          const match = output.match(
            /DevTools listening on ws:\/\/(127\.0\.0\.1|localhost):(\d+)\//,
          );
          stderr = output.slice(-stderrLimit);
          if (match) resolve(`http://127.0.0.1:${match[2]}`);
        });
        child.once("error", reject);
        child.once("exit", (status) =>
          reject(new Error(`Chrome ${mode} 提前退出（${status}）：${stderr}`)),
        );
      }),
      delay(30000).then(() => {
        throw new Error(`Chrome ${mode} 调试端口启动超时：${stderr}`);
      }),
    ]);
    timing.startupMs = performance.now() - started;
    testStarted = performance.now();
    const pageTarget = await waitForPageTarget(debugOrigin, initialURL);
    const gestureMode = mode.startsWith("motion-cards") || ["motion", "motion-reduced", "motion-mobile", "motion-mobile-reduced"].includes(mode);
    const result = await observeSmokeResult(pageTarget, initialURL === "about:blank" ? url : undefined, mobile, mode.endsWith("reduced"), mode.startsWith("motion"), gestureMode, diagnostics);
    assert.equal(
      result?.status,
      "passed",
      `Chrome ${mode} smoke 未通过：${result?.detail || "无错误详情"}`,
    );
    timing.passed = true;
    if (mode === "logs" || mode.startsWith("motion-cards")) process.stdout.write(`${result.detail}\n`);
    if (result.measurements && process.env.QCH_BROWSER_SMOKE_REPORT) {
      await mkdir(process.env.QCH_BROWSER_SMOKE_REPORT, { recursive: true });
      await writeFile(join(process.env.QCH_BROWSER_SMOKE_REPORT, `${mode}.json`), JSON.stringify(result.measurements, null, 2) + "\n");
    }
  } catch (error) {
    throw smokeFailure(mode, url, error, diagnostics, stderr);
  } finally {
    const cleanupStarted = performance.now();
    timing.testMs = testStarted ? cleanupStarted - testStarted : null;
    if (child) await stopBrowser(child);
    if (profile) await rm(profile, {
      recursive: true,
      force: true,
      maxRetries: 5,
      retryDelay: 100,
    });
    activeProfiles.delete(profile);
    timing.cleanupMs = performance.now() - cleanupStarted;
    timing.elapsedMs = performance.now() - started;
    process.stdout.write(`browser timing ${JSON.stringify(timing)}\n`);
    if (process.env.QCH_CI_REPORT_DIR) {
      await mkdir(process.env.QCH_CI_REPORT_DIR, { recursive: true });
      await writeFile(join(process.env.QCH_CI_REPORT_DIR, `browser-${mode}.json`), JSON.stringify(timing) + "\n");
    }
  }
}

if (isMain) try {
  const modes = process.env.QCH_BROWSER_SMOKE_MODES || process.env.QCH_BROWSER_SMOKE_MODE || "motion-cards-coordinates,motion-cards-coordinates-reduced,motion-cards-coordinates-mobile,motion-cards-coordinates-mobile-reduced,motion-cards,motion-cards-reduced,motion-cards-mobile,motion-cards-mobile-reduced,motion-selection,motion-selection-reduced,motion-selection-mobile,motion-selection-mobile-reduced,motion-popup,motion-popup-reduced,motion-popup-mobile,motion-popup-mobile-reduced,motion,motion-reduced,motion-mobile,motion-mobile-reduced,connections,connections-mobile,admin,uninstall-writeonly,batch-layout,batch-layout-mobile,empty,enrollment,enrollment-mobile,readonly,ports,ports-mobile,client-order,client-order-mobile,client-layout,client-layout-mobile,dashboard,dashboard-mobile,dashboard-readonly,dashboard-limited,dashboard-unavailable,regions,logs,logs-mobile,logs-restore,bbr,bbr-mobile,bbr-readonly,bbr-writeonly,config-restrictions,config-inbounds,config-inbounds-mobile,config-migration,config-scope,archive-engines,substore-scope,substore-layout,users,users-mobile,users-layout,users-layout-mobile,sharing,sharing-mobile,shared-node,shared-node-mobile,config-layout,config-layout-mobile,traffic-layout,traffic-layout-mobile,traffic-layout-dense,traffic-layout-dense-mobile,capabilities-settings,capabilities-settings-mobile,capabilities-settings-readonly,shell-layout,shell-layout-mobile,presets,ip-quality,ip-quality-mobile,ip-quality-readonly,ip-quality-reduced,ip-quality-mobile-reduced";
  const selected = modes.split(",");
  assert.ok(selected.every(mode => /^[a-z0-9-]+$/.test(mode)), "invalid browser mode");
  assert.equal(new Set(selected).size, selected.length, "duplicate browser mode");
  if (process.env.QCH_BROWSER_SMOKE_WORKER) {
    for (const mode of selected) await runMode(mode);
  } else {
    await runBrowserWorkers(fileURLToPath(import.meta.url), selected);
  }
  process.stdout.write("agents browser runtime smoke passed\n");
} finally {
  await new Promise((resolve) => server.close(resolve));
}
