import { liveConfigEditorState } from "./live-config-state.js";
import { diagnosticError } from "./errors.js";

export function createLiveConfigView({ can, esc, engineName, conciseVersion, shell }) {
  return ({ agent, engine, runtime, installedEngines, privateAccount, privateWorkspace,
    sourceMode, importSource, current, saved, source, unsupportedReason, existingAvailable,
    managedAvailable, emptyManaged, readAction }) => {
  const language = engine === "mihomo" ? "YAML" : "JSON";

  const editorState = liveConfigEditorState({
    existingAvailable: importSource,
    canOperate: can("agent-config.write"),
    sourceContent: source?.content,
    formContent: current?.content,
  });
  const canExecute = can("tasks.execute") && !unsupportedReason && (importSource || managedAvailable);
  const liveActions = !can("agent-config.write")
    ? ""
    : (privateWorkspace ? '<button class="button" type="submit" data-live-intent="save">保存个人配置</button>' : "") +
      (!canExecute ? "" : importSource
        ? '<button class="button primary" type="submit" data-live-intent="import">手动导入并迁移</button>'
        : '<button class="button" type="submit" data-live-intent="validate">保存并校验</button>' +
        '<button class="button primary" type="submit" data-live-intent="deploy">保存并部署</button>');
  let sourceSwitch = existingAvailable
    ? `<nav class="live-config-source-switch" aria-label="配置来源">${managedAvailable ? `<button class="${sourceMode === "managed" ? "active" : ""}" type="button" data-live-source="managed"><b>QAgent 配置</b><small>/etc/qagent 托管</small></button>` : ""}<button class="${sourceMode === "import" ? "active" : ""}" type="button" data-live-source="import"><b>系统服务配置</b><small>可选导入</small></button></nav>`
    : "";
  if (importSource && engine === "ss-rust") {
    sourceSwitch += '<p class="validation-note">导入保留多端口、DNS、出站绑定、IPv6 优先及出站 ACL。install-ss-rust 的入站防火墙与重应用服务不迁移，修改端口前须另行处理。日志改为 QAgent info；无离线校验，启动失败会回滚。</p>';
  }
  if (privateWorkspace) {
    sourceSwitch = '<p class="validation-note">可保存多份方案；每台主机的每种内核仅运行一份配置，不能覆盖其他用户正在运行的配置。</p>';
  }
  if (privateAccount && agent.can_manage === true) {
    sourceSwitch = `<nav class="live-config-source-switch" aria-label="配置来源"><button type="button" data-live-source="personal" class="${privateWorkspace ? "active" : ""}"><b>我的配置</b><small>个人工作区</small></button>${managedAvailable ? `<button type="button" data-live-source="managed" class="${sourceMode === "managed" ? "active" : ""}"><b>读取自有主机配置</b><small>当前托管文件</small></button>` : ""}${existingAvailable ? `<button type="button" data-live-source="import" class="${importSource ? "active" : ""}"><b>系统服务配置</b><small>可选导入</small></button>` : ""}</nav>` +
      (privateWorkspace ? sourceSwitch : '<p class="validation-note">只允许读取自有主机且未被其他账号占用的配置；共享给他人不授予读取其配置的权限。</p>');
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
    const label = info.installed ? "已安装" : info.existing_config_available ? "待导入" : "未安装";
    return `<button type="button" class="live-engine-tab ${active ? "active" : ""}" data-live-engine="${esc(item)}" aria-pressed="${active}" aria-label="${esc(engineName(item))} · ${label}" title="${label}" ${active ? 'aria-current="true"' : ""}><span>${esc(engineName(item))}</span><small class="live-engine-status${info.installed ? " installed" : ""}">${label}</small></button>`;
  }).join("")}</nav>`;
  shell(
    `<article class="live-config-workspace" data-refresh-key="live-config-content-${esc(agent.id)}-${esc(engine)}-${esc(sourceMode)}-${esc(liveConfigPhase)}" data-live-config-phase="${esc(liveConfigPhase)}"><header class="editor-toolbar"><div><p class="live-config-eyebrow">${privateWorkspace ? "我的节点配置" : "节点配置工作区"}</p><h2>${esc(agent.name)}</h2>${sourceSwitch}</div><div class="editor-toolbar-state"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><b>${unsupportedReason ? "不可自动迁移" : importSource ? "可导入" : saved?.version ? `v${saved.version}` : "未保存"}</b></div></header>${engineBar}<div class="live-config-details"><span><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i>${agent.status === "online" ? "节点在线" : "节点离线"}</span><span>${esc(agent.os)} / ${esc(agent.arch)}</span><span>${esc(engineName(engine))} · ${esc(conciseVersion(engine, runtime.version))}</span><span>${privateWorkspace ? "个人配置 · 可保存并部署到此主机" : importSource ? "系统服务 · 只读快照" : "QAgent 托管 · 编辑后需保存部署"}</span></div>${current ? `<form class="live-config-editor" id="live-config-form" data-profile-editor data-new-config="0" data-engine="${esc(engine)}"><section class="code-workspace" data-code-editor data-code-language="${language}" data-code-max-bytes="2097152"><header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h3"/></svg></span><b>${engine === "mihomo" ? "config.yaml" : "config.json"}</b></div><div class="code-editor-meta"><span class="code-language">${language}</span><span data-code-status aria-live="polite">${importSource ? "系统服务只读快照" : "QAgent 配置"}</span><span data-code-bytes>—</span><span data-code-position>行 1，列 1</span></div></header><div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input aria-label="${esc(engineName(engine))} 节点配置源码" spellcheck="false" required ${editorState.readOnly ? "readonly" : ""}>${esc(current.content)}</textarea></div><footer><span><i class="code-status-dot" data-code-status-dot></i><span data-code-validation aria-live="polite"></span></span><div><button class="button code-reset" type="button" data-code-reset disabled>恢复原文</button>${can("agent-config.write") && !editorState.readOnly ? '<button class="button code-format" type="button" data-code-format>格式化配置</button>' : ""}${liveActions}</div></footer></section><input type="hidden" name="name" value="${esc(current.name)}"><input type="hidden" name="description" value="${esc(current.description)}"><input type="hidden" name="version" value="${current.version}"></form>` : agent.status !== "online" ? '<section class="node-config-source"><h2>节点离线</h2><span class="status-label warn">无法读取</span></section>' : unsupportedReason ? `<section class="node-config-source" role="status"><h2>检测到现有服务，但不可自动迁移</h2><span class="status-label bad">${esc(unsupportedReason)}</span><p>QAgent 未执行或接管该服务。所有相关内核任务均已禁用；请按提示调整为受支持的精确布局并重启 Agent 重新发现。</p></section>` : !importSource && !readAction ? '<section class="node-config-source"><h2>需要升级 Agent</h2><span class="status-label warn">暂不可读取 QAgent 配置</span><p>升级后即可在不影响系统服务可选导入的情况下独立读取 QAgent 托管配置。</p></section>' : source?.error ? `<section class="node-config-source"><h2>读取配置失败</h2><span class="status-label bad">${esc(diagnosticError(source.error))}</span><button class="button" type="button" data-read-current>重新读取</button></section>` : `<section class="node-config-source" role="status" aria-live="polite"><h2>正在读取${importSource ? "系统服务配置" : "QAgent 配置"}</h2><span class="status-label warn">读取中</span><form data-auto-read-current hidden></form></section>`}</article>`,
    "配置",
    { viewKey: `live-config-${agent.id}-${engine}` },
  );
  const workspaceElement = document.querySelector(".live-config-workspace");
  if (emptyManaged) {
    const hint = document.createElement("p");
    hint.className = "config-install-hint";
    hint.textContent = `${engineName(engine)} 未安装；提交“增加入站”时自动安装最新稳定版，切换版本请到节点设置。`;
    workspaceElement.querySelector(".live-config-details").after(hint);
  } else if (source?.cached) {
    const hint = document.createElement("p");
    hint.className = "config-install-hint";
    hint.textContent = "最近 600 秒内已校验的节点快照；手动刷新及部署前核验会跳过缓存。";
    workspaceElement.querySelector(".live-config-details").after(hint);
  } else if (source?.saved) {
    const hint = document.createElement("p");
    hint.className = "config-install-hint";
    hint.textContent = "已保存配置，部署成功后在节点生效。";
    workspaceElement.querySelector(".live-config-details").after(hint);
  }

    return workspaceElement;
  };
}
