import { assert, delay, waitFor } from './assertions.mjs';
import { startNativeNodes } from './agent-detail-actions.mjs';
import { currentTheme } from '../modules/theme-lifecycle.js';
import { patch } from '../analytics/live-dom.js';
import { node, field, stack, button, openDialog } from '../analytics/ui.js';
import { routeModuleNames } from '../modules/routes.js';

const animations=()=>document.getAnimations().filter(a=>a.id.startsWith('analytics-'));
const settle=async()=>{await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));await waitFor(()=>!animations().length,'新主题动效未结束');};
const btn=(text,root=document)=>[...root.querySelectorAll('button')].find(b=>b.textContent===text);
const modal=()=>[...document.querySelectorAll('dialog[open]')].at(-1);
const emit=(input,type='input')=>input.dispatchEvent(new Event(type,{bubbles:true}));
const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
const ready=()=>!document.querySelector('.a-main > [aria-busy=true]');
async function route(hash,selector){location.hash=hash;await waitFor(()=>document.querySelector('.a-nav-link[aria-current=page]')?.hash===hash&&ready()&&document.querySelector(selector),`页面未完成 ${hash}`);return document.querySelector(selector);}
const fits=()=>assert.ok(document.documentElement.scrollWidth<=innerWidth+1,'页面超出视口');

export async function testNativeMotion(fixture,preview){
  const {mode,testAPI}=fixture,reduced=mode.endsWith('reduced');
  await startNativeNodes();if(preview)await new Promise(()=>{});
  assert.equal(matchMedia('(prefers-reduced-motion:reduce)').matches,reduced);
  if(reduced)assert.equal(animations().length,0,'减少动态效果偏好未生效');
  const shell=document.querySelector('.a-layout'),rail=shell.querySelector('.a-rail'),main=document.querySelector('.a-main');
  const originalFetch=window.fetch;let release,started=false,oldRouteSignal;
  window.fetch=async(input,options)=>{if(new URL(String(input),location.href).pathname==='/api/v1/traffic-policies'){started=true;oldRouteSignal=options.signal;await new Promise(resolve=>{release=resolve;});}return originalFetch(input,options);};
  try{
    location.hash='#traffic';await waitFor(()=>started,'缓慢路由未开始读取');
    await route('#node-settings','.a-probe-grid');assert.ok(oldRouteSignal.aborted,'离开页面未取消旧读取');release();await delay(30);
    assert.ok(document.querySelector('.a-probe-grid')&&!document.querySelector('.a-traffic-page'),'过期路由响应覆盖新页面');
    assert.equal(document.querySelector('.a-main'),main,'路由切换替换主体容器');
    assert.equal(document.querySelector('.a-rail'),rail,'路由切换替换导航');
  }finally{release?.();window.fetch=originalFetch;}
  const form=await route('#settings','#panel-settings-form');await settle();
  const input=form.elements.panel_name;input.value='未提交的设置';emit(input);input.focus();input.setSelectionRange(1,4);
  const scroll=scrollY,oldForm=form;btn('刷新',document.querySelector('.a-top')).click();await delay(40);
  assert.equal(document.querySelector('#panel-settings-form'),oldForm);assert.equal(input.value,'未提交的设置');
  assert.equal(document.activeElement,input);assert.equal(input.selectionStart,1);assert.equal(input.selectionEnd,4);assert.equal(scrollY,scroll);
  assert.equal(form.getAnimations().length,0,'局部刷新重播进入动画');
  const before=input.getBoundingClientRect();input.blur();input.focus();const after=input.getBoundingClientRect();assert.equal(before.top,after.top);assert.equal(before.left,after.left);
  testAPI.settingsGate=new Promise(resolve=>{release=resolve;});form.requestSubmit();
  await waitFor(()=>form.getAttribute('aria-busy')==='true','设置未进入提交状态');
  const writes=testAPI.calls.filter(c=>c.path==='/settings'&&c.method==='PUT').length;form.requestSubmit();
  location.hash='#node-settings';await waitFor(()=>location.hash==='#settings','提交中仍允许离开当前页面');
  assert.equal(document.querySelector('#panel-settings-form'),form,'提交中导航替换当前表单');
  input.value='提交后的新输入';emit(input);btn('刷新',document.querySelector('.a-top')).click();
  assert.equal(testAPI.calls.filter(c=>c.path==='/settings'&&c.method==='PUT').length,writes,'重复设置写入');
  release();testAPI.settingsGate=null;await waitFor(()=>!form.hasAttribute('aria-busy'),'设置提交未解锁');
  assert.equal(input.value,'提交后的新输入');assert.ok(!document.querySelector('[form=panel-settings-form]').disabled,'保存吞掉后续输入');
  input.value=testAPI.settings.panel_name;emit(input);
  // Reconciliation keeps unsaved controls, text ranges and disclosure state.
  const host=stack(field('草稿','draft','原值',{multiline:4}),node('details',{className:'a-disclosure',open:true},node('summary',{},'详情'),node('div',{className:'a-disclosure-body'},'原结果')));
  main.append(host);const editor=host.querySelector('textarea'),details=host.querySelector('details');editor.value='未保存文本';editor.focus();editor.setSelectionRange(2,5);
  const fresh=host.cloneNode(true);fresh.querySelector('.a-disclosure-body').textContent='更新结果';patch(host,fresh);
  assert.equal(editor.value,'未保存文本');assert.equal(editor.selectionStart,2);assert.equal(editor.selectionEnd,5);assert.ok(details.open);host.remove();
  const scope=currentTheme('analytics'),temporary=node('div',{},'临时内容');document.body.append(temporary);scope.motion.list(temporary);temporary.remove();await delay(20);assert.equal(temporary.getAnimations().length,0,'移除内容遗留动画');
  await settle();fits();
  document.querySelector('[title=退出登录]').click();const login=await waitFor(()=>document.querySelector('form[aria-label=登录控制台]'),'退出后未显示登录');
  login.elements.username.value='motion-test';login.elements.token.value='中文密码';assert.ok(login.checkValidity(),'登录新增了字符长度限制');
  login.elements.token.value='fixture-password-123';
  window.fetch=async(input,options)=>String(input).endsWith('/auth/login')?new Response(JSON.stringify({error:'账号或密码不正确'}),{status:401,headers:{'Content-Type':'application/json'}}):originalFetch(input,options);
  try{login.requestSubmit();await waitFor(()=>login.textContent.includes('账号或密码不正确'),'登录失败未反馈');
    assert.equal(document.querySelector('form[aria-label=登录控制台]'),login);assert.equal(login.elements.username.value,'motion-test');assert.equal(login.elements.token.value,'fixture-password-123');
    await waitFor(()=>!login.querySelector('button[type=submit]').disabled,'登录失败未解锁');
  }finally{window.fetch=originalFetch;}
  await testNativeAccountReplacement(testAPI,originalFetch);
  await settle();if(reduced)assert.equal(document.getAnimations().length,0);
}

async function testNativeAccountReplacement(testAPI,originalFetch){
  let sequence=0;
  const signIn=async()=>{
    const login=document.querySelector('form[aria-label=登录控制台]');
    login.elements.username.value=`replacement-${++sequence}`;login.elements.token.value='fixture-password-123';
    login.requestSubmit();
    await waitFor(()=>document.querySelector('#panel-settings-form')&&!document.querySelector('#app[aria-busy=true]'),'新账号没有进入原生设置页');
  };
  let armed=false,started=false,decoded=false,complete,oldSignal;
  window.fetch=async(input,options)=>{
    const path=new URL(String(input),location.href).pathname;
    if(path==='/api/v1/auth/login')return Response.json({role:'admin',user_id:`replacement-${sequence}`,username:`replacement-${sequence}`,csrf_token:'browser-test-csrf'});
    if(path==='/api/v1/preferences/theme')return Response.json({theme_id:'analytics'});
    if(armed&&path==='/api/v1/settings'&&(!options?.method||options.method==='GET')){
      armed=false;started=true;oldSignal=options.signal;return new Promise(resolve=>{complete=resolve;});
    }
    return originalFetch(input,options);
  };
  try{
    await signIn();
    for(const phase of ['success','unauthorized','body']){
      started=decoded=false;armed=true;btn('刷新',document.querySelector('.a-top')).click();
      await waitFor(()=>started,'旧账号刷新没有开始');
      if(phase==='body'){
        const resolve=complete;
        resolve({ok:true,status:200,json:()=>{decoded=true;return new Promise(done=>{complete=done;});}});
        await waitFor(()=>decoded,'未进入旧账号慢速正文解析');
      }
      document.querySelector('[title=退出登录]').click();
      await waitFor(()=>document.querySelector('form[aria-label=登录控制台]')&&!document.querySelector('#app[aria-busy=true]'),'旧账号退出失败');
      assert.ok(oldSignal.aborted,'退出后旧页面的读取没有取消');
      await signIn();
      const form=document.querySelector('#panel-settings-form'),input=form.elements.panel_name;
      input.value=`新账号草稿-${phase}`;emit(input);input.focus();input.setSelectionRange(1,4);
      const result={...testAPI.settings,panel_name:'旧账号私有设置',ui_font_scale:135};
      complete(phase==='body'?result:Response.json(result,{status:phase==='unauthorized'?401:200}));
      await delay(30);
      assert.equal(document.querySelector('#panel-settings-form'),form,'旧账号结果替换新账号页面或触发退出');
      assert.equal(input.value,`新账号草稿-${phase}`,'旧账号结果覆盖新账号草稿');
      assert.equal(document.activeElement,input);assert.equal(input.selectionStart,1);assert.equal(input.selectionEnd,4);
      assert.equal(document.documentElement.style.fontSize,'100%','旧账号设置污染新账号外观');
      assert.ok(document.querySelector('.a-user').textContent.includes(`replacement-${sequence}`),'新账号被旧响应替换');
      input.value=testAPI.settings.panel_name;emit(input);
    }
  }finally{armed=false;complete?.(Response.json({}));window.fetch=originalFetch;}
}

export async function testNativePopups(fixture,preview){
  await startNativeNodes();const reduced=fixture.mode.endsWith('reduced'),scope=currentTheme('analytics');await settle();
  const launcher=btn('添加节点');assert.ok(launcher,'缺少节点接入入口');
  if(preview){launcher.click();await new Promise(()=>{});}
  const background=document.querySelector('.a-main'),previousScroll=scrollY;
  for(let i=0;i<4;i++){
    launcher.focus();launcher.click();const box=await waitFor(()=>modal()?.querySelector('input[name=name]')&&modal(),'接入弹窗未打开');
    assert.ok(box.contains(document.activeElement),'弹窗未立即取得焦点');background.focus();assert.ok(box.contains(document.activeElement),'背景可抢夺模态焦点');
    const rect=box.getBoundingClientRect();assert.ok(rect.left>=-1&&rect.right<=innerWidth+1&&rect.top>=-1&&rect.bottom<=innerHeight+1);
    box.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,clientX:rect.left+8,clientY:rect.top+8}));box.dispatchEvent(new MouseEvent('click',{bubbles:true,clientX:-10,clientY:-10}));
    assert.ok(box.open,'从内容拖到背景错误关闭弹窗');
    const padding={clientX:rect.left+2,clientY:rect.top+2};box.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,...padding}));box.dispatchEvent(new MouseEvent('click',{bubbles:true,...padding}));assert.ok(box.open,'点击弹窗内边距关闭');
    if(i%2){box.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,clientX:-10,clientY:-10}));box.dispatchEvent(new MouseEvent('click',{bubbles:true,clientX:-10,clientY:-10}));}
    else {const event=new Event('cancel',{cancelable:true});if(box.dispatchEvent(event))box.close();}
    await waitFor(()=>!box.isConnected,'关闭弹窗未释放');assert.equal(document.activeElement,launcher,'关闭未恢复焦点');assert.equal(scrollY,previousScroll,'弹窗开关改变阅读位置');
    const tails=[...document.querySelectorAll('.a-modal-tail')];assert.ok(tails.every(t=>t.inert&&t.getAttribute('aria-hidden')==='true'),'关闭动效仍可交互');
    if(reduced)assert.equal(tails.length,0,'减少动态效果仍保留退出图层');
    await settle();assert.equal(document.querySelector('.a-modal-tail'),null,'退出动效未清理');
  }
  // Presentation retirement preserves current values and scroll paint, then
  // releases the whole surface even when finish events are lost.
  const select=field('选项','choice','one',{options:[['one','已保存'],['two','未保存']]});
  const multiple=node('select',{multiple:true},node('option',{value:'a'},'a'),node('option',{value:'b'},'b'),node('option',{value:'c'},'c'));
  const draft=field('文本','draft','',{multiline:4}),close=button('关闭',()=>box.close());
  const box=node('dialog',{className:'a-dialog'},node('div',{className:'a-dialog-head'},node('h2',{},'正在编辑')),node('div',{className:'a-dialog-body'},stack(select,multiple,draft,close)));
  document.body.append(box);openDialog(box);
  if (!reduced) {
    assert.ok(box.getAnimations().some(animation => animation.id === 'analytics-dialog'), '弹窗首次显示前未准备入场状态');
    assert.ok(Number(getComputedStyle(box).opacity) < .05, '首次打开弹窗先完整显示再淡入');
  }
  select.querySelector('select').value='two';multiple.options[1].selected=multiple.options[2].selected=true;
  const editor=draft.querySelector('textarea');editor.value=Array.from({length:40},(_,i)=>`草稿 ${i}`).join('\n');editor.scrollTop=35;
  const scroll=editor.scrollTop;await frame();box.close();await frame();
  if(!reduced){const tail=await waitFor(()=>document.querySelector('.a-modal-tail'),'弹窗退出图层缺失');
    assert.equal(tail.querySelector('select').value,'two');assert.equal([...tail.querySelector('select[multiple]').selectedOptions].map(o=>o.value).join(','),'b,c','退出重置多选');
    assert.equal(tail.querySelector('textarea').value,editor.value);assert.equal(tail.querySelector('textarea').scrollTop,scroll,'退出重置草稿阅读位置');
    animations().forEach(a=>{a.onfinish=null;a.finish();});await waitFor(()=>!tail.isConnected,'缺失 finish 事件未在期限内清理');
  }box.remove();
  // Inline validation, tooltip refresh and conditional visibility use the
  // actual theme components, including their accessibility state.
  const hint=button('提示',()=>{}, {title:'键盘提示'}),required=field('必填','required','',{required:true}),form=node('form',{},required,node('button',{type:'submit'},'提交'));
  const conditional=stack(field('条件字段','conditional','保留的草稿'));conditional.hidden=true;
  const notice=node('p',{className:'a-notice',role:'alert'}),host=node('section',{className:'a-card'},hint,form,conditional,notice);document.body.append(host);
  hint.focus();const tip=await waitFor(()=>document.querySelector('.a-tooltip'),'焦点提示未出现');assert.equal(tip.textContent,'键盘提示');assert.ok(!hint.hasAttribute('title'),'提示与原生标题重复');
  hint.title='更新提示';await waitFor(()=>tip.textContent==='更新提示','刷新未更新现有提示');
  document.dispatchEvent(new Event('scroll'));assert.equal(document.querySelector('.a-tooltip'),tip,'无位移滚动消息造成提示闪烁');
  const input=required.querySelector('input');input.focus();assert.equal(document.querySelector('.a-tooltip'),null);assert.equal(hint.title,'更新提示');
  form.requestSubmit();const error=await waitFor(()=>host.querySelector('.a-validation'),'原生校验无可读反馈');assert.equal(document.activeElement,input);assert.equal(input.getAttribute('aria-errormessage'),error.id);
  input.value='未保存的有效值';input.setSelectionRange(2,5);emit(input);assert.ok(!error.isConnected);assert.equal(input.getAttribute('aria-invalid'),null);assert.equal(input.selectionStart,2);assert.equal(input.selectionEnd,5);
  scope.motion.visible(conditional,true,hint);await delay(35);const value=conditional.querySelector('input');value.focus();scope.motion.visible(conditional,false,hint);assert.ok(conditional.hidden);assert.equal(document.activeElement,hint);
  scope.motion.visible(conditional,true,hint);await settle();assert.equal(value.value,'保留的草稿');assert.equal(getComputedStyle(conditional).opacity,'1');
  notice.textContent='操作失败';await settle();const same=notice.firstChild;same.data='操作失败';await delay(20);assert.equal(notice.getAnimations().length,0,'同一反馈重播');
  host.remove();scope.cancel();assert.equal(document.querySelector('.a-motion-tail,.a-tooltip'),null,'取消生命周期遗留图层');fits();
}

export async function testNativeSelections(fixture,preview){
  fixture.testAPI.clientAccessEntries=[{agent_id:'alpha',agent_name:'ALPHA',engine:'mihomo',address:'alpha.example.test',profiles:[{tag:'alpha-entry',port:443,protocol:'http',profile:{format:'URI',uri:'http://alpha.example.test:443',fields:[]}}]}];
  await startNativeNodes();if(preview)await new Promise(()=>{});
  const reduced=fixture.mode.endsWith('reduced'),main=document.querySelector('.a-main'),rail=document.querySelector('.a-rail');
  // Sweep the same public routes and legacy aliases using the current native
  // shell. Specialized route suites retain API, permission and scope assertions.
  const playback=[];
  for(const name of Object.keys(routeModuleNames)){
    const before=location.hash;location.hash=`#${name}`;await delay(10);
    await waitFor(()=>ready()&&document.querySelector('.a-main')?.textContent.trim(),`空白路由 ${name}`);await settle();
    assert.equal(document.querySelector('.a-main'),main,`路由替换主体 ${name}`);assert.equal(document.querySelector('.a-rail'),rail,`路由替换导航 ${name}`);
    assert.equal(document.querySelector('dialog:modal,.a-modal-tail'),null,`路由遗留弹窗 ${name}`);fits();if(reduced)assert.equal(animations().length,0,`减少动态效果 ${name}`);
    playback.push({route:name,from:before,reduced});
  }
  await route('#traffic','.a-traffic-page');btn('更多筛选').click();const status=document.querySelector('[name=status]');status.focus();status.value='error';emit(status,'change');await settle();assert.equal(document.activeElement,status);assert.equal(document.querySelector('.a-main'),main);status.value='';emit(status,'change');
  await route('#client-access','.a-main input[name=search]');let search=main.querySelector('[name=search]');search.value='不存在的接入';search.focus();search.setSelectionRange(1,4);emit(search);await delay(25);assert.equal(document.activeElement,search);assert.equal(search.selectionStart,1);assert.equal(search.selectionEnd,4);assert.ok(main.textContent.includes('没有符合'));
  await route('#core-logs','input[name=q]');search=main.querySelector('[name=q]');search.value='阅读中的关键字';search.focus();search.setSelectionRange(2,5);emit(search);await delay(30);assert.equal(document.activeElement,search);assert.equal(search.selectionStart,2);assert.equal(search.selectionEnd,5);
  document.documentElement.dataset.motionCoveragePages=playback.length;window.__motionCoveragePlayback=playback;fits();await settle();
}
