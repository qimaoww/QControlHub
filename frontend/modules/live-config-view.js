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
  const sources = privateWorkspace || privateAccount && agent.can_manage === true
    ? [{value:"personal", label:"个人配置"}] : [];
  if ((!privateAccount || agent.can_manage === true) && (managedAvailable || emptyManaged))
    sources.push({value:"managed", label:"节点托管配置"});
  if (existingAvailable) sources.push({value:"import", label:"系统服务配置"});
  if (!sources.length) sources.push({value:sourceMode, label:sourceMode === "personal" ? "个人配置" : "节点托管配置"});
  const sourceHint = agent.can_manage === false ? sharedAllocationText(agent, bytes) :
    importSource ? engine === "ss-rust" ? "只读快照；导入前请同步入站防火墙规则。" : "只读快照，可导入并迁移。" : "";
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
  const engineOptions = installedEngines.map(item => {
    const {label} = sharedConfigPresentation(agent, item) || liveEngineStatus(agent.runtime?.[item], agent.status === "online");
    return `<option value="${esc(item)}" ${item === engine ? "selected" : ""}>${esc(engineName(item))} · ${esc(label)}</option>`;
  }).join("");
  const {label:runtimeLabel, tone:runtimeTone} = sharedConfigPresentation(agent, engine) || liveEngineStatus(runtime, agent.status === "online");
  const context = `<div class="settings-grid" role="group" aria-label="配置工作区">
    <label class="settings-field"><span>内核</span><select data-live-engine-select aria-label="选择内核">${engineOptions}</select>
      <small data-live-context-feedback><span class="live-engine-status ${runtimeTone}">${esc(runtimeLabel)}</span> · ${esc(runtime.version || "版本未知")}</small></label>
    <label class="settings-field"><span>配置来源</span><select data-live-source-select aria-label="配置来源">${sources.map(item => `<option value="${item.value}" ${item.value === sourceMode ? "selected" : ""}>${item.label}</option>`).join("")}</select>
      ${sourceHint ? `<small>${esc(sourceHint)}</small>` : ""}</label>
  </div>`;
  shell(
    `<article class="live-config-workspace" data-refresh-key="live-config-content-${esc(agent.id)}-${esc(engine)}-${esc(sourceMode)}-${esc(liveConfigPhase)}-${saved?.version || 0}-${editorRevision}" data-live-config-phase="${esc(liveConfigPhase)}" data-motion-region="config-source" data-motion-key="${esc(sourceMode)}" data-motion-ready="${liveConfigPhase !== "loading"}"><header class="editor-toolbar"><div class="config-command-title"><h2>${esc(agent.name)}</h2><div>${agentPresenceMarkup(agent.status, { nodeLabel: true })}</div></div><div class="editor-toolbar-state"><b class="live-config-version" data-live-version>${unsupportedReason ? "不可自动迁移" : importSource ? "可导入" : saved?.version ? `已保存 v${saved.version}` : "未保存"}</b></div></header>${context}${current ? `<form class="live-config-editor" id="live-config-form" data-profile-editor data-new-config="0" data-engine="${esc(engine)}"><section class="code-workspace" data-code-editor data-code-language="${language}" data-code-max-bytes="2097152"><header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h3"/></svg></span><b>${engine === "mihomo" ? "config.yaml" : "config.json"}</b></div><div class="code-editor-meta"><span class="code-language">${language}</span><span data-code-status aria-live="polite">${importSource ? "只读快照" : "已保存"}</span><span data-code-bytes>—</span><span data-code-position>行 1，列 1</span><span data-code-validation aria-live="polite"></span></div></header><footer data-config-submit-bar><div data-config-submit-actions role="group" aria-label="保存与部署"><button class="button code-reset" type="button" data-code-reset disabled>恢复原文</button>${can("agent-config.write") && !editorState.readOnly ? '<button class="button code-format" type="button" data-code-format>格式化配置</button>' : ""}${liveActions}</div></footer><div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input aria-label="${esc(engineName(engine))} 节点配置源码" spellcheck="false" required ${editorState.readOnly ? "readonly" : ""}>${esc(current.content)}</textarea></div></section><input type="hidden" name="name" value="${esc(current.name)}"><input type="hidden" name="description" value="${esc(current.description)}"><input type="hidden" name="version" value="${current.version}"></form>` : agent.status !== "online" ? '<section class="node-config-source"><h2>节点离线</h2><span class="status-label warn">无法读取</span></section>' : unsupportedReason ? `<section class="node-config-source" role="status"><h2>检测到现有服务，但不可自动迁移</h2><span class="status-label bad">${esc(unsupportedReason)}</span><p>QAgent 未执行或接管该服务。所有相关内核任务均已禁用；请按提示调整为受支持的精确布局并重启 Agent 重新发现。</p></section>` : !importSource && !readAction ? '<section class="node-config-source"><h2>需要升级 Agent</h2><span class="status-label warn">暂不可读取 QAgent 配置</span><p>升级后即可在不影响系统服务可选导入的情况下独立读取 QAgent 托管配置。</p></section>' : source?.error ? `<section class="node-config-source"><h2>读取配置失败</h2><span class="status-label bad">${esc(diagnosticError(source.error))}</span><button class="button" type="button" data-read-current>重新读取</button></section>` : `<section class="node-config-source" role="status" aria-live="polite"><h2>正在读取${importSource ? "系统服务配置" : "QAgent 配置"}</h2><span class="status-label warn">读取中</span><form data-auto-read-current hidden></form></section>`}</article>`,
    "配置",
    { viewKey: `live-config-${agent.id}-${engine}` },
  );
  const workspaceElement = document.querySelector(".live-config-workspace");
  if (emptyManaged) {
    const hint = document.createElement("p");
    hint.className = "config-install-hint";
    const reason = coreInstallBlockReason(agent, engine, can, { automatic: true });
    updateFeedback(hint, `${engineName(engine)} 未安装；${reason || (agent.can_manage === false ? "增加入站时将受控准备稳定版内核并初始化您的独立实例。" : "提交“增加入站”时自动安装最新稳定版，切换版本请到节点设置。")}`);
    workspaceElement.querySelector(".settings-grid").after(hint);
  }

    return workspaceElement;
  };
}
