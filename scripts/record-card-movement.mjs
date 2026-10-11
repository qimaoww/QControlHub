#!/usr/bin/env node
// Local fixtures only. The visible cursor is drawn from delivered pointer
// events; all sorting is driven by native mouse/touch input, never DOM moves.
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";

const [address, label = "desktop"] = process.argv.slice(2);
const url = new URL(address), mobile = label === "mobile";
if (!["localhost", "127.0.0.1"].includes(url.hostname) || !["desktop", "mobile"].includes(label))
  throw new Error("usage: node scripts/record-card-movement.mjs http://127.0.0.1:PORT desktop|mobile");
const output = resolve("output/playwright"), temporary = mkdtempSync(join(tmpdir(), "qch-card-record-"));
mkdirSync(output, { recursive: true });
const config = join(temporary, "config.json");
writeFileSync(config, JSON.stringify({ browser: { browserName: "chromium", launchOptions: {
  executablePath: process.env.QCH_CHROME_BIN || "/usr/bin/chromium", args: ["--no-sandbox", ...(!mobile ? ["--blink-settings=primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4"] : [])],
}, contextOptions: { viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 960 }, isMobile: mobile, hasTouch: mobile } }, outputDir: output }));
function cli(...args) {
  const tool = process.env.PLAYWRIGHT_CLI || "npx";
  const prefix = process.env.PLAYWRIGHT_CLI ? [] : ["--yes", "--package", "@playwright/cli@0.1.22", "playwright-cli"];
  const result = spawnSync(tool, [...prefix, `-s=qch-card-record-${label}`, ...args], { encoding: "utf8" });
  if (result.status !== 0 || result.stdout.includes("### Error")) throw new Error(result.stdout + result.stderr);
  return result.stdout;
}
const sequence = `async page => {
  const mobile = ${mobile};
  const cdp = mobile ? await page.context().newCDPSession(page) : null;
  await page.evaluate(() => {
    const cursor = document.createElement('div');
    cursor.style.cssText = 'position:fixed;left:0;top:0;width:20px;height:26px;z-index:2147483647;pointer-events:none';
    cursor.innerHTML = '<svg viewBox="0 0 20 26"><path d="M2 1v20l5-5 4 9 4-2-4-8h7Z" fill="#111827" stroke="white" stroke-width="1.5"/></svg>';
    document.body.append(cursor);
    for (const type of ['pointerdown','pointermove','pointerup']) document.addEventListener(type, event => {
      cursor.style.transform = 'translate('+event.clientX+'px,'+event.clientY+'px)';
      cursor.style.filter = event.buttons ? 'drop-shadow(0 0 4px #f59e0b)' : '';
      window.recordPointer = { x:event.clientX,y:event.clientY };
      if(type === 'pointerdown') {
        const card = event.target.closest('.node-card,.traffic-policy-card');
        const r = card?.getBoundingClientRect();
        window.recordGrab = r && { x:event.clientX-r.left,y:event.clientY-r.top };
      }
    }, true);
  });
  const results = [];
  const touch = async (type, point) => {
    const input = point && await page.evaluate(p => ({ x: p.x - visualViewport.offsetLeft, y: p.y - visualViewport.offsetTop }), point);
    await cdp.send('Input.dispatchTouchEvent', { type, touchPoints: input ? [input] : [] });
  };
  const move = async point => {
    if (mobile) await touch('touchMove', point);
    else await page.mouse.move(point.x,point.y);
    await page.waitForTimeout(25);
  };
  for (const type of ['nodes','traffic']) {
    const hash = type === 'nodes' ? '#node-settings' : '#traffic';
    await page.evaluate(hash => {location.hash=hash;},hash);
    const selector = type === 'nodes' ? '.node-card-grid' : '.traffic-policy-grid';
    await page.waitForSelector(selector+' .node-card-grip');
    await page.waitForTimeout(700);
    await page.evaluate(t => {document.documentElement.dataset.theme = t === 'nodes' ? 'light' : 'dark';},type);
    const positions = mobile ? ['grip','grip'] : type === 'nodes' ? ['center','top-left','top-right','bottom-left','bottom-right','grip'] : ['grip','center'];
    for (const [index,position] of positions.entries()) {
      const geometry = await page.evaluate(({selector,position,mobile}) => {
        const grid = document.querySelector(selector), list = [...grid.children].filter(c=>c.matches('.node-card,.traffic-policy-card'));
        const source = list[0]; source.scrollIntoView({block:mobile?'start':'center',behavior:'instant'});
        const r = source.getBoundingClientRect(), h = source.querySelector('.node-card-grip').getBoundingClientRect(), t=list[1].getBoundingClientRect();
        let start={x:r.left+r.width/2,y:r.top+r.height/2};
        if(position==='grip') start={x:h.left+h.width/2,y:h.top+h.height/2};
        if(position==='top-left') start={x:r.left+8,y:r.top+8};
        if(position==='top-right') start={x:r.right-8,y:r.top+8};
        if(position==='bottom-left') start={x:r.left+8,y:r.bottom-8};
        if(position==='bottom-right') start={x:r.right-8,y:r.bottom-8};
        return {start,end:{x:t.left+t.width/2,y:t.top+36},original:list.map(c=>c.dataset.agentNode||c.dataset.trafficCardKey)};
      },{selector,position,mobile});
      const {start,end,original} = geometry;
      if(mobile) await touch('touchStart', start);
      else {await page.mouse.move(start.x,start.y);await page.mouse.down();}
      const samples = [];
      const destinations = index===0 ? [end,start,end] : [end];
      let previous=start;
      for(const target of destinations){
        for(let step=1;step<=18;step++){
          await move({x:previous.x+(target.x-previous.x)*step/18,y:previous.y+(target.y-previous.y)*step/18});
          const error=await page.evaluate(()=>{
            const r=document.querySelector('.card-move-ghost')?.getBoundingClientRect(),g=window.recordGrab,p=window.recordPointer;
            return r&&Math.hypot(r.left+g.x-p.x,r.top+g.y-p.y);
          });
          if(error!=null)samples.push(error);
        }
        previous=target;
      }
      await page.waitForTimeout(180);
      const expected=await page.evaluate(selector=>[...document.querySelector(selector).children].filter(c=>c.matches('.node-card,.traffic-policy-card')).sort((a,b)=>Number(a.style.order)-Number(b.style.order)).map(c=>c.dataset.agentNode||c.dataset.trafficCardKey),selector);
      const cancelled=index===positions.length-1;
      if(cancelled)await page.keyboard.press('Escape');
      if(mobile)await touch('touchEnd');else await page.mouse.up();
      await page.waitForTimeout(650);
      const actual=await page.evaluate(selector=>[...document.querySelector(selector).children].filter(c=>c.matches('.node-card,.traffic-policy-card')).map(c=>c.dataset.agentNode||c.dataset.trafficCardKey),selector);
      if(!samples.length||Math.max(...samples)>=1||JSON.stringify(actual)!==JSON.stringify(cancelled?original:expected))throw new Error('recording verification failed '+type+'/'+position);
      results.push({type,position,cancelled,maxGrabErrorPx:Math.max(...samples),samples:samples.length,previewOrder:expected,finalOrder:actual});
    }
  }
  await cdp?.detach();
  return results;
}`;
try {
  cli("open", `${url.origin}/agents-browser-smoke.html?mode=motion-cards&preview=1#node-settings`, `--config=${config}`);
  cli("snapshot");
  cli("video-start", join(output, `card-movement-${label}.webm`), `--size=${mobile ? "390x844" : "1440x960"}`, "--fps=30");
  const result = cli("run-code", sequence);
  cli("video-stop");
  const json = result.split("### Result\n")[1]?.split("\n### ")[0]?.trim();
  writeFileSync(join(output, `card-movement-${label}.json`), JSON.stringify(JSON.parse(json), null, 2) + "\n");
  console.log(join(output, `card-movement-${label}.webm`));
} finally { cli("close"); rmSync(temporary, { recursive: true, force: true }); }
