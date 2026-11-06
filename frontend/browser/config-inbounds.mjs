import { assert, pause, waitFor, createConfigFixture as fixture } from './config-fixture.mjs';
import { testConfigOutboundsRuntime } from './config-outbounds.mjs';
import { testCommonConfigRuntime } from './config-common.mjs';
import { testWireGuardConfigRuntime } from './config-wireguard.mjs';
import { testPresetFieldLayoutRuntime } from './preset-layout.mjs';
import { testInboundDeploymentRuntime } from './config-deployment.mjs';

const editor = () => [...document.querySelectorAll('dialog[open] form')].at(-1);
const ready = () => waitFor(() => editor()?.elements.tag && !document.querySelector('[aria-busy=true]'), 'actual analytics inbound editor did not load');
const close = test => test.topDialog()?.querySelector('[aria-label="关闭"]').click();

export async function testConfigInboundsRuntime(preview = false) {
  if (preview) {
    const params = new URLSearchParams(location.search);
    window.inboundFixture = await fixture(params.get('engine') || 'xray', {missing:params.has('missing'),protocol:params.get('protocol')});
    if (params.has('modal')) window.inboundFixture.click('add');
    if (params.has('modify')) { window.inboundFixture.select('second'); window.inboundFixture.click('modify'); }
    return;
  }
  await testInboundDeploymentRuntime();
  await testWireGuardConfigRuntime(); await testPresetFieldLayoutRuntime(); await testNodeConfigHistory();
  await testInboundProtocolChanges();
  for (const engine of ['xray','sing-box','mihomo','ss-rust']) {
    const test = await fixture(engine); test.click('modify'); await ready();
    const form = editor(), source = form.elements.credential?.value;
    form.elements.port.value = '23456'; test.portConflict = 'port'; test.confirm=true; test.submit('deploy');
    await waitFor(() => form.textContent.includes('草稿已保留') && !form.querySelector('[type=submit]').disabled, 'correctable port conflict froze the native editor');
    assert(!test.writes.length && form.elements.port.value === '23456' && document.activeElement === form.elements.port, 'port conflict saved, discarded or did not focus the draft');
    assert(!form.elements.credential || form.elements.credential.value === source, 'port conflict replaced authentication draft');
    assert(!form.textContent.includes('重新读取并核对'), 'port conflict forced destructive reload');
    test.portConflict = ''; form.elements.port.value = '23457'; test.submit('deploy'); await test.settle(1);
    assert(test.writes[0].input.port === 23457 && test.writes[0].expected_version === 1, 'corrected port did not save with original CAS');
    test.dispose();
  }
  for (const [engine, protocol, field] of [['sing-box','vless-ws-tls','tls_public_port'], ['xray','vless-xhttp-five','xhttp_backend_port'], ['xray','vless-xhttp-five','xhttp_tls_port']]) {
    const test = await fixture(engine, {protocol}); test.click('modify'); await ready();
    const form = editor(), target = form.elements[field]; assert(target, `${protocol} lacks ${field}`);
    if (form.elements.tls_server_name && !form.elements.tls_server_name.value) form.elements.tls_server_name.value = 'tls.example.test';
    const panel = target.closest('details'); if (panel) panel.open = false;
    test.portConflict = field; test.confirm=true; test.submit('deploy');
    await waitFor(() => form.textContent.includes('草稿已保留') && !form.querySelector('[type=submit]').disabled, `${field} conflict blocked correction`);
    assert(!test.writes.length && document.activeElement === target && (!panel || panel.open), `${field} was not revealed and focused`);
    test.portConflict = ''; target.value = '23458'; test.submit('deploy'); await test.settle(1);
    assert(test.writes[0].input[field] === 23458, `${field} correction was not submitted`); test.dispose();
  }
  for (const engine of ['xray','sing-box','mihomo','ss-rust']) {
    const test = await fixture(engine);
    test.select('second');
    const source = document.querySelector('.a-main [name=content]').value;
    assert(test.row('second').textContent.includes('访问限制'), 'native inbound has no policy entry');
    for (const [index,label] of ['停用','启用'].entries()) {
      test.clickButton(label,test.row('second')); await waitFor(()=>editor()?.elements.intent,'managed state editor missing');
      const form=editor(); test.confirm=true; form.requestSubmit(); form.requestSubmit(); await test.settle(index+1);
      assert(test.writes[index].version===index+1 && test.writes[index].disabled.length===(index===0?1:0),'managed state lost CAS or target');
      assert(document.querySelector('.a-main [name=content]').value===source,'state mutation rewrote complete source');
    }
    test.click('modify'); await ready(); let form=editor();
    assert(form.elements.tag.value==='second'&&form.elements.port.value==='21002','modify targeted another inbound');
    assert(!form.elements.protocol.disabled,'modify protocol selector is locked');
    form.elements.tag.value='renamed';form.elements.tag.dispatchEvent(new Event('input',{bubbles:true}));
    test.confirm=false;close(test); await pause(); assert(editor()===form&&form.elements.tag.value==='renamed','canceling close lost draft');
    form.elements.port.value='23002';test.confirm=true;test.submit('deploy');await test.settle(3);
    assert(test.writes[2].original_tag==='second'&&test.writes[2].expected_version===3&&!test.writes[2].install_if_missing,'modify lost identity/version/installation boundary');
    test.click('add');await ready();form=editor();
    assert(form.elements.tag.value!=='renamed','new inbound reused saved identity');
    let release;test.gate=new Promise(resolve=>{release=resolve;});test.submit('deploy');form.requestSubmit();await pause();
    assert(form.getAttribute('aria-busy')==='true'&&form.querySelector('fieldset').disabled,'pending write did not lock form');
    close(test);await pause();assert(editor()===form,'pending write closed its modal');release();await test.settle(4);test.gate=null;
    const added=test.writes[3].input.tag;assert(test.writes[3].operation==='add'&&test.writes[3].original_tag==='','add copied original target or submitted twice');
    test.select(added);test.click('delete');await waitFor(()=>editor()?.querySelector('button[type=submit]')?.textContent==='保存并部署','delete editor missing');
    assert(test.topDialog().textContent.includes(added),'delete names another target');close(test);await pause();assert(test.writes.length===4,'canceling deletion wrote data');
    test.click('delete');await waitFor(()=>editor()?.elements.intent,'delete did not reopen');test.submit('deploy');await test.settle(5);
    assert(test.writes[4].original_tag===added&&Object.keys(test.writes[4].input).sort().join(',')==='port,tag','delete lost strict selected identity');
    assert(!test.row(added),'deleted inbound retained usable action');
    const input=document.querySelector('.a-main [name=content]'), baseline=input.value;input.value+='\n ';input.dispatchEvent(new Event('input',{bubbles:true}));
    test.confirm=false;test.click('add');await pause();assert(!editor()&&input.value===baseline+'\n ','opening editor silently replaced source draft');
    input.value=baseline;input.dispatchEvent(new Event('input',{bubbles:true}));
    test.click('add');await ready();form=editor();form.elements.tag.value='keep-my-draft';test.fail=true;test.confirm=true;test.submit('deploy');
    await waitFor(()=>form.textContent.includes('保存结果需重新核对'),'conflict recovery state missing');
    assert(form.elements.tag.value==='keep-my-draft'&&form.querySelector('button[type=submit]').disabled,'conflict lost draft or permits stale resubmission');
    test.dispose();assert(!document.querySelector('dialog[open]'),'session disposal left an orphan editor');
  }
  for (const status of [200,408]) for (const kind of ['modify','delete']) {
    const test=await fixture('xray');test.click(kind);await waitFor(()=>editor()?.elements.intent,'interrupted form missing');const form=editor();
    if(form.elements.tag)form.elements.tag.value='interrupted-draft';test.mutationFailure={status};test.confirm=true;test.submit('deploy');
    await waitFor(()=>form.textContent.includes('保存结果需重新核对'),'uncertain result not identified');
    assert(!test.writes.length&&form.querySelector('button[type=submit]').disabled,'uncertain write permits duplicate submit');
    assert(!form.elements.tag||form.elements.tag.value==='interrupted-draft','uncertain result lost draft');test.dispose();
  }
  for (const options of [{missing:true},{missing:true,legacy:true},{missing:true,shared:true},{missing:true,offline:true}]) {
    const test=await fixture('xray',options);test.click('add');await ready();const submit=editor().querySelector('button[type=submit]');
    const allowed=!options.legacy&&!options.shared&&!options.offline;assert(submit.disabled!==allowed,'automatic installation permission/capability/offline boundary changed');
    if(allowed){test.confirm=true;test.submit('deploy');await test.settle(1);assert(test.writes[0].install_if_missing&&test.writes[0].intent==='deploy','missing core did not request controlled installation');assert(!test.calls.some(call=>call.path==='/tasks'),'installation requires a second browser task');}
    test.dispose();
  }
  const readonly=await fixture('xray',{readonly:true});assert(!readonly.click('add'),'readonly account has a mutation button');readonly.dispose();
  for (const [status,label] of [['inactive','已停止'],['unknown','状态未知']]) {
    const test=await fixture('xray',{serviceStatus:status});assert(document.querySelector('.a-main').textContent.includes(label),'service state label wrong');test.dispose();
  }
  const offline=await fixture('xray',{offline:true,serviceStatus:'active'});assert(document.querySelector('.a-main').textContent.includes('状态未知'),'offline cache presented as current running state');offline.dispose();
  const drift=await fixture('xray',{drift:true});drift.click('add');await ready();assert(!drift.writes.length,'opening saved source with runtime drift mutated it');drift.dispose();
  const background=await fixture('xray');background.confirm=true;background.click('add');await ready();background.submit('deploy');await background.settle(1);
  assert(!background.readRequests.length,'deploy required redundant Agent snapshot');background.select('first');background.click('delete');await waitFor(()=>editor()?.elements.intent,'background delete missing');
  const openForm=editor();background.taskStatus='succeeded';await new Promise(resolve=>setTimeout(resolve,3300));assert(editor()===openForm,'background deployment replaced current modal');background.dispose();
  const stale=await fixture('xray');let release;stale.workspaceGate=new Promise(resolve=>{release=resolve;});stale.click('add');stale.dispose();release();await pause();assert(!document.querySelector('dialog[open]')&&!stale.writes.length,'old workspace response escaped session');
  const narrow=await fixture('xray',{noFleet:true});assert(document.querySelector('.a-main [name=content]')&&!narrow.calls.some(call=>call.path==='/agents'),'configuration deep link requires fleet permission');narrow.dispose();
  for (const engine of ['mihomo','xray','sing-box','ss-rust']) for (const shared of [false,true]) {
    const native=await fixture(engine,{native:true,shared});
    assert(native.row('first').textContent.includes('修改')&&native.row('first').textContent.includes('访问限制'),'native listener lost policy/edit/delete');
    native.setAgentContent(native.saved().content.replaceAll('21001','23101'));
    native.clickButton('读取托管配置');
    await waitFor(()=>native.snapshotReads===1&&!native.root.hasAttribute('aria-busy'),'native reverse read failed');
    assert(native.row('first').textContent.includes('23101')&&!native.writes.length,'reverse read did not update inbound preview or silently saved');
    native.click('modify');await waitFor(()=>editor()?.elements.fragment,'native source editor missing');
    let form=editor();assert(!form.elements.protocol && form.querySelector('[type=submit]').textContent==='保存并部署','native object was converted into a preset');
    const value=JSON.parse(form.elements.fragment.value);assert((value.port||value.listen_port||value.server_port)===23101,'native editor used stale panel configuration');value.custom={options:['a','b'],nested:{enabled:true}};
    form.elements.fragment.value=JSON.stringify(value);form.elements.fragment.dispatchEvent(new Event('input',{bubbles:true}));
    native.confirm=false;close(native);await pause();assert(editor()===form,'native draft was discarded without confirmation');
    native.confirm=true;form.requestSubmit();await native.settle(1);
    assert(native.writes[0].original_tag==='first'&&native.writes[0].expected_version===1&&native.writes[0].snapshot_task_id?.startsWith('read-')&&JSON.parse(native.writes[0].fragment).custom.nested.enabled,'native edit lost custom fields or CAS');
    native.click('modify');await waitFor(()=>editor()?.elements.fragment,'native reread did not open');
    assert(JSON.parse(editor().elements.fragment.value).custom.options.length===2,'reverse read lost custom protocol options');
    close(native);await pause();native.click('delete');await waitFor(()=>editor()?.elements.intent,'native delete missing');native.submit('deploy');await native.settle(2);
    assert(native.writes[1].original_tag==='first','native deletion selected another resource');native.dispose();
  }
  const drafts=await fixture('xray');drafts.click('add');await ready();let form=editor(), selector=form.elements.protocol, original=selector.value, other=selector.options[1].value;
  form.elements.tag.value='protocol-specific-draft';form.elements.tag.dispatchEvent(new Event('input',{bubbles:true}));selector.value=other;selector.dispatchEvent(new Event('change',{bubbles:true}));await waitFor(()=>editor().elements.tag?.value!=='protocol-specific-draft'&&!editor().querySelector('button[type=submit]').disabled,'protocol switch failed');
  selector.value=original;selector.dispatchEvent(new Event('change',{bubbles:true}));await waitFor(()=>editor().elements.tag.value==='protocol-specific-draft','protocol draft was not restored');drafts.dispose();
  const refresh=await fixture('xray');refresh.click('add');await ready();refresh.failRefresh=true;refresh.confirm=true;refresh.submit('deploy');await refresh.settle(1);assert(document.querySelector('.a-main').textContent.includes('配置已保存且任务已提交，状态读取失败'),'durable mutation was reported failed after readback error');refresh.failRefresh=false;refresh.clickButton('重新读取状态');await waitFor(()=>![...document.querySelectorAll('.a-main .a-notice')].some(message=>message.checkVisibility()&&message.textContent.includes('配置已保存且任务已提交，状态读取失败')),'readback recovery missing');assert(refresh.writes.length===1,'readback recovery repeated mutation');refresh.dispose();
  await testConfigOutboundsRuntime();await testCommonConfigRuntime();
}

export async function testInboundProtocolChanges() {
  for (const engine of ['xray','sing-box','mihomo','ss-rust']) for (const shared of [false,true]) {
    const test=await fixture(engine,{protocol:'ss2022',shared});test.select('second');test.click('modify');await ready();
    const target=['xray','mihomo'].includes(engine)?'vmess':'shadowsocks';
    const password=target==='vmess'?'00000000-0000-4000-8000-000000000001':'retained-new-password';
    const form=editor(), selector=form.elements.protocol, original=selector.value, credential=form.elements.credential.value;
    const choose=key=>{selector.value=key;selector.dispatchEvent(new Event('change',{bubbles:true}));};
    const loaded=key=>waitFor(()=>selector.value===key&&form.elements.credential&&!form.querySelector('[type=submit]').disabled,'changed protocol did not load');
    assert(!selector.disabled&&[...selector.options].some(option=>option.value===target),'modify cannot select another supported protocol');
    if(engine==='sing-box')assert(![...selector.options].some(option=>option.value==='wireguard'),'native endpoint migration offered by inbound-only API');
    form.elements.tag.value='retained-tag';form.elements.port.value='23459';form.elements.port.dispatchEvent(new Event('input',{bubbles:true}));
    test.planError=true;choose(target);
    await waitFor(()=>form.textContent.includes('协议方案读取失败'),'protocol failure feedback missing');
    assert(selector.value===original&&form.elements.credential.value===credential&&form.elements.port.value==='23459'&&!form.querySelector('[type=submit]').disabled,'failed switch lost the active draft');
    test.planError=false;
    let release;test.planGate=new Promise(resolve=>{release=resolve;});choose(target);
    assert(form.querySelector('[type=submit]').disabled&&form.elements.port.matches(':disabled'),'loading protocol allows stale fields to submit or change');
    choose(original);await loaded(original);release();test.planGate=null;await pause();
    assert(selector.value===original&&form.elements.credential.value===credential,'late plan overwrote newer protocol selection');
    choose(target);await loaded(target);
    assert(form.elements.tag.value==='retained-tag'&&form.elements.port.value==='23459'&&form.elements.credential.value!==credential&&!form.elements.method?.value.startsWith('2022-'),'switch lost identity or reused incompatible authentication');
    form.elements.credential.value=password;form.elements.credential.dispatchEvent(new Event('input',{bubbles:true}));
    choose(original);await loaded(original);assert(form.elements.credential.value===credential,'original protocol draft lost its credential');
    choose(target);await loaded(target);assert(form.elements.credential.value===password,'new protocol draft was regenerated on return');
    test.confirm=false;test.clickButton('完整入站配置',test.topDialog());await pause();
    assert(editor()===form&&selector.value===target&&!test.writes.length,'canceling edit-mode switch discarded protocol draft');
    test.confirm=true;test.submit('deploy');await test.settle(1);
    const saved=test.writes[0];assert(saved.operation==='modify'&&saved.original_tag==='second'&&saved.expected_version===1&&!saved.install_if_missing&&saved.input.protocol===target&&saved.input.tag==='retained-tag'&&saved.input.port===23459&&saved.input.credential===password,'protocol mutation lost target, version or draft');
    test.dispose();
  }
  const offline=await fixture('xray',{offline:true,protocol:'ss2022'});offline.click('modify');await ready();
  let form=editor();form.elements.protocol.value='trojan';form.elements.protocol.dispatchEvent(new Event('change',{bubbles:true}));
  await waitFor(()=>form.elements.tls_enabled,'TLS protocol did not replace old fields');
  assert(form.elements.tls_enabled.checked&&!form.elements.method&&form.querySelector('[type=submit]').disabled&&!form.elements.protocol.disabled,'offline protocol switch lost TLS rules or enabled deployment');offline.dispose();
}

async function testNodeConfigHistory(){
  for(const engine of ['xray','sing-box','mihomo','ss-rust']){
    const test=await fixture(engine,{history:true,shared:true});test.click('history');await waitFor(()=>test.topDialog()?.querySelector('tbody tr'),'history missing');
    const rows=[...test.topDialog().querySelectorAll('tbody tr')];test.clickButton('预览',rows.at(-1));await waitFor(()=>test.topDialog()?.querySelector('pre'),'history preview missing');
    assert(!test.topDialog().querySelector('textarea'),'historical preview is editable');test.clickButton('恢复此档案',test.topDialog());await pause();assert(!test.restores.length,'canceled restore wrote data');
    test.confirm=true;test.restoreError=409;test.clickButton('恢复此档案',test.topDialog());await waitFor(()=>[...document.querySelectorAll('.a-toast[role=alert]')].some(message=>message.textContent.includes('冲突')),'restore conflict hidden');assert(test.saved().version===2,'conflict changed saved source');
    test.restoreError=403;test.clickButton('恢复此档案',test.topDialog());await pause();assert(test.saved().version===2,'authorization failure changed config');
    test.restoreError=null;let release;test.restoreGate=new Promise(resolve=>{release=resolve;});test.clickButton('恢复此档案',test.topDialog());test.clickButton('恢复此档案',test.topDialog());await pause();assert(test.topDialog()?.open,'pending restore closed early');release();await waitFor(()=>test.saved().version===3&&!test.topDialog(),'restore failed');
    assert(test.restores.length===1&&test.restores[0].expected_version===2&&test.revisions.size===3,'restore replaced history or used historical CAS');assert(!test.calls.some(call=>call.path==='/tasks'),'restore implicitly deployed');
    test.click('history');await waitFor(()=>test.topDialog()?.querySelector('tbody tr'),'restored history missing');test.clickButton('预览',test.topDialog().querySelector('tbody tr'));await waitFor(()=>test.topDialog()?.querySelector('pre'),'current archive preview missing');assert(![...test.topDialog().querySelectorAll('button')].some(button=>button.textContent==='恢复此档案'),'current revision permits duplicate restore');test.dispose();
  }
  const readonly=await fixture('xray',{history:true,readonly:true});readonly.click('history');await waitFor(()=>readonly.topDialog()?.querySelector('tbody tr'),'readonly history missing');readonly.clickButton('预览',[...readonly.topDialog().querySelectorAll('tbody tr')].at(-1));await waitFor(()=>readonly.topDialog()?.querySelector('pre'),'readonly preview missing');assert(![...readonly.topDialog().querySelectorAll('button')].some(button=>button.textContent==='恢复此档案'),'readonly restore exposed');readonly.dispose();
  const stale=await fixture('xray',{history:true});stale.click('history');await waitFor(()=>stale.topDialog()?.querySelector('tbody tr'),'stale history missing');let release;stale.revisionGate=new Promise(resolve=>{release=resolve;});stale.clickButton('预览',[...stale.topDialog().querySelectorAll('tbody tr')].at(-1));stale.dispose();const next=await fixture('sing-box');release();await pause();assert(!document.querySelector('dialog[open]'),'previous account preview entered new target');next.dispose();
}
