async page=>{
 const base='__BASE__',root='__ROOT__',results=[];
 for(const view of [{name:'desktop',width:1440,height:1000},{name:'phone',width:390,height:844},{name:'narrow',width:320,height:740},{name:'tablet',width:820,height:1180},{name:'landscape',width:844,height:390}]){
  await page.setViewportSize({width:view.width,height:view.height});
  for(const theme of ['light','dark']){
   await page.goto(`${base}/?kind=certificates&theme=${theme}&scale=110`);
   await page.waitForSelector('[data-cert-create]');
   await page.locator('[data-cert-create]').click();
   const dialog=page.locator('dialog[open]');
   await dialog.waitFor();
   await page.locator('[data-cert-form] [name=name]').fill('跨区域 TLS 证书 · API 443');
   await page.locator('[data-cert-form] [name=domains]').fill('example.test\n*.example.test');
   await page.keyboard.press('Tab');
   await page.keyboard.press('Shift+Tab');
   const geometry=await dialog.evaluate(e=>({client:[e.clientWidth,e.clientHeight],scroll:[e.scrollWidth,e.scrollHeight],bounds:{x:e.getBoundingClientRect().x,y:e.getBoundingClientRect().y,width:e.getBoundingClientRect().width,height:e.getBoundingClientRect().height},focusedInside:e.contains(document.activeElement)}));
   const cancel=dialog.locator('[data-cert-close]').last();
   await cancel.scrollIntoViewIfNeeded();
   const hit=await cancel.evaluate(e=>{const r=e.getBoundingClientRect();return e.contains(document.elementFromPoint(r.x+r.width/2,r.y+r.height/2));});
   await cancel.tap();
   await dialog.waitFor({state:'hidden'});
   const restored=await page.locator('[data-cert-create]').evaluate(e=>document.activeElement===e);
   results.push({view:view.name,theme,kind:'dialog',geometry,cancelHit:hit,focusRestored:restored});
   // Open the same shared select menu used by settings, forms and filters.
   await page.goto(`${base}/?kind=connections&theme=${theme}&scale=110`);
   await page.waitForSelector('.connection-table-scroll');
   const picker=page.locator('select[name=engine]').first();
   await picker.waitFor();await picker.tap();
   await page.waitForSelector('[role=listbox]:popover-open');
   const menu=await page.locator('[role=listbox]:popover-open').evaluate(e=>{const r=e.getBoundingClientRect();return{left:r.left,right:r.right,top:r.top,bottom:r.bottom,viewport:[innerWidth,innerHeight],scroll:e.scrollWidth,client:e.clientWidth};});
   await page.keyboard.press('Escape');
   results.push({view:view.name,theme,kind:'select',menu});
  }
 }
 return results;
}
