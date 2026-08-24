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

const server = createServer(async (request, response) => {
  try {
    const path = new URL(request.url, "http://127.0.0.1").pathname;
    if (process.env.QCH_BROWSER_SMOKE_DEBUG) process.stderr.write(`${path}\n`);
    if (path === "/" || path === "/agents-browser-smoke.html") {
      response.writeHead(200, { "Content-Type": mime(".html") });
      response.end(html);
      return;
    }
    let file;
    if (path === "/assets/app.css") file = join(root, "app.css");
    else if (path === "/assets/app.js") file = join(root, "app.js");
    else if (path === "/assets/agents_browser_runtime.mjs")
      file = join(root, "agents_browser_runtime.mjs");
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

async function runMode(mode) {
  const profile = await mkdtemp(join(tmpdir(), `qcontrolhub-browser-${mode}-`));
  try {
    await chmod(profile, 0o700);
    const url = `http://127.0.0.1:${address.port}/agents-browser-smoke.html?mode=${mode}#node-settings`;
    const child = spawn(
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
        "--window-size=1280,900",
        `--user-data-dir=${profile}`,
        "--virtual-time-budget=15000",
        "--dump-dom",
        url,
      ],
      { stdio: ["ignore", "pipe", "pipe"] },
    );
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => (stdout += chunk));
    child.stderr.on("data", (chunk) => (stderr += chunk));
    const timer = setTimeout(() => child.kill("SIGKILL"), 60000);
    const status = await new Promise((resolve) => child.once("exit", resolve));
    clearTimeout(timer);
    assert.equal(status, 0, `Chrome ${mode} smoke 退出失败：${stderr}`);
    assert.match(
      stdout,
      /data-browser-smoke="passed"/,
      `Chrome ${mode} smoke 未通过：${stdout}\n${stderr}`,
    );
  } finally {
    await rm(profile, { recursive: true, force: true });
  }
}

try {
  for (const mode of ["admin", "empty", "readonly"]) await runMode(mode);
  process.stdout.write("agents browser runtime smoke passed\n");
} finally {
  await new Promise((resolve) => server.close(resolve));
}
