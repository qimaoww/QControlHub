import {mkdir, writeFile} from "node:fs/promises";
import {createHash} from "node:crypto";
import {fileURLToPath} from "node:url";
import {resolve} from "node:path";

const {chromium} = await import(process.env.QCH_PLAYWRIGHT_MODULE || "playwright");
const [before, after, directory] = process.argv.slice(2);
if (!before || !after) throw Error("Usage: screenshots.mjs BEFORE_URL AFTER_URL [OUTPUT_DIRECTORY]");
const output = directory ? resolve(directory) : fileURLToPath(new URL("../evidence/", import.meta.url));
await mkdir(output, {recursive:true});
const browser = await chromium.launch({headless:true, executablePath:process.env.QCH_CHROMIUM || "/usr/bin/chromium", args:["--no-sandbox"]});
const results = [];
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object"
  ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
try {
  for (const [revision, url] of [["before", before], ["after", after]]) {
    for (const [device, viewport] of Object.entries({desktop:{width:1440, height:1000}, mobile:{width:390, height:844}, tablet:{width:820, height:1180}})) {
      for (const theme of ["light", "dark"]) {
        const context = await browser.newContext({viewport, colorScheme:theme, reducedMotion:"reduce", isMobile:device === "mobile", hasTouch:device !== "desktop"});
        await context.addInitScript(value => localStorage.setItem("qcontrolhub-color-theme", value), theme);
        const page = await context.newPage();
        const exceptions = [];
        page.on("pageerror", error => exceptions.push(error.message));
        await page.goto(url);
        await page.waitForSelector('[data-live-config-phase="ready"] [data-code-input]');
        for (const [view, selector] of [["workspace", '[data-config-file="0"]'], ["inbound", '[data-config-file="1"]'], ["preview", "[data-config-preview]"]]) {
          await page.locator(selector).click();
          await page.locator("[data-code-input]").press("Control+Home");
          await page.evaluate(view => {
            if (view === "workspace") {
              document.querySelector(".workspace-main").scrollTop = 0;
              window.scrollTo(0, 0);
            } else {
              document.querySelector("[data-code-editor]").scrollIntoView({block:"start", behavior:"instant"});
              const topbar = document.querySelector(".workspace-topbar"), bounds = topbar.getBoundingClientRect();
              if (getComputedStyle(topbar).position === "fixed" || getComputedStyle(topbar).position === "sticky")
                window.scrollBy({top:-bounds.height, behavior:"instant"});
            }
          }, view);
          await page.waitForTimeout(200);
          const file = `${revision}-${device}-${theme}-${view}.png`;
          await page.screenshot({path:resolve(output, file)});
          const state = await page.evaluate(async () => ({
            account:"fixture administrator", node:"alpha", engine:"xray", version:8,
            theme:document.documentElement.dataset.theme,
            readonly:document.querySelector("[data-code-input]").readOnly,
            selected:document.querySelector('.code-file-meta b')?.textContent,
            overflow:document.documentElement.scrollWidth > innerWidth,
            content:document.querySelector("[data-code-input]").value,
            savedContent:(await (await fetch("/api/v1/agents/alpha/configs/xray/workspace")).json()).config.content,
          }));
          if (state.overflow || exceptions.length) throw Error(`${file}: ${JSON.stringify({overflow:state.overflow, exceptions})}`);
          const {content, savedContent, ...metadata} = state;
          results.push({revision, device, theme, viewport, view, file, ...metadata,
            documentHash:createHash("sha256").update(content).digest("hex"),
            savedDocumentHash:createHash("sha256").update(savedContent).digest("hex"),
            semanticHash:createHash("sha256").update(JSON.stringify(canonical(JSON.parse(content)))).digest("hex")});
          process.stdout.write(`${file}\n`);
        }
        await context.close();
      }
    }
  }
  for (const before of results.filter(item => item.revision === "before" && item.view === "preview")) {
    const after = results.find(item => item.revision === "after" && item.device === before.device && item.theme === before.theme && item.view === before.view);
    if (before.savedDocumentHash !== after.savedDocumentHash || before.semanticHash !== after.semanticHash)
      throw Error(`Before/after configuration data differ: ${before.device}/${before.theme}`);
  }
  await writeFile(resolve(output, "screenshots.json"), JSON.stringify({baseline:"955f442e69ddb0a35b8163822ff3129c30e9c02f", results}, null, 2) + "\n");
} finally { await browser.close(); }
