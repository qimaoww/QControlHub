async(page)=>{
 const base='__BASE__',stage='__STAGE__',device='__DEVICE__',theme='__THEME__';
 const root='/root/.codex/worktrees/7d05/qcontrolhub/docs/ui-review-20261018/evidence/inbounds';
 await page.setViewportSize(device==='mobile'?{width:390,height:844}:device==='tablet'?{width:820,height:1180}:{width:1440,height:1000});
 await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'});
 const cases=[
 ['modal-listen','xray','vless','listen',true],['modal-identity','xray','vless','identity',true],
 ['modal-reality','xray','vless','security',true],['modal-advanced','xray','vless','security',true],
 ['modal-tls','xray','trojan','security',true],['modal-xhttp','xray','vless-xhttp-tls','identity',true],
 ['modal-xhttp-help','xray','vless-xhttp-tls','identity',true],['modal-split','xray','vless-xhttp-reality','identity',true],
 ['modal-managed','sing-box','vless-ws-tls','connection',true],['modal-managed-advanced','sing-box','vless-ws-tls','connection',true],
 ['modal-mieru','mihomo','mieru','identity',true],['modal-snell','mihomo','snell-shadow-tls-v3','identity',true],
 ['modal-sudoku','mihomo','sudoku','identity',true],['modal-ssrust','ss-rust','ss2022','identity',true],
 ['modal-forward','xray','port-forward','target',true],['modal-wireguard','sing-box','wireguard','identity',true]
 ];
 const results=[];
 for(const [name,engine,protocol,section,embedded] of cases){
  await page.goto(`${base}/?kind=inbound&engine=${engine}&protocol=${protocol}${embedded?'&embedded=1':''}`);
  await page.waitForSelector('html[data-review-ready=true]');
  await page.evaluate(async theme=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{}}).applyTheme(theme);},theme);
  if(embedded){
   await page.locator('[data-inbound-action=add]').click();
   await page.locator('[data-preset-protocol]').selectOption(protocol);
   await page.locator('#server-plan-form').waitFor();
  }
  const tab=page.locator(`[data-builder-step=${section}]`);
  if(await tab.count())await tab.click();
  if(name==='modal-managed-advanced')await page.locator('[data-tls-advanced]').evaluate(el=>el.open=true);
  if(name==='modal-advanced'&&stage==='after')await page.locator('[name=reality_min_client_ver]').evaluate(el=>el.closest('details').open=true);
  if(name==='modal-mieru')await page.locator('[name=mieru_traffic_pattern]').selectOption('custom');
  if(name==='modal-split'){
   const cb=page.locator('[name=xhttp_split]');await cb.evaluate(el=>el.closest('details').open=true);await cb.check();
  }
  await page.evaluate(()=>window.scrollTo({top:0,behavior:'instant'}));
  if(name==='modal-xhttp-help')await page.locator('.xhttp-options').nth(1).evaluate(el=>el.scrollIntoView({block:'start',behavior:'instant'}));
  if(name==='modal-split')await page.locator('[name=xhttp_split]').evaluate(el=>el.closest('details').scrollIntoView({block:'start',behavior:'instant'}));
  const record=await page.evaluate(async()=>{
   const form=document.querySelector('#server-plan-form');
   const {readServerPlanInput}=await import('/assets/modules/server-plan-form.js');
   const params=new URLSearchParams(location.search),cases=await fetch('/assets/preset-plans.json').then(r=>r.json());
   const protocol=cases.find(c=>c.engine===params.get('engine')&&c.protocol.key===params.get('protocol')).protocol;
   const values=readServerPlanInput(form,protocol);
   const section=[...form.querySelectorAll('.builder-section')].find(el=>!el.hidden&&getComputedStyle(el).display!=='none');
   return {values,section:section?.id,sectionBoxShadow:section&&getComputedStyle(section).boxShadow,
    securityClass:!!form.querySelector('.security-section'),documentOverflow:document.documentElement.scrollWidth>innerWidth+1,
    horizontalOverflows:[...form.querySelectorAll('input:not([type=hidden]),select,textarea')].filter(el=>el.getBoundingClientRect().width>0&&el.getBoundingClientRect().right>innerWidth+1).map(el=>el.name)};
  });
  if(record.documentOverflow||record.horizontalOverflows.length)throw Error(`${name} overflow ${JSON.stringify(record)}`);
  await page.screenshot({path:`${root}/${stage}-${device}-${theme}-${name}.png`});
  results.push({name,engine,protocol,embedded:!!embedded,...record});
 }
 return {stage,device,theme,results};
}
