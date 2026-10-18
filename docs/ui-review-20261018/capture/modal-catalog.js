async(page)=>{
 const device='__DEVICE__',theme='__THEME__',bases={before:'http://127.0.0.1:41767',after:'http://127.0.0.1:45231'},result={device,theme,stages:{}};
 await page.setViewportSize(device==='mobile'?{width:390,height:844}:device==='tablet'?{width:820,height:1180}:{width:1440,height:1000});await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'});
 for(const [stage,base] of Object.entries(bases)){
  await page.goto(base+'/?kind=inbound');await page.waitForSelector('html[data-review-ready=true]');
  result.stages[stage]=await page.evaluate(async({theme,stage})=>{
   const {createConfigFixture}=await import('/assets/browser/config-fixture.mjs'),{readServerPlanInput}=await import('/assets/modules/server-plan-form.js'),{createShellAppearance}=await import('/assets/modules/shell-appearance.js');
   createShellAppearance({data:{}}).applyTheme(theme);
   const catalog=await fetch('/assets/preset-plans.json').then(r=>r.json()),results=[];
   for(const entry of catalog){
    const fixture=await createConfigFixture(entry.engine,{protocol:entry.protocol.key});
    fixture.click('add');
    for(let i=0;i<200&&!document.querySelector('#server-plan-form');i++)await new Promise(r=>setTimeout(r,10));
    const protocolSelect=document.querySelector('[data-preset-protocol]');
    protocolSelect.value=entry.protocol.key;protocolSelect.dispatchEvent(new Event('change',{bubbles:true}));
    for(let i=0;i<200&&document.querySelector('[data-preset-protocol]')?.value!==entry.protocol.key;i++)await new Promise(r=>setTimeout(r,10));
    await new Promise(r=>setTimeout(r,50));
    const form=document.querySelector('#server-plan-form'),values=readServerPlanInput(form,entry.protocol);
    const controls=[...form.elements].filter(el=>el.name).map(el=>({name:el.name,type:el.type,required:el.required,disabled:el.disabled,min:el.min,max:el.max,pattern:el.pattern,maxLength:el.maxLength,options:el.options&&[...el.options].map(o=>[o.value,o.disabled])}));
    const tabs=[...form.querySelectorAll('[data-builder-step]')],states=[];
    for(const tab of tabs.length?tabs:[null]){
     tab?.click();await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
     const overflow=[...form.querySelectorAll('input:not([type=hidden]),select,textarea')].filter(el=>{const b=el.getBoundingClientRect();return b.width>0&&(b.right>innerWidth+1||b.left<0)}).map(el=>el.name);
     if(overflow.length||document.documentElement.scrollWidth>innerWidth+1)throw Error(`${stage}/${entry.engine}/${entry.protocol.key}/${tab?.dataset.builderStep}: overflow ${overflow}`);
     states.push(tab?.dataset.builderStep||'connection');
    }
    if(stage==='after'&&form.querySelector('.security-section,.tls-switch,.validation-note'))throw Error('Legacy decorated section remains');
    results.push({engine:entry.engine,protocol:entry.protocol.key,values,controls,states});fixture.dispose();
   }
   return results;
  },{theme,stage});
 }
 if(JSON.stringify(result.stages.before)!==JSON.stringify(result.stages.after))throw Error('Preset fields, constraints or payload changed');
 return {...result,passed:true};
}
