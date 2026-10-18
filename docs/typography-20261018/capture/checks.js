async page => {
 const base='__BASE__',root='__ROOT__',views=__VIEWS__,results=[];
 for(const view of views){
  await page.setViewportSize({width:view.width,height:view.height});
  for(const theme of ['light','dark'])for(const scale of [90,100,110]){
   for(const kind of ['dashboard','batch','settings','connections','config-editor','logs','certificates','traffic','users','client','bbr','substore','retry']){
    await page.goto(`${base}/?kind=${kind}&theme=${theme}&scale=${scale}`);
    await page.waitForSelector('html[data-review-ready="true"]');
    await page.waitForSelector(kind==='substore'?'.substore-workspace':'.workspace-main');
    await page.waitForTimeout(100);
    await page.evaluate(async({theme,scale})=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{settings:{ui_font_scale:scale}}}).applyTheme(theme);},{theme,scale});
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    const data=await page.evaluate(()=>{
     const visible=e=>e.checkVisibility()&&e.getBoundingClientRect().width>0;
     const boxes=[...document.querySelectorAll('.workspace-main,.desktop-app,dialog[open],.settings-section,.node-card,.client-access-node-card,.bbr-card,.live-config-workspace,.connection-detail-panel')].filter(visible);
     const overflow=boxes.filter(e=>e.scrollWidth>e.clientWidth+1).map(e=>({selector:e.className,width:e.clientWidth,scroll:e.scrollWidth}));
     const clipped=[...document.querySelectorAll('button,.button,h1,h2,h3')].filter(e=>visible(e)&&getComputedStyle(e).overflow!=='hidden'&&(e.scrollWidth>e.clientWidth+2||e.scrollHeight>e.clientHeight+2)).map(e=>({selector:e.className||e.tagName,text:e.textContent.slice(0,80),client:[e.clientWidth,e.clientHeight],scroll:[e.scrollWidth,e.scrollHeight]}));
     const input=document.querySelector('.code-editor-input'),gutter=document.querySelector('.code-gutter');
     const code=input&&gutter?{input:[getComputedStyle(input).fontSize,getComputedStyle(input).lineHeight,getComputedStyle(input).paddingTop],gutter:[getComputedStyle(gutter).fontSize,getComputedStyle(gutter).lineHeight,getComputedStyle(gutter).paddingTop]}:null;
     return{theme:document.documentElement.dataset.theme,scale:document.documentElement.dataset.uiFontScale,pageOverflow:document.documentElement.scrollWidth-innerWidth,overflow,clipped,code};
    });
    if(data.theme!==theme||Number(data.scale)!==scale)throw new Error('Appearance mismatch: '+JSON.stringify({kind,theme,scale,data}));
    results.push({view:view.name,kind,requestedTheme:theme,requestedScale:scale,...data});
   }
  }
 }
 return results;
}
