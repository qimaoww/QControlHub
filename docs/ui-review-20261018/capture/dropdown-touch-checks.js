async(page)=>{
 const checks=[];
 for(const theme of ['light','dark']){
  await page.setViewportSize({width:390,height:844});await page.goto('http://127.0.0.1:45231/?kind=certificates');await page.waitForSelector('html[data-review-ready=true]');await page.evaluate(async theme=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{}}).applyTheme(theme);},theme);
  const select=page.locator('[data-cert-source]');await select.tap();await page.locator('[role=listbox] [role=option]').filter({hasText:/^ACME$/}).tap();if(await select.inputValue()!=='acme')throw Error('Touch selection failed');
  await page.waitForFunction(()=>document.querySelectorAll('.user-allocation-row').length===1);
  await select.tap();await page.locator('[role=listbox] [role=option]').filter({hasText:/^全部来源$/}).tap();await page.waitForFunction(()=>document.querySelectorAll('.user-allocation-row').length===2);
  await page.locator('[data-cert-create]').tap();const method=page.locator('dialog[open] [name=method]');await method.tap();await page.locator('[role=listbox] [role=option]').filter({hasText:/HTTP-01/}).tap();await page.waitForSelector('dialog[open] [name=agent_id]');const agent=page.locator('dialog[open] [name=agent_id]');await agent.tap();const disabled=await page.locator('[role=listbox] [role=option]:disabled').count();if(disabled!==1)throw Error('Offline node became enabled');const sizes=await page.locator('[role=listbox] [role=option]').evaluateAll(es=>es.map(e=>e.getBoundingClientRect().height));if(sizes.some(h=>h<44))throw Error('Touch target below 44px');await page.locator('[role=listbox] [role=option]').filter({hasText:/香港/}).tap();if(await agent.inputValue()!=='alpha')throw Error('Dependent node selection failed');checks.push({theme,filterAndReset:true,methodMountsNode:true,offlineDisabled:true,dependentNodeSelected:true,optionHeights:sizes});
 }
 return checks;
}
