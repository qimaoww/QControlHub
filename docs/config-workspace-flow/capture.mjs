// CLI-driven captures of running repository pages. API/Agent data are fixtures.
import {spawnSync} from 'node:child_process';
import {mkdirSync, writeFileSync, readFileSync} from 'node:fs';
import {resolve} from 'node:path';
const [before, after] = process.argv.slice(2);
if (!before || !after) throw Error('Usage: node capture.mjs BASELINE_ORIGIN CURRENT_ORIGIN');
const directory = resolve('docs/config-workspace-flow');
const output = resolve('output/playwright/config-workspace-flow');
mkdirSync(output, {recursive:true});
const cli = '/root/.codex/skills/playwright/scripts/playwright_cli.sh';
const session = 'config-flow-capture';
const config = resolve(output, 'capture-browser.json');
writeFileSync(config, JSON.stringify({browser:{browserName:'chromium', launchOptions:{executablePath:'/usr/bin/chromium', args:['--no-sandbox']}, contextOptions:{viewport:{width:1440,height:1000}}},outputDir:output}));
function command(...args) {
  const r = spawnSync(cli, [`--session=${session}`, ...args], {encoding:'utf8', timeout:60000});
  if (r.status || r.stdout.includes('### Error')) throw Error(r.stdout + r.stderr);
  return r.stdout;
}
function run(code) {
  const result = command('run-code', `async page => { ${code} }`);
  const body = result.split('### Result\n')[1]?.split('\n### ')[0];
  return body ? JSON.parse(body) : null;
}
const recordingsOnly = process.argv.includes('--recordings-only');
const measurements = recordingsOnly ? JSON.parse(readFileSync(resolve(directory,'browser-proof.json'),'utf8')).filter(row=>!row.workflow) : [];
try {
  command('open', `${before}/agents-browser-smoke.html?mode=config-layout&preview=1#live-config`, `--config=${config}`);
  command('snapshot');
  if (!recordingsOnly) {
  for (const [phase, origin] of [['before', before], ['after', after]]) {
    for (const [device,width,height] of [['desktop',1440,1000], ['mobile',390,844]]) {
      for (const theme of ['light','dark']) {
        measurements.push(run(`
          await page.setViewportSize({width:${width},height:${height}});
          await page.goto('${origin}/agents-browser-smoke.html?mode=config-layout&preview=1#live-config');
          await page.waitForSelector('#live-config-form');
          await page.evaluate(theme=>document.documentElement.dataset.theme=theme, '${theme}');
          await page.waitForFunction(()=>document.documentElement.dataset.browserSmoke==='passed');
          await page.locator('.config-tools-menu summary').focus();
          await page.evaluate(()=>document.fonts.ready);
          await page.waitForTimeout(800);
          await page.screenshot({path:'${directory}/${phase}-${device}-${theme}.png'});
          await page.locator('.code-workspace>footer').scrollIntoViewIfNeeded();
          await page.waitForTimeout(400);
          await page.screenshot({path:'${directory}/${phase}-${device}-${theme}-actions.png'});
          return await page.evaluate(()=>({phase:'${phase}',device:'${device}',theme:'${theme}',width:innerWidth,height:innerHeight,scrollWidth:document.documentElement.scrollWidth,buttons:[...document.querySelectorAll('[data-live-intent]')].map(b=>b.textContent)}));
        `));
      }
    }
  }
  for (const [name,width,height] of [['phone-small',375,812],['phone-landscape',844,390],['tablet-portrait',820,1180],['tablet-landscape',1180,820]]) {
    measurements.push(run(`
      await page.setViewportSize({width:${width},height:${height}});
      await page.goto('${after}/agents-browser-smoke.html?mode=config-inbounds&preview=1&modal=1');
      await page.waitForSelector('dialog[open] #server-plan-form');
      await page.evaluate(()=>document.documentElement.dataset.theme='dark');
      await page.waitForTimeout(800);
      await page.screenshot({path:'${directory}/${name}-dialog.png'});
      return await page.evaluate(()=>{const d=document.querySelector('dialog[open]'),r=d.getBoundingClientRect();if(document.documentElement.scrollWidth>innerWidth||r.left<0||r.right>innerWidth||r.bottom>innerHeight)throw Error('dialog overflow');return {name:'${name}',width:innerWidth,height:innerHeight,dialog:{left:r.left,right:r.right,top:r.top,bottom:r.bottom},scroll:d.querySelector('.config-inbound-body').scrollHeight>d.querySelector('.config-inbound-body').clientHeight};});
    `));
  }
  }
  command('snapshot');
  run(`await page.setViewportSize({width:390,height:844}); await page.goto('${after}/agents-browser-smoke.html?mode=config-layout&preview=1#live-config'); await page.waitForSelector('#live-config-form'); await page.evaluate(()=>document.documentElement.dataset.theme='light');`);
  command('video-start', `${directory}/switch-and-draft.webm`, '--size=390x844');
  run(`
    await page.locator('[data-code-input]').fill((await page.locator('[data-code-input]').inputValue())+'\\n');
    await page.waitForTimeout(800);
    await page.locator('[data-live-engine="sing-box"]').click();
    await page.waitForSelector('[data-confirm-dialog][open]');
    await page.waitForTimeout(600);
    await page.locator('[data-confirm-cancel]').click();
    await page.locator('[data-config-file="1"]').click();
    await page.locator('[data-config-preview]').click();
    await page.waitForTimeout(500);
    await page.locator('[data-config-preview]').click();
    await page.locator('[data-live-engine="sing-box"]').click();
    await page.waitForSelector('[data-confirm-dialog][open]');
    await page.locator('[data-confirm-accept]').click();
    await page.waitForSelector('#live-config-form[data-engine="sing-box"]');
    await page.waitForTimeout(800);
  `);
  command('video-stop');
  run(`await page.setViewportSize({width:1440,height:1000}); await page.goto('${after}/agents-browser-smoke.html?mode=config-inbounds&preview=1'); await page.waitForSelector('#live-config-form'); await page.evaluate(async()=>{window.inboundFixture.dispose();const {createConfigFixture}=await import('/assets/browser/config-fixture.mjs');window.inboundFixture=await createConfigFixture('xray',{source:true,shared:true});window.inboundFixture.confirm=true;window.inboundFixture.taskStatus='succeeded';});`);
  command('snapshot');
  command('video-start', `${directory}/edit-save-validate-deploy.webm`, '--size=1440x1000');
  measurements.push(run(`
    await page.locator('[data-inbound-action="add"]').click();
    await page.waitForSelector('dialog[open] #server-plan-form');
    await page.locator('dialog [name="tag"]').fill('review-inbound');
    await page.waitForTimeout(800);
    await page.locator('[data-plan-intent="validate"]').click();
    await page.waitForFunction(()=>!document.querySelector('dialog[open]')&&document.querySelector('[data-preset-status]')?.textContent.includes('校验成功'));
    await page.waitForTimeout(800);
    await page.locator('[data-code-input]').fill((await page.locator('[data-code-input]').inputValue())+'\\n');
    await page.waitForTimeout(800);
    await page.locator('[data-live-intent="save"]').click();
    await page.waitForFunction(()=>document.querySelector('[data-preset-status]')?.textContent.includes('尚未部署'));
    await page.waitForTimeout(800);
    await page.locator('[data-live-intent="validate"]').click();
    await page.waitForFunction(()=>document.querySelector('[data-preset-status]')?.textContent.includes('校验成功'));
    await page.waitForTimeout(800);
    await page.evaluate(()=>window.inboundFixture.taskStatus='failed');
    await page.locator('[data-live-intent="deploy"]').click();
    await page.waitForFunction(()=>document.querySelector('[data-preset-status]')?.textContent.includes('部署失败'));
    await page.waitForTimeout(800);
    await page.screenshot({path:'${directory}/deployment-failed.png'});
    await page.waitForTimeout(700);
    await page.evaluate(()=>window.inboundFixture.taskStatus='succeeded');
    await page.locator('[data-live-intent="deploy"]').click();
    await page.waitForFunction(()=>document.querySelector('[data-preset-status]')?.textContent.includes('部署成功'));
    await page.waitForTimeout(800);
    await page.waitForTimeout(800);
    await page.screenshot({path:'${directory}/deployment-retried.png'});
    return await page.evaluate(()=>({workflow:'source and reused protocol form',writes:window.inboundFixture.writes.map(w=>({path:w.path,version:w.version,intent:w.intent})),tasks:window.inboundFixture.taskRequests,result:document.querySelector('[data-preset-status]').textContent}));
  `));
  command('video-stop');
  writeFileSync(resolve(directory,'browser-proof.json'), JSON.stringify(measurements,null,2)+'\n');
  console.log(JSON.stringify(measurements,null,2));
} finally { command('close'); }
