// Source selection, snapshot freshness and atomic configuration submission.

export function liveConfigEditorState({
  existingAvailable,
  canOperate,
  sourceContent,
  formContent,
}) {
  return {
    readOnly: Boolean(existingAvailable) || !canOperate,
    content: existingAvailable ? String(sourceContent || "") : String(formContent || ""),
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
  const editor = liveConfigEditorState({
    existingAvailable,
    canOperate: true,
    sourceContent: source?.content,
    formContent: form.get("content"),
  });
  if (intent === "import" && !editor.content)
    throw new Error("待迁移的原始节点快照已失效，请重新读取");
  if (intent === "deploy") await beforeDeploy?.();
  if (!existingAvailable && ["deploy", "validate"].includes(intent)) {
    const result = await api(
      `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}/source`,
      { method: "POST", body: JSON.stringify({
        engine, name: form.get("name"), description: form.get("description"),
        content: editor.content, version: Number(form.get("version")), intent,
      }) },
    );
    if (!result.config?.id || !result.task?.id) throw new Error("未能确认保存与任务结果，请重新读取后核对。");
    onSavedConfig?.(result.config);
    if (intent === "deploy") onDeployTask?.(result.task.id);
    return { saved: result.config, content: result.config.content, task: result.task };
  }
  let saved = savedConfig;
  if (
    !existingAvailable ||
    !saved ||
    String(saved.content || "") !== editor.content
  ) {
    saved = await api(
      `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}`,
      {
        method: "PUT",
        body: JSON.stringify({
          agent_id: agent.id,
          engine,
          name: form.get("name"),
          description: form.get("description"),
          content: editor.content,
          version: Number(form.get("version")),
        }),
      },
    );
    onSavedConfig?.(saved);
  }
  if (intent === "save") return { saved, content: editor.content };
  if (intent === "deploy" || intent === "validate") {
    if (intent === "validate") {
      await submitTask({
        agent_id: agent.id,
        engine,
        action: "validate",
        config_id: saved.id,
        expected_config_version: saved.version,
      });
      return { saved, content: editor.content };
    }
    const task = await submitTask({
      agent_id: agent.id,
      engine,
      action: "deploy",
      config_id: saved.id,
      expected_config_version: saved.version,
    });
    if (!task?.id) throw new Error("部署任务未创建");
    onDeployTask?.(task.id);
    return { saved, content: editor.content, task };
  }
  const task = await api("/tasks", {
    method: "POST",
    body: JSON.stringify({
      agent_id: agent.id,
      engine,
      action: "import-existing",
      config_id: saved.id,
      expected_config_version: saved.version,
    }),
  });
  if (!task?.id) throw new Error("迁移任务未创建");
  return { saved, content: editor.content, task };
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
