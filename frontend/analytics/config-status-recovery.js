// A status task observes the authorized instance without changing its service
// or inheriting another owner's source. The mutation still checks ports again.
export async function recoverConfigInstance(ctx, agent, engine, active = ctx.current) {
  if (!active()) throw new DOMException('目标已切换','AbortError');
  const task = await ctx.api('/tasks', {method:'POST',body:{
    agent_id:agent.id,engine,action:'status',expected_instance_id:agent.shared_allocation?.id || '',
  }});
  const matches = result => result?.id === task?.id && result.agent_id === agent.id && result.engine === engine &&
    result.action === 'status' && Boolean(result.shared_instance) === Boolean(agent.shared_allocation) &&
    (result.shared_traffic_id || '') === (agent.shared_allocation?.id || '');
  if (!task?.id || !matches(task)) throw new Error('实例状态回执与当前目标不一致，草稿已保留。');
  let result = task;
  for (let attempt = 0; attempt < 120 && !['succeeded','failed','canceled'].includes(result.status); attempt++) {
    await new Promise(resolve => setTimeout(resolve,500));
    if (!active()) throw new DOMException('目标已切换','AbortError');
    result = await ctx.api(`/tasks/${encodeURIComponent(task.id)}?view=status`);
    if (!matches(result)) throw new Error('实例状态回执与当前目标不一致，草稿已保留。');
  }
  if (!active()) throw new DOMException('目标已切换','AbortError');
  if (result.status !== 'succeeded') throw Object.assign(new Error(result.error || '实例状态尚未确认，请在执行记录中核对。草稿已保留。'),{failureTaskID:result.id,failureSaved:true});
  return result;
}
