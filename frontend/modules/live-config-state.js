// Source selection, snapshot freshness and atomic configuration submission.

export function liveConfigEditorState({
  sourceMode = "personal",
  canOperate,
  sourceContent,
  formContent,
}) {
  return {
    readOnly: sourceMode === "import" || !canOperate,
    content: sourceMode === "import" ? String(sourceContent || "") : String(formContent || ""),
  };
}

export function liveConfigEngineEligible(runtime, canAdd = false) {
  return Boolean(
    canAdd || runtime?.installed ||
      runtime?.existing_config_available ||
      runtime?.existing_config_unsupported_reason,
  );
}

export function liveConfigReadAction({
  sourceMode,
  managedReadSupported,
  existingAvailable,
}) {
  if (sourceMode === "import") return "read-config";
  if (managedReadSupported) return "read-managed-config";
  // Before managed-config-read-v1, read-config already read the managed file
  // whenever no external service mapping existed. Preserve that compatibility
  // path; an upgrade is required only when both files must be distinguished.
  return existingAvailable ? "" : "read-config";
}

export async function submitLiveConfigChange({
  api,
  submitTask,
  agent,
  engine,
  intent,
  form,
  source,
  existingAvailable,
  savedConfig,
  beforeDeploy,
  onDeployTask,
  onSavedConfig,
}) {
  if (intent !== "deploy" && intent !== "validate" && intent !== "save") throw new Error("普通配置入口不支持导入操作。");
  const content = String(form.get("content") || "");
  if (intent === "deploy") await beforeDeploy?.();
  const result = await api(`/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}${intent === "save" ? "" : "/source"}`, {
    method: intent === "save" ? "PUT" : "POST", body: JSON.stringify({
      engine,name:form.get("name"),description:form.get("description"),content,version:Number(form.get("version")),
      ...(intent === "save" ? {} : {intent,expected_instance_id:agent.shared_allocation?.id || "",
        ...(intent === "deploy" && !agent.runtime?.[engine]?.installed ? {install_if_missing:true} : {})}),
    }),
  });
  const saved = intent === "save" ? result : result.config;
  if (!saved?.id || intent !== "save" && !result.task?.id) throw new Error("未能确认保存与任务结果，请核对任务后再操作。");
  onSavedConfig?.(saved);
  if (intent === "deploy") onDeployTask?.(result.task.id);
  return {saved,content:saved.content,task:result.task};
}

const liveConfigSnapshotCacheTTL = 600 * 1000;

export function liveConfigSnapshotReusable(source, now = Date.now()) {
  if (!source || source.saved || source.reading || source.error) return true;
  const readAt = Number(source.readAt);
  // Entries created by an older frontend build have no timestamp. They exist
  // only in volatile page state and disappear on the build reload; retaining
  // them avoids treating an in-flight upgrade as a configuration conflict.
  if (!Number.isFinite(readAt)) return true;
  return now - readAt < liveConfigSnapshotCacheTTL;
}


export function liveSourceKey(agentId, engine, source = "managed") {
  return source === "import"
    ? `${agentId}|${engine}|import`
    : `${agentId}|${engine}`;
}
