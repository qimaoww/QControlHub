async(page)=>{
 const base='__BASE__',stage='__STAGE__',device='__DEVICE__',theme='__THEME__';
 const root='/root/.codex/worktrees/7d05/qcontrolhub/docs/ui-review-20261018/evidence/dropdowns';
 await page.setViewportSize(device==='mobile'?{width:390,height:844}:device==='tablet'?{width:820,height:1180}:{width:1440,height:1000});
 await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'});
 page.setDefaultTimeout(8000);const results=[];
 const open=async(kind)=>{await page.goto(base+'/?kind='+kind);await page.waitForSelector('html[data-review-ready=true]');await page.evaluate(async theme=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{}}).applyTheme(theme);},theme);};
 const capture=async(name,selector,closed=false)=>{
  const control=page.locator(selector).first();await control.evaluate(e=>{const section=e.closest('.builder-section[hidden]');if(section)document.querySelector(`[data-builder-step="${section.id}"]`)?.click();for(let p=e.parentElement;p;p=p.parentElement)if(p.tagName==='DETAILS')p.open=true;});await control.waitFor();await control.scrollIntoViewIfNeeded();
  const metrics=await page.evaluate(()=>({theme:document.documentElement.dataset.theme,touch:navigator.maxTouchPoints,viewport:{width:innerWidth,height:innerHeight},pageWidth:document.documentElement.scrollWidth,selects:[...document.querySelectorAll('select')].filter(e=>e.checkVisibility()).map(e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect();return {name:e.name||e.id||[...e.attributes].find(a=>a.name.startsWith('data-'))?.name,label:e.getAttribute('aria-label')||e.closest('label')?.childNodes[0]?.textContent||e.name,font:s.fontSize,height:r.height,width:r.width,overflow:e.scrollWidth>e.clientWidth+1,disabled:e.disabled,value:e.value,options:e.options.length,disabledOptions:[...e.options].filter(o=>o.disabled).length,borderRadius:s.borderRadius,color:s.color,background:s.backgroundColor};})}));
  if(stage==='after')for(const s of metrics.selects){if(parseFloat(s.font)<(device==='desktop'?13:16)||s.height<(device==='desktop'?42:44)-.5)throw Error(name+' select metrics '+JSON.stringify(s));}
  if(closed)await page.screenshot({path:`${root}/${stage}-${device}-${theme}-${name}-closed.png`});
  const value=await control.inputValue();await control.click();if(stage==='after'){await page.locator('[role=listbox][popover]').waitFor();const box=await page.locator('[role=listbox][popover]').boundingBox();if(!box||box.x<-.5||box.y<-.5||box.x+box.width>metrics.viewport.width+.5||box.y+box.height>metrics.viewport.height+.5)throw Error(name+' popup overflow '+JSON.stringify(box));}await page.screenshot({path:`${root}/${stage}-${device}-${theme}-${name}.png`});await page.keyboard.press('Escape');
  if(await control.inputValue()!==value)throw Error(name+' Escape changed selection');
  results.push({name,opened:true,escapePreservesValue:true,...metrics});
 };
 await open('certificates');await capture('certificate-filters','[data-cert-source]',true);
 await page.locator('[data-cert-create]').click();await page.locator('[name=method]').selectOption('http-01');await capture('certificate-node','[name=agent_id]');
 await open('certificates');await page.locator('[data-cert-accounts]').click();await page.locator('[data-cert-new-dns]').click();await capture('certificate-provider','[name=provider]');
 await open('traffic');await capture('traffic-filters','[data-traffic-filter=endpoint_key]',true);
 await page.locator('[data-traffic-edit-open]').first().click();await capture('traffic-cycle','dialog[open] [name=cycle]');
 await open('settings');await capture('settings','[name=time_zone]',true);
 await open('logs');await capture('logs','select[name=agent_id]');
 await open('config-editor');await page.locator('.config-tools-menu>summary').click();await page.locator('[data-inbound-action=advanced]').click();await capture('config-field','dialog[open] select');
 await open('client');await capture('client-format','[data-client-display-format]',true);
 const displayID=await page.locator('dialog select[name=address_mode]').first().evaluate(e=>e.closest('dialog').id);await page.locator(`[data-client-display-open="${displayID}"]`).click();await capture('client-address','dialog[open] select[name=address_mode]');
 await open('substore');await page.locator('[data-substore-remove]').click();await page.locator('[data-substore-remove]').waitFor({state:'hidden'});await page.locator('[data-substore-add]').first().click();await capture('substore-address','select[name=address_mode]');
 await open('connections');await capture('connections','select[name=engine]');
 await open('retry');await capture('tasks','#task-status');
 await open('batch');await page.locator('[data-node-batch-toggle]').click();await page.locator('.batch-select-all').click();await capture('batch','[name=action]');
 await open('tls');await page.locator('[data-tls-advanced]').evaluate(e=>e.open=true);await capture('tls','select[name=tls_mode]',true);
 await open('xhttp');await page.locator('.xhttp-options').evaluateAll(es=>es.forEach(e=>e.open=true));await capture('xhttp','select[name=xhttp_cdn]',true);
 return {stage,device,theme,results};
}
