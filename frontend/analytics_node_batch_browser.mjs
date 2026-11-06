import assert from 'node:assert/strict';

export async function checkAnalyticsNodeBatch({ page, mode, fixture, evaluate, wait, screenshot, record }) {
  await evaluate(page, "location.hash='#node-settings'");
  await wait(page, "!!document.querySelector('.a-probe-toolbar') && !document.querySelector('[aria-busy=true]')");
  const click = (label, selector = '.a-main') => evaluate(page, `[...document.querySelectorAll(${JSON.stringify(selector + ' button')})].find(button=>button.textContent===${JSON.stringify(label)}).click()`);
  await click('批量操作');
  await evaluate(page, `(() => {const input=document.querySelector('[name=${JSON.stringify('select:' + fixture.analyticsAgent)}]');input.checked=true;input.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  await click('执行批量操作');
  await wait(page, "!!document.querySelector('dialog[open] [name=action]')");
  assert.ok(await evaluate(page, "['install','upgrade-agent','status','start','restart','stop'].every(value=>document.querySelector('dialog[open] [name=action] option[value=\"'+value+'\"]'))"), 'all original batch actions are exposed');
  await evaluate(page, `window.__batchOriginalFetch=window.fetch;window.__batchBodies=[];window.__batchFailures=1;window.fetch=(url,options)=>{if(String(url).endsWith('/api/v1/tasks')&&options?.method==='POST'){window.__batchBodies.push(JSON.parse(options.body));if(window.__batchFailures-->0)return Promise.resolve(new Response(JSON.stringify({error:'批量提交暂时失败'}),{status:503,headers:{'Content-Type':'application/json'}}));}return window.__batchOriginalFetch(url,options);}`);
  try {
    await evaluate(page, "document.querySelector('dialog[open] [name=action]').form.requestSubmit()");
    await wait(page, "document.querySelectorAll('dialog[open]').length===2");
    await click('执行批量操作', 'dialog[open]:last-of-type');
    await wait(page, "!document.querySelector('[aria-busy=true]') && [...document.querySelectorAll('dialog[open] button')].some(button=>button.textContent==='重试提交'&&!button.hidden)");
    assert.ok(await evaluate(page, "document.querySelector('dialog[open]').textContent.includes('批量提交暂时失败')"));
    await screenshot(page, 'analytics-node-batch-failure');
    await click('重试提交', 'dialog[open]');
    await wait(page, "!document.querySelector('[aria-busy=true]') && document.querySelector('dialog[open]').textContent.includes('已提交 1')");
    const bodies = await evaluate(page, 'window.__batchBodies');
    assert.equal(bodies.length, 2);
    assert.deepEqual(bodies[1], bodies[0], 'retry submits the original captured payload through the actual API');
    assert.equal(bodies[0].agent_id, fixture.analyticsAgent);
    assert.equal(bodies[0].action, 'status');
    await evaluate(page, `(() => {const form=document.querySelector('dialog[open] form');form.elements.action.value='install';form.elements.action.dispatchEvent(new Event('change',{bubbles:true}));form.elements.release_channel.value='development';form.elements.release_channel.dispatchEvent(new Event('change',{bubbles:true}));form.elements.core_source.value='mirror';window.__batchFailures=2;form.requestSubmit()})()`);
    await wait(page, "document.querySelectorAll('dialog[open]').length===2");
    await click('执行批量操作', 'dialog[open]:last-of-type');
    await wait(page, "!document.querySelector('[aria-busy=true]') && window.__batchBodies.length===3");
    await evaluate(page, "document.querySelector('dialog[open] [name=release_channel]').value='stable'");
    await click('重试提交', 'dialog[open]');
    await wait(page, "!document.querySelector('[aria-busy=true]') && window.__batchBodies.length===4");
    const installs = await evaluate(page, 'window.__batchBodies.slice(2)');
    assert.deepEqual(installs[1], installs[0], 'retry retains the submitted channel/source after form changes');
    assert.equal(installs[0].core_version, 'development');
    assert.equal(installs[0].core_source, 'mirror');
    assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"));
    record(`${mode}: node batch original actions, real status API submission, failed-request retry, and captured install version/source across form changes`);
  } finally {
    await evaluate(page, "window.fetch=window.__batchOriginalFetch;document.querySelectorAll('dialog[open]').forEach(box=>box.close())");
  }
}
