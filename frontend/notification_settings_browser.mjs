import assert from 'node:assert/strict';

// Uses the existing authenticated Chromium/API journey. Only transport failures
// and delivery progress are injected; configuration and preview use the server.
export async function checkNotificationSettings({page, mode, evaluate, wait, screenshot, api, identity, call, record}) {
  const click = (text, root = '.a-main') => evaluate(page, `[...document.querySelectorAll(${JSON.stringify(root)}+' button')].find(b=>b.textContent===${JSON.stringify(text)}).click()`);
  const loaded = () => wait(page, `!document.querySelector('[aria-busy=true]')`);
  await evaluate(page, `document.querySelectorAll('.a-toast-close').forEach(button=>button.click())`);
  const baseline = (await api(page, identity, 'GET', '/settings/notifications')).configuration;
  const config = structuredClone(baseline);
  for (let n=0;n<14;n++) {
    const id = `layout-${mode}-${n}`;
    config.channels.push({id, name:`渠道 ${n} `+'abcdefghij'.repeat(8), type:'webhook', enabled:false, secrets:{url:'https://example.invalid/isolated-layout'}});
    config.rules.push({id, name:`规则 ${n} `+'abcdefghij'.repeat(8), event:'agent.offline', enabled:false, channels:[id,`layout-${mode}-${(n+1)%14}`], duration_seconds:30, cooldown_seconds:300});
  }
  await api(page, identity, 'PUT', '/settings/notifications', config);
  await click('刷新', '.a-top'); await loaded();
  await wait(page, `!!document.querySelector('[data-channel-id="layout-${mode}-0"]')`);
  await evaluate(page, `window.__layoutScroll=document.querySelector('.a-table-scroll[aria-label="通知渠道"]');window.__layoutRow=document.querySelector('[data-channel-id="layout-${mode}-0"]').closest('tr')`);
  await click('下一页', '.a-main section:first-of-type');
  await click('刷新', '.a-top'); await loaded();
  assert.ok(await evaluate(page, `window.__layoutScroll===document.querySelector('.a-table-scroll[aria-label="通知渠道"]') && document.querySelector('.a-main section:first-of-type .a-pagination').textContent.includes('第 2 /')`), 'configuration refresh retains the table and current page');
  await click('上一页', '.a-main section:first-of-type');
  const measure = async label => {
    const result = await evaluate(page, `(() => {
      const box=document.querySelector('dialog[open]');
      return {viewport:[innerWidth,innerHeight,devicePixelRatio,visualViewport.scale],
        page:document.documentElement.scrollWidth<=innerWidth,
        tables:[...document.querySelectorAll('.a-main .a-table-scroll')].filter(e=>e.clientWidth).every(e=>e.scrollWidth<=e.clientWidth+1),
        dialog:!box||box.scrollWidth<=box.clientWidth+1,
        controls:[...(box||document.querySelector('.a-main')).querySelectorAll('button,input,select,textarea')].filter(e=>e.getClientRects().length).every(e=>{const r=e.getBoundingClientRect();return r.left>=-1&&r.right<=innerWidth+1})};
    })()`);
    assert.ok(result.page && result.tables && result.dialog && result.controls, `${label}: ${JSON.stringify(result)}`);
    record(`${mode}: notification ${label} CSS viewport/DPR/scale ${result.viewport.join('/')} fits`);
  };
  const widths = mode==='mobile' ? [[320,568],[390,844],[844,390],[768,1024],[1024,768]] : [[900,900],[1280,900],[1536,960],[1920,1080],[3840,2160]];
  for (const [width,height] of widths) {
    await call('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:mode==='mobile'?3:1,mobile:mode==='mobile'},page);
    await measure(`${width}x${height}`);
    if (width===390||width===1920||width===3840) await screenshot(page,`notification-list-${width}`);
  }
  await call('Emulation.setDeviceMetricsOverride',{width:mode==='mobile'?390:1920,height:mode==='mobile'?844:1080,deviceScaleFactor:1,mobile:mode==='mobile'},page);
  await click('添加渠道');
  const change = (name,value) => evaluate(page, `(()=>{const input=document.querySelector('dialog[open] [name="${name}"]');input.value=${JSON.stringify(value)};input.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  for (const type of ['telegram','smtp','bark','ntfy','gotify','webhook']) {
    await change('type',type); await measure(`channel ${type}`);
    if (type==='smtp') assert.equal(await evaluate(page, `document.querySelector('dialog[open] [name=tls]').value`),'starttls','SMTP starts with a valid security mode');
  }
  await change('url','https://example.invalid/draft');
  await change('type','smtp'); await change('type','webhook');
  assert.equal(await evaluate(page, `document.querySelector('dialog[open] [name=url]').value`),'https://example.invalid/draft');
  await screenshot(page,'notification-channel-dialog');
  await evaluate(page, `document.querySelector('dialog[open] [aria-label="关闭"]').click()`);
  await wait(page, `document.querySelectorAll('dialog[open]').length===2`);
  await click('取消','dialog[open]:last-of-type');
  assert.equal(await evaluate(page, `document.querySelector('dialog[open] [name=url]').value`),'https://example.invalid/draft');
  await evaluate(page, `document.querySelector('dialog[open] [aria-label="关闭"]').click()`);
  await click('关闭编辑','dialog[open]:last-of-type');
  await click('添加规则');
  for (const event of Object.keys((await api(page,identity,'GET','/settings/notifications')).events).filter(e=>e!=='speed.recovered')) {
    await change('event',event); await measure(`rule ${event}`);
  }
  await change('event','speed.high'); await change('threshold','123456');
  await change('event','panel.update'); await change('event','speed.high');
  assert.equal(await evaluate(page, `document.querySelector('dialog[open] [name=threshold]').value`),'123456');
  await screenshot(page,'notification-rule-dialog');
  await evaluate(page, `document.querySelector('dialog[open] [aria-label="关闭"]').click()`);
  await click('关闭编辑','dialog[open]:last-of-type');
  await evaluate(page, `(()=>{const d=[...document.querySelectorAll('summary')].find(s=>s.textContent==='编辑事件模板').parentElement;d.open=true})()`);
  // A late preview must not overwrite a changed draft or lock navigation.
  await evaluate(page, `window.__layoutFetch=window.fetch;window.fetch=async (...args)=>{if(String(args[0]).endsWith('/notifications/preview'))await new Promise(resolve=>window.__releaseLayoutPreview=resolve);return window.__layoutFetch(...args)};`);
  await click('渲染预览');
  await wait(page, `!!window.__releaseLayoutPreview`);
  await evaluate(page, `(()=>{const input=document.querySelector('textarea[name=body]');input.value='新的预览草稿';input.dispatchEvent(new Event('input',{bubbles:true}));window.__releaseLayoutPreview();window.fetch=window.__layoutFetch})()`);
  await loaded();
  assert.ok(await evaluate(page, `![...document.querySelectorAll('.a-main [role=status]')].some(e=>e.textContent.includes('脱敏示例'))`),'late preview is discarded');
  // Real preview renders long links, code, title and multiline body.
  await evaluate(page, `(()=>{const body=document.querySelector('textarea[name=body]'),title=document.querySelector('input[name=title]'),format=document.querySelector('select[name=template_format]');title.value='长标题'+ 'abcdefghij'.repeat(35);body.value='**值班告警**\\n[长链接](https://example.invalid/'+ 'abcdefghij'.repeat(50)+')\\n'+ 'abcdefghij'.repeat(120)+'\\n\u0060'+ 'abcdefghij'.repeat(60)+'\u0060';format.value='markdown';body.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  await click('渲染预览'); await loaded();
  await wait(page, `[...document.querySelectorAll('.a-main [role=status]')].some(e=>e.textContent.includes('脱敏示例'))`);
  await measure('long Markdown preview');
  await evaluate(page, `[...document.querySelectorAll('.a-main [role=status]')].find(e=>e.textContent.includes('脱敏示例')).scrollIntoView({block:'center'})`);
  await screenshot(page,'notification-long-preview');
  // Keep table identity, pagination, selection and draft while refreshing statuses.
  await evaluate(page, `window.__layoutDeliveryReads=0;window.fetch=async (...args)=>{
    if(!String(args[0]).endsWith('/notifications/deliveries'))return window.__layoutFetch(...args);
    window.__layoutDeliveryReads++;
    return new Response(JSON.stringify(Array.from({length:18},(_,i)=>({id:i+1,channel_id:'layout-${mode}-0',event:{type:'agent.offline'},created_at:'2026-11-01T01:02:03Z',updated_at:'2026-11-01T01:03:03Z',next_at:'2026-11-01T01:04:03Z',attempts:i%6,status:['pending','sending','accepted','failed','canceled','pending'][i%6],error:i%6===3?'投递错误 '+ 'abcdefghij'.repeat(80):''}))),{headers:{'Content-Type':'application/json'}})
  }`);
  await click('刷新记录'); await loaded();
  await click('下一页','.a-main section:nth-of-type(4)');
  await evaluate(page, `window.__layoutRecord=document.querySelector('[data-delivery-id]');const body=document.querySelector('textarea[name=body]');body.focus();body.setSelectionRange(2,7);window.__layoutBody=body;window.__layoutY=scrollY`);
  // Use click() to avoid moving focus just to invoke the read.
  await click('刷新记录'); await loaded();
  assert.ok(await evaluate(page, `window.__layoutRecord===document.querySelector('[data-delivery-id]')&&document.activeElement===window.__layoutBody&&window.__layoutBody.selectionStart===2&&scrollY===window.__layoutY&&document.querySelector('.a-main section:nth-of-type(4) .a-pagination').textContent.includes('第 2 /')`),'delivery refresh retains row, page, focus, selection and scrolling');
  await measure('all delivery statuses and long error');
  await evaluate(page, `document.querySelector('.a-main section:nth-of-type(4)').scrollIntoView()`);
  await screenshot(page,'notification-delivery-states');
  // Bound consecutive read failures without waiting through production backoff.
  await evaluate(page, `window.__layoutTimeout=window.setTimeout;window.setTimeout=(fn,ms,...args)=>window.__layoutTimeout(fn,[10000,20000,30000].includes(ms)?20:ms,...args);window.__layoutFailedReads=0;window.fetch=(...args)=>{if(!String(args[0]).endsWith('/notifications/deliveries'))return window.__layoutFetch(...args);window.__layoutFailedReads++;return Promise.resolve(new Response(JSON.stringify({error:'隔离环境连续读取失败'}),{status:503,headers:{'Content-Type':'application/json'}}))}`);
  await click('刷新记录');
  await wait(page, `document.querySelector('.a-main').textContent.includes('已暂停自动刷新')`);
  assert.equal(await evaluate(page, `window.__layoutFailedReads`),6,'polling stops after five bounded retries');
  await evaluate(page, `window.setTimeout=window.__layoutTimeout;window.fetch=window.__layoutFetch`);
  await click('刷新记录'); await loaded();
  // Restore saved content through API, exercising the existing discard path.
  await evaluate(page, `window.fetch=window.__layoutFetch`);
  const latest=(await api(page,identity,'GET','/settings/notifications')).configuration;
  await api(page,identity,'PUT','/settings/notifications',{...baseline,revision:latest.revision});
  await click('刷新','.a-top');
  await wait(page, `!!document.querySelector('dialog[open]')`);
  await click('刷新通知配置','dialog[open]'); await loaded();
  await call('Emulation.setDeviceMetricsOverride',{width:mode==='mobile'?390:1440,height:mode==='mobile'?844:1000,deviceScaleFactor:1,mobile:mode==='mobile'},page);
  record(`${mode}: notification responsive lists/dialogs, per-event drafts, stale preview, real Markdown preview and stable delivery refresh passed; delivery states use injected read responses`);
}
