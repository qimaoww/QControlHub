#!/usr/bin/env node
// Record the loopback fixture only; never save production sessions or logs.
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
const [address, label = "desktop"] = process.argv.slice(2);
const url = new URL(address);
if ((!["127.0.0.1", "localhost"].includes(url.hostname) && url.origin !== "http://192.168.31.205:18781") || !["desktop", "mobile"].includes(label))
  throw new Error("usage: node scripts/record-core-logs.mjs <isolated-fixture-url> desktop|mobile");
const output = resolve("output/playwright"); mkdirSync(output, { recursive: true });
const temporary = mkdtempSync(join(tmpdir(), "qch-log-recorder-"));
const config = join(temporary, "browser.json");
const mobile = label === "mobile";
writeFileSync(config, JSON.stringify({ browser: { browserName: "chromium", launchOptions: { channel: "chrome", chromiumSandbox: process.getuid?.() !== 0 }, contextOptions: { viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 960 }, isMobile: mobile, hasTouch: mobile } }, outputDir: output }));
const session = `qch-logs-${label}`;
function cli(...args) {
  const executable = process.env.PLAYWRIGHT_CLI || "npx";
  const prefix = process.env.PLAYWRIGHT_CLI ? [] : ["--yes", "--package", "@playwright/cli@0.1.23", "playwright-cli"];
  const result = spawnSync(executable, [...prefix, `-s=${session}`, ...args], { encoding: "utf8" });
  if (result.status !== 0 || result.stdout.includes("### Error")) throw new Error(result.stdout + result.stderr);
}
const sequence = `async page => {
  if (!await page.evaluate(() => window.__coreLogPerformanceFixture === true)) throw new Error('Use the synthetic performance fixture');
  await page.evaluate(()=>{ location.hash='#core-logs'; });
  await page.waitForSelector('[data-log-entry]');
  await page.getByRole('button', { name: '暂停更新', exact: true }).or(page.getByRole('button', { name: '自动更新', exact: true })).click();
  await page.waitForFunction(()=>document.querySelector('[data-log-refresh-label]')?.textContent==='自动更新已暂停');
  await page.waitForTimeout(700);
  await page.locator('select[name=limit]').selectOption('2000');
  await page.waitForFunction(()=>document.querySelector('[data-log-refresh-label]')?.nextElementSibling?.textContent.includes('8000 条日志'));
  await page.waitForTimeout(600);
  await page.locator('input[name=q]').fill('pressure entry 1999');
  await page.waitForTimeout(900);
  await page.getByRole('button', { name: '清除筛选', exact: true }).click();
  await page.getByRole('button', { name: '下一页', exact: true }).click();
  await page.waitForTimeout(900);
  await page.getByRole('button', { name: '上一页', exact: true }).click();
  await page.evaluate(()=>{const select = document.querySelector('[data-log-filters] select[name=agent_id]'); select.value = 'bravo'; select.dispatchEvent(new Event('change', { bubbles: true }));});
  await page.waitForTimeout(250);
  await page.evaluate(()=>{const select = document.querySelector('[data-log-filters] select[name=agent_id]'); select.value = 'charlie'; select.dispatchEvent(new Event('change', { bubbles: true }));});
  await page.waitForTimeout(100);
  await page.evaluate(()=>{const select = document.querySelector('[data-log-filters] select[name=agent_id]'); select.value = 'bravo'; select.dispatchEvent(new Event('change', { bubbles: true }));});
  await page.waitForFunction(()=>document.querySelector('[data-log-refresh-label]')?.textContent==='自动更新已暂停');
  await page.waitForTimeout(600);
  await page.getByRole('button', { name: '暂停更新', exact: true }).or(page.getByRole('button', { name: '自动更新', exact: true })).click();
  await page.locator('input[name=q]').fill('pressure entry 1900');
  await page.waitForTimeout(15000);
  await page.getByRole('button', { name: '暂停更新', exact: true }).or(page.getByRole('button', { name: '自动更新', exact: true })).click();
  await page.waitForTimeout(600);
}`;
try {
  cli("open", `${url.origin}/?mode=logs#core-logs`, `--config=${config}`);
  cli("snapshot", `--filename=${join(output, `record-${label}.yml`)}`);
  cli("video-start", join(output, `core-logs-${label}.webm`), `--size=${mobile ? "390x844" : "1440x960"}`, "--fps=30");
  cli("run-code", sequence);
  cli("video-stop");
  console.log(join(output, `core-logs-${label}.webm`));
} finally { cli("close"); rmSync(temporary, { recursive: true, force: true }); }
