import { startAnalytics } from '../analytics/app.js';
import { assert, waitFor, delay } from './assertions.mjs';

export async function testLiveTaskRuntime(preview = false) {
  const nativeFetch = globalThis.fetch, nativeNow = Date.now, nativeTimeout = window.setTimeout, nativeClearTimeout = window.clearTimeout;
  let clockOffset = 0;
  Date.now = () => nativeNow() + clockOffset;
  const session = { role: 'user', user_id: 'task-reader', permissions: ['tasks.read'], csrf_token: 'tasks-live-fixture' };
  const task = index => ({ id: `live-${index}`, agent_id: index === 25 ? 'beta' : 'alpha', engine: 'mihomo', action: index === 0 ? 'install' : 'validate', attempt: 2, core_version: 'development', core_source: 'mirror', ...(index === 13 ? { config_id: 'config-alpha', config_version: 3, install_if_missing: true } : {}), status: 'running', created_at: '2026-11-06T10:00:00Z', started_at: new Date(nativeNow()-65000).toISOString(), output: Array.from({length:160}, (_,line)=>`任务输出 ${line} · ${'诊断文本'.repeat(8)}`).join('\n') });
  let tasks = Array.from({length:26}, (_, index)=>task(index));
  let detailReads = 0, statusReads = 0;
  let failed = false, denied = false, detailDenied = 0, hold = false, release, reads = 0;
  let settings = { task_page_size: 50, task_poll_interval_ms: 5000 }, settingsReads = 0, settingsFailed = false;
  let agents = [{ id: 'alpha', name: '节点 Alpha' }, { id: 'beta', name: '节点 Beta' }], agentReads = 0, orderReads = 0, agentsStatus = 200, cancels = 0;
  const pollTimers = new Set();
  const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (url, options={}) => {
    if (!String(url).startsWith('/api/v1')) return nativeFetch(url, options);
    const parsed = new URL(url, location.href), path = parsed.pathname.slice(7);
    if (path === '/auth/session') return reply(session);
    if (path === '/settings') { settingsReads++; return settingsFailed ? reply({ error: '设置暂不可用' }, 503) : reply(settings); }
    if (path === '/agents') { agentReads++; return reply(agentsStatus === 200 ? agents : {error: '节点读取失败'}, agentsStatus); }
    if (path === '/preferences/node-order') { orderReads++; return reply({ids: ['beta', 'alpha'], revision: 1}); }
    if (path === '/tasks') {
      reads++;
      const result = tasks.filter(task => !parsed.searchParams.get('agent_id') || task.agent_id === parsed.searchParams.get('agent_id'));
      if (hold) await new Promise(resolve=>{release=resolve;});
      if (denied) return reply({error:'任务访问已撤销'},403);
      if (failed) return reply({error:'临时任务读取失败'},503);
      return reply(result);
    }
    if (path.startsWith('/tasks/')) {
      if (options.method === 'DELETE') {
        assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'),'tasks-live-fixture','取消任务缺少 CSRF');
        cancels++;tasks.find(task=>task.id===path.split('/')[2]).status='canceled';return reply({});
      }
      if (detailDenied) return reply({error:'任务详情访问已撤销'},detailDenied);
      const task = tasks.find(task=>task.id===path.split('/')[2]);
      if (parsed.searchParams.get('view') === 'status') { statusReads++; return reply({ ...task, output: '' }); }
      detailReads++; return reply(task);
    }
    throw new Error(`Unexpected task live API ${path}`);
  };
  const styles = Object.assign(document.createElement('link'), { rel:'stylesheet', href:'/assets/analytics/theme.css' });
  document.head.append(styles); await new Promise((resolve,reject)=>{styles.onload=resolve;styles.onerror=reject;});
  const button = (text, root=document)=>[...root.querySelectorAll('button')].find(item=>item.textContent===text);
  const table = ()=>document.querySelector('[aria-label=任务]');
  const row = id=>document.querySelector(`[data-task-id="${id}"]`)?.closest('tr');
  const refresh = async predicate => { const before=reads; button('刷新',document.querySelector('.a-top')).click(); await waitFor(()=>reads>before,'任务刷新未读取'); if(predicate) await waitFor(predicate,'任务刷新结果未呈现'); await delay(20); };
  try {
    window.dispatchEvent(new Event('pagehide'));
    document.body.replaceChildren(Object.assign(document.createElement('div'),{id:'live-task-test'}));
    history.replaceState(null,'','#tasks');
    startAnalytics({app:document.querySelector('#live-task-test'),session});
    await waitFor(()=>row('live-0'),'任务列表未加载');
    assert.ok(row('live-0').textContent.replace(/\s/g,'').includes('已运行1分'), '运行任务缺少真实耗时');
    assert.equal(settingsReads, 0, '任务只读账号读取了面板设置');
    const filter=document.querySelector('.a-main select[name=agent_id]');
    assert.equal(agentReads,0,'任务只读账号读取了节点目录');
    assert.equal(orderReads,0,'任务只读账号读取了节点顺序');
    assert.equal(filter.options.length,3,'任务只读账号没有按已有任务提供节点选项');
    button('下一页').click(); await waitFor(()=>row('live-12'),'任务第二页未打开');
    await waitFor(()=>!document.getAnimations().some(animation=>animation.id.startsWith('analytics-')), '任务列表入场未结束');
    const stable=row('live-12'), stableTable=table();
    const pageLabel=[...document.querySelectorAll('.a-pagination')].find(item=>item.textContent.includes('第 2 / 3 页'));
    filter.focus();window.scrollTo({top:100,behavior:'instant'});await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));const scroll=scrollY;
    tasks[12].status='succeeded';
    await refresh(()=>stable.textContent.includes('成功'));
    assert.equal(row('live-12'),stable,'刷新重建任务行');assert.equal(table(),stableTable,'刷新重建任务表格');
    assert.ok(pageLabel.textContent.includes('第 2 / 3 页'),'刷新重置任务分页');
    assert.equal(document.activeElement,filter,'刷新丢失筛选焦点');assert.equal(scrollY,scroll,'刷新改变任务页面滚动');
    const range=document.createRange();range.selectNodeContents(stable.cells[1]);getSelection().removeAllRanges();getSelection().addRange(range);const selected=getSelection().toString();
    await refresh();assert.equal(getSelection().toString(),selected,'无变化刷新丢失任务文本选择');
    tasks.reverse(); await refresh(()=>row('live-13')===stableTable.querySelector('tbody tr'));
    assert.ok(pageLabel.textContent.includes('第 2 / 3 页'),'排序后重置分页');
    const current=row('live-13');
    failed=true; await refresh(()=>document.querySelector('.a-main [role=status]')?.textContent.includes('更新失败'));
    assert.equal(row('live-13'),current,'失败清空有效任务');failed=false;
    await refresh(()=>document.querySelector('.a-main [role=status]')?.textContent==='正在实时更新');
    button('详情',current).click();
    const box=await waitFor(()=>!document.querySelector('dialog[open] [aria-busy=true]') && document.querySelector('dialog[open] pre')?.closest('dialog'),'任务详情未打开');
    const output=box.querySelector('pre');
    assert.ok(box.textContent.includes('第 2 次执行')&&box.textContent.includes('缺少时安装稳定版'),'配置任务缺少执行次数或自动安装上下文');
    assert.ok(!box.textContent.includes('内核版本'),'配置任务错误展示安装版本');
    output.scrollTop=80;const outputScroll=output.scrollTop;
    output.focus();const text=output.firstChild;
    getSelection().setBaseAndExtent(text,5,text,15);const outputSelected=getSelection().toString();
    const chosen=tasks.find(task=>task.id==='live-13');chosen.output+='\n新的输出';chosen.status='succeeded';chosen.attempt=3;chosen.finished_at=new Date(Date.parse(chosen.started_at)+5000).toISOString();
    button('刷新结果',box).click();
    await waitFor(()=>output.textContent.endsWith('新的输出'),'任务输出未刷新');
    assert.ok(box.textContent.replace(/\s/g,'').includes('执行5秒'),'详情未更新已结束任务的实际耗时');
    assert.ok(box.textContent.includes('第 3 次执行'),'详情未刷新执行次数');
    assert.equal(box.querySelector('pre'),output,'任务输出重建');assert.equal(document.activeElement,output,'任务输出刷新丢失焦点');
    assert.equal(getSelection().toString(),outputSelected,'任务输出更新丢失文本选择');assert.equal(output.scrollTop,outputScroll,'任务输出强制跟随');
    if (preview) { document.documentElement.dataset.taskLivePreview = 'ready'; await new Promise(()=>{}); }
    const beforePoll=reads, beforeDetails=detailReads, beforeStatus=statusReads; await delay(2200);
    assert.equal(detailReads,beforeDetails,'终态自动刷新重复读取完整任务输出');
    assert.ok(statusReads>beforeStatus,'终态任务未继续核验资源权限');
    assert.ok(output.textContent.endsWith('新的输出'),'状态响应清空了已完成的任务输出');
    assert.ok(reads>beforePoll,'任务列表未持续刷新');assert.ok(box.open&&row('live-13')===current,'轮询打断任务详情');
    detailDenied=403;
    await waitFor(()=>output.hidden&&box.textContent.includes('无权读取'),'已结束任务权限撤销后未自动清除旧输出');
    assert.ok(!box.textContent.includes('第 3 次执行')&&!box.textContent.includes('缺少时安装稳定版'),'任务权限撤销仍显示上下文');
    box.querySelector('[aria-label=关闭]').click();detailDenied=0;
    for (const deniedStatus of [403, 404]) {
      button('详情',current).click();
      const deniedBox=await waitFor(()=>!document.querySelector('dialog[open] [aria-busy=true]') && document.querySelector('dialog[open] pre')?.closest('dialog'),'手动拒绝测试未打开任务详情');
      detailDenied=deniedStatus;
      const resultRefresh=button('刷新结果',deniedBox);resultRefresh.click();
      await waitFor(()=>deniedBox.querySelector('pre').hidden&&!resultRefresh.hasAttribute('aria-busy'),'手动权限拒绝未完成收尾');
      assert.ok(resultRefresh.disabled,`手动 ${deniedStatus} 后恢复了无效的结果刷新入口`);
      assert.ok(deniedBox.textContent.includes('无权读取'),'手动权限拒绝未反馈终态');
      deniedBox.querySelector('[aria-label=关闭]').click();detailDenied=0;
    }
    hold=true;release=null;const before=reads;button('刷新',document.querySelector('.a-top')).click();
    await waitFor(()=>release&&reads>before,'延迟任务读取未开始');
    const releaseOld = release; hold=false;
    filter.value='beta';filter.dispatchEvent(new Event('input',{bubbles:true}));filter.dispatchEvent(new Event('change',{bubbles:true}));
    // A new filter must commit before the older full-list read is released.
    try { await waitFor(()=>row('live-25')&&!row('live-13'),'新节点筛选未加载结果'); }
    catch(error) { throw new Error(`${error.message}：${JSON.stringify({reads,hold,filter:filter.value,hidden:table().hidden,html:table().outerHTML.slice(0,300),state:document.querySelector('.a-main [role=status]')?.textContent})}`); }
    releaseOld();await delay(30);
    assert.ok(row('live-25')&&!row('live-13'),'旧读取覆盖新节点筛选');
    filter.value='';filter.dispatchEvent(new Event('input',{bubbles:true}));filter.dispatchEvent(new Event('change',{bubbles:true}));
    await waitFor(()=>document.querySelector('[data-task-id]'),'筛选清除未恢复列表');
    filter.value='beta';filter.dispatchEvent(new Event('change',{bubbles:true}));
    await waitFor(()=>row('live-25')&&!row('live-13'),'空状态测试未选择节点');
    tasks=tasks.filter(task=>task.agent_id!=='beta');
    await refresh(()=>table().hidden);
    assert.equal(filter.value,'beta','节点没有任务后筛选被重置');
    filter.value='';filter.dispatchEvent(new Event('change',{bubbles:true}));
    await waitFor(()=>!table().hidden,'清除筛选没有恢复任务');
    denied=true;await refresh(()=>!document.querySelector('[data-task-id]'));
    assert.ok(document.querySelector('.a-main [role=status]').textContent.includes('访问权限已变化'),'权限撤销没有说明');
    assert.ok(document.documentElement.scrollWidth<=innerWidth+1,'任务页面横向溢出');
    // The bootstrap settings supply the initial size and one dynamic poller.
    window.dispatchEvent(new Event('pagehide')); denied=false;tasks=Array.from({length:6},(_,index)=>task(index));
    tasks[1].action='upgrade-agent';tasks[2].action='ip-quality';tasks[3].action='configure-tcp';
    session.permissions.push('settings.read','agents.read','tasks.execute');
    const delays=[];
    window.setTimeout=(callback, timeout, ...args)=>{
      if(callback.name!=='trigger'||timeout>5000)return nativeTimeout(callback,timeout,...args);
      delays.push(timeout);
      const id=nativeTimeout(()=>{pollTimers.delete(id);callback(...args);},timeout);
      pollTimers.add(id);return id;
    };
    window.clearTimeout=id=>{pollTimers.delete(id);nativeClearTimeout(id);};
    document.body.replaceChildren(Object.assign(document.createElement('div'),{id:'task-preferences-test'}));
    startAnalytics({app:document.querySelector('#task-preferences-test'),session});
    await waitFor(()=>row('live-0'),'带设置的任务页面未加载');
    await waitFor(()=>document.querySelector('.a-main input[name=limit]').value==='50' && row('live-0').cells[1].textContent==='节点 Alpha', '独立任务偏好与节点名称未读取');
    assert.equal(document.querySelector('.a-main input[name=limit]').value,'50','任务条数未读取账号设置');
    assert.equal(settingsReads,1,'首次加载重复读取设置');
    await waitFor(()=>delays.at(-1)===5000, '设置晚到后轮询未采用账号间隔');
    assert.equal(delays.at(-1),5000,'轮询未使用设置间隔');
    assert.equal(pollTimers.size,1,'任务页首次加载没有只保留一个轮询计时器');
    const picker=document.querySelector('.a-main select[name=agent_id]');
    assert.equal(agentReads,1,'首次任务读取重复加载节点目录');
    assert.equal(orderReads,1,'首次任务读取没有加载账号节点顺序');
    assert.equal(picker.options[1].value,'beta','任务节点筛选没有使用账号顺序');
    assert.equal(row('live-0').cells[1].textContent,'节点 Alpha','任务列表没有显示授权节点名称');
    assert.equal(row('live-1').cells[2].textContent,'QAgent','升级任务没有区分 Agent');
    assert.equal(row('live-2').cells[2].textContent,'IPQuality','IP 质量任务没有区分检测工具');
    assert.equal(row('live-3').cells[2].textContent,'BBR / TCP','TCP 任务没有区分系统操作');
    button('详情',row('live-0')).click();
    const install=await waitFor(()=>!document.querySelector('dialog[open] [aria-busy=true]') && document.querySelector('dialog[open]'),'安装任务详情未打开');
    assert.ok(install.textContent.includes('节点 Alpha')&&install.textContent.includes('development')&&install.textContent.includes('vernesong/mihomo 镜像（第三方）'),'任务详情缺少节点名称、安装版本或来源');
    install.querySelector('[aria-label=关闭]').click();
    const updatedName='节点 Alpha · 更新后 '+ 'LongNodeNameWithoutSpaces'.repeat(30);
    agents=[{id:'alpha',name:updatedName},{id:'beta',name:'节点 Beta'}];
    settings={task_page_size:500,task_poll_interval_ms:600};clockOffset=31000;
    const beforeSettings=reads;document.dispatchEvent(new Event('visibilitychange'));
    await waitFor(()=>reads>beforeSettings&&delays.at(-1)===600,'过期设置未更新轮询间隔');
    assert.equal(settingsReads,2,'设置缓存过期未重新读取');
    assert.equal(pollTimers.size,1,'缓存过期后创建了重复轮询计时器');
    assert.equal(agentReads,2,'节点目录缓存过期未重新读取');
    assert.equal(row('live-0').cells[1].textContent,updatedName,'节点名称更新未应用到任务行');
    assert.ok(document.documentElement.scrollWidth<=innerWidth+1,'超长节点名称导致任务页面溢出');
    assert.equal(document.querySelector('.a-main input[name=limit]').value,'50','设置更新覆盖当前查询');
    await refresh();assert.equal(settingsReads,2,'普通任务刷新重复读取未过期设置');
    assert.equal(agentReads,2,'普通任务刷新重复读取未过期节点目录');
    clockOffset=62000;settingsFailed=true;agentsStatus=503;
    await refresh(()=>document.querySelector('.a-main [role=status]')?.textContent==='正在实时更新');
    assert.ok(row('live-0'),'设置失败清空仍获授权的任务');
    assert.equal(row('live-0').cells[1].textContent,updatedName,'节点目录临时失败清空有效名称');
    picker.value='alpha';picker.dispatchEvent(new Event('change',{bubbles:true}));
    await waitFor(()=>!document.querySelector('.a-filter-form[aria-busy]'),'节点筛选未结束');
    clockOffset=93000;agentsStatus=403;
    await refresh(()=>row('live-0').cells[1].textContent==='alpha');
    assert.equal(picker.value,'alpha','节点目录权限撤销重置了任务查询');
    assert.ok(!picker.textContent.includes('节点 Alpha'),'节点目录权限撤销仍保留名称');
    tasks[0].status='pending';await refresh(()=>row('live-0').textContent.includes('准备中'));
    button('取消',row('live-0')).click();
    let confirmation=await waitFor(()=>document.querySelector('dialog[open]'),'取消任务没有确认');
    button('取消',confirmation).click();await delay(20);
    assert.equal(cancels,0,'放弃取消确认仍提交了请求');
    button('取消',row('live-0')).click();
    confirmation=await waitFor(()=>document.querySelector('dialog[open]'),'再次取消任务没有确认');
    button('取消任务',confirmation).click();await waitFor(()=>cancels===1&&row('live-0').textContent.includes('已取消'),'取消任务未更新结果');
  } finally { window.dispatchEvent(new Event('pagehide'));document.querySelectorAll('dialog').forEach(box=>box.close());globalThis.fetch=nativeFetch;Date.now=nativeNow;window.setTimeout=nativeTimeout;window.clearTimeout=nativeClearTimeout;styles.remove(); }
}
