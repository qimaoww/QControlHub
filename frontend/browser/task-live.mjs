import { startAnalytics } from '../analytics/app.js';
import { assert, waitFor, delay } from './assertions.mjs';

export async function testLiveTaskRuntime(preview = false) {
  const nativeFetch = globalThis.fetch;
  const session = { role: 'user', user_id: 'task-reader', permissions: ['tasks.read'], csrf_token: 'tasks-live-fixture' };
  const task = index => ({ id: `live-${index}`, agent_id: 'alpha', engine: 'mihomo', action: 'validate', status: 'running', created_at: '2026-11-06T10:00:00Z', output: Array.from({length:160}, (_,line)=>`任务输出 ${line} · ${'诊断文本'.repeat(8)}`).join('\n') });
  let tasks = Array.from({length:26}, (_, index)=>task(index));
  let failed = false, denied = false, detailDenied = false, hold = false, release, reads = 0;
  const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (url, options={}) => {
    if (!String(url).startsWith('/api/v1')) return nativeFetch(url, options);
    const parsed = new URL(url, location.href), path = parsed.pathname.slice(7);
    if (path === '/auth/session') return reply(session);
    if (path === '/tasks') {
      reads++;
      const result = tasks.filter(task => !parsed.searchParams.get('agent_id') || task.agent_id === parsed.searchParams.get('agent_id'));
      if (hold) await new Promise(resolve=>{release=resolve;});
      if (denied) return reply({error:'任务访问已撤销'},403);
      if (failed) return reply({error:'临时任务读取失败'},503);
      return reply(result);
    }
    if (path.startsWith('/tasks/')) {
      if (detailDenied) return reply({error:'任务详情访问已撤销'},403);
      return reply(tasks.find(task=>task.id===path.split('/')[2]));
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
    const filter=document.querySelector('.a-main input[name=agent_id]');
    button('下一页').click(); await waitFor(()=>row('live-12'),'任务第二页未打开');
    await waitFor(()=>!document.getAnimations().some(animation=>animation.id.startsWith('analytics-')), '任务列表入场未结束');
    const stable=row('live-12'), stableTable=table();
    const pageLabel=[...document.querySelectorAll('.a-pagination')].find(item=>item.textContent.includes('第 2 / 3 页'));
    filter.focus();filter.setSelectionRange(0,0);window.scrollTo({top:100,behavior:'instant'});await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));const scroll=scrollY;
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
    const box=await waitFor(()=>document.querySelector('dialog[open] pre')?.closest('dialog'),'任务详情未打开');
    const output=box.querySelector('pre');
    output.scrollTop=80;const outputScroll=output.scrollTop;
    output.focus();const text=output.firstChild;
    getSelection().setBaseAndExtent(text,5,text,15);const outputSelected=getSelection().toString();
    const chosen=tasks.find(task=>task.id==='live-13');chosen.output+='\n新的输出';
    button('刷新结果',box).click();
    await waitFor(()=>output.textContent.endsWith('新的输出'),'任务输出未刷新');
    assert.equal(box.querySelector('pre'),output,'任务输出重建');assert.equal(document.activeElement,output,'任务输出刷新丢失焦点');
    assert.equal(getSelection().toString(),outputSelected,'任务输出更新丢失文本选择');assert.equal(output.scrollTop,outputScroll,'任务输出强制跟随');
    if (preview) { document.documentElement.dataset.taskLivePreview = 'ready'; await new Promise(()=>{}); }
    const beforePoll=reads; await delay(2200);
    assert.ok(reads>beforePoll,'任务列表未持续刷新');assert.ok(box.open&&row('live-13')===current,'轮询打断任务详情');
    detailDenied=true;button('刷新结果',box).click();
    await waitFor(()=>output.hidden&&box.textContent.includes('无权读取'),'任务权限撤销仍显示旧输出');
    box.querySelector('[aria-label=关闭]').click();detailDenied=false;
    hold=true;release=null;const before=reads;button('刷新',document.querySelector('.a-top')).click();
    await waitFor(()=>release&&reads>before,'延迟任务读取未开始');
    const releaseOld = release; hold=false;
    filter.value='missing-agent';filter.dispatchEvent(new Event('input',{bubbles:true}));filter.dispatchEvent(new Event('change',{bubbles:true}));
    // A new filter must commit before the older full-list read is released.
    try { await waitFor(()=>table().hidden,'新筛选未加载空结果'); }
    catch(error) { throw new Error(`${error.message}：${JSON.stringify({reads,hold,filter:filter.value,hidden:table().hidden,html:table().outerHTML.slice(0,300),state:document.querySelector('.a-main [role=status]')?.textContent})}`); }
    releaseOld();await delay(30);
    assert.ok(table().hidden&&!document.querySelector('[data-task-id]'),'旧读取覆盖新筛选');
    filter.value='';filter.dispatchEvent(new Event('input',{bubbles:true}));filter.dispatchEvent(new Event('change',{bubbles:true}));
    await waitFor(()=>document.querySelector('[data-task-id]'),'筛选清除未恢复列表');
    denied=true;await refresh(()=>!document.querySelector('[data-task-id]'));
    assert.ok(document.querySelector('.a-main [role=status]').textContent.includes('访问权限已变化'),'权限撤销没有说明');
    assert.ok(document.documentElement.scrollWidth<=innerWidth+1,'任务页面横向溢出');
  } finally { window.dispatchEvent(new Event('pagehide'));document.querySelectorAll('dialog').forEach(box=>box.close());globalThis.fetch=nativeFetch;styles.remove(); }
}
