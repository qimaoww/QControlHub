import { assert, pause, waitFor, createConfigFixture as fixture } from './config-fixture.mjs';
import { testOutboundPresetsRuntime } from './config-outbound-presets.mjs';

export const outboundDialog = () => [...document.querySelectorAll('dialog[open]')].at(-1);
export const outboundForm = () => outboundDialog()?.querySelector('form:has([name=fragment])');
export const outboundInput = () => outboundForm()?.elements.fragment;
export async function openOutbound(test, operation, wait = true) {
  test.clickButton('出站', test.row('first'));
  const chooser=outboundDialog(), label={add:'添加并绑定',bind:'绑定已有出站',modify:'修改绑定出站',delete:'删除绑定出站'}[operation];
  test.clickButton(label,chooser);
  if(wait)await waitFor(outboundInput, `${operation} outbound editor missing`);
}
const alert = () => [...document.querySelectorAll('[role=alert],.a-notice')].filter(item=>item.checkVisibility()).map(item=>item.textContent).join(' ');
export async function testConfigOutboundsRuntime() {
  for (const {engine,protocol} of [{engine:'xray'},{engine:'sing-box'},{engine:'xray',protocol:'wireguard'},{engine:'sing-box',protocol:'wireguard'}]) {
    const test=await fixture(engine,{drift:true,protocol}),section=engine==='sing-box'&&protocol==='wireguard'?'endpoints':'inbounds';
    const listeners=JSON.parse(test.saved().content)[section];
    test.clickButton('出站',test.row('first'));const chooser=outboundDialog();
    assert([...chooser.querySelectorAll('button')].filter(button=>button.disabled).length===2,'unbound inbound allows modifying another exit');
    chooser.querySelector('[aria-label=关闭]').click();
    let release;test.workspaceGate=new Promise(resolve=>{release=resolve;});await openOutbound(test,'add',false);
    assert(outboundDialog().textContent.includes('正在读取'),'outbound read has no immediate feedback');
    outboundDialog().querySelector('[aria-label=关闭]').click();release();test.workspaceGate=null;await pause();
    assert(!outboundDialog(),'late read reopened canceled editor');
    await openOutbound(test,'add');let form=outboundForm();
    assert(form.textContent.includes('first')&&form.textContent.includes('0x51435209'),'outbound binding identity/mark missing');
    assert(!outboundDialog().querySelector('form form'),'nested forms break submission ownership');
    const mode=form.querySelector('[data-outbound-mode]');mode.value='json';mode.dispatchEvent(new Event('change',{bubbles:true}));
    outboundInput().value=outboundInput().value.replace('exit-21001','exit-a');outboundInput().dispatchEvent(new Event('input',{bubbles:true}));
    outboundDialog().querySelector('[aria-label=关闭]').click();await pause();assert(outboundInput()?.value.includes('exit-a'),'canceling close discarded draft');
    test.confirm=true;test.gate=new Promise(resolve=>{release=resolve;});test.submit('deploy');test.submit('deploy');await pause();
    assert(form.getAttribute('aria-busy')==='true'&&form.querySelector('fieldset').disabled,'pending outbound save lacks lock');
    outboundDialog().dispatchEvent(new Event('cancel',{cancelable:true}));assert(outboundDialog(),'pending outbound write closed');
    release();await test.settle(1);test.gate=null;
    const routeKey=engine==='xray'?'routing':'route',targetKey=engine==='xray'?'outboundTag':'outbound';
    const root=()=>JSON.parse(test.saved().content),binding=tag=>root()[routeKey].rules.some(rule=>rule[targetKey]===tag);
    assert(test.writes[0].path.endsWith('/source')&&binding('exit-a'),'atomic source save lost inbound binding');
    await openOutbound(test,'modify');outboundInput().value=outboundInput().value.replace('exit-a','exit-b');test.submit('deploy');await test.settle(2);
    assert(!binding('exit-a')&&binding('exit-b'),'rename failed to update current inbound route');
    await openOutbound(test,'bind');form=outboundForm();assert(outboundInput().readOnly,'existing binding preview is editable');
    form.elements.tag.value='direct';form.elements.tag.dispatchEvent(new Event('change',{bubbles:true}));assert(outboundInput().value.includes('direct'),'binding preview does not follow selected exit');
    test.confirm=false;test.submit('deploy');await pause();assert(test.writes.length===2&&outboundForm(),'canceling rebinding wrote data');
    await waitFor(()=>!form.hasAttribute('aria-busy')&&!form.querySelector('[type=submit]').disabled,'canceled rebinding did not release its form');
    test.confirm=true;test.submit('deploy');await test.settle(3);await openOutbound(test,'modify');test.submit('deploy');
    await waitFor(()=>alert().includes('共享出口'),'default/shared exit was not protected');assert(test.writes.length===3,'default exit changed through one inbound');
    outboundDialog().querySelector('[aria-label=关闭]').click();await pause();
    await openOutbound(test,'bind');outboundForm().elements.tag.value='exit-b';outboundForm().elements.tag.dispatchEvent(new Event('change',{bubbles:true}));test.submit('deploy');await test.settle(4);
    await openOutbound(test,'delete');assert(outboundInput().readOnly&&outboundInput().value.includes('exit-b')&&outboundForm().querySelector('button[type=submit]').classList.contains('danger'),'delete lacks readonly preview/danger action');
    test.confirm=false;test.submit('deploy');await pause();assert(test.writes.length===4&&outboundForm(),'canceling delete wrote data');
    await waitFor(()=>!outboundForm().hasAttribute('aria-busy')&&!outboundForm().querySelector('[type=submit]').disabled,'canceled deletion did not release its form');
    test.confirm=true;test.submit('deploy');await test.settle(5);
    assert(!root().outbounds.some(entry=>entry.tag==='exit-b')&&!root()[routeKey].rules.length&&JSON.stringify(root()[section])===JSON.stringify(listeners),'delete left binding or modified another inbound');
    test.dispose();
  }
  for(const status of [200,400,408,409,503,undefined]){
    const test=await fixture('xray');await openOutbound(test,'add');const form=outboundForm(),mode=form.querySelector('[data-outbound-mode]');mode.value='json';mode.dispatchEvent(new Event('change',{bubbles:true}));
    if(status===200){
      outboundInput().value='{"tag":';test.confirm=true;test.submit('deploy');await waitFor(()=>form.textContent.includes('JSON 格式无效'),'invalid JSON has no Chinese correction feedback');
      assert(!test.writes.length&&outboundInput().value==='{"tag":','invalid JSON was submitted or discarded');
      outboundInput().value='{"tag":"exit-21001","protocol":"freedom"}';
    }
    outboundInput().value=outboundInput().value.replace('exit-21001','retained-draft');outboundInput().dispatchEvent(new Event('input',{bubbles:true}));test.sourceFailure={status};test.confirm=true;test.submit('deploy');
    await waitFor(()=>form.getAttribute('aria-busy')!=='true'&&alert(),'failed save has no feedback');
    assert(outboundInput().value.includes('retained-draft')&&!form.querySelector('fieldset').disabled&&!test.writes.length,'failed save lost draft or left inputs locked');
    assert(form.querySelector('button[type=submit]').disabled===(status!==400),'uncertain source result permits duplicate submit');test.dispose();
  }
  const late=await fixture('xray');let release;late.workspaceGate=new Promise(resolve=>{release=resolve;});await openOutbound(late,'add',false);late.dispose();release();await pause();assert(!outboundDialog()&&!late.writes.length,'outbound read escaped session');
  for(const options of [{readonly:true},{noTasks:true},{import:true}]){
    const test=await fixture('xray',options);assert(![...document.querySelectorAll('.a-main button')].some(button=>button.textContent==='出站'),'unauthorized/import source exposes outbound mutation');test.dispose();
  }
  await testOutboundPresetsRuntime();
}
