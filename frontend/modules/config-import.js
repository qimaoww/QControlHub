// Import never passes through the ordinary source-save path. A confirmed
// read task identifies the protected source; editor text is never trusted as it.
export async function submitConfigImport({api,agent,engine,source,savedConfig,form}) {
  if (!source?.taskId || !source.content) throw new Error("系统服务快照已失效，请重新读取后确认导入。");
  const task = await api(`/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}/import`, {
    method:"POST", body:JSON.stringify({snapshot_task_id:source.taskId,expected_version:savedConfig?.version || 0,
      expected_instance_id:agent.shared_allocation?.id || "",name:form.get("name"),description:form.get("description")}),
  });
  if (!task?.id) throw new Error("未能确认迁移任务，请到任务页核对结果，避免重复提交。");
  return {saved:savedConfig,content:source.content,task};
}
