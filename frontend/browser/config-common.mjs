import { assert, pause, waitFor, createConfigFixture as fixture } from './config-fixture.mjs';
const dialog=()=>[...document.querySelectorAll('dialog[open]')].at(-1);
const form=()=>dialog()?.querySelector('form:has([name=mutation])');
const alerts=()=>[...document.querySelectorAll('.a-toast[role=alert],.a-dialog .a-notice')].filter(item=>item.checkVisibility()).map(item=>item.textContent).join(' ');
const source=()=>document.querySelector('.a-main [name=content]');
async function operation(test,kind){
  await waitFor(()=>!test.root.hasAttribute('aria-busy'),'previous common operation did not release its lock');
  test.common();await waitFor(form,'common editor missing');
  form().elements.mutation.value=kind;form().elements.mutation.dispatchEvent(new Event('change',{bubbles:true}));
  await waitFor(()=>!form()?.textContent.includes('正在读取配置项'),'common operation did not load');
}
async function field(key){
  assert([...form().elements.key.options].some(option=>option.value===key),`common field ${key} unavailable`);
  form().elements.key.value=key;form().elements.key.dispatchEvent(new Event('change',{bubbles:true}));
  await waitFor(()=>!form()?.textContent.includes('正在读取配置项'),`common field ${key} did not load`);
}
const protectedStructure=test=>{const root=JSON.parse(test.saved().content);return JSON.stringify(Object.fromEntries(['inbounds','outbounds','listeners','servers'].filter(key=>key in root).map(key=>[key,root[key]])));};
export async function testCommonConfigRuntime(){
  await testCommonReadRecovery();
  await testSSRustOverrides();
  for(const engine of ['xray','sing-box','mihomo','ss-rust']){
    const test=await fixture(engine),structure=protectedStructure(test);
    assert([...document.querySelectorAll('.a-main button')].filter(button=>button.textContent==='添加入站').length===1,'standalone add-inbound entry duplicated/missing');
    await operation(test,'add');assert(!form().elements.protocol&&!test.calls.some(call=>call.path.endsWith('/plans')),'common editor generates unrelated inbound credentials');
    assert([...form().elements.key.options].map(option=>option.value).join(',')===`dns,${test.extraField}`,'add includes present or inbound-scoped fields');
    assert(dialog().scrollWidth<=dialog().clientWidth+1,'common dialog overflows');
    form().elements.fragment.value='{"servers":["1.1.1.1"]}';form().elements.fragment.dispatchEvent(new Event('input',{bubbles:true}));
    await field(test.extraField);form().elements.fragment.value='{"draft":true}';form().elements.fragment.dispatchEvent(new Event('input',{bubbles:true}));await field('dns');
    assert(form().elements.fragment.value==='{"servers":["1.1.1.1"]}','field switch discarded draft');
    dialog().querySelector('[aria-label=关闭]').click();await pause();assert(form()?.elements.fragment.value.includes('1.1.1.1'),'canceling close lost common draft');
    test.confirm=true;let release;test.gate=new Promise(resolve=>{release=resolve;});const current=form();current.elements.intent.value='deploy';current.requestSubmit();current.requestSubmit();await pause();
    assert(current.getAttribute('aria-busy')==='true'&&current.querySelector('fieldset').disabled,'pending common write does not lock fields');
    dialog().querySelector('[aria-label=关闭]').click();await pause();assert(form()===current,'pending common write closed');release();await test.settle(1);test.gate=null;
    assert(test.writes[0].path.endsWith('/fields/dns')&&test.writes[0].mutation==='add'&&test.writes[0].expected_version===1&&!Object.hasOwn(test.writes[0],'install_if_missing'),'common save lost target/CAS/install boundary');
    assert(protectedStructure(test)===structure&&JSON.parse(test.saved().content).dns.servers[0]==='1.1.1.1','common add changed protected structure');
    assert(test.confirmations.some(item=>item.title==='其他草稿尚未保存'),'saving one field discarded other drafts without confirmation');
    const baseline=source().value;source().value+='\n ';source().dispatchEvent(new Event('input',{bubbles:true}));test.confirm=false;test.common();await pause();assert(!form()&&source().value===baseline+'\n ','opening common editor overwrote source draft');source().value=baseline;source().dispatchEvent(new Event('input',{bubbles:true}));
    await operation(test,'modify');await field('dns');const choices=[...form().elements.key.options].map(option=>option.value);assert(choices.includes(test.primaryField)&&choices.includes(test.secondaryField)&&choices.includes('dns')&&!choices.includes(test.extraField),'modify choices ignore saved presence');
    form().elements.fragment.value='{"servers":["9.9.9.9"]}';form().elements.mutation.value='delete';test.submit('deploy');await waitFor(()=>alerts().includes('操作已变化'),'tampered operation not rejected');assert(test.writes.length===1,'tampered operation reached API');form().elements.mutation.value='modify';test.confirm=true;test.submit('deploy');await test.settle(2);
    assert(test.writes[1].expected_version===2&&protectedStructure(test)===structure,'common modify changed unrelated target');
    const before=JSON.parse(test.saved().content);await operation(test,'delete');await field('dns');assert(form().elements.fragment.readOnly&&form().elements.fragment.value.includes('9.9.9.9'),'delete has no readonly preview');
    test.confirm=false;test.submit('deploy');await pause();assert(test.writes.length===2&&form(),'canceled common delete wrote data');assert(test.confirmations.at(-1).message.includes('dns')&&test.confirmations.at(-1).message.includes('重启'),'delete confirmation lacks field/deploy impact');
    await waitFor(()=>!form().hasAttribute('aria-busy'),'canceled common delete did not release its form');
    test.confirm=true;test.submit('deploy');await test.settle(3);delete before.dns;
    assert(test.writes[2].mutation==='delete'&&test.writes[2].expected_version===3&&test.writes[2].fragment===''&&JSON.stringify(JSON.parse(test.saved().content))===JSON.stringify(before),'delete changed more than selected field');
    await operation(test,'add');assert([...form().elements.key.options].some(option=>option.value==='dns'),'deleted field cannot be re-added');test.dispose();
  }
  for(const options of [{missing:true},{emptyInbounds:true},{native:true}]){
    const test=await fixture(options.native?'mihomo':'xray',options);assert([...document.querySelectorAll('.a-main button')].some(button=>button.textContent==='添加入站'),'empty/native workspace lacks standalone inbound entry');test.dispose();
  }
  for(const options of [{readonly:true},{noTasks:true},{import:true}]){
    const test=await fixture('xray',options);assert(![...document.querySelectorAll('.a-main button')].some(button=>button.textContent==='通用配置项'),'denied/import source exposes common mutation');assert(!test.writes.length,'denied common operation wrote data');test.dispose();
  }
  for(const options of [{missing:true},{missing:true,savedMissing:true},{offline:true}]){
    const test=await fixture('ss-rust',options);test.common();await pause();
    if(options.missing&&!options.savedMissing)assert(!form(),'missing config permits common mutation');else{await waitFor(()=>form()&&!form().textContent.includes('正在读取配置项'),'unavailable core draft did not load');assert(form().querySelector('button[type=submit]').disabled,'unavailable core permits common write');}
    assert(!test.writes.length,'common operation implicitly installed core');test.dispose();
  }
  for(const options of [{noCommon:true},{allCommon:true}]){
    const test=await fixture('xray',options);await operation(test,options.noCommon?'delete':'add');assert(!form().elements.key.options.length&&form().querySelector('button[type=submit]').disabled,'empty choices expose accidental save target');test.dispose();
  }
  for(const kind of ['workspace','field']){
    const test=await fixture('xray');if(kind==='workspace')test.stale=true;else test.staleField=true;test.common();await waitFor(()=>alerts().includes('配置已变化'),'stale common read conflict hidden');assert(!test.writes.length&&(!form()||form().querySelector('button[type=submit]').disabled),'stale common read permits write');test.dispose();
  }
  const malicious='测试读取失败：</textarea><img src=x onerror="bad()"><script>bad()</script>';
  const escaped=await fixture('xray');escaped.fieldError=malicious;escaped.common();
  await waitFor(()=>form()&&form().textContent.includes(malicious),'field failure text missing');
  assert(!dialog().querySelector('img,script')&&form().querySelector('[type=submit]').disabled&&!escaped.writes.length,'failed field injected markup or permits mutation');escaped.dispose();
  const isolated=await fixture('xray');await operation(isolated,'modify');const key=form().elements.key.value;form().elements.fragment.value='{"level":"draft-only"}';form().elements.fragment.dispatchEvent(new Event('input',{bubbles:true}));form().elements.mutation.value='delete';form().elements.mutation.dispatchEvent(new Event('change',{bubbles:true}));await field(key);assert(form().elements.fragment.readOnly&&!form().elements.fragment.value.includes('draft-only'),'modify draft leaked into delete preview');isolated.dispose();
  const conflict=await fixture('xray');await operation(conflict,'modify');form().elements.fragment.value='{"level":"keep-this-draft"}';conflict.fail=true;conflict.confirm=true;conflict.submit('deploy');await waitFor(()=>form()?.textContent.includes('草稿已保留'),'common conflict lost draft feedback');assert(form().elements.fragment.value.includes('keep-this-draft')&&form().querySelector('button[type=submit]').disabled,'common conflict permits stale resubmit');conflict.dispose();
  const late=await fixture('xray');let release;late.fieldGate=new Promise(resolve=>{release=resolve;});late.common();await waitFor(form,'deferred common form missing');late.dispose();release();await pause();assert(!dialog()&&!late.writes.length,'late common field escaped disposed session');
}

async function testCommonReadRecovery(){
  for(const shared of [false,true]){
    const test=await fixture('xray',{shared});
    let release;
    try{
      await operation(test,'modify');
      await field(test.primaryField);
      const draft=JSON.stringify({message:"private draft $&$`$'"});
      const editor=form().elements.fragment;
      editor.value=draft;editor.dispatchEvent(new Event('input',{bubbles:true}));
      test.fieldGate=new Promise(resolve=>{release=resolve;});
      const before=test.calls.length;
      form().elements.key.value=test.secondaryField;form().elements.key.dispatchEvent(new Event('change',{bubbles:true}));
      await waitFor(()=>test.calls.slice(before).some(call=>call.path.includes(`/fields/${test.secondaryField}`)),'deferred field read did not start');
      test.fieldGate=null;
      await field(test.primaryField);
      assert(form().elements.fragment===editor&&editor.value===draft,'rapid field switching replaced the editor or lost the draft');
      editor.focus();editor.setSelectionRange(2,7);
      release();await pause();
      assert(form().elements.key.value===test.primaryField&&editor.value===draft,'late field read overwrote the selected field');
      assert(document.activeElement===editor&&editor.selectionStart===2&&editor.selectionEnd===7,'late read reset draft focus or selection');
      test.fieldFailure=true;
      await field(test.secondaryField);
      assert(form().textContent.includes('配置项读取失败')&&form().querySelector('[type=submit]').disabled,'failed read hid the error or permitted a write');
      test.fieldFailure=false;
      await field(test.primaryField);
      assert(editor.value===draft&&!form().querySelector('[type=submit]').disabled,'retry discarded the cached draft or kept the editor locked');
      await field(test.secondaryField);
      assert(!form().textContent.includes('配置项读取失败')&&!form().querySelector('[type=submit]').disabled,'failed field read was pinned instead of retried');
      assert(!test.writes.length,'reading or switching fields submitted configuration');
    }finally{release?.();test.fieldGate=null;test.dispose();}
    const replacement=await fixture('xray',{shared});
    try{
      await operation(replacement,'modify');await field(replacement.primaryField);
      assert(!form().elements.fragment.value.includes('private draft'),'retired workspace draft leaked into a new workspace');
    }finally{replacement.dispose();}
  }
}

async function testSSRustOverrides(){
  const tag='HK & JP </textarea><img src=x onerror="bad()">';
  for(const shared of [false,true]){
    const test=await fixture('ss-rust',{native:true,shared,tags:[tag,'second'],globalFields:{outbound_bind_addr:'192.0.2.1'}});test.confirm=true;
    const edit=async()=>{
      test.select(tag);test.click('modify');
      await waitFor(()=>dialog()?.querySelector('[name=fragment]'),'native SS Rust source editor missing');
      return dialog().querySelector('form');
    };
    const submit=async(current,fragment,count)=>{
      current.elements.fragment.value=JSON.stringify(fragment,null,2);current.elements.fragment.dispatchEvent(new Event('input',{bubbles:true}));
      current.requestSubmit();await test.settle(count);
    };
    try{
      const original=JSON.parse(test.saved().content),current=await edit(),entry=JSON.parse(current.elements.fragment.value);
      assert(!dialog().querySelector('img,script')&&dialog().textContent.includes(tag),'inbound identity escaped text or injected markup');
      assert(test.calls.some(call=>call.path.split('?')[0]===`/agents/node/configs/ss-rust/server-inbounds/${encodeURIComponent(tag)}`),'native source read did not encode selected inbound identity');
      assert(!Object.hasOwn(entry,'mode')&&!Object.hasOwn(entry,'outbound_bind_addr'),'inherited globals were copied into native port overrides');
      await submit(current,{...entry,mode:'udp_only',outbound_udp_allow_fragmentation:false},1);
      let saved=JSON.parse(test.saved().content);
      assert(saved.outbound_bind_addr===original.outbound_bind_addr&&saved.mode===original.mode&&saved.servers.find(item=>item.remarks===tag).mode==='udp_only'&&saved.servers.find(item=>item.remarks===tag).outbound_udp_allow_fragmentation===false&&!Object.hasOwn(saved.servers.find(item=>item.remarks==='second'),'mode'),'port override changed global defaults or sibling');
      const port=JSON.stringify(saved.servers);
      await operation(test,'modify');await field('mode');
      assert(![...form().elements.key.options].some(option=>['server','server_port','servers'].includes(option.value)),'global editor includes inbound structural fields');
      form().elements.fragment.value='"tcp_only"';form().elements.fragment.dispatchEvent(new Event('input',{bubbles:true}));test.submit('deploy');await test.settle(2);
      saved=JSON.parse(test.saved().content);
      assert(saved.mode==='tcp_only'&&JSON.stringify(saved.servers)===port,'global default mutation erased port overrides');
      const reopened=await edit(),inherited=JSON.parse(reopened.elements.fragment.value);delete inherited.mode;
      await submit(reopened,inherited,3);saved=JSON.parse(test.saved().content);
      assert(saved.mode==='tcp_only'&&!Object.hasOwn(saved.servers.find(item=>item.remarks===tag),'mode')&&saved.servers.find(item=>item.remarks===tag).outbound_udp_allow_fragmentation===false,'removing port override lost inheritance or unrelated options');
      assert(test.writes[0].original_tag===tag&&test.writes[0].expected_version===1&&test.writes[1].path.endsWith('/fields/mode')&&test.writes[1].expected_version===2&&test.writes[2].expected_version===3,'scoped mutations lost original identity or CAS');
      assert(test.confirmations.some(item=>item.message.includes('重启')),'SS Rust deployment omitted restart confirmation');
    }finally{test.dispose();}
  }
}
