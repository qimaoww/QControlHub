async page=>{
 const base='__BASE__',root='__ROOT__',results=[];
 for(const view of [{name:'desktop',width:1440,height:1000},{name:'phone',width:320,height:740},{name:'tablet',width:820,height:1180},{name:'landscape',width:844,height:390}]){
  await page.setViewportSize({width:view.width,height:view.height});
  for(const theme of ['light','dark']){
   await page.goto(`${base}/?kind=dashboard&theme=${theme}&scale=110`);
   await page.waitForSelector('.dashboard-workspace');
   await page.evaluate(async theme=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{settings:{ui_font_scale:110}}}).applyTheme(theme);},theme);
   await page.evaluate(()=>{
    document.querySelector('.dashboard-head h2').textContent='跨区域运行总览 · Hong Kong / 新加坡：长标题、12345678901234567890 与中文标点（延迟 / 流量）';
    document.querySelector('.panel-metric>small').textContent='多行说明：IPv6 与 12345678901234567890 连续数字保持自然换行。节点的 CPU、内存及 1.25 GiB/s 流量仅表示当前采样；连接失败时请检查配置。'.repeat(3);
   });
   const geometry=()=>page.evaluate(()=>({pageOverflow:document.documentElement.scrollWidth-innerWidth,boxes:[...document.querySelectorAll('h1,h2,h3,.panel-metric>small')].filter(e=>e.checkVisibility()).map(e=>({text:e.textContent.slice(0,40),width:e.clientWidth,scroll:e.scrollWidth}))}));
   results.push({view:view.name,theme,kind:'long-copy',...await geometry()});
   await page.screenshot({path:`${root}/stress-${view.name}-${theme}-copy.png`});
   await page.goto(`${base}/?kind=config-editor&theme=${theme}&scale=110`);
   await page.waitForSelector('.code-editor-input');
   await page.evaluate(async theme=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{settings:{ui_font_scale:110}}}).applyTheme(theme);},theme);
   const content=JSON.stringify({说明:'中英文、端口与 units：12345678901234567890 · 443 / 65535',items:Array.from({length:160},(_,i)=>({port:21000+i,name:`跨区域节点 ${i}`,url:'https://example.test/'+ '0123456789abcdef'.repeat(24)}))},null,2);
   await page.locator('.code-editor-input').fill(content);
   await page.locator('[data-code-format]').click();
   const code=await page.evaluate(()=>{
    const input=document.querySelector('.code-editor-input'),gutter=document.querySelector('.code-gutter');
    input.scrollTop=1600;input.scrollLeft=120;input.dispatchEvent(new Event('scroll'));
    const props=e=>{const c=getComputedStyle(e);return[c.fontFamily,c.fontSize,c.lineHeight,c.paddingTop]};
    return{lines:input.value.split('\n').length,gutterLines:gutter.textContent.split('\n').length,input:props(input),gutter:props(gutter),scroll:[input.scrollTop,gutter.scrollTop],horizontalCodeScroll:input.scrollWidth>input.clientWidth,pageOverflow:document.documentElement.scrollWidth-innerWidth,valid:JSON.parse(input.value).items.length===160};
   });
   results.push({view:view.name,theme,kind:'code',...code});
   await page.screenshot({path:`${root}/stress-${view.name}-${theme}-code.png`});
  }
 }
 return results;
}
