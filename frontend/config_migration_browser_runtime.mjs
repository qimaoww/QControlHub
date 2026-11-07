import { assert, pause, waitFor, createConfigFixture } from './browser/config-fixture.mjs';
import { testNativeConfigReadCacheRuntime } from './browser/config-snapshots.mjs';
import { testSourceReceipts } from './browser/preset-recovery.mjs';
import { patch } from './analytics/live-dom.js';
const sourceForm=()=>document.querySelector('.a-main form:has([name=content])');
const feedback=()=>[...document.querySelectorAll('.a-main .a-notice,.a-toast')].filter(node=>node.checkVisibility()).map(node=>node.textContent).join(' ');
const source='{"inbounds":[{"tag":"first","port":21001},{"tag":"second","port":21002}],"outbounds":[{"tag":"direct"},{"tag":"qch-trf-21001-abcdef012345","custom_integer":9007199254740993}],"large":9007199254740993}';
export async function testConfigMigrationRuntime(preview=false){
  if(preview){const params=new URLSearchParams(location.search);await createConfigFixture(params.get('engine')||'xray',{readonly:params.has('readonly'),import:params.has('import'),legacy:params.has('legacy')});return}
  for(const engine of ['xray','sing-box','mihomo','ss-rust'])for(const readonly of [false,true]){
    const test=await createConfigFixture(engine,{readonly,...(['xray','sing-box'].includes(engine)?{content:source}:{})});
    try{
      const form=sourceForm(),input=form.elements.content;
      assert(!document.querySelector('[data-live-intent="migrate-files"]')&&!document.body.textContent.includes('生成入站与出口成套文件'),'removed bundle-generation action remains');
      const submit=form.querySelector('[type=submit]');assert(Boolean(submit)===!readonly,'source deployment permission changed');
      assert(!form.querySelector('select[name=intent]')&&!document.body.textContent.includes('保存并校验'),'retired source action returned');
      if(readonly){assert(input.readOnly,'readonly source remains editable');continue}
      const baseline=input.value;form.requestSubmit();await pause();assert(!test.writes.length,'canceled source deployment wrote');
      if(['xray','sing-box'].includes(engine)){
        test.select('second');assert(input.value===baseline&&input.value.includes('qch-trf-21001'),'inbound selection replaced full source/dedicated exit');
        assert(!document.querySelector('[data-config-preview]'),'single-file source retained merged preview');
        input.value=baseline.replaceAll('21001','23001').replaceAll('9007199254740993','9007199254740995');
      }else input.value=baseline+'\n ';
      input.dispatchEvent(new Event('input',{bubbles:true}));const draft=input.value;
      // Refreshing an unchanged source container must retain the complete draft.
      patch(input.parentElement,input.parentElement.cloneNode(true));assert(input.value===draft,'source patch discarded draft');
      test.confirm=true;form.requestSubmit();await test.settle(1);
      const written=test.writes[0];assert(written.intent==='deploy'&&written.version===1&&written.content===draft&&test.saved().version===2,'source deployment lost complete draft/CAS');
      assert(!test.readRequests.length&&!test.calls.some(call=>call.path==='/tasks'),'source submission required redundant Agent read or second task');
      if(['xray','sing-box'].includes(engine))assert(written.content.includes('9007199254740995')&&written.content.includes('qch-trf-23001'),'source corrupted exact integer or dedicated exit');
      assert(test.sawTaskDetails,'accepted source submission did not open task result');
      assert(test.confirmations.filter(item=>item.title==='部署并重启').length===2,'source submission presented duplicate confirmations');
      assert(form.textContent.includes('已保存')&&!form.textContent.includes('版本 2'),'save date missing or internal revision exposed');
    }finally{test.dispose()}
  }
  for(const engine of ['xray','sing-box','mihomo','ss-rust']){
    const imported=await createConfigFixture(engine,{import:true});
    try{
      const form=sourceForm();assert(form.elements.content.readOnly&&form.querySelector('[type=submit]')?.textContent==='导入并迁移','external source lost explicit migration');
      assert(![...form.querySelectorAll('button')].some(button=>button.textContent==='保存并部署'),'external source exposed regular source deployment');
      imported.clickButton('读取系统配置');await waitFor(()=>imported.snapshotReads===1&&!imported.root.hasAttribute('aria-busy'),'system source read did not finish');
      form.requestSubmit();await pause();assert(!imported.imports?.length,'canceled external migration submitted');
      imported.confirm=true;form.requestSubmit();await waitFor(()=>imported.imports?.length===1&&!imported.root.hasAttribute('aria-busy'),'explicit source migration failed');
      assert(imported.imports[0].action==='import-existing'&&imported.imports[0].expected_config_version===imported.saved().version&&imported.imports[0].engine===engine,'migration lost exact saved target');
      assert(imported.sawTaskDetails,'migration did not show task result');
    }finally{imported.dispose()}
  }
  for(const engine of ['xray','sing-box'])for(const shared of [false,true]){
    const test=await createConfigFixture(engine,{legacy:true,shared,content:source});
    try{
      const form=sourceForm();form.requestSubmit();await pause();assert(!test.writes.length,'canceled legacy source deployment wrote');
      test.confirm=true;form.requestSubmit();await test.settle(1);
      assert(test.writes[0].intent==='deploy'&&!test.readRequests.length,'legacy Agent lost regular atomic deployment');
    }finally{test.dispose()}
  }
  await testSelectedNodeWorkspace();
  await testPendingAndRejectedSource();
  await testSourceReceipts();
  await testNativeConfigReadCacheRuntime();
  await testRuntimeAndSourceSelection();
}
async function testSelectedNodeWorkspace(){
  for(const engine of ['mihomo','xray','sing-box','ss-rust']){
    const other={id:'unselected',name:'OTHER-NODE-CONTENT',status:'online',capabilities:['mihomo','xray','sing-box','ss-rust'],runtime:{}};
    const test=await createConfigFixture(engine,{additionalAgents:[other]});
    try{
      const selector=test.root.querySelector('.a-target-form [name=agent]');
      assert([...selector.options].some(option=>option.value===other.id),'other authorized node missing from selection');
      assert(selector.value==='node','explicit configuration target was not retained');
      const reads=test.calls.filter(call=>call.path.includes('/configs'));
      assert(reads.length===1&&reads[0].path===`/agents/node/configs/${engine}/workspace`,'opening configuration fetched an unselected node or redundant fleet configs');
      assert(!sourceForm().elements.content.value.includes(other.name),'unselected node content entered active editor');
      assert(sourceForm().querySelector('[type=submit]')?.textContent==='保存并部署','managed configuration requires an optional system import');
      assert(!test.writes.length&&!test.readRequests.length,'opening selected configuration unexpectedly submitted a task');
    }finally{test.dispose()}
  }
}
async function testRuntimeAndSourceSelection(){
  const test=await createConfigFixture('sing-box',{import:true});
  const target=()=>test.root.querySelector('.a-target-form');
  const idle=()=>sourceForm()&&!test.root.querySelector('[aria-busy=true]');
  const selectSource=async value=>{
    const select=target().elements.source;
    select.value=value;select.dispatchEvent(new Event('change',{bubbles:true}));
    await waitFor(()=>idle()&&target().elements.source.value===value,`native source selection failed: ${value}`);
  };
  let release;
  try{
    for(const reason of ['complex wrapper is unsupported','standard executable did not pass protected native binary validation']){
      Object.assign(test.agent.runtime['sing-box'],{installed:false,existing_config_available:false,existing_config_unsupported_reason:reason});
      const reads=test.calls.filter(call=>call.path==='/agents').length;
      await test.reenter();
      assert(test.calls.filter(call=>call.path==='/agents').length===reads+1,'explicit refresh duplicated runtime reads');
      assert(target().elements.engine.value==='sing-box'&&target().elements.engine.options.length===1,'unsupported runtime removed selected engine');
      assert(test.root.textContent.includes(reason),'fresh unsupported runtime reason missing');
      assert(sourceForm().querySelector('[type=submit]').disabled,'unsupported runtime permits deployment');
      assert(![...target().elements.source.options].some(option=>option.value==='import'),'unsupported runtime permits system migration');
      if(reason.startsWith('standard'))assert(!test.root.textContent.includes('complex wrapper is unsupported'),'runtime refresh retained stale unsupported reason');
    }
    Object.assign(test.agent.runtime['sing-box'],{installed:true,existing_config_available:true,existing_config_unsupported_reason:''});
    const beforeWorkspaces=test.calls.filter(call=>call.path.endsWith('/workspace')).length;
    location.hash='#tasks';
    await waitFor(()=>!sourceForm()&&test.calls.some(call=>call.path.startsWith('/tasks?'))&&!test.root.querySelector('[aria-busy=true]'),'tasks route did not load before returning to configuration');
    const afterTasksAgents=test.calls.filter(call=>call.path==='/agents').length;
    location.hash='#live-config?agent=node&engine=sing-box&source=personal';
    await waitFor(()=>idle()&&target().elements.source.value==='personal','configuration did not reload after tasks route');
    assert(test.calls.filter(call=>call.path==='/agents').length===afterTasksAgents+1,'return from tasks reused obsolete runtime');
    assert(test.calls.filter(call=>call.path.endsWith('/workspace')).length===beforeWorkspaces+1,'runtime refresh duplicated workspace reads');
    assert(!test.root.textContent.includes('standard executable did not pass'),'upgrade retained unsupported warning');
    assert(!sourceForm().querySelector('[type=submit]').disabled,'upgraded runtime did not restore deployment');
    assert(['personal','managed','import'].every(value=>[...target().elements.source.options].some(option=>option.value===value)),'upgraded runtime lost available source choices');
    await selectSource('personal');
    assert(!sourceForm().elements.content.readOnly&&sourceForm().elements.content.value===test.saved().content,'saved source is not editable or shows system snapshot');
    await selectSource('managed');
    assert(!sourceForm().elements.content.readOnly&&sourceForm().querySelector('[type=submit]').textContent==='保存并部署','managed source lost atomic deployment');
    await selectSource('import');
    const snapshot='{"inbounds":[{"tag":"complete-existing-snapshot"}],"large":9007199254740993}';
    test.setAgentContent(snapshot);test.clickButton('读取系统配置');
    await waitFor(()=>idle()&&sourceForm().elements.content.value===snapshot,'newly available system source did not read exact snapshot');
    assert(sourceForm().elements.content.readOnly,'system migration snapshot became editable');
    assert(sourceForm().querySelectorAll('[type=submit]').length===1&&sourceForm().querySelector('[type=submit]').textContent==='导入并迁移','system migration has duplicate or incorrect submit action');
    await selectSource('personal');
    assert(sourceForm().elements.content.value===test.saved().content,'system snapshot polluted saved source');
    for(const ignoreAbort of [false,true]){
      test.ignoreAgentAbort=ignoreAbort;
      const nextSource=ignoreAbort?'personal':'managed';
      Object.assign(test.agent.runtime['sing-box'],{installed:false,existing_config_available:false,existing_config_unsupported_reason:'stale unsupported reason'});
      test.agentReadGate=new Promise(resolve=>{release=resolve;});
      const agentReads=test.calls.filter(call=>call.path==='/agents').length;
      test.clickButton('刷新');
      await waitFor(()=>test.calls.filter(call=>call.path==='/agents').length===agentReads+1,'stale runtime read did not begin');
      Object.assign(test.agent.runtime['sing-box'],{installed:true,existing_config_available:true,existing_config_unsupported_reason:''});
      location.hash=`#live-config?agent=node&engine=sing-box&source=${nextSource}`;
      await waitFor(()=>idle()&&target().elements.source.value===nextSource,'current runtime did not supersede held old read');
      const currentForm=sourceForm(),workspaceReads=test.calls.filter(call=>call.path.endsWith('/workspace')).length;
      release();release=null;await pause();
      assert(sourceForm()===currentForm&&!test.root.textContent.includes('stale unsupported reason'),'late old runtime replaced current workspace');
      assert(!sourceForm().querySelector('[type=submit]').disabled&&target().elements.source.value===nextSource,'late runtime changed current source or deployment permission');
      if(!ignoreAbort)assert(test.calls.filter(call=>call.path.endsWith('/workspace')).length===workspaceReads,'obsolete runtime triggered another workspace read');
    }
    assert(!test.writes.length&&!test.imports?.length,'runtime and source navigation unexpectedly wrote configuration');
  }finally{release?.();test.dispose();}
}
async function testPendingAndRejectedSource(){
  for(const engine of ['xray','sing-box']){
    const test=await createConfigFixture(engine,{multi:true,content:source});
    try{
      test.confirm=true;const form=sourceForm(),input=form.elements.content;
      input.value=source.replaceAll('9007199254740993','9007199254740995');input.dispatchEvent(new Event('input',{bubbles:true}));const draft=input.value;
      test.sourceFailure={status:422};form.requestSubmit();await waitFor(()=>feedback().includes('测试源码保存失败')&&!form.hasAttribute('aria-busy'),'rejected source lost feedback');
      assert(!test.writes.length&&input.value===draft&&!form.querySelector('[type=submit]').disabled,'correctable source rejection lost draft or retry');
      test.sourceFailure=null;let release;test.gate=new Promise(resolve=>{release=resolve});form.requestSubmit();
      await waitFor(()=>test.calls.filter(call=>call.path.endsWith('/source')).length===2,'pending source save did not start');
      assert(form.getAttribute('aria-busy')==='true'&&form.querySelector('fieldset').disabled,'pending source save left editable controls');
      form.requestSubmit();const engineSelect=test.root.querySelector('.a-target-form [name=engine]');engineSelect.value='mihomo';engineSelect.dispatchEvent(new Event('change',{bubbles:true}));await pause();
      assert(sourceForm()===form&&input.value===draft&&new URLSearchParams(location.hash.split('?')[1]).get('engine')===engine,'pending source target switch discarded workspace');
      assert(test.calls.filter(call=>call.path.endsWith('/source')).length===2,'pending source submitted twice');
      release();await test.settle(1);assert(test.writes[0].content===draft&&test.saved().version===2&&!form.querySelector('[type=submit]').disabled,'retry did not save exact source once');
      assert(form.textContent.includes('已保存'),'accepted source lost save date');
    }finally{test.dispose()}
  }
  const late=await createConfigFixture('xray',{content:source});let release;late.gate=new Promise(resolve=>{release=resolve});late.confirm=true;sourceForm().requestSubmit();await waitFor(()=>late.calls.some(call=>call.path.endsWith('/source')),'late source submission not started');late.dispose();const next=await createConfigFixture('sing-box');release();await pause();assert(!document.querySelector('dialog[open]')&&next.saved().version===1,'late source response entered new account/workspace');next.dispose();
}
