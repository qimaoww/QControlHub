import { bindConfigFiles } from './modules/config-files.js';
import { bindConfigRestrictions } from './modules/config-restrictions.js';
const assert = (v,m) => {if(!v) throw Error(m);};
const wait = async check => {const end=Date.now()+3000;while(!check()){if(Date.now()>end)throw Error('restriction wait timeout');await new Promise(r=>setTimeout(r,10));}};
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
export async function testConfigRestrictionsRuntime(){
 for(const engine of ['xray','sing-box','mihomo','ss-rust']) {
  for(const locked of [false,true]) {
   document.body.innerHTML='<form id="restriction-fixture"><section data-code-editor><header class="code-editor-toolbar"><div class="code-file-meta"><b>config.json</b></div></header><textarea data-code-input></textarea></section></form>';
   const form=document.querySelector('form'),input=form.querySelector('textarea');
   input.value=JSON.stringify({inbounds:[{tag:'first',port:1080,listen_port:1080},{tag:'second',port:2080,listen_port:2080}]});input.readOnly=locked;
   const original=input.value,files=bindConfigFiles(form,engine,()=>{});
   const abort=new AbortController();
   const state={route:'live-config',navigationEpoch:1,routeSignal:abort.signal,data:{liveAgent:'node',liveEngine:engine}};
   let accept=false, saved=0, writes=[], notices=[],hold=null;
   const entries=['first','second'].map((tag,i)=>({agent_id:'node',engine,tag,port:1080+i*1000,config_version:3,agent_status:'online',agent_name:'Node',block_mainland_source:false,block_mainland_destination:false}));
   await bindConfigRestrictions({form,files,agent:{id:'node'},engine,saved:{version:3},sourceMode:'managed',state,esc,engineName:v=>v,can:()=>!locked,
    confirmAction:async()=>accept,notify:msg=>notices.push(msg),onSaved:async()=>saved++,api:async(path,opts={})=>{
     if(opts.method==='PUT'){writes.push(JSON.parse(opts.body));return {config:{content:original},task:{id:'task-123'}};}
     if(hold)await hold;return entries;
    }});
   const button=form.querySelector('[data-config-access-open]');assert(button.disabled,'common must not open restrictions');
   const tabs=form.querySelectorAll(files?'[data-config-file]':'[data-access-inbound]');tabs[files?2:1].click();
   assert(!button.disabled,'inbound must enable restrictions');button.click();await wait(()=>document.querySelector('dialog[open]'));
   let dialog=document.querySelector('dialog');assert(dialog.textContent.includes('second · :2080'),'wrong selected inbound');
   if(locked){assert(!dialog.querySelector('[type=submit]'),'readonly actions exposed');assert(dialog.querySelector('input[type=checkbox]').disabled,'readonly checkbox editable');}
   else{
    const checkbox=dialog.querySelector('input[type=checkbox]');checkbox.click();dialog.querySelector('[data-access-close]').click();await new Promise(r=>setTimeout(r,20));assert(dialog.open,'dirty cancel discarded state');
    dialog.querySelector('[data-access-intent=deploy]').click();await new Promise(r=>setTimeout(r,20));assert(writes.length===0,'canceled deploy wrote');
    dialog.querySelector('[data-access-intent=validate]').click();await wait(()=>saved===1);assert(writes[0].tag==='second'&&writes[0].port===2080&&writes[0].expected_version===3,'wrong write target/version');assert(!dialog.isConnected,'saved modal remains');
    input.value+='\n';input.dispatchEvent(new Event('input'));button.click();await new Promise(r=>setTimeout(r,20));assert(!document.querySelector('dialog'),'dirty source allowed mutation');assert(notices.some(n=>n.includes('源码')),'missing dirty message');
    input.value=input.value.slice(0,-1);input.dispatchEvent(new Event('input'));
   }
   if(dialog.isConnected) {abort.abort();assert(!dialog.isConnected,'route left orphan dialog');}
   else {let release;hold=new Promise(r=>release=r);button.click();state.data={};release();await new Promise(r=>setTimeout(r,20));assert(!document.querySelector('dialog'),'late response opened stale session modal');}
  }
 }
}
