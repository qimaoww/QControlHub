import assert from 'node:assert/strict';

// Real password/CSRF API sessions. Response failures are injected only at the
// browser boundary; successful reads/writes still use the isolated API/store.
export async function checkAnalyticsTraffic({ page, mode, fixture, evaluate, wait, screenshot, api, identity, record }) {
  const capture = screenshot;
  screenshot = async (target, label) => {
    await evaluate(target, "document.querySelectorAll('.a-toast [aria-label=关闭通知]').forEach(button=>button.click())");
    await capture(target, label);
  };
  const act = source => evaluate(page, `(() => { ${source} })()`);
  const agentID = fixture.analyticsAgent;
  const policies = await api(page, identity, 'GET', '/traffic-policies');
  const policy = policies.find(value => value.agent_id === agentID);
  assert.ok(policy?.deployment_confirmed && policy.is_current_user);
  const key = `policy:${policy.id}`;
  const row = `.a-main [data-traffic-key="${key}"]`;
  const click = (label, selector = 'dialog[open]') => evaluate(page, `[...document.querySelectorAll(${JSON.stringify(selector + ' button')})].find(button=>!button.closest('select')&&button.textContent===${JSON.stringify(label)}).click()`);
  const navigate = async hash => {
    await act( `location.hash=${JSON.stringify(hash)}`);
    await wait(page, `location.hash===${JSON.stringify(hash)} && !!document.querySelector('.a-traffic-page') && !document.querySelector('[aria-busy=true]')`);
  };
  const close = async () => {
    await act( "document.querySelectorAll('dialog[open]').forEach(box=>box.close())");
    await wait(page, "!document.querySelector('dialog[open],.a-motion-tail')");
  };
  const manage = async (label, confirmation) => {
    await click('统计详情', row);
    await wait(page, "!!document.querySelector('dialog[data-traffic-details] [name=month]') && !document.querySelector('[aria-busy=true]')");
    await click(label, 'dialog[data-traffic-details]');
    await wait(page, `[...document.querySelectorAll('dialog[open]:not([data-traffic-details]) h2')].some(title=>title.textContent===${JSON.stringify(confirmation)})`);
    if (label === '删除') assert.ok(await evaluate(page, "document.querySelector('dialog[open]:not([data-traffic-details])').textContent.includes('每日历史')"));
    await click(confirmation, 'dialog[open]:not([data-traffic-details])');
    await wait(page, "!document.querySelector('[aria-busy=true],dialog[open]:not([data-traffic-details])')");
    await close();
  };
  await navigate(`#traffic?agent=${agentID}`);
  assert.equal(await evaluate(page, "document.querySelector('.a-traffic-filters [name=agent]').value"), agentID);
  assert.equal(await evaluate(page, "document.querySelectorAll('.a-traffic-filters button[type=submit]').length"), 0);
  await act( "const search=document.querySelector('.a-traffic-filters [name=search]');search.value='no-such-port';search.dispatchEvent(new Event('input',{bubbles:true}))");
  assert.equal(await evaluate(page, "document.querySelectorAll('.a-main [data-traffic-key]').length"), 0);
  await click('重置', '.a-filterbar');
  assert.equal(await evaluate(page, "document.querySelector('.a-traffic-filters [name=agent]').value"), '');
  await act( `window.__trafficFetch=window.fetch;window.__trafficReads=0;window.fetch=(url,options)=>{if(String(url).endsWith('/traffic-policies')&&(!options?.method||options.method==='GET'))window.__trafficReads++;return window.__trafficFetch(url,options)};window.__trafficControl=document.querySelector(${JSON.stringify(row)}).querySelector('button');window.__trafficControl.focus()`);
  await wait(page, 'window.__trafficReads>0');
  assert.ok(await evaluate(page, `window.__trafficControl===document.querySelector(${JSON.stringify(row)}).querySelector('button') && document.activeElement===window.__trafficControl`), 'polling preserves row action identity and keyboard focus');
  assert.equal(await evaluate(page, "document.querySelector('.a-traffic-filters [name=agent]').value"), '', 'polling must not reapply a cleared route filter');
  await click('统计详情', row);
  await wait(page, "!!document.querySelector('dialog[open] [name=month]') && !document.querySelector('[aria-busy=true]')");
  assert.ok(await evaluate(page, "document.querySelector('dialog[open]').textContent.includes('统计口径')"));
  await act( "const input=document.querySelector('dialog[open] [name=month]');input.value='2025-01';input.dispatchEvent(new Event('change',{bubbles:true}))");
  await wait(page, "!document.querySelector('[aria-busy=true]')");
  assert.ok(await evaluate(page, "!document.querySelector('dialog[open] [role=alert]')"));
  await screenshot(page, 'analytics-traffic-history'); await close();

  await click(policy.quota_enabled === false ? '设置配额' : '编辑配额', row);
  await wait(page, "!!document.querySelector('dialog[open] [name=limit_gb]')");
  const engines = await evaluate(page, "[...document.querySelectorAll('dialog[open] [name=engine] option')].map(option=>option.value)");
  const agent = (await api(page, identity, 'GET', '/agents')).find(value => value.id === agentID);
  assert.deepEqual(engines, agent.capabilities);
  await act( `window.__trafficFail=true;window.fetch=(url,options)=>window.__trafficFail&&String(url).endsWith(${JSON.stringify('/traffic-policies/' + policy.id)})&&options?.method==='PUT'?(window.__trafficFail=false,Promise.resolve(new Response(JSON.stringify({error:'配额保存测试失败'}),{status:503,headers:{'Content-Type':'application/json'}}))):window.__trafficFetch(url,options);const input=document.querySelector('dialog[open] [name=limit_gb]');input.value='2';input.dispatchEvent(new Event('input',{bubbles:true}));input.form.requestSubmit()`);
  await wait(page, "!document.querySelector('[aria-busy=true]') && [...document.querySelectorAll('dialog[open] [role=alert]')].some(item=>item.textContent.includes('配额保存测试失败'))");
  assert.equal(await evaluate(page, "document.querySelector('dialog[open] [name=limit_gb]').value"), '2', 'failed save retains draft');
  await act( "document.querySelector('dialog[open] [name=limit_gb]').form.requestSubmit()");
  await wait(page, `!document.querySelector('dialog[open],[aria-busy=true]') && !!document.querySelector(${JSON.stringify(row)})`);
  const saved = (await api(page, identity, 'GET', '/traffic-policies')).find(value => value.id === policy.id);
  assert.equal(saved.limit_bytes, 2 * 1024 ** 3);
  assert.ok(saved.is_current_user && saved.deployment_confirmed);
  assert.ok(await evaluate(page, `document.querySelector(${JSON.stringify(row + ' [role=meter]')})?.getAttribute('aria-valuetext').includes('2'+String.fromCharCode(160)+'GB')`), 'saved quota does not reach the usage meter');
  await manage('清零', '清零并解封');
  await wait(page, `!document.querySelector('dialog[open],[aria-busy=true]') && !!document.querySelector(${JSON.stringify(row)})`);
  assert.equal((await api(page, identity, 'GET', '/traffic-policies')).find(value => value.id === policy.id).used_bytes, 0);
  await manage('取消配额', '取消配额');
  await wait(page, `!document.querySelector('dialog[open],[aria-busy=true]') && document.querySelector(${JSON.stringify(row)})?.textContent.includes('未设置配额') && document.querySelector(${JSON.stringify(row)})?.textContent.includes('等待 Agent 同步')`).catch(async error => {
    await screenshot(page, 'analytics-traffic-quota-failure');
    const presentation = await evaluate(page, `({dialogs:[...document.querySelectorAll('dialog[open]')].map(box=>box.textContent),row:document.querySelector(${JSON.stringify(row)})?.textContent,busy:[...document.querySelectorAll('[aria-busy=true]')].map(control=>control.textContent),alerts:[...document.querySelectorAll('[role=alert],.a-toast')].map(item=>item.textContent)})`);
    const stored = (await api(page, identity, 'GET', '/traffic-policies')).find(value=>value.id===policy.id);
    throw new Error(`${error.message}; quota diagnostics: ${JSON.stringify({presentation,stored})}`);
  });
  assert.equal((await api(page, identity, 'GET', '/traffic-policies')).find(value => value.id === policy.id).quota_enabled, false);
  assert.ok(!(await api(page, identity, 'GET', '/traffic-policies')).find(value => value.id === policy.id).last_reported_at, 'reset must await a new Agent sample');
  assert.equal(await evaluate(page, `document.querySelector(${JSON.stringify(row + ' [role=meter]')})`), null, 'removed quota retains a numeric usage meter');
  await click('统计详情', row); await wait(page, "document.querySelector('dialog[open][data-traffic-details]')?.textContent.includes('持续统计') && !document.querySelector('[aria-busy=true]')"); await close();
  await click('设置配额', row);
  await wait(page, "!!document.querySelector('dialog[open] [name=limit_gb]')");
  await act( "const input=document.querySelector('dialog[open] [name=limit_gb]');input.value='1';input.form.requestSubmit()");
  await wait(page, `!document.querySelector('dialog[open],[aria-busy=true]') && document.querySelector(${JSON.stringify(row)})?.textContent.includes('编辑配额')`);
  await screenshot(page, 'analytics-traffic-operations');
  await click('添加配额', '.a-main');
  await wait(page, "!!document.querySelector('dialog[open] [name=agent_id]')");
  await act(`window.__createdTraffic=null;window.fetch=async(url,options)=>{const response=await window.__trafficFetch(url,options);if(String(url).endsWith('/traffic-policies')&&options?.method==='POST'&&response.ok)window.__createdTraffic=await response.clone().json();return response};const form=document.querySelector('dialog[open] [name=agent_id]').form;form.elements.agent_id.value=${JSON.stringify(agentID)};form.elements.agent_id.dispatchEvent(new Event('change',{bubbles:true}));form.elements.name.value='待部署端口';form.elements.port.value='23997';form.elements.limit_gb.value='1';form.requestSubmit()`);
  await wait(page, "!!window.__createdTraffic && !document.querySelector('dialog[open],[aria-busy=true]')");
  const created = await evaluate(page, 'window.__createdTraffic');
  assert.equal(created.agent_id, agentID);
  assert.equal(created.port, 23997);
  assert.ok(!(await api(page, identity, 'GET', '/traffic-policies')).some(value=>value.id===created.id), 'undeployed policy is never presented as an active listener');
  await api(page, identity, 'DELETE', `/traffic-policies/${created.id}/monitoring`, undefined, 204);
  await act('window.fetch=window.__trafficFetch;delete window.__createdTraffic');


  await manage('删除', '删除流量记录');
  await wait(page, `!document.querySelector('dialog[open],[aria-busy=true]') && !document.querySelector(${JSON.stringify(row)})`);
  assert.ok(!(await api(page, identity, 'GET', '/traffic-policies')).some(value => value.id === policy.id && value.monitoring_enabled));
  await click('管理', '.a-main');
  await click('同步配置端口', '.a-main');
  await wait(page, "document.querySelector('dialog[open] [name=select-all]')?.disabled===false");
  await act( "document.querySelector('dialog[open] [name=select-all]').click()");
  assert.ok(await evaluate(page, "[...document.querySelectorAll('dialog[open] input[name^=\"port:\"]:not(:disabled)')].every(input=>input.checked)"));
  await act( "document.querySelector('dialog[open] input[name^=\"port:\"]:checked').click()");
  assert.ok(await evaluate(page, "!document.querySelector('dialog[open] [name=select-all]').checked"));
  await click('重新扫描');
  await wait(page, "document.querySelector('dialog[open] [name=select-all]')?.disabled===false");
  assert.equal(await evaluate(page, "document.querySelectorAll('dialog[open] input[name^=\"port:\"]:checked').length"), 0);
  await act( `window.__trafficConflict=true;window.fetch=(url,options)=>window.__trafficConflict&&String(url).endsWith('/traffic-endpoints/sync')&&options?.method==='POST'?(window.__trafficConflict=false,Promise.resolve(new Response(JSON.stringify({error:'snapshot changed'}),{status:409,headers:{'Content-Type':'application/json'}}))):window.__trafficFetch(url,options);document.querySelector('dialog[open] [name=select-all]').click();document.querySelector('dialog[open] form').requestSubmit()`);
  await wait(page, "!document.querySelector('[aria-busy=true]') && document.querySelector('dialog[open]').textContent.includes('候选已失效')");
  assert.ok(await evaluate(page, "document.querySelector('dialog[open] button[type=submit]').disabled"));
  await click('重新扫描');
  await wait(page, "document.querySelector('dialog[open] [name=select-all]')?.disabled===false");
  await act( "document.querySelector('dialog[open] [name=select-all]').click();document.querySelector('dialog[open] form').requestSubmit()");
  await wait(page, `!document.querySelector('dialog[open],[aria-busy=true]') && !!document.querySelector(${JSON.stringify(row)})`);
  await screenshot(page, 'analytics-traffic-synchronized');
  await click('批量操作', '.a-main');
  await wait(page, "!!document.querySelector('dialog[open] [name=action]')");
  await act(`window.__trafficBatchBodies=[];window.__trafficLoseReceipt=true;window.fetch=async(url,options)=>{if(String(url).endsWith('/traffic-policies/batch')&&options?.method==='POST'){window.__trafficBatchBodies.push(options.body);const response=await window.__trafficFetch(url,options);if(window.__trafficLoseReceipt&&response.ok){await response.clone().text();window.__trafficLoseReceipt=false;throw new TypeError('test response lost')}return response}return window.__trafficFetch(url,options)};const form=document.querySelector('dialog[open] [name=action]').form;form.elements.action.value='quota';form.elements.action.dispatchEvent(new Event('change',{bubbles:true}));form.elements.namedItem(${JSON.stringify(policy.id)}).click();form.elements.namedItem('patch:limit_bytes').click();form.elements.limit_gb.value='3';form.requestSubmit()`);
  await wait(page, "[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent==='设置配额')");
  await click('设置配额');
  await wait(page, "!document.querySelector('[aria-busy=true]') && window.__trafficBatchBodies.length===1 && [...document.querySelectorAll('dialog[open] [role=alert]')].some(item=>item.textContent) && [...document.querySelectorAll('dialog[open] button')].some(button=>button.textContent==='重试未完成项'&&!button.hidden)").catch(async error => { throw new Error(`${error.message}; batch diagnostics: ${JSON.stringify(await evaluate(page, "({text:[...document.querySelectorAll('dialog[open]')].map(box=>box.textContent),requests:window.__trafficBatchBodies?.length})"))}`); });
  assert.equal((await api(page, identity, 'GET', '/traffic-policies')).find(value=>value.id===policy.id).limit_bytes, 3*1024**3, 'lost response does not undo the committed mutation');
  await click('重试未完成项');
  await wait(page, "!document.querySelector('[aria-busy=true]') && !!document.querySelector('dialog[open] tbody') && document.querySelector('dialog[open]').textContent.includes('成功')");
  const receipts = await evaluate(page, 'window.__trafficBatchBodies');
  assert.equal(receipts.length, 2);
  assert.equal(receipts[0], receipts[1], 'uncertain mutation replays the identical server receipt and payload');
  assert.equal(await evaluate(page, `document.querySelector('dialog[open] [name="${policy.id}"]').checked`), false, 'completed selections are cleared after receipt recovery');
  await close();
  await act('window.fetch=window.__trafficFetch;delete window.__trafficBatchBodies;delete window.__trafficLoseReceipt');

  await act( `const input=document.querySelector('.a-traffic-filters [name=agent]');input.value=${JSON.stringify(agentID)};input.dispatchEvent(new Event('change',{bubbles:true}));location.hash='#tasks'`);
  await wait(page, "document.querySelector('.a-top h1')?.textContent==='任务' && !document.querySelector('[aria-busy=true]')");
  await navigate('#traffic');
  assert.equal(await evaluate(page, "document.querySelector('.a-traffic-filters [name=agent]').value"), agentID, 'filters survive remount in the same account state');
  await click('重置', '.a-filterbar');
  await act( 'window.fetch=window.__trafficFetch;delete window.__trafficFetch');
  record(`${mode}: traffic immediate filters/reset, route/account filter state, polling/focus preservation, supported engines, quota failure/retry and authoritative readback, reset, remove/reinstate quota, daily history, delete monitoring, sync all/partial/rescan/conflict recovery and authoritative list refresh passed`);
}
