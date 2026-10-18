#!/usr/bin/env node
// Opt-in acceptance against a disposable, seeded panel. Uses independent real
// browser sessions and native pointer/keyboard input. Never targets production.
import { spawnSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
const [address, baseline] = process.argv.slice(2);
for (const value of [address, baseline]) if (!value || !["localhost", "127.0.0.1"].includes(new URL(value).hostname)) throw new Error("Pass two loopback fixture URLs");
const secret = process.env.QCH_ORDER_TEST_PASSWORD;
if (!secret) throw new Error("QCH_ORDER_TEST_PASSWORD is required for disposable test accounts");
const output = resolve("output/playwright/account-order"); mkdirSync(output, { recursive: true });
const config = resolve(output, "browser-config.json"), mobileConfig = resolve(output, "mobile-config.json");
const settings = mobile => ({ browser: { browserName: "chromium", launchOptions: { executablePath: process.env.QCH_CHROME_BIN || "/usr/bin/chromium", args: ["--no-sandbox"] }, contextOptions: { viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 960 }, isMobile: mobile, hasTouch: mobile } }, outputDir: output });
writeFileSync(config, JSON.stringify(settings(false))); writeFileSync(mobileConfig, JSON.stringify(settings(true)));
const sessions = [];
function cli(session, ...args) {
 const result = spawnSync(process.env.PLAYWRIGHT_CLI || "npx", [...(process.env.PLAYWRIGHT_CLI ? [] : ["--yes", "--package", "@playwright/cli@0.1.22", "playwright-cli"]), `-s=${session}`, ...args], { encoding: "utf8", timeout: 90000 });
 if (result.status || result.stdout.includes("### Error")) throw new Error(result.stdout + result.stderr);
 return result.stdout;
}
function run(session, code) {
 const output = cli(session, "run-code", `async page => { ${common}\n${code}\n }`);
 const json = output.split("### Result\n")[1]?.split("\n### ")[0]?.trim();
 return json ? JSON.parse(json) : null;
}
const common = `
 const ids = () => page.locator('.node-card-grid > .node-card').evaluateAll(cards => cards.map(c => c.dataset.agentNode));
 const equal = (a,b,message) => { if(JSON.stringify(a)!==JSON.stringify(b)) throw new Error(message+': '+JSON.stringify({actual:a,expected:b})); };
 const ready = async () => {await page.waitForSelector('.node-card-grid > .node-card');await page.waitForTimeout(800);};
 const saved = async () => {await page.waitForFunction(() => document.querySelector('[data-card-order-status]')?.dataset.phase === 'saved');};
 const login = async name => {
  await page.getByRole('textbox',{name:'用户名',exact:true}).fill(name);
  await page.getByRole('textbox',{name:'密码 / 管理令牌'}).fill(${JSON.stringify(secret)});
  await page.getByRole('button',{name:'登录',exact:true}).click();
  await page.waitForFunction(() => !document.querySelector('#login-form'));
  await page.goto(${JSON.stringify(address)}+'/#node-settings'); await ready();
 };
 const server = () => page.evaluate(() => fetch('/api/v1/preferences/node-order').then(r=>r.json()));
`;
function open(name, url, mobile = false) {
 sessions.push(name);cli(name, "open", url, `--config=${mobile ? mobileConfig : config}`);cli(name, "snapshot");
}
const results = [];
try {
 open("qch-order-before", baseline);
 run("qch-order-before", `
 await page.getByRole('textbox',{name:'用户名',exact:true}).fill('order-alice');
 await page.getByRole('textbox',{name:'密码 / 管理令牌'}).fill(${JSON.stringify(secret)});
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.waitForFunction(() => !document.querySelector('#login-form'));
 const identity = await page.evaluate(() => fetch('/api/v1/auth/session').then(r=>r.json()));
 await page.evaluate(user => { localStorage.setItem('qcontrolhub:node-card-order:account:'+encodeURIComponent(user), JSON.stringify([1,2,3,4].map(i=>'agt_'+String(i).padStart(16,'0'))));localStorage.setItem('qcontrolhub-color-theme','light'); }, identity.user_id);
 await page.goto(${JSON.stringify(baseline)}+'/#node-settings'); await ready();
 await page.screenshot({path:${JSON.stringify(resolve(output, "before.png"))}});
 return await ids();`);
 open("qch-order-device-a", address);run("qch-order-device-a", "await login('order-alice'); await page.evaluate(()=>localStorage.setItem('qcontrolhub-color-theme','light')); await page.reload();await ready();");
 open("qch-order-device-b", address);run("qch-order-device-b", "await login('order-alice');");
 cli("qch-order-device-a", "video-start", resolve(output, "sorting-desktop.webm"), "--size=1440x960", "--fps=30");
 results.push(run("qch-order-device-a", `
 const before = await ids();
 const grip = await page.locator('.node-card-grip').last().boundingBox(), target = await page.locator('.node-card').first().boundingBox();
 await page.mouse.move(grip.x+grip.width/2,grip.y+grip.height/2);await page.mouse.down();
 await page.mouse.move(target.x+target.width/2,target.y+80,{steps:25});await page.waitForTimeout(250);await page.mouse.up();await saved();
 await page.waitForTimeout(800);const actual=await ids();equal(actual,[before[3],...before.slice(0,3)],'mouse drag');equal((await server()).ids,actual,'committed DB order');
 await page.mouse.click(1300,900);await page.waitForTimeout(500);await page.screenshot({path:${JSON.stringify(resolve(output, "after-device-a.png"))}});
 return {test:'desktop mouse, save acknowledgement',ids:actual};`));
 cli("qch-order-device-a", "video-stop");
 const expected = results[0].ids;
 results.push(run("qch-order-device-b", `await page.reload();await ready();const actual=await ids();equal(actual,${JSON.stringify(expected)},'independent device B');await page.screenshot({path:${JSON.stringify(resolve(output,"after-device-b.png"))}});return {test:'independent browser B reload',ids:actual};`));
 results.push(run("qch-order-device-a", `
 await page.evaluate(()=>localStorage.clear());await page.reload();await ready();equal(await ids(),${JSON.stringify(expected)},'storage clear');
 await page.getByRole('button',{name:'退出登录',exact:true}).click();await page.waitForSelector('#login-form');await login('order-bob');
 const bob=await ids();equal(bob,[1,2,3,4].map(i=>'agt_'+String(i).padStart(16,'0')),'bob isolated');
 await page.locator('.node-card-grip').first().press('ArrowRight');await saved();const bobSaved=await ids();
 await page.getByRole('button',{name:'退出登录',exact:true}).click();await page.waitForSelector('#login-form');await login('order-alice');
 equal(await ids(),${JSON.stringify(expected)},'alice restored');return {test:'clear storage, logout, account switch and shared user keyboard save',alice:await ids(),bob:bobSaved};`));
 results.push(run("qch-order-device-a", `
 let writes=0;
 await page.route('**/api/v1/preferences/node-order', async route => { if(route.request().method()==='PUT'){writes++;await new Promise(resolve=>setTimeout(resolve,800));}await route.continue(); });
 const original=await ids();const first=page.locator('.node-card-grip').first();await first.press('ArrowRight');
 await page.waitForFunction(()=>document.querySelector('[data-card-order-status]')?.textContent.includes('正在保存'));
 await page.locator('.node-card-grip').nth(1).press('ArrowRight');
 await saved();await page.waitForTimeout(1000);equal(await ids(),[original[1],original[2],original[0],original[3]],'rapid gesture queue');
 equal((await server()).ids,await ids(),'delayed saves database');await page.unroute('**/api/v1/preferences/node-order');
 const confirmed=await ids();
 await page.route('**/api/v1/preferences/node-order',route=>route.request().method()==='PUT'?route.fulfill({status:503,contentType:'application/json',body:'{"error":"fixture save failure"}'}):route.continue());
 await page.locator('.node-card-grip').first().press('ArrowRight');await page.waitForFunction(()=>document.querySelector('[data-card-order-status]')?.dataset.phase==='error');
 equal(await ids(),confirmed,'save failure rollback');equal((await server()).ids,confirmed,'failure database');await page.unroute('**/api/v1/preferences/node-order');
 await page.locator('.node-card-grip').first().press('ArrowRight');await saved();
 return {test:'delay, rapid keyboard commits, polling, save failure and retry',writes,ids:await ids()};`));
 open("qch-order-mobile", address, true);run("qch-order-mobile", "await login('order-alice');");
 cli("qch-order-mobile", "video-start", resolve(output,"sorting-mobile.webm"), "--size=390x844", "--fps=30");
 results.push(run("qch-order-mobile", `
 const before=await ids();const grip=page.locator('.node-card-grip').first();await grip.scrollIntoViewIfNeeded();
 const start=await grip.boundingBox(),end=await page.locator('.node-card').nth(1).boundingBox();
 const cdp=await page.context().newCDPSession(page);
 const point={x:start.x+start.width/2,y:start.y+start.height/2};
 await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]});
 const destination={x:end.x+end.width/2,y:Math.min(780,end.y+80)};
 for(let step=1;step<=25;step++){await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:point.x+(destination.x-point.x)*step/25,y:point.y+(destination.y-point.y)*step/25}]});await page.waitForTimeout(20);}
 await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await saved();
 equal(await ids(),[before[1],before[0],...before.slice(2)],'native touch reorder');equal((await server()).ids,await ids(),'touch database');await cdp.detach();
 await page.waitForTimeout(800);await page.screenshot({path:${JSON.stringify(resolve(output,"mobile.png"))}});return {test:'390px native touch sorting',ids:await ids()};`));
 cli("qch-order-mobile", "video-stop");
 // Restart is explicit and restricted to the fixture unit created by this task.
 const unit = process.env.QCH_ORDER_TEST_RESTART_UNIT;
 if (!unit || !/^qch-account-order-[a-z0-9-]+$/.test(unit)) throw new Error("Set QCH_ORDER_TEST_RESTART_UNIT to this fixture unit");
 const restart=spawnSync("systemctl",["restart",unit],{encoding:"utf8"});if(restart.status)throw new Error(restart.stderr);
 results.push(run("qch-order-device-b", `await page.reload();await page.waitForSelector('#login-form');await login('order-alice');const actual=await ids();equal(actual,${JSON.stringify(results.at(-1).ids)},'panel process restart');return {test:'panel restart and fresh login',ids:actual};`));
 writeFileSync(resolve(output,"browser-results.json"),JSON.stringify({viewport:{width:1440,height:960},theme:"light",account:"order-alice",results},null,2)+'\n');
 console.log(JSON.stringify(results,null,2));
} finally {
 for(const session of sessions){try{cli(session,"video-stop");}catch{}try{cli(session,"close");}catch{}}
}
