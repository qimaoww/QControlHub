import { renderProtocolForm } from "./protocol-form.js";
import { renderEmbeddedPresetView } from "./preset-embedded-view.js";
import { coreInstallBlockReason } from "./core-install-state.js";
import { protocolNavigationNames } from "./server-plan-form.js";
import { renderCommonFieldStudio, renderConfigSourceStudio, renderGlobalFieldStudio } from "./config-fields.js";
import { renderSSRustFieldStudio } from "./ss-rust-fields.js";

export function createPresetView({ state, can, esc, engineName, shell }) {
  return ({ agent, engine, workspace, config, protocol, plan, selectedProtocolKey, selectedInbound,
    fields, ssRustGroups, selectedField, selectedInboundField, fieldValue, inboundFieldValue,
    commonMutation, engineInstalled, host }) => {
  const inboundNav = !host ? (workspace.inbounds || [])
    .map(
      (input) =>
        `<a class="${selectedInbound?.tag === input.tag ? "active" : ""}" href="#agent-config" data-inbound="${esc(input.tag)}"><span><strong>${esc(input.tag)}</strong><small>${esc(input.listen)}:${input.port}</small></span></a>`,
    )
    .join("") : "";
  const protocolNav = !host ? workspace.protocols
    .map(
      (item) =>
        `<a class="${item.key === selectedProtocolKey ? "active" : ""}" href="#agent-config" data-protocol="${esc(item.key)}" data-protocol-group="${esc(item.group || "")}" title="${esc(item.name)}"><b>${esc(item.badge)}</b><span><strong>${esc(protocolNavigationNames[item.key] || item.name)}</strong></span></a>`,
    )
    .join("") : "";
  const sourceStudio = !host ? renderConfigSourceStudio({ config, catalog: workspace.catalog, engineInstalled,
    open: state.data.settings?.default_config_editor === "source", ssRust: engine === "ss-rust" }) : "";
  const revisionTimeline = config && (!host || host.kind === "history")
    ? `<details class="revision-timeline node-revision-timeline" id="revisions"><summary><b>版本历史</b><strong>当前 v${config.version}</strong></summary><div class="timeline-body" data-revision-body>展开加载版本历史</div></details>`
    : "";
  const installReason = coreInstallBlockReason(agent, engine, can, { automatic: true });
  const canAutoInstall = !engineInstalled && !installReason;
  const executionCallout = !engineInstalled
    ? `<aside class="config-execution-callout"><span><b>${esc(engineName(engine))} 尚未安装</b><small>${commonMutation ? "通用配置提交需要已安装内核。增加入站可自动安装稳定版；管理内核版本请到节点设置。" : canAutoInstall ? agent.can_manage === false ? "增加入站时将受控准备稳定版内核，再校验或部署您的独立实例；宿主机版本由所有者管理。" : "增加入站并提交时，将自动安装最新稳定版，再执行校验或部署。切换版本请到节点设置。" : esc(installReason)}</small></span><a class="button small" href="#node-${esc(agent.id)}">节点设置</a></aside>`
    : agent.runtime?.[engine]?.existing_config_unsupported_reason
      ? `<aside class="config-execution-callout"><span><b>当前内核暂不可提交任务</b><small>${esc(agent.runtime[engine].existing_config_unsupported_reason)}</small></span><a class="button small" href="#node-settings">检查节点配置</a></aside>`
      : agent.status !== "online"
        ? '<aside class="config-execution-callout"><span><b>节点当前离线</b><small>可以继续编辑草稿，节点上线并重新加载后可校验或部署。</small></span></aside>'
        : !can("agent-config.write") || !can("tasks.execute")
          ? '<aside class="config-execution-callout"><span><b>当前账号不可提交预设</b><small>保存并执行需要配置写入和任务执行权限。</small></span></aside>' : "";
  const usesAdvancedStudio = !host || !["add", "modify", "history"].includes(host.kind);
  const advancedStudio = !usesAdvancedStudio ? "" : commonMutation
    ? renderCommonFieldStudio({ engine, fields, selected:selectedField, value:fieldValue, config,
        catalog:workspace.catalog, mutation:commonMutation })
    : engine === "ss-rust"
    ? renderSSRustFieldStudio({ scope: "inbound", fields: ssRustGroups.inbound, selected: selectedInboundField,
        value: inboundFieldValue, config, inbound: selectedInbound }) +
      renderSSRustFieldStudio({ scope: "global", fields, selected: selectedField, value: fieldValue,
        config, presentFields: workspace.present_fields }) + sourceStudio
    : renderGlobalFieldStudio({ fields, selected: selectedField, value: fieldValue, config,
        catalog: workspace.catalog, presentFields: workspace.present_fields }) + sourceStudio;
  const protocolForm = !host || ["add", "modify"].includes(host.kind)
    ? renderProtocolForm({agent, engine, workspace, config, protocol, plan, selectedProtocolKey, selectedInbound, engineInstalled, esc,
      embedded:Boolean(host), operation:host?.kind}) : "";
  const viewKey = `agent-config-${agent.id}-${engine}-${selectedProtocolKey}-${selectedInbound?.tag || "new"}-${config?.version || 0}-${state.data.presetDraftReset || 0}`;
  if (host) {
    renderEmbeddedPresetView(host, {viewKey, protocols:workspace.protocols, selectedProtocolKey, commonFields:commonMutation ? fields : [],
      selectedField, executionCallout, protocolForm, advancedStudio, revisionTimeline, esc});
  } else {
    shell(`<section class="config-command-bar loaded"><header class="config-command-head"><div class="config-command-title"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><div><h2>${esc(protocol?.name || "Protocol")} · ${selectedInbound ? esc(selectedInbound.tag) : "新增入站"}</h2><small>${esc(agent.name)} · ${esc(workspace.catalog.name)}</small></div></div><div class="config-command-state"><button class="button small" type="button" data-refresh-preset>刷新状态</button><span class="status-label ${!engineInstalled ? "muted" : config ? "ok" : "warn"}">${!engineInstalled ? "内核未安装" : config ? "已读取" : "新方案"}</span><span class="recipe-version"><b>${config ? `v${config.version}` : "草稿"}</b><small>${esc(workspace.catalog.format)}</small></span><a href="${esc(protocol?.docs)}" target="_blank" rel="noopener noreferrer">文档 ↗</a></div></header><details class="config-hierarchy-menu" open><summary><b>切换入站 / 协议</b><i>＋</i></summary><div class="config-command-selectors${workspace.protocols.length > 5 ? " protocol-catalog-wide" : ""}">${inboundNav ? `<section class="inbound-browser config-selector"><header><span><b>入站</b><small>${workspace.inbounds.length} 个</small></span><button class="button small" type="button" data-new-inbound>＋ 新增</button></header><nav>${inboundNav}</nav></section>` : ""}<section class="protocol-browser config-selector"><header><span><b>协议</b><small>${workspace.protocols.length} 种</small></span></header><nav>${protocolNav}</nav></section></div></details></section>${executionCallout}<article class="recipe-workspace">${protocolForm}</article>${revisionTimeline}${advancedStudio}`, "节点配置", {viewKey});
  }

    return { canAutoInstall };
  };
}
