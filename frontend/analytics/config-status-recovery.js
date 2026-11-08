// A status task reads the exact caller-owned instance. It never retries the
// rejected configuration mutation or assumes an uncertain port is available.
export async function recoverConfigInstance(ctx, agent, engine, active = ctx.current) {
  const task = await ctx.api('/tasks', {method:'POST',body:{
    agent_id:agent.id,engine,action:'status',expected_instance_id:agent.shared_allocation?.id || '',
  }});
  let result = task;
  for (let attempt = 0; attempt < 120 && !['succeeded','failed','canceled'].includes(result.status); attempt++) {
    await new Promise(resolve => setTimeout(resolve,500));
    if (!active()) throw new DOMException('目标已切换','AbortError');
    result = await ctx.api(`/tasks/${encodeURIComponent(task.id)}?view=status`);
  }
  if (!active()) throw new DOMException('目标已切换','AbortError');
  if (result.status !== 'succeeded') throw new Error(result.error || '实例状态尚未确认，请在执行记录中核对。草稿已保留。');
  return result;
}
