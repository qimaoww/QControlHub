import { testNativeInvitations } from "./users-native-invitations.mjs";
import { testNativeAllocations } from "./users-native-allocations.mjs";
import { startAnalytics } from '../analytics/app.js';
import { assert, waitFor, delay } from './assertions.mjs';

// Keep the allocation and invitation regression suite while moving account
// behavior onto the actual independent theme, including its modal locks.
export async function testNativeUsersRuntime() {
  window.dispatchEvent(new Event('pagehide'));
  document.querySelectorAll('link[rel=stylesheet]').forEach(link => link.remove());
  const style = Object.assign(document.createElement('link'), {rel:'stylesheet',href:'/assets/analytics/theme.css'});
  document.head.append(style);
  await new Promise((resolve,reject)=>{style.onload=resolve;style.onerror=reject;});
  const nativeFetch = globalThis.fetch;
  const session = {user_id:'self',username:'self',role:'admin',csrf_token:'users-fixture'};
  let users = [
    {id:'alice',username:'alice',display_name:'普通用户',role:'user',permissions:['agents.read']},
    {id:'bob',username:'bob',display_name:'另一用户',role:'user',permissions:['agents.read']},
    {id:'last',username:'last',role:'admin',account_actions:{delete_restriction:'last_administrator',disable_restriction:'last_administrator',demote_restriction:'last_administrator'}},
    {id:'self',username:'self',role:'admin'},
    {id:'other-admin',username:'other-admin',role:'admin'},
  ];
  const writes=[];
  let failure='',held=false,release;
  const reply=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}});
  globalThis.fetch=async(url,options={})=>{
    if(!String(url).startsWith('/api/v1'))return nativeFetch(url,options);
    const path=String(url).slice(7),method=options.method||'GET';
    if(path==='/auth/session')return reply(session);
    if(path==='/settings')return reply({});
    if(path==='/users'&&method==='GET')return reply(users);
    if(method==='POST'||method==='PUT'){
      assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'),session.csrf_token,'账号写入缺少 CSRF');
      const body=JSON.parse(options.body||'{}');writes.push({path,method,body});
      if(held)await new Promise(resolve=>{release=resolve;});
      if(failure)return reply({error:failure},409);
      if(path==='/users'){
        const user={...body,id:body.username};users.push(user);return reply(user);
      }
      const id=path.split('/')[2];
      if(path.endsWith('/purge')){users=users.filter(user=>user.id!==id);return reply({});}
      const user=users.find(user=>user.id===id);Object.assign(user,body);return reply(user);
    }
    throw new Error(`Unexpected native users API: ${method} ${path}`);
  };
  const button=(text,root=document)=>[...root.querySelectorAll('button')].find(item=>item.textContent===text);
  const dialog=()=>[...document.querySelectorAll('dialog[open]')].at(-1);
  const form=()=>dialog()?.querySelector('form');
  const input=(control,value)=>{control.value=value;control.dispatchEvent(new Event('input',{bubbles:true}));};
  const row=id=>[...document.querySelectorAll('.a-main tbody tr')].find(item=>item.cells[0]?.textContent===id);
  const modal=async title=>waitFor(()=>dialog()?.querySelector('h2')?.textContent===title,`未显示弹窗：${title}`);
  const confirm=async title=>{await modal(title);button(title,dialog()).click();await delay(20);};
  const fits=()=>{
    assert.ok(document.documentElement.scrollWidth<=innerWidth,'用户页面横向溢出');
    const box=dialog();if(!box)return;
    assert.ok(box.scrollWidth<=box.clientWidth+1,'账号弹窗横向溢出');
    const submit=box.querySelector('[type=submit]');
    if(submit){submit.scrollIntoView({block:'nearest'});const bounds=submit.getBoundingClientRect();assert.ok(bounds.left>=0&&bounds.right<=innerWidth&&bounds.bottom<=innerHeight+1,'账号保存操作不可达');}
  };
  const restart = async (nextSession = session) => {
    window.dispatchEvent(new Event('pagehide'));
    document.querySelectorAll('dialog').forEach(box => box.close());
    document.body.replaceChildren(Object.assign(document.createElement('div'),{id:'native-users-test'}));
    history.replaceState(null,'','#users');
    startAnalytics({app:document.querySelector('#native-users-test'),session:nextSession});
    await waitFor(()=>row('alice'),'新主题用户页未加载');
  };
  try{
    await restart();
    for(const id of ['self','last']){
      assert.ok(button('停用',row(id)).disabled&&button('永久删除',row(id)).disabled,'受保护管理员可停用或删除');
    }
    button('编辑',row('last')).click();await modal('编辑 last');
    assert.ok(form().elements.role.disabled,'最后一个管理员可降级');
    input(form().elements.display_name,'管理员显示名称');form().requestSubmit();
    await waitFor(()=>!dialog()&&row('last').textContent.includes('管理员显示名称'),'受保护管理员名称无法保存');
    assert.equal(writes.at(-1).body.role,'admin','保存名称改变受保护管理员角色');
    assert.equal(writes.at(-1).body.permissions.length,0,'保存名称改变管理员权限语义');
    await restart({ ...session, user_id: '', auth_type: 'environment_token', token_admin_available: true });
    assert.ok(!button('停用',row('self')).disabled && !button('永久删除',row('self')).disabled, '环境令牌管理员无法管理数据库管理员');
    assert.ok(!button('共享资源',row('self')), '管理员被提供共享分配');
    button('编辑',row('self')).click(); await modal('编辑 self');
    assert.ok(!form().elements.role.disabled, '环境令牌管理员无法编辑其他管理员角色');
    dialog().querySelector('[aria-label=关闭]').click(); await restart();

    // Protection can change after the editor or confirmation has opened.
    button('编辑',row('other-admin')).click(); await modal('编辑 other-admin');
    form().elements.role.value = 'user';
    users.find(user => user.id === 'other-admin').account_actions = { demote_restriction: 'last_administrator' };
    const beforeDemote = writes.length; form().requestSubmit();
    await waitFor(() => form()?.textContent.includes('最后一个') && !form().querySelector('[type=submit]').disabled, '迟到的管理员保护没有阻止降级');
    assert.equal(writes.length, beforeDemote, '管理员保护变化后仍提交降级');
    dialog().querySelector('[aria-label=关闭]').click();
    delete users.find(user => user.id === 'other-admin').account_actions;
    button('永久删除',row('other-admin')).click(); await confirm('永久删除用户');
    users.find(user => user.id === 'other-admin').account_actions = { delete_restriction: 'last_administrator' };
    await confirm('确认永久删除');
    await waitFor(() => !dialog() && button('永久删除',row('other-admin')).disabled, '确认期间的管理员保护没有重新读取');
    assert.equal(writes.length, beforeDemote, '管理员保护变化后仍删除');
    delete users.find(user => user.id === 'other-admin').account_actions;
    button('刷新', document.querySelector('.a-top')).click();
    await waitFor(() => !button('永久删除',row('other-admin')).disabled, '保护解除后未恢复');
    held = true; release = null; failure = '请先停止共享节点上的独立内核并完成流量结算，再删除账号。';
    button('永久删除',row('other-admin')).click(); await confirm('永久删除用户'); await confirm('确认永久删除');
    await waitFor(() => release, '管理员删除未开始');
    users.find(user => user.id === 'other-admin').account_actions = { delete_restriction: 'last_administrator' };
    release(); held = false;
    await waitFor(() => button('永久删除',row('other-admin')).disabled && [...document.querySelectorAll('.a-toast.error')].some(item => item.textContent.includes(failure)), '迟到失败重新启用了受保护管理员');
    failure = ''; delete users.find(user => user.id === 'other-admin').account_actions;
    button('刷新', document.querySelector('.a-top')).click();
    await waitFor(() => !button('永久删除',row('other-admin')).disabled, '重读后管理员仍被旧操作锁定');
    button('创建用户').click();await modal('创建用户');
    assert.ok(form().querySelector('details.a-disclosure')&&!form().querySelector('details.a-disclosure').open,'账号权限没有默认收起');
    form().querySelector('summary').click();fits();form().querySelector('summary').click();
    assert.equal(form().elements.password.minLength,12,'创建账号未约束密码长度');
    assert.equal(form().elements.password.maxLength,72,'创建账号未约束密码上限');
    input(form().elements.username,'native-user');input(form().elements.display_name,'原生主题测试用户');
    input(form().elements.password,'only-test-password-12');fits();
    failure='账号保存冲突';form().requestSubmit();
    await waitFor(()=>dialog()?.textContent.includes(failure)&&!form().querySelector('[type=submit]').disabled,'账号失败无反馈或未解锁');
    assert.equal(form().elements.password.value,'only-test-password-12','失败丢失密码草稿');
    failure='';held=true;form().requestSubmit();form().requestSubmit();
    await waitFor(()=>release,'账号保存没有提交');const count=writes.length;
    assert.ok([...form().querySelectorAll('input,select,button')].every(item=>item.matches(':disabled')),'提交期间账号字段未锁定');
    dialog().querySelector('[aria-label=关闭]').click();assert.ok(form(),'提交期间关闭账号弹窗');
    release();held=false;await waitFor(()=>!dialog()&&row('native-user'),'创建未返回列表');
    assert.equal(writes.length,count,'重复提交创建多个账号');
    const created=writes.at(-1).body;
    assert.equal(created.agent_isolation,true,'新普通用户默认未隔离');
    for(const permission of ['enrollment.manage','agents.manage','settings.manage'])assert.ok(created.permissions.includes(permission),'普通用户缺少自有资源管理权限');
    assert.ok(!created.permissions.includes('panel-metrics.read'),'默认授予面板主机指标权限');
    button('编辑',row('native-user')).click();await modal('编辑 native-user');
    input(form().elements.display_name,'更新后的显示名称');form().requestSubmit();
    await waitFor(()=>!dialog()&&row('native-user').textContent.includes('更新后的显示名称'),'账号编辑未保存');
    assert.equal(writes.at(-1).path,'/users/native-user','编辑写入错误账号');
    button('创建用户').click();await modal('创建用户');
    input(form().elements.username,'native-admin');input(form().elements.password,'only-test-password-12');
    form().elements.role.value='admin';form().elements.role.dispatchEvent(new Event('change',{bubbles:true}));form().requestSubmit();
    await waitFor(()=>!dialog()&&row('native-admin'),'管理员未创建');
    assert.equal(writes.at(-1).body.agent_isolation,false,'管理员被设置普通用户隔离');
    assert.equal(writes.at(-1).body.permissions.length,0,'管理员保留隐藏权限表单值');
    const checkDeletedDraft = await testNativeAllocations({ button, dialog, input, row, modal, confirm, fits, restart, session });
    const before=writes.length,disable=button('停用',row('alice'));
    disable.click();disable.click();await modal('停用用户');
    assert.ok(disable.disabled&&document.querySelectorAll('dialog[open]').length===1,'停用确认允许重入');
    {const box=dialog();if(box.dispatchEvent(new Event('cancel',{cancelable:true})))box.close();}await waitFor(()=>!dialog()&&!disable.disabled,'Escape 取消停用未解锁');assert.equal(writes.length,before,'取消停用仍写入');
    disable.click();await confirm('停用用户');await waitFor(()=>button('启用',row('alice')),'停用结果未更新');
    assert.equal(writes.at(-1).body.disabled,true,'停用语义错误');
    button('启用',row('alice')).click();await confirm('启用用户');await waitFor(()=>button('停用',row('alice')),'启用结果未更新');
    let remove=button('永久删除',row('alice'));remove.click();remove.click();await modal('永久删除用户');
    assert.ok(remove.disabled&&document.querySelectorAll('dialog[open]').length===1,'删除确认允许重入');
    await confirm('永久删除用户');await modal('确认永久删除');button('取消',dialog()).click();
    await waitFor(()=>!remove.disabled,'取消第二次删除确认未解锁');assert.ok(!writes.some(item=>item.path==='/users/alice/purge'),'取消第二次确认仍删除');
    failure='删除条件已变化';remove.click();await confirm('永久删除用户');await confirm('确认永久删除');
    await waitFor(()=>!remove.disabled&&[...document.querySelectorAll('.a-toast.error')].some(item=>item.textContent.includes(failure)),'删除失败无反馈');
    assert.ok(row('alice'),'删除失败移除了账号');
    failure='';held=true;release=null;remove.click();await confirm('永久删除用户');await confirm('确认永久删除');
    await waitFor(()=>release,'删除未提交');const purgeCount=writes.filter(item=>item.path.endsWith('/purge')).length;
    remove.click();assert.equal(writes.filter(item=>item.path.endsWith('/purge')).length,purgeCount,'重复删除请求');
    release();held=false;await waitFor(()=>!row('alice'),'删除后账号仍在列表');fits();
    users.push({id:'alice',username:'alice',role:'user',permissions:['agents.read']});
    button('刷新', document.querySelector('.a-top')).click(); await waitFor(()=>row('alice'),'同一测试标识未重新载入');
    await checkDeletedDraft();
    await testNativeInvitations();
  }finally{
    window.dispatchEvent(new Event('pagehide'));globalThis.fetch=nativeFetch;
  }
}
