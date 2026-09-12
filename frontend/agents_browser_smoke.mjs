import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { chmod, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css"></head><body><div id="app"><div class="boot">测试载入中</div></div><script type="module" src="/assets/agents_browser_runtime.mjs"></script></body></html>`;

const mime = (path) =>
  path.endsWith(".css")
    ? "text/css; charset=utf-8"
    : path.endsWith(".js") || path.endsWith(".mjs")
      ? "text/javascript; charset=utf-8"
      : "text/html; charset=utf-8";

const previewFlags = new Map();
let presetCatalog;
const server = createServer(async (request, response) => {
  try {
    const path = new URL(request.url, "http://127.0.0.1").pathname;
    if (process.env.QCH_BROWSER_SMOKE_DEBUG) process.stderr.write(`${path}\n`);
    if (path === "/" || path === "/agents-browser-smoke.html") {
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
    let file;
    if (path === "/assets/app.css") file = join(root, "app.css");
    else if (path === "/assets/app.js") file = join(root, "app.js");
    else if (path === "/assets/agents_browser_runtime.mjs")
      file = join(root, "agents_browser_runtime.mjs");
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

await new Promise((resolve, reject) => {
  server.once("error", reject);
  server.listen(0, "127.0.0.1", resolve);
});

// Reuse the same fixture for manual layout inspection, without launching the
// automated browser runner. No production API or credentials are involved.
if (process.env.QCH_BROWSER_SMOKE_SERVE_ONLY) {
  process.stdout.write(`http://127.0.0.1:${server.address().port}/agents-browser-smoke.html?mode=logs&preview=1#node-settings\n`);
  await new Promise(() => {});
}

const chrome = [
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

assert.ok(chrome, "真实浏览器 smoke 需要 Google Chrome 或 Chromium");
const address = server.address();

const delay = (milliseconds) =>
  new Promise((resolve) => setTimeout(resolve, milliseconds));

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

async function observeSmokeResult(webSocketURL, mobileURL) {
  const socket = new WebSocket(webSocketURL);
  await Promise.race([
    new Promise((resolve, reject) => {
      socket.addEventListener("open", resolve, { once: true });
      socket.addEventListener("error", reject, { once: true });
    }),
    delay(10000).then(() => {
      throw new Error("连接浏览器调试目标超时");
    }),
  ]);

  let requestID = 0;
  const pending = new Map();
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data));
    if (!message.id || !pending.has(message.id)) return;
    const { resolve, reject } = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) reject(new Error(JSON.stringify(message.error)));
    else resolve(message.result);
  });

  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const id = ++requestID;
      pending.set(id, { resolve, reject });
      socket.send(JSON.stringify({ id, method, params }));
    });

  try {
    if (mobileURL) {
      // A narrow desktop window is not a touch device and Chrome may clamp its
      // width. Apply actual mobile metrics before the fixture starts running.
      await send("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
      await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 1 });
      await send("Page.navigate", { url: mobileURL });
    }
    const deadline = Date.now() + 45000;
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
    return evaluation.result?.value;
  } finally {
    socket.close();
  }
}

async function stopBrowser(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = new Promise((resolve) => child.once("exit", resolve));
  child.kill("SIGTERM");
  await Promise.race([
    exited,
    delay(3000).then(() => {
      child.kill("SIGKILL");
      return exited;
    }),
  ]);
}

async function runMode(mode) {
  const profile = await mkdtemp(join(tmpdir(), `qcontrolhub-browser-${mode}-`));
  let child;
  try {
    await chmod(profile, 0o700);
    const url = `http://127.0.0.1:${address.port}/agents-browser-smoke.html?mode=${mode}#node-settings`;
    const mobile = ["substore-scope", "users-mobile", "sharing-mobile", "shared-node-mobile"].includes(mode);
    const initialURL = mobile ? "about:blank" : url;
    child = spawn(
      chrome,
      [
        "--headless=new",
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
        mobile ? "--window-size=390,844" : "--window-size=1280,900",
        `--user-data-dir=${profile}`,
        "--remote-debugging-port=0",
        initialURL,
      ],
      { stdio: ["ignore", "ignore", "pipe"] },
    );
    let stderr = "";
    const debugOrigin = await Promise.race([
      new Promise((resolve, reject) => {
        child.stderr.on("data", (chunk) => {
          stderr += chunk;
          const match = stderr.match(
            /DevTools listening on ws:\/\/(127\.0\.0\.1|localhost):(\d+)\//,
          );
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
    const pageTarget = await waitForPageTarget(debugOrigin, initialURL);
    const result = await observeSmokeResult(pageTarget, mobile ? url : undefined);
    assert.equal(
      result?.status,
      "passed",
      `Chrome ${mode} smoke 未通过：${result?.detail || "无错误详情"}\n${stderr}`,
    );
    if (mode === "logs") process.stdout.write(`${result.detail}\n`);
  } finally {
    if (child) await stopBrowser(child);
    await rm(profile, {
      recursive: true,
      force: true,
      maxRetries: 5,
      retryDelay: 100,
    });
  }
}

try {
  const modes = process.env.QCH_BROWSER_SMOKE_MODES || process.env.QCH_BROWSER_SMOKE_MODE || "admin,empty,readonly,ports,regions,logs,logs-restore,bbr,bbr-readonly,bbr-writeonly,config-migration,config-scope,substore-scope,users,users-mobile,sharing,sharing-mobile,shared-node,shared-node-mobile,config-layout,traffic-layout,capabilities-settings,capabilities-settings-readonly,presets";
  for (const mode of modes.split(",")) await runMode(mode);
  process.stdout.write("agents browser runtime smoke passed\n");
} finally {
  await new Promise((resolve) => server.close(resolve));
}
