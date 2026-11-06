async page => {
 const address = new URL(page.url());
 if (!['127.0.0.1', 'localhost'].includes(address.hostname) && address.origin !== 'http://192.168.31.205:18781') throw new Error('Use the isolated performance fixture');
 if (!await page.evaluate(() => window.__coreLogPerformanceFixture === true)) throw new Error('Use the synthetic performance fixture');
 await page.waitForFunction(()=>document.querySelector('[data-log-refresh-label]')?.textContent==='自动更新已暂停');
 const client = await page.context().newCDPSession(page);
 const events = [];
 client.on('Tracing.dataCollected', batch => events.push(...batch.value));
 await client.send('Tracing.start', {categories:'devtools.timeline',options:'record-as-much-as-possible'});
 await page.evaluate(()=>{ window.__phaseMetrics=[]; window.__longTasks=[]; });
 const steps = [];
 const step = async (name, action) => {
  const started = await page.evaluate(()=>performance.now());
  await action();
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  steps.push({name,ms:Math.round(await page.evaluate(()=>performance.now())-started)});
 };
 await step('search',async()=>{
  await page.evaluate(()=>{const input=document.querySelector('input[name=q]');input.focus();input.value='pressure entry 1999';input.dispatchEvent(new Event('input',{bubbles:true}));});
  await page.waitForFunction(()=>document.querySelectorAll('[data-log-entry]').length===4);
 });
 await step('reset',()=>page.evaluate(()=>[...document.querySelectorAll('.a-main button')].find(button => button.textContent === '清除筛选').click()));
 await step('page2',()=>page.evaluate(()=>[...document.querySelectorAll('.a-main button')].find(button => button.textContent === '下一页').click()));
 const completed = new Promise(resolve=>client.once('Tracing.tracingComplete',resolve));
 await client.send('Tracing.end'); await completed;
 const rendering = {};
 for (const event of events) {
  if (event.ph !== 'X' || !['Layout','UpdateLayoutTree','Paint'].includes(event.name)) continue;
  const value = rendering[event.name] ||= {calls:0,totalMs:0,maxMs:0};
  value.calls++; value.totalMs += (event.dur||0)/1000; value.maxMs=Math.max(value.maxMs,(event.dur||0)/1000);
 }
 const result = await page.evaluate(()=>({phases:window.__phaseMetrics,longTasks:window.__longTasks}));
 await client.detach();
 return {steps,rendering,...result};
}
