import { node, button, actions, stack, table, field, notice } from './ui.js';

const labels={reset:'清零当前周期',quota:'设置配额','remove-quota':'取消配额',delete:'删除流量记录'};
const requestID=()=>Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('');
export function trafficBatch(ctx, policies, agentName, onSaved) {
  const selected=new Set(), results=node('div'), feedback=notice('');
  let uncertain=null,last=null;
  const choices=policies.map(policy=>field(`${agentName(policy.agent_id)} · ${policy.engine} · ${policy.port}`,policy.id,false,{type:'checkbox',onchange:event=>{event.target.checked?selected.add(policy.id):selected.delete(policy.id);syncSelection();}}));
  const all = field('全选', 'select-all', false, { type: 'checkbox', disabled: !policies.length, onchange(event) {
    policies.forEach(policy => event.target.checked ? selected.add(policy.id) : selected.delete(policy.id));
    choices.forEach(choice => { choice.querySelector('input').checked = event.target.checked; });
    syncSelection();
  } });
  function syncSelection() {
    const input = all.querySelector('input');
    input.checked = policies.length > 0 && selected.size === policies.length;
    input.indeterminate = selected.size > 0 && selected.size < policies.length;
  }
  const operation=field('批量操作','action','reset',{options:Object.entries(labels)});
  const quota=node('div',{className:'a-form-grid'},[
    ['limit_bytes','周期额度（GiB）','limit_gb',{type:'number',min:0,max:8388607,step:'any'},''],
    ['cycle','统计周期','cycle',{options:[['monthly','每月'],['yearly','每年']]},'monthly'],
    ['cycle_anchor','周期起始日期','cycle_anchor',{type:'date'},new Date().toISOString().slice(0,10)],
    ['auto_block','超额自动封禁','auto_block',{options:[['true','开启'],['false','关闭']]},'true'],
  ].map(([key,label,name,attrs,value])=>{
    const control=field(label,name,value,{...attrs,disabled:true});
    return stack(field(`修改${label}`,`patch:${key}`,false,{type:'checkbox',onchange:event=>{const input=control.querySelector('input,select');input.disabled=!event.target.checked||operation.querySelector('select').value!=='quota';input.required=!input.disabled;}}),control);
  }));
  async function execute(input) {
    try {
      const response=await ctx.api('/traffic-policies/batch',{method:'POST',body:input});
      if(!ctx.current()||!box.open)return;
      uncertain=null;last={input,results:response.results};
      results.replaceChildren(table(['记录','状态','结果'],response.results.map(item=>[policies.find(policy=>policy.id===item.id)?.name||item.id,({succeeded:'成功',failed:'失败',not_executed:'未执行'})[item.status]||item.status,item.message||'已完成']),{label:'批量操作结果'}));
      feedback.textContent='';retry.hidden=!response.results.some(item=>item.status!=='succeeded');
      const succeeded=response.results.filter(item=>item.status==='succeeded');
      succeeded.forEach(item=>selected.delete(item.id));
      choices.forEach(choice=>{const input=choice.querySelector('input');input.checked=selected.has(input.name);});syncSelection();
      const refreshed=await onSaved();
      const incomplete=response.results.some(item=>item.status!=='succeeded');
      ctx.announce(incomplete?'部分操作未完成，请查看结果':refreshed?'批量操作已完成':'操作已完成，列表刷新失败，请重试。',incomplete||!refreshed?'error':'');
    } catch(error) {
      if(error.name==='AbortError')throw error;
      if(ctx.current()&&box.open){uncertain=!error.status||error.status>=500?input:null;retry.hidden=!uncertain;feedback.textContent=error.message;}
    }
  }
  const retry=button('重试未完成项',event=>void ctx.mutate(event.currentTarget,async()=>{
    // An uncertain response must replay the identical receipt key and payload.
    const input=uncertain||{...last.input,request_id:requestID(),ids:last.results.filter(item=>item.status!=='succeeded').map(item=>item.id)};
    if(input.ids.length)await execute(input);
  }));retry.hidden=true;
  const form=ctx.form([operation,all,node('div',{className:'a-stack'},choices),quota,results,feedback,actions(retry)],'执行批量操作',async(values,form)=>{
    if(uncertain)throw new Error('请先重试未完成项，确认上次操作结果');
    if(!selected.size)throw new Error('请选择流量记录');
    const patch={};
    if(values.action==='quota'){
      for(const key of ['limit_bytes','cycle','cycle_anchor','auto_block'])if(form.elements.namedItem(`patch:${key}`).checked){
        patch[key]=key==='limit_bytes'?Math.round(Number(values.limit_gb)*1024**3):key==='cycle_anchor'?`${values.cycle_anchor}T00:00:00Z`:key==='auto_block'?values.auto_block==='true':values.cycle;
      }
      if(!Object.keys(patch).length)throw new Error('请选择要修改的配额字段');
      if(patch.limit_bytes!==undefined&&(!Number.isSafeInteger(patch.limit_bytes)||patch.limit_bytes<0))throw new Error('请输入有效配额');
    }
    if(!await ctx.confirm(`${labels[values.action]} · ${selected.size} 条记录${values.action==='delete'?'，同时删除每日历史':''}，确定继续？`,labels[values.action])||!ctx.current())return;
    await execute({request_id:requestID(),action:values.action,ids:[...selected],...(values.action==='quota'?{quota:patch}:{})});
  });
  const sync=()=>{const enabled=form.elements.action.value==='quota';ctx.visible(quota,enabled);for(const key of ['limit_bytes','cycle','cycle_anchor','auto_block']){const toggle=form.elements.namedItem(`patch:${key}`),control=toggle.parentElement.parentElement.querySelector('.a-field input,.a-field select');control.disabled=!enabled||!toggle.checked;control.required=!control.disabled;}};
  operation.querySelector('select').addEventListener('change',sync);sync();
  const box=ctx.dialog('流量批量操作',form,{wide:true});
  const close=()=>box.close();
  ctx.signal.addEventListener('abort',close,{once:true});
  box.addEventListener('close',()=>ctx.signal.removeEventListener('abort',close),{once:true});
}
