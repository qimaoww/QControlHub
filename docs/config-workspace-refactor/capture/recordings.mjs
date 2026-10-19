import {mkdir, writeFile} from "node:fs/promises";
import {resolve} from "node:path";
import {strict as assert} from "node:assert";

const playwright = await import(process.env.QCH_PLAYWRIGHT_MODULE || "playwright");
// Playwright ships CommonJS, so a filesystem module path resolves to its default export.
const {chromium} = playwright.chromium ? playwright : playwright.default;
const [base, directory] = process.argv.slice(2);
if (!base || !directory) throw Error("Usage: recordings.mjs AFTER_URL RAW_VIDEO_DIRECTORY");
const output = resolve(directory);
await mkdir(output, {recursive:true});
const browser = await chromium.launch({headless:true, executablePath:process.env.QCH_CHROMIUM || "/usr/bin/chromium", args:["--no-sandbox"]});
const results = [];
const pause = page => page.waitForTimeout(900);
const source = page => page.locator("[data-code-input]");
const ready = page => page.waitForSelector('[data-live-config-phase="ready"] [data-code-input]');
async function record(name, viewport, theme, action) {
  const context = await browser.newContext({viewport, colorScheme:theme, isMobile:viewport.width < 600,
    hasTouch:viewport.width < 600, recordVideo:{dir:output, size:viewport}});
  await context.addInitScript(value => localStorage.setItem("qcontrolhub-color-theme", value), theme);
  const page = await context.newPage(), exceptions = [];
  page.on("pageerror", error => exceptions.push(error.message));
  try {
    await action(page);
    assert.deepEqual(exceptions, []);
    await pause(page);
  } catch (error) {
    await page.screenshot({path:resolve(output, `${name}-failure.png`)});
    throw error;
  } finally { await context.close(); }
  results.push({name, viewport, theme, rawVideo:await page.video().path(), exceptions});
  await writeFile(resolve(output, "recordings.json"), JSON.stringify(results, null, 2) + "\n");
  process.stdout.write(`${name} passed\n`);
}
try {
  await record("desktop-context-drafts", {width:1440, height:1000}, "light", async page => {
    await page.goto(base); await ready(page); await pause(page);
    await page.locator('[data-config-file="1"]').click();
    const original = await source(page).inputValue();
    await source(page).fill(original.replace('"port": 1080', '"port": 1081'));
    await pause(page);
    await page.locator("[data-config-preview]").click();
    assert.match(await source(page).inputValue(), /"port": 1081/);
    assert.equal(await source(page).getAttribute("readonly"), "");
    await pause(page);
    await page.locator("[data-config-source]").click();
    await page.locator('[data-live-engine="sing-box"]').click();
    await page.waitForSelector("[data-confirm-dialog][open]"); await pause(page);
    await page.locator("[data-confirm-cancel]").click();
    assert.match(await source(page).inputValue(), /"port": 1081/);
    await page.locator('[data-live-engine="sing-box"]').click();
    await page.locator("[data-confirm-accept]").click();
    await page.waitForFunction(() => location.hash.includes("engine=sing-box") && document.querySelector('[data-live-config-phase="ready"]'));
    await pause(page);
    await page.locator('[data-live-engine="xray"]').click();
    await page.waitForFunction(() => location.hash.includes("engine=xray") && document.querySelector("[data-workspace-save-state]")?.textContent === "有未保存修改");
    assert.match(await source(page).inputValue(), /"port": 1081/);
    await pause(page);
    await page.reload(); await ready(page);
    assert.match(await source(page).inputValue(), /"port": 1081/);
    await pause(page);
    await page.locator('[data-live-agent="bravo"]').click();
    await page.locator("[data-confirm-accept]").click();
    await page.waitForFunction(() => location.hash.includes("agent=bravo") && document.querySelector('[data-live-config-phase="ready"]'));
    await pause(page);
    await page.locator('[data-live-agent="alpha"]').click();
    await page.waitForFunction(() => location.hash.includes("agent=alpha") && document.querySelector('[data-live-config-phase="ready"]'));
    assert.match(await source(page).inputValue(), /"port": 1081/);
  });
  await record("mobile-file-save-retry-deploy", {width:390, height:844}, "dark", async page => {
    await page.goto(new URL("?kind=form&engine=xray", base).href); await ready(page); await pause(page);
    const content = '{\n  "future_counter":18446744073709551615,\n  "log":{"loglevel":"warning"},\n  "inbounds":[{"tag":"local-socks","protocol":"socks","listen":"127.0.0.1","port":23001,"settings":{"auth":"noauth"}}],\n  "outbounds":[{"tag":"direct","protocol":"freedom"}]\n}\n';
    await page.locator("[data-config-file-input]").setInputFiles({name:"my-xray.json", mimeType:"application/json", buffer:Buffer.from(content)});
    await page.waitForFunction(value => document.querySelector("[data-code-input]")?.value === value, content);
    assert.equal(await page.evaluate(() => window.reviewConfig.writes.length), 0);
    await pause(page);
    await page.locator("[data-config-preview]").click(); await pause(page);
    assert.equal(await source(page).inputValue(), content);
    await page.locator("[data-config-source]").click();
    await source(page).scrollIntoViewIfNeeded(); await pause(page);
    await page.evaluate(() => { window.reviewConfig.taskFailure = true; });
    await page.locator('[data-live-intent="validate"]').click();
    await page.waitForFunction(() => document.querySelector("[data-workspace-result]")?.textContent === "fixture task unavailable");
    await pause(page);
    assert.equal(await page.evaluate(() => window.reviewConfig.saved().content), content);
    await page.evaluate(() => { window.reviewConfig.taskFailure = false; window.reviewConfig.taskStatus = "running"; });
    await page.locator('[data-live-intent="validate"]').click();
    await page.waitForFunction(() => document.querySelector("[data-workspace-result]")?.textContent.includes("正在校验"));
    await pause(page);
    await page.evaluate(() => { window.reviewConfig.taskStatus = "succeeded"; });
    await page.waitForFunction(() => document.querySelector("[data-workspace-result]")?.textContent.includes("校验成功"));
    assert.equal(await page.evaluate(() => window.reviewConfig.writes.length), 1);
    await pause(page);
    await page.evaluate(() => { window.reviewConfig.confirm = true; window.reviewConfig.taskStatus = "running"; });
    await page.locator('[data-live-intent="deploy"]').click();
    await page.waitForFunction(() => document.querySelector("[data-workspace-result]")?.firstChild?.textContent === "正在部署…");
    await page.locator("[data-workspace-result]").scrollIntoViewIfNeeded(); await pause(page);
    await page.screenshot({path:resolve(output, "feedback-mobile-dark-progress.png")});
    await page.evaluate(() => { window.reviewConfig.taskStatus = "failed"; window.reviewConfig.taskError = "listen tcp 0.0.0.0:23001: bind: address already in use"; });
    await page.waitForFunction(() => document.querySelector("[data-workspace-result]")?.textContent === "listen tcp 0.0.0.0:23001: bind: address already in use");
    assert.equal(await page.evaluate(() => window.reviewConfig.notices.length), 0);
    await page.locator("[data-workspace-result]").scrollIntoViewIfNeeded(); await pause(page);
    await page.screenshot({path:resolve(output, "feedback-mobile-dark-error.png")});
    await page.evaluate(() => { window.reviewConfig.taskStatus = "succeeded"; window.reviewConfig.taskError = ""; });
    await page.locator('[data-live-intent="deploy"]').click();
    await page.waitForFunction(() => document.querySelector("[data-workspace-result]")?.textContent.includes("部署成功"));
    assert.equal(await page.evaluate(() => window.reviewConfig.writes.length), 1);
    assert.equal(await page.evaluate(() => window.reviewConfig.saved().content), content);
    await page.locator("[data-workspace-result]").scrollIntoViewIfNeeded(); await pause(page);
    await page.screenshot({path:resolve(output, "feedback-mobile-dark-success.png")});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    await page.locator("[data-workspace-deploy-state]").scrollIntoViewIfNeeded();
    await page.waitForFunction(() => document.querySelector("[data-workspace-deploy-state]")?.textContent === "已部署 v2");
  });
  await record("mobile-protocol-outbound", {width:390, height:844}, "light", async page => {
    await page.goto(new URL("?kind=form&engine=sing-box", base).href); await ready(page);
    await page.locator('[data-inbound-action="add"]').click();
    await page.waitForSelector("#server-plan-form"); await pause(page);
    await page.locator('#server-plan-form [name="tag"]').fill("mobile-inbound");
    await page.locator('#server-plan-form [name="port"]').fill("23003");
    await page.locator('[data-builder-step="identity"]').click(); await pause(page);
    await page.locator('[data-builder-step="listen"]').click();
    await page.evaluate(() => { window.reviewConfig.taskStatus = "succeeded"; });
    await page.locator('[data-plan-intent="validate"]').click();
    await page.waitForFunction(() => !document.querySelector(".config-inbound-dialog"));
    await pause(page);
    assert.equal(await page.evaluate(() => window.reviewConfig.writes[0].input.tag), "mobile-inbound");
    await page.locator('[data-outbound-action="create"]').click();
    await page.waitForSelector("[data-outbound-source]"); await pause(page);
    await page.locator('[data-outbound-close]').click();
    await page.waitForFunction(() => !document.querySelector(".config-inbound-dialog"));
    assert.equal(await page.evaluate(() => window.reviewConfig.writes.length), 1);
  });
} finally { await browser.close(); }
