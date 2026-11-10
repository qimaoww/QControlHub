import { assert, pause, waitFor, createConfigFixture } from './browser/config-fixture.mjs';
const form=()=>[...document.querySelectorAll('dialog[open] form')].at(-1);
const alerts=()=>[...document.querySelectorAll('.a-toast[role=alert],dialog .a-notice')].map(node=>node.textContent).join(' ');
export async function testConfigRestrictionsRuntime(){
  for(const engine of ['xray','sing-box','mihomo','ss-rust'])for(const readonly of [false,true])for(const shared of [false,true]){
    const test=await createConfigFixture(engine,{readonly,shared});
    try{
      assert(![...document.querySelectorAll('.a-rail a')].some(link=>link.getAttribute('href')==='#access-control'),'retired policy navigation remains');
      test.select('second');test.click('access');await waitFor(()=>form()?.elements.block_mainland_source,'inbound policy did not open');
      const editor=form(),box=test.topDialog();
      assert(box.textContent.includes('second')&&box.textContent.includes('21002'),'policy selected another inbound');
      assert(box.textContent.includes(shared?'共享实例':'主实例'),'policy lost instance ownership');
      const locked=readonly||(shared&&engine==='ss-rust');
      assert(editor.elements.block_mainland_source.disabled===locked,'policy write permission boundary changed');
      assert(Boolean(editor.querySelector('[type=submit]'))===!locked,'readonly policy exposes submission');
      if(engine==='ss-rust')assert(editor.elements.block_mainland_destination.disabled,'multi-port Rust target restriction enabled');
      if(locked)continue;
      editor.elements.block_mainland_source.click();test.topDialog().querySelector('[aria-label=关闭]').click();
      await waitFor(()=>test.topDialog()===box&&test.confirmations.some(item=>item.title==='放弃更改'),'dirty-close cancellation did not finish');
      assert(form()===editor&&editor.elements.block_mainland_source.checked,'canceling dirty close lost policy draft');
      const confirmations=test.confirmations.length;
      editor.requestSubmit();
      await waitFor(()=>test.confirmations.length>confirmations&&!editor.hasAttribute('aria-busy')&&!test.root.hasAttribute('aria-busy'),'canceled deployment did not finish');
      assert(!test.accessWrites?.length,'canceled deployment wrote policy');
      test.confirm=true;test.accessError=409;editor.requestSubmit();await waitFor(()=>alerts().includes('测试访问限制保存失败')&&!editor.hasAttribute('aria-busy'),'policy conflict hid error');
      assert(!test.accessWrites?.length&&editor.elements.block_mainland_source.checked,'policy conflict lost draft or wrote');
      test.accessError=null;let release;test.accessGate=new Promise(resolve=>{release=resolve});editor.requestSubmit();editor.requestSubmit();await pause();
      assert(editor.getAttribute('aria-busy')==='true'&&editor.querySelector('fieldset').disabled,'pending policy is mutable');
      box.querySelector('[aria-label=关闭]').click();await pause();assert(box.open,'pending policy closed');release();
      await waitFor(()=>test.accessWrites?.length===1&&!test.root.hasAttribute('aria-busy'),'policy write did not settle');
      const written=test.accessWrites[0];assert(written.tag==='second'&&written.port===21002&&written.expected_version===1&&written.intent==='deploy'&&written.block_mainland_source,'policy lost exact target, CAS, restriction or deployment');
      assert(test.sawTaskDetails,'accepted policy did not open deployment task');test.topDialog()?.close();await pause();
      test.click('access');await waitFor(()=>form()?.elements.block_mainland_source,'saved policy did not reopen');assert(form().elements.block_mainland_source.checked,'saved policy not restored');test.topDialog().querySelector('[aria-label=关闭]').click();await pause();
      const input=document.querySelector('.a-main [name=content]');input.value+='\n ';input.dispatchEvent(new Event('input',{bubbles:true}));
      test.click('access');await waitFor(()=>alerts().includes('源码有未保存修改'),'dirty source did not block policy');assert(!form(),'dirty source opened mutating policy');
    }finally{test.dispose()}
  }
  for(const engine of ['xray','sing-box'])for(const readonly of [false,true])for(const legacy of [false,true]){
    const test=await createConfigFixture(engine,{readonly,plan:{protocol:'wireguard',block_mainland_source:legacy}});
    try{
      test.click('access');await waitFor(()=>form()?.elements.block_mainland_source,'WireGuard policy did not open');
      const editor=form();
      assert(editor.elements.block_mainland_source.checked===legacy,'legacy WireGuard source restriction was lost');
      assert(editor.elements.block_mainland_source.disabled===(readonly||!legacy),'WireGuard source boundary changed');
      assert(editor.elements.block_mainland_destination.disabled===readonly,'WireGuard destination policy was unnecessarily disabled');
      assert(editor.textContent.includes('公网来源限制须在节点防火墙配置'),'WireGuard source limitation was hidden');
      if(legacy&&!readonly){
        test.confirm=true;editor.elements.block_mainland_source.click();editor.requestSubmit();
        await waitFor(()=>test.accessWrites?.length===1&&!test.root.hasAttribute('aria-busy'),'legacy WireGuard source restriction could not be cleared');
        assert(!test.accessWrites[0].block_mainland_source,'WireGuard legacy source rule was retained');
      }
    }finally{test.dispose()}
  }
  const late=await createConfigFixture('xray');let release;late.accessReadGate=new Promise(resolve=>{release=resolve});late.click('access');await pause();late.dispose();const next=await createConfigFixture('sing-box');release();await pause();assert(!form(),'previous account policy response entered new target');next.dispose();
}
