async(page)=>{
 const base='__BASE__',stage='__STAGE__',device='__DEVICE__',theme='__THEME__';
 const root='/root/.codex/worktrees/7d05/qcontrolhub/docs/ui-review-20261018/evidence/dropdowns';
 await page.setViewportSize(device==='mobile'?{width:390,height:844}:device==='tablet'?{width:820,height:1180}:{width:1440,height:1000});
 await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'});
 await page.goto(base+'/?kind=reality');await page.waitForSelector('html[data-review-ready=true]');
 await page.evaluate(async theme=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{}}).applyTheme(theme);},theme);
 await page.locator('[data-builder-step=security]').click();const input=page.locator('[name=reality_server_name]');await input.fill('');await input.click();await page.keyboard.press('ArrowDown');
 if(stage==='after')await page.locator('[role=listbox][popover]').waitFor();
 await page.screenshot({path:`${root}/${stage}-${device}-${theme}-reality-domain.png`});await page.keyboard.press('Escape');
 if(await input.inputValue()!=='')throw Error('Escape changed free text');
 if(stage==='after'){await input.fill('custom.example.test');if(await page.locator('[role=listbox]').count())throw Error('Free text retained unrelated suggestion');}
 return {stage,device,theme,realitySuggestions:true,freeTextAllowed:true};
}
