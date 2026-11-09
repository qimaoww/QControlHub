import assert from 'node:assert/strict';
export async function runConnectionSessions({createPage,login,api,evaluate,wait,call,reload,fixture,record}) {
 const [a,b,manager]=await Promise.all([createPage(),createPage(),createPage()]);
 const [alice,bob,admin]=await Promise.all([login(a,'alice'),login(b,'bob'),login(manager,'logs-admin')]);
 const ready=`document.querySelector('.a-main')?.textContent.includes('已读取') && document.querySelector('[aria-label="客户端来源 IP 明细"] tbody tr')`;
 const settled=`document.querySelector('.a-main')?.textContent.includes('已读取') && !document.querySelector('[aria-busy=true]')`;
 const rows=`[...document.querySelectorAll('[aria-label="客户端来源 IP 明细"] tbody tr')]`;
 for(const page of [a,b,manager]){await evaluate(page,"location.hash='#client-connections'");await wait(page,ready)}
 for(const [page,own]of [[a,'alice'],[b,'bob'],[manager,'logs-admin']]) {
  assert.ok(await evaluate(page,`${rows}.every(row=>row.querySelector('[data-column=节点]').textContent.includes(${JSON.stringify(own)}))`),'another user host telemetry entered page');
  assert.ok(await evaluate(page,"document.querySelector('.a-main').textContent.includes('独立 IP 240')"));
 }
 await evaluate(a,`${rows}[0].dataset.identity='retained';document.querySelector('[name=client_ip]').focus();document.querySelector('[name=client_ip]').value='unsubmitted';`);
 await evaluate(a,`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click()`);await wait(a,ready);
 assert.ok(await evaluate(a,`${rows}[0].dataset.identity==='retained' && document.activeElement.name==='client_ip' && document.activeElement.value==='unsubmitted'`),'delta confirmation rebuilt rows or disturbed draft');
 await evaluate(a,`(()=>{const input=document.querySelector('[name=client_ip]');input.value='203.0.113.255';input.dispatchEvent(new Event('change',{bubbles:true}))})()`);
 await wait(a,`(${settled}) && document.querySelector('.a-main').textContent.includes('独立 IP 0')`);
 await evaluate(a,`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click()`);
 await wait(a,settled);
 assert.ok(await evaluate(a,"!document.querySelector('.a-notice')?.textContent && document.querySelector('.a-main').textContent.includes('独立 IP 0')"),'empty delta confirmation became a failed read');
 await evaluate(a,`(()=>{const input=document.querySelector('[name=client_ip]');input.value='';input.dispatchEvent(new Event('change',{bubbles:true}))})()`);await wait(a,ready);
 await evaluate(a,`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='下一页').click()`);await wait(a,`(${ready}) && document.querySelector('.a-main').textContent.includes('第 2 页')`);
 const pageIPs=await evaluate(a,`${rows}.map(row=>row.querySelector('code').textContent)`);
 await reload(a);await wait(a,ready);
 assert.deepEqual(await evaluate(a,`${rows}.map(row=>row.querySelector('code').textContent)`),pageIPs,'real reload lost cached page');
 await call('Network.enable',{},a);await call('Network.emulateNetworkConditions',{offline:true,latency:0,downloadThroughput:-1,uploadThroughput:-1},a);
 await evaluate(a,`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click()`);
 await wait(a,"document.querySelector('.a-main').textContent.includes('更新失败，保留上次结果')");
 assert.deepEqual(await evaluate(a,`${rows}.map(row=>row.querySelector('code').textContent)`),pageIPs,'offline erased real history');
 await call('Network.emulateNetworkConditions',{offline:false,latency:0,downloadThroughput:-1,uploadThroughput:-1},a);
 await evaluate(a,`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click()`);await wait(a,ready);
 const host=fixture['logs-admin'];
 const forbidden=await api(a,alice,'GET',`/client-connections?sync=true&group_by=ip&timeline=false&agent_id=${host}&locations=cached`);
 assert.equal(forbidden.records.length,0,'shared kernel access exposed host source IPs');
 await api(manager,admin,'DELETE',`/users/${alice.user_id}`,undefined,204);
 await api(a,alice,'GET','/client-connections?scope_only=true',undefined,401);
 await reload(a);await wait(a,"!!document.querySelector('#login-form,.a-login form')");await login(a,'bob');await evaluate(a,"location.hash='#client-connections'");await wait(a,ready);
 assert.ok(await evaluate(a,`${rows}.every(row=>row.querySelector('[data-column=节点]').textContent.includes('bob'))`),'same document account switch retained prior IPs');
 record('Real PostgreSQL/password sessions: owner/admin/A/B host isolation, shared grants do not expose host IPs, cached page reload, stable DOM/draft, offline recovery, user revocation and account switch passed');
}
