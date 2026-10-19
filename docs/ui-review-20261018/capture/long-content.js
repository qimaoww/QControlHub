async(page)=>{
 const base='__BASE__',stage='__STAGE__',device='__DEVICE__',theme='__THEME__';
 const root='/root/.codex/worktrees/7d05/qcontrolhub/docs/ui-review-20261018/evidence/round3';
 await page.setViewportSize(device==='mobile'?{width:390,height:844}:device==='tablet'?{width:820,height:1180}:{width:1440,height:1000});
 await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'});
 await page.goto(base+'/?kind=certificates');await page.locator('[data-cert-create]').waitFor();
 await page.evaluate(async theme=>{
  const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{}}).applyTheme(theme);
  const original=window.fetch;
  const domain='a'.repeat(63)+'.'+'b'.repeat(63)+'.'+'c'.repeat(63)+'.example.test';
  const name='ProductionCertificate'+'x'.repeat(79);
  window.fetch=async(input,options)=>{
   const response=await original(input,options);const path=new URL(input instanceof Request?input.url:input,location.href).pathname;
   if(!path.startsWith('/api/v1/certificate'))return response;
   const body=await response.json();
   if(path==='/api/v1/certificate-managed-sources')body[0].domain=domain;
   else if(body.certificate){body.certificate.name=name;body.certificate.domains=[domain];for(const b of body.bindings||[])b.config_name='WebSocketTLS'+'x'.repeat(70);for(const g of body.grants||[])g.username='recipient'+'a'.repeat(31);for(const o of body.orders||[])for(const c of o.challenges||[]){c.domain=domain;c.name='_acme-challenge.'+domain;c.value='DnsChallenge'+'x'.repeat(170);}}
   return new Response(JSON.stringify(body),{status:response.status,headers:{'Content-Type':'application/json'}});
  };
 },theme);
 const results=[];const close=async()=>{await page.locator('dialog[open] [data-cert-close]').first().click();await page.locator('dialog[open]').waitFor({state:'detached'});};
 const shot=async name=>{
  await page.screenshot({path:`${root}/${stage}-${device}-${theme}-${name}.png`});
  results.push({name,...await page.evaluate(()=>{const d=document.querySelector('dialog[open]'),c=document.querySelector('[data-cert-close]');return {theme:document.documentElement.dataset.theme,width:innerWidth,document:document.documentElement.scrollWidth,dialog:d?{width:d.clientWidth,scroll:d.scrollWidth}:null,close:c?{rect:c.getBoundingClientRect().toJSON(),reachable:c.getBoundingClientRect().right<=innerWidth,unobstructed:(()=>{const r=c.getBoundingClientRect();return c.contains(document.elementFromPoint(r.x+r.width/2,r.y+r.height/2));})()}:null,clipped:[...d.querySelectorAll('h2,h3,.agent-share-usage,code')].filter(e=>(()=>{if(!e.checkVisibility())return false;const r=e.getBoundingClientRect(),bounds=(e.closest('.settings-section')||d).getBoundingClientRect(),range=document.createRange();range.selectNodeContents(e);return (e.clientWidth>0&&e.scrollWidth>e.clientWidth+1)||[...range.getClientRects()].some(t=>t.width>0&&(t.right>Math.min(r.right,bounds.right)+1||t.left<Math.max(r.left,bounds.left)-1));})()).map(e=>({tag:e.tagName,classes:e.className,width:e.clientWidth,scroll:e.scrollWidth})),titleFont:getComputedStyle(d.querySelector('h2')).fontSize}})});
 };
 await page.locator('[data-cert-detail="review-cert"]').click();await page.locator('dialog[open] h2').waitFor();await shot('long-title');await page.getByRole('heading',{name:'实例绑定与同步'}).scrollIntoViewIfNeeded();await shot('long-instance');
 await page.locator('[data-cert-share]').click();await page.locator('[data-cert-grant-form]').waitFor();await shot('long-binding');await close();
 await page.locator('[data-cert-sources]').click();await page.locator('[data-cert-source-action]').first().waitFor();await shot('long-source');await close();
 await page.locator('[data-cert-detail="review-dns"]').click();await page.locator('[data-cert-continue]').waitFor();await page.locator('dialog[open] .user-allocation-tools > strong.agent-share-usage').evaluate(e=>e.scrollIntoView({block:'start'}));await shot('long-dns-domain');await page.locator('[data-cert-continue]').scrollIntoViewIfNeeded();await shot('long-dns');
 if(stage==='after')for(const r of results){if(r.dialog.scroll>r.dialog.width+1||r.clipped.length||!r.close.unobstructed)throw Error(JSON.stringify(r));}return {stage,device,theme,results};
}
