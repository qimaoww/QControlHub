#!/usr/bin/env node
// Optional deterministic review recorder; no production API or credentials.
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";

const [origin, label = "after"] = process.argv.slice(2);
if (!origin || !/^[a-z0-9-]+$/.test(label)) throw new Error("usage: node scripts/record-motion.mjs http://127.0.0.1:PORT before|after");
const url = new URL(origin);
if (!["127.0.0.1", "localhost"].includes(url.hostname)) throw new Error("use the local agents_browser_smoke fixture server");
const output = resolve("output/playwright");
mkdirSync(output, { recursive: true });
const temporary = mkdtempSync(join(tmpdir(), "qch-motion-recorder-"));
const config = join(temporary, "browser.json");
writeFileSync(config, JSON.stringify({ browser: {
  browserName: "chromium", launchOptions: { channel: "chrome", chromiumSandbox: process.getuid?.() !== 0 },
  contextOptions: { viewport: { width: 1440, height: 960 }, reducedMotion: "no-preference" },
}, outputDir: output }));
const session = `qch-record-${label}`;
function cli(...args) {
  const executable = process.env.PLAYWRIGHT_CLI || "npx";
  const prefix = process.env.PLAYWRIGHT_CLI ? [] : ["--yes", "--package", "@playwright/cli@0.1.22", "playwright-cli"];
  const result = spawnSync(executable, [...prefix, `-s=${session}`, ...args], { encoding: "utf8" });
  if (result.status !== 0 || result.stdout.includes("### Error")) throw new Error(result.stdout + result.stderr);
}
const sequence = String.raw`async (page) => {
  await page.mouse.move(1200,80); await page.waitForTimeout(350);
  await page.locator('.dock-nav a[href="#traffic"]').click(); await page.waitForTimeout(950);
  await page.locator('.dock-nav a[href="#live-config"]').click(); await page.waitForTimeout(950);
  await page.locator('.dock-nav a[href="#node-settings"]').click(); await page.waitForTimeout(950);
  await page.mouse.move(32,280); await page.waitForTimeout(650);
  await page.mouse.move(1200,80); await page.waitForTimeout(600);
  await page.mouse.move(32,280); await page.waitForTimeout(130);
  await page.mouse.move(1200,80); await page.waitForTimeout(100);
  await page.mouse.move(32,280); await page.waitForTimeout(600);
  await page.mouse.move(1200,80); await page.waitForTimeout(600);
  await page.locator('[data-open-enrollment]').click(); await page.waitForTimeout(800);
  await page.keyboard.press('Escape'); await page.waitForTimeout(550);
  await page.locator('[data-open-enrollment]').click(); await page.waitForTimeout(100);
  await page.keyboard.press('Escape'); await page.waitForTimeout(70);
  await page.locator('[data-open-enrollment]').click(); await page.waitForTimeout(650);
  await page.keyboard.press('Escape'); await page.waitForTimeout(550);
  const handles=page.locator('.node-card-grip');
  const start=await handles.nth(0).boundingBox(), end=await handles.nth(1).boundingBox();
  const x=start.x+start.width/2,y=start.y+start.height/2;
  await page.mouse.move(x,y);await page.mouse.down();
  await page.mouse.move(end.x+end.width/2,end.y+end.height/2,{steps:20});
  await page.waitForTimeout(300);await page.mouse.up();await page.waitForTimeout(650);
  await handles.first().focus();await page.keyboard.press('ArrowRight');await page.waitForTimeout(85);
  await page.keyboard.press('ArrowLeft');await page.waitForTimeout(85);
  await page.keyboard.press('ArrowRight');await page.waitForTimeout(650);
  await page.evaluate(()=>{document.activeElement.blur();const fetch=window.fetch;window.fetch=async(...args)=>{if(String(args[0]).includes('/client-connections?'))await new Promise(r=>setTimeout(r,900));return fetch(...args);};});
  await page.locator('.dock-nav a[href="#client-connections"]').click();await page.waitForTimeout(1700);
  await page.locator('.dock-nav a[href="#node-settings"]').click();await page.waitForTimeout(850);
  await page.evaluate(()=>{const fetch=window.fetch;window.fetch=async(...args)=>{if(String(args[0]).includes('traffic-policies'))await new Promise(r=>setTimeout(r,700));return fetch(...args);};});
  await page.locator('.dock-nav a[href="#traffic"]').click();await page.waitForTimeout(60);
  await page.locator('.dock-nav a[href="#node-settings"]').click();await page.waitForTimeout(1250);
}`;
try {
  cli("open", `${url.origin}/agents-browser-smoke.html?mode=motion-selection&preview=1#node-settings`, `--config=${config}`);
  cli("snapshot");
  cli("video-start", join(output, `motion-${label}.webm`), "--size=1440x960", "--fps=60");
  cli("run-code", sequence);
  cli("video-stop");
  console.log(join(output, `motion-${label}.webm`));
} finally {
  cli("close");
  rmSync(temporary, { recursive: true, force: true });
}
