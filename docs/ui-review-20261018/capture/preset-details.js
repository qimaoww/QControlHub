async(page)=>{
 const root='/root/.codex/worktrees/7d05/qcontrolhub/docs/ui-review-20261018/evidence',results=[];
 const device='__DEVICE__';await page.setViewportSize(device==='mobile'?{width:390,height:844}:{width:1440,height:1000});
 for(const stage of ['before','after'])for(const theme of ['light','dark'])for(const kind of ['tls','xhttp']){
  const base=stage==='before'?'http://127.0.0.1:42135':'http://127.0.0.1:41959';
  await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'});await page.goto(base+'/?kind='+kind);await page.locator('#server-plan-form').waitFor();await page.waitForSelector('html[data-review-ready="true"]');await page.waitForTimeout(250);
  await page.evaluate(async t=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{}}).applyTheme(t)},theme);
  if(kind==='tls'){
   await page.locator('[data-tls-advanced] summary').focus();await page.keyboard.press('Enter');await page.locator('[name=tls_acme_email]').scrollIntoViewIfNeeded();
  }else{
   await page.locator('[data-builder-step=identity]').click();
   const summary=page.locator('.xhttp-options summary').filter({hasText:'XHTTP 高级参数'});await summary.focus();await page.keyboard.press('Enter');await page.locator('[name=xhttp_server_extra]').scrollIntoViewIfNeeded();
  }
  await page.screenshot({path:`${root}/${stage}-${device}-${theme}-${kind}-advanced.png`});
  const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1);if(overflow)throw Error('Preset overflows');results.push({stage,theme,kind,device,overflow});
 }return results;
}
