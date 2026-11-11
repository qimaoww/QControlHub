import { startAnalytics } from './analytics/app.js';
import { assert, waitFor, delay } from './browser/assertions.mjs';

// Exercise the same quota route, modal, mutation locks and session lifetime
// shipped by the independent theme, with deterministic API responses.
export async function testAgentSharingRuntime() {
  const nativeFetch = globalThis.fetch;
  let session = { user_id: 'alice', username: 'alice', role: 'user', permissions: ['overview.read', 'agents.read', 'agents.manage', 'agent-access.read', 'settings.read'], csrf_token: 'sharing-fixture' };
  const agent = { id: 'own', name: 'Alice 自有节点', can_manage: true, status: 'online', capabilities: ['mihomo', 'xray'], features: ['shared-traffic-v1', 'shared-engines-v1', 'independent-egress-v1'] };
  let sharing = { revision: 2, shares: [{ id: 'shr_bob', username: 'bob', user_id: 'bob-id', status: 'accepted', invitation_revision: 2, engines: ['mihomo'], ports: [21003], limit_bytes: 1024 ** 3, used_bytes: 64, enabled: true }] };
  const writes = [], reads = [];
  let hold = false, release, rejectRead = false, holdRead = false, readStatus = 503, retainedFailures = 0;
  const pendingReads = [];
  const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (url, options = {}) => {
    if (!String(url).startsWith('/api/v1')) return nativeFetch(url, options);
    const path = String(url).slice('/api/v1'.length), method = options.method || 'GET';
    if (path === '/tasks/failures' && method === 'POST') {
      assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'), session.csrf_token, '失败留存缺少 CSRF');
      return reply({id:`fixture-failure-${++retainedFailures}`});
    }
    reads.push({path,method});
    if (path === '/auth/session') return reply(session);
    if (path === '/preferences/ui' && method === 'GET') return reply({ values: {}, revision: 0 });
    if (path === '/settings') return reply({});
    if (path === '/agents') return reply([agent]);
    if (path === '/agent-access') return reply({ isolated: false, shares: [] });
    if (path === '/preferences/node-order') return reply({agent_ids:['own'],revision:1});
    if (path === '/overview') return reply({});
    if (path === '/agents/own/sharing') {
      if (method === 'PUT') {
        assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'), session.csrf_token, '共享保存缺少 CSRF');
        const body = JSON.parse(options.body); writes.push(body);
        if (hold) await new Promise(resolve => { release = resolve; });
        if (body.revision !== sharing.revision) return reply({error:'共享分配已变更，请重新读取'},409);
        sharing = { revision: sharing.revision + 1, shares: body.shares.map(row => ({
          ...row, id: `shr_${row.username}`, user_id: `${row.username}-id`, used_bytes:64,
          status: row.reinvite ? 'pending' : sharing.shares.find(share => share.username === row.username)?.status || 'pending', reinvite:false,
        })) };
      } else {
        const snapshot = structuredClone(sharing);
        if (holdRead) await new Promise(resolve => pendingReads.push(resolve));
        if (rejectRead) return reply({error:'共享读取失败'},readStatus);
        return reply(snapshot);
      }
      return reply(structuredClone(sharing));
    }
    throw new Error(`Unexpected sharing API: ${method} ${path}`);
  };
  const styles = document.createElement('link'); styles.rel='stylesheet'; styles.href='/assets/analytics/theme.css'; document.head.append(styles);
  await new Promise((resolve,reject)=>{styles.onload=resolve;styles.onerror=reject;});
  const boot = async () => {
    window.dispatchEvent(new Event('pagehide'));
    document.querySelectorAll('dialog.a-dialog').forEach(box=>box.close());
    document.body.replaceChildren(Object.assign(document.createElement('div'),{id:'native-sharing-test'}));
    history.replaceState(null,'','#my-quota');
    startAnalytics({app:document.querySelector('#native-sharing-test'),session});
    await waitFor(()=>document.querySelector('.a-main')?.textContent.includes(agent.can_manage ? '共享 Agent 设置' : '暂无共享记录')&&!document.querySelector('.a-main [aria-busy=true]'),'共享与额度未加载');
  };
  const dialog = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const form = () => dialog()?.querySelector('form');
  const field = name => form().querySelector(`[name="${name}"]`);
  const button = (text, root = dialog()) => [...root.querySelectorAll('button')].find(control=>control.textContent===text);
  const input = (control,value) => {control.value=value;control.dispatchEvent(new Event('input',{bubbles:true}));};
  const submit = () => form().requestSubmit();
  const errors = () => [...document.querySelectorAll('.a-dialog .a-notice,.a-toast.error')].filter(el=>el.checkVisibility()).map(el=>el.textContent).join(' ');
  const idle = () => !document.querySelector('#native-sharing-test[aria-busy=true],dialog[open] [aria-busy=true]');
  const loading = () => [...dialog().querySelectorAll('.a-notice')].some(el => el.checkVisibility() && /正在读取|正在更新/.test(el.textContent));
  const getCount = () => reads.filter(call => call.path.endsWith('/sharing') && call.method === 'GET').length;
  const close = async () => {dialog().querySelector('[aria-label=关闭]').click();await waitFor(()=>!dialog(),'共享弹窗未关闭');await delay(30);};
  const open = async () => {
    document.querySelector('[data-sharing-agent=own]').click();
    await waitFor(()=>fieldSafe()&&idle(),'共享设置未就绪');
    assert.ok(!loading(),'首次读取成功后残留共享读取提示');
  };
  const fieldSafe = () => form()?.querySelector('[name=ports]');
  const reload = async accept => {
    button('重新读取').click();
    await waitFor(()=>dialog()?.querySelector('h2')?.textContent==='重新读取','缺少重新读取确认');
    button(accept?'重新读取':'取消').click();
    await waitFor(()=>fieldSafe()&&idle(),'重新读取未结束');await delay(30);
  };
  try {
    agent.can_manage=false;await boot();
    assert.ok(!document.querySelector('[data-sharing-agent]'),'共享用户可转授权节点');
    assert.ok(!reads.some(call=>call.path.endsWith('/sharing')),'共享用户读取所有者共享分配');
    agent.can_manage=true;agent.features=agent.features.filter(feature=>feature!=='independent-egress-v1');
    await boot();await open();
    assert.ok(errors().includes('升级 Agent')&&form().querySelector('[type=submit]').disabled,'缺少能力仍允许保存');
    input(field('ports'),'21004');submit();await delay(30);assert.equal(writes.length,0,'缺少能力产生写入');await close();
    agent.features.push('independent-egress-v1');await boot();await open();
    const beforeRead = getCount(), beforeFailures = retainedFailures;
    button('重新读取').click();await waitFor(idle,'无修改重新读取未结束');
    assert.equal(getCount(),beforeRead+1,'无修改重新读取请求数');assert.ok(!loading());
    holdRead=true;button('重新读取').click();await waitFor(()=>pendingReads.length,'重新读取未开始');
    assert.ok(loading()&&button('重新读取').disabled,'实际读取未显示状态或未防重复');
    input(field('ports'),'21004');assert.ok(form().querySelector('[type=submit]').disabled,'读取期间允许保存');
    // Even editing back to the original value must not accept a late snapshot.
    input(field('ports'),'21003');holdRead=false;pendingReads.shift()();
    await waitFor(()=>idle()&&errors().includes('草稿已保留'),'迟到读取覆盖编辑');
    assert.equal(retainedFailures,beforeFailures,'读取或编辑竞争产生失败任务');
    button('重新读取').click();await waitFor(idle,'竞争后重新读取未恢复');
    assert.ok(field('username').readOnly&&form().querySelectorAll('[name=engines]').length===2,'接收用户或内核范围不正确');
    assert.ok(form().querySelector('[type=submit]').disabled,'无变化仍要求保存');
    field('engines').click();submit();await waitFor(()=>errors().includes('内核'),'未选择内核无错误');
    assert.equal(writes.length,0,'空内核提交到 API');field('engines').click();
    assert.ok(form().querySelector('[type=submit]').disabled,'恢复原值仍要求保存');
    button('添加用户').click();assert.equal(form().querySelectorAll('[name=username]').length,2);
    button('移除').click();assert.ok(form().querySelector('[type=submit]').disabled,'移除临时空行仍要求保存');
    for (const [ports,error] of [['0,21001','不能'],['21000-21256','256'],['21000-21002,21001','重叠'],['10084-10087','保留']]) {
      input(field('ports'),ports);submit();await waitFor(()=>errors().includes(error),`无端口校验：${ports}`);assert.equal(writes.length,0);
    }
    assert.ok(field('ports').placeholder.includes('21000-21100'));
    input(field('ports'),'21003 - 21005, 22000');form().querySelector('[name=engines][value=xray]').click();
    await close();await open();
    assert.equal(field('ports').value,'21003 - 21005, 22000','重新打开丢失草稿');
    assert.ok(form().querySelector('[name=engines][value=xray]').checked,'重新打开丢失内核草稿');
    sharing.revision++;submit();await waitFor(()=>errors().includes('已变更')&&idle(),'冲突无反馈');
    assert.equal(field('ports').value,'21003 - 21005, 22000');
    const canceledReads = getCount();await reload(false);assert.equal(field('ports').value,'21003 - 21005, 22000','取消读取丢失草稿');
    assert.equal(getCount(),canceledReads,'取消确认仍执行请求');assert.ok(!loading(),'取消确认残留读取提示');
    rejectRead=true;await reload(true);assert.equal(field('ports').value,'21003 - 21005, 22000','读取失败丢失草稿');assert.ok(errors().includes('共享读取失败'));rejectRead=false;
    await reload(true);assert.equal(field('ports').value,'21003');assert.ok(!dialog().querySelector('.a-dialog-body').textContent.includes('已变更'),'成功重新读取仍显示旧冲突');assert.ok(!form().querySelector('[name=engines][value=xray]').checked);
    assert.ok(form().querySelector('[type=submit]').disabled,'重新读取仍要求保存');
    assert.equal([...dialog().querySelectorAll('button')].filter(el=>el.checkVisibility()&&el.textContent==='重新读取').length,1,'重新读取入口重复');
    input(field('ports'),'21000 - 21100，22000');input(field('limit_gib'),'2.5');input(field('http_domains'),'auth.example.test\napi.example.test');hold=true;
    submit();submit();await waitFor(()=>release,'保存未开始');
    assert.equal(writes.length,2,'重复保存未阻止');assert.ok(field('ports').matches(':disabled'),'提交中可编辑');
    dialog().querySelector('[aria-label=关闭]').click();assert.ok(form(),'提交中可关闭并绕过锁定');
    hold=false;release();release=null;await waitFor(()=>idle()&&form().querySelector('[type=submit]').disabled,'保存后仍要求保存');
    assert.equal(sharing.shares[0].limit_bytes,2.5*1024**3);
    assert.equal(sharing.shares[0].ports.join(','),[...Array.from({length:101},(_,i)=>21000+i),22000].join(','));
    assert.equal(sharing.shares[0].username,'bob');assert.equal(field('ports').value,'21000-21100, 22000');
    assert.equal(writes.at(-1).shares[0].engines.join(','),'mihomo','保存扩大内核分配');
    assert.equal(writes.at(-1).shares[0].http_domains.join(','),'auth.example.test,api.example.test','HTTP-01 域名未保存');
    assert.ok(dialog().scrollWidth<=dialog().clientWidth+1,'共享弹窗横向溢出');
    for(const [text,expected] of [['0',[0]],['',[]],['21001-21003',[21001,21002,21003]],['0',[0]]]) {
      input(field('ports'),text);await close();await open();assert.equal(field('ports').value,text,'空端口或无限制草稿丢失');
      const before=writes.length;submit();await waitFor(()=>writes.length===before+1&&idle()&&form().querySelector('[type=submit]').disabled,'端口模式保存失败');
      assert.equal(JSON.stringify(sharing.shares[0].ports),JSON.stringify(expected));assert.equal(field('ports').value,text);
      assert.equal(sharing.shares[0].limit_bytes,2.5*1024**3);assert.equal(sharing.shares[0].used_bytes,64);
    }
    input(field('limit_gib'),'3');submit();await waitFor(()=>idle()&&form().querySelector('[type=submit]').disabled,'额度保存未结束');
    assert.equal(JSON.stringify(sharing.shares[0].ports),'[0]','修改额度丢失无限制端口');
    await close();sharing.shares[0].engines=['xray'];agent.capabilities=['mihomo'];await boot();await open();
    assert.ok(form().querySelector('[name=engines][value=xray]')?.checked&&dialog().textContent.includes('xray · 不可用'),'不可用的已有内核分配被丢弃');
    input(field('limit_gib'),'4');submit();await waitFor(()=>idle()&&form().querySelector('[type=submit]').disabled,'已有分配保存未结束');
    assert.equal(writes.at(-1).shares[0].engines.join(','),'xray','额度保存静默移除已有内核');
    await close();sharing.shares[0].engines=['mihomo'];agent.capabilities=['mihomo','xray'];sharing.shares[0].status='rejected';sharing.revision++;await boot();await open();
    assert.ok(dialog().textContent.includes('已拒绝'));button('重新邀请').click();assert.ok(dialog().textContent.includes('待发送'));
    await close();await open();assert.ok(button('重新邀请').disabled,'重新打开重复邀请');
    submit();await waitFor(()=>dialog()?.textContent.includes('待接受')&&idle(),'重新邀请未变成待接受');
    assert.ok(writes.at(-1).shares[0].reinvite&&!button('重新邀请'),'重新邀请未显式提交');
    const savedWrites=writes.length;await close();await open();assert.ok(form().querySelector('[type=submit]').disabled,'保存后重新打开仍要求保存');submit();await delay(30);assert.equal(writes.length,savedWrites);
    for (const status of [403,404]) {
      readStatus=status;rejectRead=true;button('重新读取').click();await waitFor(()=>idle()&&errors().includes('共享读取失败'),'失效目标未退出读取');
      assert.ok(!fieldSafe()&&button('添加用户').disabled&&form().querySelector('[type=submit]').disabled,'权限失效后保留可操作内容');
      rejectRead=false;button('重新读取').click();await waitFor(()=>fieldSafe()&&idle(),'权限恢复后未恢复操作');assert.ok(!loading());
    }
    readStatus=503;
    await close();sharing.shares=[];await boot();
    document.querySelector('[data-sharing-agent=own]').click();await waitFor(()=>button('添加用户')&&idle(),'空共享未就绪');
    assert.ok(!loading()&&!button('添加用户').disabled,'空数据持续读取或不能添加');
    await close();rejectRead=true;document.querySelector('[data-sharing-agent=own]').click();
    await waitFor(()=>idle()&&errors().includes('共享读取失败'),'首次读取失败持续等待');
    assert.ok(!loading()&&button('保存共享设置').disabled,'失败变成可提交的空数据');
    rejectRead=false;button('重新读取').click();await waitFor(()=>button('添加用户')&&idle(),'首次读取失败后未恢复');
    await close();holdRead=true;document.querySelector('[data-sharing-agent=own]').click();await waitFor(()=>pendingReads.length,'慢首次读取未开始');
    assert.ok(loading()&&!dialog().querySelector('[aria-label=关闭]').disabled,'慢读取不能关闭或无提示');
    await close();holdRead=false;document.querySelector('[data-sharing-agent=own]').click();await waitFor(()=>button('添加用户')&&idle(),'快速重开未完成');
    pendingReads.shift()();await delay(30);assert.equal(document.querySelectorAll('dialog[open]').length,1,'旧读取重新打开弹窗');assert.ok(!loading());
    await close();sharing.shares=[{username:'bob',user_id:'bob-id',enabled:true,engines:['mihomo'],ports:[21003],limit_bytes:1024**3}];await boot();await open();
    input(field('ports'),'21003, 21005');hold=true;submit();await waitFor(()=>release,'账号切换保存未开始');
    session={...session,user_id:'carol',username:'carol'};agent.can_manage=false;await boot();hold=false;release();await delay(60);
    assert.ok(!dialog()&&!document.querySelector('.a-toast'),'旧账号结果进入新账号');
    assert.ok(!document.querySelector('[data-sharing-agent]'),'新账号获得旧账号共享入口');
  } finally {
    window.dispatchEvent(new Event('pagehide'));document.querySelectorAll('dialog.a-dialog').forEach(box=>box.close());styles.remove();globalThis.fetch=nativeFetch;
  }
}
