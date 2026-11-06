async page => {
 const origin = new URL(page.url());
 if (!['127.0.0.1', 'localhost'].includes(origin.hostname) && origin.origin !== 'http://192.168.31.205:18781') throw new Error('Use the isolated performance fixture');
 if (!await page.evaluate(() => window.__coreLogPerformanceFixture === true)) throw new Error('Use the synthetic performance fixture');
 await page.addInitScript(() => { localStorage.clear(); sessionStorage.clear(); });
 const navigationAt = Date.now();
 await page.goto(origin.origin+'/?mode=logs&run='+Date.now()+'#core-logs');
 await page.waitForSelector('[data-log-entry]', {timeout:60000});
 const firstVisibleMs = Date.now() - navigationAt;
 const result = {firstVisibleMs,steps:[]};
 const measure = async (name, action, loaded) => {
   const started = await page.evaluate(()=>performance.now());
   await action();
   if (loaded) await page.waitForFunction(loaded, null, {timeout:60000});
   await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   result.steps.push({name,ms:Math.round(await page.evaluate(()=>performance.now())-started)});
 };
 result.initial = await page.evaluate(()=>({loaded:document.querySelector('[data-log-refresh-label]')?.nextElementSibling?.textContent, rows:document.querySelectorAll('[data-log-entry]').length, requests:performance.getEntriesByType('resource').filter(e=>e.name.includes('/perf-core-logs')).map(e=>({limit:new URL(e.name).searchParams.get('limit'),ttfbMs:Math.round(e.responseStart-e.requestStart),transferMs:Math.round(e.responseEnd-e.responseStart),durationMs:Math.round(e.duration),encodedBytes:e.encodedBodySize,decodedBytes:e.decodedBodySize}))}));
 const toggle=page.getByRole('button', { name: '暂停更新', exact: true }).or(page.getByRole('button', { name: '自动更新', exact: true }));
 if (await toggle.textContent() === '暂停更新') await toggle.click();
 await page.waitForFunction(()=>document.querySelector('[data-log-refresh-label]')?.textContent === '自动更新已暂停');
 result.initialCompleteMs = Date.now() - navigationAt;
 await measure('limit2000', ()=>page.locator('select[name=limit]').selectOption('2000'), ()=>document.querySelector('[data-log-refresh-label]')?.textContent==='自动更新已暂停' && !document.querySelector('[data-log-refresh-label]').hasAttribute('aria-busy'));
 await measure('search',()=>page.locator('input[name=q]').fill('error'));
 await measure('reset',()=>page.getByRole('button', { name: '清除筛选', exact: true }).click());
 const next=page.getByRole('button', { name: '下一页', exact: true });
 if (await next.count() && await next.isEnabled()) await measure('page2',()=>next.click());
 const nodes=page.locator('[data-log-filters] select[name=agent_id]');
 await measure('coldSwitch',()=>nodes.selectOption('bravo'),()=>document.querySelector('[data-log-refresh-label]')?.textContent==='自动更新已暂停' && !document.querySelector('[data-log-refresh-label]').hasAttribute('aria-busy'));
 await nodes.selectOption('');
 await page.waitForFunction(()=>document.querySelector('[data-log-refresh-label]')?.textContent==='自动更新已暂停' && !document.querySelector('[data-log-refresh-label]').hasAttribute('aria-busy'));
 await measure('cachedSwitch',()=>nodes.selectOption('bravo'));
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
