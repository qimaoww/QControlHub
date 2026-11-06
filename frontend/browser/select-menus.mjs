import { bindAnalyticsCertificateSelector } from '../analytics/certificate-selector.js';
import { patch } from '../analytics/live-dom.js';
import { node, field, button, stack, openDialog } from '../analytics/ui.js';
import { assert, waitFor, delay } from './assertions.mjs';

const paint = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
async function input(request) {
  window.__selectMenuInput = request;
  await waitFor(() => !window.__selectMenuInput, `native input did not complete: ${request.action || request.key}`);
  await paint();
}
const key = (key, keyCode, text) => input({key,keyCode,text});
const open = async select => {
  select.focus();
  await input({action:'open',selector:`[name="${select.name}"]`});
  await waitFor(() => select.matches(':open'), 'native picker did not open');
  await Promise.all(select.getAnimations({subtree:true}).filter(animation=>Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation=>animation.finished.catch(()=>{})));
  await paint();
};
async function click(element) {
  const box = element.getBoundingClientRect();
  await input({action:'click',point:{x:box.left+box.width/2,y:box.top+box.height/2}});
}

export async function testSelectMenusRuntime() {
  const service = field('服务','service','',{required:true,options:[['','请选择'],['alpha','Alpha']]});
  const select = service.querySelector('select');
  select.add(new Option('Disabled','disabled'));select.options[2].disabled=true;
  const offline=node('optgroup',{label:'离线',disabled:true},new Option('Offline','offline'));
  select.append(offline,node('optgroup',{label:'地区'},new Option('Beta','beta'),new Option('Gamma','gamma')));
  const hidden=new Option('Hidden','hidden');hidden.hidden=true;select.add(hidden);
  const form=node('form',{className:'a-stack'},service,node('button',{type:'submit',className:'a-button'},'保存'));
  const outside=button('其他操作',()=>{});
  const modalField=field('节点','node','0',{options:[]}), modalSelect=modalField.querySelector('select');
  for(let i=0;i<100;i++)modalSelect.add(new Option(`节点 ${i} ${'长名称'.repeat(12)}`,String(i)));
  const dialog=node('dialog',{className:'a-dialog'},node('div',{className:'a-dialog-body'},modalField));
  document.body.replaceChildren(node('main',{className:'a-main'},stack(form,outside)),dialog);
  let inputs=0,changes=0,submits=0;
  select.addEventListener('input',()=>inputs++);select.addEventListener('change',()=>changes++);
  form.addEventListener('submit',event=>{event.preventDefault();submits++});
  assert.ok(!form.checkValidity(),'empty required select must remain invalid');
  const fresh=form.cloneNode(true);
  await open(select);const pickerFocus=document.activeElement;patch(form,fresh);await paint();
  assert.ok(select.matches(':open')&&document.activeElement===pickerFocus,'refresh closed the picker or moved focus');
  assert.equal(getComputedStyle(hidden).display,'none','hidden option was exposed');
  assert.equal([...select.options].filter(option=>option.matches(':disabled')).length,2,'disabled option/group became selectable');
  await key('ArrowDown',40);await key('ArrowDown',40);
  assert.equal(document.activeElement.value,'beta','arrow key did not skip disabled options/groups');
  await key('Escape',27);
  assert.ok(!select.matches(':open')&&select.value===''&&changes===0,'Escape committed a tentative selection');
  await open(select);await key('b',66,'b');await key('Enter',13);
  assert.equal(select.value,'beta','typeahead selection failed');
  assert.ok(inputs===1&&changes===1,'selection must emit input/change exactly once');
  assert.equal(new FormData(form).get('service'),'beta','native form value was not preserved');
  form.requestSubmit();assert.equal(submits,1,'native control blocked valid submission');
  await open(select);await click([...select.options].find(option=>option.value==='beta'));
  assert.ok(changes===1&&!select.matches(':open'),'reselecting same value emitted change');
  await open(select);await key('End',35);await key('Tab',9);
  assert.equal(select.value,'gamma','Tab did not accept keyboard selection');
  form.reset();assert.ok(select.value===''&&!select.matches(':open')&&!form.checkValidity(),'form reset or required validation changed');
  select.disabled=true;select.click();assert.ok(!select.matches(':open'),'disabled control opened picker');select.disabled=false;
  await open(select);select.options[1].disabled=true;
  await key('ArrowDown',40);await key('Enter',13);
  assert.equal(select.value,'beta','dynamic disabled option remained selectable');
  await open(select);await input({action:'click',point:{x:innerWidth-4,y:innerHeight-4}});assert.ok(!select.matches(':open'),'outside pointer did not dismiss picker');
  await open(select);select.remove();await paint();assert.ok(!select.matches(':open'),'route removal leaked picker');

  const spacer=node('section',{},Array.from({length:80},()=>node('p',{},'滚动后的弹窗定位')));document.body.append(spacer);
  window.scrollTo({top:400,behavior:'instant'});await paint();assert.ok(scrollY>0,'scroll regression did not establish scrolled page');
  openDialog(dialog);await open(modalSelect);
  assert.ok(dialog.matches(':modal')&&modalSelect.matches(':open'),'native picker did not open above modal');
  const style=getComputedStyle(modalSelect,'::picker(select)');
  assert.ok(style.backgroundColor!=='rgba(0, 0, 0, 0)'&&getComputedStyle(document.documentElement).getPropertyValue('--paper').trim(),'picker lost current theme surface');
  assert.ok(parseFloat(style.maxHeight)<=Math.min(320,innerHeight/2)+1,'long option list has no bounded scrolling area');
  await key('End',35);
  const last=modalSelect.options[99],bounds=last.getBoundingClientRect();
  assert.equal(document.activeElement,last,'End did not focus last option');
  assert.ok(bounds.left>=-1&&bounds.right<=innerWidth+1&&bounds.top>=-1&&bounds.bottom<=innerHeight+1,'long option was not scrolled inside viewport');
  await key('Escape',27);assert.ok(dialog.open&&!modalSelect.matches(':open')&&modalSelect.value==='0','Escape changed value or closed parent dialog');
  await open(modalSelect);dialog.addEventListener('close',()=>dialog.remove(),{once:true});dialog.close();await paint();assert.ok(!modalSelect.matches(':open'),'closing modal leaked picker');spacer.remove();

  const domainField=field('域名','domain','',{required:true,suggestions:['alpha.example.test','beta.example.test']});
  form.append(domainField);const domain=form.elements.domain;
  domain.value='beta';domain.focus();const listID=domain.getAttribute('list');
  const freshInput=domain.cloneNode(true);patch(domain,freshInput);
  assert.equal(domain.value,'beta','refresh overwrote typed suggestion query');
  assert.equal(domain.getAttribute('list'),listID,'refresh lost datalist association');
  domain.value='';await input({action:'text',text:'beta'});await delay(300);
  await input({keys:['ArrowDown','Enter'],native:true});
  await waitFor(()=>domain.value==='beta.example.test','domain suggestion did not commit');
  domain.value='custom.example.test';domain.dispatchEvent(new Event('input',{bubbles:true}));
  assert.ok(domain.checkValidity()&&domain.value==='custom.example.test','suggestions restricted free text');
  domain.value='';await input({action:'open',selector:'[name="domain"]'});await input({key:'Escape',native:true});
  assert.equal(domain.value,'','Escape changed domain text');outside.focus();
  assert.equal(domain.getAttribute('list'),listID,'blur lost native datalist association');
  assert.ok(!domain.hasAttribute('role'),'blur leaked authored combobox attributes');
  await testCertificateSelectorStates();
}

async function testCertificateSelectorStates() {
  const controller=new AbortController();
  const form=node('form',{className:'a-stack'},field('证书','certificate_path','/etc/test.crt'),field('私钥','private_key_path','/etc/test.key'),field('SNI','tls_server_name',''));
  document.querySelector('main').append(form);
  let fail=false;
  const base={id:'crt_0000000000000001',name:'one.example.test',domains:['one.example.test'],source:'import',owned:true,version:1,not_before:new Date(Date.now()-86400000).toISOString(),not_after:new Date(Date.now()+86400000*90).toISOString(),status:'issued'};
  let items=[base,{...base,id:'crt_0000000000000002',name:'未签发',version:0,not_after:null},{...base,id:'crt_0000000000000003',name:'将生效',not_before:new Date(Date.now()+86400000).toISOString()},{...base,id:'crt_0000000000000004',name:'已过期',not_after:new Date(Date.now()-86400000).toISOString()},{...base,id:'crt_0000000000000005',owned:false,name:'多域名',domains:['*.example.test','two.example.test']}];
  const selector=bindAnalyticsCertificateSelector({api:async()=>{if(fail)throw Error('offline');return {items}},signal:controller.signal,current:()=>!controller.signal.aborted},{form,protocol:{supports_tls:true},plan:{}});
  const select=form.elements.panel_certificate_id,summary=form.querySelector('.a-notice'),retry=[...form.querySelectorAll('button')].find(button=>button.textContent==='重试');
  await waitFor(()=>select.options.length===6,'certificate loading did not settle');
  const refresh=async()=>{await selector.refresh();await paint()};
  assert.equal([...select.options].filter(option=>option.disabled).length,3,'pending/future/expired must be unavailable');
  assert.ok(!select.options[1].title.includes(base.domains[0]),'duplicate domain was not removed');
  assert.equal(select.options[5].parentElement.label,'授权给我的证书','shared certificate ownership lost');
  select.value=base.id;select.dispatchEvent(new Event('change',{bubbles:true}));
  assert.equal(form.elements.tls_server_name.value,base.domains[0],'single domain autofill');
  form.elements.tls_server_name.value='draft.example.test';form.elements.tls_server_name.dispatchEvent(new Event('input'));
  assert.ok(select.validationMessage.includes('不覆盖'),'domain mismatch accepted');
  select.scrollIntoView({block:'center',behavior:'instant'});await open(select);
  const sharedOption=select.options[5],before=sharedOption.getBoundingClientRect();
  assert.ok(sharedOption.title.includes('*.example.test')&&sharedOption.title.includes('有效')&&sharedOption.title.includes('到期'),'domain or certificate validity metadata missing');
  items=[{...base,status:'failed',binding_statuses:{synced:1,failed:1,pending:1}},...items.slice(1)];await refresh();
  assert.equal(form.elements.tls_server_name.value,'draft.example.test','refresh overwrote SNI draft');
  assert.ok(select.matches(':open')&&select.options[5]===sharedOption,'refresh replaced open menu options');
  assert.equal(sharedOption.getBoundingClientRect().top,before.top,'refresh lost menu scroll');
  await key('Escape',27);
  form.elements.tls_server_name.value=base.domains[0];form.elements.tls_server_name.dispatchEvent(new Event('input'));
  assert.ok(summary.textContent.includes('更新失败')&&summary.textContent.includes('部分完成'),'update and target states conflated');
  assert.ok(!select.options[1].disabled,'failed renewal disabled valid old pair');
  document.querySelector('button[data-outside]')?.focus();select.blur();
  items=[{...base,source:'node',sync_status:'blocked',binding_statuses:{pending:1}},...items.slice(1)];await refresh();
  assert.ok(summary.textContent.includes('来源更新受阻')&&summary.textContent.includes('等待同步'),'source collection and target deployment states conflated');
  assert.ok(!select.options[1].disabled,'blocked source disabled valid old pair');
  fail=true;await refresh();assert.ok(!retry.hidden&&summary.textContent.includes('加载失败'),'load failure recovery missing');
  fail=false;items=items.slice(1);await refresh();
  assert.equal(select.value,base.id,'revoked selection was silently reset');assert.ok(select.validationMessage.includes('撤销'),'revoked selection still valid');
  select.value='crt_0000000000000005';form.elements.tls_server_name.value='';select.dispatchEvent(new Event('change',{bubbles:true}));
  assert.equal(form.elements.tls_server_name.value,'','wildcard/multiple domains supplied invalid SNI');
  select.value='';select.dispatchEvent(new Event('change',{bubbles:true}));
  assert.equal(form.elements.certificate_path.value,'/etc/test.crt','local certificate path lost');assert.equal(form.elements.private_key_path.value,'/etc/test.key','local key path lost');
  controller.abort();form.remove();
}
