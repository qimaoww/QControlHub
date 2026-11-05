import { checkAnalyticsTraffic } from './analytics_traffic_browser.mjs';
import assert from 'node:assert/strict';
export async function checkAnalyticsWorkflows({ page, mode, fixture, evaluate, wait, screenshot, api, identity, record, call }) {
  await checkAnalyticsTraffic({ page, mode, fixture, evaluate, wait, screenshot, api, identity, record });
  const agentID=fixture.analyticsAgent;
  const navigate=async hash=>{await evaluate(page,`location.hash=${JSON.stringify(hash)}`);await wait(page,`location.hash===${JSON.stringify(hash)} && document.querySelector('.a-top h1')?.textContent===${JSON.stringify(hash.startsWith('#live-config')?'实时配置':hash==='#node-settings'?'节点':hash==='#client-access'?'客户端接入':hash==='#traffic'?'流量':'配置档案')} && !document.querySelector('[aria-busy=true]')`);};
  const click=async(label,selector='dialog[open]')=>evaluate(page,`[...document.querySelectorAll(${JSON.stringify(selector+' button')})].find(button=>!button.closest('select')&&button.textContent===${JSON.stringify(label)}).click()`);
  const close=async()=>{await evaluate(page,"document.querySelectorAll('dialog[open]').forEach(box=>box.close())");try { await wait(page,"!document.querySelector('dialog[open],.a-motion-tail')"); }
    catch(error) { throw new Error(`${error.message}; dialog diagnostics: ${JSON.stringify(await evaluate(page, "({dialogs:[...document.querySelectorAll('dialog[open] h2')].map(el=>el.textContent),tails:document.querySelectorAll('.a-motion-tail').length,animations:document.getAnimations().map(animation=>({id:animation.id,state:animation.playState}))})"))}`); }
  };
  await navigate('#node-settings');
  assert.equal(await evaluate(page,"document.querySelectorAll('.a-main input[type=checkbox]').length"),0,'initial node monitor has no selection boxes');
  assert.ok(await evaluate(page,"!!document.querySelector('.a-probe-grid')"));
  const {targetInfo} = await call('Target.getTargetInfo', {}, page);
  await call('Browser.grantPermissions', {origin:new URL(await evaluate(page,'location.href')).origin,
    browserContextId:targetInfo.browserContextId, permissions:['clipboardReadWrite','clipboardSanitizedWrite']});
  const checkAddresses = async () => {
    const agents = await api(page, identity, 'GET', '/agents');
    const agent = agents.find(item => item.id === agentID);
    assert.equal(agent.region_code, 'JP');
    assert.ok(await evaluate(page, `(() => {
      const identity=[...document.querySelectorAll('.a-probe,.a-probe-identity')].find(item=>item.querySelector('.a-probe-flag[data-region-code="JP"]'));
      return identity && identity.querySelector('[data-ip-family=v4] [dir]').textContent===${JSON.stringify(agent.metrics.public_ipv4)}
        && identity.querySelector('[data-ip-family=v6] [dir]').textContent===${JSON.stringify(agent.metrics.public_ipv6)}
        && identity.querySelector('.a-probe-flag').getAttribute('aria-label')==='日本'
        && identity.querySelector('[data-ip-stack=dual]').querySelectorAll('.a-ip-copy').length===2
        && (()=>{const rows=[...identity.querySelectorAll('.a-probe-address')];const copies=rows.map(row=>row.querySelector('button').getBoundingClientRect());return Math.abs(copies[0].right-copies[1].right)<1 && rows.every(row=>row.scrollWidth<=row.clientWidth+1)})()
        && document.documentElement.scrollWidth<=innerWidth+1;
    })()`), 'card/list display both real API address families and the resolved region');
    for (const [family, value] of [['v4',agent.metrics.public_ipv4],['v6',agent.metrics.public_ipv6]]) {
      const selector=`.a-probe-flag[data-region-code="JP"]`;
      await evaluate(page, `(() => {
        const identity=[...document.querySelectorAll('.a-probe,.a-probe-identity')].find(item=>item.querySelector(${JSON.stringify(selector)}));
        window.__ipCopyControl=identity.querySelector('[data-ip-family=${family}] button');
        window.__ipCopyControl.click();
      })()`);
      await wait(page,"!window.__ipCopyControl.disabled && document.querySelector('.a-toast-region').textContent.includes('已复制')");
      const clipboard = await call('Runtime.evaluate', {expression:'navigator.clipboard.readText()',awaitPromise:true,returnByValue:true,userGesture:true},page);
      assert.equal(clipboard.result.value,value,`${mode}: ${family} copy writes the exact API address to the real clipboard`);
      assert.ok(await evaluate(page,"window.__ipCopyControl.getAttribute('aria-label').includes('复制') && !window.__ipCopyControl.textContent.includes('√') && !!window.__ipCopyControl.querySelector('svg')"));
    }
  };
  const captureAddresses = async label => {
    await evaluate(page, `(() => {const input=document.querySelector('.a-probe-toolbar [name=search]');input.value=${JSON.stringify(agentID)};input.dispatchEvent(new Event('input',{bubbles:true}));document.querySelectorAll('.a-toast [aria-label=关闭通知]').forEach(button=>button.click());})()`);
    await wait(page, "document.querySelectorAll('[data-ip-stack=dual]').length===1 && !document.querySelector('.a-toast,.a-motion-tail') && [...document.querySelectorAll('.a-probe-flag img')].every(image=>image.complete)");
    await screenshot(page,label);
    await evaluate(page, "(() => {const input=document.querySelector('.a-probe-toolbar [name=search]');input.value='';input.dispatchEvent(new Event('input',{bubbles:true}));})()");
  };
  await checkAddresses();
  const regionTrigger = `.a-probe[data-agent-id="${agentID}"] .a-region-trigger`;
  await evaluate(page, `document.querySelector(${JSON.stringify(regionTrigger)}).click()`);
  await wait(page, "!!document.querySelector('dialog[open] [data-region-choice=JP]') && !document.querySelector('dialog[open] button[type=submit]').disabled");
  assert.ok(await evaluate(page, "[...document.querySelectorAll('dialog[open] .a-region-options button')].every(button=>!button.textContent.includes('√')&&!button.textContent.includes('✓'))"));
  assert.ok(await evaluate(page, "getComputedStyle(document.querySelector('dialog[open] [data-region-choice=JP] .a-probe-flag'),'::after').borderTopWidth==='1px'"), 'white flag fields keep a visible frame');
  await screenshot(page,'analytics-region-picker');
  await evaluate(page, "(() => {const input=document.querySelector('dialog[open] [name=region_search]');input.value='Germany';input.dispatchEvent(new Event('input',{bubbles:true}));})()");
  assert.equal(await evaluate(page, "document.querySelectorAll('dialog[open] [data-region-choice]').length"),1);
  await evaluate(page, "document.querySelector('dialog[open] [data-region-choice=DE]').click()");
  await evaluate(page, `window.__regionFetch=window.fetch;window.__regionFail=true;window.fetch=(url,options)=>window.__regionFail&&String(url).endsWith(${JSON.stringify('/agents/'+agentID+'/region')})&&options?.method==='PUT'?(window.__regionFail=false,Promise.resolve(new Response(JSON.stringify({error:'地区保存测试失败'}),{status:503,headers:{'Content-Type':'application/json'}}))):window.__regionFetch(url,options);document.querySelector('dialog[open] [name=region_search]').form.requestSubmit()`);
  await wait(page, "!document.querySelector('[aria-busy=true]') && [...document.querySelectorAll('dialog[open] [role=alert]')].some(item=>item.textContent.includes('地区保存测试失败'))");
  assert.equal((await api(page,identity,'GET','/agents')).find(agent=>agent.id===agentID).labels.region_code,'JP','failed manual region save preserves confirmed server value');
  assert.ok(await evaluate(page, "document.querySelector('dialog[open] [data-region-choice=DE]').getAttribute('aria-pressed')==='true'"));
  await evaluate(page, "document.querySelector('dialog[open] [name=region_search]').form.requestSubmit()");
  await wait(page, `!document.querySelector('dialog[open],[aria-busy=true]') && !!document.querySelector('.a-probe[data-agent-id="${agentID}"] .a-probe-flag[data-region-code=DE]')`);
  assert.equal((await api(page,identity,'GET','/agents')).find(agent=>agent.id===agentID).labels.region_code,'DE');
  await navigate('#traffic');await navigate('#node-settings');
  assert.ok(await evaluate(page, `!!document.querySelector('.a-probe[data-agent-id="${agentID}"] .a-probe-flag[data-region-code=DE]')`),'manual region survives remount');
  await evaluate(page, `document.querySelector(${JSON.stringify(regionTrigger)}).click()`);
  await wait(page, "!!document.querySelector('dialog[open] [data-region-choice=JP]') && !document.querySelector('dialog[open] button[type=submit]').disabled");
  await click('自动识别');
  await evaluate(page, "document.querySelector('dialog[open] [name=region_search]').form.requestSubmit()");
  await wait(page, "!document.querySelector('dialog[open],[aria-busy=true]')");
  assert.ok(!(await api(page,identity,'GET','/agents')).find(agent=>agent.id===agentID).labels?.region_code,'automatic mode clears only manual override');
  await api(page,identity,'PUT',`/agents/${agentID}/region`,{country_code:'JP'});
  await navigate('#traffic');await navigate('#node-settings');
  await evaluate(page, "window.fetch=window.__regionFetch;delete window.__regionFetch;delete window.__regionFail");
  await captureAddresses('analytics-dual-stack-cards');await click('列表','.a-main');
  await wait(page,"!!document.querySelector('.a-probe-list') && !document.querySelector('[aria-busy=true]')");
  await checkAddresses();
  await captureAddresses('analytics-dual-stack-list');
  record(`${mode}: dual-stack cards/list retain both API addresses, align independent copy controls, wrap long IPv6 and write exact IPv4/IPv6 to the real clipboard`);
  await evaluate(page, `(() => { const image=document.querySelector('.a-probe-flag[data-region-code="JP"] img'); if(image) image.dispatchEvent(new Event('error')); })()`);
  assert.equal(await evaluate(page,"document.querySelector('.a-probe-flag[data-region-code=JP]').textContent"),'JP','flag failure keeps a readable region fallback');
  assert.ok(await evaluate(page,"!document.querySelector('.a-main input[type=checkbox], .a-probe-summary, .a-main h1')"));await screenshot(page,'analytics-probe-list');
  assert.equal((await api(page,identity,'GET','/preferences/node-view')).view,'list');
  await navigate('#traffic');await navigate('#node-settings');
  assert.ok(await evaluate(page,"!!document.querySelector('.a-probe-list')"),'account layout restored after route remount');
  await evaluate(page,`window.__viewFetch=window.fetch;window.fetch=(url,options)=>String(url).includes('/preferences/node-view')&&options?.method==='PUT'?Promise.resolve(new Response(JSON.stringify({error:'布局保存测试失败'}),{status:503,headers:{'Content-Type':'application/json'}})):window.__viewFetch(url,options)`);
  await click('卡片','.a-main');await wait(page,"!document.querySelector('[aria-busy=true]') && document.querySelector('.a-toast.error')?.textContent");
  assert.ok(await evaluate(page,"!!document.querySelector('.a-probe-list')"),'failed save retains confirmed layout');
  assert.equal((await api(page,identity,'GET','/preferences/node-view')).view,'list');
  await evaluate(page,"window.fetch=window.__viewFetch;delete window.__viewFetch");
  const preference=await api(page,identity,'GET','/preferences/node-view');
  await api(page,identity,'PUT','/preferences/node-view',{view:'cards',revision:preference.revision});
  await click('卡片','.a-main');await wait(page,"!!document.querySelector('.a-probe-grid') && !document.querySelector('[aria-busy=true]')");
  assert.equal((await api(page,identity,'GET','/preferences/node-view')).revision,preference.revision+1,'stale UI write cannot overwrite concurrent account preference');
  await click('列表','.a-main');await wait(page,"!!document.querySelector('.a-probe-list') && !document.querySelector('[aria-busy=true]')");
  await click('卡片','.a-main');await wait(page,"!!document.querySelector('.a-probe-grid') && !document.querySelector('[aria-busy=true]')");
  await click('批量操作','.a-main');
  assert.ok(await evaluate(page,"!!document.querySelector('.a-probe input[type=checkbox]')"));
  await evaluate(page,"document.querySelector('.a-probe-batch [name=select-all]').click()");
  assert.ok(await evaluate(page,"[...document.querySelectorAll('.a-probe input[type=checkbox]:not(:disabled)')].every(input=>input.checked)"));
  await evaluate(page,"document.querySelector('.a-probe input[type=checkbox]:checked').click()");
  assert.ok(await evaluate(page,"const all=document.querySelector('.a-probe-batch [name=select-all]');!all.checked && (document.querySelectorAll('.a-probe input[type=checkbox]:not(:disabled)').length<2 || all.indeterminate)"));
  await screenshot(page,'analytics-batch-partial-selection');
  await evaluate(page,"document.querySelector('.a-probe-batch [name=select-all]').click();document.querySelector('.a-probe-batch [name=select-all]').click()");
  assert.equal(await evaluate(page,"document.querySelectorAll('.a-probe input[type=checkbox]:checked').length"),0);
  await click('取消选择','.a-main');
  assert.equal(await evaluate(page,"document.querySelectorAll('.a-main input[type=checkbox]').length"),0);
  await click('添加节点','.a-main');
  await wait(page,"!!document.querySelector('dialog[open] [name=name]') && !document.querySelector('[aria-busy=true]')");await screenshot(page,'analytics-enrollment');
  await evaluate(page,"(()=>{const form=document.querySelector('dialog[open] form');form.elements.name.value='独立主题添加验证';form.requestSubmit()})()");
  await wait(page,"[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent==='QAgent 安装命令') && !document.querySelector('[aria-busy=true]')");
  const token=(await api(page,identity,'GET','/enrollment-tokens')).find(token=>token.name==='独立主题添加验证');assert.ok(token?.id);
  assert.ok(await evaluate(page,"[...document.querySelectorAll('dialog[open] pre')].some(item=>item.textContent.includes('/install-agent.sh')&&item.textContent.includes('X-QControlHub-Enrollment'))"));
  await close();await api(page,identity,'DELETE',`/enrollment-tokens/${token.id}`,undefined,204);
  await evaluate(page,`document.querySelector(${JSON.stringify('[data-agent-id="'+agentID+'"]')}).querySelectorAll('footer button')[0].click()`);
  await wait(page,"!!document.querySelector('dialog[open] [name=name]')");
  await evaluate(page,"(()=>{const form=document.querySelector('dialog[open] [name=name]').form;form.elements.name.value='主题工作流节点';form.requestSubmit()})()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && document.querySelector('.a-main').textContent.includes('主题工作流节点')");
  assert.equal((await api(page,identity,'GET','/agents')).find(agent=>agent.id===agentID).name,'主题工作流节点');
  await wait(page,"!!document.querySelector('.a-toast') && !document.getAnimations().some(animation=>animation.id==='analytics-feedback')");
  assert.ok(await evaluate(page,"(()=>{const region=document.querySelector('.a-toast-region'),toast=region.querySelector('.a-toast'),box=toast.getBoundingClientRect(),control=toast.querySelector('button'),point=control.getBoundingClientRect();return region.matches(':popover-open')&&region.closest('dialog')?.open&&box.right<=innerWidth&&box.top>=0&&document.elementFromPoint(point.left+point.width/2,point.top+point.height/2)?.closest('.a-toast')===toast&&!document.querySelector('.a-main > .a-notice')&&!toast.textContent.includes('√')})()"),'notifications stay above the real modal, right aligned, interactive and without a checkmark');
  await screenshot(page,'analytics-notification-modal');
  await evaluate(page,"document.querySelector('.a-toast [aria-label=关闭通知]').click()");

  await screenshot(page,'analytics-node-settings');await click('查看指标历史');await wait(page,"[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent.includes('指标历史')) && !document.querySelector('[aria-busy=true]')");await close();
  await evaluate(page,`document.querySelector(${JSON.stringify('[data-agent-id="'+agentID+'"]')}).querySelectorAll('footer button')[1].click()`);
  await wait(page,"[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent.includes('共享 Agent')) && !document.querySelector('[aria-busy=true]')");await click('添加用户');
  await evaluate(page,"(()=>{const form=document.querySelector('dialog[open] form');form.querySelector('[name=username]').value='alice';form.querySelector('[name=ports]').value='23450-23451';form.querySelector('[name=limit_gib]').value='1';form.querySelector('[name=engines]').checked=true;form.requestSubmit()})()");
  await wait(page,"document.querySelector('.a-toast-region')?.textContent.includes('共享设置已保存') && !document.querySelector('[aria-busy=true]')");
  const sharing=await api(page,identity,'GET',`/agents/${agentID}/sharing`);assert.deepEqual(sharing.shares[0].ports,[23450,23451]);assert.equal(sharing.shares[0].status,'pending');
  await screenshot(page,'analytics-node-sharing');await close();await api(page,identity,'PUT',`/agents/${agentID}/sharing`,{revision:sharing.revision,shares:[]});
  await navigate(`#live-config?agent=${agentID}&engine=mihomo`);await click('添加入站','.a-main');
  await wait(page,"!!document.querySelector('dialog[open] [name=tag]') && !document.querySelector('[aria-busy=true]')");await screenshot(page,'analytics-inbound-editor');
  await evaluate(page,"(()=>{const form=document.querySelector('dialog[open] [name=tag]').form;form.elements.tag.value='theme-inbound';form.elements.port.value='23477';form.requestSubmit()})()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && document.querySelector('.a-main').textContent.includes('theme-inbound')");
  const workspace=await api(page,identity,'GET',`/agents/${agentID}/configs/mihomo/workspace`);assert.ok(workspace.inbounds.some(input=>input.tag==='theme-inbound'&&input.port===23477));assert.ok(workspace.config?.version);await close();
  for(const task of (await api(page,identity,'GET',`/tasks?agent_id=${agentID}&limit=100`)).filter(task=>['pending','running'].includes(task.status)))await api(page,identity,'DELETE',`/tasks/${task.id}`,undefined,204);
  await click('展开源码','.a-main');await wait(page,"[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent==='展开源码')");await screenshot(page,'analytics-config-files');
  await api(page,identity,'PUT',`/agents/${agentID}/configs/mihomo`,{name:workspace.config.name,description:workspace.config.description,content:workspace.config.content,version:workspace.config.version});
  await evaluate(page,"document.querySelector('dialog[open] [name=content]').form.requestSubmit()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && [...document.querySelectorAll('dialog[open] [role=alert]')].some(item=>item.textContent)");
  assert.ok(await evaluate(page,"!!document.querySelector('dialog[open] [name=content]')?.value"),'a stale save preserves the editor and draft');
  assert.equal((await api(page,identity,'GET',`/agents/${agentID}/configs/mihomo/workspace`)).config.version,workspace.config.version+1);
  await close();await click('展开源码','.a-main');await wait(page,"!!document.querySelector('dialog[open] [name=content]') && !document.querySelector('[aria-busy=true]')");
  await evaluate(page,"document.querySelector('dialog[open] [name=content]').form.requestSubmit()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && ![...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent==='展开源码')");
  assert.equal((await api(page,identity,'GET',`/agents/${agentID}/configs/mihomo/workspace`)).config.version,workspace.config.version+2);
  // Exercise the saved target state separately from actual deployment: queued
  // validation must not claim a listener was stopped on the remote Agent.
  const cancelPending = async () => {
    for (const task of (await api(page,identity,'GET',`/tasks?agent_id=${agentID}&limit=100`)).filter(task=>['pending','running'].includes(task.status))) await api(page,identity,'DELETE',`/tasks/${task.id}`,undefined,204);
  };
  await cancelPending();
  for (const [enabled,label] of [[false,'停用'],[true,'启用']]) {
    await evaluate(page,`(()=>{const row=[...document.querySelectorAll('.a-main tr')].find(row=>row.textContent.includes('theme-inbound'));const button=[...row.querySelectorAll('button')].find(button=>button.textContent.trim()===${JSON.stringify(label)});if(!button)throw new Error('port state action missing');button.click()})()`);
    await wait(page,"!!document.querySelector('dialog[open] [name=intent]') && !document.querySelector('[aria-busy=true]')");
    await evaluate(page,"document.querySelector('dialog[open] [name=intent]').form.requestSubmit()");
    await wait(page,"!document.querySelector('[aria-busy=true]') && ![...document.querySelectorAll('dialog[open] h2')].some(title=>/配置对象$/.test(title.textContent))");
    const updated=await api(page,identity,'GET',`/agents/${agentID}/configs/mihomo/workspace`);
    const object=updated.managed_objects.find(item=>item.tag==='theme-inbound');
    assert.equal(object.enabled,enabled);
    assert.equal(updated.config.content,workspace.config.content,'target state must retain complete source');
    assert.ok(updated.management.disabled.some(item=>item.tag==='theme-inbound')===!enabled);
    await close();
    await evaluate(page,"document.querySelectorAll('.a-toast [aria-label=关闭通知]').forEach(button=>button.click())");
    await wait(page,"!document.querySelector('.a-motion-tail') && !document.getAnimations().some(animation=>animation.id?.startsWith('analytics-'))");
    assert.ok(await evaluate(page,"document.documentElement.scrollWidth<=innerWidth+1 && [...document.querySelectorAll('.a-port-list tbody td:first-child')].every(cell=>getComputedStyle(cell).whiteSpace==='nowrap')"));
    await screenshot(page,enabled?'analytics-port-enabled-target':'analytics-port-disabled-target');
    await cancelPending();
  }
  await click('添加出站','.a-main');
  await wait(page,"!!document.querySelector('dialog[open] [name=fragment]')");
  await evaluate(page,"(()=>{const input=document.querySelector('dialog[open] [name=fragment]');input.value='name: unused-exit\\ntype: http\\nserver: 127.0.0.1\\nport: 443\\n';input.form.requestSubmit()})()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && document.querySelector('.a-main').textContent.includes('unused-exit')");
  await close();await cancelPending();
  const exitWorkspace=await api(page,identity,'GET',`/agents/${agentID}/configs/mihomo/workspace`);
  assert.ok(exitWorkspace.managed_objects.some(item=>!item.inbound&&item.tag==='unused-exit'));
  await evaluate(page,"(()=>{const row=[...document.querySelectorAll('.a-main tr')].find(row=>row.textContent.includes('unused-exit'));[...row.querySelectorAll('button')].find(button=>button.textContent==='删除').click()})()");
  await wait(page,"[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent==='删除出站')");
  await evaluate(page,"document.querySelector('dialog[open] [name=intent]').form.requestSubmit()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && !document.querySelector('.a-main').textContent.includes('unused-exit')");
  await close();await cancelPending();
  const removedExit=await api(page,identity,'GET',`/agents/${agentID}/configs/mihomo/workspace`);
  assert.ok(removedExit.inbounds.some(item=>item.tag==='theme-inbound'&&item.port===23477));
  assert.ok(!removedExit.managed_objects.some(item=>item.tag==='unused-exit'));
  await navigate('#client-access');
  await evaluate(page,`(()=>{const select=document.querySelector('.a-filterbar [name=agent]');select.value=${JSON.stringify(agentID)};select.dispatchEvent(new Event('change',{bubbles:true}))})()`);
  await click('修改显示参数','.a-main');
  await wait(page,"!!document.querySelector('dialog[open] [name=name]')");
  await evaluate(page,"(()=>{const form=document.querySelector('dialog[open] [name=name]').form;form.elements.name.value='主题接入名称';form.requestSubmit()})()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && document.querySelector('.a-main').textContent.includes('主题接入名称')");
  assert.ok((await api(page,identity,'GET','/client-access')).find(entry=>entry.agent_id===agentID).profiles.some(item=>item.client_name==='主题接入名称'));
  await click('参数详情','.a-main');await wait(page,"[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent.includes('参数详情'))");await screenshot(page,'analytics-client-details');await close();
  const policies=(await api(page,identity,'GET','/traffic-policies')).filter(policy=>policy.agent_id===agentID);assert.ok(policies.length);
  await navigate('#traffic');await click('批量操作','.a-main');
  await wait(page,"[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent==='流量批量操作')");
  await evaluate(page,"document.querySelector('dialog[open] [name=select-all]').click()");
  assert.ok(await evaluate(page,"(() => { const inputs=[...document.querySelectorAll('dialog[open] input[type=checkbox]')].filter(input=>input.name!=='select-all'&&!input.name.startsWith('patch:'));return inputs.length>0&&inputs.every(input=>input.checked); })()"));
  await evaluate(page,"document.querySelector('dialog[open] [name=select-all]').click()");
  await evaluate(page,`(()=>{const choice=document.querySelector('dialog[open] [name="${policies[0].id}"]');choice.checked=true;choice.dispatchEvent(new Event('change',{bubbles:true}));choice.form.requestSubmit()})()`);
  await wait(page,"[...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent==='清零当前周期')");await click('清零当前周期');
  await wait(page,"!document.querySelector('[aria-busy=true]') && (!!document.querySelector('dialog[open] tbody tr') || [...document.querySelectorAll('dialog[open] [role=alert]')].some(item=>item.textContent))");
  assert.ok(await evaluate(page,"!!document.querySelector('dialog[open] tbody tr') && document.querySelector('dialog[open]').textContent.includes('成功')"),await evaluate(page,"document.querySelector('dialog[open]')?.textContent"));
  await screenshot(page,'analytics-traffic-batch');await close();
  await navigate(`#live-config?agent=${fixture.analyticsXray}&engine=xray`);await click('出站','.a-main');await click('添加并绑定');
  await wait(page,"!!document.querySelector('dialog[open] [name=fragment]') && !document.querySelector('[aria-busy=true]')");
  await evaluate(page,"(()=>{const input=document.querySelector('dialog[open] [name=protocol]');input.form.requestSubmit()})()");
  await wait(page,"document.querySelector('dialog[open] [name=fragment]')?.value.includes('freedom') && !document.querySelector('[aria-busy=true]')");
  await evaluate(page,"document.querySelector('dialog[open] [name=fragment]').form.requestSubmit()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && [...document.querySelectorAll('dialog[open] h2')].some(item=>item.textContent==='任务详情')");await close();
  const xray=await api(page,identity,'GET',`/agents/${fixture.analyticsXray}/configs/xray/workspace`);
  const document=JSON.parse(xray.config.content);assert.ok(document.outbounds.some(item=>item.tag==='exit-23479'));assert.ok(document.routing.rules.some(item=>item.inboundTag?.includes('theme-xray')&&item.outboundTag==='exit-23479'));
  for(const task of (await api(page,identity,'GET',`/tasks?agent_id=${fixture.analyticsXray}&limit=100`)).filter(task=>['pending','running'].includes(task.status)))await api(page,identity,'DELETE',`/tasks/${task.id}`,undefined,204);
  await click('展开源码','.a-main');await wait(page,"!!document.querySelector('dialog[open] [name=content]') && !document.querySelector('[aria-busy=true]')");
  assert.ok(await evaluate(page,"!document.querySelector('dialog[open] [name=file]') && document.querySelector('dialog[open] [name=content]').value.includes('inbounds') && document.querySelector('dialog[open] [name=content]').value.includes('outbounds')"));await screenshot(page,'analytics-xray-files');
  await evaluate(page,"document.querySelector('dialog[open] [name=content]').form.requestSubmit()");
  await wait(page,"!document.querySelector('[aria-busy=true]') && ![...document.querySelectorAll('dialog[open] h2')].some(title=>title.textContent==='展开源码')");
  assert.equal((await api(page,identity,'GET',`/agents/${fixture.analyticsXray}/configs/xray/workspace`)).config.version,xray.config.version+1);
  await navigate('#archive-config');record(`${mode}: analytics enrollment command, owner name save, revisioned pending share allocation, actual inbound generation/save/validation task, configuration-file conflict/draft preservation and save/readback, and client display save/readback, traffic batch reset receipts, Xray outbound preset/binding and complete-source save and persistent port target enable/disable and unbound outbound add/delete passed`);
  // Filters query real endpoints without a confirmation button. Delay an old
  // response to exercise rapid changes and navigation during a pending query.
  await evaluate(page,"location.hash='#tasks'");
  await wait(page,"document.querySelector('.a-top h1')?.textContent==='任务' && !!document.querySelector('.a-auto-filter') && !document.querySelector('[aria-busy=true]')");
  assert.equal(await evaluate(page,"document.querySelectorAll('.a-auto-filter button[type=submit]').length"),0);
  await evaluate(page,`(() => {
    window.__queryFetch=window.fetch;window.__queryCalls=[];window.__oldQueryFinished=false;window.__oldQueryReady=false;
    window.fetch=async (url,options)=>{
      if(!String(url).includes('/tasks?'))return window.__queryFetch(url,options);
      window.__queryCalls.push(String(url));
      const response=await window.__queryFetch(url,options);
      if(String(url).includes('status=pending')) {
        const body=await response.text();window.__oldQueryReady=true;
        await new Promise(resolve=>setTimeout(resolve,700));window.__oldQueryFinished=true;
        return new Response(body,{status:response.status,headers:response.headers});
      }
      return response;
    };
    const select=document.querySelector('.a-auto-filter [name=status]');select.value='pending';select.dispatchEvent(new Event('change',{bubbles:true}));
  })()`);
  await wait(page,"window.__oldQueryReady");
  await evaluate(page,"const select=document.querySelector('.a-auto-filter [name=status]');select.value='failed';select.dispatchEvent(new Event('change',{bubbles:true}))");
  await wait(page,"window.__queryCalls.some(url=>url.includes('status=failed')) && !document.querySelector('[aria-busy=true]')");
  const filtered=await evaluate(page,"document.querySelector('.a-main section').textContent");
  await wait(page,"window.__oldQueryFinished");
  assert.equal(await evaluate(page,"document.querySelector('.a-main section').textContent"),filtered,'old query cannot replace current results');
  await evaluate(page,"window.fetch=window.__queryFetch;delete window.__queryFetch;delete window.__queryCalls;delete window.__oldQueryFinished;delete window.__oldQueryReady");
  await evaluate(page,"[...document.querySelectorAll('.a-auto-filter button')].find(button=>button.textContent==='重置').click()");
  await wait(page,"document.querySelector('.a-auto-filter [name=status]').value==='' && !document.querySelector('[aria-busy=true]')");
  assert.equal(await evaluate(page,"document.querySelector('.a-auto-filter [name=limit]').value"),'100');
  await screenshot(page,'analytics-automatic-task-filter');
  await evaluate(page,"location.hash='#core-logs'");
  await wait(page,"document.querySelector('.a-top h1')?.textContent==='日志' && !!document.querySelector('.a-auto-filter') && !document.querySelector('[aria-busy=true]')");
  assert.equal(await evaluate(page,"document.querySelectorAll('.a-auto-filter button[type=submit]').length"),0);
  await evaluate(page,"const input=document.querySelector('.a-auto-filter [name=q]');input.value='automatic filter';input.dispatchEvent(new Event('input',{bubbles:true}));location.hash='#archive-config'");
  await wait(page,"document.querySelector('.a-top h1')?.textContent==='配置档案' && !document.querySelector('[aria-busy=true]')");
  record(`${mode}: automatic query filters, removed confirmation controls, delayed response isolation and navigation with pending debounce passed`);

}
