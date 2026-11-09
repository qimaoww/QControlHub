import { assert, pause, waitFor, createConfigFixture } from "./config-fixture.mjs";

const deferred = () => {
  let resolve;
  const promise = new Promise(done=>{resolve=done;});
  return {promise, resolve};
};
const editor = () => document.querySelector('dialog[open] form:has([name=protocol])');
const notices = () => [...document.querySelectorAll('.a-main .a-notice,dialog[open] .a-notice')].filter(item=>item.checkVisibility()).map(item=>item.textContent).join(' ');
const ready = () => waitFor(()=>editor()?.elements.port&&!editor().querySelector('[type=submit]').disabled,'native preset editor not ready');

// Exercise accepted writes, rejected writes and recovery on the actual theme,
// including a shared user's workspace rather than the old classic form fixture.
export async function testPresetRecoveryRuntime() {
  for (const engine of ['xray','mihomo','sing-box','ss-rust']) for (const shared of [false,true]) {
    const test=await createConfigFixture(engine,{shared,cachedMissing:true});
    const auxiliary=deferred();
    test.confirm=true;test.taskStatus='succeeded';
    test.fieldGate=auxiliary.promise;test.historyGate=auxiliary.promise;
    try {
      test.select('second');test.click('modify');await ready();let form=editor();
      assert(form.elements.intent.value==='deploy' && !form.querySelector('[type=submit]').disabled && !form.querySelector('select[name=intent]'),'stale fleet cache disabled deployment or redundant choices remain');
      const generated=form.querySelectorAll('[data-regenerate]').length;
      test.topDialog().querySelector('[aria-label=关闭]').click();await pause();
      test.click('modify');await ready();form=editor();
      assert(form.querySelectorAll('[data-regenerate]').length===generated,'reopening duplicated generator controls');
      const credential=form.elements.credential?.value;
      form.elements.port.value='23002';form.elements.port.dispatchEvent(new Event('input',{bubbles:true}));
      test.mutationFailure={status:400};test.submit('deploy');
      await waitFor(()=>form.textContent.includes('测试保存响应中断')&&!form.querySelector('[type=submit]').disabled,'rejected save blocked retry or hid failure');
      assert(test.saved().version===1&&form.elements.port.value==='23002'&&(!form.elements.credential||form.elements.credential.value===credential),'rejected save changed source or lost credentials');
      test.mutationFailure=null;const pending=deferred();test.gate=pending.promise;
      const calls=test.calls.filter(call=>call.path.endsWith('/server-inbounds')).length;
      test.submit('deploy');await waitFor(()=>test.calls.filter(call=>call.path.endsWith('/server-inbounds')).length>calls,'save did not reach API');
      form.requestSubmit();await pause();
      assert(form.getAttribute('aria-busy')==='true'&&form.querySelector('fieldset').disabled,'pending save lacks immediate locking');
      assert(test.calls.filter(call=>call.path.endsWith('/server-inbounds')).length===calls+1,'pending save submitted twice');
      pending.resolve();await test.settle(1);test.gate=null;
      assert(test.saved().version===2&&test.writes[0].original_tag==='second'&&test.writes[0].expected_version===1,'accepted mutation lost target or CAS');
      assert(!test.calls.some(call=>call.path.includes('/fields/')||call.path.includes('/revisions')),'primary save waited for unopened auxiliary data');
      auxiliary.resolve();test.fieldGate=null;test.historyGate=null;

      const sourceForm=document.querySelector('.a-main form:has([name=content])');
      const draft=sourceForm.elements.content.value+'\n ';
      sourceForm.elements.content.value=draft;sourceForm.elements.content.dispatchEvent(new Event('input',{bubbles:true}));
      sourceForm.elements.intent.value='deploy';test.sourceFailure={status:400};sourceForm.requestSubmit();
      await waitFor(()=>sourceForm.textContent.includes('测试源码保存失败')&&!sourceForm.querySelector('[type=submit]').disabled,'atomic source rejection did not allow retry');
      assert(test.saved().version===2&&sourceForm.elements.content.value===draft,'failed atomic source submission saved or discarded draft');
      test.sourceFailure=null;sourceForm.requestSubmit();await test.settle(2);
      assert(test.writes[1].version===2&&test.writes[1].content===draft&&test.saved().version===3,'source retry lost current CAS or submitted content');

      test.select('first');test.click('modify');await ready();test.failRefresh=3;test.submit('deploy');await test.settle(3);
      assert(notices().includes('配置已保存且任务已提交，状态读取失败'),'accepted write reported failed after readback error');
      test.failRefresh=false;test.clickButton('重新读取状态');
      try {await waitFor(()=>!notices().includes('配置已保存且任务已提交，状态读取失败'),'accepted write recovery did not finish');}
      catch(error){throw new Error(`${error.message}; notices=${notices()}; busy=${test.root.hasAttribute('aria-busy')}; calls=${test.calls.slice(-5).map(call=>call.path).join(',')}`);}
      assert(test.writes.length===3&&test.saved().version===4,'readback recovery repeated accepted mutation');

      test.taskStatus='running';test.click('modify');await ready();test.submit('deploy');await test.settle(4);
      const version=test.saved().version;
      test.click('modify');await ready();form=editor();
      const target=form.elements.credential||form.elements.tag;
      target.value='unsaved-authentication-draft';target.dispatchEvent(new Event('input',{bubbles:true}));
      target.focus();target.setSelectionRange(2,8);
      test.failRefresh=true;
      await waitFor(()=>notices().includes('部署状态更新失败'),'failed deployment refresh was not reported');
      assert(editor()===form&&document.activeElement===target&&target.selectionStart===2&&target.selectionEnd===8,'failed deployment refresh remounted or blurred draft');
      assert(target.value==='unsaved-authentication-draft'&&test.saved().version===version&&test.writes.length===4,'failed refresh changed draft or submitted another write');
      test.failRefresh=false;test.taskStatus='succeeded';
      await waitFor(()=>[...document.querySelectorAll('.a-main .a-badge')].some(item=>item.textContent==='已部署'),'deployment result did not recover after failed refresh');
      assert(editor()===form&&target.value==='unsaved-authentication-draft'&&document.activeElement===target,'successful status recovery replaced ongoing draft');
      assert(test.writes.length===4&&test.saved().version===version,'status recovery changed saved config');
    } finally {auxiliary.resolve();test.dispose();}
  }
  for(const shared of [false,true]) {
    const test=await createConfigFixture('xray',{shared,readonly:true});
    try {assert(!test.click('add')&&!test.click('modify')&&!test.click('delete')&&!test.writes.length,'read-only account can mutate presets');}
    finally {test.dispose();}
  }
  await testSourceReceipts();
}

export async function testSourceReceipts() {
  for(const shared of [false,true]) for(const outcome of ['recovered','delayed','superseded','rejected','readback']) {
    const test=await createConfigFixture('xray',{shared});test.confirm=true;test.taskStatus='succeeded';
    try {
      const form=document.querySelector('.a-main form:has([name=content])'),input=form.elements.content;
      input.value+='\n ';input.dispatchEvent(new Event('input',{bubbles:true}));const draft=input.value;
      form.elements.intent.value='deploy';
      test.loseSourceResponse=['recovered','delayed','superseded'].includes(outcome);
      test.receiptUnavailable=outcome==='delayed';test.supersedeSourceResponse=outcome==='superseded';
      test.sourceFailure=outcome==='rejected'?{status:503}:null;test.failRefresh=outcome==='readback';
      form.requestSubmit();
      if(outcome==='recovered') {
        await test.settle(1);
        assert(test.saved().version===2&&input.value===draft&&!form.querySelector('[type=submit]').disabled,'lost response did not recover accepted source');
      } else {
        await waitFor(()=>notices().includes(outcome==='readback'?'配置已保存且任务已提交':'保存结果需重新核对')&&!form.hasAttribute('aria-busy'),'uncertain source did not freeze safely');
        assert(form.querySelector('[type=submit]').disabled&&input.value===draft,'uncertain source permits duplicate save or lost draft');
        const writes=test.writes.length;
        form.requestSubmit();await pause();assert(test.writes.length===writes,'uncertain source replayed mutation');
        assert(test.saved().version===(outcome==='superseded'?3:outcome==='rejected'?1:2),'source receipt associated wrong revision');
        test.receiptUnavailable=false;test.failRefresh=false;
        let delayed;
        const reads=test.calls.filter(call=>call.path.endsWith('/workspace')).length;
        if(outcome==='delayed'){delayed=deferred();test.workspaceGate=delayed.promise;}
        test.clickButton(outcome==='delayed'||outcome==='rejected'?'核对保存结果':'重新读取配置');
        if(outcome==='rejected') {
          await waitFor(()=>!test.root.hasAttribute('aria-busy'),'unconfirmed readback did not finish');
          assert(form.querySelector('[type=submit]').disabled&&test.saved().version===1,'missing task receipt unlocked a rejected transaction');
        } else {
          if(delayed){
            await waitFor(()=>test.calls.filter(call=>call.path.endsWith('/workspace')).length>reads,'manual receipt recovery did not read workspace');
            input.value=draft+'\n ';input.dispatchEvent(new Event('input',{bubbles:true}));
            delayed.resolve();test.workspaceGate=null;
            await waitFor(()=>!test.root.hasAttribute('aria-busy'),'draft change during recovery did not release lock');
            assert(input.value===draft+'\n '&&form.querySelector('[type=submit]').disabled,'late recovery overwrote a newer source draft');
            test.clickButton('核对保存结果');
          }
          await waitFor(()=>!form.querySelector('[type=submit]').disabled,'same-page source recovery did not unlock confirmed state');
          assert(test.writes.length===writes&&input.value===test.saved().content,'source recovery replayed write or retained wrong baseline');
          assert(!form.querySelector(':scope > .a-notice').textContent,'confirmed source recovery retained stale error feedback');
        }
      }
      const submission=test.calls.find(call=>call.path.endsWith('/source'));
      assert(submission,'source submission missing');
      if(outcome!=='rejected')assert(test.calls.some(call=>call.path.startsWith('/tasks/tsk_')),'source did not use actual submission task address');
    } finally {test.dispose();}
  }
}
