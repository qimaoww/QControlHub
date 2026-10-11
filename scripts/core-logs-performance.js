async page => {
 const origin = new URL(page.url());
 if (!['127.0.0.1','localhost'].includes(origin.hostname)) throw new Error('Use the loopback performance fixture');
 await page.addInitScript(() => { localStorage.clear(); sessionStorage.clear(); });
 const navigationAt = Date.now();
 await page.goto(origin.origin+'/?mode=logs&run='+Date.now()+'#core-logs');
 await page.waitForSelector('.core-log-row', {timeout:60000});
 const firstVisibleMs = Date.now() - navigationAt;
 const result = {firstVisibleMs,steps:[]};
 const measure = async (name, action, loaded) => {
   const started = await page.evaluate(()=>performance.now());
   await action();
   if (loaded) await page.waitForFunction(loaded, null, {timeout:60000});
   await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   result.steps.push({name,ms:Math.round(await page.evaluate(()=>performance.now())-started)});
 };
 result.initial = await page.evaluate(()=>({loaded:document.querySelector('.core-log-status')?.textContent, rows:document.querySelectorAll('.core-log-row').length, requests:performance.getEntriesByType('resource').filter(e=>e.name.includes('/perf-core-logs')).map(e=>({limit:new URL(e.name).searchParams.get('limit'),ttfbMs:Math.round(e.responseStart-e.requestStart),transferMs:Math.round(e.responseEnd-e.responseStart),durationMs:Math.round(e.duration),encodedBytes:e.encodedBodySize,decodedBytes:e.decodedBodySize}))}));
 const toggle=page.locator('[data-toggle-core-log-refresh]');
 if (await toggle.getAttribute('aria-checked') === 'true') await toggle.click();
 await page.waitForFunction(()=>document.querySelector('[data-core-log-refresh-label]')?.textContent === '自动更新已暂停');
 result.initialCompleteMs = Date.now() - navigationAt;
 await measure('limit2000', ()=>page.locator('select[name=limit]').selectOption('2000'), ()=>document.querySelector('[data-core-log-refresh-label]')?.textContent==='自动更新已暂停');
 await measure('search',()=>page.locator('input[name=q]').fill('error'));
 await measure('reset',()=>page.locator('[data-reset-core-logs]').click());
 const next=page.locator('.core-log-result-toolbar [data-core-log-page-index]').last();
 if (await next.count() && await next.isEnabled()) await measure('page2',()=>next.click());
 const nodes=page.locator('[data-core-log-agent]').filter({hasNotText:'全部节点日志'});
 if (await nodes.count()) {
  await measure('coldSwitch',()=>nodes.first().click(),()=>document.querySelector('[data-core-log-refresh-label]')?.textContent==='自动更新已暂停');
  await page.locator('[data-core-log-agent=""]').click();
  await page.waitForFunction(()=>document.querySelector('[data-core-log-refresh-label]')?.textContent==='自动更新已暂停');
  await measure('cachedSwitch',()=>nodes.first().click());
 }
 result.network = await page.evaluate(()=>performance.getEntriesByType('resource').filter(e=>e.name.includes('/perf-core-logs')).map(e=>({limit:new URL(e.name).searchParams.get('limit'),ttfbMs:Math.round(e.responseStart-e.requestStart),transferMs:Math.round(e.responseEnd-e.responseStart),durationMs:Math.round(e.duration),encodedBytes:e.encodedBodySize,decodedBytes:e.decodedBodySize})));
 result.longTasks = await page.evaluate(()=>window.__longTasks);
 result.phases = await page.evaluate(() => {
   const result = {};
   for (const { phase, ms } of window.__phaseMetrics || []) {
     const value = result[phase] ||= { calls: 0, totalMs: 0, maxMs: 0 };
     value.calls++; value.totalMs += ms; value.maxMs = Math.max(value.maxMs, ms);
   }
   return result;
 });
 result.browser = await page.evaluate(()=>({ua:navigator.userAgent,width:innerWidth,height:innerHeight}));
 return result;
}
