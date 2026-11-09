import { actions, badge, field, node, pathID } from './ui.js';

// Full native objects cover options that cannot be represented by a preset.
export async function editInboundSource(ctx, {target, agent, engine, base, workspace, preparing, write, committed, track, isCurrent=ctx.current}) {
  const config = workspace.config;
  const query = workspace.snapshot_task_id ? `?snapshot=${pathID(workspace.snapshot_task_id)}` : '';
  const result = await ctx.api(`${base}/server-inbounds/${pathID(target.tag)}${query}`);
  if (!ctx.current()) return;
  if (!isCurrent()) throw new Error('配置已变化，已保留当前输入，请重新打开入站');
  if (result.version !== config.version) throw new Error('配置已变化，请重新读取后编辑');
  const editor = field(engine === 'mihomo' ? '入站配置 · YAML' : '入站配置 · JSON','fragment',result.fragment,{multiline:18,required:true,spellcheck:false});
  const form = ctx.form([
    actions(badge(agent.name),badge(engine),badge(agent.shared_allocation?'共享实例':'主实例'),badge(`端口 ${target.port || '无固定端口'}`)),
    node('div',{className:'a-editor'},editor),
  ],'保存并部署',async values=>{
    if (!isCurrent()) throw new Error('配置已变化，已保留当前输入，请重新打开入站');
    if (!await preparing('deploy')) return;
    const saved = await write(`${base}/server-inbounds`,{operation:'modify',original_tag:target.tag,expected_version:config.version,fragment:values.fragment,intent:'deploy',snapshot_task_id:workspace.snapshot_task_id},box);
    await committed(saved,box);
  });
  const box = ctx.dialog(`修改入站 · ${target.tag}`,form,{wide:true});
  track(form,box,true,()=>form.elements.fragment.value !== result.fragment);
  return box;
}
