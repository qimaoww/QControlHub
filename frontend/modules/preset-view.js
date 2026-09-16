import { protocolNavigationNames, snellProtocolOptions, sudokuProtocolOptions, wireguardProtocolOptions } from "./server-plan-form.js";
import { renderEmbeddedPreset } from "./config-inbounds.js";
import { renderPresetIdentity } from "./preset-runtime.js";
import { renderCommonFieldStudio, renderConfigSourceStudio, renderGlobalFieldStudio } from "./config-fields.js";
import { renderSSRustFieldStudio, ssRustPlanBinding } from "./ss-rust-fields.js";

export function createPresetView({ state, can, esc, engineName, shell }) {
  return ({ agent, engine, workspace, config, protocol, plan, selectedProtocolKey, selectedInbound,
    fields, ssRustGroups, selectedField, selectedInboundField, fieldValue, inboundFieldValue,
    commonMutation, engineInstalled, host }) => {
  const inboundNav = (workspace.inbounds || [])
    .map(
      (input) =>
        `<a class="${selectedInbound?.tag === input.tag ? "active" : ""}" href="#agent-config" data-inbound="${esc(input.tag)}"><span><strong>${esc(input.tag)}</strong><small>${esc(input.listen)}:${input.port}</small></span></a>`,
    )
    .join("");
  const protocolNav = workspace.protocols
    .map(
      (item) =>
        `<a class="${item.key === selectedProtocolKey ? "active" : ""}" href="#agent-config" data-protocol="${esc(item.key)}" title="${esc(item.name)}"><b>${esc(item.badge)}</b><span><strong>${esc(protocolNavigationNames[item.key] || item.name)}</strong></span></a>`,
    )
    .join("");
  const methods = (protocol?.methods || [])
    .map(
      (method) =>
        `<option value="${esc(method)}" ${method === plan.method ? "selected" : ""}>${esc(method)}</option>`,
    )
    .join("");
  const transports = (protocol?.transports || ["raw"])
    .map(
      (transport) =>
        `<option value="${esc(transport)}" ${transport === plan.transport ? "selected" : ""}>${transport === "raw" ? "Raw / TCP" : transport === "websocket" ? "WebSocket" : transport === "xhttp" ? "XHTTP" : "gRPC"}</option>`,
    )
    .join("");
  const portForward = Boolean(protocol?.port_forward);
  const protocolOptions =
    engine === "ss-rust"
      ? `<details class="preset-option-panel" open><summary><b>当前端口 · 出站绑定</b><small>仅当前端口生效</small></summary><div class="plan-fields one"><label>出站绑定 IP<input name="ss_rust_outbound_bind_addr" value="${esc(ssRustPlanBinding(plan, config, selectedInbound))}" placeholder="留空继承全局绑定"><small>填写节点本机网卡上的 IPv4 / IPv6 地址。留空删除端口覆盖，不代表禁用全局绑定。DNS、IPv6 优先与性能选项在下方「全局与默认值」单独保存。</small></label></div></details>`
      : selectedProtocolKey === "snell"
      ? snellProtocolOptions(plan, false)
      : selectedProtocolKey === "snell-shadow-tls-v3"
        ? snellProtocolOptions(plan, true)
      : selectedProtocolKey === "sudoku"
        ? sudokuProtocolOptions(plan)
        : protocol?.uses_wireguard
          ? wireguardProtocolOptions(plan, protocol.uses_endpoint)
        : "";
  const vlessEncryptionOptions = protocol?.uses_vless_encryption
    ? `<div class="plan-fields one"><label class="secret-input">服务端 VLESS Decryption<span class="secret-value-control"><input type="password" name="vless_decryption" required value="${esc(plan.vless_decryption || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span><small>由 xray vlessenc 兼容算法生成的 X25519 私有值；只写入服务端，客户端配置不得包含。</small></label><label>客户端 VLESS Encryption<input name="vless_encryption" required value="${esc(plan.vless_encryption || "")}"><small>由服务端 Decryption 自动推导的公开值；分享链接的 encryption 参数使用此值。</small></label></div>`
    : '<input type="hidden" name="vless_decryption" value=""><input type="hidden" name="vless_encryption" value="">';
  const identitySection = `<section class="builder-section" id="target"><header><span class="section-number">02</span><strong>转发目标</strong></header><div><div class="plan-fields three"><label>目标地址<input name="target_address" maxlength="253" required value="${esc(plan.target_address)}" placeholder="127.0.0.1 或 target.example.com"></label><label>目标端口<input type="number" name="target_port" min="1" max="65535" required value="${Number(plan.target_port)}"></label><label>转发协议<select name="network"><option value="tcp" ${plan.network === "tcp" ? "selected" : ""}>TCP</option><option value="udp" ${plan.network === "udp" ? "selected" : ""}>UDP</option><option value="tcp,udp" ${plan.network === "tcp,udp" ? "selected" : ""}>TCP + UDP</option></select></label></div><p class="validation-note">流量由当前内核直连转发到目标地址；部署前请确认监听端口与防火墙已放行。</p><input type="hidden" name="username" value=""><input type="hidden" name="credential" value=""><input type="hidden" name="secondary_credential" value=""><input type="hidden" name="method" value=""></div></section>`;
  const xrayRealityAdvanced = engine === "xray"
    ? `<div class="plan-fields one"><label>最低客户端 Xray 版本<input name="reality_min_client_ver" required pattern="[0-9]{1,3}(\\.[0-9]{1,3}){2}" value="${esc(plan.reality_min_client_ver || "0.0.0")}"><small>保持原预设默认值 0.0.0；可在此自定义客户端最低版本。提高版本会拒绝更旧的客户端。</small></label></div><div class="plan-fields one"><label class="secret-input">ML-DSA-65 Seed（可选，仅服务端）<span class="secret-value-control"><input type="password" name="reality_mldsa65_seed" value="${esc(plan.reality_mldsa65_seed || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span><small>使用 xray mldsa65 生成；系统会推导客户端 Verify。启用时强制 target 证书链严格大于 3500 bytes，并要求 X25519MLKEM768。</small></label></div>`
    : '<input type="hidden" name="reality_min_client_ver" value="0.0.0"><input type="hidden" name="reality_mldsa65_seed" value="">';
  const security = protocol?.uses_reality
    ? `<input type="hidden" name="reality_enabled" value="1"><section class="builder-section security-section" id="security"><header><span class="section-number">04</span><strong>Reality</strong></header><div><div class="plan-fields two"><label>目标域名 / ServerName<input name="reality_server_name" list="reality-presets" required value="${esc(plan.reality_server_name)}"><datalist id="reality-presets">${workspace.reality_presets.map((value) => `<option value="${esc(value)}">`).join("")}</datalist><small>校验公网 DNS；拒绝 Cloudflare 与非公网地址。</small></label><label>Short ID<input name="reality_short_id" required value="${esc(plan.reality_short_id)}"></label></div><div class="plan-fields one"><label>客户端 Public Key<input name="reality_public_key" required value="${esc(plan.reality_public_key)}"></label><label class="secret-input">服务端 Private Key<span class="secret-value-control"><input type="password" name="reality_private_key" required value="${esc(plan.reality_private_key)}"><button type="button" data-secret-visibility>显示</button></span></label></div>${xrayRealityAdvanced}</div></section>`
    : protocol?.supports_tls
      ? `<input type="hidden" name="reality_enabled" value="0"><section class="builder-section security-section" id="security"><header><span class="section-number">04</span><strong>TLS</strong></header><div><label class="tls-switch"><input type="checkbox" name="tls_enabled" value="1" ${plan.tls_enabled || protocol.requires_tls ? "checked" : ""} ${protocol.requires_tls ? "disabled" : ""}><strong>${protocol.requires_tls ? "TLS" : "启用 TLS"}</strong></label><div class="plan-fields two"><label>证书路径<input name="certificate_path" value="${esc(plan.certificate_path)}"></label><label>私钥路径<input name="private_key_path" value="${esc(plan.private_key_path)}"></label></div><p class="validation-note">私钥仅目标内核服务组可读。</p></div></section>`
      : '<input type="hidden" name="reality_enabled" value="0"><input type="hidden" name="tls_enabled" value="0">';
  const sourceStudio = renderConfigSourceStudio({ config, catalog: workspace.catalog, engineInstalled,
    open: state.data.settings?.default_config_editor === "source", ssRust: engine === "ss-rust" });
  const revisionTimeline = config
    ? `<details class="revision-timeline node-revision-timeline" id="revisions"><summary><b>版本历史</b><strong>当前 v${config.version}</strong></summary><div class="timeline-body" data-revision-body>展开加载版本历史</div></details>`
    : "";
  const canAutoInstall = !engineInstalled && (state.session?.role === "admin" || agent.can_manage === true) &&
    (agent.features || []).includes("preset-auto-install-v1");
  const executionCallout = !engineInstalled
    ? `<aside class="config-execution-callout"><span><b>${esc(engineName(engine))} 尚未安装</b><small>${commonMutation ? "通用配置提交需要已安装内核。增加入站可自动安装稳定版；管理内核版本请到节点设置。" : canAutoInstall ? "增加入站并提交时，将自动安装最新稳定版，再执行校验或部署。切换版本请到节点设置。" : "自动安装需要节点管理权及新版 Agent；请到节点设置检查权限或升级 Agent。"}</small></span><a class="button small" href="#node-${esc(agent.id)}">节点设置</a></aside>`
    : agent.runtime?.[engine]?.existing_config_unsupported_reason
      ? `<aside class="config-execution-callout"><span><b>当前内核暂不可提交任务</b><small>${esc(agent.runtime[engine].existing_config_unsupported_reason)}</small></span><a class="button small" href="#node-settings">检查节点配置</a></aside>`
      : agent.status !== "online"
        ? '<aside class="config-execution-callout"><span><b>节点当前离线</b><small>可以继续编辑草稿，节点上线并重新加载后可校验或部署。</small></span></aside>'
        : !can("agent-config.write") || !can("tasks.execute")
          ? '<aside class="config-execution-callout"><span><b>当前账号不可提交预设</b><small>保存并执行需要配置写入和任务执行权限。</small></span></aside>' : "";
  const advancedStudio = commonMutation
    ? renderCommonFieldStudio({ engine, fields, selected:selectedField, value:fieldValue, config,
        catalog:workspace.catalog, mutation:commonMutation })
    : engine === "ss-rust"
    ? renderSSRustFieldStudio({ scope: "inbound", fields: ssRustGroups.inbound, selected: selectedInboundField,
        value: inboundFieldValue, config, inbound: selectedInbound }) +
      renderSSRustFieldStudio({ scope: "global", fields, selected: selectedField, value: fieldValue,
        config, presentFields: workspace.present_fields }) + sourceStudio
    : renderGlobalFieldStudio({ fields, selected: selectedField, value: fieldValue, config,
        catalog: workspace.catalog, presentFields: workspace.present_fields }) + sourceStudio;
  (host ? (markup, _title, options) => renderEmbeddedPreset(host, markup, options) : shell)(
    renderPresetIdentity(`<section class="config-command-bar loaded"><header class="config-command-head"><div class="config-command-title"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><div><p class="eyebrow">Server recipe</p><h2>${esc(protocol?.name || "Protocol")} · ${selectedInbound ? esc(selectedInbound.tag) : "新入站"}</h2><small>${esc(agent.name)} · ${esc(workspace.catalog.name)}</small></div></div><div class="config-command-state"><button class="button small" type="button" data-refresh-preset>刷新状态</button><span class="status-label ${!engineInstalled ? "muted" : config ? "ok" : "warn"}">${!engineInstalled ? "内核未安装" : config ? "已读取" : "新方案"}</span><span class="recipe-version"><b>${config ? `v${config.version}` : "草稿"}</b><small>${esc(workspace.catalog.format)}</small></span><a href="${esc(protocol?.docs)}" target="_blank" rel="noopener noreferrer">文档 ↗</a></div></header><details class="config-hierarchy-menu" open><summary><b>切换入站 / 协议</b><i>＋</i></summary><div class="config-command-selectors${workspace.protocols.length > 5 ? " protocol-catalog-wide" : ""}">${inboundNav ? `<section class="inbound-browser config-selector"><header><span><b>入站</b><small>${workspace.inbounds.length} 个</small></span><button class="button small" type="button" data-new-inbound>＋ 新增</button></header><nav>${inboundNav}</nav></section>` : ""}<section class="protocol-browser config-selector"><header><span><b>协议</b><small>${workspace.protocols.length} 种</small></span></header><nav>${protocolNav}</nav></section></div></details></section>${executionCallout}<article class="recipe-workspace"><form class="server-form" id="server-plan-form"><div class="config-mutation"><label>操作<select name="operation">${selectedInbound ? `<option value="modify">修改 · ${esc(selectedInbound.tag)}</option><option value="add">新增入站</option><option value="delete">删除 · ${esc(selectedInbound.tag)}</option>` : '<option value="add">新增入站</option>'}</select></label></div><div class="builder-layout" data-builder-workbench><nav class="builder-index"><a href="#listen" data-builder-step="listen"><b>01</b><strong>监听</strong></a><a href="#identity" data-builder-step="identity"><b>02</b><strong>认证</strong></a>${protocol?.transport_config ? '<a href="#transport" data-builder-step="transport"><b>03</b><strong>传输</strong></a>' : ""}${protocol?.uses_reality || protocol?.supports_tls ? '<a href="#security" data-builder-step="security"><b>04</b><strong>安全</strong></a>' : ""}</nav><div class="builder-sections"><section class="builder-section" id="listen"><header><span class="section-number">01</span><strong>监听</strong></header><div class="plan-fields three"><label>入站标签<input name="tag" maxlength="64" required value="${esc(plan.tag)}"></label><label>监听地址<input name="listen" required value="${esc(plan.listen)}"></label><label>监听端口<input type="number" name="port" min="1" max="65535" required value="${Number(plan.port)}"></label></div></section><section class="builder-section" id="identity"><header><span class="section-number">02</span><strong>认证</strong></header><div><div class="plan-fields two">${protocol?.ignores_username ? '<input type="hidden" name="username" value="default">' : `<label>用户名或备注<input name="username" maxlength="64" required value="${esc(plan.username)}"></label>`}${protocol?.uses_wireguard ? '<input type="hidden" name="credential" value="">' : `<label class="secret-input">${esc(protocol?.credential_label || "凭据")}<span class="secret-value-control"><input type="password" name="credential" required value="${esc(plan.credential)}"><button type="button" data-secret-visibility>显示</button></span></label>`}${protocol?.secondary_credential_label ? `<label class="secret-input">${esc(protocol.secondary_credential_label)}<span class="secret-value-control"><input type="password" name="secondary_credential" required value="${esc(plan.secondary_credential)}"><button type="button" data-secret-visibility>显示</button></span></label>` : '<input type="hidden" name="secondary_credential" value="">'}</div>${methods ? `<div class="plan-fields one"><label>加密方式<select name="method">${methods}</select></label></div>` : '<input type="hidden" name="method" value="">'}</div></section>${protocol?.transport_config ? `<section class="builder-section" id="transport"><header><span class="section-number">03</span><strong>传输</strong></header><div class="plan-fields two"><label>传输<select name="transport">${transports}</select></label><label>路径 / ServiceName<input name="transport_path" value="${esc(plan.transport_path)}"></label></div></section>` : '<input type="hidden" name="transport" value="raw"><input type="hidden" name="transport_path" value="">'}${security}</div></div><footer class="builder-actions compact"><span class="builder-regenerate-status" data-regenerate-status role="status" aria-live="polite"></span><div><button class="button" type="button" data-regenerate>重新生成参数</button><button class="button" type="submit" data-plan-intent="validate" ${agent.status !== "online" || !engineInstalled ? "disabled" : ""}>保存并校验</button><button class="button primary" type="submit" data-plan-intent="deploy" ${agent.status !== "online" || !engineInstalled ? "disabled" : ""}>保存并部署</button></div></footer></form></article>${revisionTimeline}${advancedStudio}`, protocolOptions + vlessEncryptionOptions, portForward ? identitySection : ""),
    "节点配置",
    {
      viewKey: `agent-config-${agent.id}-${engine}-${selectedProtocolKey}-${selectedInbound?.tag || "new"}-${config?.version || 0}-${state.data.presetDraftReset || 0}`,
      commonFields: commonMutation ? fields : [],
      selectedField,
    },
  );

    return { canAutoInstall };
  };
}
