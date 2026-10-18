import { coreInstallBlockReason } from "./core-install-state.js";
import { updateFeedback } from "./presence-motion.js";
import { liveConfigEditorState } from "./live-config-state.js";
import { diagnosticError } from "./errors.js";
import { agentPresenceMarkup } from "./agent-presence.js";
import { sharedAllocationText, sharedConfigPresentation } from "./shared-instance-state.js";

function liveEngineStatus(info = {}, online = true) {
  if (typeof info.installed !== "boolean") return { label: "状态未知", tone: "unknown" };
  if (!info.installed)
    return { label: info.existing_config_available ? "待导入" : "未安装", tone: "uninstalled" };
  if (!online)
    return { label: "状态未知", tone: "unknown" };
  switch (info.service_status) {
    case "active":
    case "running":
      return { label: "正在运行", tone: "running" };
    case "inactive":
    case "stopped":
      return { label: "已停止", tone: "stopped" };
    case "activating":
      return { label: "启动中", tone: "pending" };
    case "deactivating":
      return { label: "停止中", tone: "pending" };
    case "failed":
      return { label: "运行失败", tone: "failed" };
    default:
      return { label: "状态未知", tone: "unknown" };
  }
}

export function createLiveConfigView({ can, esc, engineName, conciseVersion, shell, bytes }) {
  return ({ agent, engine, runtime, installedEngines, privateAccount, privateWorkspace,
    sourceMode, importSource, current, saved, source, unsupportedReason, existingAvailable,
    managedAvailable, emptyManaged, readAction, editorRevision = 0 }) => {
  const language = engine === "mihomo" ? "YAML" : "JSON";

  const editorState = liveConfigEditorState({
    existingAvailable: importSource,
    canOperate: can("agent-config.write"),
    sourceContent: source?.content,
    formContent: current?.content,
  });
  const canExecute = can("tasks.execute") && agent.status === "online" && !unsupportedReason && (importSource || managedAvailable);
  const liveActions = !can("agent-config.write")
    ? ""
    : (!importSource ? '<button class="button" type="submit" data-live-intent="save">保存草稿</button>' : "") +
      (!canExecute ? "" : importSource
        ? '<button class="button primary" type="submit" data-live-intent="import">导入并迁移</button>'
        : '<button class="button" type="submit" data-live-intent="validate">保存并校验</button>' +
        '<button class="button primary" type="submit" data-live-intent="deploy">保存并部署</button>');
  let sourceSwitch = existingAvailable
    ? `<nav class="live-config-source-switch" aria-label="配置来源">${managedAvailable ? `<button class="${sourceMode === "managed" ? "active" : ""}" type="button" aria-pressed="${sourceMode === "managed"}" data-live-source="managed"><b>节点托管配置</b></button>` : ""}<button class="${sourceMode === "import" ? "active" : ""}" type="button" aria-pressed="${importSource}" data-live-source="import"><b>系统服务配置</b></button></nav>`
    : "";
  if (importSource && engine === "ss-rust") {
    sourceSwitch += '<p class="validation-note">导入不迁移入站防火墙；修改端口前请同步防火墙规则。启动失败会恢复原服务。</p>';
  }
  if (privateWorkspace) {
    sourceSwitch = agent.can_manage === false ? `<span class="live-config-detail"><span class="live-config-detail-label">来源</span>个人配置</span><p class="validation-note">${esc(sharedAllocationText(agent, bytes))}</p>` : "";
  }
  if (privateAccount && agent.can_manage === true) {
    sourceSwitch = `<nav class="live-config-source-switch" aria-label="配置来源"><button type="button" aria-pressed="${privateWorkspace}" data-live-source="personal" class="${privateWorkspace ? "active" : ""}"><b>个人配置</b></button>${managedAvailable ? `<button type="button" aria-pressed="${sourceMode === "managed"}" data-live-source="managed" class="${sourceMode === "managed" ? "active" : ""}"><b>节点托管配置</b></button>` : ""}${existingAvailable ? `<button type="button" aria-pressed="${importSource}" data-live-source="import" class="${importSource ? "active" : ""}"><b>系统服务配置</b></button>` : ""}</nav>` +
      (privateWorkspace ? sourceSwitch : "");
  }
  const liveConfigPhase = current
    ? "ready"
    : source?.error
      ? "error"
      : agent.status !== "online"
        ? "offline"
        : unsupportedReason
          ? "unsupported"
          : !importSource && !readAction
            ? "upgrade"
          : "loading";
  const engineBar = `<nav class="live-engine-bar" aria-label="选择内核">${installedEngines.map(item => {
    const info = agent.runtime?.[item] || {};
    const active = item === engine;
    const { label, tone } = sharedConfigPresentation(agent, item) || liveEngineStatus(info, agent.status === "online");
    return `<button type="button" class="live-engine-tab ${active ? "active" : ""}" data-live-engine="${esc(item)}" aria-pressed="${active}" aria-label="${esc(engineName(item))} · ${esc(label)}" title="${esc(label)}" ${active ? 'aria-current="true"' : ""}><span>${esc(engineName(item))}</span><small class="live-engine-status ${tone}">${esc(label)}</small></button>`;
  }).join("")}</nav>`;
  const details = `<div class="live-config-details" role="group" aria-label="当前配置状态">
    ${agentPresenceMarkup(agent.status, { nodeLabel: true })}
    <span class="live-config-detail">${esc(conciseVersion(engine, runtime.version))}</span>
  </div>`;
  shell(
    `<article class="live-config-workspace" data-refresh-key="live-config-content-${esc(agent.id)}-${esc(engine)}-${esc(sourceMode)}-${esc(liveConfigPhase)}-${saved?.version || 0}-${editorRevision}" data-live-config-phase="${esc(liveConfigPhase)}" data-motion-region="config-source" data-motion-key="${esc(sourceMode)}" data-motion-ready="${liveConfigPhase !== "loading"}"><header class="editor-toolbar"><div><h2>${esc(agent.name)}</h2></div><div class="editor-toolbar-state"><b class="live-config-version" data-live-version>${unsupportedReason ? "不可自动迁移" : importSource ? "可导入" : saved?.version ? `已保存 v${saved.version}` : "未保存"}</b></div></header>${details}${engineBar}<div class="live-config-details" role="group" aria-label="当前配置来源">${sourceSwitch || `<span class="live-config-detail"><span class="live-config-detail-label">来源</span>${privateWorkspace ? "个人配置" : importSource ? "系统服务 · 只读快照" : "QAgent 托管配置"}</span>`}</div>${current ? `<form class="live-config-editor" id="live-config-form" data-profile-editor data-new-config="0" data-engine="${esc(engine)}"><section class="code-workspace" data-code-editor data-code-language="${language}" data-code-max-bytes="2097152"><header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h3"/></svg></span><b>${engine === "mihomo" ? "config.yaml" : "config.json"}</b></div><div class="code-editor-meta"><span class="code-language">${language}</span><span data-code-status aria-live="polite">${importSource ? "系统服务只读快照" : privateWorkspace ? "个人配置" : "节点托管配置"}</span><span data-code-bytes>—</span><span data-code-position>行 1，列 1</span></div></header><div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input aria-label="${esc(engineName(engine))} 节点配置源码" spellcheck="false" required ${editorState.readOnly ? "readonly" : ""}>${esc(current.content)}</textarea></div><footer><span><i class="code-status-dot" data-code-status-dot></i><span data-code-validation aria-live="polite"></span></span><div><button class="button code-reset" type="button" data-code-reset disabled>恢复原文</button>${can("agent-config.write") && !editorState.readOnly ? '<button class="button code-format" type="button" data-code-format>格式化配置</button>' : ""}${liveActions}</div></footer></section><input type="hidden" name="name" value="${esc(current.name)}"><input type="hidden" name="description" value="${esc(current.description)}"><input type="hidden" name="version" value="${current.version}"></form>` : agent.status !== "online" ? '<section class="node-config-source"><h2>节点离线</h2><span class="status-label warn">无法读取</span></section>' : unsupportedReason ? `<section class="node-config-source" role="status"><h2>检测到现有服务，但不可自动迁移</h2><span class="status-label bad">${esc(unsupportedReason)}</span><p>QAgent 未执行或接管该服务。所有相关内核任务均已禁用；请按提示调整为受支持的精确布局并重启 Agent 重新发现。</p></section>` : !importSource && !readAction ? '<section class="node-config-source"><h2>需要升级 Agent</h2><span class="status-label warn">暂不可读取 QAgent 配置</span><p>升级后即可在不影响系统服务可选导入的情况下独立读取 QAgent 托管配置。</p></section>' : source?.error ? `<section class="node-config-source"><h2>读取配置失败</h2><span class="status-label bad">${esc(diagnosticError(source.error))}</span><button class="button" type="button" data-read-current>重新读取</button></section>` : `<section class="node-config-source" role="status" aria-live="polite"><h2>正在读取${importSource ? "系统服务配置" : "QAgent 配置"}</h2><span class="status-label warn">读取中</span><form data-auto-read-current hidden></form></section>`}</article>`,
    "配置",
    { viewKey: `live-config-${agent.id}-${engine}` },
  );
  const workspaceElement = document.querySelector(".live-config-workspace");
  if (emptyManaged) {
    const hint = document.createElement("p");
    hint.className = "config-install-hint";
    const reason = coreInstallBlockReason(agent, engine, can, { automatic: true });
    updateFeedback(hint, `${engineName(engine)} 未安装；${reason || (agent.can_manage === false ? "增加入站时将受控准备稳定版内核并初始化您的独立实例。" : "提交“增加入站”时自动安装最新稳定版，切换版本请到节点设置。")}`);
    workspaceElement.querySelector(".live-config-details").after(hint);
  }

    return workspaceElement;
  };
}
