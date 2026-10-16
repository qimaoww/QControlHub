// Real password/cookie/CSRF browser flow against the isolated Go API fixture.
// Reuse the native CDP observer; no npm/browser-test dependency is required.
import { observeSmokeResult } from "./agents_browser_smoke.mjs";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";
const profile = await mkdtemp(join(tmpdir(), "qch-core-install-session-"));
const child = spawn(process.env.QCH_CHROME_BIN || "chromium", ["--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--window-size=1440,1000", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"], {stdio:["ignore","ignore","pipe"]});
let stderr = "";
const timer = setTimeout(() => child.kill("SIGKILL"), 55000);
try {
  const origin = await new Promise((resolve, reject) => {
    child.stderr.on("data", chunk => {
      stderr = (stderr + chunk).slice(-8192);
      const match = stderr.match(/DevTools listening on ws:\/\/(127\.0\.0\.1:\d+)\//);
      if (match) resolve(`http://${match[1]}`);
    });
    child.once("error", reject);
    child.once("exit", code => reject(new Error(`Chrome exited ${code}: ${stderr}`)));
  });
  const targets = await (await fetch(`${origin}/json/list`)).json();
  const target = targets.find(item => item.type === "page");
  assert.ok(target, "missing browser page target");
  const diagnostics = {};
  const result = await observeSmokeResult(target.webSocketDebuggerUrl, process.argv[2], process.argv.includes("--mobile"), false, false, false, diagnostics);
  assert.equal(result.status, "passed", `${result.detail}\n${JSON.stringify(diagnostics)}`);
  console.log(result.detail);
} finally {
  clearTimeout(timer);
  child.kill("SIGKILL");
  await new Promise(resolve => child.exitCode !== null || child.signalCode !== null ? resolve() : child.once("exit", resolve));
  await rm(profile, {recursive:true,force:true,maxRetries:5,retryDelay:100});
}
