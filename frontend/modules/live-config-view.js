import { coreInstallBlockReason } from "./core-install-state.js";
import { diagnosticError } from "./errors.js";
import { agentPresenceMarkup } from "./agent-presence.js";
import { sharedAllocationText } from "./shared-instance-state.js";
import { renderConfigObjectActions, renderConfigWorkspaceTools } from "./config-workspace-toolbar.js";
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

export function createLiveConfigView({state, can, esc, engineName, conciseVersion, shell, bytes}) {
  return ({agent, engine, runtime, installedEngines, privateAccount, privateWorkspace, sourceMode, importSource,
    current, saved, source, unsupportedReason, existingAvailable, managedAvailable, emptyManaged, readAction, workspace}) => {
    const language = engine === "mihomo" ? "YAML" : "JSON";
    const writable = can("agent-config.write") && !importSource && !unsupportedReason;
    const executable = writable && can("tasks.execute") && agent.status === "online" && managedAvailable;
    const sourceOptions = [
      ...(privateAccount ? [{key:"personal", title:"我的配置", detail:"个人"}] : []),
      ...(!privateAccount || agent.can_manage === true ? [
        ...(managedAvailable || emptyManaged ? [{key:"managed", title:"托管配置", detail:"QAgent"}] : []),
        ...(existingAvailable ? [{key:"import", title:"系统服务", detail:"只读快照"}] : []),
      ] : []),
    ];
    const sourceSwitch = `<nav class="live-config-source-switch" aria-label="配置来源">${sourceOptions.map(option => `<button type="button" data-live-source="${option.key}" class="${sourceMode === option.key ? "active" : ""}" aria-pressed="${sourceMode === option.key}"><b>${option.title}</b><small>${option.detail}</small></button>`).join("")}</nav>`;
    const liveConfigPhase = current ? "ready" : source?.error ? "error" : agent.status !== "online" ? "offline" : unsupportedReason ? "unsupported" : !importSource && !readAction ? "upgrade" : "loading";
    const engineBar = `<nav class="live-engine-bar" aria-label="选择内核">${installedEngines.map(item => {
      const active = item === engine;
      const status = liveEngineStatus(agent.runtime?.[item], agent.status === "online");
      return `<button type="button" class="live-engine-tab${item === engine ? " active" : ""}" data-live-engine="${esc(item)}" aria-pressed="${active}" aria-label="${esc(engineName(item))} · ${esc(status.label)}" title="${esc(status.label)}"${item === engine ? ' aria-current="true"' : ""}><span>${esc(engineName(item))}</span><small class="live-engine-status ${status.tone}">${esc(status.label)}</small></button>`;
    }).join("")}</nav>`;
    const instance = runtime.instance;
    const deployed = workspace?.deployedVersion || instance?.deployed_version;
    const deploymentState = importSource ? "待导入并迁移" : instance?.config_uncertain ? "部署版本待确认" : deployed ? `已部署 v${deployed}${saved?.version !== deployed ? ` · v${saved?.version || 0} 待部署` : ""}` : source?.agentContent && saved?.content === source.agentContent ? `节点内容与 v${saved.version} 一致` : saved?.version ? "已保存 · 部署状态待确认" : "尚未部署";
    const ownerLabel = importSource ? "节点系统服务" : state.session?.username || (privateAccount ? "当前用户" : "管理员");
    const details = `<div class="live-config-details" role="group" aria-label="工作区状态">${agentPresenceMarkup(agent.status, {nodeLabel:true})}<span class="live-config-detail">${esc(conciseVersion(engine, runtime.version))}</span><span class="live-config-detail"><span class="live-config-detail-label">${importSource ? "配置归属" : "所属账号"}</span>${esc(ownerLabel)}</span><span class="live-config-detail" data-workspace-save-state>${importSource ? "系统服务快照" : saved?.version ? `已保存 v${saved.version}` : "尚未保存"}</span><span class="live-config-detail" data-workspace-deploy-state>${esc(deploymentState)}</span></div>`;
    const actions = importSource
      ? can("agent-config.write") && can("tasks.execute") && agent.status === "online" && !unsupportedReason ? '<button class="button primary" type="submit" data-live-intent="import">导入并迁移</button>' : ""
      : writable ? `<button class="button" type="submit" data-live-intent="save">保存草稿</button>${executable ? '<button class="button" type="submit" data-live-intent="validate">保存并校验</button><button class="button primary" type="submit" data-live-intent="deploy">保存并部署</button>' : ""}` : "";
    const editor = current ? `<form class="live-config-editor" id="live-config-form" data-profile-editor data-engine="${esc(engine)}"><nav class="config-file-buttons" aria-label="配置对象"><div class="config-file-list" data-config-objects data-refresh-scroll></div></nav><section class="code-workspace" data-code-editor data-code-language="${language}" data-code-max-bytes="2097152"><header class="code-editor-toolbar"><div class="code-file-meta"><b>公共配置</b></div><div class="code-editor-meta"><span class="code-language">${language}</span><span data-code-status aria-live="polite">${importSource ? "只读快照" : "已保存"}</span><span data-code-bytes></span><span data-code-position>行 1，列 1</span><span data-config-object-count></span></div>${renderConfigObjectActions(can, sourceMode)}${writable ? '<div class="config-editor-actions" role="group" aria-label="当前对象源码操作"><button class="button code-reset" type="button" data-code-reset disabled>恢复当前对象</button><button class="button code-format" type="button" data-code-format>格式化</button></div>' : ""}</header><div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input aria-label="${esc(engineName(engine))} 节点配置源码" spellcheck="false" required${writable ? "" : " readonly"}>${esc(current.content)}</textarea></div><footer><span><span data-code-validation aria-live="polite"></span></span><span class="config-draft-summary" role="status"></span><div>${actions}</div><span data-workspace-result data-live-save-status role="status" aria-live="polite"></span></footer></section><input type="hidden" name="name" value="${esc(current.name)}"><input type="hidden" name="description" value="${esc(current.description || "")}"><input type="hidden" name="version" value="${current.version}"></form>` : `<section class="node-config-source" role="status" aria-live="polite"><h2>${liveConfigPhase === "offline" ? "节点离线" : liveConfigPhase === "unsupported" ? "检测到现有服务，但不可自动迁移" : liveConfigPhase === "upgrade" ? "需要升级 Agent 后读取配置" : liveConfigPhase === "error" ? "读取配置失败" : "正在读取配置"}</h2>${source?.error || unsupportedReason ? `<p class="status-label bad">${esc(diagnosticError(source?.error || unsupportedReason))}</p>` : ""}${liveConfigPhase === "error" ? '<button class="button" type="button" data-read-current>重新读取</button>' : ""}</section>`;
    const hint = emptyManaged ? `${engineName(engine)} 未安装；${coreInstallBlockReason(agent, engine, can, {automatic:true}) || "增加入站时可准备稳定版内核。"}` : agent.can_manage === false ? sharedAllocationText(agent, bytes) : "";
    shell(`<article class="live-config-workspace" data-refresh-key="live-config-content-${esc(agent.id)}-${esc(engine)}-${esc(sourceMode)}-${esc(liveConfigPhase)}" data-live-config-phase="${esc(liveConfigPhase)}" data-motion-region="config-source" data-motion-key="${esc(sourceMode)}" data-motion-ready="${liveConfigPhase !== "loading"}"><header class="editor-toolbar"><div><h2>${esc(agent.name)} · ${esc(engineName(engine))}</h2></div><div class="editor-toolbar-state">${renderConfigWorkspaceTools({can, agent, engine, sourceMode, esc})}</div></header>${details}${engineBar}${sourceSwitch}${hint ? `<p class="config-install-hint">${esc(hint)}</p>` : ""}<div class="config-file-buttons"><span class="config-draft-summary">${esc(saved?.name || current?.name || "新配置")} · ${privateWorkspace ? "个人配置" : importSource ? "系统服务快照" : "节点托管配置"}</span><div class="config-file-actions">${writable ? '<button class="button" type="button" data-config-file-open>导入文件</button><input type="file" accept=".json,.yaml,.yml,application/json,application/yaml,text/yaml,text/plain" data-config-file-input hidden aria-label="选择本地配置文件">' : ""}${can("agent-config.write") && can("tasks.execute") && !importSource && !unsupportedReason ? `<button class="button" type="button" data-inbound-action="add">增加入站</button>${["xray", "sing-box"].includes(engine) ? '<button class="button" type="button" data-outbound-action="create">增加出站</button>' : ""}` : ""}</div></div>${workspace?.conflict ? '<p class="alert error" role="alert">服务端版本已变化，已保留原草稿。请复制需要的修改，再刷新配置并核对新版本。</p>' : ""}${editor}</article>`, "配置", {viewKey:`live-config-${agent.id}-${engine}-${sourceMode}`});
    return document.querySelector(".live-config-workspace");
  };
}
