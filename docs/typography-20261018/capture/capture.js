async page => {
 const base='__BASE__',stage='__STAGE__',device='__DEVICE__',theme='__THEME__';
 const root='__ROOT__', viewport=device==='mobile'?{width:390,height:844}:{width:1440,height:1000};
 await page.setViewportSize(viewport);
 await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'});
 await page.addInitScript(()=>{
  const NativeDate=Date,epoch=NativeDate.parse('2026-10-18T05:00:00Z');
  window.Date=class extends NativeDate{constructor(...a){super(...(a.length?a:[epoch]));}static now(){return epoch;}};
 });
 const results=[];
 for(const kind of ['dashboard','settings','connections','config-editor','logs','certificates']){
  await page.goto(`${base}/?kind=${kind}&theme=${theme}&scale=100`);
  await page.waitForSelector('html[data-review-ready="true"]');
  await page.waitForSelector('.workspace-main');
  if(kind==='dashboard')await page.waitForSelector('.ops-stats strong');
  if(kind==='settings')await page.waitForSelector('[name=ui_font_scale]');
  if(kind==='connections')await page.waitForSelector('.connection-table-scroll');
  if(kind==='config-editor'){
   
   await page.waitForSelector('.code-editor-input');
  }
  if(kind==='logs')await page.waitForSelector('.core-log-row');
  if(kind==='certificates'){
   await page.locator('[data-cert-create]').click();
   await page.waitForSelector('[data-cert-form]');
  }
  await page.waitForTimeout(300);
  await page.screenshot({path:`${root}/${stage}-${device}-${theme}-${kind}.png`});
  results.push(await page.evaluate(({kind,viewport})=>{
   const styles=selector=>[...document.querySelectorAll(selector)].filter(e=>e.getClientRects().length).slice(0,12).map(e=>{
    const c=getComputedStyle(e);return{tag:e.tagName,classes:e.className,text:e.textContent.slice(0,80),size:c.fontSize,weight:c.fontWeight,lineHeight:c.lineHeight,family:c.fontFamily,spacing:c.letterSpacing};
   });
   return{kind,viewport,theme:document.documentElement.dataset.theme,scale:document.documentElement.dataset.uiFontScale,overflow:document.documentElement.scrollWidth-innerWidth,headings:styles('h1,h2,h3'),controls:styles('input,select,textarea,button'),code:styles('.code-editor-input,.code-gutter'),dialogs:[...document.querySelectorAll('dialog[open]')].map(d=>({client:d.clientWidth,scroll:d.scrollWidth}))};
  },{kind,viewport}));
 }
 return{stage,device,theme,results};
}
