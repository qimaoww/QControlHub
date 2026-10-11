#!/usr/bin/env node
// Record the loopback fixture only; never save production sessions or logs.
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
const [address, label = "desktop"] = process.argv.slice(2);
const url = new URL(address);
if (!["127.0.0.1", "localhost"].includes(url.hostname) || !["desktop", "mobile"].includes(label))
  throw new Error("usage: node scripts/record-core-logs.mjs http://127.0.0.1:PORT desktop|mobile");
const output = resolve("output/playwright"); mkdirSync(output, { recursive: true });
const temporary = mkdtempSync(join(tmpdir(), "qch-log-recorder-"));
const config = join(temporary, "browser.json");
const mobile = label === "mobile";
writeFileSync(config, JSON.stringify({ browser: { browserName: "chromium", launchOptions: { channel: "chrome", chromiumSandbox: process.getuid?.() !== 0 }, contextOptions: { viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 960 }, isMobile: mobile, hasTouch: mobile } }, outputDir: output }));
const session = `qch-logs-${label}`;
function cli(...args) {
  const executable = process.env.PLAYWRIGHT_CLI || "npx";
  const prefix = process.env.PLAYWRIGHT_CLI ? [] : ["--yes", "--package", "@playwright/cli@0.1.22", "playwright-cli"];
  const result = spawnSync(executable, [...prefix, `-s=${session}`, ...args], { encoding: "utf8" });
  if (result.status !== 0 || result.stdout.includes("### Error")) throw new Error(result.stdout + result.stderr);
}
const sequence = `async page => {
  await page.evaluate(()=>{ location.hash='#core-logs'; });
  await page.waitForSelector('.core-log-row');
  await page.locator('[data-toggle-core-log-refresh]').click();
  await page.waitForFunction(()=>document.querySelector('[data-core-log-refresh-label]')?.textContent==='自动更新已暂停');
  await page.waitForTimeout(700);
  await page.locator('select[name=limit]').selectOption('2000');
  await page.waitForFunction(()=>document.querySelector('.core-log-status')?.textContent.includes('已加载 8000 条'));
  await page.waitForTimeout(600);
  await page.locator('input[name=q]').fill('pressure entry 1999');
  await page.waitForTimeout(900);
  await page.locator('[data-reset-core-logs]').click();
  await page.locator('.core-log-result-toolbar [data-core-log-page-index="1"]').click();
  await page.waitForTimeout(900);
  await page.locator('.core-log-result-toolbar [data-core-log-page-index="0"]').click();
  await page.evaluate(()=>{document.querySelector('[data-core-log-agent="bravo"]').click();});
  await page.waitForTimeout(250);
  await page.evaluate(()=>{document.querySelector('[data-core-log-agent="charlie"]').click();});
  await page.waitForTimeout(100);
  await page.evaluate(()=>{document.querySelector('[data-core-log-agent="bravo"]').click();});
  await page.waitForFunction(()=>document.querySelector('[data-core-log-refresh-label]')?.textContent==='自动更新已暂停');
  await page.waitForTimeout(600);
  await page.locator('[data-toggle-core-log-refresh]').click();
  await page.locator('input[name=q]').fill('pressure entry 1900');
  await page.waitForTimeout(15000);
  await page.locator('[data-toggle-core-log-refresh]').click();
  await page.waitForTimeout(600);
}`;
try {
  cli("open", `${url.origin}/?mode=logs#node-settings`, `--config=${config}`);
  cli("snapshot", `--filename=${join(output, `record-${label}.yml`)}`);
  cli("video-start", join(output, `core-logs-${label}.webm`), `--size=${mobile ? "390x844" : "1440x960"}`, "--fps=30");
  cli("run-code", sequence);
  cli("video-stop");
  console.log(join(output, `core-logs-${label}.webm`));
} finally { cli("close"); rmSync(temporary, { recursive: true, force: true }); }
