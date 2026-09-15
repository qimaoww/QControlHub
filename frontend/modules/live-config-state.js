// Snapshot compatibility, freshness and fail-closed deployment preflight.

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

function deployPreflightError(message) {
  const error = new Error(message);
  error.deployPreflight = true;
  return error;
}

export function assertAgentConfigBaseline(expected, actual) {
  if (typeof expected !== "string") {
    throw deployPreflightError(
      "部署前没有可核验的 Agent 配置基线，已停止保存和部署。请先重新读取节点配置。",
    );
  }
  if (actual !== expected) {
    throw deployPreflightError(
      "部署前核验发现 Agent 当前配置已在页面读取后发生变化，已停止保存和部署。当前草稿已保留，请重新读取节点配置并合并修改。",
    );
  }
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
  // A managed configuration can be cached for fast page entry, but deployment
  // must never persist a draft or enqueue a task until a fresh Agent read has
  // been compared with the exact baseline that the editor was opened from.
  if (intent === "deploy") await beforeDeploy?.();
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

export { deployPreflightError };

export function liveSourceKey(agentId, engine, source = "managed") {
  return source === "import"
    ? `${agentId}|${engine}|import`
    : `${agentId}|${engine}`;
}
