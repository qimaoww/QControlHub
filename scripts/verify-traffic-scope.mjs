#!/usr/bin/env node
// Acceptance driver for this task's disposable loopback fixture only.
import { spawnSync } from "node:child_process";
import { writeFileSync, mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { homedir } from "node:os";
const base = "http://127.0.0.1:55592";
const output=resolve("output/playwright/traffic-owner-bulk");mkdirSync(output,{recursive:true});
const config=resolve(output,"browser-config.json");
writeFileSync(config,JSON.stringify({browser:{browserName:"chromium",launchOptions:{executablePath:"/usr/bin/chromium",args:["--no-sandbox"]},contextOptions:{viewport:{width:1440,height:1000}}},outputDir:output}));
const session="traffic-acceptance";
const playwrightCLI=process.env.QCH_PLAYWRIGHT_CLI || resolve(homedir(), ".codex/skills/playwright/scripts/playwright_cli.sh");
function cli(...args){const r=spawnSync(playwrightCLI,[`--session=${session}`,...args],{encoding:"utf8",timeout:60000});if(r.status||r.stdout.includes("### Error"))throw Error(r.stdout+r.stderr);return r.stdout;}
function run(code){const r=cli("run-code",`async page => { const eq=(a,b,m)=>{if(JSON.stringify(a)!==JSON.stringify(b))throw Error(m+JSON.stringify({actual:a,expected:b}))}; ${code} }`);const body=r.split("### Result\n")[1]?.split("\n### ")[0];return body?JSON.parse(body):null;}
const results=[];
try{
 cli("open",base,`--config=${config}`);cli("snapshot");
 results.push(run(`
 const login=async name=>{await page.getByRole('textbox',{name:'用户名',exact:true}).fill(name);await page.getByRole('textbox',{name:'密码 / 管理令牌'}).fill('traffic fixture password');await page.getByRole('button',{name:'登录',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('#login-form'));await page.goto('${base}/#traffic');await page.waitForSelector('[data-traffic-filter=user_scope]');await page.waitForTimeout(500);};
 await login('traffic-alice');
 eq(await page.locator('.traffic-policy-grid article').count(),2,'Alice owns base and shared instance');
 eq(await page.locator('[data-traffic-filter=user_scope]').inputValue(),'current','default user');
 eq(await page.locator('.traffic-policy-grid article').evaluateAll(cards=>cards.map(c=>c.getAttribute('aria-label')).sort()),['东京 · Alice 节点，端口 21101，Xray 内核','香港 · 共享节点，端口 22001，Xray 内核'].sort(),'instance ownership');
 await page.locator('[data-traffic-filter=user_scope]').selectOption('all');await page.waitForTimeout(500);eq(await page.locator('.traffic-policy-grid article').count(),2,'recipient authorized all');
 await page.locator('[data-traffic-batch-toggle]').click();await page.locator('[data-traffic-select-all]').check();eq(await page.locator('[data-traffic-choice]:checked').count(),1,'shared quota read only');
 await page.locator('[data-traffic-filter-reset]').click();eq(await page.locator('[data-traffic-choice]:checked').count(),0,'reset clears selection');eq(await page.locator('[data-traffic-filter=user_scope]').inputValue(),'current','reset user');
 await page.evaluate(()=>document.querySelector('#logout').click());await page.waitForSelector('#login-form');await login('traffic-bob');eq(await page.locator('.traffic-policy-grid article').count(),1,'account switch isolated');eq(await page.locator('[data-traffic-filter=user_scope]').inputValue(),'current','account switch scope');eq(await page.locator('[data-traffic-choice]').count(),0,'account switch batch');
 await page.evaluate(()=>document.querySelector('#logout').click());await page.waitForSelector('#login-form');await login('traffic-admin');eq(await page.locator('.traffic-policy-grid article').count(),2,'administrator current');await page.locator('[data-traffic-filter=user_scope]').selectOption('all');await page.waitForTimeout(500);eq(await page.locator('.traffic-policy-grid article').count(),5,'administrator authorized all');
 await page.locator('[data-traffic-sync]').click();await page.waitForSelector('[data-sync-choice]');const allSync=await page.locator('[data-sync-choice]').count();await page.locator('[data-sync-close]').first().click();await page.locator('[data-traffic-filter=user_scope]').selectOption('current');await page.locator('[data-traffic-sync]').click();await page.waitForSelector('[data-sync-choice]');const mineSync=await page.locator('[data-sync-choice]').count();if(!(mineSync<allSync))throw Error('sync scope not applied');await page.locator('[data-sync-close]').first().click();
 return {alice:2,bob:1,adminCurrent:2,adminAll:5,allSync,mineSync,accountIsolation:true};`));
 writeFileSync(resolve(output,"browser-acceptance.json"),JSON.stringify(results,null,2));
 console.log(JSON.stringify(results,null,2));
}finally{cli("close");}
